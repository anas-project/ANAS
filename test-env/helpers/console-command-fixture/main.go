// console-command-fixture materializes a real workspace whose active deployment
// freezes two Module commands, so the invoke E2E exercises the production
// resolution, digest binding, locking and executor protocol rather than a stub.
//
// The executor it writes is a real program: it reads the ABI request on stdin,
// emits progress and a result record on stdout, and touches a side-effect file
// so the test can prove the command actually ran instead of trusting the job
// status alone.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/anas-project/ANAS/internal/configschema"
	"github.com/anas-project/ANAS/internal/deployment"
	"gopkg.in/yaml.v3"
)

const deploymentID = "dep-command-invoke-e2e"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: console-command-fixture materialize|drift ABSOLUTE_WORKSPACE")
	}
	workspace := args[1]
	if !filepath.IsAbs(workspace) {
		return errors.New("workspace must be an absolute path")
	}
	switch args[0] {
	case "materialize":
		return materialize(workspace)
	case "drift":
		// Rewrites the frozen descriptor so the digest a client already read no
		// longer matches, which is how the E2E reaches the 412 path without
		// racing a real redeploy.
		return drift(workspace)
	default:
		return errors.New("usage: console-command-fixture materialize|drift ABSOLUTE_WORKSPACE")
	}
}

// The executor never receives the workspace path, and the service builds its
// environment explicitly, so it cannot be told where to write through the
// environment either. It records into its own working directory -- which the
// service sets to the module root -- and dumps the environment it actually
// received, so the E2E can prove both that the command ran and that nothing
// from the daemon's own environment reached it (CONSOLE-R-147).
const executorProgram = `#!/usr/bin/env sh
set -eu
request=$(cat)
command_id=$(printf '%s' "$request" | sed -n 's/.*"command":"\([a-z-]*\)".*/\1/p')
printf '%s\n' "$command_id" >>./invocations.log
env | sort >./last-environment.txt
printf '{"type":"progress","phase":"working","current":1,"total":2,"unit":"steps"}\n'
printf '{"type":"progress","phase":"working","current":2,"total":2,"unit":"steps"}\n'
printf '{"type":"result","changed":true,"result":{"command":"%s","ran":true}}\n' "$command_id"
`

// ModuleRootFor names where the executor's side-effect files land, so the E2E
// script and this fixture cannot disagree about the path.
func ModuleRootFor(workspace string) string {
	return filepath.Join(workspace, ".anas", "deployments", deploymentID, "modules", "demo")
}

func materialize(workspace string) error {
	moduleRoot := filepath.Join(workspace, ".anas", "deployments", deploymentID, "modules", "demo")
	executable := filepath.Join(moduleRoot, "executor")
	if err := writeFile(executable, []byte(executorProgram), 0o700); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(moduleRoot, ".env"), []byte("DEMO_ENDPOINT=https://demo.test:8443\n"), 0o600); err != nil {
		return err
	}
	executorDigest, err := fileDigest(executable)
	if err != nil {
		return err
	}
	commands := []deployment.ModuleCommand{normalCommand(), destructiveCommand()}
	for index := range commands {
		digest, err := deployment.CommandDigest(commands[index])
		if err != nil {
			return err
		}
		commands[index].Digest = digest
	}
	if err := writeManifest(workspace, executorDigest, commands); err != nil {
		return err
	}
	return printDescriptors(commands)
}

func drift(workspace string) error {
	moduleRoot := filepath.Join(workspace, ".anas", "deployments", deploymentID, "modules", "demo")
	executorDigest, err := fileDigest(filepath.Join(moduleRoot, "executor"))
	if err != nil {
		return err
	}
	normal := normalCommand()
	// Any semantic change moves the digest; the title is the least invasive one
	// that leaves the command runnable.
	normal.Title = "Reindex the managed service (revised)"
	commands := []deployment.ModuleCommand{normal, destructiveCommand()}
	for index := range commands {
		digest, digestErr := deployment.CommandDigest(commands[index])
		if digestErr != nil {
			return digestErr
		}
		commands[index].Digest = digest
	}
	if err := writeManifest(workspace, executorDigest, commands); err != nil {
		return err
	}
	return printDescriptors(commands)
}

func normalCommand() deployment.ModuleCommand {
	return deployment.ModuleCommand{
		ID: "reindex", Title: "Reindex the managed service", Description: "Rebuild the search index.",
		Handler: "reindex", Mode: "change", Risk: "normal", RuntimeState: "any", Lock: "module_write",
		TimeoutSeconds: 30, Cancellable: "false", Env: []string{"DEMO_ENDPOINT"},
		Parameters: []deployment.ModuleCommandParameter{{
			Name: "full", Title: "Full rebuild", Description: "Rebuild every shard.",
			Type: configschema.Parameter{Kind: "bool"}, Default: false,
		}},
	}
}

func destructiveCommand() deployment.ModuleCommand {
	return deployment.ModuleCommand{
		ID: "purge", Title: "Purge the managed cache", Description: "Delete every cached object.",
		Handler: "purge", Mode: "change", Risk: "destructive", RuntimeState: "any", Lock: "module_write",
		TimeoutSeconds: 30, Cancellable: "false", Env: []string{"DEMO_ENDPOINT"},
		Parameters: []deployment.ModuleCommandParameter{},
	}
}

func writeManifest(workspace, executorDigest string, commands []deployment.ModuleCommand) error {
	manifest := deployment.Manifest{
		APIVersion: deployment.ManifestAPIVersion, ID: deploymentID, ModuleOrder: []string{"demo"},
		Modules: map[string]deployment.Module{"demo": {
			Name: "demo", Version: "1.2.3", Revision: 4, ArtifactDeployment: deploymentID, EnvPrefix: "DEMO",
			CommandExecutor: deployment.CommandExecutor{Command: []string{"./executor"}, Digest: executorDigest},
			Commands:        commands,
		}},
	}
	body, err := yaml.Marshal(manifest)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(workspace, ".anas", "deployments", deploymentID, "deployment.yml"), body, 0o600); err != nil {
		return err
	}
	state := "api_version: anas.state/v2\nactive_deployment: " + deploymentID + "\nruntime_status: running\n"
	return writeFile(filepath.Join(workspace, ".anas", "state", "active.yml"), []byte(state), 0o600)
}

// The script reads these digests rather than recomputing them, so the test can
// never disagree with the daemon about what the active deployment froze.
func printDescriptors(commands []deployment.ModuleCommand) error {
	report := map[string]any{"deployment_id": deploymentID, "module": "demo", "commands": map[string]any{}}
	for _, command := range commands {
		report["commands"].(map[string]any)[command.ID] = map[string]any{
			"digest": command.Digest, "risk": command.Risk,
		}
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(body))
	return nil
}

func writeFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}
