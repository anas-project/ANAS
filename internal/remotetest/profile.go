// Package remotetest implements the trust, source-identity, allocation, and
// preflight contracts used before a remote test deployment is allowed to make
// changes on a target host.
package remotetest

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const TargetProfileAPI = "anas.remote-test-targets/v1"

var (
	targetNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	sshAliasPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	capabilityPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	dnsLabelPattern   = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

// TargetProfileFile is local-only operator configuration. Its schema is
// intentionally small: SSH authentication stays in ssh-agent/config, not here.
type TargetProfileFile struct {
	APIVersion string                   `yaml:"api_version"`
	Targets    map[string]TargetProfile `yaml:"targets"`
}

type TargetProfile struct {
	SSHAlias       string   `yaml:"ssh_alias" json:"ssh_alias"`
	RemoteWorkRoot string   `yaml:"remote_work_root" json:"remote_work_root"`
	Capabilities   []string `yaml:"capabilities" json:"capabilities"`
	MaxConcurrency int      `yaml:"max_concurrency" json:"max_concurrency"`
}

type ResolvedTarget struct {
	Name            string   `json:"name"`
	SSHAlias        string   `json:"ssh_alias"`
	RemoteWorkRoot  string   `json:"remote_work_root"`
	Capabilities    []string `json:"capabilities"`
	MaxConcurrency  int      `json:"max_concurrency"`
	Authorization   string   `json:"authorization"`
	RegisteredLocal bool     `json:"registered_local"`
	SSHUser         string   `json:"-"`
}

type TargetRequest struct {
	RegisteredName       string
	ExplicitSSHAlias     string
	ExplicitAuthorizeAs  string
	ExplicitRemoteRoot   string
	ExplicitCapabilities []string
	ExplicitConcurrency  int
}

func ParseTargetProfiles(data []byte) (TargetProfileFile, error) {
	var profiles TargetProfileFile
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&profiles); err != nil {
		return profiles, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return profiles, errors.New("multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return profiles, err
	}
	if profiles.APIVersion != TargetProfileAPI {
		return profiles, fmt.Errorf("api_version must be %q", TargetProfileAPI)
	}
	if len(profiles.Targets) == 0 {
		return profiles, errors.New("targets must not be empty")
	}
	for name, profile := range profiles.Targets {
		if !targetNamePattern.MatchString(name) {
			return profiles, fmt.Errorf("target name %q is invalid", name)
		}
		if err := validateTargetProfile(profile); err != nil {
			return profiles, fmt.Errorf("target %s: %w", name, err)
		}
	}
	return profiles, nil
}

func LoadLocalTargetProfiles(root, name string) (TargetProfileFile, error) {
	path, rel, err := localProfilePath(root, name)
	if err != nil {
		return TargetProfileFile{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return TargetProfileFile{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return TargetProfileFile{}, errors.New("target profile must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		return TargetProfileFile{}, fmt.Errorf("target profile %s must use mode 0600", rel)
	}
	if err := ensureGitIgnored(root, rel); err != nil {
		return TargetProfileFile{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return TargetProfileFile{}, err
	}
	return ParseTargetProfiles(data)
}

func ResolveTarget(profiles TargetProfileFile, request TargetRequest) (ResolvedTarget, error) {
	registered := strings.TrimSpace(request.RegisteredName) != ""
	explicit := strings.TrimSpace(request.ExplicitSSHAlias) != "" || strings.TrimSpace(request.ExplicitAuthorizeAs) != ""
	if registered == explicit {
		return ResolvedTarget{}, errors.New("select exactly one authorization source: a registered target or an explicitly authorized SSH alias")
	}
	if registered {
		profile, ok := profiles.Targets[request.RegisteredName]
		if !ok {
			return ResolvedTarget{}, fmt.Errorf("registered target %q does not exist", request.RegisteredName)
		}
		return resolvedFromProfile(request.RegisteredName, profile, "registered-local-profile", true), nil
	}
	if request.ExplicitSSHAlias != request.ExplicitAuthorizeAs {
		return ResolvedTarget{}, errors.New("--authorize-target must exactly repeat --ssh-target in the current invocation")
	}
	profile := TargetProfile{
		SSHAlias:       request.ExplicitSSHAlias,
		RemoteWorkRoot: request.ExplicitRemoteRoot,
		Capabilities:   request.ExplicitCapabilities,
		MaxConcurrency: request.ExplicitConcurrency,
	}
	if err := validateTargetProfile(profile); err != nil {
		return ResolvedTarget{}, fmt.Errorf("explicit target: %w", err)
	}
	return resolvedFromProfile("explicit", profile, "explicit-current-invocation", false), nil
}

func resolvedFromProfile(name string, profile TargetProfile, authorization string, registered bool) ResolvedTarget {
	capabilities := append([]string(nil), profile.Capabilities...)
	sort.Strings(capabilities)
	return ResolvedTarget{
		Name: name, SSHAlias: profile.SSHAlias, RemoteWorkRoot: profile.RemoteWorkRoot,
		Capabilities: capabilities, MaxConcurrency: profile.MaxConcurrency,
		Authorization: authorization, RegisteredLocal: registered,
	}
}

func validateTargetProfile(profile TargetProfile) error {
	if !sshAliasPattern.MatchString(profile.SSHAlias) || strings.ContainsAny(profile.SSHAlias, "@:/\\") {
		return fmt.Errorf("ssh_alias %q must be an SSH config alias, not user@host, an address, or a path", profile.SSHAlias)
	}
	if err := validateRemoteWorkRoot(profile.RemoteWorkRoot); err != nil {
		return err
	}
	if len(profile.Capabilities) == 0 {
		return errors.New("capabilities must not be empty")
	}
	seen := make(map[string]bool)
	for _, capability := range profile.Capabilities {
		if !capabilityPattern.MatchString(capability) {
			return fmt.Errorf("capability %q is invalid", capability)
		}
		if seen[capability] {
			return fmt.Errorf("capability %q is repeated", capability)
		}
		seen[capability] = true
	}
	if profile.MaxConcurrency < 1 || profile.MaxConcurrency > 64 {
		return errors.New("max_concurrency must be between 1 and 64")
	}
	for _, value := range append([]string{profile.SSHAlias, profile.RemoteWorkRoot}, profile.Capabilities...) {
		lower := strings.ToLower(value)
		for _, marker := range []string{"-----begin", "password=", "token=", "secret=", "://"} {
			if strings.Contains(lower, marker) {
				return errors.New("profile values must not contain credentials, URLs, or Secret material")
			}
		}
	}
	return nil
}

func validateRemoteWorkRoot(root string) error {
	if root == "" || !strings.HasPrefix(root, "/") || filepath.Clean(root) != root {
		return errors.New("remote_work_root must be a clean absolute path")
	}
	if root == "/" || len(strings.Split(strings.Trim(root, "/"), "/")) < 2 {
		return errors.New("remote_work_root must be a dedicated nested directory, not a broad system path")
	}
	if !strings.Contains(strings.ToLower(root), "test") && !strings.Contains(strings.ToLower(root), "e2e") {
		return errors.New("remote_work_root must identify a test/e2e scope")
	}
	return nil
}

func localProfilePath(root, name string) (string, string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", "", errors.New("target profile path must be repository-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	wantPrefix := filepath.Join("test-env", "remote") + string(filepath.Separator)
	if !strings.HasPrefix(clean, wantPrefix) || !strings.HasSuffix(clean, ".local.yml") {
		return "", "", errors.New("target profile must be test-env/remote/*.local.yml")
	}
	path := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", errors.New("target profile must stay inside the repository")
	}
	return path, filepath.ToSlash(rel), nil
}

func ensureGitIgnored(root, rel string) error {
	tracked := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel)
	if tracked.Run() == nil {
		return fmt.Errorf("target profile %s is tracked by Git; local target profiles must never be committed", rel)
	}
	ignored := exec.Command("git", "-C", root, "check-ignore", "-q", "--", rel)
	if output, err := ignored.CombinedOutput(); err != nil {
		return fmt.Errorf("target profile %s is not covered by .gitignore: %s", rel, strings.TrimSpace(string(output)))
	}
	return nil
}
