// TEST_CASES: MAILMOD-T-065
package cloudrelay

import (
	"context"
	"crypto/x509"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"golang.org/x/sys/unix"
)

// This test is only enabled by the isolated OSA harness, which creates and
// cleans a random unit. It never targets the fixed production unit.
func TestActivateSystemdLiveUnit(t *testing.T) {
	name := os.Getenv("ANAS_MAIL_TEST_ACTIVATE_NAME")
	if name == "" {
		t.Skip("requires isolated systemd fixture")
	}
	if os.Geteuid() != 0 || !regexp.MustCompile(`^anas-mail-isolated-[0-9a-f]{16}\.service$`).MatchString(name) {
		t.Fatal("invalid isolated unit")
	}
	work := os.Getenv("ANAS_MAIL_TEST_ACTIVATE_WORK")
	if work != "/run/"+name[:len(name)-len(".service")] {
		t.Fatal("invalid isolated directory")
	}
	cfg, err := LoadRuntimeConfig(filepath.Join(work, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(filepath.Join(work, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("test root")
	}
	binary, err := os.ReadFile(os.Getenv("ANAS_MAIL_TEST_ACTIVATE_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(work, "fake-state")
	if err = os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := managedDirectory(work, 0755)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dir)
	layout := systemdLayout{units: "/run/systemd/system", state: state}
	var actions []string
	actualShow := func(ctx context.Context) (systemdObservation, error) {
		s, e := systemdShowUnit(ctx, name)
		if e != nil {
			return s, e
		}
		if s.ID != name || s.Fragment != "/run/systemd/system/"+name {
			return s, ErrPublishConflict
		}
		s.ID = "anas-mail-relay.service"
		s.Fragment = filepath.Join(layout.units, "anas-mail-relay.service")
		return s, nil
	}
	ops := systemdActivationOps{show: actualShow, run: func(ctx context.Context, action string) error {
		actions = append(actions, action)
		var args []string
		switch action {
		case "reload":
			args = []string{"daemon-reload"}
		case "enable", "start":
			args = []string{action, name}
		default:
			return ErrPublishConflict
		}
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C"}
		if output, e := cmd.CombinedOutput(); e != nil {
			return fmt.Errorf("isolated systemctl: %w: %s", e, output)
		}
		return nil
	}, probe: func(ctx context.Context, c RuntimeConfig, b []byte, s systemdObservation) error {
		if err := verifySystemdProcess(s.MainPID, b); err != nil {
			return err
		}
		if err := localSourceStatus(ctx, c, roots); err != nil {
			return err
		}
		again, err := actualShow(ctx)
		if err != nil || again.Active != "active" || again.MainPID != s.MainPID {
			return ErrPublishUnknown
		}
		return nil
	}}
	binding := []byte("isolated binding")
	if err = activatePublished(context.Background(), layout, cfg, binary, binding, dir, roots, ops); err != nil {
		t.Fatalf("activation: %v; actions %v", err, actions)
	}
	if err = activatePublished(context.Background(), layout, cfg, binary, binding, dir, roots, ops); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if actionCount(actions, "enable") != 1 || actionCount(actions, "start") != 1 {
		t.Fatalf("repeated action: %v", actions)
	}
}
