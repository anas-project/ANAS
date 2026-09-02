//go:build unix

package jobexecutor

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/deploymentaudit"
	"github.com/anas-project/ANAS/internal/processgroup"
)

// CONSOLE-R-057: drive cancellation through the durable executor into a real
// descendant process group, then require the compensation marker to clear.
func TestExecutorCancellationTerminatesLifecycleProcessGroupAndCompensates(t *testing.T) {
	store := openExecutorStore(t)
	request := application.LifecycleRequest{
		Action: application.LifecycleRestart, Modules: []string{"fixture"}, ExpectedDeploymentID: "dep-fixture",
		ExpectedDigest: strings.Repeat("a", 64), ExpectedModules: []string{"fixture"},
	}
	body, err := jsonObject(request)
	if err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateOrGet(context.Background(), consolejobs.CreateSpec{
		Kind: KindDeploymentRestart, WorkspaceID: "main", Mutating: true, Request: body,
		Idempotency: consolejobs.IdempotencyInput{
			Principal: consolejobs.PrincipalLocalOwner, Method: "POST", CanonicalPath: "/fixture/restart",
			Key: "fixture-cancel", RequestDigest: consolejobs.DigestRequest([]byte("fixture-cancel")),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 1)
	compensated := make(chan struct{})
	var compensationOnce sync.Once
	executor, err := New(Options{
		Store: store, Audit: deploymentaudit.SinkFunc(func(context.Context, deploymentaudit.Event) error { return nil }),
		Workspaces: []Workspace{{ID: "main", Path: "/main"}}, PollInterval: 5 * time.Millisecond,
		DeploymentFactory: func(string, application.EventSink) application.DeploymentService {
			return &fakeDeploymentService{
				apply: func(context.Context, application.ApplyRequest) (application.ApplyResult, error) {
					return application.ApplyResult{}, nil
				},
				lifecycle: func(ctx context.Context, incoming application.LifecycleRequest) (application.LifecycleResult, error) {
					command := exec.CommandContext(ctx, "sh", "-c", `trap '' TERM; sh -c "trap '' TERM; while :; do sleep 1; done" & while :; do sleep 1; done`)
					processgroup.ConfigureWithGrace(command, 100*time.Millisecond)
					if err := command.Start(); err != nil {
						return application.LifecycleResult{}, err
					}
					started <- command.Process.Pid
					_ = command.Wait()
					return application.LifecycleResult{}, ctx.Err()
				},
				compensate: func(context.Context) error {
					compensationOnce.Do(func() { close(compensated) })
					return nil
				},
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	daemonContext, stopDaemon := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- executor.Run(daemonContext) }()
	executor.Notify("main")
	var pid int
	select {
	case pid = <-started:
	case <-time.After(2 * time.Second):
		stopDaemon()
		t.Fatal("lifecycle process group did not start")
	}
	if _, err := executor.Cancel(context.Background(), created.Job.ID); err != nil {
		stopDaemon()
		t.Fatal(err)
	}
	select {
	case <-compensated:
	case <-time.After(3 * time.Second):
		stopDaemon()
		t.Fatal("canceled lifecycle did not reach compensation")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		job, err := store.Get(context.Background(), created.Job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == consolejobs.StatusCanceled && !job.NeedsCompensationCheck {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("compensation marker did not clear: %#v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("lifecycle process group %d survived cancellation: %v", pid, err)
	}
	stopDaemon()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
