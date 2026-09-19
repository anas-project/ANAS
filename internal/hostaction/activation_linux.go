//go:build linux

package hostaction

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"strings"

	"github.com/anas-project/ANAS/internal/buildinfo"
	"golang.org/x/sys/unix"
)

// OpenSystemdActivation consumes fd 3 ONLY after validating activation markers.
// It accepts an Accept=yes connected AF_UNIX stream with the installed name,
// not a listener, socketpair, abstract socket, TCP socket or caller-selected fd.
// Markers are only a protocol check; root-owned paths and SO_PEERCRED supply
// independent boundaries. No root process, service or network is started here.
func OpenSystemdActivation(ctx context.Context) (*Activation, error) {
	if ctx == nil || ctx.Err() != nil || os.Getuid() != 0 || os.Geteuid() != 0 || os.Getgid() != 0 || os.Getegid() != 0 {
		return nil, ErrUnavailable
	}
	if checkActivationEnvironment(os.Getpid(), os.Getenv) != nil {
		return nil, ErrUnavailable
	}
	release := ReleaseIdentity{Version: buildinfo.Version, Commit: buildinfo.Commit}
	if release.Validate() != nil {
		return nil, ErrUnavailable
	}
	file := os.NewFile(3, "host-action-activation")
	defer file.Close()
	unix.CloseOnExec(3)
	// Do not allow activation metadata to leak into any future subprocess.
	for _, key := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		if os.Unsetenv(key) != nil {
			return nil, ErrUnavailable
		}
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer unix.Close(root)
	return openActivationAt(ctx, root, file, release, 0, installationPath, activationSocketPath, activationSocketPath)
}

// root/uid/path arguments are private fixture seams. The production entry
// fixes them above; neither a request nor environment can choose them.
func openActivationAt(ctx context.Context, root int, file *os.File, release ReleaseIdentity, uid uint32, configPath, socketPath, boundSocketPath string) (*Activation, error) {
	if ctx == nil || ctx.Err() != nil || file == nil {
		return nil, ErrUnavailable
	}
	config, err := openPinnedInstallationPath(root, configPath, uid, false)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = config.close()
		}
	}()
	body, err := config.readPolicy()
	if err != nil {
		return nil, err
	}
	policy, err := decodeInstallation(body, release)
	if err != nil {
		return nil, err
	}
	socket, err := openPinnedInstallationPath(root, socketPath, uid, true)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keep {
			_ = socket.close()
		}
	}()
	if socket.last().stat.Gid != policy.socketGroup() || validateActivatedFD(int(file.Fd()), boundSocketPath) != nil {
		return nil, ErrUnavailable
	}
	guard := &installedGuard{config: config, socket: socket, body: bytes.Clone(body)}
	if guard.check() != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	conn, err := net.FileConn(file) // FileConn duplicates; file stays caller-owned.
	if err != nil {
		return nil, ErrUnavailable
	}
	stream, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, ErrUnavailable
	}
	if guard.check() != nil {
		_ = stream.Close()
		return nil, ErrUnavailable
	}
	keep = true
	return &Activation{connection: stream, guard: guard, policy: policy}, nil
}

func validateActivatedFD(fd int, path string) error {
	for _, item := range []struct{ option, want int }{{unix.SO_TYPE, unix.SOCK_STREAM}, {unix.SO_DOMAIN, unix.AF_UNIX}, {unix.SO_ACCEPTCONN, 0}} {
		got, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, item.option)
		if err != nil || got != item.want {
			return ErrUnavailable
		}
	}
	local, err := unix.Getsockname(fd)
	if err != nil {
		return ErrUnavailable
	}
	address, ok := local.(*unix.SockaddrUnix)
	if !ok || address.Name != path || !strings.HasPrefix(path, "/") {
		return ErrUnavailable
	}
	remote, err := unix.Getpeername(fd)
	if err != nil {
		return ErrUnavailable
	}
	if _, ok := remote.(*unix.SockaddrUnix); !ok {
		return ErrUnavailable
	}
	return nil
}

type pinnedInstallationNode struct {
	file *os.File
	stat unix.Stat_t
	name string
}
type pinnedInstallationPath struct {
	nodes  []pinnedInstallationNode
	socket bool
	uid    uint32
}

