// Package dms implements the fixed, private interface inside the DMS container.
// It does not accept shell commands, paths, Docker operations or AD credentials.
package dms

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/store"
)

const ProtocolRealm = "mailboxes.anas.invalid"

var ErrConflict = errors.New("DMS configuration revision conflict")
var ErrUnavailable = errors.New("DMS configuration not confirmed")

type checkFailure struct{ step string }

func (e *checkFailure) Error() string { return "DMS configuration not confirmed: " + e.step }
func (e *checkFailure) Unwrap() error { return ErrUnavailable }

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var login = regexp.MustCompile(`^mbx_[a-f0-9]{32}$`)
var storage = regexp.MustCompile(`^store_[a-f0-9]{32}$`)
var webmail = regexp.MustCompile(`^webmail_[a-f0-9]{32}$`)

type ApplyRequest struct {
	ExpectedRevision string           `json:"expected_revision"`
	Projection       store.Projection `json:"projection"`
}
type Receipt struct {
	MailboxID   string `json:"mailbox_id"`
	Revision    string `json:"revision"`
	Fingerprint string `json:"fingerprint"`
	Applied     bool   `json:"applied"`
}
type entry struct {
	Projection   store.Projection `json:"projection"`
	PasswordHash string           `json:"password_hash"`
	Applied      bool             `json:"applied"`
}
type diskState struct {
	Schema string           `json:"schema"`
	Boxes  map[string]entry `json:"boxes"`
}

type Adapter struct {
	// Fixed locations are supplied by the container entrypoint, never HTTP.
	StateDir, ConfigDir string
	CapacityBytes       int64
	mu                  sync.Mutex
	startupReady        func(context.Context) error
	probe               func(context.Context, map[string]entry) error
	intakeSubmit        func(context.Context, intakeRecord, string, io.Reader) intakeSubmission
	intakeObserve       func(context.Context, intakeRecord, string) (string, string)
}

func canonicalProjection(p store.Projection) bool {
	fingerprint, _, err := store.ProjectionFingerprint(p.Resource)
	if err != nil || fingerprint != p.Fingerprint || !uuid.MatchString(p.Revision) || p.Epoch < 1 || p.Resource.ID != p.Mailbox.ID || p.Resource.OwnerAnchor != p.Mailbox.OwnerAnchor || p.Resource.DefaultAddress != p.Mailbox.DefaultAddress || !login.MatchString(p.Mailbox.ProtocolLogin) || !storage.MatchString(p.Mailbox.StorageRef) || !webmail.MatchString(p.Mailbox.RoundcubeKey) || p.Resource.State == "pending" {
		return false
	}
	for _, address := range p.Resource.Addresses {
		if address.State == "pending" {
			return false
		}
	}
	return true
}

