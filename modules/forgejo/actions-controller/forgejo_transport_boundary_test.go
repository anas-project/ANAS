package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestForgejoRedirectCannotLeaveTheApprovedRunnerCallSet(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			c := NewForgejoClient("http://forgejo.test", "controller", "private-password").(*forgejoClient)
			calls := 0
			c.client.Transport = controllerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if calls > 1 {
					return controllerResponse(http.StatusOK, `[]`), nil
				}
				response := controllerResponse(http.StatusTemporaryRedirect, "")
				response.Header.Set("Location", "/api/v1/admin/users")
				return response, nil
			})
			scope := Scope{Owner: "team", Repo: "repo"}
			var err error
			switch method {
			case http.MethodGet:
				_, err = c.ListJobs(context.Background(), scope, "docker")
			case http.MethodPost:
				_, err = c.CreateRunner(context.Background(), scope, "runner")
			case http.MethodDelete:
				err = c.DeleteRunner(context.Background(), scope, 7)
			}
			if err == nil || calls != 1 {
				t.Fatalf("redirect followed or accepted: calls=%d error=%v", calls, err)
			}
		})
	}
}

func TestForgejoQueueRequiresOneCompleteBoundedArray(t *testing.T) {
	for _, body := range []string{"", "{}", "[", "[] {}", "[]" + strings.Repeat(" ", 4<<20)} {
		c := NewForgejoClient("http://forgejo.test", "controller", "password").(*forgejoClient)
		c.client.Transport = controllerRoundTripFunc(func(*http.Request) (*http.Response, error) { return controllerResponse(http.StatusOK, body), nil })
		if _, err := c.ListJobs(context.Background(), Scope{Owner: "team", Repo: "repo"}, "docker"); err == nil {
			t.Fatalf("incomplete or oversized queue became absence (length %d)", len(body))
		}
	}
}

func TestForgejoQueueNormalizesTheNativeNullableArray(t *testing.T) {
	for _, body := range []string{"[]", "null"} {
		c := NewForgejoClient("http://forgejo.test", "controller", "password").(*forgejoClient)
		c.client.Transport = controllerRoundTripFunc(func(*http.Request) (*http.Response, error) { return controllerResponse(http.StatusOK, body), nil })
		jobs, err := c.ListJobs(context.Background(), Scope{Owner: "team", Repo: "repo"}, "docker")
		if err != nil || jobs == nil || len(jobs) != 0 {
			t.Fatal("valid native empty queue rejected or not normalized", err)
		}
	}
}

func TestForgejoTransportErrorsArePrivateAndKeepCancellation(t *testing.T) {
	c := NewForgejoClient("http://forgejo.test", "controller", "password").(*forgejoClient)
	c.client.Transport = controllerRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private-upstream-marker") })
	_, err := c.ListJobs(context.Background(), Scope{Owner: "team"}, "docker")
	if err == nil || strings.Contains(err.Error(), "private-upstream-marker") {
		t.Fatal("private transport diagnostics escaped")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = c.ListJobs(ctx, Scope{Owner: "team"}, "docker")
	if !errors.Is(err, context.Canceled) {
		t.Fatal("caller cancellation identity lost", err)
	}
}
