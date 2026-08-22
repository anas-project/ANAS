package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

type ForgejoAPI interface {
	ListJobs(context.Context, Scope, string) ([]ActionJob, error)
	CreateRunner(context.Context, Scope, string) (RunnerRegistration, error)
	DeleteRunner(context.Context, Scope, int64) error
}

type ActionJob struct {
	ID      int64    `json:"id"`
	Attempt int64    `json:"attempt"`
	Handle  string   `json:"handle"`
	Name    string   `json:"name"`
	RunsOn  []string `json:"runs_on"`
	TaskID  int64    `json:"task_id"`
	Status  string   `json:"status"`
}

type RunnerRegistration struct {
	ID    int64  `json:"id"`
	UUID  string `json:"uuid"`
	Token string `json:"token"`
}

var runnerTokenPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type forgejoClient struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

func NewForgejoClient(baseURL, username, password string) ForgejoAPI {
	return &forgejoClient{
		baseURL: baseURL, username: username, password: password,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *forgejoClient) ListJobs(ctx context.Context, scope Scope, label string) ([]ActionJob, error) {
	query := url.Values{}
	query.Set("labels", labelName(label))
	path := scope.runnersPath("/jobs") + "?" + query.Encode()
	var jobs []ActionJob
	if err := c.do(ctx, http.MethodGet, path, nil, &jobs, http.StatusOK); err != nil {
		return nil, fmt.Errorf("list waiting jobs for scope %s: %w", scope, err)
	}
	return jobs, nil
}

func (c *forgejoClient) CreateRunner(ctx context.Context, scope Scope, name string) (RunnerRegistration, error) {
	body, err := json.Marshal(map[string]any{"name": name, "ephemeral": true})
	if err != nil {
		return RunnerRegistration{}, err
	}
	var registration RunnerRegistration
	if err := c.do(ctx, http.MethodPost, scope.runnersPath(""), bytes.NewReader(body), &registration, http.StatusCreated, http.StatusOK); err != nil {
		return RunnerRegistration{}, fmt.Errorf("create ephemeral Runner for scope %s: %w", scope, err)
	}
	if registration.ID < 1 || registration.UUID == "" || len(registration.UUID) > 128 || hasControl(registration.UUID) ||
		!runnerTokenPattern.MatchString(registration.Token) {
		return RunnerRegistration{}, fmt.Errorf("Forgejo returned an incomplete Runner registration")
	}
	return registration, nil
}

func (c *forgejoClient) DeleteRunner(ctx context.Context, scope Scope, id int64) error {
	path := scope.runnersPath("/" + strconv.FormatInt(id, 10))
	return c.do(ctx, http.MethodDelete, path, nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
}

func (c *forgejoClient) do(ctx context.Context, method, path string, body io.Reader, output any, accepted ...int) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	for _, status := range accepted {
		if resp.StatusCode == status {
			if output == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
				return nil
			}
			return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(output)
		}
	}
	// Never copy the response body into an error: runner-creation responses
	// contain a credential, and a reverse proxy may reflect request data.
	return fmt.Errorf("Forgejo API returned status %d", resp.StatusCode)
}

func labelName(mapping string) string {
	for index, r := range mapping {
		if r == ':' {
			return mapping[:index]
		}
	}
	return mapping
}
