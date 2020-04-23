package machineoutput

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/openshift/odo/pkg/exec"
)

// formatTime returns time in UTC Unix Epoch Seconds and then the microsecond portion of that time.
func formatTime(time time.Time) string {
	result := strconv.FormatInt(time.Unix(), 10) + "." + fmt.Sprintf("%06d", time.Nanosecond()/1000)
	return result

}

// TimestampNow returns timestamp in format of (seconds since UTC Unix epoch).(microseconds time component)
func TimestampNow() string {
	return formatTime(time.Now())
}

// NewMachineEventContainerOutputReceiver creates a new instance of MachineEventContainerOutputReceiver
func NewMachineEventContainerOutputReceiver(client *MachineEventLoggingClient) *MachineEventContainerOutputReceiver {

	return &MachineEventContainerOutputReceiver{
		client,
	}

}

// ReceiveText will receive container console output and pass it to the contained client.
func (c MachineEventContainerOutputReceiver) ReceiveText(text []exec.TimestampedText) {
	for _, line := range text {
		(*c.client).LogText(line.Text, formatTime(line.Timestamp))
	}
}

// NewNoOpMachineEventLoggingClient creates a new instance of NoOpMachineEventLoggingClient,
// which will ignore any provided events.
func NewNoOpMachineEventLoggingClient() *NoOpMachineEventLoggingClient {
	return &NoOpMachineEventLoggingClient{}
}

// DevFileCommandExecutionBegin ignores the provided event.
func (c *NoOpMachineEventLoggingClient) DevFileCommandExecutionBegin(commandName string, timestamp string) {
}

// DevFileCommandExecutionComplete ignores the provided event.
func (c *NoOpMachineEventLoggingClient) DevFileCommandExecutionComplete(commandName string, timestamp string) {
}

// DevFileActionExecutionBegin ignores the provided event.
func (c *NoOpMachineEventLoggingClient) DevFileActionExecutionBegin(actionCommandString string, commandIndex int, commandName string, timestamp string) {
}

// DevFileActionExecutionComplete ignores the provided event.
func (c *NoOpMachineEventLoggingClient) DevFileActionExecutionComplete(actionCommandString string, commandIndex int, commandName string, timestamp string, errorVal error) {
}

// LogText ignores the provided event.
func (c *NoOpMachineEventLoggingClient) LogText(text string, timestamp string) {}

// NewConsoleMachineEventLoggingClient creates a new instance of ConsoleMachineEventLoggingClient,
// which will output event as JSON to the console.
func NewConsoleMachineEventLoggingClient() *ConsoleMachineEventLoggingClient {
	return &ConsoleMachineEventLoggingClient{}
}

