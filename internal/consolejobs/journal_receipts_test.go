package consolejobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalReceiptLogIsBoundedAndExpiresToFullRecovery(t *testing.T) {
	log := newJournalReceiptLog()
	snapshots := make([]journalSnapshot, journalAppendReceiptsLimit+2)
	base := time.Date(2026, time.August, 29, 1, 0, 0, 0, time.UTC)
	for index := range snapshots {
		snapshots[index] = journalSnapshot{
			completeBytes: int64(index + 1),
			modTime:       base.Add(time.Duration(index) * time.Nanosecond),
			changeSeconds: int64(index + 10),
			changeNanos:   int64(index + 20),
			changeKnown:   true,
		}
	}
	for index := 1; index < len(snapshots); index++ {
		log.record(snapshots[index-1], snapshots[index])
	}
	if got := len(log.receipts); got != journalAppendReceiptsLimit {
		t.Fatalf("receipt count = %d, want strict limit %d", got, journalAppendReceiptsLimit)
	}
	if log.confirmsAppendChain(snapshots[0], snapshots[len(snapshots)-1]) {
		t.Fatal("expired receipt chain was incorrectly accepted")
	}
	if !log.confirmsAppendChain(snapshots[1], snapshots[len(snapshots)-1]) {
		t.Fatal("retained receipt chain was not accepted")
	}
}

func TestJournalReceiptRegistryBindsFileAndStoreIdentityAndCleansUp(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "journal")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}

	first := registerJournalReceiptHandle(path, "store-a", info)
	second := registerJournalReceiptHandle(path, "store-a", info)
	differentStore := registerJournalReceiptHandle(path, "store-b", info)
	if first.coordinator != second.coordinator {
		t.Fatal("same file/store identity did not share a receipt coordinator")
	}
	if first.coordinator == differentStore.coordinator {
		t.Fatal("different store identity shared a receipt coordinator")
	}
	journalReceiptRegistry.Lock()
	if got := len(journalReceiptRegistry.byPath[path]); got != 2 {
		journalReceiptRegistry.Unlock()
		t.Fatalf("registered coordinator count = %d, want 2", got)
	}
	journalReceiptRegistry.Unlock()

	first.close()
	first.close()
	second.close()
	differentStore.close()
	journalReceiptRegistry.Lock()
	_, remains := journalReceiptRegistry.byPath[path]
	journalReceiptRegistry.Unlock()
	if remains {
		t.Fatal("receipt coordinators remained after their last handles closed")
	}
}

func TestJournalReceiptRegistrySeparatesReplacedFileAtSamePath(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "journal")
	oldPath := filepath.Join(directory, "journal.old")
	oldFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	oldInfo, err := oldFile.Stat()
	if err != nil {
		_ = oldFile.Close()
		t.Fatal(err)
	}
	if err := oldFile.Close(); err != nil {
		t.Fatal(err)
	}
	oldHandle := registerJournalReceiptHandle(path, "store-a", oldInfo)
	defer oldHandle.close()
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	newFile, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	newInfo, err := newFile.Stat()
	if err != nil {
		_ = newFile.Close()
		t.Fatal(err)
	}
	if err := newFile.Close(); err != nil {
		t.Fatal(err)
	}
	newHandle := registerJournalReceiptHandle(path, "store-a", newInfo)
	defer newHandle.close()
	if oldHandle.coordinator == newHandle.coordinator {
		t.Fatal("replacement journal inherited receipts from the old file identity")
	}
}

func TestStoreReceiptRegistrationLifecycle(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "jobs")
	first, err := Open(directory, Options{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(directory, Options{})
	if err != nil {
		_ = first.Close()
		t.Fatal(err)
	}
	if first.receipts == nil || second.receipts == nil || first.receipts.coordinator != second.receipts.coordinator {
		t.Fatal("open Stores did not share the journal receipt coordinator")
	}
	path := first.journalPath
	if err := first.Close(); err != nil {
		_ = second.Close()
		t.Fatal(err)
	}
	journalReceiptRegistry.Lock()
	countAfterFirstClose := len(journalReceiptRegistry.byPath[path])
	journalReceiptRegistry.Unlock()
	if countAfterFirstClose != 1 {
		_ = second.Close()
		t.Fatalf("coordinator count after first Close = %d, want 1", countAfterFirstClose)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	journalReceiptRegistry.Lock()
	_, remains := journalReceiptRegistry.byPath[path]
	journalReceiptRegistry.Unlock()
	if remains {
		t.Fatal("Store Close did not unregister the last receipt coordinator")
	}
}
