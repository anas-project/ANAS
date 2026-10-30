package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
)

func storedInputFixture(t *testing.T) (*workspaceConfigApplication, string) {
	t.Helper()
	reg := configApplicationTestRegistry()
	demo := reg["demo"]
	policy := demo.Changes["password"]
	policy.StoreInput = true
	demo.Changes["password"] = policy
	reg["demo"] = demo
	return newConfigApplicationTestServiceWithRegistry(t, configApplicationTestBody, false, reg)
}

func putStoredInput(t *testing.T, service *workspaceConfigApplication, mutation application.ConfigSensitiveMutation, observer *configApplicationTestObserver) application.ConfigPutResult {
	t.Helper()
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	candidate.Sensitive["demo.password"] = mutation
	result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, observer)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStoredInputMigratesUpdatesAndUnsetsAtomically(t *testing.T) {
	service, workspace := storedInputFixture(t)
	for _, step := range []struct {
		name, value string
		operation   application.ConfigSensitiveOperation
	}{
		{"migrate", "private-password", application.ConfigSensitiveUnchanged},
		{"update", "replacement-secret", application.ConfigSensitiveSet},
		{"unset", "", application.ConfigSensitiveUnset},
	} {
		t.Run(step.name, func(t *testing.T) {
			mutation := application.ConfigSensitiveMutation{Operation: step.operation}
			if step.operation == application.ConfigSensitiveSet {
				mutation.Value = &step.value
			}
			observer := &configApplicationTestObserver{}
			result := putStoredInput(t, service, mutation, observer)
			if observer.calls != 1 || !configChangeByPath(t, observer.intents[0].Changes, "demo.password").Sensitive {
				t.Fatal("sensitive mutation omitted from audit")
			}
			configBytes := mustReadConfigApplicationFile(t, workspaceConfigPath(workspace))
			if bytes.Contains(configBytes, []byte("private-password")) || bytes.Contains(configBytes, []byte("replacement-secret")) {
				t.Fatal("config contains private input")
			}
			store, err := loadSecretStore(stateDir(workspace))
			if err != nil || store.values["DEMO_PASSWORD"] != step.value {
				t.Fatal("incorrect private value", err)
			}
			if step.value != "" && (store.metadata["DEMO_PASSWORD"].Kind != "managed_input" || store.metadata["DEMO_PASSWORD"].Owner != "demo") {
				t.Fatal("incorrect private ownership")
			}
			if step.value != "" {
				runtime := &app{base: stateDir(workspace), env: map[string]string{}, envOwner: map[string]string{}}
				if err := runtime.loadImportedSecrets(); err != nil || runtime.env["DEMO_PASSWORD"] != step.value || runtime.envOwner["DEMO_PASSWORD"] != "!user-secret" {
					t.Fatal("runtime private input missing", err)
				}
			}
			public, _ := json.Marshal(result)
			if strings.Contains(string(public), "private-password") || strings.Contains(string(public), "replacement-secret") {
				t.Fatal("public response contains private input")
			}
		})
	}
}

func TestStoredInputIdenticalRetryIsNoOpAndStaleWriteRejected(t *testing.T) {
	service, workspace := storedInputFixture(t)
	putStoredInput(t, service, application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnchanged}, &configApplicationTestObserver{})
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	value := "private-password"
	candidate := candidateFromConfigSnapshot(t, snapshot)
	candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: &value}
	result, err := service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, &configApplicationTestObserver{})
	if err != nil || result.Validator != snapshot.Validator {
		t.Fatal("identical retry changed configuration", err)
	}
	assertConfigApplicationTargetsEqual(t, before)
	value = "other-private"
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: "cfgv-ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"}, &configApplicationTestObserver{})
	if err == nil {
		t.Fatal("stale update accepted")
	}
	assertConfigApplicationTargetsEqual(t, before)
}

func TestStoredInputPrivatePreconditionUsesTransactionSnapshot(t *testing.T) {
	service, workspace := storedInputFixture(t)
	putStoredInput(t, service, application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnchanged}, &configApplicationTestObserver{})
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	newValue, wrongOld, correctOld := "replacement-secret", "stale-secret", "private-password"
	candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: &newValue}
	before := configApplicationTargetBytes(t, workspace)
	request := application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator, ExpectedStoredInputs: map[string]*string{"demo.password": &wrongOld}}
	public, err := json.Marshal(request)
	if err != nil || bytes.Contains(public, []byte(wrongOld)) {
		t.Fatal("private precondition serialized", err)
	}
	observer := &configApplicationTestObserver{}
	_, err = service.PutConfig(context.Background(), request, observer)
	if e, ok := application.ErrorOf(err); !ok || e.Code != "config_precondition_failed" || observer.calls != 0 {
		t.Fatal("stale secret accepted", err)
	}
	assertConfigApplicationTargetsEqual(t, before)
	request.ExpectedStoredInputs["demo.password"] = &correctOld
	if _, err = service.PutConfig(context.Background(), request, observer); err != nil {
		t.Fatal("matching secret precondition failed", err)
	}
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil || store.values["DEMO_PASSWORD"] != newValue {
		t.Fatal("new private value not committed", err)
	}
}

