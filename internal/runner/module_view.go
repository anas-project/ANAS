package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/modulestore"
)

const workspaceModuleViewName = "module-view.json"

func marshalWorkspaceModuleView(view modulestore.View) ([]byte, error) {
	if view.APIVersion != "anas.module-view/v1" || strings.TrimSpace(view.ModuleRoot) == "" {
		return nil, fmt.Errorf("invalid Module view")
	}
	body, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func saveWorkspaceModuleView(workspace string, view modulestore.View) error {
	body, err := marshalWorkspaceModuleView(view)
	if err != nil {
		return err
	}
	path := filepath.Join(stateDir(workspace), workspaceModuleViewName)
	temp, err := os.CreateTemp(filepath.Dir(path), ".module-view-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

// commitWorkspaceModuleState publishes the lock and its immutable Module view
// as one logical update. A failure replacing either file restores both prior
// documents, so readers never observe a new lock paired with the old view.
func commitWorkspaceModuleState(lockPath, workspace string, lock *moduleLock, view modulestore.View) error {
	lockBody, err := marshalModuleLock(lock)
	if err != nil {
		return err
	}
	viewBody, err := marshalWorkspaceModuleView(view)
	if err != nil {
		return err
	}
	return commitImportedFiles([]importFile{
		{path: lockPath, data: lockBody, mode: 0644},
		{path: filepath.Join(stateDir(workspace), workspaceModuleViewName), data: viewBody, mode: 0600},
	})
}

func loadWorkspaceModuleView(workspace string) (modulestore.View, error) {
	path := filepath.Join(stateDir(workspace), workspaceModuleViewName)
	body, err := os.ReadFile(path)
	if err != nil {
		return modulestore.View{}, err
	}
	var view modulestore.View
	if err := json.Unmarshal(body, &view); err != nil {
		return modulestore.View{}, err
	}
	if view.APIVersion != "anas.module-view/v1" || strings.TrimSpace(view.ModuleRoot) == "" {
		return modulestore.View{}, fmt.Errorf("invalid workspace Module view")
	}
	return view, nil
}

func locateModuleRootForWorkspace(explicit, workspace string) (string, error) {
	// Explicit development overrides retain highest priority.
	if strings.TrimSpace(explicit) != "" || strings.TrimSpace(os.Getenv("ANAS_MODULE_ROOT")) != "" {
		return locateModuleRoot(explicit)
	}
	if strings.TrimSpace(workspace) != "" {
		if view, err := loadWorkspaceModuleView(workspace); err == nil {
			if root, locateErr := locateModuleRoot(view.ModuleRoot); locateErr == nil {
				return root, nil
			}
		}
	}
	return locateModuleRoot(explicit)
}
