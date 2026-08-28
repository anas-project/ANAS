package remotetest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"gopkg.in/yaml.v3"
)

const (
	HelperConfigAPI          = "anas.remote-test-helper/v1"
	RemoteHelperConfigPath   = "/etc/anas-test-helper.yml"
	RemoteIsolationGuardPath = "/usr/local/libexec/anas/server-require-isolated-docker.sh"
)

type HelperConfig struct {
	APIVersion            string   `yaml:"api_version"`
	RemoteWorkRoot        string   `yaml:"remote_work_root"`
	Capabilities          []string `yaml:"capabilities"`
	MaxConcurrency        int      `yaml:"max_concurrency"`
	PortBase              int      `yaml:"port_base"`
	PortBlockSize         int      `yaml:"port_block_size"`
	TestUser              string   `yaml:"test_user"`
	DockerHost            string   `yaml:"docker_host"`
	ComposeWorkspaceGuard bool     `yaml:"compose_workspace_guard"`
}

func ParseHelperConfig(data []byte) (HelperConfig, error) {
	var config HelperConfig
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return config, errors.New("multiple YAML documents are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return config, err
	}
	if config.APIVersion != HelperConfigAPI {
		return config, fmt.Errorf("api_version must be %q", HelperConfigAPI)
	}
	if err := validateRemoteWorkRoot(config.RemoteWorkRoot); err != nil {
		return config, err
	}
	profile := TargetProfile{
		SSHAlias: "config-validation", RemoteWorkRoot: config.RemoteWorkRoot,
		Capabilities: config.Capabilities, MaxConcurrency: config.MaxConcurrency,
	}
	if err := validateTargetProfile(profile); err != nil {
		return config, err
	}
	if config.PortBase < 1024 || config.PortBase > 65535 || config.PortBlockSize < 16 ||
		config.PortBlockSize > 65536-config.PortBase || config.MaxConcurrency > (65536-config.PortBase)/config.PortBlockSize {
		return config, errors.New("helper port allocation is invalid")
	}
	if !targetNamePattern.MatchString(config.TestUser) {
		return config, errors.New("test_user is invalid")
	}
	if config.DockerHost == "unix:///run/docker.sock" || config.DockerHost == "unix:///var/run/docker.sock" || !strings.HasPrefix(config.DockerHost, "unix:///") {
		return config, errors.New("docker_host must be an explicit non-default unix socket")
	}
	dockerSocket := strings.TrimPrefix(config.DockerHost, "unix://")
	if !filepath.IsAbs(dockerSocket) || filepath.Clean(dockerSocket) != dockerSocket || strings.ContainsAny(dockerSocket, "\x00\r\n") {
		return config, errors.New("docker_host must contain one clean absolute Unix socket path")
	}
	lowerDockerHost := strings.ToLower(config.DockerHost)
	if !strings.Contains(lowerDockerHost, "anas") || (!strings.Contains(lowerDockerHost, "test") && !strings.Contains(lowerDockerHost, "e2e")) {
		return config, errors.New("docker_host must identify an ANAS test/e2e scope")
	}
	if !config.ComposeWorkspaceGuard {
		return config, errors.New("compose_workspace_guard must be enabled")
	}
	return config, nil
}

func LoadHelperConfig(path string) (HelperConfig, error) {
	if filepath.Clean(path) != RemoteHelperConfigPath {
		return HelperConfig{}, fmt.Errorf("helper config path must be %s", RemoteHelperConfigPath)
	}
	if err := requireRootOwnedFile(path); err != nil {
		return HelperConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return HelperConfig{}, err
	}
	return ParseHelperConfig(data)
}

