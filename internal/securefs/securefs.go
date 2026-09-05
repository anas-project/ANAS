// Package securefs holds the 0600/0700, single-link, current-owner filesystem
// discipline shared by the console's durable stores.
//
// The job store and the audit journal both need the same guarantees: a
// directory and its files must never be a symlink, never have a second hard
// link, always belong to the current effective user, and always be verified
// through the descriptor that was actually opened rather than through a path
// another process could swap underneath. Both packages previously carried their
// own copy of these helpers, which had already begun to drift; keeping one
// implementation is what stops a fix landing in one store and not the other.
//
// Every helper takes the caller's own label so error text still names the store
// the operator is looking at.
package securefs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// File is the descriptor contract the durable journals need. Both stores use
// concrete *os.File in production and a fault-injecting fake in tests.
type File interface {
	io.Reader
	io.Writer
	io.Seeker
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

// ValidateDirectoryInfo requires a non-symlink directory owned by the current
// effective user with exactly mode 0700.
func ValidateDirectoryInfo(info os.FileInfo, label string) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s must be a non-symlink directory", label)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s mode is %04o, want 0700", label, info.Mode().Perm())
	}
	return ValidateCurrentOwner(info, label)
}

// ValidateFileInfo requires a non-symlink regular file owned by the current
// effective user, with exactly mode 0600 and exactly one hard link. The
// link-count check is what keeps a second name from surviving an atomic
// replacement of the canonical path.
func ValidateFileInfo(info os.FileInfo, name string) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a non-symlink regular file", name)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode is %04o, want 0600", name, info.Mode().Perm())
	}
	if err := ValidateCurrentOwner(info, name); err != nil {
		return err
	}
	return ValidateSingleLink(info, name)
}

// VerifyOpenDirectory validates both the opened descriptor and the current path
// entry, then requires them to be the same inode.
func VerifyOpenDirectory(directory *os.File, path, label string) error {
	if directory == nil {
		return fmt.Errorf("%s descriptor is unavailable", label)
	}
	opened, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", label, err)
	}
	if err := ValidateDirectoryInfo(opened, label); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect %s: %w", label, err)
	}
	if err := ValidateDirectoryInfo(current, label); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("%s path entry no longer names the opened directory", label)
	}
	return nil
}

// VerifyOpenNamedFile is VerifyOpenDirectory's file counterpart.
func VerifyOpenNamedFile(file File, path, name string) error {
	if file == nil {
		return fmt.Errorf("%s descriptor is unavailable", name)
	}
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", name, err)
	}
	if err := ValidateFileInfo(opened, name); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect %s: %w", name, err)
	}
	if err := ValidateFileInfo(current, name); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("%s path entry no longer names the opened file", name)
	}
	return nil
}

// OpenFileNamesPath reports whether an already-open descriptor is still the
// inode the path names, validating the path entry before answering.
func OpenFileNamesPath(file File, path, name string) (bool, error) {
	opened, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect opened %s: %w", name, err)
	}
	current, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect canonical %s: %w", name, err)
	}
	if err := ValidateFileInfo(current, name); err != nil {
		return false, err
	}
	return os.SameFile(opened, current), nil
}

// OpenDirectory opens or creates the store directory and returns the entries it
// had to create, leaf last, so the caller can fsync them in the right order.
func OpenDirectory(dir, label string) (*os.File, []string, error) {
	info, err := os.Lstat(dir)
	created := false
	var createdEntries []string
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(dir)
		createdEntries, err = MissingDirectoryEntries(parent, label)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create %s parent: %w", label, err)
		}
		if err := os.Mkdir(dir, 0o700); err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("create %s: %w", label, err)
		}
		// The final entry was absent when creation began. Sync it even when a
		// concurrent secure opener won the Mkdir race.
		createdEntries = append(createdEntries, dir)
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect %s: %w", label, err)
	}
	if created {
		// A directory this call just created has not been chmodded yet, so the
		// full 0700 check would fail against the process umask.
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, nil, fmt.Errorf("%s must be a non-symlink directory", label)
		}
		if err := ValidateCurrentOwner(info, label); err != nil {
			return nil, nil, err
		}
	} else if err := ValidateDirectoryInfo(info, label); err != nil {
		return nil, nil, err
	}

	directory, err := os.Open(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", label, err)
	}
	closeOnError := func(err error) (*os.File, []string, error) {
		_ = directory.Close()
		return nil, nil, err
	}
	if created {
		// Apply the exact mode through the descriptor we just created, never
		// through a path that another process may have replaced.
		if err := directory.Chmod(0o700); err != nil {
			return closeOnError(fmt.Errorf("secure %s: %w", label, err))
		}
	}
	if err := VerifyOpenDirectory(directory, dir, label); err != nil {
		return closeOnError(err)
	}
	return directory, createdEntries, nil
}

