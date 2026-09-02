package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

func configApplicationTestRegistry() map[string]Module {
	return map[string]Module{
		"fieldless": {Name: "fieldless"},
		"demo": {
			Name:       "demo",
			EnvPrefix:  "DEMO",
			Parameters: []string{"public", "alias", "password", "rotate", "migrate", "fixed"},
			Types: map[string]ParamType{
				"public":   {Kind: "string"},
				"alias":    {Kind: "string"},
				"password": {Kind: "string"},
				"rotate":   {Kind: "string"},
				"migrate":  {Kind: "string"},
				"fixed":    {Kind: "string"},
			},
			Changes: map[string]ChangePolicy{
				"public":   {Effect: "reconcile", Apply: "demo reconcile"},
				"alias":    {Effect: "reconcile", Apply: "demo reconcile"},
				"password": {Effect: "container_recreate", Sensitive: true, Apply: "demo restart"},
				"rotate":   {Effect: "credential_rotate", Sensitive: true, Apply: "demo rotate"},
				"migrate":  {Effect: "data_migrate", Sensitive: true, Apply: "demo migrate"},
				"fixed":    {Effect: "immutable", Sensitive: true, Apply: "demo replace"},
			},
		},
	}
}

const configApplicationTestBody = `module_source: official
modules:
  demo:
    config:
      public: visible
      password: private-password
      migrate: private-migration
      fixed: private-fixed
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
`

func newConfigApplicationTestService(t *testing.T, body string, aliases bool) (*workspaceConfigApplication, string) {
	return newConfigApplicationTestServiceWithRegistry(t, body, aliases, configApplicationTestRegistry())
}

func newConfigApplicationTestServiceWithRegistry(t *testing.T, body string, aliases bool, reg map[string]Module) (*workspaceConfigApplication, string) {
	t.Helper()
	workspace := t.TempDir()
	if err := ensureRuntimeLayout(stateDir(workspace)); err != nil {
		t.Fatal(err)
	}
	if aliases {
		body = strings.Replace(body, "      public: visible\n", "      public: visible\n      alias: vault-alias\n", 1)
		body += "secrets:\n  EXTRA_SECRET: raw-private\n"
	}
	configBody := []byte(body)
	if err := os.WriteFile(workspaceConfigPath(workspace), configBody, 0o600); err != nil {
		t.Fatal(err)
	}
	stateBytes, err := managedConfigStateBytes(configBody, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedConfigStatePath(stateDir(workspace)), stateBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	store := &secretStore{
		path: filepath.Join(stateDir(workspace), "secrets.yml"),
		values: map[string]string{
			"DEMO_ROTATE": "private-rotation",
			"VAULT_ALIAS": "vault-alias",
		},
		metadata: map[string]secretMetadata{
			"DEMO_ROTATE": {Owner: "demo", Kind: "lifecycle_managed", Provenance: "test"},
			"VAULT_ALIAS": {Owner: "test", Kind: "generated", Provenance: "test"},
		},
	}
	if err := os.WriteFile(store.path, marshalSecretStore(store), 0o600); err != nil {
		t.Fatal(err)
	}
	return newWorkspaceConfigServiceWithRegistry(workspace, reg), workspace
}

func makeConfigApplicationStateLegacy(t *testing.T, workspace string) managedConfigState {
	t.Helper()
	path := managedConfigStatePath(stateDir(workspace))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state managedConfigState
	if err := yaml.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	state.Validator = ""
	body, err = yaml.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return state
}

func candidateFromConfigSnapshot(t *testing.T, snapshot application.ConfigSnapshot) application.ConfigCandidate {
	candidate := publicCandidateFromConfigSnapshot(t, snapshot)
	candidate.Sensitive = map[string]application.ConfigSensitiveMutation{
		"demo.password": {Operation: application.ConfigSensitiveUnchanged},
		"demo.rotate":   {Operation: application.ConfigSensitiveUnchanged},
		"demo.migrate":  {Operation: application.ConfigSensitiveUnchanged},
		"demo.fixed":    {Operation: application.ConfigSensitiveUnchanged},
	}
	return candidate
}

func publicCandidateFromConfigSnapshot(t *testing.T, snapshot application.ConfigSnapshot) application.ConfigCandidate {
	t.Helper()
	body, err := json.Marshal(snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	var document application.ConfigDocument
	if err := json.Unmarshal(body, &document); err != nil {
		t.Fatal(err)
	}
	return application.ConfigCandidate{Document: document, Sensitive: map[string]application.ConfigSensitiveMutation{}}
}

func candidateDemoConfig(t *testing.T, candidate application.ConfigCandidate) map[string]any {
	t.Helper()
	modules, ok := candidate.Document["modules"].(map[string]any)
	if !ok {
		t.Fatalf("candidate modules = %#v", candidate.Document["modules"])
	}
	demo, ok := modules["demo"].(map[string]any)
	if !ok {
		t.Fatalf("candidate demo = %#v", modules["demo"])
	}
	values, ok := demo["config"].(map[string]any)
	if !ok {
		t.Fatalf("candidate demo config = %#v", demo["config"])
	}
	return values
}

func candidateDemoModule(t *testing.T, candidate application.ConfigCandidate) map[string]any {
	t.Helper()
	modules, ok := candidate.Document["modules"].(map[string]any)
	if !ok {
		t.Fatalf("candidate modules = %#v", candidate.Document["modules"])
	}
	demo, ok := modules["demo"].(map[string]any)
	if !ok {
		t.Fatalf("candidate demo = %#v", modules["demo"])
	}
	return demo
}

func fieldByPath(t *testing.T, fields []application.ConfigField, path string) application.ConfigField {
	t.Helper()
	for _, field := range fields {
		if field.Path == path {
			return field
		}
	}
	t.Fatalf("field %q missing from %+v", path, fields)
	return application.ConfigField{}
}

func configChangeByPath(t *testing.T, changes []application.ConfigChange, path string) application.ConfigChange {
	t.Helper()
	for _, change := range changes {
		if change.Path == path {
			return change
		}
	}
	t.Fatalf("change %q missing from %+v", path, changes)
	return application.ConfigChange{}
}

type configApplicationTestObserver struct {
	err     error
	calls   int
	intents []application.ConfigCommitIntent
	before  func(context.Context, application.ConfigCommitIntent) error
}

func (observer *configApplicationTestObserver) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	observer.calls++
	observer.intents = append(observer.intents, intent)
	if observer.before != nil {
		return observer.before(ctx, intent)
	}
	return observer.err
}

func configApplicationTargetBytes(t *testing.T, workspace string) map[string][]byte {
	t.Helper()
	paths := []string{
		workspaceConfigPath(workspace),
		filepath.Join(stateDir(workspace), "secrets.yml"),
		managedConfigStatePath(stateDir(workspace)),
	}
	out := make(map[string][]byte, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = body
	}
	return out
}

func assertConfigApplicationTargetsEqual(t *testing.T, want map[string][]byte) {
	t.Helper()
	for path, expected := range want {
		actual, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(actual, expected) {
			t.Errorf("%s changed despite rejected/read-only operation", path)
		}
	}
}

func requireConfigApplicationError(t *testing.T, err error, kind application.ErrorKind, code string) {
	t.Helper()
	appErr, ok := application.ErrorOf(err)
	if !ok {
		t.Fatalf("error = %T %v, want application error", err, err)
	}
	if appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("error kind/code = %q/%q, want %q/%q; cause=%v", appErr.Kind, appErr.Code, kind, code, appErr.Cause)
	}
}

