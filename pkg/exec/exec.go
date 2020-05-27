package exec

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"

	"k8s.io/klog"

	"github.com/openshift/odo/pkg/devfile/adapters/common"
	"github.com/openshift/odo/pkg/log"
)

type ExecClient interface {
	ExecCMDInContainer(common.ComponentInfo, []string, io.Writer, io.Writer, io.Reader, bool) error
}

// ExecuteCommand executes the given command in the pod's container
func ExecuteCommand(client ExecClient, compInfo common.ComponentInfo, command []string, show bool, consoleOutputStdout io.Writer, consoleOutputStderr io.Writer) (err error) {
	stdoutReader, stdoutWriter := io.Pipe()
	stderrReader, stderrWriter := io.Pipe()

	// This contains both stdout and stderr lines; acquire mutex when reading/writing 'cmdOutput'
	var cmdOutput string
	cmdOutputMutex := &sync.Mutex{}

	klog.V(3).Infof("Executing command %v for pod: %v in container: %v", command, compInfo.PodName, compInfo.ContainerName)

	// Read stdout and stderr, store their output in cmdOutput, and also pass output to consoleOutput Writers (if non-nil)
	startReaderGoroutine(stdoutReader, show, cmdOutputMutex, &cmdOutput, consoleOutputStdout)
	startReaderGoroutine(stderrReader, show, cmdOutputMutex, &cmdOutput, consoleOutputStderr)

	err = client.ExecCMDInContainer(compInfo, command, stdoutWriter, stderrWriter, nil, false)
	if err != nil {

		cmdOutputMutex.Lock()
		log.Errorf("\nUnable to exec command %v: \n%v", command, cmdOutput)
		cmdOutputMutex.Unlock()

		return err
	}

	return
}

// This Go routine will automatically pipe the output from the writer (passes into ExecCMDInContainer) to
// the loggers.
func startReaderGoroutine(reader io.Reader, show bool, cmdOutputMutex *sync.Mutex, cmdOutput *string, consoleOutput io.Writer) {
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()

			if log.IsDebug() || show {
				_, err := fmt.Fprintln(os.Stdout, line)
				if err != nil {
					log.Errorf("Unable to print to stdout: %s", err.Error())
				}
			}

			cmdOutputMutex.Lock()
			*cmdOutput += fmt.Sprintln(line)
			cmdOutputMutex.Unlock()

			if consoleOutput != nil {
				_, err := consoleOutput.Write([]byte(line + "\n"))
				if err != nil {
					log.Errorf("Error occurred on writing string to consoleOutput writer: %s", err.Error())
				}
			}
		}
	}()

}
