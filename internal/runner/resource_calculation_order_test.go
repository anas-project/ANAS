package runner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Use a differently named provider: ordering is a resource dependency rule,
// not an Incus-specific host-file reader in Core. The native test separately
// uses the unmodified Incus Hook and the installed host bundle.
func calculatedComputeApp(t *testing.T, architecture string, consumers map[string]string) *app {
	t.Helper()
	a := computeApp(t, consumers)
	a.base = t.TempDir()
	delete(a.env, "INCUS_IMAGE_ARCHITECTURE")
	provider := Module{Name: "sandbox_provider", EnvPrefix: "SANDBOX_PROVIDER", SourceDir: t.TempDir(),
		MustResolve: []string{"SANDBOX_PROVIDER_IMAGE_ARCHITECTURE"}}
	response, err := json.Marshal(hookResponse{Env: map[string]string{
		"SANDBOX_PROVIDER_IMAGE_ARCHITECTURE":   architecture,
		"SANDBOX_PROVIDER_ENDPOINT":             a.env["INCUS_ENDPOINT"],
		"SANDBOX_PROVIDER_SERVER_CERT_B64":      a.env["INCUS_SERVER_CERT_B64"],
		"SANDBOX_PROVIDER_CONTROL_NETWORK_NAME": "anas-incus-control",
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider.Hook = HookConfig{Command: []string{"sh", "-c", `cat >/dev/null; printf x >> calls; printf '%s' "$1"`, "provider-fixture", string(response)}}
	a.reg[provider.Name] = provider
	a.order = []string{provider.Name}
	a.deps = map[string][]string{}
	for name := range consumers {
		a.order = append(a.order, name)
		a.deps[name] = []string{provider.Name}
		a.resolvedBindings[name]["compute"] = provider.Name
		mod := a.reg[name]
		mod.SourceDir = t.TempDir()
		mod.Hook = HookConfig{Command: []string{"sh", "-c", `cat > hook-input.json; printf '{}'`}}
		a.reg[name] = mod
	}
	seedCalculateGlobalRequirements(a.env)
	return a
}

func TestCalculateFreezesResourcesAfterProviderBeforeConsumerHook(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		t.Run(architecture, func(t *testing.T) {
			a := calculatedComputeApp(t, architecture, map[string]string{"worker": "anas-worker"})
			if err := a.calculate(); err != nil {
				t.Fatal("provider-derived target was unavailable during resource preparation", err)
			}
			if len(a.resourceRequests) != 1 || a.resourceRequests[0].ComputeImages == nil || len(a.resourceRequests[0].ComputeImages.Images) != 1 ||
				a.resourceRequests[0].ComputeImages.Images[0].Target.Architecture != architecture {
				t.Fatal("calculation did not freeze the provider-derived image target")
			}
			body, err := os.ReadFile(filepath.Join(a.reg["worker"].SourceDir, "hook-input.json"))
			if err != nil {
				t.Fatal(err)
			}
			var input hookRequest
			if err := json.Unmarshal(body, &input); err != nil {
				t.Fatal(err)
			}
			prefix := computeResourcePrefix("worker", "runners")
			if input.Env[prefix+"SANDBOX"] != "anas-worker" || input.Env[prefix+"CLIENT_KEY"] == "" ||
				input.Env[prefix+"CONTROL_NETWORK_NAME"] != "anas-incus-control" {
				t.Fatal("consumer calculate Hook ran without its generated resource projection")
			}
			credential := a.resourceRequests[0].Credential
			if err := a.calculate(); err != nil || len(a.resourceRequests) != 1 || a.resourceRequests[0].Credential != credential {
				t.Fatal("a new calculation duplicated resources or replaced a stable credential", err)
			}
			calls, err := os.ReadFile(filepath.Join(a.reg["sandbox_provider"].SourceDir, "calls"))
			if err != nil || string(calls) != "xx" {
				t.Fatal("provider Hook was run more than once per calculation", err)
			}
		})
	}
}

func TestCalculateValidatesProviderTargetBeforeMintingConsumerCredential(t *testing.T) {
	a := calculatedComputeApp(t, "unknown-target", map[string]string{"worker": "anas-worker"})
	var failure *CLIError
	if err := a.calculate(); !errors.As(err, &failure) || failure.Code != "resource_invalid" {
		t.Fatal("resource preparation lost its established machine-readable failure code", err)
	}
	if len(a.resourceRequests) != 0 || len(a.secrets.values) != 0 || exists(filepath.Join(a.reg["worker"].SourceDir, "hook-input.json")) {
		t.Fatal("invalid target minted credentials or executed the consumer Hook")
	}
}

func TestCalculateRetainsCrossConsumerUniquenessAndDisabledResourceRules(t *testing.T) {
	t.Run("shared sandbox", func(t *testing.T) {
		a := calculatedComputeApp(t, "amd64", map[string]string{"one": "anas-shared", "two": "anas-shared"})
		if err := a.calculate(); err == nil || !strings.Contains(err.Error(), "same sandbox") {
			t.Fatal("incremental resource preparation lost cross-consumer conflict checks", err)
		}
	})
	t.Run("disabled subsystem", func(t *testing.T) {
		a := calculatedComputeApp(t, "amd64", map[string]string{"worker": "anas-worker"})
		module := a.reg["worker"]
		module.Resources[0].EnabledBy = "compute_enabled"
		a.reg["worker"] = module
		a.env["WORKER_COMPUTE_ENABLED"] = "false"
		if err := a.calculate(); err != nil || len(a.resourceRequests) != 0 || len(a.secrets.values) != 0 {
			t.Fatal("disabled resource minted a credential", err)
		}
	})
	// The shared resource implementation must keep the same final requests as
	// materializing already-settled inputs, rather than a second compute path.
	t.Run("settled inputs", func(t *testing.T) {
		a := calculatedComputeApp(t, "amd64", map[string]string{"one": "anas-one", "two": "anas-two"})
		if err := a.calculate(); err != nil {
			t.Fatal(err)
		}
		before := append([]ResourceRequest{}, a.resourceRequests...)
		if err := a.materializeResourceSecrets(); err != nil || !reflect.DeepEqual(before, a.resourceRequests) {
			t.Fatal("incremental and settled resource preparation disagree", err)
		}
	})
}
