package main

import (
	"context"

	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/jobexecutor"
)

type workspaceJobAuthority interface {
	CheckJobOwner(context.Context, string, string, string) error
	CurrentBootstrapTransaction(context.Context, consoleauth.ConsoleState) (string, error)
}

// Revalidate the durable actor rather than retaining an HTTP request/session.
// Called under jobs.lock: only independent auth/state reads are allowed.
// Transaction-scoped bootstrap/enrollment applies keep their original narrow
// authority; they never acquire the permissions of the later full owner.
func workspaceJobAuthorizer(authority workspaceJobAuthority, current func(context.Context) (consoleauth.ConsoleState, error), issuer, group string) func(context.Context, consolejobs.Job) error {
	return func(ctx context.Context, job consolejobs.Job) error {
		if ctx == nil || ctx.Err() != nil || authority == nil || current == nil {
			return consoleauth.ErrSessionUnauthorized
		}
		state, err := current(ctx)
		if err != nil {
			return consoleauth.ErrSessionUnauthorized
		}
		kind, transaction, scoped := consolejobs.ParseTransactionPrincipal(job.CreatedBy)
		if !scoped {
			return authority.CheckJobOwner(ctx, job.CreatedBy, issuer, group)
		}
		expected := consoleauth.StateBootstrap
		if kind == consolejobs.PrincipalEnrollment {
			expected = consoleauth.StateEnrollment
		}
		if state != expected || job.Kind != jobexecutor.KindDeploymentApply || job.Action != nil || !job.Mutating ||
			job.Request[consolejobs.ConfirmationTransactionRequestKey] != transaction ||
			job.Request[consolejobs.ConfirmationIdentitySourceRequestKey] != string(kind) ||
			job.Request[consolejobs.ConfirmationActionRequestKey] != jobexecutor.KindDeploymentApply {
			return consoleauth.ErrSessionUnauthorized
		}
		active, err := authority.CurrentBootstrapTransaction(ctx, expected)
		if err != nil || active != transaction {
			return consoleauth.ErrSessionUnauthorized
		}
		after, err := current(ctx)
		if err != nil || after != expected {
			return consoleauth.ErrSessionUnauthorized
		}
		return ctx.Err()
	}
}
