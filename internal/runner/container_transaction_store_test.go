package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupPendingDoesNotReusePredictableTemporaryFile(t *testing.T) {
	base := t.TempDir()
	txn := &containerTransaction{APIVersion: activeStateVersion, ID: "guarded", Kind: containerTransactionKind,
		State: containerTransactionStopped, Modules: []string{"worker"}}
	path := transactionPath(base, txn.ID)
	if err := writeYAMLAtomic(path, txn, 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "must-not-change")
	if err := os.WriteFile(target, []byte("unrelated-file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := txn.setCleanupPending(base, "worker"); err != nil {
		t.Fatal(err)
	}
	if body, err := os.ReadFile(target); err != nil || string(body) != "unrelated-file" {
		t.Fatal("backup journal followed the predictable temporary name into another file")
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		t.Fatal("cleanup marker is not an independent private regular file", err)
	}
	if info, err := os.Lstat(path + ".tmp"); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("unowned historical temporary entry was removed or overwritten", err)
	}
}

func TestContainerJournalSyncsDataThenPublishedDirectory(t *testing.T) {
	base := t.TempDir()
	txn := &containerTransaction{APIVersion: activeStateVersion, ID: "sync-order", Kind: containerTransactionKind,
		State: containerTransactionCleanupPending, CleanupPending: "worker"}
	stages := []string{}
	err := writeContainerTransactionWithSync(base, txn, func(file *os.File) error {
		info, err := file.Stat()
		if err != nil {
			return err
		}
		if info.IsDir() {
			stages = append(stages, "directory")
			var current containerTransaction
			if err := readYAML(transactionPath(base, txn.ID), &current); err != nil || current.State != containerTransactionCleanupPending {
				t.Fatal("directory sync preceded publication of the pending record", err)
			}
		} else {
			stages = append(stages, "file")
			if _, err := os.Lstat(transactionPath(base, txn.ID)); !os.IsNotExist(err) {
				t.Fatal("record was published before data synchronization")
			}
		}
		return file.Sync()
	})
	if err != nil || strings.Join(stages, ",") != "file,directory" {
		t.Fatal("journal durability ordering is incomplete", err, stages)
	}
}

func TestContainerJournalSyncFailureCannotStartCleanup(t *testing.T) {
	for _, failDirectory := range []bool{false, true} {
		a, release, log := stopBarrierFixture(t, false)
		txn := &containerTransaction{APIVersion: activeStateVersion, ID: "sync-failure", Kind: containerTransactionKind,
			State: containerTransactionCleanupPending, CleanupPending: "worker"}
		failure := errors.New("injected storage sync failure")
		err := stopModulesWithCleanupMarker(a, release, []string{"worker"}, func(module string) error {
			if module != "worker" {
				t.Fatal("unconfirmed journal permitted cleanup completion")
			}
			return writeContainerTransactionWithSync(a.base, txn, func(file *os.File) error {
				info, err := file.Stat()
				if err != nil {
					return err
				}
				if info.IsDir() == failDirectory {
					return failure
				}
				return file.Sync()
			})
		})
		if !errors.Is(err, failure) {
			t.Fatal("storage sync failure did not stop the backup operation", err)
		}
		if _, err := os.Stat(log); !os.IsNotExist(err) {
			t.Fatal("Hook or Compose executed without a durable cleanup marker")
		}
		if failDirectory {
			// Publication may have happened despite the failed directory sync.
			// Preserve that pending evidence and refuse recovery on reopen.
			if err := compensateContainerTransactionsWithOptions(a.base, runtimeRecoveryOptions{}); err == nil {
				t.Fatal("reopen discarded a published but unconfirmed cleanup record")
			}
		}
	}
}

func TestContainerJournalRejectsUnownedPublicationEntries(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "public-file", "directory-link"} {
		t.Run(kind, func(t *testing.T) {
			base := t.TempDir()
			root := transactionsDir(base)
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			txn := &containerTransaction{ID: "protected", State: containerTransactionStopped}
			name := transactionPath(base, txn.ID)
			target := filepath.Join(t.TempDir(), "unrelated")
			if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(target, name); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, name); err != nil {
					t.Fatal(err)
				}
			case "public-file":
				if err := os.WriteFile(name, []byte("unchanged"), 0644); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.Remove(root); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Dir(target), root); err != nil {
					t.Fatal(err)
				}
			}
			if err := txn.setCleanupPending(base, "worker"); err == nil || txn.State != containerTransactionStopped || txn.CleanupPending != "" {
				t.Fatal("unsafe publication established a pending transaction or changed caller state", err)
			}
			if body, err := os.ReadFile(target); err != nil || string(body) != "unchanged" {
				t.Fatal("unsafe publication altered an unrelated file", err)
			}
		})
	}
}
