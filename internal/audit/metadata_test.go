package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditLockMetadataUsesAlternatingChecksummedSlots(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	initial, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if initial.revision != 1 || initial.slot != 0 || initial.metadata.LastSequence != 0 {
		t.Fatalf("initial metadata = revision %d slot %d sequence %d", initial.revision, initial.slot, initial.metadata.LastSequence)
	}
	if _, err := writer.Append(Event{Type: "slot-one"}); err != nil {
		t.Fatal(err)
	}
	second, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if second.revision != 2 || second.slot != 1 || second.metadata.LastSequence != 1 {
		t.Fatalf("second metadata = revision %d slot %d sequence %d", second.revision, second.slot, second.metadata.LastSequence)
	}
	if _, err := writer.Append(Event{Type: "slot-zero"}); err != nil {
		t.Fatal(err)
	}
	third, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if third.revision != 3 || third.slot != 0 || third.metadata.LastSequence != 2 {
		t.Fatalf("third metadata = revision %d slot %d sequence %d", third.revision, third.slot, third.metadata.LastSequence)
	}
	info, err := writer.lockFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maximumLockMetadataBytes {
		t.Fatalf("audit.lock size = %d, want %d", info.Size(), maximumLockMetadataBytes)
	}
}

func TestAuditOpenFallsBackFromTornNewestLockMetadataSlot(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Event{Type: "fallback-one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(Event{Type: "fallback-two"}); err != nil {
		t.Fatal(err)
	}
	active, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if active.slot != 0 || active.metadata.LastSequence != 2 {
		t.Fatalf("active metadata before tear = slot %d sequence %d", active.slot, active.metadata.LastSequence)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	lockPath := filepath.Join(directory, lockFilename)
	lock, err := os.OpenFile(lockPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.WriteAt(bytes.Repeat([]byte{0xff}, lockMetadataSlotBytes/2), 0); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	if err := lock.Sync(); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(directory)
	if err != nil {
		t.Fatalf("Open did not fall back to the previous complete metadata slot: %v", err)
	}
	defer reopened.Close()
	reconciled, err := readLockMetadataDiskState(reopened.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if reconciled.slot != 0 || reconciled.metadata.LastSequence != 2 {
		t.Fatalf("reconciled metadata = slot %d sequence %d", reconciled.slot, reconciled.metadata.LastSequence)
	}
	next, err := reopened.Append(Event{Type: "fallback-next"})
	if err != nil || next.Sequence != 3 {
		t.Fatalf("Append after metadata fallback = %#v, %v", next, err)
	}
}

func TestAuditOpenRejectsMissingEmptyOrFullyTornLockMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(string) error
	}{
		{name: "missing", mutate: os.Remove},
		{name: "empty", mutate: func(path string) error { return os.Truncate(path, 0) }},
		{name: "fully torn", mutate: func(path string) error {
			return os.WriteFile(path, []byte("partial-lock-metadata"), 0o600)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			policy := Options{MaxEvents: 4, Retention: time.Hour}
			writer, err := OpenWithOptions(directory, policy)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Append(Event{Type: "lock-loss-must-not-reset"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := test.mutate(filepath.Join(directory, lockFilename)); err != nil {
				t.Fatal(err)
			}

			opened, err := OpenWithOptions(directory, Options{MaxEvents: 1})
			if err == nil {
				_ = opened.Close()
				t.Fatal("OpenWithOptions accepted missing or invalid fixed metadata")
			}
			if !strings.Contains(err.Error(), "lock metadata") {
				t.Fatalf("OpenWithOptions error = %v, want lock metadata rejection", err)
			}
		})
	}
}

func TestAuditOpenReconcilesJournalAheadOfLockMetadata(t *testing.T) {
	t.Run("append", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		writer, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "metadata-before-append"}); err != nil {
			t.Fatal(err)
		}
		oldMetadata, err := os.ReadFile(filepath.Join(directory, lockFilename))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "journal-ahead-append"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, lockFilename), oldMetadata, 0o600); err != nil {
			t.Fatal(err)
		}

		reopened, err := Open(directory)
		if err != nil {
			t.Fatalf("Open rejected journal-ahead append recovery: %v", err)
		}
		defer reopened.Close()
		metadata, err := readLockMetadata(reopened.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.LastSequence != 2 {
			t.Fatalf("reconciled append watermark = %d, want 2", metadata.LastSequence)
		}
		next, err := reopened.Append(Event{Type: "journal-ahead-append-next"})
		if err != nil || next.Sequence != 3 {
			t.Fatalf("Append after journal-ahead recovery = %#v, %v", next, err)
		}
	})

	t.Run("compaction", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		options := Options{MaxEvents: 1, CompactionThreshold: disabledAutomaticAuditCompaction}
		writer, err := OpenWithOptions(directory, options)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "metadata-before-compaction"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Compact(t.Context()); err != nil {
			t.Fatal(err)
		}
		oldMetadata, err := os.ReadFile(filepath.Join(directory, lockFilename))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "journal-ahead-compaction"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Compact(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, lockFilename), oldMetadata, 0o600); err != nil {
			t.Fatal(err)
		}

		reopened, err := OpenWithOptions(directory, options)
		if err != nil {
			t.Fatalf("Open rejected journal-ahead compaction recovery: %v", err)
		}
		defer reopened.Close()
		metadata, err := readLockMetadata(reopened.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if metadata.Generation != 2 || metadata.LastSequence != 2 || metadata.PrunedThrough != 1 {
			t.Fatalf("reconciled compaction metadata = generation %d last %d pruned %d", metadata.Generation, metadata.LastSequence, metadata.PrunedThrough)
		}
		next, err := reopened.Append(Event{Type: "journal-ahead-compaction-next"})
		if err != nil || next.Sequence != 3 {
			t.Fatalf("Append after journal-ahead compaction recovery = %#v, %v", next, err)
		}
	})
}

