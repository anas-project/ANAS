// TEST_CASES: MAILMOD-T-080
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// The real service coordinator, source handlers and PostgreSQL communicate
// over TLS with a synthetic Cloudflare API. This is not a public mail test.
func TestPostgreSQLWorkerToForwardCutoverPreservesOldUntilVerified(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	owner, box, version, aliasVersion := handoffPerson(t, db), directFixtureID(t), directFixtureID(t), directFixtureID(t)
	address, alias := "main-"+box+"@example.test", "alias-"+box+"@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{
		{Address: address, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"},
		{Address: alias, Kind: "alias", State: "active", Version: aliasVersion, OperationID: aliasVersion, OperationState: "complete"},
	}}
	projection, err := data.StageMailbox(t.Context(), resource, "")
	if err != nil {
		t.Fatal(err)
	}
	ready := map[string]string{box: projection.Revision}
	oldID, oldGeneration, newID, newGeneration := directFixtureID(t), directFixtureID(t), directFixtureID(t), directFixtureID(t)
	returnID, returnGeneration := directFixtureID(t), directFixtureID(t)
	secondID, secondGeneration := directFixtureID(t), directFixtureID(t)
	directID, directGeneration := directFixtureID(t), directFixtureID(t)
	fixtureSources := []string{oldID, newID, returnID, secondID, directID}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, source := range fixtureSources {
			for _, query := range []string{
				`DELETE FROM mail.forward_rule WHERE source_id=$1`,
				`DELETE FROM mail.forward_destination WHERE source_id=$1`,
				`DELETE FROM mail.forward_source WHERE source_id=$1`,
				`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`,
				`DELETE FROM mail.cloudflare_rule WHERE source_id=$1`,
				`DELETE FROM mail.ingress_binding WHERE source_id=$1`,
				`DELETE FROM mail.ingress_route_operation WHERE source_id=$1`,
				`DELETE FROM mail.ingress_route_state WHERE source_id=$1`,
			} {
				if _, err := db.ExecContext(ctx, query, source); err != nil {
					t.Error("source cleanup", err)
				}
			}
		}
	})
	readerOld, controlOld := strings.Repeat("a", 64), strings.Repeat("b", 64)
	readerNew, controlNew := strings.Repeat("c", 64), strings.Repeat("d", 64)
	readerReturn, controlReturn := strings.Repeat("e", 64), strings.Repeat("f", 64)
	readerSecond, controlSecond := strings.Repeat("1", 64), strings.Repeat("2", 64)
	readerDirect, controlDirect := strings.Repeat("3", 64), strings.Repeat("4", 64)
	makeHandler := func(id, gen, reader, control string) http.Handler {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		ledger, err := cloudrelay.InitializeRouteLedger(dir, id, gen, []string{"example.test"})
		if err != nil {
			t.Fatal(err)
		}
		originals, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := cloudrelay.NewSourceHandler(originals, reader, control, nil)
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	oldHandler := makeHandler(oldID, oldGeneration, readerOld, controlOld)
	newHandler := makeHandler(newID, newGeneration, readerNew, controlNew)
	returnHandler := makeHandler(returnID, returnGeneration, readerReturn, controlReturn)
	secondHandler := makeHandler(secondID, secondGeneration, readerSecond, controlSecond)
	directHandler := makeHandler(directID, directGeneration, readerDirect, controlDirect)
	var mu sync.Mutex
	rules := map[string]ingress.CloudflareRule{}
	destinations := map[string]string{}
	pendingDestinations := map[string]bool{}
	verified := false
	newDestinationsPending := false
	loseForwardDelete := false
	writes, deletes := 0, 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			auth := r.Header.Get("Authorization")
			if auth == "Bearer "+readerOld || auth == "Bearer "+controlOld {
				oldHandler.ServeHTTP(w, r)
				return
			}
			if auth == "Bearer "+readerNew || auth == "Bearer "+controlNew {
				if r.URL.Path == "/v1/status" {
					_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: newID, Generation: newGeneration, CapacityBytes: 8 << 20, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "unconfigured", ForwardDomain: "in.example.test"})
				} else {
					newHandler.ServeHTTP(w, r)
				}
				return
			}
			if auth == "Bearer "+readerReturn || auth == "Bearer "+controlReturn {
				returnHandler.ServeHTTP(w, r)
				return
			}
			if auth == "Bearer "+readerSecond || auth == "Bearer "+controlSecond {
				if r.URL.Path == "/v1/status" {
					_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: secondID, Generation: secondGeneration, CapacityBytes: 8 << 20, DeliveredDays: 7, AccountQuota: "unknown", Notifications: "unconfigured", ForwardDomain: "in.example.test"})
				} else {
					secondHandler.ServeHTTP(w, r)
				}
				return
			}
			if auth == "Bearer "+readerDirect || auth == "Bearer "+controlDirect {
				directHandler.ServeHTTP(w, r)
				return
			}
			http.Error(w, "bad token", http.StatusUnauthorized)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		emit := func(value any, list bool) {
			out := map[string]any{"success": true, "result": value}
			if list {
				n := 0
				switch v := value.(type) {
				case []ingress.CloudflareRule:
					n = len(v)
				case []map[string]any:
					n = len(v)
				}
				out["result_info"] = map[string]int{"page": 1, "per_page": 50, "count": n, "total_count": n}
			}
			_ = json.NewEncoder(w).Encode(out)
		}
		if strings.Contains(r.URL.Path, "/accounts/") {
			if r.Method == http.MethodPost {
				var in struct {
					Email string `json:"email"`
				}
				if json.NewDecoder(r.Body).Decode(&in) != nil {
					http.Error(w, "invalid", 400)
					return
				}
				id := fmt.Sprintf("%032x", len(destinations)+100)
				destinations[id] = in.Email
				pendingDestinations[id] = newDestinationsPending
				emit(map[string]any{"id": id, "email": in.Email, "verified": nil}, false)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/addresses") {
				rows := []map[string]any{}
				for id, email := range destinations {
					rows = append(rows, map[string]any{"id": id, "email": email})
				}
				emit(rows, true)
				return
			}
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			email, ok := destinations[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var when any
			if verified && !pendingDestinations[id] {
				when = "2026-01-01T00:00:00Z"
			}
			emit(map[string]any{"id": id, "email": email, "verified": when}, false)
			return
		}
		if strings.Contains(r.URL.Path, "/email/routing/rules") {
			if r.Method == http.MethodPost {
				var rule ingress.CloudflareRule
				if json.NewDecoder(r.Body).Decode(&rule) != nil || len(rule.Matchers) != 1 {
					http.Error(w, "invalid", 400)
					return
				}
				for _, existing := range rules {
					if existing.Matchers[0].Value == rule.Matchers[0].Value {
						http.Error(w, "duplicate", 409)
						return
					}
				}
				writes++
				rule.ID = fmt.Sprintf("%032x", writes)
				rules[rule.ID] = rule
				emit(rule, false)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/rules") {
				rows := []ingress.CloudflareRule{}
				for _, rule := range rules {
					rows = append(rows, rule)
				}
				emit(rows, true)
				return
			}
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			rule, ok := rules[id]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodDelete {
				delete(rules, id)
				deletes++
				if loseForwardDelete && rule.Actions[0].Type == "forward" {
					loseForwardDelete = false
					http.Error(w, "synthetic lost response", http.StatusServiceUnavailable)
					return
				}
				emit(map[string]string{"id": id}, false)
				return
			}
			emit(rule, false)
			return
		}
		http.NotFound(w, r)
	})
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(100), Subject: pkix.Name{CommonName: "api.cloudflare.com"}, DNSNames: []string{"api.cloudflare.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	defer server.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "api.cloudflare.com:443" {
			http.Error(w, "unexpected host", 403)
			return
		}
		target, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			target.Close()
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		go func() { defer conn.Close(); defer target.Close(); _, _ = io.Copy(target, conn) }()
		go func() { defer conn.Close(); defer target.Close(); _, _ = io.Copy(conn, target) }()
	}))
	defer proxy.Close()
	makeClient := func(id, gen, reader, control string) *ingress.SourceClient {
		c, err := ingress.NewSourceClient(ingress.SourceConfig{ID: id, Generation: gen, Origin: "https://api.cloudflare.com", Token: reader, ControlToken: control, Domains: []string{"example.test"}, Proxy: proxy.URL, AdditionalCA: ca})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(c.Close)
		return c
	}
	oldClient, newClient := makeClient(oldID, oldGeneration, readerOld, controlOld), makeClient(newID, newGeneration, readerNew, controlNew)
	returnClient := makeClient(returnID, returnGeneration, readerReturn, controlReturn)
	secondClient := makeClient(secondID, secondGeneration, readerSecond, controlSecond)
	directClient := makeClient(directID, directGeneration, readerDirect, controlDirect)
	zone, account := strings.Repeat("1", 32), strings.Repeat("2", 32)
	oldRules, err := ingress.NewCloudflareRules(oldClient, zone, "old-worker", strings.Repeat("r", 40))
	if err != nil {
		t.Fatal(err)
	}
	newRules, err := ingress.NewCloudflareForwardRules(newClient, account, zone, "in.example.test", strings.Repeat("s", 40), strings.Repeat("t", 40))
	if err != nil {
		t.Fatal(err)
	}
	old := ingressEntry{client: oldClient, spec: ingress.SourceSpec{ID: oldID, Generation: oldGeneration, Origin: "https://api.cloudflare.com", Mode: "active", Provider: "worker-r2", Domains: []string{"example.test"}}, routes: &ingress.RouteSync{Ledger: data, Source: oldClient, ID: oldID, Generation: oldGeneration}, public: &ingress.CloudflareRuleSync{Ledger: data, Client: oldRules}}
	next := ingressEntry{client: newClient, spec: ingress.SourceSpec{ID: newID, Generation: newGeneration, Origin: "https://api.cloudflare.com", Mode: "active", Provider: "cloud-relay", Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7, CloudflareForward: &ingress.CloudflareForwardSpec{AccountID: account, ZoneID: zone, Domain: "in.example.test"}}, routes: &ingress.RouteSync{Ledger: data, Source: newClient, ID: newID, Generation: newGeneration}, forward: &ingress.ForwardSync{Ledger: data, Client: newRules}, forwardReader: newRules}
	initial := &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old}}
	if err := initial.Synchronize(t.Context(), ready); err != nil {
		t.Fatal("initial worker", err)
	}
	old.spec.Mode = "draining"
	// Prepare a direct VPS while the old Worker is still public. No MX proof
	// exists here: both source bindings and both Worker rules must remain.
	direct := ingressEntry{client: directClient, spec: ingress.SourceSpec{ID: directID, Generation: directGeneration, Origin: "https://api.cloudflare.com", Mode: "active", Provider: "cloud-relay", Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7}, routes: &ingress.RouteSync{Ledger: data, Source: directClient, ID: directID, Generation: directGeneration}}
	directRuntime := &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old, directID: direct}}
	for attempt := 0; attempt < 2; attempt++ {
		err = directRuntime.Synchronize(t.Context(), ready)
		if !errors.Is(err, errDirectCutoverPending) || directRuntime.cutoverReady {
			t.Fatal("unverified direct MX did not remain pending", err)
		}
		for _, client := range []*ingress.SourceClient{oldClient, directClient} {
			state, readErr := client.ReadRoutes(t.Context())
			if readErr != nil || len(state.Bindings) != 2 {
				t.Fatal("direct preparation did not preserve both sources", readErr)
			}
			for _, binding := range state.Bindings {
				if binding.ClosedSequence != nil {
					t.Fatal("direct preparation closed a receiving binding")
				}
			}
		}
		mu.Lock()
		if len(rules) != 2 || deletes != 0 {
			mu.Unlock()
			t.Fatal("direct preparation changed the old Worker rules")
		}
		mu.Unlock()
	}
	if err = direct.routes.Synchronize(t.Context(), []ingress.Route{}, ready); err != nil {
		t.Fatal("isolated direct preparation cleanup", err)
	}
	runtime := &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old, newID: next}}
	err = runtime.Synchronize(t.Context(), ready)
	var pending forwardPublicPending
	if !errors.As(err, &pending) || !errors.Is(err, ingress.ErrDestinationPending) || runtime.cutoverReady {
		t.Fatal("verification did not remain pending", err)
	}
	summary, err := data.IngressSummary(t.Context(), newID, newGeneration)
	if err != nil || summary.RouteError != "cloudflare-destination-pending" {
		t.Fatal("pending destination status not recorded", err, summary.RouteError)
	}
	for _, client := range []*ingress.SourceClient{oldClient, newClient} {
		state, err := client.ReadRoutes(t.Context())
		if err != nil || len(state.Bindings) != 2 {
			t.Fatal("missing old/new receiving bindings", err)
		}
		for _, b := range state.Bindings {
			if b.ClosedSequence != nil {
				t.Fatal("pending verification closed a binding")
			}
		}
	}
	mu.Lock()
	if len(rules) != 2 || len(destinations) != 2 || deletes != 0 {
		t.Fatal("pending changed public rules", len(rules), len(destinations), deletes)
	}
	for id, rule := range rules {
		rule.Enabled = false
		rules[id] = rule
		break
	}
	mu.Unlock()
	if err = runtime.Synchronize(t.Context(), ready); !errors.Is(err, ingress.ErrRuleConflict) {
		t.Fatal("changed public rule did not block switch", err)
	}
	summary, err = data.IngressSummary(t.Context(), newID, newGeneration)
	if err != nil || summary.RouteError != "cloudflare-rule-conflict" {
		t.Fatal("public conflict not reflected in status", err, summary.RouteError)
	}
	mu.Lock()
	for id, rule := range rules {
		rule.Enabled = true
		rules[id] = rule
	}
	verified = true
	mu.Unlock()
	// Rebuild the runtime and synchronizers to exercise durable recovery,
	// rather than relying on the in-process pending/backoff cache.
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old, newID: next}}
	if err = runtime.Synchronize(t.Context(), ready); err != nil {
		t.Fatal("verified cutover", err)
	}
	if !runtime.cutoverReady {
		t.Fatal("successful cutover was not marked")
	}
	summary, err = data.IngressSummary(t.Context(), newID, newGeneration)
	if err != nil || summary.RouteError != "" {
		t.Fatal("confirmed public routing did not clear pending status", err, summary.RouteError)
	}
	oldState, err := oldClient.ReadRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range oldState.Bindings {
		if b.ClosedSequence == nil {
			t.Fatal("old binding remained open after cutover")
		}
	}
	// A provider can withdraw verification after the old entrance was closed.
	// Restarting must report pending without reviving those closed bindings.
	mu.Lock()
	verified = false
	mu.Unlock()
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old, newID: next}}
	err = runtime.Synchronize(t.Context(), ready)
	if !errors.As(err, &pending) || !errors.Is(err, ingress.ErrDestinationPending) || runtime.cutoverReady {
		t.Fatal("withdrawn provider verification was not pending", err)
	}
	oldState, err = oldClient.ReadRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range oldState.Bindings {
		if b.ClosedSequence == nil {
			t.Fatal("pending retry reopened a closed old binding")
		}
	}
	mu.Lock()
	verified = true
	mu.Unlock()
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{oldID: old, newID: next}}
	if err = runtime.Synchronize(t.Context(), ready); err != nil {
		t.Fatal("cutover did not recover after renewed verification", err)
	}
	mu.Lock()
	if len(rules) != 2 || deletes != 2 {
		mu.Unlock()
		t.Fatal("public rule replacement mismatch", len(rules), deletes)
	}
	for _, rule := range rules {
		if len(rule.Actions) != 1 || rule.Actions[0].Type != "forward" {
			mu.Unlock()
			t.Fatal("old public worker rule survived")
		}
	}
	mu.Unlock()

	secondRules, err := ingress.NewCloudflareForwardRules(secondClient, account, zone, "in.example.test", strings.Repeat("v", 40), strings.Repeat("w", 40))
	if err != nil {
		t.Fatal(err)
	}
	second := ingressEntry{client: secondClient, spec: ingress.SourceSpec{ID: secondID, Generation: secondGeneration, Origin: "https://api.cloudflare.com", Mode: "active", Provider: "cloud-relay", Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7, CloudflareForward: &ingress.CloudflareForwardSpec{AccountID: account, ZoneID: zone, Domain: "in.example.test"}}, routes: &ingress.RouteSync{Ledger: data, Source: secondClient, ID: secondID, Generation: secondGeneration}, forward: &ingress.ForwardSync{Ledger: data, Client: secondRules}, forwardReader: secondRules}
	next.spec.Mode = "draining"
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	mu.Lock()
	newDestinationsPending = true
	mu.Unlock()
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{newID: next, secondID: second}}
	err = runtime.Synchronize(t.Context(), ready)
	if !errors.As(err, &pending) || !errors.Is(err, ingress.ErrDestinationPending) || runtime.cutoverReady {
		t.Fatal("second VPS verification did not remain pending", err)
	}
	for _, client := range []*ingress.SourceClient{newClient, secondClient} {
		state, err := client.ReadRoutes(t.Context())
		if err != nil || len(state.Bindings) != 2 {
			t.Fatal("missing VPS transition bindings", err)
		}
		for _, b := range state.Bindings {
			if b.ClosedSequence != nil {
				t.Fatal("pending second VPS closed an active binding")
			}
		}
	}
	mu.Lock()
	if len(rules) != 2 || len(destinations) != 4 || deletes != 2 {
		mu.Unlock()
		t.Fatal("pending second VPS changed public rules")
	}
	for id := range pendingDestinations {
		pendingDestinations[id] = false
	}
	newDestinationsPending = false
	loseForwardDelete = true
	mu.Unlock()
	second.forward = &ingress.ForwardSync{Ledger: data, Client: secondRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{newID: next, secondID: second}}
	err = runtime.Synchronize(t.Context(), ready)
	if !errors.Is(err, ingress.ErrRuleUnknown) || runtime.cutoverReady {
		t.Fatal("VPS-to-VPS lost deletion response reported as success", err)
	}
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	second.forward = &ingress.ForwardSync{Ledger: data, Client: secondRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{newID: next, secondID: second}}
	if err = runtime.Synchronize(t.Context(), ready); err != nil || !runtime.cutoverReady {
		t.Fatal("VPS-to-VPS cutover did not recover lost deletion", err)
	}
	state, err := newClient.ReadRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range state.Bindings {
		if binding.ClosedSequence == nil {
			t.Fatal("old VPS binding still open after VPS-to-VPS cutover")
		}
	}
	mu.Lock()
	if len(rules) != 2 || deletes != 4 {
		mu.Unlock()
		t.Fatal("VPS-to-VPS public replacement mismatch")
	}
	mu.Unlock()
	next, newID, newGeneration, newClient, newRules = second, secondID, secondGeneration, secondClient, secondRules

	returnRules, err := ingress.NewCloudflareRules(returnClient, zone, "return-worker", strings.Repeat("u", 40))
	if err != nil {
		t.Fatal(err)
	}
	returnWorker := ingressEntry{client: returnClient, spec: ingress.SourceSpec{ID: returnID, Generation: returnGeneration, Origin: "https://api.cloudflare.com", Mode: "active", Provider: "worker-r2", Domains: []string{"example.test"}, CapacityBytes: 8 << 20, DeliveredDays: 7}, routes: &ingress.RouteSync{Ledger: data, Source: returnClient, ID: returnID, Generation: returnGeneration}, public: &ingress.CloudflareRuleSync{Ledger: data, Client: returnRules}}
	next.spec.Mode = "draining"
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{newID: next, returnID: returnWorker}}
	mu.Lock()
	for id, rule := range rules {
		rule.Enabled = false
		rules[id] = rule
		break
	}
	mu.Unlock()
	if err = runtime.Synchronize(t.Context(), ready); !errors.Is(err, ingress.ErrRuleConflict) {
		t.Fatal("drifted old forward rule did not block reverse switch", err)
	}
	summary, err = data.IngressSummary(t.Context(), returnID, returnGeneration)
	if err != nil || summary.RouteError != "cloudflare-rule-conflict" {
		t.Fatal("reverse public conflict not reflected in status", err, summary.RouteError)
	}
	state, err = newClient.ReadRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range state.Bindings {
		if b.ClosedSequence != nil {
			t.Fatal("reverse preflight closed old VPS on conflict")
		}
	}
	mu.Lock()
	for id, rule := range rules {
		rule.Enabled = true
		rules[id] = rule
	}
	loseForwardDelete = true
	mu.Unlock()
	err = runtime.Synchronize(t.Context(), ready)
	if !errors.Is(err, ingress.ErrRuleUnknown) || runtime.cutoverReady {
		t.Fatal("lost forward deletion response reported as success", err)
	}
	// Recreate both synchronizers to recover the issued deletion from PG.
	next.forward = &ingress.ForwardSync{Ledger: data, Client: newRules}
	returnWorker.public = &ingress.CloudflareRuleSync{Ledger: data, Client: returnRules}
	runtime = &ingressRuntime{store: data, entries: map[string]ingressEntry{newID: next, returnID: returnWorker}}
	if err = runtime.Synchronize(t.Context(), ready); err != nil || !runtime.cutoverReady {
		t.Fatal("reverse cutover did not recover lost deletion", err)
	}
	state, err = newClient.ReadRoutes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range state.Bindings {
		if b.ClosedSequence == nil {
			t.Fatal("old VPS binding still open after reverse cutover")
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(rules) != 2 || deletes != 6 {
		t.Fatal("reverse public rule replacement mismatch", len(rules), deletes)
	}
	for _, rule := range rules {
		if len(rule.Actions) != 1 || rule.Actions[0].Type != "worker" || rule.Actions[0].Value[0] != "return-worker" {
			t.Fatal("old forward rule survived reverse cutover")
		}
	}
}
