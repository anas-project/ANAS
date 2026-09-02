package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/modulesource"
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

// importedSecretSet keeps all accepted one-time credential spellings on one
// canonical environment key. Without this, two paths could supply the same
// lifecycle-managed value and whichever happened to sort last would win.
// Collision diagnostics name paths only and therefore cannot echo a secret.
type importedSecretSet struct {
	values []importedConfigSecret
	paths  map[string]string
}

func (s *importedSecretSet) add(secret importedConfigSecret) error {
	secret.Key = config.EnvKey(secret.Key)
	if s.paths == nil {
		s.paths = map[string]string{}
	}
	if previous, exists := s.paths[secret.Key]; exists {
		return fmt.Errorf("%s and %s both provide lifecycle-managed secret %s", previous, secret.Path, secret.Key)
	}
	s.paths[secret.Key] = secret.Path
	s.values = append(s.values, secret)
	return nil
}

type managedConfigState struct {
	APIVersion    string `yaml:"api_version"`
	ContentDigest string `yaml:"digest"`
	Validator     string `yaml:"validator,omitempty"`
	UpdatedBy     string `yaml:"updated_by"`
}

type configCandidateValidationError struct{ err error }

func (e *configCandidateValidationError) Error() string { return e.err.Error() }
func (e *configCandidateValidationError) Unwrap() error { return e.err }

type configCandidateLockFileError struct{ err error }

func (e *configCandidateLockFileError) Error() string { return e.err.Error() }
func (e *configCandidateLockFileError) Unwrap() error { return e.err }

type configCandidateSecretStoreError struct{ err error }

func (e *configCandidateSecretStoreError) Error() string { return e.err.Error() }
func (e *configCandidateSecretStoreError) Unwrap() error { return e.err }

func candidateConfigInvalid(err error) error {
	if err == nil {
		return nil
	}
	return &configCandidateValidationError{err: err}
}

func managedConfigStatePath(base string) string { return filepath.Join(base, "config-managed.yml") }

func newManagedConfigValidator() (string, error) {
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "cfgv-" + hex.EncodeToString(random[:]), nil
}

func validManagedConfigValidator(value string) bool {
	if len(value) != len("cfgv-")+64 || !strings.HasPrefix(value, "cfgv-") {
		return false
	}
	_, err := hex.DecodeString(value[len("cfgv-"):])
	return err == nil
}

func managedConfigStateBytes(configBytes []byte, updatedBy string) ([]byte, error) {
	validator, err := newManagedConfigValidator()
	if err != nil {
		return nil, err
	}
	return managedConfigStateBytesWithValidator(configBytes, updatedBy, validator)
}

func managedConfigStateBytesWithValidator(configBytes []byte, updatedBy, validator string) ([]byte, error) {
	if !validManagedConfigValidator(validator) {
		return nil, errors.New("managed config validator is invalid")
	}
	state := managedConfigState{
		APIVersion: "anas.config/v1", ContentDigest: fmt.Sprintf("sha256:%x", sha256.Sum256(configBytes)),
		Validator: validator, UpdatedBy: updatedBy,
	}
	return yaml.Marshal(&state)
}

func writeManagedConfigState(workspace, updatedBy string) error {
	b, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		return err
	}
	stateBytes, err := managedConfigStateBytes(b, updatedBy)
	if err != nil {
		return err
	}
	var state managedConfigState
	if err := yaml.Unmarshal(stateBytes, &state); err != nil {
		return err
	}
	return writeYAMLAtomic(managedConfigStatePath(stateDir(workspace)), &state, 0600)
}