func TestWorkspaceConfigGetRedactsPrivateValuesAndReportsSensitiveState(t *testing.T) {
	service, _ := newConfigApplicationTestService(t, configApplicationTestBody, true)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Managed || snapshot.Validator == "" {
		t.Fatalf("managed snapshot = %+v", snapshot)
	}
	if got, want := snapshot.AvailableModules, []string{"demo", "fieldless"}; !slices.Equal(got, want) {
		t.Fatalf("available modules = %v, want %v", got, want)
	}
	body, err := json.Marshal(snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"private-password", "private-migration", "private-fixed", "private-rotation", "vault-alias", "raw-private", "EXTRA_SECRET"} {
		if bytes.Contains(body, []byte(private)) {
			t.Errorf("GET config leaked %q: %s", private, body)
		}
	}
	values := candidateDemoConfig(t, candidateFromConfigSnapshot(t, snapshot))
	if values["public"] != "visible" {
		t.Fatalf("public value = %#v", values["public"])
	}
	for _, privateField := range []string{"password", "migrate", "fixed", "alias"} {
		if _, ok := values[privateField]; ok {
			t.Errorf("redacted field %s remained in public config", privateField)
		}
	}
	password := fieldByPath(t, snapshot.Fields, "demo.password")
	if !password.Sensitive || password.SensitiveState != "set" || !password.Editable {
		t.Errorf("password field = %+v", password)
	}
	if got, want := password.DocumentPath, []string{"modules", "demo", "config", "password"}; !slices.Equal(got, want) {
		t.Errorf("password document path = %v, want %v", got, want)
	}
	baseDomain := fieldByPath(t, snapshot.Fields, "global.base_domain")
	if got, want := baseDomain.DocumentPath, []string{"global", "base_domain"}; !slices.Equal(got, want) {
		t.Errorf("base domain document path = %v, want %v", got, want)
	}
	rotate := fieldByPath(t, snapshot.Fields, "demo.rotate")
	if !rotate.Sensitive || rotate.SensitiveState != "set" || rotate.Editable {
		t.Errorf("rotate field = %+v", rotate)
	}
	alias := fieldByPath(t, snapshot.Fields, "demo.alias")
	if !alias.Sensitive || alias.SensitiveState != "set" || alias.Editable {
		t.Errorf("effective-sensitive alias field = %+v", alias)
	}
	for _, field := range snapshot.Fields {
		if field.AllowedValues == nil {
			t.Errorf("field %s encoded allowed_values as null rather than an array", field.Path)
		}
	}
}

func TestWorkspaceConfigMigratesLegacyManagedStateBeforeExposure(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	legacy := makeConfigApplicationStateLegacy(t, workspace)

	const readers = 2
	results := make(chan application.ConfigSnapshot, readers)
	errorsSeen := make(chan error, readers)
	for index := 0; index < readers; index++ {
		go func() {
			snapshot, err := service.GetConfig(context.Background())
			results <- snapshot
			errorsSeen <- err
		}()
	}
	var validator string
	for index := 0; index < readers; index++ {
		if err := <-errorsSeen; err != nil {
			t.Fatal(err)
		}
		snapshot := <-results
		if !validManagedConfigValidator(snapshot.Validator) || snapshot.Validator == legacy.ContentDigest {
			t.Fatalf("migrated validator = %q, legacy content digest = %q", snapshot.Validator, legacy.ContentDigest)
		}
		if validator != "" && snapshot.Validator != validator {
			t.Fatalf("concurrent readers observed different validators: %q and %q", validator, snapshot.Validator)
		}
		validator = snapshot.Validator
	}

	managed, err := readManagedConfigSnapshot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if managed.contentDigest != legacy.ContentDigest || managed.validator != validator {
		t.Fatalf("migrated state = digest %q validator %q", managed.contentDigest, managed.validator)
	}
}

