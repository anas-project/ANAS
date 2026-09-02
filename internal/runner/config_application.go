package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

type workspaceConfigApplication struct {
	workspace      string
	registryLoader func() (map[string]Module, error)
}

var _ application.ConfigService = (*workspaceConfigApplication)(nil)

// NewWorkspaceConfigService returns the daemon-facing configuration service.
// Its Module declarations come only from the workspace's persisted immutable
// module view; unlike CLI discovery it never consults ANAS_MODULE_ROOT, cwd, or
// another process-environment override.
func NewWorkspaceConfigService(workspace string) application.ConfigService {
	workspace = filepath.Clean(workspace)
	service := &workspaceConfigApplication{workspace: workspace}
	service.registryLoader = func() (map[string]Module, error) {
		view, err := loadWorkspaceModuleView(workspace)
		if err != nil {
			return nil, err
		}
		root := filepath.Clean(view.ModuleRoot)
		if !filepath.IsAbs(root) {
			return nil, errors.New("workspace Module view does not contain an absolute root")
		}
		return loadRegistryDir(root)
	}
	return service
}

func newWorkspaceConfigServiceWithRegistry(workspace string, reg map[string]Module) *workspaceConfigApplication {
	return &workspaceConfigApplication{
		workspace:      filepath.Clean(workspace),
		registryLoader: func() (map[string]Module, error) { return reg, nil },
	}
}

type managedConfigSnapshot struct {
	managed       bool
	contentDigest string
	validator     string
	configBytes   []byte
	stateBytes    []byte
	state         managedConfigState
}

type preparedConfigCandidate struct {
	normalized         []byte
	secretBytes        []byte
	secretStore        *secretStore
	contentDigest      string
	document           application.ConfigDocument
	fields             []application.ConfigField
	changes            []application.ConfigChange
	storeChanged       bool
	currentStoreBytes  []byte
	currentStoreExists bool
}

func (service *workspaceConfigApplication) GetConfig(ctx context.Context) (application.ConfigSnapshot, error) {
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.ConfigSnapshot{}, configServiceBoundaryError("config_unavailable", err)
	}
	defer unlock()
	reg, err := service.registry()
	if err != nil {
		return application.ConfigSnapshot{}, configInternalError("config_schema_unavailable", err)
	}
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil {
		return application.ConfigSnapshot{}, err
	}
	store, err := loadSecretStore(stateDir(service.workspace))
	if err != nil {
		return application.ConfigSnapshot{}, configInternalError("secrets_unavailable", err)
	}
	return buildConfigSnapshot(current, reg, store)
}

func (service *workspaceConfigApplication) ValidateConfig(ctx context.Context, candidate application.ConfigCandidate) (application.ConfigValidationResult, error) {
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return application.ConfigValidationResult{}, configServiceBoundaryError("config_unavailable", err)
	}
	defer unlock()
	reg, err := service.registry()
	if err != nil {
		return application.ConfigValidationResult{}, configInternalError("config_schema_unavailable", err)
	}
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil {
		return application.ConfigValidationResult{}, err
	}
	prepared, err := service.prepareCandidate(ctx, candidate, current, reg)
	if err != nil {
		return application.ConfigValidationResult{}, err
	}
	return application.ConfigValidationResult{
		BaseValidator: current.validator,
		Config:        prepared.document, Changes: prepared.changes,
	}, nil
}

func (service *workspaceConfigApplication) PutConfig(ctx context.Context, request application.ConfigPutRequest, observer application.ConfigCommitObserver) (application.ConfigPutResult, error) {
	if !validConfigTransactionOperationID(request.OperationID) {
		return application.ConfigPutResult{}, configInternalError("audit_unavailable", errors.New("configuration audit operation ID is invalid"))
	}
	unlock, err := acquireRuntimeLockContext(ctx, stateDir(service.workspace))
	if err != nil {
		return application.ConfigPutResult{}, configInternalError("runtime_lock_unavailable", err)
	}
	defer unlock()
	if err := ensureManagedConfigValidator(service.workspace); err != nil {
		return application.ConfigPutResult{}, configServiceBoundaryError("config_state_invalid", err)
	}
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil {
		return application.ConfigPutResult{}, err
	}
	if err := checkConfigPrecondition(current, request); err != nil {
		return application.ConfigPutResult{}, err
	}
	reg, err := service.registry()
	if err != nil {
		return application.ConfigPutResult{}, configInternalError("config_schema_unavailable", err)
	}
	prepared, err := service.prepareCandidate(ctx, request.Candidate, current, reg)
	if err != nil {
		return application.ConfigPutResult{}, err
	}
	if observer == nil {
		return application.ConfigPutResult{}, configInternalError("audit_unavailable", errors.New("config commit observer is unavailable"))
	}
	noOp := current.managed && bytes.Equal(current.configBytes, prepared.normalized) && !prepared.storeChanged
	candidateValidator := current.validator
	if !noOp {
		candidateValidator, err = newManagedConfigValidator()
		if err != nil {
			return application.ConfigPutResult{}, configInternalError("config_unavailable", err)
		}
	}
	intent := application.ConfigCommitIntent{
		OperationID:      request.OperationID,
		CurrentValidator: current.validator, CandidateValidator: candidateValidator,
		Changes: append([]application.ConfigChange(nil), prepared.changes...),
	}
	if err := observer.BeforeConfigCommit(ctx, intent); err != nil {
		return application.ConfigPutResult{}, configInternalError("audit_unavailable", err)
	}
	expected := workspaceConfigExpectedGeneration{
		"config":        {data: current.configBytes, present: current.managed},
		"secrets":       {data: prepared.currentStoreBytes, present: prepared.currentStoreExists},
		"managed_state": {data: current.stateBytes, present: current.managed},
	}
	if noOp {
		if err := verifyWorkspaceConfigExpectedGeneration(service.workspace, expected); err != nil {
			return application.ConfigPutResult{}, configTransactionApplicationError(err)
		}
		return application.ConfigPutResult{
			PreviousValidator: current.validator, Validator: current.validator,
			Config: prepared.document, AvailableModules: configAvailableModuleNames(reg),
			Fields: prepared.fields, Changes: prepared.changes,
		}, nil
	}
	stateBytes, err := managedConfigStateBytesWithValidator(prepared.normalized, "console", candidateValidator)
	if err != nil {
		return application.ConfigPutResult{}, configInternalError("config_unavailable", err)
	}
	if err := commitWorkspaceConfigFilesExpected(service.workspace, request.OperationID, expected, prepared.normalized, prepared.secretBytes, stateBytes); err != nil {
		return application.ConfigPutResult{}, configTransactionApplicationError(err)
	}
	return application.ConfigPutResult{
		PreviousValidator: current.validator, Validator: candidateValidator,
		Config: prepared.document, AvailableModules: configAvailableModuleNames(reg),
		Fields: prepared.fields, Changes: prepared.changes,
	}, nil
}

