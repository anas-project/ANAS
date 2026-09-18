package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// These are local transport fixtures, not Docker/Incus isolation acceptance.
func relayTCPPair(t *testing.T) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := net.DialTCP("tcp4", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	server, err := listener.AcceptTCP()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	for _, connection := range []*net.TCPConn{client, server} {
		if err := connection.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return client, server
}

func TestRelayPreservesBytesAndHalfClose(t *testing.T) {
	client, downstream := relayTCPPair(t)
	upstream, server := relayTCPPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- relayTCP(ctx, downstream, upstream, time.Second) }()
	request := []byte{0x16, 0x03, 0x03, 0, 8, 0, 0xff, '\n', '\r', 0x80, 0x01, 0, 0x02}
	response := []byte{0x17, 0x03, 0x03, 0, 3, 0xfe, 0, 0xff}
	if _, err := client.Write(request); err != nil {
		t.Fatal(err)
	}
	if err := client.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	seen, err := io.ReadAll(server)
	if err != nil || !bytes.Equal(seen, request) {
		t.Fatal("request was not forwarded verbatim through half-close")
	}
	if _, err := server.Write(response); err != nil {
		t.Fatal(err)
	}
	if err := server.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	seen, err = io.ReadAll(client)
	if err != nil || !bytes.Equal(seen, response) {
		t.Fatal("response was not forwarded verbatim after request EOF")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("copy loops did not stop")
	}
}

func TestRelayCancellationClosesBothDirections(t *testing.T) {
	client, downstream := relayTCPPair(t)
	upstream, server := relayTCPPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- relayTCP(ctx, downstream, upstream, time.Second) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation was not observed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation left copy loops running")
	}
	for _, peer := range []*net.TCPConn{client, server} {
		var one [1]byte
		if _, err := peer.Read(one[:]); !errors.Is(err, io.EOF) {
			t.Fatal("peer was not closed on cancellation")
		}
	}
}

func TestRelayIdleTimeoutClosesConnection(t *testing.T) {
	_, downstream := relayTCPPair(t)
	upstream, _ := relayTCPPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := relayTCP(ctx, downstream, upstream, 100*time.Millisecond)
	var networkError net.Error
	if !errors.As(err, &networkError) || !networkError.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("idle connection did not stop at its transport deadline")
	}
}
