package component

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	gosync "sync"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/fatih/color"
	"github.com/golang/glog"
	"github.com/pkg/errors"

	"github.com/openshift/odo/pkg/component"
	"github.com/openshift/odo/pkg/config"
	"github.com/openshift/odo/pkg/devfile/adapters/common"
	adaptersCommon "github.com/openshift/odo/pkg/devfile/adapters/common"
	"github.com/openshift/odo/pkg/devfile/adapters/kubernetes/storage"
	"github.com/openshift/odo/pkg/devfile/adapters/kubernetes/utils"
	versionsCommon "github.com/openshift/odo/pkg/devfile/parser/data/common"
	"github.com/openshift/odo/pkg/exec"
	"github.com/openshift/odo/pkg/kclient"
	"github.com/openshift/odo/pkg/log"
	"github.com/openshift/odo/pkg/machineoutput"
	odoutil "github.com/openshift/odo/pkg/odo/util"
	"github.com/openshift/odo/pkg/sync"
	"github.com/openshift/odo/pkg/url"
)

// New instantiantes a component adapter
func New(adapterContext common.AdapterContext, client kclient.Client) Adapter {

	var loggingClient machineoutput.MachineEventLoggingClient

	if log.IsJSON() {
		loggingClient = machineoutput.NewConsoleMachineEventLoggingClient()
	} else {
		loggingClient = machineoutput.NewNoOpMachineEventLoggingClient()
	}

	return Adapter{
		Client:             client,
		AdapterContext:     adapterContext,
		machineEventLogger: loggingClient,
	}
}

// Adapter is a component adapter implementation for Kubernetes
type Adapter struct {
	Client kclient.Client
	common.AdapterContext
	devfileBuildCmd    string
	devfileRunCmd      string
	machineEventLogger machineoutput.MachineEventLoggingClient
}

// Push updates the component if a matching component exists or creates one if it doesn't exist
// Once the component has started, it will sync the source code to it.
func (a Adapter) Push(parameters common.PushParameters) (err error) {
	componentExists := utils.ComponentExists(a.Client, a.ComponentName)

	a.devfileBuildCmd = parameters.DevfileBuildCmd
	a.devfileRunCmd = parameters.DevfileRunCmd

	podChanged := false
	var podName string

	// If the component already exists, retrieve the pod's name before it's potentially updated
	if componentExists {
		pod, err := a.waitAndGetComponentPod(true)
		if err != nil {
			return errors.Wrapf(err, "unable to get pod for component %s", a.ComponentName)
		}
		podName = pod.GetName()
	}

	// Validate the devfile build and run commands
	log.Info("\nValidation")
	s := log.Spinner("Validating the devfile")
	pushDevfileCommands, err := common.ValidateAndGetPushDevfileCommands(a.Devfile.Data, a.devfileBuildCmd, a.devfileRunCmd)
	if err != nil {
		s.End(false)
		return errors.Wrap(err, "failed to validate devfile build and run commands")
	}
	s.End(true)

	log.Infof("\nCreating Kubernetes resources for component %s", a.ComponentName)
	err = a.createOrUpdateComponent(componentExists)
	if err != nil {
		return errors.Wrap(err, "unable to create or update component")
	}

	_, err = a.Client.WaitForDeploymentRollout(a.ComponentName)
	if err != nil {
		return errors.Wrap(err, "error while waiting for deployment rollout")
	}

	// Wait for Pod to be in running state otherwise we can't sync data or exec commands to it.
	pod, err := a.waitAndGetComponentPod(true)
	if err != nil {
		return errors.Wrapf(err, "unable to get pod for component %s", a.ComponentName)
	}

	err = component.ApplyConfig(nil, &a.Client, config.LocalConfigInfo{}, parameters.EnvSpecificInfo, color.Output, componentExists)
	if err != nil {
		odoutil.LogErrorAndExit(err, "Failed to update config to component deployed.")
	}

	// Compare the name of the pod with the one before the rollout. If they differ, it means there's a new pod and a force push is required
	if componentExists && podName != pod.GetName() {
		podChanged = true
	}

	// Find at least one pod with the source volume mounted, error out if none can be found
	containerName, err := getFirstContainerWithSourceVolume(pod.Spec.Containers)
	if err != nil {
		return errors.Wrapf(err, "error while retrieving container from pod %s with a mounted project volume", podName)
	}

	log.Infof("\nSyncing to component %s", a.ComponentName)
	// Get a sync adapter. Check if project files have changed and sync accordingly
	syncAdapter := sync.New(a.AdapterContext, &a.Client)
	compInfo := common.ComponentInfo{
		ContainerName: containerName,
		PodName:       pod.GetName(),
	}
	syncParams := adaptersCommon.SyncParameters{
		PushParams:      parameters,
		CompInfo:        compInfo,
		ComponentExists: componentExists,
		PodChanged:      podChanged,
	}
	execRequired, err := syncAdapter.SyncFiles(syncParams)
	if err != nil {
		return errors.Wrapf(err, "Failed to sync to component with name %s", a.ComponentName)
	}

	if execRequired {
		log.Infof("\nExecuting devfile commands for component %s", a.ComponentName)
		err = a.execDevfile(pushDevfileCommands, componentExists, parameters.Show, pod.GetName(), pod.Spec.Containers)
		if err != nil {
			return err
		}
	}

	return nil
}

