//go:build linux && (amd64 || arm64)

package computeingress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type linuxRequestDirectory struct {
	path      string
	root      *os.Root
	directory *os.File
	identity  os.FileInfo
	broken    bool
}

func openRequestDirectory(path string) (requestDirectory, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrRequestWriterUnavailable
	}
	before, err := os.Lstat(path)
	if err != nil || !privateRequestDirectory(before) {
		return nil, ErrRequestWriterUnavailable
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrRequestWriterUnavailable
	}
	directory, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return nil, ErrRequestWriterUnavailable
	}
	d := &linuxRequestDirectory{path: path, root: root, directory: directory, identity: before}
	var filesystem syscall.Statfs_t
	if d.check() != nil || syscall.Fstatfs(int(directory.Fd()), &filesystem) != nil || !requestLocalFilesystem(filesystem.Type) {
		_ = d.close()
		return nil, ErrRequestWriterUnavailable
	}
	return d, nil
}

func privateRequestDirectory(info os.FileInfo) bool {
	if info == nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid()) && stat.Ino != 0
}

// Locking and rename guarantees have not been verified for remote/FUSE
// filesystems. Linux UAPI magic values cover the local deployment backends.
func requestLocalFilesystem(kind int64) bool {
	switch kind {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994, 0x794c7630, 0x2fc12fc1:
		return true // ext*, XFS, btrfs, tmpfs, overlayfs, ZFS.
	default:
		return false
	}
}

func (d *linuxRequestDirectory) check() error {
	if d.broken {
		return ErrRequestWriterUnavailable
	}
	opened, err := d.directory.Stat()
	if err != nil || !privateRequestDirectory(opened) || !os.SameFile(d.identity, opened) {
		return ErrRequestWriterUnavailable
	}
	current, err := os.Lstat(d.path)
	if err != nil || !privateRequestDirectory(current) || !os.SameFile(d.identity, current) {
		return ErrRequestWriterUnavailable
	}
	return nil
}

func (d *linuxRequestDirectory) lock(ctx context.Context) (func() error, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := d.check(); err != nil {
			return nil, err
		}
		err := syscall.Flock(int(d.directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() error {
				checkErr := d.check()
				if syscall.Flock(int(d.directory.Fd()), syscall.LOCK_UN) != nil || checkErr != nil {
					d.broken = true
					return ErrRequestCommitUncertain
				}
				return nil
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, ErrRequestWriterUnavailable
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *linuxRequestDirectory) read(name string) (*os.File, Request, error) {
	if err := d.check(); err != nil {
		return nil, Request{}, err
	}
	file, request, err := readRequestFile(d.directory, name)
	if err != nil {
		return nil, Request{}, err
	}
	info, err := file.Stat()
	stat, ok := infoSys(info)
	if err != nil || !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0 {
		_ = file.Close()
		return nil, Request{}, ErrRequestWriterUnavailable
	}
	// Also settle a retry after an earlier ambiguous rename/fsync result.
	if file.Sync() != nil || d.directory.Sync() != nil || d.check() != nil {
		_ = file.Close()
		return nil, Request{}, ErrRequestCommitUncertain
	}
	return file, request, nil
}

func infoSys(info os.FileInfo) (*syscall.Stat_t, bool) {
	if info == nil {
		return nil, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return stat, ok
}

func (d *linuxRequestDirectory) publish(ctx context.Context, name string, request Request) (result *os.File, resultErr error) {
	if !requestFileName.MatchString(name) || d.check() != nil {
		return nil, ErrRequestWriterUnavailable
	}
	listing, err := d.root.Open(".")
	if err != nil {
		return nil, ErrRequestWriterUnavailable
	}
	entries, readErr := listing.Readdirnames(257)
	closeErr := listing.Close()
	// Reserve room for this operation's temporary file as well as the final
	// request; atomic rename keeps the maximum at 256 throughout publication.
	if (readErr != nil && readErr != io.EOF) || closeErr != nil || len(entries) >= 256 {
		return nil, ErrRequestWriterUnavailable
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > MaxRequestBytes {
		return nil, ErrRequestConflict
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrRequestWriterUnavailable
	}
	temporary := ".http-" + hex.EncodeToString(nonce[:]) + ".tmp"
	file, err := d.root.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, ErrRequestWriterUnavailable
	}
	renamed, retained := false, false
	defer func() {
		if !renamed {
			info, statErr := file.Stat()
			current, currentErr := d.root.Lstat(temporary)
			if statErr != nil || currentErr != nil || !os.SameFile(info, current) || d.root.Remove(temporary) != nil || d.directory.Sync() != nil {
				resultErr = errors.Join(resultErr, ErrRequestCommitUncertain)
			}
		}
		if !retained {
			_ = file.Close()
		}
	}()
	// Set the exact private mode on the pinned new inode, independently of
	// the consumer's umask; no pathname-based chmod follows a replacement.
	if file.Chmod(0600) != nil {
		return nil, ErrRequestWriterUnavailable
	}
	if n, err := file.Write(body); err != nil || n != len(body) || file.Sync() != nil {
		return nil, ErrRequestWriterUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.check() != nil {
		return nil, ErrRequestWriterUnavailable
	}
	if _, err := d.root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return nil, ErrRequestConflict
	}
	// The directory flock serializes cooperating consumer writers. A hostile
	// consumer can already replace its own requests; this is not an authority
	// check or a defense against malicious writers ignoring the flock.
	if err := d.root.Rename(temporary, name); err != nil {
		return nil, ErrRequestCommitUncertain
	}
	renamed = true
	current, err := d.root.Lstat(name)
	info, statErr := file.Stat()
	if err != nil || statErr != nil || !os.SameFile(info, current) || d.directory.Sync() != nil || d.check() != nil || ctx.Err() != nil {
		return nil, ErrRequestCommitUncertain
	}
	retained = true
	return file, nil
}

func (d *linuxRequestDirectory) remove(ctx context.Context, name string, receipt *os.File, expected Request) error {
	file, current, err := d.read(name)
	if errors.Is(err, os.ErrNotExist) {
		// Missing intent already requests withdrawal. Sync still matters after
		// a prior ambiguous unlink; never promise route removal here.
		if d.directory.Sync() != nil || d.check() != nil {
			return ErrRequestCommitUncertain
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	before, beforeErr := receipt.Stat()
	after, afterErr := file.Stat()
	if beforeErr != nil || afterErr != nil || !os.SameFile(before, after) || current != expected {
		return ErrRequestReceiptStale
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	visible, err := d.root.Lstat(name)
	if err != nil || !os.SameFile(after, visible) || d.check() != nil {
		return ErrRequestReceiptStale
	}
	if d.root.Remove(name) != nil || d.directory.Sync() != nil || d.check() != nil {
		return ErrRequestCommitUncertain
	}
	return nil
}

func (d *linuxRequestDirectory) close() error {
	return errors.Join(d.directory.Close(), d.root.Close())
}