func TestWorkspaceConfigLegacyMigrationRejectsManualEdit(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	makeConfigApplicationStateLegacy(t, workspace)
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte(configApplicationTestBody+"future: changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := service.GetConfig(context.Background())
	requireConfigApplicationError(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
	body, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		t.Fatal(err)
	}
	var state managedConfigState
	if err := yaml.Unmarshal(body, &state); err != nil {
		t.Fatal(err)
	}
	if state.Validator != "" {
		t.Fatalf("failed migration published validator %q", state.Validator)
	}
}

func TestWorkspaceConfigPutRejectsExternalManualConfigEdit(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	candidateDemoConfig(t, candidate)["public"] = "next"

	configPath := workspaceConfigPath(workspace)
	externalBody := []byte(configApplicationTestBody + "\n# edited outside ANAS\n")
	if err := os.WriteFile(configPath, externalBody, 0o600); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := service.GetConfig(context.Background()); err == nil {
		t.Fatal("GET accepted config.yml edited outside ANAS")
	} else {
		requireConfigApplicationError(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
	}
	observer := &configApplicationTestObserver{}
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID: configTransactionTestOperationID,
		Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
	}, observer)
	requireConfigApplicationError(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
	if observer.calls != 0 {
		t.Fatalf("observer called %d times for externally modified config", observer.calls)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configAfter, externalBody) {
		t.Fatalf("failed PUT overwrote external config: %q", configAfter)
	}
	stateAfter, err := os.ReadFile(managedConfigStatePath(stateDir(workspace)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateAfter, stateBefore) {
		t.Fatal("failed PUT changed managed config state")
	}
}

func TestWorkspaceConfigGetRedactsDeclaredSensitiveEnvSpellingAndEqualAliases(t *testing.T) {
	body := `module_source: official
modules:
  demo:
    config:
      public: visible
      alias: env-private-password
env:
  DEMO_PASSWORD: env-private-password
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
`
	service, _ := newConfigApplicationTestService(t, body, false)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot.Config)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"env-private-password", "DEMO_PASSWORD", "DEMO_ALIAS"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Errorf("GET config leaked declared-sensitive env spelling or alias %q: %s", forbidden, encoded)
		}
	}
	password := fieldByPath(t, snapshot.Fields, "demo.password")
	if !password.Sensitive || password.SensitiveState != "set" {
		t.Fatalf("password field = %+v", password)
	}
	alias := fieldByPath(t, snapshot.Fields, "demo.alias")
	if !alias.Sensitive || alias.SensitiveState != "set" || alias.Editable {
		t.Fatalf("equal-value alias field = %+v", alias)
	}

	result, err := service.ValidateConfig(context.Background(), candidateFromConfigSnapshot(t, snapshot))
	if err != nil {
		t.Fatalf("GET projection did not round-trip through validate: %v", err)
	}
	validated, err := json.Marshal(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(validated, []byte("env-private-password")) {
		t.Fatalf("validate response leaked sensitive env value: %s", validated)
	}
}

func TestWorkspaceConfigValidateIsReadOnlyAndRoundTripsPrivateAliases(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, true)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	candidateDemoConfig(t, candidate)["public"] = "next"
	before := configApplicationTargetBytes(t, workspace)
	result, err := service.ValidateConfig(context.Background(), candidate)
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseValidator != snapshot.Validator {
		t.Fatalf("validation base validator = %q, want %q", result.BaseValidator, snapshot.Validator)
	}
	serialized, err := json.Marshal(result.Config)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(serialized, []byte("vault-alias")) || bytes.Contains(serialized, []byte("raw-private")) {
		t.Fatalf("validation leaked private alias: %s", serialized)
	}
	assertConfigApplicationTargetsEqual(t, before)
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("validate left a config transaction: %v", err)
	}
}

func TestWorkspaceConfigValidateCancellationTerminatesModuleHook(t *testing.T) {
	baseService, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	reg := configApplicationTestRegistry()
	module := reg["demo"]
	module.SourceDir = t.TempDir()
	module.Version = "1.0.0"
	module.Revision = 1
	started := filepath.Join(t.TempDir(), "hook-started")
	module.Hook = HookConfig{
		Command: moduleValidationHookHelperCommand("block", started),
		Phases:  []string{"validate"},
	}
	reg["demo"] = module
	service := newWorkspaceConfigServiceWithRegistry(workspace, reg)
	pinConfigTestModules(t, workspaceConfigPath(workspace), reg, "demo")

	snapshot, err := baseService.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := service.ValidateConfig(ctx, candidate)
		result <- err
	}()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	waiting := true
	for waiting {
		select {
		case <-deadline.C:
			t.Fatal("validation Hook did not start")
		case <-ticker.C:
			if _, statErr := os.Stat(started); statErr == nil {
				waiting = false
			} else if !os.IsNotExist(statErr) {
				t.Fatal(statErr)
			}
		}
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("validation cancellation error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("validation Hook ignored request cancellation")
	}
}

