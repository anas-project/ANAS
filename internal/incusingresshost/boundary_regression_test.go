package incusingresshost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTargetDigestBindsEveryIdentityField(t *testing.T) {
	original := testTarget()
	for _, field := range []string{"Scope", "Epoch", "Incarnation", "Reservation", "Deployment", "InstanceID", "InstanceUUID", "GuestIP", "NICMAC", "ServerUUID", "HostVethName", "HostVethMAC"} {
		t.Run(field, func(t *testing.T) {
			changed := original
			value := reflect.ValueOf(&changed).Elem().FieldByName(field)
			value.SetString(value.String() + "-changed")
			if targetDigest(original) == targetDigest(changed) {
				t.Fatalf("target digest omits %s", field)
			}
		})
	}
	for _, field := range []string{"Consumer", "Resource"} {
		t.Run("Lease."+field, func(t *testing.T) {
			changed := original
			value := reflect.ValueOf(&changed.Lease).Elem().FieldByName(field)
			value.SetString(value.String() + "_changed")
			if targetDigest(original) == targetDigest(changed) {
				t.Fatalf("target digest omits Lease.%s", field)
			}
		})
	}
	changed := original
	changed.GuestPort++
	if targetDigest(original) == targetDigest(changed) {
		t.Fatal("target digest omits guest port")
	}
	changed = original
	changed.HostVethPeerIfIndex++
	if targetDigest(original) == targetDigest(changed) {
		t.Fatal("target digest omits peer ifindex")
	}
}

func TestObservedJSONRejectsAmbiguousKeys(t *testing.T) {
	for _, body := range []string{
		`{"scope":"first","scope":"second"}`,
		`{"Scope":"scope_one"}`,
		`{"scope":"scope_one","Scope":"scope_two"}`,
		`{"scope":"scope_one","lease":{"resource":"a","resource":"b"}}`,
		`{"scope":"` + string([]byte{0xff}) + `"}`,
	} {
		var target Target
		if err := decodeObservedJSON([]byte(body), &target); err == nil {
			t.Errorf("accepted ambiguous JSON %q", body)
		}
	}
	var observation []map[string]any
	if err := decodeObservedJSON([]byte(`[{"ifindex":1,"ifindex":2}]`), &observation); err == nil {
		t.Fatal("duplicate native observation key accepted")
	}
}

func TestIngressGuardRejectsLinkAliases(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			backend, _, _ := testBackend(t)
			other := filepath.Join(t.TempDir(), "other-lock")
			if err := os.WriteFile(other, nil, 0600); err != nil {
				t.Fatal(err)
			}
			link := os.Link
			if kind == "symlink" {
				link = os.Symlink
			}
			if err := link(other, filepath.Join(backend.config.ReceiptDir, ".lock")); err != nil {
				t.Fatal(err)
			}
			called := false
			err := backend.withGuard(context.Background(), func() error { called = true; return nil })
			if err == nil || called {
				t.Fatal("aliased guard authorized an effect")
			}
		})
	}
}

func TestIngressGuardWaitHonorsCancellation(t *testing.T) {
	backend, _, _ := testBackend(t)
	file, err := os.OpenFile(filepath.Join(backend.config.ReceiptDir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- backend.withGuard(ctx, func() error { return errors.New("unexpected effect") }) }()
	// Give the waiter a chance to enter flock. The outer timeout only keeps
	// a broken implementation from hanging the test process.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation was not preserved: %v", err)
		}
	case <-time.After(time.Second):
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		<-done
		t.Fatal("guard ignored cancellation while another process held the lock")
	}
}

func TestIngressGuardDetectsReplacement(t *testing.T) {
	backend, _, _ := testBackend(t)
	err := backend.withGuard(context.Background(), func() error {
		path := filepath.Join(backend.config.ReceiptDir, ".lock")
		if err := os.Rename(path, path+".old"); err != nil {
			return err
		}
		return os.WriteFile(path, nil, 0600)
	})
	if err == nil {
		t.Fatal("replaced lock inode was reported as a successful guarded operation")
	}
}

func TestReceiptWriteDoesNotFollowExistingTempLink(t *testing.T) {
	backend, target, _ := testBackend(t)
	if err := backend.HoldAddress(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(victim, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, backend.receiptPath(target)+".tmp"); err != nil {
		t.Fatal(err)
	}
	_ = backend.HoldAddress(context.Background(), target)
	body, err := os.ReadFile(victim)
	if err != nil || string(body) != "keep" {
		t.Fatal("receipt writer followed an existing temporary-file symlink")
	}
}

func TestReceiptReadRejectsDuplicateFields(t *testing.T) {
	backend, target, _ := testBackend(t)
	if err := backend.HoldAddress(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	path := backend.receiptPath(target)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	modified := strings.TrimSuffix(strings.TrimSpace(string(body)), "}") + `,"scope":` + string(raw["scope"]) + `}`
	if err := os.WriteFile(path, []byte(modified), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.loadReceipt(target); err == nil {
		t.Fatal("receipt with duplicate fields was accepted")
	}
}

func TestAddressHoldRejectsReallocatedOrRestartedGuest(t *testing.T) {
	for _, mutation := range []string{"instance_uuid", "incarnation", "resource", "epoch", "host_veth"} {
		t.Run(mutation, func(t *testing.T) {
			backend, first, _ := testBackend(t)
			second := first
			second.Reservation = strings.Repeat("e", 32) + ":2"
			second.GuestPort++
			switch mutation {
			case "instance_uuid":
				second.InstanceUUID = "44444444-4444-4444-8444-444444444444"
			case "incarnation":
				second.Incarnation = strings.Repeat("f", 64)
			case "resource":
				second.Lease.Resource = "another"
			case "epoch":
				second.Epoch = strings.Repeat("f", 64)
			case "host_veth":
				second.HostVethName = "vethother"
			}
			backend.config.Resolver = multiResolver{first.Reservation: identityFor(first), second.Reservation: identityFor(second)}
			if err := backend.HoldAddress(context.Background(), first); err != nil {
				t.Fatal(err)
			}
			if err := backend.HoldAddress(context.Background(), second); err == nil {
				t.Fatal("new allocation reused an address with an unreleased old hold")
			}
			if _, err := backend.loadReceipt(first); err != nil {
				t.Fatalf("old ownership evidence was lost: %v", err)
			}
		})
	}
}

func TestDisabledBackendCannotClaimAddressHold(t *testing.T) {
	backend, target, _ := testBackend(t)
	backend.config.productionDisabledReason = "allocation lifecycle is not installed"
	if err := backend.HoldAddress(context.Background(), target); err == nil {
		t.Fatal("disabled backend claimed an address allocation")
	}
	if _, err := backend.loadReceipt(target); !errors.Is(err, errReceiptMissing) {
		t.Fatalf("disabled backend wrote ownership evidence: %v", err)
	}
}
