package incusprovision

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The isolated Ubuntu Incus daemon returns HTTP 201 with a synchronous 200
// envelope for storage-pool creation. That is completed creation, not async
// acceptance. Preserve method/envelope checks rather than accepting all 2xx.
func TestIncusUnixCreatedResponseCompletesOnePostWithoutRetry(t *testing.T) {
	for _, path := range []string{"/1.0/storage-pools", "/1.0/certificates"} {
		t.Run(path, func(t *testing.T) {
			calls := 0
			c := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodPost || r.URL.Path != path {
					t.Fatal("creation was replayed or polled")
				}
				return incusTestResponse(http.StatusCreated, `{"type":"sync","status":"Success","status_code":200,"operation":"","error_code":0,"error":"","metadata":null}`), nil
			})}
			if err := c.do(context.Background(), http.MethodPost, path, map[string]string{"name": "fixture"}, nil); err != nil || calls != 1 {
				t.Fatalf("completed synchronous creation rejected: %v (calls=%d)", err, calls)
			}
		})
	}
}

// doSyncOnly creates the anas-traefik address set with a POST; Incus 7.0.1
// answers 201. Decoding it as a read failed every incus.configure on the
// 2026-10-08 lease network host.
func TestIncusSyncOnlyCreationAcceptsCreated(t *testing.T) {
	calls := 0
	c := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost {
			t.Fatal("creation was replayed or polled")
		}
		return incusTestResponse(http.StatusCreated, `{"type":"sync","status":"Success","status_code":200,"operation":"","error_code":0,"error":"","metadata":{}}`), nil
	})}
	if err := c.doSyncOnly(context.Background(), http.MethodPost, "/1.0/network-address-sets?project=default", map[string]string{"name": "anas-traefik"}, nil); err != nil || calls != 1 {
		t.Fatalf("completed synchronous creation rejected: %v (calls=%d)", err, calls)
	}
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return incusTestResponse(http.StatusCreated, `{"type":"sync","status_code":200,"metadata":{}}`), nil
		})}
		if err := c.doSyncOnly(context.Background(), method, "/1.0/network-address-sets/anas-traefik?project=default", nil, nil); err == nil {
			t.Fatalf("%s accepted HTTP 201", method)
		}
	}
}

func TestIncusCreatedResponseCannotMasqueradeAsReadOrUnknownSuccess(t *testing.T) {
	for _, tc := range []struct {
		method, body string
		status       int
	}{
		{http.MethodGet, `{"type":"sync","status_code":200,"metadata":{}}`, 201},
		{http.MethodPut, `{"type":"sync","status_code":200,"metadata":{}}`, 201},
		{http.MethodPatch, `{"type":"sync","status_code":200,"metadata":{}}`, 201},
		{http.MethodDelete, `{"type":"sync","status_code":200,"metadata":{}}`, 201},
		{http.MethodPost, `{"type":"sync","status_code":200,"metadata":null}`, 204},
		{http.MethodPost, `{"type":"sync","status_code":103,"metadata":null}`, 201},
		{http.MethodPost, `{"type":"sync","status_code":200,"operation":"` + testOperationURL + `","metadata":null}`, 201},
		{http.MethodPost, `{"type":"sync","status_code":200,"error":"private-created-marker","metadata":null}`, 201},
		{http.MethodPost, `{"type":"sync","status_code":200,"error_code":500,"metadata":null}`, 201},
		{http.MethodPost, `{"type":"sync","status_code":103,"status_code":200}`, 201},
		{http.MethodPost, `{"type":"async","status_code":100,"operation":"` + testOperationURL + `"}`, 201},
	} {
		calls := 0
		c := &incusUnixClient{transport: incusRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return incusTestResponse(tc.status, tc.body), nil
		})}
		if err := c.do(context.Background(), tc.method, "/1.0/storage-pools", nil, nil); err == nil || calls != 1 || strings.Contains(err.Error(), "private-created-marker") {
			t.Fatalf("ambiguous created response accepted: method=%s status=%d err=%v calls=%d", tc.method, tc.status, err, calls)
		}
	}
}
