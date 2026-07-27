package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	if module != "lego" {
		return nil
	}
	return calcLego(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "lego" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "lego" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "lego" {
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
func calcLego(e map[string]string, _ string, _ *secretStore) error {
	e["LEGO_EMAIL"] = defaultValue(e["LEGO_EMAIL"], e["EMAIL"])
	e["LEGO_DATA_PATH"] = defaultValue(e["LEGO_DATA_PATH"], filepath.Join(e["DATA_PATH"], "lego/certs"))
	e["LEGO_CERTS_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certificates") + string(os.PathSeparator)
	e["LEGO_CERTS_USER1000_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certs1000") + string(os.PathSeparator)
	e["LEGO_CERT_NAME"] = e["BASE_DOMAIN"] + ".crt"
	e["LEGO_KEY_NAME"] = e["BASE_DOMAIN"] + ".key"
	e["LEGO_CA_CERT_NAME"] = e["BASE_DOMAIN"] + ".issuer.crt"

	// Issuer-neutral contract for consumers. LEGO_* stays this cask's private
	// vocabulary: naming the implementation in something every other cask
	// reads is the coupling the IAM binding just removed, and it would have to
	// be unpicked the day a different issuer appears.
	//
	// Consumers mount ANAS_TLS_CERTS_DIR at /certs and reference the names
	// below, matching the convention the existing casks already use.
	e["ANAS_TLS_CERTS_DIR"] = e["LEGO_CERTS_PATH"]
	e["ANAS_TLS_CERT_NAME"] = e["LEGO_CERT_NAME"]
	e["ANAS_TLS_KEY_NAME"] = e["LEGO_KEY_NAME"]
	// Whatever signed the serving certificate: the public intermediate under
	// ACME, the internal root otherwise. Consumers trust it without knowing
	// which, so no cask branches on the certificate source.
	e["ANAS_TLS_ISSUER_NAME"] = e["LEGO_CA_CERT_NAME"]
	// The internal root is published under a stable name even while ACME
	// serves traffic, because bootstrap and renewal failures fall back to it.
	e["ANAS_TLS_INTERNAL_CA_NAME"] = "anas-internal-ca.crt"
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
