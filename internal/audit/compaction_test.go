package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const disabledAutomaticAuditCompaction = int64(1 << 62)

func TestAuditCompactionCapacityPrunePreservesNextSequenceAcrossRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{
		MaxEvents:           2,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	base := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	writer.now = func() time.Time { return base }

	for index := 1; index <= 4; index++ {
		persisted, err := writer.Append(Event{Type: fmt.Sprintf("capacity-marker-%d", index)})
		if err != nil {
			t.Fatal(err)
		}
		if persisted.Sequence != uint64(index) {
			t.Fatalf("event %d sequence = %d", index, persisted.Sequence)
		}
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body := readAuditCompactionBody(t, directory)
	assertAuditMarkers(t, body,
		[]string{"capacity-marker-3", "capacity-marker-4"},
		[]string{"capacity-marker-1", "capacity-marker-2"},
	)

	reopened := openAuditCompactionWriter(t, directory, options)
	reopened.now = func() time.Time { return base.Add(time.Minute) }
	next, err := reopened.Append(Event{Type: "capacity-after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 5 {
		t.Fatalf("sequence after capacity prune and restart = %d, want 5", next.Sequence)
	}
}

func TestAuditFullyPrunedCheckpointPreservesNextSequenceAcrossRestart(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{
		Retention:           time.Hour,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	base := time.Date(2026, time.August, 30, 8, 0, 0, 0, time.UTC)
	writer.now = func() time.Time { return base }
	if event, err := writer.Append(Event{Type: "fully-pruned-marker"}); err != nil || event.Sequence != 1 {
		t.Fatalf("initial Append = %#v, %v", event, err)
	}
	writer.now = func() time.Time { return base.Add(2 * time.Hour) }
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if writer.state.lastSequence != 1 || writer.state.prunedThrough != 1 || len(writer.state.events) != 0 {
		t.Fatalf("fully pruned state = last %d pruned %d events %d", writer.state.lastSequence, writer.state.prunedThrough, len(writer.state.events))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openAuditCompactionWriter(t, directory, options)
	reopened.now = func() time.Time { return base.Add(2*time.Hour + time.Minute) }
	next, err := reopened.Append(Event{Type: "after-fully-pruned-restart"})
	if err != nil || next.Sequence != 2 {
		t.Fatalf("Append after fully pruned restart = %#v, %v", next, err)
	}
}

func TestAuditRetentionUsesCommitTimeRatherThanCallerTimestamp(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{
		MaxEvents:           10,
		Retention:           time.Hour,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	base := time.Date(2026, time.August, 30, 9, 0, 0, 0, time.UTC)
	commitTime := base
	writer.now = func() time.Time { return commitTime }

	first, err := writer.Append(Event{
		Type:      "expired-commit-future-occurrence",
		Timestamp: base.AddDate(100, 0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	commitTime = base.Add(2 * time.Hour)
	second, err := writer.Append(Event{
		Type:      "retained-commit-past-occurrence",
		Timestamp: base.AddDate(-100, 0, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Sequence != 1 || second.Sequence != 2 {
		t.Fatalf("sequences = %d, %d, want 1, 2", first.Sequence, second.Sequence)
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body := readAuditCompactionBody(t, directory)
	assertAuditMarkers(t, body,
		[]string{"retained-commit-past-occurrence"},
		[]string{"expired-commit-future-occurrence"},
	)

	reopened := openAuditCompactionWriter(t, directory, options)
	reopened.now = func() time.Time { return commitTime.Add(time.Minute) }
	next, err := reopened.Append(Event{Type: "retention-after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 3 {
		t.Fatalf("sequence after commit-time retention = %d, want 3", next.Sequence)
	}
}

func TestAuditLegacyMigrationDoesNotTrustOccurrenceTimestampForRetention(t *testing.T) {
	directory := secureTestDir(t)
	path := filepath.Join(directory, Filename)
	base := time.Date(2026, time.August, 30, 9, 0, 0, 0, time.UTC)
	legacy := `{"sequence":1,"timestamp":"2126-08-30T09:00:00Z","type":"legacy-future-occurrence"}` + "\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, base, base); err != nil {
		t.Fatal(err)
	}
	writer := openAuditCompactionWriter(t, directory, Options{
		MaxEvents: 10, Retention: time.Hour, CompactionThreshold: disabledAutomaticAuditCompaction,
	})
	writer.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := writer.Append(Event{
		Type: "legacy-retained-current-commit", Timestamp: base.AddDate(-100, 0, 0),
	}); err != nil {
		t.Fatal(err)
	}
	body := readAuditCompactionBody(t, directory)
	assertAuditSnapshotKinds(t, body)
	assertAuditMarkers(t, body,
		[]string{"legacy-retained-current-commit"},
		[]string{"legacy-future-occurrence"},
	)
}

func TestAuditCompactionMigratesLegacyJournal(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := []byte(
		compactionLegacyLine(1, "legacy-first") +
			compactionLegacyLine(2, "legacy-second"),
	)
	if err := os.WriteFile(filepath.Join(directory, Filename), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{
		MaxEvents:           10,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	writer.now = func() time.Time {
		return time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	}
	third, err := writer.Append(Event{Type: "legacy-third"})
	if err != nil {
		t.Fatal(err)
	}
	if third.Sequence != 3 {
		t.Fatalf("sequence appended to legacy journal = %d, want 3", third.Sequence)
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body := readAuditCompactionBody(t, directory)
	assertAuditSnapshotKinds(t, body)
	assertAuditMarkers(t, body,
		[]string{"legacy-first", "legacy-second", "legacy-third"},
		nil,
	)

	reopened := openAuditCompactionWriter(t, directory, options)
	reopened.now = func() time.Time {
		return time.Date(2026, time.August, 30, 10, 1, 0, 0, time.UTC)
	}
	fourth, err := reopened.Append(Event{Type: "legacy-fourth"})
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Sequence != 4 {
		t.Fatalf("sequence after legacy migration restart = %d, want 4", fourth.Sequence)
	}
}

func TestAuditExplicitCompactionRoundTrip(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{
		MaxEvents:           8,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	fixed := time.Date(2026, time.August, 30, 11, 0, 0, 0, time.UTC)
	writer.now = func() time.Time { return fixed }
	for index := 1; index <= 3; index++ {
		if _, err := writer.Append(Event{
			Type: fmt.Sprintf("roundtrip-marker-%d", index),
			Details: map[string]any{
				"index": index,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Compact(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	body := readAuditCompactionBody(t, directory)
	assertAuditSnapshotKinds(t, body)
	assertAuditMarkers(t, body,
		[]string{"roundtrip-marker-1", "roundtrip-marker-2", "roundtrip-marker-3"},
		nil,
	)

	reopened := openAuditCompactionWriter(t, directory, options)
	reopened.now = func() time.Time { return fixed.Add(time.Minute) }
	next, err := reopened.Append(Event{Type: "roundtrip-after-restart"})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 4 {
		t.Fatalf("sequence after explicit compaction = %d, want 4", next.Sequence)
	}
}

func TestAuditOversizedEventDoesNotPoisonWriter(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	options := Options{
		MaxEvents:           8,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	writer := openAuditCompactionWriter(t, directory, options)
	path := filepath.Join(directory, Filename)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = writer.Append(Event{
		Type: "oversized-event",
		Details: map[string]any{
			"safe_payload": strings.Repeat("x", maximumRecordBytes),
		},
	})
	if !errors.Is(err, ErrInvalidEvent) {
		t.Fatalf("oversized Append error = %v, want ErrInvalidEvent", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("journal size after rejected event = %d, want %d", after.Size(), before.Size())
	}

	persisted, err := writer.Append(Event{Type: "after-oversized-event"})
	if err != nil {
		t.Fatalf("healthy append after oversized input: %v", err)
	}
	if persisted.Sequence != 1 {
		t.Fatalf("sequence after oversized input = %d, want 1", persisted.Sequence)
	}
}

func TestAuditOpenRejectsMissingOrCorruptSnapshotSeal(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, []byte) []byte
	}{
		{name: "missing seal", mutate: removeAuditSnapshotSeal},
		{name: "corrupt seal", mutate: corruptAuditSnapshotSeal},
		{name: "mismatched footer time", mutate: mismatchAuditSnapshotFooterRecordedAt},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			options := Options{
				MaxEvents:           8,
				CompactionThreshold: disabledAutomaticAuditCompaction,
			}
			writer := openAuditCompactionWriter(t, directory, options)
			if _, err := writer.Append(Event{Type: "sealed-fixture"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Compact(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(directory, Filename)
			sealed := readAuditCompactionBody(t, directory)
			assertAuditSnapshotKinds(t, sealed)
			broken := test.mutate(t, sealed)
			if err := os.WriteFile(path, broken, 0o600); err != nil {
				t.Fatal(err)
			}

			opened, err := OpenWithOptions(directory, options)
			if err == nil {
				_ = opened.Close()
				t.Fatal("OpenWithOptions accepted an invalid snapshot")
			}
			after, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !reflect.DeepEqual(after, broken) {
				t.Fatal("failed recovery modified a complete corrupt checkpoint")
			}
		})
	}
}

func TestAuditAutomaticCompactionCommitsProspectiveEventOnceAndReclaimsOldInode(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{MaxEvents: 1, CompactionThreshold: 1})
	fixed := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	writer.now = func() time.Time { return fixed }
	if _, err := writer.Append(Event{
		Type:    "automatic-obsolete-marker",
		Details: map[string]any{"safe_payload": strings.Repeat("x", 16<<10)},
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, Filename)
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	lockBefore, err := os.Stat(filepath.Join(directory, lockFilename))
	if err != nil {
		t.Fatal(err)
	}

	second, err := writer.Append(Event{Type: "automatic-retained-marker"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Sequence != 2 || writer.state.generation != 1 {
		t.Fatalf("automatic compaction result = sequence %d generation %d", second.Sequence, writer.state.generation)
	}
	body := readAuditCompactionBody(t, directory)
	assertAuditSnapshotKinds(t, body)
	assertAuditMarkers(t, body, []string{"automatic-retained-marker"}, []string{"automatic-obsolete-marker"})
	if got := bytes.Count(body, []byte("automatic-retained-marker")); got != 1 {
		t.Fatalf("prospective event count = %d, want 1", got)
	}
	oldInfo, err := old.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if oldInfo.Size() != 0 {
		t.Fatalf("old audit inode size = %d, want 0", oldInfo.Size())
	}
	lockAfter, err := os.Stat(filepath.Join(directory, lockFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(lockBefore, lockAfter) {
		t.Fatal("audit.lock inode changed during compaction")
	}
}

func TestAuditAutomaticCompactionSkipsLiveOnlyHistory(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer := openAuditCompactionWriter(t, directory, Options{CompactionThreshold: 1})
	path := filepath.Join(directory, Filename)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if _, err := writer.Append(Event{Type: fmt.Sprintf("live-only-%d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || writer.state.generation != 0 {
		t.Fatalf("live-only history was needlessly replaced: generation=%d", writer.state.generation)
	}
}

func TestAuditRecoveryRejectsOversizedCompleteAndResidualLinesWithoutMutation(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body []byte
	}{
		{name: "complete", body: append(bytes.Repeat([]byte{'x'}, maximumRecordBytes), '\n')},
		{name: "residual", body: bytes.Repeat([]byte{'x'}, maximumRecordBytes+1)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := secureTestDir(t)
			path := filepath.Join(directory, Filename)
			if err := os.WriteFile(path, fixture.body, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(directory); err == nil || !strings.Contains(err.Error(), "size limit") {
				t.Fatalf("Open error = %v, want bounded record rejection", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, fixture.body) {
				t.Fatal("oversized audit line was modified during failed recovery")
			}
		})
	}
}

func TestAuditReplacementStateRejectsLineageRollbackAndRetainedMutation(t *testing.T) {
	recordedAt := time.Date(2026, time.August, 30, 13, 0, 0, 0, time.UTC)
	previous := &auditState{
		storeID:        "00112233445566778899aabbccddeeff",
		generation:     2,
		compacted:      true,
		lastSequence:   3,
		prunedThrough:  1,
		lastRecordedAt: recordedAt,
		events: []storedEvent{
			{event: Event{Sequence: 2, Timestamp: recordedAt, Type: "retained-two"}, recordedAt: recordedAt},
			{event: Event{Sequence: 3, Timestamp: recordedAt, Type: "retained-three"}, recordedAt: recordedAt},
		},
	}
	valid := previous.clone()
	valid.generation = 3
	if err := stateAdvances(previous, valid); err != nil {
		t.Fatalf("valid replacement rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*auditState)
	}{
		{name: "different store", mutate: func(state *auditState) { state.storeID = strings.Repeat("f", 32) }},
		{name: "same generation", mutate: func(state *auditState) { state.generation = previous.generation }},
		{name: "last sequence rollback", mutate: func(state *auditState) { state.lastSequence = 2; state.events = state.events[:1] }},
		{name: "prune rollback", mutate: func(state *auditState) { state.prunedThrough = 0 }},
		{name: "retained event mutation", mutate: func(state *auditState) { state.events[0].event.Type = "rewritten-two" }},
		{name: "retained commit time mutation", mutate: func(state *auditState) {
			state.events[0].recordedAt = recordedAt.Add(time.Minute)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid.clone()
			test.mutate(candidate)
			if err := stateAdvances(previous, candidate); err == nil {
				t.Fatal("stateAdvances accepted an invalid replacement")
			}
		})
	}
}

func openAuditCompactionWriter(t *testing.T, directory string, options Options) *Writer {
	t.Helper()
	writer, err := OpenWithOptions(directory, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := writer.Close(); err != nil && !errors.Is(err, ErrUnavailable) {
			t.Errorf("close audit compaction writer: %v", err)
		}
	})
	return writer
}

func readAuditCompactionBody(t *testing.T, directory string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertAuditMarkers(t *testing.T, body []byte, present, absent []string) {
	t.Helper()
	for _, marker := range present {
		if !bytes.Contains(body, []byte(marker)) {
			t.Errorf("audit checkpoint is missing marker %q", marker)
		}
	}
	for _, marker := range absent {
		if bytes.Contains(body, []byte(marker)) {
			t.Errorf("audit checkpoint retained pruned marker %q", marker)
		}
	}
}

func assertAuditSnapshotKinds(t *testing.T, body []byte) {
	t.Helper()
	var begin, end bool
	for lineNumber, line := range bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'}) {
		var record struct {
			RecordKind string `json:"record_kind"`
		}
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode checkpoint line %d: %v", lineNumber+1, err)
		}
		switch record.RecordKind {
		case "snapshot_begin":
			begin = true
		case "snapshot_end":
			end = true
		}
	}
	if !begin || !end {
		t.Fatalf("checkpoint kinds: begin=%t end=%t", begin, end)
	}
}

func compactionLegacyLine(sequence uint64, eventType string) string {
	return fmt.Sprintf(
		`{"sequence":%d,"timestamp":"2026-08-30T08:00:00Z","type":%q}`+"\n",
		sequence,
		eventType,
	)
}

func removeAuditSnapshotSeal(t *testing.T, body []byte) []byte {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'})
	if len(lines) < 2 {
		t.Fatalf("sealed checkpoint has %d lines, want at least 2", len(lines))
	}
	var footer struct {
		RecordKind string `json:"record_kind"`
	}
	if err := json.Unmarshal(lines[len(lines)-1], &footer); err != nil {
		t.Fatal(err)
	}
	if footer.RecordKind != "snapshot_end" {
		t.Fatalf("last checkpoint record kind = %q, want snapshot_end", footer.RecordKind)
	}
	return append(bytes.Join(lines[:len(lines)-1], []byte{'\n'}), '\n')
}

func corruptAuditSnapshotSeal(t *testing.T, body []byte) []byte {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'})
	if len(lines) == 0 {
		t.Fatal("sealed checkpoint is empty")
	}
	var footer map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &footer); err != nil {
		t.Fatal(err)
	}
	if kind, _ := footer["record_kind"].(string); kind != "snapshot_end" {
		t.Fatalf("last checkpoint record kind = %q, want snapshot_end", kind)
	}
	checkpoint, ok := footer["checkpoint"].(map[string]any)
	if !ok {
		t.Fatal("snapshot_end checkpoint boundary is missing")
	}
	digest, ok := checkpoint["digest"].(string)
	if !ok || len(digest) != 64 {
		t.Fatalf("snapshot_end digest = %#v, want SHA-256 hex", checkpoint["digest"])
	}
	if digest[0] == '0' {
		checkpoint["digest"] = "1" + digest[1:]
	} else {
		checkpoint["digest"] = "0" + digest[1:]
	}
	corruptFooter, err := json.Marshal(footer)
	if err != nil {
		t.Fatal(err)
	}
	lines[len(lines)-1] = corruptFooter
	return append(bytes.Join(lines, []byte{'\n'}), '\n')
}

func mismatchAuditSnapshotFooterRecordedAt(t *testing.T, body []byte) []byte {
	t.Helper()
	lines := bytes.Split(bytes.TrimSuffix(body, []byte{'\n'}), []byte{'\n'})
	if len(lines) == 0 {
		t.Fatal("sealed checkpoint is empty")
	}
	var footer map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &footer); err != nil {
		t.Fatal(err)
	}
	if kind, _ := footer["record_kind"].(string); kind != "snapshot_end" {
		t.Fatalf("last checkpoint record kind = %q, want snapshot_end", kind)
	}
	footer["recorded_at"] = "2126-08-30T09:00:00Z"
	mismatchedFooter, err := json.Marshal(footer)
	if err != nil {
		t.Fatal(err)
	}
	lines[len(lines)-1] = mismatchedFooter
	return append(bytes.Join(lines, []byte{'\n'}), '\n')
}