func setManagedConfigScalar(workspace, configPath string, yamlPath []string, value string, preserveText bool, reg map[string]Module) error {
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
	set := config.SetScalar
	if preserveText {
		set = config.SetString
	}
	if err := set(tmpPath, yamlPath, value); err != nil {
		return candidateConfigInvalid(err)
	}
	if err := validateConfigRuntimeKeyCollisions(tmpPath, reg); err != nil {
		return candidateConfigInvalid(err)
	}
	loaded, err := config.Load(tmpPath)
	if err != nil {
		return candidateConfigInvalid(err)
	}
	store, err := loadSecretStore(base)
	if err != nil {
		return &configCandidateSecretStoreError{err: err}
	}
	if err := validateConfiguredParameterSchema(loaded, reg, store.values); err != nil {
		return candidateConfigInvalid(err)
	}
	lock, err := loadModuleLockFile(projectLockPath(configPath))
	if err != nil {
		return &configCandidateLockFileError{err: err}
	}
	if err := validateDeferredModuleHooks(loaded, reg, store, lock); err != nil {
		return candidateConfigInvalid(err)
	}
	next, err := os.ReadFile(tmpPath)
	if err != nil {
		return err
	}
	stateBytes, err := managedConfigStateBytes(next, "config-set")
	if err != nil {
		return err
	}
	return commitImportedFiles([]importFile{
		{path: configPath, data: next, mode: 0600},
		{path: managedConfigStatePath(base), data: stateBytes, mode: 0600},
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
	if digest != state.ContentDigest {
		return fmt.Errorf("workspace config was modified outside ANAS CLI; re-import the external source or restore it using `anas config` commands")
	}
	return nil
}

func rejectUnimportedConfigSecrets(path string, reg map[string]Module, privateTaint ...map[string]string) error {
	var sources map[string]string
	if len(privateTaint) > 0 {
		sources = privateTaint[0]
	}
	result, err := normalizeImportedConfigWithTaint(path, reg, sources)
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
	return normalizeImportedConfigWithTaint(source, reg, nil)
}

// normalizeImportedConfigWithTaint is used when importing into an existing
// workspace. Its private sources never become configuration inputs; they only
// ensure that early node-level type normalization cannot echo a value already
// protected by any Secret Store record kind.
func normalizeImportedConfigWithTaint(source string, reg map[string]Module, privateTaint map[string]string) (configImportResult, error) {
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
	importedSecrets := importedSecretSet{}
	if err := canonicalizeImportedConfigKeys(root, reg); err != nil {
		return result, err
	}

	// Persist the CN source's runtime defaults into normalized desired state.
	// This makes the automatic behavior visible to `config plan`, backups and
	// operators instead of leaving it as an in-memory side effect of resolution.
	sourceNode := mappingValue(root, "module_source")
	if sourceNode == nil {
		sourceNode = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: modulesource.InstalledDefaultName("")}
		root.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "module_source"}, sourceNode,
		}, root.Content...)
	}
	if sourceNode != nil {
		if sourceNode.Kind != yaml.ScalarNode {
			return result, fmt.Errorf("module_source must be a scalar")
		}
		sourceNode.Value = modulesource.DefaultName(sourceNode.Value)
		if _, ok := modulesource.LookupBuiltin(sourceNode.Value); !ok {
			return result, fmt.Errorf("module_source must be official, official-cn, or cn")
		}
		if modulesource.UsesChineseDefaults(sourceNode.Value) {
			global := ensureMappingValue(root, "global")
			if global.Kind != yaml.MappingNode {
				return result, fmt.Errorf("global must be a mapping")
			}
			chineseSpeedup := mappingValue(global, "chinese_speedup")
			rawEnv := mappingValue(root, "env")
			rawSecrets := mappingValue(root, "secrets")
			explicitRuntimeValue := mappingValue(rawEnv, "CHINESE_SPEEDUP") != nil || mappingValue(rawSecrets, "CHINESE_SPEEDUP") != nil
			if chineseSpeedup == nil && !explicitRuntimeValue {
				global.Content = append(global.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "chinese_speedup"},
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
			} else if chineseSpeedup != nil && chineseSpeedup.Kind == yaml.ScalarNode &&
				(chineseSpeedup.Tag == "!!null" || strings.TrimSpace(chineseSpeedup.Value) == "") {
				// YAML null and the empty optional-bool spelling have the same
				// meaning as omission; persist the resolved default instead.
				chineseSpeedup.Tag = "!!bool"
				chineseSpeedup.Value = "true"
			}
		}
	}

	sensitiveEnv := map[string]bool{}
	localEnv := map[string]importedConfigSecret{}
	for _, mod := range reg {
		for parameter, policy := range mod.Changes {
			if policy.Effect != "credential_rotate" {
				continue
			}
			key := moduleParamEnvKey(mod.Name, mod.EnvPrefix, mod.Exports, parameter)
			sensitiveEnv[key] = true
		}
		if len(mod.LocalAccounts) == 1 {
			account := mod.LocalAccounts[0]
			secret := importedConfigSecret{Key: localAdminSecretKey(mod.Name, account.ID), Owner: mod.Name, Generated: true}
			_, localPasswordKey := localAdminEnvKeys(mod, account.ID)
			prefix := defaultEnvPrefix(mod.EnvPrefix)
			for _, alias := range []string{localPasswordKey, prefix + "_ADMIN_PASSWORD", prefix + "_ADMIN_PWD"} {
				localEnv[alias] = secret
			}
		}
	}

	// env: and secrets: are two input spellings for the same runtime key. Both
	// must therefore use the same declaration lookup, schema normalization and
	// lifecycle extraction. Ordinary secrets remain in desired config; only
	// credential_rotate and managed-bootstrap values move to the Secret Store.
	extractLifecycleMapping := func(section string) error {
		mapping := mappingValue(root, section)
		if mapping == nil {
			return nil
		}
		if mapping.Kind != yaml.MappingNode {
			return fmt.Errorf("%s must be a mapping", section)
		}
		for i := 0; i+1 < len(mapping.Content); {
			key := config.EnvKey(mapping.Content[i].Value)
			local, isLocal := localEnv[key]
			if !isLocal && !sensitiveEnv[key] {
				i += 2
				continue
			}
			path := section + "." + key
			value, err := secretScalar(mapping.Content[i+1], path)
			if err != nil {
				return err
			}
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			if isLocal {
				// The compatibility aliases represent the Module's bootstrap
				// administrator password when it declares that parameter. A Module
				// without such a declaration simply has no additional type to apply.
				module, parameter = local.Owner, "admin_password"
			}
			value, err = normalizeImportedSensitiveParameter(path, module, parameter, value, reg)
			if err != nil {
				return err
			}
			secret := importedConfigSecret{Path: path, Key: key, Value: value, Owner: module}
			if isLocal {
				secret.Key, secret.Owner, secret.Generated = local.Key, local.Owner, true
			}
			if err := importedSecrets.add(secret); err != nil {
				return err
			}
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
		}
		if len(mapping.Content) == 0 {
			removeMappingKey(root, section)
		}
		return nil
	}
	for _, section := range []string{"env", "secrets"} {
		if err := extractLifecycleMapping(section); err != nil {
			return result, err
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
					value, err = normalizeImportedSensitiveParameter(path, name, parameter, value, reg)
					if err != nil {
						return result, err
					}
					key := moduleParamEnvKey(name, mod.EnvPrefix, mod.Exports, parameter)
					generated := false
					if isManagedBootstrap {
						key, generated = localAdminSecretKey(name, mod.LocalAccounts[0].ID), true
					}
					if err := importedSecrets.add(importedConfigSecret{Path: path, Key: key, Value: value, Owner: name, Generated: generated}); err != nil {
						return result, err
					}
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
				if err := importedSecrets.add(importedConfigSecret{Path: path, Key: localAdminSecretKey(name, id), Value: value, Owner: name, Generated: true}); err != nil {
					return result, err
				}
				removeMappingKey(accountNode, "password")
			}
		}
	}
	extractedTaint := make(map[string]string, len(importedSecrets.values))
	for _, secret := range importedSecrets.values {
		extractedTaint[secret.Key] = secret.Value
	}
	if err := normalizeImportedParameterNodes(root, reg, privateTaint, extractedTaint); err != nil {
		return result, err
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
	result.Secrets = importedSecrets.values
	sort.Slice(result.Secrets, func(i, j int) bool { return result.Secrets[i].Path < result.Secrets[j].Path })
	return result, nil
}

