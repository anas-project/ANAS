package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ForgejoIssues is the collaboration surface: everything the orchestrator does
// inside an issue or a repository's files. It is separate from ForgejoAdmin
// because the two are used under different credentials -- the admin password
// for identity work, an agent's scoped token for issue work -- and keeping them
// in one interface would make it easy to reach for the wrong one.
type ForgejoIssues interface {
	Issue(ctx context.Context, repo Repo, number int) (Issue, error)
	CreateIssue(ctx context.Context, repo Repo, title, body string, labels []int64) (Issue, error)
	AssignIssue(ctx context.Context, repo Repo, number int, assignees []string) error
	SetLabels(ctx context.Context, repo Repo, number int, labelIDs []int64) error
	AddLabels(ctx context.Context, repo Repo, number int, labelIDs []int64) error
	RemoveLabel(ctx context.Context, repo Repo, number int, labelID int64) error

	Comments(ctx context.Context, repo Repo, number int) ([]Comment, error)
	CreateComment(ctx context.Context, repo Repo, number int, body string) (Comment, error)
	EditComment(ctx context.Context, repo Repo, commentID int64, body string) (Comment, error)
	React(ctx context.Context, repo Repo, commentID int64, content string) error

	RepoLabels(ctx context.Context, repo Repo) ([]RepoLabel, error)
	CreateLabel(ctx context.Context, repo Repo, label Label) (RepoLabel, error)
	UpdateLabel(ctx context.Context, repo Repo, id int64, label Label) (RepoLabel, error)

	EditIssueBody(ctx context.Context, repo Repo, number int, body string) (Issue, error)
	SetDueDate(ctx context.Context, repo Repo, number int, due time.Time) error
	AddDependency(ctx context.Context, repo Repo, number int, blocker Repo, blockerNumber int) error
	TrackTime(ctx context.Context, repo Repo, number int, spent time.Duration, user string) error
	PinIssue(ctx context.Context, repo Repo, number int) error
	PinnedIssues(ctx context.Context, repo Repo) ([]Issue, error)
	IssuesByLabel(ctx context.Context, repo Repo, label string) ([]Issue, error)
	NewPinAllowed(ctx context.Context, repo Repo) (bool, error)

	Repository(ctx context.Context, repo Repo) (Repository, error)
	FileContents(ctx context.Context, repo Repo, path, ref string) (FileContent, error)
	PutFile(ctx context.Context, repo Repo, request FileWrite) (FileCommit, error)
	CreatePullRequest(ctx context.Context, repo Repo, head, base, title, body string) (PullRequest, error)
}

type Issue struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels    []RepoLabel `json:"labels"`
	Assignees []struct {
		Login string `json:"login"`
	} `json:"assignees"`
	Updated time.Time `json:"updated_at"`
}

// LabelNames is what the state machine reads; the ids are only needed to write.
func (i Issue) LabelNames() []string {
	names := make([]string, 0, len(i.Labels))
	for _, label := range i.Labels {
		names = append(names, label.Name)
	}
	return names
}

type Comment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
	} `json:"user"`
	Created time.Time `json:"created_at"`
	Updated time.Time `json:"updated_at"`
}

type RepoLabel struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

type Repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Empty         bool   `json:"empty"`
	Private       bool   `json:"private"`
}

