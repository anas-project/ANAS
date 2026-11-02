//go:build unix

package processgroup

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestCancellationTerminatesWholeProcessGroupAfterGrace(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "sh", "-c", `trap '' TERM; sh -c "trap '' TERM; while :; do sleep 1; done" & while :; do sleep 1; done`)
	grace := 100 * time.Millisecond
	ConfigureWithGrace(command, grace)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	pid := command.Process.Pid
	started := time.Now()
	cancel()
	err := command.Wait()
	if err == nil {
		t.Fatal("canceled process group exited successfully")
	}
	if elapsed := time.Since(started); elapsed < grace {
		t.Fatalf("process group exited after %s, before %s TERM grace", elapsed, grace)
	}
	if !errors.Is(err, context.Canceled) {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("wait error = %T %v", err, err)
		}
	}
	// Wait only reaps the direct child; allow init to reap killed descendants.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := syscall.Kill(-pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process group %d still exists after cancellation: %v", pid, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