// DoesComponentExist returns true if a component with the specified name exists, false otherwise
func (a Adapter) DoesComponentExist(cmpName string) bool {
	return utils.ComponentExists(a.Client, cmpName)
}

func (a Adapter) createOrUpdateComponent(componentExists bool) (err error) {
	componentName := a.ComponentName

	labels := map[string]string{
		"component": componentName,
	}

	containers, err := utils.GetContainers(a.Devfile)
	if err != nil {
		return err
	}

	if len(containers) == 0 {
		return fmt.Errorf("No valid components found in the devfile")
	}

	containers, err = utils.UpdateContainersWithSupervisord(a.Devfile, containers, a.devfileRunCmd)
	if err != nil {
		return err
	}

	objectMeta := kclient.CreateObjectMeta(componentName, a.Client.Namespace, labels, nil)
	podTemplateSpec := kclient.GeneratePodTemplateSpec(objectMeta, containers)

	kclient.AddBootstrapSupervisordInitContainer(podTemplateSpec)

	componentAliasToVolumes := adaptersCommon.GetVolumes(a.Devfile)

	var uniqueStorages []common.Storage
	volumeNameToPVCName := make(map[string]string)
	processedVolumes := make(map[string]bool)

	// Get a list of all the unique volume names and generate their PVC names
	for _, volumes := range componentAliasToVolumes {
		for _, vol := range volumes {
			if _, ok := processedVolumes[*vol.Name]; !ok {
				processedVolumes[*vol.Name] = true

				// Generate the PVC Names
				glog.V(3).Infof("Generating PVC name for %v", *vol.Name)
				generatedPVCName, err := storage.GeneratePVCNameFromDevfileVol(*vol.Name, componentName)
				if err != nil {
					return err
				}

				// Check if we have an existing PVC with the labels, overwrite the generated name with the existing name if present
				existingPVCName, err := storage.GetExistingPVC(&a.Client, *vol.Name, componentName)
				if err != nil {
					return err
				}
				if len(existingPVCName) > 0 {
					glog.V(3).Infof("Found an existing PVC for %v, PVC %v will be re-used", *vol.Name, existingPVCName)
					generatedPVCName = existingPVCName
				}

				pvc := common.Storage{
					Name:   generatedPVCName,
					Volume: vol,
				}
				uniqueStorages = append(uniqueStorages, pvc)
				volumeNameToPVCName[*vol.Name] = generatedPVCName
			}
		}
	}

	// Add PVC and Volume Mounts to the podTemplateSpec
	err = kclient.AddPVCAndVolumeMount(podTemplateSpec, volumeNameToPVCName, componentAliasToVolumes)
	if err != nil {
		return err
	}

	deploymentSpec := kclient.GenerateDeploymentSpec(*podTemplateSpec)
	var containerPorts []corev1.ContainerPort
	for _, c := range deploymentSpec.Template.Spec.Containers {
		if len(containerPorts) == 0 {
			containerPorts = c.Ports
		} else {
			containerPorts = append(containerPorts, c.Ports...)
		}
	}
	serviceSpec := kclient.GenerateServiceSpec(objectMeta.Name, containerPorts)
	glog.V(3).Infof("Creating deployment %v", deploymentSpec.Template.GetName())
	glog.V(3).Infof("The component name is %v", componentName)

	if utils.ComponentExists(a.Client, componentName) {
		// If the component already exists, get the resource version of the deploy before updating
		glog.V(3).Info("The component already exists, attempting to update it")
		deployment, err := a.Client.UpdateDeployment(*deploymentSpec)
		if err != nil {
			return err
		}
		glog.V(3).Infof("Successfully updated component %v", componentName)
		oldSvc, err := a.Client.KubeClient.CoreV1().Services(a.Client.Namespace).Get(componentName, metav1.GetOptions{})
		objectMetaTemp := objectMeta
		ownerReference := kclient.GenerateOwnerReference(deployment)
		objectMetaTemp.OwnerReferences = append(objectMeta.OwnerReferences, ownerReference)
		if err != nil {
			// no old service was found, create a new one
			if len(serviceSpec.Ports) > 0 {
				_, err = a.Client.CreateService(objectMetaTemp, *serviceSpec)
				if err != nil {
					return err
				}
				glog.V(3).Infof("Successfully created Service for component %s", componentName)
			}
		} else {
			if len(serviceSpec.Ports) > 0 {
				serviceSpec.ClusterIP = oldSvc.Spec.ClusterIP
				objectMetaTemp.ResourceVersion = oldSvc.GetResourceVersion()
				_, err = a.Client.UpdateService(objectMetaTemp, *serviceSpec)
				if err != nil {
					return err
				}
				glog.V(3).Infof("Successfully update Service for component %s", componentName)
			} else {
				err = a.Client.KubeClient.CoreV1().Services(a.Client.Namespace).Delete(componentName, &metav1.DeleteOptions{})
				if err != nil {
					return err
				}
			}
		}
	} else {
		deployment, err := a.Client.CreateDeployment(*deploymentSpec)
		if err != nil {
			return err
		}
		glog.V(3).Infof("Successfully created component %v", componentName)
		ownerReference := kclient.GenerateOwnerReference(deployment)
		objectMetaTemp := objectMeta
		objectMetaTemp.OwnerReferences = append(objectMeta.OwnerReferences, ownerReference)
		if len(serviceSpec.Ports) > 0 {
			_, err = a.Client.CreateService(objectMetaTemp, *serviceSpec)
			if err != nil {
				return err
			}
			glog.V(3).Infof("Successfully created Service for component %s", componentName)
		}

	}

	// Get the storage adapter and create the volumes if it does not exist
	stoAdapter := storage.New(a.AdapterContext, a.Client)
	err = stoAdapter.Create(uniqueStorages)
	if err != nil {
		return err
	}

	return nil
}

