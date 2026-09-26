//go:build linux

package hostaction

import (
	"context"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

func waitSystemdAuthenticationDrain(ctx context.Context, stream *net.UnixConn) error {
	if ctx == nil || stream == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := stream.SyscallConn()
	if err != nil {
		return ErrUnavailable
	}
	// The direct systemd peer can leave a first method buffered when BEGIN
	// and its binary frame arrive in the same authentication read. Observe
	// actual UNIX send-queue drain, rather than sleep for a guessed interval
	// or retry a possibly executed method. This is sequencing, not an auth
	// acknowledgement: PID1 SO_PEERCRED and every response check still apply.
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		var queued int
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			queued, socketErr = unix.IoctlGetInt(int(fd), unix.TIOCOUTQ)
		}); err != nil || socketErr != nil || queued < 0 {
			return ErrUnavailable
		}
		if queued == 0 {
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