func TestWorkspaceConfigPutSensitiveOperations(t *testing.T) {
	tests := []struct {
		name      string
		operation application.ConfigSensitiveOperation
		value     *string
		wantSet   bool
		wantValue string
	}{
		{name: "unchanged", operation: application.ConfigSensitiveUnchanged, wantSet: true, wantValue: "private-password"},
		{name: "set", operation: application.ConfigSensitiveSet, value: stringPointer("new-private"), wantSet: true, wantValue: "new-private"},
		{name: "explicit empty set", operation: application.ConfigSensitiveSet, value: stringPointer(""), wantSet: true, wantValue: ""},
		{name: "unset", operation: application.ConfigSensitiveUnset, wantSet: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
			snapshot, err := service.GetConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			candidate := candidateFromConfigSnapshot(t, snapshot)
			candidateDemoConfig(t, candidate)["public"] = "next"
			candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: test.operation, Value: test.value}
			observer := &configApplicationTestObserver{}
			result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
				OperationID: configTransactionTestOperationID, Candidate: candidate,
				Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
			}, observer)
			if err != nil {
				t.Fatal(err)
			}
			if observer.calls != 1 || len(observer.intents) != 1 ||
				observer.intents[0].OperationID != configTransactionTestOperationID || observer.intents[0].CurrentValidator != snapshot.Validator {
				t.Fatalf("observer = calls %d intents %+v", observer.calls, observer.intents)
			}
			if result.Validator == "" || result.Validator == snapshot.Validator || result.PreviousValidator != snapshot.Validator {
				t.Fatalf("PUT digests = %+v", result)
			}
			if got, want := result.AvailableModules, []string{"demo", "fieldless"}; !slices.Equal(got, want) {
				t.Fatalf("PUT available modules = %v, want %v", got, want)
			}
			settings, cleanup, err := settingsFromConfigBytes(mustReadConfigApplicationFile(t, workspaceConfigPath(workspace)))
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			value, present := settings["modules.demo.config.password"]
			if present != test.wantSet || value != test.wantValue {
				t.Errorf("saved password = %q, present=%t; want %q, %t", value, present, test.wantValue, test.wantSet)
			}
			field := fieldByPath(t, result.Fields, "demo.password")
			wantState := "unset"
			if test.wantSet {
				wantState = "set"
			}
			if field.SensitiveState != wantState {
				t.Errorf("password state = %q, want %q", field.SensitiveState, wantState)
			}
			resultBody, err := json.Marshal(result.Config)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantValue != "" && bytes.Contains(resultBody, []byte(test.wantValue)) {
				t.Fatalf("PUT response leaked sensitive value: %s", resultBody)
			}
		})
	}
}

func TestWorkspaceConfigRejectsGuardedSensitiveAndRawOperations(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	for _, test := range []struct {
		name     string
		path     string
		mutation application.ConfigSensitiveMutation
	}{
		{name: "credential rotate", path: "demo.rotate", mutation: application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: stringPointer("new")}},
		{name: "data migrate", path: "demo.migrate", mutation: application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnset}},
		{name: "immutable", path: "demo.fixed", mutation: application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: stringPointer("new")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := candidateFromConfigSnapshot(t, snapshot)
			candidate.Sensitive[test.path] = test.mutation
			_, err := service.ValidateConfig(context.Background(), candidate)
			requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_field_read_only")
			assertConfigApplicationTargetsEqual(t, before)
		})
	}
	t.Run("raw sensitive value", func(t *testing.T) {
		candidate := candidateFromConfigSnapshot(t, snapshot)
		candidateDemoConfig(t, candidate)["password"] = "request-plaintext"
		_, err := service.ValidateConfig(context.Background(), candidate)
		requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
	})
	t.Run("unknown operation", func(t *testing.T) {
		candidate := candidateFromConfigSnapshot(t, snapshot)
		candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: "replace", Value: stringPointer("request-plaintext")}
		_, err := service.ValidateConfig(context.Background(), candidate)
		requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_sensitive_operation_invalid")
	})
}

func TestWorkspaceConfigRejectsUnknownEmptyModuleMappings(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	tests := []struct {
		name   string
		mutate func(*testing.T, application.ConfigCandidate)
	}{
		{
			name: "module",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				candidateDemoModule(t, candidate)["unknown"] = map[string]any{}
			},
		},
		{
			name: "module case alias",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				modules := candidate.Document["modules"].(map[string]any)
				demo := modules["demo"]
				delete(modules, "demo")
				modules["DEMO"] = demo
			},
		},
		{
			name: "config parameter",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				candidateDemoConfig(t, candidate)["unknown"] = map[string]any{}
			},
		},
		{
			name: "config parameter case alias",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				values := candidateDemoConfig(t, candidate)
				public := values["public"]
				delete(values, "public")
				values["PUBLIC"] = public
			},
		},
		{
			name: "identity",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				candidateDemoModule(t, candidate)["identity"] = map[string]any{"unknown": map[string]any{}}
			},
		},
		{
			name: "administration",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				candidateDemoModule(t, candidate)["administration"] = map[string]any{"unknown": map[string]any{}}
			},
		},
		{
			name: "local account",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				candidateDemoModule(t, candidate)["administration"] = map[string]any{
					"local_accounts": map[string]any{"intruder": map[string]any{}},
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := candidateFromConfigSnapshot(t, snapshot)
			test.mutate(t, candidate)
			_, err := service.ValidateConfig(context.Background(), candidate)
			requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
			assertConfigApplicationTargetsEqual(t, before)

			candidate = candidateFromConfigSnapshot(t, snapshot)
			test.mutate(t, candidate)
			observer := &configApplicationTestObserver{}
			_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
				OperationID:       configTransactionTestOperationID,
				Candidate:         candidate,
				Precondition:      application.ConfigPreconditionMatch,
				ExpectedValidator: snapshot.Validator,
			}, observer)
			requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
			if observer.calls != 0 {
				t.Fatalf("invalid candidate reached observer %d times", observer.calls)
			}
			assertConfigApplicationTargetsEqual(t, before)
		})
	}
}

