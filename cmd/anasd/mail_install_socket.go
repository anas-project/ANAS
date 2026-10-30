package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/api/httpapi"
	"golang.org/x/sys/unix"
)

const mailInstallSocketName = "install.sock"

var errMailInstallSocket = errors.New("mail install socket unavailable")

// openMailSocketDirectory rejects symlinks and untrusted writable ancestors.
// The final directory must be provisioned separately for the mail client group.
func openMailSocketDirectory(path string, group uint32) (int, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" || len(filepath.Join(path, mailInstallSocketName)) >= 100 {
		return -1, errMailInstallSocket
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return -1, errMailInstallSocket
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		unix.Close(fd)
		if e != nil {
			return -1, errMailInstallSocket
		}
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&07022 != 0 || st.Uid != 0 && st.Uid != uint32(os.Geteuid()) || i == len(parts)-1 && (st.Uid != uint32(os.Geteuid()) || st.Gid != group || st.Mode&0777 != 0750) {
			unix.Close(fd)
			return -1, errMailInstallSocket
		}
	}
	return fd, nil
}

type mailSocketListener struct {
	*net.UnixListener
	dir, lock int
	dev       uint64
	ino       uint64
	once      sync.Once
	closeErr  error
}

func (l *mailSocketListener) Close() error {
	l.once.Do(func() {
		l.closeErr = l.UnixListener.Close()
		var st unix.Stat_t
		if e := unix.Fstatat(l.dir, mailInstallSocketName, &st, unix.AT_SYMLINK_NOFOLLOW); e == nil {
			if st.Mode&unix.S_IFMT != unix.S_IFSOCK || uint64(st.Dev) != l.dev || st.Ino != l.ino {
				l.closeErr = errors.Join(l.closeErr, errMailInstallSocket)
			} else if e = unix.Unlinkat(l.dir, mailInstallSocketName, 0); e != nil {
				l.closeErr = errors.Join(l.closeErr, errMailInstallSocket)
			}
		} else if !errors.Is(e, syscall.ENOENT) {
			l.closeErr = errors.Join(l.closeErr, errMailInstallSocket)
		}
		_ = unix.Flock(l.lock, unix.LOCK_UN)
		_ = unix.Close(l.lock)
		_ = unix.Close(l.dir)
	})
	return l.closeErr
}

// listenMailInstallSocket keeps an exclusive private lock for its lifetime.
// Only a socket owned by this daemon and no longer accepting connections may
// be removed as stale. Unknown files and active listeners are never replaced.
func listenMailInstallSocket(path string, group uint32) (*mailSocketListener, error) {
	dir, err := openMailSocketDirectory(path, group)
	if err != nil {
		return nil, err
	}
	fail := func(lock int) (*mailSocketListener, error) {
		if lock >= 0 {
			unix.Close(lock)
		}
		unix.Close(dir)
		return nil, errMailInstallSocket
	}
	lock, err := unix.Openat(dir, ".lock", unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return fail(-1)
	}
	var lockStat unix.Stat_t
	if unix.Fstat(lock, &lockStat) != nil || lockStat.Mode&unix.S_IFMT != unix.S_IFREG || lockStat.Mode&07777 != 0600 || lockStat.Uid != uint32(os.Geteuid()) || lockStat.Nlink != 1 || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return fail(lock)
	}
	// The read-only mount must not expose unrelated pre-existing files.
	scanFD, e := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if e != nil {
		return fail(lock)
	}
	scan := os.NewFile(uintptr(scanFD), "mail socket directory")
	names, e := scan.Readdirnames(-1)
	closeErr := scan.Close()
	if e != nil || closeErr != nil {
		return fail(lock)
	}
	for _, name := range names {
		if name != ".lock" && name != mailInstallSocketName {
			return fail(lock)
		}
	}
	var st unix.Stat_t
	err = unix.Fstatat(dir, mailInstallSocketName, &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != uint32(os.Geteuid()) || st.Gid != group || st.Mode&07777 != 0660 {
			return fail(lock)
		}
		c, e := net.DialTimeout("unix", filepath.Join(path, mailInstallSocketName), time.Second)
		if e == nil {
			c.Close()
			return fail(lock)
		}
		if !errors.Is(e, syscall.ECONNREFUSED) {
			return fail(lock)
		}
		var current unix.Stat_t
		if unix.Fstatat(dir, mailInstallSocketName, &current, unix.AT_SYMLINK_NOFOLLOW) != nil || current.Dev != st.Dev || current.Ino != st.Ino || unix.Unlinkat(dir, mailInstallSocketName, 0) != nil {
			return fail(lock)
		}
	} else if !errors.Is(err, syscall.ENOENT) {
		return fail(lock)
	}
	address := filepath.Join(path, mailInstallSocketName)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: address, Net: "unix"})
	if err != nil {
		return fail(lock)
	}
	listener.SetUnlinkOnClose(false)
	if os.Chown(address, os.Geteuid(), int(group)) != nil || os.Chmod(address, 0660) != nil || unix.Fstatat(dir, mailInstallSocketName, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != uint32(os.Geteuid()) || st.Gid != group || st.Mode&07777 != 0660 {
		listener.Close()
		return fail(lock)
	}
	return &mailSocketListener{UnixListener: listener, dir: dir, lock: lock, dev: uint64(st.Dev), ino: st.Ino}, nil
}

// serveMailInstallSocket remains TLS-only; the ordinary service route still
// performs its existing Host, state, grant, reauthorization and audit checks.
func serveMailInstallSocket(ctx context.Context, listener net.Listener, handler http.Handler, tlsConfig *tls.Config) error {
	if listener == nil || handler == nil || tlsConfig == nil {
		return errMailInstallSocket
	}
	server := newHTTPServer("", httpapi.MailInstallOnly(handler))
	result := make(chan error, 1)
	go func() { result <- server.Serve(tls.NewListener(listener, tlsConfig)) }()
	select {
	case err := <-result:
		if ctx.Err() == nil {
			return fmt.Errorf("mail install socket stopped: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		return err
	}
	if err := <-result; err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}
