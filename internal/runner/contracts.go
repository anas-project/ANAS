package runner

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/computeclient"
	"gopkg.in/yaml.v3"
)

const (
	contractAPIVersion = "anas.contract/v1"
	providerAPIVersion = "anas.provider/v1"
)

var resourceIdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type Contract struct {
	Name       string
	Version    string
	Interfaces []string
	Operations map[string]ContractOperation
	SourceDir  string
	Digest     string
}

type ContractOperation struct {
	Required bool
}

type contractManifest struct {
	APIVersion string                       `yaml:"api_version"`
	Kind       string                       `yaml:"kind"`
	Name       string                       `yaml:"name"`
	Version    string                       `yaml:"version"`
	Interfaces []string                     `yaml:"interfaces"`
	Resource   contractResourceManifest     `yaml:"resource"`
	Operations map[string]contractOperation `yaml:"operations"`
}

type contractResourceManifest struct {
	Schema   string   `yaml:"schema"`
	Identity []string `yaml:"identity"`
}

type contractOperation struct {
	RequestSchema string `yaml:"request_schema"`
	ResultSchema  string `yaml:"result_schema"`
	Required      bool   `yaml:"required"`
}

type providerManifest struct {
	APIVersion      string                               `yaml:"api_version"`
	Kind            string                               `yaml:"kind"`
	Contract        string                               `yaml:"contract"`
	ContractVersion string                               `yaml:"contract_version"`
	Interface       string                               `yaml:"interface"`
	Operations      map[string]providerOperationManifest `yaml:"operations"`
}

type providerOperationManifest struct {
	Runtime string   `yaml:"runtime"`
	Service string   `yaml:"service"`
	Command []string `yaml:"command"`
}

type ResourceRequest struct {
	Consumer        string
	ID              string
	Contract        string
	ContractVersion string
	Provider        string
	Interface       string
	Spec            map[string]any
	SecretKey       string
	Credential      string
}

func loadContractRegistry(moduleRoot string) (map[string]Contract, error) {
	root := filepath.Join(filepath.Dir(filepath.Clean(moduleRoot)), "contracts")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("contract directory %s: %w", root, err)
	}
	out := map[string]Contract{}
	for _, entry := range entries {
		dir := filepath.Join(root, entry.Name())
		info, statErr := os.Stat(dir)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			dir = resolved
		}
		path := filepath.Join(dir, "contract.yml")
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var manifest contractManifest
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if manifest.APIVersion != contractAPIVersion || manifest.Kind != "Contract" {
			return nil, fmt.Errorf("%s is not an %s Contract", path, contractAPIVersion)
		}
		if manifest.Name != entry.Name() {
			return nil, fmt.Errorf("contract directory %s contains name %q", entry.Name(), manifest.Name)
		}
		if _, err := parseSemver(manifest.Version); err != nil {
			return nil, fmt.Errorf("contract %s version: %w", manifest.Name, err)
		}
		if len(manifest.Interfaces) == 0 {
			return nil, fmt.Errorf("contract %s has no interfaces", manifest.Name)
		}
		if manifest.Resource.Schema != "" && !exists(filepath.Join(dir, manifest.Resource.Schema)) {
			return nil, fmt.Errorf("contract %s resource schema %s does not exist", manifest.Name, manifest.Resource.Schema)
		}
		operations := map[string]ContractOperation{}
		for name, operation := range manifest.Operations {
			for _, rel := range []string{operation.RequestSchema, operation.ResultSchema} {
				if rel != "" && !exists(filepath.Join(dir, rel)) {
					return nil, fmt.Errorf("contract %s operation %s schema %s does not exist", manifest.Name, name, rel)
				}
			}
			operations[name] = ContractOperation{Required: operation.Required}
		}
		digest, err := directoryDigest(dir)
		if err != nil {
			return nil, err
		}
		out[manifest.Name] = Contract{
			Name: manifest.Name, Version: manifest.Version,
			Interfaces: append([]string{}, manifest.Interfaces...), Operations: operations,
			SourceDir: dir, Digest: digest,
		}
	}
	return out, nil
}

