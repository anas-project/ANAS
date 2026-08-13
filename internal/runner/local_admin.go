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

func (a *app) materializeLocalAccounts() error {
	state, err := loadLocalAdminState(a.base)
	if err != nil {
		return err
	}
	a.localAdmins = state
	for _, module := range a.order {
		mod := a.reg[module]
		for _, declared := range mod.LocalAccounts {
			key := localAdminKey(module, declared.ID)
			record, locked := state.Accounts[key]
			if !locked {
				username := declared.FixedUsername
				if username == "" {
					if service, ok := a.cfg.Modules.Values[module]; ok {
						if override, ok := service.Administration.LocalAccounts[declared.ID]; ok && strings.TrimSpace(override.Username) != "" {
							username = strings.TrimSpace(override.Username)
						}
					}
				}
				if username == "" {
					username = strings.ReplaceAll(a.cfg.Administration.LocalAccounts.UsernameTemplate, "{module}", strings.ReplaceAll(module, "-", "_"))
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
			if _, err := a.secrets.Ensure(record.SecretKey, func() (string, error) {
				return randomPassword(a.cfg.Administration.LocalAccounts.PasswordLength)
			}); err != nil {
				return err
			}
			usernameKey, _ := localAdminEnvKeys(mod, declared.ID)
			a.env[usernameKey] = record.Username
			a.setEnvOwner(usernameKey, module)
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
