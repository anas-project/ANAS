//go:build unix

package consolejobs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	compactionRebindHelperMarker = "ANAS_CONSOLEJOBS_COMPACTION_REBIND_HELPER"
	compactionRebindProtocol     = "consolejobs-compaction-rebind-v1"
)

type compactionRebindRequest struct {
	Directory     string `json:"directory"`
	JobID         string `json:"jobId"`
	EventCapacity int    `json:"eventCapacity"`
	ExpectedID    uint64 `json:"expectedId"`
	Command       string `json:"command,omitempty"`
}

type compactionRebindFrame struct {
	Protocol string `json:"protocol"`
	Type     string `json:"type"`
	Error    string `json:"error,omitempty"`
	Event    *Event `json:"event,omitempty"`
}

func TestCrossProcessCompactionRebindPreservesConcurrentWriter(t *testing.T) {
	if !crossProcessLockSupported {
		t.Skip("cross-process locking unsupported")
	}

	directory := filepath.Join(t.TempDir(), "jobs")
	options := Options{EventCapacity: 2}
	parent, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	parentClosed := false
	t.Cleanup(func() {
		if !parentClosed {
			if err := parent.Close(); err != nil {
				t.Errorf("close parent store: %v", err)
			}
		}
	})

	job := createJobForTest(t, parent, testCreateSpec("cross-process-compaction", "workspace-a", false))
	first := appendEventForTest(t, parent, job.ID, "parent-first")
	second := appendEventForTest(t, parent, job.ID, "parent-second")
	third := appendEventForTest(t, parent, job.ID, "parent-third")

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processContext, cancelProcess := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProcess()
	command := exec.CommandContext(processContext, executable, "-test.run=^TestCrossProcessCompactionRebindHelper$")
	// Store coordinates and job identity travel over the private stdin pipe;
	// only the non-sensitive helper marker is added to the child environment.
	command.Env = compactionRebindChildEnvironment(os.Environ())
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = stdin.Close()
		if !waited && command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})

	requestEncoder := json.NewEncoder(stdin)
	if err := requestEncoder.Encode(compactionRebindRequest{
		Directory:     directory,
		JobID:         job.ID,
		EventCapacity: options.EventCapacity,
		ExpectedID:    third.ID + 1,
	}); err != nil {
		t.Fatalf("send child setup: %v", err)
	}
	reader := bufio.NewReader(stdout)
	var transcript bytes.Buffer
	ready := readCompactionRebindFrame(t, reader, &transcript)
	if ready.Type == "error" {
		t.Fatalf("child failed before ready: %s\nstdout:\n%s", ready.Error, transcript.String())
	}
	if ready.Type != "ready" {
		t.Fatalf("first child frame type = %q, want ready", ready.Type)
	}

	// The ready frame is emitted only after the child has opened jobs.jsonl,
	// so Compact replaces the canonical inode while the child retains the old FD.
	if err := parent.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := requestEncoder.Encode(compactionRebindRequest{Command: "append"}); err != nil {
		t.Fatalf("send append command: %v", err)
	}

	result := readCompactionRebindFrame(t, reader, &transcript)
	if result.Type == "error" {
		t.Fatalf("child append failed: %s\nstdout:\n%s", result.Error, transcript.String())
	}
	if result.Type != "event" || result.Event == nil {
		t.Fatalf("child result = %+v, want event frame", result)
	}
	if result.Event.ID != third.ID+1 || result.Event.JobID != job.ID || result.Event.Kind != "child-after-compaction" {
		t.Fatalf("child event = %+v, want ID %d for job %s", *result.Event, third.ID+1, job.ID)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(&transcript, reader); err != nil {
		t.Fatalf("drain child stdout: %v", err)
	}
	if err := command.Wait(); err != nil {
		waited = true
		t.Fatalf("child process: %v\nstdout:\n%s\nstderr:\n%s", err, transcript.String(), stderr.String())
	}
	waited = true

	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	parentClosed = true
	reopened, err := Open(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened store: %v", err)
		}
	})

	page, err := reopened.Replay(context.Background(), job.ID, ReplayOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || page.Events[0].ID != third.ID || page.Events[1].ID != result.Event.ID {
		t.Fatalf("retained events after cross-process rebind = %v, want [%d %d]", eventIDs(page.Events), third.ID, result.Event.ID)
	}
	if page.PrunedThrough != second.ID || page.LatestID != result.Event.ID {
		t.Fatalf("event watermarks after cross-process rebind = %+v, want prunedThrough=%d latestId=%d", page, second.ID, result.Event.ID)
	}
	if first.ID+1 != second.ID || second.ID+1 != third.ID || third.ID+1 != result.Event.ID {
		t.Fatalf("event IDs are not contiguous across compaction: [%d %d %d %d]", first.ID, second.ID, third.ID, result.Event.ID)
	}
}

func TestCrossProcessCompactionRebindHelper(t *testing.T) {
	if os.Getenv(compactionRebindHelperMarker) != "1" {
		t.Skip("subprocess helper")
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	report := func(err error) {
		_ = encoder.Encode(compactionRebindFrame{
			Protocol: compactionRebindProtocol,
			Type:     "error",
			Error:    err.Error(),
		})
		t.Error(err)
	}

	var request compactionRebindRequest
	if err := decoder.Decode(&request); err != nil {
		report(fmt.Errorf("decode setup: %w", err))
		return
	}
	store, err := Open(request.Directory, Options{EventCapacity: request.EventCapacity})
	if err != nil {
		report(fmt.Errorf("open child store: %w", err))
		return
	}
	closed := false
	defer func() {
		if !closed {
			if err := store.Close(); err != nil {
				t.Errorf("close child store: %v", err)
			}
		}
	}()
	staleDescriptor := store.file
	if err := encoder.Encode(compactionRebindFrame{Protocol: compactionRebindProtocol, Type: "ready"}); err != nil {
		t.Error(err)
		return
	}

	var appendRequest compactionRebindRequest
	if err := decoder.Decode(&appendRequest); err != nil {
		report(fmt.Errorf("decode append command: %w", err))
		return
	}
	if appendRequest.Command != "append" {
		report(fmt.Errorf("unexpected command %q", appendRequest.Command))
		return
	}
	event, err := store.AppendEvent(context.Background(), request.JobID, EventInput{Kind: "child-after-compaction"})
	if err != nil {
		report(fmt.Errorf("append through stale store: %w", err))
		return
	}
	if event.ID != request.ExpectedID {
		report(fmt.Errorf("appended event ID = %d, want %d", event.ID, request.ExpectedID))
		return
	}
	if store.file == staleDescriptor {
		report(fmt.Errorf("child retained superseded journal descriptor"))
		return
	}
	if err := store.Close(); err != nil {
		report(fmt.Errorf("close rebound child store: %w", err))
		return
	}
	closed = true
	if err := encoder.Encode(compactionRebindFrame{
		Protocol: compactionRebindProtocol,
		Type:     "event",
		Event:    &event,
	}); err != nil {
		t.Error(err)
	}
}

func compactionRebindChildEnvironment(environment []string) []string {
	prefix := compactionRebindHelperMarker + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+"1")
}

func readCompactionRebindFrame(t *testing.T, reader *bufio.Reader, transcript *bytes.Buffer) compactionRebindFrame {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		transcript.WriteString(line)
		if err != nil {
			t.Fatalf("read child protocol frame: %v\nstdout:\n%s", err, transcript.String())
		}
		var frame compactionRebindFrame
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &frame); err == nil && frame.Protocol == compactionRebindProtocol {
			return frame
		}
	}
}