// DevFileCommandExecutionBegin outputs the provided event as JSON to the console.
func (c *ConsoleMachineEventLoggingClient) DevFileCommandExecutionBegin(commandName string, timestamp string) {

	json := DevFileCommandWrapper{
		DevFileCommandExecutionBegin: &DevFileCommandExecutionBegin{
			CommandName: commandName,
			Timestamp:   timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

// DevFileCommandExecutionComplete outputs the provided event as JSON to the console.
func (c *ConsoleMachineEventLoggingClient) DevFileCommandExecutionComplete(commandName string, timestamp string) {

	json := DevFileCommandWrapper{
		DevFileCommandExecutionComplete: &DevFileCommandExecutionComplete{
			CommandName: commandName,
			Timestamp:   timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

// DevFileActionExecutionBegin outputs the provided event as JSON to the console.
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

// DevFileActionExecutionComplete outputs the provided event as JSON to the console.
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

// LogText outputs the provided event as JSON to the console.
func (c *ConsoleMachineEventLoggingClient) LogText(text string, timestamp string) {

	json := DevFileCommandWrapper{
		LogText: &LogText{
			Text:      text,
			Timestamp: timestamp,
		},
	}

	OutputSuccessUnindented(json)
}

// GetEntry will return the JSON event parsed from a single line of '-o json' machine readable console output.
func (w DevFileCommandWrapper) GetEntry() (MachineEventLogEntry, error) {

	if w.DevFileActionExecutionBegin != nil {
		return w.DevFileActionExecutionBegin, nil

	} else if w.DevFileActionExecutionComplete != nil {
		return w.DevFileActionExecutionComplete, nil

	} else if w.DevFileCommandExecutionBegin != nil {
		return w.DevFileCommandExecutionBegin, nil

	} else if w.DevFileCommandExecutionComplete != nil {
		return w.DevFileCommandExecutionComplete, nil

	} else if w.LogText != nil {
		return w.LogText, nil

	} else {
		return nil, errors.New("unexpected machine event log entry")
	}

}

// GetTimestamp returns the timestamp element for this event.
func (c DevFileCommandExecutionBegin) GetTimestamp() string { return c.Timestamp }

// GetTimestamp returns the timestamp element for this event.
func (c DevFileCommandExecutionComplete) GetTimestamp() string { return c.Timestamp }

// GetTimestamp returns the timestamp element for this event.
func (c DevFileActionExecutionBegin) GetTimestamp() string { return c.Timestamp }

// GetTimestamp returns the timestamp element for this event.
func (c DevFileActionExecutionComplete) GetTimestamp() string { return c.Timestamp }

// GetTimestamp returns the timestamp element for this event.
func (c LogText) GetTimestamp() string { return c.Timestamp }

// GetType returns the event type for this event.
func (c DevFileCommandExecutionBegin) GetType() MachineEventLogEntryType {
	return TypeDevFileCommandExecutionBegin
}

// GetType returns the event type for this event.
func (c DevFileCommandExecutionComplete) GetType() MachineEventLogEntryType {
	return TypeDevFileCommandExecutionComplete
}

// GetType returns the event type for this event.
func (c DevFileActionExecutionBegin) GetType() MachineEventLogEntryType {
	return TypeDevFileActionExecutionBegin
}

// GetType returns the event type for this event.
func (c DevFileActionExecutionComplete) GetType() MachineEventLogEntryType {
	return TypeDevFileActionExecutionComplete
}

// GetType returns the event type for this event.
func (c LogText) GetType() MachineEventLogEntryType { return TypeLogText }

// MachineEventLogEntryType indicates the machine-readable event type from an ODO operation
type MachineEventLogEntryType int

const (
	// TypeDevFileCommandExecutionBegin is the entry type for that event.
	TypeDevFileCommandExecutionBegin MachineEventLogEntryType = 0
	// TypeDevFileCommandExecutionComplete is the entry type for that event.
	TypeDevFileCommandExecutionComplete MachineEventLogEntryType = 1
	// TypeDevFileActionExecutionBegin is the entry type for that event.
	TypeDevFileActionExecutionBegin MachineEventLogEntryType = 2
	// TypeDevFileActionExecutionComplete is the entry type for that event.
	TypeDevFileActionExecutionComplete MachineEventLogEntryType = 3
	// TypeLogText is the entry type for that event.
	TypeLogText MachineEventLogEntryType = 4
)

// GetCommandName returns a command if the MLE supports that field (otherwise empty string is returned).
// Currently used for test purposes only.
func GetCommandName(entry MachineEventLogEntry) string {

	if entry.GetType() == TypeDevFileActionExecutionBegin {
		return entry.(*DevFileActionExecutionBegin).CommandName
	} else if entry.GetType() == TypeDevFileActionExecutionComplete {
		return entry.(*DevFileActionExecutionComplete).CommandName
	} else if entry.GetType() == TypeDevFileCommandExecutionBegin {
		return entry.(*DevFileCommandExecutionBegin).CommandName
	} else if entry.GetType() == TypeDevFileCommandExecutionComplete {
		return entry.(*DevFileCommandExecutionComplete).CommandName
	} else {
		return ""
	}

}

// FindNextEntryByType locates the next entry of a given type within a slice. Currently used for test purposes only.
func FindNextEntryByType(initialIndex int, typeToFind MachineEventLogEntryType, entries []MachineEventLogEntry) (MachineEventLogEntry, int) {

	for index, entry := range entries {
		if index < initialIndex {
			continue
		}

		if entry.GetType() == typeToFind {
			return entry, index
		}
	}

	return nil, -1

}
