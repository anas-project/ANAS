package consoleauth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	bootstrapFileName        = "bootstrap.json"
	enrollmentCommitFileName = "bootstrap-enrollment-commit.json"
	ownerCommitFileName      = "owner-enrollment-commit.json"
	localFileName            = "local.json"
	proxyFileName            = "proxy.json"
	lockFileName             = "auth.lock"
	maximumStateBytes        = 8 << 20
)

func ensureStoreDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create console authentication directory: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect console authentication directory: %w", err)
	}
	return validatePrivateInfo(path, info, true)
}

func validateStoreDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect console authentication directory: %w", err)
	}
	return validatePrivateInfo(path, info, true)
}

func validatePrivateInfo(path string, info fs.FileInfo, directory bool) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must not be a symbolic link", path)
	}
	if directory {
		if !info.IsDir() {
			return fmt.Errorf("%s must be a directory", path)
		}
		if info.Mode().Perm() != 0o700 {
			return fmt.Errorf("%s must use mode 0700, got %04o", path, info.Mode().Perm())
		}
	} else {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a regular file", path)
		}
		if info.Mode().Perm() != 0o600 {
			return fmt.Errorf("%s must use mode 0600, got %04o", path, info.Mode().Perm())
		}
	}
	uid, err := ownerUID(info)
	if err != nil {
		return fmt.Errorf("inspect %s owner: %w", path, err)
	}
	if uid != effectiveUID() {
		return fmt.Errorf("%s owner UID is %d, want %d", path, uid, effectiveUID())
	}
	return nil
}

func readJSONFile(path string, target any) (bool, error) {
	entryInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect authentication state %s: %w", filepath.Base(path), err)
	}
	if err := validatePrivateInfo(path, entryInfo, false); err != nil {
		return false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open authentication state %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return false, fmt.Errorf("inspect opened authentication state %s: %w", filepath.Base(path), err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return false, fmt.Errorf("authentication state %s changed while opening", filepath.Base(path))
	}
	if err := validatePrivateInfo(path, openedInfo, false); err != nil {
		return false, err
	}
	source, err := io.ReadAll(io.LimitReader(file, maximumStateBytes+1))
	if err != nil {
		return false, fmt.Errorf("read authentication state %s: %w", filepath.Base(path), err)
	}
	if len(source) > maximumStateBytes {
		return false, fmt.Errorf("authentication state %s exceeds %d bytes", filepath.Base(path), maximumStateBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return false, fmt.Errorf("decode authentication state %s: %w", filepath.Base(path), err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return false, fmt.Errorf("decode authentication state %s: multiple JSON values are not allowed", filepath.Base(path))
	} else if !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("decode authentication state %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

func writeJSONFile(path string, value any) error {
	return writeJSONFileWithHooks(path, value, writeJSONHooks{})
}

func writeJSONFileWithSync(path string, value any, syncDirectoryFn func(string) error) error {
	return writeJSONFileWithHooks(path, value, writeJSONHooks{syncDirectory: syncDirectoryFn})
}

type writeJSONHooks struct {
	afterRename   func(string) error
	syncDirectory func(string) error
}

func writeJSONFileWithHooks(path string, value any, hooks writeJSONHooks) error {
	if err := validateStoreDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	syncDirectoryFn := hooks.syncDirectory
	if syncDirectoryFn == nil {
		syncDirectoryFn = syncDirectory
	}
	previous, existed, err := readPrivateFileBytes(path)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode authentication state %s: %w", filepath.Base(path), err)
	}
	body = append(body, '\n')
	if err := commitPrivateFileBytesWithHook(path, body, hooks.afterRename); err != nil {
		rollbackErr := restorePrivateFileBytes(path, previous, existed, syncDirectoryFn)
		return errors.Join(err, rollbackErr)
	}
	if err := syncDirectoryFn(filepath.Dir(path)); err != nil {
		rollbackErr := restorePrivateFileBytes(path, previous, existed, syncDirectoryFn)
		return errors.Join(fmt.Errorf("sync authentication state directory: %w", err), rollbackErr)
	}
	return nil
}

func commitPrivateFileBytes(path string, body []byte) error {
	return commitPrivateFileBytesWithHook(path, body, nil)
}

func commitPrivateFileBytesWithHook(path string, body []byte, afterRename func(string) error) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create authentication state temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set authentication state permissions: %w", err)
	}
	if _, err := temporary.Write(body); err != nil {
		return fmt.Errorf("write authentication state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync authentication state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close authentication state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit authentication state %s: %w", filepath.Base(path), err)
	}
	committed = true
	if afterRename != nil {
		if err := afterRename(path); err != nil {
			return fmt.Errorf("validate committed authentication state %s: %w", filepath.Base(path), err)
		}
	}
	entryInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect committed authentication state %s: %w", filepath.Base(path), err)
	}
	if err := validatePrivateInfo(path, entryInfo, false); err != nil {
		return err
	}
	return nil
}

func readPrivateFileBytes(path string) ([]byte, bool, error) {
	entryInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect authentication state %s: %w", filepath.Base(path), err)
	}
	if err := validatePrivateInfo(path, entryInfo, false); err != nil {
		return nil, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return nil, false, fmt.Errorf("authentication state %s changed while opening", filepath.Base(path))
	}
	if err := validatePrivateInfo(path, openedInfo, false); err != nil {
		return nil, false, err
	}
	body, err := io.ReadAll(io.LimitReader(file, maximumStateBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > maximumStateBytes {
		return nil, false, fmt.Errorf("authentication state %s exceeds %d bytes", filepath.Base(path), maximumStateBytes)
	}
	return body, true, nil
}

func restorePrivateFileBytes(path string, previous []byte, existed bool, syncDirectoryFn func(string) error) error {
	if existed {
		if err := commitPrivateFileBytes(path, previous); err != nil {
			return fmt.Errorf("restore authentication state %s: %w", filepath.Base(path), err)
		}
	} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove uncommitted authentication state %s: %w", filepath.Base(path), err)
	}
	if err := syncDirectoryFn(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync rolled back authentication state directory: %w", err)
	}
	return nil
}

func removePrivateFile(path string) error {
	return removePrivateFileWithSync(path, syncDirectory)
}

func removePrivateFileWithSync(path string, syncDirectoryFn func(string) error) error {
	if err := validateStoreDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	previous, existed, err := readPrivateFileBytes(path)
	if err != nil || !existed {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove authentication state %s: %w", filepath.Base(path), err)
	}
	if err := syncDirectoryFn(filepath.Dir(path)); err != nil {
		rollbackErr := restorePrivateFileBytes(path, previous, true, syncDirectoryFn)
		return errors.Join(fmt.Errorf("sync authentication state removal: %w", err), rollbackErr)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
