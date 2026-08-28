package remotetest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const HostFactsAPI = "anas.remote-test-host-facts/v1"

type PreflightRequirements struct {
	Architecture   string   `json:"architecture" yaml:"architecture"`
	MinDiskBytes   uint64   `json:"min_disk_bytes" yaml:"min_disk_bytes"`
	MinMemoryBytes uint64   `json:"min_memory_bytes" yaml:"min_memory_bytes"`
	Ports          []int    `json:"ports" yaml:"ports"`
	DNSNames       []string `json:"dns_names" yaml:"dns_names"`
	Routes         []string `json:"routes" yaml:"routes"`
	Capabilities   []string `json:"capabilities" yaml:"capabilities"`
}

func ParsePreflightRequirements(data []byte) (PreflightRequirements, error) {
	var requirements PreflightRequirements
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&requirements); err != nil {
		return requirements, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return requirements, errors.New("multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return requirements, err
	}
	if err := ValidatePreflightRequirements(requirements); err != nil {
		return requirements, err
	}
	return requirements, nil
}

func ValidatePreflightRequirements(requirements PreflightRequirements) error {
	if requirements.Architecture == "" || !capabilityPattern.MatchString(requirements.Architecture) {
		return errors.New("architecture is required and must be a stable capability name")
	}
	if requirements.MinDiskBytes == 0 || requirements.MinMemoryBytes == 0 {
		return errors.New("min_disk_bytes and min_memory_bytes must be greater than zero")
	}
	if requirements.Ports == nil || requirements.DNSNames == nil || requirements.Routes == nil || len(requirements.Capabilities) == 0 {
		return errors.New("ports, dns_names, routes, and capabilities must be explicitly declared")
	}
	seenPorts := make(map[int]bool)
	for _, port := range requirements.Ports {
		if port < 1 || port > 65535 || seenPorts[port] {
			return fmt.Errorf("port %d is invalid or repeated", port)
		}
		seenPorts[port] = true
	}
	for label, values := range map[string][]string{"dns_names": requirements.DNSNames, "routes": requirements.Routes, "capabilities": requirements.Capabilities} {
		seen := make(map[string]bool)
		for _, value := range values {
			if value == "" || strings.ContainsAny(value, "\r\n") || seen[value] {
				return fmt.Errorf("%s contains an empty, repeated, or multiline value", label)
			}
			seen[value] = true
		}
	}
	for _, name := range requirements.DNSNames {
		if len(name) > 253 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
			return fmt.Errorf("DNS name %q is invalid", name)
		}
		for _, label := range strings.Split(name, ".") {
			if label == "" || len(label) > 63 || !dnsLabelPattern.MatchString(label) {
				return fmt.Errorf("DNS name %q is invalid", name)
			}
		}
	}
	for _, route := range requirements.Routes {
		if net.ParseIP(route) == nil {
			if _, _, err := net.ParseCIDR(route); err != nil {
				return fmt.Errorf("route %q must be an IP address or CIDR", route)
			}
		}
	}
	for _, capability := range requirements.Capabilities {
		if !capabilityPattern.MatchString(capability) {
			return fmt.Errorf("capability %q is invalid", capability)
		}
	}
	return nil
}

type AccountFacts struct {
	User                      string   `json:"user"`
	UID                       int      `json:"uid"`
	Groups                    []string `json:"groups"`
	ArbitraryPasswordlessSudo bool     `json:"arbitrary_passwordless_sudo"`
	HelperPath                string   `json:"helper_path"`
	HelperOwnerUID            int      `json:"helper_owner_uid"`
	HelperMode                uint32   `json:"helper_mode"`
}

type HostFacts struct {
	APIVersion            string       `json:"api_version"`
	RunID                 string       `json:"run_id"`
	RemoteWorkRoot        string       `json:"remote_work_root"`
	Architecture          string       `json:"architecture"`
	DiskAvailableBytes    uint64       `json:"disk_available_bytes"`
	MemoryAvailableBytes  uint64       `json:"memory_available_bytes"`
	OccupiedPorts         []int        `json:"occupied_ports"`
	UnresolvedDNSNames    []string     `json:"unresolved_dns_names"`
	UnavailableRoutes     []string     `json:"unavailable_routes"`
	Capabilities          []string     `json:"capabilities"`
	ActiveRuns            int          `json:"active_runs"`
	MaxConcurrency        int          `json:"max_concurrency"`
	AvailableSlot         int          `json:"available_slot"`
	PortBase              int          `json:"port_base"`
	PortBlockSize         int          `json:"port_block_size"`
	DockerIsolationGuard  string       `json:"docker_isolation_guard"`
	ComposeWorkspaceGuard string       `json:"compose_workspace_guard"`
	Account               AccountFacts `json:"account"`
}

type PreflightError struct {
	Failures []string
}

func (e *PreflightError) Error() string {
	return "remote preflight failed:\n  - " + strings.Join(e.Failures, "\n  - ")
}

