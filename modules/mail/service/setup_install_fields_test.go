// TEST_CASES: MAILMOD-T-042, MAILMOD-T-059, MAILMOD-T-073
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestInstallFieldNamesMatchInputTypes(t *testing.T) {
	for _, tc := range []struct {
		value  any
		fields string
	}{
		{workerInstallInput{}, workerInstallFields},
		{relayInstallInput{}, relayInstallFields},
		{vpsInstallInput{}, vpsInstallFields},
	} {
		ty := reflect.TypeOf(tc.value)
		declared := strings.Fields(tc.fields)
		if len(declared) != ty.NumField() {
			t.Fatalf("%s field list length does not match", ty.Name())
		}
		for i := 0; i < ty.NumField(); i++ {
			name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]
			body, _ := json.Marshal(map[string]any{name: nil})
			if name == "" || !uniqueInstallJSON(body, tc.fields) {
				t.Fatalf("%s: missing %q", ty.Name(), name)
			}
			body, _ = json.Marshal(map[string]any{strings.ToUpper(name): nil})
			if uniqueInstallJSON(body, tc.fields) {
				t.Fatalf("%s: accepted case variant of %q", ty.Name(), name)
			}
		}
	}
}

func TestInstallRequiresExplicitRevisionAndNonNullNetworkMode(t *testing.T) {
	for _, kind := range []string{"worker", "relay", "vps"} {
		for _, mode := range []string{"valid-zero", "missing-revision", "null-revision", "negative-revision", "null-ipv4", "multiple-content-types"} {
			if kind == "worker" && mode == "null-ipv4" {
				continue
			}
			t.Run(kind+"/"+mode, func(t *testing.T) {
				h, login, _, dir := loginFixture(t)
				draft := store.SetupDraft{Inbound: "vps-direct", Domain: "example.test", SourceURL: "https://relay.example.test", LimitsConfirmed: true, Limits: store.SetupLimits{CloudBytes: 2 << 30, DeliveredDays: 7}, NotificationURL: "https://mail.example.test"}
				data := &setupHTTPStore{httpStore: login, state: store.SetupState{Revision: 0, Draft: draft}}
				directory := &setupHTTPDirectory{httpDirectory: dir, admin: true}
				h.store, h.directory = data, directory
				var input any
				var calls func() int
				path := "/api/setup/" + kind + "-install"
				switch kind {
				case "worker":
					data.state.Draft.Inbound = "worker-r2"
					installer := &fakeWorkerInstaller{}
					h.install, calls = installer, func() int { return installer.calls }
					input = workerInstallInput{Revision: 0, Action: "prepare", OperationID: "cfg-0123456789abcdef0123456789abcdef", AccountID: "account", ZoneID: "zone", Worker: "worker", Bucket: "bucket", CapacityBytes: 1 << 30, DeploymentToken: "private-deployment-token", RoutingToken: "private-routing-token"}
				case "relay":
					installer := &fakeRelayInstaller{}
					h.install, calls = installer, func() int { return installer.relayCalls }
					input = relayInstallInput{Revision: 0, Action: "prepare", OperationID: "cfg-0123456789abcdef0123456789abcdef", SourceID: "11111111-1111-4111-8111-111111111111", Generation: "22222222-2222-4222-8222-222222222222", CapacityBytes: 1 << 30, ReaderToken: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64), NotificationToken: strings.Repeat("c", 64)}
				case "vps":
					installer := &fakeVPSInstaller{}
					h.install, calls = installer, func() int { return installer.calls }
					input = vpsInstallInput{Revision: 0, Action: "publish", OperationID: "cfg-0123456789abcdef0123456789abcdef", CapacityBytes: 1 << 30, SSHHost: "relay.example.test", SSHPort: 22, SSHUser: "relay", SSHHostKey: "public-host-key", SSHPrivateKey: "private-ssh-key", TLSCertificate: "certificate", TLSPrivateKey: "private-tls-key", SMTPHostname: "relay.example.test"}
				}
				raw, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				body := string(raw)
				switch mode {
				case "missing-revision":
					body = strings.Replace(body, `"revision":0,`, "", 1)
				case "null-revision":
					body = strings.Replace(body, `"revision":0`, `"revision":null`, 1)
				case "negative-revision":
					body = strings.Replace(body, `"revision":0`, `"revision":-1`, 1)
				case "null-ipv4":
					body = strings.Replace(body, `"ipv4_only":false`, `"ipv4_only":null`, 1)
				}
				r := httptest.NewRequest(http.MethodPost, "https://mail.example.test"+path, strings.NewReader(body))
				r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "session-secret"})
				r.Header.Set("Origin", "https://mail.example.test")
				r.Header.Set("Content-Type", "application/json")
				if mode == "multiple-content-types" {
					r.Header.Add("Content-Type", "application/json")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				want, wantCalls := http.StatusBadRequest, 0
				if mode == "valid-zero" {
					want, wantCalls = http.StatusOK, 1
				}
				if w.Code != want || calls() != wantCalls || strings.Contains(w.Body.String(), "private-") {
					t.Fatalf("mode=%s status=%d installer calls=%d", mode, w.Code, calls())
				}
			})
		}
	}
}
