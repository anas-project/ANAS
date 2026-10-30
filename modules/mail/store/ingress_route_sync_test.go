// TEST_CASES: MAILMOD-T-012
package store_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestPostgreSQLSourceRouteHTTPSLostResponseAndUnknownCloudState(t *testing.T) {
	for _, mode := range []string{"lost-response", "stale-readback"} {
		t.Run(mode, func(t *testing.T) { sourceRouteTLSOutcome(t, mode) })
	}
}

func sourceRouteTLSOutcome(t *testing.T, mode string) {
	db, ctx := consumerDatabase(t)
	data := store.Store{DB: db}
	if e := data.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	source, generation, box, owner := fixtureUUID(t), fixtureUUID(t), fixtureUUID(t), fixtureUUID(t)
	version := fixtureUUID(t)
	address := "route-" + box + "@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"}}}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := db.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
		for _, q := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.address WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.mailbox WHERE owner_anchor=$1`, `DELETE FROM mail.person WHERE anchor=$1`} {
			if _, e := db.ExecContext(clean, q, owner); e != nil {
				t.Error(e)
			}
		}
	})
	p, e := data.StageMailbox(ctx, resource, "")
	if e != nil {
		t.Fatal(e)
	}
	ready := map[string]string{box: p.Revision}
	routes := []ingress.Route{{Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version}}
	empty, _ := ingress.RouteFingerprint(nil)
	remote := ingress.RouteState{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, Fingerprint: empty, Bindings: []ingress.RouteBinding{}}
	var mu sync.Mutex
	posts := 0
	var stale *ingress.RouteState
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var staged bool
		if db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM mail.ingress_route_state WHERE source_id=$1)`, source).Scan(&staged) != nil || !staged {
			t.Error("network used before durable intent")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path != "/v1/routes" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
				w.WriteHeader(401)
				return
			}
			if stale != nil {
				old := *stale
				stale = nil
				json.NewEncoder(w).Encode(old)
				return
			}
			json.NewEncoder(w).Encode(remote)
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 64) {
			w.WriteHeader(401)
			return
		}
		posts++
		var in struct {
			SourceID    string          `json:"source_id"`
			Generation  string          `json:"generation"`
			Expected    int64           `json:"expected_revision"`
			Operation   string          `json:"operation_id"`
			Fingerprint string          `json:"fingerprint"`
			Routes      []ingress.Route `json:"routes"`
		}
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&in) != nil || in.SourceID != source || in.Generation != generation || in.Expected != remote.Revision {
			w.WriteHeader(409)
			return
		}
		var issued bool
		if db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM mail.ingress_route_operation WHERE id=$1 AND state='issued')`, in.Operation).Scan(&issued) != nil || !issued {
			t.Error("POST before issued intent")
			w.WriteHeader(500)
			return
		}
		if mode == "stale-readback" {
			old := remote
			stale = &old
		}
		remote.Revision++
		remote.OperationID = in.Operation
		remote.Fingerprint = in.Fingerprint
		for _, route := range in.Routes {
			b := store.IngressBinding{SourceID: source, Generation: generation, Address: route.Address, MailboxID: route.MailboxID, OwnerAnchor: route.OwnerAnchor, ClaimVersion: route.ClaimVersion}
			remote.Bindings = append(remote.Bindings, ingress.RouteBinding{Route: route, ID: b.Key(), FirstSequence: 1})
		}
		if mode == "stale-readback" {
			json.NewEncoder(w).Encode(remote)
		} else {
			// Persisted cloud mutation, failed HTTP acknowledgement.
			w.WriteHeader(503)
		}
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, e := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: ca, IPv4Only: true})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	syncer := func() *ingress.RouteSync {
		return &ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation}
	}
	if e = syncer().Synchronize(ctx, routes, ready); e == nil {
		t.Fatal("lost cloud response falsely confirmed")
	}
	if e = syncer().Synchronize(ctx, routes, ready); e != nil {
		t.Fatal("restart did not recover persisted operation", e)
	}
	mu.Lock()
	count := posts
	remote.Revision++
	remote.OperationID = fixtureUUID(t)
	mu.Unlock()
	if count != 1 {
		t.Fatal("lost response duplicated write", count)
	}
	if e = syncer().Synchronize(ctx, routes, ready); !errors.Is(e, store.ErrIngressChanged) {
		t.Fatal("unknown newer cloud state adopted", e)
	}
	mu.Lock()
	count = posts
	mu.Unlock()
	if count != 1 {
		t.Fatal("unknown newer cloud state overwritten")
	}
	observed, e := data.IngressSummary(ctx, source, generation)
	if e != nil || observed.RouteError != "source-state-unrecognized" || observed.RouteRevision != 1 {
		t.Fatal("cloud conflict missing from administrator status", observed, e)
	}
	p, e = data.MailboxProjection(ctx, box)
	if e != nil || p.Applied {
		t.Fatal("source sync granted mailbox login", e)
	}
}
