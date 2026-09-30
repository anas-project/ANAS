//go:build linux

package hostaction

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/actionabi"
)

func testUnixPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	// A short socket pathname avoids the AF_UNIX path limit under go test.
	dir, err := os.MkdirTemp("", "anas-peer-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	addr := &net.UnixAddr{Name: filepath.Join(dir, "socket"), Net: "unix"}
	l, err := net.ListenUnix("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	client, err := net.DialUnix("unix", nil, addr)
	if err != nil {
		t.Fatal(err)
	}
	server, err := l.AcceptUnix()
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close(); server.Close() })
	return client, server
}

func TestReceiveUsesKernelCredentialsAndStrictEOF(t *testing.T) {
	withTestPeerVerifier(t)
	policy := installedPolicy().peers()
	for _, tc := range []struct {
		name       string
		wire       string
		closeWrite bool
		wantError  bool
	}{
		{"valid", "", true, false},
		{"extra frame", "{}\n", true, true},
		{"no EOF", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, server := testUnixPair(t)
			body, err := actionabi.EncodeRequest(testRequest())
			if err != nil {
				t.Fatal(err)
			}
			body = append(body, tc.wire...)
			if _, err := client.Write(body); err != nil {
				t.Fatal(err)
			}
			if tc.closeWrite {
				if err := client.CloseWrite(); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			call, err := Receive(ctx, server, policy)
			if tc.wantError {
				if err == nil {
					t.Fatal("malformed/incomplete request accepted")
				}
				return
			}
			if err != nil || call.peer.uid != uint32(os.Geteuid()) || call.peer.gid != uint32(os.Getegid()) || call.peer.pid != int32(os.Getpid()) {
				t.Fatal(call, err)
			}
		})
	}
}

func TestReceiveDeniesUnauthorizedPeerWithoutReadingPayload(t *testing.T) {
	_, server := testUnixPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Receive(ctx, server, PeerPolicy{})
	if !errors.Is(err, ErrDenied) {
		t.Fatal("peer was not rejected before request read", err)
	}
}
