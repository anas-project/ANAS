package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/runner"
)

func registerLabRequests(workspace, destination string) error {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return fmt.Errorf("lab workspace must be an absolute clean path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return err
	}
	for _, grant := range snapshot.Authorizations {
		if grant.BaseDomain != "example.test" {
			return fmt.Errorf("lab request registration refuses production domains")
		}
	}
	return computeingressruntime.Register(ctx, workspace, destination, snapshot.Epoch)
}

func (m *labMediator) openWorkspaceRequestDirectory() (*os.File, error) {
	c := m.config
	if !filepath.IsAbs(c.Workspace) || filepath.Clean(c.Workspace) != c.Workspace {
		return nil, fmt.Errorf("lab workspace must be an absolute clean path")
	}
	if c.ActiveDeployment != "" || c.Authorization != nil || c.RequestDirectory != "" || c.LeaseSecretFile != "" {
		return nil, fmt.Errorf("workspace mediation cannot override Core authority, request directory or naming-key source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lease := computeingress.Lease{Consumer: c.Consumer, Resource: c.Resource}
	snapshot, grant, directory, err := computeingressruntime.OpenLease(ctx, c.Workspace, c.RequestRegistry, lease)
	if err != nil {
		return nil, err
	}
	if grant.BaseDomain != "example.test" {
		_ = directory.Close()
		return nil, fmt.Errorf("lab mediation refuses production domains")
	}
	m.snapshot = snapshot
	m.config.Authorization, m.config.ActiveDeployment = grant, snapshot.Deployment
	return directory, nil
}

func (m *labMediator) readWorkspaceNamingKey() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lease := computeingress.Lease{Consumer: m.config.Consumer, Resource: m.config.Resource}
	value, err := runner.ReadComputeHTTPNamingKey(ctx, m.config.Workspace, m.snapshot, lease)
	if err != nil {
		return err
	}
	m.secret = value
	return nil
}

func (m *labMediator) revalidateWorkspace() error {
	if m == nil || m.snapshot == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return computeingressruntime.StillCurrent(ctx, m.config.Workspace, m.snapshot)
}
