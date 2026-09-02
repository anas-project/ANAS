// Command console-jobs-fixture creates and inspects narrowly scoped job state
// for black-box daemon recovery tests. It is a test artifact, not an operator
// interface: production job lifecycle changes remain owned by anasd.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consolejobs"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: console-jobs-fixture seed-running STORE | seed-pruned STORE TRANSACTION_ID AUDIT_STORE | inspect STORE JOB_ID | list STORE")
	}
	switch args[0] {
	case "seed-running":
		if len(args) != 2 {
			return errors.New("usage: console-jobs-fixture seed-running STORE")
		}
		return seedRunning(args[1])
	case "inspect":
		if len(args) != 3 {
			return errors.New("usage: console-jobs-fixture inspect STORE JOB_ID")
		}
		return inspect(args[1], args[2])
	case "list":
		if len(args) != 2 {
			return errors.New("usage: console-jobs-fixture list STORE")
		}
		return list(args[1])
	case "seed-pruned":
		if len(args) != 4 {
			return errors.New("usage: console-jobs-fixture seed-pruned STORE TRANSACTION_ID AUDIT_STORE")
		}
		return seedPruned(args[1], args[2], args[3])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

type prunedFixtureResult struct {
	JobID                 string `json:"job_id"`
	PrunedThrough         uint64 `json:"pruned_through"`
	OldestAvailable       uint64 `json:"oldest_available"`
	LatestID              uint64 `json:"latest_id"`
	JobEventCapacity      int    `json:"job_event_capacity"`
	AuditMaxEvents        int    `json:"audit_max_events"`
	RetainedJobEvents     int    `json:"retained_job_events"`
	RetainedAuditEvents   int    `json:"retained_audit_events"`
	JobBytesBeforeCompact int64  `json:"job_bytes_before_compact"`
	JobBytesAfterCompact  int64  `json:"job_bytes_after_compact"`
	AuditBytesBefore      int64  `json:"audit_bytes_before_compact"`
	AuditBytesAfter       int64  `json:"audit_bytes_after_compact"`
}

func seedPruned(directory, transactionID, auditDirectory string) (resultErr error) {
	principal, err := consolejobs.TransactionPrincipal(consolejobs.PrincipalBootstrap, transactionID)
	if err != nil {
		return fmt.Errorf("construct bootstrap principal: %w", err)
	}
	const (
		jobCapacity   = 1
		auditCapacity = 2
	)
	store, err := consolejobs.Open(directory, consolejobs.Options{
		EventCapacity:              jobCapacity,
		EventRetention:             24 * time.Hour,
		JournalCompactionThreshold: 1 << 60,
	})
	if err != nil {
		return fmt.Errorf("open pruned job store: %w", err)
	}
	storeOpen := true
	defer func() {
		if storeOpen {
			resultErr = errors.Join(resultErr, store.Close())
		}
	}()
	created, err := store.Create(context.Background(), consolejobs.CreateSpec{
		Kind:        "fixture.replay-gap",
		WorkspaceID: "main",
		Mutating:    false,
		Request:     map[string]any{"fixture": "r059-event-gap"},
		Idempotency: consolejobs.IdempotencyInput{
			Principal:     principal,
			Method:        "POST",
			CanonicalPath: "/test-fixtures/r059-event-gap",
			Key:           "r059-event-gap",
			RequestDigest: consolejobs.DigestRequest([]byte("r059-event-gap")),
		},
	})
	if err != nil {
		return fmt.Errorf("create pruned event fixture: %w", err)
	}
	largePayload := strings.Repeat("j", 512<<10)
	first, err := store.AppendEvent(context.Background(), created.Job.ID, consolejobs.EventInput{
		Kind: "obsolete-large-event", Data: map[string]any{"payload": largePayload},
	})
	if err != nil {
		return fmt.Errorf("append obsolete job event: %w", err)
	}
	latest, err := store.AppendEvent(context.Background(), created.Job.ID, consolejobs.EventInput{
		Kind: "retained-event", Data: map[string]any{"marker": "retained"},
	})
	if err != nil {
		return fmt.Errorf("append retained job event: %w", err)
	}
	if _, err := store.Start(context.Background(), created.Job.ID); err != nil {
		return fmt.Errorf("start replay-gap job: %w", err)
	}
	if _, err := store.Transition(context.Background(), created.Job.ID, consolejobs.StatusSucceeded, consolejobs.TransitionInput{}); err != nil {
		return fmt.Errorf("finish replay-gap job: %w", err)
	}
	jobBefore, err := fileSize(filepath.Join(directory, consolejobs.JournalFilename))
	if err != nil {
		return err
	}
	if err := store.Compact(context.Background()); err != nil {
		return fmt.Errorf("compact job journal: %w", err)
	}
	jobAfter, err := fileSize(filepath.Join(directory, consolejobs.JournalFilename))
	if err != nil {
		return err
	}
	zero := uint64(0)
	_, replayErr := store.Replay(context.Background(), created.Job.ID, consolejobs.ReplayOptions{AfterID: &zero})
	var gap *consolejobs.EventGapError
	if !errors.As(replayErr, &gap) {
		return fmt.Errorf("replay after pruned cursor = %v, want event gap", replayErr)
	}
	if gap.PrunedThrough != first.ID || gap.OldestAvailable != latest.ID || gap.LatestID != latest.ID {
		return fmt.Errorf("event gap = %#v, want pruned=%d oldest/latest=%d", gap, first.ID, latest.ID)
	}
	page, err := store.Replay(context.Background(), created.Job.ID, consolejobs.ReplayOptions{})
	if err != nil {
		return fmt.Errorf("replay retained job event: %w", err)
	}
	if jobAfter >= jobBefore {
		return fmt.Errorf("job compaction did not reclaim disk: %d -> %d", jobBefore, jobAfter)
	}
	if err := store.Close(); err != nil {
		return fmt.Errorf("close pruned job store: %w", err)
	}
	storeOpen = false

	writer, err := audit.OpenWithOptions(auditDirectory, audit.Options{
		MaxEvents:           auditCapacity,
		CompactionThreshold: 1 << 60,
	})
	if err != nil {
		return fmt.Errorf("open bounded audit store: %w", err)
	}
	writerOpen := true
	defer func() {
		if writerOpen {
			resultErr = errors.Join(resultErr, writer.Close())
		}
	}()
	if _, err := writer.Append(audit.Event{Type: "obsolete-large-audit", Details: map[string]any{"payload": strings.Repeat("a", 512<<10)}}); err != nil {
		return fmt.Errorf("append obsolete audit event: %w", err)
	}
	if _, err := writer.Append(audit.Event{Type: "retained-audit-one"}); err != nil {
		return fmt.Errorf("append first retained audit event: %w", err)
	}
	if _, err := writer.Append(audit.Event{Type: "retained-audit-two"}); err != nil {
		return fmt.Errorf("append second retained audit event: %w", err)
	}
	auditBefore, err := fileSize(filepath.Join(auditDirectory, audit.Filename))
	if err != nil {
		return err
	}
	if err := writer.Compact(context.Background()); err != nil {
		return fmt.Errorf("compact audit journal: %w", err)
	}
	auditAfter, err := fileSize(filepath.Join(auditDirectory, audit.Filename))
	if err != nil {
		return err
	}
	auditEvents, err := writer.List(context.Background())
	if err != nil {
		return fmt.Errorf("list retained audit events: %w", err)
	}
	if auditAfter >= auditBefore {
		return fmt.Errorf("audit compaction did not reclaim disk: %d -> %d", auditBefore, auditAfter)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close bounded audit store: %w", err)
	}
	writerOpen = false

	return json.NewEncoder(os.Stdout).Encode(prunedFixtureResult{
		JobID: created.Job.ID, PrunedThrough: gap.PrunedThrough,
		OldestAvailable: gap.OldestAvailable, LatestID: gap.LatestID,
		JobEventCapacity: jobCapacity, AuditMaxEvents: auditCapacity,
		RetainedJobEvents: len(page.Events), RetainedAuditEvents: len(auditEvents),
		JobBytesBeforeCompact: jobBefore, JobBytesAfterCompact: jobAfter,
		AuditBytesBefore: auditBefore, AuditBytesAfter: auditAfter,
	})
}

func fileSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("inspect %s: %w", path, err)
	}
	return info.Size(), nil
}

