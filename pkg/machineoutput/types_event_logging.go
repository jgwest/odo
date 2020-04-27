package machineoutput

import (
	"github.com/openshift/odo/pkg/exec"
)

// MachineEventLoggingClient is an interface which is used by consuming code to
// output machine-readable JSON to the console. Implementations of this interface
// exist later in the file.
type MachineEventLoggingClient interface {
	DevFileCommandExecutionBegin(commandName string, timestamp string)
	DevFileCommandExecutionComplete(commandName string, timestamp string)

	DevFileActionExecutionBegin(actionCommandString string, commandIndex int, commandName string, timestamp string)
	DevFileActionExecutionComplete(actionCommandString string, commandIndex int, commandName string, timestamp string, errorVal error)

	LogText(text string, timestamp string)
}

// MachineEventWrapper - a single line of machine-readable event console output must contain only one
// of these commands; the MachineEventWrapper is used to create (and parse) these lines.
type MachineEventWrapper struct {
	DevFileCommandExecutionBegin    *DevFileCommandExecutionBegin    `json:"devFileCommandExecutionBegin,omitempty"`
	DevFileCommandExecutionComplete *DevFileCommandExecutionComplete `json:"devFileCommandExecutionComplete,omitempty"`
	DevFileActionExecutionBegin     *DevFileActionExecutionBegin     `json:"devFileActionExecutionBegin,omitempty"`
	DevFileActionExecutionComplete  *DevFileActionExecutionComplete  `json:"devFileActionExecutionComplete,omitempty"`
	LogText                         *LogText                         `json:"logText,omitempty"`
}

// DevFileCommandExecutionBegin is the JSON event that is emitted when a dev file command begins execution.
type DevFileCommandExecutionBegin struct {
	CommandName string `json:"commandName"`
	Timestamp   string `json:"timestamp"`
}

// DevFileCommandExecutionComplete is the JSON event that is emitted when a dev file command completes execution.
type DevFileCommandExecutionComplete struct {
	CommandName string `json:"commandName"`
	Timestamp   string `json:"timestamp"`
}

// DevFileActionExecutionBegin is the JSON event that is emitted when a dev file action begins execution.
type DevFileActionExecutionBegin struct {
	CommandName         string `json:"commandName"`
	ActionCommandString string `json:"actionCommand"`
	Index               int    `json:"index"`
	Timestamp           string `json:"timestamp"`
}

// DevFileActionExecutionComplete is the JSON event that is emitted when a dev file action completes execution.
type DevFileActionExecutionComplete struct {
	CommandName         string `json:"commandName"`
	ActionCommandString string `json:"actionCommand"`
	Index               int    `json:"index"`
	Timestamp           string `json:"timestamp"`
	Error               string `json:"error,omitempty"`
}

// LogText is the JSON event that is emitted when a dev file action outputs text to the console.
type LogText struct {
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// Ensure the various events correctly implement the desired interface.
var _ MachineEventLogEntry = &DevFileCommandExecutionBegin{}
var _ MachineEventLogEntry = &DevFileCommandExecutionComplete{}
var _ MachineEventLogEntry = &DevFileActionExecutionBegin{}
var _ MachineEventLogEntry = &DevFileActionExecutionComplete{}
var _ MachineEventLogEntry = &LogText{}

// MachineEventLogEntry contains the expected methods for every event that is emitted.
// (This is mainly used for test purposes.)
type MachineEventLogEntry interface {
	GetTimestamp() string
	GetType() MachineEventLogEntryType
}

// Ensure receiver is interface compatible
var _ exec.ContainerOutputReceiver = &MachineEventContainerOutputReceiver{}

// MachineEventContainerOutputReceiver receives console output from exec command, and passes it to the
// previously specified logging client.
type MachineEventContainerOutputReceiver struct {
	client *MachineEventLoggingClient
}

// Ensure these clients are interface compatible
var _ MachineEventLoggingClient = &NoOpMachineEventLoggingClient{}
var _ MachineEventLoggingClient = &ConsoleMachineEventLoggingClient{}

// NoOpMachineEventLoggingClient will ignore (eg not output) all events passed to it
type NoOpMachineEventLoggingClient struct {
}

// ConsoleMachineEventLoggingClient will output all events to the console as JSON
type ConsoleMachineEventLoggingClient struct {
}
