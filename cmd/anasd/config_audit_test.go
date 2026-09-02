package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/audit"
)

const testConfigAuditOperationID = "cfg-0123456789abcdef0123456789abcdef"

type recordingConfigAuditAppender struct {
	events []audit.Event
	err    error
}

func (appender *recordingConfigAuditAppender) AppendContext(_ context.Context, event audit.Event) (audit.Event, error) {
	if appender.err != nil {
		return audit.Event{}, appender.err
	}
	appender.events = append(appender.events, event)
	return event, nil
}

func TestConfigAuditSinkMapsOnlyClosedValueFreeFields(t *testing.T) {
	appender := &recordingConfigAuditAppender{}
	event := httpapi.ConfigAuditEvent{
		Stage: httpapi.ConfigAuditAuthorized, OperationID: testConfigAuditOperationID,
		Actor: "local-owner", WorkspaceID: "main",
		CurrentValidator: "cfgv-old", CandidateValidator: "cfgv-new",
		Changes: []httpapi.ConfigAuditChange{{
			Path: "modules.demo.password", Change: "change", Effect: "runtime",
			Sensitive: true, Editable: true,
		}},
	}
	if err := (configAuditSink{writer: appender}).RecordConfigEvent(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if len(appender.events) != 1 {
		t.Fatalf("events = %d", len(appender.events))
	}
	persisted := appender.events[0]
	if persisted.Type != "workspace.config.put" || persisted.Actor != "local-owner" || persisted.WorkspaceID != "main" || persisted.Outcome != httpapi.ConfigAuditAuthorized {
		t.Fatalf("event = %#v", persisted)
	}
	if persisted.Details["operation_id"] != testConfigAuditOperationID {
		t.Fatalf("operation correlation = %#v", persisted.Details)
	}
	if persisted.Details["current_validator"] != "cfgv-old" || persisted.Details["candidate_validator"] != "cfgv-new" {
		t.Fatalf("audit validators = %#v", persisted.Details)
	}
	serialized := fmt.Sprintf("%#v", persisted)
	for _, forbidden := range []string{"request_body", "candidate_body", "current_digest", "candidate_digest", "secret-value"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("audit event exposed %q: %s", forbidden, serialized)
		}
	}
}

func TestConfigAuditSinkFailsClosed(t *testing.T) {
	valid := httpapi.ConfigAuditEvent{
		Stage: httpapi.ConfigAuditAttempt, OperationID: testConfigAuditOperationID, Actor: "actor", WorkspaceID: "main",
	}
	if err := (configAuditSink{}).RecordConfigEvent(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) {
		t.Fatalf("nil writer error = %v", err)
	}
	want := errors.New("disk unavailable")
	if err := (configAuditSink{writer: &recordingConfigAuditAppender{err: want}}).RecordConfigEvent(context.Background(), valid); !errors.Is(err, audit.ErrUnavailable) || !errors.Is(err, want) {
		t.Fatalf("writer error = %v", err)
	}
	invalid := valid
	invalid.Stage = "invented"
	if err := (configAuditSink{writer: &recordingConfigAuditAppender{}}).RecordConfigEvent(context.Background(), invalid); err == nil {
		t.Fatal("invalid stage was accepted")
	}
}

func TestConfigAuditSinkLogsAppendFailureWithoutValues(t *testing.T) {
	var output bytes.Buffer
	want := errors.New("disk unavailable")
	event := httpapi.ConfigAuditEvent{
		Stage: httpapi.ConfigAuditIndeterminate, OperationID: testConfigAuditOperationID,
		Actor: "actor", WorkspaceID: "main", FailureCode: "config_recovery_required",
	}
	err := (configAuditSink{
		writer: &recordingConfigAuditAppender{err: want}, logger: log.New(&output, "", 0),
	}).RecordConfigEvent(context.Background(), event)
	if !errors.Is(err, want) {
		t.Fatalf("append error = %v", err)
	}
	logged := output.String()
	if !strings.Contains(logged, testConfigAuditOperationID) || !strings.Contains(logged, "indeterminate") || strings.Contains(logged, "candidate") {
		t.Fatalf("audit failure log = %q", logged)
	}
}
