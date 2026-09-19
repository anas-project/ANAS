package actionabi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func approvalFixture() ConfirmationBinding {
	return ConfirmationBinding{ABI: Version, PlanJobID: "plan-1", PlanInvocationID: "plan-call-1", Action: "incus.uninstall",
		WorkspaceID: "main", Actor: "local-owner", ParametersDigest: strings.Repeat("a", 64), StateDigest: strings.Repeat("b", 64),
		SummaryDigest: strings.Repeat("c", 64), ReleaseDigest: strings.Repeat("d", 64), PlannedAt: time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC)}
}

func TestConfirmationBindingBindsEveryApprovalDimension(t *testing.T) {
	b := approvalFixture()
	original, err := b.Digest()
	if err != nil { t.Fatal(err) }
	for _, change := range []func(*ConfirmationBinding){
		func(b *ConfirmationBinding) { b.PlanJobID += "x" }, func(b *ConfirmationBinding) { b.PlanInvocationID += "x" },
		func(b *ConfirmationBinding) { b.Action = "incus.install" }, func(b *ConfirmationBinding) { b.WorkspaceID = "other" },
		func(b *ConfirmationBinding) { b.Actor = "another-owner" }, func(b *ConfirmationBinding) { b.ParametersDigest = strings.Repeat("e",64) },
		func(b *ConfirmationBinding) { b.StateDigest = strings.Repeat("e",64) }, func(b *ConfirmationBinding) { b.SummaryDigest = strings.Repeat("e",64) },
		func(b *ConfirmationBinding) { b.ReleaseDigest = strings.Repeat("e",64) }, func(b *ConfirmationBinding) { b.PlannedAt = b.PlannedAt.Add(time.Second) },
	} {
		changed := b; change(&changed)
		digest, err := changed.Digest()
		if err != nil || digest == original { t.Fatal("approval dimension was not bound", err) }
	}
}

func TestConfirmationHasFixedFiveMinutePlanLifetime(t *testing.T) {
	b := approvalFixture()
	if ConfirmationTTL != 5*time.Minute || !b.ValidAt(b.PlannedAt) || !b.ValidAt(b.ExpiresAt().Add(-time.Nanosecond)) ||
		b.ValidAt(b.PlannedAt.Add(-time.Nanosecond)) || b.ValidAt(b.ExpiresAt()) || b.ValidAt(b.ExpiresAt().Add(time.Hour)) {
		t.Fatal("incorrect approval validity window")
	}
}

func TestConfirmationBindingStrictDecode(t *testing.T) {
	b := approvalFixture(); body, _ := json.Marshal(b)
	decoded, err := DecodeConfirmationBinding(body)
	if err != nil || decoded != b { t.Fatal("binding did not round trip", err) }
	for _, body := range []string{
		`null`, `{}`, strings.Replace(string(body), `"actor":`, `"Actor":`, 1),
		strings.Replace(string(body), `"actor":`, `"actor":"other","actor":`, 1),
		strings.Replace(string(body), `"local-owner"`, `null`, 1),
		strings.Replace(string(body), `"action":`, `"command":"private-marker","action":`, 1),
		string(body)+`{}`, strings.Replace(string(body), `"planned_at":"2026-09-19T00:00:00Z"`, `"planned_at":"2026-09-19T00:00:00+01:00"`,1),
	} {
		if _, err := DecodeConfirmationBinding([]byte(body)); err == nil || strings.Contains(err.Error(), "private-marker") { t.Fatal("unsafe confirmation accepted or exposed", err) }
	}
}
