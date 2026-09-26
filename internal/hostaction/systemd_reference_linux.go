//go:build linux

package hostaction

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// Unit.Ref needs a message-bus client identity: the direct PID1 peer rejects
// it with InvalidArgs. This auxiliary connection only prevents collection of
// the unit object. It supplies NO process, authorization or exit facts. All
// those observations still come from the separately kernel-pinned PID1 socket;
// a lying/lost reference bus can cause unavailable evidence, never authorize
// an action or turn missing exit evidence into success.
type systemdUnitReference struct {
	conn *dbus.Conn
	unit dbus.BusObject
}

func retainSystemdUnit(ctx context.Context, path dbus.ObjectPath) (*systemdUnitReference, error) {
	if ctx == nil || ctx.Err() != nil || os.Geteuid() != 0 || !path.IsValid() ||
		!strings.HasPrefix(string(path), "/org/freedesktop/systemd1/unit/") {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 3 * time.Second}
	stream, err := dialer.DialContext(ctx, "unix", "/run/dbus/system_bus_socket")
	if err != nil {
		return nil, ErrUnavailable
	}
	keep := false
	defer func() {
		if !keep {
			_ = stream.Close()
		}
	}()
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
	// This is the fixed system bus, not the direct manager connection. No
	// DBUS_* environment, auto-launch or caller-selected address is consulted.
	if conn.Auth([]dbus.Auth{dbus.AuthExternal(strconv.Itoa(os.Geteuid()))}) != nil ||
		waitSystemdAuthenticationDrain(ctx, stream.(*net.UnixConn)) != nil || conn.Hello() != nil ||
		stream.SetDeadline(time.Time{}) != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	unit := conn.Object(systemdName, path)
	if unit.CallWithContext(ctx, systemdUnitInterface+".Ref", dbus.FlagNoAutoStart).Err != nil || ctx.Err() != nil {
		return nil, ErrUnavailable
	}
	keep = true
	return &systemdUnitReference{conn: conn, unit: unit}, nil
}

func (r *systemdUnitReference) close() error {
	if r == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := r.unit.CallWithContext(ctx, systemdUnitInterface+".Unref", dbus.FlagNoAutoStart).Err
	closed := r.conn.Close()
	if err != nil || closed != nil {
		return ErrUnavailable
	}
	return nil
}
