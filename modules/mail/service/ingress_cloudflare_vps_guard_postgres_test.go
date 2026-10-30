// TEST_CASES: MAILMOD-T-080
package main

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Real PostgreSQL, route synchronization and relay source protocol. The two
// messages are synthetic; no actual Cloudflare, DMS or notification is used.
func TestPostgreSQLVPSFixtureProtectsVerification(t *testing.T) {
	db := loginTestDatabase(t)
	data := store.Store{DB: db}
	owner, box, version := handoffPerson(t, db), directFixtureID(t), directFixtureID(t)
	source, generation := directFixtureID(t), directFixtureID(t)
	address := "guard-" + box + "@example.test"
	resource := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"}}}
	projection, err := data.StageMailbox(t.Context(), resource, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
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
	originals, err := cloudrelay.InitializeOriginalStore(ledger, 8<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	handler, err := cloudrelay.NewSourceHandler(originals, reader, control, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{"example.test"}, AdditionalCA: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	route := ingress.Route{Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version}
	sync := ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation}
	if err = sync.Synchronize(t.Context(), []ingress.Route{route}, map[string]string{box: projection.Revision}); err != nil {
		t.Fatal("route intent and readback", err)
	}
	state, err := client.ReadRoutes(t.Context())
	if err != nil || state.Revision != 1 || len(state.Bindings) != 1 {
		t.Fatal("route not confirmed", err)
	}
	if _, err = originals.Store(directFixtureID(t), "sender@example.test", []string{address}, []byte("synthetic verification")); err != nil {
		t.Fatal(err)
	}
	first, err := client.List(t.Context(), "")
	if err != nil || len(first.Messages) != 1 || first.Cursor != nil {
		t.Fatal("first message unavailable", err)
	}
	verificationID := first.Messages[0].Metadata.MessageID
	if _, err = originals.Store(directFixtureID(t), "sender@example.test", []string{address}, []byte("synthetic test message")); err != nil {
		t.Fatal(err)
	}
	guard := cloudflareVPSFixtureSource{source: client, sourceID: source, generation: generation, verification: verificationID, route: route}
	consumer := ingress.Consumer{Ledger: data, Source: guard, Native: guardUnusedNative{}, Directory: t.TempDir(), SourceID: source, Generation: generation}
	if err = consumer.Poll(t.Context()); err != nil {
		t.Fatal("real consumer poll", err)
	}
	var total, verification int
	if err = db.QueryRowContext(t.Context(), `SELECT count(*), count(*) FILTER (WHERE message_id=$2) FROM mail.ingress_message WHERE source_id=$1`, source, verificationID).Scan(&total, &verification); err != nil || total != 1 || verification != 0 {
		t.Fatal("unexpected staged targets", total, verification, err)
	}
	if _, err = guard.Download(t.Context(), first.Messages[0], io.Discard); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("verification body accessible", err)
	}
	all, err := client.List(t.Context(), "")
	if err != nil || len(all.Messages) != 2 {
		t.Fatal("source messages not retained", err)
	}
	for _, m := range all.Messages {
		if m.Delivery != nil {
			t.Fatal("unexpected source acknowledgment")
		}
	}
}
