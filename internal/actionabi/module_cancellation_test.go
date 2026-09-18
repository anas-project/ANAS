package actionabi

import (
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

type cancellationFixtureReader struct {
	read func([]byte) (int, error)
}

func (reader cancellationFixtureReader) Read(buffer []byte) (int, error) {
	return reader.read(buffer)
}

func TestModuleCancellationRequiresDedicatedPipeEOF(t *testing.T) {
	if err := ReadModuleCancellation(strings.NewReader("")); err != nil {
		t.Fatalf("pipe EOF rejected: %v", err)
	}
	for name, reader := range map[string]io.Reader{
		"nil":   nil,
		"byte":  strings.NewReader("cancel"),
		"error": cancellationFixtureReader{read: func([]byte) (int, error) { return 0, errors.New("private-read-error") }},
		"byte and EOF": cancellationFixtureReader{read: func(buffer []byte) (int, error) {
			buffer[0] = 'x'
			return 1, io.EOF
		}},
		"no progress": cancellationFixtureReader{read: func([]byte) (int, error) { return 0, nil }},
	} {
		t.Run(name, func(t *testing.T) {
			err := ReadModuleCancellation(reader)
			if !errors.Is(err, ErrProtocol) || strings.Contains(err.Error(), "private-read-error") {
				t.Fatalf("expected stable protocol error, got %v", err)
			}
		})
	}
}

func TestModuleCancellationDoesNotCloseBeforePipeWriter(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	completed := make(chan error, 1)
	started := make(chan struct{})
	var once sync.Once
	observed := cancellationFixtureReader{read: func(buffer []byte) (int, error) {
		once.Do(func() { close(started) })
		return reader.Read(buffer)
	}}
	go func() { completed <- ReadModuleCancellation(observed) }()
	select {
	case <-started:
	case err := <-completed:
		t.Fatalf("returned before reading the cancellation pipe: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation reader did not start")
	}
	select {
	case err := <-completed:
		t.Fatalf("open pipe was treated as cancellation: %v", err)
	default:
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatalf("closed pipe did not request cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation reader did not finish after EOF")
	}
}
