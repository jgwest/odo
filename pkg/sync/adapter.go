package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/openshift/odo/pkg/devfile/adapters/common"
	versionsCommon "github.com/openshift/odo/pkg/devfile/parser/data/common"
	"github.com/openshift/odo/pkg/exec"
	"github.com/openshift/odo/pkg/kclient"
	"github.com/openshift/odo/pkg/log"
	"github.com/openshift/odo/pkg/util"
	"k8s.io/klog"

	"github.com/pkg/errors"
)

// New instantiantes a component adapter
func New(adapterContext common.AdapterContext, client SyncClient) Adapter {
	return Adapter{
		Client:         client,
		AdapterContext: adapterContext,
	}
}

// Adapter is a component adapter implementation for sync
type Adapter struct {
	Client SyncClient
	common.AdapterContext
}

// SyncFiles does a couple of things:
// if files changed/deleted are passed in from watch, it syncs them to the component
// otherwise, it checks which files have changed and syncs the delta
// it returns a boolean execRequired and an error. execRequired tells us if files have
// changed and devfile execution is required
func (a Adapter) SyncFiles(syncParameters common.SyncParameters) (isPushRequired bool, err error) {

	// Whether to write the indexer content to the index file path (resolvePath)
	forceWrite := false

	// Ret from Indexer function
	var ret util.IndexerRet

	deletedFiles := []string{}
	changedFiles := []string{}
	pushParameters := syncParameters.PushParams

	// The logic for watch is:
	// 1) If this is the first time that watch has called Push, then generate the file index using the file indexer, and use that to
	//    sync files (don't use changed/deleted files from watch; they will be included in the index)
	// 2) For every other push/sync call after the first, don't run the file indexer, instead use the watch events to determine
	//    what changed; ensure that the index is then updated based on the watch events.

	// True if the index was updated based on the deleted/changed files values from the watch (and
	// thus the indexer doesn't need to run), false otherwise
	indexRegeneratedByWatch := false

	// If watch files are specified _and_ this is not the first call (by this process) to SyncFiles by the watch command, then insert the
	// changed files into the existing file index, and delete removed files from the index
	if (len(pushParameters.WatchFiles) > 0 || len(pushParameters.WatchDeletedFiles) > 0) && !syncParameters.PushParams.DevfileScanIndexForWatch {

		err := updateIndexWithWatchChanges(pushParameters)

		if err != nil {
			return false, err
		}

		changedFiles = pushParameters.WatchFiles
		deletedFiles = pushParameters.WatchDeletedFiles
		indexRegeneratedByWatch = true

	}

	// Sync source code to the component
	// If syncing for the first time, sync the entire source directory
	// If syncing to an already running component, sync the deltas
	if !syncParameters.PodChanged && !pushParameters.ForceBuild && !indexRegeneratedByWatch {
		absIgnoreRules := util.GetAbsGlobExps(pushParameters.Path, pushParameters.IgnoredFiles)

		var s *log.Status
		if syncParameters.ComponentExists {
			s = log.Spinner("Checking file changes for pushing")
		} else {
			// if the component doesn't exist, we don't check for changes in the files
			// thus we show a different message
			s = log.Spinner("Checking files for pushing")
		}
		defer s.End(false)

		// Before running the indexer, make sure the .odo folder exists (or else the index file will not get created)
		odoFolder := filepath.Join(pushParameters.Path, ".odo")
		if _, err := os.Stat(odoFolder); os.IsNotExist(err) {
			err = os.Mkdir(odoFolder, 0750)
			if err != nil {
				return false, errors.Wrap(err, "unable to create directory")
			}
		}

		// run the indexer and find the modified/added/deleted/renamed files
		ret, err = util.RunIndexer(pushParameters.Path, absIgnoreRules)
		s.End(true)

		if err != nil {
			return false, errors.Wrap(err, "unable to run indexer")
		}

		if len(ret.FilesChanged) > 0 || len(ret.FilesDeleted) > 0 {
			forceWrite = true
		}

		// If the component already exists, sync only the files that changed
		if syncParameters.ComponentExists {
			// apply the glob rules from the .gitignore/.odo file
			// and ignore the files on which the rules apply and filter them out
			filesChangedFiltered, filesDeletedFiltered := util.FilterIgnores(ret.FilesChanged, ret.FilesDeleted, absIgnoreRules)

			// Remove the relative file directory from the list of deleted files
			// in order to make the changes correctly within the Kubernetes pod
			deletedFiles, err = util.RemoveRelativePathFromFiles(filesDeletedFiltered, pushParameters.Path)
			if err != nil {
				return false, errors.Wrap(err, "unable to remove relative path from list of changed/deleted files")
			}
			klog.V(4).Infof("List of files to be deleted: +%v", deletedFiles)
			changedFiles = filesChangedFiltered
			klog.V(4).Infof("List of files changed: +%v", changedFiles)

			if len(filesChangedFiltered) == 0 && len(filesDeletedFiltered) == 0 {
				// no file was modified/added/deleted/renamed, thus return without building
				log.Success("No file changes detected, skipping build. Use the '-f' flag to force the build.")
				return false, nil
			}
		}
	}

	isForcePush := false

	if pushParameters.ForceBuild || !syncParameters.ComponentExists || syncParameters.PodChanged {
		isForcePush = true
	}

	err = a.pushLocal(pushParameters.Path,
		changedFiles,
		deletedFiles,
		isForcePush,
		util.GetAbsGlobExps(pushParameters.Path, pushParameters.IgnoredFiles),
		syncParameters.CompInfo,
	)
	if err != nil {
		return false, errors.Wrapf(err, "failed to sync to component with name %s", a.ComponentName)
	}
	if forceWrite {
		err = util.WriteFile(ret.NewFileMap, ret.ResolvedPath)
		if err != nil {
			return false, errors.Wrapf(err, "Failed to write file")
		}
	}

	return true, nil
}

