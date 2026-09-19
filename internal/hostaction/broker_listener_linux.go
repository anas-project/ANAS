//go:build linux

package hostaction

import (
	"context"
	"errors"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// OpenJobBrokerListener requires the installation to have provided
// /run/anas-job-broker as service-owned 0700. It is deliberately not socket
// activation: the owner itself must call listen so peer credentials identify
// the process that owns the shared job lease, not the service manager.
func OpenJobBrokerListener(ctx context.Context) (*JobBrokerListener, error) {
	if ctx == nil || ctx.Err() != nil || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return nil, ErrDenied
	}
	root, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer unix.Close(root)
	if os.Geteuid() == 0 || os.Getegid() == 0 {
		policy, err := readInstalledPolicyAt(root)
		if err != nil {
			return nil, err
		}
		if !policy.peers().authorizes(ctx, Peer{pid: int32(os.Getpid()), uid: 0, gid: 0}) {
			return nil, ErrDenied
		}
	}
	return openJobBrokerListenerAt(ctx, root, 0, uint32(os.Geteuid()), uint32(os.Getegid()), brokerSocketPath)
}

// Private path/UID seams permit isolated unprivileged Linux tests. The public
// entry fixes all of them and never reads request/environment path overrides.
func openJobBrokerListenerAt(ctx context.Context, root int, ancestorUID, uid, gid uint32, path string) (*JobBrokerListener, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	directory, err := openBrokerEndpointNodes(root, ancestorUID, uid, gid, 3)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = directory.close()
		}
	}()
	dirFD := int(directory.nodes[2].Fd())
	// A directory lock adds no writable policy/lock file and serializes all
	// cooperating listener lifetimes. Existing sockets are never removed here,
	// even when a previous process crashed and the lock is now available.
	if unix.Flock(dirFD, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil, ErrUnavailable
	}
	var existing unix.Stat_t
	if err := unix.Fstatat(dirFD, "socket", &existing, unix.AT_SYMLINK_NOFOLLOW); !errors.Is(err, unix.ENOENT) || directory.checkNodes(3) != nil {
		return nil, ErrUnavailable
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, ErrUnavailable
	}
	// net's default unlink on Close is path-based and could delete a replacement.
	// Ownership-aware retirement below is the only cleanup path.
	listener.SetUnlinkOnClose(false)
	defer func() {
		if !keep {
			_ = listener.Close()
		}
	}()
	var before, after unix.Stat_t
	if directory.checkNodes(3) != nil || unix.Fstatat(dirFD, "socket", &before, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		before.Uid != uid || before.Gid != gid || before.Mode&unix.S_IFMT != unix.S_IFSOCK || before.Nlink != 1 || before.Mode&07000 != 0 {
		return nil, ErrUnavailable
	}
	// The verified private parent excludes other accounts while initial mode
	// is tightened. Do not mutate the process-global umask in a Go daemon.
	if unix.Fchmodat(dirFD, "socket", 0600, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		unix.Fstatat(dirFD, "socket", &after, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		before.Dev != after.Dev || before.Ino != after.Ino || !directory.valid(3, after) {
		return nil, ErrUnavailable
	}
	endpoint, err := openBrokerEndpoint(root, ancestorUID, uid, gid)
	if err != nil {
		return nil, err
	}
	guard := &ownedBrokerListener{directory: directory, endpoint: endpoint}
	if guard.check() != nil || !sameInstallationStat(after, endpoint.stats[3], true) || ctx.Err() != nil {
		_ = endpoint.close()
		return nil, ErrUnavailable
	}
	keep = true
	return &JobBrokerListener{listener: listener, guard: guard}, nil
}

type ownedBrokerListener struct {
	directory *brokerEndpoint // Holds the directory's exclusive flock.
	endpoint  *brokerEndpoint
}

func (g *ownedBrokerListener) check() error {
	if g == nil || g.directory.checkNodes(3) != nil || g.endpoint.check() != nil {
		return ErrUnavailable
	}
	for i := range g.directory.stats {
		if !sameInstallationStat(g.directory.stats[i], g.endpoint.stats[i], false) {
			return ErrUnavailable
		}
	}
	return nil
}

func (g *ownedBrokerListener) retire() error {
	failed := g.check() != nil
	// Refuse to unlink after any name/metadata drift. All path mutation is
	// relative to the still-pinned private directory, never a recursive path.
	if !failed {
		dirFD := int(g.directory.nodes[2].Fd())
		if unix.Unlinkat(dirFD, "socket", 0) != nil || unix.Fsync(dirFD) != nil {
			failed = true
		}
	}
	failed = g.endpoint.close() != nil || failed
	failed = g.directory.close() != nil || failed // Releases the flock last.
	if failed {
		return ErrUnavailable
	}
	return nil
}
