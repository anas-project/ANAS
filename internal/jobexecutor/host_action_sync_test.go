package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
