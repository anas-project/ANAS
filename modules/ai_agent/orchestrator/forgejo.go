package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ForgejoAdmin is the administrative surface this module is allowed to use.
// The list is deliberately short and closed: create accounts, mint their
// credentials, register the system webhook, and read back what exists. Nothing
// here writes repository content, and none of it is ever reachable from a work
// instance (AGENT-R-008).
type ForgejoAdmin interface {
	EnsureUser(ctx context.Context, account, email string) (ForgejoUser, error)
	CreateToken(ctx context.Context, account, name string, scopes, repositories []string) (ForgejoToken, error)
	ListTokens(ctx context.Context, account string) ([]ForgejoToken, error)
	DeleteToken(ctx context.Context, account string, id int64) error
	// EnsureCollaborator has to run before a repository-scoped token can name
	// the repository: Forgejo resolves `repositories` in the acting account's
	// own context, so a repository the agent cannot see is reported as
	// non-existent rather than as forbidden.
	EnsureCollaborator(ctx context.Context, repo Repo, account, permission string) error
	AddKey(ctx context.Context, account, title, publicKey string) (ForgejoKey, error)
	DeleteKey(ctx context.Context, account string, id int64) error
	ListSystemHooks(ctx context.Context) ([]ForgejoHook, error)
	// SystemHook reads one hook by id. It exists because listing cannot be
	// trusted on the pinned Forgejo: GET /admin/hooks answers with an empty
	// array even when POST /admin/hooks has just created a hook, while
	// GET /admin/hooks/{id} returns it. Existence is therefore an id lookup.
	SystemHook(ctx context.Context, id int64) (ForgejoHook, error)
	CreateSystemHook(ctx context.Context, hook ForgejoHookSpec) (ForgejoHook, error)
	UpdateSystemHook(ctx context.Context, id int64, hook ForgejoHookSpec) (ForgejoHook, error)
	DeleteSystemHook(ctx context.Context, id int64) error
	IssuesUpdatedSince(ctx context.Context, repo Repo, since time.Time) ([]ForgejoIssue, error)
}

type ForgejoUser struct {
	ID       int64  `json:"id"`
	Login    string `json:"login"`
	IsAdmin  bool   `json:"is_admin"`
	Restrict bool   `json:"restricted"`
}

type ForgejoToken struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Token is returned exactly once, at creation. It is registered with the
	// redactor immediately and never persisted in clear.
	Token        string          `json:"sha1"`
	Scopes       []string        `json:"scopes"`
	Repositories []ForgejoTarget `json:"repositories"`
}

// ForgejoTarget is the `RepoTargetOption` shape Forgejo uses to limit a token
// to specific repositories. It is an object, not an `owner/name` string: the
// string form is rejected with an unmarshal error.
type ForgejoTarget struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

type ForgejoKey struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type ForgejoHookSpec struct {
	URL    string
	Secret string
	Events []string
	Active bool
}

type ForgejoHook struct {
	ID     int64             `json:"id"`
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
	Events []string          `json:"events"`
	Active bool              `json:"active"`
}

