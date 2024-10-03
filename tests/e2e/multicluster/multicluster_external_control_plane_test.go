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
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/istio-ecosystem/sail-operator/api/v1alpha1"
	"github.com/istio-ecosystem/sail-operator/pkg/kube"
	"github.com/istio-ecosystem/sail-operator/pkg/test/project"
	. "github.com/istio-ecosystem/sail-operator/pkg/test/util/ginkgo"
	"github.com/istio-ecosystem/sail-operator/pkg/test/util/supportedversion"
	common "github.com/istio-ecosystem/sail-operator/tests/e2e/util/common"
	. "github.com/istio-ecosystem/sail-operator/tests/e2e/util/gomega"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/helm"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/istioctl"
	"github.com/istio-ecosystem/sail-operator/tests/e2e/util/shell"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Multicluster deployment models", Label("multicluster"), Ordered, func() {
	SetDefaultEventuallyTimeout(180 * time.Second)
	SetDefaultEventuallyPollingInterval(time.Second)
	externalAddress := ""

	BeforeAll(func(ctx SpecContext) {
		if !skipDeploy {
			// Deploy the Sail Operator on both clusters
			Expect(kubectlClient1.CreateNamespace(namespace)).To(Succeed(), "Namespace failed to be created on External Control Plane Cluster")
			Expect(kubectlClient2.CreateNamespace(namespace)).To(Succeed(), "Namespace failed to be created on Remote Cluster")

			Expect(helm.Install("sail-operator", filepath.Join(project.RootDir, "chart"), "--namespace "+namespace, "--set=image="+image, "--kubeconfig "+kubeconfig)).
				To(Succeed(), "Operator failed to be deployed in External Control Plane Cluster")

			Eventually(common.GetObject).
				WithArguments(ctx, clPrimary, kube.Key(deploymentName, namespace), &appsv1.Deployment{}).
				Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Error getting Istio CRD")
			Success("Operator is deployed in the External Control Plane cluster namespace and Running")

			Expect(helm.Install("sail-operator", filepath.Join(project.RootDir, "chart"), "--namespace "+namespace, "--set=image="+image, "--kubeconfig "+kubeconfig2)).
				To(Succeed(), "Operator failed to be deployed in Remote Cluster")

			Eventually(common.GetObject).
				WithArguments(ctx, clRemote, kube.Key(deploymentName, namespace), &appsv1.Deployment{}).
				Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Error getting Istio CRD")
			Success("Operator is deployed in the Remote namespace and Running")
		}
	})

	Describe("External Control Plane configuration", func() {
		// Test the External Control Plane configuration for each supported Istio version
		for _, version := range supportedversion.List {
			// Skip versions less than 1.23.0
			if version.Major < 1 || (version.Major == 1 && version.Minor < 23) {
				continue
			}

			Context("Istio version is: "+version.Version, func() {
				When("Istio is created in External Control Plane cluster", func() {
					BeforeAll(func(ctx SpecContext) {
						Expect(kubectlClient1.CreateNamespace(controlPlaneNamespace)).To(Succeed(), "Namespace failed to be created")

						IstioResourceYAML := `
apiVersion: sailoperator.io/v1alpha1
kind: Istio
metadata:
  name: default
spec:
  version: %s
  namespace: %s
  values:
    global:
      network: network1`
						IstioResourceYAML = fmt.Sprintf(IstioResourceYAML, version.Name, controlPlaneNamespace)
						Log("Istio CR Primary: ", IstioResourceYAML)
						Expect(kubectlClient1.CreateFromString(IstioResourceYAML)).To(Succeed(), "Istio Resource creation failed on External Control Plane Cluster")
					})

					It("updates Istio CR on External Control Plane cluster status to Ready", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clPrimary, kube.Key(istioName), &v1alpha1.Istio{}).
							Should(HaveCondition(v1alpha1.IstioConditionReady, metav1.ConditionTrue), "Istio is not Ready on External Control Plane cluster; unexpected Condition")
						Success("Istio CR is Ready on External Control Plane Cluster")
					})

					It("deploys istiod", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clPrimary, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Istiod is not Available on External Control Plane cluster; unexpected Condition")
						Expect(common.GetVersionFromIstiod()).To(Equal(version.Version), "Unexpected istiod version")
						Success("Istiod is deployed in the namespace and Running on External Control Plane Cluster")
					})
				})

				When("Gateway is created on External Control Plane cluster ", func() {
					BeforeAll(func(ctx SpecContext) {
						Expect(kubectlClient1.SetNamespace(controlPlaneNamespace).Apply(controlPlaneGateway)).To(Succeed(), "Gateway creation failed on External Control Plane Cluster")
					})

					It("updates Gateway status to Available", func(ctx SpecContext) {
						Eventually((common.GetObject)).
							WithArguments(ctx, clPrimary, kube.Key("istio-ingressgateway", controlPlaneNamespace), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Gateway is not Ready on Primary; unexpected Condition")
					})

					It("has a LoadBalancer address", func(ctx SpecContext) {
						externalAddress, err = common.GetSVCLoadBalancerAddress(ctx, clPrimary, controlPlaneNamespace, "istio-ingressgateway")
						Expect(externalAddress).NotTo(BeEmpty(), "Pilot Address is empty")
						Expect(err).NotTo(HaveOccurred(), "Error getting Pilot Address")
						Success("Gateway is created and available in External Control Plane cluster")
					})
				})

				When("RemoteIstio is created in Remote cluster", func() {
					BeforeAll(func(ctx SpecContext) {
						Expect(kubectlClient2.CreateNamespace("external-istiod")).To(Succeed(), "Namespace failed to be created on Remote Cluster")
						Expect(kubectlClient1.CreateNamespace("external-istiod")).To(Succeed(), "Namespace failed to be created on Remote Cluster")

						RemoteIstioExternalYAML := `
apiVersion: sailoperator.io/v1alpha1
kind: RemoteIstio
metadata:
  name: external-istiod
spec:
  version: %s
  namespace: external-istiod
  values:
    defaultRevision: external-istiod
    global:
      istioNamespace: external-istiod
      remotePilotAddress: %s
      configCluster: true
    pilot:
      configMap: true
    istiodRemote:
      injectionPath: /inject/cluster/cluster2/net/network1`

						RemoteIstioExternalYAML = fmt.Sprintf(RemoteIstioExternalYAML, version.Name, externalAddress)
						Log("RemoteIstio CR: ", RemoteIstioExternalYAML)
						By("Creating RemoteIstio CR on Remote Cluster")
						Expect(kubectlClient2.CreateFromString(RemoteIstioExternalYAML)).To(Succeed(), "RemoteIstio Resource creation failed on Remote Cluster")

						// To be able to access the remote cluster from the External Control Plane cluster, we need to create a secret in the External Control Plane cluster
						// RemoteIstio resource will not be Ready until the secret is created
						// Get the internal IP of the control plane node in Remote cluster
						internalIPRemote, err := kubectlClient2.GetInternalIP("node-role.kubernetes.io/control-plane")
						Expect(internalIPRemote).NotTo(BeEmpty(), "Internal IP is empty for Remote Cluster")
						Expect(err).NotTo(HaveOccurred())

						// Wait for the RemoteIstio CR to be created, this can be moved to a condition verification, but the resource it not will be Ready at this point
						time.Sleep(5 * time.Second)

						// Install a remote secret in External Control Plane cluster that provides access to the Remote cluster API server.
						By("Creating Remote Secret on External Control Plane Cluster")
						_, err = kubectlClient1.SetNamespace("external-istiod").ExecuteKubectlCmd("create sa istiod-service-account")
						Expect(err).NotTo(HaveOccurred(), "Error creating service account on External Control Plane Cluster")
						secret, err := istioctl.CreateRemoteSecret(kubeconfig2, internalIPRemote, "--type=config", "--namespace=external-istiod", "--service-account=istiod-external-istiod", "--create-service-account=false")
						Expect(err).NotTo(HaveOccurred(), "Error creating secret on External Control Plane Cluster")
						Expect(kubectlClient1.ApplyString(secret)).To(Succeed(), "Remote secret creation failed on External Control Plane Cluster")
					})

					It("secret is created", func(ctx SpecContext) {
						secret, err := common.GetObject(ctx, clRemote, kube.Key("istiod-external-istiod-istio-remote-secret-token", "external-istiod"), &corev1.Secret{})
						Expect(err).NotTo(HaveOccurred())
						Expect(secret).NotTo(BeNil(), "Secret is not created on Primary Cluster")
						Success("Remote secret is created in Primary cluster")
					})
				})

				When("Istio resource is created in External Control Plane cluster to manage external-istiod", func() {
					// Create Istio resource in External Control Plane cluster. This will manage both Istio configuration and proxies on the remote cluster
					BeforeAll(func(ctx SpecContext) {
						istioExternalYAML := `
apiVersion: sailoperator.io/v1alpha1
kind: Istio
metadata:
  name: external-istiod
spec:
  namespace: external-istiod
  profile: empty
  values:
    meshConfig:
      rootNamespace: external-istiod
      defaultConfig:
        discoveryAddress: %s:15012
    pilot:
      enabled: true
      volumes:
      - name: config-volume
        configMap:
          name: istio-external-istiod
      - name: inject-volume
        configMap:
          name: istio-sidecar-injector-external-istiod
      volumeMounts:
      - name: config-volume
        mountPath: /etc/istio/config
      - name: inject-volume
        mountPath: /var/lib/istio/inject
      env:
        INJECTION_WEBHOOK_CONFIG_NAME: "istio-sidecar-injector-external-istiod-external-istiod"
        VALIDATION_WEBHOOK_CONFIG_NAME: "istio-validator-external-istiod-external-istiod"
        EXTERNAL_ISTIOD: "true"
        LOCAL_CLUSTER_SECRET_WATCHER: "true"
        CLUSTER_ID: cluster2
        SHARED_MESH_CONFIG: istio
    global:
      caAddress: %s:15012
      istioNamespace: external-istiod
      operatorManageWebhooks: true
      configValidation: false
      meshID: mesh1
      multiCluster:
        clusterName: cluster2
      network: network1`

						istioExternalYAML = fmt.Sprintf(istioExternalYAML, externalAddress, externalAddress)
						Log("Istio CR External: ", istioExternalYAML)
						Expect(kubectlClient1.CreateFromString(istioExternalYAML)).To(Succeed(), "Istio Resource creation failed on External Control Plane Cluster")
						Success("Istio CR is created in namespace external-istiod on External Control Plane cluster")
					})

					It("updates Istio CR on External Control Plane cluster status to Ready", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clPrimary, kube.Key("external-istiod"), &v1alpha1.Istio{}).
							Should(HaveCondition(v1alpha1.IstioConditionReady, metav1.ConditionTrue), "Istio is not Ready on namespace external-istiod on External Control Plane cluster; unexpected Condition")
						Success("Istio CR is Ready on namespace external-istiod on External Control Plane Cluster")
					})
				})

				When("resources to route traffic are being created", func() {
					BeforeAll(func(ctx SpecContext) {
						resourceYAML := `
apiVersion: networking.istio.io/v1
kind: Gateway
metadata:
  name: external-istiod-gw
  namespace: external-istiod
spec:
  selector:
    istio: ingressgateway
  servers:
    - port:
        number: 15012
        protocol: tls
        name: tls-XDS
      tls:
        mode: PASSTHROUGH
      hosts:
      - "*"
    - port:
        number: 15017
        protocol: tls
        name: tls-WEBHOOK
      tls:
        mode: PASSTHROUGH
      hosts:
      - "*"
---
apiVersion: networking.istio.io/v1
kind: VirtualService
metadata:
  name: external-istiod-vs
  namespace: external-istiod
spec:
    hosts:
    - "*"
    gateways:
    - external-istiod-gw
    tls:
    - match:
      - port: 15012
        sniHosts:
        - "*"
      route:
      - destination:
          host: istiod-external-istiod.external-istiod.svc.cluster.local
          port:
            number: 15012
    - match:
      - port: 15017
        sniHosts:
        - "*"
      route:
      - destination:
          host: istiod-external-istiod.external-istiod.svc.cluster.local
          port:
            number: 443`
						Log("Resource to route traffic: ", resourceYAML)
						Expect(kubectlClient1.CreateFromString(resourceYAML)).To(Succeed(), "Resource creation failed on External Control Plane Cluster")
						Success("Resources to route traffic are created in External Control Plane cluster")
					})

					It("updates RemoteIstio CR status to Ready", func(ctx SpecContext) {
						Eventually(common.GetObject).
							WithArguments(ctx, clRemote, kube.Key("external-istiod"), &v1alpha1.RemoteIstio{}).
							Should(HaveCondition(v1alpha1.IstioConditionReady, metav1.ConditionTrue), "RemoteIstio is not Ready on Remote; unexpected Condition")
						Success("RemoteIstio CR is Ready on Remote Cluster")
					})
				})

				When("sample apps are deployed on Remote cluster", func() {
					BeforeAll(func(ctx SpecContext) {
						// Deploy the sample app only in the Remote cluster
						deploySample("sample", version)
						Success("Sample app is deployed in Remote clusters")
					})

					samplePodsRemote := &corev1.PodList{}

					It("updates the pods status to Ready", func(ctx SpecContext) {
						clRemote.List(ctx, samplePodsRemote, client.InNamespace("sample"))
						Expect(samplePodsRemote.Items).ToNot(BeEmpty(), "No pods found in bookinfo namespace")

						for _, pod := range samplePodsRemote.Items {
							Eventually(common.GetObject).
								WithArguments(ctx, clRemote, kube.Key(pod.Name, "sample"), &corev1.Pod{}).
								Should(HaveCondition(corev1.PodReady, metav1.ConditionTrue), "Pod is not Ready on Remote; unexpected Condition")
						}
						Success("Sample app is created in both clusters and Running")
					})

					It("has sidecars with the correct istio version", func(ctx SpecContext) {
						for _, pod := range samplePodsRemote.Items {
							if strings.Contains(pod.Name, "gateway") {
								// Skip the gateway pod
								continue
							}
							sidecarVersion, err := getProxyVersion(pod.Name, "sample")
							Expect(err).To(Succeed(), "Error getting sidecar version")
							Expect(sidecarVersion).To(ContainSubstring(version.Version), "Sidecar Istio version does not match the expected version")
						}
						Success("Istio sidecar version matches the expected Istio version")
					})

					It("can access the sample app from both clusters", func(ctx SpecContext) {
						sleepPodNameRemote, err := common.GetPodNameByLabel(ctx, clRemote, "sample", "app", "sleep")
						Expect(sleepPodNameRemote).NotTo(BeEmpty(), "Sleep pod not found on Remote Cluster")
						Expect(err).NotTo(HaveOccurred(), "Error getting sleep pod name on Remote Cluster")

						remoteResponses := strings.Join(getListCurlResponses(kubectlClient2, sleepPodNameRemote), "\n")
						Expect(remoteResponses).To(ContainSubstring("Hello version: v1"), "Responses from Remote Cluster are not the expected")
						Success("Sample app is accessible in Remote cluster")
					})
				})

				When("ingress gateway is created in External Control Plane cluster", func() {
					BeforeAll(func(ctx SpecContext) {
						// Check if the crd gateways.gateway.networking.k8s.io exists in the Remote cluster
						crdGateway := &corev1.Namespace{}
						err := clRemote.Get(ctx, kube.Key("gateways.gateway.networking.k8s.io"), crdGateway)
						if err != nil {
							// Generate the ingress gateway yaml using kustomize
							output, err := kubectlClient2.ExecuteKubectlCmd("kustomize github.com/kubernetes-sigs/gateway-api/config/crd?ref=v1.1.0")
							Expect(err).NotTo(HaveOccurred(), "Error executing kustomize command")
							Expect(kubectlClient2.ApplyString(output)).To(Succeed(), "Error creating gateway crd in Remote Cluster")
						}

						// Expose hello-world through the ingress gateway
						istioBranch := version.Version
						if version.Name == "latest" {
							istioBranch = "master"
						}
						helloworldGatewayURL := fmt.Sprintf("https://raw.githubusercontent.com/istio/istio/%s/samples/helloworld/gateway-api/helloworld-gateway.yaml", istioBranch)
						Expect(kubectlClient2.SetNamespace("sample").Apply(helloworldGatewayURL)).To(Succeed(), "Gateway creation failed on Remote Cluster")
					})

					It("updates Gateway status to Available", func(ctx SpecContext) {
						Eventually((common.GetObject)).
							WithArguments(ctx, clRemote, kube.Key("helloworld-gateway-istio", "sample"), &appsv1.Deployment{}).
							Should(HaveCondition(appsv1.DeploymentAvailable, metav1.ConditionTrue), "Gateway helloworld-gateway is not Ready; unexpected Condition")
						Success("Gateway helloworld-gateway is created and available in Remote cluster")
					})

					It("is accessible helloworld through the ingress gateway created in the Remote cluster", func(ctx SpecContext) {
						// Get ingress gateway url from the Remote cluster
						url, err := kubectlClient2.SetNamespace("sample").ExecuteKubectlCmd("get gtw helloworld-gateway -o jsonpath='{.status.addresses[0].value}'")
						Expect(err).NotTo(HaveOccurred(), "Error getting gateway URL from Remote Cluster")
						Expect(url).NotTo(BeEmpty(), "Gateway URL is empty")
						response, err := shell.ExecuteCommand(fmt.Sprintf("curl -s %s:80/hello", url))
						Expect(err).NotTo(HaveOccurred(), "Error executing curl command")
						Expect(response).To(ContainSubstring("Hello version: v1"), "Unexpected response from Remote Cluster")
						Success("Helloworld is accessible in Remote cluster through ingress gateway")
					})
				})

				// When("Istio CR and RemoteIstio CR are deleted in both clusters", func() {
				// 	BeforeEach(func() {
				// 		Expect(kubectlClient1.SetNamespace(controlPlaneNamespace).Delete("istio", istioName)).To(Succeed(), "Istio CR failed to be deleted")
				// 		Expect(kubectlClient2.SetNamespace(controlPlaneNamespace).Delete("remoteistio", istioName)).To(Succeed(), "RemoteIstio CR failed to be deleted")
				// 		Success("Istio and RemoteIstio are deleted")
				// 	})

				// 	It("removes istiod on Primary", func(ctx SpecContext) {
				// 		Eventually(clPrimary.Get).WithArguments(ctx, kube.Key("istiod", controlPlaneNamespace), &appsv1.Deployment{}).
				// 			Should(ReturnNotFoundError(), "Istiod should not exist anymore")
				// 		Success("Istiod is deleted on Primary Cluster")
				// 	})
				// })

				// AfterAll(func(ctx SpecContext) {
				// 	// Delete namespace to ensure clean up for new tests iteration
				// 	Expect(kubectlClient1.DeleteNamespace(controlPlaneNamespace)).To(Succeed(), "Namespace failed to be deleted on Primary Cluster")
				// 	Expect(kubectlClient2.DeleteNamespace(controlPlaneNamespace)).To(Succeed(), "Namespace failed to be deleted on Remote Cluster")

				// 	common.CheckNamespaceEmpty(ctx, clPrimary, controlPlaneNamespace)
				// 	common.CheckNamespaceEmpty(ctx, clRemote, controlPlaneNamespace)
				// 	Success("ControlPlane Namespaces are empty")

				// 	// Delete the entire sample namespace in both clusters
				// 	Expect(kubectlClient1.DeleteNamespace("sample")).To(Succeed(), "Namespace failed to be deleted on Primary Cluster")
				// 	Expect(kubectlClient2.DeleteNamespace("sample")).To(Succeed(), "Namespace failed to be deleted on Remote Cluster")

				// 	common.CheckNamespaceEmpty(ctx, clPrimary, "sample")
				// 	common.CheckNamespaceEmpty(ctx, clRemote, "sample")
				// 	Success("Sample app is deleted in both clusters")
				// })
			})
		}
	})

	// AfterAll(func(ctx SpecContext) {
	// 	// Delete the Sail Operator from both clusters
	// 	Expect(kubectlClient1.DeleteNamespace(namespace)).To(Succeed(), "Namespace failed to be deleted on Primary Cluster")
	// 	Expect(kubectlClient2.DeleteNamespace(namespace)).To(Succeed(), "Namespace failed to be deleted on Remote Cluster")

	// 	// Check that the namespace is empty
	// 	common.CheckNamespaceEmpty(ctx, clPrimary, namespace)
	// 	common.CheckNamespaceEmpty(ctx, clRemote, namespace)
	// })
})

