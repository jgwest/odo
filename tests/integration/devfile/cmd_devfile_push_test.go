package devfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openshift/odo/pkg/machineoutput"
	"github.com/openshift/odo/tests/helper"
	"github.com/openshift/odo/tests/integration/devfile/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("odo devfile push command tests", func() {
	var namespace, context, cmpName, currentWorkingDirectory, projectDirPath string
	var projectDir = "/projectDir"
	var sourcePath = "/projects/nodejs-web-app"

	// TODO: all oc commands in all devfile related test should get replaced by kubectl
	// TODO: to goal is not to use "oc"
	oc := helper.NewOcRunner("oc")

	// This is run after every Spec (It)
	var _ = BeforeEach(func() {
		SetDefaultEventuallyTimeout(10 * time.Minute)
		namespace = helper.CreateRandProject()
		context = helper.CreateNewContext()
		currentWorkingDirectory = helper.Getwd()
		projectDirPath = context + projectDir
		cmpName = helper.RandString(6)

		helper.Chdir(context)

		os.Setenv("GLOBALODOCONFIG", filepath.Join(context, "config.yaml"))

		// Devfile push requires experimental mode to be set
		helper.CmdShouldPass("odo", "preference", "set", "Experimental", "true")
	})

	// Clean up after the test
	// This is run after every Spec (It)
	var _ = AfterEach(func() {
		helper.DeleteProject(namespace)
		helper.Chdir(currentWorkingDirectory)
		helper.DeleteDir(context)
		os.Unsetenv("GLOBALODOCONFIG")
	})

	Context("Verify devfile push works", func() {

		It("should have no errors when no endpoints within the devfile, should create a service when devfile has endpoints", func() {
			helper.CmdShouldPass("git", "clone", "https://github.com/che-samples/web-nodejs-sample.git", projectDirPath)
			helper.Chdir(projectDirPath)

			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs"), projectDirPath)

			helper.RenameFile("devfile.yaml", "devfile-old.yaml")
			helper.RenameFile("devfile-no-endpoints.yaml", "devfile.yaml")
			helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)
			output := oc.GetServices(namespace)
			Expect(output).NotTo(ContainSubstring(cmpName))

			helper.RenameFile("devfile-old.yaml", "devfile.yaml")
			output = helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)

			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))
			output = oc.GetServices(namespace)
			Expect(output).To(ContainSubstring(cmpName))
		})

		It("checks that odo push works with a devfile", func() {
			helper.CmdShouldPass("git", "clone", "https://github.com/che-samples/web-nodejs-sample.git", projectDirPath)
			helper.Chdir(projectDirPath)

			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs"), projectDirPath)

			output := helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)
			Expect(output).To(ContainSubstring("Changes successfully pushed to component"))

			// update devfile and push again
			helper.ReplaceString("devfile.yaml", "name: FOO", "name: BAR")
			helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)
		})

		It("checks that odo push with -o json displays machine readable JSON event output", func() {
			helper.CmdShouldPass("git", "clone", "https://github.com/che-samples/web-nodejs-sample.git", projectDirPath)
			helper.Chdir(projectDirPath)

			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs"), projectDirPath)

			output := helper.CmdShouldPass("odo", "push", "-o", "json", "--devfile", "devfile.yaml", "--namespace", namespace)

			analyzePushConsoleOutput(output)

			// update devfile and push again
			helper.ReplaceString("devfile.yaml", "name: FOO", "name: BAR")
			output = helper.CmdShouldPass("odo", "push", "-o", "json", "--devfile", "devfile.yaml", "--namespace", namespace)
			analyzePushConsoleOutput(output)

		})

	})

	Context("When devfile push command is executed", func() {

		It("should not build when no changes are detected in the directory and build when a file change is detected", func() {
			utils.ExecPushToTestFileChanges(projectDirPath, cmpName, namespace)
		})

		It("should be able to create a file, push, delete, then push again propagating the deletions", func() {
			newFilePath := filepath.Join(projectDirPath, "foobar.txt")
			newDirPath := filepath.Join(projectDirPath, "testdir")
			utils.ExecPushWithNewFileAndDir(projectDirPath, cmpName, namespace, newFilePath, newDirPath)

			// Check to see if it's been pushed (foobar.txt abd directory testdir)
			podName := oc.GetRunningPodNameByComponent(cmpName, namespace)

			stdOut := oc.ExecListDir(podName, namespace, sourcePath)
			Expect(stdOut).To(ContainSubstring(("foobar.txt")))
			Expect(stdOut).To(ContainSubstring(("testdir")))

			// Now we delete the file and dir and push
			helper.DeleteDir(newFilePath)
			helper.DeleteDir(newDirPath)
			helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace, "-v4")

			// Then check to see if it's truly been deleted
			stdOut = oc.ExecListDir(podName, namespace, sourcePath)
			Expect(stdOut).To(Not(ContainSubstring(("foobar.txt"))))
			Expect(stdOut).To(Not(ContainSubstring(("testdir"))))
		})

		It("should delete the files from the container if its removed locally", func() {
			helper.CmdShouldPass("git", "clone", "https://github.com/che-samples/web-nodejs-sample.git", projectDirPath)
			helper.Chdir(projectDirPath)

			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs"), projectDirPath)

			helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)

			// Check to see if it's been pushed (foobar.txt abd directory testdir)
			podName := oc.GetRunningPodNameByComponent(cmpName, namespace)

			var statErr error
			oc.CheckCmdOpInRemoteDevfilePod(
				podName,
				"",
				namespace,
				[]string{"stat", "/projects/nodejs-web-app/app/app.js"},
				func(cmdOp string, err error) bool {
					statErr = err
					return true
				},
			)
			Expect(statErr).ToNot(HaveOccurred())
			Expect(os.Remove(filepath.Join(projectDirPath, "app", "app.js"))).NotTo(HaveOccurred())
			helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)

			oc.CheckCmdOpInRemoteDevfilePod(
				podName,
				"",
				namespace,
				[]string{"stat", "/projects/nodejs-web-app/app/app.js"},
				func(cmdOp string, err error) bool {
					statErr = err
					return true
				},
			)
			Expect(statErr).To(HaveOccurred())
			Expect(statErr.Error()).To(ContainSubstring("cannot stat '/projects/nodejs-web-app/app/app.js': No such file or directory"))
		})

		It("should build when no changes are detected in the directory and force flag is enabled", func() {
			utils.ExecPushWithForceFlag(projectDirPath, cmpName, namespace)
		})

		It("should execute the default devbuild and devrun commands if present", func() {
			utils.ExecDefaultDevfileCommands(projectDirPath, cmpName, namespace)

			// Check to see if it's been pushed (foobar.txt abd directory testdir)
			podName := oc.GetRunningPodNameByComponent(cmpName, namespace)

			var statErr error
			var cmdOutput string
			oc.CheckCmdOpInRemoteDevfilePod(
				podName,
				"runtime",
				namespace,
				[]string{"ps", "-ef"},
				func(cmdOp string, err error) bool {
					cmdOutput = cmdOp
					statErr = err
					return true
				},
			)
			Expect(statErr).ToNot(HaveOccurred())
			Expect(cmdOutput).To(ContainSubstring("/myproject/app.jar"))
		})

		It("should be able to handle a missing devbuild command", func() {
			utils.ExecWithMissingBuildCommand(projectDirPath, cmpName, namespace)
		})

		It("should error out on a missing devrun command", func() {
			utils.ExecWithMissingRunCommand(projectDirPath, cmpName, namespace)
		})

		It("should be able to push using the custom commands", func() {
			utils.ExecWithCustomCommand(projectDirPath, cmpName, namespace)
		})

		It("should error out on a wrong custom commands", func() {
			utils.ExecWithWrongCustomCommand(projectDirPath, cmpName, namespace)
		})

		It("should create pvc and reuse if it shares the same devfile volume name", func() {
			helper.CmdShouldPass("git", "clone", "https://github.com/che-samples/web-nodejs-sample.git", projectDirPath)
			helper.Chdir(projectDirPath)

			helper.CmdShouldPass("odo", "create", "nodejs", "--project", namespace, cmpName)

			helper.CopyExample(filepath.Join("source", "devfiles", "nodejs"), projectDirPath)
			helper.RenameFile("devfile.yaml", "devfile-old.yaml")
			helper.RenameFile("devfile-with-volumes.yaml", "devfile.yaml")

			output := helper.CmdShouldPass("odo", "push", "--devfile", "devfile.yaml", "--namespace", namespace)
			Expect(output).To(ContainSubstring("Executing devbuild command"))
			Expect(output).To(ContainSubstring("Executing devrun command"))

			// Check to see if it's been pushed (foobar.txt abd directory testdir)
			podName := oc.GetRunningPodNameByComponent(cmpName, namespace)

			var statErr error
			var cmdOutput string
			oc.CheckCmdOpInRemoteDevfilePod(
				podName,
				"runtime2",
				namespace,
				[]string{"cat", "/data/myfile.log"},
				func(cmdOp string, err error) bool {
					cmdOutput = cmdOp
					statErr = err
					return true
				},
			)
			Expect(statErr).ToNot(HaveOccurred())
			Expect(cmdOutput).To(ContainSubstring("hello"))

			oc.CheckCmdOpInRemoteDevfilePod(
				podName,
				"runtime2",
				namespace,
				[]string{"stat", "/data2"},
				func(cmdOp string, err error) bool {
					statErr = err
					return true
				},
			)
			Expect(statErr).ToNot(HaveOccurred())

			volumesMatched := false

			// check the volume name and mount paths for the containers
			volNamesAndPaths := oc.GetVolumeMountNamesandPathsFromContainer(cmpName, "runtime", namespace)
			volNamesAndPathsArr := strings.Fields(volNamesAndPaths)
			for _, volNamesAndPath := range volNamesAndPathsArr {
				volNamesAndPathArr := strings.Split(volNamesAndPath, ":")

				if strings.Contains(volNamesAndPathArr[0], "myvol") && volNamesAndPathArr[1] == "/data" {
					volumesMatched = true
				}
			}
			Expect(volumesMatched).To(Equal(true))
		})

	})

})

