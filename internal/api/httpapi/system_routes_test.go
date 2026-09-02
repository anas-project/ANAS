package httpapi

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var testInternalCAPEM = []byte("-----BEGIN CERTIFICATE-----\nYW5hcy10ZXN0LWNh\n-----END CERTIFICATE-----\n")

func TestSystemReportsCurrentCertificateIssuer(t *testing.T) {
	handler := newSystemRouteTestHandler(t, StateEnrollment, SystemOptions{
		CurrentCertificate: func(context.Context) (CertificateMaterial, error) {
			return CertificateMaterial{Issuer: CertificateIssuerInternal, InternalCAPEM: testInternalCAPEM}, nil
		},
		DirectRecoveryURLs: []string{"https://nas.example:8080"},
		ProxyURL:           "https://anas.example",
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://nas.example/api/v1/system", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("system = %d, %s", response.Code, response.Body.String())
	}
	var body systemResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CertificateIssuer != CertificateIssuerInternal {
		t.Fatalf("certificate issuer = %q", body.CertificateIssuer)
	}
	if body.ConsoleState != StateEnrollment {
		t.Fatalf("console state = %q", body.ConsoleState)
	}
	if len(body.DirectRecoveryURLs) != 1 || body.DirectRecoveryURLs[0] != "https://nas.example:8080" || body.ProxyURL == nil || *body.ProxyURL != "https://anas.example" {
		t.Fatalf("system access origins = %#v, %#v", body.DirectRecoveryURLs, body.ProxyURL)
	}
}

func TestSystemRejectsNonCanonicalOrDuplicateAccessOrigins(t *testing.T) {
	tests := []SystemOptions{
		{DirectRecoveryURLs: []string{"HTTPS://nas.example:8080"}},
		{DirectRecoveryURLs: []string{"http://nas.example:8080", "http://nas.example:8080"}},
		{ProxyURL: "http://anas.example"},
	}
	for _, options := range tests {
		if _, err := newSystemHTTPState(options); err == nil {
			t.Fatalf("accepted invalid system options: %#v", options)
		}
	}
}

func TestInternalCADownloadUsesEnrollmentAndFullTransportBoundaries(t *testing.T) {
	options := SystemOptions{CurrentCertificate: func(context.Context) (CertificateMaterial, error) {
		return CertificateMaterial{Issuer: CertificateIssuerACME, InternalCAPEM: testInternalCAPEM}, nil
	}}
	tests := []struct {
		name  string
		state ConsoleState
		tls   bool
		want  int
	}{
		{name: "bootstrap hidden", state: StateBootstrap, want: http.StatusNotFound},
		{name: "enrollment plaintext", state: StateEnrollment, want: http.StatusOK},
		{name: "enrollment TLS", state: StateEnrollment, tls: true, want: http.StatusOK},
		{name: "full plaintext hidden", state: StateFull, want: http.StatusNotFound},
		{name: "full TLS", state: StateFull, tls: true, want: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newSystemRouteTestHandler(t, test.state, options)
			request := httptest.NewRequest(http.MethodGet, "http://nas.example/api/v1/system/ca", nil)
			if test.tls {
				request.URL.Scheme = "https"
				request.TLS = &tls.ConnectionState{}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("CA = %d, %s", response.Code, response.Body.String())
			}
			if test.want == http.StatusOK {
				if response.Body.String() != string(testInternalCAPEM) || response.Header().Get("Content-Type") != "application/pem-certificate-chain" ||
					response.Header().Get("Content-Disposition") != `attachment; filename="anas-internal-ca.crt"` {
					t.Fatalf("CA headers/body = %q, %q, %q", response.Header().Get("Content-Type"), response.Header().Get("Content-Disposition"), response.Body.String())
				}
			}
		})
	}
}

func TestInternalCADownloadIsHiddenWithoutValidatedMaterial(t *testing.T) {
	handler := newSystemRouteTestHandler(t, StateEnrollment, SystemOptions{
		CurrentCertificate: func(context.Context) (CertificateMaterial, error) {
			return CertificateMaterial{Issuer: CertificateIssuerTemporary}, nil
		},
	})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://nas.example/api/v1/system/ca", nil))
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), "certificate") {
		t.Fatalf("missing CA = %d, %s", response.Code, response.Body.String())
	}
}

func TestPlaintextRedirectUsesOnlyConfiguredHTTPSOrigin(t *testing.T) {
	options := SystemOptions{CanonicalHTTPSOrigin: "https://anas.example.test:8080"}
	for _, state := range []ConsoleState{StateEnrollment, StateFull} {
		handler := newSystemRouteTestHandler(t, state, options)
		request := httptest.NewRequest(http.MethodGet, "http://attacker.invalid/", nil)
		request.Host = "attacker.invalid"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusPermanentRedirect || response.Header().Get("Location") != "https://anas.example.test:8080/" || response.Body.Len() != 0 {
			t.Fatalf("%s redirect = %d, location %q, body %q", state, response.Code, response.Header().Get("Location"), response.Body.String())
		}
	}
}

func TestConsoleRootServesAllowedStatesAndRejectsUnsafeRedirectInputs(t *testing.T) {
	options := SystemOptions{CanonicalHTTPSOrigin: "https://anas.example.test:8080"}
	tests := []struct {
		name       string
		state      ConsoleState
		path       string
		body       string
		tls        bool
		apply      func(*http.Request)
		wantStatus int
	}{
		{name: "bootstrap plaintext UI", state: StateBootstrap, path: "/", wantStatus: http.StatusOK},
		{name: "enrollment TLS UI", state: StateEnrollment, path: "/", tls: true, wantStatus: http.StatusOK},
		{name: "query", state: StateEnrollment, path: "/?next=https://attacker.invalid", wantStatus: http.StatusBadRequest},
		{name: "body", state: StateFull, path: "/", body: "secret", wantStatus: http.StatusNotFound},
		{name: "cookie", state: StateFull, path: "/", apply: func(r *http.Request) { r.Header.Add("Cookie", "session=secret") }, wantStatus: http.StatusNotFound},
		{name: "authorization", state: StateFull, path: "/", apply: func(r *http.Request) { r.Header.Add("Authorization", "Bearer secret") }, wantStatus: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := newSystemRouteTestHandler(t, test.state, options)
			request := httptest.NewRequest(http.MethodGet, "http://nas.example"+test.path, strings.NewReader(test.body))
			if test.body == "" {
				request.Body = nil
				request.ContentLength = 0
			}
			if test.tls {
				request.URL.Scheme = "https"
				request.TLS = &tls.ConnectionState{}
			}
			if test.apply != nil {
				test.apply(request)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || response.Header().Get("Location") != "" {
				t.Fatalf("console root = %d, want %d, location %q, body %s", response.Code, test.wantStatus, response.Header().Get("Location"), response.Body.String())
			}
		})
	}
}

func newSystemRouteTestHandler(t *testing.T, state ConsoleState, options SystemOptions) http.Handler {
	t.Helper()
	systemState, err := newSystemHTTPState(options)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandler(nil, func(string) QueryService { return &fakeQueryService{} }, SecurityOptions{
		InitialState: state,
		HostAllowed:  func(*http.Request) bool { return true },
		Listener:     ListenerDirect,
		Authorize: func(*http.Request, AuthorizationRequest) (Principal, error) {
			return Principal{ID: "test", Role: "owner", Source: "local", TransactionID: "txn-test"}, nil
		},
	}, nil, nil, nil, nil, nil, nil, systemState)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}
