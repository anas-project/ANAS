package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleclient"
)

type fakeHostConsoleClient struct {
	invokeCalls int
	jobCalls    int
	applyCalls  int
	invokeErr   error
	jobErr      error
}

func (c *fakeHostConsoleClient) InvokeIncusPreflight(_ context.Context, workspace, key string) (map[string]any, string, error) {
	c.invokeCalls++
	if workspace != "main" || key != "cli-test-key" {
		return nil, "", errors.New("bad test arguments")
	}
	if c.invokeErr != nil {
		return nil, "", c.invokeErr
	}
	return map[string]any{"job": map[string]any{"id": "job-host", "status": "queued"}}, "/api/v1/jobs/job-host", nil
}

func (c *fakeHostConsoleClient) InvokeIncusPlan(context.Context, string, string, json.RawMessage, string) (map[string]any, string, error) {
	return nil, "", errors.New("unexpected plan call")
}

func (c *fakeHostConsoleClient) InvokeIncusImagePrunePlan(context.Context, string, string) (map[string]any, string, error) {
	return nil, "", errors.New("unexpected prune plan call")
}

func (c *fakeHostConsoleClient) IssueHostActionConfirmation(context.Context, string, string, string) (map[string]any, error) {
	return nil, errors.New("unexpected confirmation call")
}

func (c *fakeHostConsoleClient) InvokeIncusApply(_ context.Context, workspace, phase, planJobID, token string, parameters json.RawMessage, key string) (map[string]any, string, error) {
	c.applyCalls++
	if workspace != "main" || phase != "install" || planJobID != "plan-1" || token != "secret-token" || key != "cli-test-key" || !strings.Contains(string(parameters), `"schema"`) {
		return nil, "", errors.New("bad apply test arguments")
	}
	return map[string]any{"job": map[string]any{"id": "apply-1", "status": "queued"}}, "/api/v1/jobs/apply-1", nil
}

func (c *fakeHostConsoleClient) InvokeIncusImagePruneApply(context.Context, string, string, string, string) (map[string]any, string, error) {
	return nil, "", errors.New("unexpected prune apply call")
}

func (c *fakeHostConsoleClient) GetJob(_ context.Context, jobID string) (map[string]any, error) {
	c.jobCalls++
	if jobID != "job-host" {
		return nil, errors.New("bad test job")
	}
	if c.jobErr != nil {
		return nil, c.jobErr
	}
	return map[string]any{"job": map[string]any{"id": "job-host", "status": "succeeded"}}, nil
}

func TestHostIncusPreflightUsesConsoleClientAndReportsJob(t *testing.T) {
	client := &fakeHostConsoleClient{}
	restore := replaceHostConsoleClient(t, client, nil)
	defer restore()
	out, stderr, exit := captureWithStdin(t, "", "host", "incus-preflight", "-w", "main", "--session-json", "-", "--idempotency-key", "cli-test-key", "--json")
	if exit != 0 || stderr != "" || client.invokeCalls != 1 || client.jobCalls != 0 {
		t.Fatalf("exit=%d invoke=%d job=%d stdout=%s stderr=%s", exit, client.invokeCalls, client.jobCalls, out, stderr)
	}
	if strings.Contains(out, "session-token") || strings.Contains(out, "csrf-token") {
		t.Fatalf("credentials leaked in output: %s", out)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(out), &document); err != nil {
		t.Fatal(err)
	}
	if document["location"] != "/api/v1/jobs/job-host" || document["idempotency_key"] != "cli-test-key" {
		t.Fatalf("unexpected preflight response: %s", out)
	}
}

func TestHostJobQueriesOnlyJobEndpoint(t *testing.T) {
	client := &fakeHostConsoleClient{}
	restore := replaceHostConsoleClient(t, client, nil)
	defer restore()
	out, _, exit := captureWithStdin(t, "", "host", "job", "job-host", "--session-json", "-", "--json")
	if exit != 0 || client.jobCalls != 1 || client.invokeCalls != 0 || !strings.Contains(out, `"status": "succeeded"`) {
		t.Fatalf("query failed: exit=%d stdout=%s", exit, out)
	}
}

