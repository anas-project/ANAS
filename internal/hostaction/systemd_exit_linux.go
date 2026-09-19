//go:build linux

package hostaction

import (
	"context"
	"encoding/hex"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

const systemdName = "org.freedesktop.systemd1"
const systemdUnitInterface = systemdName + ".Unit"
const systemdServiceInterface = systemdName + ".Service"

type systemdBusSource struct {
	conn  *dbus.Conn
	unit  dbus.BusObject
	owner string // Destination on the kernel-authenticated, direct PID 1 connection.
}

func verifySystemdPeerUnit(ctx context.Context, peer Peer, unit string) (result error) {
	if ctx == nil || ctx.Err() != nil || peer.pid <= 1 || peer.uid != 0 || peer.gid != 0 || !installedServiceUnit.MatchString(unit) {
		return ErrUnavailable
	}
	source, err := openSystemdUnitByPID(ctx, uint32(peer.pid))
	if err != nil {
		return err
	}
	defer func() {
		if source.close() != nil {
			result = ErrUnavailable
		}
	}()
	s, err := source.snapshot(ctx)
	if err != nil {
		return err
	}
	if !validInstalledServiceSnapshot(s, peer, unit) || ctx.Err() != nil {
		return ErrUnavailable
	}
	again, err := source.snapshot(ctx)
	if err != nil || !validInstalledServiceSnapshot(again, peer, unit) || again.Invocation != s.Invocation ||
		again.Start != s.Start || again.Restarts != s.Restarts || ctx.Err() != nil {
		return ErrUnavailable
	}
	return nil
}

func captureSystemdExit(ctx context.Context, peer Peer, process brokerProcess) (*systemdExitWatch, error) {
	if ctx == nil || ctx.Err() != nil || process == nil || process.alive() != nil || peer.pid <= 1 ||
		peer.uid != 0 || peer.gid != 0 || !peer.verified {
		return nil, ErrUnavailable
	}
	source, err := openSystemdUnitByPID(ctx, uint32(peer.pid))
	if err != nil {
		return nil, err
	}
	watch, err := bindSystemdExit(ctx, peer, process, source)
	if err != nil {
		_ = source.close()
		return nil, err
	}
	return watch, nil
}

func openSystemdUnitByPID(ctx context.Context, targetPID uint32) (*systemdBusSource, error) {
	if ctx == nil || ctx.Err() != nil || targetPID <= 1 || os.Getuid() != 0 || os.Geteuid() != 0 {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Installed owner/executor processes are root. Connect directly to PID 1,
	// not a system bus whose daemon may legitimately run as an unprivileged
	// account. Never consult DBUS_* or fall back to a caller-selected endpoint.
	dialer := net.Dialer{Timeout: 3 * time.Second}
	c, err := dialer.DialContext(ctx, "unix", "/run/systemd/private")
	if err != nil {
		return nil, ErrUnavailable
	}
	stream, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, ErrUnavailable
	}
	keep := false
	defer func() {
		if !keep {
			_ = stream.Close()
		}
	}()
	// Socket credentials bind the manager itself, not a replaceable bus name.
	if verifySystemdManagerConnection(stream) != nil {
		return nil, ErrUnavailable
	}
	// Authentication has no context API; the socket deadline bounds it. A
	// direct systemd peer is not a message bus and must NOT receive Hello.
	deadline, _ := ctx.Deadline()
	if stream.SetDeadline(deadline) != nil {
		return nil, ErrUnavailable
	}
	conn, err := dbus.NewConn(stream)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer func() {
		if !keep {
			_ = conn.Close()
		}
	}()
	if conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}) != nil || stream.SetDeadline(time.Time{}) != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	owner := systemdName
	manager := conn.Object(owner, dbus.ObjectPath("/org/freedesktop/systemd1"))
	var path dbus.ObjectPath
	if manager.CallWithContext(ctx, systemdName+".Manager.GetUnitByPID", dbus.FlagNoAutoStart, targetPID).Store(&path) != nil ||
		!path.IsValid() || !strings.HasPrefix(string(path), "/org/freedesktop/systemd1/unit/") {
		return nil, ErrUnavailable
	}
	source := &systemdBusSource{conn: conn, unit: conn.Object(owner, path), owner: owner}
	if source.unit.CallWithContext(ctx, systemdUnitInterface+".Ref", dbus.FlagNoAutoStart).Err != nil {
		return nil, ErrUnavailable
	}
	keep = true
	return source, nil
}

