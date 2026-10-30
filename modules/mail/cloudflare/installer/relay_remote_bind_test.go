// TEST_CASES: MAILMOD-T-071
package installer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestRelayRemotePlanBindsBeforeActionAndStopsAfterCommit(t *testing.T) {
	source, _, _, _ := relayHandoffFixture(t, false)
	r := RelayInstallRequest{Runtime: source, TotalCapacity: source.CapacityBytes, OperationID: "cfg-0123456789abcdef0123456789abcdef"}
	dir := provisionDir(t)
	j, err := OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	audit := &relayAudit{prepareErr: errors.New("revoked")}
	if err = j.BindRemotePlan(context.Background(), audit, r, nil); !errors.Is(err, ErrUnknown) {
		t.Fatal("revoked", err)
	}
	if _, err = os.Lstat(filepath.Join(dir, "relay-binding.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("bound after revocation")
	}
	audit.prepareErr = nil
	if err = j.BindRemotePlan(context.Background(), audit, r, nil); err != nil {
		t.Fatal(err)
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenRelayJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.BindRemotePlan(context.Background(), audit, r, nil); err != nil {
		t.Fatal("retry", err)
	}
	changed := r
	changed.TotalCapacity++
	if err = j.BindRemotePlan(context.Background(), audit, changed, nil); !errors.Is(err, ErrConflict) {
		t.Fatal("stale snapshot", err)
	}
	if err = os.WriteFile(filepath.Join(dir, "commit-guard.json"), []byte("existing guard"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, current := range [][]ingress.RuntimeSource{nil, {{SourceSpec: ingress.SourceSpec{ID: source.SourceID}}}} {
		if err = j.BindRemotePlan(context.Background(), audit, r, current); err == nil {
			t.Fatal("SSH action allowed after commit attempt")
		}
	}
}