func TestStoredInputAbsentPreconditionRejectsUnexpectedValue(t *testing.T) {
	service, workspace := storedInputFixture(t)
	putStoredInput(t, service, application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnchanged}, &configApplicationTestObserver{})
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	value := "replacement-secret"
	candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: &value}
	before := configApplicationTargetBytes(t, workspace)
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator, ExpectedStoredInputs: map[string]*string{"demo.password": nil}}, &configApplicationTestObserver{})
	if e, ok := application.ErrorOf(err); !ok || e.Code != "config_precondition_failed" {
		t.Fatal("unexpected existing credential accepted", err)
	}
	assertConfigApplicationTargetsEqual(t, before)
}

func TestStoredInputPreconditionRejectsOutOfBandChange(t *testing.T) {
	service, workspace := storedInputFixture(t)
	putStoredInput(t, service, application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnchanged}, &configApplicationTestObserver{})
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	candidate := candidateFromConfigSnapshot(t, snapshot)
	old, replacement := "private-password", "replacement-secret"
	candidate.Sensitive["demo.password"] = application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveSet, Value: &replacement}
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("DEMO_PASSWORD", "out-of-band-private", store.metadata["DEMO_PASSWORD"])
	if err := os.WriteFile(store.path, marshalSecretStore(store), 0600); err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	request := application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator, ExpectedStoredInputs: map[string]*string{"demo.password": &old}}
	_, err = service.PutConfig(context.Background(), request, &configApplicationTestObserver{})
	if e, ok := application.ErrorOf(err); !ok || e.Code != "config_precondition_failed" {
		t.Fatal("out-of-band secret change overwritten", err)
	}
	assertConfigApplicationTargetsEqual(t, before)
}

func TestStoredInputManifestPolicyIsOptInAndBounded(t *testing.T) {
	for _, test := range []struct {
		name   string
		policy manifestChangePolicy
		valid  bool
	}{
		{"allowed", manifestChangePolicy{Effect: "container_recreate", Sensitive: true, StoreInput: true}, true},
		{"ordinary", manifestChangePolicy{Effect: "container_recreate", Sensitive: true}, true},
		{"nonsecret", manifestChangePolicy{Effect: "container_recreate", StoreInput: true}, false},
		{"rotation", manifestChangePolicy{Effect: "credential_rotate", Sensitive: true, StoreInput: true}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policies, err := normalizeChangePolicies("demo", map[string]manifestChangePolicy{"password": test.policy})
			if (err == nil) != test.valid {
				t.Fatal("unexpected declaration outcome", err)
			}
			if err == nil && policies["password"].StoreInput != test.policy.StoreInput {
				t.Fatal("declaration was not preserved")
			}
		})
	}
}

func TestStoredInputConfigImportExtractsAllSpellings(t *testing.T) {
	reg := configApplicationTestRegistry()
	demo := reg["demo"]
	policy := demo.Changes["password"]
	policy.StoreInput = true
	demo.Changes["password"] = policy
	reg["demo"] = demo
	for _, test := range []struct{ name, body string }{
		{"module", "modules:\n  demo:\n    config:\n      password: private-import-value\n"},
		{"env", "modules:\n  demo: {}\nenv:\n  DEMO_PASSWORD: private-import-value\n"},
		{"secrets", "modules:\n  demo: {}\nsecrets:\n  DEMO_PASSWORD: private-import-value\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yml")
			if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}
			result, err := normalizeImportedConfig(path, reg)
			if err != nil || len(result.Secrets) != 1 || !result.Secrets[0].ManagedInput || result.Secrets[0].Key != "DEMO_PASSWORD" || bytes.Contains(result.Normalized, []byte("private-import-value")) {
				t.Fatal("stored input import failed", err)
			}
		})
	}
}

