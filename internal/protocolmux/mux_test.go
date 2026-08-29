package protocolmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestDispatcherClassifiesAndReplaysPrefixes(t *testing.T) {
	dispatcher, connect, stop := startDispatcher(t, Options{})
	defer stop()

	plainClient := connect(t)
	defer plainClient.Close()
	plainRequest := []byte("GET /healthz HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n")
	plainWritten := writeAsync(plainClient, plainRequest)
	plainServer := accept(t, dispatcher.Plaintext())
	defer plainServer.Close()
	assertRead(t, plainServer, plainRequest)
	assertWrite(t, plainWritten)

	tlsClient := connect(t)
	defer tlsClient.Close()
	tlsRecord := []byte{0x16, 0x03, 0x03, 0x00, 0x02, 0x01, 0x00}
	tlsWritten := writeAsync(tlsClient, tlsRecord)
	tlsServer := accept(t, dispatcher.TLS())
	defer tlsServer.Close()
	assertRead(t, tlsServer, tlsRecord)
	assertWrite(t, tlsWritten)
}

func TestDispatcherRejectsUnknownAndSlowPrefixes(t *testing.T) {
	rejected := make(chan error, 2)
	_, connect, stop := startDispatcher(t, Options{
		FirstByteTimeout: 40 * time.Millisecond,
		OnReject:         func(err error) { rejected <- err },
	})
	defer stop()

	unknown := connect(t)
	_, _ = unknown.Write([]byte("S"))
	assertEventuallyClosed(t, unknown)

	slow := connect(t)
	if _, err := slow.Write([]byte("G")); err != nil {
		t.Fatal(err)
	}
	assertEventuallyClosed(t, slow)

	for range 2 {
		select {
		case <-rejected:
		case <-time.After(time.Second):
			t.Fatal("missing rejection callback")
		}
	}
}

func TestDispatcherConnectionLimitIsHeldUntilHandoffCloses(t *testing.T) {
	dispatcher, connect, stop := startDispatcher(t, Options{MaxConnections: 1})
	defer stop()

	firstClient := connect(t)
	defer firstClient.Close()
	if _, err := firstClient.Write([]byte("GET ")); err != nil {
		t.Fatal(err)
	}
	firstServer := accept(t, dispatcher.Plaintext())

	secondClient := connect(t)
	_, _ = secondClient.Write([]byte("GET "))
	assertEventuallyClosed(t, secondClient)

	if err := firstServer.Close(); err != nil {
		t.Fatal(err)
	}
	thirdClient := connect(t)
	defer thirdClient.Close()
	if _, err := thirdClient.Write([]byte("GET ")); err != nil {
		t.Fatal(err)
	}
	thirdServer := accept(t, dispatcher.Plaintext())
	_ = thirdServer.Close()
}

func TestConnectionLimitCanBeSharedAcrossListeners(t *testing.T) {
	limiter, err := NewConnectionLimiter(1)
	if err != nil {
		t.Fatal(err)
	}
	first, firstConnect, stopFirst := startDispatcher(t, Options{Limiter: limiter})
	defer stopFirst()
	_, secondConnect, stopSecond := startDispatcher(t, Options{Limiter: limiter})
	defer stopSecond()

	firstClient := firstConnect(t)
	defer firstClient.Close()
	if _, err := firstClient.Write([]byte("GET ")); err != nil {
		t.Fatal(err)
	}
	firstServer := accept(t, first.Plaintext())

	secondClient := secondConnect(t)
	_, _ = secondClient.Write([]byte("GET "))
	assertEventuallyClosed(t, secondClient)
	_ = firstServer.Close()
}

func TestDispatcherCloseRacesAreIdempotent(t *testing.T) {
	dispatcher, _, stop := startDispatcher(t, Options{})
	var wait sync.WaitGroup
	for range 20 {
		wait.Add(3)
		go func() { defer wait.Done(); _ = dispatcher.Close() }()
		go func() { defer wait.Done(); _ = dispatcher.Plaintext().Close() }()
		go func() { defer wait.Done(); _ = dispatcher.TLS().Close() }()
	}
	wait.Wait()
	stop()
	if _, err := dispatcher.Plaintext().Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("plaintext Accept error = %v", err)
	}
}

