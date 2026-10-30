package cloudrelay

import (
	"context"
	"crypto/hmac"
	"crypto/x509"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// The lock is held while the caller inspects or changes the service. This
// revalidates the private binding and all three exact published files without
// repairing, creating, or replacing them.
func withVerifiedPublication(layout systemdLayout, configPath string, binary, unit []byte, fn func(RuntimeConfig, int, []byte) error) error {
	config, cfg, err := readPrivateInstallInput(configPath)
	if err != nil {
		return err
	}
	defer clear(config)
	if cfg.Directory != filepath.Join(layout.state, "anas-mail-relay", "data", "source") || cfg.SMTPListen == "" {
		return ErrPublishConflict
	}
	binding, err := installBinding(cfg, config, binary, unit)
	if err != nil {
		return err
	}
	etc, err := managedDirectory(filepath.Join(layout.etc, "anas-mail-relay"), 0700)
	if err != nil {
		return err
	}
	defer unix.Close(etc)
	fd, err := unix.Openat(etc, "install.lock", unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrPublishUnknown
	}
	defer unix.Close(fd)
	if !secureNoticeFile(fd, false) || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return ErrPublishConflict
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	dupe, err := unix.Dup(fd)
	if err != nil {
		return ErrPublishUnknown
	}
	f := os.NewFile(uintptr(dupe), "publication binding")
	actual, err := io.ReadAll(io.LimitReader(f, 129))
	f.Close()
	if err != nil || !hmac.Equal(actual, binding) {
		return ErrPublishConflict
	}
	for _, label := range []string{"config", "binary", "unit"} {
		for _, suffix := range []string{".intent", ".done"} {
			present, e := checkActionMarker(etc, "publish-"+label+suffix, binding)
			if e != nil {
				return e
			}
			if !present {
				return ErrPublishUnknown
			}
		}
	}
	binaryDir, err := managedDirectory(filepath.Join(layout.libexec, "anas-mail-relay"), 0755)
	if err != nil {
		return err
	}
	defer unix.Close(binaryDir)
	units, err := managedDirectory(layout.units, 0)
	if err != nil {
		return err
	}
	defer unix.Close(units)
	for _, item := range []struct {
		dir  int
		name string
		data []byte
		mode uint32
	}{{etc, "runtime.json", config, 0600}, {binaryDir, "cloudrelay", binary, 0755}, {units, "anas-mail-relay.service", unit, 0644}} {
		ok, e := verifyInstallFile(item.dir, item.name, item.data, item.mode)
		if e != nil || !ok {
			return ErrPublishConflict
		}
	}
	if yes, e := existsAt(units, "anas-mail-relay.service.d"); e != nil || yes {
		return ErrPublishConflict
	}
	for _, path := range layout.otherUnits {
		if err = checkOptionalConflicts(path, "anas-mail-relay.service", "anas-mail-relay.service.d"); err != nil {
			return err
		}
	}
	return fn(cfg, etc, binding)
}

type systemdObservation struct{ LoadState, ID, Fragment, DropIns, Reload, Transient, DynamicUser, Active, SubState, UnitFileState, MainPID string }

// parseSystemdObservation refuses missing or repeated properties. A malformed
// or changing manager response is never interpreted as a stopped service.
func parseSystemdObservation(raw []byte) (systemdObservation, error) {
	if len(raw) > 16384 {
		return systemdObservation{}, ErrPublishUnknown
	}
	expected := map[string]*string{}
	var out systemdObservation
	expected["LoadState"] = &out.LoadState
	expected["Id"] = &out.ID
	expected["FragmentPath"] = &out.Fragment
	expected["DropInPaths"] = &out.DropIns
	expected["NeedDaemonReload"] = &out.Reload
	expected["Transient"] = &out.Transient
	expected["DynamicUser"] = &out.DynamicUser
	expected["ActiveState"] = &out.Active
	expected["SubState"] = &out.SubState
	expected["UnitFileState"] = &out.UnitFileState
	expected["MainPID"] = &out.MainPID
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		dest, allowed := expected[key]
		if !ok || !allowed || seen[key] || strings.ContainsAny(value, "\r\x00") {
			return systemdObservation{}, ErrPublishUnknown
		}
		*dest = value
		seen[key] = true
	}
	if len(seen) != len(expected) {
		return systemdObservation{}, ErrPublishUnknown
	}
	return out, nil
}
func (s systemdObservation) owned(fragment string) bool {
	return s.ID == "anas-mail-relay.service" && s.LoadState == "loaded" && s.Fragment == fragment && s.DropIns == "" && s.Reload == "no" && s.Transient == "no" && s.DynamicUser == "yes" && (s.UnitFileState == "enabled" || s.UnitFileState == "disabled")
}

