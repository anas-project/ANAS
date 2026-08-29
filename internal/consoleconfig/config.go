// Package consoleconfig loads the root-managed configuration for anasd.
//
// The configuration is deliberately independent from workspace config.yml and
// is never supplemented from process environment variables. Callers must pass
// an explicit file security policy to Load.
package consoleconfig

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// APIVersion is the only service configuration schema accepted by this
	// package. Schema changes that are not backward compatible require a new
	// value rather than permissive decoding.
	APIVersion = "anas.console-config/v1"

	// DefaultPort preserves the original anasd development port while making
	// the selected management port a static service configuration value.
	DefaultPort = 8080
)

type Mode string

const (
	ModeLAN      Mode = "lan"
	ModeLoopback Mode = "loopback"
)

// Config is the root-managed, host-level configuration for anasd. It is not a
// workspace desired-state document and must not be populated from config.yml.
type Config struct {
	APIVersion      string      `yaml:"api_version"`
	Mode            Mode        `yaml:"mode"`
	Port            int         `yaml:"port"`
	AllowedDNSHosts []string    `yaml:"allowed_dns_hosts,omitempty"`
	ConsoleStore    string      `yaml:"console_store"`
	Workspaces      []Workspace `yaml:"workspaces,omitempty"`
	TLS             TLSConfig   `yaml:"tls,omitempty"`
}

// Workspace registers a public ID to a server-selected host path. Filesystem
// existence, .anas marker, and symlink canonicalisation remain registry
// responsibilities; this package establishes the static schema and absolute
// path boundary.
type Workspace struct {
	ID   string `yaml:"id"`
	Path string `yaml:"path"`
}

type TLSConfig struct {
	Lego      *LegoTLSPaths      `yaml:"lego,omitempty"`
	Temporary *TemporaryTLSPaths `yaml:"temporary,omitempty"`
}

// LegoTLSPaths names the complete files produced by the lego certificate
// contract. Every field is required when this block is present so serving-pair,
// chain, trust, identity, and issuer-source validation fail closed together.
type LegoTLSPaths struct {
	BaseDomain  string `yaml:"base_domain"`
	Certificate string `yaml:"certificate"`
	PrivateKey  string `yaml:"private_key"`
	Issuer      string `yaml:"issuer"`
	TrustBundle string `yaml:"trust_bundle"`
	IssuerMark  string `yaml:"issuer_marker"`
}

// TemporaryTLSPaths names the explicitly generated, disposable bootstrap
// certificate pair. It is kept distinct from lego so it cannot become an
// accidental long-term issuer configuration.
type TemporaryTLSPaths struct {
	Certificate string   `yaml:"certificate"`
	PrivateKey  string   `yaml:"private_key"`
	DNSNames    []string `yaml:"dns_names,omitempty"`
	IPAddresses []string `yaml:"ip_addresses,omitempty"`
}

