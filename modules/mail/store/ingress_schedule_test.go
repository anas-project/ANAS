// TEST_CASES: MAILMOD-T-011
package store_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestPostgreSQLIngressRealMinuteScheduleAndNotificationWake(t *testing.T) {
	db, ctx := consumerDatabase(t)
	data := store.Store{DB: db}
	if e := data.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	source, generation := fixtureUUID(t), fixtureUUID(t)
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		if _, e := db.ExecContext(clean, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, source); e != nil {
			t.Error(e)
		}
	})
	times := make(chan time.Time, 8)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("b", 64) {
			w.WriteHeader(401)
			return
		}
		select {
		case times <- time.Now():
		default:
		}
		json.NewEncoder(w).Encode(ingress.SourcePage{Schema: "anas.mail-source/v1", SourceID: source, Generation: generation, Messages: []ingress.SourceMessage{}})
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, e := ingress.NewSourceClient(ingress.SourceConfig{ID: source, Generation: generation, Origin: server.URL, Token: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: ca, IPv4Only: true})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	consumer := &ingress.Consumer{Ledger: data, Source: client, Native: &nativeConsumerFixture{}, Directory: filepath.Join(t.TempDir(), "spool"), SourceID: source, Generation: generation}
	runCtx, cancel := context.WithCancel(ctx)
	wake := make(chan struct{}, 1)
	finished := make(chan error, 1)
	go func() { finished <- consumer.Run(runCtx, wake) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-finished:
			if !errors.Is(e, context.Canceled) {
				t.Error("consumer shutdown", e)
			}
		case <-time.After(5 * time.Second):
			t.Error("consumer did not stop")
		}
	})
	next := func(wait time.Duration) time.Time {
		t.Helper()
		select {
		case stamp := <-times:
			return stamp
		case <-time.After(wait):
			t.Fatal("source schedule did not fire")
			return time.Time{}
		}
	}
	first := next(5 * time.Second)
	wake <- struct{}{}
	woken := next(3 * time.Second)
	if woken.Sub(first) > 3*time.Second {
		t.Fatal("notification did not wake existing source scan")
	}
	periodic := next(65 * time.Second)
	elapsed := periodic.Sub(first)
	if elapsed < 59*time.Second || elapsed > 65*time.Second {
		t.Fatal("normal idle scan is not a real minute", elapsed)
	}
	var durable bool
	if e = db.QueryRowContext(ctx, `SELECT generation=$2::uuid AND last_success IS NOT NULL FROM mail.ingress_poll WHERE source_id=$1`, source, generation).Scan(&durable); e != nil || !durable {
		t.Fatal("real schedule did not persist progress", e)
	}
	t.Logf("real idle cadence %.3f seconds; notification wake %.3f seconds; no personnel login required", elapsed.Seconds(), woken.Sub(first).Seconds())
}