func (a Adapter) waitAndGetComponentPod(hideSpinner bool) (*corev1.Pod, error) {
	podSelector := fmt.Sprintf("component=%s", a.ComponentName)
	watchOptions := metav1.ListOptions{
		LabelSelector: podSelector,
	}
	// Wait for Pod to be in running state otherwise we can't sync data to it.
	pod, err := a.Client.WaitAndGetPod(watchOptions, corev1.PodRunning, "Waiting for component to start", hideSpinner)
	if err != nil {
		return nil, errors.Wrapf(err, "error while waiting for pod %s", podSelector)
	}
	return pod, nil
}

// Push syncs source code from the user's disk to the component
func (a Adapter) execDevfile(pushDevfileCommands []versionsCommon.DevfileCommand, componentExists, show bool, podName string, containers []corev1.Container) (err error) {
	buildRequired, err := common.IsComponentBuildRequired(pushDevfileCommands)
	if err != nil {
		return err
	}

	outputReceiver := machineoutput.NewMachineEventContainerOutputReceiver(&a.machineEventLogger)

	for i := 0; i < len(pushDevfileCommands); i++ {
		command := pushDevfileCommands[i]

		// Exec the devBuild command if buildRequired is true
		if (command.Name == string(common.DefaultDevfileBuildCommand) || command.Name == a.devfileBuildCmd) && buildRequired {
			glog.V(4).Infof("Executing devfile command %v", command.Name)

			a.machineEventLogger.DevFileCommandExecutionBegin(command.Name, machineoutput.TimestampNow())

			for index, action := range command.Actions {
				compInfo := common.ComponentInfo{
					ContainerName: *action.Component,
					PodName:       podName,
				}

				a.machineEventLogger.DevFileActionExecutionBegin(*action.Command, index, command.Name, machineoutput.TimestampNow())

				err = exec.ExecuteDevfileBuildAction(&a.Client, action, command.Name, compInfo, show, outputReceiver)

				a.machineEventLogger.DevFileActionExecutionComplete(*action.Command, index, command.Name, machineoutput.TimestampNow(), err)
				if err != nil {
					a.machineEventLogger.ReportError(err, machineoutput.TimestampNow())
					return err
				}
			}

			a.machineEventLogger.DevFileCommandExecutionComplete(command.Name, machineoutput.TimestampNow())

			// Reset the for loop counter and iterate through all the devfile commands again for others
			i = -1
			// Set the buildRequired to false since we already executed the build command
			buildRequired = false
		} else if (command.Name == string(common.DefaultDevfileRunCommand) || command.Name == a.devfileRunCmd) && !buildRequired {
			// Always check for buildRequired is false, since the command may be iterated out of order and we always want to execute devBuild first if buildRequired is true. If buildRequired is false, then we don't need to build and we can execute the devRun command
			glog.V(3).Infof("Executing devfile command %v", command.Name)

			a.machineEventLogger.DevFileCommandExecutionBegin(command.Name, machineoutput.TimestampNow())

			for index, action := range command.Actions {

				a.machineEventLogger.DevFileActionExecutionBegin(*action.Command, index, command.Name, machineoutput.TimestampNow())

				// Check if the devfile run component containers have supervisord as the entrypoint.
				// Start the supervisord if the odo component does not exist
				if !componentExists {
					err = a.InitRunContainerSupervisord(*action.Component, podName, containers)
					if err != nil {
						a.machineEventLogger.ReportError(err, machineoutput.TimestampNow())
						return
					}
				}

				compInfo := common.ComponentInfo{
					ContainerName: *action.Component,
					PodName:       podName,
				}

				err = exec.ExecuteDevfileRunAction(&a.Client, action, command.Name, compInfo, show, outputReceiver)

				a.machineEventLogger.DevFileActionExecutionComplete(*action.Command, index, command.Name, machineoutput.TimestampNow(), err)
				if err != nil {
					a.machineEventLogger.ReportError(err, machineoutput.TimestampNow())
					return err
				}
			}

			a.machineEventLogger.DevFileCommandExecutionComplete(command.Name, machineoutput.TimestampNow())
		}
	}

	return
}

