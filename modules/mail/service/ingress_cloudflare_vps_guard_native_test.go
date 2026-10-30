// TEST_CASES: MAILMOD-T-080
package main

import (
	"context"
	"encoding/pem"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Real PG, VPS source and DMS for the guarded single-message subset. The
// Cloudflare verification and public SMTP messages here are synthetic; a
// separate live controller is required before calling this a public test.
func nativeGuardVPSIngress(t *testing.T, data store.Store, backend *dms.Client, resource identity.MailboxResource) string {
	t.Helper()
	ctx := t.Context()
	source, generation := directFixtureID(t), directFixtureID(t)
	route := ingress.Route{Address: resource.Addresses[0].Address, MailboxID: resource.ID, OwnerAnchor: resource.OwnerAnchor, ClaimVersion: resource.Addresses[0].Version}
	projection, err := data.MailboxProjection(ctx, resource.ID)
	if err != nil || !projection.Applied {
		t.Fatal("native mailbox projection unavailable", err)
	}
	dir := t.TempDir()
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := cloudrelay.InitializeRouteLedger(dir, source, generation, []string{strings.Split(route.Address, "@")[1]})
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
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{strings.Split(route.Address, "@")[1]}, AdditionalCA: ca})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(clean, q, source); e != nil {
				t.Error("fixture cleanup", e)
			}
		}
	})
	sync := ingress.RouteSync{Ledger: data, Source: client, ID: source, Generation: generation}
	if err = sync.Synchronize(ctx, []ingress.Route{route}, map[string]string{resource.ID: projection.Revision}); err != nil {
		t.Fatal("native route intent", err)
	}
	if _, err = originals.Store(directFixtureID(t), "sender@example.test", []string{route.Address}, []byte("synthetic verification secret")); err != nil {
		t.Fatal(err)
	}
	first, err := client.List(ctx, "")
	if err != nil || first.Cursor != nil || len(first.Messages) != 1 {
		t.Fatal("verification fixture unavailable", err)
	}
	verification := first.Messages[0].Metadata.MessageID
	subject := fmt.Sprintf("ANAS guarded VPS native %s", source[:8])
	raw := []byte("From: probe@mail.mailtest.invalid\r\nTo: forged@foreign.example\r\nSubject: " + subject + "\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=anas-native-ingress\r\n\r\n--anas-native-ingress\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nNative ingress body\r\n--anas-native-ingress\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=proof.bin\r\nContent-Transfer-Encoding: base64\r\n\r\nAAEC/w==\r\n--anas-native-ingress--\r\n")
	if _, err = originals.Store(directFixtureID(t), "sender@example.test", []string{route.Address}, raw); err != nil {
		t.Fatal(err)
	}
	guard := cloudflareVPSFixtureSource{source: client, sourceID: source, generation: generation, verification: verification, route: route}
	native := &countedNativeIntake{Client: backend}
	staging := t.TempDir()
	if err = os.Chmod(staging, 0700); err != nil {
		t.Fatal("private native staging", err)
	}
	consumer := ingress.Consumer{Ledger: data, Source: guard, Native: native, Directory: staging, SourceID: source, Generation: generation}
	deadline := time.Now().Add(150 * time.Second)
	var pollErr, workErr error
	var firstPollErr, firstWorkErr error
	for {
		pollErr = consumer.Poll(ctx)
		workErr = consumer.Work(ctx)
		if firstPollErr == nil && pollErr != nil {
			firstPollErr = pollErr
		}
		if firstWorkErr == nil && workErr != nil {
			firstWorkErr = workErr
		}
		var done int
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, source).Scan(&done); err != nil {
			t.Fatal(err)
		}
		if done == 1 {
			break
		}
		if time.Now().After(deadline) {
			var phase, state, lastError string
			var attempts int
			queryErr := data.DB.QueryRowContext(ctx, `SELECT phase,native_state,last_error,attempts FROM mail.ingress_message WHERE source_id=$1`, source).Scan(&phase, &state, &lastError, &attempts)
			t.Fatal("native delivery remained unconfirmed", "poll", pollErr, "work", workErr, "first poll", firstPollErr, "first work", firstWorkErr, "phase", phase, "state", state, "last error", lastError, "attempts", attempts, "posts", native.posts.Load(), "query", queryErr)
		}
		time.Sleep(time.Second)
	}
	if native.posts.Load() != 1 {
		t.Fatal("unexpected native submission count", native.posts.Load())
	}
	page, err := client.List(ctx, "")
	if err != nil || page.Cursor != nil || len(page.Messages) != 2 {
		t.Fatal("source messages unavailable", err)
	}
	var saved, delivered bool
	for _, m := range page.Messages {
		if m.Metadata.MessageID == verification {
			saved = m.Delivery == nil
		} else {
			delivered = m.Delivery != nil && m.Delivery.State == "stored"
		}
	}
	if !saved || !delivered {
		t.Fatal("verification or native receipt mismatch")
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("repeat poll", err)
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 1 {
		t.Fatal("repeat native submission", err)
	}
	return subject
}