// normalizeImportedParameterNodes persists the same canonical value that the
// runtime consumes. This keeps an imported legacy selector such as MARIADB
// compatible without leaving the managed config and its rendered environment
// with different spellings. The traversal is entirely manifest-driven.
func normalizeImportedParameterNodes(root *yaml.Node, reg map[string]Module, privateTaintSources ...map[string]string) error {
	privateValues := map[string]bool{}
	for _, source := range privateTaintSources {
		for _, value := range source {
			addSensitiveValueForms(privateValues, value)
		}
	}
	normalize := func(path, module, parameter string, value *yaml.Node, sourceSensitive bool) error {
		spec := parameterType(module, parameter, reg)
		if !spec.Declared() {
			return nil
		}
		if value == nil {
			return fmt.Errorf("%s must be a scalar %s", path, spec.Kind)
		}
		normalizeValue := func(raw string) (string, error) {
			if sourceSensitive || matchesSensitiveValue(privateValues, raw) || policyForTarget(configTarget{Display: path, Module: module, Parameter: parameter}, reg).Sensitive {
				return normalizeImportedSensitiveParameter(path, module, parameter, raw, reg)
			}
			normalized, err := normalizeParameterValue(module, parameter, raw, reg)
			if err != nil {
				return "", fmt.Errorf("%s: %w", path, err)
			}
			return normalized, nil
		}
		if value.Kind == yaml.AliasNode {
			// Do not mutate the shared anchor: it may also feed an unrelated setting.
			// Expand this declared parameter to its own canonical scalar instead so
			// the managed YAML, config list and runtime environment all observe the
			// same value even when the anchor itself is not a declared parameter.
			target := value.Alias
			seen := map[*yaml.Node]bool{}
			for target != nil && target.Kind == yaml.AliasNode && !seen[target] {
				seen[target] = true
				target = target.Alias
			}
			if target != nil && target.Kind == yaml.ScalarNode {
				raw := target.Value
				if target.Tag == "!!null" {
					raw = ""
				}
				normalized, err := normalizeValue(raw)
				if err != nil {
					return err
				}
				// Null is the explicit unset spelling and remains an alias so its YAML
				// semantics are preserved. Every non-null declared value is persisted
				// as canonical text; config.Load accepts this uniformly for every kind.
				if target.Tag != "!!null" {
					*value = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: normalized}
				}
				return nil
			}
			return fmt.Errorf("%s must be a scalar %s", path, spec.Kind)
		}
		if value.Kind != yaml.ScalarNode {
			return fmt.Errorf("%s must be a scalar %s", path, spec.Kind)
		}
		raw := value.Value
		if value.Tag == "!!null" {
			// yaml.Node retains the lexical null spelling ("null" or "~"),
			// while config.Load decodes it as the empty/unset value. Validate the
			// same semantic value and preserve the null node in managed YAML.
			raw = ""
		}
		normalized, err := normalizeValue(raw)
		if err != nil {
			return err
		}
		if value.Tag == "!!null" {
			return nil
		}
		value.Value = normalized
		if spec.Kind == "string" {
			value.Tag = "!!str"
		}
		return nil
	}

	if env := mappingValue(root, "env"); env != nil && env.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(env.Content); i += 2 {
			key := config.EnvKey(env.Content[i].Value)
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return fmt.Errorf("env.%s: %w", key, err)
			}
			if err := normalize("env."+key, module, parameter, env.Content[i+1], false); err != nil {
				return err
			}
		}
	}
	if secrets := mappingValue(root, "secrets"); secrets != nil && secrets.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(secrets.Content); i += 2 {
			key := config.EnvKey(secrets.Content[i].Value)
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return fmt.Errorf("secrets.%s: %w", key, err)
			}
			if err := normalize("secrets."+key, module, parameter, secrets.Content[i+1], true); err != nil {
				return err
			}
		}
	}
	if global := mappingValue(root, "global"); global != nil && global.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(global.Content); i += 2 {
			parameter := strings.ToLower(strings.TrimSpace(global.Content[i].Value))
			if err := normalize("global."+parameter, globalModuleName, parameter, global.Content[i+1], false); err != nil {
				return err
			}
		}
	}

	modules := mappingValue(root, "modules")
	if modules == nil || modules.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(modules.Content); i += 2 {
		module := strings.ToLower(strings.TrimSpace(modules.Content[i].Value))
		moduleNode := modules.Content[i+1]
		if identity := mappingValue(moduleNode, "identity"); identity != nil && identity.Kind == yaml.MappingNode {
			if login := mappingValue(identity, "login_protocol"); login != nil {
				parameter, ok := moduleIdentityLoginParameter(module, reg)
				if !ok {
					return fmt.Errorf("modules.%s.identity.login_protocol is not declared by the Module identity contract", module)
				}
				path := "modules." + module + ".identity.login_protocol"
				if err := normalize(path, module, parameter, login, false); err != nil {
					return err
				}
			}
		}
		configNode := mappingValue(moduleNode, "config")
		if configNode == nil || configNode.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(configNode.Content); j += 2 {
			parameter := strings.ToLower(strings.TrimSpace(configNode.Content[j].Value))
			path := "modules." + module + ".config." + parameter
			if err := normalize(path, module, parameter, configNode.Content[j+1], false); err != nil {
				return err
			}
		}
	}
	return nil
}

