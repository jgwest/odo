package component

import (
	"fmt"
	"reflect"
	"time"

	"github.com/golang/glog"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
)

type PodWatcher struct {
	adapter          *Adapter
	statusReconciler chan ReceiverTaskChannelEntry
}

func NewPodWatcher(adapter *Adapter) *PodWatcher {
	return &PodWatcher{
		adapter:          adapter,
		statusReconciler: createStatusReconciler(adapter),
	}
}

func (pw *PodWatcher) startStatusTimer() {
	pw.doWatch(pw.adapter)
}

type ReceiverTaskChannelEntry struct {
	pods                   []*corev1.Pod
	err                    error
	isCompleteListOfPods   bool
	isDeleteEventFromWatch bool
}

func getLatestContainerStatus(adapter *Adapter) *KubernetesContainerStatus {
	// Keep trying to acquire the replicaset and deploymentset of the component, so that we can reliably find its pods
	for {
		containerStatus, err := adapter.GetContainerStatus()
		if err == nil {

			innerKubeContainerStatus := containerStatus.(KubernetesContainerStatus)

			if innerKubeContainerStatus.DeploymentUID == "" || innerKubeContainerStatus.ReplicaSetUID == "" {
				glog.V(5).Infof("Unable to retrieve component deployment and replica set, trying again in 5 seconds.")
				time.Sleep(5 * time.Second)
				continue
			}

			return &innerKubeContainerStatus
		}

		glog.V(5).Infof("Unable to retrieve component deployment and replica set, trying again in 5 seconds. Error was:  %v", err)
		time.Sleep(5 * time.Second)
	}

}

func (pw *PodWatcher) doWatch(adapter *Adapter) {

	// TODO: all these glog should be klog?

	go func() {

		watchAttempts := 0

		var w watch.Interface = nil
		for {
			glog.V(5).Infof("Attempting to acquire watch, attempt #%d", watchAttempts+1)

			var err error
			w, err = adapter.Client.KubeClient.CoreV1().Pods(adapter.Client.Namespace).Watch(metav1.ListOptions{})

			if err != nil || w == nil {
				glog.V(5).Infof("Unable to establish watch, trying again in 5 seconds. Error was:  %v", err)
				time.Sleep(5 * time.Second)
				watchAttempts++
			} else {
				break
			}
		}

		glog.V(5).Infof("Watch is successfully established.")

		kubeContainerStatus := getLatestContainerStatus(adapter)

		// After the watch is established, provide the reconciler with a list of all the pods in the namespace, so that
		// pods may be deleted from the reconciler that were deleted in the namespace while the watch was dead.
		// (This prevents a race condition where pods deleted during a watch outage might be missed).
		pw.statusReconciler <- ReceiverTaskChannelEntry{
			pods:                   kubeContainerStatus.Pods,
			isCompleteListOfPods:   true,
			isDeleteEventFromWatch: false,
			err:                    nil,
		}

		// We have succesfully established the watch, so kick off the watch event listener
		go pw.watchEventListener(w, kubeContainerStatus.ReplicaSetUID)

	}()

}

func (pw *PodWatcher) watchEventListener(w watch.Interface, replicaSetUID types.UID) {
	for {
		entry := <-w.ResultChan()

		if entry.Object == nil && entry.Type == "" {
			glog.V(5).Infof("Watch has died; initiating re-establish.")
			pw.doWatch(pw.adapter)
			return
		}

		if pod, ok := entry.Object.(*corev1.Pod); ok && pod != nil {

			ownerRefMatches := false
			for _, ownerRef := range pod.OwnerReferences {

				// If the pod is owned by the replicaset of our deployment
				if ownerRef.UID == replicaSetUID {
					ownerRefMatches = true
					break
				}

			}

			if !ownerRefMatches {
				continue
			}

			// fmt.Printf("Watcher: [%v] %v %v %v\n", entry.Type, pod.Name, pod.UID, pod.Status.Phase)

			// Inform the reconciler of a new
			pw.statusReconciler <- ReceiverTaskChannelEntry{
				pods:                   []*corev1.Pod{pod},
				err:                    nil,
				isCompleteListOfPods:   false,
				isDeleteEventFromWatch: entry.Type == watch.Deleted,
			}

		}
	}
}