func TestAuditNewJournalHeaderMatchesFixedLockStoreID(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	body, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	line, _, ok := bytes.Cut(body, []byte{'\n'})
	if !ok {
		t.Fatal("new audit journal header is incomplete")
	}
	var header auditRecord
	if err := json.Unmarshal(line, &header); err != nil {
		t.Fatal(err)
	}
	metadata, err := readLockMetadata(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if header.Kind != recordHeader || header.StoreID == "" || header.StoreID != metadata.StoreID {
		t.Fatalf("header = kind %q store %q, lock store %q", header.Kind, header.StoreID, metadata.StoreID)
	}
}

func TestAuditOpenRecoversOnlyMetadataFirstInitializationWindows(t *testing.T) {
	t.Run("pristine metadata with empty journal", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		writer, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		before, err := readLockMetadataDiskState(writer.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(directory, Filename), 0); err != nil {
			t.Fatal(err)
		}

		reopened, err := Open(directory)
		if err != nil {
			t.Fatalf("Open could not finish the pristine-metadata initialization window: %v", err)
		}
		defer reopened.Close()
		after, err := readLockMetadataDiskState(reopened.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if after.metadata.StoreID != before.metadata.StoreID || after.revision != before.revision {
			t.Fatalf("recovered metadata = store %q revision %d, want store %q revision %d",
				after.metadata.StoreID, after.revision, before.metadata.StoreID, before.revision)
		}
		if got := readAuditHeaderStoreID(t, directory); got != before.metadata.StoreID {
			t.Fatalf("recovered header StoreID = %q, want %q", got, before.metadata.StoreID)
		}
	})

	t.Run("pristine metadata with partial header", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		writer, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		before, err := readLockMetadata(writer.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		partial := []byte(`{"schema_version":1,"record_kind":"header","recorded_at":"2026-08-30T12:00:00Z","store_id":"` + before.StoreID + `"}`)
		if err := os.WriteFile(filepath.Join(directory, Filename), partial, 0o600); err != nil {
			t.Fatal(err)
		}

		reopened, err := Open(directory)
		if err != nil {
			t.Fatalf("Open could not replace a partial initial header: %v", err)
		}
		defer reopened.Close()
		if got := readAuditHeaderStoreID(t, directory); got != before.StoreID {
			t.Fatalf("recovered header StoreID = %q, want %q", got, before.StoreID)
		}
	})

	t.Run("torn first metadata slot with exact empty journal", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		writer, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Truncate(filepath.Join(directory, Filename), 0); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, lockFilename), bytes.Repeat([]byte{0xff}, 127), 0o600); err != nil {
			t.Fatal(err)
		}

		reopened, err := Open(directory)
		if err != nil {
			t.Fatalf("Open could not retry a torn first metadata slot: %v", err)
		}
		defer reopened.Close()
		disk, err := readLockMetadataDiskState(reopened.lockFile)
		if err != nil {
			t.Fatal(err)
		}
		if disk.revision != 1 || disk.slot != 0 || disk.metadata.StoreID == "" {
			t.Fatalf("replacement metadata = revision %d slot %d StoreID %q", disk.revision, disk.slot, disk.metadata.StoreID)
		}
		if got := readAuditHeaderStoreID(t, directory); got != disk.metadata.StoreID {
			t.Fatalf("replacement header StoreID = %q, want %q", got, disk.metadata.StoreID)
		}
	})
}

