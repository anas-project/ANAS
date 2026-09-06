package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// TestPostgresStore exercises the authoritative store against a real
// PostgreSQL. It is opt-in because the unit suite must run without a database,
// but the semantics AGENT-R-003 and AGENT-R-012 rest on -- the primary-key
// dedupe, the idempotency reservation, surviving a reconnect -- are properties
// of the database, not of the Go code, so they are worth asserting where one
// exists.
//
//	AI_AGENT_TEST_DSN=postgres://user:pass@localhost:5432/ai_agent_test?sslmode=disable \
//	  go test ./modules/ai_agent/orchestrator -run TestPostgresStore
func TestPostgresStore(t *testing.T) {
	dsn := os.Getenv("AI_AGENT_TEST_DSN")
	if dsn == "" {
		t.Skip("set AI_AGENT_TEST_DSN to run the PostgreSQL store tests")
	}
	ctx := context.Background()
	redactor := NewRedactor(dsn)
	store, err := OpenPostgres(ctx, dsn, redactor)
	if err != nil {
		t.Fatalf("OpenPostgres: %v", err)
	}
	t.Cleanup(store.Close)
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	// Applying the schema twice must converge, because it runs at every start.
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	for _, table := range []string{"audit_record", "reconcile_cursor", "webhook_registration",
		"agent_identity", "outbox_write", "inbox_event"} {
		if _, err := store.pool.Exec(ctx, "DELETE FROM "+table); err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}

	repo := testRepo(t)
	event := InboxEvent{
		DeliveryID: "d-1", Event: "issues", Repo: repo, Sender: "alice",
		Payload: []byte(`{"issue":{"number":1}}`), ReceivedAt: time.Now().UTC(), Source: "webhook",
	}
	fresh, err := store.RecordDelivery(ctx, event)
	if err != nil || !fresh {
		t.Fatalf("RecordDelivery = %v, %v", fresh, err)
	}
	fresh, err = store.RecordDelivery(ctx, event)
	if err != nil || fresh {
		t.Fatalf("repeat RecordDelivery = %v, %v; want it reported as a duplicate", fresh, err)
	}
	pending, err := store.PendingEvents(ctx, 10)
	if err != nil || len(pending) != 1 || pending[0].Repo != repo {
		t.Fatalf("PendingEvents = %+v, %v", pending, err)
	}
	if err := store.MarkProcessed(ctx, "d-1"); err != nil {
		t.Fatalf("MarkProcessed: %v", err)
	}
	if pending, _ = store.PendingEvents(ctx, 10); len(pending) != 0 {
		t.Fatalf("PendingEvents after processing = %+v, want none", pending)
	}

	write := OutboxWrite{Key: "k-1", RunID: "run-1", Kind: "comment", Target: "anas-project/ANAS#1"}
	if claimed, err := store.ReserveWrite(ctx, write); err != nil || !claimed {
		t.Fatalf("ReserveWrite = %v, %v", claimed, err)
	}
	if claimed, err := store.ReserveWrite(ctx, write); err != nil || claimed {
		t.Fatalf("repeat ReserveWrite = %v, %v; want the key already held", claimed, err)
	}
	if err := store.CompleteWrite(ctx, "k-1"); err != nil {
		t.Fatalf("CompleteWrite: %v", err)
	}
	if mine, err := store.WriteByRunID(ctx, "run-1"); err != nil || !mine {
		t.Fatalf("WriteByRunID = %v, %v", mine, err)
	}

	identity := AgentIdentity{
		Runtime: "r", Account: "agent-r", ForgejoUserID: 1, TokenID: 2,
		TokenFingerprint: "f", SSHKeyID: 3, Generation: 1, RotatedAt: time.Now().UTC(),
	}
	if err := store.SaveIdentity(ctx, identity); err != nil {
		t.Fatalf("SaveIdentity: %v", err)
	}
	identity.Generation, identity.TokenID = 2, 4
	if err := store.SaveIdentity(ctx, identity); err != nil {
		t.Fatalf("SaveIdentity upsert: %v", err)
	}
	identities, err := store.Identities(ctx)
	if err != nil || len(identities) != 1 || identities[0].Generation != 2 {
		t.Fatalf("Identities = %+v, %v; want one row upserted to generation 2", identities, err)
	}

	if _, err := store.Webhook(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Webhook on an empty table = %v, want ErrNotFound", err)
	}
	registration := WebhookRegistration{
		HookID: 3, URL: "https://agent.example" + WebhookPath,
		SecretFingerprint: "abc", Events: subscribedEvents,
	}
	if err := store.SaveWebhook(ctx, registration); err != nil {
		t.Fatalf("SaveWebhook: %v", err)
	}
	registration.SecretFingerprint = "def"
	if err := store.SaveWebhook(ctx, registration); err != nil {
		t.Fatalf("SaveWebhook upsert: %v", err)
	}
	stored, err := store.Webhook(ctx)
	if err != nil || stored.SecretFingerprint != "def" || len(stored.Events) != len(subscribedEvents) {
		t.Fatalf("Webhook = %+v, %v", stored, err)
	}

	if cursor, err := store.Cursor(ctx, repo); err != nil || !cursor.IsZero() {
		t.Fatalf("Cursor on a fresh repository = %v, %v; want the zero time", cursor, err)
	}
	at := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	if err := store.SetCursor(ctx, repo, at); err != nil {
		t.Fatalf("SetCursor: %v", err)
	}
	if cursor, err := store.Cursor(ctx, repo); err != nil || !cursor.Equal(at) {
		t.Fatalf("Cursor = %v, %v, want %v", cursor, err, at)
	}

	// The reason is the one audit field built from upstream text, so it is
	// scrubbed on the way in.
	if err := store.AppendAudit(ctx, AuditRecord{
		Subject: "alice", Source: "label", Action: "execute", Decision: "denied",
		Reason: "refused while holding " + dsn, Repo: repo.String(), Issue: 1,
	}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	records, err := store.Audit(ctx, 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("Audit = %+v, %v", records, err)
	}
	if records[0].Reason == "refused while holding "+dsn {
		t.Fatal("the audit reason was stored with the credential still in it")
	}
}
