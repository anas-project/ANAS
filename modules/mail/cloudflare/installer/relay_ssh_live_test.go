// TEST_CASES: MAILMOD-T-074
package installer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// The explicit fixture is an isolated real sshd whose forced command invokes a
// root-owned test copy of cloudrelay. Inspecting an absent installation must
// return a conflict, without publishing anything on the host.
func TestRelaySSHRealRestrictedInspect(t *testing.T) {
	portValue := os.Getenv("ANAS_TEST_VPS_SSH_PORT")
	if portValue == "" {
		t.Skip("explicit isolated SSH fixture required")
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatal(err)
	}
	hostKey, err := os.ReadFile(os.Getenv("ANAS_TEST_VPS_SSH_HOSTKEY"))
	if err != nil {
		t.Fatal(err)
	}
	privateKey, err := os.ReadFile(os.Getenv("ANAS_TEST_VPS_SSH_KEY"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(privateKey)
	input := provisionFixture(t)
	input.Template.SourceID = "11111111-1111-4111-8111-111111111111"
	input.Template.Generation = "22222222-2222-4222-8222-222222222222"
	input.Template.Reader = strings.Repeat("a", 64)
	input.Template.Control = strings.Repeat("b", 64)
	input.Template.NotificationSecret = strings.Repeat("c", 64)
	user := os.Getenv("ANAS_TEST_VPS_SSH_USER")
	if user == "" {
		user = "ubuntu"
	}
	config := RelaySSHConfig{Host: "127.0.0.1", Port: port, User: user, HostKey: hostKey, PrivateKey: privateKey}
	// Check the actual protocol response first: an SSH or sudo failure must not
	// be mistaken for the expected unknown installation state.
	pinned, _, _, _, err := ssh.ParseAuthorizedKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.ParsePrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ssh.Dial("tcp", net.JoinHostPort(config.Host, portValue), &ssh.ClientConfig{User: config.User, HostKeyCallback: ssh.FixedHostKey(pinned), Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}})
	if err != nil {
		t.Fatalf("real SSH connection failed: %v", err)
	}
	session, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	payload, err := json.Marshal(struct {
		Schema string `json:"schema"`
		Action string `json:"action"`
		Config any    `json:"config"`
	}{"anas.mail-systemd-bootstrap/v1", "inspect", input.Template})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload)
	session.Stdin = bytes.NewReader(payload)
	var output, stderr bytes.Buffer
	session.Stdout = &output
	session.Stderr = &stderr
	runErr := session.Run(relayBootstrapCommand)
	session.Close()
	client.Close()
	var exit *ssh.ExitError
	if !errors.As(runErr, &exit) || exit.ExitStatus() != 1 || output.String() != `{"state":"conflict"}`+"\n" {
		t.Fatalf("real helper returned an unexpected inspect result: exit=%v output=%q stderr=%q", runErr, output.String(), stderr.String())
	}
	if _, err = RelaySSHBootstrap(context.Background(), config, "inspect", input.Template); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict for absent installation: %v", err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err = ssh.NewSignerFromKey(other)
	if err != nil {
		t.Fatal(err)
	}
	config.HostKey = ssh.MarshalAuthorizedKey(signer.PublicKey())
	if _, err = RelaySSHBootstrap(context.Background(), config, "inspect", input.Template); !errors.Is(err, ErrUnknown) {
		t.Fatalf("untrusted host was not rejected: %v", err)
	}
}