func configTransactionApplicationError(err error) error {
	if errors.Is(err, errConfigTransactionPreconditionChanged) {
		return configPreconditionError("config_precondition_failed", "workspace configuration changed before commit", err)
	}
	code := "config_unavailable"
	if configTransactionRecoveryRequired(err) {
		code = "config_recovery_required"
	}
	return configInternalError(code, err)
}

func configServiceBoundaryError(code string, err error) error {
	if _, ok := application.ErrorOf(err); ok {
		return err
	}
	return configInternalError(code, err)
}

func (service *workspaceConfigApplication) registry() (map[string]Module, error) {
	if service == nil || service.registryLoader == nil {
		return nil, errors.New("configuration schema loader is unavailable")
	}
	return service.registryLoader()
}

func acquireWorkspaceConfigReadLock(ctx context.Context, base string) (func(), error) {
	for {
		migrateValidator := false
		unlock, err := acquireRuntimeSharedLockContext(ctx, base)
		if err != nil {
			return nil, err
		}
		_, err = os.Lstat(configTransactionDirectory(workspaceOf(base)))
		if os.IsNotExist(err) {
			needsMigration, migrationErr := managedConfigValidatorMigrationNeeded(workspaceOf(base))
			if migrationErr != nil {
				unlock()
				return nil, migrationErr
			}
			if !needsMigration {
				return unlock, nil
			}
			migrateValidator = true
			err = nil
		}
		unlock()
		if err != nil {
			return nil, err
		}
		recoverUnlock, err := acquireRuntimeLockContext(ctx, base)
		if err != nil {
			return nil, err
		}
		if migrateValidator {
			if err := ensureManagedConfigValidator(workspaceOf(base)); err != nil {
				recoverUnlock()
				return nil, err
			}
		}
		recoverUnlock()
	}
}

func managedConfigValidatorMigrationNeeded(workspace string) (bool, error) {
	stateBytes, _, present, _, err := readConfigTransactionTarget(
		managedConfigStatePath(stateDir(workspace)), configTransactionMaxStateSize,
	)
	if err != nil || !present {
		return false, err
	}
	var state managedConfigState
	decoder := yaml.NewDecoder(bytes.NewReader(stateBytes))
	decoder.KnownFields(true)
	if err := decoder.Decode(&state); err != nil {
		return false, nil
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false, nil
	}
	return state.APIVersion == "anas.config/v1" && validConfigTransactionDigest(state.ContentDigest) && state.Validator == "", nil
}

// ensureManagedConfigValidator upgrades the legacy v1 state while the caller
// holds the exclusive workspace runtime lock. The content digest remains an
// internal manual-edit detector and is never reused as a client validator.
func ensureManagedConfigValidator(workspace string) error {
	configBytes, _, configPresent, _, configErr := readConfigTransactionTarget(
		workspaceConfigPath(workspace), configTransactionMaxConfigSize,
	)
	statePath := managedConfigStatePath(stateDir(workspace))
	stateBytes, _, statePresent, _, stateErr := readConfigTransactionTarget(statePath, configTransactionMaxStateSize)
	if configErr != nil || stateErr != nil {
		return errors.Join(configErr, stateErr)
	}
	if !configPresent && !statePresent {
		return nil
	}
	if !configPresent || !statePresent {
		return errors.New("workspace configuration is not in a managed state")
	}
	var state managedConfigState
	decoder := yaml.NewDecoder(bytes.NewReader(stateBytes))
	decoder.KnownFields(true)
	if err := decoder.Decode(&state); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("managed config state contains trailing data")
	}
	if state.APIVersion != "anas.config/v1" || !validConfigTransactionDigest(state.ContentDigest) {
		return errors.New("managed config state is invalid")
	}
	if configDigest(configBytes) != state.ContentDigest {
		return configPreconditionError(
			"config_precondition_failed", "workspace configuration changed outside ANAS",
			errors.New("managed digest mismatch"),
		)
	}
	if state.Validator != "" {
		if !validManagedConfigValidator(state.Validator) {
			return errors.New("managed config state validator is invalid")
		}
		return nil
	}
	validator, err := newManagedConfigValidator()
	if err != nil {
		return err
	}
	state.Validator = validator
	state.UpdatedBy = "validator-migration"
	return writeYAMLAtomic(statePath, &state, 0600)
}

