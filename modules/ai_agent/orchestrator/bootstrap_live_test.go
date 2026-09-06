package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestM1AgainstLiveDependencies is the milestone's end-to-end at the level the
// control plane can be held to without an Incus host: real Forgejo, real
// PostgreSQL, and the actual bootstrap, webhook and ingress paths rather than
// fakes of them. It covers AGENT-R-003, R-005, R-006, R-007, R-009, R-011,
// R-012 and R-014 in one pass.
//
//	AI_AGENT_TEST_DSN=... AI_AGENT_TEST_FORGEJO_URL=... AI_AGENT_TEST_FORGEJO_USER=... \
//	AI_AGENT_TEST_FORGEJO_PASSWORD=... AI_AGENT_TEST_FORGEJO_ORG=... \
//	AI_AGENT_TEST_FORGEJO_REPO_IN=... AI_AGENT_TEST_FORGEJO_REPO_OUT=... \
//	  go test ./modules/ai_agent/orchestrator -run TestM1AgainstLiveDependencies
func TestM1AgainstLiveDependencies(t *testing.T) {
	dsn := os.Getenv("AI_AGENT_TEST_DSN")
	baseURL := os.Getenv("AI_AGENT_TEST_FORGEJO_URL")
	username := os.Getenv("AI_AGENT_TEST_FORGEJO_USER")
	password := os.Getenv("AI_AGENT_TEST_FORGEJO_PASSWORD")
	org := os.Getenv("AI_AGENT_TEST_FORGEJO_ORG")
	repoName := os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_IN")
	if dsn == "" || baseURL == "" || username == "" || password == "" || org == "" || repoName == "" {
		t.Skip("set AI_AGENT_TEST_DSN and the AI_AGENT_TEST_FORGEJO_* variables to run the M1 live test")
	}
	ctx := context.Background()
	repo, err := ParseRepo(org + "/" + repoName)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}

	redactor := NewRedactor(password, dsn, testSecret)
	store, err := OpenPostgres(ctx, dsn, redactor)
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for _, table := range []string{"audit_record", "reconcile_cursor", "webhook_registration",
		"agent_identity", "outbox_write", "inbox_event"} {
		if _, err := store.pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}

	suffix := strconv.FormatInt(time.Now().Unix(), 10)
	registry, err := NewRegistry([]string{"codex"}, map[string]string{"codex": strings.Repeat("a", 64)})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	cfg := Config{
		Enabled: true, WebhookSecret: testSecret, Runtimes: registry,
		RepositoryAllow: []Repo{repo},
		WebhookURL:      "https://agent.invalid/live-" + suffix + WebhookPath,
	}
	admin := NewForgejoAdmin(baseURL, username, password, redactor)
	logf := func(string) {}
	bootstrapper := &Bootstrapper{
		Config: cfg, Admin: admin, Store: store, Redactor: redactor,
		Scoping: ScopingRepositories, Log: logf,
	}

	// The registry's account name is shared across runs, so the scratch account
	// is removed at the end rather than renamed -- the point is that the real
	// account name works.
	t.Cleanup(func() {
		identities, err := store.Identities(context.Background())
		if err != nil {
			return
		}
		for _, identity := range identities {
			_ = admin.DeleteToken(context.Background(), identity.Account, identity.TokenID)
			_ = admin.DeleteKey(context.Background(), identity.Account, identity.SSHKeyID)
		}
		if registration, err := store.Webhook(context.Background()); err == nil {
			_ = admin.DeleteSystemHook(context.Background(), registration.HookID)
		}
	})

	// AGENT-R-005, R-006: unattended identity, scoped token, own SSH key.
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	identities, err := store.Identities(ctx)
	if err != nil || len(identities) != 1 {
		t.Fatalf("identities = %+v, %v; want one per runtime", identities, err)
	}
	provisioned := identities[0]
	if provisioned.TokenID == 0 || provisioned.SSHKeyID == 0 || provisioned.ForgejoUserID == 0 {
		t.Fatalf("identity = %+v, want a Forgejo user, a token and a key", provisioned)
	}

	// Running again must not mint a second credential.
	if err := bootstrapper.Reconcile(ctx); err != nil {
		t.Fatalf("second Reconcile: %v", err)
	}
	unchanged, _ := store.Identities(ctx)
	if unchanged[0].TokenID != provisioned.TokenID {
		t.Fatal("a second reconciliation replaced a credential that was still fresh")
	}
	tokens, err := admin.ListTokens(ctx, provisioned.Account)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	managed := 0
	for _, token := range tokens {
		if ManagedToken(token.Name) {
			managed++
		}
	}
	if managed != 1 {
		t.Fatalf("the account holds %d managed tokens, want exactly 1", managed)
	}

	// AGENT-R-007: rotation replaces both credentials and leaves one of each.
	if err := bootstrapper.Rotate(ctx, unchanged[0]); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	rotated, _ := store.Identities(ctx)
	if rotated[0].TokenID == provisioned.TokenID || rotated[0].Generation != provisioned.Generation+1 {
		t.Fatalf("identity = %+v, want a new token at the next generation", rotated[0])
	}
	tokens, err = admin.ListTokens(ctx, provisioned.Account)
	if err != nil {
		t.Fatalf("ListTokens after rotation: %v", err)
	}
	managed = 0
	for _, token := range tokens {
		if ManagedToken(token.Name) {
			managed++
		}
	}
	if managed != 1 {
		t.Fatalf("after rotation the account holds %d managed tokens; a rotation must never leave two live", managed)
	}

	// AGENT-R-009: the hook is registered, is stable across sweeps, and follows
	// a key rotation.
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook: %v", err)
	}
	registration, err := store.Webhook(ctx)
	if err != nil {
		t.Fatalf("Webhook: %v", err)
	}
	for sweep := 0; sweep < 3; sweep++ {
		if err := bootstrapper.EnsureWebhook(ctx); err != nil {
			t.Fatalf("sweep %d: %v", sweep, err)
		}
	}
	stable, _ := store.Webhook(ctx)
	if stable.HookID != registration.HookID {
		t.Fatalf("the hook id changed from %d to %d across unchanged sweeps", registration.HookID, stable.HookID)
	}
	bootstrapper.Config.WebhookSecret = "rotated-" + testSecret
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		t.Fatalf("EnsureWebhook after a key rotation: %v", err)
	}
	afterRotation, _ := store.Webhook(ctx)
	if afterRotation.HookID != registration.HookID {
		t.Fatal("a key rotation created a second hook instead of re-registering the existing one")
	}
	if afterRotation.SecretFingerprint == registration.SecretFingerprint {
		t.Fatal("the recorded fingerprint did not follow the key rotation")
	}
	bootstrapper.Config.WebhookSecret = testSecret

	// AGENT-R-011, R-012, R-014 against the real store.
	ingress := &Ingress{Config: cfg, Store: store, Redactor: redactor, Log: logf}
	handler := ingress.Handler()
	body := payloadFor(repo.String(), "alice", "please look at this")
	signature := Sign(testSecret, body)
	for attempt := 0; attempt < 10; attempt++ {
		resp := post(t, handler, "live-delivery-"+suffix, signature, body)
		if resp.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: status = %d", attempt, resp.Code)
		}
	}
	pending, err := store.PendingEvents(ctx, 10)
	if err != nil {
		t.Fatalf("PendingEvents: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("inbox holds %d events after ten identical deliveries, want 1", len(pending))
	}

	// An event from the agent's own account is dropped.
	own := payloadFor(repo.String(), provisioned.Account, "I have looked at this")
	resp := post(t, handler, "live-self-"+suffix, Sign(testSecret, own), own)
	if got := resp.Header().Get("X-Anas-Delivery-Status"); got != string(DeliverySelf) {
		t.Fatalf("self-triggered delivery status = %q, want %q", got, DeliverySelf)
	}

	// AGENT-R-003: a fresh process reading the same database sees the same
	// state. This is the restart the requirement asks about, minus the
	// container.
	reopened, err := OpenPostgres(ctx, dsn, redactor)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	after, err := reopened.Identities(ctx)
	if err != nil || len(after) != 1 || after[0].TokenID != rotated[0].TokenID {
		t.Fatalf("identities after reopening = %+v, %v; want the rotated identity", after, err)
	}
	if _, err := reopened.Webhook(ctx); err != nil {
		t.Fatalf("webhook registration did not survive: %v", err)
	}
	stillPending, _ := reopened.PendingEvents(ctx, 10)
	if len(stillPending) != 1 {
		t.Fatalf("the inbox lost its event across a reconnect: %+v", stillPending)
	}

	// AGENT-R-013: the sweep reads the real repository and recovers what the
	// webhook never delivered, without duplicating on a second pass.
	reconciler := &Reconciler{Config: cfg, Admin: admin, Store: store, Ingress: ingress, Log: logf}
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	firstPass, _ := store.PendingEvents(ctx, 100)
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	secondPass, _ := store.PendingEvents(ctx, 100)
	if len(secondPass) != len(firstPass) {
		t.Fatalf("a repeated sweep grew the inbox from %d to %d", len(firstPass), len(secondPass))
	}
}

// The live ingress assertions reuse the unit helpers; this keeps the compiler
// honest about them being shared rather than duplicated.
var _ = httptest.NewRequest
var _ = json.Marshal
