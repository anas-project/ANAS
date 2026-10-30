package installer

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"

	"golang.org/x/sys/unix"
)

type sourceInputs struct {
	Domains       []string `json:"domains"`
	CapacityBytes int64    `json:"capacity_bytes"`
	DeliveredDays int      `json:"delivered_days"`
	NotifyOrigin  string   `json:"notify_origin,omitempty"`
}

type privateSource struct {
	Schema      string       `json:"schema"`
	Bucket      string       `json:"bucket"`
	Inputs      sourceInputs `json:"inputs"`
	SourceID    string       `json:"source_id"`
	Generation  string       `json:"generation"`
	OperationID string       `json:"operation_id"`
	Reader      string       `json:"reader"`
	Control     string       `json:"control"`
	NotifyToken string       `json:"notify_token,omitempty"`
}

func newUUID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	s := hex.EncodeToString(raw[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}

func newSecret() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (p privateSource) config() versionConfig {
	return versionConfig{Bucket: p.Bucket, SourceID: p.SourceID, Generation: p.Generation, OperationID: p.OperationID,
		Domains: append([]string(nil), p.Inputs.Domains...), CapacityBytes: p.Inputs.CapacityBytes,
		DeliveredDays: p.Inputs.DeliveredDays, Reader: p.Reader, Control: p.Control,
		NotifyOrigin: p.Inputs.NotifyOrigin, NotifyToken: p.NotifyToken}
}

// Validate user-supplied values without creating credentials or cloud resources.
func validSourceInputs(spec Spec, inputs sourceInputs) bool {
	check := privateSource{Bucket: spec.Bucket, Inputs: inputs, SourceID: "11111111-1111-4111-8111-111111111111", Generation: "11111111-1111-4111-8111-111111111111", OperationID: "11111111-1111-4111-8111-111111111111", Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
	if inputs.NotifyOrigin != "" {
		check.NotifyToken = strings.Repeat("c", 64)
	}
	return check.config().valid()
}

// sourceConfig persists the generated Reader/Control credentials once, before
// publication. The held journal lock serializes creation and recovery. The
// file is never overwritten, included in the public journal, or auto-repaired
// after an incomplete write. Provider management tokens are never stored here.
func (j *Journal) sourceConfig(inputs sourceInputs) (versionConfig, error) {
	if j.file == nil || j.failed || j.dirfd < 0 {
		return versionConfig{}, ErrJournal
	}
	if !validSourceInputs(j.state.Spec, inputs) {
		return versionConfig{}, ErrJournal
	}
	switch j.state.Stage {
	case "reserved", "version-issued", "version-created", "version-verified", "schedule-issued", "schedule-verified", "endpoint-issued", "endpoint-configured", "endpoint-verified":
	default:
		return versionConfig{}, ErrJournal
	}
	const filename = "source-secrets.json"
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	var fd int
	var err error
	created := false
	if j.state.Stage == "reserved" {
		fd, err = unix.Openat(j.dirfd, filename, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		created = err == nil
		if errors.Is(err, unix.EEXIST) {
			fd, err = unix.Openat(j.dirfd, filename, flags, 0)
		}
	} else {
		fd, err = unix.Openat(j.dirfd, filename, flags, 0)
		if errors.Is(err, unix.ENOENT) {
			return versionConfig{}, ErrUnknown
		}
	}
	if err != nil {
		return versionConfig{}, ErrJournal
	}
	f := os.NewFile(uintptr(fd), filename)
	defer f.Close()
	if !safeFile(fd, false) {
		return versionConfig{}, ErrJournal
	}
	if created {
		p := privateSource{Schema: "anas.mail-cloud-source-secrets/v1", Bucket: j.state.Spec.Bucket, Inputs: inputs}
		if p.SourceID, err = newUUID(); err == nil {
			p.Generation, err = newUUID()
		}
		if err == nil {
			p.OperationID, err = newUUID()
		}
		if err == nil {
			p.Reader, err = newSecret()
		}
		if err == nil {
			p.Control, err = newSecret()
		}
		if err == nil && inputs.NotifyOrigin != "" {
			p.NotifyToken, err = newSecret()
		}
		if err != nil || !p.config().valid() {
			return versionConfig{}, ErrJournal
		}
		data, err := json.Marshal(p)
		if err != nil {
			return versionConfig{}, ErrJournal
		}
		data = append(data, '\n')
		if n, err := f.Write(data); err != nil || n != len(data) || f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
			return versionConfig{}, ErrUnknown
		}
		return p.config(), nil
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	if err != nil || len(raw) == 0 || len(raw) > 16384 || raw[len(raw)-1] != '\n' {
		return versionConfig{}, ErrUnknown
	}
	var p privateSource
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&p) != nil {
		return versionConfig{}, ErrUnknown
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || p.Schema != "anas.mail-cloud-source-secrets/v1" || p.Bucket != j.state.Spec.Bucket || !p.config().valid() {
		return versionConfig{}, ErrUnknown
	}
	if !reflect.DeepEqual(p.Inputs, inputs) {
		return versionConfig{}, ErrConflict
	}
	if f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
		return versionConfig{}, ErrJournal
	}
	return p.config(), nil
}