func readManagedConfigSnapshot(workspace string) (managedConfigSnapshot, error) {
	configPath := workspaceConfigPath(workspace)
	statePath := managedConfigStatePath(stateDir(workspace))
	configBytes, _, configPresent, _, configErr := readConfigTransactionTarget(configPath, configTransactionMaxConfigSize)
	stateBytes, _, statePresent, _, stateErr := readConfigTransactionTarget(statePath, configTransactionMaxStateSize)
	if configErr == nil && stateErr == nil && !configPresent && !statePresent {
		return managedConfigSnapshot{}, nil
	}
	if configErr != nil || stateErr != nil {
		return managedConfigSnapshot{}, configInternalError("config_unavailable", errors.Join(configErr, stateErr))
	}
	if !configPresent || !statePresent {
		return managedConfigSnapshot{}, configPreconditionError("config_not_managed", "workspace configuration is not in a managed state", errors.Join(configErr, stateErr))
	}
	var state managedConfigState
	decoder := yaml.NewDecoder(bytes.NewReader(stateBytes))
	decoder.KnownFields(true)
	if err := decoder.Decode(&state); err != nil {
		return managedConfigSnapshot{}, configInternalError("config_state_invalid", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return managedConfigSnapshot{}, configInternalError("config_state_invalid", errors.New("managed config state contains trailing data"))
	}
	if state.APIVersion != "anas.config/v1" || !validConfigTransactionDigest(state.ContentDigest) || !validManagedConfigValidator(state.Validator) {
		return managedConfigSnapshot{}, configInternalError("config_state_invalid", errors.New("managed config state is invalid"))
	}
	digest := configDigest(configBytes)
	if digest != state.ContentDigest {
		return managedConfigSnapshot{}, configPreconditionError("config_precondition_failed", "workspace configuration changed outside ANAS", errors.New("managed digest mismatch"))
	}
	return managedConfigSnapshot{
		managed: true, contentDigest: state.ContentDigest, validator: state.Validator,
		configBytes: configBytes, stateBytes: stateBytes, state: state,
	}, nil
}

func checkConfigPrecondition(current managedConfigSnapshot, request application.ConfigPutRequest) error {
	if current.managed {
		if request.Precondition != application.ConfigPreconditionMatch {
			return configRequiredError("config_precondition_required", "managed configuration requires If-Match")
		}
		if request.ExpectedValidator != current.validator {
			return configPreconditionError("config_precondition_failed", "configuration precondition did not match", nil)
		}
		return nil
	}
	if request.Precondition != application.ConfigPreconditionMustCreate {
		return configRequiredError("config_precondition_required", "initial configuration requires If-None-Match")
	}
	return nil
}

func (service *workspaceConfigApplication) prepareCandidate(ctx context.Context, candidate application.ConfigCandidate, current managedConfigSnapshot, reg map[string]Module) (preparedConfigCandidate, error) {
	if candidate.Document == nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config must be an object", nil)
	}
	store, currentStoreBytes, currentStoreExists, err := loadSecretStoreSnapshot(stateDir(service.workspace))
	if err != nil {
		return preparedConfigCandidate{}, configInternalError("secrets_unavailable", err)
	}
	currentDoc, currentRoot, effectiveSensitivePaths, err := privateConfigDocument(current.configBytes, reg, store)
	_ = currentDoc
	if err != nil {
		return preparedConfigCandidate{}, err
	}
	candidateRoot, err := candidateYAMLRoot(candidate.Document)
	if err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
	}
	if err := stabilizeCandidateModuleOrder(candidateRoot, currentRoot); err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
	}
	entries, err := collectConfigParameters(reg, nil)
	if err != nil {
		return preparedConfigCandidate{}, configInternalError("config_schema_unavailable", err)
	}
	if err := validateHTTPConfigSurface(candidateRoot, currentRoot, reg, entries, effectiveSensitivePaths); err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config contains an unsupported field", err)
	}
	if err := restoreEffectiveSensitiveAliases(candidateRoot, currentRoot, effectiveSensitivePaths, reg); err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
	}
	if err := applySensitiveCandidateOperations(candidateRoot, currentRoot, candidate.Sensitive, reg, entries); err != nil {
		return preparedConfigCandidate{}, err
	}
	candidateBytes, err := encodeYAMLDocument(candidateRoot)
	if err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config could not be normalized", err)
	}
	tempPath, cleanup, err := writeConfigValidationTemp(stateDir(service.workspace), candidateBytes)
	if err != nil {
		return preparedConfigCandidate{}, configInternalError("config_validation_unavailable", err)
	}
	defer cleanup()
	result, err := normalizeImportedConfigWithTaint(tempPath, reg, store.values)
	if err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config failed validation", err)
	}
	lock, err := loadModuleLockFile(projectLockPath(workspaceConfigPath(service.workspace)))
	if err != nil {
		return preparedConfigCandidate{}, configInternalError("config_lock_unavailable", err)
	}
	validationSecrets := make([]importedConfigSecret, 0, len(store.values)+len(result.Secrets))
	for key, value := range store.lifecycleManagedValues() {
		validationSecrets = append(validationSecrets, importedConfigSecret{Path: "secret-store." + key, Key: key, Value: value})
	}
	validationSecrets = append(validationSecrets, result.Secrets...)
	if err := validateNormalizedImportedConfigWithLockAndTaintContext(ctx, result.Normalized, reg, lock, store.values, validationSecrets...); err != nil {
		return preparedConfigCandidate{}, configInvalidError("config_candidate_invalid", "candidate config failed validation", err)
	}
	for _, secret := range result.Secrets {
		if old := store.values[secret.Key]; old != "" && old != secret.Value {
			return preparedConfigCandidate{}, configInvalidError("config_field_read_only", "lifecycle-managed configuration is read-only", nil)
		}
		if old := store.values[secret.Key]; old == secret.Value && old != "" {
			continue
		}
		metadata := secretMetadata{Owner: secret.Owner, Kind: "lifecycle_managed", Provenance: "console-config:" + secret.Path}
		if secret.Generated {
			metadata.Kind = "local_admin"
		}
		store.SetWithMetadata(secret.Key, secret.Value, metadata)
	}
	secretBytes := currentStoreBytes
	if store.dirty || !currentStoreExists {
		secretBytes = marshalSecretStore(store)
	}
	changes, err := buildCandidateChanges(current.configBytes, result.Normalized, reg, entries, effectiveSensitivePaths)
	if err != nil {
		return preparedConfigCandidate{}, err
	}
	if current.managed && len(changes) == 0 {
		// JSON cannot preserve irrelevant YAML mapping order or spelling. If the
		// complete writable semantic projection did not change, retain the exact
		// managed bytes so a GET/PUT round trip remains a true no-op and keeps its
		// validator stable.
		result.Normalized = append([]byte(nil), current.configBytes...)
	}
	document, _, _, err := privateConfigDocument(result.Normalized, reg, store)
	if err != nil {
		return preparedConfigCandidate{}, err
	}
	fields, err := buildConfigFieldsFromBytes(result.Normalized, reg, store)
	if err != nil {
		return preparedConfigCandidate{}, err
	}
	return preparedConfigCandidate{
		normalized: result.Normalized, secretBytes: secretBytes, secretStore: store,
		contentDigest: configDigest(result.Normalized), document: document, fields: fields,
		changes: changes, storeChanged: store.dirty || !currentStoreExists,
		currentStoreBytes: currentStoreBytes, currentStoreExists: currentStoreExists,
	}, nil
}

// restoreEffectiveSensitiveAliases round-trips fields whose current value is
// private even though their declaration is not. GET removes these aliases and
// marks the corresponding field read-only, so only the server may put the
// exact current node back into a candidate. Removing an enclosing module still
// removes its private aliases instead of silently recreating the module.
func restoreEffectiveSensitiveAliases(candidate, current *yaml.Node, paths map[string]bool, reg map[string]Module) error {
	ordered := make([]string, 0, len(paths))
	for dotted := range paths {
		ordered = append(ordered, dotted)
	}
	sort.Strings(ordered)
	for _, dotted := range ordered {
		path := strings.Split(dotted, ".")
		target := targetForSettingPath(dotted, reg)
		if policyForTarget(target, reg).Sensitive {
			// Declared sensitive fields are restored in the stable schema order by
			// applySensitiveCandidateOperations below. This helper is only for
			// aliases that became private because of their current value.
			continue
		}
		if target.Module != globalModuleName && !candidateContainsModule(candidate, target.Module) {
			continue
		}
		value := nodeAtYAMLPath(current, path)
		if value == nil {
			continue
		}
		if err := setYAMLPath(candidate, path, cloneExpandedYAMLNode(value)); err != nil {
			return err
		}
	}
	return nil
}

