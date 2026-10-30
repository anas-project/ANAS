// TEST_CASES: MAILMOD-T-061
package cloudrelay

import (
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func prepareFixture(t *testing.T) (RuntimeConfig, string, *x509.CertPool) {
	t.Helper()
	cfg, _ := initializationFixture(t)
	cfg.Listen = spareListen(t)
	cfg.SMTPListen = spareListen(t)
	for cfg.SMTPListen == cfg.Listen {
		cfg.SMTPListen = spareListen(t)
	}
	cfg.SMTPHostname = "relay.example.test"
	path := writeRuntimeConfig(t, cfg)
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(cfg.TLSCertificate)) {
		t.Fatal("test certificate")
	}
	return cfg, path, pool
}

func TestPrepareInstallInitializesAndRecoversWithoutLosingMail(t *testing.T) {
	cfg, path, pool := prepareFixture(t)
	result, err := prepareInstallRuntime(path, cfg, pool)
	if err != nil || result.Directory != "available" || result.PublicReachability != "unknown" {
		t.Fatalf("prepare: %+v %v", result, err)
	}
	ledger, err := OpenRouteLedger(cfg.Directory, cfg.SourceID, cfg.Generation, cfg.Domains)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Replace(0, routeOp1, []ingress.Route{routeFixture("alice@example.test")}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Store(routeOp2, "", []string{"alice@example.test"}, []byte("retained")); err != nil {
		t.Fatal(err)
	}
	// Existing service may own both ports. Rechecking sealed state must still work.
	a, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := net.Listen("tcp", cfg.SMTPListen)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	result, err = prepareInstallRuntime(path, cfg, pool)
	if err != nil || result.Directory != "occupied" || result.HTTPSPort != "unavailable" || result.SMTPPort != "unavailable" {
		t.Fatalf("recovery: %+v %v", result, err)
	}
	_, raw, err := store.Read(routeOp2)
	if err != nil || string(raw) != "retained" {
		t.Fatalf("lost mail: %v", err)
	}
}

func TestPrepareInstallRejectsUnsafeFreshAndDriftedState(t *testing.T) {
	t.Run("untrusted", func(t *testing.T) {
		cfg, path, _ := prepareFixture(t)
		_, err := prepareInstallRuntime(path, cfg, x509.NewCertPool())
		if !errors.Is(err, ErrInstallPreflight) {
			t.Fatal(err)
		}
		if _, err = os.Lstat(cfg.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created directory")
		}
	})
	t.Run("port", func(t *testing.T) {
		cfg, _, pool := prepareFixture(t)
		busy, err := net.Listen("tcp", cfg.SMTPListen)
		if err != nil {
			t.Fatal(err)
		}
		defer busy.Close()
		path := writeRuntimeConfig(t, cfg)
		_, err = prepareInstallRuntime(path, cfg, pool)
		if !errors.Is(err, ErrInstallPreflight) {
			t.Fatal(err)
		}
		if _, err = os.Lstat(cfg.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created directory")
		}
	})
	t.Run("foreign-directory", func(t *testing.T) {
		cfg, path, pool := prepareFixture(t)
		if err := os.Mkdir(cfg.Directory, 0700); err != nil {
			t.Fatal(err)
		}
		sentinel := filepath.Join(cfg.Directory, "foreign")
		if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := prepareInstallRuntime(path, cfg, pool)
		if !errors.Is(err, ErrInstallOutcomeUnknown) {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(sentinel)
		if err != nil || string(raw) != "preserve" {
			t.Fatal("changed foreign state")
		}
	})
	t.Run("config-changed", func(t *testing.T) {
		cfg, path, pool := prepareFixture(t)
		modified := cfg
		modified.Reader = modified.Control
		raw, err := os.ReadFile(writeRuntimeConfig(t, modified))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		_, err = prepareInstallRuntime(path, cfg, pool)
		if !errors.Is(err, ErrInstallPreflight) {
			t.Fatal(err)
		}
		if _, err = os.Lstat(cfg.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("created source after known configuration drift")
		}
	})
}
