// TEST_CASES: MAILMOD-T-067
package installer

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"golang.org/x/crypto/ssh"
)

type sshFixture struct {
	config   RelaySSHConfig
	listener net.Listener
	done     chan struct{}
	calls    chan string
}

func newSSHFixture(t *testing.T, response string, exit uint32, closeEarly bool) *sshFixture {
	t.Helper()
	_, hostPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivate)
	if err != nil {
		t.Fatal(err)
	}
	_, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(clientPrivate)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(clientPrivate, "")
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, portString, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(portString)
	f := &sshFixture{config: RelaySSHConfig{Host: "127.0.0.1", Port: port, User: "relaydeploy", HostKey: ssh.MarshalAuthorizedKey(hostSigner.PublicKey()), PrivateKey: pem.EncodeToMemory(block)}, listener: listener, done: make(chan struct{}), calls: make(chan string, 1)}
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-f.done:
		case <-time.After(5 * time.Second):
			t.Error("SSH fixture did not stop")
		}
	})
	go func() {
		defer close(f.done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		conf := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if !bytes.Equal(key.Marshal(), clientSigner.PublicKey().Marshal()) {
				return nil, errors.New("unauthorized")
			}
			return nil, nil
		}}
		conf.AddHostKey(hostSigner)
		server, channels, requests, e := ssh.NewServerConn(conn, conf)
		if e != nil {
			return
		}
		defer server.Close()
		go ssh.DiscardRequests(requests)
		for ch := range channels {
			if ch.ChannelType() != "session" {
				ch.Reject(ssh.UnknownChannelType, "session only")
				continue
			}
			channel, reqs, e := ch.Accept()
			if e != nil {
				return
			}
			for req := range reqs {
				if req.Type != "exec" {
					req.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if ssh.Unmarshal(req.Payload, &payload) != nil || payload.Command != relayBootstrapCommand {
					req.Reply(false, nil)
					channel.Close()
					return
				}
				req.Reply(true, nil)
				raw, e := io.ReadAll(io.LimitReader(channel, 96<<10+1))
				if e != nil {
					channel.Close()
					return
				}
				f.calls <- string(raw)
				if !closeEarly {
					_, _ = channel.Write([]byte(response))
					_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{exit}))
				}
				channel.Close()
				return
			}
		}
	}()
	return f
}
func TestRelaySSHBootstrapPinnedHostAndFixedCommand(t *testing.T) {
	f := newSSHFixture(t, `{"state":"published"}`, 0, false)
	cfg := cloudrelay.RuntimeConfig{Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64), TLSPrivateKey: "synthetic-private-key"}
	state, err := RelaySSHBootstrap(context.Background(), f.config, "publish", cfg)
	if err != nil || state != "published" {
		t.Fatal(state, err)
	}
	select {
	case message := <-f.calls:
		var req struct {
			Schema, Action string
			Config         cloudrelay.RuntimeConfig
		}
		if json.Unmarshal([]byte(message), &req) != nil || req.Schema != "anas.mail-systemd-bootstrap/v1" || req.Action != "publish" || req.Config.Reader != cfg.Reader {
			t.Fatal("wrong request")
		}
	case <-time.After(time.Second):
		t.Fatal("request not delivered")
	}
	if strings.Contains(fmt.Sprintf("%+v", f.config), string(f.config.PrivateKey)) {
		t.Fatal("SSH credential printed")
	}
}
func TestRelaySSHBootstrapRejectsWrongHostAndAmbiguousResult(t *testing.T) {
	t.Run("host key", func(t *testing.T) {
		f := newSSHFixture(t, `{"state":"published"}`, 0, false)
		_, other, _ := ed25519.GenerateKey(rand.Reader)
		signer, _ := ssh.NewSignerFromKey(other)
		f.config.HostKey = ssh.MarshalAuthorizedKey(signer.PublicKey())
		if _, err := RelaySSHBootstrap(context.Background(), f.config, "publish", cloudrelay.RuntimeConfig{Reader: "private"}); !errors.Is(err, ErrUnknown) {
			t.Fatal(err)
		}
		select {
		case <-f.calls:
			t.Fatal("configuration sent to wrong host")
		default:
		}
	})
	for _, tc := range []struct {
		name, response string
		exit           uint32
		drop           bool
		want           error
	}{{"lost response", "", 0, true, ErrUnknown}, {"duplicate response", `{"state":"published","state":"published"}`, 0, false, ErrUnknown}, {"conflict", `{"state":"conflict"}`, 1, false, ErrConflict}, {"false success", `{"state":"active-local-verified"}`, 0, false, ErrUnknown}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSSHFixture(t, tc.response, tc.exit, tc.drop)
			if _, err := RelaySSHBootstrap(context.Background(), f.config, "publish", cloudrelay.RuntimeConfig{}); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			select {
			case <-f.calls:
			case <-time.After(time.Second):
				t.Fatal("request not received")
			}
		})
	}
}

func TestRelaySSHBootstrapReauthorizesBeforeExec(t *testing.T) {
	f := newSSHFixture(t, `{"state":"published"}`, 0, false)
	calls := 0
	_, err := relaySSHBootstrapAuthorized(context.Background(), f.config, "publish", cloudrelay.RuntimeConfig{Reader: "synthetic"}, func(context.Context) error { calls++; return errors.New("revoked") })
	if !errors.Is(err, ErrUnknown) || calls != 1 {
		t.Fatalf("revoked authorization: %v", err)
	}
	select {
	case <-f.calls:
		t.Fatal("sent configuration after revocation")
	default:
	}
}
