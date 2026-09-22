package incusprovision

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"syscall"
)

// The caller owns the private directory descriptor and the existing host lock.
// Names are derived from validated workspace IDs. No pathname from a request
// reaches these helpers. They are shared with non-root local file fixtures.
func readObserverScopeAt(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 1<<20 {
		return nil, ErrUnsafeState
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || st.Nlink != 1 {
		return nil, ErrUnsafeState
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrUnsafeState
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !sameFileIdentity(info, opened) {
		return nil, ErrUnsafeState
	}
	body, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	after, e1 := f.Stat()
	linked, e2 := root.Lstat(name)
	if err != nil || e1 != nil || e2 != nil || int64(len(body)) != info.Size() || !sameFileIdentity(info, after) || !sameFileIdentity(info, linked) {
		return nil, ErrUnsafeState
	}
	return body, nil
}

func replaceObserverScopeAt(ctx context.Context, root *os.Root, name string, expected, desired []byte, check func(context.Context) error) error {
	if ctx == nil || root == nil || check == nil || len(desired) > 1<<20 || check(ctx) != nil {
		return ErrBlocked
	}
	verify := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check(ctx) != nil {
			return ErrDrift
		}
		body, err := readObserverScopeAt(root, name)
		if err != nil || !bytes.Equal(body, expected) {
			return ErrDrift
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	if bytes.Equal(expected, desired) {
		return nil
	}
	if len(desired) == 0 {
		if err := root.Remove(name); err != nil {
			return ErrUnsafeState
		}
	} else {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return ErrUnsafeState
		}
		tmp := ".observer-" + hex.EncodeToString(nonce[:]) + ".tmp"
		file, err := root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return ErrUnsafeState
		}
		defer root.Remove(tmp)
		n, writeErr := file.Write(desired)
		syncErr, closeErr := file.Sync(), file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil || n != len(desired) {
			return ErrUnsafeState
		}
		if err := verify(); err != nil {
			return err
		}
		if err := root.Rename(tmp, name); err != nil {
			return ErrUnsafeState
		}
	}
	dir, err := root.Open(".")
	if err != nil {
		return ErrUnsafeState
	}
	err = errors.Join(dir.Sync(), dir.Close())
	if err != nil || check(ctx) != nil {
		return ErrUnsafeState
	}
	body, err := readObserverScopeAt(root, name)
	if err != nil || !bytes.Equal(body, desired) {
		return ErrDrift
	}
	return ctx.Err()
}