func buildConfigSnapshot(current managedConfigSnapshot, reg map[string]Module, store *secretStore) (application.ConfigSnapshot, error) {
	document, _, _, err := privateConfigDocument(current.configBytes, reg, store)
	if err != nil {
		return application.ConfigSnapshot{}, err
	}
	fields, err := buildConfigFieldsFromBytes(current.configBytes, reg, store)
	if err != nil {
		return application.ConfigSnapshot{}, err
	}
	return application.ConfigSnapshot{
		Managed: current.managed, Validator: current.validator, Config: document,
		AvailableModules: configAvailableModuleNames(reg), Fields: fields,
	}, nil
}

func configAvailableModuleNames(reg map[string]Module) []string {
	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func buildConfigFieldsFromBytes(body []byte, reg map[string]Module, store *secretStore) ([]application.ConfigField, error) {
	settings := map[string]string{}
	view := configParameterValueView{Values: map[string]string{}, Present: map[string]bool{}, Sensitive: map[string]bool{}}
	if len(body) > 0 {
		path, cleanup, err := writeConfigValidationTemp(filepath.Dir(store.path), body)
		if err != nil {
			return nil, configInternalError("config_projection_unavailable", err)
		}
		defer cleanup()
		loaded, err := config.Load(path)
		if err != nil {
			return nil, configInternalError("config_state_invalid", err)
		}
		for key, value := range configBaseEnv(loaded, reg) {
			key = config.EnvKey(key)
			view.Values[key], view.Present[key] = value, true
		}
		for key := range loaded.Secrets {
			view.Sensitive[config.EnvKey(key)] = true
		}
		markDeclaredSensitiveEnvKeys(view.Sensitive, reg)
		markSensitiveValueAliases(view.Values, view.Sensitive)
		settings, err = config.Settings(path)
		if err != nil {
			return nil, configInternalError("config_projection_unavailable", err)
		}
		privateValues := map[string]bool{}
		for _, value := range store.values {
			addSensitiveValueForms(privateValues, value)
		}
		for key, value := range view.Values {
			if matchesSensitiveValue(privateValues, value) {
				view.Sensitive[key] = true
			}
		}
		for key, value := range store.lifecycleManagedValues() {
			key = config.EnvKey(key)
			view.Present[key], view.Sensitive[key], view.Values[key] = true, true, value
		}
		markSensitiveValueAliases(view.Values, view.Sensitive)
		for key := range store.lifecycleManagedValues() {
			delete(view.Values, config.EnvKey(key))
		}
	}
	entries, err := collectConfigParameters(reg, settings, view)
	if err != nil {
		return nil, configInternalError("config_schema_unavailable", err)
	}
	fields := make([]application.ConfigField, 0, len(entries))
	for _, entry := range entries {
		target, err := resolveConfigTarget(entry.Path, reg)
		if err != nil {
			return nil, configInternalError("config_schema_unavailable", err)
		}
		spec := targetParamType(target, reg)
		kind, allowed := paramTypeDocument(spec)
		editable, editCommand := configEditability(entry.Policy)
		effectiveSensitive := entry.Policy.Sensitive || entry.ValueSensitive
		field := application.ConfigField{
			Path: entry.Path, DocumentPath: append([]string{}, target.YAMLPath...),
			Module: entry.Module, Parameter: entry.Parameter,
			Type: kind, AllowedValues: append([]string{}, allowed...), Default: entry.Default,
			HasDefault: entry.HasDefault, DefaultSource: entry.DefaultSource,
			InputRequired: parameterInputRequired(entry.Module, entry.Parameter, reg),
			MustResolve:   parameterMustResolve(entry.Module, entry.Parameter, reg),
			Constraints:   spec.Constraints, Sensitive: effectiveSensitive,
			// A schema-declared sensitive field remains editable through the
			// explicit sensitive-operation map. Only an otherwise ordinary field
			// tainted by a private value alias must be locked against round-trip
			// writes, because its plaintext was deliberately removed from GET.
			Editable: editable && !(entry.ValueSensitive && !entry.Policy.Sensitive), EditCommand: editCommand,
			Effect: entry.Policy.Effect, Apply: entry.Policy.Apply, Description: entry.Policy.Description,
		}
		if effectiveSensitive {
			field.Default = ""
			if entry.Set {
				field.SensitiveState = "set"
			} else {
				field.SensitiveState = "unset"
			}
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func markDeclaredSensitiveEnvKeys(sensitive map[string]bool, reg map[string]Module) {
	for parameter, policy := range globalConfig.Changes {
		if policy.Sensitive {
			sensitive[paramEnvKey(globalScope, "", parameter)] = true
		}
	}
	for name, module := range reg {
		for parameter, policy := range module.Changes {
			if policy.Sensitive {
				sensitive[moduleParamEnvKey(name, module.EnvPrefix, module.Exports, parameter)] = true
			}
		}
	}
}

func privateConfigDocument(body []byte, reg map[string]Module, store *secretStore) (application.ConfigDocument, *yaml.Node, map[string]bool, error) {
	rawRoot := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if len(body) > 0 {
		var document yaml.Node
		if err := yaml.Unmarshal(body, &document); err != nil {
			return nil, nil, nil, configInternalError("config_state_invalid", err)
		}
		if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
			return nil, nil, nil, configInternalError("config_state_invalid", errors.New("config root is not an object"))
		}
		rawRoot = document.Content[0]
	}
	privateValues := map[string]bool{}
	for _, value := range store.values {
		addSensitiveValueForms(privateValues, value)
	}
	if secrets := mappingValue(rawRoot, "secrets"); secrets != nil {
		collectYAMLScalarSecrets(secrets, privateValues)
	}
	root := cloneExpandedYAMLNode(rawRoot)
	effectivePaths := map[string]bool{}
	entries, err := collectConfigParameters(reg, nil)
	if err != nil {
		return nil, nil, nil, configInternalError("config_schema_unavailable", err)
	}
	// A sensitive declaration is private regardless of which supported YAML
	// spelling currently carries it. Collect every declared value before
	// removing anything so ordinary aliases equal to that value inherit the
	// same taint and cannot survive the public projection.
	for _, entry := range entries {
		if !entry.Policy.Sensitive {
			continue
		}
		target, targetErr := resolveConfigTarget(entry.Path, reg)
		if targetErr != nil {
			continue
		}
		for _, path := range sensitiveConfigYAMLPaths(target, entry.EnvKey) {
			if value := nodeAtYAMLPath(rawRoot, path); value != nil {
				collectYAMLScalarSecrets(value, privateValues)
			}
		}
	}
	for _, entry := range entries {
		if !entry.Policy.Sensitive {
			continue
		}
		target, targetErr := resolveConfigTarget(entry.Path, reg)
		if targetErr != nil {
			continue
		}
		for _, path := range sensitiveConfigYAMLPaths(target, entry.EnvKey) {
			effectivePaths[strings.Join(path, ".")] = true
			removeYAMLPath(root, path)
		}
	}
	removeMappingKey(root, "secrets")
	redactYAMLSensitiveAliases(root, nil, privateValues, effectivePaths)
	var decoded map[string]any
	if err := root.Decode(&decoded); err != nil {
		return nil, nil, nil, configInternalError("config_projection_unavailable", err)
	}
	if decoded == nil {
		decoded = map[string]any{}
	}
	return application.ConfigDocument(decoded), rawRoot, effectivePaths, nil
}

func sensitiveConfigYAMLPaths(target configTarget, envKey string) [][]string {
	paths := [][]string{target.YAMLPath}
	envPath := []string{"env", config.EnvKey(envKey)}
	if strings.Join(target.YAMLPath, "\x00") != strings.Join(envPath, "\x00") {
		paths = append(paths, envPath)
	}
	return paths
}

func candidateYAMLRoot(document application.ConfigDocument) (*yaml.Node, error) {
	body, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var parsed yaml.Node
	if err := yaml.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Content) == 0 || parsed.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config root must be an object")
	}
	return parsed.Content[0], nil
}

// JSON objects do not carry a portable member order, while the modules YAML
// mapping is an ordered deployment input. Preserve every surviving module's
// current relative order and append newly selected modules deterministically.
// Explicit reordering needs a future ordered API field rather than relying on
// JSON object member order.
func stabilizeCandidateModuleOrder(candidate, current *yaml.Node) error {
	modules := mappingValue(candidate, "modules")
	if modules == nil || modules.Kind != yaml.MappingNode {
		return nil
	}
	type modulePair struct {
		key   *yaml.Node
		value *yaml.Node
	}
	pairs := make(map[string]modulePair, len(modules.Content)/2)
	for index := 0; index+1 < len(modules.Content); index += 2 {
		rawName := modules.Content[index].Value
		name := canonicalConfigName(rawName)
		if name == "" || rawName != name {
			return fmt.Errorf("module %q does not use a canonical name", rawName)
		}
		if _, duplicate := pairs[name]; duplicate {
			return fmt.Errorf("module %q is duplicated", name)
		}
		pairs[name] = modulePair{key: modules.Content[index], value: modules.Content[index+1]}
	}
	ordered := make([]*yaml.Node, 0, len(modules.Content))
	used := map[string]bool{}
	currentModules := mappingValue(current, "modules")
	if currentModules != nil && currentModules.Kind == yaml.MappingNode {
		for index := 0; index+1 < len(currentModules.Content); index += 2 {
			name := canonicalConfigName(currentModules.Content[index].Value)
			pair, present := pairs[name]
			if !present || used[name] {
				continue
			}
			ordered = append(ordered, pair.key, pair.value)
			used[name] = true
		}
	}
	added := make([]string, 0, len(pairs)-len(used))
	for name := range pairs {
		if !used[name] {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	for _, name := range added {
		pair := pairs[name]
		ordered = append(ordered, pair.key, pair.value)
	}
	modules.Content = ordered
	return nil
}

func validateHTTPConfigSurface(candidate, current *yaml.Node, reg map[string]Module, entries []configListEntry, effectiveSensitive map[string]bool) error {
	if mappingValue(candidate, "secrets") != nil {
		return errors.New("raw secrets are not accepted by the console API")
	}
	allowedEnv := map[string]configListEntry{}
	sensitivePaths := map[string]bool{}
	for _, entry := range entries {
		target, err := resolveConfigTarget(entry.Path, reg)
		if err != nil {
			return err
		}
		path := strings.Join(target.YAMLPath, ".")
		if entry.Policy.Sensitive {
			sensitivePaths[path] = true
			if nodeAtYAMLPath(candidate, target.YAMLPath) != nil {
				return fmt.Errorf("sensitive field %s must use the sensitive operation map", entry.Path)
			}
		}
		if len(target.YAMLPath) == 2 && target.YAMLPath[0] == "env" {
			allowedEnv[config.EnvKey(target.YAMLPath[1])] = entry
		}
	}
	for path := range effectiveSensitive {
		if nodeAtYAMLPath(candidate, strings.Split(path, ".")) != nil {
			return errors.New("effective-sensitive fields must not be submitted in config")
		}
	}
	if env := mappingValue(candidate, "env"); env != nil {
		if env.Kind != yaml.MappingNode {
			return errors.New("env must be an object")
		}
		for index := 0; index+1 < len(env.Content); index += 2 {
			rawKey := env.Content[index].Value
			key := config.EnvKey(rawKey)
			if rawKey != key {
				return fmt.Errorf("env.%s must use its canonical key %s", rawKey, key)
			}
			entry, ok := allowedEnv[key]
			if !ok {
				old := nodeAtYAMLPath(current, []string{"env", key})
				if old == nil || !yamlNodesEqual(old, env.Content[index+1]) {
					return fmt.Errorf("env.%s is not a declared console field", key)
				}
			}
			if ok && entry.Policy.Sensitive {
				return fmt.Errorf("env.%s is sensitive and must use an operation", key)
			}
		}
	}
	modules := mappingValue(candidate, "modules")
	if modules != nil {
		if err := validateHTTPModuleTree(modules, mappingValue(current, "modules"), reg, entries); err != nil {
			return err
		}
	}
	_ = sensitivePaths
	return nil
}

func validateHTTPModuleTree(modules, currentModules *yaml.Node, reg map[string]Module, entries []configListEntry) error {
	if modules.Kind != yaml.MappingNode {
		return errors.New("modules must be an object")
	}
	allowedParameters := map[string]map[string]bool{}
	for _, entry := range entries {
		target, err := resolveConfigTarget(entry.Path, reg)
		if err != nil || len(target.YAMLPath) != 4 || target.YAMLPath[0] != "modules" || target.YAMLPath[2] != "config" {
			continue
		}
		module := target.YAMLPath[1]
		if allowedParameters[module] == nil {
			allowedParameters[module] = map[string]bool{}
		}
		allowedParameters[module][target.YAMLPath[3]] = true
	}
	for index := 0; index+1 < len(modules.Content); index += 2 {
		rawName := modules.Content[index].Value
		name := canonicalConfigName(rawName)
		if rawName != name {
			return fmt.Errorf("module %q must use its canonical name %q", rawName, name)
		}
		moduleSpec, ok := reg[name]
		if !ok {
			return fmt.Errorf("unknown module %q", name)
		}
		module := modules.Content[index+1]
		currentModule := canonicalHTTPMappingValue(currentModules, name)
		if module.Kind != yaml.MappingNode {
			return fmt.Errorf("modules.%s must be an object", name)
		}
		for child := 0; child+1 < len(module.Content); child += 2 {
			key := module.Content[child].Value
			value := module.Content[child+1]
			switch key {
			case "version", "enabled", "depends_on":
			case "config":
				if err := validateHTTPModuleParameterMap(name, value, allowedParameters[name]); err != nil {
					return err
				}
			case "identity":
				if err := validateClosedHTTPMapping(value, "modules."+name+".identity", map[string]bool{"login_protocol": true}); err != nil {
					return err
				}
			case "administration":
			default:
				return fmt.Errorf("modules.%s.%s is not a registered configuration field", name, key)
			}
		}
		if err := validateHTTPModuleAdministration(name, mappingValue(module, "administration"), mappingValue(currentModule, "administration"), moduleSpec); err != nil {
			return err
		}
	}
	return nil
}

func canonicalHTTPMappingValue(mapping *yaml.Node, canonicalKey string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if strings.ToLower(strings.TrimSpace(mapping.Content[index].Value)) == canonicalKey {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func validateHTTPModuleParameterMap(module string, node *yaml.Node, allowed map[string]bool) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("modules.%s.config must be an object", module)
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		rawParameter := node.Content[index].Value
		parameter := canonicalConfigName(rawParameter)
		if rawParameter != parameter {
			return fmt.Errorf("modules.%s.config.%s must use its canonical name %s", module, rawParameter, parameter)
		}
		if !allowed[parameter] {
			return fmt.Errorf("modules.%s.config.%s is not a registered configuration field", module, parameter)
		}
	}
	return nil
}

func validateHTTPModuleAdministration(module string, node, current *yaml.Node, moduleSpec Module) error {
	if node != nil {
		if err := validateClosedHTTPMapping(node, "modules."+module+".administration", map[string]bool{"local_accounts": true}); err != nil {
			return err
		}
	}
	accounts := mappingValue(node, "local_accounts")
	candidateIDs, err := validateHTTPReadOnlyLocalAccounts(module, accounts, moduleSpec)
	if err != nil {
		return err
	}
	currentIDs, err := httpLocalAccountIDs(module, mappingValue(current, "local_accounts"))
	if err != nil {
		return err
	}
	if len(candidateIDs) != len(currentIDs) {
		return fmt.Errorf("modules.%s.administration.local_accounts is read-only", module)
	}
	for id := range candidateIDs {
		if !currentIDs[id] {
			return fmt.Errorf("modules.%s.administration.local_accounts is read-only", module)
		}
	}
	return nil
}

func validateHTTPReadOnlyLocalAccounts(module string, accounts *yaml.Node, moduleSpec Module) (map[string]bool, error) {
	ids, err := httpLocalAccountIDs(module, accounts)
	if err != nil {
		return nil, err
	}
	if accounts == nil {
		return ids, nil
	}
	for index := 0; index+1 < len(accounts.Content); index += 2 {
		id := accounts.Content[index].Value
		value := accounts.Content[index+1]
		if !moduleHasLocalAccount(moduleSpec, id) {
			return nil, fmt.Errorf("modules.%s.administration.local_accounts.%s is not a manifest account id", module, id)
		}
		if value.Kind != yaml.MappingNode || len(value.Content) != 0 {
			return nil, fmt.Errorf("modules.%s.administration.local_accounts.%s is read-only", module, id)
		}
	}
	return ids, nil
}

func httpLocalAccountIDs(module string, accounts *yaml.Node) (map[string]bool, error) {
	ids := map[string]bool{}
	if accounts == nil {
		return ids, nil
	}
	if accounts.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("modules.%s.administration.local_accounts must be an object", module)
	}
	for index := 0; index+1 < len(accounts.Content); index += 2 {
		ids[accounts.Content[index].Value] = true
	}
	return ids, nil
}

func validateClosedHTTPMapping(node *yaml.Node, path string, allowed map[string]bool) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be an object", path)
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		key := node.Content[index].Value
		if !allowed[key] {
			return fmt.Errorf("%s.%s is not a registered configuration field", path, key)
		}
	}
	return nil
}

