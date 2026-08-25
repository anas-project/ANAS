//go:build unix

package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func acquireModuleCommandLock(ctx context.Context, workspace, module, scope string) (func(), error) {
	stateDir := filepath.Join(workspace, ".anas", "state")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, err
	}
	workspaceMode := syscall.LOCK_SH
	if scope == "workspace_write" {
		workspaceMode = syscall.LOCK_EX
	}
	workspaceUnlock, err := flockModuleCommandContext(ctx, filepath.Join(stateDir, "lock"), workspaceMode)
	if err != nil {
		return nil, err
	}
	if scope == "workspace_write" {
		return workspaceUnlock, nil
	}
	moduleMode := syscall.LOCK_SH
	if scope == "module_write" {
		moduleMode = syscall.LOCK_EX
	}
	moduleUnlock, err := flockModuleCommandContext(ctx, filepath.Join(stateDir, "module-command-"+module+".lock"), moduleMode)
	if err != nil {
		workspaceUnlock()
		return nil, err
	}
	return func() {
		moduleUnlock()
		workspaceUnlock()
	}, nil
}

func flockModuleCommandContext(ctx context.Context, path string, mode int) (func(), error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