func (a *Adapter) load() (diskState, error) {
	state := diskState{Schema: "anas.dms-state/v1", Boxes: map[string]entry{}}
	raw, err := os.ReadFile(filepath.Join(a.StateDir, "state.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil || len(raw) > 32<<20 {
		return diskState{}, ErrUnavailable
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&state) != nil || state.Schema != "anas.dms-state/v1" || state.Boxes == nil {
		return diskState{}, ErrUnavailable
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return diskState{}, ErrUnavailable
	}
	for id, box := range state.Boxes {
		if box.Projection.Mailbox.ID != id || !canonicalProjection(box.Projection) || !validPasswordHash(box.PasswordHash) {
			return diskState{}, ErrUnavailable
		}
	}
	return state, nil
}
func validPasswordHash(value string) bool {
	if !strings.HasPrefix(value, "{SHA512}") {
		return false
	}
	encoded := strings.TrimPrefix(value, "{SHA512}")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(raw) == 64 && base64.StdEncoding.EncodeToString(raw) == encoded
}

func atomicFile(path string, raw []byte, mode os.FileMode) error {
	// All parents are module-owned fixed private paths. An unexpected symlink
	// is an error rather than a target selected by a request or old artifact.
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return ErrUnavailable
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".anas-mail-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func (a *Adapter) save(state diskState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return atomicFile(filepath.Join(a.StateDir, "state.json"), raw, 0600)
}

func files(boxes map[string]entry, capacity int64) (map[string][]byte, error) {
	if capacity < 1<<20 || capacity > 1<<40 {
		return nil, ErrConflict
	}
	ids := make([]string, 0, len(boxes))
	for id := range boxes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var accounts, aliases strings.Builder
	addresses, identities, paths := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		box := boxes[id]
		p := box.Projection
		if !canonicalProjection(p) || !validPasswordHash(box.PasswordHash) || identities[p.Mailbox.ProtocolLogin] || paths[p.Mailbox.StorageRef] {
			return nil, ErrConflict
		}
		identities[p.Mailbox.ProtocolLogin] = true
		paths[p.Mailbox.StorageRef] = true
		user := p.Mailbox.ProtocolLogin + "@" + ProtocolRealm
		path := "/var/mail/" + p.Mailbox.StorageRef
		// DMS's quotas-file helper adds a ten-percent delivery grace. Explicit
		// userdb fields and an empty quotas file enforce the configured hard cap.
		fmt.Fprintf(&accounts, "%s|%s|userdb_mail_path=%s userdb_home=%s/home userdb_quota_storage_size=%dB userdb_quota_storage_grace=0\n", user, box.PasswordHash, path, path, capacity)
		for _, address := range p.Resource.Addresses {
			if address.State != "active" {
				continue
			}
			if addresses[address.Address] {
				return nil, ErrConflict
			}
			addresses[address.Address] = true
			fmt.Fprintf(&aliases, "%s %s\n", address.Address, user)
		}
	}
	return map[string][]byte{"postfix-accounts.cf": []byte(accounts.String()), "postfix-virtual.cf": []byte(aliases.String()), "dovecot-quotas.cf": {}}, nil
}

