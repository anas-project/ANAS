package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/securefs"
	"gopkg.in/yaml.v3"
)

func writeContainerTransaction(base string, txn *containerTransaction) error {
	return writeContainerTransactionWithSync(base, txn, func(file *os.File) error { return file.Sync() })
}

// Cleanup intent must survive a machine crash, not only process termination.
// Keep this stronger contract local to the backup journal: generic YAML
// snapshots historically have different creation and compatibility rules.
// The sync argument is an in-process fault-injection seam, never request data.
func writeContainerTransactionWithSync(base string, txn *containerTransaction, sync func(*os.File) error) error {
	if txn == nil || sync == nil || !filepath.IsAbs(base) || filepath.Clean(base) != base ||
		txn.ID == "" || txn.ID == "." || txn.ID == ".." || filepath.Base(txn.ID) != txn.ID {
		return errors.New("invalid backup transaction destination")
	}
	body, err := yaml.Marshal(txn)
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("backup transaction exceeds its size limit")
	}
	name := transactionPath(base, txn.ID)
	parent := filepath.Dir(name)
	dir, created, err := securefs.OpenDirectory(parent, "backup transaction journal")
	if err != nil {
		return err
	}
	defer dir.Close()
	previous, err := os.Lstat(name)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if previous != nil {
		if err := securefs.ValidateFileInfo(previous, "backup transaction"); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(parent, ".anas-backup-transaction-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	identity, statErr := file.Stat()
	defer func() {
		_ = file.Close()
		if current, err := os.Lstat(temporary); statErr == nil && err == nil && os.SameFile(identity, current) {
			_ = os.Remove(temporary)
		}
	}()
	if statErr != nil {
		return statErr
	}
	if err := file.Chmod(0600); err != nil {
		return err
	}
	if err := securefs.WriteAll(file, body); err != nil {
		return err
	}
	if err := sync(file); err != nil {
		return fmt.Errorf("sync backup transaction data: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := securefs.VerifyOpenDirectory(dir, parent, "backup transaction journal"); err != nil {
		return err
	}
	current, currentErr := os.Lstat(name)
	if previous == nil {
		if !errors.Is(currentErr, os.ErrNotExist) {
			return errors.New("backup transaction appeared during publication")
		}
	} else {
		if currentErr != nil || !os.SameFile(previous, current) || previous.Size() != current.Size() ||
			!previous.ModTime().Equal(current.ModTime()) {
			return errors.New("backup transaction changed during publication")
		}
		if err := securefs.ValidateFileInfo(current, "backup transaction"); err != nil {
			return err
		}
	}
	if err := os.Rename(temporary, name); err != nil {
		return err
	}
	// Rename alone does not make the new name durable. A failed sync still
	// prevents the caller from invoking the Hook; any published pending record
	// remains intact for conservative recovery, rather than being rolled back.
	if err := sync(dir); err != nil {
		return fmt.Errorf("sync backup transaction directory: %w", err)
	}
	if err := securefs.SyncCreatedDirectoryEntries(created); err != nil {
		return err
	}
	return securefs.VerifyOpenDirectory(dir, parent, "backup transaction journal")
}