func seedRunning(directory string) (resultErr error) {
	store, err := consolejobs.Open(directory, consolejobs.Options{})
	if err != nil {
		return fmt.Errorf("open job store: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	created, err := store.Create(context.Background(), consolejobs.CreateSpec{
		Kind:        "deployment.apply",
		WorkspaceID: "main",
		Mutating:    true,
		Request:     map[string]any{"fixture": "daemon-restart"},
		Idempotency: consolejobs.IdempotencyInput{
			Principal:     "local-owner",
			Method:        "POST",
			CanonicalPath: "/api/v1/workspaces/main/actions/apply",
			Key:           "r049-running-fixture",
			RequestDigest: consolejobs.DigestRequest([]byte("r049-running-fixture")),
		},
	})
	if err != nil {
		return fmt.Errorf("create running fixture: %w", err)
	}
	running, err := store.Start(context.Background(), created.Job.ID)
	if err != nil {
		return fmt.Errorf("start running fixture: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(running)
}

func inspect(directory, jobID string) (resultErr error) {
	store, err := consolejobs.Open(directory, consolejobs.Options{})
	if err != nil {
		return fmt.Errorf("open job store: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	job, err := store.Get(context.Background(), jobID)
	if err != nil {
		return fmt.Errorf("get job: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(job)
}

func list(directory string) (resultErr error) {
	store, err := consolejobs.Open(directory, consolejobs.Options{})
	if err != nil {
		return fmt.Errorf("open job store: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, store.Close()) }()
	jobs, err := store.List(context.Background())
	if err != nil {
		return fmt.Errorf("list jobs: %w", err)
	}
	return json.NewEncoder(os.Stdout).Encode(jobs)
}
