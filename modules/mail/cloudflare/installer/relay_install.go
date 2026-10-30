package installer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

// RelayInstallRequest contains private source credentials and a trusted prior
// source snapshot. Neither belongs in a browser response or operational log.
type RelayInstallRequest struct {
	Runtime       RelayRequest            `json:"-"`
	Existing      []ingress.RuntimeSource `json:"-"`
	DrainIDs      []string                `json:"-"`
	TotalCapacity int64                   `json:"-"`
	OperationID   string                  `json:"-"`
}

func (r RelayInstallRequest) String() string   { return "[private relay installation request]" }
func (r RelayInstallRequest) GoString() string { return r.String() }

type RelayInstallIntent struct {
	OperationID string
	SourceID    string
	Origin      string
	Domains     []string
}
type RelayInstallObserver interface {
	BeforeRelayPrepare(context.Context, RelayInstallIntent) error
	BeforeRelayCommit(context.Context, RelayInstallIntent) error
}

// RelayJournal serializes one operation within an existing private directory.
// The caller must reauthorize the administrator and service grant, take the
// workspace-wide installation lock, and provide a trusted source snapshot.
type RelayJournal struct {
	dirfd int
	lock  *os.File
	fresh bool
}

func OpenRelayJournal(directory string) (*RelayJournal, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrJournal
	}
	dir, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrJournal
	}
	if !safeFile(dir, true) {
		unix.Close(dir)
		return nil, ErrJournal
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(dir, "relay.lock", flags|unix.O_CREAT|unix.O_EXCL, 0600)
	fresh := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(dir, "relay.lock", flags, 0)
	}
	if err != nil {
		unix.Close(dir)
		return nil, ErrJournal
	}
	f := os.NewFile(uintptr(fd), "relay installation lock")
	if !safeFile(fd, false) {
		f.Close()
		unix.Close(dir)
		return nil, ErrJournal
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		unix.Close(dir)
		return nil, ErrBusy
	}
	if fresh {
		scan, e := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e != nil {
			unix.Flock(fd, unix.LOCK_UN)
			f.Close()
			unix.Close(dir)
			return nil, ErrUnknown
		}
		entries := os.NewFile(uintptr(scan), "relay installation directory")
		names, e := entries.Readdirnames(-1)
		entries.Close()
		if e != nil || len(names) != 1 || names[0] != "relay.lock" {
			unix.Flock(fd, unix.LOCK_UN)
			f.Close()
			unix.Close(dir)
			return nil, ErrUnknown
		}
	}
	if f.Sync() != nil || unix.Fsync(dir) != nil {
		unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		unix.Close(dir)
		return nil, ErrUnknown
	}
	return &RelayJournal{dirfd: dir, lock: f, fresh: fresh}, nil
}

func (j *RelayJournal) Close() error {
	if j == nil || j.lock == nil {
		return nil
	}
	_ = unix.Flock(int(j.lock.Fd()), unix.LOCK_UN)
	err := j.lock.Close()
	j.lock = nil
	_ = unix.Close(j.dirfd)
	j.dirfd = -1
	return err
}

func relayRequestCandidate(r RelayInstallRequest) (PreparedSource, error) {
	if !validInstallOperationID(r.OperationID) || ingress.ValidateSources(r.Existing, r.Runtime.AllowedDomains) != nil || ingress.ValidateCapacity(r.Existing, r.TotalCapacity) != nil {
		return PreparedSource{}, ErrConflict
	}
	p, err := relayCandidate(r.Runtime)
	if err != nil {
		return PreparedSource{}, err
	}
	planned, err := planSourceDrains(r.Existing, r.DrainIDs, p.spec.Domains)
	if err != nil {
		return PreparedSource{}, err
	}
	if _, err = p.Merge(planned, r.Runtime.AllowedDomains, r.TotalCapacity); err != nil {
		return PreparedSource{}, err
	}
	return p, nil
}