// Parse decodes exactly one strict, versioned YAML document, applies documented
// defaults, validates it, and returns normalized host names and paths.
func Parse(source []byte) (Config, error) {
	config := Config{
		Mode: ModeLAN,
		Port: DefaultPort,
	}
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode anasd service configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Config{}, errors.New("decode anasd service configuration: multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("decode anasd service configuration: %w", err)
	}
	if err := config.validateAndNormalize(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config *Config) validateAndNormalize() error {
	if config.APIVersion != APIVersion {
		return fmt.Errorf("api_version must be %q", APIVersion)
	}
	if config.Mode != ModeLAN && config.Mode != ModeLoopback {
		return fmt.Errorf("mode must be %q or %q", ModeLAN, ModeLoopback)
	}
	if config.Port < 1 || config.Port > 65535 {
		return errors.New("port must be a number between 1 and 65535")
	}

	consoleStore, err := normalizeAbsolutePath("console_store", config.ConsoleStore)
	if err != nil {
		return err
	}
	config.ConsoleStore = consoleStore

	seenHosts := make(map[string]struct{}, len(config.AllowedDNSHosts))
	for index, value := range config.AllowedDNSHosts {
		host, err := normalizeDNSHost(value)
		if err != nil {
			return fmt.Errorf("allowed_dns_hosts[%d]: %w", index, err)
		}
		if _, exists := seenHosts[host]; exists {
			return fmt.Errorf("allowed_dns_hosts contains duplicate host %q", host)
		}
		seenHosts[host] = struct{}{}
		config.AllowedDNSHosts[index] = host
	}
	if config.AllowedDNSHosts == nil {
		config.AllowedDNSHosts = []string{}
	}

	seenWorkspaceIDs := make(map[string]struct{}, len(config.Workspaces))
	seenWorkspacePaths := make(map[string]string, len(config.Workspaces))
	for index := range config.Workspaces {
		workspace := &config.Workspaces[index]
		if err := validateWorkspaceID(workspace.ID); err != nil {
			return fmt.Errorf("workspaces[%d]: %w", index, err)
		}
		if _, exists := seenWorkspaceIDs[workspace.ID]; exists {
			return fmt.Errorf("workspace ID %q is registered more than once", workspace.ID)
		}
		path, err := normalizeAbsolutePath(fmt.Sprintf("workspace %q path", workspace.ID), workspace.Path)
		if err != nil {
			return err
		}
		if previousID, exists := seenWorkspacePaths[path]; exists {
			return fmt.Errorf("workspaces %q and %q use the same path", previousID, workspace.ID)
		}
		workspace.Path = path
		seenWorkspaceIDs[workspace.ID] = struct{}{}
		seenWorkspacePaths[path] = workspace.ID
	}
	if config.Workspaces == nil {
		config.Workspaces = []Workspace{}
	}
	for _, workspace := range config.Workspaces {
		if pathWithin(config.ConsoleStore, workspace.Path) {
			return fmt.Errorf("console_store must be outside registered workspace %q so snapshots and restores cannot overwrite it", workspace.ID)
		}
	}

	if err := normalizeTLSConfig(&config.TLS); err != nil {
		return err
	}
	return nil
}

func normalizeTLSConfig(config *TLSConfig) error {
	if config.Lego != nil {
		baseDomain, err := normalizeDNSHost(config.Lego.BaseDomain)
		if err != nil {
			return fmt.Errorf("tls.lego.base_domain: %w", err)
		}
		certificate, err := normalizeAbsolutePath("tls.lego.certificate", config.Lego.Certificate)
		if err != nil {
			return err
		}
		privateKey, err := normalizeAbsolutePath("tls.lego.private_key", config.Lego.PrivateKey)
		if err != nil {
			return err
		}
		if certificate == privateKey {
			return errors.New("tls.lego certificate and private_key must use different paths")
		}
		config.Lego.BaseDomain = baseDomain
		config.Lego.Certificate = certificate
		config.Lego.PrivateKey = privateKey
		config.Lego.Issuer, err = normalizeAbsolutePath("tls.lego.issuer", config.Lego.Issuer)
		if err != nil {
			return err
		}
		config.Lego.TrustBundle, err = normalizeAbsolutePath("tls.lego.trust_bundle", config.Lego.TrustBundle)
		if err != nil {
			return err
		}
		config.Lego.IssuerMark, err = normalizeAbsolutePath("tls.lego.issuer_marker", config.Lego.IssuerMark)
		if err != nil {
			return err
		}
	}
	if config.Temporary != nil {
		certificate, err := normalizeAbsolutePath("tls.temporary.certificate", config.Temporary.Certificate)
		if err != nil {
			return err
		}
		privateKey, err := normalizeAbsolutePath("tls.temporary.private_key", config.Temporary.PrivateKey)
		if err != nil {
			return err
		}
		if certificate == privateKey {
			return errors.New("tls.temporary certificate and private_key must use different paths")
		}
		config.Temporary.Certificate = certificate
		config.Temporary.PrivateKey = privateKey
		seenNames := make(map[string]struct{}, len(config.Temporary.DNSNames))
		for index, value := range config.Temporary.DNSNames {
			name, err := normalizeDNSHost(value)
			if err != nil {
				return fmt.Errorf("tls.temporary.dns_names[%d]: %w", index, err)
			}
			if _, exists := seenNames[name]; exists {
				return fmt.Errorf("tls.temporary.dns_names contains duplicate name %q", name)
			}
			seenNames[name] = struct{}{}
			config.Temporary.DNSNames[index] = name
		}
		seenAddresses := make(map[string]struct{}, len(config.Temporary.IPAddresses))
		for index, value := range config.Temporary.IPAddresses {
			if value == "" || strings.TrimSpace(value) != value || strings.Contains(value, "%") {
				return fmt.Errorf("tls.temporary.ip_addresses[%d] must be an IP literal without a zone", index)
			}
			address := net.ParseIP(value)
			if address == nil || address.IsUnspecified() {
				return fmt.Errorf("tls.temporary.ip_addresses[%d] must be a concrete IP literal", index)
			}
			canonical := address.String()
			if _, exists := seenAddresses[canonical]; exists {
				return fmt.Errorf("tls.temporary.ip_addresses contains duplicate address %q", canonical)
			}
			seenAddresses[canonical] = struct{}{}
			config.Temporary.IPAddresses[index] = canonical
		}
		if len(config.Temporary.DNSNames) == 0 && len(config.Temporary.IPAddresses) == 0 {
			return errors.New("tls.temporary requires at least one explicit dns_names or ip_addresses SAN")
		}
	}
	return nil
}

func pathWithin(candidate, parent string) bool {
	relative, err := filepath.Rel(parent, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateResolvedStorageBoundary(config Config) error {
	resolvedStore, err := resolvePathAllowMissing(config.ConsoleStore)
	if err != nil {
		return fmt.Errorf("resolve console_store: %w", err)
	}
	for _, workspace := range config.Workspaces {
		resolvedWorkspace, err := filepath.EvalSymlinks(workspace.Path)
		if err != nil {
			return fmt.Errorf("resolve workspace %q for console_store isolation: %w", workspace.ID, err)
		}
		if pathWithin(resolvedStore, resolvedWorkspace) {
			return fmt.Errorf("console_store resolves inside registered workspace %q; snapshots and restores must not reach control-plane state", workspace.ID)
		}
	}
	return nil
}

// resolvePathAllowMissing resolves every existing ancestor and then appends
// the missing suffix. This catches a symlinked parent without requiring the
// first startup to pre-create the console store.
func resolvePathAllowMissing(path string) (string, error) {
	current := filepath.Clean(path)
	missing := make([]string, 0, 4)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func normalizeAbsolutePath(field, value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be an absolute path", field)
	}
	return filepath.Clean(value), nil
}

func validateWorkspaceID(id string) error {
	if id == "" || len(id) > 64 {
		return errors.New("workspace ID must contain between 1 and 64 characters")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("workspace ID %q is reserved", id)
	}
	for index, char := range id {
		valid := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
		if index > 0 {
			valid = valid || char == '-' || char == '_' || char == '.'
		}
		if !valid {
			return fmt.Errorf("workspace ID %q must start with an ASCII letter or digit and contain only letters, digits, '.', '_' or '-'", id)
		}
	}
	return nil
}

func normalizeDNSHost(value string) (string, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return "", errors.New("host must not be empty or contain surrounding whitespace")
	}
	if strings.HasSuffix(value, ".") {
		value = strings.TrimSuffix(value, ".")
	}
	if value == "" || len(value) > 253 {
		return "", errors.New("host must contain between 1 and 253 characters")
	}
	if net.ParseIP(value) != nil || strings.ContainsAny(value, "[]:") {
		return "", fmt.Errorf("host %q must be a DNS name without an IP literal or port", value)
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 {
			return "", fmt.Errorf("host %q contains an invalid DNS label length", value)
		}
		for index, char := range label {
			asciiLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
			digit := char >= '0' && char <= '9'
			if !asciiLetter && !digit && !(char == '-' && index > 0 && index < len(label)-1) {
				return "", fmt.Errorf("host %q is not a valid ASCII DNS name", value)
			}
		}
	}
	return strings.ToLower(value), nil
}
