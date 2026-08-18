package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseOptionsDefaultsAndRepeatedWorkspaces(t *testing.T) {
	opts, err := parseOptions([]string{"--workspace", "main=/srv/anas", "--workspace=lab=/opt/lab"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.listen != defaultListenAddress {
		t.Fatalf("listen = %q", opts.listen)
	}
	if len(opts.workspaces) != 2 || opts.workspaces[0].ID != "main" || opts.workspaces[1].Path != "/opt/lab" {
		t.Fatalf("workspaces = %#v", opts.workspaces)
	}
}

func TestParseOptionsRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"positional"},
		{"--workspace", "main"},
		{"--workspace", "main="},
		{"--unknown"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Errorf("parseOptions(%q) succeeded", args)
		}
	}
}

func TestValidateLoopbackAddress(t *testing.T) {
	for _, address := range []string{"127.0.0.1:8080", "127.1.2.3:0", "[::1]:8080", "[::1%lo0]:8080"} {
		if err := validateLoopbackAddress(address); err != nil {
			t.Errorf("%s: %v", address, err)
		}
	}
	for _, address := range []string{"", ":8080", "0.0.0.0:8080", "192.0.2.1:8080", "[::]:8080", "example.com:8080", "localhost:8080", "127.0.0.1", "127.0.0.1:http", "127.0.0.1:65536"} {
		if err := validateLoopbackAddress(address); err == nil {
			t.Errorf("%s was accepted", address)
		} else if !strings.Contains(err.Error(), "loopback") && !strings.Contains(err.Error(), "numeric port") {
			t.Errorf("%s: unexpected error %v", address, err)
		}
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	got := []time.Duration{server.ReadHeaderTimeout, server.ReadTimeout, server.WriteTimeout, server.IdleTimeout}
	want := []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second, 60 * time.Second}
	if !reflect.DeepEqual(got, want) || server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("server limits = %v, %d", got, server.MaxHeaderBytes)
	}
}

func TestServeShutsDownWhenContextIsCanceled(t *testing.T) {
	listener := newBlockingListener()
	defer listener.Close()
	server := newHTTPServer(listener.Addr().String(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, server, listener) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}

type blockingListener struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingListener() *blockingListener {
	return &blockingListener{closed: make(chan struct{})}
}

func (listener *blockingListener) Accept() (net.Conn, error) {
	<-listener.closed
	return nil, errors.New("listener closed")
}

func (listener *blockingListener) Close() error {
	listener.once.Do(func() { close(listener.closed) })
	return nil
}

func (*blockingListener) Addr() net.Addr { return testAddr("127.0.0.1:0") }

type testAddr string

func (address testAddr) Network() string { return "tcp" }
func (address testAddr) String() string  { return string(address) }
