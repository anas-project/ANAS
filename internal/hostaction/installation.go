package hostaction

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
)

const (
	installationSchema   = "anas.host-action-installation/v3"
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

func (r ReleaseIdentity) Validate() error {
	if len(r.Version) > 128 || !releaseVersion.MatchString(r.Version) || !releaseCommit.MatchString(r.Commit) {
		return ErrUnavailable
	}
	return nil
}

// This is root installation data, not a public/Module configuration schema.
// It binds the socket to one release and contains no paths, executables,
// handlers, secrets, peer identities or environment overrides: the socket is
// root:root 0600 and only a root peer is admitted (PeerPolicy).
type installationPolicy struct {
	Schema  string          `json:"schema"`
	Release ReleaseIdentity `json:"release"`
}

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
		p.Schema != installationSchema || p.Release != expected {
		return installationPolicy{}, ErrUnavailable
	}
	return p, nil
}

func (installationPolicy) peers() PeerPolicy { return PeerPolicy{} }

func (installationPolicy) socketGroup() uint32 { return admittedPeer.gid }

// Accept=yes hands over one connected fd, rather than a listening socket.
// Environment markers describe that handoff; they are NOT peer authentication.
func checkActivationEnvironment(pid int, lookup func(string) string) error {
	if pid <= 0 || lookup == nil || lookup("LISTEN_PID") != strconv.Itoa(pid) ||
		lookup("LISTEN_FDS") != "1" || lookup("LISTEN_FDNAMES") != "connection" {
		return ErrUnavailable
	}
	return nil
}
