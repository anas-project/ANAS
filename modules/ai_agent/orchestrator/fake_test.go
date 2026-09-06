package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// fakeAdmin is an in-memory Forgejo. It records every call so a test can assert
// on ordering -- which matters most for rotation, where the whole guarantee is
// about what happens before what.
type fakeAdmin struct {
	mu            sync.Mutex
	users         map[string]ForgejoUser
	tokens        map[string]map[int64]ForgejoToken
	keys          map[string]map[int64]ForgejoKey
	hooks         map[int64]ForgejoHook
	issues        map[string][]ForgejoIssue
	calls         []string
	nextID        int64
	failOn        map[string]error
	scopeOff      bool
	listHooks     bool
	collaborators map[string]string
}

func newFakeAdmin() *fakeAdmin {
	return &fakeAdmin{
		users: map[string]ForgejoUser{}, tokens: map[string]map[int64]ForgejoToken{},
		keys: map[string]map[int64]ForgejoKey{}, hooks: map[int64]ForgejoHook{},
		issues: map[string][]ForgejoIssue{}, failOn: map[string]error{},
	}
}

func (f *fakeAdmin) record(call string) { f.calls = append(f.calls, call) }

func (f *fakeAdmin) id() int64 {
	f.nextID++
	return f.nextID
}

func (f *fakeAdmin) EnsureUser(_ context.Context, account, email string) (ForgejoUser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("EnsureUser:" + account)
	if err := f.failOn["EnsureUser"]; err != nil {
		return ForgejoUser{}, err
	}
	if user, ok := f.users[account]; ok {
		return user, nil
	}
	user := ForgejoUser{ID: f.id(), Login: account}
	f.users[account] = user
	return user, nil
}

func (f *fakeAdmin) CreateToken(_ context.Context, account, name string, scopes, repositories []string) (ForgejoToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateToken:" + account)
	if err := f.failOn["CreateToken"]; err != nil {
		return ForgejoToken{}, err
	}
	id := f.id()
	// A Forgejo that ignores the repository restriction is the degradation this
	// module has to survive, so the fake can be told to behave that way.
	// Forgejo resolves the repository list in the acting account's own context,
	// so a repository the agent is not a collaborator on is reported as
	// non-existent. The fake reproduces that, which is what makes the missing
	// grant a test failure rather than a surprise in production.
	var stored []ForgejoTarget
	for _, name := range repositories {
		if f.collaborators[name+"|"+account] == "" {
			return ForgejoToken{}, fmt.Errorf("repository %s does not exist", name)
		}
		repo, err := ParseRepo(name)
		if err != nil {
			return ForgejoToken{}, err
		}
		stored = append(stored, ForgejoTarget{Owner: repo.Owner, Name: repo.Name})
	}
	if f.scopeOff {
		stored = nil
	}
	token := ForgejoToken{
		ID: id, Name: name, Token: "token-value-" + strconv.FormatInt(id, 10),
		Scopes: scopes, Repositories: stored,
	}
	if f.tokens[account] == nil {
		f.tokens[account] = map[int64]ForgejoToken{}
	}
	f.tokens[account][id] = token
	return token, nil
}

func (f *fakeAdmin) ListTokens(_ context.Context, account string) ([]ForgejoToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ForgejoToken
	for _, token := range f.tokens[account] {
		out = append(out, token)
	}
	return out, nil
}

func (f *fakeAdmin) DeleteToken(_ context.Context, account string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteToken:" + account + ":" + strconv.FormatInt(id, 10))
	if err := f.failOn["DeleteToken"]; err != nil {
		return err
	}
	delete(f.tokens[account], id)
	return nil
}

func (f *fakeAdmin) EnsureCollaborator(_ context.Context, repo Repo, account, permission string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("EnsureCollaborator:" + repo.String() + ":" + account + ":" + permission)
	if err := f.failOn["EnsureCollaborator"]; err != nil {
		return err
	}
	if f.collaborators == nil {
		f.collaborators = map[string]string{}
	}
	f.collaborators[repo.String()+"|"+account] = permission
	return nil
}

