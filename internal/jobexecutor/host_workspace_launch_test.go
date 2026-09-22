package jobexecutor

import (
	"context"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/computeingressruntime"
	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusingresshost"
)

type rejectedLaunchInvoker struct{}

func (rejectedLaunchInvoker) InvokeHostAction(context.Context, string, []byte) ([]byte, error) {
	panic("caller-supplied invoker must not execute")
}

func TestHostWorkspaceLaunchRequiresRunningOwnerAndQueueBoundAuthority(t *testing.T) {
	s, _, _, _ := serviceFixture(t, func(context.Context, string, string) error { return hostaction.ErrDenied })
	request := computeingressruntime.HostWorkspaceStart{ScopeID: "main", Workspace: "/must-not-be-read"}
	if _, err := s.StartIngressWorkspace("alice", request); !errors.Is(err, hostaction.ErrUnavailable) {
		t.Fatal("launch before service Run", err)
	}
	cancel, done := runServiceFixture(t, s)
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	if _, err := s.StartIngressWorkspace("alice", request); !errors.Is(err, hostaction.ErrDenied) {
		t.Fatal("revoked actor reached filesystem", err)
	}
	request.Observation = incusingresshost.ProjectionClient{Invoker: rejectedLaunchInvoker{}}
	if _, err := s.StartIngressWorkspace("alice", request); !errors.Is(err, hostaction.ErrDenied) {
		t.Fatal("caller injected independent host invoker", err)
	}
	request.Observation = incusingresshost.ProjectionClient{}
	cancel()
	if _, err := s.StartIngressWorkspace("alice", request); !errors.Is(err, hostaction.ErrUnavailable) {
		t.Fatal("launch during shutdown", err)
	}
}