// MissingDirectoryEntries lists the absent ancestors of path, root first.
func MissingDirectoryEntries(path, label string) ([]string, error) {
	var leafToRoot []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect %s parent: %w", label, err)
		}
		leafToRoot = append(leafToRoot, path)
		parent := filepath.Dir(path)
		if parent == path {
			return nil, fmt.Errorf("no existing ancestor for %s %s", label, path)
		}
		path = parent
	}
	entries := make([]string, len(leafToRoot))
	for index := range leafToRoot {
		entries[len(leafToRoot)-1-index] = leafToRoot[index]
	}
	return entries, nil
}

// OpenNamedFile opens an append-only journal, creating it when absent, and
// reports whether this call created it.
func OpenNamedFile(path, name string) (*os.File, bool, error) {
	return openNamedFile(path, name, true)
}

// OpenNamedFileForRandomAccess is the same, without O_APPEND. A slotted lock
// file rewrites fixed offsets with WriteAt, which O_APPEND would silently
// redirect to the end of the file.
func OpenNamedFileForRandomAccess(path, name string) (*os.File, bool, error) {
	return openNamedFile(path, name, false)
}

func openNamedFile(path, name string, appendWrites bool) (*os.File, bool, error) {
	for {
		info, err := os.Lstat(path)
		created := false
		switch {
		case errors.Is(err, os.ErrNotExist):
			created = true
		case err != nil:
			return nil, false, fmt.Errorf("inspect %s: %w", name, err)
		default:
			if err := ValidateFileInfo(info, name); err != nil {
				return nil, false, err
			}
		}
		flags := os.O_RDWR
		if appendWrites {
			flags |= os.O_APPEND
		}
		if created {
			flags |= os.O_CREATE | os.O_EXCL
		}
		file, err := os.OpenFile(path, flags, 0o600)
		if created && errors.Is(err, os.ErrExist) {
			// Another secure opener won the create race. Reinspect its result
			// rather than weakening O_EXCL or trusting the raced path.
			continue
		}
		if err != nil {
			return nil, false, fmt.Errorf("open %s: %w", name, err)
		}
		closeOnError := func(err error) (*os.File, bool, error) {
			_ = file.Close()
			return nil, false, err
		}
		if created {
			if err := file.Chmod(0o600); err != nil {
				return closeOnError(fmt.Errorf("secure %s: %w", name, err))
			}
		}
		if err := VerifyOpenNamedFile(file, path, name); err != nil {
			return closeOnError(err)
		}
		if created {
			if err := file.Sync(); err != nil {
				return closeOnError(fmt.Errorf("sync new %s: %w", name, err))
			}
		}
		return file, created, nil
	}
}

// OpenExistingNamedFile refuses to create the file, so a caller that requires
// an existing journal cannot quietly start a new one.
func OpenExistingNamedFile(path, name string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", name, err)
	}
	if err := ValidateFileInfo(info, name); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	if err := VerifyOpenNamedFile(file, path, name); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

// CreateExclusiveNamedFile fails when the path already exists, which is what
// makes a compaction temp file safe to write.
func CreateExclusiveNamedFile(path, name string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	closeOnError := func(cause error) (*os.File, error) {
		_ = file.Close()
		return nil, cause
	}
	if err := file.Chmod(0o600); err != nil {
		return closeOnError(fmt.Errorf("secure %s: %w", name, err))
	}
	if err := VerifyOpenNamedFile(file, path, name); err != nil {
		return closeOnError(err)
	}
	return file, nil
}

// SyncParentDirectory makes a rename or create durable.
func SyncParentDirectory(path string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent of %s: %w", path, err)
	}
	if err := parent.Sync(); err != nil {
		_ = parent.Close()
		return fmt.Errorf("sync parent of %s: %w", path, err)
	}
	return parent.Close()
}

// SyncCreatedDirectoryEntries fsyncs leaf first so no entry becomes durable
// before the directory that contains it.
func SyncCreatedDirectoryEntries(entries []string) error {
	for index := len(entries) - 1; index >= 0; index-- {
		if err := SyncParentDirectory(entries[index]); err != nil {
			return err
		}
	}
	return nil
}

// WriteAll loops until the whole buffer is written, since a short write with a
// nil error would otherwise truncate a journal record.
func WriteAll(file io.Writer, body []byte) error {
	for len(body) > 0 {
		written, err := file.Write(body)
		if written < 0 || written > len(body) {
			return errors.New("writer reported an impossible write length")
		}
		body = body[written:]
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
