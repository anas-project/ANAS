// TEST_CASES: MAILMOD-T-057
package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func relayHandoffFixture(t *testing.T, notifications bool) (RelayRequest, []byte, *cloudrelay.RouteLedger, http.Handler) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	source, generation := "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	ledger, err := cloudrelay.InitializeRouteLedger(dir, source, generation, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	var notifier *cloudrelay.Notifier
	notifyOrigin, notifySecret := "", ""
	if notifications {
		notifyOrigin, notifySecret = "https://nas.example.test", strings.Repeat("c", 64)
		notifier, err = cloudrelay.NewNotifier(cloudrelay.NotificationConfig{Origin: notifyOrigin, SourceID: source, Generation: generation, Secret: notifySecret, Reader: reader, Control: control})
		if err != nil {
			t.Fatal(err)
		}
	}
	handler, err := cloudrelay.NewSourceHandler(store, reader, control, notifier)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	req := RelayRequest{SourceID: source, Generation: generation, Origin: server.URL, Domains: []string{"example.test"}, AllowedDomains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7, NotificationOrigin: notifyOrigin, Reader: reader, Control: control, Notification: notifySecret}
	return req, ca, ledger, handler
}

func TestRelayHandoffVerifiesSourceAndPreservesSecrets(t *testing.T) {
	for _, notifications := range []bool{false, true} {
		t.Run(fmt.Sprint(notifications), func(t *testing.T) {
			req, ca, ledger, handler := relayHandoffFixture(t, notifications)
			before, err := ledger.Read()
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareRelay(context.Background(), req, ca)
			if err != nil {
				t.Fatal(err)
			}
			var secret bytes.Buffer
			if err = prepared.WriteCredentials(&secret); err != nil {
				t.Fatal(err)
			}
			spec := prepared.Specification()
			public, err := json.Marshal([]ingress.SourceSpec{spec})
			if err != nil {
				t.Fatal(err)
			}
			sources, err := ingress.ParseSources(string(public), secret.String(), req.AllowedDomains)
			if err != nil || len(sources) != 1 || sources[0].Provider != "cloud-relay" || sources[0].Credentials.Control != req.Control || sources[0].Credentials.Notification != req.Notification {
				t.Fatalf("handoff: %v", err)
			}
			requestJSON, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			for _, output := range []string{string(public), string(requestJSON), fmt.Sprintf("%+v", prepared), fmt.Sprintf("%#v", prepared), fmt.Sprintf("%+v", req), fmt.Sprintf("%#v", req)} {
				for _, value := range []string{req.Reader, req.Control, req.Notification} {
					if value != "" && strings.Contains(output, value) {
						t.Fatal("public output exposed credential")
					}
				}
			}
			after, err := ledger.Read()
			if err != nil || before.Revision != after.Revision || before.Fingerprint != after.Fingerprint || before.Sequence != after.Sequence {
				t.Fatalf("read-only verification changed routes: %v", err)
			}
			record := httptest.NewRecorder()
			request := httptest.NewRequest("GET", "/v1/control-status?challenge="+strings.Repeat("1", 64), nil)
			request.Header.Set("Authorization", "Bearer "+req.Reader)
			handler.ServeHTTP(record, request)
			if record.Code != http.StatusUnauthorized {
				t.Fatalf("reader accessed control status: %d", record.Code)
			}
			record = httptest.NewRecorder()
			request.Header.Set("Authorization", "Bearer "+req.Control)
			handler.ServeHTTP(record, request)
			if record.Code != http.StatusOK || strings.Contains(record.Body.String(), req.Reader) || strings.Contains(record.Body.String(), req.Control) || notifications && strings.Contains(record.Body.String(), req.Notification) {
				t.Fatal("invalid or sensitive control response")
			}
			for _, query := range []string{"", "?challenge=bad", "?challenge=" + strings.Repeat("1", 64) + "&challenge=" + strings.Repeat("2", 64)} {
				record = httptest.NewRecorder()
				request = httptest.NewRequest("GET", "/v1/control-status"+query, nil)
				request.Header.Set("Authorization", "Bearer "+req.Control)
				handler.ServeHTTP(record, request)
				if record.Code != http.StatusBadRequest {
					t.Fatalf("invalid challenge accepted: %d", record.Code)
				}
			}
		})
	}
}

