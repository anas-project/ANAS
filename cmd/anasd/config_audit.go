package main

import (
	"context"
	"encoding/hex"
	"errors"
	"log"
	"strings"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
)

type configAuditSink struct {
	writer consoleaudit.Appender
	logger *log.Logger
}

func (sink configAuditSink) RecordConfigEvent(ctx context.Context, event httpapi.ConfigAuditEvent) error {
	if sink.writer == nil || !validConfigAuditOperationID(event.OperationID) || event.Actor == "" || event.WorkspaceID == "" {
		sink.reportFailure(event, audit.ErrUnavailable)
		return audit.ErrUnavailable
	}
	switch event.Stage {
	case httpapi.ConfigAuditAttempt, httpapi.ConfigAuditAuthorized, httpapi.ConfigAuditSuccess, httpapi.ConfigAuditFailure, httpapi.ConfigAuditIndeterminate:
	default:
		return errors.New("invalid config audit stage")
	}
	details := map[string]any{"stage": event.Stage, "operation_id": event.OperationID}
	if event.CurrentValidator != "" {
		details["current_validator"] = event.CurrentValidator
	}
	if event.CandidateValidator != "" {
		details["candidate_validator"] = event.CandidateValidator
	}
	if event.FailureCode != "" {
		details["failure_code"] = event.FailureCode
	}
	if len(event.Changes) != 0 {
		changes := make([]map[string]any, 0, len(event.Changes))
		for _, change := range event.Changes {
			item := map[string]any{
				"path": change.Path, "change": change.Change, "effect": change.Effect,
				"sensitive": change.Sensitive, "editable": change.Editable,
			}
			if change.Apply != "" {
				item["apply"] = change.Apply
			}
			changes = append(changes, item)
		}
		details["changes"] = changes
	}
	_, err := sink.writer.AppendContext(ctx, audit.Event{
		Type: "workspace.config.put", Actor: event.Actor, WorkspaceID: event.WorkspaceID,
		Outcome: event.Stage, Details: details,
	})
	if err != nil {
		failure := errors.Join(audit.ErrUnavailable, err)
		sink.reportFailure(event, failure)
		return failure
	}
	return nil
}

func validConfigAuditOperationID(value string) bool {
	if len(value) != len("cfg-")+32 || !strings.HasPrefix(value, "cfg-") {
		return false
	}
	_, err := hex.DecodeString(value[len("cfg-"):])
	return err == nil
}

func (sink configAuditSink) reportFailure(event httpapi.ConfigAuditEvent, err error) {
	if sink.logger != nil {
		sink.logger.Printf("config audit append failed: operation_id=%q stage=%q workspace=%q: %v",
			event.OperationID, event.Stage, event.WorkspaceID, err)
	}
}