func TestWorkspaceConfigRejectsRemovalOfReadOnlyLocalAccount(t *testing.T) {
	reg := configApplicationTestRegistry()
	demo := reg["demo"]
	demo.LocalAccounts = []LocalAccount{{ID: "primary"}}
	reg["demo"] = demo
	body := strings.Replace(configApplicationTestBody, "    config:\n", "    administration:\n      local_accounts:\n        primary: {}\n    config:\n", 1)
	service, workspace := newConfigApplicationTestServiceWithRegistry(t, body, false, reg)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	tests := []struct {
		name   string
		mutate func(*testing.T, application.ConfigCandidate)
	}{
		{
			name: "account",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				demo := candidateDemoModule(t, candidate)
				administration := demo["administration"].(map[string]any)
				accounts := administration["local_accounts"].(map[string]any)
				delete(accounts, "primary")
			},
		},
		{
			name: "local accounts mapping",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				administration := candidateDemoModule(t, candidate)["administration"].(map[string]any)
				delete(administration, "local_accounts")
			},
		},
		{
			name: "administration mapping",
			mutate: func(t *testing.T, candidate application.ConfigCandidate) {
				delete(candidateDemoModule(t, candidate), "administration")
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := candidateFromConfigSnapshot(t, snapshot)
			test.mutate(t, candidate)
			_, err := service.ValidateConfig(context.Background(), candidate)
			requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
			assertConfigApplicationTargetsEqual(t, before)

			candidate = candidateFromConfigSnapshot(t, snapshot)
			test.mutate(t, candidate)
			observer := &configApplicationTestObserver{}
			_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
				OperationID: configTransactionTestOperationID,
				Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
			}, observer)
			requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
			if observer.calls != 0 {
				t.Fatalf("invalid candidate reached observer %d times", observer.calls)
			}
			assertConfigApplicationTargetsEqual(t, before)
		})
	}
}

func TestWorkspaceConfigReportsEmptyModuleSelectionChanges(t *testing.T) {
	assertChange := func(t *testing.T, service *workspaceConfigApplication, mutate func(application.ConfigCandidate), path, wantChange string) {
		t.Helper()
		snapshot, err := service.GetConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		candidate := candidateFromConfigSnapshot(t, snapshot)
		mutate(candidate)
		validation, err := service.ValidateConfig(context.Background(), candidate)
		if err != nil {
			t.Fatal(err)
		}
		change := configChangeByPath(t, validation.Changes, path)
		if change.Change != wantChange || change.Effect != "container_recreate" || change.Apply != "render-and-recreate" || change.Sensitive || !change.Editable {
			t.Fatalf("validation change = %+v", change)
		}
		observer := &configApplicationTestObserver{}
		_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
			OperationID: configTransactionTestOperationID,
			Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
		}, observer)
		if err != nil {
			t.Fatal(err)
		}
		if observer.calls != 1 {
			t.Fatalf("observer calls = %d", observer.calls)
		}
		intentChange := configChangeByPath(t, observer.intents[0].Changes, path)
		if intentChange != change {
			t.Fatalf("authorized change = %+v, validation change = %+v", intentChange, change)
		}
	}

	t.Run("add", func(t *testing.T) {
		reg := configApplicationTestRegistry()
		reg["extra"] = Module{Name: "extra", EnvPrefix: "EXTRA", Changes: map[string]ChangePolicy{}, Types: map[string]ParamType{}}
		service, _ := newConfigApplicationTestServiceWithRegistry(t, configApplicationTestBody, false, reg)
		assertChange(t, service, func(candidate application.ConfigCandidate) {
			modules := candidate.Document["modules"].(map[string]any)
			modules["extra"] = map[string]any{}
		}, "modules.extra", "add")
	})

	t.Run("remove", func(t *testing.T) {
		body := `module_source: official
modules:
  demo: {}
  extra: {}
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
`
		reg := configApplicationTestRegistry()
		reg["extra"] = Module{Name: "extra", EnvPrefix: "EXTRA", Changes: map[string]ChangePolicy{}, Types: map[string]ParamType{}}
		service, _ := newConfigApplicationTestServiceWithRegistry(t, body, false, reg)
		assertChange(t, service, func(candidate application.ConfigCandidate) {
			modules := candidate.Document["modules"].(map[string]any)
			delete(modules, "demo")
		}, "modules.demo", "remove")
	})
}

