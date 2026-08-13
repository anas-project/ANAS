package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const localAdminStateVersion = "anas.dev/v1"
const defaultLocalAdminUsernameTemplate = "admin_{module}"

var localAdminUsernamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

type localAdminState struct {
	APIVersion string                      `yaml:"api_version" json:"api_version"`
	Path       string                      `yaml:"-" json:"-"`
	Accounts   map[string]localAdminRecord `yaml:"accounts" json:"accounts"`
	dirty      bool
}

type localAdminRecord struct {
	Module    string `yaml:"module" json:"module"`
	ID        string `yaml:"id" json:"id"`
	Purpose   string `yaml:"purpose" json:"purpose"`
	Username  string `yaml:"username" json:"username"`
	SecretKey string `yaml:"secret_key" json:"secret_key"`
	URIFrom   string `yaml:"uri_from,omitempty" json:"uri_from,omitempty"`
}

func localAdminStatePath(base string) string { return filepath.Join(base, "local-admins.yml") }

func loadLocalAdminState(base string) (*localAdminState, error) {
	s := &localAdminState{APIVersion: localAdminStateVersion, Path: localAdminStatePath(base), Accounts: map[string]localAdminRecord{}}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, s); err != nil {
		return nil, err
	}
	s.Path = localAdminStatePath(base)
	if s.APIVersion != localAdminStateVersion {
		return nil, fmt.Errorf("unsupported local administrator state version %q", s.APIVersion)
	}
	if s.Accounts == nil {
		s.Accounts = map[string]localAdminRecord{}
	}
	return s, nil
}

func (s *localAdminState) Save() error {
	if s == nil || !s.dirty {
		return nil
	}
	return writeYAMLAtomic(s.Path, s, 0600)
}

func copyLocalAdminState(base, dst string) error {
	src := localAdminStatePath(base)
	if exists(src) {
		return copyFileMode(src, dst, 0600)
	}
	state := localAdminState{APIVersion: localAdminStateVersion, Accounts: map[string]localAdminRecord{}}
	return writeYAMLAtomic(dst, &state, 0600)
}

func localAdminKey(module, id string) string { return module + "." + id }

func localAdminSecretKey(module, id string) string {
	return "ANAS_LOCAL_ADMIN__" + defaultEnvPrefix(module) + "__" + defaultEnvPrefix(id) + "__PASSWORD"
}

func localAdminEnvKeys(mod Module, id string) (string, string) {
	prefix := mod.EnvPrefix + "_LOCAL_ADMIN"
	if id != "primary" {
		prefix += "__" + defaultEnvPrefix(id)
	}
	return prefix + "_USERNAME", prefix + "_PASSWORD"
}

func localAdminPasswordFileEnvKey(mod Module, id string) string {
	_, passwordKey := localAdminEnvKeys(mod, id)
	return passwordKey + "_FILE"
}

func localAdminPasswordFile(base, module, id string) string {
	return filepath.Join(base, "runtime-secrets", "local-admins", module, id+".password")
}

func writeLocalAdminPasswordFile(path, password string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".local-admin-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(password + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (a *app) materializeLocalAccounts() error {
	state, err := loadLocalAdminState(a.base)
	if err != nil {
		return err
	}
	a.localAdmins = state
	for _, module := range a.order {
		mod := a.reg[module]
		if service, ok := a.cfg.Modules.Values[module]; ok {
			for overrideID := range service.Administration.LocalAccounts {
				knownID := false
				for _, account := range mod.LocalAccounts {
					if account.ID == overrideID {
						knownID = true
						break
					}
				}
				if !knownID {
					return fmt.Errorf("modules.%s.administration.local_accounts.%s is not a manifest account id", module, overrideID)
				}
			}
		}
		for _, declared := range mod.LocalAccounts {
			key := localAdminKey(module, declared.ID)
			record, locked := state.Accounts[key]
			if locked && (record.Module != module || record.ID != declared.ID || record.SecretKey != localAdminSecretKey(module, declared.ID)) {
				return fmt.Errorf("local administrator state %s does not match the manifest account identity", key)
			}
			if locked {
				uriFrom := localAdminURIFrom(mod)
				if record.Purpose != declared.Purpose || record.URIFrom != uriFrom {
					record.Purpose, record.URIFrom = declared.Purpose, uriFrom
					state.Accounts[key], state.dirty = record, true
				}
			}
			if !locked {
				username := declared.FixedUsername
				if username == "" {
					username = strings.ReplaceAll(defaultLocalAdminUsernameTemplate, "{module}", strings.ReplaceAll(module, "-", "_"))
				}
				if !localAdminUsernamePattern.MatchString(username) {
					return fmt.Errorf("local administrator %s resolves to invalid username %q", key, username)
				}
				record = localAdminRecord{
					Module: module, ID: declared.ID, Purpose: declared.Purpose, Username: username,
					SecretKey: localAdminSecretKey(module, declared.ID), URIFrom: localAdminURIFrom(mod),
				}
				state.Accounts[key] = record
				state.dirty = true
			}
			password, err := a.secrets.Ensure(record.SecretKey, func() (string, error) {
				return randomPassword(a.cfg.Administration.LocalAccounts.PasswordLength)
			})
			if err != nil {
				return err
			}
			meta := a.secrets.metadata[record.SecretKey]
			if meta.Kind != "local_admin" {
				a.secrets.metadata[record.SecretKey] = secretMetadata{Owner: module, Kind: "local_admin", Provenance: "generated-local-admin"}
				a.secrets.dirty = true
			}
			usernameKey, _ := localAdminEnvKeys(mod, declared.ID)
			a.env[usernameKey] = record.Username
			a.setEnvOwner(usernameKey, module)
			if declared.ContainerFormat == "plaintext_on_bootstrap" {
				path := localAdminPasswordFile(a.base, module, declared.ID)
				if err := writeLocalAdminPasswordFile(path, password); err != nil {
					return err
				}
				fileKey := localAdminPasswordFileEnvKey(mod, declared.ID)
				a.env[fileKey] = path
				a.setEnvOwner(fileKey, module)
			}
		}
	}
	return nil
}