// pushLocal syncs source code from the user's disk to the component
func (a Adapter) pushLocal(path string, files []string, delFiles []string, isForcePush bool, globExps []string, compInfo common.ComponentInfo) error {
	klog.V(4).Infof("Push: componentName: %s, path: %s, files: %s, delFiles: %s, isForcePush: %+v", a.ComponentName, path, files, delFiles, isForcePush)

	// Edge case: check to see that the path is NOT empty.
	emptyDir, err := util.IsEmpty(path)
	if err != nil {
		return errors.Wrapf(err, "unable to check directory: %s", path)
	} else if emptyDir {
		return errors.New(fmt.Sprintf("directory/file %s is empty", path))
	}

	// Sync the files to the pod
	s := log.Spinner("Syncing files to the component")
	defer s.End(false)

	// Determine which folder we need to sync to
	var syncFolder string
	if compInfo.SourceMount != kclient.OdoSourceVolumeMount {
		syncFolder = compInfo.SourceMount
	} else {
		// If there's only one project defined in the devfile, sync to `/projects/project-name`, otherwise sync to /projects
		syncFolder, err = getSyncFolder(a.Devfile.Data.GetProjects())
		if err != nil {
			return errors.Wrapf(err, "unable to determine sync folder")
		}
	}

	if syncFolder != kclient.OdoSourceVolumeMount {
		// Need to make sure the folder already exists on the component or else sync will fail
		klog.V(4).Infof("Creating %s on the remote container if it doesn't already exist", syncFolder)
		cmdArr := getCmdToCreateSyncFolder(syncFolder)

		err = exec.ExecuteCommand(a.Client, compInfo, cmdArr, false, nil, nil)
		if err != nil {
			return err
		}
	}
	// If there were any files deleted locally, delete them remotely too.
	if len(delFiles) > 0 {
		cmdArr := getCmdToDeleteFiles(delFiles, syncFolder)

		err = exec.ExecuteCommand(a.Client, compInfo, cmdArr, false, nil, nil)
		if err != nil {
			return err
		}
	}

	if !isForcePush {
		if len(files) == 0 && len(delFiles) == 0 {
			// nothing to push
			s.End(true)
			return nil
		}
	}

	if isForcePush || len(files) > 0 {
		klog.V(4).Infof("Copying files %s to pod", strings.Join(files, " "))
		err = CopyFile(a.Client, path, compInfo, syncFolder, files, globExps)
		if err != nil {
			s.End(false)
			return errors.Wrap(err, "unable push files to pod")
		}
	}
	s.End(true)

	return nil
}

