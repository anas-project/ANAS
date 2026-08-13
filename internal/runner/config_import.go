package runner

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

type importedConfigSecret struct {
	Path      string
	Key       string
	Value     string
	Owner     string
	Generated bool
}

type configImportResult struct {
	Normalized []byte
	Secrets    []importedConfigSecret
}

type managedConfigState struct {
	APIVersion string `yaml:"api_version"`
	Digest     string `yaml:"digest"`
	UpdatedBy  string `yaml:"updated_by"`
}

func managedConfigStatePath(base string) string { return filepath.Join(base, "config-managed.yml") }

func managedConfigStateBytes(configBytes []byte, updatedBy string) []byte {
	state := managedConfigState{APIVersion: "anas.config/v1", Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(configBytes)), UpdatedBy: updatedBy}
	b, _ := yaml.Marshal(&state)
	return b
}

func writeManagedConfigState(workspace, updatedBy string) error {
	b, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		return err
	}
	stateBytes := managedConfigStateBytes(b, updatedBy)
	var state managedConfigState
	if err := yaml.Unmarshal(stateBytes, &state); err != nil {
		return err
	}
	return writeYAMLAtomic(managedConfigStatePath(stateDir(workspace)), &state, 0600)
}

func setManagedConfigScalar(workspace, configPath string, yamlPath []string, value string) error {
	if err := validateManagedConfig(workspace, configPath); err != nil {
		return err
	}
	base := stateDir(workspace)
	tmp, err := os.CreateTemp(base, ".config-set-*.yml")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		return err
	}
	defer os.Remove(tmpPath)
	current, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmpPath, current, 0600); err != nil {
		return err
	}
	if err := config.SetScalar(tmpPath, yamlPath, value); err != nil {
		return err
	}
	next, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	return commitImportedFiles([]importFile{
		{path: configPath, data: next, mode: 0600},
		{path: managedConfigStatePath(base), data: managedConfigStateBytes(next, "config-set"), mode: 0600},
	})
}

func validateManagedConfig(workspace, configPath string) error {
	wantPath := workspaceConfigPath(workspace)
	abs, err := filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if abs != wantPath {
		return fmt.Errorf("external config is import-only; run `anas config import %s -w %s` and use %s", abs, workspace, wantPath)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	stateBytes, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace config predates managed config state; run `anas config migrate -w %s`", workspace)
		}
		return err
	}
	var state managedConfigState
	if err := yaml.Unmarshal(stateBytes, &state); err != nil || state.APIVersion != "anas.config/v1" {
		return fmt.Errorf("workspace managed-config state is invalid; re-import the external source")
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	if digest != state.Digest {
		return fmt.Errorf("workspace config was modified outside ANAS CLI; re-import the external source or restore it using `anas config` commands")
	}
	return nil
}

func rejectUnimportedConfigSecrets(path string, reg map[string]Module) error {
	result, err := normalizeImportedConfig(path, reg)
	if err != nil {
		return err
	}
	if len(result.Secrets) == 0 {
		return nil
	}
	paths := make([]string, 0, len(result.Secrets))
	for _, secret := range result.Secrets {
		paths = append(paths, secret.Path)
	}
	return fmt.Errorf("config contains secret input at %s; run `anas config import %s -w <workspace>` (or `anas config migrate -w <workspace>` for an existing workspace)", strings.Join(paths, ", "), path)
}