func ValidatePreflight(target ResolvedTarget, runID string, requirements PreflightRequirements, facts HostFacts) (Allocation, error) {
	var failures []string
	add := func(format string, args ...any) { failures = append(failures, fmt.Sprintf(format, args...)) }
	if err := ValidatePreflightRequirements(requirements); err != nil {
		add("invalid preflight requirements: %v", err)
	}
	if facts.APIVersion != HostFactsAPI {
		add("host facts api_version is %q, want %q", facts.APIVersion, HostFactsAPI)
	}
	if err := ValidateRunID(runID); err != nil {
		add("%v", err)
	}
	if facts.RunID != runID {
		add("host facts run-id %q does not match %q", facts.RunID, runID)
	}
	if facts.RemoteWorkRoot != target.RemoteWorkRoot {
		add("remote work root %q does not match authorized target root %q", facts.RemoteWorkRoot, target.RemoteWorkRoot)
	}
	if requirements.Architecture == "" {
		add("required architecture is empty")
	} else if facts.Architecture != requirements.Architecture {
		add("architecture %q does not satisfy %q", facts.Architecture, requirements.Architecture)
	}
	if facts.DiskAvailableBytes < requirements.MinDiskBytes {
		add("available disk %d is below required %d bytes", facts.DiskAvailableBytes, requirements.MinDiskBytes)
	}
	if facts.MemoryAvailableBytes < requirements.MinMemoryBytes {
		add("available memory %d is below required %d bytes", facts.MemoryAvailableBytes, requirements.MinMemoryBytes)
	}
	occupied := integerSet(facts.OccupiedPorts)
	for _, port := range requirements.Ports {
		if port < 1 || port > 65535 {
			add("required port %d is invalid", port)
		} else if occupied[port] {
			add("required port %d is already occupied", port)
		}
	}
	for _, name := range facts.UnresolvedDNSNames {
		if containsString(requirements.DNSNames, name) {
			add("DNS name %q did not resolve", name)
		}
	}
	for _, route := range facts.UnavailableRoutes {
		if containsString(requirements.Routes, route) {
			add("route %q is unavailable", route)
		}
	}
	targetCapabilities := stringSet(target.Capabilities)
	hostCapabilities := stringSet(facts.Capabilities)
	if !targetCapabilities[requirements.Architecture] {
		add("target profile does not authorize architecture %q", requirements.Architecture)
	}
	for _, capability := range requirements.Capabilities {
		if !targetCapabilities[capability] {
			add("target profile does not authorize capability %q", capability)
		} else if !hostCapabilities[capability] {
			add("host does not currently provide capability %q", capability)
		}
	}
	if facts.MaxConcurrency != target.MaxConcurrency {
		add("host concurrency %d does not match target profile %d", facts.MaxConcurrency, target.MaxConcurrency)
	}
	if facts.ActiveRuns < 0 || facts.ActiveRuns >= facts.MaxConcurrency {
		add("concurrency quota is exhausted: %d/%d active", facts.ActiveRuns, facts.MaxConcurrency)
	}
	if facts.AvailableSlot < 0 || facts.AvailableSlot >= facts.MaxConcurrency {
		add("host did not return a valid available slot")
	}
	if facts.DockerIsolationGuard != "server-require-isolated-docker.sh:passed" {
		add("existing isolated Docker socket/data-root guard did not pass")
	}
	if facts.ComposeWorkspaceGuard != "runner-compose-owner:enabled" {
		add("Runner Compose workspace-owner guard is not enabled")
	}
	failures = append(failures, validateAccountFacts(target.SSHUser, facts.Account)...)
	if len(failures) > 0 {
		sort.Strings(failures)
		return Allocation{}, &PreflightError{Failures: failures}
	}
	allocation, err := AllocateRun(runID, target.RemoteWorkRoot, facts.AvailableSlot, facts.MaxConcurrency, facts.PortBase, facts.PortBlockSize)
	if err != nil {
		return Allocation{}, fmt.Errorf("allocate remote run: %w", err)
	}
	for port := allocation.PortStart; port <= allocation.PortEnd; port++ {
		if occupied[port] {
			return Allocation{}, &PreflightError{Failures: []string{fmt.Sprintf("allocated port block %d-%d is not free", allocation.PortStart, allocation.PortEnd)}}
		}
	}
	return allocation, nil
}

func validateAccountFacts(expectedSSHUser string, account AccountFacts) []string {
	var failures []string
	if account.User == "" || account.UID <= 0 {
		failures = append(failures, "remote test account must be a named non-root user")
	}
	if expectedSSHUser == "" || account.User != expectedSSHUser {
		failures = append(failures, fmt.Sprintf("audited account %q does not match SSH login user %q", account.User, expectedSSHUser))
	}
	for _, group := range account.Groups {
		switch group {
		case "root", "docker", "wheel", "sudo", "admin":
			failures = append(failures, fmt.Sprintf("remote test account must not belong to privileged group %q", group))
		}
	}
	if account.ArbitraryPasswordlessSudo {
		failures = append(failures, "remote test account has arbitrary passwordless sudo")
	}
	if filepath.Clean(account.HelperPath) != RemoteHelperPath {
		failures = append(failures, fmt.Sprintf("remote helper path is %q, want %q", account.HelperPath, RemoteHelperPath))
	}
	if account.HelperOwnerUID != 0 {
		failures = append(failures, "remote helper is not owned by root")
	}
	if account.HelperMode&0o022 != 0 {
		failures = append(failures, "remote helper is writable by group or others")
	}
	return failures
}

func DecodeHostFacts(data []byte) (HostFacts, error) {
	var facts HostFacts
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&facts); err != nil {
		return facts, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return facts, errors.New("host facts contain multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return facts, err
	}
	return facts, nil
}

func integerSet(values []int) map[int]bool {
	out := make(map[int]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
