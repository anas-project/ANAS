// TEST_CASES: MAILMOD-T-065
package cloudrelay

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type activationFixture struct {
	layout          systemdLayout
	cfg             RuntimeConfig
	dir             int
	binary, binding []byte
	roots           *x509.CertPool
	status          systemdObservation
	actions         []string
	fail            string
	apply           bool
	badProbe        bool
}

func newActivationFixture(t *testing.T) *activationFixture {
	t.Helper()
	l, path, cfg, binary, unit := publishFixture(t)
	for cfg.Listen == cfg.SMTPListen {
		cfg.SMTPListen = spareListen(t)
		path = writeRuntimeConfig(t, cfg)
	}
	if err := publishSystemdFiles(l, path, binary, unit); err != nil {
		t.Fatal(err)
	}
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := installBinding(cfg, config, binary, unit)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := managedDirectory(filepath.Join(l.etc, "anas-mail-relay"), 0700)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(dir) })
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(cfg.TLSCertificate)) {
		t.Fatal("certificate")
	}
	s := systemdObservation{ID: "anas-mail-relay.service", LoadState: "loaded", Fragment: filepath.Join(l.units, "anas-mail-relay.service"), Reload: "no", Transient: "no", DynamicUser: "yes", Active: "inactive", SubState: "dead", UnitFileState: "disabled", MainPID: "0"}
	return &activationFixture{layout: l, cfg: cfg, dir: dir, binary: binary, binding: binding, roots: roots, status: s}
}
func (f *activationFixture) run(ctx context.Context) error {
	ops := systemdActivationOps{show: func(context.Context) (systemdObservation, error) { return f.status, nil }, run: func(_ context.Context, action string) error {
		f.actions = append(f.actions, action)
		if f.fail != action || f.apply {
			switch action {
			case "enable":
				f.status.UnitFileState = "enabled"
			case "start":
				f.status.Active = "active"
				f.status.SubState = "running"
				f.status.MainPID = "123"
			}
		}
		if f.fail == action {
			return ErrPublishUnknown
		}
		return nil
	}, probe: func(_ context.Context, _ RuntimeConfig, _ []byte, s systemdObservation) error {
		if f.badProbe || s.Active != "active" {
			return ErrPublishUnknown
		}
		return nil
	}}
	return activatePublished(ctx, f.layout, f.cfg, f.binary, f.binding, f.dir, f.roots, ops)
}
func actionCount(actions []string, want string) int {
	n := 0
	for _, v := range actions {
		if v == want {
			n++
		}
	}
	return n
}
func TestActivateSystemdStartsOnceAndVerifiesRetry(t *testing.T) {
	f := newActivationFixture(t)
	if err := f.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if actionCount(f.actions, "enable") != 1 || actionCount(f.actions, "start") != 1 {
		t.Fatal(f.actions)
	}
	if err := f.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if actionCount(f.actions, "enable") != 1 || actionCount(f.actions, "start") != 1 {
		t.Fatal("reissued action", f.actions)
	}
}
func TestActivateSystemdRecoversOnlyObservedLostAcknowledgement(t *testing.T) {
	for _, action := range []string{"enable", "start"} {
		t.Run(action, func(t *testing.T) {
			f := newActivationFixture(t)
			f.fail = action
			f.apply = true
			if err := f.run(context.Background()); !errors.Is(err, ErrPublishUnknown) {
				t.Fatal("lost ack", err)
			}
			f.fail = ""
			if err := f.run(context.Background()); err != nil {
				t.Fatal("recovery", err)
			}
			if actionCount(f.actions, action) != 1 {
				t.Fatal("repeated unknown action", f.actions)
			}
		})
	}
}
func TestActivateSystemdDoesNotReplayUnconfirmedActions(t *testing.T) {
	for _, action := range []string{"enable", "start"} {
		t.Run(action, func(t *testing.T) {
			f := newActivationFixture(t)
			f.fail = action
			if err := f.run(context.Background()); !errors.Is(err, ErrPublishUnknown) {
				t.Fatal(err)
			}
			f.fail = ""
			if err := f.run(context.Background()); !errors.Is(err, ErrPublishUnknown) {
				t.Fatal("unknown outcome was retried", err)
			}
			if actionCount(f.actions, action) != 1 {
				t.Fatal("repeated unknown action", f.actions)
			}
		})
	}
}
func TestActivateSystemdBlocksUnownedSourceAndDrift(t *testing.T) {
	t.Run("existing state", func(t *testing.T) {
		f := newActivationFixture(t)
		if err := os.Mkdir(filepath.Join(f.layout.state, "anas-mail-relay"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := f.run(context.Background()); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
		if actionCount(f.actions, "enable") != 0 {
			t.Fatal(f.actions)
		}
	})
	t.Run("drop-in", func(t *testing.T) {
		f := newActivationFixture(t)
		f.status.DropIns = "/foreign"
		if err := f.run(context.Background()); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
		if actionCount(f.actions, "enable") != 0 {
			t.Fatal(f.actions)
		}
	})
	t.Run("untrusted", func(t *testing.T) {
		f := newActivationFixture(t)
		f.roots = x509.NewCertPool()
		if err := f.run(context.Background()); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
	})
	t.Run("occupied port", func(t *testing.T) {
		f := newActivationFixture(t)
		busy, err := net.Listen("tcp", f.cfg.SMTPListen)
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Close()
		if err := f.run(context.Background()); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
	})
	t.Run("foreign active", func(t *testing.T) {
		f := newActivationFixture(t)
		f.status.Active = "active"
		f.status.SubState = "running"
		f.status.MainPID = "123"
		if err := f.run(context.Background()); !errors.Is(err, ErrPublishConflict) {
			t.Fatal(err)
		}
	})
	t.Run("unverified process", func(t *testing.T) {
		f := newActivationFixture(t)
		f.badProbe = true
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if err := f.run(ctx); !errors.Is(err, ErrPublishUnknown) {
			t.Fatal(err)
		}
		if actionCount(f.actions, "start") != 1 {
			t.Fatal(f.actions)
		}
	})
}
