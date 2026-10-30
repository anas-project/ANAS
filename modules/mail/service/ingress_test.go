// TEST_CASES: MAILMOD-T-009
package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestIngressRefusesUnreservedCloudCapacityBeforePreparingRuntime(t *testing.T) {
	cfg := loginConfig{Sources: []ingress.RuntimeSource{{SourceSpec: ingress.SourceSpec{CapacityBytes: 2 << 30}}}, IngressCapacityBytes: 1 << 30}
	if _, e := prepareIngress(cfg, store.Store{}, nil); e == nil || e.Error() != "mail source total capacity invalid" {
		t.Fatal("source budget validation did not precede native or filesystem work", e)
	}
}

// No database or old route client is provided: reaching either one would mean
// the old entrance could be changed before the replacement failure was found.
func TestIngressCutoverChecksReplacementBeforeOldSource(t *testing.T) {
	for _, mode := range []string{"offline", "direct-offline", "wrong-capacity", "missing-notification"} {
		t.Run(mode, func(t *testing.T) {
			const nextID = "22222222-2222-4222-8222-222222222222"
			const oldID = "11111111-1111-4111-8111-111111111111"
			const generation = "33333333-3333-4333-8333-333333333333"
			reads := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reads++
				if r.Method != "GET" || r.URL.Path != "/v1/status" {
					t.Error("unexpected mutation before cutover")
				}
				if mode == "offline" || mode == "direct-offline" {
					w.WriteHeader(503)
					return
				}
				status := ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: nextID, Generation: generation, CapacityBytes: 1 << 30, UsedBytes: 4096, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "configured"}
				if mode == "wrong-capacity" {
					status.CapacityBytes++
				}
				if mode == "missing-notification" {
					status.Notifications = "configuration-error"
				}
				json.NewEncoder(w).Encode(status)
			}))
			defer srv.Close()
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
			client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: nextID, Generation: generation, Origin: srv.URL, Domains: []string{"example.test"}, Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64), AdditionalCA: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			next := ingressEntry{client: client, spec: ingress.SourceSpec{ID: nextID, Generation: generation, Origin: srv.URL, Mode: "active", Domains: []string{"example.test"}, CapacityBytes: 1 << 30, DeliveredDays: 7, Notifications: true}, routes: &ingress.RouteSync{}, public: &ingress.CloudflareRuleSync{}}
			if mode == "direct-offline" {
				next.spec.Provider, next.public = "cloud-relay", nil
			}
			old := ingressEntry{spec: ingress.SourceSpec{ID: oldID, Generation: generation, Mode: "draining", Domains: []string{"example.test"}}, public: &ingress.CloudflareRuleSync{}}
			runtime := &ingressRuntime{entries: map[string]ingressEntry{oldID: old, nextID: next}}
			err = runtime.Synchronize(t.Context(), nil)
			want := ingress.ErrConfiguration
			if mode == "offline" || mode == "direct-offline" {
				want = ingress.ErrUnavailable
			}
			if !errors.Is(err, want) || reads != 1 || runtime.cutoverReady {
				t.Fatal("failed replacement permitted old source withdrawal", err, reads, runtime.cutoverReady)
			}
		})
	}
}

func TestIngressDirectVPSReplacementRemainsFailClosed(t *testing.T) {
	for _, nextDirect := range []bool{false, true} {
		old := ingressEntry{spec: ingress.SourceSpec{ID: "11111111-1111-4111-8111-111111111111", Provider: "worker-r2", Mode: "draining", Domains: []string{"example.test"}}, public: &ingress.CloudflareRuleSync{}}
		next := ingressEntry{spec: ingress.SourceSpec{ID: "22222222-2222-4222-8222-222222222222", Provider: "worker-r2", Mode: "active", Domains: []string{"example.test"}}, client: &ingress.SourceClient{}, routes: &ingress.RouteSync{}, public: &ingress.CloudflareRuleSync{}}
		if nextDirect {
			next.spec.Provider, next.public = "cloud-relay", nil
			next.client = nil // No direct source may be prepared without a client.
		} else {
			old.spec.Provider, old.public = "cloud-relay", nil
		}
		runtime := &ingressRuntime{entries: map[string]ingressEntry{old.spec.ID: old, next.spec.ID: next}}
		if err := runtime.Synchronize(t.Context(), nil); err == nil || runtime.cutoverReady {
			t.Fatal("unsupported source replacement was allowed", nextDirect)
		}
	}
}

func TestForwardPendingOnlyExcludesMixedFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{ingress.ErrDestinationPending, true},
		{ingress.ErrForwardBatchPending, true},
		{errors.Join(ingress.ErrDestinationPending, ingress.ErrForwardBatchPending), true},
		{errors.Join(ingress.ErrDestinationPending, ingress.ErrRuleConflict), false},
		{errors.Join(ingress.ErrDestinationPending, ingress.ErrUnavailable), false},
		{errors.Join(ingress.ErrForwardBatchPending, context.DeadlineExceeded), false},
		{ingress.ErrRuleUnknown, false}, {nil, false},
	} {
		if got := onlyForwardPending(tc.err); got != tc.want {
			t.Fatal("pending classification", tc.err, got)
		}
	}
}
