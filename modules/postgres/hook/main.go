package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

// The runner sends the module-hook ABI it speaks; this unreleased format has no legacy aliases.
var supportedHookABIs = []string{"anas.module-hook/v1"}

func supportedABI(v string) bool {
	for _, abi := range supportedHookABIs {
		if v == abi {
			return true
		}
	}
	return false
}

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

type hookResponse struct {
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	DisableServices []string          `json:"disable_services,omitempty"`
	DockerCopies    []dockerCopy      `json:"docker_copies,omitempty"`
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

type secretStore struct {
	values map[string]string
}

func (s *secretStore) Ensure(key string, gen func() (string, error)) (string, error) {
	if v := s.values[key]; v != "" {
		return v, nil
	}
	v, err := gen()
	if err != nil {
		return "", err
	}
	s.values[key] = v
	return v, nil
}

func main() {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var req hookRequest
	if err := json.Unmarshal(b, &req); err != nil {
		fail(err)
	}
	if !supportedABI(req.ABI) {
		fail(fmt.Errorf("unsupported ABI %q", req.ABI))
	}
	resp, err := handle(req)
	if err != nil {
		fail(err)
	}
	if resp.Env == nil {
		resp.Env = map[string]string{}
	}
	if resp.Secrets == nil {
		resp.Secrets = map[string]string{}
	}
	out, err := json.Marshal(resp)
	if err != nil {
		fail(err)
	}
	fmt.Print(string(out))
}
func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
func handle(req hookRequest) (hookResponse, error) {
	env := cloneMap(req.Env)
	secrets := &secretStore{values: cloneMap(req.Secrets)}
	switch req.Phase {
	case "calculate":
		if err := calculate(req.Module, env, req.Workdir, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env":
		files, err := renderEnv(req.Module, env, req.Workdir)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Files: files}, nil
	case "services":
		return hookResponse{DisableServices: disabledServices(req.Module, env)}, nil
	case "after_start":
		if req.Module == "postgres" {
			if err := maintainExtensions(env); err != nil {
				return hookResponse{}, err
			}
		}
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "postgres" {
		return nil
	}
	return calcPostgres(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "postgres" {
		return map[string]string{}, nil
	}
	env["POSTGRES_HOST_AUTH_METHOD"] = "scram-sha-256"
	env["POSTGRES_INITDB_ARGS"] = "--auth-local=scram-sha-256 --auth-host=scram-sha-256"
	env["ADMINER_DESIGN"] = "nette"
	return map[string]string{}, nil
}

// The runner enables upgrade only after stopping affected writers and taking
// its ANAS recovery point. Ordinary start/restart checks state without updates.
func maintenanceCommand(env map[string]string) ([]string, error) {
	container := env["POSTGRES_HOST"]
	if container == "" {
		return nil, fmt.Errorf("PostgreSQL lifecycle requires POSTGRES_HOST")
	}
	mode := "inspect"
	if env["ANAS_POSTGRES_EXTENSION_MAINTENANCE"] == "true" {
		mode = "upgrade"
	}
	return []string{"exec", container, "/usr/local/bin/anas-postgres-extensions", mode}, nil
}

func maintainExtensions(env map[string]string) error {
	args, err := maintenanceCommand(env)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("PostgreSQL extension readiness/maintenance failed: %w", err)
	}
	return nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "postgres" {
		return nil
	}
	if env["POSTGRES_ADMINER_ENABLED"] != "true" {
		return []string{"postgres_adminer"}
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "postgres" {
		return nil
	}
	return nil
}
func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}
func changed(old, cur map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cur {
		if old[k] != v {
			out[k] = v
		}
	}
	return out
}
func calcPostgres(e map[string]string, _ string, secrets *secretStore) error {
	e["POSTGRES_NETWORK_NAME"] = defaultValue(e["POSTGRES_NETWORK_NAME"], e["NETWORK_PREFIX"]+"postgres")
	if e["POSTGRES_PASSWORD"] == "" {
		password, err := ensureRandomPassword("POSTGRES_PASSWORD", secrets)
		if err != nil {
			return err
		}
		e["POSTGRES_PASSWORD"] = password
	}
	e["POSTGRES_USER"] = e["POSTGRES_USERNAME"]
	e["POSTGRES_HOST"] = e["CONTAINER_PREFIX"] + "postgres"
	e["POSTGRES_PORT"] = "5432"
	e["POSTGRES_HOST_PORT"] = e["POSTGRES_HOST"] + ":" + e["POSTGRES_PORT"]
	e["POSTGRES_ADMINER_DOMAIN_PREFIX"] = defaultValue(e["POSTGRES_ADMINER_DOMAIN_PREFIX"], "postgres_adminer")
	e["POSTGRES_ADMINER_DOMAIN"] = e["POSTGRES_ADMINER_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	return nil
}

func ensureRandomPassword(key string, secrets *secretStore) (string, error) {
	if secrets == nil {
		return "", fmt.Errorf("secret store is required to generate %s", key)
	}
	return secrets.Ensure(key, func() (string, error) {
		buf := make([]byte, 24)
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		return "Aa1!" + hex.EncodeToString(buf), nil
	})
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
