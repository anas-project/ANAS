package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFaultProxyBlocksOnlyMainJavaScript(t *testing.T) {
	parsed, err := validateUpstream("http://127.0.0.1:7793")
	if err != nil {
		t.Fatal(err)
	}
	handler := newFaultHandlerWithTransport(parsed, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Host != parsed.Host {
			t.Fatalf("proxy Host = %q, want %q", request.Host, parsed.Host)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("upstream:" + request.URL.Path)),
		}, nil
	}))

	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, blockedMainScript, nil))
	if blocked.Code != http.StatusServiceUnavailable || blocked.Header().Get("X-ANAS-Test-Fault") != "main-js-blocked" {
		t.Fatalf("blocked response = %d %#v", blocked.Code, blocked.Header())
	}
	for _, path := range []string{"/", "/emergency", "/assets/emergency.js", "/healthz"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		body, _ := io.ReadAll(response.Result().Body)
		if response.Code != http.StatusOK || string(body) != "upstream:"+path {
			t.Fatalf("%s response = %d %q", path, response.Code, body)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestFaultProxyRejectsUnexpectedSurface(t *testing.T) {
	parsed, _ := validateUpstream("http://127.0.0.1:7793")
	handler := newFaultHandlerWithTransport(parsed, roundTripFunc(func(request *http.Request) (*http.Response, error) {
		t.Fatalf("unexpected upstream request: %s %s", request.Method, request.URL)
		return nil, nil
	}))
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/emergency", nil),
		httptest.NewRequest(http.MethodGet, "/api/v1/system", nil),
		httptest.NewRequest(http.MethodGet, "/?query=blocked", nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d", request.Method, request.URL, response.Code)
		}
	}
}