func CollectHostFacts(ctx context.Context, config HelperConfig, requirements PreflightRequirements, runID string) (HostFacts, error) {
	if err := ValidateRunID(runID); err != nil {
		return HostFacts{}, err
	}
	if err := ValidatePreflightRequirements(requirements); err != nil {
		return HostFacts{}, err
	}
	if _, err := ParseHelperConfig(mustYAMLConfig(config)); err != nil {
		return HostFacts{}, err
	}
	if err := requireRootOwnedDirectory(config.RemoteWorkRoot); err != nil {
		return HostFacts{}, err
	}
	disk, err := availableDisk(config.RemoteWorkRoot)
	if err != nil {
		return HostFacts{}, err
	}
	memory, err := availableMemory(ctx)
	if err != nil {
		return HostFacts{}, err
	}
	activeRuns, slot, err := inspectRunSlots(config.RemoteWorkRoot, config.MaxConcurrency, runID)
	if err != nil {
		return HostFacts{}, err
	}
	ports := append([]int(nil), requirements.Ports...)
	for currentSlot := 0; currentSlot < config.MaxConcurrency; currentSlot++ {
		start := config.PortBase + currentSlot*config.PortBlockSize
		for port := start; port < start+config.PortBlockSize; port++ {
			ports = append(ports, port)
		}
	}
	occupied := occupiedTCPPorts(ports)
	var unresolved []string
	for _, name := range requirements.DNSNames {
		if _, err := net.DefaultResolver.LookupHost(ctx, name); err != nil {
			unresolved = append(unresolved, name)
		}
	}
	var unavailableRoutes []string
	ipCommand, ipCommandErr := fixedExecutable("/usr/sbin/ip", "/usr/bin/ip", "/sbin/ip")
	for _, route := range requirements.Routes {
		address := strings.SplitN(route, "/", 2)[0]
		if ipCommandErr != nil {
			unavailableRoutes = append(unavailableRoutes, route)
			continue
		}
		command := exec.CommandContext(ctx, ipCommand, "route", "get", address)
		if err := command.Run(); err != nil {
			unavailableRoutes = append(unavailableRoutes, route)
		}
	}
	isolationStatus := "server-require-isolated-docker.sh:failed"
	dockerCommand, dockerCommandErr := fixedExecutable("/usr/bin/docker", "/usr/local/bin/docker")
	if err := requireRootOwnedFile(RemoteIsolationGuardPath); err == nil && dockerCommandErr == nil {
		command := exec.CommandContext(ctx, "/bin/sh", "-c", `. "$1"`, "sh", RemoteIsolationGuardPath)
		command.Env = []string{
			"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "DOCKER_HOST=" + config.DockerHost,
			"ANAS_TEST_DOCKER_HOST=" + config.DockerHost, "DOCKER_CMD=" + dockerCommand,
		}
		if err := command.Run(); err == nil {
			isolationStatus = "server-require-isolated-docker.sh:passed"
		}
	}
	account, err := inspectAccount(config.TestUser)
	if err != nil {
		return HostFacts{}, err
	}
	return HostFacts{
		APIVersion: HostFactsAPI, RunID: runID, RemoteWorkRoot: config.RemoteWorkRoot,
		Architecture: runtime.GOARCH, DiskAvailableBytes: disk, MemoryAvailableBytes: memory,
		OccupiedPorts: occupied, UnresolvedDNSNames: unresolved, UnavailableRoutes: unavailableRoutes,
		Capabilities: append([]string(nil), config.Capabilities...), ActiveRuns: activeRuns, MaxConcurrency: config.MaxConcurrency,
		AvailableSlot: slot, PortBase: config.PortBase, PortBlockSize: config.PortBlockSize,
		DockerIsolationGuard: isolationStatus, ComposeWorkspaceGuard: "runner-compose-owner:enabled",
		Account: account,
	}, nil
}

func availableDisk(root string) (uint64, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(root, &stats); err != nil {
		return 0, err
	}
	return stats.Bavail * uint64(stats.Bsize), nil
}

func availableMemory(ctx context.Context) (uint64, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 && fields[0] == "MemAvailable:" {
				value, parseErr := strconv.ParseUint(fields[1], 10, 64)
				return value * 1024, parseErr
			}
		}
	}
	output, commandErr := exec.CommandContext(ctx, "/usr/sbin/sysctl", "-n", "hw.memsize").Output()
	if commandErr != nil {
		return 0, errors.New("cannot inspect available memory")
	}
	return strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
}

