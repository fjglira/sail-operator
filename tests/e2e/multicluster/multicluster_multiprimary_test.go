//go:build e2e

// Copyright Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package multicluster

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/istio-ecosystem/sail-operator/api/v1alpha1"
	"github.com/istio-ecosystem/sail-operator/pkg/kube"
	"github.com/istio-ecosystem/sail-operator/pkg/test/project"
	. "github.com/istio-ecosystem/sail-operator/pkg/test/util/ginkgo"
	"github.com/istio-ecosystem/sail-operator/pkg/test/util/supportedversion"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/certs"
	common "github.com/istio-ecosystem/sail-operator/tests/e2e/util/common"
	. "github.com/istio-ecosystem/sail-operator/tests/e2e/util/gomega"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/helm"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/istioctl"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/kubectl"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Multicluster deployment models", Ordered, func() {
	SetDefaultEventuallyTimeout(180 * time.Second)
	SetDefaultEventuallyPollingInterval(time.Second)

	BeforeAll(func(ctx SpecContext) {
		if !skipDeploy {
			// Deploy the Sail Operator on both clusters
			Expect(kubectl.CreateNamespace(namespace, kubeconfig)).To(Succeed(), "Namespace failed to be created on Cluster #1")
			Expect(kubectl.CreateNamespace(namespace, kubeconfig2)).To(Succeed(), "Namespace failed to be created on  Cluster #2")

			Expect(helm.Install("sail-operator", filepath.Join(project.RootDir, "chart"), "--namespace "+namespace, "--set=image="+image, "--kubeconfig "+kubeconfig)).
				To(Succeed(), "Operator failed to be deployed in Cluster #1")

			Eventually(common.GetObject).
				WithArguments(ctx, clPrimary, kube.Key(deploymentName, namespace), &appsv1.Deployment{}).
				Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Error getting Istio CRD")
			Success("Operator is deployed in the Cluster #1 namespace and Running")

			Expect(helm.Install("sail-operator", filepath.Join(project.RootDir, "chart"), "--namespace "+namespace, "--set=image="+image, "--kubeconfig "+kubeconfig2)).
				To(Succeed(), "Operator failed to be deployed in  Cluster #2")

			Eventually(common.GetObject).
				WithArguments(ctx, clRemote, kube.Key(deploymentName, namespace), &appsv1.Deployment{}).
				Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Error getting Istio CRD")
			Success("Operator is deployed in the Cluster #2 namespace and Running")
		}
	})

	Describe("Multi-Primary Multi-Network configuration", func() {
		// Test the Multi-Primary Multi-Network configuration for each supported Istio version
		for _, version := range supportedversion.List {
			Context("Istio version is: "+version.Version, func() {
				When("Istio resources are created in both clusters with multicluster configuration", func() {
					BeforeAll(func(ctx SpecContext) {
						Expect(kubectl.CreateNamespace(controlPlaneNamespace, kubeconfig)).To(Succeed(), "Namespace failed to be created")
						Expect(kubectl.CreateNamespace(controlPlaneNamespace, kubeconfig2)).To(Succeed(), "Namespace failed to be created")

						// Push the intermediate CA to both clusters
						certs.PushIntermediateCA(controlPlaneNamespace, kubeconfig, "east", "network1", artifacts, clPrimary)
						certs.PushIntermediateCA(controlPlaneNamespace, kubeconfig2, "west", "network2", artifacts, clRemote)

						// Wait for the secret to be created in both clusters
						Eventually(func() error {
							_, err := common.GetObject(context.Background(), clPrimary, kube.Key("cacerts", controlPlaneNamespace), &corev1.Secret{})
							return err
						}).ShouldNot(HaveOccurred(), "Secret is not created on Cluster #1")

						Eventually(func() error {
							_, err := common.GetObject(context.Background(), clRemote, kube.Key("cacerts", controlPlaneNamespace), &corev1.Secret{})
							return err
						}).ShouldNot(HaveOccurred(), "Secret is not created on Cluster #1")

						multiclusterYAML := `
apiVersion: sailoperator.io/v1alpha1
kind: Istio
metadata:
  name: default
spec:
  version: %s
  namespace: %s
  values:
    global:
      meshID: %s
      multiCluster:
        clusterName: %s
      network: %s`
						multiclusterCluster1YAML := fmt.Sprintf(multiclusterYAML, version.Name, controlPlaneNamespace, "mesh1", "cluster1", "network1")
						Log("Istio CR Cluster #1: ", multiclusterCluster1YAML)
						Expect(kubectl.CreateFromString(multiclusterCluster1YAML, kubeconfig)).To(Succeed(), "Istio Resource creation failed on Cluster #1")

						multiclusterCluster2YAML := fmt.Sprintf(multiclusterYAML, version.Name, controlPlaneNamespace, "mesh1", "cluster2", "network2")
						Log("Istio CR Cluster #2: ", multiclusterCluster2YAML)
						Expect(kubectl.CreateFromString(multiclusterCluster2YAML, kubeconfig2)).To(Succeed(), "Istio Resource creation failed on  Cluster #2")
					})

					It("updates both Istio CR status to Ready", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clPrimary, kube.Key(istioName), &v1alpha1.Istio{}).
							Should(HaveCondition(v1alpha1.IstioConditionReady, metav1.ConditionTrue), "Istio is not Ready on Cluster #1; unexpected Condition")
						Success("Istio CR is Ready on Cluster #1")

						Eventually(common.GetObject).
							WithArguments(ctx, clRemote, kube.Key(istioName), &v1alpha1.Istio{}).
							Should(HaveCondition(v1alpha1.IstioConditionReady, metav1.ConditionTrue), "Istio is not Ready on Cluster #2; unexpected Condition")
						Success("Istio CR is Ready on Cluster #1")
					})

					It("deploys istiod", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clPrimary, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Istiod is not Available on Cluster #1; unexpected Condition")
						Expect(common.GetVersionFromIstiod()).To(Equal(version.Version), "Unexpected istiod version")
						Success("Istiod is deployed in the namespace and Running on Cluster #1")

						Eventually(common.GetObject).
							WithArguments(ctx, clRemote, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Istiod is not Available on Cluster #2; unexpected Condition")
						Expect(common.GetVersionFromIstiod()).To(Equal(version.Version), "Unexpected istiod version")
						Success("Istiod is deployed in the namespace and Running on  Cluster #2")
					})
				})

				When("Gateway is created in both clusters", func() {
					BeforeAll(func(ctx SpecContext) {
						eastGatewayURL := "https://raw.githubusercontent.com/istio-ecosystem/sail-operator/main/docs/multicluster/east-west-gateway-net1.yaml"
						Expect(kubectl.Apply(controlPlaneNamespace, eastGatewayURL, kubeconfig)).To(Succeed(), "Gateway creation failed on Cluster #1")

						westGatewayURL := "https://raw.githubusercontent.com/istio-ecosystem/sail-operator/main/docs/multicluster/east-west-gateway-net2.yaml"
						Expect(kubectl.Apply(controlPlaneNamespace, westGatewayURL, kubeconfig2)).To(Succeed(), "Gateway creation failed on  Cluster #2")

						// Expose the Gateway service in both clusters
						exposeServiceURL := "https://raw.githubusercontent.com/istio-ecosystem/sail-operator/main/docs/multicluster/expose-services.yaml"
						Expect(kubectl.Apply(controlPlaneNamespace, exposeServiceURL, kubeconfig)).To(Succeed(), "Expose Service creation failed on Cluster #1")
						Expect(kubectl.Apply(controlPlaneNamespace, exposeServiceURL, kubeconfig2)).To(Succeed(), "Expose Service creation failed on  Cluster #2")
					})

					It("updates both Gateway status to Available", func(ctx SpecContext) {
						Eventually((common.GetObject)).
							WithArguments(ctx, clPrimary, kube.Key("istio-eastwestgateway", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Gateway is not Ready on Cluster #1; unexpected Condition")

						Eventually((common.GetObject)).
							WithArguments(ctx, clRemote, kube.Key("istio-eastwestgateway", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Gateway is not Ready on Cluster #2; unexpected Condition")
						Success("Gateway is created and available in both clusters")
					})
				})

				When("are installed Secondsecrets on each cluster", func() {
					BeforeAll(func(ctx SpecContext) {
						// Get the internal IP of the control plane node in both clusters
						internalIPCluster1, err := kubectl.GetInternalIP("node-role.kubernetes.io/control-plane", kubeconfig)
						Expect(err).NotTo(HaveOccurred())
						Expect(internalIPCluster1).NotTo(BeEmpty(), "Internal IP is empty for Cluster #1")

						internalIPCluster2, err := kubectl.GetInternalIP("node-role.kubernetes.io/control-plane", kubeconfig2)
						Expect(internalIPCluster2).NotTo(BeEmpty(), "Internal IP is empty for  Cluster #2")
						Expect(err).NotTo(HaveOccurred())

						// Install a Secondsecret in Cluster #1 that provides access to the  Cluster #2 API server.
						secret, err := istioctl.CreateRemoteSecret(kubeconfig2, "cluster2", internalIPCluster2)
						Expect(err).NotTo(HaveOccurred())
						Expect(kubectl.ApplyString("", secret, kubeconfig)).To(Succeed(), "Remote secret creation failed on Cluster #1")

						// Install a Secondsecret in  Cluster #2 that provides access to the Cluster #1 API server.
						secret, err = istioctl.CreateRemoteSecret(kubeconfig, "cluster1", internalIPCluster1)
						Expect(err).NotTo(HaveOccurred())
						Expect(kubectl.ApplyString("", secret, kubeconfig2)).To(Succeed(), "Remote secret creation failed on Cluster #1")
					})

					It("secrets are created", func(ctx SpecContext) {
						secret, err := common.GetObject(ctx, clPrimary, kube.Key("istio-remote-secret-cluster2", controlPlaneNamespace), &corev1.Secret{})
						Expect(err).NotTo(HaveOccurred())
						Expect(secret).NotTo(BeNil(), "Secret is not created on Cluster #1")

						secret, err = common.GetObject(ctx, clRemote, kube.Key("istio-remote-secret-cluster1", controlPlaneNamespace), &corev1.Secret{})
						Expect(err).NotTo(HaveOccurred())
						Expect(secret).NotTo(BeNil(), "Secret is not created on  Cluster #2")
						Success("Remote secrets are created in both clusters")
					})
				})

				When("sample apps are deployed in both clusters", func() {
					BeforeAll(func(ctx SpecContext) {
						// Deploy the sample app in both clusters
						deploySampleApp("sample", version, kubeconfig, kubeconfig2)
						Success("Sample app is deployed in both clusters")
					})

					It("updates the pods status to Ready", func(ctx SpecContext) {
						samplePodsCluster1 := &corev1.PodList{}

						clPrimary.List(ctx, samplePodsCluster1, client.InNamespace("sample"))
						Expect(samplePodsCluster1.Items).ToNot(BeEmpty(), "No pods found in bookinfo namespace")

						for _, pod := range samplePodsCluster1.Items {
							Eventually(common.GetObject).
								WithArguments(ctx, clPrimary, kube.Key(pod.Name, "sample"), &corev1.Pod{}).
								Should(HaveCondition(corev1.PodReady, metav1.ConditionTrue), "Pod is not Ready on Cluster #1; unexpected Condition")
						}

						samplePodsCluster2 := &corev1.PodList{}
						clRemote.List(ctx, samplePodsCluster2, client.InNamespace("sample"))
						Expect(samplePodsCluster2.Items).ToNot(BeEmpty(), "No pods found in bookinfo namespace")

						for _, pod := range samplePodsCluster2.Items {
							Eventually(common.GetObject).
								WithArguments(ctx, clRemote, kube.Key(pod.Name, "sample"), &corev1.Pod{}).
								Should(HaveCondition(corev1.PodReady, metav1.ConditionTrue), "Pod is not Ready on Cluster #2; unexpected Condition")
						}
						Success("Sample app is created in both clusters and Running")
					})

					It("can access the sample app from both clusters", func(ctx SpecContext) {
						sleepPodNameCluster1, err := common.GetPodNameByLabel(ctx, clPrimary, "sample", "app", "sleep")
						Expect(sleepPodNameCluster1).NotTo(BeEmpty(), "Sleep pod not found on Cluster #1")
						Expect(err).NotTo(HaveOccurred(), "Error getting sleep pod name on Cluster #1")

						sleepPodNameCluster2, err := common.GetPodNameByLabel(ctx, clRemote, "sample", "app", "sleep")
						Expect(sleepPodNameCluster2).NotTo(BeEmpty(), "Sleep pod not found on  Cluster #2")
						Expect(err).NotTo(HaveOccurred(), "Error getting sleep pod name on  Cluster #2")

						// Run the curl command from the sleep pod in the  Cluster #2 and get response list to validate that we get responses from both clusters
						Cluster2Responses := strings.Join(getListCurlResponses(sleepPodNameCluster2, kubeconfig2), "\n")
						Expect(Cluster2Responses).To(ContainSubstring("Hello version: v1"), "Responses from  Cluster #2 are not the expected")
						Expect(Cluster2Responses).To(ContainSubstring("Hello version: v2"), "Responses from  Cluster #2 are not the expected")

						// Run the curl command from the sleep pod in the Cluster #1 and get response list to validate that we get responses from both clusters
						Cluster1Responses := strings.Join(getListCurlResponses(sleepPodNameCluster1, kubeconfig), "\n")
						Expect(Cluster1Responses).To(ContainSubstring("Hello version: v1"), "Responses from Cluster #1 are not the expected")
						Expect(Cluster1Responses).To(ContainSubstring("Hello version: v2"), "Responses from Cluster #1 are not the expected")
						Success("Sample app is accessible from both clusters")
					})
				})

				When("sample apps are deleted in both clusters", func() {
					BeforeAll(func(ctx SpecContext) {
						// Delete the entire sample namespace in both clusters
						Expect(kubectl.DeleteNamespace("sample", kubeconfig)).To(Succeed(), "Namespace failed to be deleted on Cluster #1")
						Expect(kubectl.DeleteNamespace("sample", kubeconfig2)).To(Succeed(), "Namespace failed to be deleted on  Cluster #2")
					})

					It("sample app is deleted in both clusters and the namespace is empty", func(ctx SpecContext) {
						common.CheckNamespaceEmpty(ctx, clPrimary, "sample")
						common.CheckNamespaceEmpty(ctx, clRemote, "sample")
						Success("Sample app is deleted in both clusters")
					})
				})

				When("control plane namespace and gateway are deleted in both clusters", func() {
					BeforeEach(func() {
						// Delete the Istio CR in both clusters
						Expect(kubectl.Delete(controlPlaneNamespace, "istio", istioName, kubeconfig)).To(Succeed(), "Istio CR failed to be deleted")
						Expect(kubectl.Delete(controlPlaneNamespace, "istio", istioName, kubeconfig2)).To(Succeed(), "Istio CR failed to be deleted")
						Success("Istio CR is deleted in both clusters")

						// Delete the gateway in both clusters
						eastGatewayURL := "https://raw.githubusercontent.com/istio-ecosystem/sail-operator/main/docs/multicluster/east-west-gateway-net1.yaml"
						Expect(kubectl.DeleteFromFile(eastGatewayURL, kubeconfig)).To(Succeed(), "Gateway deletion failed on Cluster #1")

						westGatewayURL := "https://raw.githubusercontent.com/istio-ecosystem/sail-operator/main/docs/multicluster/east-west-gateway-net2.yaml"
						Expect(kubectl.DeleteFromFile(westGatewayURL, kubeconfig2)).To(Succeed(), "Gateway deletion failed on  Cluster #2")

						// Delete the namespace in both clusters
						Expect(kubectl.DeleteNamespace(controlPlaneNamespace, kubeconfig)).To(Succeed(), "Namespace failed to be deleted on Cluster #1")
						Expect(kubectl.DeleteNamespace(controlPlaneNamespace, kubeconfig2)).To(Succeed(), "Namespace failed to be deleted on  Cluster #2")
					})

					It("removes everything from the namespace", func(ctx SpecContext) {
						Eventually(clPrimary.Get).WithArguments(ctx, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(ReturnNotFoundError(), "Istiod should not exist anymore")
						Eventually(clRemote.Get).WithArguments(ctx, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(ReturnNotFoundError(), "Istiod should not exist anymore")
						common.CheckNamespaceEmpty(ctx, clPrimary, controlPlaneNamespace)
						common.CheckNamespaceEmpty(ctx, clRemote, controlPlaneNamespace)
						Success("Namespace is empty")
					})
				})
			})
		}
	})

	AfterAll(func(ctx SpecContext) {
		// Delete the Sail Operator from both clusters
		Expect(kubectl.DeleteNamespace(namespace, kubeconfig)).To(Succeed(), "Namespace failed to be deleted on Cluster #1")
		Expect(kubectl.DeleteNamespace(namespace, kubeconfig2)).To(Succeed(), "Namespace failed to be deleted on  Cluster #2")

		// Delete the intermediate CA from both clusters
		common.CheckNamespaceEmpty(ctx, clPrimary, namespace)
		common.CheckNamespaceEmpty(ctx, clRemote, namespace)
	})
})

// deploySampleApp deploys the sample app in the given cluster
func deploySampleApp(ns string, istioVersion supportedversion.VersionInfo, kubeconfig string, kubeconfig2 string) {
	// Create the namespace
	Expect(kubectl.CreateNamespace(ns, kubeconfig)).To(Succeed(), "Namespace failed to be created")
	Expect(kubectl.CreateNamespace(ns, kubeconfig2)).To(Succeed(), "Namespace failed to be created")

	// Label the namespace
	Expect(kubectl.Patch("", "namespace", ns, "merge", `{"metadata":{"labels":{"istio-injection":"enabled"}}}`)).
		To(Succeed(), "Error patching sample namespace")
	Expect(kubectl.Patch("", "namespace", ns, "merge", `{"metadata":{"labels":{"istio-injection":"enabled"}}}`, kubeconfig2)).
		To(Succeed(), "Error patching sample namespace")

	version := istioVersion.Version
	// Deploy the sample app from upstream URL in both clusters
	if istioVersion.Name == "latest" {
		version = "master"
	}
	helloWorldURL := fmt.Sprintf("https://raw.githubusercontent.com/istio/istio/%s/samples/helloworld/helloworld.yaml", version)
	Expect(kubectl.ApplyWithLabels(ns, helloWorldURL, "service=helloworld", kubeconfig)).To(Succeed(), "Sample service deploy failed on Cluster #1")
	Expect(kubectl.ApplyWithLabels(ns, helloWorldURL, "service=helloworld", kubeconfig2)).To(Succeed(), "Sample service deploy failed on  Cluster #2")

	Expect(kubectl.ApplyWithLabels(ns, helloWorldURL, "version=v1", kubeconfig)).To(Succeed(), "Sample service deploy failed on Cluster #1")
	Expect(kubectl.ApplyWithLabels(ns, helloWorldURL, "version=v2", kubeconfig2)).To(Succeed(), "Sample service deploy failed on  Cluster #2")

	sleepURL := fmt.Sprintf("https://raw.githubusercontent.com/istio/istio/%s/samples/sleep/sleep.yaml", version)
	Expect(kubectl.Apply(ns, sleepURL, kubeconfig)).To(Succeed(), "Sample sleep deploy failed on Cluster #1")
	Expect(kubectl.Apply(ns, sleepURL, kubeconfig2)).To(Succeed(), "Sample sleep deploy failed on  Cluster #2")
}

// getListCurlResponses runs the curl command 10 times from the sleep pod in the given cluster and get response list
func getListCurlResponses(podName, kubeconfig string) []string {
	var responses []string
	for i := 0; i < 10; i++ {
		response, err := kubectl.Exec("sample", podName, "sleep", "curl -sS helloworld.sample:5000/hello", kubeconfig)
		Expect(err).NotTo(HaveOccurred())
		responses = append(responses, response)
	}
	return responses
}
