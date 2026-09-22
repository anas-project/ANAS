package computeingressruntime

import (
	"context"
	"errors"
	"os"

	"github.com/anas-project/ANAS/internal/securefs"
)

var (
	ErrWorkspaceIngressActive = errors.New("workspace HTTP ingress is active or requires recovery; drain the original controller before mutation")
	ErrWorkspaceIngressState  = errors.New("workspace HTTP ingress fence is invalid or its recovery journal is unavailable")
	ErrWorkspaceIngressBusy   = errors.New("workspace is in use; HTTP controller cannot acquire its lifetime fence")
)

// WorkspaceStateStore adds cross-process writer exclusion to FileStateStore.
// It uses the EXISTING .anas/state/lock, not a second job/state database. The
// lock's nonempty durable marker pins the original journal directory until a
// complete drain. A dead process releasing flock is not proof of withdrawal.
//
// Workspace, its private state directories and the runtime lock must already
// exist. Directory is an installed, dedicated FileStateStore directory. All
// workspace-backed publishers must use this wrapper. Bare FileStateStore and
// arbitrary adapters remain laboratory primitives, not production launchers.
type WorkspaceStateStore struct {
	Workspace string
	Directory string
	layout    *workspaceLaunchLayout
}

// CheckWorkspaceMutationLock is called on the writer's actual descriptor,
// during contention and again after LOCK_EX succeeds, BEFORE recovery, Hooks,
// randomness or credential writes. Nonempty/corrupt/unknown bytes never grant
// authority and are never cleared by a writer. Read-only LOCK_SH is unchanged.
// This check by itself is not a lock: the caller must hold LOCK_EX throughout
// the protected mutation and must not replace/unlink its lock file.
func CheckWorkspaceMutationLock(ctx context.Context, file *os.File, path string) error {
	if ctx == nil {
		return ErrWorkspaceIngressState
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if file == nil || securefs.VerifyOpenNamedFile(file, path, "workspace runtime lock") != nil {
		return ErrWorkspaceIngressState
	}
	info, err := file.Stat()
	if err != nil {
		return ErrWorkspaceIngressState
	}
	if info.Size() != 0 {
		return ErrWorkspaceIngressActive
	}
	return ctx.Err()
}

// Only package-owned workspace journals can clear the fence. Callers cannot
// turn an application result or context cancellation into drain evidence.
func (s execution) completeWorkspaceDrain(ctx context.Context) error {
	if journal, ok := s.journal.(interface{ completeWorkspaceDrain(context.Context) error }); ok {
		return journal.completeWorkspaceDrain(ctx)
	}
	return nil
}
