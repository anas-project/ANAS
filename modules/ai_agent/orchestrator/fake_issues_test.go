package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// fakeIssues is an in-memory repository. It reproduces the upstream behaviours
// the design leans on -- a comment can be edited in place, a file write needs
// the blob it replaces, a missing file is a 404 -- so a test that passes here
// is testing the same rules the server enforces.
type fakeIssues struct {
	mu        sync.Mutex
	issues    map[int]*Issue
	comments  map[int64]*Comment
	byIssue   map[int][]int64
	labels    map[string]RepoLabel
	files     map[string]string
	pulls     []PullRequest
	branches  map[string]bool
	calls     []string
	nextID    int64
	repo      Repository
	failOn    map[string]error
	reactions map[int64][]string
}

func newFakeIssues() *fakeIssues {
	return &fakeIssues{
		issues: map[int]*Issue{}, comments: map[int64]*Comment{}, byIssue: map[int][]int64{},
		labels: map[string]RepoLabel{}, files: map[string]string{}, branches: map[string]bool{"main": true},
		failOn: map[string]error{}, reactions: map[int64][]string{},
		repo: Repository{FullName: "anas-project/ANAS", DefaultBranch: "main"},
	}
}

func (f *fakeIssues) id() int64 { f.nextID++; return f.nextID }

func (f *fakeIssues) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeIssues) seedIssue(number int, author, body string) *Issue {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue := &Issue{ID: f.id(), Number: number, Body: body, State: "open"}
	issue.User.Login = author
	f.issues[number] = issue
	return issue
}

func (f *fakeIssues) seedFile(path, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = content
}

func (f *fakeIssues) Issue(_ context.Context, _ Repo, number int) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	issue, ok := f.issues[number]
	if !ok {
		return Issue{}, &statusError{status: http.StatusNotFound, body: "no such issue"}
	}
	return *issue, nil
}

func (f *fakeIssues) CreateIssue(_ context.Context, _ Repo, title, body string, _ []int64) (Issue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateIssue")
	number := len(f.issues) + 1
	issue := &Issue{ID: f.id(), Number: number, Title: title, Body: body, State: "open"}
	f.issues[number] = issue
	return *issue, nil
}

func (f *fakeIssues) AssignIssue(_ context.Context, _ Repo, number int, assignees []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("AssignIssue:" + strings.Join(assignees, ","))
	issue, ok := f.issues[number]
	if !ok {
		return &statusError{status: http.StatusNotFound, body: "no such issue"}
	}
	issue.Assignees = nil
	for _, assignee := range assignees {
		entry := struct {
			Login string `json:"login"`
		}{Login: assignee}
		issue.Assignees = append(issue.Assignees, entry)
	}
	return nil
}

func (f *fakeIssues) SetLabels(ctx context.Context, repo Repo, number int, ids []int64) error {
	f.mu.Lock()
	f.issues[number].Labels = nil
	f.mu.Unlock()
	return f.AddLabels(ctx, repo, number, ids)
}

func (f *fakeIssues) AddLabels(_ context.Context, _ Repo, number int, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("AddLabels")
	issue, ok := f.issues[number]
	if !ok {
		return &statusError{status: http.StatusNotFound, body: "no such issue"}
	}
	for _, id := range ids {
		for _, label := range f.labels {
			if label.ID == id {
				issue.Labels = append(issue.Labels, label)
			}
		}
	}
	return nil
}

func (f *fakeIssues) RemoveLabel(_ context.Context, _ Repo, number int, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("RemoveLabel")
	issue, ok := f.issues[number]
	if !ok {
		return nil
	}
	var kept []RepoLabel
	for _, label := range issue.Labels {
		if label.ID != id {
			kept = append(kept, label)
		}
	}
	issue.Labels = kept
	return nil
}

func (f *fakeIssues) Comments(_ context.Context, _ Repo, number int) ([]Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Comment
	for _, id := range f.byIssue[number] {
		out = append(out, *f.comments[id])
	}
	return out, nil
}

