package runner

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const resourceStateAPIVersion = "anas.resource-state/v1"

type resourceActual struct {
	Host           string `yaml:"host"`
	Port           string `yaml:"port"`
	Database       string `yaml:"database"`
	Username       string `yaml:"username"`
	PasswordSecret string `yaml:"password_secret"`
	Network        string `yaml:"network"`
}

type resourceState struct {
	APIVersion      string         `yaml:"api_version"`
	Consumer        string         `yaml:"consumer"`
	ResourceID      string         `yaml:"resource_id"`
	Contract        string         `yaml:"contract"`
	ContractVersion string         `yaml:"contract_version"`
	Provider        string         `yaml:"provider"`
	Interface       string         `yaml:"interface"`
	SpecFingerprint string         `yaml:"spec_fingerprint"`
	Actual          resourceActual `yaml:"actual"`
	Status          string         `yaml:"status"`
	DeletionPolicy  string         `yaml:"deletion_policy"`
	ProvisionedAt   string         `yaml:"provisioned_at"`
	ReconciledAt    string         `yaml:"last_reconciled_at"`
}

func (a *app) ensureResourcesFor(consumer, modulesRoot string) error {
	for _, request := range a.resourceRequests {
		if request.Consumer != consumer {
			continue
		}
		providerModule, ok := a.reg[request.Provider]
		if !ok {
			return fmt.Errorf("resource %s.%s provider module %s is unavailable", consumer, request.ID, request.Provider)
		}
		provider, ok := providerModule.providedContract(request.Contract, request.Interface)
		if !ok {
			return fmt.Errorf("module %s does not provide %s/%s", request.Provider, request.Contract, request.Interface)
		}
		operation, ok := provider.Operations["ensure"]
		if !ok || operation.Runtime != "compose_run" {
			return fmt.Errorf("provider %s %s/%s has no supported ensure operation", request.Provider, request.Contract, request.Interface)
		}
		providerDir := filepath.Join(modulesRoot, request.Provider)
		if a.useFrozenHooks && providerModule.SourceDir != "" {
			providerDir = providerModule.SourceDir
		}
		env := a.moduleEnv(providerDir)
		env["ANAS_RESOURCE_DATABASE"], _ = request.Spec["name"].(string)
		env["ANAS_RESOURCE_USERNAME"], _ = request.Spec["principal"].(string)
		env["ANAS_RESOURCE_PASSWORD"] = request.Password
		args := resourceEnsureComposeArgs(operation.Service, operation.Command)
		if err := a.compose.RunFile(providerDir, "anas_"+request.Provider, providerModule.ComposeFile, env, args...); err != nil {
			return fmt.Errorf("ensure resource %s.%s through %s: %w", consumer, request.ID, request.Provider, err)
		}
		if err := a.saveResourceReady(request, env); err != nil {
			return err
		}
	}
	return nil
}

func resourceEnsureComposeArgs(service string, command []string) []string {
	// Resource providers are one-shot, non-interactive jobs. compose run tries
	// to allocate a TTY by default, but RunFile intentionally does not attach
	// stdin. Tell Compose that explicitly so apply also works from automation,
	// redirected shells, and SSH sessions without a controlling terminal.
	args := []string{"run", "--rm", "--no-deps", "--no-TTY", service}
	return append(args, command...)
}

func (a *app) saveResourceReady(request ResourceRequest, providerEnv map[string]string) error {
	prefix := defaultEnvPrefix(request.Interface)
	database, _ := request.Spec["name"].(string)
	username, _ := request.Spec["principal"].(string)
	deletionPolicy, _ := request.Spec["deletion_policy"].(string)
	spec, err := yaml.Marshal(request.Spec)
	if err != nil {
		return err
	}
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256(spec))
	path := filepath.Join(a.base, "state", "resources", request.Consumer+"."+request.ID+".yml")
	created := ""
	if body, err := os.ReadFile(path); err == nil {
		var previous resourceState
		if yaml.Unmarshal(body, &previous) == nil {
			created = previous.ProvisionedAt
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if created == "" {
		created = now
	}
	state := resourceState{
		APIVersion: resourceStateAPIVersion, Consumer: request.Consumer, ResourceID: request.ID,
		Contract: request.Contract, ContractVersion: request.ContractVersion,
		Provider: request.Provider, Interface: request.Interface,
		SpecFingerprint: fingerprint, Status: "ready", DeletionPolicy: deletionPolicy,
		ProvisionedAt: created, ReconciledAt: now,
		Actual: resourceActual{
			Host: providerEnv[prefix+"_HOST"], Port: providerEnv[prefix+"_PORT"],
			Database: database, Username: username, PasswordSecret: request.SecretKey,
			Network: providerEnv[prefix+"_NETWORK_NAME"],
		},
	}
	if strings.TrimSpace(state.Actual.Host) == "" || strings.TrimSpace(state.Actual.Network) == "" {
		return fmt.Errorf("provider %s did not publish connection endpoint for %s.%s", request.Provider, request.Consumer, request.ID)
	}
	return writeYAMLAtomic(path, state, 0600)
}

// retainRemovedResources records the lifecycle transition without touching the
// provider. Removing a consumer must never implicitly delete its persistent
// database; a future explicit resource-delete command owns that destructive
// operation.
func retainRemovedResources(base string, current, target *deploymentManifest) error {
	if current == nil {
		return nil
	}
	targetResources := map[string]bool{}
	if target != nil {
		for _, resource := range target.Resources {
			targetResources[resource.Consumer+"."+resource.ID] = true
		}
	}
	for _, resource := range current.Resources {
		identity := resource.Consumer + "." + resource.ID
		if targetResources[identity] {
			continue
		}
		path := filepath.Join(base, "state", "resources", identity+".yml")
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read removed resource %s: %w", identity, err)
		}
		var state resourceState
		if err := yaml.Unmarshal(body, &state); err != nil {
			return fmt.Errorf("decode removed resource %s: %w", identity, err)
		}
		state.Status = "retained"
		state.ReconciledAt = time.Now().UTC().Format(time.RFC3339)
		if err := writeYAMLAtomic(path, state, 0600); err != nil {
			return fmt.Errorf("retain removed resource %s: %w", identity, err)
		}
	}
	return nil
}
