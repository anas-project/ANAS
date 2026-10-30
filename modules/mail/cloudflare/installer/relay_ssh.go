package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"golang.org/x/crypto/ssh"
)

const relayBootstrapCommand = "sudo -n /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap"

var relaySSHUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

type RelaySSHConfig struct {
	Host       string
	Port       int
	User       string
	HostKey    []byte `json:"-"`
	PrivateKey []byte `json:"-"`
}

func (RelaySSHConfig) String() string   { return "[private relay SSH configuration]" }
func (RelaySSHConfig) GoString() string { return "[private relay SSH configuration]" }

func validateRelaySSHConfig(c RelaySSHConfig) (ssh.PublicKey, ssh.Signer, error) {
	if !relaySSHUser.MatchString(c.User) || c.Port < 1 || c.Port > 65535 || len(c.HostKey) > 8192 || len(c.PrivateKey) > 65536 {
		return nil, nil, ErrJournal
	}
	if net.ParseIP(c.Host) == nil {
		canonical, err := directorymail.Domain(c.Host, "")
		if err != nil || canonical != c.Host {
			return nil, nil, ErrJournal
		}
	}
	key, _, options, rest, err := ssh.ParseAuthorizedKey(c.HostKey)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, nil, ErrJournal
	}
	signer, err := ssh.ParsePrivateKey(c.PrivateKey)
	if err != nil {
		return nil, nil, ErrJournal
	}
	return key, signer, nil
}

// RelaySSHBootstrap sends only a fixed bootstrap operation over a host-key
// pinned SSH session. The SSH principal must be restricted server-side to the
// matching forced command and sudoers rule. It neither uploads executables nor
// silently retries an operation after a lost response.
func RelaySSHBootstrap(ctx context.Context, sshConfig RelaySSHConfig, action string, cfg cloudrelay.RuntimeConfig) (string, error) {
	return relaySSHBootstrapAuthorized(ctx, sshConfig, action, cfg, nil)
}

func relaySSHBootstrapAuthorized(ctx context.Context, sshConfig RelaySSHConfig, action string, cfg cloudrelay.RuntimeConfig, authorize func(context.Context) error) (string, error) {
	key, signer, err := validateRelaySSHConfig(sshConfig)
	if err != nil {
		return "", ErrJournal
	}
	host := sshConfig.Host
	if action != "publish" && action != "activate" && action != "inspect" {
		return "", ErrJournal
	}
	config, err := json.Marshal(cfg)
	if err != nil || len(config) > 64<<10 {
		return "", ErrJournal
	}
	defer clear(config)
	request, err := json.Marshal(struct {
		Schema string          `json:"schema"`
		Action string          `json:"action"`
		Config json.RawMessage `json:"config"`
	}{"anas.mail-systemd-bootstrap/v1", action, config})
	if err != nil || len(request) > 96<<10 {
		return "", ErrJournal
	}
	defer clear(request)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	address := net.JoinHostPort(host, strconv.Itoa(sshConfig.Port))
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", ErrUnknown
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()
	deadline := time.Now().Add(20 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if conn.SetDeadline(deadline) != nil {
		return "", ErrUnknown
	}
	clientConn, channels, requests, err := ssh.NewClientConn(conn, address, &ssh.ClientConfig{User: sshConfig.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.FixedHostKey(key), Timeout: 20 * time.Second})
	if err != nil {
		return "", ErrUnknown
	}
	if deadline, ok := ctx.Deadline(); ok {
		if conn.SetDeadline(deadline) != nil {
			clientConn.Close()
			return "", ErrUnknown
		}
	} else if conn.SetDeadline(time.Time{}) != nil {
		clientConn.Close()
		return "", ErrUnknown
	}
	client := ssh.NewClient(clientConn, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", ErrUnknown
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(request)
	var output boundedSSHOutput
	session.Stdout = &output
	session.Stderr = io.Discard
	// Recheck after network authentication and immediately before the remote
	// exec request, so revocation during SSH setup cannot issue a new action.
	if authorize != nil && authorize(ctx) != nil {
		return "", ErrUnknown
	}
	runErr := session.Run(relayBootstrapCommand)
	if output.overflow {
		return "", ErrUnknown
	}
	state, err := decodeBootstrapReply(output.data)
	if err != nil {
		return "", ErrUnknown
	}
	if runErr != nil {
		var exit *ssh.ExitError
		if errors.As(runErr, &exit) && exit.ExitStatus() == 1 && state == "conflict" {
			return "", ErrConflict
		}
		return "", ErrUnknown
	}
	if action == "publish" && state == "published" || action == "activate" && state == "active-local-verified" || action == "inspect" && (state == "inactive" || state == "active-local-verified") {
		return state, nil
	}
	return "", ErrUnknown
}

type boundedSSHOutput struct {
	data     []byte
	overflow bool
}

func (b *boundedSSHOutput) Write(p []byte) (int, error) {
	if len(p) > 512-len(b.data) {
		b.overflow = true
		return len(p), nil
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func decodeBootstrapReply(raw []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return "", ErrUnknown
	}
	token, err = d.Token()
	if err != nil || token != "state" {
		return "", ErrUnknown
	}
	var state string
	if d.Decode(&state) != nil {
		return "", ErrUnknown
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') {
		return "", ErrUnknown
	}
	var extra any
	if d.Decode(&extra) != io.EOF || strings.TrimSpace(state) != state {
		return "", ErrUnknown
	}
	return state, nil
}
