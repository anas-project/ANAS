package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// TestFormRenderingAgainstLiveForgejo settles the one assumption the rest of
// the answer parser rests on: what Forgejo actually writes into an issue body
// when someone submits one of the generated forms.
//
// It can be automated because the rendering is server-side --
// issue_template.RenderToMarkdown runs in the issue-creation handler, not in
// the browser -- so a scripted form post exercises the real function. The test
// signs in over HTTP the way the repository's other end-to-end scripts do,
// against a disposable instance whose credential the harness is given.
//
//	AI_AGENT_TEST_FORGEJO_URL=... AI_AGENT_TEST_FORGEJO_USER=... \
//	AI_AGENT_TEST_FORGEJO_PASSWORD=... AI_AGENT_TEST_FORGEJO_TOKEN=... \
//	AI_AGENT_TEST_FORGEJO_ORG=... AI_AGENT_TEST_FORGEJO_REPO_IN=... \
//	  go test ./modules/ai_agent/orchestrator -run TestFormRenderingAgainstLiveForgejo
func TestFormRenderingAgainstLiveForgejo(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("AI_AGENT_TEST_FORGEJO_URL"), "/")
	user := os.Getenv("AI_AGENT_TEST_FORGEJO_USER")
	password := os.Getenv("AI_AGENT_TEST_FORGEJO_PASSWORD")
	token := os.Getenv("AI_AGENT_TEST_FORGEJO_TOKEN")
	org := os.Getenv("AI_AGENT_TEST_FORGEJO_ORG")
	repoName := os.Getenv("AI_AGENT_TEST_FORGEJO_REPO_IN")
	if baseURL == "" || user == "" || password == "" || token == "" || org == "" || repoName == "" {
		t.Skip("set the AI_AGENT_TEST_FORGEJO_* variables to run the form rendering test")
	}
	ctx := context.Background()
	repo, err := ParseRepo(org + "/" + repoName)
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	redactor := NewRedactor(password, token)
	issues := NewForgejoIssues(baseURL, token, redactor)

	// The forms have to be on the default branch for Forgejo to render them.
	templates := recognisedTemplates(ctx, t, baseURL, token, repo)
	if len(templates) == 0 {
		t.Skip("the generated forms are not on the default branch yet; merge the templates pull request first")
	}

	web := newWebSession(t, baseURL, redactor)
	web.signIn(ctx, t, user, password)

	// Read back the template Forgejo holds, so the field ids posted are the
	// ones it will look for rather than the ones this test assumes.
	templatePath := templateDir + "/discuss.yaml"
	content, err := issues.FileContents(ctx, repo, templatePath, "")
	if err != nil {
		t.Fatalf("read the committed template: %v", err)
	}
	decoded, err := content.Decoded()
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	var template formTemplate
	if err := yaml.Unmarshal([]byte(decoded), &template); err != nil {
		t.Fatalf("the committed template does not parse: %v", err)
	}

	suffix := strconv.FormatInt(time.Now().Unix(), 10)
	answers := map[string]string{
		FieldGoal:       "Ship the interaction contract.",
		FieldAcceptance: "The parser reads back what Forgejo wrote.",
		FieldScope:      "modules/ai_agent",
		FieldChatAgents: "codex",
		FieldExecAgent:  "codex",
		FieldLanguage:   "zh-CN",
		// Deliberately left empty, to see the placeholder Forgejo writes:
		// FieldForbidden, FieldReferences.
	}
	number := web.submitIssueForm(ctx, t, repo, templatePath,
		"[agent] form rendering check "+suffix, template, answers)

	created, err := issues.Issue(ctx, repo, number)
	if err != nil {
		t.Fatalf("read the created issue: %v", err)
	}
	t.Logf("issue body Forgejo rendered:\n%s", created.Body)

	// The assumption under the whole parser: `### <label>` then the value.
	parsed := ParseAnswers(created.Body)
	if got := parsed.Get("Goal"); got != answers[FieldGoal] {
		t.Fatalf("Goal parsed as %q, want %q -- the rendering is not what the parser assumes", got, answers[FieldGoal])
	}
	if got := parsed.List("Discussing agents"); len(got) != 1 || got[0] != "codex" {
		t.Fatalf("Discussing agents parsed as %v", got)
	}
	// An unanswered field must not become a literal answer.
	if got := parsed.Get("References"); got != "" {
		t.Fatalf("an empty field parsed as %q; Forgejo's placeholder is not an answer", got)
	}
	if !strings.Contains(created.Body, noResponse) {
		t.Fatalf("the body carries no %q, so this run did not exercise the empty-field case", noResponse)
	}
	// The instructional markdown block must not have been written into the
	// body: it is form-only, and a paragraph repeated in every issue is noise.
	if strings.Contains(created.Body, "This form configures an agent") {
		t.Fatal("the instructional markdown block was written into the issue body")
	}

	// The whole path, end to end: what Forgejo wrote becomes a usable
	// configuration.
	config := ResolveIssueConfig(parsed, testRegistry(t), fullDefaults())
	if config.Goal != answers[FieldGoal] || len(config.ChatAgents) != 1 || config.ExecAgent != "codex" {
		t.Fatalf("config = %+v", config)
	}
	if config.Language != "zh-CN" {
		t.Fatalf("language = %q, want the submitted value", config.Language)
	}
	if len(config.Downgrades) != 0 {
		t.Fatalf("a valid submission was narrowed: %v", config.Downgrades)
	}

	// A template's front-matter labels are a *pre-selection in the web page*,
	// not something the server applies on submission: the new-issue handler
	// turns them into pre-ticked sidebar entries which the browser then posts
	// back as label_ids. A submission that does not carry them -- this one, and
	// equally a person who unticks them -- creates an issue with no labels at
	// all. So the orchestrator must recognise an agent issue from its body and
	// apply the labels itself, never assume the template did it.
	if contains(created.LabelNames(), LabelAuto) {
		t.Fatalf("labels = %v; this submission carried no label_ids, so any label here "+
			"means the server started applying front-matter labels and the recognition "+
			"path should be revisited", created.LabelNames())
	}
	if !LooksLikeAgentIssue(created.Body) {
		t.Fatal("a rendered agent form is not recognised as an agent issue, " +
			"and the front-matter label cannot be relied on to say so")
	}
}

