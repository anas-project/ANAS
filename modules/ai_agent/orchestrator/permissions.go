package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// forgejoPermissions answers the two questions the policy engine asks Forgejo.
type forgejoPermissions struct{ *forgejoClient }

// NewPermissionReader builds the reader. It signs in with the administrator's
// credential because one of its two calls -- reading another person's team
// membership -- is only available by acting as that person through Sudo.
func NewPermissionReader(baseURL, username, password string, redactor *Redactor) PermissionReader {
	return forgejoPermissions{&forgejoClient{
		baseURL: strings.TrimRight(baseURL, "/"), username: username, password: password,
		redactor: redactor, http: defaultHTTPClient(),
	}}
}

// RepoPermission asks what a person holds on a repository. A person who is not
// a collaborator is reported as "none" by Forgejo rather than as an error, so
// there is no absent case to guess at here.
func (p forgejoPermissions) RepoPermission(ctx context.Context, repo Repo, user string) (RepoPermission, error) {
	var answer struct {
		Permission string `json:"permission"`
		RoleName   string `json:"role_name"`
	}
	path := "/api/v1/repos/" + url.PathEscape(repo.Owner) + "/" + url.PathEscape(repo.Name) +
		"/collaborators/" + url.PathEscape(user) + "/permission"
	if err := p.do(ctx, http.MethodGet, path, nil, &answer, nil, http.StatusOK); err != nil {
		if isStatus(err, http.StatusNotFound) {
			// The repository or the account is gone. Failing closed is the only
			// safe reading: an absent answer is not permission.
			return PermissionNone, nil
		}
		return "", fmt.Errorf("read the permission of %q on %s: %w", user, repo, err)
	}
	switch RepoPermission(answer.Permission) {
	case PermissionNone, PermissionRead, PermissionWrite, PermissionAdmin, PermissionOwner:
		return RepoPermission(answer.Permission), nil
	}
	// An unrecognised level grants nothing. A new upstream value must not be
	// read as more access than the ones already known.
	return PermissionNone, nil
}

// UserTeams lists the Forgejo teams a person belongs to, which is where the
// directory's capability groups arrive after the identity provider projects
// them. It acts as that person through Sudo: one call answers the whole
// question, where checking each team's membership would be one call per team.
func (p forgejoPermissions) UserTeams(ctx context.Context, user string) ([]string, error) {
	var teams []struct {
		Name         string `json:"name"`
		Organization struct {
			Name string `json:"username"`
		} `json:"organization"`
	}
	if err := p.do(ctx, http.MethodGet, "/api/v1/user/teams?limit=200", nil, &teams,
		sudo(user), http.StatusOK); err != nil {
		return nil, fmt.Errorf("read the teams of %q: %w", user, err)
	}
	names := make([]string, 0, len(teams))
	for _, team := range teams {
		names = append(names, team.Name)
	}
	return names, nil
}
