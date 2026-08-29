package consolejobs

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalReplayRejectsEventAfterTerminalJob(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	store, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	job := createJobForTest(t, store, testCreateSpec("terminal-replay", "workspace-a", false))
	if _, err := store.Start(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(t.Context(), job.ID, StatusSucceeded, TransitionInput{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	record := journalRecord{
		SchemaVersion: JournalVersion,
		Sequence:      store.state.lastRecordSequence + 1,
		Kind:          recordEventAdded,
		RecordedAt:    now,
		Event: &Event{
			ID: store.state.lastEventID + 1, JobID: job.ID, Timestamp: now, Kind: "too-late",
		},
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, '\n')
	file, err := os.OpenFile(filepath.Join(directory, JournalFilename), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeAll(file, body); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(directory, Options{}); !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "terminal job") {
		t.Fatalf("Open terminal-event journal error = %v, want fail-closed terminal rejection", err)
	}
}

func TestRecoverJournalRecordSizeBoundary(t *testing.T) {
	initialization := journalRecord{
		SchemaVersion: JournalVersion,
		Sequence:      1,
		Kind:          recordStoreInitialized,
		RecordedAt:    time.Date(2026, time.August, 29, 0, 0, 0, 0, time.UTC),
		StoreID:       strings.Repeat("a", 64),
	}
	body, err := json.Marshal(initialization)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("exact maximum is accepted", func(t *testing.T) {
		file := writeSizedJournalLine(t, body, maximumJournalRecordBytes, true)
		defer file.Close()
		state, err := recoverJournal(file)
		if err != nil {
			t.Fatal(err)
		}
		if !state.initialized || state.lastRecordSequence != 1 {
			t.Fatalf("recovered state = %+v", state)
		}
	})

	t.Run("complete record over maximum is rejected", func(t *testing.T) {
		file := writeSizedJournalLine(t, body, maximumJournalRecordBytes+1, true)
		defer file.Close()
		if _, err := recoverJournal(file); err == nil || !strings.Contains(err.Error(), "record exceeds") {
			t.Fatalf("recover oversized complete record error = %v", err)
		}
	})

	t.Run("incomplete record over maximum is rejected without truncation", func(t *testing.T) {
		file := writeSizedJournalLine(t, body, maximumJournalRecordBytes+1, false)
		defer file.Close()
		before, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := recoverJournal(file); err == nil || !strings.Contains(err.Error(), "record exceeds") {
			t.Fatalf("recover oversized incomplete record error = %v", err)
		}
		after, err := file.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if after.Size() != before.Size() {
			t.Fatalf("oversized incomplete record was truncated from %d to %d", before.Size(), after.Size())
		}
	})
}

func TestBoundedJournalLineReaderStopsOversizedUnterminatedInput(t *testing.T) {
	source := &countedByteReader{remaining: 1 << 40}
	reader := bufio.NewReaderSize(source, 4096)
	line, err := readBoundedJournalLine(reader)
	if !errors.Is(err, errJournalRecordTooLarge) {
		t.Fatalf("read oversized line error = %v, want size-limit error", err)
	}
	if line != nil {
		t.Fatalf("oversized line returned %d buffered bytes", len(line))
	}
	if source.readBytes > int64(maximumJournalRecordBytes+reader.Size()) {
		t.Fatalf("oversized line consumed %d bytes before rejection", source.readBytes)
	}
}

func writeSizedJournalLine(t *testing.T, body []byte, totalBytes int, terminated bool) *os.File {
	t.Helper()
	terminatorBytes := 0
	if terminated {
		terminatorBytes = 1
	}
	if len(body)+terminatorBytes > totalBytes {
		t.Fatalf("journal body requires %d bytes, fixture limit is %d", len(body)+terminatorBytes, totalBytes)
	}
	file, err := os.CreateTemp(t.TempDir(), "journal-*.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	line := make([]byte, totalBytes)
	copy(line, body)
	for index := len(body); index < len(line)-terminatorBytes; index++ {
		line[index] = ' '
	}
	if terminated {
		line[len(line)-1] = '\n'
	}
	if err := writeAll(file, line); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file
}

type countedByteReader struct {
	remaining int64
	readBytes int64
}

func (reader *countedByteReader) Read(body []byte) (int, error) {
	if reader.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(body))
	if count > reader.remaining {
		count = reader.remaining
	}
	for index := int64(0); index < count; index++ {
		body[index] = 'x'
	}
	reader.remaining -= count
	reader.readBytes += count
	return int(count), nil
}
