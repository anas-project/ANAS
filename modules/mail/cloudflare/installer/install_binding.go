package installer

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"golang.org/x/sys/unix"
)

// bindInstallRequest uses the routing token as a MAC key, without storing it
// in the journal. The private file binds all other inputs to the operation so
// recovery cannot silently substitute an old source, proxy, or route scope.
// It is not an authorization decision and cannot replace a live grant check.
func (j *Journal) bindInstallRequest(request InstallRequest) error {
	if len(request.Runtime.RoutingToken) < 16 {
		return ErrJournal
	}
	value := struct {
		Schema        string                  `json:"schema"`
		Spec          Spec                    `json:"spec"`
		Domains       []string                `json:"domains"`
		Allowed       []string                `json:"allowed"`
		Capacity      int64                   `json:"capacity"`
		Days          int                     `json:"days"`
		Notification  string                  `json:"notification"`
		Proxy         string                  `json:"proxy"`
		Zone          string                  `json:"zone"`
		Existing      []ingress.RuntimeSource `json:"existing"`
		Drain         []string                `json:"drain"`
		TotalCapacity int64                   `json:"total_capacity"`
		OperationID   string                  `json:"operation_id"`
	}{"anas.mail-install-input/v1", j.state.Spec, request.Runtime.Domains, request.Runtime.AllowedDomains,
		request.Runtime.CapacityBytes, request.Runtime.DeliveredDays, request.Runtime.NotificationOrigin,
		request.Runtime.Proxy, request.Runtime.ZoneID, request.Existing, request.DrainIDs,
		request.TotalCapacity, request.OperationID}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 1<<20 {
		return ErrJournal
	}
	mac := hmac.New(sha256.New, []byte(request.Runtime.RoutingToken))
	_, _ = mac.Write(raw)
	wanted := hex.EncodeToString(mac.Sum(nil))
	const filename = "install-binding.json"
	flags := unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
	fd, err := unix.Openat(j.dirfd, filename, flags, 0)
	created := false
	if errors.Is(err, unix.ENOENT) {
		if j.state.Stage != "prepared" {
			// Do not adopt a prior reservation whose request was not bound.
			return ErrUnknown
		}
		fd, err = unix.Openat(j.dirfd, filename, flags|unix.O_CREAT|unix.O_EXCL, 0600)
		created = err == nil
	}
	if err != nil {
		return ErrJournal
	}
	f := os.NewFile(uintptr(fd), filename)
	defer f.Close()
	if !safeFile(fd, false) {
		return ErrJournal
	}
	type binding struct {
		Schema string `json:"schema"`
		MAC    string `json:"mac"`
	}
	if created {
		encoded, err := json.Marshal(binding{Schema: "anas.mail-install-binding/v1", MAC: wanted})
		if err != nil {
			return ErrJournal
		}
		encoded = append(encoded, '\n')
		if n, e := f.Write(encoded); e != nil || n != len(encoded) || f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
			return ErrUnknown
		}
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) == 0 || len(data) > 4096 || data[len(data)-1] != '\n' {
		return ErrUnknown
	}
	var stored binding
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var extra any
	if decoder.Decode(&stored) != nil || decoder.Decode(&extra) != io.EOF || stored.Schema != "anas.mail-install-binding/v1" || !fingerprint.MatchString(stored.MAC) {
		return ErrUnknown
	}
	if !hmac.Equal([]byte(stored.MAC), []byte(wanted)) {
		return ErrConflict
	}
	if f.Sync() != nil || unix.Fsync(j.dirfd) != nil {
		return ErrJournal
	}
	return nil
}