func TestHostIncusPreflightUnknownExecutionDoesNotRetry(t *testing.T) {
	client := &fakeHostConsoleClient{invokeErr: errors.New("connection closed")}
	restore := replaceHostConsoleClient(t, client, nil)
	defer restore()
	out, _, exit := captureWithStdin(t, "", "host", "incus-preflight", "-w", "main", "--session-json", "-", "--idempotency-key", "cli-test-key", "--json")
	if exit != exitFailure || client.invokeCalls != 1 || !strings.Contains(out, `"code": "unknown_execution"`) || !strings.Contains(out, `"idempotency_key": "cli-test-key"`) {
		t.Fatalf("unknown execution was retried or not reported: exit=%d calls=%d stdout=%s", exit, client.invokeCalls, out)
	}
}

func TestHostIncusApplyRejectsArgvTokenAndReadsSingleRequestEnvelope(t *testing.T) {
	client := &fakeHostConsoleClient{}
	restore := replaceHostConsoleClientFromEnvelope(t, client, nil)
	defer restore()
	out, stderr, exit := captureWithStdin(t, "", "host", "incus-apply", "-w", "main", "--phase", "install", "--plan-job", "plan-1", "--confirmation-token", "secret-token", "--parameters-json", "{}", "--session-json", "-", "--idempotency-key", "cli-test-key", "--json")
	if exit != exitUsage || client.applyCalls != 0 || strings.Contains(out+stderr, "secret-token") {
		t.Fatalf("argv token accepted or leaked: exit=%d stdout=%s stderr=%s", exit, out, stderr)
	}
	request := `{"session":{"schema":"anas.console-session/v1","origin":"https://nas.example","ca_pem":"x","session":{"source":"local","session_token":"session-token","csrf_token":"csrf-token"}},"plan_job_id":"plan-1","confirmation_token":"secret-token","parameters":{"schema":"anas.host-action.incus/v1"}}`
	out, stderr, exit = captureWithStdin(t, request, "host", "incus-apply", "-w", "main", "--phase", "install", "--request-json", "-", "--idempotency-key", "cli-test-key", "--json")
	if exit != 0 || stderr != "" || client.applyCalls != 1 || strings.Contains(out, "secret-token") || strings.Contains(out, "session-token") {
		t.Fatalf("stdin apply failed/leaked: exit=%d calls=%d stdout=%s stderr=%s", exit, client.applyCalls, out, stderr)
	}
}

func TestHostConsoleEnvelopeRejectsInsecureOrMissingInputs(t *testing.T) {
	for _, envelope := range []string{
		`{"schema":"anas.console-session/v1","origin":"http://nas.example","ca_pem":"x","session":{"source":"local","session_token":"s","csrf_token":"c"}}`,
		`{"schema":"anas.console-session/v1","origin":"https://nas.example","ca_pem":"","session":{"source":"local","session_token":"s","csrf_token":"c"}}`,
		`{"schema":"anas.console-session/v1","origin":"https://nas.example","ca_pem":"x","session":{"source":"local","session_token":"","csrf_token":"c"}}`,
	} {
		restore := replaceHostConsoleClient(t, nil, nil)
		out, stderr, exit := captureWithStdin(t, envelope, "host", "job", "job-host", "--session-json", "-", "--json")
		restore()
		if exit != exitUsage || strings.Contains(out+stderr, "session_token") || strings.Contains(out+stderr, "csrf_token") {
			t.Fatalf("invalid envelope accepted or leaked fields: exit=%d stdout=%s stderr=%s", exit, out, stderr)
		}
	}
}

func replaceHostConsoleClient(t *testing.T, client hostConsoleClient, err error) func() {
	t.Helper()
	previous := newHostConsoleClientFromStdin
	if client == nil && err == nil {
		newHostConsoleClientFromStdin = previous
		return func() {}
	}
	newHostConsoleClientFromStdin = func() (hostConsoleClient, error) {
		return client, err
	}
	return func() { newHostConsoleClientFromStdin = previous }
}

func replaceHostConsoleClientFromEnvelope(t *testing.T, client hostConsoleClient, err error) func() {
	t.Helper()
	previous := newHostConsoleClientFromEnvelope
	newHostConsoleClientFromEnvelope = func(consoleclient.Envelope) (hostConsoleClient, error) {
		return client, err
	}
	return func() { newHostConsoleClientFromEnvelope = previous }
}

func captureWithStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := write.WriteString(stdin); err != nil {
		t.Fatal(err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	realStdin := os.Stdin
	os.Stdin = read
	defer func() {
		os.Stdin = realStdin
		_ = read.Close()
	}()
	return capture(t, args...)
}