// REQUIREMENTS: CONSOLE-R-120
func TestWorkspaceConfigValidatesBundledFieldlessModuleSelection(t *testing.T) {
	reg, err := loadRegistry(repoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	service, _ := newConfigApplicationTestServiceWithRegistry(t, `module_source: official
modules:
  samba_dc:
    config:
      domain: r120.test
global:
  base_domain: r120.test
  email: admin@r120.test
  timezone: UTC
env:
  CONTAINER_PREFIX: anas_r120_
  NETWORK_PREFIX: anas_r120_
`, false, reg)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := publicCandidateFromConfigSnapshot(t, snapshot)
	modules := candidate.Document["modules"].(map[string]any)
	modules["freeradius"] = map[string]any{}
	global := candidate.Document["global"].(map[string]any)
	global["timezone"] = "Asia/Singapore"

	validation, err := service.ValidateConfig(context.Background(), candidate)
	if err != nil {
		t.Fatalf("%v (cause: %v)", err, errors.Unwrap(err))
	}
	if change := configChangeByPath(t, validation.Changes, "modules.freeradius"); change.Change != "add" {
		t.Fatalf("freeradius change = %+v", change)
	}
	if change := configChangeByPath(t, validation.Changes, "global.timezone"); change.Change != "change" {
		t.Fatalf("timezone change = %+v", change)
	}
	validatedModules, ok := validation.Config["modules"].(map[string]any)
	if !ok {
		t.Fatalf("validated modules = %#v", validation.Config["modules"])
	}
	if _, ok := validatedModules["freeradius"]; !ok {
		t.Fatalf("validated fieldless module moved outside modules: %#v", validation.Config)
	}
	if _, ok := validation.Config["freeradius"]; ok {
		t.Fatalf("validated fieldless module leaked into the root: %#v", validation.Config)
	}

	candidate.Document["env"].(map[string]any)["CONTAINER_PREFIX"] = "changed_"
	_, err = service.ValidateConfig(context.Background(), candidate)
	requireConfigApplicationError(t, err, application.ErrorKindInvalidArgument, "config_candidate_invalid")
}

func TestWorkspaceConfigPreservesModuleOrderAcrossJSONRoundTrip(t *testing.T) {
	reg := map[string]Module{}
	for _, name := range []string{"zeta", "alpha", "middle", "beta", "aardvark"} {
		reg[name] = Module{Name: name, EnvPrefix: strings.ToUpper(name), Changes: map[string]ChangePolicy{}, Types: map[string]ParamType{}}
	}
	body := `module_source: official
modules:
  zeta: {}
  alpha: {}
  middle: {}
global:
  base_domain: nas.test
  email: admin@nas.test
  timezone: UTC
`

	t.Run("no-op", func(t *testing.T) {
		service, workspace := newConfigApplicationTestServiceWithRegistry(t, body, false, reg)
		snapshot, err := service.GetConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		candidate := publicCandidateFromConfigSnapshot(t, snapshot)
		validation, err := service.ValidateConfig(context.Background(), candidate)
		if err != nil {
			t.Fatal(err)
		}
		if validation.BaseValidator != snapshot.Validator || len(validation.Changes) != 0 {
			t.Fatalf("round-trip validation = %+v, base validator %q", validation, snapshot.Validator)
		}
		observer := &configApplicationTestObserver{}
		result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
			OperationID: configTransactionTestOperationID,
			Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
		}, observer)
		if err != nil {
			t.Fatal(err)
		}
		if result.Validator != snapshot.Validator || len(result.Changes) != 0 || observer.calls != 1 || len(observer.intents[0].Changes) != 0 {
			t.Fatalf("round-trip PUT = %+v, observer = %+v", result, observer)
		}
		loaded, err := config.Load(workspaceConfigPath(workspace))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(loaded.Modules.Order, ","), "zeta,alpha,middle"; got != want {
			t.Fatalf("module order = %q, want %q", got, want)
		}
	})

	t.Run("append new modules deterministically", func(t *testing.T) {
		service, workspace := newConfigApplicationTestServiceWithRegistry(t, body, false, reg)
		snapshot, err := service.GetConfig(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		candidate := publicCandidateFromConfigSnapshot(t, snapshot)
		modules := candidate.Document["modules"].(map[string]any)
		modules["beta"] = map[string]any{}
		modules["aardvark"] = map[string]any{}
		observer := &configApplicationTestObserver{}
		result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
			OperationID: configTransactionTestOperationID,
			Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
		}, observer)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"aardvark", "beta"} {
			if change := configChangeByPath(t, result.Changes, "modules."+name); change.Change != "add" {
				t.Fatalf("module %s change = %+v", name, change)
			}
		}
		loaded, err := config.Load(workspaceConfigPath(workspace))
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(loaded.Modules.Order, ","), "zeta,alpha,middle,aardvark,beta"; got != want {
			t.Fatalf("module order = %q, want %q", got, want)
		}
	})
}

func TestWorkspaceConfigPutPreconditionsAndObserverFailureDoNotWrite(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	candidateDemoConfig(t, candidate)["public"] = "next"
	before := configApplicationTargetBytes(t, workspace)
	missingOperationObserver := &configApplicationTestObserver{}
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
		Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
	}, missingOperationObserver)
	requireConfigApplicationError(t, err, application.ErrorKindInternal, "audit_unavailable")
	if missingOperationObserver.calls != 0 {
		t.Fatalf("invalid operation ID reached observer %d times", missingOperationObserver.calls)
	}
	assertConfigApplicationTargetsEqual(t, before)

	for _, test := range []struct {
		name      string
		mode      application.ConfigPreconditionMode
		validator string
		wantKind  application.ErrorKind
		wantCode  string
	}{
		{name: "missing", mode: application.ConfigPreconditionNone, wantKind: application.ErrorKindPreconditionRequired, wantCode: "config_precondition_required"},
		{name: "stale", mode: application.ConfigPreconditionMatch, validator: "cfgv-ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", wantKind: application.ErrorKindFailedPrecondition, wantCode: "config_precondition_failed"},
		{name: "must create against managed", mode: application.ConfigPreconditionMustCreate, wantKind: application.ErrorKindPreconditionRequired, wantCode: "config_precondition_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			observer := &configApplicationTestObserver{}
			_, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
				OperationID: configTransactionTestOperationID, Candidate: candidate,
				Precondition: test.mode, ExpectedValidator: test.validator,
			}, observer)
			requireConfigApplicationError(t, err, test.wantKind, test.wantCode)
			if observer.calls != 0 {
				t.Errorf("observer called %d times before failed CAS", observer.calls)
			}
			assertConfigApplicationTargetsEqual(t, before)
		})
	}

	observer := &configApplicationTestObserver{err: errors.New("audit disk full")}
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID: configTransactionTestOperationID, Candidate: candidate,
		Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
	}, observer)
	requireConfigApplicationError(t, err, application.ErrorKindInternal, "audit_unavailable")
	if observer.calls != 1 {
		t.Fatalf("observer calls = %d, want 1", observer.calls)
	}
	assertConfigApplicationTargetsEqual(t, before)
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("observer veto left transaction state: %v", err)
	}
}

func TestConfigTransactionApplicationErrorDistinguishesWALBoundary(t *testing.T) {
	cause := errors.New("injected transaction failure")
	for _, test := range []struct {
		name  string
		phase configTransactionCommitPhase
		code  string
	}{
		{name: "before WAL", phase: configTransactionBeforeWAL, code: "config_unavailable"},
		{name: "after WAL", phase: configTransactionAfterWAL, code: "config_recovery_required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := configTransactionApplicationError(configTransactionFailure(test.phase, cause))
			requireConfigApplicationError(t, err, application.ErrorKindInternal, test.code)
			if !errors.Is(err, cause) {
				t.Fatalf("application error did not retain transaction cause: %v", err)
			}
		})
	}
}