func receipt(e entry) Receipt {
	return Receipt{MailboxID: e.Projection.Mailbox.ID, Revision: e.Projection.Revision, Fingerprint: e.Projection.Fingerprint, Applied: e.Applied}
}
func (a *Adapter) Apply(ctx context.Context, in ApplyRequest) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if !canonicalProjection(in.Projection) || in.ExpectedRevision != "" && !uuid.MatchString(in.ExpectedRevision) {
		return Receipt{}, ErrConflict
	}
	state, err := a.load()
	if err != nil {
		return Receipt{}, err
	}
	id := in.Projection.Mailbox.ID
	old, exists := state.Boxes[id]
	if exists {
		if old.Projection.Mailbox.ProtocolLogin != in.Projection.Mailbox.ProtocolLogin || old.Projection.Mailbox.StorageRef != in.Projection.Mailbox.StorageRef || old.Projection.Mailbox.RoundcubeKey != in.Projection.Mailbox.RoundcubeKey || old.Projection.Mailbox.OwnerAnchor != in.Projection.Mailbox.OwnerAnchor || old.Projection.Resource.Kind != in.Projection.Resource.Kind {
			return Receipt{}, ErrConflict
		}
		if old.Projection.Revision == in.Projection.Revision {
			if old.Projection.Fingerprint != in.Projection.Fingerprint || old.Projection.Epoch != in.Projection.Epoch {
				return Receipt{}, ErrConflict
			}
		} else if in.ExpectedRevision != old.Projection.Revision {
			return Receipt{}, ErrConflict
		}
	} else if in.ExpectedRevision != "" {
		return Receipt{}, ErrConflict
	}
	startupErr := a.initializationReady(ctx)
	// DMS needs the first account to finish bootstrapping. Keep that initial
	// generation stable until its change detector has started: otherwise DMS
	// can load one generation and checkpoint a later one without reloading it.
	// Replaying an existing intent may repair partial files, but cannot change
	// the generation or add another mailbox during native initialization.
	sameGeneration := exists && old.Projection.Revision == in.Projection.Revision
	if startupErr != nil && len(state.Boxes) != 0 && !sameGeneration {
		out := receipt(old)
		out.Applied = false
		return out, startupErr
	}
	hash := old.PasswordHash
	if !exists {
		var random [64]byte
		if _, err = rand.Read(random[:]); err != nil {
			return Receipt{}, err
		}
		sum := sha512.Sum512(random[:])
		hash = "{SHA512}" + base64.StdEncoding.EncodeToString(sum[:])
	}
	state.Boxes[id] = entry{Projection: in.Projection, PasswordHash: hash, Applied: false}
	generated, err := files(state.Boxes, a.CapacityBytes)
	if err != nil {
		return Receipt{}, err
	}
	// Persist the intended complete generation before touching DMS inputs.
	// An interrupted or unknown outcome stays unapplied and is retried in place.
	if err = a.save(state); err != nil {
		return Receipt{}, err
	}
	for _, name := range []string{"postfix-accounts.cf", "postfix-virtual.cf", "dovecot-quotas.cf"} {
		if err = atomicFile(filepath.Join(a.ConfigDir, name), generated[name], 0600); err != nil {
			return Receipt{}, err
		}
	}
	if startupErr != nil {
		return receipt(state.Boxes[id]), startupErr
	}
	probe := a.probe
	if probe == nil {
		probe = a.liveProbe
	}
	if err = probe(ctx, map[string]entry{id: state.Boxes[id]}); err != nil {
		return receipt(state.Boxes[id]), err
	}
	box := state.Boxes[id]
	box.Applied = true
	state.Boxes[id] = box
	if err = a.save(state); err != nil {
		return Receipt{}, err
	}
	return receipt(box), nil
}

func (a *Adapter) Read(ctx context.Context, id string) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Receipt{}, err
	}
	if !uuid.MatchString(id) {
		return Receipt{}, ErrConflict
	}
	state, err := a.load()
	if err != nil {
		return Receipt{}, err
	}
	box, ok := state.Boxes[id]
	if !ok {
		return Receipt{}, ErrConflict
	}
	if err := a.initializationReady(ctx); err != nil {
		out := receipt(box)
		out.Applied = false
		return out, err
	}
	// Re-read generated files and live DMS state. A saved receipt alone never
	// proves the current container has loaded the intended configuration.
	generated, err := files(state.Boxes, a.CapacityBytes)
	if err != nil {
		out := receipt(box)
		out.Applied = false
		return out, ErrUnavailable
	}
	for name, want := range generated {
		actual, err := os.ReadFile(filepath.Join(a.ConfigDir, name))
		if err != nil || !bytes.Equal(actual, want) {
			out := receipt(box)
			out.Applied = false
			return out, ErrUnavailable
		}
	}
	probe := a.probe
	if probe == nil {
		probe = a.liveProbe
	}
	if err := probe(ctx, map[string]entry{id: box}); err != nil {
		out := receipt(box)
		out.Applied = false
		return out, err
	}
	if !box.Applied {
		return receipt(box), ErrUnavailable
	}
	return receipt(box), nil
}

func command(ctx context.Context, name string, args ...string) (string, error) {
	out, err := commandWithOutcome(ctx, name, args...)
	if err != nil {
		return "", ErrUnavailable
	}
	return out, nil
}

// Keep timeout information for the private startup diagnostic without
// exposing command output or changing the other callers' error contract.
func commandWithOutcome(ctx context.Context, name string, args ...string) (string, error) {
	return commandWithin(ctx, 3*time.Second, name, args...)
}

