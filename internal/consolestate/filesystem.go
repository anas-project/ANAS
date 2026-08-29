package consolestate

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

const maximumStateBytes = 64 << 10

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create console_store: %w", err)
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return fmt.Errorf("inspect console_store: %w", err)
	}
	return validatePrivateDirectoryInfo(info)
}

func verifyPrivateDirectory(path string) error {
	entryInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect console_store: %w", err)
	}
	if err := validatePrivateDirectoryInfo(entryInfo); err != nil {
		return err
	}
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open console_store: %w", err)
	}
	defer directory.Close()
	openedInfo, err := directory.Stat()
	if err != nil {
		return fmt.Errorf("inspect opened console_store: %w", err)
	}
	if err := validatePrivateDirectoryInfo(openedInfo); err != nil {
		return err
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return errors.New("console_store changed while opening")
	}
	return nil
}

func validatePrivateDirectoryInfo(info fs.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("console_store must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("console_store mode is %04o, want 0700", info.Mode().Perm())
	}
	return validateCurrentOwner(info, "console_store")
}

func validatePrivateFileInfo(info fs.FileInfo, name string) error {
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

func readStateFile(path string) (stateFile, bool, error) {
	entryInfo, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return stateFile{}, false, nil
	}
	if err != nil {
		return stateFile{}, false, fmt.Errorf("inspect %s: %w", StateFileName, err)
	}
	if err := validatePrivateFileInfo(entryInfo, StateFileName); err != nil {
		return stateFile{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		return stateFile{}, false, fmt.Errorf("open %s: %w", StateFileName, err)
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return stateFile{}, false, fmt.Errorf("inspect opened %s: %w", StateFileName, err)
	}
	if !os.SameFile(entryInfo, openedInfo) {
		return stateFile{}, false, fmt.Errorf("%s changed while opening", StateFileName)
	}
	if err := validatePrivateFileInfo(openedInfo, StateFileName); err != nil {
		return stateFile{}, false, err
	}
	body, err := io.ReadAll(io.LimitReader(file, maximumStateBytes+1))
	if err != nil {
		return stateFile{}, false, fmt.Errorf("read %s: %w", StateFileName, err)
	}
	if len(body) > maximumStateBytes {
		return stateFile{}, false, fmt.Errorf("%s exceeds %d bytes", StateFileName, maximumStateBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var record stateFile
	if err := decoder.Decode(&record); err != nil {
		return stateFile{}, false, fmt.Errorf("decode %s: %w", StateFileName, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return stateFile{}, false, fmt.Errorf("decode %s: multiple JSON values are not allowed", StateFileName)
	} else if !errors.Is(err, io.EOF) {
		return stateFile{}, false, fmt.Errorf("decode %s: %w", StateFileName, err)
	}
	return record, true, nil
}

func writeStateFile(directory string, record stateFile, beforeRename func(string, string) error) error {
	if err := validateStateFile(record); err != nil {
		return fmt.Errorf("refuse invalid state: %w", err)
	}
	if err := verifyPrivateDirectory(directory); err != nil {
		return err
	}
	body, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", StateFileName, err)
	}
	body = append(body, '\n')
	temporary, err := os.CreateTemp(directory, "."+StateFileName+"-*.tmp")
	if err != nil {
		return fmt.Errorf("create state temporary file: %w", err)
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
		return fmt.Errorf("set state temporary permissions: %w", err)
	}
	if info, err := temporary.Stat(); err != nil {
		return fmt.Errorf("inspect state temporary file: %w", err)
	} else if err := validatePrivateFileInfo(info, "state temporary file"); err != nil {
		return err
	}
	if _, err := temporary.Write(body); err != nil {
		return fmt.Errorf("write state temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync state temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close state temporary file: %w", err)
	}
	statePath := filepath.Join(directory, StateFileName)
	if beforeRename != nil {
		if err := beforeRename(temporaryPath, statePath); err != nil {
			return err
		}
	}
	if err := verifyPrivateDirectory(directory); err != nil {
		return err
	}
	if entryInfo, err := os.Lstat(statePath); err == nil {
		if err := validatePrivateFileInfo(entryInfo, StateFileName); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect replacement target %s: %w", StateFileName, err)
	}
	if err := os.Rename(temporaryPath, statePath); err != nil {
		return fmt.Errorf("commit %s: %w", StateFileName, err)
	}
	committed = true
	entryInfo, err := os.Lstat(statePath)
	if err != nil {
		return fmt.Errorf("inspect committed %s: %w", StateFileName, err)
	}
	if err := validatePrivateFileInfo(entryInfo, StateFileName); err != nil {
		return err
	}
	if err := syncDirectory(directory); err != nil {
		return fmt.Errorf("sync console_store: %w", err)
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
