//go:build linux && (amd64 || arm64)

package computeingress

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"syscall"
)

// O_PATH is Linux UAPI on amd64/arm64; the frozen syscall package omits
// its exported name on amd64. Keep this reader limited to the supported hosts.
const requestOPath = 0x200000

var requestFileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}\.json$`)

// ReadRequest reads one flat file relative to an already opened, trusted lease
// directory. The caller binds that descriptor to the active authorization and
// owns its parent/mount. Writers publish complete files by rename. Hard links
// and mutable partial writes are rejected; names never appear in errors.
func ReadRequest(directory *os.File, name string) (Request, error) {
	file, request, err := readRequestFile(directory, name)
	if file != nil {
		_ = file.Close()
	}
	if errors.Is(err, os.ErrNotExist) {
		return Request{}, fmt.Errorf("cannot open HTTP request without following links")
	}
	return request, err
}

// readRequestFile also serves consumer receipts. O_PATH|O_NOFOLLOW pins the
// inode without opening a device or FIFO. Only after fstat accepts a regular
// file do we reopen the pinned descriptor via trusted /proc/self/fd. The
// consumer pathname is never reopened. A successful caller owns the descriptor.
func readRequestFile(directory *os.File, name string) (*os.File, Request, error) {
	defer runtime.KeepAlive(directory)
	var empty Request
	if directory == nil || !requestFileName.MatchString(name) {
		return nil, empty, fmt.Errorf("request must be a simple JSON basename")
	}
	info, err := directory.Stat()
	if err != nil || !info.IsDir() {
		return nil, empty, fmt.Errorf("HTTP request directory is unavailable")
	}
	pinned, err := syscall.Openat(int(directory.Fd()), name, requestOPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil, empty, os.ErrNotExist
		}
		return nil, empty, fmt.Errorf("cannot open HTTP request without following links")
	}
	defer syscall.Close(pinned)
	var before, after syscall.Stat_t
	if syscall.Fstat(pinned, &before) != nil || before.Mode&syscall.S_IFMT != syscall.S_IFREG || before.Nlink != 1 || before.Size > MaxRequestBytes {
		return nil, empty, fmt.Errorf("HTTP request must be a single-link regular file of at most 4 KiB")
	}
	fd, err := syscall.Open("/proc/self/fd/"+strconv.Itoa(pinned), syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, empty, fmt.Errorf("cannot read pinned HTTP request")
	}
	file := os.NewFile(uintptr(fd), "http-request")
	keep := false
	defer func() {
		if !keep {
			_ = file.Close()
		}
	}()
	if syscall.Fstat(fd, &after) != nil || before.Dev != after.Dev || before.Ino != after.Ino {
		return nil, empty, fmt.Errorf("HTTP request descriptor identity changed")
	}
	body, err := io.ReadAll(io.LimitReader(file, MaxRequestBytes+1))
	if err != nil || syscall.Fstat(fd, &after) != nil || (before.Dev != after.Dev || before.Ino != after.Ino || before.Mode != after.Mode || before.Nlink != after.Nlink || before.Size != after.Size || before.Mtim != after.Mtim || before.Ctim != after.Ctim) || int64(len(body)) != after.Size {
		return nil, empty, fmt.Errorf("HTTP request changed or could not be read")
	}
	request, err := ParseRequest(body)
	if err != nil {
		return nil, empty, err
	}
	keep = true
	return file, request, nil
}
