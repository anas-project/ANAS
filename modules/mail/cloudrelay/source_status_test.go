// TEST_CASES: MAILMOD-T-051
package cloudrelay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestSourceStatusCapacityAlertPersistsAndClearsAfterCleanup(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+3000)
	client, _, server := sourceHTTPFixture(t, source)
	ctx := context.Background()
	spec := ingress.SourceSpec{ID: routeSource, Generation: routeGeneration, Origin: server.URL, Mode: "active", CapacityBytes: routeMax + 3000, DeliveredDays: 7}
	status, err := client.Status(ctx)
	if err != nil || status.UsedBytes != routeMax || status.Notifications != "unconfigured" || status.Alert != nil || status.AccountQuota != "unknown" {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	if err = client.CheckReady(ctx, spec); err != nil {
		t.Fatalf("storage preflight: %v", err)
	}
	original := []byte(strings.Repeat("a", 2400))
	r, err := source.Store(routeOp2, "", []string{"alice@example.test"}, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Store(routeOp3, "", []string{"alias@example.test"}, []byte(strings.Repeat("b", 700))); !errors.Is(err, ErrOriginalCapacity) {
		t.Fatalf("expected capacity: %v", err)
	}
	status, err = client.Status(ctx)
	if err != nil || status.UsedBytes != routeMax+2400 || status.Alert == nil || status.Alert.Code != "capacity-or-admission-limit" {
		t.Fatalf("alert: %+v %v", status, err)
	}
	if err = client.CheckReady(ctx, spec); !errors.Is(err, ingress.ErrUnavailable) {
		t.Fatalf("pressure treated ready: %v", err)
	}
	reopenedLedger, err := OpenRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenOriginalStore(reopenedLedger, routeMax+3000, 7)
	if err != nil {
		t.Fatal(err)
	}
	client2, _, _ := sourceHTTPFixture(t, reopened)
	status, err = client2.Status(ctx)
	if err != nil || status.Alert == nil {
		t.Fatalf("lost alert on restart: %+v %v", status, err)
	}
	ackReservation(t, reopened, r, 0)
	if removed, err := reopened.Maintain(time.Now().Add(8 * 24 * time.Hour)); err != nil || !removed {
		t.Fatalf("cleanup: %v %v", removed, err)
	}
	status, err = client2.Status(ctx)
	if err != nil || status.Alert != nil || status.UsedBytes >= routeMax+2400 {
		t.Fatalf("alert did not clear after verified space: %+v %v", status, err)
	}
	if err = client.CheckReady(ctx, spec); err != nil {
		t.Fatalf("recovered storage preflight: %v", err)
	}
}

func TestSourceStatusPendingLimitAndRecovery(t *testing.T) {
	ledger, source, _ := originalFixture(t, routeMax+10000)
	body := []byte("data")
	digest := sha256.Sum256(body)
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("99999999-9999-4999-8999-%012d", i)
		if _, err := ledger.ReserveEnvelope(id, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alice@example.test"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, body); !errors.Is(err, ErrEnvelopeCapacity) {
		t.Fatalf("pending limit: %v", err)
	}
	client, _, _ := sourceHTTPFixture(t, source)
	status, err := client.Status(context.Background())
	if err != nil || status.PendingReservations != 64 || status.Alert == nil {
		t.Fatalf("pending status: %+v %v", status, err)
	}
	if _, err = source.Store("99999999-9999-4999-8999-000000000000", "", []string{"alice@example.test"}, body); err != nil {
		t.Fatal(err)
	}
	status, err = client.Status(context.Background())
	if err != nil || status.PendingReservations != 63 || status.Alert != nil {
		t.Fatalf("pending recovery: %+v %v", status, err)
	}
}

func TestSourceStatusConfiguredNotificationAndAuth(t *testing.T) {
	_, source, _ := originalFixture(t, routeMax+1000)
	notifyServer := httptest.NewTLSServer(http.HandlerFunc(validNoticeReply))
	defer notifyServer.Close()
	n := notifierForServer(t, notifyServer)
	reader, control := strings.Repeat("a", 64), strings.Repeat("b", 64)
	reused := *n
	reused.secret = bytesOfHex(t, reader)
	if _, err := NewSourceHandler(source, reader, control, &reused); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("reused key accepted: %v", err)
	}
	handler, err := NewSourceHandler(source, reader, control, n)
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
	status, err := client.Status(context.Background())
	if err != nil || status.Notifications != "configured" {
		t.Fatalf("configured: %+v %v", status, err)
	}
	spec := ingress.SourceSpec{ID: routeSource, Generation: routeGeneration, Origin: server.URL, Mode: "active", CapacityBytes: routeMax + 1000, DeliveredDays: 7, Notifications: true}
	if err = client.CheckReady(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, token string
		want        int
	}{{"/v1/status", control, 401}, {"/v1/status?extra=1", reader, 404}} {
		req := httptest.NewRequest("GET", "https://source.example"+tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("status request: %d want %d", w.Code, tc.want)
		}
	}
}
func bytesOfHex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestSourceStatusDetectsUnfinishedNotificationBindingChange(t *testing.T) {
	_, source, _ := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("message")); err != nil {
		t.Fatal(err)
	}
	notifyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer notifyServer.Close()
	n := notifierForServer(t, notifyServer)
	if _, err := source.ProcessNextNotification(t.Context(), n, time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	changed := *n
	changed.secret = bytesOfHex(t, strings.Repeat("d", 64))
	status, err := source.sourceStatus(&changed)
	if err != nil || status.Notifications != "configuration-error" {
		t.Fatalf("binding drift: %+v %v", status, err)
	}
}

func TestSourceStatusReportsBoundForwardDomain(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ledger, err := initializeRouteLedger(dir, routeSource, routeGeneration, []string{"example.test"}, "in.example.test")
	if err != nil {
		t.Fatal(err)
	}
	source, err := InitializeOriginalStore(ledger, 8<<20, 7)
	if err != nil {
		t.Fatal(err)
	}
	client, _, server := sourceHTTPFixture(t, source)
	status, err := client.Status(t.Context())
	if err != nil || status.ForwardDomain != "in.example.test" {
		t.Fatal("forward domain absent", err)
	}
	spec := ingress.SourceSpec{ID: routeSource, Generation: routeGeneration, Origin: server.URL, Provider: "cloud-relay", CloudflareForward: &ingress.CloudflareForwardSpec{Domain: "in.example.test"}}
	if err = client.CheckRelayMode(t.Context(), spec); err != nil {
		t.Fatal("matching relay rejected", err)
	}
	spec.CloudflareForward = nil
	if err = client.CheckRelayMode(t.Context(), spec); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("forward relay accepted as direct", err)
	}
	spec.CloudflareForward = &ingress.CloudflareForwardSpec{Domain: "wrong.example.test"}
	if err = client.CheckRelayMode(t.Context(), spec); !errors.Is(err, ingress.ErrIdentity) {
		t.Fatal("wrong domain accepted", err)
	}
}
