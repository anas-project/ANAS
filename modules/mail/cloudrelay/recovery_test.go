// TEST_CASES: MAILMOD-T-054
package cloudrelay

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func writeRecoveryFile(t *testing.T, path string, data []byte) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRecoveryPreservesBindingAndRestarts(t *testing.T) {
	for _, name := range []string{".pending-", "raw-"} {
		t.Run(name, func(t *testing.T) {
			ledger, store, dir := originalFixture(t, routeMax+1000)
			body := []byte("Message-ID: <untrusted@example.test>\r\n\r\noriginal\r\n")
			digest := sha256.Sum256(body)
			reservation, err := ledger.ReserveEnvelope(routeOp2, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alias@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			writeRecoveryFile(t, filepath.Join(dir, "originals", name+routeOp2), body)
			newRoute := routeFixture("alias@example.test")
			newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
			if _, err = ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), newRoute}); err != nil {
				t.Fatal(err)
			}
			cfg := runtimeFixture(t, dir, "127.0.0.1:18448")
			p, err := prepareRuntime(cfg)
			if err != nil {
				t.Fatalf("startup recovery: %v", err)
			}
			p.close()
			got, original, err := store.Read(routeOp2)
			if err != nil || got.State != "ready" || got.Recipients[0].Route != reservation.Recipients[0].Route || got.Recipients[0].Sequence != reservation.Recipients[0].Sequence || !bytes.Equal(original, body) {
				t.Fatalf("recovered wrong binding or data: %+v %v", got, err)
			}
			p, err = prepareRuntime(cfg)
			if err != nil {
				t.Fatalf("second startup: %v", err)
			}
			p.close()
			state, err := ledger.Read()
			if err != nil || state.Sequence != reservation.Recipients[0].Sequence {
				t.Fatalf("restart allocated a new sequence: %+v %v", state, err)
			}
		})
	}
}

func TestPendingRecoveryDoesNotGuessOrOverwrite(t *testing.T) {
	t.Run("unbound staged file", func(t *testing.T) {
		_, store, dir := originalFixture(t, routeMax+1000)
		path := filepath.Join(dir, "originals", ".pending-"+routeOp2)
		writeRecoveryFile(t, path, []byte("unknown owner"))
		if _, err := store.Store(routeOp2, "", []string{"alice@example.test"}, []byte("new content")); !errors.Is(err, ErrOriginalState) {
			t.Fatalf("unbound file claimed: %v", err)
		}
		if n, err := store.recoverPending(); err != nil || n != 0 {
			t.Fatalf("unbound recovery: %d %v", n, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != "unknown owner" {
			t.Fatalf("unbound data changed: %v", err)
		}
	})
	for _, state := range []string{"missing", "incomplete", "changed", "duplicate"} {
		t.Run(state, func(t *testing.T) {
			ledger, store, dir := originalFixture(t, routeMax+1000)
			body := []byte("complete original")
			digest := sha256.Sum256(body)
			if _, err := ledger.ReserveEnvelope(routeOp2, "", hex.EncodeToString(digest[:]), int64(len(body)), []string{"alice@example.test"}); err != nil {
				t.Fatal(err)
			}
			stage := filepath.Join(dir, "originals", ".pending-"+routeOp2)
			switch state {
			case "incomplete":
				writeRecoveryFile(t, stage, body[:3])
			case "changed":
				writeRecoveryFile(t, stage, bytes.Repeat([]byte{'x'}, len(body)))
			case "duplicate":
				writeRecoveryFile(t, stage, body)
				writeRecoveryFile(t, filepath.Join(dir, "originals", "raw-"+routeOp2), body)
			}
			count, err := store.recoverPending()
			if count != 0 || state == "missing" && err != nil || state != "missing" && !errors.Is(err, ErrOriginalState) {
				t.Fatalf("unexpected recovery: %d %v", count, err)
			}
			if _, _, err = store.Read(routeOp2); !errors.Is(err, ErrOriginalPending) {
				t.Fatalf("unverified original became ready: %v", err)
			}
			if state != "missing" {
				if _, err = os.Stat(stage); err != nil {
					t.Fatalf("unverified file removed: %v", err)
				}
			}
		})
	}
}

func TestPendingRecoveryProcessCrash(t *testing.T) {
	if stage := os.Getenv("ANAS_TEST_CRASH_STAGE"); stage != "" {
		ledger, err := OpenRouteLedger(os.Getenv("ANAS_TEST_CRASH_DIR"), routeSource, routeGeneration, []string{"example.test"})
		if err != nil {
			os.Exit(90)
		}
		store, err := OpenOriginalStore(ledger, routeMax+1000, 7)
		if err != nil {
			os.Exit(91)
		}
		_, _ = store.storeWithCheckpoint(routeOp2, "", []string{"alias@example.test"}, []byte("crash test original"), func(actual string) {
			if actual == stage {
				os.Exit(71)
			}
		})
		os.Exit(92)
	}
	for _, stage := range []string{"staged", "reserved", "published", "ready"} {
		t.Run(stage, func(t *testing.T) {
			ledger, store, dir := originalFixture(t, routeMax+1000)
			cmd := exec.Command(os.Args[0], "-test.run=^TestPendingRecoveryProcessCrash$")
			cmd.Env = append(os.Environ(), "ANAS_TEST_CRASH_STAGE="+stage, "ANAS_TEST_CRASH_DIR="+dir)
			if err := cmd.Run(); err == nil {
				t.Fatal("child did not crash")
			} else {
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 71 {
					t.Fatalf("child failed before checkpoint: %v", err)
				}
			}
			newRoute := routeFixture("alias@example.test")
			newRoute.OwnerAnchor, newRoute.MailboxID, newRoute.ClaimVersion = routeOp2, routeOp3, routeOp1
			if _, err := ledger.Replace(1, routeOp2, []ingress.Route{routeFixture("alice@example.test"), newRoute}); err != nil {
				t.Fatal(err)
			}
			cfg := runtimeFixture(t, dir, "127.0.0.1:18448")
			p, err := prepareRuntime(cfg)
			if err != nil {
				t.Fatal(err)
			}
			p.close()
			entry, original, err := store.Read(routeOp2)
			if stage == "staged" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("unbound data promoted: %+v %v", entry, err)
				}
				if data, e := os.ReadFile(filepath.Join(dir, "originals", ".pending-"+routeOp2)); e != nil || string(data) != "crash test original" {
					t.Fatalf("unbound data not retained: %v", e)
				}
				return
			}
			if err != nil || string(original) != "crash test original" || entry.Recipients[0].Route != routeFixture("alias@example.test") {
				t.Fatalf("crash recovery changed binding or lost data: %+v %v", entry, err)
			}
			newEntry, err := store.Store(routeOp3, "", []string{"alias@example.test"}, []byte("after transfer"))
			if err != nil || newEntry.Recipients[0].Route != newRoute || newEntry.Recipients[0].Sequence <= entry.Recipients[0].Sequence {
				t.Fatalf("new address owner: %+v %v", newEntry, err)
			}
		})
	}
}
