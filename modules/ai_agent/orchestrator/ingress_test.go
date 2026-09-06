package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func testRepo(t *testing.T) Repo {
	t.Helper()
	repo, err := ParseRepo("anas-project/ANAS")
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	return repo
}

func testConfig(t *testing.T) Config {
	t.Helper()
	registry, err := NewRegistry([]string{"codex"}, map[string]string{
		"codex": strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return Config{
		Enabled: true, WebhookSecret: testSecret, Runtimes: registry,
		RepositoryAllow: []Repo{testRepo(t)},
	}
}

func testIngress(t *testing.T) (*Ingress, *MemoryStore, *[]string) {
	t.Helper()
	store := NewMemoryStore()
	var lines []string
	ingress := &Ingress{
		Config: testConfig(t), Store: store, Redactor: NewRedactor(testSecret),
		Log: func(line string) { lines = append(lines, line) },
	}
	return ingress, store, &lines
}

func payloadFor(repo, sender, body string) []byte {
	envelope := map[string]any{
		"repository": map[string]any{"full_name": repo},
		"sender":     map[string]any{"login": sender},
		"issue":      map[string]any{"number": 7, "body": body},
	}
	encoded, _ := json.Marshal(envelope)
	return encoded
}

func post(t *testing.T, handler http.Handler, delivery, signature string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, WebhookPath, strings.NewReader(string(body)))
	req.Header.Set("X-Forgejo-Event", "issues")
	req.Header.Set("X-Forgejo-Delivery", delivery)
	if signature != "" {
		req.Header.Set("X-Forgejo-Signature", signature)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

// AGENT-R-011: an unsigned or wrongly signed delivery never reaches the inbox.
func TestIngressRefusesDeliveriesItCannotVerify(t *testing.T) {
	ingress, store, _ := testIngress(t)
	handler := ingress.Handler()
	body := payloadFor("anas-project/ANAS", "alice", "hello")

	for name, signature := range map[string]string{
		"missing":   "",
		"malformed": "not-hex",
		"wrong key": Sign("a different secret entirely", body),
		"tampered":  Sign(testSecret, []byte(`{"repository":{"full_name":"anas-project/other"}}`)),
	} {
		t.Run(name, func(t *testing.T) {
			resp := post(t, handler, "delivery-"+name, signature, body)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", resp.Code, http.StatusUnauthorized)
			}
		})
	}
	pending, err := store.PendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("inbox holds %d event(s); an unverified delivery must not be recorded", len(pending))
	}
}

// An empty configured secret must not turn every unsigned request into a valid
// one, which is what a plain "compare the HMACs" implementation does when both
// sides are empty.
func TestEmptySecretRefusesEverything(t *testing.T) {
	body := []byte(`{}`)
	if ValidSignature("", Sign("", body), body) {
		t.Fatal("an empty webhook secret accepted a signature; it must refuse outright")
	}
}

// AGENT-R-011: the delivery is persisted and answered 202 without any handler
// running on the request path.
func TestIngressRecordsThenAccepts(t *testing.T) {
	ingress, store, _ := testIngress(t)
	body := payloadFor("anas-project/ANAS", "alice", "please look at this")
	resp := post(t, ingress.Handler(), "delivery-1", Sign(testSecret, body), body)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusAccepted)
	}
	pending, err := store.PendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}
	if len(pending) != 1 || pending[0].DeliveryID != "delivery-1" {
		t.Fatalf("inbox = %+v, want the delivery recorded once and left pending", pending)
	}
	if pending[0].Source != "webhook" {
		t.Fatalf("source = %q, want webhook", pending[0].Source)
	}
}

// AGENT-R-012: ten deliveries of the same event produce one row, and every one
// of them is answered 202 -- a repeat is a success, not an error.
func TestRepeatedDeliveryIsRecordedOnce(t *testing.T) {
	ingress, store, _ := testIngress(t)
	handler := ingress.Handler()
	body := payloadFor("anas-project/ANAS", "alice", "please look at this")
	signature := Sign(testSecret, body)
	for attempt := 0; attempt < 10; attempt++ {
		resp := post(t, handler, "delivery-1", signature, body)
		if resp.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: status = %d, want %d", attempt, resp.Code, http.StatusAccepted)
		}
		want := string(DeliveryDuplicate)
		if attempt == 0 {
			want = string(DeliveryAccepted)
		}
		if got := resp.Header().Get("X-Anas-Delivery-Status"); got != want {
			t.Fatalf("attempt %d: status header = %q, want %q", attempt, got, want)
		}
	}
	pending, err := store.PendingEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("inbox holds %d events, want exactly 1", len(pending))
	}
}

// AGENT-R-011, AGENT-R-030: a repository nobody enabled is dropped before any
// handler sees it, and the drop is still a 202 so Forgejo does not retry.
func TestIngressDropsRepositoriesThatAreNotParticipating(t *testing.T) {
	ingress, store, lines := testIngress(t)
	body := payloadFor("someone-else/private", "alice", "hello")
	resp := post(t, ingress.Handler(), "delivery-2", Sign(testSecret, body), body)
	if resp.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusAccepted)
	}
	if got := resp.Header().Get("X-Anas-Delivery-Status"); got != string(DeliveryDropped) {
		t.Fatalf("status header = %q, want %q", got, DeliveryDropped)
	}
	pending, _ := store.PendingEvents(context.Background(), 10)
	if len(pending) != 0 {
		t.Fatalf("inbox holds %d event(s) for an unlisted repository", len(pending))
	}
	if len(*lines) == 0 || !strings.Contains((*lines)[0], "allowlist") {
		t.Fatalf("log = %v, want the drop explained", *lines)
	}
}

