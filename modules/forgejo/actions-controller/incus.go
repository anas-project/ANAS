package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	incusRemote  = "anas-actions"
	incusProject = "anas-forgejo-runners"
)

type IncusConfig struct {
	Endpoint      string
	ClientCertB64 string
	ClientKeyB64  string
	ServerCertB64 string
	Profile       string
	ConfigDir     string
}

type commandExecutor interface {
	Run(context.Context, io.Reader, ...string) ([]byte, error)
}

type execCommand struct {
	configDir string
}

func (r execCommand) Run(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "incus", args...)
	cmd.Env = append(os.Environ(), "INCUS_CONF="+r.configDir, "INCUS_PROJECT="+incusProject)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		// Incus stderr can contain operational detail. Keep it out of the
		// controller error path, where a future client regression could also
		// echo stdin.
		return nil, fmt.Errorf("incus command %q failed: %w", firstArg(args), err)
	}
	return stdout.Bytes(), nil
}

type IncusProvider struct {
	cfg IncusConfig
	run commandExecutor
}

func NewIncusProvider(cfg IncusConfig) (*IncusProvider, error) {
	if cfg.Endpoint == "" || hasControl(cfg.Endpoint) || !strings.HasPrefix(cfg.Endpoint, "https://") {
		return nil, fmt.Errorf("Incus endpoint must be an HTTPS URL")
	}
	if cfg.Profile == "" || hasControl(cfg.Profile) {
		return nil, fmt.Errorf("Incus profile is required")
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = "/run/anas-incus"
	}
	p := &IncusProvider{cfg: cfg, run: execCommand{configDir: cfg.ConfigDir}}
	if err := p.prepareCredentialFiles(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := p.run.Run(ctx, nil,
		"remote", "add", incusRemote, cfg.Endpoint, "--protocol=lxd", "--project="+incusProject,
	); err != nil {
		return nil, fmt.Errorf("prepare Incus project-scoped remote: %w", err)
	}
	if err := p.verifyRestrictedProject(ctx); err != nil {
		return nil, err
	}
	if err := p.verifyRunnerProfile(ctx); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *IncusProvider) prepareCredentialFiles() error {
	if err := os.MkdirAll(filepath.Join(p.cfg.ConfigDir, "servercerts"), 0o700); err != nil {
		return fmt.Errorf("prepare Incus client state: %w", err)
	}
	for _, item := range []struct {
		name, encoded string
		mode          os.FileMode
	}{
		{"client.crt", p.cfg.ClientCertB64, 0o600},
		{"client.key", p.cfg.ClientKeyB64, 0o600},
		{filepath.Join("servercerts", incusRemote+".crt"), p.cfg.ServerCertB64, 0o600},
	} {
		body, err := base64.StdEncoding.DecodeString(item.encoded)
		if err != nil || len(body) == 0 {
			return fmt.Errorf("Incus TLS credential is missing or not valid base64")
		}
		if err := os.WriteFile(filepath.Join(p.cfg.ConfigDir, item.name), body, item.mode); err != nil {
			return fmt.Errorf("write Incus TLS credential: %w", err)
		}
		for i := range body {
			body[i] = 0
		}
	}
	return nil
}

func (p *IncusProvider) verifyRestrictedProject(ctx context.Context) error {
	body, err := p.run.Run(ctx, nil, "project", "show", incusRemote+":"+incusProject, "--format=json")
	if err != nil {
		return fmt.Errorf("inspect Incus restricted project: %w", err)
	}
	var project struct {
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(body, &project); err != nil {
		return fmt.Errorf("decode Incus project policy: %w", err)
	}
	if project.Config["restricted"] != "true" {
		return fmt.Errorf("Incus project is not restricted")
	}
	for _, key := range []string{"limits.instances", "limits.cpu", "limits.memory", "limits.disk"} {
		if strings.TrimSpace(project.Config[key]) == "" {
			return fmt.Errorf("Incus project quota %s is missing", key)
		}
	}
	return nil
}

func (p *IncusProvider) verifyRunnerProfile(ctx context.Context) error {
	body, err := p.run.Run(ctx, nil, "profile", "show", incusRemote+":"+p.cfg.Profile, "--format=json")
	if err != nil {
		return fmt.Errorf("inspect Incus Runner profile: %w", err)
	}
	var profile struct {
		Config  map[string]string            `json:"config"`
		Devices map[string]map[string]string `json:"devices"`
	}
	if err := json.Unmarshal(body, &profile); err != nil {
		return fmt.Errorf("decode Incus Runner profile: %w", err)
	}
	if profile.Config["user.anas.egress"] != "restricted" {
		return fmt.Errorf("Incus Runner profile does not declare restricted egress")
	}
	for key, value := range profile.Config {
		lower := strings.ToLower(key + "=" + value)
		if key == "raw.qemu" || key == "raw.qemu.conf" || strings.HasPrefix(key, "cloud-init.") ||
			strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") {
			return fmt.Errorf("Incus Runner profile contains forbidden configuration")
		}
	}
	nics := 0
	for _, device := range profile.Devices {
		switch device["type"] {
		case "nic":
			nics++
			if device["network"] != "anas-forgejo-runners" || device["parent"] != "" || device["nictype"] != "" {
				return fmt.Errorf("Incus Runner profile NIC is not attached to the managed network")
			}
		case "disk":
			if device["source"] != "" || device["path"] != "/" {
				return fmt.Errorf("Incus Runner profile contains a host or non-root disk mount")
			}
		default:
			return fmt.Errorf("Incus Runner profile contains a forbidden device type")
		}
	}
	if nics != 1 {
		return fmt.Errorf("Incus Runner profile must contain exactly one managed NIC")
	}
	return nil
}

func (p *IncusProvider) Create(ctx context.Context, spec InstanceSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	_, err := p.run.Run(ctx, nil,
		"init", incusRemote+":"+spec.Image, incusRemote+":"+spec.ID,
		"--vm", "--profile="+p.cfg.Profile,
		"--config=limits.cpu="+strconv.Itoa(spec.CPU),
		"--config=limits.memory="+strconv.Itoa(spec.MemoryMiB)+"MiB",
		"--config=security.secureboot=true",
		"--config=user.anas.managed=true",
		"--config=user.anas.workload="+spec.WorkloadID,
		"--device=root,size="+strconv.Itoa(spec.DiskGiB)+"GiB",
	)
	if err != nil {
		return fmt.Errorf("create managed Runner VM: %w", err)
	}
	return nil
}

func (p *IncusProvider) Inspect(ctx context.Context, id string) (Instance, error) {
	if !instanceIDPattern.MatchString(id) {
		return Instance{}, fmt.Errorf("compute instance identity is invalid")
	}
	body, err := p.run.Run(ctx, nil, "list", incusRemote+":"+id, "--format=json")
	if err != nil {
		return Instance{}, err
	}
	instances, err := decodeIncusInstances(body)
	if err != nil {
		return Instance{}, err
	}
	if len(instances) == 0 {
		return Instance{ID: id, State: "missing"}, nil
	}
	return instances[0], nil
}

func (p *IncusProvider) Start(ctx context.Context, id string) error {
	if err := p.instanceCommand(ctx, "start", id); err != nil {
		return err
	}
	for {
		if _, err := p.run.Run(ctx, nil, "exec", incusRemote+":"+id, "--", "/usr/bin/test", "-x", "/usr/local/libexec/anas-forgejo-runner-start"); err == nil {
			return nil
		}
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for managed Runner VM agent: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (p *IncusProvider) Stop(ctx context.Context, id string) error {
	return p.instanceCommand(ctx, "stop", id, "--force")
}

func (p *IncusProvider) Delete(ctx context.Context, id string) error {
	instance, err := p.Inspect(ctx, id)
	if err != nil {
		return fmt.Errorf("inspect managed Runner VM before delete: %w", err)
	}
	if instance.State == "missing" {
		return nil
	}
	return p.instanceCommand(ctx, "delete", id, "--force")
}

func (p *IncusProvider) instanceCommand(ctx context.Context, action, id string, extra ...string) error {
	if !instanceIDPattern.MatchString(id) {
		return fmt.Errorf("compute instance identity is invalid")
	}
	args := append([]string{action, incusRemote + ":" + id}, extra...)
	if _, err := p.run.Run(ctx, nil, args...); err != nil {
		return fmt.Errorf("%s managed Runner VM: %w", action, err)
	}
	return nil
}

func (p *IncusProvider) ExecStdin(ctx context.Context, id string, command []string, stdin io.Reader) error {
	if !instanceIDPattern.MatchString(id) {
		return fmt.Errorf("compute instance identity is invalid")
	}
	if stdin == nil {
		return fmt.Errorf("compute exec requires a stdin stream")
	}
	if err := validateGuestCommand(command); err != nil {
		return err
	}
	args := append([]string{"exec", incusRemote + ":" + id, "--"}, command...)
	if _, err := p.run.Run(ctx, stdin, args...); err != nil {
		return fmt.Errorf("start one-job Runner in guest: %w", err)
	}
	return nil
}

func (p *IncusProvider) ListManaged(ctx context.Context) ([]Instance, error) {
	body, err := p.run.Run(ctx, nil, "list", incusRemote+":", "--format=json")
	if err != nil {
		return nil, err
	}
	instances, err := decodeIncusInstances(body)
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(instances))
	for _, instance := range instances {
		if instanceIDPattern.MatchString(instance.ID) {
			out = append(out, instance)
		}
	}
	return out, nil
}

func decodeIncusInstances(body []byte) ([]Instance, error) {
	var raw []struct {
		Name   string            `json:"name"`
		Status string            `json:"status"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("decode Incus instance list: %w", err)
	}
	out := make([]Instance, 0, len(raw))
	for _, item := range raw {
		if item.Config["user.anas.managed"] != "true" {
			continue
		}
		out = append(out, Instance{ID: item.Name, State: strings.ToLower(item.Status)})
	}
	return out, nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

var _ ComputeProvider = (*IncusProvider)(nil)