func createStatusReconciler(adapter *Adapter) chan ReceiverTaskChannelEntry {

	ch := make(chan ReceiverTaskChannelEntry)

	go func() {

		// This map is the single source of truth re: what odo expects the cluster namespace to look like; when
		// new events are received that contain pod data that differs from this, the user should be informed of the delta
		// (and this 'truth' should be updated.)
		//
		// key is pod UID
		mostRecentPodStatus := map[string]*KubernetesPodStatus{}

		for {

			entry := <-ch

			if entry.err != nil {
				fmt.Printf("Error received: %v\n", entry.err)
				continue
			}

			entryPodUIDs := map[string]string{}
			for _, pod := range entry.pods {
				entryPodUIDs[string(pod.UID)] = string(pod.UID)
			}

			// New algorithm:
			changeDetected := false

			// This section of the algorithm only works if the entry was from a podlist (which containers the full list
			// of all pods that exist in the namespace), rather than the watch (which contains only one pod in
			// the namespace.)
			if entry.isCompleteListOfPods {
				// Detect if there exists a UID in mostRecentPodStatus that is not in entry; if so, one or more previous
				// pods have disappeared, so set changeDetected to true.
				for mostRecentPodUID := range mostRecentPodStatus {
					if _, exists := entryPodUIDs[mostRecentPodUID]; !exists {
						glog.V(5).Infof("Status change detected: Could not find previous pod %s in most recent pod status", mostRecentPodUID)
						delete(mostRecentPodStatus, mostRecentPodUID)
						changeDetected = true
					}
				}
			}

			if !changeDetected {
				for _, pod := range entry.pods {
					podVal := CreateKubernetesPodStatusFromPod(*pod)

					if entry.isDeleteEventFromWatch {
						delete(mostRecentPodStatus, string(pod.UID))
						glog.V(5).Infof("Removing deleted pod %s", pod.UID)
						changeDetected = true
						continue
					}

					// If a pod exists in the new pod status, that we have not seen before, then a change is detected.
					prevValue, exists := mostRecentPodStatus[string(pod.UID)]
					if !exists {
						mostRecentPodStatus[string(pod.UID)] = &podVal
						glog.V(5).Infof("Adding new pod to most recent pod status %s", pod.UID)
						changeDetected = true

					} else {
						// If the pod exists in both the old and new status, then do a deep comparison
						areEqual := areEqual(&podVal, prevValue)
						if areEqual != "" {
							mostRecentPodStatus[string(pod.UID)] = &podVal
							glog.V(5).Infof("Pod value %s has changed:  %s", pod.UID, areEqual)
							changeDetected = true
						}
					}
				}
			}

			if changeDetected {
				for k, v := range mostRecentPodStatus {
					fmt.Printf("%v -> %v\n", k, v)
				}

			}
		}
	}()

	return ch
}

func areEqual(one *KubernetesPodStatus, two *KubernetesPodStatus) string {
	if one.UID != two.UID {
		return fmt.Sprintf("UIDs differ %s %s", one.UID, two.UID)
	}

	if one.Name != two.Name {
		return fmt.Sprintf("Names differ %s %s", one.Name, two.Name)
	}

	if !reflect.DeepEqual(one.StartTime, two.StartTime) {
		return fmt.Sprintf("Start times differ %v %v", one.StartTime, two.StartTime)
	}

	if one.Phase != two.Phase {
		return fmt.Sprintf("Pod phase differs %s %s", one.Phase, two.Phase)
	}

	if !reflect.DeepEqual(one.Labels, two.Labels) {
		return fmt.Sprintf("Labels differ %v %v", one.Labels, two.Labels)
	}

	initContainerComparison := compareCoreContainerStatusList(one.InitContainers, two.InitContainers)
	if initContainerComparison != "" {
		return fmt.Sprintf("Init containers differ: %s", initContainerComparison)
	}

	containerComparison := compareCoreContainerStatusList(one.Containers, two.Containers)
	if containerComparison != "" {
		return fmt.Sprintf("Containers differ %s", containerComparison)
	}

	return ""
}

func compareCoreContainerStatusList(oneParam []corev1.ContainerStatus, twoParam []corev1.ContainerStatus) string {

	// One-way list compare, using container name to identify individual entries
	compareFunc := func(paramA []corev1.ContainerStatus, paramB []corev1.ContainerStatus) string {
		oneMap := map[string]*corev1.ContainerStatus{}

		for _, one := range paramA {
			oneEntry := &one // de-alias the range val
			oneMap[one.Name] = oneEntry
		}

		for _, two := range paramB {

			oneEntry, exists := oneMap[two.Name]

			// If an entry is present in two but not one
			if !exists || oneEntry == nil {
				return fmt.Sprintf("Container with id %s was present in one state but not the other", two.Name)
			}

			comparison := areCoreContainerStatusesEqual(oneEntry, &two)

			if comparison != "" {
				return comparison
			}
		}

		return ""
	}

	// Since compareFunc is unidirectional, we do it twice

	result := compareFunc(oneParam, twoParam)

	if result != "" {
		return result
	}

	result = compareFunc(twoParam, oneParam)

	return result

}

func areCoreContainerStatusesEqual(one *corev1.ContainerStatus, two *corev1.ContainerStatus) string {

	if one.Name != two.Name {
		return fmt.Sprintf("Core status names differ [%s] [%s]", one.Name, two.Name)
	}

	if one.ContainerID != two.ContainerID {
		return fmt.Sprintf("Core status container IDs differ: [%s] [%s]", one.ContainerID, two.ContainerID)
	}

	compareStates := compareCoreContainerState(one.State, two.State)
	if compareStates != "" {
		return fmt.Sprintf("Core status states differ %s", compareStates)
	}

	return ""
}

func compareCoreContainerState(oneParam corev1.ContainerState, twoParam corev1.ContainerState) string {

	// At present, we only compare the state, and not the state contents
	toString := func(one corev1.ContainerState) string {
		if one.Running != nil {
			return fmt.Sprintf("Running")
		}

		if one.Terminated != nil {
			return fmt.Sprintf("Terminated")
		}

		if one.Waiting != nil {
			return fmt.Sprintf("Waiting")
		}

		return ""
	}

	oneParamState := toString(oneParam)
	twoParamState := toString(twoParam)

	if oneParamState != twoParamState {
		return "Core container states different: " + oneParamState + " " + twoParamState
	}

	return ""

}
