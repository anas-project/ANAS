// TEST_CASES: MAILMOD-T-009
package ingress

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSourceStatusRejectsFalseQuotaIdentityAndRawErrors(t *testing.T) {
	c, route, _ := routeFixture(t)
	base := SourceStatus{Schema: sourceSchema, SourceID: route.SourceID, Generation: route.Generation, CapacityBytes: 2 << 30, UsedBytes: 1024, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "configured"}
	for _, mode := range []string{"normal", "wrong-source", "impossible-usage", "quota-claim", "unbounded-reservations", "arbitrary-alert", "future-alert", "wrong-token"} {
		t.Run(mode, func(t *testing.T) {
			v := base
			status := 200
			switch mode {
			case "wrong-source":
				v.SourceID = route.OperationID
			case "impossible-usage":
				v.UsedBytes = v.CapacityBytes + 1
			case "quota-claim":
				v.AccountQuota = "unlimited"
			case "unbounded-reservations":
				v.PendingReservations = 1000
			case "arbitrary-alert":
				v.Alert = &SourceAlert{Code: "raw-secret-response", At: time.Now()}
			case "future-alert":
				v.Alert = &SourceAlert{Code: "capacity-or-admission-limit", At: time.Now().Add(time.Hour)}
			case "wrong-token":
				status = 401
			}
			c.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Path != "/v1/status" || r.Header.Get("Authorization") != "Bearer "+c.cfg.Token {
					t.Error("status used control or arbitrary URL")
				}
				return routeResponse(status, v), nil
			})
			got, e := c.Status(t.Context())
			if mode == "normal" {
				if e != nil || got.AccountQuota != "unknown" {
					t.Fatal(e)
				}
			} else if mode == "wrong-token" {
				if !errors.Is(e, ErrAuthentication) {
					t.Fatal(e)
				}
			} else if !errors.Is(e, ErrIdentity) {
				t.Fatal("invalid provider observation accepted", e)
			}
		})
	}
}

func TestReplacementReadinessChecksActualStorageSettings(t *testing.T) {
	for _, mode := range []string{"ready", "capacity-changed", "retention-changed", "notifications-missing", "notifications-unexpected", "full", "busy", "pressure", "alert", "unreachable", "wrong-generation", "forward-domain-mismatch"} {
		t.Run(mode, func(t *testing.T) {
			c, state, _ := routeFixture(t)
			spec := SourceSpec{ID: c.cfg.ID, Generation: c.cfg.Generation, Origin: c.cfg.Origin, Mode: "active", CapacityBytes: 1 << 30, DeliveredDays: 7, Notifications: true}
			status := SourceStatus{Schema: sourceSchema, SourceID: spec.ID, Generation: spec.Generation, CapacityBytes: spec.CapacityBytes, UsedBytes: 4096, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "configured"}
			code := 200
			switch mode {
			case "capacity-changed":
				status.CapacityBytes++
			case "retention-changed":
				status.DeliveredDays = 1
			case "notifications-missing":
				status.Notifications = "configuration-error"
			case "notifications-unexpected":
				spec.Notifications = false
			case "full":
				status.UsedBytes = status.CapacityBytes
			case "busy":
				status.PendingReservations = 64
			case "pressure":
				status.PressureScan = true
			case "alert":
				status.Alert = &SourceAlert{Code: "capacity-or-admission-limit", At: time.Now()}
			case "unreachable":
				code = 503
			case "wrong-generation":
				status.Generation = state.OperationID
			case "forward-domain-mismatch":
				spec.Provider = "cloud-relay"
				spec.CloudflareForward = &CloudflareForwardSpec{Domain: "in.example.test"}
				status.ForwardDomain = "other.example.test"
			}
			calls := 0
			c.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != "GET" || r.URL.Path != "/v1/status" {
					t.Error("readiness changed receiving permissions")
				}
				return routeResponse(code, status), nil
			})
			err := c.CheckReady(t.Context(), spec)
			if (err == nil) != (mode == "ready") || calls != 1 {
				t.Fatal("replacement readiness accepted mismatched or unavailable storage", err, calls)
			}
		})
	}
}

func TestRelayModeRequiresAuthenticatedExactDeliveryDomain(t *testing.T) {
	for _, mode := range []string{"direct", "forward", "wrong-domain", "missing-domain", "direct-points-forward", "null-domain", "duplicate-domain", "case-domain", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			c, state, _ := routeFixture(t)
			spec := SourceSpec{ID: c.cfg.ID, Generation: c.cfg.Generation, Origin: c.cfg.Origin, Provider: "cloud-relay", Mode: "active"}
			status := SourceStatus{Schema: sourceSchema, SourceID: state.SourceID, Generation: state.Generation, CapacityBytes: 1 << 30, UsedBytes: 1, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "unconfigured"}
			if mode != "direct" && mode != "direct-points-forward" {
				spec.CloudflareForward = &CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: "in.example.test"}
				status.ForwardDomain = "in.example.test"
			}
			switch mode {
			case "wrong-domain", "direct-points-forward":
				status.ForwardDomain = "foreign.example.test"
			case "missing-domain":
				status.ForwardDomain = ""
			}
			c.http.Transport = routeTransport(func(r *http.Request) (*http.Response, error) {
				if mode == "unavailable" {
					return nil, errors.New("private")
				}
				raw, _ := json.Marshal(status)
				body := string(raw)
				switch mode {
				case "null-domain":
					body = strings.Replace(body, `"forward_domain":"in.example.test"`, `"forward_domain":null`, 1)
				case "duplicate-domain":
					body = strings.Replace(body, `"forward_domain":"in.example.test"`, `"forward_domain":"foreign.example.test","forward_domain":"in.example.test"`, 1)
				case "case-domain":
					body = strings.Replace(body, `"forward_domain":`, `"Forward_Domain":`, 1)
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			err := c.CheckRelayMode(t.Context(), spec)
			if mode == "direct" || mode == "forward" {
				if err != nil {
					t.Fatal("matching mode rejected", err)
				}
			} else if mode == "unavailable" {
				if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private") {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrIdentity) {
				t.Fatal("incorrect or ambiguous mode accepted", err)
			}
		})
	}
}
