package remotetest

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const RemoteHelperPath = "/usr/local/libexec/anas/anas-test-helper"

type SSHConfiguration struct {
	Alias           string
	HostName        string
	User            string
	Port            int
	StrictHostKey   string
	KnownHostsFiles []string
}

func InspectSSHConfiguration(ctx context.Context, alias string) (SSHConfiguration, error) {
	if !sshAliasPattern.MatchString(alias) || strings.ContainsAny(alias, "@:/\\") {
		return SSHConfiguration{}, errors.New("SSH destination must be a config alias")
	}
	args := []string{"-G", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "--", alias}
	command := exec.CommandContext(ctx, "ssh", args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return SSHConfiguration{}, fmt.Errorf("inspect SSH config alias %q: %w: %s", alias, err, strings.TrimSpace(string(output)))
	}
	configuration, err := ParseSSHConfiguration(alias, output)
	if err != nil {
		return SSHConfiguration{}, err
	}
	if err := VerifyKnownHostBinding(ctx, configuration); err != nil {
		return SSHConfiguration{}, err
	}
	return configuration, nil
}

func ParseSSHConfiguration(alias string, output []byte) (SSHConfiguration, error) {
	configuration := SSHConfiguration{Alias: alias, Port: 22}
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		key, value, found := strings.Cut(strings.TrimSpace(scanner.Text()), " ")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(key) {
		case "hostname":
			configuration.HostName = value
		case "user":
			configuration.User = value
		case "port":
			port, err := strconv.Atoi(value)
			if err != nil {
				return configuration, fmt.Errorf("SSH config has invalid port %q", value)
			}
			configuration.Port = port
		case "stricthostkeychecking":
			configuration.StrictHostKey = strings.ToLower(value)
		case "userknownhostsfile":
			configuration.KnownHostsFiles = append(configuration.KnownHostsFiles, strings.Fields(value)...)
		}
	}
	if err := scanner.Err(); err != nil {
		return configuration, err
	}
	if configuration.HostName == "" || configuration.User == "" {
		return configuration, errors.New("SSH alias must resolve a hostname and user")
	}
	if configuration.User == "root" || configuration.User == "0" {
		return configuration, errors.New("SSH alias must not log in as root")
	}
	if configuration.Port < 1 || configuration.Port > 65535 {
		return configuration, errors.New("SSH alias resolves an invalid port")
	}
	if configuration.StrictHostKey != "yes" && configuration.StrictHostKey != "true" {
		return configuration, fmt.Errorf("SSH alias does not enforce StrictHostKeyChecking=yes (resolved %q)", configuration.StrictHostKey)
	}
	if len(configuration.KnownHostsFiles) == 0 {
		return configuration, errors.New("SSH alias has no UserKnownHostsFile")
	}
	for _, name := range configuration.KnownHostsFiles {
		if name == "/dev/null" || strings.EqualFold(name, "none") {
			return configuration, errors.New("SSH alias disables known-host persistence")
		}
		if strings.ContainsAny(name, "\r\n") {
			return configuration, errors.New("SSH known-host path contains a newline")
		}
	}
	return configuration, nil
}

func VerifyKnownHostBinding(ctx context.Context, configuration SSHConfiguration) error {
	host := configuration.HostName
	if configuration.Port != 22 {
		host = fmt.Sprintf("[%s]:%d", host, configuration.Port)
	}
	var checked []string
	for _, configuredPath := range configuration.KnownHostsFiles {
		name, err := expandHomePath(configuredPath)
		if err != nil {
			return err
		}
		checked = append(checked, name)
		info, err := os.Stat(name)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		command := exec.CommandContext(ctx, "ssh-keygen", "-F", host, "-f", name)
		if output, err := command.CombinedOutput(); err == nil && len(bytes.TrimSpace(output)) > 0 {
			return nil
		}
	}
	return fmt.Errorf("known-host binding for %s was not found in %s", host, strings.Join(checked, ", "))
}

func BuildRemotePreflightCommand(target ResolvedTarget, runID string, requirements PreflightRequirements) ([]string, error) {
	if err := ValidateRunID(runID); err != nil {
		return nil, err
	}
	if !sshAliasPattern.MatchString(target.SSHAlias) {
		return nil, errors.New("target has an invalid SSH config alias")
	}
	if err := ValidatePreflightRequirements(requirements); err != nil {
		return nil, err
	}
	command := []string{
		"ssh", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "--", target.SSHAlias,
		"sudo", "-n", RemoteHelperPath, "preflight", "--run-id", runID, "--format", "json",
		"--architecture", requirements.Architecture,
		"--min-disk-bytes", strconv.FormatUint(requirements.MinDiskBytes, 10),
		"--min-memory-bytes", strconv.FormatUint(requirements.MinMemoryBytes, 10),
	}
	for _, port := range requirements.Ports {
		command = append(command, "--port", strconv.Itoa(port))
	}
	for _, name := range requirements.DNSNames {
		command = append(command, "--dns", name)
	}
	for _, route := range requirements.Routes {
		command = append(command, "--route", route)
	}
	for _, capability := range requirements.Capabilities {
		command = append(command, "--capability", capability)
	}
	return command, nil
}

func expandHomePath(name string) (string, error) {
	if strings.HasPrefix(name, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, filepath.FromSlash(strings.TrimPrefix(name, "~/"))), nil
	}
	if strings.Contains(name, "%") || strings.Contains(name, "$HOME") {
		return "", fmt.Errorf("SSH known-host path %q was not expanded by ssh -G", name)
	}
	return name, nil
}