func directoryDigest(root string) (string, error) {
	h := sha256.New()
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

func normalizeContractDependencies(module string, in []manifestContractDependency, types map[string]ParamType) ([]ContractDependency, error) {
	out := make([]ContractDependency, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		name := strings.ToLower(strings.TrimSpace(raw.Name))
		if name == "" || seen[name] {
			return nil, fmt.Errorf("module %q has an empty or duplicate contract dependency %q", module, name)
		}
		seen[name] = true
		if strings.TrimSpace(raw.SelectedBy) == "" || len(raw.Interfaces) == 0 {
			return nil, fmt.Errorf("module %q contract %s requires selected_by and interfaces", module, name)
		}
		if _, err := parseVersionConstraint(raw.Version); err != nil {
			return nil, fmt.Errorf("module %q contract %s version %q: %w", module, name, raw.Version, err)
		}
		interfaces := []string{}
		for _, iface := range raw.Interfaces {
			iface = strings.ToLower(strings.TrimSpace(iface))
			if iface == "" || contains(interfaces, iface) {
				return nil, fmt.Errorf("module %q contract %s has an empty or duplicate interface", module, name)
			}
			interfaces = append(interfaces, iface)
		}
		fallback := strings.ToLower(strings.TrimSpace(raw.Default))
		if fallback == "" || !contains(interfaces, fallback) {
			return nil, fmt.Errorf("module %q contract %s default %q is not in interfaces", module, name, raw.Default)
		}
		enabledBy, err := normalizeEnabledBy(module, "contracts", name, raw.EnabledBy, types)
		if err != nil {
			return nil, err
		}
		out = append(out, ContractDependency{
			Name: name, Version: raw.Version, SelectedBy: strings.TrimSpace(raw.SelectedBy),
			Interfaces: interfaces, Default: fallback, EnabledBy: enabledBy,
		})
	}
	return out, nil
}

func loadContractProviders(moduleDir, module string, in []manifestContractProvider) ([]ContractProvider, error) {
	out := make([]ContractProvider, 0, len(in))
	for _, declared := range in {
		rel := filepath.Clean(strings.TrimSpace(declared.Implementation))
		if rel == "." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("module %q contract provider has invalid implementation %q", module, declared.Implementation)
		}
		path := filepath.Join(moduleDir, rel)
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("module %q contract provider %s: %w", module, rel, err)
		}
		var manifest providerManifest
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&manifest); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if manifest.APIVersion != providerAPIVersion || manifest.Kind != "ContractProvider" {
			return nil, fmt.Errorf("%s is not an %s ContractProvider", path, providerAPIVersion)
		}
		name := strings.ToLower(strings.TrimSpace(declared.Name))
		iface := strings.ToLower(strings.TrimSpace(declared.Interface))
		if manifest.Contract != name || manifest.Interface != iface || manifest.ContractVersion != declared.Version {
			return nil, fmt.Errorf("module %q provider declaration and %s disagree", module, rel)
		}
		if _, err := parseSemver(declared.Version); err != nil {
			return nil, fmt.Errorf("module %q provider contract version: %w", module, err)
		}
		operations := map[string]ProviderOperation{}
		services := []string{}
		for opName, op := range manifest.Operations {
			if op.Runtime != "compose_run" || strings.TrimSpace(op.Service) == "" {
				return nil, fmt.Errorf("module %q provider operation %s must use compose_run with a service", module, opName)
			}
			operations[opName] = ProviderOperation{Runtime: op.Runtime, Service: op.Service, Command: append([]string{}, op.Command...)}
			if !contains(services, op.Service) {
				services = append(services, op.Service)
			}
		}
		out = append(out, ContractProvider{
			Name: name, Version: declared.Version, Interface: iface, Manifest: rel,
			Operations: operations, OperationSvcs: services,
		})
	}
	return out, nil
}

