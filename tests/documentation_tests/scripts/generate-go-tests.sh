#!/usr/bin/env bash
# generate-go-tests.sh — Generate Ginkgo E2E tests from .adoc documentation
# using GoE2E-DocSyncer.
#
# Usage: tests/documentation_tests/scripts/generate-go-tests.sh

set -euo pipefail

# Resolve the sail-operator repo root (parent of tests/)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

GENERATED_DIR="${REPO_ROOT}/tests/e2e/generated-sail"
CONFIG="${REPO_ROOT}/tests/documentation_tests/docsyncer-sail.yaml"

echo "==> Cleaning old generated test files..."
find "${GENERATED_DIR}" -name 'generated_*' -type f -delete

echo "==> Generating tests from documentation..."
cd "${REPO_ROOT}"
GOPROXY=direct go run github.com/fjglira/GoE2E-DocSyncer/cmd/docsyncer@latest generate \
    --config "${CONFIG}" \
    --verbose

echo ""
echo "==> Generated files:"
ls -1 "${GENERATED_DIR}/"
