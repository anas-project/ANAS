package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/securefs"
)

// The deployment owns the read-only host input. Root copies it before loadConfig
// so the runtime file follows securefs' current-owner discipline; listeners and
// workers still start only after the existing privilege drop.
func prepareMailBinding(input, output string) error {
	info, err := os.Lstat(input)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	validate := func(info os.FileInfo) error {
		if info == nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 || securefs.ValidateSingleLink(info, "portal mail input") != nil {
			return errors.New("private portal mail input required")
		}
		return nil
	}
	if err != nil || validate(info) != nil {
		return errors.New("private portal mail input required")
	}
	f, err := os.Open(input)
	if err != nil {
		return errors.New("portal mail input unavailable")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || validate(opened) != nil || !os.SameFile(info, opened) {
		return errors.New("portal mail input changed")
	}
	body, err := io.ReadAll(io.LimitReader(f, 65537))
	current, statErr := os.Lstat(input)
	if err != nil || len(body) > 65536 || statErr != nil || validate(current) != nil || !os.SameFile(opened, current) {
		return errors.New("portal mail input changed")
	}
	// The Hook reconstructs an empty private input when directory management is
	// disabled. Remove a previous runtime copy before loadConfig can consume it.
	if len(body) == 0 {
		prior, err := os.Lstat(output)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil || securefs.ValidateFileInfo(prior, "portal mail runtime") != nil {
			return errors.New("unsafe portal mail runtime")
		}
		if os.Remove(output) != nil || securefs.SyncParentDirectory(output) != nil {
			return errors.New("portal mail disable failed")
		}
		return nil
	}
	dir, _, err := securefs.OpenDirectory(filepath.Dir(output), "portal mail runtime")
	if err != nil {
		return errors.New("portal mail runtime unavailable")
	}
	defer dir.Close()
	if prior, err := os.Lstat(output); err == nil {
		if securefs.ValidateFileInfo(prior, "portal mail runtime") != nil {
			return errors.New("unsafe portal mail runtime")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("portal mail runtime unavailable")
	}
	tmp, err := os.CreateTemp(filepath.Dir(output), ".binding-")
	if err != nil {
		return errors.New("portal mail runtime unavailable")
	}
	defer os.Remove(tmp.Name())
	if err = securefs.WriteAll(tmp, body); err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err != nil || closeErr != nil {
		return errors.New("portal mail runtime write failed")
	}
	if securefs.VerifyOpenDirectory(dir, filepath.Dir(output), "portal mail runtime") != nil || os.Rename(tmp.Name(), output) != nil || dir.Sync() != nil {
		return errors.New("portal mail runtime commit failed")
	}
	return nil
}