func localAdminURIFrom(mod Module) string {
	for _, surface := range mod.ManagementSurfaces {
		if surface.Authentication == "local" {
			return surface.URIFrom
		}
	}
	return ""
}

func (a *app) localAdminHookEnv(module string, env map[string]string) map[string]string {
	if a.localAdmins == nil || len(a.reg[module].LocalAccounts) == 0 {
		return env
	}
	out := cloneMap(env)
	mod := a.reg[module]
	for _, declared := range mod.LocalAccounts {
		record, ok := a.localAdmins.Accounts[localAdminKey(module, declared.ID)]
		if !ok {
			continue
		}
		usernameKey, passwordKey := localAdminEnvKeys(mod, declared.ID)
		out[usernameKey] = record.Username
		out[passwordKey] = a.secrets.values[record.SecretKey]
	}
	return out
}

// restoreLocalAdminPasswordFiles recreates non-authoritative runtime
// projections after snapshot restore or artifact activation. The Secret Store
// remains the only backed-up plaintext source.
func (a *app) restoreLocalAdminPasswordFiles() error {
	if a.localAdmins == nil || a.secrets == nil {
		return nil
	}
	for module, mod := range a.reg {
		for _, declared := range mod.LocalAccounts {
			if declared.ContainerFormat != "plaintext_on_bootstrap" {
				continue
			}
			record, ok := a.localAdmins.Accounts[localAdminKey(module, declared.ID)]
			if !ok {
				return fmt.Errorf("local administrator state for %s.%s is missing", module, declared.ID)
			}
			password := a.secrets.values[record.SecretKey]
			if password == "" {
				return fmt.Errorf("local administrator secret %s is missing", record.SecretKey)
			}
			if err := writeLocalAdminPasswordFile(localAdminPasswordFile(a.base, module, declared.ID), password); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) applyLocalAdministrators(mod Module, dir string, env map[string]string) error {
	for _, declared := range mod.LocalAccounts {
		if strings.TrimSpace(declared.Apply) == "" {
			continue
		}
		record, ok := a.localAdmins.Accounts[localAdminKey(mod.Name, declared.ID)]
		if !ok {
			return fmt.Errorf("local administrator state for %s.%s is missing", mod.Name, declared.ID)
		}
		password := a.secrets.values[record.SecretKey]
		if password == "" {
			return fmt.Errorf("local administrator secret %s is missing", record.SecretKey)
		}
		op := localAccountOperation{Handler: declared.Apply, AccountID: declared.ID, Username: record.Username, SecretKey: record.SecretKey, CandidateSecretKey: localAdminCandidateSecretKey}
		secretView := a.scopedSecrets(mod.Name)
		secretView[record.SecretKey], secretView[localAdminCandidateSecretKey] = password, password
		if _, err := a.runLocalAccountHook(mod, "local_account_apply", dir, env, op, secretView); err != nil {
			return err
		}
	}
	return nil
}

func sortedLocalAdminRecords(state *localAdminState) []localAdminRecord {
	out := make([]localAdminRecord, 0, len(state.Accounts))
	for _, record := range state.Accounts {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Module == out[j].Module {
			return out[i].ID < out[j].ID
		}
		return out[i].Module < out[j].Module
	})
	return out
}