// InitRunContainerSupervisord initializes the supervisord in the container if
// the container has entrypoint that is not supervisord
func (a Adapter) InitRunContainerSupervisord(containerName, podName string, containers []corev1.Container) (err error) {
	for _, container := range containers {
		if container.Name == containerName && !reflect.DeepEqual(container.Command, []string{common.SupervisordBinaryPath}) {
			command := []string{common.SupervisordBinaryPath, "-c", common.SupervisordConfFile, "-d"}
			compInfo := common.ComponentInfo{
				ContainerName: containerName,
				PodName:       podName,
			}
			err = exec.ExecuteCommand(&a.Client, compInfo, command, true, nil)
		}
	}

	return
}

// getFirstContainerWithSourceVolume returns the first container that set mountSources: true
// Because the source volume is shared across all components that need it, we only need to sync once,
// so we only need to find one container. If no container was found, that means there's no
// container to sync to, so return an error
func getFirstContainerWithSourceVolume(containers []corev1.Container) (string, error) {
	for _, c := range containers {
		for _, vol := range c.VolumeMounts {
			if vol.Name == kclient.OdoSourceVolume {
				return c.Name, nil
			}
		}
	}

	return "", fmt.Errorf("In order to sync files, odo requires at least one component in a devfile to set 'mountSources: true'")
}

// Delete deletes the component
func (a Adapter) Delete(labels map[string]string) error {
	if !utils.ComponentExists(a.Client, a.ComponentName) {
		return errors.Errorf("the component %s doesn't exist on the cluster", a.ComponentName)
	}

	return a.Client.DeleteDeployment(labels)
}