func (j *RelayJournal) bind(r RelayInstallRequest, create bool) error {
	if j == nil || j.lock == nil || j.dirfd < 0 {
		return ErrJournal
	}
	p, err := relayRequestCandidate(r)
	if err != nil {
		return err
	}
	key, err := hex.DecodeString(r.Runtime.Control)
	if err != nil || len(key) != 32 {
		return ErrJournal
	}
	defer clear(key)
	value := struct {
		Schema             string                  `json:"schema"`
		Source             ingress.RuntimeSource   `json:"source"`
		NotificationOrigin string                  `json:"notification_origin"`
		Allowed            []string                `json:"allowed"`
		Existing           []ingress.RuntimeSource `json:"existing"`
		Drain              []string                `json:"drain"`
		TotalCapacity      int64                   `json:"total_capacity"`
		OperationID        string                  `json:"operation_id"`
	}{"anas.mail-relay-install-input/v1", ingress.RuntimeSource{SourceSpec: p.Specification(), Credentials: p.credentials}, r.Runtime.NotificationOrigin, r.Runtime.AllowedDomains, r.Existing, r.DrainIDs, r.TotalCapacity, r.OperationID}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return ErrJournal
	}
	defer clear(raw)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(raw)
	wanted := hex.EncodeToString(mac.Sum(nil))
	type binding struct {
		Schema string `json:"schema"`
		MAC    string `json:"mac"`
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(j.dirfd, "relay-binding.json", flags, 0)
	if errors.Is(err, unix.ENOENT) && create && j.fresh {
		j.fresh = false
		fd, err = unix.Openat(j.dirfd, "relay-binding.json", flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if err != nil {
			return ErrUnknown
		}
		f := os.NewFile(uintptr(fd), "relay binding")
		defer f.Close()
		encoded, _ := json.Marshal(binding{Schema: "anas.mail-relay-install-binding/v1", MAC: wanted})
		encoded = append(encoded, '\n')
		if !safeFile(fd, false) {
			return ErrJournal
		}
		if n, e := f.Write(encoded); e != nil || n != len(encoded) || f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
			return ErrUnknown
		}
		return nil
	}
	if errors.Is(err, unix.ENOENT) {
		return ErrUnknown
	}
	if err != nil {
		return ErrJournal
	}
	j.fresh = false
	f := os.NewFile(uintptr(fd), "relay binding")
	defer f.Close()
	if !safeFile(fd, false) {
		return ErrJournal
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) == 0 || len(data) > 4096 || data[len(data)-1] != '\n' {
		return ErrUnknown
	}
	var found binding
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&found) != nil || d.Decode(&extra) != io.EOF || found.Schema != "anas.mail-relay-install-binding/v1" || !fingerprint.MatchString(found.MAC) {
		return ErrUnknown
	}
	if !hmac.Equal([]byte(found.MAC), []byte(wanted)) {
		return ErrConflict
	}
	if f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
		return ErrUnknown
	}
	return nil
}

func (j *RelayJournal) prepared(create bool) error {
	const name, content = "relay-prepared", "anas.mail-relay-prepared/v1\n"
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(j.dirfd, name, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		// A missing prepared marker after any commit attempt cannot be safely
		// reconstructed as a fresh operation.
		guard, e := unix.Openat(j.dirfd, "commit-guard.json", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if e == nil {
			unix.Close(guard)
			return ErrUnknown
		}
		if !errors.Is(e, unix.ENOENT) {
			return ErrJournal
		}
		fd, err = unix.Openat(j.dirfd, name, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if err != nil {
			return ErrUnknown
		}
		f := os.NewFile(uintptr(fd), name)
		defer f.Close()
		if !safeFile(fd, false) {
			return ErrJournal
		}
		if n, e := f.WriteString(content); e != nil || n != len(content) || f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
			return ErrUnknown
		}
		return nil
	}
	if errors.Is(err, unix.ENOENT) {
		return ErrUnknown
	}
	if err != nil {
		return ErrJournal
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if !safeFile(fd, false) {
		return ErrJournal
	}
	data, err := io.ReadAll(io.LimitReader(f, 128))
	if err != nil || string(data) != content || f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
		return ErrUnknown
	}
	return nil
}

func relayIntent(r RelayInstallRequest) RelayInstallIntent {
	return RelayInstallIntent{OperationID: r.OperationID, SourceID: r.Runtime.SourceID, Origin: r.Runtime.Origin, Domains: append([]string(nil), r.Runtime.Domains...)}
}

// BindRemotePlan records the trusted source snapshot before any VPS SSH
// operation. After a commit was attempted, old SSH actions are refused; the
// guarded configuration transaction can still recover without reissuing them.
func (j *RelayJournal) BindRemotePlan(ctx context.Context, observer RelayInstallObserver, r RelayInstallRequest, current []ingress.RuntimeSource) error {
	if j == nil || j.lock == nil || observer == nil || ctx.Err() != nil {
		return ErrJournal
	}
	request, err := j.RequestWithCurrentSources(r, current)
	if err != nil {
		return err
	}
	if _, err = relayRequestCandidate(request); err != nil {
		return err
	}
	var st unix.Stat_t
	err = unix.Fstatat(j.dirfd, "commit-guard.json", &st, unix.AT_SYMLINK_NOFOLLOW)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, unix.ENOENT) {
		return ErrUnknown
	}
	if observer.BeforeRelayPrepare(ctx, relayIntent(request)) != nil {
		return ErrUnknown
	}
	return j.bind(request, true)
}

