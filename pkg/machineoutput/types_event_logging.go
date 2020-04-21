package machineoutput

import (
	"github.com/openshift/odo/pkg/exec"
)

type MachineEventLoggingClient interface {
	DevFileCommandExecutionBegin(commandName string, timestamp string)
	DevFileCommandExecutionComplete(commandName string, timestamp string)

	DevFileActionExecutionBegin(actionCommandString string, commandIndex int, commandName string, timestamp string)
	DevFileActionExecutionComplete(actionCommandString string, commandIndex int, commandName string, timestamp string, errorVal error)

	LogText(text string, timestamp string)
}

type DevFileCommandWrapper struct {
	DevFileCommandExecutionBegin    *DevFileCommandExecutionBegin    `json:"devFileCommandExecutionBegin,omitempty"`
	DevFileCommandExecutionComplete *DevFileCommandExecutionComplete `json:"devFileCommandExecutionComplete,omitempty"`
	DevFileActionExecutionBegin     *DevFileActionExecutionBegin     `json:"devFileActionExecutionBegin,omitempty"`
	DevFileActionExecutionComplete  *DevFileActionExecutionComplete  `json:"devFileActionExecutionComplete,omitempty"`
	LogText                         *LogText                         `json:"logText,omitempty"`
}

type DevFileCommandExecutionBegin struct {
	CommandName string `json:"commandName"`
	Timestamp   string `json:"timestamp"`
}

type DevFileCommandExecutionComplete struct {
	CommandName string `json:"commandName"`
	Timestamp   string `json:"timestamp"`
}

type DevFileActionExecutionBegin struct {
	CommandName         string `json:"commandName"`
	ActionCommandString string `json:"actionCommand"`
	Index               int    `json:"index"`
	Timestamp           string `json:"timestamp"`
}

type DevFileActionExecutionComplete struct {
	CommandName         string `json:"commandName"`
	ActionCommandString string `json:"actionCommand"`
	Index               int    `json:"index"`
	Timestamp           string `json:"timestamp"`
	Error               string `json:"error,omitempty"`
}

type LogText struct {
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
}

// Ensure receiver is interface compatible
var _ exec.ContainerOutputReceiver = &MachineEventContainerOutputReceiver{}

type MachineEventContainerOutputReceiver struct {
	client *MachineEventLoggingClient
}

// Ensure these clients are interface compatible
var _ MachineEventLoggingClient = &NoOpMachineEventLoggingClient{}
var _ MachineEventLoggingClient = &ConsoleMachineEventLoggingClient{}

type NoOpMachineEventLoggingClient struct {
}

type ConsoleMachineEventLoggingClient struct {
}
