package machineoutput

import (
	"fmt"
	"strconv"
	"time"

	"github.com/openshift/odo/pkg/exec"
)

func formatTime(time time.Time) string {

	result := strconv.FormatInt(time.Unix(), 10) + "." + fmt.Sprintf("%06d", time.Nanosecond()/1000)

	return result

}

// TimestampNow returns timestamp in format of (seconds since UTC Unix epoch).(microseconds time component)
func TimestampNow() string {
	return formatTime(time.Now())
}

func NewMachineEventContainerOutputReceiver(client *MachineEventLoggingClient) *MachineEventContainerOutputReceiver {

	return &MachineEventContainerOutputReceiver{
		client,
	}

}

func (c MachineEventContainerOutputReceiver) ReceiveText(text []exec.ReceivedText) {
	for _, line := range text {
		(*c.client).LogText(line.Text, formatTime(line.Timestamp))
	}
}

func NewNoOpMachineEventLoggingClient() *NoOpMachineEventLoggingClient {
	return &NoOpMachineEventLoggingClient{}
}

func (c *NoOpMachineEventLoggingClient) DevFileCommandExecutionBegin(commandName string, timestamp string) {
}
func (c *NoOpMachineEventLoggingClient) DevFileCommandExecutionComplete(commandName string, timestamp string) {
}

func (c *NoOpMachineEventLoggingClient) DevFileActionExecutionBegin(actionCommandString string, commandIndex int, commandName string, timestamp string) {
}
func (c *NoOpMachineEventLoggingClient) DevFileActionExecutionComplete(actionCommandString string, commandIndex int, commandName string, timestamp string, errorVal error) {
}

func (c *NoOpMachineEventLoggingClient) LogText(text string, timestamp string) {}

func NewConsoleMachineEventLoggingClient() *ConsoleMachineEventLoggingClient {
	return &ConsoleMachineEventLoggingClient{}
}

func (c *ConsoleMachineEventLoggingClient) DevFileCommandExecutionBegin(commandName string, timestamp string) {

	json := DevFileCommandWrapper{
		DevFileCommandExecutionBegin: &DevFileCommandExecutionBegin{
			CommandName: commandName,
			Timestamp:   timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

func (c *ConsoleMachineEventLoggingClient) DevFileCommandExecutionComplete(commandName string, timestamp string) {

	json := DevFileCommandWrapper{
		DevFileCommandExecutionComplete: &DevFileCommandExecutionComplete{
			CommandName: commandName,
			Timestamp:   timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

func (c *ConsoleMachineEventLoggingClient) DevFileActionExecutionBegin(actionCommandString string, commandIndex int, commandName string, timestamp string) {

	json := DevFileCommandWrapper{
		DevFileActionExecutionBegin: &DevFileActionExecutionBegin{
			CommandName:         commandName,
			ActionCommandString: actionCommandString,
			Index:               commandIndex,
			Timestamp:           timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

func (c *ConsoleMachineEventLoggingClient) DevFileActionExecutionComplete(actionCommandString string, commandIndex int, commandName string, timestamp string, errorVal error) {

	var errorStr string

	if errorVal != nil {
		errorStr = errorVal.Error()
	}

	json := DevFileCommandWrapper{
		DevFileActionExecutionComplete: &DevFileActionExecutionComplete{
			CommandName:         commandName,
			ActionCommandString: actionCommandString,
			Index:               commandIndex,
			Timestamp:           timestamp,
			Error:               errorStr,
		},
	}

	OutputSuccessUnindented(json)

}

func (c *ConsoleMachineEventLoggingClient) LogText(text string, timestamp string) {

	json := DevFileCommandWrapper{
		LogText: &LogText{
			Text:      text,
			Timestamp: timestamp,
		},
	}

	OutputSuccessUnindented(json)
}
