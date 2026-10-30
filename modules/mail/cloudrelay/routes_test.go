// TEST_CASES: MAILMOD-T-045
package cloudrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

const (
	routeSource     = "11111111-1111-4111-8111-111111111111"
	routeGeneration = "22222222-2222-4222-8222-222222222222"
	routeOwner      = "33333333-3333-4333-8333-333333333333"
	routeMailbox    = "44444444-4444-4444-8444-444444444444"
	routeClaim      = "55555555-5555-4555-8555-555555555555"
	routeOp1        = "66666666-6666-4666-8666-666666666666"
	routeOp2        = "77777777-7777-4777-8777-777777777777"
	routeOp3        = "88888888-8888-4888-8888-888888888888"
)

func ledgerFixture(t *testing.T) (*RouteLedger, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := InitializeRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	return l, dir
}

func TestRouteLedgerSourceClientProtocol(t *testing.T) {
	l, _ := ledgerFixture(t)
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	handler, err := NewRouteHandler(l, reader, control)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(handler)
	defer server.Close()
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: server.URL, Token: reader, ControlToken: control, Domains: []string{"example.test"}, AdditionalCA: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	initial, err := client.ReadRoutes(ctx)
	if err != nil || initial.Revision != 0 {
		t.Fatalf("read: %+v %v", initial, err)
	}
	route := routeFixture("alice@example.test")
	updated, err := client.ReplaceRoutes(ctx, 0, routeOp1, []ingress.Route{route})
	if err != nil || updated.Revision != 1 || len(updated.Bindings) != 1 {
		t.Fatalf("replace: %+v %v", updated, err)
	}
	if _, err = client.ReplaceRoutes(ctx, 0, routeOp2, []ingress.Route{route}); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatalf("stale: %v", err)
	}
	fingerprint, _ := ingress.RouteFingerprint([]ingress.Route{route})
	body, _ := json.Marshal(routeRequest{SourceID: routeSource, Generation: routeGeneration, ExpectedRevision: 1, OperationID: routeOp2, Fingerprint: fingerprint, Routes: []ingress.Route{route}})
	for _, tc := range []struct {
		name, token string
		body        []byte
		status      int
	}{
		{"reader cannot write", reader, body, 401},
		{"duplicate root", control, append([]byte(`{"source_id":"`+routeSource+`",`), body[1:]...), 400},
		{"case alias", control, bytes.Replace(body, []byte(`"source_id"`), []byte(`"Source_ID"`), 1), 400},
		{"duplicate nested", control, bytes.Replace(body, []byte(`"address":`), []byte(`"address":"alice@example.test","address":`), 1), 400},
		{"wrong fingerprint", control, bytes.Replace(body, []byte(fingerprint), []byte(strings.Repeat("0", 64)), 1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "https://source.example/v1/routes", bytes.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d", w.Code, tc.status)
			}
		})
	}
	final, err := client.ReadRoutes(ctx)
	if err != nil || final.Revision != 1 {
		t.Fatalf("invalid requests changed state: %+v %v", final, err)
	}
}
func routeFixture(address string) ingress.Route {
	return ingress.Route{Address: address, MailboxID: routeMailbox, OwnerAnchor: routeOwner, ClaimVersion: routeClaim}
}
func TestRouteLedgerReplacementAndRestart(t *testing.T) {
	l, dir := ledgerFixture(t)
	primary, alias := routeFixture("alice@example.test"), routeFixture("alias@example.test")
	first, err := l.Replace(0, routeOp1, []ingress.Route{primary, alias})
	if err != nil || first.Revision != 1 || len(first.Bindings) != 2 || first.Bindings[0].FirstSequence != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	replay, err := l.Replace(0, routeOp1, []ingress.Route{alias, primary})
	if err != nil || replay.Fingerprint != first.Fingerprint || replay.Revision != 1 {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if _, err = l.Replace(0, routeOp2, []ingress.Route{primary}); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err = l.Replace(1, routeOp1, []ingress.Route{primary}); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("reused operation: %v", err)
	}
	// The reservation is metadata only; no SMTP or raw storage is connected.
	_, err = l.ReserveEnvelope(routeOp3, "", strings.Repeat("a", 64), 10, []string{alias.Address})
	if err != nil {
		t.Fatal(err)
	}
	transferred := alias
	transferred.OwnerAnchor = routeOp2
	transferred.MailboxID = routeOp3
	transferred.ClaimVersion = routeOp1
	second, err := l.Replace(1, routeOp2, []ingress.Route{primary, transferred})
	if err != nil || len(second.Bindings) != 3 || second.Bindings[1].ClosedSequence == nil || *second.Bindings[1].ClosedSequence != 1 || second.Bindings[2].FirstSequence != 2 {
		t.Fatalf("transfer: %+v %v", second, err)
	}
	if _, err = l.Replace(2, routeOp3, []ingress.Route{primary, alias}); !errors.Is(err, ErrRouteConflict) {
		t.Fatalf("reopened binding: %v", err)
	}
	reopened, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Read()
	if err != nil || got.Revision != 2 || got.Sequence != 1 || got.Fingerprint != second.Fingerprint {
		t.Fatalf("restart: %+v %v", got, err)
	}
	if _, err = OpenRouteLedger(dir, routeSource, routeGeneration, []string{"other.test"}); !errors.Is(err, ErrRouteState) {
		t.Fatalf("changed domain: %v", err)
	}
}
func TestRouteLedgerFailsClosed(t *testing.T) {
	for _, mode := range []string{"missing", "bad-json", "duplicate-field", "wrong-permission", "symlink", "changed-source"} {
		t.Run(mode, func(t *testing.T) {
			l, dir := ledgerFixture(t)
			path := filepath.Join(dir, "routes.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "missing":
				err = os.Remove(path)
			case "bad-json":
				err = os.WriteFile(path, []byte("{"), 0600)
			case "duplicate-field":
				err = os.WriteFile(path, append([]byte(`{"domains":[],`), data[1:]...), 0600)
			case "wrong-permission":
				err = os.Chmod(path, 0644)
			case "symlink":
				if err = os.Remove(path); err == nil {
					err = os.Symlink("routes.lock", path)
				}
			case "changed-source":
				l.source = routeGeneration
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = l.Read(); !errors.Is(err, ErrRouteState) {
				t.Fatalf("read: %v", err)
			}
			if _, err = InitializeRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}); !errors.Is(err, ErrRouteState) {
				t.Fatalf("reinitialize: %v", err)
			}
		})
	}
}
func TestRouteLedgerRejectsInvalidAndConcurrentChanges(t *testing.T) {
	l, dir := ledgerFixture(t)
	primary := routeFixture("alice@example.test")
	foreign := routeFixture("alice@elsewhere.test")
	for _, routes := range [][]ingress.Route{{foreign}, {primary, primary}} {
		if _, err := l.Replace(0, routeOp1, routes); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("invalid routes: %v", err)
		}
	}
	fd, err := unix.Open(filepath.Join(dir, "routes.lock"), unix.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if _, err = l.Read(); !errors.Is(err, ErrRouteBusy) {
		t.Fatalf("held lock: %v", err)
	}
	unix.Flock(fd, unix.LOCK_UN)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, op := range []string{routeOp1, routeOp2} {
		wg.Add(1)
		go func(op string) { defer wg.Done(); _, err := l.Replace(0, op, []ingress.Route{primary}); results <- err }(op)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrRouteBusy) && !errors.Is(err, ErrRouteConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("successes: %d", success)
	}
}
