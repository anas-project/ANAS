package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// The runner sends the ABI it speaks. Both are accepted so a cask bundle
// stays usable with a v1 runner; only the v2 fields are runner-side.
var supportedHookABIs = []string{"anas.cask/v1", "anas.cask/v2"}

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
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "ddns" {
		return nil
	}
	return calcDDNS(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "ddns" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "ddns" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "ddns" {
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
func calcDDNS(e map[string]string, _ string, _ *secretStore) error {
	e["DDNS_DNS_SERVER"] = defaultValue(e["DDNS_DNS_SERVER"], e["DNS_SERVER"])
	e["DDNS_DOMAIN"] = e["DDNS_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	if e["DNS_PROVIDER"] == "dnspod" {
		settings := []string{}
		if e["IPv4"] == "true" {
			settings = append(settings,
				fmt.Sprintf(`{"provider":"dnspod","token":%q,"domain":%q,"host":"@","ip_version":"ipv4"}`, e["DNSPOD_API_KEY"], e["BASE_DOMAIN"]),
				fmt.Sprintf(`{"provider":"dnspod","token":%q,"domain":%q,"host":"*","ip_version":"ipv4"}`, e["DNSPOD_API_KEY"], e["BASE_DOMAIN"]),
			)
		}
		if e["IPv6"] == "true" {
			settings = append(settings,
				fmt.Sprintf(`{"provider":"dnspod","token":%q,"domain":%q,"host":"@","ip_version":"ipv6"}`, e["DNSPOD_API_KEY"], e["BASE_DOMAIN"]),
				fmt.Sprintf(`{"provider":"dnspod","token":%q,"domain":%q,"host":"*","ip_version":"ipv6"}`, e["DNSPOD_API_KEY"], e["BASE_DOMAIN"]),
			)
		}
		e["DDNS_CONFIG"] = `{"settings":[` + strings.Join(settings, ",") + `]}`
	}
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