type FileContent struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA      string `json:"sha"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// Decoded returns the file's bytes. Forgejo base64-encodes content, and a
// caller that forgot would be comparing an encoding to a document.
func (f FileContent) Decoded() (string, error) {
	if f.Encoding != "" && f.Encoding != "base64" {
		return "", fmt.Errorf("unsupported content encoding %q", f.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// FileWrite is one file committed through the contents API. SHA is the blob
// the write is expected to replace: sending it turns a concurrent edit into a
// conflict instead of a silent overwrite.
type FileWrite struct {
	Path      string
	Content   string
	Message   string
	Branch    string
	NewBranch string
	SHA       string
	Author    string
	Email     string
}

type FileCommit struct {
	Content FileContent `json:"content"`
	Commit  struct {
		SHA string `json:"sha"`
		URL string `json:"url"`
	} `json:"commit"`
}

type PullRequest struct {
	ID     int64  `json:"id"`
	Number int    `json:"number"`
	URL    string `json:"html_url"`
	State  string `json:"state"`
}

// issuesClient reuses the admin client's transport but is constructed with the
// credential appropriate to the caller.
type issuesClient struct{ *forgejoClient }

// NewForgejoIssues builds the collaboration client. Token authentication is the
// normal mode here: unlike token creation, every call below accepts it.
func NewForgejoIssues(baseURL, token string, redactor *Redactor) ForgejoIssues {
	client := &forgejoClient{
		baseURL: strings.TrimRight(baseURL, "/"), token: token,
		redactor: redactor, http: defaultHTTPClient(),
	}
	return issuesClient{client}
}

func (c issuesClient) repoPath(repo Repo, suffix string) string {
	return "/api/v1/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name) + suffix
}

func (c issuesClient) Issue(ctx context.Context, repo Repo, number int) (Issue, error) {
	var issue Issue
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number))
	if err := c.do(ctx, http.MethodGet, path, nil, &issue, nil, http.StatusOK); err != nil {
		return Issue{}, fmt.Errorf("read %s#%d: %w", repo, number, err)
	}
	return issue, nil
}

func (c issuesClient) CreateIssue(ctx context.Context, repo Repo, title, body string, labels []int64) (Issue, error) {
	request := map[string]any{"title": title, "body": body}
	if len(labels) > 0 {
		request["labels"] = labels
	}
	var issue Issue
	if err := c.do(ctx, http.MethodPost, c.repoPath(repo, "/issues"), request, &issue, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return Issue{}, fmt.Errorf("create an issue in %s: %w", repo, err)
	}
	return issue, nil
}

// AssignIssue exists because an issue form cannot assign: its front matter
// carries labels but no assignees, so the orchestrator does it by API after the
// issue is created (AGENT-R-018).
func (c issuesClient) AssignIssue(ctx context.Context, repo Repo, number int, assignees []string) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number))
	body := map[string]any{"assignees": assignees}
	return c.do(ctx, http.MethodPatch, path, body, nil, nil, http.StatusCreated, http.StatusOK)
}

func (c issuesClient) EditIssueBody(ctx context.Context, repo Repo, number int, body string) (Issue, error) {
	var issue Issue
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number))
	if err := c.do(ctx, http.MethodPatch, path, map[string]any{"body": body}, &issue, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return Issue{}, fmt.Errorf("update the body of %s#%d: %w", repo, number, err)
	}
	return issue, nil
}

// SetDueDate writes the issue's own deadline. The deadline is upstream's field
// rather than one of this module's: a person editing it in the UI and an agent
// setting it with /due are changing the same thing.
func (c issuesClient) SetDueDate(ctx context.Context, repo Repo, number int, due time.Time) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/deadline")
	body := map[string]any{"due_date": due.UTC().Format(time.RFC3339)}
	return c.do(ctx, http.MethodPost, path, body, nil, nil, http.StatusCreated, http.StatusOK)
}

// AddDependency records that an issue is blocked by another. The hard order in
// the queue is written here as well as kept in the database, so "waiting for
// #12" is visible on the issue itself and not only in the queue overview.
//
// The payload is an IssueMeta: sending only the index is answered with
// "repository does not exist", which reads like a server fault and is not one.
//
// A dependency that is already recorded comes back as a 500 rather than a
// conflict, and every reconciliation sweep re-asserts the same ordering -- so
// that one case is treated as success. It is matched on the message and not on
// the status, because a blanket "ignore 500" would swallow real faults.
func (c issuesClient) AddDependency(ctx context.Context, repo Repo, number int, blocker Repo, blockerNumber int) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/dependencies")
	body := map[string]any{"owner": blocker.Owner, "repo": blocker.Name, "index": blockerNumber}
	err := c.do(ctx, http.MethodPost, path, body, nil, nil,
		http.StatusCreated, http.StatusOK, http.StatusConflict)
	if err != nil && isAlreadyDependent(err) {
		return nil
	}
	return err
}

// isAlreadyDependent recognises the one upstream refusal that means the desired
// state is already in place.
func isAlreadyDependent(err error) bool {
	var status *statusError
	if !asStatusError(err, &status) {
		return false
	}
	return strings.Contains(status.body, "dependency does already exist")
}

// TrackTime writes the actual duration into Forgejo's own time tracking, so the
// effort an agent spent appears in the same reports as everyone else's rather
// than only inside this module (AGENT-R-050).
func (c issuesClient) TrackTime(ctx context.Context, repo Repo, number int, spent time.Duration, user string) error {
	seconds := int64(spent.Round(time.Second) / time.Second)
	if seconds <= 0 {
		// Forgejo rejects a non-positive duration, and a job that took under a
		// second is not worth a time entry anyway.
		return nil
	}
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/times")
	body := map[string]any{"time": seconds}
	if user != "" {
		body["user_name"] = user
	}
	return c.do(ctx, http.MethodPost, path, body, nil, nil, http.StatusOK, http.StatusCreated)
}

func (c issuesClient) PinIssue(ctx context.Context, repo Repo, number int) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/pin")
	return c.do(ctx, http.MethodPost, path, nil, nil, nil,
		http.StatusNoContent, http.StatusOK, http.StatusCreated)
}

func (c issuesClient) PinnedIssues(ctx context.Context, repo Repo) ([]Issue, error) {
	var issues []Issue
	if err := c.do(ctx, http.MethodGet, c.repoPath(repo, "/issues/pinned"), nil, &issues, nil,
		http.StatusOK); err != nil {
		return nil, fmt.Errorf("read the pinned issues of %s: %w", repo, err)
	}
	return issues, nil
}

// IssuesByLabel finds the issues carrying a label. It is how an issue this
// module owns is recovered when its number is not held anywhere else and it is
// not pinned -- which is precisely the degraded case, so recovery cannot depend
// on the pin.
func (c issuesClient) IssuesByLabel(ctx context.Context, repo Repo, label string) ([]Issue, error) {
	query := url.Values{"labels": {label}, "state": {"all"}, "limit": {"50"}}
	var issues []Issue
	if err := c.do(ctx, http.MethodGet, c.repoPath(repo, "/issues?"+query.Encode()), nil, &issues, nil,
		http.StatusOK); err != nil {
		return nil, fmt.Errorf("find the issues of %s labelled %q: %w", repo, label, err)
	}
	return issues, nil
}

func (c issuesClient) NewPinAllowed(ctx context.Context, repo Repo) (bool, error) {
	var answer struct {
		Issues bool `json:"issues"`
	}
	if err := c.do(ctx, http.MethodGet, c.repoPath(repo, "/new_pin_allowed"), nil, &answer, nil,
		http.StatusOK); err != nil {
		return false, err
	}
	return answer.Issues, nil
}

func (c issuesClient) SetLabels(ctx context.Context, repo Repo, number int, labelIDs []int64) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/labels")
	return c.do(ctx, http.MethodPut, path, map[string]any{"labels": labelIDs}, nil, nil,
		http.StatusOK, http.StatusCreated)
}

func (c issuesClient) AddLabels(ctx context.Context, repo Repo, number int, labelIDs []int64) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/labels")
	return c.do(ctx, http.MethodPost, path, map[string]any{"labels": labelIDs}, nil, nil,
		http.StatusOK, http.StatusCreated)
}

func (c issuesClient) RemoveLabel(ctx context.Context, repo Repo, number int, labelID int64) error {
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/labels/"+strconv.FormatInt(labelID, 10))
	return c.do(ctx, http.MethodDelete, path, nil, nil, nil,
		http.StatusNoContent, http.StatusOK, http.StatusNotFound)
}

func (c issuesClient) Comments(ctx context.Context, repo Repo, number int) ([]Comment, error) {
	var comments []Comment
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/comments")
	if err := c.do(ctx, http.MethodGet, path, nil, &comments, nil, http.StatusOK); err != nil {
		return nil, fmt.Errorf("read comments on %s#%d: %w", repo, number, err)
	}
	return comments, nil
}

func (c issuesClient) CreateComment(ctx context.Context, repo Repo, number int, body string) (Comment, error) {
	var comment Comment
	path := c.repoPath(repo, "/issues/"+strconv.Itoa(number)+"/comments")
	if err := c.do(ctx, http.MethodPost, path, map[string]any{"body": body}, &comment, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return Comment{}, fmt.Errorf("comment on %s#%d: %w", repo, number, err)
	}
	return comment, nil
}

// EditComment is what makes a single status comment possible: it is updated in
// place forever rather than reposted (AGENT-R-019).
func (c issuesClient) EditComment(ctx context.Context, repo Repo, commentID int64, body string) (Comment, error) {
	var comment Comment
	path := c.repoPath(repo, "/issues/comments/"+strconv.FormatInt(commentID, 10))
	if err := c.do(ctx, http.MethodPatch, path, map[string]any{"body": body}, &comment, nil,
		http.StatusOK); err != nil {
		return Comment{}, fmt.Errorf("update comment %d: %w", commentID, err)
	}
	return comment, nil
}

// React is the lightweight acknowledgement. Receiving an instruction with
// nothing to say yet is answered with a reaction, never with a placeholder
// comment that would later have to be deleted or rewritten (AGENT-R-019).
func (c issuesClient) React(ctx context.Context, repo Repo, commentID int64, content string) error {
	path := c.repoPath(repo, "/issues/comments/"+strconv.FormatInt(commentID, 10)+"/reactions")
	return c.do(ctx, http.MethodPost, path, map[string]any{"content": content}, nil, nil,
		http.StatusCreated, http.StatusOK)
}

func (c issuesClient) RepoLabels(ctx context.Context, repo Repo) ([]RepoLabel, error) {
	var labels []RepoLabel
	if err := c.do(ctx, http.MethodGet, c.repoPath(repo, "/labels?limit=200"), nil, &labels, nil,
		http.StatusOK); err != nil {
		return nil, fmt.Errorf("read labels of %s: %w", repo, err)
	}
	return labels, nil
}

func (c issuesClient) CreateLabel(ctx context.Context, repo Repo, label Label) (RepoLabel, error) {
	var created RepoLabel
	body := map[string]any{"name": label.Name, "color": label.Color, "description": label.Description}
	if err := c.do(ctx, http.MethodPost, c.repoPath(repo, "/labels"), body, &created, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return RepoLabel{}, fmt.Errorf("create label %q in %s: %w", label.Name, repo, err)
	}
	return created, nil
}

func (c issuesClient) UpdateLabel(ctx context.Context, repo Repo, id int64, label Label) (RepoLabel, error) {
	var updated RepoLabel
	body := map[string]any{"name": label.Name, "color": label.Color, "description": label.Description}
	path := c.repoPath(repo, "/labels/"+strconv.FormatInt(id, 10))
	if err := c.do(ctx, http.MethodPatch, path, body, &updated, nil, http.StatusOK); err != nil {
		return RepoLabel{}, fmt.Errorf("update label %q in %s: %w", label.Name, repo, err)
	}
	return updated, nil
}

func (c issuesClient) Repository(ctx context.Context, repo Repo) (Repository, error) {
	var repository Repository
	if err := c.do(ctx, http.MethodGet, c.repoPath(repo, ""), nil, &repository, nil,
		http.StatusOK); err != nil {
		return Repository{}, fmt.Errorf("read repository %s: %w", repo, err)
	}
	return repository, nil
}

func (c issuesClient) FileContents(ctx context.Context, repo Repo, path, ref string) (FileContent, error) {
	query := ""
	if ref != "" {
		query = "?ref=" + url.QueryEscape(ref)
	}
	var content FileContent
	endpoint := c.repoPath(repo, "/contents/"+escapePath(path)) + query
	if err := c.do(ctx, http.MethodGet, endpoint, nil, &content, nil, http.StatusOK); err != nil {
		return FileContent{}, err
	}
	return content, nil
}

// PutFile commits one file. It is a PUT when replacing a known blob and a POST
// when creating, because Forgejo distinguishes the two and answering the wrong
// one turns a legitimate update into a "already exists".
func (c issuesClient) PutFile(ctx context.Context, repo Repo, request FileWrite) (FileCommit, error) {
	body := map[string]any{
		"content": base64.StdEncoding.EncodeToString([]byte(request.Content)),
		"message": request.Message,
	}
	if request.Branch != "" {
		body["branch"] = request.Branch
	}
	if request.NewBranch != "" {
		body["new_branch"] = request.NewBranch
	}
	if request.Author != "" {
		identity := map[string]any{"name": request.Author, "email": request.Email}
		body["author"], body["committer"] = identity, identity
	}
	method := http.MethodPost
	if request.SHA != "" {
		body["sha"], method = request.SHA, http.MethodPut
	}
	var commit FileCommit
	endpoint := c.repoPath(repo, "/contents/"+escapePath(request.Path))
	if err := c.do(ctx, method, endpoint, body, &commit, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return FileCommit{}, fmt.Errorf("commit %s to %s: %w", request.Path, repo, err)
	}
	return commit, nil
}

func (c issuesClient) CreatePullRequest(ctx context.Context, repo Repo, head, base, title, body string) (PullRequest, error) {
	request := map[string]any{"head": head, "base": base, "title": title, "body": body}
	var pull PullRequest
	if err := c.do(ctx, http.MethodPost, c.repoPath(repo, "/pulls"), request, &pull, nil,
		http.StatusCreated, http.StatusOK); err != nil {
		return PullRequest{}, fmt.Errorf("open a pull request in %s: %w", repo, err)
	}
	return pull, nil
}

// escapePath encodes a repository path for the contents API. Each segment is
// escaped separately: escaping the whole path would turn its separators into
// %2F and address a file whose name contains slashes.
func escapePath(path string) string {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}
