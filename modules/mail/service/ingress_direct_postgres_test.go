// TEST_CASES: MAILMOD-T-080
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func directFixtureID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	b[6], b[8] = b[6]&15|64, b[8]&63|128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type forwardFixtureSync struct {
	routes []ingress.Route
	calls  int
	before func()
}

func (f *forwardFixtureSync) Synchronize(_ context.Context, routes []ingress.Route) error {
	f.calls++
	f.routes = append([]ingress.Route(nil), routes...)
	if f.before != nil {
		f.before()
	}
	return ingress.ErrDestinationPending
}

// This tests the actual route coordinator against PostgreSQL and the real
// relay route handler. Directory and DMS readiness are controlled fixtures.
func TestPostgreSQLDirectVPSRouteCoordinator(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	owner, box, version := handoffPerson(t, db), directFixtureID(t), directFixtureID(t)
	primary, alias := "main-"+box+"@example.test", "alias-"+box+"@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: primary, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{
		{Address: primary, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"},
		{Address: alias, Kind: "alias", State: "active", Version: version, OperationID: version, OperationState: "complete"},
	}}
	projection, err := data.StageMailbox(t.Context(), resource, "")
	if err != nil {
		t.Fatal(err)
	}
	ready := map[string]string{box: projection.Revision}
	for _, provider := range []string{"cloud-relay", "worker-r2"} {
		t.Run(provider, func(t *testing.T) {
			source, generation := directFixtureID(t), directFixtureID(t)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				for _, q := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
					if _, err := db.ExecContext(ctx, q, source); err != nil {
						t.Error("fixture cleanup", err)
					}
				}
			})
			dir := t.TempDir()
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			ledger, err := cloudrelay.InitializeRouteLedger(dir, source, generation, []string{"example.test"})
			if err != nil {
				t.Fatal(err)
			}
			reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
			originals, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
			if err != nil {
				t.Fatal(err)
			}
			handler, err := cloudrelay.NewSourceHandler(originals, reader, control, nil)
			if err != nil {
				t.Fatal(err)
			}
			var wrongMode atomic.Bool
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if wrongMode.Load() && r.URL.Path == "/v1/status" {
					if r.Header.Get("Authorization") != "Bearer "+reader {
						http.Error(w, "unauthorized", 401)
						return
					}
					_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, CapacityBytes: 8 << 20, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "unconfigured", ForwardDomain: "in.example.test"})
					return
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{"example.test"}, AdditionalCA: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			entry := ingressEntry{client: client, spec: ingress.SourceSpec{ID: source, Generation: generation, Origin: server.URL, Provider: provider, Mode: "active", Domains: []string{"example.test"}}, routes: &ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation}}
			runtime := &ingressRuntime{store: data, entries: map[string]ingressEntry{source: entry}}
			if provider == "cloud-relay" {
				wrongMode.Store(true)
				if err = runtime.Synchronize(t.Context(), ready); !errors.Is(err, ingress.ErrIdentity) {
					t.Fatal("forward source accepted as direct", err)
				}
				state, e := client.ReadRoutes(t.Context())
				if e != nil || state.Revision != 0 {
					t.Fatal("mode mismatch changed source routes", e)
				}
				wrongMode.Store(false)
			}
			err = runtime.Synchronize(t.Context(), ready)
			if provider == "worker-r2" {
				if err == nil {
					t.Fatal("worker source without public routing reported success")
				}
				summary, e := data.IngressSummary(t.Context(), source, generation)
				if e != nil || summary.RouteError != "cloudflare-not-configured" {
					t.Fatal("missing worker routing not reported", e)
				}
				return
			}
			if err != nil {
				t.Fatal("VPS direct source route reconciliation failed", err)
			}
			state, err := client.ReadRoutes(t.Context())
			if err != nil || state.Revision != 1 || len(state.Bindings) != 2 {
				t.Fatal("VPS main/alias routes missing", err)
			}
			for _, binding := range state.Bindings {
				if (binding.Address != primary && binding.Address != alias) || binding.MailboxID != box || binding.OwnerAnchor != owner {
					t.Fatal("VPS route identity mismatch")
				}
			}
			if err := data.CloudflareRuleFailure(t.Context(), source, generation, "cloudflare-not-configured"); err != nil {
				t.Fatal(err)
			}
			if err := runtime.Synchronize(t.Context(), ready); err != nil {
				t.Fatal("VPS route retry failed", err)
			}
			summary, err := data.IngressSummary(t.Context(), source, generation)
			if err != nil || summary.RouteError != "" {
				t.Fatal("stale Cloudflare error not cleared", err)
			}
			state, err = client.ReadRoutes(t.Context())
			if err != nil || state.Revision != 1 {
				t.Fatal("retry repeated route write", err)
			}
			forward := &forwardFixtureSync{}
			entry.spec.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: "in.example.test"}
			entry.forward = forward
			runtime.entries[source] = entry
			wrongMode.Store(true)
			if err = runtime.Synchronize(t.Context(), ready); !errors.Is(err, ingress.ErrDestinationPending) || forward.calls != 1 || len(forward.routes) != 2 {
				t.Fatal("forward synchronization bypassed or falsely reported ready", err)
			}
			wrongMode.Store(false)
			if err = runtime.Synchronize(t.Context(), ready); !errors.Is(err, ingress.ErrIdentity) || forward.calls != 1 {
				t.Fatal("direct relay accepted as forward", err)
			}
		})
	}
}

