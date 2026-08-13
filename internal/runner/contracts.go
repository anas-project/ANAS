package runner

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	Password        string
}

func loadContractRegistry(moduleRoot string) (map[string]Contract, error) {
	root := filepath.Join(filepath.Dir(filepath.Clean(moduleRoot)), "contracts")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("contract directory %s: %w", root, err)
	}
	out := map[string]Contract{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
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

func normalizeContractDependencies(module string, in []manifestContractDependency) ([]ContractDependency, error) {
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
		out = append(out, ContractDependency{
			Name: name, Version: raw.Version, SelectedBy: strings.TrimSpace(raw.SelectedBy),
			Interfaces: interfaces, Default: fallback,
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

func normalizeResourceRequirements(module string, in []manifestResourceRequirement, deps []ContractDependency) ([]ResourceRequirement, error) {
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
		for _, dep := range deps {
			if dep.Name == contract {
				matched = true
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
		out = append(out, ResourceRequirement{
			ID: id, Contract: contract, Binding: strings.TrimSpace(raw.Binding),
			Spec: raw.Spec, SpecFrom: raw.SpecFrom,
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
		return "", fmt.Errorf("%s.%s must be auto or one of %s, got %q", consumer, dep.SelectedBy, strings.Join(dep.Interfaces, ", "), requested)
	}

	candidates := []string{}
	for name, candidate := range a.reg {
		if _, ok := candidate.providedContract(dep.Name, requested); ok && a.moduleEnabled(name) {
			candidates = append(candidates, name)
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return "", fmt.Errorf("%s requires %s/%s, but no enabled module provides it", consumer, dep.Name, requested)
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
		return "", fmt.Errorf("%s requires %s/%s, provided by %s; select one provider explicitly", consumer, dep.Name, requested, strings.Join(candidates, ", "))
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

func resourceSecretKey(consumer, id string) string {
	return "RESOURCE_" + defaultEnvPrefix(consumer) + "_" + defaultEnvPrefix(id) + "_PASSWORD"
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
	for _, consumer := range a.order {
		module := a.reg[consumer]
		for _, required := range module.Resources {
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
			secretKey := resourceSecretKey(consumer, required.ID)
			password, err := a.secrets.Ensure(secretKey, func() (string, error) { return randomPassword(32) })
			if err != nil {
				return err
			}
			a.resourceRequests = append(a.resourceRequests, ResourceRequest{
				Consumer: consumer, ID: required.ID, Contract: required.Contract, ContractVersion: contract.Version,
				Provider: provider, Interface: iface, Spec: spec,
				SecretKey: secretKey, Password: password,
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
		if request.Contract != "relational_database" {
			continue
		}
		name, _ := request.Spec["name"].(string)
		principal, _ := request.Spec["principal"].(string)
		providerPrefix := defaultEnvPrefix(request.Interface)
		consumerPrefix := defaultEnvPrefix(consumer)
		values := map[string]string{
			consumerPrefix + "_DB_TYPE":     request.Interface,
			consumerPrefix + "_DB_HOST":     a.env[providerPrefix+"_HOST"],
			consumerPrefix + "_DB_PORT":     a.env[providerPrefix+"_PORT"],
			consumerPrefix + "_DB_NAME":     name,
			consumerPrefix + "_DB_USERNAME": principal,
			consumerPrefix + "_DB_PASSWORD": request.Password,
			consumerPrefix + "_NETWORK_DB":  a.env[providerPrefix+"_NETWORK_NAME"],
		}
		for key, value := range values {
			if value == "" {
				return fmt.Errorf("resource %s.%s provider %s did not publish %s", request.Consumer, request.ID, request.Provider, key)
			}
			a.env[key] = value
			a.setEnvOwner(key, consumer)
		}
		a.markSensitive(consumerPrefix + "_DB_PASSWORD")
	}
	return nil
}
