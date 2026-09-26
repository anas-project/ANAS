//go:build linux

package hostaction

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func systemdAuthPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	address := &net.UnixAddr{Name: filepath.Join(t.TempDir(), "auth.sock"), Net: "unix"}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialUnix("unix", nil, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return client, server
}

func TestSystemdAuthenticationWaitsForActualQueueDrain(t *testing.T) {
	client, server := systemdAuthPair(t)
	frame := []byte("BEGIN\r\n")
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- waitSystemdAuthenticationDrain(ctx, client) }()
	select {
	case err := <-done:
		t.Fatalf("binary protocol admitted before peer consumed auth bytes: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if _, err := io.ReadFull(server, make([]byte, len(frame))); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("drained authentication did not advance")
	}
}

func TestSystemdAuthenticationDrainKeepsDeadlineAndCancellation(t *testing.T) {
	client, _ := systemdAuthPair(t)
	if _, err := client.Write([]byte("BEGIN\r\n")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := waitSystemdAuthenticationDrain(ctx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled authentication escaped its deadline: %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := waitSystemdAuthenticationDrain(canceled, client); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled authentication advanced: %v", err)
	}
	if waitSystemdAuthenticationDrain(nil, client) == nil || waitSystemdAuthenticationDrain(context.Background(), nil) == nil {
		t.Fatal("missing authentication context/socket was admitted")
	}
	_ = client.Close()
	if waitSystemdAuthenticationDrain(context.Background(), client) == nil {
		t.Fatal("closed authentication transport was admitted")
	}
}
