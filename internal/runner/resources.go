package runner

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const resourceStateAPIVersion = "anas.resource-state/v1"

type resourceActual struct {
	Host                  string `yaml:"host,omitempty"`
	Port                  string `yaml:"port,omitempty"`
	Database              string `yaml:"database,omitempty"`
	Username              string `yaml:"username,omitempty"`
	PasswordSecret        string `yaml:"password_secret,omitempty"`
	Network               string `yaml:"network,omitempty"`
	Endpoint              string `yaml:"endpoint,omitempty"`
	Region                string `yaml:"region,omitempty"`
	Bucket                string `yaml:"bucket,omitempty"`
	AccessKeyID           string `yaml:"access_key_id,omitempty"`
	SecretAccessKeySecret string `yaml:"secret_access_key_secret,omitempty"`
	PathStyle             bool   `yaml:"path_style,omitempty"`

	// compute records the fence, never the key material inside it.
	Sandbox                      string `yaml:"sandbox,omitempty"`
	InstancePrefix               string `yaml:"instance_prefix,omitempty"`
	ServerCertificateFingerprint string `yaml:"server_certificate_fingerprint,omitempty"`
	ClientCertificateSecret      string `yaml:"client_certificate_secret,omitempty"`
	MaxInstances                 int    `yaml:"max_instances,omitempty"`
	CPU                          int    `yaml:"cpu,omitempty"`
	MemoryMiB                    int    `yaml:"memory_mib,omitempty"`
	DiskGiB                      int    `yaml:"disk_gib,omitempty"`
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
		switch request.Contract {
		case "relational_database":
			env["ANAS_RESOURCE_DATABASE"], _ = request.Spec["name"].(string)
			env["ANAS_RESOURCE_USERNAME"], _ = request.Spec["principal"].(string)
			env["ANAS_RESOURCE_PASSWORD"] = request.Credential
		case "object_storage":
			env["ANAS_RESOURCE_BUCKET"], _ = request.Spec["bucket"].(string)
			env["ANAS_RESOURCE_ACCESS_KEY_ID"], _ = request.Spec["access_key_id"].(string)
			env["ANAS_RESOURCE_SECRET_ACCESS_KEY"] = request.Credential
		case "compute":
			quota, allowlist, err := validateComputeSpec(request.Consumer, request.ID, request.Spec)
			if err != nil {
				return err
			}
			// Only the certificate half crosses into the provider. The private
			// key goes straight to the consumer and is never handed to the
			// module that registers the trust entry.
			certPEM, _, err := splitComputeCredential(request.Credential)
			if err != nil {
				return fmt.Errorf("resource %s.%s: %w", consumer, request.ID, err)
			}
			env["ANAS_RESOURCE_CONSUMER"] = request.Consumer
			env["ANAS_RESOURCE_SANDBOX"], _ = request.Spec["sandbox"].(string)
			env["ANAS_RESOURCE_INSTANCE_PREFIX"], _ = request.Spec["instance_prefix"].(string)
			env["ANAS_RESOURCE_MAX_INSTANCES"] = strconv.Itoa(quota.MaxInstances)
			env["ANAS_RESOURCE_CPU"] = strconv.Itoa(quota.CPU)
			env["ANAS_RESOURCE_MEMORY_MIB"] = strconv.Itoa(quota.MemoryMiB)
			env["ANAS_RESOURCE_DISK_GIB"] = strconv.Itoa(quota.DiskGiB)
			env["ANAS_RESOURCE_IMAGE_ALLOWLIST"] = strings.Join(allowlist, ",")
			env["ANAS_RESOURCE_CLIENT_CERT"] = base64.StdEncoding.EncodeToString([]byte(certPEM))
		default:
			return fmt.Errorf("resource %s.%s contract %s has no runtime projection", consumer, request.ID, request.Contract)
		}
		args := resourceEnsureComposeArgs(operation.Service, operation.Command)
		if err := a.runCompose(providerDir, request.Provider, providerModule.ComposeFile, env, args...); err != nil {
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
	}
	switch request.Contract {
	case "relational_database":
		prefix := defaultEnvPrefix(request.Interface)
		state.Actual = resourceActual{
			Host: providerEnv[prefix+"_HOST"], Port: providerEnv[prefix+"_PORT"],
			Database: stringSpec(request.Spec, "name"), Username: stringSpec(request.Spec, "principal"),
			PasswordSecret: request.SecretKey, Network: providerEnv[prefix+"_NETWORK_NAME"],
		}
		if strings.TrimSpace(state.Actual.Host) == "" || strings.TrimSpace(state.Actual.Network) == "" {
			return fmt.Errorf("provider %s did not publish connection endpoint for %s.%s", request.Provider, request.Consumer, request.ID)
		}
	case "object_storage":
		prefix := "ANAS_OBJECT_STORAGE_" + defaultEnvPrefix(request.Interface) + "_"
		pathStyleValue := providerEnv[prefix+"PATH_STYLE"]
		if pathStyleValue != "true" && pathStyleValue != "false" {
			return fmt.Errorf("provider %s published invalid object storage path-style value for %s.%s", request.Provider, request.Consumer, request.ID)
		}
		state.Actual = resourceActual{
			Endpoint: providerEnv[prefix+"ENDPOINT"], Region: providerEnv[prefix+"REGION"],
			Bucket: stringSpec(request.Spec, "bucket"), AccessKeyID: stringSpec(request.Spec, "access_key_id"),
			SecretAccessKeySecret: request.SecretKey, PathStyle: pathStyleValue == "true",
		}
		if strings.TrimSpace(state.Actual.Endpoint) == "" || strings.TrimSpace(state.Actual.Region) == "" {
			return fmt.Errorf("provider %s did not publish object storage endpoint for %s.%s", request.Provider, request.Consumer, request.ID)
		}
	case "compute":
		prefix := defaultEnvPrefix(request.Provider)
		quota, _, err := validateComputeSpec(request.Consumer, request.ID, request.Spec)
		if err != nil {
			return err
		}
		fingerprint, err := computeServerFingerprint(providerEnv[prefix+"_SERVER_CERT_B64"])
		if err != nil {
			return fmt.Errorf("provider %s for %s.%s: %w", request.Provider, request.Consumer, request.ID, err)
		}
		state.Actual = resourceActual{
			Endpoint: providerEnv[prefix+"_ENDPOINT"],
			Sandbox:  stringSpec(request.Spec, "sandbox"), InstancePrefix: stringSpec(request.Spec, "instance_prefix"),
			ServerCertificateFingerprint: fingerprint, ClientCertificateSecret: request.SecretKey,
			MaxInstances: quota.MaxInstances, CPU: quota.CPU, MemoryMiB: quota.MemoryMiB, DiskGiB: quota.DiskGiB,
		}
		if strings.TrimSpace(state.Actual.Endpoint) == "" {
			return fmt.Errorf("provider %s did not publish compute endpoint for %s.%s", request.Provider, request.Consumer, request.ID)
		}
	default:
		return fmt.Errorf("resource %s.%s contract %s has no state projection", request.Consumer, request.ID, request.Contract)
	}
	return writeYAMLAtomic(path, state, 0600)
}

func stringSpec(spec map[string]any, key string) string {
	value, _ := spec[key].(string)
	return value
}

// retainRemovedResources records the lifecycle transition without touching the
// provider. Removing a consumer must never implicitly delete its persistent
// database, bucket, or objects; a future explicit resource-delete command owns
// that destructive operation.
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