// AGENT-R-014: an event caused by this deployment's own agent account is
// dropped, so a reply cannot trigger another reply.
func TestIngressDropsItsOwnAgentsEvents(t *testing.T) {
	ingress, store, _ := testIngress(t)
	body := payloadFor("anas-project/ANAS", "agent-codex", "I have looked at this")
	resp := post(t, ingress.Handler(), "delivery-3", Sign(testSecret, body), body)
	if got := resp.Header().Get("X-Anas-Delivery-Status"); got != string(DeliverySelf) {
		t.Fatalf("status header = %q, want %q", got, DeliverySelf)
	}
	pending, _ := store.PendingEvents(context.Background(), 10)
	if len(pending) != 0 {
		t.Fatalf("inbox holds %d event(s) caused by our own agent", len(pending))
	}
}

// AGENT-R-014: the account check is not the only guard. A write made under a
// person's identity but carrying this orchestrator's run marker is also its
// own, and must not feed back in.
func TestIngressDropsEventsCarryingItsOwnRunMarker(t *testing.T) {
	ingress, store, _ := testIngress(t)
	ctx := context.Background()
	if _, err := store.ReserveWrite(ctx, OutboxWrite{
		Key: "k1", RunID: "run-42", Kind: "comment", Target: "anas-project/ANAS#7",
	}); err != nil {
		t.Fatalf("ReserveWrite: %v", err)
	}
	body := payloadFor("anas-project/ANAS", "alice", "summary "+RunMarker("run-42"))
	resp := post(t, ingress.Handler(), "delivery-4", Sign(testSecret, body), body)
	if got := resp.Header().Get("X-Anas-Delivery-Status"); got != string(DeliverySelf) {
		t.Fatalf("status header = %q, want %q", got, DeliverySelf)
	}
	pending, _ := store.PendingEvents(ctx, 10)
	if len(pending) != 0 {
		t.Fatalf("inbox holds %d event(s) that this orchestrator itself caused", len(pending))
	}
}

// A run marker from some other deployment is not ours, and must not be treated
// as a reason to drop a person's event.
func TestForeignRunMarkerIsNotTreatedAsOurOwn(t *testing.T) {
	ingress, store, _ := testIngress(t)
	body := payloadFor("anas-project/ANAS", "alice", "quoting "+RunMarker("run-from-elsewhere"))
	resp := post(t, ingress.Handler(), "delivery-5", Sign(testSecret, body), body)
	if got := resp.Header().Get("X-Anas-Delivery-Status"); got != string(DeliveryAccepted) {
		t.Fatalf("status header = %q, want %q", got, DeliveryAccepted)
	}
	pending, _ := store.PendingEvents(context.Background(), 10)
	if len(pending) != 1 {
		t.Fatalf("inbox holds %d event(s), want the person's event recorded", len(pending))
	}
}

func TestRunIDFromPayloadIgnoresUnterminatedMarkers(t *testing.T) {
	for name, payload := range map[string]string{
		"absent":       `{"issue":{"body":"nothing here"}}`,
		"unterminated": `{"issue":{"body":"<!-- anas-agent-run:run-9"}}`,
		"empty":        `{"issue":{"body":"<!-- anas-agent-run: -->"}}`,
		"not JSON":     `not json at all`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := RunIDFromPayload([]byte(payload)); got != "" {
				t.Fatalf("RunIDFromPayload = %q, want empty", got)
			}
		})
	}
	// The marker is found in a comment body as readily as in an issue body,
	// and it survives Forgejo's HTML-escaped encoding of the same text.
	escaped, err := json.Marshal(map[string]any{
		"comment": map[string]any{"body": "x " + RunMarker("run-9") + " y"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(escaped), `\u003c`) {
		t.Fatal("the fixture is not HTML-escaped, so it does not exercise the case Forgejo actually sends")
	}
	if got := RunIDFromPayload(escaped); got != "run-9" {
		t.Fatalf("RunIDFromPayload = %q, want run-9", got)
	}
}

func TestIngressRefusesNonPostAndOversizedBodies(t *testing.T) {
	ingress, _, _ := testIngress(t)
	handler := ingress.Handler()

	req := httptest.NewRequest(http.MethodGet, WebhookPath, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}

	oversized := strings.Repeat("x", maxPayload+1)
	resp := post(t, handler, "delivery-big", Sign(testSecret, []byte(oversized)), []byte(oversized))
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want %d", resp.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestParseRepoRejectsUnusableNames(t *testing.T) {
	for _, value := range []string{"", "owner", "owner/", "/name", "owner/name/extra", "../name", "owner/ name"} {
		if _, err := ParseRepo(value); err == nil {
			t.Fatalf("ParseRepo(%q) accepted an unusable repository", value)
		}
	}
	repo, err := ParseRepo("anas-project/ANAS")
	if err != nil || repo.Owner != "anas-project" || repo.Name != "ANAS" {
		t.Fatalf("ParseRepo = %+v, %v", repo, err)
	}
}

func TestIngressStampsReceiveTime(t *testing.T) {
	ingress, store, _ := testIngress(t)
	fixed := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	ingress.Now = func() time.Time { return fixed }
	body := payloadFor("anas-project/ANAS", "alice", "hello")
	post(t, ingress.Handler(), "delivery-6", Sign(testSecret, body), body)
	pending, _ := store.PendingEvents(context.Background(), 1)
	if len(pending) != 1 || !pending[0].ReceivedAt.Equal(fixed) {
		t.Fatalf("received_at = %v, want %v", pending, fixed)
	}
}
