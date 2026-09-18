//go:build linux || darwin

package computeingressruntime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func privateArtifactRoot(path string) (*os.Root, os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." || path == "/" {
		return nil, nil, fmt.Errorf("HTTP private artifacts require an absolute clean file path")
	}
	parent := filepath.Dir(path)
	before, err := os.Lstat(parent)
	if err != nil || !privateOwned(before, true) {
		return nil, nil, fmt.Errorf("HTTP private artifact parent must be private and owned by the runtime user")
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, nil, fmt.Errorf("HTTP private artifact directory is unavailable")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, nil, fmt.Errorf("HTTP private artifact directory changed while opening")
	}
	return root, opened, nil
}

func checkPrivateArtifactRoot(ctx context.Context, path string, pinned os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	linked, err := os.Lstat(filepath.Dir(path))
	if err != nil || !privateOwned(linked, true) || !os.SameFile(pinned, linked) {
		return fmt.Errorf("HTTP private artifact directory changed")
	}
	return nil
}

func readPrivateArtifact(ctx context.Context, path string) ([]byte, os.FileInfo, os.FileInfo, error) {
	fail := func() ([]byte, os.FileInfo, os.FileInfo, error) {
		return nil, nil, nil, fmt.Errorf("HTTP private artifacts must be an unchanged private 0400 single-link bounded file")
	}
	if ctx.Err() != nil {
		return fail()
	}
	root, parent, err := privateArtifactRoot(path)
	if err != nil {
		return fail()
	}
	defer root.Close()
	name := filepath.Base(path)
	valid := func(info os.FileInfo) bool {
		return privateOwned(info, false) && info.Mode().Perm() == 0400 && info.Size() > 0 && info.Size() <= privateArtifactLimit
	}
	before, err := root.Lstat(name)
	if err != nil || !valid(before) {
		return fail()
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fail()
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !valid(opened) || !os.SameFile(before, opened) {
		return fail()
	}
	body, readErr := io.ReadAll(io.LimitReader(file, privateArtifactLimit+1))
	after, statErr := file.Stat()
	linked, linkErr := root.Lstat(name)
	if readErr != nil || statErr != nil || linkErr != nil || !valid(after) || !valid(linked) || !os.SameFile(opened, linked) || opened.Size() != after.Size() || !opened.ModTime().Equal(after.ModTime()) || int64(len(body)) != after.Size() || len(body) > privateArtifactLimit || checkPrivateArtifactRoot(ctx, path, parent) != nil {
		return fail()
	}
	return body, parent, after, nil
}

func publishPrivateArtifact(ctx context.Context, path string, body []byte, checkCurrent func(context.Context) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(body) == 0 || len(body) > privateArtifactLimit || checkCurrent == nil {
		return fmt.Errorf("HTTP private artifact requires bounded content and a current-authority check")
	}
	root, parent, err := privateArtifactRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("cannot allocate HTTP private artifact temporary name")
	}
	temporary := ".http-private-" + hex.EncodeToString(nonce[:])
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0400)
	if err != nil {
		return fmt.Errorf("cannot create private HTTP private artifact artifact")
	}
	defer file.Close()
	owned, err := file.Stat()
	if err != nil {
		return fmt.Errorf("HTTP private artifact temporary identity is unavailable; private staging cleanup requires recovery")
	}
	defer func() {
		linked, e := root.Lstat(temporary)
		if os.IsNotExist(e) {
			return
		}
		if e != nil || !os.SameFile(owned, linked) {
			err = fmt.Errorf("HTTP private artifact staging identity changed; cleanup requires recovery")
			return
		}
		if root.Remove(temporary) != nil || syncRoot(root) != nil {
			err = fmt.Errorf("HTTP private artifact staging cleanup requires recovery")
		}
	}()
	if !privateOwned(owned, false) || owned.Mode().Perm() != 0400 {
		return fmt.Errorf("HTTP private artifact temporary permissions are invalid")
	}
	if n, err := file.Write(body); err != nil || n != len(body) {
		return fmt.Errorf("cannot write complete HTTP private artifacts")
	}
	if file.Sync() != nil {
		return fmt.Errorf("cannot sync HTTP private artifacts")
	}
	complete, err := file.Stat()
	if err != nil || !privateOwned(complete, false) || complete.Mode().Perm() != 0400 || !os.SameFile(owned, complete) || complete.Size() != int64(len(body)) {
		return fmt.Errorf("HTTP private artifact temporary identity is invalid")
	}
	if file.Close() != nil {
		return fmt.Errorf("cannot close complete HTTP private artifacts")
	}
	if checkPrivateArtifactRoot(ctx, path, parent) != nil || checkCurrent(ctx) != nil {
		return fmt.Errorf("Active authority changed before HTTP private artifact delivery")
	}
	name := filepath.Base(path)
	// Link is an atomic no-overwrite publication. Remove the private staging
	// link before any reader accepts the single-link destination.
	if root.Link(temporary, name) != nil {
		return fmt.Errorf("HTTP private artifact destination already exists or cannot be published")
	}
	defer func() {
		if err == nil {
			return
		}
		if linked, e := root.Lstat(name); e == nil && os.SameFile(owned, linked) {
			if root.Remove(name) != nil || syncRoot(root) != nil {
				err = fmt.Errorf("HTTP private artifact delivery failed and private artifact cleanup requires recovery")
			}
		} else if !os.IsNotExist(e) {
			err = fmt.Errorf("HTTP private artifact destination identity changed; cleanup requires recovery")
		}
	}()
	if root.Remove(temporary) != nil || syncRoot(root) != nil {
		return fmt.Errorf("cannot finalize HTTP private artifact artifact")
	}
	if checkPrivateArtifactRoot(ctx, path, parent) != nil || checkCurrent(ctx) != nil {
		return fmt.Errorf("Active authority changed during HTTP private artifact delivery")
	}
	return nil
}