func inspectRunSlots(root string, maxConcurrency int, runID string) (int, int, error) {
	leaseRoot := filepath.Join(root, ".leases")
	active := 0
	available := -1
	for slot := 0; slot < maxConcurrency; slot++ {
		data, err := os.ReadFile(filepath.Join(leaseRoot, fmt.Sprintf("slot-%d", slot)))
		if os.IsNotExist(err) {
			if available == -1 {
				available = slot
			}
			continue
		}
		if err != nil {
			return 0, -1, err
		}
		owner := strings.TrimSpace(string(data))
		if owner == runID && available == -1 {
			available = slot
			continue
		}
		if owner != "" {
			active++
		}
	}
	return active, available, nil
}

func occupiedTCPPorts(ports []int) []int {
	seen := make(map[int]bool)
	var occupied []int
	for _, port := range ports {
		if seen[port] {
			continue
		}
		seen[port] = true
		listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			occupied = append(occupied, port)
			continue
		}
		_ = listener.Close()
	}
	sort.Ints(occupied)
	return occupied
}

func inspectAccount(name string) (AccountFacts, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return AccountFacts{}, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return AccountFacts{}, err
	}
	groupIDs, err := account.GroupIds()
	if err != nil {
		return AccountFacts{}, err
	}
	var groups []string
	for _, groupID := range groupIDs {
		group, lookupErr := user.LookupGroupId(groupID)
		if lookupErr == nil {
			groups = append(groups, group.Name)
		}
	}
	sort.Strings(groups)
	if err := requireRootOwnedFile(RemoteHelperPath); err != nil {
		return AccountFacts{}, err
	}
	info, err := os.Stat(RemoteHelperPath)
	if err != nil {
		return AccountFacts{}, err
	}
	arbitrarySudo, err := hasArbitraryPasswordlessSudo(name)
	if err != nil {
		return AccountFacts{}, err
	}
	stats, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return AccountFacts{}, errors.New("cannot inspect helper ownership")
	}
	return AccountFacts{
		User: name, UID: uid, Groups: groups, ArbitraryPasswordlessSudo: arbitrarySudo,
		HelperPath: RemoteHelperPath, HelperOwnerUID: int(stats.Uid), HelperMode: uint32(info.Mode().Perm()),
	}, nil
}

func hasArbitraryPasswordlessSudo(name string) (bool, error) {
	sudoCommand, err := fixedExecutable("/usr/bin/sudo", "/bin/sudo")
	if err != nil {
		return false, err
	}
	command := exec.Command(sudoCommand, "-n", "-l", "-U", name)
	output, err := command.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("audit sudo policy for %s: %w", name, err)
	}
	lower := strings.ToLower(string(output))
	for _, line := range strings.Split(lower, "\n") {
		marker := strings.Index(line, "nopasswd:")
		if marker == -1 {
			continue
		}
		commandSpec := strings.TrimSpace(line[marker+len("nopasswd:"):])
		if commandSpec != strings.ToLower(RemoteHelperPath) {
			return true, nil
		}
	}
	return false, nil
}

func fixedExecutable(candidates ...string) (string, error) {
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		stats, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !ok || stats.Uid != 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("none of the fixed executable paths is a root-owned non-writable file: %s", strings.Join(candidates, ", "))
}

func requireRootOwnedFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("%s must be a root-owned regular file not writable by group/others", path)
	}
	stats, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stats.Uid != 0 {
		return fmt.Errorf("%s must be owned by root", path)
	}
	return nil
}

func requireRootOwnedDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stats, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 || !ok || stats.Uid != 0 {
		return fmt.Errorf("%s must be a root-owned directory not writable by group/others", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return fmt.Errorf("%s must not traverse a symlink", path)
	}
	return nil
}

func mustYAMLConfig(config HelperConfig) []byte {
	data, _ := yaml.Marshal(config)
	return data
}
