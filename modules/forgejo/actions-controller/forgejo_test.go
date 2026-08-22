package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type controllerRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn controllerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func controllerResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestForgejoClientUsesScopedEphemeralRunnerAPI(t *testing.T) {
	transport := controllerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		username, password, ok := request.BasicAuth()
		if !ok || username != "controller" || password != "managed-password" {
			t.Fatal("Forgejo API request is missing the managed controller credential")
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api/v1/repos/team/repo/actions/runners/jobs":
			if request.URL.Query().Get("labels") != "docker" {
				t.Fatalf("job label query = %q", request.URL.RawQuery)
			}
			return controllerResponse(http.StatusOK, `[{"id":42,"handle":"job-handle","runs_on":["docker"],"status":"waiting"}]`), nil
		case request.Method == http.MethodPost && request.URL.Path == "/api/v1/repos/team/repo/actions/runners":
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["ephemeral"] != true || body["name"] == "" {
				t.Fatalf("runner registration body = %#v", body)
			}
			return controllerResponse(http.StatusCreated, `{"id":7,"uuid":"runner-uuid","token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), nil
		case request.Method == http.MethodDelete && request.URL.Path == "/api/v1/repos/team/repo/actions/runners/7":
			return controllerResponse(http.StatusNotFound, ""), nil
		default:
			t.Fatalf("unexpected Forgejo API request %s %s", request.Method, request.URL.String())
		}
		return controllerResponse(http.StatusInternalServerError, ""), nil
	})

	client := NewForgejoClient("http://forgejo.test", "controller", "managed-password")
	client.(*forgejoClient).client = &http.Client{Transport: transport}
	scope := Scope{Owner: "team", Repo: "repo"}
	jobs, err := client.ListJobs(context.Background(), scope, "docker:docker://node:24")
	if err != nil || len(jobs) != 1 || jobs[0].Handle != "job-handle" {
		t.Fatalf("jobs = %#v, %v", jobs, err)
	}
	registration, err := client.CreateRunner(context.Background(), scope, "anas-fj-0123456789abcdef0123")
	if err != nil || registration.ID != 7 || len(registration.Token) != 40 {
		t.Fatalf("registration = %#v, %v", registration, err)
	}
	if err := client.DeleteRunner(context.Background(), scope, 7); err != nil {
		t.Fatal(err)
	}
}

func TestForgejoClientErrorNeverIncludesResponseCredential(t *testing.T) {
	client := NewForgejoClient("http://forgejo.test", "controller", "password")
	client.(*forgejoClient).client = &http.Client{Transport: controllerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return controllerResponse(http.StatusInternalServerError, `{"token":"must-not-leak"}`), nil
	})}
	_, err := client.CreateRunner(
		context.Background(), Scope{Owner: "team", Repo: "repo"}, "runner",
	)
	if err == nil || strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("credential-bearing API error = %v", err)
	}
}