func (p *pinnedInstallationPath) last() *pinnedInstallationNode { return &p.nodes[len(p.nodes)-1] }
func (p *pinnedInstallationPath) close() error {
	failed := false
	for i := len(p.nodes) - 1; i >= 0; i-- {
		if p.nodes[i].file.Close() != nil {
			failed = true
		}
	}
	if failed {
		return ErrUnavailable
	}
	return nil
}

func openPinnedInstallationPath(root int, path string, uid uint32, socket bool) (*pinnedInstallationPath, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !strings.HasPrefix(path, "/") || len(parts) > 8 {
		return nil, ErrUnavailable
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, ErrUnavailable
		}
	}
	fd, err := unix.Openat(root, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	p := &pinnedInstallationPath{socket: socket, uid: uid}
	keep := false
	defer func() {
		if !keep {
			_ = p.close()
		}
	}()
	for i := -1; ; i++ {
		name := "."
		if i >= 0 {
			name = parts[i]
		}
		node := pinnedInstallationNode{file: os.NewFile(uintptr(fd), "host-installation-node"), name: name}
		p.nodes = append(p.nodes, node)
		last := p.last()
		if unix.Fstat(fd, &last.stat) != nil {
			return nil, ErrUnavailable
		}
		leaf := i == len(parts)-1
		if !p.validStat(last.stat, leaf) {
			return nil, ErrUnavailable
		}
		if leaf {
			break
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i+1 < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		} else if socket {
			flags = unix.O_PATH | unix.O_CLOEXEC | unix.O_NOFOLLOW
		}
		fd, err = unix.Openat(fd, parts[i+1], flags, 0)
		if err != nil {
			return nil, ErrUnavailable
		}
	}
	if p.check() != nil {
		return nil, ErrUnavailable
	}
	keep = true
	return p, nil
}

func (p *pinnedInstallationPath) validStat(s unix.Stat_t, leaf bool) bool {
	if s.Uid != p.uid || s.Mode&07000 != 0 {
		return false
	}
	if !leaf {
		return s.Mode&unix.S_IFMT == unix.S_IFDIR && s.Mode&0022 == 0
	}
	if p.socket {
		return s.Mode&unix.S_IFMT == unix.S_IFSOCK && s.Mode&0777 == 0600 && s.Nlink == 1
	}
	return s.Mode&unix.S_IFMT == unix.S_IFREG && s.Mode&0777 == 0600 && s.Nlink == 1 && s.Size > 0 && s.Size <= maxInstallationBytes
}

func (p *pinnedInstallationPath) check() error {
	for i, node := range p.nodes {
		var current, named unix.Stat_t
		leaf := i == len(p.nodes)-1
		if unix.Fstat(int(node.file.Fd()), &current) != nil || !p.validStat(current, leaf) || !sameInstallationStat(node.stat, current, leaf) {
			return ErrUnavailable
		}
		if i > 0 && (unix.Fstatat(int(p.nodes[i-1].file.Fd()), node.name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameInstallationStat(node.stat, named, leaf)) {
			return ErrUnavailable
		}
	}
	return nil
}

func sameInstallationStat(a, b unix.Stat_t, content bool) bool {
	if a.Dev != b.Dev || a.Ino != b.Ino || a.Uid != b.Uid || a.Gid != b.Gid || a.Mode != b.Mode {
		return false
	}
	return !content || a.Size == b.Size && a.Nlink == b.Nlink && a.Mtim == b.Mtim && a.Ctim == b.Ctim
}

func (p *pinnedInstallationPath) readPolicy() ([]byte, error) {
	if p.socket || p.check() != nil {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.NewSectionReader(p.last().file, 0, maxInstallationBytes+1))
	if err != nil || int64(len(body)) != p.last().stat.Size || p.check() != nil {
		return nil, ErrUnavailable
	}
	return body, nil
}

type installedGuard struct {
	config, socket *pinnedInstallationPath
	body           []byte
}

func (g *installedGuard) check() error {
	if g == nil || g.config == nil || g.socket == nil {
		return ErrUnavailable
	}
	body, err := g.config.readPolicy()
	if err != nil || !bytes.Equal(body, g.body) || g.socket.check() != nil {
		return ErrUnavailable
	}
	return nil
}
func (g *installedGuard) close() error {
	a, b := g.config.close(), g.socket.close()
	if a != nil || b != nil {
		return ErrUnavailable
	}
	return nil
}