func normalizeImportedConfig(source string, reg map[string]Module) (configImportResult, error) {
	b, err := os.ReadFile(source)
	if err != nil {
		return configImportResult{}, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return configImportResult{}, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return configImportResult{}, fmt.Errorf("config root must be a mapping")
	}
	root := doc.Content[0]
	result := configImportResult{}

	sensitiveEnv := map[string]bool{}
	localEnv := map[string]importedConfigSecret{}
	for _, mod := range reg {
		for parameter, policy := range mod.Changes {
			if policy.Effect != "credential_rotate" {
				continue
			}
			key := mod.EnvPrefix + "_" + config.EnvKey(parameter)
			if bare, ok := mod.bareEnvParameter(parameter); ok {
				key = bare
			}
			sensitiveEnv[key] = true
		}
		if len(mod.LocalAccounts) == 1 {
			account := mod.LocalAccounts[0]
			secret := importedConfigSecret{Key: localAdminSecretKey(mod.Name, account.ID), Owner: mod.Name, Generated: true}
			_, localPasswordKey := localAdminEnvKeys(mod, account.ID)
			for _, alias := range []string{localPasswordKey, mod.EnvPrefix + "_ADMIN_PASSWORD", mod.EnvPrefix + "_ADMIN_PWD"} {
				localEnv[alias] = secret
			}
		}
	}

	if env := mappingValue(root, "env"); env != nil {
		if env.Kind != yaml.MappingNode {
			return result, fmt.Errorf("env must be a mapping")
		}
		for i := 0; i+1 < len(env.Content); {
			key := config.EnvKey(env.Content[i].Value)
			local, isLocal := localEnv[key]
			if !isLocal && !sensitiveEnv[key] {
				i += 2
				continue
			}
			value, err := secretScalar(env.Content[i+1], "env."+env.Content[i].Value)
			if err != nil {
				return result, err
			}
			secret := importedConfigSecret{Path: "env." + env.Content[i].Value, Key: key, Value: value, Owner: "runner"}
			if isLocal {
				secret.Key, secret.Owner, secret.Generated = local.Key, local.Owner, true
			}
			result.Secrets = append(result.Secrets, secret)
			env.Content = append(env.Content[:i], env.Content[i+2:]...)
		}
		if len(env.Content) == 0 {
			removeMappingKey(root, "env")
		}
	}

	modules := mappingValue(root, "modules")
	if modules != nil && modules.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(modules.Content); i += 2 {
			name := strings.ToLower(strings.TrimSpace(modules.Content[i].Value))
			mod, ok := reg[name]
			if !ok || modules.Content[i+1].Kind != yaml.MappingNode {
				continue
			}
			moduleNode := modules.Content[i+1]
			if configNode := mappingValue(moduleNode, "config"); configNode != nil && configNode.Kind == yaml.MappingNode {
				for j := 0; j+1 < len(configNode.Content); {
					parameter := strings.ToLower(strings.TrimSpace(configNode.Content[j].Value))
					policy, declared := mod.Changes[parameter]
					isManagedBootstrap := parameter == "admin_password" && len(mod.LocalAccounts) == 1
					if (!declared || policy.Effect != "credential_rotate") && !isManagedBootstrap {
						j += 2
						continue
					}
					path := "modules." + name + ".config." + parameter
					value, err := secretScalar(configNode.Content[j+1], path)
					if err != nil {
						return result, err
					}
					key := mod.EnvPrefix + "_" + config.EnvKey(parameter)
					generated := false
					if isManagedBootstrap {
						key, generated = localAdminSecretKey(name, mod.LocalAccounts[0].ID), true
					} else if bare, ok := mod.bareEnvParameter(parameter); ok {
						key = bare
					}
					result.Secrets = append(result.Secrets, importedConfigSecret{Path: path, Key: key, Value: value, Owner: name, Generated: generated})
					configNode.Content = append(configNode.Content[:j], configNode.Content[j+2:]...)
				}
				if len(configNode.Content) == 0 {
					removeMappingKey(moduleNode, "config")
				}
			}

			admin := mappingValue(moduleNode, "administration")
			accounts := mappingValue(admin, "local_accounts")
			if accounts == nil || accounts.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(accounts.Content); j += 2 {
				id := strings.TrimSpace(accounts.Content[j].Value)
				accountNode := accounts.Content[j+1]
				if mappingValue(accountNode, "username") != nil {
					return result, fmt.Errorf("modules.%s.administration.local_accounts.%s.username is not configurable; the Module fixed_username or ANAS default determines the locked username", name, id)
				}
				passwordNode := mappingValue(accountNode, "password")
				if passwordNode == nil {
					continue
				}
				if !moduleHasLocalAccount(mod, id) {
					return result, fmt.Errorf("modules.%s.administration.local_accounts.%s is not a manifest account id", name, id)
				}
				path := "modules." + name + ".administration.local_accounts." + id + ".password"
				value, err := secretScalar(passwordNode, path)
				if err != nil {
					return result, err
				}
				result.Secrets = append(result.Secrets, importedConfigSecret{Path: path, Key: localAdminSecretKey(name, id), Value: value, Owner: name, Generated: true})
				removeMappingKey(accountNode, "password")
			}
		}
	}

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return result, err
	}
	if err := enc.Close(); err != nil {
		return result, err
	}
	result.Normalized = out.Bytes()
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].Path < result.Secrets[j].Path })
	return result, nil
}

