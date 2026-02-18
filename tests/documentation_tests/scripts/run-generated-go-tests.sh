#!/usr/bin/env bash
# run-generated-go-tests.sh — Run the generated Go tests from documentation against a KIND cluster
#
# This script sets up a KIND cluster and runs the generated Ginkgo tests,
# handling cleanup between test cases similar to run-asciidocs-test.sh
#
# Usage: tests/documentation_tests/scripts/run-generated-go-tests.sh

set -euo pipefail

# Resolve the sail-operator repo root
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Cluster and environment setup (same as run-asciidocs-test.sh)
export KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-docs-automation-go}"
export IP_FAMILY="${IP_FAMILY:-ipv4}"
export ISTIOCTL="${ROOT_DIR}/bin/istioctl"
export IMAGE_BASE="sail-operator"
export TAG="latest"
export LOCAL_REGISTRY="localhost:5000"
export OCP=false
export KIND_REGISTRY_NAME="kind-registry"
export KIND_REGISTRY_PORT="5000"
export KIND_REGISTRY="localhost:${KIND_REGISTRY_PORT}"
export HUB="${KIND_REGISTRY}"
export IMAGE="${HUB}/${IMAGE_BASE}:${TAG}"
export ARTIFACTS="${ARTIFACTS:-$(mktemp -d)}"
export KUBECONFIG="${KUBECONFIG:-"${ARTIFACTS}/config"}"
export HELM_TEMPL_DEF_FLAGS="--include-crds --values chart/values.yaml"

# Generated tests directory
export GENERATED_TESTS_DIR="${ROOT_DIR}/tests/e2e/generated-sail"

# macOS KIND image override
if [[ "$(uname)" == "Darwin" ]]; then
  export KIND_IMAGE="docker.io/kindest/node:v1.33.2"
fi

echo "==> Setting up environment for generated Go tests"
echo "Cluster: $KIND_CLUSTER_NAME"
echo "Generated tests: $GENERATED_TESTS_DIR"
echo "Artifacts: $ARTIFACTS"

# Validate required tools
for tool in kubectl istioctl go; do
  if ! command -v "$tool" &> /dev/null; then
    echo "Error: $tool could not be found. Please install it."
    exit 1
  fi
done

# Validate generated tests exist
if [[ ! -d "$GENERATED_TESTS_DIR" ]]; then
  echo "Error: Generated tests directory not found: $GENERATED_TESTS_DIR"
  echo "Run 'make test.generate-docs-test' first to generate tests"
  exit 1
fi

if ! find "$GENERATED_TESTS_DIR" -name "generated_*.go" -type f | head -1 > /dev/null; then
  echo "Error: No generated test files found in $GENERATED_TESTS_DIR"
  echo "Run 'make test.generate-docs-test' first to generate tests"
  exit 1
fi

# Generate JUnit XML report from go test output
function generate_junit_xml() {
  local junit_file="${ARTIFACTS}/junit-generated-docs.xml"
  local test_output_file="${ARTIFACTS}/go-test-output.txt"

  if [[ ! -f "$test_output_file" ]]; then
    echo "No test output found, skipping JUnit report generation"
    return 0
  fi

  echo "Generating JUnit report from Go test output..."

  # Use gotestsum to convert go test output to JUnit (if available)
  if command -v gotestsum &> /dev/null; then
    gotestsum --junitfile "$junit_file" --raw-command -- cat "$test_output_file"
  else
    echo "Warning: gotestsum not found, creating basic JUnit report"
    # Create a basic JUnit XML structure
    {
      echo '<?xml version="1.0" encoding="UTF-8"?>'
      echo '<testsuite name="generated-docs-tests">'
      echo '  <testcase name="generated-go-tests" classname="docs-tests">'
      if grep -q "FAIL" "$test_output_file"; then
        echo '    <failure message="Test failed">'
        cat "$test_output_file"
        echo '    </failure>'
      fi
      echo '  </testcase>'
      echo '</testsuite>'
    } > "$junit_file"
  fi

  echo "JUnit report written to: $junit_file"
}

