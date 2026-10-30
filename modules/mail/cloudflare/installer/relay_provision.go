package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"
)

// RelayProvisionInput is private, including TLS and SSH private keys. Callers
// must authorize and audit the administrator and workspace before using it.
type RelayProvisionInput struct {
	OperationID       string
	Origin            string
	AllowedDomains    []string
	IPv4Only          bool
	Template          cloudrelay.RuntimeConfig
	SSH               RelaySSHConfig
	CloudflareForward *ingress.CloudflareForwardSpec
	Routing           string
	Destination       string
}

func (RelayProvisionInput) String() string   { return "[private relay provision input]" }
func (RelayProvisionInput) GoString() string { return "[private relay provision input]" }

type PreparedRelayProvision struct {
	Runtime cloudrelay.RuntimeConfig `json:"-"`
	Source  RelayRequest             `json:"-"`
}

func (PreparedRelayProvision) String() string   { return "[private relay provision]" }
func (PreparedRelayProvision) GoString() string { return "[private relay provision]" }
func (p PreparedRelayProvision) SourceSpec() ingress.SourceSpec {
	spec := ingress.SourceSpec{ID: p.Source.SourceID, Generation: p.Source.Generation, Provider: "cloud-relay", Mode: "active", Origin: p.Source.Origin, Domains: append([]string(nil), p.Source.Domains...), CapacityBytes: p.Source.CapacityBytes, DeliveredDays: p.Source.DeliveredDays, Notifications: p.Source.Notification != "", IPv4Only: p.Source.IPv4Only}
	if p.Source.CloudflareForward != nil {
		binding := *p.Source.CloudflareForward
		spec.CloudflareForward = &binding
	}
	return spec
}

type provisionRecord struct {
	Schema               string                         `json:"schema"`
	OperationID          string                         `json:"operation_id"`
	Origin               string                         `json:"origin"`
	AllowedDomains       []string                       `json:"allowed_domains"`
	IPv4Only             bool                           `json:"ipv4_only"`
	CloudflareForward    *ingress.CloudflareForwardSpec `json:"cloudflare_forward,omitempty"`
	SSHHost              string                         `json:"ssh_host"`
	SSHPort              int                            `json:"ssh_port"`
	SSHUser              string                         `json:"ssh_user"`
	SSHHostFingerprint   string                         `json:"ssh_host_fingerprint"`
	SSHClientFingerprint string                         `json:"ssh_client_fingerprint"`
	Runtime              cloudrelay.RuntimeConfig       `json:"runtime"`
}

type RelayProvisionJournal struct {
	dir   int
	lock  *os.File
	fresh bool
}

func OpenRelayProvisionJournal(directory string) (*RelayProvisionJournal, error) {
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return nil, ErrJournal
	}
	dir, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrJournal
	}
	fail := func(e error) (*RelayProvisionJournal, error) { unix.Close(dir); return nil, e }
	if !safeFile(dir, true) {
		return fail(ErrJournal)
	}
	// Refuse to create even a lock in an unowned, nonempty operation directory.
	scan, err := unix.Openat(dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fail(ErrUnknown)
	}
	entries := os.NewFile(uintptr(scan), "provision directory")
	names, err := entries.Readdirnames(-1)
	entries.Close()
	if err != nil {
		return fail(ErrUnknown)
	}
	hasLock := false
	for _, name := range names {
		if name == "provision.lock" {
			hasLock = true
		} else if name != "provision.json" && name != "remote-publish.issued" && name != "remote-publish.done" && name != "remote-activate.issued" && name != "remote-activate.done" {
			return fail(ErrUnknown)
		}
	}
	if len(names) > 0 && !hasLock {
		return fail(ErrUnknown)
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(dir, "provision.lock", flags|unix.O_CREAT|unix.O_EXCL, 0600)
	fresh := err == nil
	if errors.Is(err, unix.EEXIST) {
		fd, err = unix.Openat(dir, "provision.lock", flags, 0)
	}
	if err != nil {
		return fail(ErrJournal)
	}
	f := os.NewFile(uintptr(fd), "provision lock")
	if !safeFile(fd, false) || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		f.Close()
		return fail(ErrBusy)
	}
	if fresh && len(names) != 0 {
		unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		return fail(ErrUnknown)
	}
	if f.Sync() != nil || unix.Fsync(dir) != nil {
		unix.Flock(fd, unix.LOCK_UN)
		f.Close()
		return fail(ErrUnknown)
	}
	return &RelayProvisionJournal{dir: dir, lock: f, fresh: fresh}, nil
}
func (j *RelayProvisionJournal) Close() error {
	if j == nil || j.lock == nil {
		return nil
	}
	_ = unix.Flock(int(j.lock.Fd()), unix.LOCK_UN)
	err := j.lock.Close()
	j.lock = nil
	_ = unix.Close(j.dir)
	j.dir = -1
	return err
}