// deploySample deploys the sample in the remote cluster
func deploySample(ns string, istioVersion supportedversion.VersionInfo) {
	// Create the namespace
	Expect(kubectlClient2.CreateNamespace(ns)).To(Succeed(), "Namespace failed to be created")

	// Label the namespace
	Expect(kubectlClient2.Patch("namespace", ns, "merge", `{"metadata":{"labels":{"istio-injection":"enabled"}}}`)).
		To(Succeed(), "Error patching sample namespace")

	version := istioVersion.Version
	// Deploy the sample app from upstream URL in both clusters
	if istioVersion.Name == "latest" {
		version = "master"
	}
	helloWorldURL := fmt.Sprintf("https://raw.githubusercontent.com/istio/istio/%s/samples/helloworld/helloworld.yaml", version)
	Expect(kubectlClient2.SetNamespace(ns).ApplyWithLabels(helloWorldURL, "service=helloworld")).To(Succeed(), "Sample service deploy failed on  Cluster #2")

	Expect(kubectlClient2.SetNamespace(ns).ApplyWithLabels(helloWorldURL, "version=v1")).To(Succeed(), "Sample service deploy failed on  Cluster #2")

	sleepURL := fmt.Sprintf("https://raw.githubusercontent.com/istio/istio/%s/samples/sleep/sleep.yaml", version)
	Expect(kubectlClient2.SetNamespace(ns).Apply(sleepURL)).To(Succeed(), "Sample sleep deploy failed on  Cluster #2")
}

func getProxyVersion(podName, namespace string) (string, error) {
	proxyVersion, err := kubectlClient2.SetNamespace(namespace).Exec(
		podName,
		"istio-proxy",
		`curl -s http://localhost:15000/server_info | grep "ISTIO_VERSION" | awk -F '"' '{print $4}'`)
	if err != nil {
		return "", fmt.Errorf("error getting sidecar version: %w", err)
	}

	return proxyVersion, nil
}
