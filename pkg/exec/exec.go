package exec

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/golang/glog"

	"github.com/openshift/odo/pkg/devfile/adapters/common"
	"github.com/openshift/odo/pkg/log"
)

type ExecClient interface {
	ExecCMDInContainer(common.ComponentInfo, []string, io.Writer, io.Writer, io.Reader, bool) error
}

// ExecuteCommand executes the given command in the pod's container
func ExecuteCommand(client ExecClient, compInfo common.ComponentInfo, command []string, show bool, receiver ContainerOutputReceiver) (err error) {
	reader, writer := io.Pipe()

	var cmdOutput string
	cmdOutputMutex := &sync.Mutex{}

	var textChannel *(chan string) = nil
	if receiver != nil {
		textChannel = createTextReceiverChannel(receiver)
	}

	glog.V(3).Infof("Executing command %v for pod: %v in container: %v", command, compInfo.PodName, compInfo.ContainerName)

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

			if textChannel != nil {
				*textChannel <- line
			}
		}
	}()

	err = client.ExecCMDInContainer(compInfo, command, writer, writer, nil, false)
	if err != nil {

		cmdOutputMutex.Lock()
		log.Errorf("\nUnable to exec command %v: \n%v", command, cmdOutput)
		cmdOutputMutex.Unlock()

		return err
	}

	return
}

type ContainerOutputReceiver interface {
	ReceiveText(text []ReceivedText)
}

func createTextReceiverChannel(receiver ContainerOutputReceiver) *(chan string) {
	if receiver == nil {
		return nil
	}

	mutex := &sync.Mutex{}
	result := make(chan string)

	// lock on mutex when accessing this
	receivedTextArr := []ReceivedText{}

	interFuncChan := make(chan interface{})

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

			mutex.Lock()
			localReceivedTextArr := receivedTextArr
			receivedTextArr = []ReceivedText{}
			mutex.Unlock()

			if len(localReceivedTextArr) > 0 {
				// Pass the received text to the requester
				receiver.ReceiveText(localReceivedTextArr)
			}
		}
	}()

	// This goroutine reads text from 'result' channel, and appends it to the receivedText array; this
	// function is designed never to block the channel writer.
	go func() {
		for {

			text := <-result

			var receivedText = ReceivedText{text, time.Now()}
			mutex.Lock()
			receivedTextArr = append(receivedTextArr, receivedText)
			mutex.Unlock()

			// Inform this function's other goroutine that a new line of text is available.
			go func() { interFuncChan <- nil }()

		}
	}()

	return &result
}

type ReceivedText struct {
	Text      string
	Timestamp time.Time
}