// Analyze the output of 'odo push -o json' for the machine readable event push test above.
func analyzePushConsoleOutput(pushConsoleOutput string) {

	lines := strings.Split(strings.Replace(pushConsoleOutput, "\r\n", "\n", -1), "\n")

	var entries []machineoutput.MachineEventLogEntry

	// Ensure that all lines can be correctly parsed into their expected JSON structure
	for _, line := range lines {

		if len(strings.TrimSpace(line)) == 0 {
			continue
		}

		fmt.Println("Processing output line: " + line)

		lineWrapper := machineoutput.MachineEventWrapper{}

		err := json.Unmarshal([]byte(line), &lineWrapper)
		Expect(err).NotTo(HaveOccurred())

		entry, err := lineWrapper.GetEntry()
		Expect(err).NotTo(HaveOccurred())

		entries = append(entries, entry)

	}

	if len(entries) < 4 {
		Fail("Expected at least 4 entries, corresponding to command/action execution.")
	}

	// Timestamps should be monotonically increasing
	mostRecentTimestamp := float64(-1)
	for _, entry := range entries {
		timestamp, err := strconv.ParseFloat(entry.GetTimestamp(), 64)
		Expect(err).NotTo(HaveOccurred())

		if timestamp < mostRecentTimestamp {
			Fail("Timestamp was not monotonically increasing " + entry.GetTimestamp() + " " + strconv.FormatFloat(mostRecentTimestamp, 'E', -1, 64))
		}

		mostRecentTimestamp = timestamp
	}

	// First look for the expected devbuild events, then look for the expected devrun events.
	expectedEventOrder := []struct {
		entryType   machineoutput.MachineEventLogEntryType
		commandName string
	}{
		// first the devbuild command (and its action) should run
		{
			machineoutput.TypeDevFileCommandExecutionBegin,
			"devbuild",
		},
		{
			machineoutput.TypeDevFileActionExecutionBegin,
			"devbuild",
		},
		{
			// at least one logged line of text
			machineoutput.TypeLogText,
			"",
		},
		{
			machineoutput.TypeDevFileActionExecutionComplete,
			"devbuild",
		},
		{
			machineoutput.TypeDevFileCommandExecutionComplete,
			"devbuild",
		},
		// next the devbuild command (and its action) should run
		{
			machineoutput.TypeDevFileCommandExecutionBegin,
			"devrun",
		},
		{
			machineoutput.TypeDevFileActionExecutionBegin,
			"devrun",
		},
		{
			// at least one logged line of text
			machineoutput.TypeLogText,
			"",
		},
		{
			machineoutput.TypeDevFileActionExecutionComplete,
			"devrun",
		},
		{
			machineoutput.TypeDevFileCommandExecutionComplete,
			"devrun",
		},
	}

	currIndex := -1
	for _, nextEventOrder := range expectedEventOrder {
		entry, newIndex := machineoutput.FindNextEntryByType(currIndex, nextEventOrder.entryType, entries)
		Expect(entry).NotTo(BeNil())
		Expect(newIndex).To(BeNumerically(">=", 0))
		Expect(newIndex).To(BeNumerically(">", currIndex)) // monotonically increasing index

		// We should see devbuild for the first set of events, then devrun
		commandName := machineoutput.GetCommandName(entry)
		Expect(commandName).To(Equal(nextEventOrder.commandName))

		currIndex = newIndex
		fmt.Printf("Index: %d\n", currIndex)
	}

}