func secretScalar(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || strings.TrimSpace(node.Value) == "" {
		return "", fmt.Errorf("%s must be a non-empty scalar secret", path)
	}
	return node.Value, nil
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func removeMappingKey(mapping *yaml.Node, key string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func moduleHasLocalAccount(mod Module, id string) bool {
	for _, account := range mod.LocalAccounts {
		if account.ID == id {
			return true
		}
	}
	return false
}

func importConfigIntoWorkspace(workspace, source string, reg map[string]Module) (configImportResult, error) {
	result, err := normalizeImportedConfig(source, reg)
	if err != nil {
		return result, err
	}
	base := stateDir(workspace)
	if err := os.MkdirAll(base, 0700); err != nil {
		return result, err
	}
	validation := filepath.Join(base, ".config-import-validation.yml")
	if err := os.WriteFile(validation, result.Normalized, 0600); err != nil {
		return result, err
	}
	defer os.Remove(validation)
	if _, err := config.Load(validation); err != nil {
		return result, fmt.Errorf("normalized config is invalid: %w", err)
	}

	store, err := loadSecretStore(base)
	if err != nil {
		return result, err
	}
	for _, secret := range result.Secrets {
		if old := store.values[secret.Key]; old != "" && old != secret.Value {
			command := "the declared credential rotation command"
			if secret.Generated {
				command = "`anas admin local rotate`"
			}
			return result, fmt.Errorf("%s is already lifecycle-managed; use %s instead of importing a replacement", secret.Path, command)
		}
		if secret.Generated {
			store.SetWithMetadata(secret.Key, secret.Value, secretMetadata{Owner: secret.Owner, Kind: "local_admin", Provenance: "config-import:" + secret.Path})
		} else {
			store.SetWithMetadata(secret.Key, secret.Value, secretMetadata{Owner: secret.Owner, Kind: "lifecycle_managed", Provenance: "config-import:" + secret.Path})
		}
	}

	files := []importFile{
		{path: workspaceConfigPath(workspace), data: result.Normalized, mode: 0600},
		{path: store.path, data: marshalSecretStore(store), mode: 0600},
		{path: managedConfigStatePath(base), data: managedConfigStateBytes(result.Normalized, "config-import"), mode: 0600},
	}
	if err := commitImportedFiles(files); err != nil {
		return result, err
	}
	return result, nil
}

type importFile struct {
	path string
	data []byte
	mode os.FileMode
}

func marshalSecretStore(store *secretStore) []byte {
	doc := secretStoreDocument{APIVersion: "anas.secrets/v2", Secrets: map[string]secretStoreRecord{}}
	for key, value := range store.values {
		meta := store.metadata[key]
		doc.Secrets[key] = secretStoreRecord{Value: value, Owner: meta.Owner, Kind: meta.Kind, Provenance: meta.Provenance}
	}
	b, _ := yaml.Marshal(&doc)
	return b
}

// commitImportedFiles stages every output before replacing any target. If a
// replacement fails, already-replaced files are restored from their in-memory
// originals, so a reported import failure leaves the workspace unchanged.
func commitImportedFiles(files []importFile) error {
	type stagedFile struct {
		importFile
		tmp       string
		old       []byte
		oldMode   os.FileMode
		hadTarget bool
	}
	staged := make([]stagedFile, 0, len(files))
	cleanup := func() {
		for _, file := range staged {
			_ = os.Remove(file.tmp)
		}
	}
	defer cleanup()
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), 0700); err != nil {
			return err
		}
		entry := stagedFile{importFile: file}
		if info, err := os.Stat(file.path); err == nil {
			entry.hadTarget, entry.oldMode = true, info.Mode().Perm()
			entry.old, err = os.ReadFile(file.path)
			if err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(file.path), ".anas-import-*.tmp")
		if err != nil {
			return err
		}
		entry.tmp = tmp.Name()
		if err := tmp.Chmod(file.mode); err != nil {
			tmp.Close()
			return err
		}
		if _, err := tmp.Write(file.data); err != nil {
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
		staged = append(staged, entry)
	}
	for i := range staged {
		if err := os.Rename(staged[i].tmp, staged[i].path); err != nil {
			for j := 0; j < i; j++ {
				if staged[j].hadTarget {
					_ = os.WriteFile(staged[j].path, staged[j].old, staged[j].oldMode)
				} else {
					_ = os.Remove(staged[j].path)
				}
			}
			return err
		}
		staged[i].tmp = ""
	}
	return nil
}