type KubernetesContainerStatus struct {
	DeploymentUID types.UID
	ReplicaSetUID types.UID
	Pods          []*corev1.Pod
}

type KubernetesPodStatus struct {
	Name           string
	UID            string
	Phase          string
	Labels         map[string]string
	StartTime      *time.Time
	Containers     []corev1.ContainerStatus
	InitContainers []corev1.ContainerStatus
}

var _ common.ContainerStatus = KubernetesContainerStatus{}

func (kcs KubernetesContainerStatus) GetType() common.ContainerStatusType {
	return common.ContainerStatusKubernetes
}

func (a Adapter) GetContainerStatus() (common.ContainerStatus, error) {

	deployment, err := a.Client.KubeClient.AppsV1().Deployments(a.Client.Namespace).Get(a.ComponentName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if deployment == nil {
		// TODO: Return NO_DEPLOYMENT status
		return nil, errors.New("Deployment status from Kubernetes API was nil")
	}

	deploymentUID := deployment.UID

	replicaSetList, err := a.Client.KubeClient.AppsV1().ReplicaSets(a.Client.Namespace).List(metav1.ListOptions{})

	if err != nil {
		return nil, err
	}

	matchingReplicaSets := []v1.ReplicaSet{}
	for _, replicaSet := range replicaSetList.Items {

		for _, ownerRef := range replicaSet.OwnerReferences {

			if ownerRef.UID == deploymentUID {
				matchingReplicaSets = append(matchingReplicaSets, replicaSet)
			}
		}
	}

	// debugOutputJSON(matchingReplicaSets)

	if len(matchingReplicaSets) == 0 {
		// TODO: Return NO_REPLICA_SET status
		return nil, errors.New("No replica sets found")
	}

	if len(matchingReplicaSets) > 1 {
		// TODO: Return MULTIPLE_REPLICA_SET status or handle this case better
		return nil, errors.New("Multiple replica sets found")
	}

	replicaSetUID := matchingReplicaSets[0].UID

	// fmt.Println("replicaSetUID ", replicaSetUID)

	podList, err := a.Client.KubeClient.CoreV1().Pods(a.Client.Namespace).List(metav1.ListOptions{})

	matchingPods := []*corev1.Pod{}
	for _, podItem := range podList.Items {
		for _, ownerRef := range podItem.OwnerReferences {

			// fmt.Println(podItem.Name, " ", "ref uid ", ownerRef.UID, " ", replicaSetUID)

			if string(ownerRef.UID) == string(replicaSetUID) {
				podItemPtr := &podItem
				matchingPods = append(matchingPods, podItemPtr)
				fmt.Println("match!") // TODO: Remove this
			}
		}
	}

	result := KubernetesContainerStatus{}

	for _, matchingPod := range matchingPods {

		// podStatus := CreateKubernetesPodStatusFromPod(matchingPod)

		// podStatus := KubernetesPodStatus{
		// 	Name:           matchingPod.Name,
		// 	UID:            string(matchingPod.UID),
		// 	Phase:          string(matchingPod.Status.Phase),
		// 	Labels:         matchingPod.Labels,
		// 	InitContainers: []corev1.ContainerStatus{},
		// 	Containers:     []corev1.ContainerStatus{},
		// }

		// if matchingPod.Status.StartTime != nil {
		// 	podStatus.StartTime = &matchingPod.Status.StartTime.Time
		// }

		// podStatus.InitContainers = matchingPod.Status.InitContainerStatuses

		// podStatus.Containers = matchingPod.Status.ContainerStatuses

		result.Pods = append(result.Pods, matchingPod)

	}

	result.DeploymentUID = deploymentUID
	result.ReplicaSetUID = replicaSetUID

	return result, nil

}

func CreateKubernetesPodStatusFromPod(pod corev1.Pod) KubernetesPodStatus {
	podStatus := KubernetesPodStatus{
		Name:           pod.Name,
		UID:            string(pod.UID),
		Phase:          string(pod.Status.Phase),
		Labels:         pod.Labels,
		InitContainers: []corev1.ContainerStatus{},
		Containers:     []corev1.ContainerStatus{},
	}

	if pod.Status.StartTime != nil {
		podStatus.StartTime = &pod.Status.StartTime.Time
	}

	podStatus.InitContainers = pod.Status.InitContainerStatuses

	podStatus.Containers = pod.Status.ContainerStatuses

	return podStatus

}

var podPhaseSortOrder = map[corev1.PodPhase]int{
	corev1.PodFailed:    0,
	corev1.PodSucceeded: 1,
	corev1.PodUnknown:   2,
	corev1.PodPending:   3,
	corev1.PodRunning:   4,
}

type SupervisordStatusWatcher struct {
	statusReconcilerChannel chan supervisorDStatusEvent
}

func NewSupervisordStatusWatch() *SupervisordStatusWatcher {
	inputChan := createSupervisorDStatusReconciler()

	return &SupervisordStatusWatcher{
		statusReconcilerChannel: inputChan,
	}
}

func (a Adapter) StartSupervisordCtlStatusWatch() {

	watcher := NewSupervisordStatusWatch()

	// start time

	ticker := time.NewTicker(10 * time.Second)

	go func() {
		for {
			// On initial goroutine start, perform a query
			watcher.querySupervisordStatusFromContainers(a)
			<-ticker.C
		}

	}()

}

func (sw *SupervisordStatusWatcher) querySupervisordStatusFromContainers(a Adapter) {
	genericStatus, err := a.GetContainerStatus()
	if err != nil {
		fmt.Println(err)
		return
	}

	// TODO: Move all this status stuff to its own file in adapter package

	status := genericStatus.(KubernetesContainerStatus)

	// debugOutputJSON(status.Pods)

	sort.Slice(status.Pods, func(i, j int) bool {

		iPod := status.Pods[i]
		jPod := status.Pods[j]

		iTime := iPod.CreationTimestamp.Time
		jTime := jPod.CreationTimestamp.Time

		if !jTime.Equal(iTime) {
			// Sort descending by creation timestamp
			return jTime.After(iTime)
		}

		// Next, sort descending to find the pod with most successful pod phase:
		// PodRunning > PodPending > PodUnknown > PodSucceeded > PodFailed
		return podPhaseSortOrder[jPod.Status.Phase] > podPhaseSortOrder[iPod.Status.Phase]
	})

	if len(status.Pods) < 1 {
		return
	}

	fmt.Println("Unimplemented 10")

	pod := status.Pods[0]

	// Acquire the run command object to verify correctness
	runCommand, err := adaptersCommon.GetRunCommand(a.Devfile.Data, a.devfileRunCmd)
	if err != nil {
		fmt.Println(err)
		return
	}

	componentAliasToActionMap := map[string][]versionsCommon.DevfileCommandAction{}
	for _, action := range runCommand.Actions {

		if action.Component != nil {

			actionList := componentAliasToActionMap[*action.Component]

			componentAliasToActionMap[*action.Component] = append(actionList, action)
		}
	}

	for _, container := range pod.Status.ContainerStatuses {

		status := getSupervisordStatusInContainer(pod.Name, container.Name, a)

		// The name of the container is the component alias
		actionsForContainer := componentAliasToActionMap[container.Name]

		for _, action := range actionsForContainer {

			sw.statusReconcilerChannel <- supervisorDStatusEvent{
				commandName:   runCommand.Name,
				containerName: container.Name,
				status:        status,
				actionCommand: *action.Command,
			}

		}

	}

}

type supervisorDStatusEvent struct {
	commandName   string
	containerName string
	status        string
	actionCommand string
}

func createSupervisorDStatusReconciler() chan supervisorDStatusEvent {

	// TODO: Need to initially communicate the status

	result := make(chan supervisorDStatusEvent)

	go func() {
		// containerName -> status
		lastContainerStatus := map[string]string{}

		for {

			event := <-result

			fmt.Println("Event received\n")

			previousStatus, present := lastContainerStatus[event.containerName]
			lastContainerStatus[event.containerName] = event.status

			reportChange := false

			if present {

				if previousStatus != event.status {
					glog.V(4).Infof("Container %v status changed - to: %v, was: %v", event.containerName, event.status, previousStatus)
					reportChange = true
				} else {
					glog.V(5).Infof("Container %v status has not changed - is: %v", event.containerName, event.status)
					reportChange = false
				}

			} else {
				glog.V(4).Infof("A new container %v status is reported - is: %v", event.containerName, event.status)
				reportChange = true
			}

			if reportChange {
				fmt.Printf("action: commandName: \"%s\" component: \"%s\" command: \"%s\" status: \"%s\"\n", event.commandName, event.containerName, event.actionCommand, event.status)
			}

		}

	}()

	return result
}

func getSupervisordStatusInContainer(podName string, containerName string, a Adapter) string {

	command := []string{common.SupervisordBinaryPath, common.SupervisordCtlSubCommand, "status"}
	compInfo := common.ComponentInfo{
		ContainerName: containerName,
		PodName:       podName,
	}

	receiver := SupervisordReceiver{receivedText: &[]string{}, mutex: &gosync.Mutex{}}

	err := exec.ExecuteCommand(&a.Client, compInfo, command, true, receiver)
	fmt.Println("Post")
	if err != nil {
		// TODO: Do something w/ this
		fmt.Println(err)
		return "ERROR"
	}

	for _, line := range *receiver.receivedText {

		if strings.Contains(line, string(common.DefaultDevfileRunCommand)) {

			if strings.Contains(line, "STARTED") {
				return "STARTED"
			} else if strings.Contains(line, "STOPPED") {
				return "STOPPED"
			} else {
				return "UNKNOWN"
			}
		}

	}

	return "UNKNOWN"
}

type SupervisordReceiver struct {
	receivedText *[]string
	mutex        *gosync.Mutex
}

func (receiver SupervisordReceiver) ReceiveText(text []exec.TimestampedText) {

	receiver.mutex.Lock()
	defer receiver.mutex.Unlock()

	for _, line := range text {
		*receiver.receivedText = append(*receiver.receivedText, line.Text)
	}

}

var _ exec.ContainerOutputReceiver = SupervisordReceiver{}

func (a Adapter) StartContainerStatusWatch() {

	pw := NewPodWatcher(&a)
	pw.startStatusTimer()

	// fmt.Println("Unimplemented 4")

}

func debugOutputJSON(jsonObj interface{}) {
	output, err := json.MarshalIndent(jsonObj, "", "    ")
	if err != nil {
		fmt.Println("Parsing error occured", err)
		return
	}
	fmt.Println(string(output))
}

func (a Adapter) StartURLHttpRequestStatusWatch() {
	// a.Client.ListIngresses()
	fmt.Println("unimplemented 32")

	urls, err := url.ListPushedIngress(&a.Client, a.ComponentName)

	for _, item := range urls.Items {

		url := url.GetURLString(url.GetProtocol(routev1.Route{}, item), "", item.Spec.Rules[0].Host)

		startURLTestGoRoutine(url, true, 10*time.Second)

		// fmt.Printf("%v %v\n", url, item.Spec.TLS != nil)
	}

	// debugOutputJSON(urls)

	// localUrls := urls.EnvSpecificInfo.GetURL()

	// for _, i := range localUrls {
	// 	for _, u := range urls.Items {
	// 		if i.Name == u.Name {
	// 			fmt.Printf("%v\n", u.Spec.TLS != nil)
	// 		}
	// 	}
	// }

	if err != nil {
		fmt.Println("Error", err) // TODO: Handle this
		return
	}

}

func startURLTestGoRoutine(url string, insecureSkipVerify bool, delayBetweenRequests time.Duration) {

	go func() {

		var previousResult *bool = nil

		for {
			result, _ := testUrl(url, insecureSkipVerify)

			if previousResult == nil || *previousResult != result {
				fmt.Printf("URL: %s  result: %v\n", url, result)
			}

			previousResult = &result

			time.Sleep(delayBetweenRequests)

		}
	}()
}

func testUrl(url string, insecureSkipVerify bool) (bool, error) {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify},
	}

	client := &http.Client{Transport: tr}

	resp, err := client.Get(url)
	if err != nil || resp == nil {
		errMsg := "Get request failed for " + url + " , with no response code."
		return false, errors.New(errMsg)
	}

	defer resp.Body.Close()

	// if resp.StatusCode != 200 {
	// 	errMsg := "Get response failed for " + url + ", response code: " + strconv.Itoa(resp.StatusCode)
	// 	return false, errors.New(errMsg)
	// }

	return true, nil

}
