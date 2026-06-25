/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/shamubernetes/git-mirror-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "git-mirror-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "git-mirror-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "git-mirror-operator-controller-manager-metrics-service"

// githubWebhookServiceName is the service exposing the GitHub webhook endpoint.
const githubWebhookServiceName = "git-mirror-operator-github-webhook-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "git-mirror-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string
	skipTeardown := os.Getenv("E2E_SKIP_TEARDOWN") == "true"

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		if skipTeardown {
			_, _ = fmt.Fprintln(GinkgoWriter, "Skipping e2e teardown because E2E_SKIP_TEARDOWN=true")
			return
		}

		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=git-mirror-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("waiting for the metrics endpoint to be ready")
			verifyMetricsEndpointReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "endpoints", metricsServiceName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("8443"), "Metrics endpoint is not ready")
			}
			Eventually(verifyMetricsEndpointReady).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted).Should(Succeed())

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token); curl --fail --show-error --silent --insecure -H \"Authorization: Bearer ${TOKEN}\" https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccount": "%s"
					}
				}`, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			metricsOutput := getMetricsOutput()
			Expect(metricsOutput).To(ContainSubstring(
				"controller_runtime_reconcile_total",
			))
		})

		It("should schedule a sync Job from a signed GitHub push webhook", func() {
			By("creating sync job prerequisites")
			commands := []*exec.Cmd{
				exec.Command("kubectl", "create", "secret", "generic", "source-repo-github-webhook",
					"-n", namespace, "--from-literal=secret=webhook-secret"),
				exec.Command("kubectl", "create", "secret", "generic", "source-repo-source-ssh",
					"-n", namespace, "--from-literal=ssh-privatekey=dummy-source-key"),
				exec.Command("kubectl", "create", "secret", "generic", "source-repo-target-ssh",
					"-n", namespace, "--from-literal=ssh-privatekey=dummy-target-key"),
			}
			for _, cmd := range commands {
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}

			By("creating a GitMirror resource")
			applyManifest(fmt.Sprintf(`
apiVersion: mirror.maude.dev/v1alpha1
kind: GitMirror
metadata:
  name: source-repo
  namespace: %s
spec:
  provider: github
  github:
    owner: example
    repo: source-repo
    webhookSecretRef:
      name: source-repo-github-webhook
      key: secret
  source:
    url: git@github.com:example/source-repo.git
    sshSecretRef:
      name: source-repo-source-ssh
      key: ssh-privatekey
  target:
    url: git@codeberg.org:example/source-repo.git
    sshSecretRef:
      name: source-repo-target-ssh
      key: ssh-privatekey
  mirror:
    mode: exact
  job:
    image: %s
    activeDeadlineSeconds: 60
    ttlSecondsAfterFinished: 60
`, namespace, syncContractImage))

			By("port-forwarding the GitHub webhook endpoint")
			portForward := exec.Command("kubectl", "port-forward", "service/"+githubWebhookServiceName, "18082:8082", "-n", namespace)
			portForward.Stdout = GinkgoWriter
			portForward.Stderr = GinkgoWriter
			Expect(portForward.Start()).To(Succeed())
			defer func() {
				if portForward.Process != nil {
					_ = portForward.Process.Kill()
					_ = portForward.Wait()
				}
			}()

			By("sending a signed GitHub push event")
			payload := []byte(`{"repository":{"full_name":"example/source-repo"},"after":"abc123"}`)
			webhookClient := &http.Client{Timeout: 5 * time.Second}
			sendWebhook := func(g Gomega) {
				req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:18082/webhooks/github", bytes.NewReader(payload))
				g.Expect(err).NotTo(HaveOccurred())
				req.Header.Set("X-GitHub-Event", "push")
				req.Header.Set("X-GitHub-Delivery", "delivery-e2e-1")
				req.Header.Set("X-Hub-Signature-256", webhookSignature(payload, "webhook-secret"))

				resp, err := webhookClient.Do(req)
				g.Expect(err).NotTo(HaveOccurred())
				defer func() {
					_ = resp.Body.Close()
				}()
				body, err := io.ReadAll(resp.Body)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(resp.StatusCode).To(Equal(http.StatusAccepted), string(body))
			}
			Eventually(sendWebhook).Should(Succeed())

			By("verifying a sync Job was created and recorded in status")
			var jobName string
			verifyJob := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "jobs", "-n", namespace,
					"-l", "mirror.maude.dev/gitmirror=source-repo",
					"-o", "jsonpath={.items[*].metadata.name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				names := strings.Fields(output)
				g.Expect(names).To(HaveLen(1))
				jobName = names[0]

				cmd = exec.Command("kubectl", "get", "gitmirror", "source-repo", "-n", namespace,
					"-o", "jsonpath={.status.lastJobName}")
				output, err = utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal(jobName))
			}
			Eventually(verifyJob).Should(Succeed())

			By("verifying the sync Job completed its runner contract")
			cmd := exec.Command("kubectl", "wait", "job/"+jobName,
				"--for=condition=complete", "-n", namespace, "--timeout=2m")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "sync contract job should complete")

			cmd = exec.Command("kubectl", "logs", "job/"+jobName, "-n", namespace)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "sync contract job logs should be available")
			Expect(output).To(ContainSubstring("sync contract ok"))

			By("verifying successful sync status records the mirrored revision")
			verifyMirroredRevision := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "gitmirror", "source-repo", "-n", namespace,
					"-o", "jsonpath={.status.lastMirroredRevision}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("abc123"))
			}
			Eventually(verifyMirroredRevision).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})
})

func webhookSignature(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func applyManifest(manifest string) {
	file, err := os.CreateTemp("", "gitmirror-e2e-*.yaml")
	Expect(err).NotTo(HaveOccurred())
	defer func() {
		_ = os.Remove(file.Name())
	}()
	_, err = file.WriteString(manifest)
	Expect(err).NotTo(HaveOccurred())
	Expect(file.Close()).To(Succeed())

	cmd := exec.Command("kubectl", "apply", "-f", file.Name())
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() string {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	metricsOutput, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
	return metricsOutput
}