func TestStoredInputCLIRefusesPlaintextConfigSet(t *testing.T) {
	workspace := newWorkspace(t)
	for _, path := range []string{"mail.ingress_credentials", "env.MAIL_INGRESS_CREDENTIALS", "mail.outbound_passwords"} {
		stdout, stderr, exit := capture(t, "config", "set", path, "sensitive-test-value", "-w", workspace, "--root", repoRoot(t), "--defer", "--json")
		if exit == 0 || strings.Contains(stdout, "sensitive-test-value") || strings.Contains(stderr, "sensitive-test-value") {
			t.Fatal("stored input CLI accepted or exposed a secret")
		}
		config, err := os.ReadFile(workspaceConfigPath(workspace))
		if err == nil && bytes.Contains(config, []byte("sensitive-test-value")) {
			t.Fatal("CLI wrote secret into config")
		}
	}
}

func TestStoredInputAuditFailureDoesNotCommit(t *testing.T) {
	service, workspace := storedInputFixture(t)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	candidate := candidateFromConfigSnapshot(t, snapshot)
	observer := &configApplicationTestObserver{err: errors.New("audit unavailable")}
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, observer)
	if err == nil {
		t.Fatal("audit failure accepted")
	}
	assertConfigApplicationTargetsEqual(t, before)
}

func TestStoredInputInterruptedCommitRecoversTogether(t *testing.T) {
	service, workspace := storedInputFixture(t)
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actualRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") && !failed {
			failed = true
			return errors.New("injected secret publish interruption")
		}
		return actualRename(source, target)
	})
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidateFromConfigSnapshot(t, snapshot), Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, &configApplicationTestObserver{})
	if err == nil || !failed {
		t.Fatal("expected interrupted transaction", err)
	}
	configTransactionRename = actualRename
	recovered, err := service.GetConfig(context.Background())
	if err != nil || recovered.Validator == snapshot.Validator {
		t.Fatal("transaction did not recover", err)
	}
	config := mustReadConfigApplicationFile(t, workspaceConfigPath(workspace))
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil || bytes.Contains(config, []byte("private-password")) || store.values["DEMO_PASSWORD"] != "private-password" || store.metadata["DEMO_PASSWORD"].Kind != "managed_input" {
		t.Fatal("recovered configuration and secret are inconsistent", err)
	}
	if _, err := os.Lstat(configTransactionDirectory(workspace)); !os.IsNotExist(err) {
		t.Fatal("recovery journal remains", err)
	}
}

func TestStoredInputChangeRejectsLegacyPrivateAliases(t *testing.T) {
	for _, operation := range []application.ConfigSensitiveOperation{application.ConfigSensitiveSet, application.ConfigSensitiveUnset} {
		t.Run(string(operation), func(t *testing.T) {
			service, workspace := storedInputFixture(t)
			putStoredInput(t, service, application.ConfigSensitiveMutation{Operation: application.ConfigSensitiveUnchanged}, &configApplicationTestObserver{})
			path := workspaceConfigPath(workspace)
			if err := config.SetString(path, []string{"modules", "demo", "config", "public"}, "private-password"); err != nil {
				t.Fatal(err)
			}
			if err := writeManagedConfigState(workspace, "test"); err != nil {
				t.Fatal(err)
			}
			snapshot, err := service.GetConfig(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			before := configApplicationTargetBytes(t, workspace)
			candidate := candidateFromConfigSnapshot(t, snapshot)
			mutation := application.ConfigSensitiveMutation{Operation: operation}
			value := "replacement-secret"
			if operation == application.ConfigSensitiveSet {
				mutation.Value = &value
			}
			candidate.Sensitive["demo.password"] = mutation
			_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, &configApplicationTestObserver{})
			if err == nil {
				t.Fatal("private alias would become public")
			}
			assertConfigApplicationTargetsEqual(t, before)
		})
	}
}

func TestStoredInputRejectsOtherOwners(t *testing.T) {
	service, workspace := storedInputFixture(t)
	store, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("DEMO_PASSWORD", "foreign-value", secretMetadata{Owner: "other", Kind: "generated", Provenance: "test"})
	if err := os.WriteFile(store.path, marshalSecretStore(store), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.GetConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := configApplicationTargetBytes(t, workspace)
	_, err = service.PutConfig(context.Background(), application.ConfigPutRequest{OperationID: configTransactionTestOperationID, Candidate: candidateFromConfigSnapshot(t, snapshot), Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, &configApplicationTestObserver{})
	if err == nil {
		t.Fatal("foreign secret adopted")
	}
	assertConfigApplicationTargetsEqual(t, before)
}
