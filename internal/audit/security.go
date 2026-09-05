package audit

import (
	"context"
	"io"
	"os"

	"github.com/anas-project/ANAS/internal/securefs"
)

// storeLabel names this store in every filesystem-discipline error, so the
// shared helpers still produce messages that point at the audit store.
const storeLabel = "audit directory"

// logFile is the descriptor contract the journal and compaction paths use.
// Tests substitute a fault-injecting implementation.
type logFile = securefs.File

const crossProcessAuditLockSupported = securefs.CrossProcessLockSupported

func lockAuditFile(ctx context.Context, done <-chan struct{}, file *os.File) error {
	return securefs.Lock(ctx, done, file, errWriterClosed)
}

func unlockAuditFile(file *os.File) error { return securefs.Unlock(file) }

func tryAuditLock(file *os.File) (bool, error) { return securefs.TryLock(file) }

func openSecureDirectory(dir string) (*os.File, []string, error) {
	return securefs.OpenDirectory(dir, storeLabel)
}

func missingDirectoryEntries(path string) ([]string, error) {
	return securefs.MissingDirectoryEntries(path, storeLabel)
}

func openSecureNamedFile(path, name string) (*os.File, bool, error) {
	return securefs.OpenNamedFile(path, name)
}

// The lock file rewrites two fixed slots with WriteAt, so it must not be opened
// with O_APPEND.
func openSecureLockFile(path, name string) (*os.File, bool, error) {
	return securefs.OpenNamedFileForRandomAccess(path, name)
}

func openExistingSecureNamedFile(path, name string) (*os.File, error) {
	return securefs.OpenExistingNamedFile(path, name)
}

func createExclusiveSecureNamedFile(path, name string) (*os.File, error) {
	return securefs.CreateExclusiveNamedFile(path, name)
}

func validateSecureDirectoryInfo(info os.FileInfo) error {
	return securefs.ValidateDirectoryInfo(info, storeLabel)
}

func validateSecureFileInfo(info os.FileInfo, name string) error {
	return securefs.ValidateFileInfo(info, name)
}

func validateCurrentOwner(info os.FileInfo, name string) error {
	return securefs.ValidateCurrentOwner(info, name)
}

func validateSingleLink(info os.FileInfo, name string) error {
	return securefs.ValidateSingleLink(info, name)
}

func verifyOpenDirectory(directory *os.File, path string) error {
	return securefs.VerifyOpenDirectory(directory, path, storeLabel)
}

func verifyOpenNamedFile(file logFile, path, name string) error {
	return securefs.VerifyOpenNamedFile(file, path, name)
}

func openFileNamesPath(file logFile, path, name string) (bool, error) {
	return securefs.OpenFileNamesPath(file, path, name)
}

func syncParentDirectory(path string) error {
	return securefs.SyncParentDirectory(path)
}

func syncCreatedDirectoryEntries(entries []string) error {
	return securefs.SyncCreatedDirectoryEntries(entries)
}

func writeAll(writer io.Writer, body []byte) error {
	return securefs.WriteAll(writer, body)
}