func commandWithin(ctx context.Context, limit time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	var output bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errors.Join(ErrUnavailable, ctx.Err())
		}
		return "", ErrUnavailable
	}
	if output.Len() > 1<<20 {
		return "", ErrUnavailable
	}
	return output.String(), nil
}
func (a *Adapter) liveProbe(ctx context.Context, boxes map[string]entry) error {
	mechanisms, err := command(ctx, "/usr/bin/doveconf", "-h", "auth_mechanisms")
	if err != nil {
		return &checkFailure{"authentication-config"}
	}
	for _, mechanism := range strings.Fields(mechanisms) {
		if mechanism != "oauthbearer" && mechanism != "xoauth2" {
			return &checkFailure{"authentication-config"}
		}
	}
	if strings.TrimSpace(mechanisms) == "" {
		return &checkFailure{"authentication-config"}
	}
	cache, err := command(ctx, "/usr/bin/doveconf", "-h", "auth_cache_size")
	if err != nil || strings.TrimSpace(cache) != "0" {
		return &checkFailure{"authentication-cache"}
	}
	for _, box := range boxes {
		p := box.Projection
		user := p.Mailbox.ProtocolLogin + "@" + ProtocolRealm
		out, err := command(ctx, "/usr/bin/doveadm", "user", user)
		if err != nil {
			return &checkFailure{"mailbox-storage"}
		}
		foundPath := false
		foundQuota, zeroGrace := false, false
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[0] == "mail_path" && fields[1] == "/var/mail/"+p.Mailbox.StorageRef {
				foundPath = true
			}
			if len(fields) == 2 && fields[0] == "quota_storage_size" && fields[1] == fmt.Sprintf("%dB", a.CapacityBytes) {
				foundQuota = true
			}
			if len(fields) == 2 && fields[0] == "quota_storage_grace" && (fields[1] == "0" || fields[1] == "0B") {
				zeroGrace = true
			}
		}
		if !foundPath {
			return &checkFailure{"mailbox-storage"}
		}
		if !foundQuota || !zeroGrace {
			return &checkFailure{"mailbox-quota"}
		}
		for _, address := range p.Resource.Addresses {
			out, err := command(ctx, "/usr/sbin/postmap", "-q", address.Address, "texthash:/etc/postfix/virtual")
			if err != nil && address.State == "active" {
				return &checkFailure{"address-route"}
			}
			if address.State == "active" && strings.TrimSpace(out) != user || address.State != "active" && strings.TrimSpace(out) != "" {
				return &checkFailure{"address-route"}
			}
			if address.State == "active" && ProbeRecipient(ctx, address.Address) != nil {
				return &checkFailure{"smtp-recipient"}
			}
		}
	}
	return nil
}

// ProbeRecipient performs no DATA command and sends no email. Used by the
// coordinator's final live-recipient check after the map/userdb readback.
func ProbeRecipient(ctx context.Context, address string) error {
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", "127.0.0.1:25")
	if err != nil {
		return ErrUnavailable
	}
	defer conn.Close()
	// Postscreen may intentionally wait about six seconds before greeting a
	// new peer. A three-second command-style deadline falsely rejects DMS.
	deadline := time.Now().Add(12 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	c, err := smtp.NewClient(conn, "localhost")
	if err != nil {
		return ErrUnavailable
	}
	defer c.Close()
	if c.Hello("localhost.localdomain") != nil || c.Mail("") != nil || c.Rcpt(address) != nil {
		return ErrUnavailable
	}
	return c.Reset()
}

func Kick(ctx context.Context, session string) error {
	return KickMailbox(ctx, session, "")
}
func KickMailbox(ctx context.Context, session, box string) error {
	if !uuid.MatchString(session) {
		return ErrConflict
	}
	field, value := "user_anas_sid", session
	if box != "" {
		if !uuid.MatchString(box) {
			return ErrConflict
		}
		field, value = "user_anas_context", session+"."+box
	}
	_, err := command(ctx, "/usr/bin/doveadm", "kick", "-f", field, value)
	return err
}
