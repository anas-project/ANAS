package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestForgejoAdminAgainstLiveInstance drives the real admin surface. It is
// opt-in for the same reason the PostgreSQL test is, and it exists because the
// stub cannot know what the server actually accepts: the first run against a
// pinned 15.0.7 found that the endpoint this client used did not exist, that
// token authentication is refused there, and that the repository list is a list
// of objects rather than of `owner/name` strings. A stub agreeing with a wrong
// assumption is worth nothing, so the assumptions are checked here.
//
//	AI_AGENT_TEST_FORGEJO_URL=http://127.0.0.1:13000 \
//	AI_AGENT_TEST_FORGEJO_USER=probeadmin \
//	AI_AGENT_TEST_FORGEJO_PASSWORD=... \
//	AI_AGENT_TEST_FORGEJO_ORG=probeorg \
//	  go test ./modules/ai_agent/orchestrator -run TestForgejoAdminAgainstLiveInstance
func TestForgejoAdminAgainstLiveInstance(t *testing.T) {
	baseURL := os.Getenv("AI_AGENT_TEST_FORGEJO_URL")
	username := os.Getenv("AI_AGENT_TEST_FORGEJO_USER")
	password := os.Getenv("AI_AGENT_TEST_FORGEJO_PASSWORD")
	org := os.Getenv("AI_AGENT_TEST_FORGEJO_ORG")
	if baseURL == "" || username == "" || password == "" || org == "" {
		t.Skip("set AI_AGENT_TEST_FORGEJO_URL/USER/PASSWORD/ORG to run the live Forgejo tests")
	}
	ctx := context.Background()
	redactor := NewRedactor(password)
	client := NewForgejoAdmin(baseURL, username, password, redactor)

	suffix := strconv.FormatInt(time.Now().Unix(), 10)
	account := "agent-live-" + suffix
	inScope, err := ParseRepo(org + "/" + os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_IN"))
	if err != nil {
		t.Fatalf("in-scope repository: %v", err)
	}
	outOfScope, err := ParseRepo(org + "/" + os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_OUT"))
	if err != nil {
		t.Fatalf("out-of-scope repository: %v", err)
	}

	user, err := client.EnsureUser(ctx, account, account+"@localhost.invalid")
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if user.ID == 0 || user.Login != account {
		t.Fatalf("user = %+v", user)
	}
	// Idempotence is not a nicety here: the bootstrapper runs on every sweep.
	again, err := client.EnsureUser(ctx, account, account+"@localhost.invalid")
	if err != nil || again.ID != user.ID {
		t.Fatalf("second EnsureUser = %+v, %v; want the same account", again, err)
	}

	// The grant has to precede the token, and it is granted on both
	// repositories so the scoping test below proves the token restricts rather
	// than the collaboration doing it.
	for _, repo := range []Repo{inScope, outOfScope} {
		if err := client.EnsureCollaborator(ctx, repo, account, "read"); err != nil {
			t.Fatalf("EnsureCollaborator on %s: %v", repo, err)
		}
	}

	token, err := client.CreateToken(ctx, account, tokenNameFor(1), discussionScopes, []string{inScope.String()})
	if err != nil {
		t.Fatalf("CreateToken: %v", err)
	}
	if token.Token == "" || token.ID == 0 {
		t.Fatalf("token = %+v", token)
	}
	if len(token.Repositories) != 1 || token.Repositories[0].Name != inScope.Name {
		t.Fatalf("repositories = %+v; AGENT-R-006 needs the restriction to be recorded", token.Repositories)
	}

	// The restriction has to bite where it matters: writing an issue outside
	// the scope must fail even though the account is a collaborator there.
	if err := issueWriteAllowed(ctx, baseURL, token.Token, inScope); err != nil {
		t.Fatalf("the in-scope repository refused a write the agent needs: %v", err)
	}
	if err := issueWriteAllowed(ctx, baseURL, token.Token, outOfScope); err == nil {
		t.Fatal("an out-of-scope repository accepted a write; the token restriction is not enforced")
	}

	tokens, err := client.ListTokens(ctx, account)
	if err != nil {
		t.Fatalf("ListTokens: %v", err)
	}
	var found bool
	for _, candidate := range tokens {
		if candidate.ID == token.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("ListTokens = %+v, want the minted token", tokens)
	}

	public, _, err := generateSSHKey()
	if err != nil {
		t.Fatalf("generateSSHKey: %v", err)
	}
	key, err := client.AddKey(ctx, account, keyTitleFor(1), public)
	if err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	hook, err := client.CreateSystemHook(ctx, ForgejoHookSpec{
		URL: "https://probe.invalid/ingress-" + suffix, Secret: testSecret,
		Events: subscribedEvents, Active: false,
	})
	if err != nil {
		t.Fatalf("CreateSystemHook: %v", err)
	}
	if len(hook.Events) == 0 {
		t.Fatalf("hook = %+v, want the subscribed events accepted", hook)
	}
	// Existence is an id lookup: GET /admin/hooks answers with an empty array
	// on this version even though the hook was just created, which is exactly
	// why EnsureWebhook does not judge existence by listing.
	if _, err := client.SystemHook(ctx, hook.ID); err != nil {
		t.Fatalf("SystemHook: %v", err)
	}
	if hooks, err := client.ListSystemHooks(ctx); err != nil {
		t.Fatalf("ListSystemHooks: %v", err)
	} else {
		for _, candidate := range hooks {
			if candidate.ID == hook.ID {
				t.Log("note: this instance does list system hooks; the id lookup remains correct either way")
			}
		}
	}
	if _, err := client.UpdateSystemHook(ctx, hook.ID, ForgejoHookSpec{
		URL: "https://probe.invalid/ingress-" + suffix, Secret: "rotated-" + testSecret,
		Events: subscribedEvents, Active: false,
	}); err != nil {
		t.Fatalf("UpdateSystemHook: %v", err)
	}

	if _, err := client.IssuesUpdatedSince(ctx, inScope, time.Time{}); err != nil {
		t.Fatalf("IssuesUpdatedSince with no cursor: %v", err)
	}
	if _, err := client.IssuesUpdatedSince(ctx, inScope, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("IssuesUpdatedSince with a cursor: %v", err)
	}

	t.Cleanup(func() {
		_ = client.DeleteSystemHook(ctx, hook.ID)
		_ = client.DeleteKey(ctx, account, key.ID)
		_ = client.DeleteToken(ctx, account, token.ID)
	})
}

// issueWriteAllowed asks whether a token may open an issue in a repository. It
// is the smallest write the scoping decision actually governs. It builds the
// request by hand rather than through the admin client, because the client
// always presents the administrator's basic auth and the question here is what
// the agent's own token can do.
func issueWriteAllowed(ctx context.Context, baseURL, token string, repo Repo) error {
	payload := strings.NewReader(`{"title":"scope check"}`)
	url := strings.TrimRight(baseURL, "/") + "/api/v1/repos/" + repo.Owner + "/" + repo.Name + "/issues"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("issue write returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