func systemdShow(ctx context.Context) (systemdObservation, error) {
	return systemdShowUnit(ctx, "anas-mail-relay.service")
}

func systemdShowUnit(ctx context.Context, unit string) (systemdObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", unit, "--property=Id,LoadState,FragmentPath,DropInPaths,NeedDaemonReload,Transient,DynamicUser,ActiveState,SubState,UnitFileState,MainPID", "--no-pager")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	// Fixed property output is normally tiny. Reject abnormal output or failure.
	var out boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if cmd.Run() != nil || out.overflow {
		return systemdObservation{}, ErrPublishUnknown
	}
	return parseSystemdObservation(out.bytes)
}

type boundedBuffer struct {
	bytes    []byte
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > 16384-len(b.bytes) {
		b.overflow = true
		return len(p), nil
	}
	b.bytes = append(b.bytes, p...)
	return len(p), nil
}

// InspectSystemdInstallation only reads published materials and systemd's
// currently loaded state. It does not reload or start a service.
func InspectSystemdInstallation(ctx context.Context, configPath string) (string, error) {
	if os.Geteuid() != 0 {
		return "", ErrPublishConflict
	}
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		return "", ErrPublishConflict
	}
	defer file.Close()
	binary, err := io.ReadAll(io.LimitReader(file, 128<<20+1))
	if err != nil || len(binary) == 0 || len(binary) > 128<<20 {
		return "", ErrPublishConflict
	}
	layout := systemdLayout{etc: "/etc", libexec: "/usr/local/libexec", units: "/etc/systemd/system", state: "/var/lib", otherUnits: []string{"/run/systemd/system", "/run/systemd/transient", "/usr/lib/systemd/system", "/lib/systemd/system"}}
	state := ""
	err = withVerifiedPublication(layout, configPath, binary, systemdUnit, func(cfg RuntimeConfig, _ int, _ []byte) error {
		observed, e := systemdShow(ctx)
		if e != nil {
			return e
		}
		if !observed.owned("/etc/systemd/system/anas-mail-relay.service") {
			return ErrPublishConflict
		}
		switch observed.Active {
		case "inactive":
			if observed.SubState != "dead" || observed.MainPID != "0" {
				return ErrPublishUnknown
			}
			state = "inactive"
		case "active":
			pid, parseErr := strconv.ParseUint(observed.MainPID, 10, 31)
			if observed.SubState != "running" || parseErr != nil || pid == 0 {
				return ErrPublishUnknown
			}
			if verifySystemdProcess(observed.MainPID, binary) != nil {
				return ErrPublishUnknown
			}
			roots, e := x509.SystemCertPool()
			if e != nil || roots == nil || localSourceStatus(ctx, cfg, roots) != nil {
				return ErrPublishUnknown
			}
			again, e := systemdShow(ctx)
			if e != nil || !again.owned("/etc/systemd/system/anas-mail-relay.service") || again.Active != "active" || again.SubState != "running" || again.MainPID != observed.MainPID || verifySystemdProcess(again.MainPID, binary) != nil {
				return ErrPublishUnknown
			}
			state = "active-local-verified"
		default:
			return ErrPublishUnknown
		}
		return nil
	})
	return state, err
}