func verifySystemdManagerConnection(stream *net.UnixConn) error {
	if stream == nil {
		return ErrUnavailable
	}
	raw, err := stream.SyscallConn()
	if err != nil {
		return ErrUnavailable
	}
	verified := false
	if raw.Control(func(fd uintptr) {
		kind, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_TYPE)
		if e != nil || kind != unix.SOCK_STREAM {
			return
		}
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		verified = e == nil && cred != nil && cred.Pid == 1 && cred.Uid == 0 && cred.Gid == 0
	}) != nil || !verified {
		return ErrUnavailable
	}
	return nil
}

func (b *systemdBusSource) snapshot(ctx context.Context) (systemdSnapshot, error) {
	s := systemdSnapshot{}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	get := func(iface, name string, target any) error {
		var v dbus.Variant
		if b.unit.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", dbus.FlagNoAutoStart, iface, name).Store(&v) != nil || v.Store(target) != nil {
			return ErrUnavailable
		}
		return nil
	}
	var invocation, after []byte
	if get(systemdUnitInterface, "InvocationID", &invocation) != nil || len(invocation) != 16 {
		return s, ErrUnavailable
	}
	for _, p := range []struct {
		iface, name string
		value       any
	}{
		{systemdUnitInterface, "Id", &s.ID}, {systemdUnitInterface, "LoadState", &s.LoadState},
		{systemdUnitInterface, "ActiveState", &s.Active}, {systemdUnitInterface, "SubState", &s.Sub},
		{systemdUnitInterface, "FragmentPath", &s.Fragment}, {systemdUnitInterface, "DropInPaths", &s.DropIns},
		{systemdUnitInterface, "Transient", &s.Transient},
		{systemdServiceInterface, "User", &s.User}, {systemdServiceInterface, "Group", &s.Group},
		{systemdServiceInterface, "Type", &s.Type}, {systemdServiceInterface, "Restart", &s.Restart},
		{systemdServiceInterface, "KillMode", &s.KillMode}, {systemdServiceInterface, "Result", &s.Result},
		{systemdServiceInterface, "MainPID", &s.MainPID}, {systemdServiceInterface, "ExecMainPID", &s.ExecPID},
		{systemdServiceInterface, "ControlPID", &s.ControlPID}, {systemdServiceInterface, "NRestarts", &s.Restarts},
		{systemdServiceInterface, "ExecMainStartTimestampMonotonic", &s.Start},
		{systemdServiceInterface, "ExecMainExitTimestampMonotonic", &s.Stop},
		{systemdServiceInterface, "ExecMainCode", &s.Code}, {systemdServiceInterface, "ExecMainStatus", &s.Status},
		{systemdServiceInterface, "DynamicUser", &s.DynamicUser}, {systemdServiceInterface, "Delegate", &s.Delegate},
	} {
		if get(p.iface, p.name, p.value) != nil {
			return systemdSnapshot{}, ErrUnavailable
		}
	}
	if get(systemdUnitInterface, "InvocationID", &after) != nil || hex.EncodeToString(invocation) != hex.EncodeToString(after) || ctx.Err() != nil {
		return systemdSnapshot{}, ErrUnavailable
	}
	s.Invocation = hex.EncodeToString(invocation)
	return s, nil
}

func (b *systemdBusSource) empty(ctx context.Context, name string) (bool, error) {
	if !hostUnitName.MatchString(name) {
		return false, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var processes []struct {
		Group   string
		PID     uint32
		Command string
	}
	manager := b.conn.Object(b.owner, dbus.ObjectPath("/org/freedesktop/systemd1"))
	if manager.CallWithContext(ctx, systemdName+".Manager.GetUnitProcesses", dbus.FlagNoAutoStart, name).Store(&processes) != nil {
		return false, ErrUnavailable
	}
	return len(processes) == 0, nil
}

func (b *systemdBusSource) close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := b.unit.CallWithContext(ctx, systemdUnitInterface+".Unref", dbus.FlagNoAutoStart).Err
	closed := b.conn.Close()
	if err != nil || closed != nil {
		return ErrUnavailable
	}
	return nil
}