// Prepare validates the source through TLS after auditing and binding inputs.
// It does not modify the source, DNS, workspace configuration or Secret Store.
func (j *RelayJournal) Prepare(ctx context.Context, observer RelayInstallObserver, r RelayInstallRequest) (ingress.SourceSpec, error) {
	return j.prepare(ctx, observer, r, nil)
}
func (j *RelayJournal) prepare(ctx context.Context, observer RelayInstallObserver, r RelayInstallRequest, ca []byte) (ingress.SourceSpec, error) {
	if j == nil || j.lock == nil || observer == nil || ctx.Err() != nil {
		return ingress.SourceSpec{}, ErrJournal
	}
	if _, err := relayRequestCandidate(r); err != nil {
		return ingress.SourceSpec{}, err
	}
	if observer.BeforeRelayPrepare(ctx, relayIntent(r)) != nil {
		return ingress.SourceSpec{}, ErrUnknown
	}
	if err := j.bind(r, true); err != nil {
		return ingress.SourceSpec{}, err
	}
	p, err := prepareRelay(ctx, r.Runtime, ca)
	if err != nil {
		return ingress.SourceSpec{}, err
	}
	if err = j.prepared(true); err != nil {
		return ingress.SourceSpec{}, err
	}
	return p.Specification(), nil
}

// Commit reauthorizes and rechecks the live source. The workspace-locked
// observer audits actual writes; the durable guard rejects stale-base replay.
func (j *RelayJournal) Commit(ctx context.Context, service application.ConfigService, observer RelayInstallObserver, commit application.ConfigCommitObserver, r RelayInstallRequest) (ingress.SourceSpec, error) {
	return j.commit(ctx, service, observer, commit, r, nil)
}
func (j *RelayJournal) commit(ctx context.Context, service application.ConfigService, observer RelayInstallObserver, commit application.ConfigCommitObserver, r RelayInstallRequest, ca []byte) (ingress.SourceSpec, error) {
	if j == nil || j.lock == nil || service == nil || observer == nil || commit == nil || ctx.Err() != nil {
		return ingress.SourceSpec{}, ErrJournal
	}
	if observer.BeforeRelayCommit(ctx, relayIntent(r)) != nil {
		return ingress.SourceSpec{}, ErrUnknown
	}
	if err := j.bind(r, false); err != nil {
		return ingress.SourceSpec{}, err
	}
	if err := j.prepared(false); err != nil {
		return ingress.SourceSpec{}, err
	}
	p, err := prepareRelay(ctx, r.Runtime, ca)
	if err != nil {
		return ingress.SourceSpec{}, err
	}
	guarded := installCommitGuard{dirfd: j.dirfd, operation: r.OperationID, observer: commit}
	if err = p.CommitSourceWithDraining(ctx, service, guarded, r.OperationID, r.Existing, r.DrainIDs, r.Runtime.AllowedDomains, r.TotalCapacity); err != nil {
		return ingress.SourceSpec{}, err
	}
	return p.Specification(), nil
}

// RecoverRequest derives the original pre-commit snapshot from a trusted
// current snapshot and requires it to match the bound private input exactly.
func (j *RelayJournal) RecoverRequest(r RelayInstallRequest, current []ingress.RuntimeSource) (RelayInstallRequest, error) {
	if j == nil || j.lock == nil {
		return RelayInstallRequest{}, ErrJournal
	}
	p, err := relayCandidate(r.Runtime)
	if err != nil {
		return RelayInstallRequest{}, err
	}
	wanted := ingress.RuntimeSource{SourceSpec: p.Specification(), Credentials: p.credentials}
	previous := append([]ingress.RuntimeSource(nil), current...)
	for i := range previous {
		previous[i].Domains = append([]string(nil), previous[i].Domains...)
		if previous[i].Cloudflare != nil {
			cf := *previous[i].Cloudflare
			previous[i].Cloudflare = &cf
		}
	}
	for i, source := range previous {
		if source.ID != wanted.ID {
			continue
		}
		if i != len(previous)-1 || !reflect.DeepEqual(source, wanted) {
			return RelayInstallRequest{}, ErrConflict
		}
		previous = previous[:i]
		for _, id := range r.DrainIDs {
			matched := false
			for k := range previous {
				if previous[k].ID == id && previous[k].Mode == "draining" && !matched {
					previous[k].Mode, matched = "active", true
				}
			}
			if !matched {
				return RelayInstallRequest{}, ErrConflict
			}
		}
		break
	}
	if len(previous) == 0 {
		previous = nil
	}
	r.Existing = previous
	if err = j.bind(r, false); err != nil {
		if len(previous) != 0 || err != ErrConflict {
			return RelayInstallRequest{}, err
		}
		r.Existing = []ingress.RuntimeSource{}
		if err = j.bind(r, false); err != nil {
			return RelayInstallRequest{}, err
		}
	}
	return r, nil
}

// RequestWithCurrentSources accepts a trusted snapshot only for a fresh
// operation. Existing operations must reconstruct and match their binding.
func (j *RelayJournal) RequestWithCurrentSources(r RelayInstallRequest, current []ingress.RuntimeSource) (RelayInstallRequest, error) {
	if j == nil || j.lock == nil {
		return RelayInstallRequest{}, ErrJournal
	}
	if j.fresh {
		r.Existing = current
		return r, nil
	}
	return j.RecoverRequest(r, current)
}
