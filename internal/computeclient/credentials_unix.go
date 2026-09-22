//go:build linux || darwin

package computeclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
)

const credentialLockName = ".anas-compute-credentials.lock"

func credentialOwned(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	if directory {
		return securefs.ValidateDirectoryInfo(info, "compute credentials") == nil
	}
	return securefs.ValidateFileInfo(info, "compute credential") == nil
}

func publishClientCredentials(ctx context.Context, path string, items []credentialItem) (result error) {
	stage := "open configuration directory"
	defer func() {
		if errors.Is(result, errClientCredentialState) {
			// Fixed stage labels identify failures without echoing paths or TLS material.
			result = fmt.Errorf("%s: %w", stage, result)
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return errClientCredentialState
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return errClientCredentialState
	}
	before, err := os.Lstat(path)
	if err != nil || !credentialOwned(before, true) {
		return errClientCredentialState
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return errClientCredentialState
	}
	defer func() {
		if root.Close() != nil {
			result = errors.Join(result, errClientCredentialState)
		}
	}()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return errClientCredentialState
	}
	stage = "open credential lock"
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	// Separate exclusive creation from opening an existing lock. Concurrent
	// create-or-open calls can return ENOENT on the tested Darwin filesystem.
	// An existing entry is opened without O_CREATE: disappearance or replacement
	// must fail rather than silently creating a different lock inode.
	lock, err := root.OpenFile(credentialLockName, flags|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		lock, err = root.OpenFile(credentialLockName, flags, 0)
	}
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			return errors.Join(errClientCredentialState, errno)
		}
		return errClientCredentialState
	}
	defer func() {
		if lock.Close() != nil {
			result = errors.Join(result, errClientCredentialState)
		}
	}()
	identity, err := lock.Stat()
	if err != nil {
		return errClientCredentialState
	}
	if err := securefs.ValidateFileInfo(identity, "compute credential lock"); err != nil {
		return errors.Join(errClientCredentialState, err)
	}
	if identity.Size() != 0 {
		return errClientCredentialState
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, e := os.Lstat(path)
		linked, le := root.Lstat(credentialLockName)
		if e != nil || le != nil || !credentialOwned(current, true) || !credentialOwned(linked, false) || linked.Size() != 0 || !os.SameFile(opened, current) || !os.SameFile(identity, linked) {
			return errClientCredentialState
		}
		return nil
	}
	stage = "acquire credential lock"
	for {
		if err := check(); err != nil {
			return err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return errClientCredentialState
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() {
		if syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) != nil {
			result = errors.Join(result, errClientCredentialState)
		}
	}()
	if err := check(); err != nil {
		return err
	}
	stage = "open server certificate directory"
	if err := root.Mkdir("servercerts", 0700); err != nil && !os.IsExist(err) {
		return errClientCredentialState
	}
	serverInfo, err := root.Lstat("servercerts")
	if err != nil || !credentialOwned(serverInfo, true) {
		return errClientCredentialState
	}
	serverRoot, err := root.OpenRoot("servercerts")
	if err != nil {
		return errClientCredentialState
	}
	defer func() {
		if serverRoot.Close() != nil {
			result = errors.Join(result, errClientCredentialState)
		}
	}()
	serverOpened, err := serverRoot.Stat(".")
	if err != nil || !os.SameFile(serverInfo, serverOpened) {
		return errClientCredentialState
	}
	checkAll := func() error {
		if err := check(); err != nil {
			return err
		}
		current, err := root.Lstat("servercerts")
		if err != nil || !credentialOwned(current, true) || !os.SameFile(serverOpened, current) {
			return errClientCredentialState
		}
		return nil
	}
	selectRoot := func(item credentialItem) *os.Root {
		if item.server {
			return serverRoot
		}
		return root
	}
	stage = "validate existing credential files"
	missing := make([]credentialItem, 0, len(items))
	// Inspect every existing target before writing any certificate or key.
	for _, item := range items {
		if err := checkAll(); err != nil {
			return err
		}
		exists, err := matchCredentialFile(selectRoot(item), item)
		if err != nil {
			return err
		}
		if !exists {
			missing = append(missing, item)
		}
	}
	stage = "create missing credential files"
	for _, item := range missing {
		if err := checkAll(); err != nil {
			return err
		}
		if err := createCredentialFile(selectRoot(item), item); err != nil {
			return err
		}
	}
	stage = "sync credential directories"
	for _, directory := range []*os.Root{serverRoot, root} {
		file, err := directory.Open(".")
		if err != nil {
			return errClientCredentialState
		}
		syncErr, closeErr := file.Sync(), file.Close()
		if syncErr != nil || closeErr != nil {
			return errClientCredentialState
		}
	}
	stage = "verify committed credential files"
	for _, item := range items {
		exists, err := matchCredentialFile(selectRoot(item), item)
		if err != nil || !exists {
			return errClientCredentialState
		}
	}
	return checkAll()
}

func matchCredentialFile(root *os.Root, item credentialItem) (exists bool, result error) {
	before, err := root.Lstat(item.name)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || !credentialOwned(before, false) || before.Size() != int64(len(item.body)) {
		return false, errClientCredentialState
	}
	file, err := root.OpenFile(item.name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, errClientCredentialState
	}
	defer func() {
		if file.Close() != nil {
			result = errClientCredentialState
		}
	}()
	opened, err := file.Stat()
	if err != nil || !credentialOwned(opened, false) || !os.SameFile(before, opened) {
		return false, errClientCredentialState
	}
	body, err := io.ReadAll(io.LimitReader(file, int64(len(item.body))+1))
	defer clear(body)
	after, se := file.Stat()
	linked, le := root.Lstat(item.name)
	if err != nil || se != nil || le != nil || !credentialOwned(after, false) || !credentialOwned(linked, false) ||
		!os.SameFile(opened, linked) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || !bytes.Equal(body, item.body) {
		return false, errClientCredentialState
	}
	return true, nil
}

func createCredentialFile(root *os.Root, item credentialItem) (result error) {
	file, err := root.OpenFile(item.name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return errClientCredentialState
	}
	defer func() {
		if file.Close() != nil {
			result = errClientCredentialState
		}
	}()
	info, err := file.Stat()
	if err != nil || !credentialOwned(info, false) {
		return errClientCredentialState
	}
	n, err := file.Write(item.body)
	if err != nil || n != len(item.body) || file.Sync() != nil {
		return errClientCredentialState
	}
	linked, err := root.Lstat(item.name)
	if err != nil || !credentialOwned(linked, false) || !os.SameFile(info, linked) {
		return errClientCredentialState
	}
	// Preserve incomplete effects for explicit recovery. Never remove a path
	// another participant might already be using or silently truncate on retry.
	return nil
}
