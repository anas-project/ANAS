package consoleaudit

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolestate"
)

func TestAuthSinkMapsOnlyClosedNonCredentialFields(t *testing.T) {
	appender := &recordingAppender{}
	sink := AuthSink{Writer: appender, Actor: "direct-client"}
	timestamp := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	err := sink.Record(context.Background(), consoleauth.AuditEvent{
		Action:          consoleauth.AuditBootstrapIssue,
		Outcome:         consoleauth.AuditSuccess,
		OccurredAt:      timestamp,
		Reason:          "issued",
		TransactionID:   "bootstrap",
		Origin:          "http://192.0.2.20:8080",
		State:           consoleauth.StateBootstrap,
		ReplacedToken:   true,
		RevokedSessions: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(appender.events) != 1 {
		t.Fatalf("events = %d", len(appender.events))
	}
	event := appender.events[0]
	if event.Type != string(consoleauth.AuditBootstrapIssue) || event.Actor != "direct-client" || event.Timestamp != timestamp {
		t.Fatalf("event = %#v", event)
	}
	for key := range event.Details {
		if strings.Contains(key, "password") || strings.Contains(key, "csrf") || strings.Contains(key, "token_value") || strings.Contains(key, "session_token") {
			t.Fatalf("credential-shaped field crossed adapter: %q", key)
		}
	}
}

func TestAuthSinkFailsClosedWithoutAppender(t *testing.T) {
	if err := (AuthSink{}).Record(context.Background(), consoleauth.AuditEvent{}); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if err := (AuthSink{Writer: &recordingAppender{}}).Record(context.Background(), consoleauth.AuditEvent{}); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("empty actor error = %v", err)
	}
}

func TestStateSinkMapsOnlyClosedTransitionFields(t *testing.T) {
	appender := &recordingAppender{}
	sink := StateSink{Writer: appender}
	for _, event := range []consolestate.TransitionEvent{
		{From: consolestate.StateBootstrap, To: consolestate.StateEnrollment, Actor: "certificate-monitor", Reason: consolestate.ReasonBootstrapCompleted},
		{From: consolestate.StateEnrollment, To: consolestate.StateFull, Actor: "local-owner", Reason: consolestate.ReasonOwnerEnrolled},
	} {
		if err := sink.RecordTransition(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if len(appender.events) != 2 {
		t.Fatalf("events = %d", len(appender.events))
	}
	for index, event := range appender.events {
		if event.Type != "console_state.transition" || event.Outcome != "success" {
			t.Fatalf("event %d = %#v", index, event)
		}
		if len(event.Details) != 3 {
			t.Fatalf("event %d details = %#v", index, event.Details)
		}
		for key := range event.Details {
			for _, marker := range []string{"password", "token", "session", "csrf", "credential", "secret"} {
				if strings.Contains(strings.ToLower(key), marker) {
					t.Fatalf("credential-shaped state audit field %q", key)
				}
			}
		}
	}
}

func TestStateSinkRejectsInvalidOrUnavailableAudit(t *testing.T) {
	valid := consolestate.TransitionEvent{
		From: consolestate.StateBootstrap, To: consolestate.StateEnrollment,
		Actor: "certificate-monitor", Reason: consolestate.ReasonBootstrapCompleted,
	}
	if err := (StateSink{}).RecordTransition(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("nil writer error = %v", err)
	}
	for _, event := range []consolestate.TransitionEvent{
		{From: consolestate.StateBootstrap, To: consolestate.StateEnrollment, Reason: consolestate.ReasonBootstrapCompleted},
		{From: consolestate.StateBootstrap, To: consolestate.StateFull, Actor: "owner", Reason: consolestate.ReasonOwnerEnrolled},
		{From: consolestate.StateBootstrap, To: consolestate.StateEnrollment, Actor: "owner", Reason: consolestate.ReasonOwnerEnrolled},
	} {
		appender := &recordingAppender{}
		if err := (StateSink{Writer: appender}).RecordTransition(context.Background(), event); err == nil {
			t.Fatalf("invalid event succeeded: %#v", event)
		}
		if len(appender.events) != 0 {
			t.Fatalf("invalid event was appended: %#v", appender.events)
		}
	}
	want := errors.New("storage unavailable")
	if err := (StateSink{Writer: &recordingAppender{err: want}}).RecordTransition(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) || !errors.Is(err, want) {
		t.Fatalf("writer failure = %v", err)
	}
}

func TestRecordStateTransitionUsesClosedForwardSequence(t *testing.T) {
	appender := &recordingAppender{}
	if err := RecordStateTransition(context.Background(), appender, "direct-owner", consoleauth.StateBootstrap, consoleauth.StateEnrollment); err != nil {
		t.Fatal(err)
	}
	if err := RecordStateTransition(context.Background(), appender, "direct-owner", consoleauth.StateEnrollment, consoleauth.StateFull); err != nil {
		t.Fatal(err)
	}
	if len(appender.events) != 2 {
		t.Fatalf("events = %d", len(appender.events))
	}
	if got := appender.events[0].Details; got["from"] != "bootstrap" || got["to"] != "enrollment" || got["reason"] != "bootstrap_completed" {
		t.Fatalf("bootstrap transition = %#v", got)
	}
	if got := appender.events[1].Details; got["from"] != "enrollment" || got["to"] != "full" || got["reason"] != "owner_enrolled" {
		t.Fatalf("owner transition = %#v", got)
	}
	if err := RecordStateTransition(context.Background(), appender, "direct-owner", consoleauth.StateFull, consoleauth.StateBootstrap); err == nil {
		t.Fatal("reverse transition unexpectedly accepted")
	}
	if len(appender.events) != 2 {
		t.Fatalf("invalid transition appended an event: %d", len(appender.events))
	}
}

func TestRecordStateTransitionFailsClosed(t *testing.T) {
	want := errors.New("storage unavailable")
	appender := &recordingAppender{err: want}
	err := RecordStateTransition(context.Background(), appender, "direct-owner", consoleauth.StateBootstrap, consoleauth.StateEnrollment)
	if !errors.Is(err, audit.ErrUnavailable) || !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if err := RecordStateTransition(context.Background(), nil, "direct-owner", consoleauth.StateBootstrap, consoleauth.StateEnrollment); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("nil writer error = %v", err)
	}
	if err := RecordStateTransition(context.Background(), &recordingAppender{}, "", consoleauth.StateBootstrap, consoleauth.StateEnrollment); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("empty actor error = %v", err)
	}
}

type recordingAppender struct {
	events []audit.Event
	err    error
}

func (appender *recordingAppender) AppendContext(_ context.Context, event audit.Event) (audit.Event, error) {
	if appender.err != nil {
		return audit.Event{}, appender.err
	}
	appender.events = append(appender.events, event)
	return event, nil
}