func normalizeResourceRequirements(module string, in []manifestResourceRequirement, deps []ContractDependency, types map[string]ParamType) ([]ResourceRequirement, error) {
	out := make([]ResourceRequirement, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		id := strings.ToLower(strings.TrimSpace(raw.ID))
		contract := strings.ToLower(strings.TrimSpace(raw.Contract))
		if !resourceIdentifierPattern.MatchString(id) || seen[id] {
			return nil, fmt.Errorf("module %q resource id %q is invalid or duplicated", module, raw.ID)
		}
		seen[id] = true
		matched := false
		declaredEnabledBy := ""
		for _, dep := range deps {
			if dep.Name == contract {
				matched, declaredEnabledBy = true, dep.EnabledBy
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("module %q resource %s uses undeclared contract %q", module, id, contract)
		}
		if raw.Spec == nil {
			return nil, fmt.Errorf("module %q resource %s has no spec", module, id)
		}
		for field, parameter := range raw.SpecFrom {
			field = strings.TrimSpace(field)
			parameter = strings.TrimSpace(parameter)
			if field == "" || parameter == "" {
				return nil, fmt.Errorf("module %q resource %s has an empty spec_from mapping", module, id)
			}
			if _, exists := raw.Spec[field]; exists {
				return nil, fmt.Errorf("module %q resource %s defines spec.%s and spec_from.%s", module, id, field, field)
			}
		}
		enabledBy, err := normalizeEnabledBy(module, "resources", id, raw.EnabledBy, types)
		if err != nil {
			return nil, err
		}
		// A resource cannot outlive the contract dependency it rides on. If the
		// contract is conditional the resource must carry the same condition,
		// otherwise a switched-off contract would still drag in a provider the
		// moment the resource asked for one.
		if declaredEnabledBy != "" && enabledBy != declaredEnabledBy {
			return nil, fmt.Errorf("module %q resource %s must declare enabled_by %q to match its contract dependency",
				module, id, declaredEnabledBy)
		}
		out = append(out, ResourceRequirement{
			ID: id, Contract: contract, Binding: strings.TrimSpace(raw.Binding),
			Spec: raw.Spec, SpecFrom: raw.SpecFrom, EnabledBy: enabledBy,
		})
	}
	return out, nil
}

func (a *app) validateContractRegistry() error {
	for _, module := range a.reg {
		for _, dep := range module.RequiresContracts {
			contract, ok := a.contracts[dep.Name]
			if !ok {
				return fmt.Errorf("module %s requires unavailable contract %s", module.Name, dep.Name)
			}
			constraint, _ := parseVersionConstraint(dep.Version)
			version, _ := parseSemver(contract.Version)
			if !constraint.Check(version) {
				return fmt.Errorf("module %s requires contract %s %s, core provides %s", module.Name, dep.Name, dep.Version, contract.Version)
			}
			for _, iface := range dep.Interfaces {
				if !contains(contract.Interfaces, iface) {
					return fmt.Errorf("module %s requires unknown %s interface %s", module.Name, dep.Name, iface)
				}
			}
		}
		for _, provider := range module.ContractProviders {
			contract, ok := a.contracts[provider.Name]
			if !ok || contract.Version != provider.Version || !contains(contract.Interfaces, provider.Interface) {
				return fmt.Errorf("module %s provider %s/%s is incompatible with installed contract", module.Name, provider.Name, provider.Interface)
			}
			for operation, definition := range contract.Operations {
				if definition.Required {
					if _, ok := provider.Operations[operation]; !ok {
						return fmt.Errorf("module %s provider %s/%s does not implement required operation %s", module.Name, provider.Name, provider.Interface, operation)
					}
				}
			}
		}
	}
	return nil
}

// contractRequired answers whether a conditional contract dependency exists in
// this deployment. It reads the operator's value and falls back to the module's
// declared default, for the same reason capabilityRequired does: at this point
// a.env holds only what was written down, so an unset switch must be read from
// the manifest rather than treated as empty.
func (a *app) contractRequired(moduleName string, mod Module, enabledBy string) bool {
	if enabledBy == "" {
		return true
	}
	key := paramEnvKey(moduleName, mod.EnvPrefix, enabledBy)
	value := strings.TrimSpace(a.env[key])
	if value == "" {
		value = strings.TrimSpace(mod.Defaults[key])
	}
	return strings.EqualFold(value, "true")
}

func (a *app) resolveContractDependency(consumer string, module Module, dep ContractDependency) (string, error) {
	contract, ok := a.contracts[dep.Name]
	// Low-level resolver tests may construct an app directly without loading
	// the core contract catalog. Production entry points always validate the
	// installed catalog before resolution; here the module declaration is
	// sufficient to exercise provider selection in isolation.
	if !ok && a.contracts == nil {
		contract = Contract{Name: dep.Name, Interfaces: append([]string{}, dep.Interfaces...)}
		ok = true
	}
	if !ok {
		return "", fmt.Errorf("module %s requires unavailable contract %s", consumer, dep.Name)
	}
	key := paramEnvKey(consumer, module.EnvPrefix, dep.SelectedBy)
	if err := a.rejectSourceSensitiveSelector(key, consumer+"."+dep.SelectedBy); err != nil {
		return "", err
	}
	requested := strings.ToLower(strings.TrimSpace(a.env[key]))
	if requested == "" {
		if value := module.Defaults[key]; value != "" {
			requested = strings.ToLower(strings.TrimSpace(value))
		}
	}
	if requested == "" || requested == "auto" {
		requested = ""
		if a.lock != nil {
			if locked := a.lock.Bindings[consumer][dep.Name+".interface"]; contains(dep.Interfaces, locked) {
				requested = locked
			}
		}
		if requested == "" {
			configuredInterfaces := []string{}
			for _, name := range a.cfg.Modules.Order {
				candidate, exists := a.reg[name]
				if !exists || !a.moduleEnabled(name) {
					continue
				}
				for _, provider := range candidate.ContractProviders {
					if provider.Name == dep.Name && contains(dep.Interfaces, provider.Interface) && !contains(configuredInterfaces, provider.Interface) {
						configuredInterfaces = append(configuredInterfaces, provider.Interface)
					}
				}
			}
			if len(configuredInterfaces) == 1 {
				requested = configuredInterfaces[0]
			}
		}
		if requested == "" {
			requested = dep.Default
		}
	}
	if !contains(dep.Interfaces, requested) || !contains(contract.Interfaces, requested) {
		return "", fmt.Errorf("%s.%s must be auto or one of %s, got %q", consumer, dep.SelectedBy, strings.Join(dep.Interfaces, ", "), a.resolvedValueForError(key, requested))
	}

	candidates := []string{}
	for name, candidate := range a.reg {
		if _, ok := candidate.providedContract(dep.Name, requested); ok && a.moduleEnabled(name) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	displayRequested := a.resolvedValueForError(key, requested)
	if len(candidates) == 0 {
		return "", fmt.Errorf("%s requires %s/%s, but no enabled module provides it", consumer, dep.Name, displayRequested)
	}
	provider := ""
	if a.lock != nil && contains(candidates, a.lock.Bindings[consumer][dep.Name]) {
		provider = a.lock.Bindings[consumer][dep.Name]
	}
	if provider == "" {
		configured := []string{}
		for _, name := range a.cfg.Modules.Order {
			if contains(candidates, name) && a.moduleEnabled(name) {
				configured = append(configured, name)
			}
		}
		if len(configured) == 1 {
			provider = configured[0]
		}
	}
	if provider == "" && len(candidates) == 1 {
		provider = candidates[0]
	}
	if provider == "" {
		return "", unresolvedBindingErrorf("%s requires %s/%s, provided by %s; select one provider explicitly", consumer, dep.Name, displayRequested, strings.Join(candidates, ", "))
	}
	a.env[key] = requested
	if a.resolvedBindings == nil {
		a.resolvedBindings = map[string]map[string]string{}
	}
	if a.resolvedBindings[consumer] == nil {
		a.resolvedBindings[consumer] = map[string]string{}
	}
	a.resolvedBindings[consumer][dep.Name] = provider
	a.resolvedBindings[consumer][dep.Name+".interface"] = requested
	return provider, nil
}

func resourceSecretKey(consumer, id, contract string) string {
	suffix := "PASSWORD"
	if contract == "object_storage" {
		suffix = "SECRET_ACCESS_KEY"
	}
	return "RESOURCE_" + defaultEnvPrefix(consumer) + "_" + defaultEnvPrefix(id) + "_" + suffix
}

func objectStorageAccessKeyID(consumer, id string) string {
	value := "ANAS_" + defaultEnvPrefix(consumer) + "_" + defaultEnvPrefix(id)
	if len(value) <= 64 {
		return value
	}
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(consumer+"."+id)))
	return value[:47] + "_" + digest[:16]
}

func validObjectStorageBucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`).MatchString(value) {
		return false
	}
	if strings.Contains(value, "..") || strings.Contains(value, ".-") || strings.Contains(value, "-.") {
		return false
	}
	parts := strings.Split(value, ".")
	if len(parts) == 4 {
		ipv4 := true
		for _, part := range parts {
			if part == "" || len(part) > 3 {
				ipv4 = false
				break
			}
			for _, digit := range part {
				if digit < '0' || digit > '9' {
					ipv4 = false
					break
				}
			}
		}
		if ipv4 {
			return false
		}
	}
	return true
}

func objectStorageResourcePrefix(consumer, id string) string {
	return "ANAS_OBJECT_STORAGE_RESOURCE__" + defaultEnvPrefix(consumer) + "__" + defaultEnvPrefix(id) + "__"
}

func cloneContractProviders(in []ContractProvider) []ContractProvider {
	out := make([]ContractProvider, 0, len(in))
	for _, provider := range in {
		operations := map[string]ProviderOperation{}
		for name, operation := range provider.Operations {
			operation.Command = append([]string{}, operation.Command...)
			operations[name] = operation
		}
		provider.Operations = operations
		provider.OperationSvcs = append([]string{}, provider.OperationSvcs...)
		out = append(out, provider)
	}
	return out
}

// materializeResourceSecrets gives every resource a stable credential before
// hooks render consumer configuration.  Connection endpoints are published
// later, after the provider's calculate hook has derived its host and network.
func (a *app) materializeResourceSecrets() error {
	a.resourceRequests = nil
	objectBuckets := map[string]string{}
	objectAccessKeys := map[string]string{}
	computeSandboxes := map[string]string{}
	for _, consumer := range a.order {
		module := a.reg[consumer]
		for _, required := range module.Resources {
			// A condition decides whether the resource exists, nothing else.
			// Skipping the whole iteration is what makes that true: no secret is
			// minted, no provider is called, and no lease is published for a
			// subsystem this deployment has switched off.
			if !a.contractRequired(consumer, module, required.EnabledBy) {
				continue
			}
			spec := cloneAnyMap(required.Spec)
			for field, parameter := range required.SpecFrom {
				key := paramEnvKey(consumer, module.EnvPrefix, parameter)
				value := strings.TrimSpace(a.env[key])
				if value == "" {
					return fmt.Errorf("resource %s.%s spec.%s references empty parameter %s", consumer, required.ID, field, parameter)
				}
				spec[field] = value
			}
			bindings := a.resolvedBindings[consumer]
			contract, ok := a.contracts[required.Contract]
			if !ok && a.contracts != nil {
				return fmt.Errorf("resource %s.%s contract %s is unavailable", consumer, required.ID, required.Contract)
			}
			provider := bindings[required.Contract]
			iface := bindings[required.Contract+".interface"]
			if provider == "" || iface == "" {
				return fmt.Errorf("resource %s.%s has no resolved %s provider", consumer, required.ID, required.Contract)
			}
			if required.Contract == "relational_database" {
				name, _ := spec["name"].(string)
				principal, _ := spec["principal"].(string)
				if !resourceIdentifierPattern.MatchString(name) || !resourceIdentifierPattern.MatchString(principal) {
					return fmt.Errorf("resource %s.%s database name or principal is invalid", consumer, required.ID)
				}
				policy, _ := spec["deletion_policy"].(string)
				if policy != "retain" && policy != "delete" {
					return fmt.Errorf("resource %s.%s deletion_policy must be retain or delete", consumer, required.ID)
				}
			}
			if required.Contract == "relational_database" || required.Contract == "object_storage" || required.Contract == "compute" {
				credential, ok := spec["credential"].(map[string]any)
				policy, _ := credential["policy"].(string)
				if !ok || policy != "generated" {
					return fmt.Errorf("resource %s.%s credential.policy must be generated", consumer, required.ID)
				}
			}
			if required.Contract == "compute" {
				if _, _, err := validateComputeSpec(consumer, required.ID, spec); err != nil {
					return err
				}
				// One sandbox belongs to one consumer. Sharing a project would
				// put two consumers behind the same fence, which is the exact
				// isolation this contract exists to provide.
				sandbox, _ := spec["sandbox"].(string)
				identity := consumer + "." + required.ID
				if previous := computeSandboxes[sandbox]; previous != "" {
					return fmt.Errorf("compute resources %s and %s use the same sandbox %s", previous, identity, sandbox)
				}
				computeSandboxes[sandbox] = identity
			}
			if required.Contract == "object_storage" {
				bucket, _ := spec["bucket"].(string)
				if !validObjectStorageBucket(bucket) {
					return fmt.Errorf("resource %s.%s object storage bucket %q is invalid", consumer, required.ID, bucket)
				}
				accessKey, _ := spec["access_key_id"].(string)
				if accessKey == "" {
					accessKey = objectStorageAccessKeyID(consumer, required.ID)
					spec["access_key_id"] = accessKey
				}
				if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{2,63}$`).MatchString(accessKey) {
					return fmt.Errorf("resource %s.%s object storage access_key_id is invalid", consumer, required.ID)
				}
				policy, _ := spec["deletion_policy"].(string)
				if policy != "retain" && policy != "delete" {
					return fmt.Errorf("resource %s.%s deletion_policy must be retain or delete", consumer, required.ID)
				}
				identity := consumer + "." + required.ID
				if previous := objectBuckets[bucket]; previous != "" {
					return fmt.Errorf("object storage resources %s and %s use the same bucket %s", previous, identity, bucket)
				}
				if previous := objectAccessKeys[accessKey]; previous != "" {
					return fmt.Errorf("object storage resources %s and %s use the same access_key_id %s", previous, identity, accessKey)
				}
				objectBuckets[bucket], objectAccessKeys[accessKey] = identity, identity
			}
			secretKey := resourceSecretKey(consumer, required.ID, required.Contract)
			credentialLength := 32
			if required.Contract == "object_storage" {
				credentialLength = 40
			}
			// compute authenticates with a client certificate rather than a
			// password, so its resource credential is a keypair bundle instead
			// of a random string. Everything downstream still sees one stable
			// secret per resource.
			generate := func() (string, error) { return randomPassword(credentialLength) }
			if required.Contract == "compute" {
				generate = func() (string, error) { return generateComputeClientCredential(consumer, required.ID) }
			}
			credential, err := a.secrets.Ensure(secretKey, generate)
			if err != nil {
				return err
			}
			a.secrets.SetWithMetadata(secretKey, credential, secretMetadata{
				Owner: consumer, Kind: required.Contract + "_resource", Provenance: "generated-resource",
			})
			a.resourceRequests = append(a.resourceRequests, ResourceRequest{
				Consumer: consumer, ID: required.ID, Contract: required.Contract, ContractVersion: contract.Version,
				Provider: provider, Interface: iface, Spec: spec,
				SecretKey: secretKey, Credential: credential,
			})
		}
	}
	return nil
}

func cloneAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (a *app) publishModuleResources(consumer string) error {
	for _, request := range a.resourceRequests {
		if request.Consumer != consumer {
			continue
		}
		values := map[string]string{}
		sensitive := ""
		switch request.Contract {
		case "relational_database":
			name, _ := request.Spec["name"].(string)
			principal, _ := request.Spec["principal"].(string)
			providerPrefix := defaultEnvPrefix(request.Interface)
			consumerPrefix := defaultEnvPrefix(consumer)
			values = map[string]string{
				consumerPrefix + "_DB_TYPE":     request.Interface,
				consumerPrefix + "_DB_HOST":     a.env[providerPrefix+"_HOST"],
				consumerPrefix + "_DB_PORT":     a.env[providerPrefix+"_PORT"],
				consumerPrefix + "_DB_NAME":     name,
				consumerPrefix + "_DB_USERNAME": principal,
				consumerPrefix + "_DB_PASSWORD": request.Credential,
				consumerPrefix + "_NETWORK_DB":  a.env[providerPrefix+"_NETWORK_NAME"],
			}
			sensitive = consumerPrefix + "_DB_PASSWORD"
		case "object_storage":
			bucket, _ := request.Spec["bucket"].(string)
			accessKey, _ := request.Spec["access_key_id"].(string)
			providerPrefix := "ANAS_OBJECT_STORAGE_" + defaultEnvPrefix(request.Interface) + "_"
			resourcePrefix := objectStorageResourcePrefix(consumer, request.ID)
			pathStyle := a.env[providerPrefix+"PATH_STYLE"]
			if pathStyle != "true" && pathStyle != "false" {
				return fmt.Errorf("resource %s.%s provider %s published invalid path-style value", request.Consumer, request.ID, request.Provider)
			}
			values = map[string]string{
				resourcePrefix + "INTERFACE":         request.Interface,
				resourcePrefix + "ENDPOINT":          a.env[providerPrefix+"ENDPOINT"],
				resourcePrefix + "REGION":            a.env[providerPrefix+"REGION"],
				resourcePrefix + "BUCKET":            bucket,
				resourcePrefix + "ACCESS_KEY_ID":     accessKey,
				resourcePrefix + "SECRET_ACCESS_KEY": request.Credential,
				resourcePrefix + "PATH_STYLE":        pathStyle,
			}
			sensitive = resourcePrefix + "SECRET_ACCESS_KEY"
		case "compute":
			quota, allowlist, err := validateComputeSpec(consumer, request.ID, request.Spec)
			if err != nil {
				return err
			}
			certPEM, keyPEM, err := splitComputeCredential(request.Credential)
			if err != nil {
				return fmt.Errorf("resource %s.%s: %w", consumer, request.ID, err)
			}
			providerPrefix := defaultEnvPrefix(request.Provider)
			fingerprint, err := computeServerFingerprint(a.env[providerPrefix+"_SERVER_CERT_B64"])
			if err != nil {
				return fmt.Errorf("resource %s.%s provider %s: %w", consumer, request.ID, request.Provider, err)
			}
			resourcePrefix := computeResourcePrefix(consumer, request.ID)
			// The consumer receives the fence and the key to it, and drives
			// instance lifecycle itself from here on. ANAS is not on that path.
			values = map[string]string{
				resourcePrefix + "INTERFACE":       request.Interface,
				resourcePrefix + "ENDPOINT":        a.env[providerPrefix+"_ENDPOINT"],
				resourcePrefix + "SANDBOX":         stringSpec(request.Spec, "sandbox"),
				resourcePrefix + "INSTANCE_PREFIX": stringSpec(request.Spec, "instance_prefix"),
				// Fixed by the contract, not chosen per deployment: the provider
				// writes this profile and the consumer only names it.
				resourcePrefix + "PROFILE": computeclient.ProfileName,
				// Both halves of the pin: the certificate the consumer's client
				// must match against, and its digest as an independent
				// cross-check. Neither is secret -- a server certificate is
				// public by construction.
				resourcePrefix + "SERVER_CERT":             a.env[providerPrefix+"_SERVER_CERT_B64"],
				resourcePrefix + "SERVER_CERT_FINGERPRINT": fingerprint,
				resourcePrefix + "CLIENT_CERT":             base64.StdEncoding.EncodeToString([]byte(certPEM)),
				resourcePrefix + "CLIENT_KEY":              base64.StdEncoding.EncodeToString([]byte(keyPEM)),
				resourcePrefix + "IMAGE_ALLOWLIST":         strings.Join(allowlist, ","),
				resourcePrefix + "MAX_INSTANCES":           strconv.Itoa(quota.MaxInstances),
				resourcePrefix + "CPU":                     strconv.Itoa(quota.CPU),
				resourcePrefix + "MEMORY_MIB":              strconv.Itoa(quota.MemoryMiB),
				resourcePrefix + "DISK_GIB":                strconv.Itoa(quota.DiskGiB),
			}
			sensitive = resourcePrefix + "CLIENT_KEY"
		default:
			continue
		}
		for key, value := range values {
			if value == "" {
				return fmt.Errorf("resource %s.%s provider %s did not publish %s", request.Consumer, request.ID, request.Provider, key)
			}
			a.env[key] = value
			a.setEnvOwner(key, consumer)
		}
		a.markSensitive(sensitive)
	}
	return nil
}
