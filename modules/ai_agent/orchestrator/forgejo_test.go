package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// forgejoStub records what the client sent and replies with what Forgejo's
// documented shapes look like. It is the contract test §13.1 asks for: the
// request and result shapes of the admin surface, without a live Forgejo.
type forgejoStub struct {
	t        *testing.T
	requests []recordedRequest
	handler  func(recordedRequest) (int, string)
}

type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Sudo   string
	Body   map[string]any
}

func (s *forgejoStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	recorded := recordedRequest{
		Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Sudo: r.Header.Get("Sudo"),
	}
	if user, password, ok := r.BasicAuth(); ok {
		recorded.Auth = user + ":" + password
	}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &recorded.Body); err != nil {
			s.t.Errorf("%s %s sent a body that is not JSON: %s", r.Method, r.URL.Path, body)
		}
	}
	s.requests = append(s.requests, recorded)
	status, payload := s.handler(recorded)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, payload)
}

func newStubClient(t *testing.T, handler func(recordedRequest) (int, string)) (ForgejoAdmin, *forgejoStub, *Redactor) {
	t.Helper()
	stub := &forgejoStub{t: t, handler: handler}
	server := httptest.NewServer(stub)
	t.Cleanup(server.Close)
	redactor := NewRedactor("an-administrator-password")
	return NewForgejoAdmin(server.URL, "anas_ai_agent", "an-administrator-password", redactor), stub, redactor
}

// AGENT-R-005: an existing account is reused, and a missing one is created
// through the admin API with a password it can never sign in with.
func TestEnsureUserReadsBeforeItCreates(t *testing.T) {
	client, stub, _ := newStubClient(t, func(req recordedRequest) (int, string) {
		switch {
		case req.Method == http.MethodGet && req.Path == "/api/v1/users/agent-x":
			return http.StatusNotFound, `{"message":"user does not exist"}`
		case req.Method == http.MethodPost && req.Path == "/api/v1/admin/users":
			return http.StatusCreated, `{"id":42,"login":"agent-x"}`
		}
		t.Fatalf("unexpected request %s %s", req.Method, req.Path)
		return 0, ""
	})
	user, err := client.EnsureUser(context.Background(), "agent-x", "agent-x@localhost.invalid")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if user.ID != 42 || user.Login != "agent-x" {
		t.Fatalf("user = %+v", user)
	}
	if len(stub.requests) != 2 || stub.requests[0].Method != http.MethodGet {
		t.Fatalf("requests = %+v, want a read before the create", stub.requests)
	}
	created := stub.requests[1].Body
	if created["username"] != "agent-x" {
		t.Fatalf("create body = %+v", created)
	}
	if created["must_change_password"] != false {
		t.Fatal("the agent account would be asked to change its password, which it can never do")
	}
	password, _ := created["password"].(string)
	if len(password) < 32 {
		t.Fatalf("the placeholder password is %d characters; it must not be guessable", len(password))
	}
	if stub.requests[1].Auth != "anas_ai_agent:an-administrator-password" {
		t.Fatalf("auth = %q, want the dedicated administrator", stub.requests[1].Auth)
	}
}

