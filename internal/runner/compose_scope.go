package runner

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var composeProjectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// inspectComposeProjectOwners is replaceable in tests. Docker Compose writes
// the working directory into every managed container, which gives the runner a
// daemon-observed ownership boundary instead of trusting a project name alone.
var inspectComposeProjectOwners = dockerComposeProjectOwners

func composeProjectName(module string, env map[string]string) (string, error) {
	prefix := strings.TrimSpace(env["CONTAINER_PREFIX"])
	if prefix == "" {
		prefix = "anas_"
	}
	project := prefix + module
	if !composeProjectPattern.MatchString(project) {
		return "", fmt.Errorf("invalid Compose project name %q derived from CONTAINER_PREFIX=%q and module %q", project, prefix, module)
	}
	return project, nil
}

func composeCommandMutates(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "up", "down", "start", "stop", "restart", "rm", "run":
		return true
	default:
		return false
	}
}

func (a *app) runCompose(dir, module, composeFile string, env map[string]string, args ...string) error {
	project, err := composeProjectName(module, env)
	if err != nil {
		return err
	}
	if composeCommandMutates(args) {
		if err := a.ensureComposeProjectOwner(project); err != nil {
			return err
		}
	}
	return a.compose.RunFile(dir, project, composeFile, env, args...)
}

func (a *app) outputCompose(dir, module, composeFile string, env map[string]string, args ...string) (string, error) {
	project, err := composeProjectName(module, env)
	if err != nil {
		return "", err
	}
	return a.compose.OutputFile(dir, project, composeFile, env, args...)
}

func (a *app) ensureComposeProjectOwner(project string) error {
	owners, err := inspectComposeProjectOwners(project)
	if err != nil {
		return fmt.Errorf("inspect Compose project %q ownership: %w", project, err)
	}
	if len(owners) == 0 {
		return nil
	}
	want, err := filepath.Abs(a.workspace)
	if err != nil {
		return fmt.Errorf("resolve workspace ownership for Compose project %q: %w", project, err)
	}
	want = filepath.Clean(want)
	for _, owner := range owners {
		workspace, ok := composeWorkingDirWorkspace(owner)
		if !ok {
			return fmt.Errorf("refusing to operate Compose project %q: existing containers do not expose an ANAS workspace owner (working_dir=%q)", project, owner)
		}
		if workspace != want {
			return fmt.Errorf("refusing to operate Compose project %q owned by workspace %q from workspace %q; choose a unique container_prefix", project, workspace, want)
		}
	}
	return nil
}

func dockerComposeProjectOwners(project string) ([]string, error) {
	cmd := exec.Command("docker", "ps", "--all",
		"--filter", "label=com.docker.compose.project="+project,
		"--format", `{{.Label "com.docker.compose.project.working_dir"}}`)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !seen[line] {
			seen[line] = true
		}
	}
	delete(seen, "")
	owners := make([]string, 0, len(seen))
	for owner := range seen {
		owners = append(owners, owner)
	}
	sort.Strings(owners)
	return owners, nil
}

func composeWorkingDirWorkspace(dir string) (string, bool) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "." || !filepath.IsAbs(dir) {
		return "", false
	}
	marker := string(filepath.Separator) + ".anas" + string(filepath.Separator) + "deployments" + string(filepath.Separator)
	if i := strings.Index(dir, marker); i > 0 {
		return filepath.Clean(dir[:i]), true
	}
	// Pre-deployment-layout releases lived directly below .anas/releases. Keep
	// their ownership readable so an upgrade can safely replace its own stack.
	marker = string(filepath.Separator) + ".anas" + string(filepath.Separator) + "releases" + string(filepath.Separator)
	if i := strings.Index(dir, marker); i > 0 {
		return filepath.Clean(dir[:i]), true
	}
	return "", false
}
