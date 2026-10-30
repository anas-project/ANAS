// TEST_CASES: MAILMOD-T-058
package installer

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

type relayAudit struct {
	prepareErr, commitErr error
	prepares, commits     int
}

func (a *relayAudit) BeforeRelayPrepare(context.Context, RelayInstallIntent) error {
	a.prepares++
	return a.prepareErr
}
func (a *relayAudit) BeforeRelayCommit(context.Context, RelayInstallIntent) error {
	a.commits++
	return a.commitErr
}

func TestRelayInstallCommitRecoveryAndNoResurrection(t *testing.T) {
	runtime, ca, _, _ := relayHandoffFixture(t, true)
	request := RelayInstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	service, workspace := installWorkspace(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	audit := &relayAudit{prepareErr: errors.New("revoked")}
	if _, err = j.prepare(t.Context(), audit, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("revoked prepare: %v", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "relay-binding.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("denied preparation bound request")
	}
	audit.prepareErr = nil
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unprepared commit: %v", err)
	}
	if _, err = j.prepare(t.Context(), audit, request, ca); err != nil {
		t.Fatal(err)
	}
	if _, err = OpenRelayJournal(dir); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent operation: %v", err)
	}
	for _, file := range []string{"relay-binding.json", "relay-prepared"} {
		data, e := os.ReadFile(filepath.Join(dir, file))
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{runtime.Reader, runtime.Control, runtime.Notification} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("private input leaked into operation record")
			}
		}
	}
	audit.commitErr = errors.New("revoked")
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("revoked commit: %v", err)
	}
	audit.commitErr = nil
	if _, err = j.commit(t.Context(), lostConfigReply{service}, audit, noopObserver{}, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost commit response: %v", err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, runtime.AllowedDomains)
	if err != nil || len(current) != 1 {
		t.Fatalf("current sources: %v", err)
	}
	recovered, err := j.RecoverRequest(request, current)
	if err != nil || len(recovered.Existing) != 0 {
		t.Fatalf("recover request: %v", err)
	}
	before, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, recovered, ca); err != nil {
		t.Fatalf("retry: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("idempotent retry changed configuration")
	}
	audit.commitErr = errors.New("revoked after commit")
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, recovered, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("read-only retry skipped authorization: %v", err)
	}
	audit.commitErr = nil
	changed := request
	changed.Runtime.Control = strings.Repeat("d", 64)
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, changed, ca); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed input accepted: %v", err)
	}
	state, err := service.GetConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	values := state.Config["modules"].(map[string]any)["mail"].(map[string]any)["config"].(map[string]any)
	values["ingress_sources"], values["ingress_capacity_bytes"] = "", 0
	candidate := application.ConfigCandidate{Document: state.Config, Sensitive: map[string]application.ConfigSensitiveMutation{"mail.ingress_credentials": {Operation: application.ConfigSensitiveUnset}}}
	if _, err = service.PutConfig(t.Context(), application.ConfigPutRequest{OperationID: "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Candidate: candidate, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: state.Validator}, noopObserver{}); err != nil {
		t.Fatal(err)
	}
	before, _ = os.ReadFile(filepath.Join(workspace, "config.yml"))
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); err == nil {
		t.Fatal("stale operation resurrected removed source")
	}
	after, _ = os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("stale operation modified configuration")
	}
}

func TestRelayInstallRejectsIncompleteAndUnsafeJournals(t *testing.T) {
	for _, scenario := range []string{"missing-binding", "partial-binding", "symlink-binding", "missing-prepared", "foreign-file"} {
		t.Run(scenario, func(t *testing.T) {
			runtime, ca, _, _ := relayHandoffFixture(t, false)
			request := RelayInstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if scenario == "foreign-file" {
				if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				if j, err := OpenRelayJournal(dir); err == nil {
					j.Close()
					t.Fatal("adopted nonempty directory")
				}
				return
			}
			j, err := OpenRelayJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			audit := &relayAudit{}
			if scenario == "missing-binding" {
				j.Close()
				j, err = OpenRelayJournal(dir)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = j.prepare(t.Context(), audit, request, ca); !errors.Is(err, ErrUnknown) {
					t.Fatalf("missing binding recreated: %v", err)
				}
				j.Close()
				return
			}
			if _, err = j.prepare(t.Context(), audit, request, ca); err != nil {
				t.Fatal(err)
			}
			j.Close()
			path := filepath.Join(dir, "relay-binding.json")
			switch scenario {
			case "partial-binding":
				if err = os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink-binding":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err = os.Symlink("relay.lock", path); err != nil {
					t.Fatal(err)
				}
			case "missing-prepared":
				if err = os.Remove(filepath.Join(dir, "relay-prepared")); err != nil {
					t.Fatal(err)
				}
			}
			j, err = OpenRelayJournal(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer j.Close()
			service, _ := installWorkspace(t)
			if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); err == nil {
				t.Fatal("unsafe journal committed")
			}
		})
	}
}

