// TEST_CASES: MAILMOD-T-028, MAILMOD-T-042
package installer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCloudflareRechecksServiceGrantBeforeEveryRequest(t *testing.T) {
	allowed := true
	checks, requests := 0, 0
	c, err := NewCloudflareWithAuthorization("synthetic-token-value", func(context.Context) error {
		checks++
		if !allowed {
			return errors.New("revoked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return responseWith(200, `{"success":true,"result":{"id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","name":"anas-worker"}}`), nil
	})
	account := strings.Repeat("a", 32)
	if _, err := c.CreateWorker(t.Context(), account, "anas-worker"); err != nil {
		t.Fatal(err)
	}
	allowed = false
	if _, err := c.CreateWorker(t.Context(), account, "anas-worker"); !errors.Is(err, ErrUnknown) || checks != 2 || requests != 1 {
		t.Fatal("revoked service made a provider request", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func cloudFixture(t *testing.T, fn roundTripFunc) *Cloudflare {
	t.Helper()
	c, err := NewCloudflare("synthetic-token-value")
	if err != nil {
		t.Fatal(err)
	}
	c.http.Transport = fn
	return c
}

func responseWith(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestCloudflareReservationUsesFixedAuthenticatedEndpoints(t *testing.T) {
	account, workerID := strings.Repeat("a", 32), strings.Repeat("b", 32)
	const bucket = `{"name":"anas-bucket","creation_date":"2026-10-27T10:11:12Z","jurisdiction":"default","location":"apac","storage_class":"Standard"}`
	var methods []string
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.cloudflare.com" || r.Header.Get("Authorization") != "Bearer synthetic-token-value" {
			t.Fatal("request left fixed authenticated origin")
		}
		methods = append(methods, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/workers/workers"):
			return responseWith(200, `{"success":true,"result":{"id":"`+workerID+`","name":"anas-worker"}}`), nil
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/workers/workers/"+workerID):
			return responseWith(200, `{"success":true,"result":{"id":"`+workerID+`","name":"anas-worker"}}`), nil
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/r2/buckets"):
			return responseWith(200, `{"success":true,"result":`+bucket+`}`), nil
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/r2/buckets/anas-bucket"):
			return responseWith(200, `{"success":true,"result":`+bucket+`}`), nil
		default:
			t.Fatal("unexpected request", r.Method, r.URL.Path)
			return nil, nil
		}
	})
	id, err := c.CreateWorker(t.Context(), account, "anas-worker")
	if err != nil || id != workerID {
		t.Fatal("worker create", err)
	}
	if ok, err := c.WorkerMatches(t.Context(), account, id, "anas-worker"); err != nil || !ok {
		t.Fatal("worker readback", err)
	}
	fp, err := c.CreateBucket(t.Context(), account, "anas-bucket")
	if err != nil || !fingerprint.MatchString(fp) {
		t.Fatal("bucket create", err)
	}
	if ok, err := c.BucketMatches(t.Context(), account, "anas-bucket", fp); err != nil || !ok {
		t.Fatal("bucket readback", err)
	}
	if len(methods) != 4 {
		t.Fatal("unexpected requests", methods)
	}
}

func TestCloudflareConflictAndAmbiguityFailClosed(t *testing.T) {
	account := strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, response string
		status         int
		want           error
	}{
		{"worker duplicate", `{"success":false,"errors":[{"code":10040}]}`, 409, ErrConflict},
		{"different conflict", `{"success":false,"errors":[{"code":9999}]}`, 409, ErrUnknown},
		{"server error", `{"success":false,"errors":[{"code":10040}]}`, 500, ErrUnknown},
		{"malformed success", `{"success":true,"result":{"name":"anas-worker"}}`, 200, ErrUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := cloudFixture(t, func(*http.Request) (*http.Response, error) { return responseWith(tc.status, tc.response), nil })
			_, err := c.CreateWorker(t.Context(), account, "anas-worker")
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "synthetic-token") {
				t.Fatal("unexpected classification", err)
			}
		})
	}
	c := cloudFixture(t, func(*http.Request) (*http.Response, error) {
		return responseWith(409, `{"success":false,"errors":[{"code":10004}]}`), nil
	})
	if _, err := c.CreateBucket(t.Context(), account, "anas-bucket"); !errors.Is(err, ErrConflict) {
		t.Fatal("bucket conflict not detected", err)
	}
}

func TestCloudflareRejectsRedirectAndChangedBucket(t *testing.T) {
	account := strings.Repeat("a", 32)
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.cloudflare.com" {
			t.Fatal("credential followed foreign redirect")
		}
		response := responseWith(302, `{"success":false}`)
		response.Header.Set("Location", "https://foreign.example/")
		return response, nil
	})
	if _, err := c.CreateWorker(t.Context(), account, "anas-worker"); !errors.Is(err, ErrUnknown) {
		t.Fatal("redirect accepted", err)
	}
	c = cloudFixture(t, func(*http.Request) (*http.Response, error) {
		return responseWith(200, `{"success":true,"result":{"name":"anas-bucket","creation_date":"2026-10-27T10:11:12Z"}}`), nil
	})
	if ok, err := c.BucketMatches(t.Context(), account, "anas-bucket", strings.Repeat("c", 64)); err != nil || ok {
		t.Fatal("changed bucket accepted", ok, err)
	}
	if _, err := NewCloudflare("bad\ncredential"); !errors.Is(err, ErrJournal) {
		t.Fatal("invalid credential accepted")
	}
}
