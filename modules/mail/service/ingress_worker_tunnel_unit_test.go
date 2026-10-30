// TEST_CASES: MAILMOD-T-010
package main

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkerFixtureTunnelTransfersAndStops(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "anas-worker-tunnel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "worker.sock")
	unix, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := unix.Accept()
		if e == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stop := testWorkerTunnel(listener, path)
	defer stop()
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	want := []byte{0, 1, 2, 255}
	if _, err = client.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err = io.ReadFull(client, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("opaque worker tunnel changed data", err)
	}
	stop() // Must close an otherwise idle active client and both copy paths.
	client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture tunnel leaked a connection")
	}
}