func TestWorkspaceConfigPutDetectsUnlockedTargetChangeBeforeWAL(t *testing.T) {
	for _, test := range []struct {
		name string
		role string
		noOp bool
		body []byte
	}{
		{name: "config", role: "config", body: []byte("manual config edit\n")},
		{name: "secret store", role: "secrets", body: []byte("manual secret edit\n")},
		{name: "managed state", role: "managed_state", body: []byte("manual state edit\n")},
		{name: "no-op config", role: "config", noOp: true, body: []byte("manual no-op race\n")},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
			snapshot, err := service.GetConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			candidate := candidateFromConfigSnapshot(t, snapshot)
			if !test.noOp {
				candidateDemoConfig(t, candidate)["public"] = "next"
			}
			paths := map[string]string{
				"config":        workspaceConfigPath(workspace),
				"secrets":       filepath.Join(stateDir(workspace), "secrets.yml"),
				"managed_state": managedConfigStatePath(stateDir(workspace)),
			}
			before := configApplicationTargetBytes(t, workspace)
			target := paths[test.role]
			observer := &configApplicationTestObserver{before: func(context.Context, application.ConfigCommitIntent) error {
				return os.WriteFile(target, test.body, 0o600)
			}}
			_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{
				OperationID: configTransactionTestOperationID,
				Candidate:   candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator,
			}, observer)
			requireConfigApplicationError(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
			if observer.calls != 1 {
				t.Fatalf("observer calls = %d", observer.calls)
			}
			for path, expected := range before {
				if path == target {
					expected = test.body
				}
				actual, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(actual, expected) {
					t.Fatalf("target %s = %q, want %q", path, actual, expected)
				}
			}
			if _, statErr := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(statErr) {
				t.Fatalf("precondition race published transaction state: %v", statErr)
			}
		})
	}
}

func TestWorkspaceConfigConcurrentPutUsesCASInsideRuntimeLock(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	initial, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	type contender struct {
		value    string
		request  application.ConfigPutRequest
		observer *configApplicationTestObserver
	}
	contenders := make([]contender, 0, 2)
	for _, value := range []string{"alpha", "beta"} {
		candidate := candidateFromConfigSnapshot(t, initial)
		candidateDemoConfig(t, candidate)["public"] = value
		contenders = append(contenders, contender{
			value: value,
			request: application.ConfigPutRequest{
				OperationID:       configTransactionTestOperationID,
				Candidate:         candidate,
				Precondition:      application.ConfigPreconditionMatch,
				ExpectedValidator: initial.Validator,
			},
			observer: &configApplicationTestObserver{},
		})
	}
	type outcome struct {
		value  string
		result application.ConfigPutResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, len(contenders))
	for index := range contenders {
		current := &contenders[index]
		go func() {
			<-start
			result, err := service.PutConfig(context.Background(), current.request, current.observer)
			outcomes <- outcome{value: current.value, result: result, err: err}
		}()
	}
	close(start)
	got := []outcome{<-outcomes, <-outcomes}

	var winner outcome
	successes, preconditionFailures := 0, 0
	for _, result := range got {
		if result.err == nil {
			successes++
			winner = result
			continue
		}
		appErr, ok := application.ErrorOf(result.err)
		if ok && appErr.Kind == application.ErrorKindFailedPrecondition && appErr.Code == "config_precondition_failed" {
			preconditionFailures++
			continue
		}
		t.Errorf("contender %s error = %T %v", result.value, result.err, result.err)
	}
	if successes != 1 || preconditionFailures != 1 {
		t.Fatalf("concurrent outcomes: successes=%d precondition failures=%d", successes, preconditionFailures)
	}
	if calls := contenders[0].observer.calls + contenders[1].observer.calls; calls != 1 {
		t.Fatalf("audit observer calls = %d, want only the CAS winner", calls)
	}

	final, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.Validator != winner.result.Validator || winner.result.PreviousValidator != initial.Validator {
		t.Fatalf("winner digests = previous %q result %q; final %q initial %q",
			winner.result.PreviousValidator, winner.result.Validator, final.Validator, initial.Validator)
	}
	settings, cleanup, err := settingsFromConfigBytes(mustReadConfigApplicationFile(t, workspaceConfigPath(workspace)))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if got := settings["modules.demo.config.public"]; got != winner.value {
		t.Fatalf("saved public value = %q, want winning value %q", got, winner.value)
	}
	managed, err := readManagedConfigSnapshot(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !managed.managed || managed.validator != final.Validator || configDigest(managed.configBytes) != managed.contentDigest {
		t.Fatalf("final config/state generation is inconsistent: managed=%t validator=%q state digest=%q config digest=%q response=%q",
			managed.managed, managed.validator, managed.contentDigest, configDigest(managed.configBytes), final.Validator)
	}
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("concurrent PUT left transaction state: %v", err)
	}
}

func TestWorkspaceConfigNoopPutStillPassesAuditObserver(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	initial, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	canonicalCandidate := candidateFromConfigSnapshot(t, initial)
	candidateDemoConfig(t, canonicalCandidate)["public"] = "canonical"
	if _, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID:       configTransactionTestOperationID,
		Candidate:         canonicalCandidate,
		Precondition:      application.ConfigPreconditionMatch,
		ExpectedValidator: initial.Validator,
	}, &configApplicationTestObserver{}); err != nil {
		t.Fatalf("canonicalizing first PUT: %v", err)
	}

	current, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	observer := &configApplicationTestObserver{}
	result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID:       configTransactionTestOperationID,
		Candidate:         candidateFromConfigSnapshot(t, current),
		Precondition:      application.ConfigPreconditionMatch,
		ExpectedValidator: current.Validator,
	}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if observer.calls != 1 || len(observer.intents) != 1 {
		t.Fatalf("no-op observer = calls %d intents %+v", observer.calls, observer.intents)
	}
	if intent := observer.intents[0]; intent.CurrentValidator != current.Validator || intent.CandidateValidator != current.Validator || len(intent.Changes) != 0 {
		t.Fatalf("no-op audit intent = %+v, want unchanged digest and no changes", intent)
	}
	if result.Validator != current.Validator || result.PreviousValidator != current.Validator {
		t.Fatalf("no-op PUT digest changed: result=%+v current=%q", result, current.Validator)
	}
	assertConfigApplicationTargetsEqual(t, before)
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatalf("no-op PUT created transaction state: %v", err)
	}
}

