package dms

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func fixture(t *testing.T) (*Adapter, ApplyRequest) {
	t.Helper()
	root := t.TempDir()
	state := filepath.Join(root, "state")
	config := filepath.Join(root, "config")
	for _, dir := range []string{state, config} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	owner := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	r := identity.MailboxResource{ID: id, OwnerAnchor: owner, Kind: "primary", DefaultAddress: "alice@example.test", State: "active", Version: id, OperationID: owner, OperationState: "complete", Addresses: []identity.AddressResource{{Address: "alice@example.test", Kind: "primary", State: "active", Version: id, OperationID: owner, OperationState: "complete"}}}
	fingerprint, _, err := store.ProjectionFingerprint(r)
	if err != nil {
		t.Fatal(err)
	}
	p := store.Projection{Mailbox: store.Mailbox{ID: id, OwnerAnchor: owner, ProtocolLogin: "mbx_" + strings.Repeat("a", 32), StorageRef: "store_" + strings.Repeat("b", 32), RoundcubeKey: "webmail_" + strings.Repeat("c", 32), DefaultAddress: r.DefaultAddress}, Resource: r, Revision: owner, Fingerprint: fingerprint, Epoch: 2}
	return &Adapter{StateDir: state, ConfigDir: config, CapacityBytes: 2 << 30, startupReady: func(context.Context) error { return nil }, probe: func(context.Context, map[string]entry) error { return nil }}, ApplyRequest{Projection: p}
}
func nextRequest(t *testing.T, old ApplyRequest, revision string) ApplyRequest {
	t.Helper()
	out := old
	out.ExpectedRevision = old.Projection.Revision
	out.Projection.Revision = revision
	out.Projection.Epoch++
	out.Projection.Resource.Version = revision
	out.Projection.Resource.Addresses = append([]identity.AddressResource(nil), old.Projection.Resource.Addresses...)
	hash, _, err := store.ProjectionFingerprint(out.Projection.Resource)
	if err != nil {
		t.Fatal(err)
	}
	out.Projection.Fingerprint = hash
	return out
}

