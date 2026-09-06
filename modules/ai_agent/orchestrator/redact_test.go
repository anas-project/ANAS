package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// AGENT-R-010: a credential must not reach a log line, an issue, or a stored
// record. The scrubber is the enforcement, so it is asserted on the shapes that
// actually leak: a transport error quoting a URL, and free text.
func TestRedactorRemovesSecretsFromEveryOutboundShape(t *testing.T) {
	const token = "forgejo-token-0123456789"
	const password = "an-administrator-password"
	redactor := NewRedactor(token, password)

	line := redactor.String("minted " + token + " for agent-x")
	if strings.Contains(line, token) {
		t.Fatalf("log line still holds the token: %q", line)
	}
	if !strings.Contains(line, Placeholder) {
		t.Fatalf("log line = %q, want the redaction visible", line)
	}

	wrapped := fmt.Errorf("Get %q: refused", "https://user:"+password+"@git.example")
	scrubbed := redactor.Error(wrapped)
	if strings.Contains(scrubbed, password) {
		t.Fatalf("error still holds the password: %q", scrubbed)
	}
}

// A token minted after start-up is registered as soon as it exists, so the very
// first line that mentions it is already scrubbed.
func TestRedactorCoversCredentialsMintedLater(t *testing.T) {
	redactor := NewRedactor()
	const token = "a-token-issued-at-runtime"
	if got := redactor.String(token); got != token {
		t.Fatalf("an unregistered value was already altered: %q", got)
	}
	redactor.Add(token)
	if got := redactor.String("using " + token); strings.Contains(got, token) {
		t.Fatalf("a registered value survived: %q", got)
	}
}

// Very short values are deliberately not registered: blanking every occurrence
// of a two-character string would destroy the record without protecting a
// secret that short.
func TestRedactorIgnoresValuesTooShortToProtect(t *testing.T) {
	redactor := NewRedactor("ab")
	if got := redactor.String("a table of ab values"); got != "a table of ab values" {
		t.Fatalf("a two-character value was scrubbed out of unrelated text: %q", got)
	}
}

func TestRedactorHandlesNilAndEmpty(t *testing.T) {
	redactor := NewRedactor("a-real-secret-value")
	if got := redactor.Error(nil); got != "" {
		t.Fatalf("Error(nil) = %q, want empty", got)
	}
	if got := redactor.String(""); got != "" {
		t.Fatalf("String(\"\") = %q, want empty", got)
	}
}

// AGENT-R-034 with AGENT-R-010: a refusal is recorded, and the reason -- the
// one audit field built from upstream text -- is scrubbed on the way in.
func TestAuditRecordsRefusalsWithTheirReason(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if err := store.AppendAudit(ctx, AuditRecord{
		Subject: "alice", Source: "label", Action: "execute",
		Decision: "denied", Reason: "no execute grant", Repo: "anas-project/ANAS", Issue: 12,
	}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	records, err := store.Audit(ctx, 10)
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if len(records) != 1 || records[0].Decision != "denied" {
		t.Fatalf("audit = %+v, want the refusal recorded", records)
	}
	if records[0].At.IsZero() {
		t.Fatal("the audit record has no timestamp")
	}
}

func TestStoreReportsMissingRowsAsNotFound(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	if _, err := store.Webhook(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Webhook on an empty store = %v, want ErrNotFound", err)
	}
	if err := store.MarkProcessed(ctx, "nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkProcessed for an unknown delivery = %v, want ErrNotFound", err)
	}
	if err := store.CompleteWrite(ctx, "nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CompleteWrite for an unclaimed key = %v, want ErrNotFound", err)
	}
}
