// Package consoleaudit adapts security-domain events to the append-only audit
// store without carrying credential values across the boundary.
package consoleaudit

import (
	"context"
	"errors"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolestate"
)

type Appender interface {
	AppendContext(context.Context, audit.Event) (audit.Event, error)
}

type AuthSink struct {
	Writer Appender
	Actor  string
}

// StateSink durably maps the closed consolestate transition event into the
// shared append-only audit log. It rejects any from/to/reason combination that
// was not selected by the monotonic state store.
type StateSink struct {
	Writer Appender
}

func (sink StateSink) RecordTransition(ctx context.Context, event consolestate.TransitionEvent) error {
	if sink.Writer == nil {
		return audit.ErrUnavailable
	}
	if event.Actor == "" {
		return audit.ErrUnavailable
	}
	valid := event.From == consolestate.StateBootstrap && event.To == consolestate.StateEnrollment && event.Reason == consolestate.ReasonBootstrapCompleted ||
		event.From == consolestate.StateEnrollment && event.To == consolestate.StateFull && event.Reason == consolestate.ReasonOwnerEnrolled
	if !valid {
		return errors.New("invalid console state transition audit event")
	}
	_, err := sink.Writer.AppendContext(ctx, audit.Event{
		Type:    "console_state.transition",
		Actor:   event.Actor,
		Outcome: "success",
		Details: map[string]any{
			"from":   string(event.From),
			"to":     string(event.To),
			"reason": string(event.Reason),
		},
	})
	if err != nil {
		return errors.Join(audit.ErrUnavailable, err)
	}
	return nil
}

func (sink AuthSink) Record(ctx context.Context, event consoleauth.AuditEvent) error {
	if sink.Writer == nil || sink.Actor == "" {
		return audit.ErrUnavailable
	}
	details := map[string]any{
		"reason":           event.Reason,
		"transaction_id":   event.TransactionID,
		"origin":           event.Origin,
		"target_origin":    event.TargetOrigin,
		"state":            string(event.State),
		"replaced_token":   event.ReplacedToken,
		"revoked_sessions": event.RevokedSessions,
	}
	_, err := sink.Writer.AppendContext(ctx, audit.Event{
		Timestamp: event.OccurredAt,
		Type:      string(event.Action),
		Actor:     sink.Actor,
		Outcome:   string(event.Outcome),
		Details:   details,
	})
	if err != nil {
		return errors.Join(audit.ErrUnavailable, err)
	}
	return nil
}

// RecordStateTransition is the compatibility form of StateSink for callers
// that still use consoleauth state values. New capability-state composition
// should pass the closed consolestate event directly to StateSink.
func RecordStateTransition(ctx context.Context, writer Appender, actor string, from, to consoleauth.ConsoleState) error {
	var reason consolestate.TransitionReason
	switch {
	case from == consoleauth.StateBootstrap && to == consoleauth.StateEnrollment:
		reason = consolestate.ReasonBootstrapCompleted
	case from == consoleauth.StateEnrollment && to == consoleauth.StateFull:
		reason = consolestate.ReasonOwnerEnrolled
	default:
		return errors.New("invalid console state transition")
	}
	return (StateSink{Writer: writer}).RecordTransition(ctx, consolestate.TransitionEvent{
		From: consolestate.State(from), To: consolestate.State(to), Actor: actor, Reason: reason,
	})
}