func TestAdapterPersistsIntentAndRequiresLiveReadback(t *testing.T) {
	a, in := fixture(t)
	a.probe = func(context.Context, map[string]entry) error { return ErrUnavailable }
	r, err := a.Apply(t.Context(), in)
	if !errors.Is(err, ErrUnavailable) || r.Applied {
		t.Fatal("unconfirmed DMS reported applied", r, err)
	}
	state, err := a.load()
	if err != nil || state.Boxes[in.Projection.Mailbox.ID].Applied {
		t.Fatal("failed operation not retained", err)
	}
	before := state.Boxes[in.Projection.Mailbox.ID].PasswordHash
	// A new adapter instance recovers the same mailbox identity and intent.
	a = &Adapter{StateDir: a.StateDir, ConfigDir: a.ConfigDir, CapacityBytes: a.CapacityBytes, startupReady: func(context.Context) error { return nil }, probe: func(context.Context, map[string]entry) error { return nil }}
	r, err = a.Apply(t.Context(), in)
	if err != nil || !r.Applied {
		t.Fatal("restart retry", err)
	}
	state, err = a.load()
	if err != nil || state.Boxes[in.Projection.Mailbox.ID].PasswordHash != before {
		t.Fatal("retry changed private identity", err)
	}
	accounts, _ := os.ReadFile(filepath.Join(a.ConfigDir, "postfix-accounts.cf"))
	if !strings.Contains(string(accounts), "mbx_"+strings.Repeat("a", 32)+"@"+ProtocolRealm) || !strings.Contains(string(accounts), "/var/mail/store_"+strings.Repeat("b", 32)) || strings.Contains(string(accounts), in.Projection.Mailbox.OwnerAnchor) {
		t.Fatal("address/owner used as storage key")
	}
	quotas, _ := os.ReadFile(filepath.Join(a.ConfigDir, "dovecot-quotas.cf"))
	if len(quotas) != 0 || !strings.Contains(string(accounts), "userdb_quota_storage_size=2147483648B userdb_quota_storage_grace=0") {
		t.Fatal("explicit quota missing")
	}
	if _, err = a.Read(t.Context(), in.Projection.Mailbox.ID); err != nil {
		t.Fatal("confirmed readback", err)
	}
	if err = os.WriteFile(filepath.Join(a.ConfigDir, "postfix-virtual.cf"), []byte("drift@example.test other@example.test\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = a.Read(t.Context(), in.Projection.Mailbox.ID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("saved receipt hid downstream drift", err)
	}
}

func TestAdapterResumesPartialFilesWithoutFollowingSymlinks(t *testing.T) {
	a, in := fixture(t)
	outside := filepath.Join(t.TempDir(), "protected")
	if err := os.WriteFile(outside, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(a.ConfigDir, "postfix-virtual.cf")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Apply(t.Context(), in); err == nil {
		t.Fatal("symlink accepted")
	}
	raw, _ := os.ReadFile(outside)
	if string(raw) != "unchanged" {
		t.Fatal("outside file changed")
	}
	state, err := a.load()
	if err != nil || state.Boxes[in.Projection.Mailbox.ID].Applied {
		t.Fatal("partial files reported applied", err)
	}
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Apply(t.Context(), in); err != nil || !r.Applied {
		t.Fatal("partial operation did not recover", err)
	}
}

func TestAdapterConcurrentRevisionsAndImmutableIdentity(t *testing.T) {
	a, in := fixture(t)
	if _, err := a.Apply(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	one := nextRequest(t, in, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	two := nextRequest(t, in, "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for _, request := range []ApplyRequest{one, two} {
		wg.Go(func() { _, err := a.Apply(t.Context(), request); out <- err })
	}
	wg.Wait()
	close(out)
	winners := 0
	for err := range out {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatal("revision race winners", winners)
	}
	state, err := a.load()
	if err != nil {
		t.Fatal(err)
	}
	current := state.Boxes[in.Projection.Mailbox.ID].Projection
	if _, err = a.Apply(t.Context(), in); !errors.Is(err, ErrConflict) {
		t.Fatal("stale configuration replay accepted", err)
	}
	for _, field := range []string{"protocol", "storage", "webmail", "owner"} {
		t.Run(field, func(t *testing.T) {
			bad := ApplyRequest{ExpectedRevision: current.Revision, Projection: current}
			switch field {
			case "protocol":
				bad.Projection.Mailbox.ProtocolLogin = "mbx_" + strings.Repeat("d", 32)
			case "storage":
				bad.Projection.Mailbox.StorageRef = "store_" + strings.Repeat("d", 32)
			case "webmail":
				bad.Projection.Mailbox.RoundcubeKey = "webmail_" + strings.Repeat("d", 32)
			case "owner":
				bad.Projection.Mailbox.OwnerAnchor = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
			}
			if _, err := a.Apply(t.Context(), bad); !errors.Is(err, ErrConflict) {
				t.Fatal("private identity reassigned", err)
			}
		})
	}
}

func TestAdapterDisabledMailboxKeepsReceivingAndRetainedAliasStops(t *testing.T) {
	a, in := fixture(t)
	if _, err := a.Apply(t.Context(), in); err != nil {
		t.Fatal(err)
	}
	next := nextRequest(t, in, "cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	next.Projection.Resource.State = "disabled"
	next.Projection.Resource.Addresses = append(next.Projection.Resource.Addresses, identity.AddressResource{Address: "old@example.test", Kind: "alias", State: "retained", Version: next.Projection.Resource.Version, OperationID: next.Projection.Resource.OperationID, OperationState: "complete"})
	next.Projection.Fingerprint, _, _ = store.ProjectionFingerprint(next.Projection.Resource)
	if _, err := a.Apply(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	aliases, _ := os.ReadFile(filepath.Join(a.ConfigDir, "postfix-virtual.cf"))
	if !strings.Contains(string(aliases), "alice@example.test ") || strings.Contains(string(aliases), "old@example.test") {
		t.Fatal("disabled receiving/removed alias changed", string(aliases))
	}
}

func TestAdapterHTTPAuthenticatesBeforeAnyOperation(t *testing.T) {
	a, in := fixture(t)
	key := strings.Repeat("a", 64)
	h := Handler{Adapter: a, Token: key, Host: "mail.example.test:9443"}
	for _, tc := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"TLS", func(r *http.Request) { r.TLS = nil }}, {"key", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("b", 64)) }}, {"host", func(r *http.Request) { r.Host = "foreign.example.test:9443" }}, {"cookie", func(r *http.Request) { r.Header.Set("Cookie", "session=anything") }}, {"origin", func(r *http.Request) { r.Header.Set("Origin", "https://mail.example.test") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "https://mail.example.test:9443/v1/config/apply", strings.NewReader("malformed"))
			r.Header.Set("Authorization", "Bearer "+key)
			r.TLS = &tls.ConnectionState{}
			tc.change(r)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 401 {
				t.Fatal(w.Code)
			}
			if _, err := os.Stat(filepath.Join(a.StateDir, "state.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unauthenticated request persisted intent")
			}
		})
	}
	if err := Kick(t.Context(), "../other;command"); !errors.Is(err, ErrConflict) {
		t.Fatal("untrusted kick argument accepted", err)
	}
	_ = in
}
