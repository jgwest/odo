package component

import (
	"time"

	"github.com/pkg/errors"

	"fmt"

	"github.com/openshift/odo/pkg/devfile/adapters"
	"github.com/openshift/odo/pkg/devfile/adapters/kubernetes"
	devfileParser "github.com/openshift/odo/pkg/devfile/parser"
	"github.com/openshift/odo/pkg/envinfo"
	"github.com/openshift/odo/pkg/odo/genericclioptions"

	"github.com/openshift/odo/pkg/odo/util/completion"
	"github.com/openshift/odo/pkg/odo/util/experimental"
	"github.com/openshift/odo/pkg/odo/util/pushtarget"
	ktemplates "k8s.io/kubernetes/pkg/kubectl/util/templates"

	odoutil "github.com/openshift/odo/pkg/odo/util"

	"github.com/spf13/cobra"
)

// StatusRecommendedCommandName is the recommended watch command name
const StatusRecommendedCommandName = "status"

var statusExample = ktemplates.Examples(`  # Get the status for the nodejs component
%[1]s nodejs
`)

// StatusOptions contains log options
type StatusOptions struct {
	componentContext string

	DevfilePath string
	namespace   string

	*ComponentOptions
	EnvSpecificInfo *envinfo.EnvSpecificInfo
}

// NewStatusOptions returns new instance of StatusOptions
func NewStatusOptions() *StatusOptions {
	return &StatusOptions{"", "", "", &ComponentOptions{}, nil}
}

// Complete completes status args
func (so *StatusOptions) Complete(name string, cmd *cobra.Command, args []string) (err error) {
	// so.devfilePath = filepath.Join(do.componentContext, do.devfilePath)

	if !experimental.IsExperimentalModeEnabled() {
		return errors.New("the status command is only supported in experimental mode")
	}

	// if experimental mode is enabled and devfile is present
	envinfo, err := envinfo.NewEnvSpecificInfo(so.componentContext)
	if err != nil {
		return errors.Wrap(err, "unable to retrieve configuration information")
	}
	so.EnvSpecificInfo = envinfo
	so.Context = genericclioptions.NewDevfileContext(cmd)
	return nil

	// so.EnvSpecificInfo, err = envinfo.NewEnvSpecificInfo(so.componentContext)
	// if err != nil {
	// 	return err
	// }

	// return nil

}

// Validate validates the status parameters
func (so *StatusOptions) Validate() (err error) {
	return
}

// Run has the logic to perform the required actions as part of command
func (so *StatusOptions) Run() (err error) {

	// Parse devfile
	devObj, err := devfileParser.Parse(so.DevfilePath)
	if err != nil {
		return err
	}

	componentName, err := getComponentName()
	if err != nil {
		return errors.Wrap(err, "unable to get component name")
	}

	var platformContext interface{}
	if pushtarget.IsPushTargetDocker() {
		platformContext = nil
	} else {
		kc := kubernetes.KubernetesContext{
			Namespace: so.namespace,
		}
		platformContext = kc
	}

	devfileHandler, err := adapters.NewPlatformAdapter(componentName, devObj, platformContext)

	devfileHandler.StartURLHttpRequestStatusWatch()

	// devfileHandler.StartSupervisordCtlStatusWatch()

	// devfileHandler.StartContainerStatusWatch()
	// componentKube.StartStatusTimer(devfileHandler.)

	for {

		time.Sleep(2 * time.Second)

	}

	// statusReturn, err := devfileHandler.GetContainerStatus()
	// if err != nil {
	// 	fmt.Println(err)
	// }

	// if statusReturn.GetType() == common.ContainerStatusKubernetes {

	// 	// var val component.KubernetesContainerStatus = statusReturn.(component.KubernetesContainerStatus)

	// 	// val.Pods

	// } else {

	// 	return nil
	// }

	return nil
}

// NewCmdStatus implements the status odo command
func NewCmdStatus(name, fullName string) *cobra.Command {
	o := NewStatusOptions()

	annotations := map[string]string{"command": "component"}

	if experimental.IsExperimentalModeEnabled() {
		annotations["machineoutput"] = "json"
	}

	var statusCmd = &cobra.Command{
		Use:         fmt.Sprintf("%s [component_name]", name),
		Short:       "Retrieve the status for the given component",
		Long:        `Retrieve the status for the given component`,
		Example:     fmt.Sprintf(statusExample, fullName),
		Args:        cobra.RangeArgs(0, 1), // TODO: JGW: ???
		Annotations: annotations,
		Run: func(cmd *cobra.Command, args []string) {
			genericclioptions.GenericRun(o, cmd, args)
		},
	}

	statusCmd.Flags().StringVar(&o.DevfilePath, "devfile", "./devfile.yaml", "Path to a devfile.yaml")
	statusCmd.Flags().StringVar(&o.namespace, "namespace", "", "Namespace to push the component to")

	statusCmd.SetUsageTemplate(odoutil.CmdUsageTemplate)
	completion.RegisterCommandHandler(statusCmd, completion.ComponentNameCompletionHandler)

	// // Adding `--context` flag
	// genericclioptions.AddContextFlag(statusCmd, &o.componentContext)

	// //Adding `--project` flag
	// projectCmd.AddProjectFlag(statusCmd)

	// //Adding `--application` flag
	// appCmd.AddApplicationFlag(statusCmd)

	return statusCmd
}
