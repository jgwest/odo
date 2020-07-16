package devfile

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"

	"github.com/openshift/odo/pkg/util"
	"github.com/openshift/odo/pkg/watch"
	"github.com/openshift/odo/tests/helper"
	"github.com/openshift/odo/tests/integration/devfile/utils"
)

var _ = Describe("odo devfile watch command tests", func() {
	var namespace, context, cmpName, currentWorkingDirectory, originalKubeconfig string

	// Using program commmand according to cliRunner in devfile
	cliRunner := helper.GetCliRunner()

	// Setup up state for each test spec
	// create new project (not set as active) and new context directory for each test spec
	// This is run after every Spec (It)
	var _ = BeforeEach(func() {
		SetDefaultEventuallyTimeout(10 * time.Minute)
		context = helper.CreateNewContext()
		os.Setenv("GLOBALODOCONFIG", filepath.Join(context, "config.yaml"))
		originalKubeconfig = os.Getenv("KUBECONFIG")
		helper.LocalKubeconfigSet(context)
		namespace = cliRunner.CreateRandNamespaceProject()
		currentWorkingDirectory = helper.Getwd()
		cmpName = helper.RandString(6)
		helper.Chdir(context)

		// Set experimental mode to true
		helper.CmdShouldPass("odo", "preference", "set", "Experimental", "true")
	})

	// Clean up after the test
	// This is run after every Spec (It)
	var _ = AfterEach(func() {
		cliRunner.DeleteNamespaceProject(namespace)
		helper.Chdir(currentWorkingDirectory)
		err := os.Setenv("KUBECONFIG", originalKubeconfig)
		Expect(err).NotTo(HaveOccurred())
		helper.DeleteDir(context)
		os.Unsetenv("GLOBALODOCONFIG")
	})

	Context("when running help for watch command", func() {
		It("should display the help", func() {
			appHelp := helper.CmdShouldPass("odo", "watch", "-h")
			helper.MatchAllInOutput(appHelp, []string{"Watch for changes", "git components"})
		})
	})

	Context("when executing watch without pushing a devfile component", func() {
		It("should fail", func() {
			helper.Chdir(currentWorkingDirectory)
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, "--context", context, cmpName)
			output := helper.CmdShouldFail("odo", "watch", "--context", context)
			Expect(output).To(ContainSubstring("component does not exist. Please use `odo push` to create your component"))
		})
	})

	Context("when executing odo watch after odo push", func() {
		It("should listen for file changes", func() {
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs", "project"), context)
			helper.CopyExampleDevFile(filepath.Join("source", "devfiles", "nodejs", "devfile.yaml"), filepath.Join(context, "devfile.yaml"))

			output := helper.CmdShouldPass("odo", "push", "--project", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			watchFlag := ""
			odoV2Watch := utils.OdoV2Watch{
				CmpName:            cmpName,
				StringsToBeMatched: []string{"Executing devbuild command", "Executing devrun command"},
			}
			// odo watch and validate
			utils.OdoWatch(utils.OdoV1Watch{}, odoV2Watch, namespace, context, watchFlag, cliRunner, "kube")
		})
	})

	Context("when executing odo watch after odo push with flag commands", func() {
		It("should listen for file changes", func() {
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs", "project"), context)
			helper.CopyExampleDevFile(filepath.Join("source", "devfiles", "nodejs", "devfile.yaml"), filepath.Join(context, "devfile.yaml"))

			output := helper.CmdShouldPass("odo", "push", "--build-command", "build", "--run-command", "run", "--project", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			watchFlag := "--build-command build --run-command run"
			odoV2Watch := utils.OdoV2Watch{
				CmpName:            cmpName,
				StringsToBeMatched: []string{"Executing build command", "Executing run command"},
			}
			// odo watch and validate
			utils.OdoWatch(utils.OdoV1Watch{}, odoV2Watch, namespace, context, watchFlag, cliRunner, "kube")
		})
	})

	Context("when executing odo watch", func() {
		It("should show validation errors if the devfile is incorrect", func() {
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs", "project"), context)
			helper.CopyExampleDevFile(filepath.Join("source", "devfiles", "nodejs", "devfile.yaml"), filepath.Join(context, "devfile.yaml"))

			output := helper.CmdShouldPass("odo", "push", "--project", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			session := helper.CmdRunner("odo", "watch", "--context", context)
			defer session.Kill()

			waitForOutputToContain("Waiting for something to change", session)

			helper.ReplaceString(filepath.Join(context, "devfile.yaml"), "kind: build", "kind: run")

			waitForOutputToContain(watch.PushErrorString, session)

			session.Kill()

			Eventually(session).Should(gexec.Exit())

		})
	})

	Context("when executing odo watch", func() {
		It("should use the index information from previous push operation", func() {
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs", "project"), context)
			helper.CopyExampleDevFile(filepath.Join("source", "devfiles", "nodejs", "devfile.yaml"), filepath.Join(context, "devfile.yaml"))

			// 1) Push a generic project
			output := helper.CmdShouldPass("odo", "push", "--project", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			// 2) Change some file A
			textFilePath := filepath.Join(context, "my-file.txt")
			textOne := []byte("my name is my-file.txt")
			err := ioutil.WriteFile(textFilePath, textOne, 0644)
			Expect(err).NotTo(HaveOccurred())

			// 3) Odo watch that project
			session := helper.CmdRunner("odo", "watch", "--context", context)
			defer session.Kill()

			waitForOutputToContain("Waiting for something to change", session)

			// 4) Change some other file B
			helper.ReplaceString(filepath.Join(context, "server.js"), "App started", "App is super started")
			waitForOutputToContain("server.js", session)

			session.Kill()
			Eventually(session).Should(gexec.Exit())

			podName := cliRunner.GetRunningPodNameByComponent(cmpName, namespace)

			// File should exist, and its content should match what we initially set it to
			execResult := cliRunner.Exec(podName, namespace, "cat", "/projects/nodejs-starter/my-file.txt")
			Expect(execResult).To(ContainSubstring("my name is my-file.txt"))

		})
	})

	Context("when executing odo watch", func() {
		It("ensure that index information is updated by watch", func() {
			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs", "project"), context)
			helper.CopyExampleDevFile(filepath.Join("source", "devfiles", "nodejs", "devfile.yaml"), filepath.Join(context, "devfile.yaml"))

			// 1) Push a generic project
			output := helper.CmdShouldPass("odo", "push", "--project", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			indexAfterPush, err := util.ReadFileIndex(filepath.Join(context, ".odo", "odo-file-index.json"))
			Expect(err).ToNot(HaveOccurred())

			// 2) Odo watch that project
			session := helper.CmdRunner("odo", "watch", "--context", context)
			defer session.Kill()

			waitForOutputToContain("Waiting for something to change", session)

			// 3) Change server.js
			helper.ReplaceString(filepath.Join(context, "server.js"), "App started", "App is super started")
			waitForOutputToContain("server.js", session)

			// 4) Wait for the size values in the old and new index files to differ, indicating that watch has updated the index
			Eventually(func() bool {

				newIndexAfterPush, err := util.ReadFileIndex(filepath.Join(context, ".odo", "odo-file-index.json"))
				if err != nil {
					fmt.Fprintln(GinkgoWriter, "New index not found or could not be read", err)
					return false
				}

				beforePushValue, exists := indexAfterPush.Files["server.js"]
				if !exists {
					fmt.Fprintln(GinkgoWriter, "server.js not found in old index file")
					return false
				}

				afterPushValue, exists := newIndexAfterPush.Files["server.js"]
				if !exists {
					fmt.Fprintln(GinkgoWriter, "server.js not found in new index file")
					return false
				}

				fmt.Fprintln(GinkgoWriter, "comparing old and new file sizes", beforePushValue.Size, afterPushValue.Size)

				return beforePushValue.Size != afterPushValue.Size

			}, 180, 10).Should(Equal(true))

		})
	})

})

// Wait for the session stdout output to contain a particular string
func waitForOutputToContain(substring string, session *gexec.Session) {

	Eventually(func() string {
		contents := string(session.Out.Contents())
		return contents
	}, 180, 10).Should(ContainSubstring(substring))

}