# Setup KIND cluster and operator
function setup_cluster() {
  echo "==> Setting up KIND cluster: $KIND_CLUSTER_NAME"

  # Clean up existing cluster
  kind delete cluster --name "$KIND_CLUSTER_NAME" > /dev/null 2>&1 || true

  # Source setup scripts (preserve environment)
  source "${ROOT_DIR}/tests/e2e/setup/setup-kind.sh"

  # Build and push the operator image
  source "${ROOT_DIR}/tests/e2e/setup/build-and-push-operator.sh"
  build_and_push_operator_image

  # Ensure kubeconfig is set
  kind export kubeconfig --name="${KIND_CLUSTER_NAME}"

  # Deploy the sail operator
  echo "==> Deploying Sail Operator"
  kubectl create ns sail-operator || echo "namespace sail-operator already exists"
  # shellcheck disable=SC2086
  helm template chart chart ${HELM_TEMPL_DEF_FLAGS} --set image="${IMAGE}" --namespace sail-operator | kubectl apply --server-side=true -f -
  kubectl wait --for=condition=available --timeout=600s deployment/sail-operator -n sail-operator

  echo "==> Cluster setup complete"
}

# Cleanup function that can be called between tests
function cleanup_test_resources() {
  echo "==> Cleaning up test resources"

  # Delete any test namespaces (common pattern in generated tests)
  for ns in istio-system istio-test test-ns; do
    if kubectl get namespace "$ns" > /dev/null 2>&1; then
      echo "Deleting namespace: $ns"
      kubectl delete namespace "$ns" --ignore-not-found=true
      kubectl wait --for=delete namespace/"$ns" --timeout=300s || true
    fi
  done

  # Clean up cluster-wide custom resources
  echo "Cleaning up custom resources..."
  for crd in $(kubectl get crd -o name | grep sailoperator.io || true); do
    crd_name=$(basename "$crd")
    for cr in $(kubectl get "$crd_name" -A -o name 2>/dev/null || true); do
      echo "Deleting $cr"
      kubectl delete "$cr" --ignore-not-found=true || true
    done
  done

  # Wait a bit for cleanup to complete
  sleep 5
  echo "==> Cleanup complete"
}

# Run the generated Go tests
function run_generated_tests() {
  echo "==> Running generated Go tests"

  local test_output_file="${ARTIFACTS}/go-test-output.txt"
  local test_status=0

  # Change to the generated tests directory
  cd "$GENERATED_TESTS_DIR"

  # Run the tests with detailed output
  echo "Running: go test -tags e2e -v -count=1 -ginkgo.v ."

  # Run tests and capture output
  if go test -tags e2e -v -count=1 -ginkgo.v . 2>&1 | tee "$test_output_file"; then
    echo "==> All tests passed!"
  else
    test_status=$?
    echo "==> Tests failed with exit code: $test_status"
  fi

  # Return to original directory
  cd "$ROOT_DIR"

  return $test_status
}

# Cleanup function for script exit
function cleanup_on_exit() {
  echo "==> Script exiting, cleaning up..."

  # Generate JUnit report
  generate_junit_xml

  # Delete the KIND cluster
  if [[ "${KEEP_CLUSTER:-}" != "true" ]]; then
    echo "Deleting KIND cluster: $KIND_CLUSTER_NAME"
    kind delete cluster --name "$KIND_CLUSTER_NAME" || true
  else
    echo "Keeping cluster (KEEP_CLUSTER=true): $KIND_CLUSTER_NAME"
  fi
}

# Set up cleanup trap
trap cleanup_on_exit EXIT

# Main execution
main() {
  echo "==> Starting generated Go tests execution"

  # Ensure we have generated tests
  echo "==> Checking for generated tests..."
  local test_count
  test_count=$(find "$GENERATED_TESTS_DIR" -name "generated_*.go" -type f | wc -l)
  echo "Found $test_count generated test file(s)"

  # Setup cluster and operator
  setup_cluster

  # Record initial cluster state for potential cleanup
  kubectl get namespaces -o name > "${ARTIFACTS}/initial-namespaces.txt"

  # Run the tests
  if run_generated_tests; then
    echo "==> Generated Go tests completed successfully!"
    exit 0
  else
    echo "==> Generated Go tests failed!"
    exit 1
  fi
}

# Execute main function
main "$@"