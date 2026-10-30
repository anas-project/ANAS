package consoleconfig

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/serviceauth"
)

func validateServiceAPI(config *Config) error {
	if config.MailInstallSocket != nil {
		if config.ServiceAPI == nil || !hasMailInstallGrant(config.ServiceAPI) {
			return fmt.Errorf("mail_install_socket requires an explicit mail installation grant")
		}
		socket := config.MailInstallSocket
		if !filepath.IsAbs(socket.Directory) || filepath.Clean(socket.Directory) != socket.Directory || strings.ContainsAny(socket.Directory, "\x00\r\n") || filepath.Dir(socket.Directory) == string(filepath.Separator) || len(filepath.Join(socket.Directory, "install.sock")) >= 100 || socket.GroupID <= 0 || uint64(socket.GroupID) >= 1<<32-1 {
			return fmt.Errorf("mail_install_socket requires a dedicated absolute directory and non-root group")
		}
		if pathsOverlap(socket.Directory, config.ConsoleStore) || pathsOverlap(socket.Directory, config.ServiceAPI.Directory) || pathsOverlap(socket.Directory, filepath.Join(filepath.Dir(config.ServiceAPI.Directory), "mail-installations")) {
			return fmt.Errorf("mail_install_socket must not overlap console, service credentials or private installation state")
		}
		privateKeys := []string{}
		if config.TLS.Lego != nil {
			privateKeys = append(privateKeys, config.TLS.Lego.PrivateKey)
		}
		if config.TLS.Temporary != nil {
			privateKeys = append(privateKeys, config.TLS.Temporary.PrivateKey)
		}
		for _, key := range privateKeys {
			if key != "" && pathWithin(key, socket.Directory) {
				return fmt.Errorf("mail_install_socket must not contain TLS private keys")
			}
		}
		for _, w := range config.Workspaces {
			if pathsOverlap(socket.Directory, w.Path) {
				return fmt.Errorf("mail_install_socket must not overlap registered workspaces")
			}
		}
		for _, b := range config.BackupTargets {
			if pathsOverlap(socket.Directory, b.Path) {
				return fmt.Errorf("mail_install_socket must not overlap backup targets")
			}
		}
		found := false
		for _, c := range config.ServiceAPI.Clients {
			for _, g := range c.Grants {
				if c.Enabled && c.GID == socket.GroupID && (g.Permission == "mail.ingress.install" || g.Permission == "mail.outbound.configure") {
					found = true
				}
			}
		}
		if !found {
			return fmt.Errorf("mail_install_socket group must match an enabled mail installer")
		}
	}
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
			if grant.Permission != "application.credentials.read" && grant.Permission != "mail.ingress.install" && grant.Permission != "mail.outbound.configure" {
				return fmt.Errorf("service_api client %q grants an unsupported API permission", client.ID)
			}
			if (grant.Permission == "mail.ingress.install" || grant.Permission == "mail.outbound.configure") && (len(grant.Resources) != 1 || grant.Resources[0] != "mail") {
				return fmt.Errorf("service_api client %q mail installation grant must address only mail", client.ID)
			}
		}
	}
	if hasMailInstallGrant(config.ServiceAPI) {
		root := filepath.Join(filepath.Dir(config.ServiceAPI.Directory), "mail-installations")
		if pathsOverlap(root, config.ServiceAPI.Directory) || pathsOverlap(root, config.ConsoleStore) {
			return fmt.Errorf("mail installation state must not overlap service credentials or console store")
		}
		for _, workspace := range config.Workspaces {
			if pathsOverlap(root, workspace.Path) {
				return fmt.Errorf("mail installation state must not overlap registered workspace %q", workspace.ID)
			}
		}
		for _, target := range config.BackupTargets {
			if pathsOverlap(root, target.Path) {
				return fmt.Errorf("mail installation state must not overlap backup target %q", target.ID)
			}
		}
	}
	return nil
}

func hasMailInstallGrant(service *serviceauth.Config) bool {
	if service == nil {
		return false
	}
	for _, client := range service.Clients {
		for _, grant := range client.Grants {
			if grant.Permission == "mail.ingress.install" || grant.Permission == "mail.outbound.configure" {
				return true
			}
		}
	}
	return false
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
	if config.MailInstallSocket != nil {
		socket, err := resolvePathAllowMissing(config.MailInstallSocket.Directory)
		if err != nil || pathWithin(resolved, socket) {
			return fmt.Errorf("mail install socket must not contain the service configuration")
		}
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
