package hostaction

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
)

const (
	installationSchema   = "anas.host-action-installation/v2"
	installationPath     = "/etc/anas/hostd.json"
	activationSocketPath = "/run/anas/hostd.sock"
	maxInstallationBytes = 4096
)

// ReleaseIdentity is supplied by the installed binary and frozen job, never
// negotiated from a request. Development builds cannot activate the boundary.
type ReleaseIdentity struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

var releaseVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)
var releaseCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)
var installedServiceUnit = regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,180}\.service$`)

func (r ReleaseIdentity) Validate() error {
	if len(r.Version) > 128 || !releaseVersion.MatchString(r.Version) || !releaseCommit.MatchString(r.Commit) {
		return ErrUnavailable
	}
	return nil
}

// This is root installation data, not a public/Module configuration schema.
// It contains no paths, executables, handlers, secrets or environment overrides.
type installationPolicy struct {
	Schema      string          `json:"schema"`
	Release     ReleaseIdentity `json:"release"`
	ServiceMode string          `json:"service_mode"`
	ServiceUnit string          `json:"service_unit"`
	SocketGID   uint32          `json:"socket_gid"`
}

const serviceModeSystemdRoot = "systemd-root-service"

func decodeInstallation(body []byte, expected ReleaseIdentity) (installationPolicy, error) {
	var p installationPolicy
	if expected.Validate() != nil || len(body) == 0 || len(body) > maxInstallationBytes || json.Unmarshal(body, &p) != nil {
		return p, ErrUnavailable
	}
	// Exact canonical keys and numbers reject duplicates, aliases, unknowns,
	// nulls and escaped-key tricks without a second permissive JSON parser.
	canonical, err := json.Marshal(p)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, body) != nil || !bytes.Equal(canonical, compact.Bytes()) ||
		p.Schema != installationSchema || p.Release != expected || p.ServiceMode != serviceModeSystemdRoot ||
		!installedServiceUnit.MatchString(p.ServiceUnit) || p.SocketGID == ^uint32(0) {
		return installationPolicy{}, ErrUnavailable
	}
	return p, nil
}

func (p installationPolicy) peers() PeerPolicy {
	return PeerPolicy{ServiceMode: p.ServiceMode, ServiceUnit: p.ServiceUnit}
}

func (p installationPolicy) socketGroup() uint32 {
	return p.SocketGID
}

// Accept=yes hands over one connected fd, rather than a listening socket.
// Environment markers describe that handoff; they are NOT peer authentication.
func checkActivationEnvironment(pid int, lookup func(string) string) error {
	if pid <= 0 || lookup == nil || lookup("LISTEN_PID") != strconv.Itoa(pid) ||
		lookup("LISTEN_FDS") != "1" || lookup("LISTEN_FDNAMES") != "connection" {
		return ErrUnavailable
	}
	return nil
}
