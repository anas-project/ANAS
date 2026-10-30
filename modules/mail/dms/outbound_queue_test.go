// TEST_CASES: MAILMOD-T-017 MAILMOD-T-019
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

func TestOutboundQueueObservationIsBoundedAndNeverClaimsEmpty(t *testing.T) {
	for _, tc := range []struct {
		raw              string
		readErr, waitErr error
		want             string
	}{
		{"", nil, nil, "none-observed"},
		{`{"queue_name":"deferred","queue_id":"ABC123","sender":"private@example.test","recipients":[{"address":"private@example.test"}]}` + "\n", nil, nil, "queued-observed"},
		{`{"queue_name":"active","queue_id":"ABC123"}`, nil, nil, "unknown"},
		{`{"queue_name":"deferred","queue_id":"ABC123"}` + "\n" + `{"queue_name":"broken"}` + "\n", nil, nil, "unknown"},
		{`{"queue_name":"deferred","queue_id":"ABC123"}` + "\n", nil, errors.New("command failed"), "unknown"},
		{"", errors.New("read failed"), nil, "unknown"},
		{strings.Repeat("x", (2<<20)+1), nil, nil, "unknown"},
	} {
		if got := classifyOutboundQueue([]byte(tc.raw), tc.readErr, tc.waitErr); got != tc.want {
			t.Fatalf("queue observation = %s, expected %s", got, tc.want)
		}
	}
}

func TestOutboundQueuePrivateServiceAndStrictClient(t *testing.T) {
	token := strings.Repeat("a", 64)
	h := Handler{Adapter: &Adapter{}, Token: token, Host: "mail.example.test:9443"}
	for _, permitted := range []bool{false, true} {
		r := httptest.NewRequest("GET", "https://mail.example.test:9443/v1/outbound/queue", nil)
		r.TLS = &tls.ConnectionState{}
		if permitted {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if permitted && w.Code != 200 || !permitted && w.Code != 401 {
			t.Fatal("unexpected authorization", w.Code)
		}
		if strings.Contains(w.Body.String(), "queue_id") || strings.Contains(w.Body.String(), "sender") {
			t.Fatal("queue details leaked")
		}
	}
	client, err := NewClient(ClientConfig{URL: "https://mail.example.test:9443", ServiceToken: token}, &tls.Config{RootCAs: x509.NewCertPool()})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		code int
		want string
		ok   bool
	}{
		{`{"status":"queued-observed"}`, 200, "queued-observed", true},
		{`{"status":"none-observed"}`, 200, "none-observed", true},
		{`{"status":"empty"}`, 200, "unknown", false},
		{`{"status":"none-observed","status":"queued-observed"}`, 200, "unknown", false},
		{`{"Status":"none-observed"}`, 200, "unknown", false},
		{`{"status":"queued-observed","sender":"private"}`, 200, "unknown", false},
		{`{"status":"queued-observed"}` + strings.Repeat(" ", 4097), 200, "unknown", false},
		{`{"status":"queued-observed"}`, 503, "unknown", false},
	} {
		client.http.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/outbound/queue" || r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+token {
				t.Fatal("wrong request")
			}
			return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header), Request: r}, nil
		})
		got, err := client.OutboundQueue(context.Background())
		if got != tc.want || (err == nil) != tc.ok {
			t.Fatal("bad response handling", got, err)
		}
	}
}
