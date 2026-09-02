package consolejobs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type journalFile interface {
	io.Reader
	io.Writer
	io.Seeker
	Stat() (os.FileInfo, error)
	Sync() error
	Truncate(int64) error
	Close() error
}

func openSecureDirectory(dir string) (*os.File, []string, error) {
	info, err := os.Lstat(dir)
	created := false
	var createdEntries []string
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(dir)
		createdEntries, err = missingDirectoryEntries(parent)
		if err != nil {
			return nil, nil, err
		}
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return nil, nil, fmt.Errorf("create job store parent: %w", err)
		}
		if err := os.Mkdir(dir, 0o700); err == nil {
			created = true
		} else if !errors.Is(err, os.ErrExist) {
			return nil, nil, fmt.Errorf("create job store: %w", err)
		}
		createdEntries = append(createdEntries, dir)
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("inspect job store: %w", err)
	}
	if created {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, nil, errors.New("job store must be a non-symlink directory")
		}
		if err := validateCurrentOwner(info, "job store"); err != nil {
			return nil, nil, err
		}
	} else if err := validateSecureDirectoryInfo(info); err != nil {
		return nil, nil, err
	}

	directory, err := os.Open(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("open job store: %w", err)
	}
	closeOnError := func(err error) (*os.File, []string, error) {
		_ = directory.Close()
		return nil, nil, err
	}
	if created {
		if err := directory.Chmod(0o700); err != nil {
			return closeOnError(fmt.Errorf("secure job store: %w", err))
		}
	}
	if err := verifyOpenDirectory(directory, dir); err != nil {
		return closeOnError(err)
	}
	return directory, createdEntries, nil
}

func missingDirectoryEntries(path string) ([]string, error) {
	var leafToRoot []string
	for {
		_, err := os.Lstat(path)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect job store parent: %w", err)
		}
		leafToRoot = append(leafToRoot, path)
		parent := filepath.Dir(path)
		if parent == path {
			return nil, fmt.Errorf("no existing ancestor for job store %s", path)
		}
		path = parent
	}
	entries := make([]string, len(leafToRoot))
	for index := range leafToRoot {
		entries[len(leafToRoot)-1-index] = leafToRoot[index]
	}
	return entries, nil
}

func openSecureNamedFile(path, name string) (*os.File, bool, error) {
	for {
		info, err := os.Lstat(path)
		created := false
		switch {
		case errors.Is(err, os.ErrNotExist):
			created = true
		case err != nil:
			return nil, false, fmt.Errorf("inspect %s: %w", name, err)
		default:
			if err := validateSecureFileInfo(info, name); err != nil {
				return nil, false, err
			}
		}
		flags := os.O_RDWR | os.O_APPEND
		if created {
			flags |= os.O_CREATE | os.O_EXCL
		}
		file, err := os.OpenFile(path, flags, 0o600)
		if created && errors.Is(err, os.ErrExist) {
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
		if err := verifyOpenNamedFile(file, path, name); err != nil {
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

func openExistingSecureNamedFile(path, name string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", name, err)
	}
	if err := validateSecureFileInfo(info, name); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	if err := verifyOpenNamedFile(file, path, name); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func createExclusiveSecureNamedFile(path, name string) (*os.File, error) {
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
	if err := verifyOpenNamedFile(file, path, name); err != nil {
		return closeOnError(err)
	}
	return file, nil
}

func validateSecureDirectoryInfo(info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("job store must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("job store mode is %04o, want 0700", info.Mode().Perm())
	}
	return validateCurrentOwner(info, "job store")
}

func validateSecureFileInfo(info os.FileInfo, name string) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%s must be a non-symlink regular file", name)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s mode is %04o, want 0600", name, info.Mode().Perm())
	}
	if err := validateCurrentOwner(info, name); err != nil {
		return err
	}
	return validateSingleLink(info, name)
}

func verifyOpenDirectory(directory *os.File, path string) error {
	if directory == nil {
		return errors.New("job store descriptor is unavailable")
	}
	opened, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened job store: %w", err)
	}
	if err := validateSecureDirectoryInfo(opened); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect job store: %w", err)
	}
	if err := validateSecureDirectoryInfo(current); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return errors.New("job store path entry no longer names the opened directory")
	}
	return nil
}

func verifyOpenNamedFile(file journalFile, path, name string) error {
	if file == nil {
		return fmt.Errorf("%s descriptor is unavailable", name)
	}
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened %s: %w", name, err)
	}
	if err := validateSecureFileInfo(opened, name); err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("reinspect %s: %w", name, err)
	}
	if err := validateSecureFileInfo(current, name); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return fmt.Errorf("%s path entry no longer names the opened file", name)
	}
	return nil
}

func syncCreatedDirectoryEntries(entries []string) error {
	for index := len(entries) - 1; index >= 0; index-- {
		if err := syncParentDirectory(entries[index]); err != nil {
			return err
		}
	}
	return nil
}

func syncParentDirectory(path string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent directory for sync: %w", err)
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil {
		return fmt.Errorf("sync parent directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close parent directory after sync: %w", closeErr)
	}
	return nil
}