func TestAuditOpenRepairsIdentifiableLegacyFirstRecordTailWithBlankMetadata(t *testing.T) {
	directory := secureTestDir(t)
	body := []byte(`{"sequence":1,"timestamp":"2026-08-30T12:00:00Z"`)
	if err := os.WriteFile(filepath.Join(directory, Filename), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, lockFilename), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(directory)
	if err != nil {
		t.Fatalf("Open could not repair an identifiable legacy first-record tail: %v", err)
	}
	defer writer.Close()
	if got := readAuditHeaderStoreID(t, directory); got == "" {
		t.Fatal("repaired legacy tail did not initialize a StoreID header")
	}
}

func TestAuditOpenDoesNotPromoteHeaderWithoutValidMetadata(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body []byte
	}{
		{name: "complete header", body: nil},
		{name: "partial header", body: []byte(`{"schema_version":1,"record_kind":"header"`)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			if fixture.body != nil {
				if err := os.WriteFile(filepath.Join(directory, Filename), fixture.body, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(filepath.Join(directory, Filename))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, lockFilename), []byte("torn-first-slot"), 0o600); err != nil {
				t.Fatal(err)
			}

			if opened, err := Open(directory); err == nil {
				_ = opened.Close()
				t.Fatal("Open promoted a journal header without valid fixed metadata")
			} else if !errors.Is(err, errNoValidInitialLockMetadata) {
				t.Fatalf("Open error = %v, want torn initial metadata rejection", err)
			}
			after, err := os.ReadFile(filepath.Join(directory, Filename))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("failed Open modified a journal that had no valid fixed metadata")
			}
		})
	}
}

