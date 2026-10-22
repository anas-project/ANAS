package consoleconfig

import (
	"fmt"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/serviceauth"
)

func validateServiceAPI(config *Config) error {
	if config.ServiceAPI == nil {
		return nil
	}
	if config.ServiceAPI.Directory == "" {
		return fmt.Errorf("service_api.credentials_dir is required")
	}
	if err := serviceauth.ValidateConfig(*config.ServiceAPI); err != nil {
		return fmt.Errorf("service_api: %w", err)
	}
	workspaces := make(map[string]bool, len(config.Workspaces))
	for _, workspace := range config.Workspaces {
		workspaces[workspace.ID] = true
		if pathsOverlap(config.ServiceAPI.Directory, workspace.Path) {
			return fmt.Errorf("service_api.credentials_dir must not overlap registered workspace %q", workspace.ID)
		}
	}
	for _, target := range config.BackupTargets {
		if pathsOverlap(config.ServiceAPI.Directory, target.Path) {
			return fmt.Errorf("service_api.credentials_dir must not overlap backup target %q", target.ID)
		}
	}
	for _, client := range config.ServiceAPI.Clients {
		for _, grant := range client.Grants {
			if !workspaces[grant.Workspace] {
				return fmt.Errorf("service_api client %q grants an unregistered workspace %q", client.ID, grant.Workspace)
			}
			if grant.Permission != "application.credentials.read" {
				return fmt.Errorf("service_api client %q grants an unsupported API permission", client.ID)
			}
		}
	}
	return nil
}

// Service grants are host authority and must not become part of a workspace
// backup/restore or a workspace-writable configuration tree.
func validateServiceConfigLocation(config Config, path string) error {
	if config.ServiceAPI == nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve service configuration: %w", err)
	}
	for _, workspace := range config.Workspaces {
		workspacePath, err := filepath.EvalSymlinks(workspace.Path)
		if err != nil {
			return err
		}
		if pathWithin(resolved, workspacePath) {
			return fmt.Errorf("service API configuration must be outside registered workspace %q", workspace.ID)
		}
	}
	return nil
}
