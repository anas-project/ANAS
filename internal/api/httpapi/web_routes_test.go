package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConsoleBundlesAreServedUnderExplicitStateAndTransportPolicy(t *testing.T) {
	tests := []struct {
		name         string
		state        ConsoleState
		path         string
		tls          bool
		wantStatus   int
		wantType     string
		wantBody     string
		wantLocation string
	}{
		{name: "bootstrap plaintext root", state: StateBootstrap, path: "/", wantStatus: http.StatusOK, wantType: "text/html", wantBody: "data-lan-risk-banner"},
		{name: "enrollment plaintext root redirects", state: StateEnrollment, path: "/", wantStatus: http.StatusPermanentRedirect, wantLocation: "https://anas.example:8443/"},
		{name: "full TLS root", state: StateFull, path: "/", tls: true, wantStatus: http.StatusOK, wantType: "text/html", wantBody: "ANAS Console"},
		{name: "bootstrap main script", state: StateBootstrap, path: consoleMainScriptPath, wantStatus: http.StatusOK, wantType: "text/javascript", wantBody: "ANAS"},
		{name: "full plaintext main script hidden", state: StateFull, path: consoleMainScriptPath, wantStatus: http.StatusNotFound},
		{name: "full TLS recovery package", state: StateFull, path: consoleRecoveryPath, tls: true, wantStatus: http.StatusOK, wantType: "text/html", wantBody: "data-emergency-ui"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			systemState, err := newSystemHTTPState(SystemOptions{CanonicalHTTPSOrigin: "https://anas.example:8443"})
			if err != nil {
				t.Fatal(err)
			}
			handler, err := newHandler(nil, nil, SecurityOptions{
				InitialState: test.state,
				HostAllowed:  func(*http.Request) bool { return true },
			}, nil, nil, nil, nil, nil, nil, systemState)
			if err != nil {
				t.Fatal(err)
			}

			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			if test.tls {
				request.TLS = &tls.ConnectionState{}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			if test.wantType != "" && !strings.HasPrefix(response.Header().Get("Content-Type"), test.wantType) {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			if test.wantBody != "" && !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("body omitted %q", test.wantBody)
			}
			if location := response.Header().Get("Location"); location != test.wantLocation {
				t.Fatalf("Location = %q, want %q", location, test.wantLocation)
			}
		})
	}
}

func TestConsoleAssetRoutesRejectQueryAndUnsupportedMethod(t *testing.T) {
	handler, err := NewHandlerWithSecurity(nil, nil, SecurityOptions{
		InitialState: StateBootstrap,
		HostAllowed:  func(*http.Request) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, consoleMainStylesPath+"?version=1", nil),
		httptest.NewRequest(http.MethodPost, consoleRecoveryPath, nil),
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code < 400 {
			t.Fatalf("%s %s status = %d", request.Method, request.URL, response.Code)
		}
	}
}