func (f *fakeAdmin) AddKey(_ context.Context, account, title, _ string) (ForgejoKey, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("AddKey:" + account)
	if err := f.failOn["AddKey"]; err != nil {
		return ForgejoKey{}, err
	}
	for _, existing := range f.keys[account] {
		if existing.Title == title {
			return ForgejoKey{}, fmt.Errorf("key title has been used already")
		}
	}
	id := f.id()
	if f.keys[account] == nil {
		f.keys[account] = map[int64]ForgejoKey{}
	}
	f.keys[account][id] = ForgejoKey{ID: id, Title: title}
	return f.keys[account][id], nil
}

func (f *fakeAdmin) DeleteKey(_ context.Context, account string, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("DeleteKey:" + account + ":" + strconv.FormatInt(id, 10))
	if err := f.failOn["DeleteKey"]; err != nil {
		return err
	}
	delete(f.keys[account], id)
	return nil
}

// ListSystemHooks reproduces the pinned Forgejo's behaviour by default: it
// answers with an empty array even when hooks exist. Setting listHooks makes it
// report them, which is how the adoption path is exercised.
func (f *fakeAdmin) ListSystemHooks(context.Context) ([]ForgejoHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("ListSystemHooks")
	if !f.listHooks {
		return nil, nil
	}
	var out []ForgejoHook
	for _, hook := range f.hooks {
		out = append(out, hook)
	}
	return out, nil
}

func (f *fakeAdmin) SystemHook(_ context.Context, id int64) (ForgejoHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("SystemHook:" + strconv.FormatInt(id, 10))
	hook, ok := f.hooks[id]
	if !ok {
		return ForgejoHook{}, &statusError{status: http.StatusNotFound, body: "no such hook"}
	}
	return hook, nil
}

func (f *fakeAdmin) CreateSystemHook(_ context.Context, spec ForgejoHookSpec) (ForgejoHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("CreateSystemHook")
	hook := ForgejoHook{
		ID: f.id(), Type: "forgejo", Events: expandEvents(spec.Events), Active: spec.Active,
		Config: map[string]string{"url": spec.URL, "secret": spec.Secret},
	}
	f.hooks[hook.ID] = hook
	return hook, nil
}

func (f *fakeAdmin) UpdateSystemHook(_ context.Context, id int64, spec ForgejoHookSpec) (ForgejoHook, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("UpdateSystemHook:" + strconv.FormatInt(id, 10))
	hook, ok := f.hooks[id]
	if !ok {
		return ForgejoHook{}, &statusError{status: http.StatusNotFound, body: "no such hook"}
	}
	hook.Events, hook.Active = expandEvents(spec.Events), spec.Active
	hook.Config = map[string]string{"url": spec.URL, "secret": spec.Secret}
	f.hooks[id] = hook
	return hook, nil
}

func (f *fakeAdmin) DeleteSystemHook(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hooks, id)
	return nil
}

func (f *fakeAdmin) IssuesUpdatedSince(_ context.Context, repo Repo, since time.Time) ([]ForgejoIssue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("IssuesUpdatedSince:" + repo.String())
	if err := f.failOn["IssuesUpdatedSince"]; err != nil {
		return nil, err
	}
	var out []ForgejoIssue
	for _, issue := range f.issues[repo.String()] {
		if since.IsZero() || issue.Updated.After(since) {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (f *fakeAdmin) addIssue(repo Repo, number int, author string, updated time.Time) {
	issue := ForgejoIssue{Number: number, Updated: updated, State: "open"}
	issue.User.Login = author
	f.issues[repo.String()] = append(f.issues[repo.String()], issue)
}

// expandEvents mirrors what the pinned Forgejo does to a requested event list:
// asking for `issues` stores the whole issue family. Anything that compared the
// stored list with the requested one would see permanent drift.
func expandEvents(requested []string) []string {
	expanded := append([]string(nil), requested...)
	for _, event := range requested {
		if event == "issues" {
			expanded = append(expanded, "issue_milestone")
		}
	}
	return expanded
}

func (f *fakeAdmin) hookSecret(id int64) string { return f.hooks[id].Config["secret"] }

func (f *fakeAdmin) liveTokens(account string) int { return len(f.tokens[account]) }
func (f *fakeAdmin) liveKeys(account string) int   { return len(f.keys[account]) }

func (f *fakeAdmin) calledInOrder(want ...string) error {
	index := 0
	for _, call := range f.calls {
		if index < len(want) && call == want[index] {
			index++
		}
	}
	if index != len(want) {
		return fmt.Errorf("calls %v do not contain %v in order", f.calls, want)
	}
	return nil
}