func TestRelayInstallRechecksSourceBeforeCommit(t *testing.T) {
	runtime, _, _, source := relayHandoffFixture(t, true)
	var revoke atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if revoke.Load() && r.URL.Path == "/v1/control-status" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		source.ServeHTTP(w, r)
	}))
	defer server.Close()
	runtime.Origin = server.URL
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	request := RelayInstallRequest{Runtime: runtime, TotalCapacity: runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	service, workspace := installWorkspace(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	audit := &relayAudit{}
	if _, err = j.prepare(t.Context(), audit, request, ca); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	revoke.Store(true)
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("revoked source control accepted: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("source recheck failure changed configuration")
	}
	revoke.Store(false)
	if _, err = j.commit(t.Context(), service, audit, failingObserver{}, request, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("commit audit failure ignored: %v", err)
	}
	after, _ = os.ReadFile(filepath.Join(workspace, "config.yml"))
	if !bytes.Equal(before, after) {
		t.Fatal("commit audit failure changed configuration")
	}
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); err != nil {
		t.Fatalf("authorized retry: %v", err)
	}
}

func TestRelayInstallRecoversExplicitDrainAndExistingSecrets(t *testing.T) {
	runtime, ca, _, _ := relayHandoffFixture(t, false)
	old := ingress.RuntimeSource{SourceSpec: ingress.SourceSpec{ID: "33333333-3333-4333-8333-333333333333", Generation: "44444444-4444-4444-8444-444444444444", Provider: "cloud-relay", Mode: "active", Origin: "https://old.example.test", Domains: []string{"example.test"}, CapacityBytes: runtime.CapacityBytes, DeliveredDays: 7}, Credentials: ingress.SourceCredentials{Reader: strings.Repeat("d", 64), Control: strings.Repeat("e", 64)}}
	service, workspace := installWorkspace(t)
	initial := PreparedSource{spec: old.SourceSpec, credentials: old.Credentials}
	if err := initial.CommitFirstSource(t.Context(), service, noopObserver{}, "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", runtime.AllowedDomains, 2*runtime.CapacityBytes); err != nil {
		t.Fatal(err)
	}
	request := RelayInstallRequest{Runtime: runtime, Existing: []ingress.RuntimeSource{old}, DrainIDs: []string{old.ID}, TotalCapacity: 2 * runtime.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	audit := &relayAudit{}
	if _, err = j.prepare(t.Context(), audit, request, ca); err != nil {
		t.Fatal(err)
	}
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, request, ca); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	current, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, runtime.AllowedDomains)
	if err != nil || len(current) != 2 || current[0].Mode != "draining" || current[0].Credentials != old.Credentials || current[1].ID != runtime.SourceID {
		t.Fatalf("lost prior source: %v", err)
	}
	recovered, err := j.RecoverRequest(RelayInstallRequest{Runtime: runtime, DrainIDs: []string{old.ID}, TotalCapacity: request.TotalCapacity, OperationID: request.OperationID}, current)
	if err != nil || len(recovered.Existing) != 1 || recovered.Existing[0].Mode != "active" || recovered.Existing[0].Credentials != old.Credentials {
		t.Fatalf("recover draining source: %v", err)
	}
	if _, err = j.commit(t.Context(), service, audit, noopObserver{}, recovered, ca); err != nil {
		t.Fatalf("read-only retry: %v", err)
	}
}
