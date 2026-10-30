package installer

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"golang.org/x/sys/unix"
)

type RelayProvisionIntent struct {
	OperationID, Action, Origin string
	Domains                     []string
}

// The caller verifies the current human administrator, the service grant and
// its trusted workspace policy. BeforeRemote must also durably audit the action.
type RelayProvisionObserver interface {
	AuthorizeRelayProvision(context.Context) error
	BeforeRelayRemote(context.Context, RelayProvisionIntent) error
}
type relayRemoteCall func(context.Context, RelaySSHConfig, string, cloudrelay.RuntimeConfig) (string, error)

func provisionMarkerBytes(p PreparedRelayProvision, input RelayProvisionInput, name string) ([]byte, error) {
	key, err := hex.DecodeString(p.Runtime.Control)
	if err != nil || len(key) != 32 {
		return nil, ErrJournal
	}
	defer clear(key)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("anas.mail-relay-remote/v1\x00" + name + "\x00" + input.OperationID + "\x00" + p.Runtime.SourceID + "\x00" + p.Runtime.Generation))
	return []byte(hex.EncodeToString(mac.Sum(nil)) + "\n"), nil
}
func (j *RelayProvisionJournal) marker(name string, value []byte, create bool) (bool, error) {
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(j.dir, name, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		fd, err = unix.Openat(j.dir, name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if err != nil {
			return false, ErrUnknown
		}
		f := os.NewFile(uintptr(fd), "private remote operation marker")
		defer f.Close()
		if !safeFile(fd, false) {
			return false, ErrJournal
		}
		if n, e := f.Write(value); e != nil || n != len(value) || f.Sync() != nil || unix.Fsync(j.dir) != nil {
			return false, ErrUnknown
		}
		return true, nil
	}
	if errors.Is(err, unix.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, ErrUnknown
	}
	f := os.NewFile(uintptr(fd), "private remote operation marker")
	defer f.Close()
	if !safeFile(fd, false) {
		return false, ErrJournal
	}
	raw, err := io.ReadAll(io.LimitReader(f, 129))
	if err != nil || !hmac.Equal(raw, value) {
		return false, ErrUnknown
	}
	if f.Sync() != nil || unix.Fsync(j.dir) != nil {
		return false, ErrUnknown
	}
	return true, nil
}

// Remote runs one explicit fixed SSH action. It never retries a lost response;
// a later explicitly authorized call reuses the bound private source and the
// remote helper's idempotent, no-overwrite recovery rules.
func (j *RelayProvisionJournal) Remote(ctx context.Context, input RelayProvisionInput, action string, observer RelayProvisionObserver) (PreparedRelayProvision, string, error) {
	call := func(ctx context.Context, ssh RelaySSHConfig, action string, cfg cloudrelay.RuntimeConfig) (string, error) {
		return relaySSHBootstrapAuthorized(ctx, ssh, action, cfg, observer.AuthorizeRelayProvision)
	}
	if observer == nil {
		return PreparedRelayProvision{}, "", ErrJournal
	}
	return j.remote(ctx, input, action, observer, call)
}
func (j *RelayProvisionJournal) remote(ctx context.Context, input RelayProvisionInput, action string, observer RelayProvisionObserver, call relayRemoteCall) (PreparedRelayProvision, string, error) {
	var empty PreparedRelayProvision
	if j == nil || j.lock == nil || j.dir < 0 || observer == nil || call == nil || ctx.Err() != nil || action != "publish" && action != "activate" && action != "inspect" {
		return empty, "", ErrJournal
	}
	if observer.AuthorizeRelayProvision(ctx) != nil {
		return empty, "", ErrUnknown
	}
	prepared, err := j.Prepare(input)
	if err != nil {
		return empty, "", err
	}
	issueValue, err := provisionMarkerBytes(prepared, input, "remote-publish.issued")
	if err != nil {
		return empty, "", err
	}
	issued, err := j.marker("remote-publish.issued", issueValue, false)
	if err != nil {
		return empty, "", err
	}
	doneValue, err := provisionMarkerBytes(prepared, input, "remote-publish.done")
	if err != nil {
		return empty, "", err
	}
	published, err := j.marker("remote-publish.done", doneValue, false)
	if err != nil {
		return empty, "", err
	}
	if published && !issued {
		return empty, "", ErrUnknown
	}
	if action == "activate" && !published || action == "inspect" && !issued {
		return empty, "", ErrConflict
	}
	intent := RelayProvisionIntent{OperationID: input.OperationID, Action: action, Origin: input.Origin, Domains: append([]string(nil), prepared.Runtime.Domains...)}
	if observer.BeforeRelayRemote(ctx, intent) != nil || observer.AuthorizeRelayProvision(ctx) != nil || ctx.Err() != nil {
		return empty, "", ErrUnknown
	}
	if action == "publish" && !issued {
		if _, err = j.marker("remote-publish.issued", issueValue, true); err != nil {
			return empty, "", err
		}
	}
	if action == "activate" {
		value, e := provisionMarkerBytes(prepared, input, "remote-activate.issued")
		if e != nil {
			return empty, "", e
		}
		if _, err = j.marker("remote-activate.issued", value, true); err != nil {
			return empty, "", err
		}
	}
	if observer.AuthorizeRelayProvision(ctx) != nil || ctx.Err() != nil {
		return empty, "", ErrUnknown
	}
	state, err := call(ctx, input.SSH, action, prepared.Runtime)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return empty, "", ErrConflict
		}
		return empty, "", ErrUnknown
	}
	if action == "publish" && state != "published" || action == "activate" && state != "active-local-verified" || action == "inspect" && state != "inactive" && state != "active-local-verified" {
		return empty, "", ErrUnknown
	}
	if observer.AuthorizeRelayProvision(ctx) != nil || ctx.Err() != nil {
		return empty, "", ErrUnknown
	}
	if action == "publish" {
		if _, err = j.marker("remote-publish.done", doneValue, true); err != nil {
			return empty, "", err
		}
	}
	if action == "activate" {
		value, e := provisionMarkerBytes(prepared, input, "remote-activate.done")
		if e != nil {
			return empty, "", e
		}
		if _, err = j.marker("remote-activate.done", value, true); err != nil {
			return empty, "", err
		}
	}
	return prepared, state, nil
}
