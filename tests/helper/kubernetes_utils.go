package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// CopyKubeConfigFile copies default kubeconfig file into current context config file
func CopyKubeConfigFile(kubeConfigFile, tempConfigFile string) string {
	info, err := os.Stat(kubeConfigFile)
	Expect(err).NotTo(HaveOccurred())
	err = copyFile(kubeConfigFile, tempConfigFile, info)
	Expect(err).NotTo(HaveOccurred())
	os.Setenv("KUBECONFIG", tempConfigFile)
	return tempConfigFile
}

// CreateRandNamespace create new project with random name in kubernetes cluster (10 letters)
func CreateRandNamespace(context string) string {
	projectName := RandString(10)
	fmt.Fprintf(GinkgoWriter, "Creating a new project: %s\n", projectName)
	CmdShouldPass("kubectl", "create", "namespace", projectName)
	CmdShouldPass("kubectl", "config", "set-context", context, "--namespace", projectName)
	session := CmdShouldPass("kubectl", "get", "namespaces")
	Expect(session).To(ContainSubstring(projectName))
	return projectName
}

// DeleteNamespace deletes a specified project in kubernetes cluster
func DeleteNamespace(projectName string) {
	fmt.Fprintf(GinkgoWriter, "Deleting project: %s\n", projectName)
	CmdShouldPass("kubectl", "delete", "namespaces", projectName)
}

// GetExistingKubeConfigPath retrieves the Kubernetes configuration from the most appropriate location
func GetExistingKubeConfigPath() string {

	// 1) If KUBECONFIG env var is specified, return that path
	kubeconfigEnv := strings.TrimSpace(os.Getenv("KUBECONFIG"))
	if len(kubeconfigEnv) != 0 {
		return kubeconfigEnv
	}

	// 2) Otherwise return the default config path
	homeDir := GetUserHomeDir()
	return filepath.Join(homeDir, ".kube", "config")

}
