// TEST_CASES: MAILMOD-T-019
package dms

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOutboundStatusDoesNotClaimDelivery(t *testing.T) {
	for _, tc := range []struct {
		output string
		err    error
		want   string
	}{
		{"retry:ANAS outbound SMTP is not configured\n\n\n\n", nil, "not-configured"},
		{"retry:ANAS outbound SMTP is not configured\ntexthash:/map\n\n\n", nil, "unverified"},
		{"anas_direct\n\n\n\n", nil, "unverified"},
		{"anas_starttls\n\n\n[relay]:587\n", nil, "unverified"},
		{"unexpected\n", nil, "unknown"},
		{"", errors.New("postconf unavailable"), "unknown"},
		{strings.Repeat("x", 4097), nil, "unknown"},
	} {
		if got := classifyOutboundStatus([]byte(tc.output), tc.err); got != tc.want {
			t.Fatalf("status %q, want %q", got, tc.want)
		}
	}
}

func TestOutboundStatusRequiresPrivateServiceAuthentication(t *testing.T) {
	token := strings.Repeat("a", 64)
	h := Handler{Adapter: &Adapter{}, Token: token, Host: "mail.example.test:9443"}
	for _, tc := range []struct {
		token string
		code  int
	}{{"", 401}, {token, 200}} {
		r := httptest.NewRequest("GET", "https://mail.example.test:9443/v1/outbound/status", nil)
		r.TLS = &tls.ConnectionState{}
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("authentication status %d, want %d", w.Code, tc.code)
		}
		if strings.Contains(w.Body.String(), "retry:") || strings.Contains(w.Body.String(), "mail.example.test") {
			t.Fatal("internal configuration leaked")
		}
	}
}

func TestOutboundStatusClientRejectsInvalidResponses(t *testing.T) {
	client, err := NewClient(ClientConfig{URL: "https://mail.example.test:9443", ServiceToken: strings.Repeat("a", 64)}, &tls.Config{RootCAs: x509.NewCertPool()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		code int
		want string
		ok   bool
	}{
		{`{"status":"not-configured"}`, 200, "not-configured", true},
		{`{"status":"unverified"}`, 200, "unverified", true},
		{`{"status":"verified"}`, 200, "unknown", false},
		{`{"status":"unverified","endpoint":"private"}`, 200, "unknown", false},
		{`{"status":"unverified"}{}`, 200, "unknown", false},
		{`{"status":"unverified"}` + strings.Repeat(" ", 4097), 200, "unknown", false},
		{`{"status":"unverified"}`, 503, "unknown", false},
	} {
		client.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/outbound/status" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 64) {
				t.Fatal("incorrect request")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: r}, nil
		})
		got, err := client.OutboundStatus(context.Background())
		if got != tc.want || (err == nil) != tc.ok {
			t.Fatalf("response status %q, error %v", got, err)
		}
	}
}