// webSession drives Forgejo's web endpoints, which is where the server-side
// form rendering lives. It is a test-only helper: everything the orchestrator
// itself does goes through the REST API.
type webSession struct {
	baseURL  string
	client   *http.Client
	redactor *Redactor
}

func newWebSession(t *testing.T, baseURL string, redactor *Redactor) *webSession {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return &webSession{baseURL: baseURL, redactor: redactor, client: &http.Client{
		Jar: jar, Timeout: 30 * time.Second,
		// Redirects are not followed: a sign-in's 303 is the signal that it
		// worked, and following it would hide that behind the page it lands on.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// signIn establishes a session. Forgejo 15 carries no anti-forgery field in its
// forms -- it relies on SameSite cookies and an Origin/Referer check instead --
// so the Referer header below is not decoration: without it the post is
// rejected as cross-site.
func (w *webSession) signIn(ctx context.Context, t *testing.T, user, password string) {
	t.Helper()
	resp := w.post(ctx, t, "/user/login", url.Values{"user_name": {user}, "password": {password}})
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	// A redirect means the session was established; a 200 means the form came
	// back, which is how a rejected sign-in looks.
	if resp.StatusCode == http.StatusOK && strings.Contains(string(body), `name="user_name"`) {
		t.Fatal("sign-in was rejected")
	}
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
		t.Fatalf("sign-in returned %d", resp.StatusCode)
	}
}

func (w *webSession) post(ctx context.Context, t *testing.T, path string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", w.baseURL+path)
	resp, err := w.client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %s", path, w.redactor.Error(err))
	}
	return resp
}

// submitIssueForm posts the form exactly as the web UI does and returns the
// number of the issue Forgejo created.
func (w *webSession) submitIssueForm(ctx context.Context, t *testing.T, repo Repo, templatePath, title string, template formTemplate, answers map[string]string) int {
	t.Helper()
	newPath := "/" + repo.Owner + "/" + repo.Name + "/issues/new"
	form := url.Values{"title": {title}, "template-file": {templatePath}, "content": {""}}
	for _, block := range template.Body {
		if block.ID == "" {
			continue
		}
		value, ok := answers[block.ID]
		if !ok {
			continue
		}
		switch block.Type {
		case "dropdown":
			// A dropdown is submitted as comma-separated option *indices*, not
			// as labels: Forgejo matches `slices.Contains(checks, idx)`. Posting
			// the label silently produces "_No response_".
			var indices []string
			for _, wanted := range strings.Split(value, ",") {
				wanted = strings.TrimSpace(wanted)
				for index, option := range block.Attributes.Options {
					if text, _ := option.(string); text == wanted {
						indices = append(indices, strconv.Itoa(index))
					}
				}
			}
			if len(indices) == 0 {
				t.Fatalf("no option of %q matches %q", block.ID, value)
			}
			form.Set("form-field-"+block.ID, strings.Join(indices, ","))
		case "checkboxes":
			// Each box is its own field, keyed by index, and the value is "on".
			for _, wanted := range strings.Split(value, ",") {
				wanted = strings.TrimSpace(wanted)
				for index, option := range block.Attributes.Options {
					entry, _ := option.(map[string]any)
					if label, _ := entry["label"].(string); label == wanted {
						form.Set(fmt.Sprintf("form-field-%s-%d", block.ID, index), "on")
					}
				}
			}
		default:
			form.Set("form-field-"+block.ID, value)
		}
	}
	resp := w.post(ctx, t, newPath, form)
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// The handler answers with JSON carrying the redirect to the new issue.
	var answer struct {
		Redirect string `json:"redirect"`
		Error    string `json:"errorMessage"`
	}
	if err := json.Unmarshal(body, &answer); err == nil && answer.Redirect != "" {
		segments := strings.Split(strings.Trim(answer.Redirect, "/"), "/")
		number, err := strconv.Atoi(segments[len(segments)-1])
		if err != nil {
			t.Fatalf("cannot read an issue number out of %q", answer.Redirect)
		}
		return number
	}
	if answer.Error != "" {
		t.Fatalf("Forgejo refused the form: %s", answer.Error)
	}
	t.Fatalf("unexpected response to the issue form (%d): %s", resp.StatusCode,
		w.redactor.String(string(body[:min(len(body), 400)])))
	return 0
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