func secretScalar(node *yaml.Node, path string) (string, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" || strings.TrimSpace(node.Value) == "" {
		return "", fmt.Errorf("%s must be a non-empty scalar secret", path)
	}
	return node.Value, nil
}

// normalizeImportedSensitiveParameter applies the same shared schema before a
// lifecycle-managed value is removed from the config tree. Errors deliberately
// omit the rejected value: import diagnostics must never echo a credential.
func normalizeImportedSensitiveParameter(path, module, parameter, value string, reg map[string]Module) (string, error) {
	spec := parameterType(module, parameter, reg)
	if !spec.Declared() {
		return value, nil
	}
	normalized, err := spec.Normalize(value)
	if err != nil {
		return "", fmt.Errorf("%s does not satisfy its declared %s type or constraints", path, spec.Kind)
	}
	return normalized, nil
}

// canonicalizeImportedConfigKeys gives every imported setting one stable YAML
// address before secrets are extracted or values are validated. Module and
// parameter names are case-folded, raw environment keys use their runtime
// spelling, and a parameter exported without a Module prefix is moved out of
// modules.<module>.config into the top-level env mapping that BaseEnv reads.
func canonicalizeImportedConfigKeys(root *yaml.Node, reg map[string]Module) error {
	env := mappingValue(root, "env")
	if env != nil && env.Kind != yaml.MappingNode {
		return fmt.Errorf("env must be a mapping")
	}
	envKeys := map[string]string{}
	if env != nil {
		var err error
		envKeys, err = canonicalizeImportedMappingKeys(env, "env", config.EnvKey)
		if err != nil {
			return err
		}
	}
	secrets := mappingValue(root, "secrets")
	if secrets != nil && secrets.Kind != yaml.MappingNode {
		return fmt.Errorf("secrets must be a mapping")
	}
	secretKeys := map[string]string{}
	if secrets != nil {
		var err error
		secretKeys, err = canonicalizeImportedMappingKeys(secrets, "secrets", config.EnvKey)
		if err != nil {
			return err
		}
		for key, sourcePath := range secretKeys {
			if previous, duplicate := envKeys[key]; duplicate {
				return fmt.Errorf("%s and %s both resolve to runtime key %s after canonicalization", previous, sourcePath, key)
			}
		}
	}
	runtimeKeys := make(map[string]string, len(envKeys)+len(secretKeys))
	registerRuntimeKey := func(key, sourcePath string) error {
		if previous, duplicate := runtimeKeys[key]; duplicate {
			return fmt.Errorf("%s and %s both resolve to runtime key %s after canonicalization", previous, sourcePath, key)
		}
		runtimeKeys[key] = sourcePath
		return nil
	}
	for key, sourcePath := range envKeys {
		if err := registerRuntimeKey(key, sourcePath); err != nil {
			return err
		}
	}
	for key, sourcePath := range secretKeys {
		if err := registerRuntimeKey(key, sourcePath); err != nil {
			return err
		}
	}

	if global := mappingValue(root, "global"); global != nil {
		if global.Kind != yaml.MappingNode {
			return fmt.Errorf("global must be a mapping")
		}
		globalKeys, err := canonicalizeImportedMappingKeys(global, "global", canonicalConfigName)
		if err != nil {
			return err
		}
		for parameter, sourcePath := range globalKeys {
			if err := registerRuntimeKey(parameterEnvKey(globalModuleName, parameter, reg), sourcePath); err != nil {
				return err
			}
		}
	}
	if administration := mappingValue(root, "administration"); administration != nil && administration.Kind == yaml.MappingNode {
		if bootstrap := mappingValue(administration, "bootstrap"); bootstrap != nil && bootstrap.Kind == yaml.MappingNode {
			if username := mappingValue(bootstrap, "username"); username != nil {
				for _, key := range bootstrapUsernameRuntimeKeys(reg) {
					if err := registerRuntimeKey(key, "administration.bootstrap.username"); err != nil {
						return err
					}
				}
			}
		}
	}

	modules := mappingValue(root, "modules")
	if modules == nil {
		return nil
	}
	if modules.Kind != yaml.MappingNode {
		return fmt.Errorf("modules must be a mapping")
	}
	if _, err := canonicalizeImportedMappingKeys(modules, "modules", canonicalConfigName); err != nil {
		return err
	}

	for i := 0; i+1 < len(modules.Content); i += 2 {
		module := modules.Content[i].Value
		moduleNode := modules.Content[i+1]
		if moduleNode.Kind != yaml.MappingNode {
			continue
		}
		mod, known := reg[module]
		identity := mappingValue(moduleNode, "identity")
		var identityLogin *yaml.Node
		if identity != nil {
			if identity.Kind != yaml.MappingNode {
				return fmt.Errorf("modules.%s.identity must be a mapping", module)
			}
			if _, err := canonicalizeImportedMappingKeys(identity, "modules."+module+".identity", canonicalConfigName); err != nil {
				return err
			}
			identityLogin = mappingValue(identity, "login_protocol")
			if identityLogin != nil && known {
				key, supported := moduleIdentityLoginRuntimeKey(module, reg)
				if !supported {
					return fmt.Errorf("modules.%s.identity.login_protocol is not declared by the Module identity contract", module)
				}
				if err := registerRuntimeKey(key, "modules."+module+".identity.login_protocol"); err != nil {
					return err
				}
			}
		}
		configNode := mappingValue(moduleNode, "config")
		if configNode == nil {
			continue
		}
		if configNode.Kind != yaml.MappingNode {
			return fmt.Errorf("modules.%s.config must be a mapping", module)
		}
		if _, err := canonicalizeImportedMappingKeys(configNode, "modules."+module+".config", canonicalConfigName); err != nil {
			return err
		}
		if !known {
			continue
		}
		for j := 0; j+1 < len(configNode.Content); {
			parameter := configNode.Content[j].Value
			sourcePath := "modules." + module + ".config." + parameter
			runtimeKey := moduleParamEnvKey(module, mod.EnvPrefix, mod.Exports, parameter)
			if err := registerRuntimeKey(runtimeKey, sourcePath); err != nil {
				return err
			}
			if identityParameter, supported := moduleIdentityLoginParameter(module, reg); supported && parameter == identityParameter {
				if identity == nil {
					identity = ensureMappingValue(moduleNode, "identity")
				}
				keyNode, valueNode := configNode.Content[j], configNode.Content[j+1]
				keyNode.Tag = "!!str"
				keyNode.Value = "login_protocol"
				identity.Content = append(identity.Content, keyNode, valueNode)
				identityLogin = valueNode
				configNode.Content = append(configNode.Content[:j], configNode.Content[j+2:]...)
				continue
			}
			key, bare := mod.bareEnvParameter(parameter)
			if !bare {
				j += 2
				continue
			}
			if env == nil {
				env = ensureMappingValue(root, "env")
			}
			keyNode, valueNode := configNode.Content[j], configNode.Content[j+1]
			keyNode.Tag = "!!str"
			keyNode.Value = key
			env.Content = append(env.Content, keyNode, valueNode)
			envKeys[key] = sourcePath
			configNode.Content = append(configNode.Content[:j], configNode.Content[j+2:]...)
		}
		if len(configNode.Content) == 0 {
			removeMappingKey(moduleNode, "config")
		}
	}
	return nil
}

