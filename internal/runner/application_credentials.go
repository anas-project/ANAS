package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/application"
)

func validateApplicationCredentialReference(mod Module, entry appcatalog.Declaration) error {
	c := entry.Credentials
	if c == nil {
		return nil
	}
	if c.LocalAccount != "" {
		for _, account := range mod.LocalAccounts {
			if account.ID == c.LocalAccount && entry.URLFrom != "" && entry.URLFrom == localAdminURIFrom(mod) {
				return nil
			}
		}
		return fmt.Errorf("credential account must match the declared local management entry")
	}
	for _, key := range []string{c.UsernameFrom, c.PasswordFrom} {
		if !envKeyPattern.MatchString(key) || !strings.HasPrefix(key, mod.EnvPrefix+"_") {
			return fmt.Errorf("credential sources must belong to the publishing module")
		}
	}
	return nil
}

func freezeApplicationCredentials(mod Module) map[string]appcatalog.CredentialReference {
	var refs map[string]appcatalog.CredentialReference
	for _, entry := range mod.Applications {
		if entry.Credentials != nil {
			if refs == nil {
				refs = map[string]appcatalog.CredentialReference{}
			}
			refs[entry.Name] = *entry.Credentials
		}
	}
	return refs
}

type workspaceApplicationCredentials struct{ workspace string }

func NewWorkspaceApplicationCredentialService(workspace string) application.ApplicationCredentialService {
	return &workspaceApplicationCredentials{workspace: workspace}
}

// ReadApplicationCredential locks the active deployment without running runtime
// recovery. A service granted read access must never execute recovery hooks or
// migrate desired configuration as a side effect of reading a credential.
func (s *workspaceApplicationCredentials) ReadApplicationCredential(ctx context.Context, name string) (application.ApplicationCredential, error) {
	empty := application.ApplicationCredential{}
	missing := func() (application.ApplicationCredential, error) {
		return empty, maintenanceError(application.ErrorKindNotFound, "application_credentials_unavailable", "application credentials are unavailable", nil)
	}
	if ctx == nil || s.workspace == "" || !filepath.IsAbs(s.workspace) {
		return empty, maintenanceError(application.ErrorKindInvalidArgument, "workspace_required", "workspace and context are required", nil)
	}
	base := stateDir(s.workspace)
	unlock, err := acquireRuntimeSharedLockContext(ctx, base)
	if err != nil {
		return empty, maintenanceCLIError(preconditionErrorf("runtime_lock_unavailable", "workspace is busy"))
	}
	defer unlock()
	if _, err := os.Lstat(configTransactionDirectory(s.workspace)); !os.IsNotExist(err) {
		return missing()
	}
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" || active.RuntimeStatus != "running" {
		return missing()
	}
	if err := validateDeploymentID(active.ActiveDeployment); err != nil {
		return missing()
	}
	manifest, txn, err := loadActiveCredentialManifest(base)
	if err != nil || txn != nil {
		return missing()
	}
	root := filepath.Join(base, "deployments", active.ActiveDeployment)
	catalog, err := appcatalog.Read(filepath.Join(root, applicationCatalogRelative, "catalog.json"))
	if err != nil {
		return missing()
	}
	for _, app := range catalog.Applications {
		if app.Name != name {
			continue
		}
		if !app.Enabled || app.Hidden || app.Kind != "admin" || app.Access.Mode != "local" || app.Source.Kind != "module" || !app.CredentialsAvailable {
			return missing()
		}
		mod, found := manifest.Modules[app.Source.Module]
		if !found || mod.Name != app.Source.Module || mod.Name == "" || mod.Name == "." || mod.Name == ".." || filepath.Base(mod.Name) != mod.Name || strings.ContainsAny(mod.Name, `/\`) {
			return missing()
		}
		ref, found := mod.ApplicationCredentials[name]
		if !found {
			return missing()
		}
		out := application.ApplicationCredential{Application: name, URL: app.URL}
		if ref.LocalAccount != "" {
			if ref.UsernameFrom != "" || ref.PasswordFrom != "" {
				return missing()
			}
			declared := false
			for _, account := range mod.LocalAccounts {
				if account.ID == ref.LocalAccount {
					declared = true
				}
			}
			if !declared {
				return missing()
			}
			state, err := loadLocalAdminState(base)
			if err != nil {
				return missing()
			}
			record, found := state.Accounts[localAdminKey(mod.Name, ref.LocalAccount)]
			key := localAdminSecretKey(mod.Name, ref.LocalAccount)
			if !found || record.Module != mod.Name || record.ID != ref.LocalAccount || record.SecretKey != key {
				return missing()
			}
			store, err := loadSecretStore(base)
			if err != nil {
				return missing()
			}
			out.Username, out.Password = record.Username, store.values[key]
		} else {
			for _, key := range []string{ref.UsernameFrom, ref.PasswordFrom} {
				if !envKeyPattern.MatchString(key) || !strings.HasPrefix(key, mod.EnvPrefix+"_") {
					return missing()
				}
			}
			if ref.UsernameFrom == ref.PasswordFrom {
				return missing()
			}
			// The frozen module environment is the currently applied configuration,
			// including externally configured passwords. Desired config may differ.
			env, err := parseEnvFile(filepath.Join(root, "modules", mod.Name, ".env"))
			if err != nil {
				return missing()
			}
			out.Username, out.Password = env[ref.UsernameFrom], env[ref.PasswordFrom]
		}
		if out.Username == "" || out.Password == "" || appcatalog.ValidateURL(out.URL, false) != nil {
			return missing()
		}
		return out, nil
	}
	return missing()
}
