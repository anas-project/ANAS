package runner

import (
	"fmt"

	"github.com/anas-project/ANAS/internal/runtimeissues"
)

// runIssues is `anas issues` (ISSUE-R-006): it reads the workspace and host
// stores directly, so it works with anasd stopped. Like `backup verify` it is
// meant for scripts and cron: an open error, or a store it cannot read, is
// ok: false and exit 1 (ISSUE-R-008).
func runIssues(args []string, jsonMode bool) error {
	workspace, _, rest, err := parseBaseArgs("issues", args)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return usageErrorf("usage: anas issues [-w <workspace>] [--json]")
	}
	type source struct {
		Scope  string                `json:"scope"`
		Path   string                `json:"path"`
		Error  string                `json:"error,omitempty"`
		Issues []runtimeissues.Issue `json:"issues"`
	}
	sources := []source{
		{Scope: "workspace", Path: runtimeissues.WorkspacePath(workspace)},
		{Scope: "host", Path: issuesHostPath},
	}
	openErrors, unreadable := 0, 0
	for i := range sources {
		issues, err := runtimeissues.Load(sources[i].Path)
		if err != nil {
			sources[i].Error, sources[i].Issues = err.Error(), []runtimeissues.Issue{}
			unreadable++
			continue
		}
		sources[i].Issues = issues
		for _, issue := range issues {
			if issue.Open() && issue.Level == runtimeissues.LevelError {
				openErrors++
			}
		}
	}
	healthy := openErrors == 0 && unreadable == 0
	if jsonMode {
		if err := emitJSON(map[string]any{
			"api_version": cliAPIVersion, "ok": healthy, "workspace": workspace,
			"sources": sources, "open_errors": openErrors,
		}); err != nil {
			return err
		}
	} else {
		for _, s := range sources {
			if s.Error != "" {
				fmt.Printf("%s\tunreadable\t%s\n", s.Scope, s.Error)
				continue
			}
			for _, issue := range s.Issues {
				state := "open"
				if !issue.Open() {
					state = "resolved"
				}
				fmt.Printf("%s\t%s\t%s\t%s\t%d\t%s\n", s.Scope, state, issue.Level, issue.Key, issue.Count, issue.Message)
			}
		}
	}
	switch {
	case unreadable > 0:
		return &CLIError{Code: "issues_unreadable", Message: fmt.Sprintf("%d runtime issue store(s) could not be read", unreadable), Exit: exitFailure, Reported: jsonMode}
	case openErrors > 0:
		return &CLIError{Code: "issues_open", Message: fmt.Sprintf("%d open runtime error(s)", openErrors), Exit: exitFailure, Reported: jsonMode}
	}
	return nil
}

// issuesHostPath is replaced in tests.
var issuesHostPath = runtimeissues.HostPath