func TestDispatcherCloseTerminatesPendingClassifier(t *testing.T) {
	dispatcher, connect, stop := startDispatcher(t, Options{FirstByteTimeout: time.Hour})
	client := connect(t)
	if _, err := client.Write([]byte("G")); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Close(); err != nil {
		t.Fatal(err)
	}
	assertEventuallyClosed(t, client)
	stop()
}

func TestDispatcherCloseReturnsTheSameRootErrorToEveryCaller(t *testing.T) {
	closeErr := errors.New("root close failed")
	dispatcher, err := New(closeErrorListener{err: closeErr}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := dispatcher.Close(); !errors.Is(err, closeErr) {
			t.Fatalf("Close error = %v, want %v", err, closeErr)
		}
	}
}

func startDispatcher(t *testing.T, options Options) (*Dispatcher, func(*testing.T) net.Conn, func()) {
	t.Helper()
	root := newPipeListener()
	dispatcher, err := New(root, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- dispatcher.Serve(ctx) }()
	var once sync.Once
	return dispatcher, func(t *testing.T) net.Conn {
			t.Helper()
			return root.connect(t)
		}, func() {
			once.Do(func() {
				cancel()
				_ = dispatcher.Close()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("Serve: %v", err)
					}
				case <-time.After(time.Second):
					t.Error("dispatcher did not stop")
				}
			})
		}
}

func accept(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	result := make(chan struct {
		connection net.Conn
		err        error
	}, 1)
	go func() {
		connection, err := listener.Accept()
		result <- struct {
			connection net.Conn
			err        error
		}{connection: connection, err: err}
	}()
	select {
	case value := <-result:
		if value.err != nil {
			t.Fatal(value.err)
		}
		return value.connection
	case <-time.After(time.Second):
		t.Fatal("Accept timed out")
		return nil
	}
}

type pipeListener struct {
	connections chan net.Conn
	done        chan struct{}
	closeOnce   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{connections: make(chan net.Conn), done: make(chan struct{})}
}

func (listener *pipeListener) connect(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	select {
	case listener.connections <- server:
		return client
	case <-listener.done:
		_ = client.Close()
		_ = server.Close()
		t.Fatal("pipe listener is closed")
		return nil
	case <-time.After(time.Second):
		_ = client.Close()
		_ = server.Close()
		t.Fatal("pipe listener did not accept connection")
		return nil
	}
}

func (listener *pipeListener) Accept() (net.Conn, error) {
	select {
	case connection := <-listener.connections:
		return connection, nil
	case <-listener.done:
		return nil, net.ErrClosed
	}
}

func (listener *pipeListener) Close() error {
	listener.closeOnce.Do(func() { close(listener.done) })
	return nil
}

func (*pipeListener) Addr() net.Addr { return pipeAddress("pipe") }

type pipeAddress string

func (address pipeAddress) Network() string { return "pipe" }
func (address pipeAddress) String() string  { return string(address) }

type closeErrorListener struct{ err error }

func (closeErrorListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener closeErrorListener) Close() error     { return listener.err }
func (closeErrorListener) Addr() net.Addr            { return pipeAddress("close-error") }

func assertRead(t *testing.T, connection net.Conn, want []byte) {
	t.Helper()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(connection, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("read %q, want %q", got, want)
	}
}

func writeAsync(connection net.Conn, value []byte) <-chan error {
	result := make(chan error, 1)
	go func() {
		_, err := connection.Write(value)
		result <- err
	}()
	return result
}

func assertWrite(t *testing.T, result <-chan error) {
	t.Helper()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Write timed out")
	}
}

func assertEventuallyClosed(t *testing.T, connection net.Conn) {
	t.Helper()
	defer connection.Close()
	if err := connection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return
	}
	var buffer [1]byte
	if _, err := connection.Read(buffer[:]); err == nil {
		t.Fatal("connection remained open")
	}
}
