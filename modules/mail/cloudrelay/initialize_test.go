// TEST_CASES: MAILMOD-T-056
package cloudrelay

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func initializationFixture(t *testing.T) (RuntimeConfig, string) {
	t.Helper()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, filepath.Join(parent, "source"), "127.0.0.1:18448")
	return cfg, writeRuntimeConfig(t, cfg)
}

func initializationStage(cfg RuntimeConfig) string {
	sum := sha256.Sum256([]byte(filepath.Base(cfg.Directory)))
	return filepath.Join(filepath.Dir(cfg.Directory), ".anas-mail-"+hex.EncodeToString(sum[:16])+".staging")
}

func TestInitializeRuntimePreservesLiveStateAndSecrets(t *testing.T) {
	cfg, config := initializationFixture(t)
	if err := InitializeRuntimeFile(config); err != nil {
		t.Fatal(err)
	}
	seal, err := os.ReadFile(filepath.Join(cfg.Directory, "initialization.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{cfg.Reader, cfg.Control, cfg.TLSPrivateKey} {
		if strings.Contains(string(seal), value) {
			t.Fatal("seal leaks private configuration")
		}
	}
	ledger, err := OpenRouteLedger(cfg.Directory, cfg.SourceID, cfg.Generation, cfg.Domains)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ledger.Replace(0, routeOp1, []ingress.Route{routeFixture("alice@example.test")}); err != nil {
		t.Fatal(err)
	}
	source, err := OpenOriginalStore(ledger, cfg.CapacityBytes, cfg.DeliveredDays)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.Store(routeOp2, "", []string{"alice@example.test"}, []byte("retained original")); err != nil {
		t.Fatal(err)
	}
	if err = InitializeRuntimeFile(config); err != nil {
		t.Fatalf("repeat initialization: %v", err)
	}
	_, raw, err := source.Read(routeOp2)
	if err != nil || string(raw) != "retained original" {
		t.Fatalf("repeat changed existing mail: %v", err)
	}
	changed := cfg
	changed.Control = strings.Repeat("d", 64)
	if err = InitializeRuntimeFile(writeRuntimeConfig(t, changed)); !errors.Is(err, ErrInitializeConflict) {
		t.Fatalf("changed credentials claimed source: %v", err)
	}
	_, raw, err = source.Read(routeOp2)
	if err != nil || string(raw) != "retained original" {
		t.Fatalf("conflict changed mail: %v", err)
	}
}

func TestInitializeRuntimeResumesOnlyVerifiedStaging(t *testing.T) {
	cfg, config := initializationFixture(t)
	if err := InitializeRuntimeFile(config); err != nil {
		t.Fatal(err)
	}
	stage := initializationStage(cfg)
	if err := os.Rename(cfg.Directory, stage); err != nil {
		t.Fatal(err)
	}
	if err := InitializeRuntimeFile(config); err != nil {
		t.Fatalf("verified staging recovery: %v", err)
	}
	if _, err := os.Stat(cfg.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published staging still exists: %v", err)
	}
}

func TestInitializeRuntimeRejectsUnownedOrIncompleteState(t *testing.T) {
	for _, scenario := range []string{"target", "partial", "stage-symlink", "bad-cert", "parent-permissions"} {
		t.Run(scenario, func(t *testing.T) {
			cfg, config := initializationFixture(t)
			stage := initializationStage(cfg)
			switch scenario {
			case "target":
				if err := os.Mkdir(cfg.Directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(cfg.Directory, "sentinel"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "partial":
				if err := os.Mkdir(stage, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stage, "sentinel"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			case "stage-symlink":
				if err := os.Symlink(filepath.Dir(cfg.Directory), stage); err != nil {
					t.Fatal(err)
				}
			case "bad-cert":
				cfg.TLSPrivateKey = "invalid"
				config = writeRuntimeConfig(t, cfg)
			case "parent-permissions":
				if err := os.Chmod(filepath.Dir(cfg.Directory), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := InitializeRuntimeFile(config); err == nil {
				t.Fatal("unsafe or incomplete state accepted")
			}
			if scenario == "target" || scenario == "partial" {
				path := filepath.Join(cfg.Directory, "sentinel")
				if scenario == "partial" {
					path = filepath.Join(stage, "sentinel")
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "keep" {
					t.Fatalf("existing data changed: %v", err)
				}
			}
			if scenario != "target" {
				if _, err := os.Lstat(cfg.Directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unsafe target created: %v", err)
				}
			}
		})
	}
}

func TestInitializeRuntimeConcurrentDoesNotOverwrite(t *testing.T) {
	_, config := initializationFixture(t)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; results <- InitializeRuntimeFile(config) }()
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrInitializeConflict) {
			t.Fatalf("unexpected initialization result: %v", err)
		}
	}
	if success == 0 {
		t.Fatal("no initializer completed")
	}
	if err := InitializeRuntimeFile(config); err != nil {
		t.Fatalf("retry verification: %v", err)
	}
}