func applySensitiveCandidateOperations(candidate, current *yaml.Node, mutations map[string]application.ConfigSensitiveMutation, reg map[string]Module, entries []configListEntry) error {
	byPath := map[string]configListEntry{}
	for _, entry := range entries {
		if entry.Policy.Sensitive {
			byPath[entry.Path] = entry
		}
	}
	for path := range mutations {
		if _, ok := byPath[path]; !ok {
			return configInvalidError("config_sensitive_operation_invalid", "sensitive operation names an unknown field", nil)
		}
	}
	// Raw secrets are private input and are never round-tripped through JSON.
	// Preserve their complete current mapping before applying schema-addressed
	// operations, which may explicitly remove an equivalent canonical key.
	if currentSecrets := mappingValue(current, "secrets"); currentSecrets != nil {
		candidate.Content = append(candidate.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "secrets"}, cloneExpandedYAMLNode(currentSecrets))
	}
	for _, entry := range entries {
		if !entry.Policy.Sensitive {
			continue
		}
		mutation, supplied := mutations[entry.Path]
		if !supplied {
			mutation.Operation = application.ConfigSensitiveUnchanged
		}
		switch mutation.Operation {
		case application.ConfigSensitiveUnchanged:
			if mutation.Value != nil {
				return configInvalidError("config_sensitive_operation_invalid", "unchanged must not include a value", nil)
			}
		case application.ConfigSensitiveSet:
			if mutation.Value == nil {
				return configInvalidError("config_sensitive_operation_invalid", "set requires a value", nil)
			}
		case application.ConfigSensitiveUnset:
			if mutation.Value != nil {
				return configInvalidError("config_sensitive_operation_invalid", "unset must not include a value", nil)
			}
		default:
			return configInvalidError("config_sensitive_operation_invalid", "sensitive operation is invalid", nil)
		}
		if mutation.Operation != application.ConfigSensitiveUnchanged {
			if editable, _ := configEditability(entry.Policy); !editable {
				return configInvalidError("config_field_read_only", "guarded configuration fields are read-only", nil)
			}
		}
		target, err := resolveConfigTarget(entry.Path, reg)
		if err != nil {
			return configInternalError("config_schema_unavailable", err)
		}
		switch mutation.Operation {
		case application.ConfigSensitiveUnchanged:
			if !candidateContainsModule(candidate, entry.Module) {
				removeSensitiveTargetSpellings(candidate, target, entry.EnvKey)
				continue
			}
			// The public document intentionally omits sensitive values. Rehydrate
			// every supported private spelling from the current generation only;
			// the request is never allowed to supply these nodes directly.
			if currentValue := nodeAtYAMLPath(current, target.YAMLPath); currentValue != nil {
				if err := setYAMLPath(candidate, target.YAMLPath, cloneExpandedYAMLNode(currentValue)); err != nil {
					return configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
				}
			}
			if currentValue := nodeAtYAMLPath(current, []string{"env", config.EnvKey(entry.EnvKey)}); currentValue != nil {
				if err := setYAMLPath(candidate, []string{"env", config.EnvKey(entry.EnvKey)}, cloneExpandedYAMLNode(currentValue)); err != nil {
					return configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
				}
			}
		case application.ConfigSensitiveSet:
			removeSensitiveTargetSpellings(candidate, target, entry.EnvKey)
			if !candidateContainsModule(candidate, entry.Module) {
				return configInvalidError("config_candidate_invalid", "sensitive field belongs to an unselected module", nil)
			}
			if err := setYAMLPath(candidate, target.YAMLPath, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: *mutation.Value}); err != nil {
				return configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
			}
		case application.ConfigSensitiveUnset:
			removeSensitiveTargetSpellings(candidate, target, entry.EnvKey)
		}
	}
	return nil
}