// The route handlers and database are real; the directory, DMS and public
// Cloudflare verification response are controlled fixtures.
func TestPostgreSQLLocalReadinessWaitsForAllSources(t *testing.T) {
	for _, unavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("source-unavailable-%t", unavailable), func(t *testing.T) {
			db := loginTestDatabase(t)
			data := store.Store{DB: db}
			owner, box, version := handoffPerson(t, db), directFixtureID(t), directFixtureID(t)
			primary, alias := "main-"+box+"@one.example.test", "alias-"+box+"@two.example.test"
			resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: primary, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{
				{Address: primary, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"},
				{Address: alias, Kind: "alias", State: "active", Version: version, OperationID: version, OperationState: "complete"},
			}}
			backend := &syncDMS{}
			runtime := &ingressRuntime{store: data, entries: map[string]ingressEntry{}}
			clients := make([]*ingress.SourceClient, 0, 2)
			forward := &forwardFixtureSync{}
			for i, domain := range []string{"one.example.test", "two.example.test"} {
				source, generation := directFixtureID(t), directFixtureID(t)
				t.Cleanup(func() {
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					for _, q := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
						if _, err := db.ExecContext(ctx, q, source); err != nil {
							t.Error("fixture cleanup", err)
						}
					}
				})
				dir := t.TempDir()
				if err := os.Chmod(dir, 0700); err != nil {
					t.Fatal(err)
				}
				ledger, err := cloudrelay.InitializeRouteLedger(dir, source, generation, []string{domain})
				if err != nil {
					t.Fatal(err)
				}
				originals, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
				if err != nil {
					t.Fatal(err)
				}
				reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
				handler, err := cloudrelay.NewSourceHandler(originals, reader, control, nil)
				if err != nil {
					t.Fatal(err)
				}
				server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/status" && i == 1 && unavailable {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					if r.URL.Path == "/v1/status" && i == 0 {
						if r.Header.Get("Authorization") != "Bearer "+reader {
							http.Error(w, "unauthorized", http.StatusUnauthorized)
							return
						}
						_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, CapacityBytes: 8 << 20, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "unconfigured", ForwardDomain: "delivery.example.test"})
						return
					}
					handler.ServeHTTP(w, r)
				}))
				t.Cleanup(server.Close)
				ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
				client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{domain}, AdditionalCA: ca})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(client.Close)
				clients = append(clients, client)
				entry := ingressEntry{client: client, spec: ingress.SourceSpec{ID: source, Generation: generation, Origin: server.URL, Provider: "cloud-relay", Mode: "active", Domains: []string{domain}}, routes: &ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation}}
				if i == 0 {
					entry.spec.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: "delivery.example.test"}
					entry.forward = forward
				}
				runtime.entries[source] = entry
			}
			forward.before = func() {
				if !backend.receipt.Applied {
					t.Fatal("public coordination preceded DMS confirmation")
				}
				for i, client := range clients {
					state, err := client.ReadRoutes(t.Context())
					if err != nil || state.Revision != 1 || len(state.Bindings) != 1 || state.Bindings[0].MailboxID != box || state.Bindings[0].OwnerAnchor != owner || state.Bindings[0].Address != []string{primary, alias}[i] {
						t.Fatal("public coordination preceded verified local routes", i, err)
					}
				}
			}
			coordinator := mailCoordinator{store: data, directory: &syncDirectory{resource: resource}, dms: backend, sources: runtime}
			err := coordinator.reconcileMailbox(t.Context(), identity.MailboxReference{ID: box, OwnerAnchor: owner})
			projection, e := data.MailboxProjection(t.Context(), box)
			if e != nil {
				t.Fatal(e)
			}
			if unavailable {
				if err == nil || projection.Applied || forward.calls != 0 {
					t.Fatal("unavailable source granted readiness", err)
				}
				return
			}
			if !errors.Is(err, ingress.ErrDestinationPending) || !projection.Applied || projection.Mailbox.State != "active" || forward.calls != 1 {
				t.Fatal("verified local mailbox not available while public verification pending", err)
			}
			_, cookie, e := data.AdmitSession(t.Context(), store.VerifiedLogin{OwnerAnchor: owner, Issuer: fixtureIssuer, SID: "multisource-" + owner, AuthenticatedAt: time.Now().Add(-time.Minute), Expires: time.Now().Add(time.Hour)})
			if e != nil {
				t.Fatal(e)
			}
			grant, e := data.MailboxForSession(t.Context(), cookie, box)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = data.IssueCredentials(t.Context(), grant); e != nil {
				t.Fatal(e)
			}
		})
	}
}
