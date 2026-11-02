//go:build linux

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func TestNativeQueueObservationPreservesTransportAndSecrets(t *testing.T) {
	var observed bytes.Buffer
	request, _ := http.NewRequest(http.MethodGet, "https://fixture.invalid/api/queue?private=query", strings.NewReader("private body"))
	request.SetBasicAuth("private-user", "private-password")
	want := &http.Response{StatusCode: http.StatusServiceUnavailable}
	sentinel := errors.New("transport failure")
	calls := 0
	upstream := controllerRoundTripFunc(func(got *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(got.Body)
		user, password, ok := got.BasicAuth()
		if got != request || string(body) != "private body" || !ok || user != "private-user" || password != "private-password" {
			t.Fatal("observer changed the actual request")
		}
		return want, sentinel
	})
	response, err := nativeQueueObserver(&observed, "/api/queue", upstream).RoundTrip(request)
	if response != want || err != sentinel || calls != 1 {
		t.Fatal("observer changed the transport outcome")
	}
	if timestamp, err := strconv.ParseInt(strings.TrimSpace(observed.String()), 10, 64); err != nil || timestamp <= 0 {
		t.Fatal("trace must contain only one timestamp, without URL or credentials")
	}
	observed.Reset()
	request, _ = http.NewRequest(http.MethodPost, "https://fixture.invalid/api/queue", strings.NewReader("private body"))
	request.SetBasicAuth("private-user", "private-password")
	nativeQueueObserver(&observed, "/api/queue", upstream).RoundTrip(request)
	if observed.Len() != 0 {
		t.Fatal("non-queue request was traced")
	}
}
