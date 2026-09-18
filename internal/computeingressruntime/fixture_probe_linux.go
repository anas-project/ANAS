//go:build linux

package computeingressruntime

import (
	"context"
	"fmt"
	"io"
	"net/netip"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func checkProbeSocket(connection syscall.RawConn, expected uint64) error {
	var checkErr error
	err := connection.Control(func(fd uintptr) {
		cookie, err := unix.GetsockoptUint64(int(fd), unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
		if err != nil || cookie != expected {
			checkErr = fmt.Errorf("HTTP probe socket is outside the installed Traefik namespace or the kernel cannot attest it")
		}
	})
	if err != nil {
		return fmt.Errorf("cannot attest HTTP probe socket namespace")
	}
	return checkErr
}

func readProbeProc(path string, limit int64) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("HTTP probe process identity is unavailable")
	}
	defer file.Close()
	var fs unix.Statfs_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return nil, fmt.Errorf("HTTP probe identity requires actual procfs")
	}
	body, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(body) == 0 || int64(len(body)) > limit {
		return nil, fmt.Errorf("HTTP probe process identity is incomplete")
	}
	return body, nil
}

func probeProcessStamp(pid int) (string, uint64, error) {
	boot, err := readProbeProc("/proc/sys/kernel/random/boot_id", 64)
	bootID := strings.TrimSpace(string(boot))
	if err != nil || !probeBootID.MatchString(bootID) {
		return "", 0, fmt.Errorf("HTTP probe boot identity is unavailable")
	}
	stat, err := readProbeProc("/proc/"+strconv.Itoa(pid)+"/stat", 8192)
	if err != nil {
		return "", 0, err
	}
	// comm may contain spaces and parentheses. starttime is field 22, i.e.
	// index 19 after the closing comm delimiter (field 3 is then index 0).
	value := string(stat)
	end := strings.LastIndex(value, ") ")
	if end < 0 || !strings.HasPrefix(value, strconv.Itoa(pid)+" (") {
		return "", 0, fmt.Errorf("invalid HTTP probe process record")
	}
	fields := strings.Fields(value[end+2:])
	if len(fields) < 20 || (fields[0] != "R" && fields[0] != "S" && fields[0] != "D") {
		return "", 0, fmt.Errorf("installed Traefik process is not running")
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || ticks == 0 {
		return "", 0, fmt.Errorf("installed Traefik process has no start identity")
	}
	return bootID, ticks, nil
}

func checkProbeProcess(identity TraefikProbeIdentity) error {
	boot, ticks, err := probeProcessStamp(identity.PID)
	if err != nil || boot != identity.BootID || ticks != identity.StartTimeTicks {
		return fmt.Errorf("installed Traefik process incarnation changed")
	}
	return nil
}

func checkProbeNamespace(ctx context.Context, identity TraefikProbeIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkProbeProcess(identity); err != nil {
		return err
	}
	// These are kernel namespace handles: following procfs's ns symlinks is
	// intentional. Verify the filesystem and inode, not readlink text.
	for _, name := range []string{"self", "thread-self", strconv.Itoa(identity.PID)} {
		device, inode, err := probeNamespaceID(name)
		if err != nil || device != identity.NamespaceDevice || inode != identity.NamespaceInode {
			return fmt.Errorf("HTTP probe must already share the installed Traefik network namespace")
		}
	}
	if err := checkProbeProcess(identity); err != nil {
		return err
	}
	return ctx.Err()
}

func probeNamespaceID(name string) (uint64, uint64, error) {
	file, err := os.Open("/proc/" + name + "/ns/net")
	if err != nil {
		return 0, 0, fmt.Errorf("HTTP probe network namespace handle is unavailable")
	}
	defer file.Close()
	var fs unix.Statfs_t
	var stat unix.Stat_t
	if unix.Fstatfs(int(file.Fd()), &fs) != nil || unix.Fstat(int(file.Fd()), &stat) != nil || fs.Type != unix.NSFS_MAGIC || stat.Dev == 0 || stat.Ino == 0 {
		return 0, 0, fmt.Errorf("HTTP probe requires a real network namespace handle")
	}
	return uint64(stat.Dev), stat.Ino, nil
}

// CaptureTraefikProbeIdentity is a trusted launcher/lab primitive. The caller
// independently selects the installed container and derives its PID/source IP,
// then rechecks that mapping after this call. This function only observes that
// process; it cannot decide that an arbitrary caller-supplied PID is Traefik.
// It must already be in the selected netns, never enters one or gains privileges.
func CaptureTraefikProbeIdentity(ctx context.Context, pid int, sourceIPv4 string) (TraefikProbeIdentity, error) {
	empty := TraefikProbeIdentity{}
	address, err := netip.ParseAddr(sourceIPv4)
	if err != nil || !address.Is4() || !address.IsPrivate() || address.String() != sourceIPv4 || pid <= 1 {
		return empty, fmt.Errorf("HTTP probe identity capture requires a selected process and private ingress IPv4")
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	// Keep namespace checks and socket creation on the same OS thread. No
	// runtime thread namespace is ever changed, including on failure.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	boot, ticks, err := probeProcessStamp(pid)
	if err != nil {
		return empty, err
	}
	device, inode, err := probeNamespaceID(strconv.Itoa(pid))
	if err != nil {
		return empty, err
	}
	identity := TraefikProbeIdentity{PID: pid, StartTimeTicks: ticks, BootID: boot, NamespaceDevice: device, NamespaceInode: inode, SourceIPv4: sourceIPv4}
	if err := checkProbeNamespace(ctx, identity); err != nil {
		return empty, err
	}
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return empty, fmt.Errorf("cannot create HTTP namespace attestation socket")
	}
	defer unix.Close(fd)
	// This socket is never bound, connected or listened on. Its cookie is an
	// independent kernel observation from the just-verified namespace.
	cookie, err := unix.GetsockoptUint64(fd, unix.SOL_SOCKET, unix.SO_NETNS_COOKIE)
	if err != nil || cookie == 0 {
		return empty, fmt.Errorf("kernel cannot attest HTTP probe namespace")
	}
	identity.NamespaceCookie = cookie
	if err := checkProbeNamespace(ctx, identity); err != nil {
		return empty, err
	}
	if err := validateProbeIdentity(identity); err != nil {
		return empty, err
	}
	return identity, nil
}
