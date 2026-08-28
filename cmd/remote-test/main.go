// Command remote-test implements the local trust and preflight phase for
// document-driven remote tests. M2 deliberately exposes only preflight; the
// mutating run/status/collect/cleanup lifecycle is added in M3.
package main

// TEST_CASES: TESTAUTO-T-012

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/remotetest"
)

type preflightPlan struct {
	APIVersion    string                    `json:"api_version"`
	Target        remotetest.ResolvedTarget `json:"target"`
	Run           remotetest.Allocation     `json:"run"`
	Source        remotetest.SourceIdentity `json:"source"`
	BundlePath    string                    `json:"bundle_path"`
	RemoteMutated bool                      `json:"remote_mutated"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "remote-test: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] != "preflight" {
		return errors.New("usage: remote-test preflight [target, source, and requirement flags]")
	}
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	flags := flag.NewFlagSet("preflight", flag.ContinueOnError)
	profileName := flags.String("profile", "test-env/remote/targets.local.yml", "Git-ignored local target profile")
	targetName := flags.String("target", "", "registered target name")
	sshTarget := flags.String("ssh-target", "", "one-time explicit SSH config alias")
	authorizeTarget := flags.String("authorize-target", "", "repeat the explicit SSH alias to authorize this invocation")
	remoteRoot := flags.String("remote-work-root", "", "one-time explicit remote test root")
	explicitConcurrency := flags.Int("max-concurrency", 0, "one-time explicit concurrency limit")
	var explicitCapabilities stringFlags
	flags.Var(&explicitCapabilities, "target-capability", "one-time explicit target capability")
	requirementsName := flags.String("requirements", "", "preflight requirements YAML")
	runID := flags.String("run-id", "", "run id (generated when omitted)")
	sourceMode := flags.String("source", string(remotetest.SourceCommitted), "committed or worktree")
	sourceBase := flags.String("source-base", "HEAD", "source commit or worktree base")
	bundleName := flags.String("bundle", "", "local 0600 source bundle path below test-env/.remote-test/packages")
	timeout := flags.Duration("timeout", 3*time.Minute, "SSH preflight timeout")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *requirementsName == "" {
		return errors.New("preflight requires --requirements and accepts no positional arguments")
	}
	request := remotetest.TargetRequest{
		RegisteredName: *targetName, ExplicitSSHAlias: *sshTarget, ExplicitAuthorizeAs: *authorizeTarget,
		ExplicitRemoteRoot: *remoteRoot, ExplicitCapabilities: explicitCapabilities, ExplicitConcurrency: *explicitConcurrency,
	}
	var profiles remotetest.TargetProfileFile
	if *targetName != "" {
		profiles, err = remotetest.LoadLocalTargetProfiles(root, *profileName)
		if err != nil {
			return err
		}
	}
	target, err := remotetest.ResolveTarget(profiles, request)
	if err != nil {
		return err
	}
	requirementsPath, err := repositoryFile(root, *requirementsName)
	if err != nil {
		return err
	}
	requirementData, err := os.ReadFile(requirementsPath)
	if err != nil {
		return err
	}
	requirements, err := remotetest.ParsePreflightRequirements(requirementData)
	if err != nil {
		return err
	}
	if *runID == "" {
		*runID, err = remotetest.NewRunID(time.Now())
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	sshConfiguration, err := remotetest.InspectSSHConfiguration(ctx, target.SSHAlias)
	if err != nil {
		return err
	}
	target.SSHUser = sshConfiguration.User
	sshCommand, err := remotetest.BuildRemotePreflightCommand(target, *runID, requirements)
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, sshCommand[0], sshCommand[1:]...)
	hostOutput, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("remote read-only preflight failed: %w: %s", err, hostOutput)
	}
	facts, err := remotetest.DecodeHostFacts(hostOutput)
	if err != nil {
		return fmt.Errorf("decode remote preflight facts: %w", err)
	}
	allocation, err := remotetest.ValidatePreflight(target, *runID, requirements, facts)
	if err != nil {
		return err
	}
	if *bundleName == "" {
		*bundleName = filepath.ToSlash(filepath.Join("test-env", ".remote-test", "packages", *runID+".tar.gz"))
	}
	bundlePath, err := localBundlePath(root, *bundleName)
	if err != nil {
		return err
	}
	source, err := remotetest.BuildSourceBundle(ctx, root, bundlePath, remotetest.SourceMode(*sourceMode), *sourceBase)
	if err != nil {
		return err
	}
	plan := preflightPlan{
		APIVersion: "anas.remote-test-preflight/v1", Target: target, Run: allocation, Source: source,
		BundlePath: filepath.ToSlash(*bundleName), RemoteMutated: false,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(plan)
}

type stringFlags []string

func (values *stringFlags) String() string { return fmt.Sprint([]string(*values)) }
func (values *stringFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find repository root")
		}
		dir = parent
	}
}

func repositoryFile(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", errors.New("path must be repository-relative")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	path := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("path must stay inside repository")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("file %q does not exist", name)
	}
	return path, nil
}

func localBundlePath(root, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	prefix := filepath.Join("test-env", ".remote-test", "packages") + string(filepath.Separator)
	if filepath.IsAbs(name) || !strings.HasPrefix(clean, prefix) || !strings.HasSuffix(clean, ".tar.gz") {
		return "", errors.New("bundle must be a .tar.gz path below test-env/.remote-test/packages")
	}
	return filepath.Join(root, clean), nil
}