// getSyncFolder returns the folder that we need to sync the source files to
// If there's exactly one project defined in the devfile, and clonePath isn't set return `/projects/<projectName>`
// If there's exactly one project, and clonePath is set, return `/projects/<clonePath>`
// If the clonePath is an absolute path or contains '..', return an error
// Otherwise (zero projects or many), return `/projects`
func getSyncFolder(projects []versionsCommon.DevfileProject) (string, error) {
	if len(projects) == 1 {
		project := projects[0]
		// If the clonepath is set to a value, set it to be the sync folder
		// As some devfiles rely on the code being synced to the folder in the clonepath
		if project.ClonePath != "" {
			if strings.HasPrefix(project.ClonePath, "/") {
				return "", fmt.Errorf("the clonePath in the devfile must be a relative path")
			}
			if strings.Contains(project.ClonePath, "..") {
				return "", fmt.Errorf("the clonePath in the devfile cannot escape the projects root. Don't use .. to try and do that")
			}
			return filepath.ToSlash(filepath.Join(kclient.OdoSourceVolumeMount, project.ClonePath)), nil
		}
		return filepath.ToSlash(filepath.Join(kclient.OdoSourceVolumeMount, projects[0].Name)), nil
	}
	return kclient.OdoSourceVolumeMount, nil

}

func updateIndexWithWatchChanges(pushParameters common.PushParameters) error {
	indexFilePath, err := util.ResolveIndexFilePath(pushParameters.Path)

	if err != nil {
		return errors.Wrapf(err, "unable to resolve path: %s", pushParameters.Path)
	}

	// Check that the path exists
	_, err = os.Stat(indexFilePath)
	if err != nil {
		return errors.Wrapf(err, "resolved path doesn't exist: %s", indexFilePath)
	}

	// Parse the existing index
	fileIndex, err := util.ReadFileIndex(indexFilePath)
	if err != nil {
		return errors.Wrapf(err, "Unable to read index from path: %s", indexFilePath)
	}

	rootDir := pushParameters.Path

	// Remove deleted files from the existing index
	for _, deletedFile := range pushParameters.WatchDeletedFiles {

		relativePath, err := util.CalculateFileDataKeyFromPath(deletedFile, rootDir)

		if err != nil {
			klog.V(4).Infof("Error occurred for %s: %v", deletedFile, err)
			continue
		}
		delete(fileIndex.Files, relativePath)
		klog.V(4).Infof("Removing watch deleted file from index: %s", relativePath)
	}

	// Add changed files to the existing index
	for _, addedOrModifiedFile := range pushParameters.WatchFiles {
		relativePath, fileData, err := util.GenerateNewFileDataEntry(addedOrModifiedFile, rootDir)

		if err != nil {
			klog.V(4).Infof("Error occurred for %s: %v", addedOrModifiedFile, err)
			continue
		}
		fileIndex.Files[relativePath] = *fileData
		klog.V(4).Infof("Added/updated watched file in index: %s", relativePath)
	}

	util.WriteFile(fileIndex.Files, indexFilePath)

	return nil
}

// getCmdToCreateSyncFolder returns the command used to create the remote sync folder on the running container
func getCmdToCreateSyncFolder(syncFolder string) []string {
	return []string{"mkdir", "-p", syncFolder}
}

// getCmdToDeleteFiles returns the command used to delete the remote files on the container that are marked for deletion
func getCmdToDeleteFiles(delFiles []string, syncFolder string) []string {
	rmPaths := util.GetRemoteFilesMarkedForDeletion(delFiles, syncFolder)
	klog.V(4).Infof("remote files marked for deletion are %+v", rmPaths)
	cmdArr := []string{"rm", "-rf"}
	return append(cmdArr, rmPaths...)
}