type ForgejoIssue struct {
	Number  int       `json:"number"`
	Updated time.Time `json:"updated_at"`
	State   string    `json:"state"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
}

type forgejoClient struct {
	baseURL  string
	username string
	password string
	redactor *Redactor
	http     *http.Client
}

// NewForgejoAdmin builds the administrative client. It talks to Forgejo over
// the same published HTTPS URL a person would use, verified against the trust
// store the container was given, rather than a private side channel that would
// bypass the proxy's own handling.
func NewForgejoAdmin(baseURL, username, password string, redactor *Redactor) ForgejoAdmin {
	return &forgejoClient{
		baseURL: strings.TrimRight(baseURL, "/"), username: username, password: password,
		redactor: redactor, http: &http.Client{Timeout: 30 * time.Second},
	}
}

// EnsureUser is idempotent by design: an account that already exists is
// returned as it is. Bootstrap runs on every reconciliation, and a second run
// must not fail or mint a second account (AGENT-R-005).
func (c *forgejoClient) EnsureUser(ctx context.Context, account, email string) (ForgejoUser, error) {
	var existing ForgejoUser
	err := c.do(ctx, http.MethodGet, "/api/v1/users/"+url.PathEscape(account), nil, &existing, nil, http.StatusOK)
	if err == nil {
		if existing.Login != account {
			return ForgejoUser{}, fmt.Errorf("Forgejo returned account %q for %q", existing.Login, account)
		}
		return existing, nil
	}
	if !isStatus(err, http.StatusNotFound) {
		return ForgejoUser{}, err
	}
	password, err := randomSecret(48)
	if err != nil {
		return ForgejoUser{}, err
	}
	c.redactor.Add(password)
	body := map[string]any{
		"username": account, "email": email, "password": password,
		// The account exists to hold a token and an SSH key. It is never used
		// to sign in, so it must not be able to: no password change prompt to
		// satisfy, no visibility beyond the deployment, no administrator rights.
		"must_change_password": false,
		"restricted":           false,
		"visibility":           "limited",
	}
	var created ForgejoUser
	if err := c.do(ctx, http.MethodPost, "/api/v1/admin/users", body, &created, nil, http.StatusCreated, http.StatusOK); err != nil {
		return ForgejoUser{}, fmt.Errorf("create agent account %q: %w", account, err)
	}
	return created, nil
}

// tokenPath is the only token endpoint 15.0.7 has. There is deliberately no
// `/admin/users/{u}/tokens`: the probe against the pinned image returns 404 for
// it, and minting for another account is expressed with the `Sudo` header
// instead. Token authentication is refused here ("auth method not allowed"), so
// this is also the one call that must use the administrator's password.
func tokenPath(account string) string {
	return "/api/v1/users/" + url.PathEscape(account) + "/tokens"
}

func (c *forgejoClient) CreateToken(ctx context.Context, account, name string, scopes, repositories []string) (ForgejoToken, error) {
	body := map[string]any{"name": name, "scopes": scopes}
	// `repositories` is the field that makes least privilege reachable. It is
	// omitted rather than sent empty when the caller has no repository list,
	// because an empty list reads as "no repositories" on some versions and
	// "all repositories" on others.
	if len(repositories) > 0 {
		targets := make([]ForgejoTarget, 0, len(repositories))
		for _, name := range repositories {
			repo, err := ParseRepo(name)
			if err != nil {
				return ForgejoToken{}, err
			}
			targets = append(targets, ForgejoTarget{Owner: repo.Owner, Name: repo.Name})
		}
		body["repositories"] = targets
	}
	var token ForgejoToken
	if err := c.do(ctx, http.MethodPost, tokenPath(account), body, &token, sudo(account), http.StatusCreated, http.StatusOK); err != nil {
		return ForgejoToken{}, fmt.Errorf("mint token for %q: %w", account, err)
	}
	if token.Token == "" {
		return ForgejoToken{}, fmt.Errorf("Forgejo returned no token value for %q", account)
	}
	c.redactor.Add(token.Token)
	return token, nil
}

func (c *forgejoClient) ListTokens(ctx context.Context, account string) ([]ForgejoToken, error) {
	var tokens []ForgejoToken
	if err := c.do(ctx, http.MethodGet, tokenPath(account), nil, &tokens, sudo(account), http.StatusOK); err != nil {
		return nil, fmt.Errorf("list tokens for %q: %w", account, err)
	}
	return tokens, nil
}

func (c *forgejoClient) DeleteToken(ctx context.Context, account string, id int64) error {
	path := tokenPath(account) + "/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil, sudo(account),
		http.StatusNoContent, http.StatusOK, http.StatusNotFound)
}

// EnsureCollaborator grants the agent access to a repository so a
// repository-scoped token can name it. Without this the token request fails
// with "repository does not exist", which reads like a typo but is an access
// decision.
func (c *forgejoClient) EnsureCollaborator(ctx context.Context, repo Repo, account, permission string) error {
	path := "/api/v1/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name) +
		"/collaborators/" + url.PathEscape(account)
	body := map[string]any{"permission": permission}
	if err := c.do(ctx, http.MethodPut, path, body, nil, nil, http.StatusNoContent, http.StatusOK, http.StatusCreated); err != nil {
		return fmt.Errorf("add %q as a %s collaborator on %s: %w", account, permission, repo, err)
	}
	return nil
}

func (c *forgejoClient) AddKey(ctx context.Context, account, title, publicKey string) (ForgejoKey, error) {
	body := map[string]any{"title": title, "key": publicKey, "read_only": false}
	var key ForgejoKey
	path := "/api/v1/admin/users/" + url.PathEscape(account) + "/keys"
	if err := c.do(ctx, http.MethodPost, path, body, &key, nil, http.StatusCreated, http.StatusOK); err != nil {
		return ForgejoKey{}, fmt.Errorf("add SSH key for %q: %w", account, err)
	}
	return key, nil
}

func (c *forgejoClient) DeleteKey(ctx context.Context, account string, id int64) error {
	path := "/api/v1/admin/users/" + url.PathEscape(account) + "/keys/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
}

func (c *forgejoClient) ListSystemHooks(ctx context.Context) ([]ForgejoHook, error) {
	var hooks []ForgejoHook
	if err := c.do(ctx, http.MethodGet, "/api/v1/admin/hooks", nil, &hooks, nil, http.StatusOK); err != nil {
		return nil, fmt.Errorf("list system webhooks: %w", err)
	}
	return hooks, nil
}

func (c *forgejoClient) SystemHook(ctx context.Context, id int64) (ForgejoHook, error) {
	var hook ForgejoHook
	path := "/api/v1/admin/hooks/" + strconv.FormatInt(id, 10)
	if err := c.do(ctx, http.MethodGet, path, nil, &hook, nil, http.StatusOK); err != nil {
		return ForgejoHook{}, err
	}
	return hook, nil
}

func (c *forgejoClient) CreateSystemHook(ctx context.Context, hook ForgejoHookSpec) (ForgejoHook, error) {
	var created ForgejoHook
	if err := c.do(ctx, http.MethodPost, "/api/v1/admin/hooks", hookBody(hook), &created, nil, http.StatusCreated, http.StatusOK); err != nil {
		return ForgejoHook{}, fmt.Errorf("register system webhook: %w", err)
	}
	return created, nil
}

func (c *forgejoClient) UpdateSystemHook(ctx context.Context, id int64, hook ForgejoHookSpec) (ForgejoHook, error) {
	var updated ForgejoHook
	path := "/api/v1/admin/hooks/" + strconv.FormatInt(id, 10)
	if err := c.do(ctx, http.MethodPatch, path, hookBody(hook), &updated, nil, http.StatusOK); err != nil {
		return ForgejoHook{}, fmt.Errorf("update system webhook %d: %w", id, err)
	}
	return updated, nil
}

func (c *forgejoClient) DeleteSystemHook(ctx context.Context, id int64) error {
	path := "/api/v1/admin/hooks/" + strconv.FormatInt(id, 10)
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil, http.StatusNoContent, http.StatusOK, http.StatusNotFound)
}

// IssuesUpdatedSince is the reconciliation read. It asks Forgejo what changed
// rather than what was delivered, which is the only way to notice a delivery
// that never arrived (AGENT-R-013).
func (c *forgejoClient) IssuesUpdatedSince(ctx context.Context, repo Repo, since time.Time) ([]ForgejoIssue, error) {
	query := url.Values{"state": {"all"}, "limit": {"50"}}
	if !since.IsZero() {
		query.Set("since", since.UTC().Format(time.RFC3339))
	}
	path := "/api/v1/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name) +
		"/issues?" + query.Encode()
	var issues []ForgejoIssue
	if err := c.do(ctx, http.MethodGet, path, nil, &issues, nil, http.StatusOK); err != nil {
		return nil, fmt.Errorf("read issues updated since for %s: %w", repo, err)
	}
	return issues, nil
}

func hookBody(hook ForgejoHookSpec) map[string]any {
	return map[string]any{
		"type":   "forgejo",
		"active": hook.Active,
		"events": hook.Events,
		"config": map[string]string{
			"url": hook.URL, "content_type": "json", "secret": hook.Secret,
		},
	}
}

// statusError carries the HTTP status so callers can distinguish "absent" from
// "refused" without matching on message text.
type statusError struct {
	status int
	body   string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("Forgejo returned %d: %s", e.status, e.body)
}

func isStatus(err error, status int) bool {
	var target *statusError
	if ok := asStatusError(err, &target); !ok {
		return false
	}
	return target.status == status
}

func asStatusError(err error, target **statusError) bool {
	for err != nil {
		if candidate, ok := err.(*statusError); ok {
			*target = candidate
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

// sudo builds the header that makes an administrative call act as another
// account.
func sudo(account string) http.Header {
	return http.Header{"Sudo": []string{account}}
}

func (c *forgejoClient) do(ctx context.Context, method, path string, body any, output any, headers http.Header, accepted ...int) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.username, c.password)
	req.Header.Set("Accept", "application/json")
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The transport error can quote the request URL, and a URL can carry a
		// credential. Scrub before it reaches a log line.
		return fmt.Errorf("%s %s: %s", method, path, c.redactor.Error(err))
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	for _, code := range accepted {
		if resp.StatusCode != code {
			continue
		}
		if output == nil || len(payload) == 0 {
			return nil
		}
		if err := json.Unmarshal(payload, output); err != nil {
			return fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
		return nil
	}
	return &statusError{status: resp.StatusCode, body: c.redactor.String(strings.TrimSpace(string(payload)))}
}

// fingerprint is how a secret is recorded without being stored. Comparing
// fingerprints answers "is the registered hook still signing with the current
// key" without the answer itself becoming a place the key leaks from.
func fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
