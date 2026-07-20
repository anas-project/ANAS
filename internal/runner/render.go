package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The renderer supports the small ERB-like subset used by the casks:
// `<%= envs["KEY"] %>`, `<%= "#{envs["KEY"]}" %>`, and flat equality blocks
// `<% if envs['KEY'] == 'value' %> ... <% end %>`. Rendering is strict:
// an expression referencing a key absent from the cask's scoped environment
// is an error, and any template marker left after substitution is an error,
// so scoping or template mistakes fail the render instead of silently
// producing empty values.

var erbExpr = regexp.MustCompile(`<%=\s*(?:envs\[['"]([^'"]+)['"]\]|"#\{envs\[['"]([^'"]+)['"]\]\}")\s*%>`)
var erbIf = regexp.MustCompile(`(?s)<%\s*if\s+envs\[['"]([^'"]+)['"]\]\s*==\s*['"]([^'"]+)['"]\s*%>(.*?)<%\s*end\s*%>`)

func renderERBFiles(root string, env map[string]string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".erb") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s, err := renderERB(string(b), env)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		dst := strings.TrimSuffix(path, ".erb")
		if err := os.WriteFile(dst, []byte(s), 0644); err != nil {
			return err
		}
		return os.Remove(path)
	})
}

func renderERB(s string, env map[string]string) (string, error) {
	missing := map[string]bool{}
	s = erbIf.ReplaceAllStringFunc(s, func(m string) string {
		g := erbIf.FindStringSubmatch(m)
		if len(g) == 4 && env[g[1]] == g[2] {
			return g[3]
		}
		return ""
	})
	s = erbExpr.ReplaceAllStringFunc(s, func(m string) string {
		g := erbExpr.FindStringSubmatch(m)
		key := g[1]
		if key == "" {
			key = g[2]
		}
		if _, ok := env[key]; !ok {
			missing[key] = true
		}
		return env[key]
	})
	if len(missing) > 0 {
		keys := make([]string, 0, len(missing))
		for key := range missing {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		return "", fmt.Errorf("template references env keys outside the cask scope: %s (declare them in cask.yml config.consumes or set a default)", strings.Join(keys, ", "))
	}
	if idx := strings.Index(s, "<%"); idx >= 0 {
		line := 1 + strings.Count(s[:idx], "\n")
		return "", fmt.Errorf("unrendered template marker near line %d; only flat <%%= envs[...] %%> and <%% if envs[...] == '...' %%> blocks are supported", line)
	}
	return s, nil
}
