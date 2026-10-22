package serviceauth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const markerName = ".anas-service-credentials"
const markerValue = "ANAS service credential delivery v1\n"

func validateOwner(info fs.FileInfo, uid int) error {
	owner, _, err := fileOwner(info)
	if err != nil {
		return err
	}
	if owner != uid {
		return errors.New("unexpected service credential file owner")
	}
	return nil
}

func validateDirectory(path string, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
		return errors.New("service credential directory has unsafe type or permissions")
	}
	return validateOwner(info, os.Geteuid())
}

// Ancestors may be root-owned or owned by the daemon's development user.
// Shared sticky temporary directories are allowed for isolated test instances;
// every private component below them remains owned and unwritable by clients.
func ensureParents(path string) error {
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Dir(path), current), current) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		owner, _, err := fileOwner(info)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || owner != 0 && owner != os.Geteuid() {
			return errors.New("service credential path has an unsafe ancestor")
		}
		if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return errors.New("service credential path has a writable ancestor")
		}
	}
	return nil
}

func ensureRoot(path string) error {
	if err := ensureParents(path); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := validateDirectory(path, 0o700); err != nil {
		return err
	}
	return ensureMarker(path)
}

func ensureClientDirectory(path string, client Client) error {
	created := false
	if err := os.Mkdir(path, 0o700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	if created {
		// The production service has UMask=0077. Explicitly grant traversal on
		// our newly created private directory after validating its ownership;
		// the umask must not silently prevent the client from reading its token.
		if err := validateDirectory(path, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(path, 0o710); err != nil {
			return err
		}
	}
	if err := validateDirectory(path, 0o710); err != nil {
		return err
	}
	if err := ensureMarker(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	_, gid, err := fileOwner(info)
	if err != nil {
		return err
	}
	if gid != client.GID {
		if err := os.Chown(path, os.Geteuid(), client.GID); err != nil {
			return errors.New("set service credential directory group")
		}
	}
	if created {
		return syncDirectory(filepath.Dir(path))
	}
	return nil
}

// A marker claims a previously empty directory. Never adopt an existing tree of
// unrelated files just because it happens to have acceptable permissions.
func ensureMarker(path string) error {
	marker := filepath.Join(path, markerName)
	if _, err := os.Lstat(marker); err == nil {
		return checkMarker(path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("service credentials require a dedicated empty directory")
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(markerValue); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return syncDirectory(path)
}

func checkMarker(path string) error {
	info, err := os.Lstat(filepath.Join(path, markerName))
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("invalid service credential directory marker")
	}
	if err := validateOwner(info, os.Geteuid()); err != nil {
		return err
	}
	body, err := readChecked(filepath.Join(path, markerName), info, 128)
	if err != nil {
		return err
	}
	if string(body) != markerValue {
		return errors.New("unrecognized service credential directory marker")
	}
	return nil
}

func readChecked(path string, expected fs.FileInfo, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(expected, info) {
		return nil, errors.New("service credential file changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("service credential file is oversized")
	}
	return body, nil
}

func readToken(dir string, client Client) ([]byte, bool, error) {
	path := filepath.Join(dir, "token")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 || info.Mode()&os.ModeType != 0 {
		return nil, false, errors.New("service credential must be a regular read-only private file")
	}
	uid, gid, err := fileOwner(info)
	if err != nil {
		return nil, false, err
	}
	if uid != client.UID || gid != client.GID {
		return nil, false, errors.New("service credential ownership does not match its client")
	}
	body, err := readChecked(path, info, 128)
	return body, true, err
}

func writeToken(dir string, client Client, body []byte) error {
	// Refuse to replace a symlink or arbitrary file at the delivery path. Ownership
	// may legitimately differ after a host administrator changes the client UID.
	path := filepath.Join(dir, "token")
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
			return errors.New("unsafe existing service credential file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(dir, ".token-*.tmp")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if _, err := file.Write(body); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	uid, gid, err := fileOwner(info)
	if err != nil {
		return err
	}
	if uid != client.UID || gid != client.GID {
		if err := file.Chown(client.UID, client.GID); err != nil {
			return errors.New("set service credential owner")
		}
	}
	if err := file.Chmod(0o400); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	actual, found, err := readToken(dir, client)
	if err != nil {
		return err
	}
	if !found || !bytes.Equal(body, actual) {
		return errors.New("service credential delivery verification failed")
	}
	return nil
}

// Remove only tokens inside directories carrying our marker, including clients
// deleted while anasd was stopped. Keep directories so existing read-only bind
// mounts observe future issue and removal through the same directory inode.
func removeCredentials(root string, enabled map[string]bool) error {
	if err := validateDirectory(root, 0o700); err != nil {
		return err
	}
	if err := checkMarker(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	var failures []error
	for _, entry := range entries {
		id := entry.Name()
		if id == markerName {
			continue
		}
		if !validID(id) {
			failures = append(failures, errors.New("unexpected entry in service credential directory"))
			continue
		}
		if enabled[id] {
			continue
		}
		dir := filepath.Join(root, id)
		if err := validateDirectory(dir, 0o710); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := checkMarker(dir); err != nil {
			failures = append(failures, err)
			continue
		}
		path := filepath.Join(dir, "token")
		if info, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			failures = append(failures, err)
			continue
		} else if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
			failures = append(failures, errors.New("refusing to remove unsafe service credential file"))
			continue
		}
		if err := os.Remove(path); err != nil {
			failures = append(failures, err)
		} else if err := syncDirectory(dir); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync service credential directory: %w", err)
	}
	return nil
}
