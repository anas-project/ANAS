// TEST_CASES: MAILMOD-T-042
package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

type provisionAudit struct {
	intents   []ProvisionIntent
	err       error
	commits   int
	commitErr error
}

func (a *provisionAudit) BeforeMailCommit(_ context.Context, _ ProvisionIntent) error {
	a.commits++
	return a.commitErr
}

func (a *provisionAudit) BeforeMailProvision(_ context.Context, intent ProvisionIntent) error {
	a.intents = append(a.intents, intent)
	return a.err
}

func installWorkspace(t *testing.T) (application.ConfigService, string) {
	t.Helper()
	root, workspace := t.TempDir(), t.TempDir()
	moduleDir := filepath.Join(root, "mail")
	if err := os.Mkdir(moduleDir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `api_version: anas.module/v1
kind: Module
name: mail
version: 1.0.0
revision: 1
status: release
abi:
  supports: [anas.module-hook/v1]
runtime:
  type: builtin
config:
  types:
    ingress_sources: string
    ingress_credentials: string
    ingress_capacity_bytes: int
  defaults:
    ingress_sources: ""
    ingress_credentials: ""
    ingress_capacity_bytes: 0
  changes:
    ingress_sources:
      effect: container_recreate
    ingress_capacity_bytes:
      effect: container_recreate
    ingress_credentials:
      effect: container_recreate
      sensitive: true
      store_input: true
`
	if err := os.WriteFile(filepath.Join(moduleDir, "module.yml"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(workspace, ".anas")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	config := []byte("module_source: official\nmodules:\n  mail: {}\nglobal:\n  base_domain: nas.test\n  email: admin@nas.test\n  timezone: UTC\n")
	if err := os.WriteFile(filepath.Join(workspace, "config.yml"), config, 0600); err != nil {
		t.Fatal(err)
	}
	managed := fmt.Sprintf("api_version: anas.config/v1\ndigest: sha256:%x\nvalidator: cfgv-%s\nupdated_by: test\n", sha256.Sum256(config), strings.Repeat("1", 64))
	if err := os.WriteFile(filepath.Join(state, "config-managed.yml"), []byte(managed), 0600); err != nil {
		t.Fatal(err)
	}
	view, _ := json.Marshal(map[string]string{"api_version": "anas.module-view/v1", "module_root": root})
	if err := os.WriteFile(filepath.Join(state, "module-view.json"), view, 0600); err != nil {
		t.Fatal(err)
	}
	return runner.NewWorkspaceConfigService(workspace), workspace
}

func TestInstallRuntimeCommitRecovery(t *testing.T) {
	path, spec := fixture(t)
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	runtime := RuntimeRequest{Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, NotificationOrigin: "https://notify.example.test", ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("R", 32)}
	service, workspace := installWorkspace(t)
	audit := &provisionAudit{}
	request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	result, err := j.installRuntime(t.Context(), cloud, lostConfigReply{service}, audit, noopObserver{}, request)
	if !errors.Is(err, ErrUnknown) || result.ID != "" {
		t.Fatal("lost transaction reply must remain unknown", err)
	}
	configBefore, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	secretBefore, err := os.ReadFile(filepath.Join(workspace, ".anas", "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	result, err = j.installRuntime(t.Context(), cloud, service, audit, noopObserver{}, request)
	if err != nil || result.ID == "" || cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.writes != 1 {
		t.Fatal("retry failed or repeated cloud mutations", err)
	}
	configAfter, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	secretAfter, _ := os.ReadFile(filepath.Join(workspace, ".anas", "secrets.yml"))
	if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(secretBefore, secretAfter) || !bytes.Contains(configAfter, []byte(result.ID)) || bytes.Contains(configAfter, []byte(runtime.RoutingToken)) || !bytes.Contains(secretAfter, []byte(runtime.RoutingToken)) {
		t.Fatal("retry modified transaction or exposed its private input")
	}
	encoded, _ := json.Marshal(request)
	for _, output := range []string{string(encoded), fmt.Sprintf("%+v", request), fmt.Sprintf("%#v", request), fmt.Sprintf("%+v", audit.intents), fmt.Sprintf("%+v", result)} {
		if strings.Contains(output, runtime.RoutingToken) {
			t.Fatal("public output contains routing credential")
		}
	}
	if len(audit.intents) != 2 || audit.intents[0].OperationID != request.OperationID {
		t.Fatal("each attempt must be audited before provisioning")
	}
	for _, change := range []string{"zone", "routing", "proxy", "operation", "allowed"} {
		changed := request
		switch change {
		case "zone":
			changed.Runtime.ZoneID = strings.Repeat("2", 32)
		case "routing":
			changed.Runtime.RoutingToken = strings.Repeat("S", 32)
		case "proxy":
			changed.Runtime.Proxy = "http://user:password@proxy.example.test:8080"
		case "operation":
			changed.OperationID = "cfg-0123456789abcdef0123456789abcdee"
		case "allowed":
			changed.Runtime.AllowedDomains = append([]string(nil), runtime.AllowedDomains...)
			changed.Runtime.AllowedDomains = append(changed.Runtime.AllowedDomains, "other.test")
		}
		if _, err := j.installRuntime(t.Context(), cloud, service, audit, noopObserver{}, changed); !errors.Is(err, ErrConflict) {
			t.Fatal("changed recovery input accepted", change, err)
		}
		if cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.writes != 1 {
			t.Fatal("changed recovery input triggered a cloud write", change)
		}
	}
	fd, err := unix.Openat(j.dirfd, "install-binding.json", unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	file := os.NewFile(uintptr(fd), "install-binding.json")
	binding, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || bytes.Contains(binding, []byte(runtime.RoutingToken)) {
		t.Fatal("binding exposed token or was not persisted", err)
	}
}

func TestInstallRuntimeCommitAuditFailureIsRecoverable(t *testing.T) {
	j, cloud, runtime := handoffFixture(t)
	service, workspace := installWorkspace(t)
	audit := &provisionAudit{}
	request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	before, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if result, err := j.installRuntime(t.Context(), cloud, service, audit, failingObserver{}, request); !errors.Is(err, ErrUnknown) || result.ID != "" {
		t.Fatal("audit failure did not prevent configuration commit", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("audit failure changed config")
	}
	if _, err := os.Stat(filepath.Join(workspace, ".anas", "secrets.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("audit failure wrote secret store", err)
	}
	if result, err := j.installRuntime(t.Context(), cloud, service, audit, noopObserver{}, request); err != nil || result.ID == "" || cloud.workerCreates != 1 || cloud.bucketCreates != 1 || cloud.writes != 1 {
		t.Fatal("retry after audit restoration failed", err)
	}
}

func TestInstallRuntimeRejectsBeforeCloudWrites(t *testing.T) {
	for _, scenario := range []string{"audit", "capacity", "drain", "operation", "cancelled", "invalid-runtime", "binding-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			j, cloud, runtime := handoffFixture(t)
			service := &fakeConfigService{snapshot: firstSourceSnapshot()}
			audit := &provisionAudit{}
			request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
			ctx := t.Context()
			switch scenario {
			case "audit":
				audit.err = errors.New("audit unavailable")
			case "capacity":
				request.TotalCapacity--
			case "drain":
				request.DrainIDs = []string{"22222222-2222-4222-8222-222222222222"}
			case "operation":
				request.OperationID = "invalid"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "invalid-runtime":
				request.Runtime.ZoneID = "invalid"
			case "binding-symlink":
				if err := unix.Symlinkat(filepath.Join(t.TempDir(), "target"), j.dirfd, "install-binding.json"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := j.installRuntime(ctx, cloud, service, audit, noopObserver{}, request)
			if err == nil || result.ID != "" || cloud.workerCreates != 0 || cloud.bucketCreates != 0 || cloud.writes != 0 || service.calls != 0 {
				t.Fatal("failed precondition produced an external write", err)
			}
		})
	}
}

func TestInstallRuntimeIncompleteBindingAndUnknownCloudWriteFailClosed(t *testing.T) {
	for _, scenario := range []string{"partial-binding", "unbound-prior-stage", "issued-cloud-write"} {
		t.Run(scenario, func(t *testing.T) {
			j, cloud, runtime := handoffFixture(t)
			service := &fakeConfigService{snapshot: firstSourceSnapshot()}
			audit := &provisionAudit{}
			request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
			switch scenario {
			case "partial-binding":
				fd, err := unix.Openat(j.dirfd, "install-binding.json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0600)
				if err != nil {
					t.Fatal(err)
				}
				f := os.NewFile(uintptr(fd), "install-binding.json")
				_, _ = f.WriteString(`{"schema":`)
				_ = f.Close()
			case "unbound-prior-stage":
				j.state.Stage = "reserved"
			case "issued-cloud-write":
				j.state.Stage = "worker-issued"
				// Unknown issued writes take precedence over later input drift.
				request.Runtime.ZoneID = "invalid"
			}
			if result, err := j.installRuntime(t.Context(), cloud, service, audit, noopObserver{}, request); !errors.Is(err, ErrUnknown) || result.ID != "" {
				t.Fatal("incomplete operation was adopted", err)
			}
			if cloud.workerCreates != 0 || cloud.bucketCreates != 0 || cloud.writes != 0 || service.calls != 0 {
				t.Fatal("unknown state caused an external write")
			}
		})
	}
}

func TestInstallSeparatePrepareAndCommitRechecksAuthority(t *testing.T) {
	path, spec := fixture(t)
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	runtime := RuntimeRequest{Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, NotificationOrigin: "https://notify.example.test", ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("R", 32)}
	request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	service, workspace := installWorkspace(t)
	audit := &provisionAudit{}
	if _, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request); !errors.Is(err, ErrUnknown) {
		t.Fatal("unprepared operation accepted", err)
	}
	prepared, err := j.prepareInstall(t.Context(), cloud, audit, request)
	if err != nil || prepared.ID == "" {
		t.Fatal("preparation failed", err)
	}
	before, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || bytes.Contains(before, []byte(prepared.ID)) {
		t.Fatal("preparation changed workspace", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	cloudCalls := cloud.routingReads + cloud.probes + cloud.writes + cloud.workerCreates + cloud.bucketCreates
	audit.commitErr = errors.New("administrator revoked")
	if _, err = j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request); !errors.Is(err, ErrUnknown) {
		t.Fatal("revoked administrator committed", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("revocation changed workspace")
	}
	audit.commitErr = nil
	result, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request)
	if err != nil || result.ID != prepared.ID {
		t.Fatal("commit after reauthorization failed", err)
	}
	if cloudCalls != cloud.routingReads+cloud.probes+cloud.writes+cloud.workerCreates+cloud.bucketCreates {
		t.Fatal("commit made a cloud API call")
	}
	audit.commitErr = errors.New("administrator revoked again")
	if _, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request); !errors.Is(err, ErrUnknown) {
		t.Fatal("read-only retry skipped fresh authorization", err)
	}
}

func TestInstallRetryCannotRestoreIndependentlyRemovedSource(t *testing.T) {
	j, cloud, runtime := handoffFixture(t)
	service, workspace := installWorkspace(t)
	audit := &provisionAudit{}
	request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	if _, err := j.installRuntime(t.Context(), cloud, service, audit, noopObserver{}, request); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modules := snapshot.Config["modules"].(map[string]any)
	values := modules["mail"].(map[string]any)["config"].(map[string]any)
	values["ingress_sources"], values["ingress_capacity_bytes"] = "", 0
	candidate := application.ConfigCandidate{Document: snapshot.Config, Sensitive: map[string]application.ConfigSensitiveMutation{"mail.ingress_credentials": {Operation: application.ConfigSensitiveUnset}}}
	if _, err := service.PutConfig(t.Context(), application.ConfigPutRequest{OperationID: "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator}, noopObserver{}); err != nil {
		t.Fatal("independent removal failed", err)
	}
	before, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if _, err := j.CommitPreparedRuntime(t.Context(), service, audit, noopObserver{}, request); !errors.Is(err, ErrUnknown) {
		t.Fatal("old operation restored an independently removed source", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("old operation changed independently updated configuration")
	}
}

func TestInstallReadsExistingSourcesOnlyFromManagedSnapshot(t *testing.T) {
	j, cloud, runtime := handoffFixture(t)
	service, workspace := installWorkspace(t)
	snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil || snapshot.Sources != "" || snapshot.Credentials != "" || snapshot.Validator == "" {
		t.Fatal("empty managed source snapshot unavailable", err)
	}
	config, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modules := config.Config["modules"].(map[string]any)
	modules["mail"].(map[string]any)["config"] = map[string]any{"ingress_sources": "[]"}
	if _, err := service.PutConfig(t.Context(), application.ConfigPutRequest{OperationID: "cfg-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Candidate: application.ConfigCandidate{Document: config.Config}, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: config.Validator}, noopObserver{}); err != nil {
		t.Fatal("empty array configuration was rejected", err)
	}
	snapshot, err = runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil || snapshot.Sources != "[]" || snapshot.Credentials != "" {
		t.Fatal("empty source array should not require a credential", err)
	}
	request := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	if _, err := j.installRuntime(t.Context(), cloud, service, &provisionAudit{}, noopObserver{}, request); err != nil {
		t.Fatal(err)
	}
	snapshot, err = runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal("managed snapshot unavailable", err)
	}
	sources, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, runtime.AllowedDomains)
	if err != nil || len(sources) != 1 || sources[0].Credentials.Routing != runtime.RoutingToken {
		t.Fatal("snapshot did not preserve the source and private credentials", err)
	}
	encoded, _ := json.Marshal(snapshot)
	for _, output := range []string{string(encoded), fmt.Sprintf("%+v", snapshot), fmt.Sprintf("%#v", snapshot)} {
		if strings.Contains(output, runtime.RoutingToken) || strings.Contains(output, sources[0].Credentials.Reader) {
			t.Fatal("private snapshot was formatted or serialized")
		}
	}
	recovered, err := j.RecoverInstallRequest(InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: request.OperationID}, sources)
	if err != nil || len(recovered.Existing) != 0 {
		t.Fatal("single-source request could not be recovered", err)
	}
	if _, err := j.CommitPreparedRuntime(t.Context(), service, &provisionAudit{}, noopObserver{}, recovered); err != nil {
		t.Fatal("recovered single-source request did not validate", err)
	}
	secretPath := filepath.Join(workspace, ".anas", "secrets.yml")
	raw, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	secrets := document["secrets"].(map[string]any)
	secrets["MAIL_INGRESS_CREDENTIALS"].(map[string]any)["owner"] = "other-module"
	raw, err = yaml.Marshal(document)
	if err != nil || os.WriteFile(secretPath, raw, 0600) != nil {
		t.Fatal("cannot inject secret owner drift", err)
	}
	if _, err := runner.ReadMailIngressSnapshot(t.Context(), workspace); err == nil || strings.Contains(err.Error(), runtime.RoutingToken) {
		t.Fatal("foreign owned secret was read or exposed", err)
	}
}

func TestInstallRecoversMultiSourceAfterLostCommitReply(t *testing.T) {
	first, firstCloud, runtime := handoffFixture(t)
	service, workspace := installWorkspace(t)
	firstRequest := InstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	firstSource, err := first.installRuntime(t.Context(), firstCloud, service, &provisionAudit{}, noopObserver{}, firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, runtime.AllowedDomains)
	if err != nil || len(existing) != 1 {
		t.Fatal(err)
	}
	path, spec := fixture(t)
	spec.Worker, spec.Bucket = "anas-second", "anas-second"
	second, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	cloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	secondRuntime := runtime
	secondRuntime.RoutingToken = strings.Repeat("S", 32)
	request := InstallRequest{Runtime: secondRuntime, Existing: existing, DrainIDs: []string{firstSource.ID}, TotalCapacity: 4 << 30, OperationID: "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if _, err := second.installRuntime(t.Context(), cloud, lostConfigReply{service}, &provisionAudit{}, noopObserver{}, request); !errors.Is(err, ErrUnknown) {
		t.Fatal("lost commit reply was not unknown", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	second, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	snapshot, err = runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, runtime.AllowedDomains)
	if err != nil || len(current) != 2 || current[0].Mode != "draining" {
		t.Fatal("committed source set unavailable", err)
	}
	request.Existing = nil
	recovered, err := second.RecoverInstallRequest(request, current)
	if err != nil || len(recovered.Existing) != 1 || recovered.Existing[0].Mode != "active" || recovered.Existing[0].Credentials.Reader != existing[0].Credentials.Reader || current[0].Mode != "draining" {
		t.Fatal("original sources not recovered safely", err)
	}
	if _, err := second.CommitPreparedRuntime(t.Context(), service, &provisionAudit{}, noopObserver{}, recovered); err != nil {
		t.Fatal("recovered commit did not validate", err)
	}
	changed := append([]ingress.RuntimeSource(nil), current...)
	changed[0].Credentials.Reader = strings.Repeat("e", 64)
	if _, err := second.RecoverInstallRequest(request, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed prior credential adopted", err)
	}
	changed = append([]ingress.RuntimeSource(nil), current...)
	changed[1].Credentials.Reader = strings.Repeat("f", 64)
	if _, err := second.RecoverInstallRequest(request, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed prepared credential adopted", err)
	}
}
