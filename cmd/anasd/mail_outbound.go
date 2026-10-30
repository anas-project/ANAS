package main

import (
	"context"
	"errors"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/outbound"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

type mailOutboundAudit struct {
	call   httpapi.ServiceMailOutboundCall
	writer consoleaudit.Appender
}

func (a mailOutboundAudit) write(ctx context.Context, action, operation string) error {
	if a.writer == nil || a.call.Reauthorize == nil || a.call.Reauthorize(ctx) != nil {
		return audit.ErrUnavailable
	}
	_, err := a.writer.AppendContext(ctx, audit.Event{Type: action, Actor: "service:" + a.call.ClientID, WorkspaceID: a.call.WorkspaceID, Outcome: "authorized", Details: map[string]any{"actor_source": "service_declared", "declared_actor": a.call.Request.Actor, "operation_id": operation, "application": "mail"}})
	return err
}

func (a mailOutboundAudit) BeforeConfigCommit(ctx context.Context, intent application.ConfigCommitIntent) error {
	return a.write(ctx, "service.mail.outbound.config_commit", intent.OperationID)
}

// The caller must authenticate the service and authorize the current user.
// The host does not accept paths or full routing documents from the service.
// This handler saves configuration only; it does not restart DMS or touch the
// Postfix queue. The listener independently enforces the service grant.
func newMailOutboundHandler(writer consoleaudit.Appender) func(context.Context, httpapi.ServiceMailOutboundCall) (httpapi.ServiceMailOutboundResult, error) {
	return func(ctx context.Context, call httpapi.ServiceMailOutboundCall) (httpapi.ServiceMailOutboundResult, error) {
		if call.Reauthorize == nil || call.Reauthorize(ctx) != nil || !mailInstallOperation.MatchString(call.Request.OperationID) || call.ClientID == "" || call.WorkspaceID == "" || call.Request.Actor == "" {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundUnknown
		}
		auditor := mailOutboundAudit{call: call, writer: writer}
		if auditor.write(ctx, "service.mail.outbound.read", call.Request.OperationID) != nil {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundUnknown
		}
		snapshot, err := runner.ReadMailOutboundSnapshot(ctx, call.WorkspacePath)
		if err != nil {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundUnknown
		}
		previous := outbound.Previous{Routing: snapshot.Routing, Passwords: snapshot.Passwords, Validator: snapshot.Validator}
		request := call.Request
		profile := smtpconfig.Profile{Provider: request.Provider, Host: request.Host, Port: request.Port, TLS: request.TLS, Username: request.Username, Password: request.Password}
		desired, err := outbound.DefaultConfig(previous, profile)
		if err != nil {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundConflict
		}
		err = outbound.Commit(ctx, runner.NewWorkspaceConfigService(call.WorkspacePath), auditor, request.OperationID, previous, desired)
		if errors.Is(err, outbound.ErrConflict) || errors.Is(err, outbound.ErrInvalid) {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundConflict
		}
		if err != nil {
			return httpapi.ServiceMailOutboundResult{}, httpapi.ErrMailOutboundUnknown
		}
		return httpapi.ServiceMailOutboundResult{State: "configured"}, nil
	}
}
