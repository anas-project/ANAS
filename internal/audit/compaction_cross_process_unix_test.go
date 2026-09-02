//go:build unix

package audit

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
	auditCompactionProcessMarker   = "ANAS_AUDIT_COMPACTION_REBIND_HELPER"
	auditCompactionProcessProtocol = "audit-compaction-rebind-v1"
	auditCompactionThresholdOff    = int64(1 << 62)
)

type auditCompactionProcessRequest struct {
	Directory string `json:"directory,omitempty"`
	Command   string `json:"command,omitempty"`
}

type auditCompactionProcessFrame struct {
	Protocol    string `json:"protocol"`
	Type        string `json:"type"`
	Error       string `json:"error,omitempty"`
	Event       *Event `json:"event,omitempty"`
	Rebound     bool   `json:"rebound,omitempty"`
	OldFileSize int64  `json:"old_file_size,omitempty"`
}

func TestAuditCompactionCrossProcessRebindPreservesAppend(t *testing.T) {
	if !crossProcessAuditLockSupported {
		t.Skip("cross-process audit locking is unavailable")
	}

	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{MaxEvents: 2, CompactionThreshold: auditCompactionThresholdOff}
	parent, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	parentClosed := false
	t.Cleanup(func() {
		if !parentClosed {
			if err := parent.Close(); err != nil {
				t.Errorf("close parent audit Writer: %v", err)
			}
		}
	})

	first, err := parent.Append(Event{Type: "cross-process-parent-first"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first parent sequence = %d, want 1", first.Sequence)
	}

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	processContext, cancelProcess := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelProcess()
	command := exec.CommandContext(processContext, executable, "-test.run=^TestAuditCompactionCrossProcessHelper$")
	command.Env = auditCompactionChildEnvironment(os.Environ())
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
	if err := requestEncoder.Encode(auditCompactionProcessRequest{Directory: directory}); err != nil {
		t.Fatalf("send child setup: %v", err)
	}
	reader := bufio.NewReader(stdout)
	var transcript bytes.Buffer
	ready := readAuditCompactionProcessFrame(t, reader, &transcript)
	if ready.Type == "error" {
		t.Fatalf("child failed before ready: %s\nstdout:\n%s", ready.Error, transcript.String())
	}
	if ready.Type != "ready" {
		t.Fatalf("first child frame type = %q, want ready", ready.Type)
	}

	second, err := parent.Append(Event{Type: "cross-process-parent-second"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := parent.Append(Event{Type: "cross-process-parent-third"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 || third.Sequence != 3 {
		t.Fatalf("parent sequences = %d, %d, want 2, 3", second.Sequence, third.Sequence)
	}

	canonicalPath := filepath.Join(directory, Filename)
	oldCanonicalInfo, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	oldCanonical, err := os.Open(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = oldCanonical.Close() })
	lockPath := filepath.Join(directory, lockFilename)
	lockInfoBefore, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := parent.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	newCanonicalInfo, err := os.Lstat(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(oldCanonicalInfo, newCanonicalInfo) {
		t.Fatal("compaction did not replace the canonical audit inode")
	}
	oldDescriptorInfo, err := oldCanonical.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if oldDescriptorInfo.Size() != 0 {
		t.Fatalf("parent-observed superseded inode size = %d, want 0", oldDescriptorInfo.Size())
	}
	lockInfoAfterCompaction, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(lockInfoBefore, lockInfoAfterCompaction) {
		t.Fatal("audit.lock inode changed during compaction")
	}

	if err := requestEncoder.Encode(auditCompactionProcessRequest{Command: "append"}); err != nil {
		t.Fatalf("send child append command: %v", err)
	}
	result := readAuditCompactionProcessFrame(t, reader, &transcript)
	if result.Type == "error" {
		t.Fatalf("child append failed: %s\nstdout:\n%s", result.Error, transcript.String())
	}
	if result.Type != "event" || result.Event == nil {
		t.Fatalf("child result = %+v, want event frame", result)
	}
	if result.Event.Sequence != 4 || result.Event.Type != "cross-process-child-after-compaction" {
		t.Fatalf("child event = %+v, want sequence 4 after compaction", *result.Event)
	}
	if !result.Rebound {
		t.Fatal("child Writer appended without replacing its stale audit descriptor")
	}
	if result.OldFileSize != 0 {
		t.Fatalf("child stale descriptor size before rebind = %d, want 0", result.OldFileSize)
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

	canonicalBody, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonicalBody, []byte("cross-process-child-after-compaction")) {
		t.Fatal("canonical audit journal does not contain the child event")
	}
	lockInfoAfterChild, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(lockInfoBefore, lockInfoAfterChild) {
		t.Fatal("audit.lock inode changed during child rebind or append")
	}

	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	parentClosed = true
	reopened, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened audit Writer: %v", err)
		}
	})
	next, err := reopened.Append(Event{Type: "cross-process-after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 5 {
		t.Fatalf("sequence after cross-process rebind and restart = %d, want 5", next.Sequence)
	}
}

func TestAuditCompactionCrossProcessHelper(t *testing.T) {
	if os.Getenv(auditCompactionProcessMarker) != "1" {
		t.Skip("cross-process helper")
	}

	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	report := func(err error) {
		_ = encoder.Encode(auditCompactionProcessFrame{
			Protocol: auditCompactionProcessProtocol,
			Type:     "error",
			Error:    err.Error(),
		})
		t.Error(err)
	}

	var setup auditCompactionProcessRequest
	if err := decoder.Decode(&setup); err != nil {
		report(fmt.Errorf("decode setup: %w", err))
		return
	}
	if setup.Directory == "" {
		report(fmt.Errorf("audit directory is empty"))
		return
	}
	options := Options{MaxEvents: 2, CompactionThreshold: auditCompactionThresholdOff}
	writer, err := OpenWithOptions(setup.Directory, options)
	if err != nil {
		report(fmt.Errorf("open child audit Writer: %w", err))
		return
	}
	closed := false
	defer func() {
		if !closed {
			if err := writer.Close(); err != nil {
				t.Errorf("close child audit Writer: %v", err)
			}
		}
	}()
	staleDescriptor := writer.file
	if err := encoder.Encode(auditCompactionProcessFrame{
		Protocol: auditCompactionProcessProtocol,
		Type:     "ready",
	}); err != nil {
		t.Error(err)
		return
	}

	var appendRequest auditCompactionProcessRequest
	if err := decoder.Decode(&appendRequest); err != nil {
		report(fmt.Errorf("decode append command: %w", err))
		return
	}
	if appendRequest.Command != "append" {
		report(fmt.Errorf("unexpected command %q", appendRequest.Command))
		return
	}
	oldInfo, err := staleDescriptor.Stat()
	if err != nil {
		report(fmt.Errorf("inspect child stale descriptor: %w", err))
		return
	}
	persisted, err := writer.Append(Event{Type: "cross-process-child-after-compaction"})
	if err != nil {
		report(fmt.Errorf("append through child stale Writer: %w", err))
		return
	}
	rebound := writer.file != staleDescriptor
	if err := writer.Close(); err != nil {
		report(fmt.Errorf("close rebound child audit Writer: %w", err))
		return
	}
	closed = true
	if err := encoder.Encode(auditCompactionProcessFrame{
		Protocol:    auditCompactionProcessProtocol,
		Type:        "event",
		Event:       &persisted,
		Rebound:     rebound,
		OldFileSize: oldInfo.Size(),
	}); err != nil {
		t.Error(err)
	}
}

func auditCompactionChildEnvironment(environment []string) []string {
	prefix := auditCompactionProcessMarker + "="
	result := make([]string, 0, len(environment)+1)
	for _, item := range environment {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return append(result, prefix+"1")
}

func readAuditCompactionProcessFrame(t *testing.T, reader *bufio.Reader, transcript *bytes.Buffer) auditCompactionProcessFrame {
	t.Helper()
	for {
		line, err := reader.ReadString('\n')
		transcript.WriteString(line)
		if err != nil {
			t.Fatalf("read child protocol frame: %v\nstdout:\n%s", err, transcript.String())
		}
		var frame auditCompactionProcessFrame
		if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &frame); err == nil && frame.Protocol == auditCompactionProcessProtocol {
			return frame
		}
	}
}
