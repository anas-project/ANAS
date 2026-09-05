package consolejobs

import (
	"io"
	"os"

	"github.com/anas-project/ANAS/internal/securefs"
)

// storeLabel names this store in every filesystem-discipline error, so the
// shared helpers still produce messages that point at the job store.
const storeLabel = "job store"

// journalFile is the descriptor contract the journal and compaction paths use.
// Tests substitute a fault-injecting implementation.
type journalFile = securefs.File

func openSecureDirectory(dir string) (*os.File, []string, error) {
	return securefs.OpenDirectory(dir, storeLabel)
}

func missingDirectoryEntries(path string) ([]string, error) {
	return securefs.MissingDirectoryEntries(path, storeLabel)
}

func openSecureNamedFile(path, name string) (*os.File, bool, error) {
	return securefs.OpenNamedFile(path, name)
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

func verifyOpenNamedFile(file journalFile, path, name string) error {
	return securefs.VerifyOpenNamedFile(file, path, name)
}

func openFileNamesPath(file journalFile, path, name string) (bool, error) {
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
