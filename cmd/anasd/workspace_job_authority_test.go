package main

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/jobexecutor"
)

type workspaceAuthorityFixture struct {
	ownerErr             error
	transaction          string
	owners, transactions int
}

func (f *workspaceAuthorityFixture) CheckJobOwner(context.Context, string, string, string) error {
	f.owners++
	return f.ownerErr
}
func (f *workspaceAuthorityFixture) CurrentBootstrapTransaction(context.Context, consoleauth.ConsoleState) (string, error) {
	f.transactions++
	return f.transaction, nil
}

func TestWorkspaceJobAuthorityRechecksOwnerAndKeepsBootstrapNarrow(t *testing.T) {
	state := consoleauth.StateFull
	authority := &workspaceAuthorityFixture{transaction: "txn"}
	check := workspaceJobAuthorizer(authority, func(context.Context) (consoleauth.ConsoleState, error) { return state, nil }, "issuer", "group")
	job := consolejobs.Job{CreatedBy: "local-owner", Kind: jobexecutor.KindLocalAdminRotate, Mutating: true}
	if err := check(context.Background(), job); err != nil || authority.owners != 1 {
		t.Fatal(err)
	}
	authority.ownerErr = errors.New("fixture revoked owner")
	if check(context.Background(), job) == nil {
		t.Fatal("revoked owner admitted")
	}
	for _, kind := range []consolejobs.PrincipalKind{consolejobs.PrincipalBootstrap, consolejobs.PrincipalEnrollment} {
		state = consoleauth.StateBootstrap
		if kind == consolejobs.PrincipalEnrollment {
			state = consoleauth.StateEnrollment
		}
		actor, _ := consolejobs.TransactionPrincipal(kind, "txn")
		job = consolejobs.Job{CreatedBy: actor, Kind: jobexecutor.KindDeploymentApply, Mutating: true, Request: map[string]any{
			consolejobs.ConfirmationTransactionRequestKey: "txn", consolejobs.ConfirmationIdentitySourceRequestKey: string(kind), consolejobs.ConfirmationActionRequestKey: jobexecutor.KindDeploymentApply}}
		if err := check(context.Background(), job); err != nil {
			t.Fatal("narrow transaction apply rejected", err)
		}
		job.Kind = jobexecutor.KindLocalAdminRotate
		if check(context.Background(), job) == nil {
			t.Fatal("transaction escalated to rotation")
		}
		job.Kind = jobexecutor.KindDeploymentApply
		authority.transaction = "changed"
		if check(context.Background(), job) == nil {
			t.Fatal("stale transaction reused")
		}
		authority.transaction = "txn"
		state = consoleauth.StateFull
		if check(context.Background(), job) == nil {
			t.Fatal("bootstrap elevated to full owner")
		}
	}
	if authority.owners != 2 {
		t.Fatal("transaction used full owner authority")
	}
}

func TestWorkspaceJobAuthorityRejectsCancellationAndStateDrift(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := &workspaceAuthorityFixture{transaction: "txn"}
	state := func(context.Context) (consoleauth.ConsoleState, error) { return consoleauth.StateBootstrap, nil }
	check := workspaceJobAuthorizer(a, state, "", "")
	if check(ctx, consolejobs.Job{CreatedBy: "local-owner"}) == nil || a.owners != 0 {
		t.Fatal("canceled authorization ran")
	}
	reads := 0
	check = workspaceJobAuthorizer(a, func(context.Context) (consoleauth.ConsoleState, error) {
		reads++
		if reads > 1 {
			return consoleauth.StateFull, nil
		}
		return consoleauth.StateBootstrap, nil
	}, "", "")
	actor, _ := consolejobs.TransactionPrincipal(consolejobs.PrincipalBootstrap, "txn")
	job := consolejobs.Job{CreatedBy: actor, Kind: jobexecutor.KindDeploymentApply, Mutating: true, Request: map[string]any{consolejobs.ConfirmationTransactionRequestKey: "txn", consolejobs.ConfirmationIdentitySourceRequestKey: "bootstrap", consolejobs.ConfirmationActionRequestKey: jobexecutor.KindDeploymentApply}}
	if check(context.Background(), job) == nil {
		t.Fatal("state transition during auth accepted")
	}
}
