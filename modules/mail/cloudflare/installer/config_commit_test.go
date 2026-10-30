// TEST_CASES: MAILMOD-T-042
package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

type fakeConfigService struct {
	snapshot application.ConfigSnapshot
	request  application.ConfigPutRequest
	putErr   error
	calls    int
}

type lostConfigReply struct{ application.ConfigService }

func (s lostConfigReply) PutConfig(ctx context.Context, request application.ConfigPutRequest, observer application.ConfigCommitObserver) (application.ConfigPutResult, error) {
	result, err := s.ConfigService.PutConfig(ctx, request, observer)
	if err != nil {
		return result, err
	}
	return application.ConfigPutResult{}, errors.New("injected lost commit reply")
}

func TestFirstSourceCommitThroughWorkspaceTransaction(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil {
		t.Fatal(err)
	}
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
	service := runner.NewWorkspaceConfigService(workspace)
	if err := prepared.CommitFirstSource(t.Context(), service, failingObserver{}, "cfg-0123456789abcdef0123456789abcdef", req.AllowedDomains, req.CapacityBytes); !errors.Is(err, ErrUnknown) {
		t.Fatal("audit failure did not fail closed", err)
	}
	unchanged, readErr := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if readErr != nil || string(unchanged) != string(config) {
		t.Fatal("audit failure changed public config", readErr)
	}
	if _, statErr := os.Stat(filepath.Join(state, "secrets.yml")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("audit failure wrote secret store", statErr)
	}
	if err := prepared.CommitFirstSource(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", req.AllowedDomains, req.CapacityBytes); err != nil {
		t.Fatal("workspace transaction failed", err)
	}
	config, err = os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || strings.Contains(string(config), req.RoutingToken) || !strings.Contains(string(config), prepared.Specification().ID) {
		t.Fatal("invalid public config", err)
	}
	secret, err := os.ReadFile(filepath.Join(state, "secrets.yml"))
	if err != nil || !strings.Contains(string(secret), req.RoutingToken) || !strings.Contains(string(secret), "managed_input") {
		t.Fatal("private input missing from Secret Store", err)
	}
	if err := prepared.CommitFirstSource(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", req.AllowedDomains, req.CapacityBytes); err != nil {
		t.Fatal("identical retry failed read-only validation", err)
	}
	var firstCredentials bytes.Buffer
	firstDescriptions, _ := json.Marshal([]ingress.SourceSpec{prepared.Specification()})
	if err := prepared.WriteCredentials(&firstCredentials); err != nil {
		t.Fatal(err)
	}
	existing, err := ingress.ParseSources(string(firstDescriptions), firstCredentials.String(), []string{"example.test", "other.test"})
	if err != nil {
		t.Fatal(err)
	}
	secondPath, secondSpec := fixture(t)
	secondSpec.Worker, secondSpec.Bucket = "anas-second", "anas-second"
	secondJournal, err := OpenJournal(secondPath, secondSpec)
	if err != nil {
		t.Fatal(err)
	}
	defer secondJournal.Close()
	secondCloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	secondReq := RuntimeRequest{Domains: []string{"other.test"}, AllowedDomains: []string{"example.test", "other.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7, ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("S", 32)}
	second, err := secondJournal.prepareRuntime(t.Context(), secondCloud, secondReq)
	if err != nil {
		t.Fatal(err)
	}
	badExisting := append([]ingress.RuntimeSource(nil), existing...)
	badExisting[0].Credentials.Reader = strings.Repeat("e", 64)
	configBefore := append([]byte(nil), config...)
	secretBefore := append([]byte(nil), secret...)
	if err := second.CommitSource(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", badExisting, secondReq.AllowedDomains, 4<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("stale private source accepted", err)
	}
	configAfter, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	secretAfter, _ := os.ReadFile(filepath.Join(state, "secrets.yml"))
	if !bytes.Equal(configBefore, configAfter) || !bytes.Equal(secretBefore, secretAfter) {
		t.Fatal("conflicting source changed workspace")
	}
	if err := second.CommitSource(t.Context(), lostConfigReply{service}, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", existing, secondReq.AllowedDomains, 4<<30); !errors.Is(err, ErrUnknown) {
		t.Fatal("lost commit reply must remain unknown", err)
	}
	afterCommit, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := second.CommitSource(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", existing, secondReq.AllowedDomains, 4<<30); err != nil {
		t.Fatal("multi-source retry failed", err)
	}
	afterRetry, err := service.GetConfig(t.Context())
	if err != nil || afterRetry.Validator != afterCommit.Validator {
		t.Fatal("recovery performed a second write", err)
	}
	secretAfter, _ = os.ReadFile(filepath.Join(state, "secrets.yml"))
	configAfter, _ = os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Contains(secretAfter, []byte(req.RoutingToken)) || !bytes.Contains(secretAfter, []byte(secondReq.RoutingToken)) || !bytes.Contains(configAfter, []byte(prepared.Specification().ID)) || !bytes.Contains(configAfter, []byte(second.Specification().ID)) {
		t.Fatal("multi-source transaction lost a source")
	}
	combined, err := second.Merge(existing, secondReq.AllowedDomains, 4<<30)
	if err != nil {
		t.Fatal(err)
	}
	combinedPublic, _ := json.Marshal(combined.Specifications())
	var combinedSecret bytes.Buffer
	if err := combined.WriteCredentials(&combinedSecret); err != nil {
		t.Fatal(err)
	}
	existingTwo, err := ingress.ParseSources(string(combinedPublic), combinedSecret.String(), secondReq.AllowedDomains)
	if err != nil {
		t.Fatal(err)
	}
	thirdPath, thirdSpec := fixture(t)
	thirdSpec.Worker, thirdSpec.Bucket = "anas-third", "anas-third"
	thirdJournal, err := OpenJournal(thirdPath, thirdSpec)
	if err != nil {
		t.Fatal(err)
	}
	defer thirdJournal.Close()
	thirdCloud := &fakeEndpoint{fakeSchedule: fakeSchedule{fakeDeployment: fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true}}, label: "account-name"}
	thirdReq := RuntimeRequest{Domains: []string{"example.test"}, AllowedDomains: secondReq.AllowedDomains, CapacityBytes: 2 << 30, DeliveredDays: 7, ZoneID: strings.Repeat("1", 32), RoutingToken: strings.Repeat("T", 32)}
	third, err := thirdJournal.prepareRuntime(t.Context(), thirdCloud, thirdReq)
	if err != nil {
		t.Fatal(err)
	}
	if err := third.CommitSourceWithDraining(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", existingTwo, []string{second.Specification().ID}, thirdReq.AllowedDomains, 6<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("unrelated source was drained", err)
	}
	partialCoverage := append([]ingress.RuntimeSource(nil), existingTwo...)
	partialCoverage[0].Domains = []string{"example.test", "uncovered.test"}
	if err := third.CommitSourceWithDraining(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", partialCoverage, []string{prepared.Specification().ID}, []string{"example.test", "other.test", "uncovered.test"}, 6<<30); !errors.Is(err, ErrConflict) {
		t.Fatal("partially covered source was drained", err)
	}
	for i := 0; i < 2; i++ {
		if err := third.CommitSourceWithDraining(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", existingTwo, []string{prepared.Specification().ID}, thirdReq.AllowedDomains, 6<<30); err != nil {
			t.Fatal("explicit replacement or retry failed", err)
		}
	}
	snapshot, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	modules := snapshot.Config["modules"].(map[string]any)
	values := modules["mail"].(map[string]any)["config"].(map[string]any)
	var finalSpecs []ingress.SourceSpec
	if err := json.Unmarshal([]byte(values["ingress_sources"].(string)), &finalSpecs); err != nil || len(finalSpecs) != 3 || finalSpecs[0].Mode != "draining" || finalSpecs[1].Mode != "active" || finalSpecs[2].Mode != "active" {
		t.Fatal("incorrect replacement source modes", err)
	}
	// Recheck the first source in a separate first-source workspace state below;
	// after a second source exists it must never overwrite the combined state.
	prepared.credentials.Reader = strings.Repeat("f", 64)
	if err := prepared.CommitFirstSource(t.Context(), service, noopObserver{}, "cfg-0123456789abcdef0123456789abcdef", req.AllowedDomains, req.CapacityBytes); !errors.Is(err, ErrConflict) {
		t.Fatal("different credential was accepted as retry", err)
	}
}

func (s *fakeConfigService) GetConfig(context.Context) (application.ConfigSnapshot, error) {
	return s.snapshot, nil
}
func (s *fakeConfigService) ValidateConfig(context.Context, application.ConfigCandidate) (application.ConfigValidationResult, error) {
	panic("unexpected validation")
}
func (s *fakeConfigService) PutConfig(_ context.Context, request application.ConfigPutRequest, _ application.ConfigCommitObserver) (application.ConfigPutResult, error) {
	s.calls++
	s.request = request
	return application.ConfigPutResult{}, s.putErr
}

type noopObserver struct{}

func (noopObserver) BeforeConfigCommit(context.Context, application.ConfigCommitIntent) error {
	return nil
}

type failingObserver struct{}

func (failingObserver) BeforeConfigCommit(context.Context, application.ConfigCommitIntent) error {
	return errors.New("audit unavailable")
}

func firstSourceSnapshot() application.ConfigSnapshot {
	return application.ConfigSnapshot{Managed: true, Validator: "validator", Config: application.ConfigDocument{"modules": map[string]any{"mail": map[string]any{"config": map[string]any{"ingress_sources": "", "ingress_capacity_bytes": 0}}}}, Fields: []application.ConfigField{
		{Path: "mail.ingress_sources", Editable: true}, {Path: "mail.ingress_capacity_bytes", Editable: true},
		{Path: "mail.ingress_credentials", Editable: true, Sensitive: true, StoreInput: true, SensitiveState: "unset"},
	}}
}

func TestFirstSourceCommitUsesPrivateConfigTransaction(t *testing.T) {
	j, cloud, req := handoffFixture(t)
	prepared, err := j.prepareRuntime(t.Context(), cloud, req)
	if err != nil {
		t.Fatal(err)
	}
	service := &fakeConfigService{snapshot: firstSourceSnapshot()}
	if err := prepared.CommitFirstSource(t.Context(), service, noopObserver{}, "operation", req.AllowedDomains, req.CapacityBytes); err != nil {
		t.Fatal(err)
	}
	if service.calls != 1 || service.request.Precondition != application.ConfigPreconditionMatch || service.request.ExpectedValidator != "validator" {
		t.Fatal("missing transaction precondition")
	}
	mutation := service.request.Candidate.Sensitive["mail.ingress_credentials"]
	if mutation.Operation != application.ConfigSensitiveSet || mutation.Value == nil {
		t.Fatal("missing private mutation")
	}
	public, err := json.Marshal(service.request.Candidate.Document)
	if err != nil || strings.Contains(string(public), req.RoutingToken) {
		t.Fatal("public config contains routing secret", err)
	}
	modules := service.request.Candidate.Document["modules"].(map[string]any)
	values := modules["mail"].(map[string]any)["config"].(map[string]any)
	sources, err := ingress.ParseSources(values["ingress_sources"].(string), *mutation.Value, req.AllowedDomains)
	if err != nil || len(sources) != 1 || sources[0].Credentials.Routing != req.RoutingToken {
		t.Fatal("invalid transaction content", err)
	}
	old := service.snapshot.Config["modules"].(map[string]any)["mail"].(map[string]any)["config"].(map[string]any)
	if old["ingress_sources"] != "" {
		t.Fatal("mutated observed config")
	}
}

func TestFirstSourceCommitRefusesUnsafeExistingConfiguration(t *testing.T) {
	for _, mode := range []string{"old-core", "secret-set", "source-set", "env-override", "capacity", "cas", "unconfirmed"} {
		t.Run(mode, func(t *testing.T) {
			j, cloud, req := handoffFixture(t)
			prepared, err := j.prepareRuntime(t.Context(), cloud, req)
			if err != nil {
				t.Fatal(err)
			}
			service := &fakeConfigService{snapshot: firstSourceSnapshot()}
			capacity := req.CapacityBytes
			switch mode {
			case "old-core":
				service.snapshot.Fields[2].StoreInput = false
			case "secret-set":
				service.snapshot.Fields[2].SensitiveState = "set"
			case "source-set":
				service.snapshot.Config["modules"].(map[string]any)["mail"].(map[string]any)["config"].(map[string]any)["ingress_sources"] = `[{}]`
			case "env-override":
				service.snapshot.Config["env"] = map[string]any{"MAIL_INGRESS_SOURCES": ""}
			case "capacity":
				capacity--
			case "cas":
				service.putErr = &application.Error{Code: "config_precondition_failed"}
			case "unconfirmed":
				service.putErr = errors.New("private service error")
			}
			err = prepared.CommitFirstSource(t.Context(), service, noopObserver{}, "operation", req.AllowedDomains, capacity)
			if mode == "unconfirmed" {
				if !errors.Is(err, ErrUnknown) || strings.Contains(err.Error(), "private service error") {
					t.Fatal("unknown result leaked or accepted", err)
				}
			} else if !errors.Is(err, ErrConflict) {
				t.Fatal("unsafe transaction accepted", err)
			}
			if mode != "cas" && mode != "unconfirmed" && service.calls != 0 {
				t.Fatal("unsafe transaction reached service")
			}
		})
	}
}
