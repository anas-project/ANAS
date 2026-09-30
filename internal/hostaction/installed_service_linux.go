//go:build linux

package hostaction

import (
	"context"
	"os"

	"github.com/anas-project/ANAS/internal/audit"
	"golang.org/x/sys/unix"
)

// ServeInstalledActivation is the root executable's only serving entry. It
// accepts no paths or handlers. The service manager must already have installed
// its policy, activation socket and private StateDirectory; only the ledger
// directory below that StateDirectory is created here.
func ServeInstalledActivation(ctx context.Context) (result error) {
	a, err := OpenSystemdActivation(ctx)
	if err != nil {
		return err
	}
	defer func() {
		if a.Close() != nil {
			result = ErrUnavailable
		}
	}()
	j, err := openInstalledAudit()
	if err != nil {
		return err
	}
	defer func() {
		if j.close() != nil {
			result = ErrAudit
		}
	}()
	ledger, err := openInstalledLedger()
	if err != nil {
		return rejectAdmission(ctx, j, Peer{}, "invocation_ledger_unavailable", err)
	}
	defer func() {
		if ledger.close() != nil {
			result = ErrUnavailable
		}
	}()
	return a.Serve(ctx, j, ledger)
}

type installedAudit struct {
	writer *audit.Writer
	dirs   []*os.File
	stats  []unix.Stat_t
}

var hostAuditParts = [...]string{"/", "var", "lib", "anas-hostd"}

func openInstalledAudit() (*installedAudit, error) {
	j := &installedAudit{}
	keep := false
	defer func() {
		if !keep {
			_ = j.close()
		}
	}()
	parent := unix.AT_FDCWD
	for i, name := range hostAuditParts {
		fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, ErrAudit
		}
		j.dirs = append(j.dirs, os.NewFile(uintptr(fd), "host-audit-directory"))
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&unix.S_IFMT != unix.S_IFDIR || st.Mode&0022 != 0 || st.Mode&07000 != 0 || (i == 3 && st.Mode&0777 != 0700) {
			return nil, ErrAudit
		}
		j.stats = append(j.stats, st)
		parent = fd
	}
	if j.check() != nil {
		return nil, ErrAudit
	}
	var err error
	j.writer, err = audit.Open("/var/lib/anas-hostd/audit")
	if err != nil || j.check() != nil {
		return nil, ErrAudit
	}
	keep = true
	return j, nil
}

func (j *installedAudit) check() error {
	if len(j.dirs) != len(hostAuditParts) || len(j.stats) != len(j.dirs) {
		return ErrAudit
	}
	for i, f := range j.dirs {
		var current, named unix.Stat_t
		if unix.Fstat(int(f.Fd()), &current) != nil || !sameInstallationStat(j.stats[i], current, false) {
			return ErrAudit
		}
		if i > 0 && (unix.Fstatat(int(j.dirs[i-1].Fd()), hostAuditParts[i], &named, unix.AT_SYMLINK_NOFOLLOW) != nil || !sameInstallationStat(j.stats[i], named, false)) {
			return ErrAudit
		}
	}
	return nil
}
func (j *installedAudit) AppendContext(ctx context.Context, e audit.Event) (audit.Event, error) {
	if j.check() != nil {
		return audit.Event{}, ErrAudit
	}
	written, err := j.writer.AppendContext(ctx, e)
	if err != nil || j.check() != nil {
		return audit.Event{}, ErrAudit
	}
	return written, nil
}
func (j *installedAudit) close() error {
	failed := false
	if j.writer != nil {
		failed = j.writer.Close() != nil
		j.writer = nil
	}
	for _, f := range j.dirs {
		failed = f.Close() != nil || failed
	}
	j.dirs = nil
	if failed {
		return ErrAudit
	}
	return nil
}
