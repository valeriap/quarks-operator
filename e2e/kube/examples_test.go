package kube_test

import (
	b64 "encoding/base64"
	"fmt"
	"path"
	"strings"

	ejv1 "code.cloudfoundry.org/cf-operator/pkg/kube/apis/extendedjob/v1alpha1"
	"code.cloudfoundry.org/cf-operator/testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Examples Directory", func() {

	var (
		example      string
		yamlFilePath string
		kubectl      *testing.Kubectl
	)

	podWait := func(name string) {
		err := kubectl.Wait(namespace, "ready", name, kubectl.PollTimeout)
		Expect(err).ToNot(HaveOccurred())
	}

	JustBeforeEach(func() {
		kubectl = testing.NewKubectl()
		yamlFilePath = path.Join(examplesDir, example)
		err := testing.Create(namespace, yamlFilePath)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("extended-statefulset configs examples", func() {
		BeforeEach(func() {
			example = "extended-statefulset/exstatefulset_configs.yaml"
		})

		It("creates and updates statefulsets", func() {
			By("Checking for pods")
			podWait("pod/example-extendedstatefulset-v1-0")
			podWait("pod/example-extendedstatefulset-v1-1")

			yamlUpdatedFilePath := examplesDir + "extended-statefulset/exstatefulset_configs_updated.yaml"

			By("Updating the config value used by pods")
			err := testing.Apply(namespace, yamlUpdatedFilePath)
			Expect(err).ToNot(HaveOccurred())

			By("Checking for pods")
			podWait("pod/example-extendedstatefulset-v2-0")
			podWait("pod/example-extendedstatefulset-v2-1")

			By("Checking the updated value in the env")
			err = kubectl.RunCommandWithCheckString(namespace, "example-extendedstatefulset-v2-0", "env", "SPECIAL_KEY=value1Updated")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.RunCommandWithCheckString(namespace, "example-extendedstatefulset-v2-1", "env", "SPECIAL_KEY=value1Updated")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("bosh-deployment service example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment-with-service.yaml"
		})

		It("creates the deployment and an endpoint", func() {
			By("Checking for pods")
			podWait("pod/nats-deployment-nats-v1-0")
			podWait("pod/nats-deployment-nats-v1-1")

			err := kubectl.WaitForService(namespace, "nats-service")
			Expect(err).ToNot(HaveOccurred())

			ip0, err := testing.GetData(namespace, "pod", "nats-deployment-nats-v1-0", "go-template={{.status.podIP}}")
			Expect(err).ToNot(HaveOccurred())

			ip1, err := testing.GetData(namespace, "pod", "nats-deployment-nats-v1-1", "go-template={{.status.podIP}}")
			Expect(err).ToNot(HaveOccurred())

			out, err := testing.GetData(namespace, "endpoints", "nats-service", "go-template=\"{{(index .subsets 0).addresses}}\"")
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(ContainSubstring(string(ip0)))
			Expect(out).To(ContainSubstring(string(ip1)))
		})
	})

	Context("bosh-deployment example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment.yaml"
		})

		It("deploys two pods", func() {
			podWait("pod/nats-deployment-nats-v1-0")
			podWait("pod/nats-deployment-nats-v1-1")
		})
	})

	Context("bosh-deployment with a custom variable example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment-with-custom-variable.yaml"
		})

		It("uses the custom variable", func() {
			By("Checking for pods")
			podWait("pod/nats-deployment-nats-v1-0")
			podWait("pod/nats-deployment-nats-v1-1")

			By("Checking the value in the config file")
			outFile, err := testing.RunCommandWithOutput(namespace, "nats-deployment-nats-v1-1", "awk 'NR == 18 {print substr($2,2,17)}' /var/vcap/jobs/nats/config/nats.conf")
			Expect(err).ToNot(HaveOccurred())

			outSecret, err := testing.GetData(namespace, "secret", "nats-deployment.var-custom-password", "go-template={{.data.password}}")
			Expect(err).ToNot(HaveOccurred())
			outSecretDecoded, _ := b64.StdEncoding.DecodeString(string(outSecret))
			Expect(strings.TrimSuffix(outFile, "\n")).To(ContainSubstring(string(outSecretDecoded)))
		})

	})

	Context("bosh-deployment with a custom variable and logging sidecar disable example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment-with-custom-variable-disable-sidecar.yaml"
		})
		It("disables the logging sidecar", func() {
			By("Checking for pods")
			podWait("pod/nats-deployment-nats-v1-0")
			podWait("pod/nats-deployment-nats-v1-1")

			By("Ensure only one container exists")
			containerName, err := testing.GetData(namespace, "pod", "nats-deployment-nats-v1-0", "jsonpath={range .spec.containers[*]}{.name}")
			Expect(err).ToNot(HaveOccurred())
			Expect(containerName).To(ContainSubstring("nats-nats"))
			Expect(containerName).ToNot(ContainSubstring("logs"))

			containerName, err = testing.GetData(namespace, "pod", "nats-deployment-nats-v1-1", "jsonpath={range .spec.containers[*]}{.name}")
			Expect(err).ToNot(HaveOccurred())
			Expect(containerName).To(ContainSubstring("nats-nats"))
			Expect(containerName).ToNot(ContainSubstring("logs"))
		})
	})

	Context("bosh-deployment with a implicit variable example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment-with-implicit-variable.yaml"
		})

		It("updates deployment when implicit variable changes", func() {
			By("Checking for pods")
			podWait("pod/nats-deployment-nats-v1-0")

			By("Updating implicit variable")
			implicitVariablePath := examplesDir + "bosh-deployment/implicit-variable-updated.yaml"
			err := testing.Apply(namespace, implicitVariablePath)

			Expect(err).ToNot(HaveOccurred())
			By("Checking for new pods")
			podWait("pod/nats-deployment-nats-v2-0")
		})
	})

	Context("extended-job auto errand delete example", func() {
		BeforeEach(func() {
			example = "extended-job/exjob_auto-errand-deletes-pod.yaml"
		})

		It("deletes pod after job is done", func() {
			By("Checking for pods")
			err := kubectl.WaitForPod(namespace, fmt.Sprintf("%s=deletes-pod-1", ejv1.LabelEJobName), "deletes-pod-1")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitLabelFilter(namespace, "terminate", "pod", fmt.Sprintf("%s=deletes-pod-1", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("extended-job auto errand example", func() {
		BeforeEach(func() {
			example = "extended-job/exjob_auto-errand.yaml"
		})

		It("runs the errand automatically", func() {
			By("Checking for pods")
			err := kubectl.WaitForPod(namespace, fmt.Sprintf("%s=one-time-sleep", ejv1.LabelEJobName), "one-time-sleep")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitLabelFilter(namespace, "complete", "pod", fmt.Sprintf("%s=one-time-sleep", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("extended-job auto errand update example", func() {
		BeforeEach(func() {
			example = "extended-job/exjob_auto-errand-updating.yaml"
		})

		It("triggers job again when config is updated", func() {
			By("Checking for pods")

			err := kubectl.WaitForPod(namespace, fmt.Sprintf("%s=auto-errand-sleep-again", ejv1.LabelEJobName), "auto-errand-sleep-again")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitLabelFilter(namespace, "complete", "pod", fmt.Sprintf("%s=auto-errand-sleep-again", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())

			By("Delete the pod")
			err = testing.DeleteLabelFilter(namespace, "pod", fmt.Sprintf("%s=auto-errand-sleep-again", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())

			By("Update the config change")
			yamlFilePath = examplesDir + "extended-job/exjob_auto-errand-updating_updated.yaml"

			err = testing.Apply(namespace, yamlFilePath)
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitForPod(namespace, fmt.Sprintf("%s=auto-errand-sleep-again", ejv1.LabelEJobName), "auto-errand-sleep-again")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitLabelFilter(namespace, "complete", "pod", fmt.Sprintf("%s=auto-errand-sleep-again", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("extended-job errand example", func() {
		BeforeEach(func() {
			example = "extended-job/exjob_errand.yaml"
		})

		It("starts job if trigger is changed manually", func() {
			By("Updating exjob to trigger now")
			yamlFilePath = examplesDir + "extended-job/exjob_errand_updated.yaml"
			err := testing.Apply(namespace, yamlFilePath)
			Expect(err).ToNot(HaveOccurred())

			By("Checking for pods")
			err = kubectl.WaitForPod(namespace, fmt.Sprintf("%s=manual-sleep", ejv1.LabelEJobName), "manual-sleep")
			Expect(err).ToNot(HaveOccurred())

			err = kubectl.WaitLabelFilter(namespace, "complete", "pod", fmt.Sprintf("%s=manual-sleep", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("extended-job output example", func() {
		BeforeEach(func() {
			example = "extended-job/exjob_output.yaml"
		})

		It("creates a secret from job output", func() {
			By("Checking for pods")
			err := kubectl.WaitLabelFilter(namespace, "complete", "pod", fmt.Sprintf("%s=myfoo", ejv1.LabelEJobName))
			Expect(err).ToNot(HaveOccurred())

			By("Checking for secret")
			err = kubectl.WaitForSecret(namespace, "foo-json")
			Expect(err).ToNot(HaveOccurred())

			By("Checking the secret data created")
			outSecret, err := testing.GetData(namespace, "secret", "foo-json", "go-template={{.data.foo}}")
			Expect(err).ToNot(HaveOccurred())
			outSecretDecoded, _ := b64.StdEncoding.DecodeString(string(outSecret))
			Expect(string(outSecretDecoded)).To(Equal("1"))
		})
	})

	Context("extended-secret example", func() {
		BeforeEach(func() {
			example = "extended-secret/password.yaml"
		})

		It("generates a password", func() {
			By("Checking the generated password")
			err := testing.SecretCheckData(namespace, "gen-secret1", ".data.password")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("API server signed certificate example", func() {
		BeforeEach(func() {
			example = "extended-secret/certificate.yaml"
		})

		It("creates a signed cert", func() {
			By("Checking the generated certificate")
			err := kubectl.WaitForSecret(namespace, "gen-certificate")
			Expect(err).ToNot(HaveOccurred(), "error waiting for secret")
			err = testing.SecretCheckData(namespace, "gen-certificate", ".data.certificate")
			Expect(err).ToNot(HaveOccurred(), "error getting for secret")
		})
	})

	Context("self signed certificate example", func() {
		BeforeEach(func() {
			example = "extended-secret/loggregator-ca-cert.yaml"
		})

		It("creates a self-signed certificate", func() {
			certYamlFilePath := examplesDir + "extended-secret/loggregator-tls-agent-cert.yaml"

			By("Creating ExtendedSecrets")
			err := testing.Create(namespace, certYamlFilePath)
			Expect(err).ToNot(HaveOccurred())

			By("Checking the generated certificates")
			err = kubectl.WaitForSecret(namespace, "example.var-loggregator-ca")
			Expect(err).ToNot(HaveOccurred(), "error waiting for ca secret")
			err = kubectl.WaitForSecret(namespace, "example.var-loggregator-tls-agent")
			Expect(err).ToNot(HaveOccurred(), "error waiting for cert secret")

			By("Checking the generated certificates")
			outSecret, err := testing.GetData(namespace, "secret", "example.var-loggregator-ca", "go-template={{.data.certificate}}")
			Expect(err).ToNot(HaveOccurred())
			rootPEM, _ := b64.StdEncoding.DecodeString(string(outSecret))

			outSecret, err = testing.GetData(namespace, "secret", "example.var-loggregator-tls-agent", "go-template={{.data.certificate}}")
			Expect(err).ToNot(HaveOccurred())
			certPEM, _ := b64.StdEncoding.DecodeString(string(outSecret))

			By("Verify the certificates")
			dnsName := "metron"
			err = testing.CertificateVerify(rootPEM, certPEM, dnsName)
			Expect(err).ToNot(HaveOccurred(), "error verifying certificates")
		})
	})

	FContext("bosh dns example", func() {
		BeforeEach(func() {
			example = "bosh-deployment/boshdeployment-with-bosh-dns.yaml"
		})

		It("resolves bosh and k8s domains", func() {
			By("Getting expected IP")
			podName := "nats-deployment-nats-v1-0"
			podWait(fmt.Sprintf("pod/%s", podName))
			status, err := kubectl.PodStatus(namespace, podName)
			Expect(err).ToNot(HaveOccurred(), "error reading status")
			ip := status.PodIP

			By("DNS lookup")
			resolvableNames := []string{addNamespace("myalias.%s.svc.cluster.local"),
				addNamespace("myalias.%s.svc.cluster.local."),
				addNamespace("myalias.%s.svc"),
				addNamespace("myalias.%s"),
				addNamespace("nats-deployment-nats.%s"),
				addNamespace("nats-deployment-nats.%s.svc"),
				addNamespace("nats-deployment-nats.%s.svc.cluster.local"),
				"myalias",
				"myalias.service.cf.internal.",
				"myalias.service.cf.internal",
				"nats",
				"nats.service.cf.internal",
				"nats.service.cf.internal.",
				"nats-deployment-nats"}

			for _, name := range resolvableNames {
				err = kubectl.RunCommandWithCheckString(namespace, podName, fmt.Sprintf("nslookup %s", name), ip)
				Expect(err).ToNot(HaveOccurred())

			}
		})
	})

})

func addNamespace(s string) string {
	return fmt.Sprintf(s, namespace)
}