func TestAuditOpenRejectsPartialHeaderWithMissingOrBlankMetadataWithoutMutation(t *testing.T) {
	for _, withBlankLock := range []bool{false, true} {
		name := "missing lock"
		if withBlankLock {
			name = "blank lock"
		}
		t.Run(name, func(t *testing.T) {
			directory := secureTestDir(t)
			body := []byte(`{"schema_version":1,"record_kind":"header"`)
			path := filepath.Join(directory, Filename)
			if err := os.WriteFile(path, body, 0o600); err != nil {
				t.Fatal(err)
			}
			if withBlankLock {
				if err := os.WriteFile(filepath.Join(directory, lockFilename), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if opened, err := Open(directory); err == nil {
				_ = opened.Close()
				t.Fatal("Open promoted a partial header without fixed metadata")
			} else if !strings.Contains(err.Error(), "lock metadata is missing") {
				t.Fatalf("Open error = %v, want missing metadata rejection", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, body) {
				t.Fatal("failed Open truncated a partial header without fixed metadata")
			}
		})
	}
}

func TestAuditOpenRejectsAmbiguousPartialJournalWithPristineMetadata(t *testing.T) {
	for _, fixture := range []struct {
		name string
		body []byte
	}{
		{name: "non-header", body: []byte(`{"sequence":`)},
		{name: "different StoreID", body: []byte(`{"schema_version":1,"record_kind":"header","recorded_at":"2026-08-30T12:00:00Z","store_id":"` + strings.Repeat("c", 32) + `"}`)},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, Filename)
			if err := os.WriteFile(path, fixture.body, 0o600); err != nil {
				t.Fatal(err)
			}

			if opened, err := Open(directory); err == nil {
				_ = opened.Close()
				t.Fatal("Open truncated an ambiguous partial journal under pristine metadata")
			} else if !strings.Contains(err.Error(), "partial audit header") {
				t.Fatalf("Open error = %v, want partial header lineage rejection", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, fixture.body) {
				t.Fatal("failed Open modified an ambiguous partial journal")
			}
		})
	}
}

func TestAuditOpenRejectsLegacyJournalWithPristineMetadataWithoutMutation(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	body := []byte(validLine(1, "legacy-cannot-inherit-pristine-lineage"))
	path := filepath.Join(directory, Filename)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if opened, err := Open(directory); err == nil {
		_ = opened.Close()
		t.Fatal("Open bound a legacy journal to pristine fixed metadata")
	} else if !strings.Contains(err.Error(), "pristine audit lock metadata") {
		t.Fatalf("Open error = %v, want pristine metadata rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, body) {
		t.Fatal("failed Open modified a legacy journal paired with pristine metadata")
	}
}

func TestAuditOpenRejectsDifferentHeaderLineageBeforeTailRepair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	header, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion,
		Kind:          recordHeader,
		RecordedAt:    time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC),
		StoreID:       strings.Repeat("d", 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := append(header, []byte(`{"schema_version":1`)...)
	path := filepath.Join(directory, Filename)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if opened, err := Open(directory); err == nil {
		_ = opened.Close()
		t.Fatal("Open accepted a header from a different fixed lineage")
	} else if !strings.Contains(err.Error(), "different store") {
		t.Fatalf("Open error = %v, want lineage rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, body) {
		t.Fatal("failed Open truncated an untrusted different-lineage tail")
	}
}

func TestAuditOpenRejectsHeaderlessEnvelopeBeforeTailRepair(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	writer, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recordedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	event := Event{Sequence: 1, Timestamp: recordedAt, Type: "headerless-envelope"}
	line, err := marshalAuditRecord(auditRecord{
		SchemaVersion: journalSchemaVersion,
		Kind:          recordEvent,
		RecordedAt:    recordedAt,
		Event:         &event,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := append(line, []byte(`{"schema_version":1`)...)
	path := filepath.Join(directory, Filename)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	if opened, err := Open(directory); err == nil {
		_ = opened.Close()
		t.Fatal("Open accepted an event envelope without a header")
	} else if !strings.Contains(err.Error(), "before a header") {
		t.Fatalf("Open error = %v, want pre-repair header rejection", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, body) {
		t.Fatal("failed Open truncated a headerless envelope tail")
	}
}

func TestAuditOpenMigratesLegacySingleLineLockMetadata(t *testing.T) {
	directory := secureTestDir(t)
	storeID := strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(directory, Filename), []byte(validLine(1, "legacy-lock-marker")), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyMetadata, err := json.Marshal(lockMetadata{
		SchemaVersion: journalSchemaVersion,
		StoreID:       storeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyMetadata = append(legacyMetadata, '\n')
	if err := os.WriteFile(filepath.Join(directory, lockFilename), legacyMetadata, 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(directory)
	if err != nil {
		t.Fatalf("Open could not migrate single-line lock metadata: %v", err)
	}
	defer writer.Close()
	disk, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if disk.legacy || disk.revision != 1 || disk.slot != 1 || disk.metadata.StoreID != storeID || disk.metadata.LastSequence != 1 {
		t.Fatalf("migrated metadata = legacy %t revision %d slot %d store %q sequence %d",
			disk.legacy, disk.revision, disk.slot, disk.metadata.StoreID, disk.metadata.LastSequence)
	}
	next, err := writer.Append(Event{Type: "after-legacy-lock-migration"})
	if err != nil || next.Sequence != 2 {
		t.Fatalf("Append after legacy lock migration = %#v, %v", next, err)
	}
}

func TestAuditOpenFallsBackToLegacyMetadataDuringTornSlotMigration(t *testing.T) {
	directory := secureTestDir(t)
	storeID := strings.Repeat("b", 32)
	if err := os.WriteFile(filepath.Join(directory, Filename), []byte(validLine(1, "legacy-torn-slot-marker")), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyMetadata, err := json.Marshal(lockMetadata{
		SchemaVersion: journalSchemaVersion,
		StoreID:       storeID,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyMetadata = append(legacyMetadata, '\n')
	lockPath := filepath.Join(directory, lockFilename)
	if err := os.WriteFile(lockPath, legacyMetadata, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(lockPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.WriteAt([]byte(`{"revision":1`), lockMetadataSlotBytes); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	if err := lock.Sync(); err != nil {
		_ = lock.Close()
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}

	writer, err := Open(directory)
	if err != nil {
		t.Fatalf("Open could not fall back to intact legacy metadata: %v", err)
	}
	defer writer.Close()
	disk, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if disk.legacy || disk.revision != 1 || disk.slot != 1 || disk.metadata.StoreID != storeID || disk.metadata.LastSequence != 1 {
		t.Fatalf("migrated metadata = legacy %t revision %d slot %d store %q sequence %d",
			disk.legacy, disk.revision, disk.slot, disk.metadata.StoreID, disk.metadata.LastSequence)
	}
}

func TestAuditOpenRetriesTornFirstMetadataSlotForLegacyJournal(t *testing.T) {
	directory := secureTestDir(t)
	if err := os.WriteFile(filepath.Join(directory, Filename), []byte(validLine(1, "legacy-first-slot-torn")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, lockFilename), []byte("torn-first-slot"), 0o600); err != nil {
		t.Fatal(err)
	}

	writer, err := OpenWithOptions(directory, Options{MaxEvents: 3})
	if err != nil {
		t.Fatalf("Open could not retry the first metadata slot for a validated legacy journal: %v", err)
	}
	defer writer.Close()
	disk, err := readLockMetadataDiskState(writer.lockFile)
	if err != nil {
		t.Fatal(err)
	}
	if disk.revision != 1 || disk.slot != 0 || disk.metadata.LastSequence != 1 ||
		disk.metadata.Retention == nil || disk.metadata.Retention.MaxEvents != 3 {
		t.Fatalf("recovered legacy metadata = revision %d slot %d sequence %d retention %#v",
			disk.revision, disk.slot, disk.metadata.LastSequence, disk.metadata.Retention)
	}
	next, err := writer.Append(Event{Type: "after-legacy-first-slot-retry"})
	if err != nil || next.Sequence != 2 {
		t.Fatalf("Append after legacy first-slot retry = %#v, %v", next, err)
	}
}

func readAuditHeaderStoreID(t *testing.T, directory string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(directory, Filename))
	if err != nil {
		t.Fatal(err)
	}
	line, _, ok := bytes.Cut(body, []byte{'\n'})
	if !ok {
		t.Fatalf("audit journal header is incomplete: %q", body)
	}
	var header auditRecord
	if err := json.Unmarshal(line, &header); err != nil {
		t.Fatal(err)
	}
	if header.Kind != recordHeader {
		t.Fatalf("first audit record kind = %q, want %q", header.Kind, recordHeader)
	}
	return header.StoreID
}

func TestAuditOpenRejectsMissingOrEmptyInitializedJournal(t *testing.T) {
	tests := []struct {
		name      string
		wantError string
		mutate    func(string) error
	}{
		{
			name:      "missing",
			wantError: "missing",
			mutate:    os.Remove,
		},
		{
			name:      "empty",
			wantError: "empty",
			mutate: func(path string) error {
				return os.Truncate(path, 0)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "audit")
			writer, err := Open(directory)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Append(Event{Type: "acknowledged-before-loss"}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(directory, Filename)
			if err := test.mutate(path); err != nil {
				t.Fatal(err)
			}
			opened, err := Open(directory)
			if err == nil {
				_ = opened.Close()
				t.Fatalf("Open accepted an initialized %s journal", test.name)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Open error = %v, want %q", err, test.wantError)
			}
			if test.name == "missing" {
				if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("failed Open recreated missing journal: %v", statErr)
				}
			}
		})
	}
}

func TestAuditOpenRejectsAcknowledgedJournalRollbackAfterRestart(t *testing.T) {
	t.Run("same generation prefix", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		path := filepath.Join(directory, Filename)
		writer, err := Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "prefix-one"}); err != nil {
			t.Fatal(err)
		}
		prefix, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "acknowledged-two"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, prefix, 0o600); err != nil {
			t.Fatal(err)
		}

		if opened, err := Open(directory); err == nil {
			_ = opened.Close()
			t.Fatal("Open accepted a same-generation acknowledged prefix rollback")
		} else if !strings.Contains(err.Error(), "fixed lock watermark") {
			t.Fatalf("Open rollback error = %v, want fixed lock watermark rejection", err)
		}
	})

	t.Run("older sealed generation", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "audit")
		path := filepath.Join(directory, Filename)
		options := Options{MaxEvents: 1, CompactionThreshold: disabledAutomaticAuditCompaction}
		writer, err := OpenWithOptions(directory, options)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "generation-one"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Compact(t.Context()); err != nil {
			t.Fatal(err)
		}
		older, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Append(Event{Type: "generation-two"}); err != nil {
			t.Fatal(err)
		}
		if err := writer.Compact(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, older, 0o600); err != nil {
			t.Fatal(err)
		}

		if opened, err := OpenWithOptions(directory, options); err == nil {
			_ = opened.Close()
			t.Fatal("Open accepted an older acknowledged sealed generation")
		} else if !strings.Contains(err.Error(), "fixed lock watermark") {
			t.Fatalf("Open generation rollback error = %v, want fixed lock watermark rejection", err)
		}
	})
}

func TestAuditRetentionPolicyIsPinnedAcrossWriters(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "audit")
	unconfigured, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer unconfigured.Close()

	policy := Options{
		MaxEvents:           5,
		Retention:           24 * time.Hour,
		CompactionThreshold: disabledAutomaticAuditCompaction,
	}
	configured, err := OpenWithOptions(directory, policy)
	if err != nil {
		t.Fatalf("first configured Writer could not claim the retention policy: %v", err)
	}
	defer configured.Close()

	if _, err := unconfigured.Append(Event{Type: "stale-policy-must-fail"}); !errors.Is(err, ErrUnavailable) ||
		!strings.Contains(err.Error(), "retention policy changed") {
		t.Fatalf("stale policy Append error = %v, want fail-closed policy mismatch", err)
	}
	first, err := configured.Append(Event{Type: "configured-policy-writes"})
	if err != nil || first.Sequence != 1 {
		t.Fatalf("configured policy Append = %#v, %v", first, err)
	}

	matching, err := OpenWithOptions(directory, policy)
	if err != nil {
		t.Fatalf("matching retention policy was rejected: %v", err)
	}
	if err := matching.Close(); err != nil {
		t.Fatal(err)
	}

	for name, mismatch := range map[string]Options{
		"disabled":           {},
		"different count":    {MaxEvents: 4, Retention: 24 * time.Hour},
		"different duration": {MaxEvents: 5, Retention: 48 * time.Hour},
	} {
		t.Run(name, func(t *testing.T) {
			opened, err := OpenWithOptions(directory, mismatch)
			if err == nil {
				_ = opened.Close()
				t.Fatal("OpenWithOptions accepted a mismatched retention policy")
			}
			if !strings.Contains(err.Error(), "retention policy") {
				t.Fatalf("policy mismatch error = %v", err)
			}
		})
	}
}
