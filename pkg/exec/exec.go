package exec

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/golang/glog"

	"github.com/openshift/odo/pkg/log"
)

type ExecClient interface {
	ExecCMDInContainer(string, string, []string, io.Writer, io.Writer, io.Reader, bool) error
}

// ExecuteCommand executes the given command in the pod's container
func ExecuteCommand(client ExecClient, podName, containerName string, command []string, show bool, receiver ContainerOutputReceiver) (err error) {
	reader, writer := io.Pipe()

	var cmdOutput string
	cmdOutputMutex := &sync.Mutex{}

	var textChannel *(chan string) = nil
	if receiver != nil {
		textChannel = createTextReceiverChannel(receiver)
	}

	glog.V(4).Infof("Executing command %v for pod: %v in container: %v", command, podName, containerName)

	// This Go routine will automatically pipe the output from ExecCMDInContainer to
	// our logger.
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			line := scanner.Text()

			if log.IsDebug() || show {
				_, err := fmt.Fprintln(os.Stdout, line)
				if err != nil {
					log.Errorf("Unable to print to stdout: %v", err)
				}
			}

			cmdOutputMutex.Lock()
			cmdOutput += fmt.Sprintln(line)
			cmdOutputMutex.Unlock()

			// If a container output receiver was passed to this method, then pass text to it via channel.
			// The goroutine at the other end of this channel is designed never to block the channel writer.
			if textChannel != nil {
				*textChannel <- line
			}
		}
	}()

	err = client.ExecCMDInContainer(podName, containerName, command, writer, writer, nil, false)
	if err != nil {

		cmdOutputMutex.Lock()
		log.Errorf("\nUnable to exec command %v: \n%v", command, cmdOutput)
		cmdOutputMutex.Unlock()

		return err
	}

	return
}

// ContainerOutputReceiver defines the interface for receiving console output from containers executed via exec command.
type ContainerOutputReceiver interface {
	ReceiveText(text []TimestampedText)
}

// createTextReceiverChannel will, if a receiver is defined, create channels (and goroutines) that allow
// the returned channel to never block on received text. Text passed to the returned channel will be
// provided to the ContainerOutputReceiver.
func createTextReceiverChannel(receiver ContainerOutputReceiver) *(chan string) {
	if receiver == nil {
		return nil
	}

	mutex := &sync.Mutex{}
	result := make(chan string)

	// lock on mutex when accessing this
	receivedTextArr := []TimestampedText{}

	interFuncChan := make(chan interface{})

	// The goroutine waits for requests on interFuncChan, and on receiving one, it then removes the text
	// from receiveTextArr and passes that text to the receiver.
	go func() {
		for {

			// Wait for at least one request
			<-interFuncChan

			// Drain the channel of any other requests that occurred during this time.
			channelEmpty := false
			for !channelEmpty {
				select {
				case <-interFuncChan:
				default:
					channelEmpty = true
				}
			}

			// Retrieve the stored text and clear the reference back to empty
			mutex.Lock()
			localReceivedTextArr := receivedTextArr
			receivedTextArr = []TimestampedText{}
			mutex.Unlock()

			if len(localReceivedTextArr) > 0 {
				// Pass the received text to the defined receiver
				receiver.ReceiveText(localReceivedTextArr)
			}
		}
	}()

	// This goroutine reads text from 'result' channel, and appends it to the receivedText array; this
	// function is designed never to block the channel writer.
	go func() {
		for {

			text := <-result

			var receivedText = TimestampedText{text, time.Now()}
			mutex.Lock()
			receivedTextArr = append(receivedTextArr, receivedText)
			mutex.Unlock()

			// Inform this function's other goroutine that a new line of text is available.
			go func() { interFuncChan <- nil }()

		}
	}()

	return &result
}

// TimestampedText contains a string received from the container output of an exec command
type TimestampedText struct {
	Text      string
	Timestamp time.Time
}
