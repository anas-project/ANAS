package main

import (
	"context"
	"errors"
	"log"
	"path/filepath"
	"reflect"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/internal/serviceauth"
)

func configureServiceAPI(ctx context.Context, config consoleconfig.Config, reload func() (consoleconfig.Config, error), writer consoleaudit.Appender, logger *log.Logger) (*httpapi.ServiceAPIOptions, func(), error) {
	if config.ServiceAPI == nil {
		return nil, func() {}, nil
	}
	load := func() (serviceauth.Config, error) {
		current := config
		if reload != nil {
			var err error
			current, err = reload()
			if err != nil {
				return serviceauth.Config{}, err
			}
		}
		if current.ServiceAPI == nil {
			return serviceauth.Config{Directory: config.ServiceAPI.Directory}, nil
		}
		// The route registry and delivery mount are fixed at process start. Only
		// client enablement and grants are live authority; structural changes need
		// a restart rather than silently changing what an existing ID addresses.
		if current.ServiceAPI.Directory != config.ServiceAPI.Directory || !reflect.DeepEqual(current.Workspaces, config.Workspaces) {
			return serviceauth.Config{}, errors.New("service API directory or workspace registration changed; restart is required")
		}
		return *current.ServiceAPI, nil
	}
	manager, err := serviceauth.New(serviceauth.Options{LoadConfig: load, OnError: func(err error) {
		if logger != nil {
			logger.Printf("service token maintenance: %v", err)
		}
	}})
	if err != nil {
		return nil, nil, err
	}
	if err := manager.Reconcile(ctx); err != nil {
		return nil, nil, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = manager.Run(runCtx) }()
	return &httpapi.ServiceAPIOptions{Authenticate: manager.Authenticate, Factory: runner.NewWorkspaceApplicationCredentialService,
		MailInstall:  newMailInstallHandler(filepath.Join(filepath.Dir(config.ServiceAPI.Directory), "mail-installations"), writer),
		MailOutbound: newMailOutboundHandler(writer),
		Audit: func(ctx context.Context, event httpapi.ServiceCredentialAuditEvent) error {
			if writer == nil {
				return audit.ErrUnavailable
			}
			details := map[string]any{"client_id": event.ClientID, "actor_source": "service_declared", "declared_actor": event.DeclaredActor, "application": event.Application}
			if event.RequestID != "" {
				details["request_id"] = event.RequestID
			}
			_, err := writer.AppendContext(ctx, audit.Event{Type: "service.application.credentials.read", Actor: "service:" + event.ClientID, WorkspaceID: event.WorkspaceID, Outcome: "authorized", Details: details})
			return err
		},
	}, func() { cancel(); <-done }, nil
}
