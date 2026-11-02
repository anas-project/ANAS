package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/consolejobs"
	"github.com/anas-project/ANAS/internal/hostaction"
)

// HOSTACT-R-014: anasd queues the approved Traefik sync under its own actor,
// and that actor can own nothing but a sync action.
func TestDaemonOwnedSyncRunsWithoutAConsoleSession(t *testing.T) {
	consoleCalls := 0
	s, _, store, _ := serviceFixture(t, func(context.Context, string, string) error {
		consoleCalls++
		return errors.New("no console session")
	})
	stop, done := runServiceFixture(t, s)
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	created, err := s.InvokeSync(context.Background(), "main", hostaction.ActionTraefikSync)
	if err != nil {
		t.Fatal(err)
	}
	job := awaitServiceJob(t, store, created.Job.ID)
	if job.Status != consolejobs.StatusSucceeded || job.CreatedBy != SystemSyncActor || !job.Mutating {
		t.Fatalf("sync job = %+v", job)
	}
	if consoleCalls != 0 {
		t.Fatal("the daemon-owned sync consulted console authorization")
	}
	if _, err := s.InvokeSync(context.Background(), "main", hostaction.ActionConfigure); err == nil {
		t.Fatal("InvokeSync queued a confirmed action")
	}
	if _, err := s.InvokeSync(context.Background(), "unregistered", hostaction.ActionTraefikSync); err == nil {
		t.Fatal("InvokeSync accepted an unregistered workspace")
	}
	// The console path refuses the system actor outright.
	if _, err := s.Invoke(context.Background(), SystemSyncActor, "main", hostaction.ActionStatus, json.RawMessage(`{}`), "k"); err == nil {
		t.Fatal("a console invocation used the daemon's actor")
	}
	if s.authorizeAction(context.Background(), SystemSyncActor, "main", hostaction.ActionUninstall) == nil {
		t.Fatal("the daemon's actor authorized a destructive action")
	}
}

// A sync may already have read the old deployment or Docker addresses when a
// new trigger arrives. The new observation must get its own queued execution.
func TestSyncTriggeredAfterOldReadRunsAgain(t *testing.T) {
	for _, action := range []string{hostaction.ActionPortsSync, hostaction.ActionTraefikSync} {
		t.Run(action, func(t *testing.T) {
			s, runtime, store, _ := serviceFixture(t, nil)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			t.Cleanup(func() { once.Do(func() { close(release) }) })
			var desired, observed, runs atomic.Int32
			desired.Store(1)
			original := runtime.execute
			runtime.execute = func(ctx context.Context, id string, observer consolejobs.JobCommitObserver) (consolejobs.Job, error) {
				value := desired.Load()
				if runs.Add(1) == 1 {
					close(entered)
					select {
					case <-release:
					case <-ctx.Done():
						return consolejobs.Job{}, ctx.Err()
					}
				}
				// Simulate applying the read value before publishing the
				// succeeded job, just as a real action commits its effects.
				observed.Store(value)
				return original(ctx, id, observer)
			}
			stop, done := runServiceFixture(t, s)
			defer func() {
				stop()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			first, err := s.InvokeSync(context.Background(), "main", action)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("sync did not start")
			}
			desired.Store(2)
			second, err := s.InvokeSync(context.Background(), "main", action)
			once.Do(func() { close(release) })
			if err != nil {
				t.Fatal(err)
			}
			if second.Existing || second.Job.ID == first.Job.ID {
				t.Fatal("new state was merged into an execution that already read old state")
			}
			if job := awaitServiceJob(t, store, second.Job.ID); job.Status != consolejobs.StatusSucceeded {
				t.Fatalf("followup status: %s", job.Status)
			}
			if runs.Load() != 2 || observed.Load() != 2 {
				t.Fatalf("runs=%d observed=%d", runs.Load(), observed.Load())
			}
		})
	}
}
