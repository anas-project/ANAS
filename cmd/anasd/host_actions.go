package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/buildinfo"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/hostconfirmation"
	"github.com/anas-project/ANAS/internal/jobexecutor"
	"github.com/anas-project/ANAS/internal/runner"
)

type daemonHostActions interface {
	WithdrawForwarding(context.Context, string, string) error
	Run(context.Context) error
	Ready() <-chan struct{}
	InvokePreflight(context.Context, string, string, string) (consolejobs.CreateResult, error)
	InvokePlan(context.Context, string, string, string, json.RawMessage, string) (consolejobs.CreateResult, error)
	InvokeImagePrunePlan(context.Context, string, string, string) (consolejobs.CreateResult, error)
	IssueConfirmation(context.Context, string, string, string, string) (hostconfirmation.IssueResult, error)
	InvokeConfirmed(context.Context, string, string, string, string, json.RawMessage, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error)
	InvokeImagePruneConfirmed(context.Context, string, string, string, hostconfirmation.RawToken, string) (consolejobs.CreateResult, error)
	CancelQueuedPreflight(context.Context, string, string) (consolejobs.Job, error)
}

func validateHostActionDaemon(config consoleconfig.Config) error {
	if !config.HostActions {
		return nil
	}
	if runtime.GOOS != "linux" || os.Getuid() != 0 || os.Getgid() != 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() ||
		(hostaction.ReleaseIdentity{Version: buildinfo.Version, Commit: buildinfo.Commit}).Validate() != nil {
		return errors.New("host_actions requires the installed root/root Linux anasd systemd service and a versioned release build")
	}
	return nil
}

func configureHostActions(ctx context.Context, config consoleconfig.Config, store *consolejobs.Store, lease *consolejobs.ExecutionLease, journal hostaction.AuditJournal, auth *consoleauth.Store, ingress *computeingressruntime.ControllerCoordinator) (daemonHostActions, func() error, error) {
	if !config.HostActions {
		return nil, func() error { return nil }, nil
	}
	if err := validateHostActionDaemon(config); err != nil {
		return nil, nil, err
	}
	if auth == nil || hostaction.CheckHostActionClient() != nil {
		return nil, nil, hostaction.ErrUnavailable
	}
	ids := make([]string, len(config.Workspaces))
	workspaces := make([]httpapi.Workspace, len(config.Workspaces))
	for i, workspace := range config.Workspaces {
		ids[i] = workspace.ID
		workspaces[i] = httpapi.Workspace{ID: workspace.ID, Path: workspace.Path}
	}
	registry, err := httpapi.NewRegistry(workspaces)
	if err != nil {
		return nil, nil, err
	}
	issuer, group := "", ""
	if config.TrustedProxy != nil {
		issuer, group = config.TrustedProxy.OIDCIssuer, config.TrustedProxy.PlatformAdminGroup
	}
	confirmations, err := hostconfirmation.OpenProduction(ctx, journal)
	if err != nil {
		return nil, nil, err
	}
	keepConfirmations := false
	defer func() {
		if !keepConfirmations {
			_ = confirmations.Close()
		}
	}()
	s, err := jobexecutor.NewHostActionService(jobexecutor.HostActionServiceOptions{
		Store: store, Lease: lease, Confirmations: confirmations, Journal: journal, Workspaces: ids,
		IngressCoordinator: ingress,
		Release:            hostaction.ReleaseIdentity{Version: buildinfo.Version, Commit: buildinfo.Commit},
		Authorize:          func(ctx context.Context, actor, _ string) error { return auth.CheckJobOwner(ctx, actor, issuer, group) },
		ChineseSpeedup: func(ctx context.Context, id string) (bool, error) {
			path, ok := registry.Resolve(id)
			if !ok {
				return false, hostaction.ErrDenied
			}
			return runner.WorkspaceChineseSpeedup(ctx, path)
		},
	})
	if err != nil {
		return nil, nil, err
	}
	stop, err := startHostActionOwner(ctx, s)
	if err != nil {
		return nil, nil, err
	}
	keepConfirmations = true
	return s, func() error { return errors.Join(stop(), confirmations.Close()) }, nil
}

// Start and drain are owned by the SAME daemon as its existing job lease. A
// failed startup never publishes a queue. Shutdown waits for the service's own
// bounded protocol cleanup, not for an HTTP handler or a detached goroutine.
func startHostActionOwner(parent context.Context, service daemonHostActions) (func() error, error) {
	if parent == nil || service == nil {
		return nil, hostaction.ErrUnavailable
	}
	owner, cancel := context.WithCancel(parent)
	done := make(chan error, 1)
	go func() { done <- service.Run(owner) }()
	var once sync.Once
	var stopped error
	stop := func() error { once.Do(func() { cancel(); stopped = <-done }); return stopped }
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-service.Ready():
		if parent.Err() != nil {
			_ = stop()
			return nil, hostaction.ErrUnavailable
		}
		return stop, nil
	case err := <-done:
		once.Do(func() { cancel(); stopped = err })
		return nil, hostaction.ErrUnavailable
	case <-parent.Done():
		_ = stop()
		return nil, parent.Err()
	case <-timer.C:
		_ = stop()
		return nil, hostaction.ErrUnavailable
	}
}

// Generic job queries remain available when host actions are disabled. Never
// let an action ABI job fall through to the legacy executor's cancellation.
func cancelConsoleJob(ctx context.Context, id string, store *consolejobs.Store, host daemonHostActions, legacy func(context.Context, string) (consolejobs.Job, error)) (consolejobs.Job, error) {
	job, err := store.Get(ctx, id)
	if err != nil {
		return consolejobs.Job{}, err
	}
	if job.Action == nil {
		return legacy(ctx, id)
	}
	if job.Action.Name != "incus.status" || host == nil {
		return consolejobs.Job{}, consolejobs.ErrConflict
	}
	principal, ok := httpapi.PrincipalFromContext(ctx)
	if !ok || principal.Role != "owner" || principal.ID == "" {
		return consolejobs.Job{}, hostaction.ErrDenied
	}
	return host.CancelQueuedPreflight(ctx, principal.ID, id)
}
