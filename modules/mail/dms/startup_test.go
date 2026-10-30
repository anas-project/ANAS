// TEST_CASES: MAILMOD-T-016
package dms

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func TestNativeInitializationDistinguishesTimeoutWithoutConfirming(t *testing.T) {
	checkpoint := filepath.Join(t.TempDir(), "checkpoint")
	if err := os.WriteFile(checkpoint, []byte("ready"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, output, want string
		err                error
	}{
		{"running", "changedetector RUNNING pid 1", "", nil},
		{"stopped", "changedetector STOPPED", "native-initialization", nil},
		{"unavailable", "", "native-initialization", ErrUnavailable},
		{"timeout", "", "native-initialization-timeout", errors.Join(ErrUnavailable, context.DeadlineExceeded)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := nativeInitialization(t.Context(), checkpoint, func(_ context.Context, name string, args ...string) (string, error) {
				if name != "/usr/bin/supervisorctl" || strings.Join(args, " ") != "status changedetector" {
					t.Fatal("unexpected native monitor check")
				}
				return tc.output, tc.err
			})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var failure *checkFailure
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &failure) || failure.step != tc.want {
				t.Fatal("unexpected initialization result", err)
			}
		})
	}
}

func TestCommandTimeoutRemainsUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := commandWithOutcome(ctx, "/bin/sleep", "5")
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("timeout lost unavailable classification", err)
	}
}

func TestCommandLimitAndCallerDeadline(t *testing.T) {
	if _, err := nativeMonitorCommand(t.Context(), "/bin/sleep", "3.2"); err != nil {
		t.Fatal("monitor command was still limited to three seconds", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, err := nativeMonitorCommand(ctx, "/bin/sleep", "5")
	if !errors.Is(err, ErrUnavailable) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("caller deadline was not honored", err)
	}
}

func secondMailbox(t *testing.T, first ApplyRequest, digit, address string) ApplyRequest {
	t.Helper()
	out := first
	out.ExpectedRevision = ""
	out.Projection.Mailbox.ID = strings.Repeat(digit, 8) + "-" + strings.Repeat(digit, 4) + "-4" + strings.Repeat(digit, 3) + "-8" + strings.Repeat(digit, 3) + "-" + strings.Repeat(digit, 12)
	out.Projection.Mailbox.ProtocolLogin = "mbx_" + strings.Repeat(digit, 32)
	out.Projection.Mailbox.StorageRef = "store_" + strings.Repeat(digit, 32)
	out.Projection.Mailbox.RoundcubeKey = "webmail_" + strings.Repeat(digit, 32)
	out.Projection.Mailbox.DefaultAddress = address
	out.Projection.Resource.ID = out.Projection.Mailbox.ID
	out.Projection.Resource.DefaultAddress = address
	out.Projection.Resource.Kind = "additional"
	out.Projection.Resource.Addresses = append([]identity.AddressResource(nil), first.Projection.Resource.Addresses...)
	out.Projection.Resource.Addresses[0].Address = address
	out.Projection.Resource.Addresses[0].Kind = "additional"
	out.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(out.Projection.Resource)
	if !canonicalProjection(out.Projection) {
		t.Fatal("invalid second mailbox fixture")
	}
	return out
}

func TestStartupKeepsFirstGenerationStableUntilNativeMonitorRuns(t *testing.T) {
	a, first := fixture(t)
	ready := false
	a.startupReady = func(context.Context) error {
		if !ready {
			return &checkFailure{"native-initialization"}
		}
		return nil
	}
	second := secondMailbox(t, first, "d", "second@example.test")
	if r, err := a.Apply(t.Context(), first); !errors.Is(err, ErrUnavailable) || r.Applied {
		t.Fatal("first boot must write intent without confirming", r, err)
	}
	before, err := os.ReadFile(filepath.Join(a.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []ApplyRequest{second, nextRequest(t, first, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")} {
		if r, err := a.Apply(t.Context(), request); !errors.Is(err, ErrUnavailable) || r.Applied {
			t.Fatal("generation changed during native initialization", r, err)
		}
		after, _ := os.ReadFile(filepath.Join(a.StateDir, "state.json"))
		if string(after) != string(before) {
			t.Fatal("blocked generation changed persisted intent")
		}
	}
	// A crash after saving intent can leave an input missing. Replaying that
	// exact generation repairs it without allowing a newer generation through.
	accounts := filepath.Join(a.ConfigDir, "postfix-accounts.cf")
	want, _ := os.ReadFile(accounts)
	if err := os.Remove(accounts); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Apply(t.Context(), first); !errors.Is(err, ErrUnavailable) || r.Applied {
		t.Fatal("partial first generation incorrectly confirmed", r, err)
	}
	got, _ := os.ReadFile(accounts)
	if string(got) != string(want) {
		t.Fatal("same generation did not repair configuration with stable identity")
	}
	ready = true
	for _, request := range []ApplyRequest{first, second} {
		if r, err := a.Apply(t.Context(), request); err != nil || !r.Applied {
			t.Fatal("native monitor readiness did not permit recovery", r, err)
		}
	}
	ready = false
	if r, err := a.Read(t.Context(), first.Projection.Mailbox.ID); !errors.Is(err, ErrUnavailable) || r.Applied {
		t.Fatal("saved receipt hid unavailable native monitor", r, err)
	}
	ready = true
	if r, err := a.Read(t.Context(), second.Projection.Mailbox.ID); err != nil || !r.Applied {
		t.Fatal("read did not recover", r, err)
	}
}

func TestConcurrentStartupOnlyPersistsOneMailbox(t *testing.T) {
	a, first := fixture(t)
	a.startupReady = func(context.Context) error { return ErrUnavailable }
	var wg sync.WaitGroup
	for _, request := range []ApplyRequest{first, secondMailbox(t, first, "d", "second@example.test"), secondMailbox(t, first, "e", "third@example.test")} {
		wg.Go(func() {
			if r, err := a.Apply(t.Context(), request); !errors.Is(err, ErrUnavailable) || r.Applied {
				t.Error("unready concurrent generation confirmed", r, err)
			}
		})
	}
	wg.Wait()
	state, err := a.load()
	if err != nil || len(state.Boxes) != 1 {
		t.Fatal("multiple generations persisted before native initialization", len(state.Boxes), err)
	}
}

func TestPrepareRepairsPersistedInputsBeforeNativeInitialization(t *testing.T) {
	a, first := fixture(t)
	for _, request := range []ApplyRequest{first, secondMailbox(t, first, "d", "second@example.test")} {
		if _, err := a.Apply(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	state, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	want, err := files(state.Boxes, a.CapacityBytes)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(a.StateDir, "state.json"))
	for name := range want {
		if err := os.Remove(filepath.Join(a.ConfigDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Prepare must not depend on supervisor or native account availability.
	a.startupReady = func(context.Context) error { return ErrUnavailable }
	if err := a.PrepareInputs(); err != nil {
		t.Fatal(err)
	}
	for name, expected := range want {
		actual, err := os.ReadFile(filepath.Join(a.ConfigDir, name))
		if err != nil || string(actual) != string(expected) {
			t.Fatal("persisted inputs not restored before native startup", name, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(a.StateDir, "state.json"))
	if string(before) != string(after) {
		t.Fatal("prepare changed saved receipts or mailbox identities")
	}
}