func provisionInputMetadata(input RelayProvisionInput) (provisionRecord, error) {
	if !validInstallOperationID(input.OperationID) || input.Template.Schema != "anas.mail-vps-runtime/v1" || input.Template.Directory != "/var/lib/anas-mail-relay/data/source" || input.Template.SourceID != "" || input.Template.Generation != "" || input.Template.Reader != "" || input.Template.Control != "" || input.Template.NotificationSecret != "" || input.Template.SMTPListen == "" {
		return provisionRecord{}, ErrConflict
	}
	if input.CloudflareForward == nil && (input.Template.ForwardDomain != "" || input.Routing != "" || input.Destination != "") || input.CloudflareForward != nil && (input.Template.ForwardDomain != input.CloudflareForward.Domain || input.Routing == "" || input.Destination == "") {
		return provisionRecord{}, ErrConflict
	}
	hostKey, signer, err := validateRelaySSHConfig(input.SSH)
	if err != nil {
		return provisionRecord{}, ErrConflict
	}
	origin, err := url.Parse(input.Origin)
	if err != nil || origin.Scheme != "https" || origin.User != nil || origin.Host == "" || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || origin.ForceQuery || origin.Hostname() != input.Template.CertificateHost {
		return provisionRecord{}, ErrConflict
	}
	_, port, err := net.SplitHostPort(input.Template.Listen)
	if err != nil {
		return provisionRecord{}, ErrConflict
	}
	originPort := origin.Port()
	if originPort == "" {
		originPort = "443"
	}
	if port != originPort {
		return provisionRecord{}, ErrConflict
	}
	var forward *ingress.CloudflareForwardSpec
	if input.CloudflareForward != nil {
		binding := *input.CloudflareForward
		forward = &binding
	}
	return provisionRecord{Schema: "anas.mail-relay-provision/v1", OperationID: input.OperationID, Origin: input.Origin, AllowedDomains: append([]string(nil), input.AllowedDomains...), IPv4Only: input.IPv4Only, CloudflareForward: forward, SSHHost: input.SSH.Host, SSHPort: input.SSH.Port, SSHUser: input.SSH.User, SSHHostFingerprint: ssh.FingerprintSHA256(hostKey), SSHClientFingerprint: ssh.FingerprintSHA256(signer.PublicKey()), Runtime: input.Template}, nil
}
func (r provisionRecord) output(input RelayProvisionInput) (PreparedRelayProvision, error) {
	cfg := r.Runtime
	if cloudrelay.ValidateRuntimeConfig(cfg) != nil {
		return PreparedRelayProvision{}, ErrConflict
	}
	source := RelayRequest{SourceID: cfg.SourceID, Generation: cfg.Generation, Origin: r.Origin, Domains: append([]string(nil), cfg.Domains...), AllowedDomains: append([]string(nil), r.AllowedDomains...), CapacityBytes: cfg.CapacityBytes, DeliveredDays: cfg.DeliveredDays, NotificationOrigin: cfg.NotificationOrigin, IPv4Only: r.IPv4Only, CloudflareForward: r.CloudflareForward, Reader: cfg.Reader, Control: cfg.Control, Notification: cfg.NotificationSecret, Routing: input.Routing, Destination: input.Destination}
	if _, err := relayCandidate(source); err != nil {
		return PreparedRelayProvision{}, ErrConflict
	}
	return PreparedRelayProvision{Runtime: cfg, Source: source}, nil
}
func provisionTemplate(cfg cloudrelay.RuntimeConfig) cloudrelay.RuntimeConfig {
	cfg.SourceID = ""
	cfg.Generation = ""
	cfg.Reader = ""
	cfg.Control = ""
	cfg.NotificationSecret = ""
	return cfg
}

