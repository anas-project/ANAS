// TEST_CASES: MAILMOD-T-069
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
)

type provisionObserver struct {
	checks, audits int
	revokeAt       int
	auditErr       bool
}

func (o *provisionObserver) AuthorizeRelayProvision(context.Context) error {
	o.checks++
	if o.revokeAt > 0 && o.checks >= o.revokeAt {
		return errors.New("revoked")
	}
	return nil
}
func (o *provisionObserver) BeforeRelayRemote(_ context.Context, i RelayProvisionIntent) error {
	o.audits++
	if o.auditErr || i.OperationID == "" || i.Origin == "" || len(i.Domains) == 0 {
		return errors.New("audit unavailable")
	}
	return nil
}
func TestRelayProvisionRemoteUsesBoundSourceOverSSH(t *testing.T) {
	server := newSSHFixture(t, `{"state":"published"}`, 0, false)
	input := provisionFixture(t)
	input.SSH = server.config
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	obs := &provisionObserver{}
	result, state, err := j.Remote(context.Background(), input, "publish", obs)
	if err != nil || state != "published" || obs.audits != 1 || obs.checks < 4 {
		t.Fatalf("publish %s %v %#v", state, err, obs)
	}
	select {
	case message := <-server.calls:
		var req struct{ Config cloudrelay.RuntimeConfig }
		if json.Unmarshal([]byte(message), &req) != nil || req.Config.Reader != result.Runtime.Reader || req.Config.Control != result.Source.Control {
			t.Fatal("SSH source mismatch")
		}
	case <-time.After(time.Second):
		t.Fatal("no SSH request")
	}
	for _, name := range []string{"remote-publish.issued", "remote-publish.done"} {
		st, e := os.Stat(filepath.Join(dir, name))
		if e != nil || st.Mode().Perm() != 0600 {
			t.Fatal("missing private marker", e)
		}
	}
}
func TestRelayProvisionRemoteRequiresPublishAndReauthorization(t *testing.T) {
	input := provisionFixture(t)
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	calls := 0
	call := func(_ context.Context, _ RelaySSHConfig, action string, _ cloudrelay.RuntimeConfig) (string, error) {
		calls++
		if action == "publish" {
			return "published", nil
		}
		return "active-local-verified", nil
	}
	obs := &provisionObserver{}
	if _, _, err = j.remote(context.Background(), input, "activate", obs, call); !errors.Is(err, ErrConflict) || calls != 0 {
		t.Fatal("unpublished source activated", err)
	}
	obs.auditErr = true
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); !errors.Is(err, ErrUnknown) || calls != 0 {
		t.Fatal("audit failure executed", err)
	}
	obs.auditErr = false
	obs.revokeAt = obs.checks + 3
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); !errors.Is(err, ErrUnknown) || calls != 0 {
		t.Fatal("revoked action executed", err)
	}
	obs.revokeAt = 0
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.remote(context.Background(), input, "activate", obs, call); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("incorrect remote calls", calls)
	}
}
func TestRelayProvisionRemoteLostReplyAndReopen(t *testing.T) {
	input := provisionFixture(t)
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	obs := &provisionObserver{}
	calls := 0
	var first cloudrelay.RuntimeConfig
	call := func(_ context.Context, _ RelaySSHConfig, _ string, cfg cloudrelay.RuntimeConfig) (string, error) {
		calls++
		if calls == 1 {
			first = cfg
			return "", ErrUnknown
		}
		if cfg.Reader != first.Reader || cfg.SourceID != first.SourceID {
			t.Fatal("regenerated after lost reply")
		}
		return "published", nil
	}
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); !errors.Is(err, ErrUnknown) || calls != 1 {
		t.Fatal("lost response mishandled", err)
	}
	if _, err = os.Stat(filepath.Join(dir, "remote-publish.done")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unknown result marked done")
	}
	j.Close()
	j, err = OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); err != nil || calls != 2 {
		t.Fatal("explicit recovery failed", err)
	}
}
func TestRelayProvisionRemoteDoesNotConfirmAfterRevocation(t *testing.T) {
	input := provisionFixture(t)
	dir := provisionDir(t)
	j, err := OpenRelayProvisionJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	obs := &provisionObserver{revokeAt: 4}
	calls := 0
	call := func(context.Context, RelaySSHConfig, string, cloudrelay.RuntimeConfig) (string, error) {
		calls++
		return "published", nil
	}
	if _, _, err = j.remote(context.Background(), input, "publish", obs, call); !errors.Is(err, ErrUnknown) || calls != 1 {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, "remote-publish.done")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("revoked result marked done")
	}
}