func buildCandidateChanges(currentBytes, candidateBytes []byte, reg map[string]Module, entries []configListEntry, effectiveSensitive map[string]bool) ([]application.ConfigChange, error) {
	current, cleanupCurrent, err := settingsFromConfigBytes(currentBytes)
	if err != nil {
		return nil, configInternalError("config_state_invalid", err)
	}
	defer cleanupCurrent()
	next, cleanupNext, err := settingsFromConfigBytes(candidateBytes)
	if err != nil {
		return nil, configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
	}
	defer cleanupNext()
	currentModules, err := configModuleSelection(currentBytes)
	if err != nil {
		return nil, configInternalError("config_state_invalid", err)
	}
	nextModules, err := configModuleSelection(candidateBytes)
	if err != nil {
		return nil, configInvalidError("config_candidate_invalid", "candidate config is invalid", err)
	}
	byYAMLPath := map[string]configListEntry{}
	for _, entry := range entries {
		target, targetErr := resolveConfigTarget(entry.Path, reg)
		if targetErr == nil {
			byYAMLPath[strings.Join(target.YAMLPath, ".")] = entry
		}
	}
	keys := map[string]bool{}
	for key := range current {
		keys[key] = true
	}
	for key := range next {
		keys[key] = true
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		if currentValue, oldOK := current[key]; oldOK {
			if nextValue, newOK := next[key]; newOK && currentValue == nextValue {
				continue
			}
		} else if _, newOK := next[key]; !newOK {
			continue
		}
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	moduleNames := map[string]bool{}
	for name := range currentModules {
		moduleNames[name] = true
	}
	for name := range nextModules {
		moduleNames[name] = true
	}
	orderedModules := make([]string, 0, len(moduleNames))
	for name := range moduleNames {
		if currentModules[name] != nextModules[name] {
			orderedModules = append(orderedModules, name)
		}
	}
	sort.Strings(orderedModules)
	changes := make([]application.ConfigChange, 0, len(ordered)+len(orderedModules))
	for _, name := range orderedModules {
		policy := policyForTarget(configTarget{Module: name}, reg)
		change := "add"
		if currentModules[name] {
			change = "remove"
		}
		changes = append(changes, application.ConfigChange{
			Path: "modules." + name, Change: change, Effect: policy.Effect, Apply: policy.Apply,
			Sensitive: false, Editable: true,
		})
	}
	for _, key := range ordered {
		entry, declared := byYAMLPath[key]
		policy := policyForTarget(targetForSettingPath(key, reg), reg)
		path := key
		if declared {
			path, policy = entry.Path, entry.Policy
		}
		if !declared && !allowedHTTPStructuralSetting(key, reg) {
			return nil, configInvalidError("config_field_read_only", "candidate changes a field that is not writable through the console", nil)
		}
		editable, _ := configEditability(policy)
		if !editable {
			return nil, configInvalidError("config_field_read_only", "guarded configuration fields are read-only", nil)
		}
		change := "change"
		if _, ok := current[key]; !ok {
			change = "add"
		} else if _, ok := next[key]; !ok {
			change = "remove"
		}
		sensitive := policy.Sensitive || strings.HasPrefix(key, "secrets.") || effectiveSensitive[key]
		changes = append(changes, application.ConfigChange{
			Path: path, Change: change, Effect: policy.Effect, Apply: policy.Apply,
			Sensitive: sensitive, Editable: editable,
		})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

func configModuleSelection(body []byte) (map[string]bool, error) {
	selected := map[string]bool{}
	if len(body) == 0 {
		return selected, nil
	}
	var document yaml.Node
	if err := yaml.Unmarshal(body, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("config root is not an object")
	}
	modules := mappingValue(document.Content[0], "modules")
	if modules == nil {
		return selected, nil
	}
	if modules.Kind != yaml.MappingNode {
		return nil, errors.New("modules is not an object")
	}
	for index := 0; index+1 < len(modules.Content); index += 2 {
		name := canonicalConfigName(modules.Content[index].Value)
		if name == "" || selected[name] {
			return nil, errors.New("modules contains an empty or duplicate name")
		}
		selected[name] = true
	}
	return selected, nil
}

func allowedHTTPStructuralSetting(path string, reg map[string]Module) bool {
	switch path {
	case "module_source", "administration.bootstrap.username", "administration.local_accounts.password_length",
		"identity.directory.provider", "identity.iam.provider", "identity.iam.default_protocol",
		"dynamic_dns.provider", "dynamic_dns.dns_provider", "rollback.snapshot.backend", "rollback.snapshot.keep_auto":
		return true
	}
	parts := strings.Split(path, ".")
	if len(parts) == 3 && parts[0] == "modules" {
		if _, ok := reg[parts[1]]; !ok {
			return false
		}
		return parts[2] == "version" || parts[2] == "enabled" || parts[2] == "depends_on"
	}
	return false
}

func settingsFromConfigBytes(body []byte) (map[string]string, func(), error) {
	if len(body) == 0 {
		return map[string]string{}, func() {}, nil
	}
	file, err := os.CreateTemp("", "anas-config-settings-*.yml")
	if err != nil {
		return nil, func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		cleanup()
		return nil, func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	settings, err := config.Settings(path)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return settings, cleanup, nil
}

func writeConfigValidationTemp(base string, body []byte) (string, func(), error) {
	if err := os.MkdirAll(base, 0700); err != nil {
		return "", func() {}, err
	}
	file, err := os.CreateTemp(base, ".config-api-validation-*.yml")
	if err != nil {
		return "", func() {}, err
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		cleanup()
		return "", func() {}, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, err
	}
	return path, cleanup, nil
}

func encodeYAMLDocument(root *yaml.Node) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func nodeAtYAMLPath(root *yaml.Node, path []string) *yaml.Node {
	current := root
	for _, key := range path {
		current = mappingValue(current, key)
		if current == nil {
			return nil
		}
	}
	return current
}

func setYAMLPath(root *yaml.Node, path []string, value *yaml.Node) error {
	if root == nil || root.Kind != yaml.MappingNode || len(path) == 0 {
		return errors.New("configuration path is invalid")
	}
	parent := root
	for _, key := range path[:len(path)-1] {
		next := mappingValue(parent, key)
		if next == nil {
			next = ensureMappingValue(parent, key)
		}
		if next.Kind != yaml.MappingNode {
			return fmt.Errorf("configuration path %s is not an object", key)
		}
		parent = next
	}
	key := path[len(path)-1]
	for index := 0; index+1 < len(parent.Content); index += 2 {
		if parent.Content[index].Value == key {
			parent.Content[index+1] = value
			return nil
		}
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
	return nil
}

func removeYAMLPath(root *yaml.Node, path []string) {
	if len(path) == 0 {
		return
	}
	parent := root
	for _, key := range path[:len(path)-1] {
		parent = mappingValue(parent, key)
		if parent == nil {
			return
		}
	}
	removeMappingKey(parent, path[len(path)-1])
}

func removeSensitiveTargetSpellings(root *yaml.Node, target configTarget, envKey string) {
	removeYAMLPath(root, target.YAMLPath)
	removeYAMLPath(root, []string{"env", config.EnvKey(envKey)})
	removeYAMLPath(root, []string{"secrets", config.EnvKey(envKey)})
}

func candidateContainsModule(root *yaml.Node, module string) bool {
	if module == globalModuleName {
		return true
	}
	return nodeAtYAMLPath(root, []string{"modules", module}) != nil
}

func cloneExpandedYAMLNode(node *yaml.Node) *yaml.Node {
	if node == nil {
		return nil
	}
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return cloneExpandedYAMLNode(node.Alias)
	}
	clone := *node
	clone.Alias = nil
	clone.Content = make([]*yaml.Node, len(node.Content))
	for index, child := range node.Content {
		clone.Content[index] = cloneExpandedYAMLNode(child)
	}
	return &clone
}

func yamlNodesEqual(left, right *yaml.Node) bool {
	if left == nil || right == nil {
		return left == right
	}
	var leftValue, rightValue any
	if err := cloneExpandedYAMLNode(left).Decode(&leftValue); err != nil {
		return false
	}
	if err := cloneExpandedYAMLNode(right).Decode(&rightValue); err != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func collectYAMLScalarSecrets(node *yaml.Node, values map[string]bool) {
	if node == nil {
		return
	}
	if node.Kind == yaml.AliasNode {
		collectYAMLScalarSecrets(node.Alias, values)
		return
	}
	if node.Kind == yaml.ScalarNode {
		addSensitiveValueForms(values, node.Value)
		return
	}
	for _, child := range node.Content {
		collectYAMLScalarSecrets(child, values)
	}
}

func redactYAMLSensitiveAliases(node *yaml.Node, path []string, values map[string]bool, redacted map[string]bool) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for index := 0; index+1 < len(node.Content); {
		key, value := node.Content[index].Value, node.Content[index+1]
		currentPath := append(append([]string{}, path...), key)
		if yamlNodeContainsSensitiveValue(value, values) {
			redacted[strings.Join(currentPath, ".")] = true
			node.Content = append(node.Content[:index], node.Content[index+2:]...)
			continue
		}
		redactYAMLSensitiveAliases(value, currentPath, values, redacted)
		index += 2
	}
}

func yamlNodeContainsSensitiveValue(node *yaml.Node, values map[string]bool) bool {
	if node == nil {
		return false
	}
	if node.Kind == yaml.AliasNode {
		return yamlNodeContainsSensitiveValue(node.Alias, values)
	}
	if node.Kind == yaml.ScalarNode {
		return matchesSensitiveValue(values, node.Value)
	}
	return false
}

func configDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func configInvalidError(code, message string, cause error) error {
	return &application.Error{Kind: application.ErrorKindInvalidArgument, Code: code, Message: message, Cause: cause}
}

func configPreconditionError(code, message string, cause error) error {
	return &application.Error{Kind: application.ErrorKindFailedPrecondition, Code: code, Message: message, Cause: cause}
}

func configRequiredError(code, message string) error {
	return &application.Error{Kind: application.ErrorKindPreconditionRequired, Code: code, Message: message}
}

func configInternalError(code string, cause error) error {
	return &application.Error{Kind: application.ErrorKindInternal, Code: code, Message: "workspace configuration is unavailable", Cause: cause}
}
