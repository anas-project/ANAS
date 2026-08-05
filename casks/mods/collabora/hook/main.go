package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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
	if module != "collabora" {
		return nil
	}
	return domainCalc("COLLABORA", "collabora")(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "collabora" {
		return map[string]string{}, nil
	}
	return map[string]string{}, moduleCollabora(env, workdir)
}
func disabledServices(module string, env map[string]string) []string {
	if module != "collabora" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "collabora" {
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
func domainCalc(prefix, service string) func(map[string]string, string, *secretStore) error {
	return func(e map[string]string, _ string, _ *secretStore) error {
		e[prefix+"_HOSTNAME"] = e["CONTAINER_PREFIX"] + service
		e[prefix+"_DOMAIN"] = e[prefix+"_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
		e[prefix+"_DOMAIN_PORT"] = e[prefix+"_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
		e[prefix+"_DOMAIN_FULL"] = "https://" + e[prefix+"_DOMAIN_PORT"]
		return nil
	}
}
func moduleCollabora(e map[string]string, _ string) error {
	e["COLLABORA_ADMIN_USERNAME"] = e["SAMBA_DC_ADMIN_NAME"]
	e["COLLABORA_ADMIN_PASSWORD"] = e["DEFAULT_SERVICE_ROOT_PASSWORD"]
	e["COLLABORA_ALIAS_GROUP"] = e["NEXTCLOUD_DOMAIN_FULL"]
	e["COLLABORA_EXTRA_PARAMS"] = "--o:ssl.enable=false --o:ssl.termination=true --o:logging.level=" + e["COLLABORA_LOG_LEVEL"] + " --o:autosave.autosave_interval_secs=" + e["COLLABORA_AUTO_SAVE"] + " --o:welcome.enable=false --o:fetch_update_check=0 --o:allow_update_popup=false"
	return nil
}