func TestRelayHandoffRejectsIdentityCredentialAndStorageDrift(t *testing.T) {
	for _, scenario := range []string{"reader", "control", "notification-secret", "notification-origin", "domain", "capacity", "generation", "allowed-domain", "missing-ca"} {
		t.Run(scenario, func(t *testing.T) {
			req, ca, ledger, _ := relayHandoffFixture(t, true)
			switch scenario {
			case "reader":
				req.Reader = strings.Repeat("d", 64)
			case "control":
				req.Control = strings.Repeat("d", 64)
			case "notification-secret":
				req.Notification = strings.Repeat("d", 64)
			case "notification-origin":
				req.NotificationOrigin = "https://other.example.test"
			case "domain":
				req.Domains, req.AllowedDomains = []string{"other.test"}, []string{"other.test"}
			case "capacity":
				req.CapacityBytes++
			case "generation":
				req.Generation = "33333333-3333-4333-8333-333333333333"
			case "allowed-domain":
				req.AllowedDomains = []string{"other.test"}
			case "missing-ca":
				ca = nil
			}
			prepared, err := prepareRelay(context.Background(), req, ca)
			if err == nil || prepared.Specification().ID != "" || prepared.WriteCredentials(&bytes.Buffer{}) == nil || !errors.Is(err, ErrJournal) && !errors.Is(err, ErrUnknown) {
				t.Fatalf("drift accepted: %v", err)
			}
			state, e := ledger.Read()
			if e != nil || state.Revision != 0 || len(state.Bindings) != 0 {
				t.Fatalf("drift changed routes: %v", e)
			}
		})
	}
}

func TestRelayHandoffWorkspaceSecretTransactionAndLostReply(t *testing.T) {
	req, ca, _, _ := relayHandoffFixture(t, true)
	prepared, err := prepareRelay(context.Background(), req, ca)
	if err != nil {
		t.Fatal(err)
	}
	service, workspace := installWorkspace(t)
	operation := "cfg-0123456789abcdef0123456789abcdef"
	if err = prepared.CommitFirstSource(t.Context(), lostConfigReply{service}, noopObserver{}, operation, req.AllowedDomains, req.CapacityBytes); !errors.Is(err, ErrUnknown) {
		t.Fatalf("lost response not treated as unknown: %v", err)
	}
	publicPath, privatePath := filepath.Join(workspace, "config.yml"), filepath.Join(workspace, ".anas", "secrets.yml")
	publicBefore, err := os.ReadFile(publicPath)
	if err != nil {
		t.Fatal(err)
	}
	privateBefore, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err = prepareRelay(context.Background(), req, ca)
	if err != nil {
		t.Fatal(err)
	}
	if err = prepared.CommitFirstSource(t.Context(), service, noopObserver{}, operation, req.AllowedDomains, req.CapacityBytes); err != nil {
		t.Fatalf("verified retry: %v", err)
	}
	publicAfter, _ := os.ReadFile(publicPath)
	privateAfter, _ := os.ReadFile(privatePath)
	if !bytes.Equal(publicBefore, publicAfter) || !bytes.Equal(privateBefore, privateAfter) || !bytes.Contains(publicAfter, []byte(req.SourceID)) {
		t.Fatal("verified retry rewrote or lost configuration")
	}
	for _, secret := range []string{req.Reader, req.Control, req.Notification} {
		if bytes.Contains(publicAfter, []byte(secret)) || !bytes.Contains(privateAfter, []byte(secret)) {
			t.Fatal("credential missing from private store or exposed in public config")
		}
	}
}

func TestRelayHandoffRejectsReplayedNotificationProof(t *testing.T) {
	req, _, _, realHandler := relayHandoffFixture(t, true)
	var cached []byte
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/control-status" {
			realHandler.ServeHTTP(w, r)
			return
		}
		if cached == nil {
			recorder := httptest.NewRecorder()
			realHandler.ServeHTTP(recorder, r)
			cached = append([]byte(nil), recorder.Body.Bytes()...)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(cached)
	})
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	req.Origin = server.URL
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	if _, err := prepareRelay(context.Background(), req, ca); err != nil {
		t.Fatalf("first proof: %v", err)
	}
	if _, err := prepareRelay(context.Background(), req, ca); !errors.Is(err, ErrUnknown) {
		t.Fatalf("replayed proof accepted: %v", err)
	}
}