// Prepare generates independent identifiers and credentials once and keeps them
// in the locked 0600 journal. Missing or incomplete state after reopening is
// unknown, never an instruction to regenerate a possibly deployed source.
func (j *RelayProvisionJournal) Prepare(input RelayProvisionInput) (PreparedRelayProvision, error) {
	if j == nil || j.lock == nil || j.dir < 0 {
		return PreparedRelayProvision{}, ErrJournal
	}
	expected, err := provisionInputMetadata(input)
	if err != nil {
		return PreparedRelayProvision{}, err
	}
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(j.dir, "provision.json", flags, 0)
	if errors.Is(err, unix.ENOENT) && j.fresh {
		// Validate before creating the private file with temporary synthetic values.
		check := expected
		check.Runtime.SourceID = "11111111-1111-4111-8111-111111111111"
		check.Runtime.Generation = "22222222-2222-4222-8222-222222222222"
		check.Runtime.Reader = strings.Repeat("a", 64)
		check.Runtime.Control = strings.Repeat("b", 64)
		if check.Runtime.NotificationOrigin != "" {
			check.Runtime.NotificationSecret = strings.Repeat("c", 64)
		}
		if _, err = check.output(input); err != nil {
			return PreparedRelayProvision{}, err
		}
		r := expected
		if r.Runtime.SourceID, err = newUUID(); err == nil {
			r.Runtime.Generation, err = newUUID()
		}
		if err == nil {
			r.Runtime.Reader, err = newSecret()
		}
		if err == nil {
			r.Runtime.Control, err = newSecret()
		}
		if err == nil && r.Runtime.NotificationOrigin != "" {
			r.Runtime.NotificationSecret, err = newSecret()
		}
		if err != nil {
			return PreparedRelayProvision{}, ErrUnknown
		}
		prepared, err := r.output(input)
		if err != nil {
			return PreparedRelayProvision{}, ErrUnknown
		}
		raw, err := json.Marshal(r)
		if err != nil || len(raw) > 128<<10 {
			return PreparedRelayProvision{}, ErrJournal
		}
		raw = append(raw, '\n')
		defer clear(raw)
		j.fresh = false
		fd, err = unix.Openat(j.dir, "provision.json", flags|unix.O_CREAT|unix.O_EXCL, 0600)
		if err != nil {
			return PreparedRelayProvision{}, ErrUnknown
		}
		f := os.NewFile(uintptr(fd), "private provision")
		defer f.Close()
		if !safeFile(fd, false) {
			return PreparedRelayProvision{}, ErrJournal
		}
		if n, e := f.Write(raw); e != nil || n != len(raw) || f.Sync() != nil || unix.Fsync(j.dir) != nil {
			return PreparedRelayProvision{}, ErrUnknown
		}
		return prepared, nil
	}
	if errors.Is(err, unix.ENOENT) {
		return PreparedRelayProvision{}, ErrUnknown
	}
	if err != nil {
		return PreparedRelayProvision{}, ErrJournal
	}
	j.fresh = false
	f := os.NewFile(uintptr(fd), "private provision")
	defer f.Close()
	if !safeFile(fd, false) {
		return PreparedRelayProvision{}, ErrJournal
	}
	raw, err := io.ReadAll(io.LimitReader(f, 128<<10+1))
	if err != nil || len(raw) == 0 || len(raw) > 128<<10 || raw[len(raw)-1] != '\n' {
		return PreparedRelayProvision{}, ErrUnknown
	}
	defer clear(raw)
	var found provisionRecord
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&found) != nil || d.Decode(&extra) != io.EOF || found.Schema != "anas.mail-relay-provision/v1" {
		return PreparedRelayProvision{}, ErrUnknown
	}
	canonical, e := json.Marshal(found)
	if e != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		clear(canonical)
		return PreparedRelayProvision{}, ErrUnknown
	}
	clear(canonical)
	template := found
	template.Runtime = provisionTemplate(found.Runtime)
	if !reflect.DeepEqual(template, expected) {
		return PreparedRelayProvision{}, ErrConflict
	}
	prepared, err := found.output(input)
	if err != nil {
		return PreparedRelayProvision{}, ErrUnknown
	}
	if f.Sync() != nil || unix.Fsync(j.dir) != nil {
		return PreparedRelayProvision{}, ErrUnknown
	}
	return prepared, nil
}
