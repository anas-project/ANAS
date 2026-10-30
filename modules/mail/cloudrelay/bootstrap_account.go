package cloudrelay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The account comment binds a created identity to the journal and selected
// public key. This is not a credential; root controls both the journal and the
// account database. An existing account without this binding is never adopted.
func bootstrapAccountTag(binding []byte) string {
	sum := sha256.Sum256(append([]byte("anas.mail-deploy-account/v1\x00"), binding...))
	return "ANAS-mail-bootstrap-" + hex.EncodeToString(sum[:16])
}

type bootstrapAccountState int

const (
	bootstrapAccountUnknown bootstrapAccountState = iota
	bootstrapAccountAbsent
	bootstrapAccountOwned
	bootstrapAccountForeign
)

type bootstrapAccountOps struct {
	observe func(context.Context, string) (bootstrapAccountState, error)
	create  func(context.Context, string) error
}

// ensureBootstrapAccount is called with the private installation journal lock
// held. It records intent before useradd, checks a bound account after a lost
// reply, and refuses to repeat an unconfirmed creation when the account is
// absent. A completed account that disappears or changes is never recreated.
func ensureBootstrapAccount(ctx context.Context, journal int, binding []byte, ops bootstrapAccountOps) error {
	if ctx.Err() != nil || ops.observe == nil || ops.create == nil || len(binding) == 0 {
		return ErrPublishUnknown
	}
	intent, err := checkActionMarker(journal, "account.intent", binding)
	if err != nil {
		return err
	}
	done, err := checkActionMarker(journal, "account.done", binding)
	if err != nil {
		return err
	}
	if done && !intent {
		return ErrPublishUnknown
	}
	tag := bootstrapAccountTag(binding)
	state, err := ops.observe(ctx, tag)
	if err != nil || state == bootstrapAccountUnknown {
		return ErrPublishUnknown
	}
	if done {
		if state != bootstrapAccountOwned {
			return ErrPublishConflict
		}
		return nil
	}
	if !intent {
		if state != bootstrapAccountAbsent {
			return ErrPublishConflict
		}
		if err := recordActionMarker(journal, "account.intent", binding); err != nil {
			return err
		}
		if err := ops.create(ctx, tag); err != nil {
			// A failed command may still have changed the account database.
			// Only a fully verified identity bearing this binding is recoverable.
			state, err = ops.observe(ctx, tag)
			if err != nil || state != bootstrapAccountOwned {
				return ErrPublishUnknown
			}
		} else {
			state, err = ops.observe(ctx, tag)
			if err != nil || state != bootstrapAccountOwned {
				return ErrPublishUnknown
			}
		}
	} else if state == bootstrapAccountAbsent {
		return ErrPublishUnknown
	} else if state != bootstrapAccountOwned {
		return ErrPublishConflict
	}
	return recordActionMarker(journal, "account.done", binding)
}

// runBootstrapAccountCommand suppresses potentially sensitive shadow contents
// and bounds both output and execution time. Exit status 2 means absent only
// for getent, never for useradd or id.
func runBootstrapAccountCommand(ctx context.Context, path string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	out := &bootstrapCheckOutput{}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() != nil || out.overflow {
		return nil, -1, ErrPublishUnknown
	}
	if err == nil {
		return out.data.Bytes(), 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.data.Bytes(), exit.ExitCode(), nil
	}
	return nil, -1, ErrPublishUnknown
}

func observeBootstrapAccount(ctx context.Context, tag string) (bootstrapAccountState, error) {
	passwd, code, err := runBootstrapAccountCommand(ctx, "/usr/bin/getent", "passwd", bootstrapAccount)
	if err != nil {
		return bootstrapAccountUnknown, err
	}
	group, gc, err := runBootstrapAccountCommand(ctx, "/usr/bin/getent", "group", bootstrapAccount)
	if err != nil {
		return bootstrapAccountUnknown, err
	}
	if code == 2 && gc == 2 {
		return bootstrapAccountAbsent, nil
	}
	if code != 0 || gc != 0 {
		return bootstrapAccountForeign, nil
	}
	p := strings.Split(strings.TrimSpace(string(passwd)), ":")
	g := strings.Split(strings.TrimSpace(string(group)), ":")
	if len(p) != 7 || len(g) != 4 || p[0] != bootstrapAccount || p[1] != "x" || g[0] != bootstrapAccount || p[4] != tag || p[5] != "/var/lib/anas-mail-relay-bootstrap" || p[6] != "/bin/sh" || p[3] != g[2] || g[3] != "" {
		return bootstrapAccountForeign, nil
	}
	uid, ue := strconv.ParseUint(p[2], 10, 31)
	gid, ge := strconv.ParseUint(p[3], 10, 31)
	if ue != nil || ge != nil || uid == 0 || gid == 0 {
		return bootstrapAccountForeign, nil
	}
	shadow, sc, err := runBootstrapAccountCommand(ctx, "/usr/bin/getent", "shadow", bootstrapAccount)
	if err != nil {
		return bootstrapAccountUnknown, err
	}
	sp := strings.Split(strings.TrimSpace(string(shadow)), ":")
	if sc != 0 || len(sp) != 9 || sp[0] != bootstrapAccount || !(strings.HasPrefix(sp[1], "!") || sp[1] == "*") || sp[7] != "" && sp[7] != "-1" {
		return bootstrapAccountForeign, nil
	}
	groups, ic, err := runBootstrapAccountCommand(ctx, "/usr/bin/id", "-G", bootstrapAccount)
	if err != nil {
		return bootstrapAccountUnknown, err
	}
	if ic != 0 || strings.TrimSpace(string(groups)) != p[3] {
		return bootstrapAccountForeign, nil
	}
	return bootstrapAccountOwned, nil
}

func createBootstrapAccount(ctx context.Context, tag string) error {
	if len(tag) != len("ANAS-mail-bootstrap-")+32 || !strings.HasPrefix(tag, "ANAS-mail-bootstrap-") {
		return ErrPublishConflict
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(tag, "ANAS-mail-bootstrap-")); err != nil {
		return ErrPublishConflict
	}
	_, code, err := runBootstrapAccountCommand(ctx, "/usr/sbin/useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/var/lib/anas-mail-relay-bootstrap", "--shell", "/bin/sh", "--comment", tag, bootstrapAccount)
	if err != nil || code != 0 {
		return ErrPublishUnknown
	}
	return nil
}
