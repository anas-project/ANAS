package dms

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"time"
)

const nativeCheckpoint = "/tmp/docker-mailserver-config-chksum"

func (a *Adapter) initializationReady(ctx context.Context) error {
	if a.startupReady != nil {
		return a.startupReady(ctx)
	}
	return nativeInitialization(ctx, nativeCheckpoint, nativeMonitorCommand)
}

func nativeMonitorCommand(ctx context.Context, name string, args ...string) (string, error) {
	return commandWithin(ctx, 10*time.Second, name, args...)
}

func nativeInitialization(ctx context.Context, checkpoint string, run func(context.Context, string, ...string) (string, error)) error {
	info, err := os.Lstat(checkpoint)
	if err != nil || !safeCheckpoint(info) || info.Size() == 0 || info.Size() > 1<<20 {
		return &checkFailure{"native-initialization"}
	}
	// In the pinned DMS version, mailserver creates this checkpoint only after
	// generating native account files, then starts changedetector. RUNNING is
	// therefore the barrier after which new input generations are monitored.
	out, err := run(ctx, "/usr/bin/supervisorctl", "status", "changedetector")
	if errors.Is(err, context.DeadlineExceeded) {
		return &checkFailure{"native-initialization-timeout"}
	}
	fields := strings.Fields(out)
	if err != nil || len(fields) < 2 || fields[0] != "changedetector" || fields[1] != "RUNNING" {
		return &checkFailure{"native-initialization"}
	}
	return nil
}

func safeCheckpoint(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && stat.Uid == uint32(os.Geteuid()) && stat.Nlink == 1
}

// PrepareInputs repairs a persisted generation before supervisord can start
// reading DMS inputs. No receipt is confirmed and no mailbox data is changed.
func (a *Adapter) PrepareInputs() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	state, err := a.load()
	if err != nil || len(state.Boxes) == 0 {
		return err
	}
	generated, err := files(state.Boxes, a.CapacityBytes)
	if err != nil {
		return err
	}
	for _, name := range []string{"postfix-accounts.cf", "postfix-virtual.cf", "dovecot-quotas.cf"} {
		if err := atomicFile(a.ConfigDir+"/"+name, generated[name], 0600); err != nil {
			return err
		}
	}
	return nil
}
