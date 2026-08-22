package main

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

var forgejoNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Scope struct {
	Owner string
	Repo  string
}

func ParseScopes(value string) ([]Scope, error) {
	seen := map[string]bool{}
	out := []Scope{}
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		parts := strings.Split(raw, "/")
		if len(parts) > 2 || !forgejoNamePattern.MatchString(parts[0]) {
			return nil, fmt.Errorf("Actions scope %q must be {owner} or {owner}/{repo}", raw)
		}
		scope := Scope{Owner: parts[0]}
		if len(parts) == 2 {
			if !forgejoNamePattern.MatchString(parts[1]) {
				return nil, fmt.Errorf("Actions scope %q must be {owner} or {owner}/{repo}", raw)
			}
			scope.Repo = parts[1]
		}
		if seen[scope.String()] {
			return nil, fmt.Errorf("Actions scope %q is duplicated", raw)
		}
		seen[scope.String()] = true
		out = append(out, scope)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

func (s Scope) String() string {
	if s.Repo == "" {
		return s.Owner
	}
	return s.Owner + "/" + s.Repo
}

func (s Scope) runnersPath(suffix string) string {
	owner := url.PathEscape(s.Owner)
	if s.Repo == "" {
		return "/api/v1/orgs/" + owner + "/actions/runners" + suffix
	}
	return "/api/v1/repos/" + owner + "/" + url.PathEscape(s.Repo) + "/actions/runners" + suffix
}
