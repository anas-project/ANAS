package cloudrelay

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"
)

type systemdActivationOps struct {
	show  func(context.Context) (systemdObservation, error)
	run   func(context.Context, string) error
	probe func(context.Context, RuntimeConfig, []byte, systemdObservation) error
}

func systemdAction(ctx context.Context, action string) error {
	var args []string
	switch action {
	case "reload":
		args = []string{"daemon-reload"}
	case "enable":
		args = []string{"enable", "anas-mail-relay.service"}
	case "start":
		args = []string{"start", "anas-mail-relay.service"}
	default:
		return ErrPublishConflict
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if cmd.Run() != nil {
		return ErrPublishUnknown
	}
	return nil
}
func realSystemdProbe(ctx context.Context, cfg RuntimeConfig, binary []byte, s systemdObservation) error {
	pid, err := strconv.ParseUint(s.MainPID, 10, 31)
	if err != nil || pid == 0 || s.Active != "active" || s.SubState != "running" {
		return ErrPublishUnknown
	}
	if verifySystemdProcess(s.MainPID, binary) != nil {
		return ErrPublishUnknown
	}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil || localSourceStatus(ctx, cfg, roots) != nil {
		return ErrPublishUnknown
	}
	again, err := systemdShow(ctx)
	if err != nil || !again.owned("/etc/systemd/system/anas-mail-relay.service") || again.Active != "active" || again.SubState != "running" || again.MainPID != s.MainPID || verifySystemdProcess(again.MainPID, binary) != nil {
		return ErrPublishUnknown
	}
	return nil
}

func actionMarker(binding []byte, name string) []byte {
	sum := sha256.Sum256(append(append([]byte("anas.mail-systemd-action/v1\x00"+name+"\x00"), binding...), 0))
	return []byte(hex.EncodeToString(sum[:]) + "\n")
}
func checkActionMarker(dir int, name string, binding []byte) (bool, error) {
	yes, err := verifyInstallFile(dir, name, actionMarker(binding, name), 0600)
	if err != nil {
		return false, ErrPublishConflict
	}
	if !yes {
		staged, e := existsAt(dir, "."+name+".anas-stage")
		if e != nil || staged {
			return false, ErrPublishUnknown
		}
	}
	return yes, nil
}
func recordActionMarker(dir int, name string, binding []byte) error {
	return publishInstallFile(dir, name, actionMarker(binding, name), 0600)
}

// newSourceAbsent prevents systemd's StateDirectory from taking ownership of
// an unrecognized pre-existing directory. It must be checked before starting.
func newSourceAbsent(layout systemdLayout) error {
	if err := checkOptionalConflicts(layout.state, "anas-mail-relay"); err != nil {
		return err
	}
	return checkOptionalConflicts(layout.privateState, "anas-mail-relay")
}
func freshStartPreflight(cfg RuntimeConfig, layout systemdLayout, roots *x509.CertPool) error {
	host, _, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return ErrPublishConflict
	}
	ip := net.ParseIP(host)
	if ip == nil || (!ip.IsUnspecified() && !ip.IsLoopback()) {
		return ErrPublishConflict
	}
	result := preflightRuntime(cfg, roots)
	if result.Certificate != "passed" || result.CertificateTrust != "passed" || result.HTTPSPort != "available-now" || result.SMTPPort != "available-now" || cfg.SMTPListen == "" {
		return ErrPublishConflict
	}
	state, err := managedDirectory(layout.state, 0)
	if err != nil {
		return err
	}
	defer unix.Close(state)
	var fs unix.Statfs_t
	if unix.Fstatfs(state, &fs) != nil || fs.Bsize <= 0 {
		return ErrPublishUnknown
	}
	available, block := uint64(fs.Bavail), uint64(fs.Bsize)
	if available <= math.MaxUint64/block && available*block < uint64(cfg.CapacityBytes) {
		return ErrPublishConflict
	}
	return nil
}

