// TEST_CASES: MAILMOD-T-073
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

type fakeVPSInstaller struct {
	fakeWorkerInstaller
	calls   int
	request vpsInstallRequest
	after   func()
}

func (f *fakeVPSInstaller) InstallVPS(_ context.Context, r vpsInstallRequest) (workerInstallResult, error) {
	f.calls++
	f.request = r
	if f.after != nil {
		f.after()
	}
	return workerInstallResult{State: map[string]string{"publish": "published", "activate": "activated", "prepare": "prepared", "commit": "configured"}[r.Action], SourceID: "11111111-1111-4111-8111-111111111111"}, nil
}
func TestVPSInstallRequiresSavedDraftAndCurrentAdministrator(t *testing.T) {
	for _, mode := range []string{"publish", "activate", "prepare", "commit", "forward-publish", "forward-activate", "forward-prepare", "forward-commit", "forward-missing-token", "forward-same-token", "forward-domain-conflict", "direct-forward-fields", "origin", "duplicate", "case-duplicate", "case-only", "not-admin", "revoked-before", "revoked-after", "changed-after", "content-after", "read-after", "stale", "domain", "missing-client", "wrong-provider", "bad-key"} {
		t.Run(mode, func(t *testing.T) {
			h, login, _, dir := loginFixture(t)
			data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 3, Draft: store.SetupDraft{Inbound: "vps-direct", Domain: "example.test", SourceURL: "https://relay.example.test", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 2 << 30, DeliveredDays: 7}, NotificationURL: "https://mail.example.test"}}}
			directory := &setupHTTPDirectory{httpDirectory: dir, admin: true}
			install := &fakeVPSInstaller{}
			h.store, h.directory, h.install = data, directory, install
			input := vpsInstallInput{Revision: 3, Action: "publish", OperationID: "cfg-0123456789abcdef0123456789abcdef", CapacityBytes: 1 << 30, SSHHost: "relay.example.test", SSHPort: 22, SSHUser: "relay", SSHHostKey: "public-host-key", SSHPrivateKey: "private-ssh-key", TLSCertificate: "certificate", TLSPrivateKey: "private-tls-key", SMTPHostname: "relay.example.test"}
			if strings.HasPrefix(mode, "forward-") || mode == "direct-forward-fields" {
				if mode != "direct-forward-fields" {
					data.state.Draft.Inbound = "cloud-relay"
				}
				input.AccountID, input.ZoneID, input.ForwardDomain = strings.Repeat("1", 32), strings.Repeat("2", 32), "delivery.example.test"
				input.RoutingToken, input.DestinationToken = strings.Repeat("r", 40), strings.Repeat("d", 40)
			}
			origin := "https://mail.example.test"
			switch mode {
			case "activate", "prepare", "commit":
				input.Action = mode
			case "forward-activate", "forward-prepare", "forward-commit":
				input.Action = strings.TrimPrefix(mode, "forward-")
			case "forward-missing-token":
				input.DestinationToken = ""
			case "forward-same-token":
				input.DestinationToken = input.RoutingToken
			case "forward-domain-conflict":
				input.ForwardDomain = data.state.Draft.Domain
			case "origin":
				origin = "https://evil.test"
			case "not-admin":
				directory.admin = false
			case "revoked-before":
				dir.afterRead = func() {
					if dir.reads >= 2 {
						directory.admin = false
					}
				}
			case "revoked-after":
				install.after = func() { directory.admin = false }
			case "changed-after":
				install.after = func() { data.state.Revision++ }
			case "content-after":
				install.after = func() { data.state.Draft.Domain = "changed.test" }
			case "read-after":
				install.after = func() { data.err = errors.New("private database failure") }
			case "stale":
				data.state.Revision++
			case "domain":
				data.state.Draft.Domain = "other.test"
			case "missing-client":
				h.install = nil
			case "wrong-provider":
				data.state.Draft.Inbound = "worker-r2"
			case "bad-key":
				input.SSHPrivateKey = ""
			}
			raw, _ := json.Marshal(input)
			body := string(raw)
			if mode == "duplicate" {
				body = strings.Replace(body, `"revision":3`, `"revision":3,"revision":3`, 1)
			} else if mode == "case-duplicate" {
				body = strings.Replace(body, `"revision":3`, `"revision":3,"REVISION":3`, 1)
			} else if mode == "case-only" {
				body = strings.Replace(body, `"revision":3`, `"REVISION":3`, 1)
			}
			r := httptest.NewRequest("POST", "https://mail.example.test/api/setup/vps-install", strings.NewReader(body))
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
			r.Header.Set("Origin", origin)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := map[string]int{"publish": 200, "activate": 200, "prepare": 200, "commit": 200, "forward-publish": 200, "forward-activate": 200, "forward-prepare": 200, "forward-commit": 200, "forward-missing-token": 400, "forward-same-token": 400, "forward-domain-conflict": 400, "direct-forward-fields": 400, "origin": 403, "duplicate": 400, "case-duplicate": 400, "case-only": 400, "not-admin": 403, "revoked-before": 403, "revoked-after": 403, "changed-after": 503, "content-after": 503, "read-after": 503, "stale": 409, "domain": 409, "missing-client": 503, "wrong-provider": 409, "bad-key": 400}[mode]
			if w.Code != want || strings.Contains(w.Body.String(), "private-ssh-key") || strings.Contains(w.Body.String(), "private-tls-key") {
				t.Fatalf("unexpected response %d: %s", w.Code, w.Body.String())
			}
			if mode == "changed-after" || mode == "content-after" || mode == "read-after" {
				if install.calls != 1 || !strings.Contains(w.Body.String(), "install-unknown") || strings.Contains(w.Body.String(), "private database failure") || strings.Contains(w.Body.String(), "source_id") {
					t.Fatal("post-install uncertainty was not preserved")
				}
			}
			if want == 200 {
				if install.calls != 1 || install.request.Actor != fixtureAnchor || install.request.Provider != "vps-ssh" || install.request.SourceOrigin != data.state.Draft.SourceURL || install.request.TotalCapacityBytes != 2<<30 || strings.Contains(w.Body.String(), `"enabled":true`) {
					t.Fatal("bad request binding")
				}
				if strings.HasPrefix(mode, "forward-") && (install.request.AccountID != input.AccountID || install.request.ZoneID != input.ZoneID || install.request.ForwardDomain != input.ForwardDomain || install.request.RoutingToken != input.RoutingToken || install.request.DestinationToken != input.DestinationToken || strings.Contains(w.Body.String(), input.RoutingToken) || strings.Contains(w.Body.String(), input.DestinationToken)) {
					t.Fatal("forward binding missing or leaked")
				}
			} else if mode != "revoked-after" && mode != "changed-after" && mode != "content-after" && mode != "read-after" && install.calls != 0 {
				t.Fatal("denied request reached installer")
			}
		})
	}
}