func TestEnsureUserReturnsAnExistingAccountUnchanged(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"id":7,"login":"agent-x"}`
	})
	user, err := client.EnsureUser(context.Background(), "agent-x", "agent-x@localhost.invalid")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if user.ID != 7 {
		t.Fatalf("user = %+v", user)
	}
	if len(stub.requests) != 1 {
		t.Fatalf("requests = %d, want the existing account reused without a create", len(stub.requests))
	}
}

// AGENT-R-006: the token request goes to the only endpoint the pinned 15.0.7
// has, names the agent through `Sudo`, and carries `repositories` as objects.
// Every one of those three was wrong before the probe against the real image:
// `/admin/users/{u}/tokens` is a 404, token auth is refused, and an
// `owner/name` string fails to unmarshal.
func TestCreateTokenSendsScopesAndRepositories(t *testing.T) {
	client, stub, redactor := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusCreated, `{"id":9,"name":"anas-ai-agent","sha1":"a-freshly-minted-token","scopes":["read:repository"]}`
	})
	ctx := context.Background()
	token, err := client.CreateToken(ctx, "agent-x", tokenNameFor(1), discussionScopes, []string{"anas-project/ANAS"})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if token.ID != 9 || token.Token != "a-freshly-minted-token" {
		t.Fatalf("token = %+v", token)
	}
	if stub.requests[0].Path != "/api/v1/users/agent-x/tokens" {
		t.Fatalf("path = %q; /api/v1/admin/users/{u}/tokens does not exist on 15.0.7", stub.requests[0].Path)
	}
	if stub.requests[0].Sudo != "agent-x" {
		t.Fatal("the request did not act as the agent, so the token would be minted for the administrator")
	}
	// Token creation is the one call that must present the administrator's
	// password: 15.0.7 answers token authentication with "auth method not
	// allowed".
	if stub.requests[0].Auth != "anas_ai_agent:an-administrator-password" {
		t.Fatalf("auth = %q, want basic auth", stub.requests[0].Auth)
	}
	body := stub.requests[0].Body
	targets, ok := body["repositories"].([]any)
	if !ok || len(targets) != 1 {
		t.Fatalf("repositories = %v, want one target object", body["repositories"])
	}
	target, ok := targets[0].(map[string]any)
	if !ok || target["owner"] != "anas-project" || target["name"] != "ANAS" {
		t.Fatalf("repository target = %v, want {owner,name}; a string is rejected by the server", targets[0])
	}
	// The minted value is registered with the scrubber the moment it exists.
	if scrubbed := redactor.String("using a-freshly-minted-token"); strings.Contains(scrubbed, "a-freshly-minted-token") {
		t.Fatalf("the minted token was not registered for redaction: %q", scrubbed)
	}

	stub.requests = nil
	if _, err := client.CreateToken(ctx, "agent-x", tokenNameFor(1), discussionScopes, nil); err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if _, present := stub.requests[0].Body["repositories"]; present {
		t.Fatal("an empty repository list was sent, which is ambiguous across Forgejo versions")
	}
}

// AGENT-R-006: the agent has to be a collaborator before a scoped token can
// name the repository. Forgejo resolves the list as the agent, so a repository
// it cannot see comes back as "does not exist".
func TestEnsureCollaboratorGrantsReadAccess(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusNoContent, ""
	})
	if err := client.EnsureCollaborator(context.Background(), testRepo(t), "agent-x", "read"); err != nil {
		t.Fatalf("EnsureCollaborator: %v", err)
	}
	if stub.requests[0].Method != http.MethodPut ||
		stub.requests[0].Path != "/api/v1/repos/anas-project/ANAS/collaborators/agent-x" {
		t.Fatalf("request = %s %s", stub.requests[0].Method, stub.requests[0].Path)
	}
	if stub.requests[0].Body["permission"] != "read" {
		t.Fatalf("permission = %v; standing write access belongs to a job, not to the identity", stub.requests[0].Body["permission"])
	}
}

func TestDeleteTokenUsesTheUserScopedPath(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusNoContent, ""
	})
	if err := client.DeleteToken(context.Background(), "agent-x", 9); err != nil {
		t.Fatalf("DeleteToken: %v", err)
	}
	if stub.requests[0].Path != "/api/v1/users/agent-x/tokens/9" || stub.requests[0].Sudo != "agent-x" {
		t.Fatalf("request = %s (Sudo %q)", stub.requests[0].Path, stub.requests[0].Sudo)
	}
}

func TestCreateTokenRefusesAnEmptyTokenValue(t *testing.T) {
	client, _, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusCreated, `{"id":9,"name":"anas-ai-agent"}`
	})
	if _, err := client.CreateToken(context.Background(), "agent-x", tokenName, discussionScopes, nil); err == nil {
		t.Fatal("CreateToken accepted a response with no token value")
	}
}

// AGENT-R-009: the hook is registered with the JSON content type, the signing
// secret and exactly the events the ingress handles.
func TestSystemHookRegistrationShape(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusCreated, `{"id":3,"type":"forgejo","active":true,"events":["issues"]}`
	})
	_, err := client.CreateSystemHook(context.Background(), ForgejoHookSpec{
		URL: "https://agent.example" + WebhookPath, Secret: testSecret,
		Events: subscribedEvents, Active: true,
	})
	if err != nil {
		t.Fatalf("CreateSystemHook: %v", err)
	}
	if stub.requests[0].Path != "/api/v1/admin/hooks" {
		t.Fatalf("path = %q, want the system hook endpoint", stub.requests[0].Path)
	}
	config, ok := stub.requests[0].Body["config"].(map[string]any)
	if !ok {
		t.Fatalf("body = %+v, want a config object", stub.requests[0].Body)
	}
	if config["content_type"] != "json" {
		t.Fatalf("content type = %v; the ingress verifies a JSON body", config["content_type"])
	}
	if config["secret"] != testSecret {
		t.Fatal("the hook was registered without the signing secret, so every delivery would be unverifiable")
	}
	events, _ := stub.requests[0].Body["events"].([]any)
	if len(events) != len(subscribedEvents) {
		t.Fatalf("events = %v, want the subscribed set", events)
	}
}

// A delete that finds nothing is a success: the desired state is "gone".
func TestDeletesTolerateAnAlreadyAbsentObject(t *testing.T) {
	client, _, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusNotFound, `{"message":"not found"}`
	})
	ctx := context.Background()
	if err := client.DeleteToken(ctx, "agent-x", 9); err != nil {
		t.Fatalf("DeleteToken on an absent token: %v", err)
	}
	if err := client.DeleteKey(ctx, "agent-x", 9); err != nil {
		t.Fatalf("DeleteKey on an absent key: %v", err)
	}
	if err := client.DeleteSystemHook(ctx, 3); err != nil {
		t.Fatalf("DeleteSystemHook on an absent hook: %v", err)
	}
}

// AGENT-R-013: the reconciliation read asks Forgejo for what changed, with the
// cursor expressed the way the API expects it.
func TestIssuesUpdatedSinceSendsTheCursor(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `[{"number":12,"updated_at":"2026-09-05T10:00:00Z","state":"open","user":{"login":"alice"}}]`
	})
	since := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	issues, err := client.IssuesUpdatedSince(context.Background(), testRepo(t), since)
	if err != nil {
		t.Fatalf("IssuesUpdatedSince: %v", err)
	}
	if len(issues) != 1 || issues[0].Number != 12 || issues[0].User.Login != "alice" {
		t.Fatalf("issues = %+v", issues)
	}
	if !strings.Contains(stub.requests[0].Query, "since=2026-09-05T09%3A00%3A00Z") {
		t.Fatalf("query = %q, want the RFC 3339 cursor", stub.requests[0].Query)
	}
	if !strings.Contains(stub.requests[0].Query, "state=all") {
		t.Fatal("the sweep asked only for open issues, so a closed issue's change would be missed")
	}
	if stub.requests[0].Path != "/api/v1/repos/anas-project/ANAS/issues" {
		t.Fatalf("path = %q", stub.requests[0].Path)
	}
}

// A cold start has no cursor, and must ask for everything rather than sending
// an empty `since` the server would reject.
func TestIssuesUpdatedSinceOmitsAnEmptyCursor(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `[]`
	})
	if _, err := client.IssuesUpdatedSince(context.Background(), testRepo(t), time.Time{}); err != nil {
		t.Fatalf("IssuesUpdatedSince: %v", err)
	}
	if strings.Contains(stub.requests[0].Query, "since=") {
		t.Fatalf("query = %q, want no cursor on a cold start", stub.requests[0].Query)
	}
}

// AGENT-R-010: a refusal quoting the request must not carry the credential out
// with it.
func TestErrorsDoNotEchoTheAdministratorPassword(t *testing.T) {
	client, _, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusForbidden, `{"message":"an-administrator-password is not authorized"}`
	})
	_, err := client.ListSystemHooks(context.Background())
	if err == nil {
		t.Fatal("ListSystemHooks succeeded on a 403")
	}
	if strings.Contains(err.Error(), "an-administrator-password") {
		t.Fatalf("the error echoed the administrator password: %q", err)
	}
}

func TestAddKeySendsTheAuthorizedKeyLine(t *testing.T) {
	client, stub, _ := newStubClient(t, func(recordedRequest) (int, string) {
		return http.StatusCreated, `{"id":5,"title":"anas-ai-agent"}`
	})
	public, _, err := generateSSHKey()
	if err != nil {
		t.Fatalf("generateSSHKey: %v", err)
	}
	key, err := client.AddKey(context.Background(), "agent-x", keyTitleFor(1), public)
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	if key.ID != 5 {
		t.Fatalf("key = %+v", key)
	}
	sent, _ := stub.requests[0].Body["key"].(string)
	if !strings.HasPrefix(sent, "ssh-ed25519 ") {
		t.Fatalf("key sent = %q, want an authorized-keys line", sent)
	}
	// The private half must never appear in a request.
	if strings.Contains(sent, "PRIVATE KEY") {
		t.Fatal("the private key was sent to Forgejo")
	}
}

func TestFingerprintIsStableAndNotReversible(t *testing.T) {
	value := "a-token-value-to-fingerprint"
	first, second := fingerprint(value), fingerprint(value)
	if first != second {
		t.Fatal("the fingerprint is not stable")
	}
	if strings.Contains(first, value) {
		t.Fatalf("the fingerprint contains the value: %q", first)
	}
	if decoded, err := base64.StdEncoding.DecodeString(first); err == nil && strings.Contains(string(decoded), value) {
		t.Fatal("the fingerprint is an encoding of the value rather than a digest")
	}
	if fingerprint(value) == fingerprint(value+"x") {
		t.Fatal("two different values share a fingerprint")
	}
}