// activatePublished is called under the verified publication lock. It writes
// durable intent before each systemd change and never repeats a lost start or
// enable acknowledgement without an authoritative confirming observation.
func activatePublished(ctx context.Context, layout systemdLayout, cfg RuntimeConfig, binary, binding []byte, dir int, roots *x509.CertPool, ops systemdActivationOps) error {
	if ctx.Err() != nil || ops.show == nil || ops.run == nil || ops.probe == nil {
		return ErrPublishUnknown
	}
	if err := ops.run(ctx, "reload"); err != nil {
		return ErrPublishUnknown
	}
	observed, err := ops.show(ctx)
	if err != nil {
		return ErrPublishUnknown
	}
	if !observed.owned(filepath.Join(layout.units, "anas-mail-relay.service")) {
		return ErrPublishConflict
	}
	authorized, err := checkActionMarker(dir, "start.authorized", binding)
	if err != nil {
		return err
	}
	enableIssued, err := checkActionMarker(dir, "enable.issued", binding)
	if err != nil {
		return err
	}
	startIssued, err := checkActionMarker(dir, "start.issued", binding)
	if err != nil {
		return err
	}
	if !authorized && (enableIssued || startIssued) {
		return ErrPublishConflict
	}
	if !authorized {
		if observed.Active != "inactive" || observed.SubState != "dead" || observed.MainPID != "0" || observed.UnitFileState != "disabled" {
			return ErrPublishConflict
		}
		if err = newSourceAbsent(layout); err != nil {
			return err
		}
		if err = freshStartPreflight(cfg, layout, roots); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ErrPublishUnknown
		}
		if err = recordActionMarker(dir, "start.authorized", binding); err != nil {
			return err
		}
		authorized = true
	}
	if observed.UnitFileState == "disabled" {
		if enableIssued || observed.Active != "inactive" || observed.SubState != "dead" || observed.MainPID != "0" {
			return ErrPublishUnknown
		}
		if err = newSourceAbsent(layout); err != nil {
			return err
		}
		if err = freshStartPreflight(cfg, layout, roots); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ErrPublishUnknown
		}
		if err = recordActionMarker(dir, "enable.issued", binding); err != nil {
			return err
		}
		if err = ops.run(ctx, "enable"); err != nil {
			return ErrPublishUnknown
		}
		observed, err = ops.show(ctx)
		if err != nil || !observed.owned(filepath.Join(layout.units, "anas-mail-relay.service")) || observed.UnitFileState != "enabled" {
			return ErrPublishUnknown
		}
	} else if !enableIssued {
		return ErrPublishConflict
	}
	if observed.Active == "active" {
		return ops.probe(ctx, cfg, binary, observed)
	}
	if observed.Active != "inactive" || observed.SubState != "dead" || observed.MainPID != "0" || startIssued {
		return ErrPublishUnknown
	}
	// A machine reboot after enable may have attempted and stopped this unit. Do
	// not make another start claim if any state now exists.
	if err = newSourceAbsent(layout); err != nil {
		return ErrPublishUnknown
	}
	if err = freshStartPreflight(cfg, layout, roots); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ErrPublishUnknown
	}
	if err = recordActionMarker(dir, "start.issued", binding); err != nil {
		return err
	}
	if err = ops.run(ctx, "start"); err != nil {
		return ErrPublishUnknown
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		observed, err = ops.show(ctx)
		if err != nil || !observed.owned(filepath.Join(layout.units, "anas-mail-relay.service")) {
			return ErrPublishUnknown
		}
		if observed.Active == "active" && observed.SubState == "running" && ops.probe(ctx, cfg, binary, observed) == nil {
			return nil
		}
		if observed.Active == "failed" || observed.Active == "inactive" {
			return ErrPublishUnknown
		}
		select {
		case <-ctx.Done():
			return ErrPublishUnknown
		case <-time.After(250 * time.Millisecond):
		}
	}
	return ErrPublishUnknown
}

// ActivateSystemdInstallation starts only the exact previously published unit.
// Success verifies a local source, not DNS, public reachability or delivery.
func ActivateSystemdInstallation(ctx context.Context, configPath string) error {
	if os.Geteuid() != 0 {
		return ErrPublishConflict
	}
	file, err := os.Open("/proc/self/exe")
	if err != nil {
		return ErrPublishConflict
	}
	defer file.Close()
	binary, err := io.ReadAll(io.LimitReader(file, 128<<20+1))
	if err != nil || len(binary) == 0 || len(binary) > 128<<20 {
		return ErrPublishConflict
	}
	layout := systemdLayout{etc: "/etc", libexec: "/usr/local/libexec", units: "/etc/systemd/system", state: "/var/lib", otherUnits: []string{"/run/systemd/system", "/run/systemd/transient", "/usr/lib/systemd/system", "/lib/systemd/system"}, privateState: "/var/lib/private"}
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		return ErrPublishUnknown
	}
	return withVerifiedPublication(layout, configPath, binary, systemdUnit, func(cfg RuntimeConfig, dir int, binding []byte) error {
		return activatePublished(ctx, layout, cfg, binary, binding, dir, roots, systemdActivationOps{show: systemdShow, run: systemdAction, probe: realSystemdProbe})
	})
}