func (f *fakeIssues) CreateComment(_ context.Context, _ Repo, number int, body string) (Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateComment")
	if err := f.failOn["CreateComment"]; err != nil {
		return Comment{}, err
	}
	comment := &Comment{ID: f.id(), Body: body, Created: time.Now().UTC()}
	comment.User.Login = "agent-codex"
	f.comments[comment.ID] = comment
	f.byIssue[number] = append(f.byIssue[number], comment.ID)
	return *comment, nil
}

func (f *fakeIssues) EditComment(_ context.Context, _ Repo, id int64, body string) (Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("EditComment")
	comment, ok := f.comments[id]
	if !ok {
		return Comment{}, &statusError{status: http.StatusNotFound, body: "no such comment"}
	}
	comment.Body, comment.Updated = body, time.Now().UTC()
	return *comment, nil
}

func (f *fakeIssues) React(_ context.Context, _ Repo, id int64, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("React:" + content)
	f.reactions[id] = append(f.reactions[id], content)
	return nil
}

func (f *fakeIssues) RepoLabels(context.Context, Repo) ([]RepoLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RepoLabel
	for _, label := range f.labels {
		out = append(out, label)
	}
	return out, nil
}

func (f *fakeIssues) CreateLabel(_ context.Context, _ Repo, label Label) (RepoLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateLabel:" + label.Name)
	created := RepoLabel{ID: f.id(), Name: label.Name,
		Color: strings.TrimPrefix(label.Color, "#"), Description: label.Description}
	f.labels[label.Name] = created
	return created, nil
}

func (f *fakeIssues) UpdateLabel(_ context.Context, _ Repo, id int64, label Label) (RepoLabel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateLabel:" + label.Name)
	updated := RepoLabel{ID: id, Name: label.Name,
		Color: strings.TrimPrefix(label.Color, "#"), Description: label.Description}
	f.labels[label.Name] = updated
	return updated, nil
}

func (f *fakeIssues) Repository(context.Context, Repo) (Repository, error) {
	return f.repo, nil
}

func (f *fakeIssues) FileContents(_ context.Context, _ Repo, path, _ string) (FileContent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.files[strings.TrimPrefix(path, "/")]
	if !ok {
		return FileContent{}, &statusError{status: http.StatusNotFound, body: "no such file"}
	}
	return FileContent{
		Path: strings.TrimPrefix(path, "/"), SHA: fingerprint(content)[:40],
		Content: base64.StdEncoding.EncodeToString([]byte(content)), Encoding: "base64",
	}, nil
}

func (f *fakeIssues) PutFile(_ context.Context, _ Repo, request FileWrite) (FileCommit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("PutFile:" + request.Path)
	if err := f.failOn["PutFile"]; err != nil {
		return FileCommit{}, err
	}
	existing, present := f.files[request.Path]
	// Forgejo refuses a create over an existing file, and refuses an update
	// whose SHA does not match the blob it is replacing.
	if present && request.SHA == "" {
		return FileCommit{}, &statusError{status: http.StatusUnprocessableEntity, body: "file already exists"}
	}
	if present && request.SHA != fingerprint(existing)[:40] {
		return FileCommit{}, &statusError{status: http.StatusConflict, body: "sha does not match"}
	}
	if request.NewBranch != "" {
		f.branches[request.NewBranch] = true
	}
	f.files[request.Path] = request.Content
	var commit FileCommit
	commit.Commit.SHA = fingerprint(request.Path + request.Content)
	commit.Content = FileContent{Path: request.Path, SHA: fingerprint(request.Content)[:40]}
	return commit, nil
}

func (f *fakeIssues) CreatePullRequest(_ context.Context, _ Repo, head, base, title, _ string) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreatePullRequest:" + head)
	if !f.branches[head] {
		return PullRequest{}, &statusError{status: http.StatusUnprocessableEntity,
			body: fmt.Sprintf("head branch %q does not exist", head)}
	}
	pull := PullRequest{ID: f.id(), Number: len(f.pulls) + 1, State: "open",
		URL: "https://git.example/anas-project/ANAS/pulls/" + itoa(len(f.pulls)+1)}
	f.pulls = append(f.pulls, pull)
	return pull, nil
}

func (f *fakeIssues) countCalls(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}