func TestWorkspaceConfigStoreRecreationRotatesValidator(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	initial, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID: configTransactionTestOperationID, Candidate: candidateFromConfigSnapshot(t, initial),
		Precondition: application.ConfigPreconditionMatch, ExpectedValidator: initial.Validator,
	}, &configApplicationTestObserver{}); err != nil {
		t.Fatalf("canonicalize config: %v", err)
	}
	secretPath := filepath.Join(stateDir(workspace), "secrets.yml")
	if err := os.Remove(secretPath); err != nil {
		t.Fatal(err)
	}
	current, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	configBefore, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{
		OperationID: configTransactionTestOperationID, Candidate: candidateFromConfigSnapshot(t, current),
		Precondition: application.ConfigPreconditionMatch, ExpectedValidator: current.Validator,
	}, &configApplicationTestObserver{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Validator == current.Validator || !validManagedConfigValidator(result.Validator) {
		t.Fatalf("recreated store did not rotate config validator: %q -> %q", current.Validator, result.Validator)
	}
	configAfter, err := os.ReadFile(workspaceConfigPath(workspace))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configBefore, configAfter) {
		t.Fatal("recreating a missing Secret Store changed config.yml")
	}
	info, err := os.Stat(secretPath)
	if err != nil {
		t.Fatalf("missing Secret Store was not recreated: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("recreated Secret Store mode = %o, want 600", info.Mode().Perm())
	}
	body, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("api_version: anas.secrets/v2")) {
		t.Fatalf("recreated Secret Store is not v2: %s", body)
	}
}

func TestWorkspaceConfigSnapshotRejectsSymlinkedAndOversizedTargets(t *testing.T) {
	t.Run("symlinked secret store", func(t *testing.T) {
		service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
		secretPath := filepath.Join(stateDir(workspace), "secrets.yml")
		external := filepath.Join(t.TempDir(), "external-secrets.yml")
		body, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(external, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(secretPath); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(external, secretPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		_, err = service.GetConfig(context.Background())
		requireConfigApplicationError(t, err, application.ErrorKindInternal, "secrets_unavailable")
	})

	for _, test := range []struct {
		name  string
		role  string
		limit int64
		code  string
	}{
		{name: "config", role: "config", limit: configTransactionMaxConfigSize, code: "config_unavailable"},
		{name: "secret store", role: "secrets", limit: configTransactionMaxSecretsSize, code: "secrets_unavailable"},
		{name: "managed state", role: "managed_state", limit: configTransactionMaxStateSize, code: "config_unavailable"},
	} {
		t.Run("oversized "+test.name, func(t *testing.T) {
			service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
			paths := map[string]string{
				"config":        workspaceConfigPath(workspace),
				"secrets":       filepath.Join(stateDir(workspace), "secrets.yml"),
				"managed_state": managedConfigStatePath(stateDir(workspace)),
			}
			file, err := os.OpenFile(paths[test.role], os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			if err := file.Truncate(test.limit + 1); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = service.GetConfig(context.Background())
			requireConfigApplicationError(t, err, application.ErrorKindInternal, test.code)
		})
	}
}

func TestConfigPreconditionsCoverManagedAndInitialGenerations(t *testing.T) {
	managed := managedConfigSnapshot{managed: true, validator: "cfgv-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}
	if err := checkConfigPrecondition(managed, application.ConfigPutRequest{
		Precondition: application.ConfigPreconditionMatch, ExpectedValidator: managed.validator,
	}); err != nil {
		t.Fatalf("matching managed precondition: %v", err)
	}
	requireConfigApplicationError(t,
		checkConfigPrecondition(managed, application.ConfigPutRequest{Precondition: application.ConfigPreconditionMatch, ExpectedValidator: "cfgv-ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}),
		application.ErrorKindFailedPrecondition, "config_precondition_failed")
	requireConfigApplicationError(t,
		checkConfigPrecondition(managed, application.ConfigPutRequest{}),
		application.ErrorKindPreconditionRequired, "config_precondition_required")

	initial := managedConfigSnapshot{}
	if err := checkConfigPrecondition(initial, application.ConfigPutRequest{Precondition: application.ConfigPreconditionMustCreate}); err != nil {
		t.Fatalf("initial must-create precondition: %v", err)
	}
	requireConfigApplicationError(t,
		checkConfigPrecondition(initial, application.ConfigPutRequest{Precondition: application.ConfigPreconditionMatch}),
		application.ErrorKindPreconditionRequired, "config_precondition_required")
}

func TestWorkspaceConfigFailsClosedAfterExternalConfigEdit(t *testing.T) {
	service, workspace := newConfigApplicationTestService(t, configApplicationTestBody, false)
	if err := os.WriteFile(workspaceConfigPath(workspace), []byte(strings.Replace(configApplicationTestBody, "visible", "tampered", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := service.GetConfig(context.Background())
	requireConfigApplicationError(t, err, application.ErrorKindFailedPrecondition, "config_precondition_failed")
}

func stringPointer(value string) *string { return &value }

func mustReadConfigApplicationFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
