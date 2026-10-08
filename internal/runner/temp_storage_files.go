package runner

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type temporaryDirectoryHandle struct {
	path string
	root *os.Root
	file *os.File
}

func (d *temporaryDirectoryHandle) Close() error { return errors.Join(d.file.Close(), d.root.Close()) }
func (d *temporaryDirectoryHandle) Identity() string {
	info, err := d.file.Stat()
	if err != nil {
		return ""
	}
	return temporaryFileIdentity(info)
}
func (d *temporaryDirectoryHandle) check() error {
	opened, err := d.file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(d.path)
	if err != nil || !current.IsDir() || !os.SameFile(opened, current) {
		return errors.New("temporary directory was replaced")
	}
	return nil
}

func openTemporaryDirectory(path string) (*temporaryDirectoryHandle, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("temporary directory must be a canonical absolute path")
	}
	if err := validateTemporaryAncestors(path); err != nil {
		return nil, err
	}
	root, file, err := openTemporaryRoot(path)
	if err != nil {
		return nil, err
	}
	handle := &temporaryDirectoryHandle{path: path, root: root, file: file}
	if err := handle.check(); err != nil {
		handle.Close()
		return nil, err
	}
	return handle, nil
}

func validateTemporaryAncestors(path string) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("temporary path contains a symlink or non-directory: %s", current)
		}
		if info.Mode().Perm()&0022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("temporary ancestor is writable by other accounts: %s", current)
		}
	}
	return nil
}

func createTemporaryRoot(path string) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(path), current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		parent, err := openTemporaryDirectory(current)
		if err != nil {
			return err
		}
		err = parent.root.Mkdir(part, 0755)
		if errors.Is(err, fs.ErrExist) {
			err = nil
		}
		if err == nil {
			err = parent.check()
		}
		parent.Close()
		if err != nil {
			return err
		}
		current = filepath.Join(current, part)
		if err := validateTemporaryAncestors(current); err != nil {
			return err
		}
	}
	return nil
}

func createTemporaryLeaseDirectory(lease *TemporaryLease, mode os.FileMode) error {
	root, err := openTemporaryDirectory(lease.Root)
	if err != nil {
		return err
	}
	defer root.Close()
	relative, err := filepath.Rel(lease.Root, lease.Path)
	if err != nil || strings.HasPrefix(relative, "..") {
		return errors.New("lease escapes registered root")
	}
	parts := strings.Split(relative, string(filepath.Separator))
	current := root.root
	var handles []*os.Root
	defer func() {
		for _, handle := range handles {
			handle.Close()
		}
	}()
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid temporary directory component")
		}
		if err := current.Mkdir(part, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := current.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("temporary instance component is not an owned directory")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return err
		}
		handles = append(handles, next)
		current = next
	}
	file, err := current.Open(".")
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chown(lease.Declaration.UID, lease.Declaration.GID); err != nil {
		return fmt.Errorf("set declared temporary UID/GID: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	lease.DirectoryID = temporaryFileIdentity(info)
	marker, err := yaml.Marshal(lease)
	if err != nil {
		return err
	}
	if err := current.WriteFile(".anas-temp-owner.yml", marker, 0400); err != nil {
		return err
	}
	markerFile, err := current.Open(".anas-temp-owner.yml")
	if err != nil {
		return err
	}
	if err = markerFile.Sync(); err != nil {
		markerFile.Close()
		return err
	}
	markerFile.Close()
	if err := file.Sync(); err != nil {
		return err
	}
	return root.check()
}

func writeTemporaryStateFile(path string, body []byte) error {
	dir := filepath.Dir(path)
	if err := createTemporaryRoot(dir); err != nil {
		return err
	}
	handle, err := openTemporaryDirectory(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.file.Chmod(0700); err != nil {
		return err
	}
	id, err := newTemporaryID()
	if err != nil {
		return err
	}
	name := ".writing-" + id
	file, err := handle.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer handle.root.Remove(name)
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	if err := handle.check(); err != nil {
		return err
	}
	if err := handle.root.Rename(name, filepath.Base(path)); err != nil {
		return err
	}
	return errors.Join(handle.file.Sync(), handle.check())
}

func readTemporaryStateFile(path string) ([]byte, error) {
	handle, err := openTemporaryDirectory(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	body, err := readTemporaryOwnedFile(handle.root, filepath.Base(path))
	if err == nil {
		err = handle.check()
	}
	return body, err
}

// Read from the checked descriptor, so replacing the pathname cannot substitute
// a link or another account's ownership marker between validation and the read.
func readTemporaryOwnedFile(root *os.Root, name string) ([]byte, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm()&0077 != 0 || !temporaryFileOwned(before) {
		return nil, errors.New("temporary ownership file must be an owner-only regular file with one link")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(before, opened) || !temporaryFileOwned(opened) {
		return nil, errors.New("temporary ownership file was replaced")
	}
	return io.ReadAll(io.LimitReader(file, 16<<20))
}

func temporaryDirectorySize(path string) (uint64, error) {
	handle, err := openTemporaryDirectory(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()
	var total uint64
	err = fs.WalkDir(handle.root.FS(), ".", func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.Type().IsRegular() {
			info, e := entry.Info()
			if e != nil {
				return e
			}
			total += uint64(info.Size())
		}
		return nil
	})
	return total, err
}

// The probe is never a container lease. It checks the exact initialization
// privileges before stopping an old deployment, then removes only its empty,
// descriptor-confined random directory.
func probeTemporaryDirectoryPermissions(path string, declaration TemporaryDirectory) (result error) {
	mode, err := parseTemporaryDirectoryMode(declaration.Mode)
	if err != nil {
		return err
	}
	handle, err := openTemporaryDirectory(path)
	if err != nil {
		return err
	}
	defer handle.Close()
	id, err := newTemporaryID()
	if err != nil {
		return err
	}
	name := ".anas-preflight-" + id
	if err := handle.root.Mkdir(name, 0700); err != nil {
		return fmt.Errorf("temporary root is not writable: %w", err)
	}
	defer func() { result = errors.Join(result, handle.root.Remove(name), handle.check()) }()
	file, err := handle.root.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chown(declaration.UID, declaration.GID); err != nil {
		return fmt.Errorf("declared temporary UID/GID cannot be initialized: %w", err)
	}
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("declared temporary permissions cannot be initialized: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() != mode.Perm() {
		return errors.New("temporary permissions differ from declaration")
	}
	return file.Sync()
}