func canonicalizeImportedMappingKeys(mapping *yaml.Node, path string, canonical func(string) string) (map[string]string, error) {
	seen := make(map[string]string, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		keyNode := mapping.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("%s contains a non-scalar key", path)
		}
		raw := strings.TrimSpace(keyNode.Value)
		key := canonical(raw)
		if key == "" {
			return nil, fmt.Errorf("%s contains an empty key", path)
		}
		sourcePath := path + "." + raw
		if previous, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%s and %s both normalize to %s.%s after canonicalization", previous, sourcePath, path, key)
		}
		seen[key] = sourcePath
		keyNode.Tag = "!!str"
		keyNode.Value = key
	}
	return seen, nil
}

func canonicalConfigName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
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

func ensureMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if value := mappingValue(mapping, key); value != nil {
		return value
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return value
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
	base := stateDir(workspace)
	store, err := loadSecretStore(base)
	if err != nil {
		return configImportResult{}, err
	}
	result, err := normalizeImportedConfigWithTaint(source, reg, store.values)
	if err != nil {
		return result, err
	}
	lock, err := loadModuleLockFile(projectLockPath(workspaceConfigPath(workspace)))
	if err != nil {
		return result, fmt.Errorf("module lock: %w", err)
	}
	validationSecrets := make([]importedConfigSecret, 0, len(store.values)+len(result.Secrets))
	for key, value := range store.lifecycleManagedValues() {
		validationSecrets = append(validationSecrets, importedConfigSecret{Path: "secret-store." + key, Key: key, Value: value})
	}
	// Values explicitly supplied by this import are validated as the effective
	// input. The replacement guard below remains authoritative when an existing
	// lifecycle value differs, so validation does not grant rotation by import.
	validationSecrets = append(validationSecrets, result.Secrets...)
	if err := validateNormalizedImportedConfigWithLockAndTaint(result.Normalized, reg, lock, store.values, validationSecrets...); err != nil {
		return result, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
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

	stateBytes, err := managedConfigStateBytes(result.Normalized, "config-import")
	if err != nil {
		return result, err
	}
	files := []importFile{
		{path: workspaceConfigPath(workspace), data: result.Normalized, mode: 0600},
		{path: store.path, data: marshalSecretStore(store), mode: 0600},
		{path: managedConfigStatePath(base), data: stateBytes, mode: 0600},
	}
	if err := commitImportedFiles(files); err != nil {
		return result, err
	}
	return result, nil
}

func validateConfigImportSource(source string, reg map[string]Module) error {
	result, err := normalizeImportedConfig(source, reg)
	if err != nil {
		return err
	}
	return validateNormalizedImportedConfig(result.Normalized, reg, result.Secrets...)
}

func validateNormalizedImportedConfig(normalized []byte, reg map[string]Module, extractedSecrets ...importedConfigSecret) error {
	return validateNormalizedImportedConfigWithLock(normalized, reg, nil, extractedSecrets...)
}

func validateNormalizedImportedConfigWithLock(normalized []byte, reg map[string]Module, lock *moduleLock, extractedSecrets ...importedConfigSecret) error {
	return validateNormalizedImportedConfigWithLockAndTaint(normalized, reg, lock, nil, extractedSecrets...)
}

// validateNormalizedImportedConfigWithLockAndTaint keeps two intentionally
// separate Secret Store views. Only lifecycle-managed extracted values enter
// the effective input map and may satisfy input_required. privateTaint contains
// every pre-existing store record solely so equal-value config aliases inherit
// source confidentiality while their schema is checked.
func validateNormalizedImportedConfigWithLockAndTaint(normalized []byte, reg map[string]Module, lock *moduleLock, privateTaint map[string]string, extractedSecrets ...importedConfigSecret) error {
	return validateNormalizedImportedConfigWithLockAndTaintContext(context.Background(), normalized, reg, lock, privateTaint, extractedSecrets...)
}

func validateNormalizedImportedConfigWithLockAndTaintContext(ctx context.Context, normalized []byte, reg map[string]Module, lock *moduleLock, privateTaint map[string]string, extractedSecrets ...importedConfigSecret) error {
	validation, err := os.CreateTemp("", "anas-config-import-validation-*.yml")
	if err != nil {
		return err
	}
	validationPath := validation.Name()
	defer os.Remove(validationPath)
	if err := validation.Chmod(0600); err != nil {
		validation.Close()
		return err
	}
	if _, err := validation.Write(normalized); err != nil {
		validation.Close()
		return err
	}
	if err := validation.Close(); err != nil {
		return err
	}
	loaded, err := config.Load(validationPath)
	if err != nil {
		return fmt.Errorf("normalized config is invalid: %w", err)
	}
	if err := validateConfiguredParameterDeclarations(loaded, reg); err != nil {
		return fmt.Errorf("normalized config is invalid: %w", err)
	}
	if err := validateConfigRuntimeKeyCollisions(validationPath, reg); err != nil {
		return fmt.Errorf("normalized config is invalid: %w", err)
	}
	values := configBaseEnv(loaded, reg)
	sourceSensitive := map[string]bool{}
	for key := range loaded.Secrets {
		sourceSensitive[config.EnvKey(key)] = true
	}
	// credential_rotate inputs were intentionally removed from normalized YAML.
	// Restore only an in-memory validation view so input_required and constraints
	// see the same value that will be committed to the lifecycle Secret Store.
	allExtractedValues := make(map[string]string, len(extractedSecrets))
	for _, secret := range extractedSecrets {
		allExtractedValues[secret.Key] = secret.Value
		if secret.Generated {
			continue
		}
		if strings.TrimSpace(secret.Value) != "" {
			key := config.EnvKey(secret.Key)
			module, parameter, err := policyOwnerForEnv(key, reg)
			if err != nil {
				return fmt.Errorf("normalized config is invalid: lifecycle secret %s: %w", key, err)
			}
			normalized, err := normalizeImportedSensitiveParameter("lifecycle secret "+key, module, parameter, secret.Value, reg)
			if err != nil {
				return fmt.Errorf("normalized config is invalid: %w", err)
			}
			values[key] = normalized
			sourceSensitive[key] = true
		}
	}
	markSensitiveValueAliases(values, sourceSensitive)
	markSensitiveValueAliasesFromSources(values, sourceSensitive, privateTaint, allExtractedValues)
	if err := normalizeConfiguredParameterEnvWithSensitive(values, reg, sourceSensitive); err != nil {
		return fmt.Errorf("normalized config is invalid: %w", err)
	}
	if err := validateResolvedInputRequiredEnvContext(ctx, loaded, reg, values, lock, sourceSensitive, privateTaint, allExtractedValues); err != nil {
		return fmt.Errorf("normalized config is invalid: %w", err)
	}
	return nil
}

type importFile struct {
	path string
	data []byte
	mode os.FileMode
}

var renameImportedFile = os.Rename

func marshalSecretStore(store *secretStore) []byte {
	doc := secretStoreDocument{APIVersion: "anas.secrets/v2", Secrets: map[string]secretStoreRecord{}}
	for key, value := range store.values {
		meta := store.metadata[key]
		doc.Secrets[key] = secretStoreRecord{
			Value: value, Owner: meta.Owner, Kind: meta.Kind, Provenance: meta.Provenance,
			Generation: meta.Generation, RotationID: meta.RotationID,
		}
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
		if err := renameImportedFile(staged[i].tmp, staged[i].path); err != nil {
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
