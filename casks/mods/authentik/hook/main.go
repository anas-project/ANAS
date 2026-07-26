package main

import (
	"crypto/rand"
	"encoding/hex"
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
	if req.Module != "authentik" {
		return hookResponse{}, nil
	}
	switch req.Phase {
	case "calculate":
		if err := calcAuthentik(env, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env":
		files, err := renderAuthentik(env)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Files: files}, nil
	default:
		return hookResponse{}, nil
	}
}

func calcAuthentik(e map[string]string, secrets *secretStore) error {
	e["AUTHENTIK_DOMAIN"] = e["AUTHENTIK_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["AUTHENTIK_DOMAIN_PORT"] = e["AUTHENTIK_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["AUTHENTIK_DOMAIN_FULL"] = "https://" + e["AUTHENTIK_DOMAIN_PORT"]
	e["AUTHENTIK_PASSWORD"] = defaultValue(e["AUTHENTIK_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])

	switch e["AUTHENTIK_DB_TYPE"] {
	case "postgres":
		e["AUTHENTIK_NETWORK_DB"] = e["POSTGRES_NETWORK_NAME"]
		e["AUTHENTIK_POSTGRESQL__HOST"] = e["POSTGRES_HOST"]
		e["AUTHENTIK_POSTGRESQL__PORT"] = e["POSTGRES_PORT"]
		e["AUTHENTIK_POSTGRESQL__USER"] = e["POSTGRES_USERNAME"]
		e["AUTHENTIK_POSTGRESQL__PASSWORD"] = e["POSTGRES_PASSWORD"]
		e["AUTHENTIK_POSTGRESQL__NAME"] = e["AUTHENTIK_DB_NAME"]
	case "mariadb":
		return fmt.Errorf("authentik requires postgres; set authentik.db_type to postgres")
	default:
		return fmt.Errorf("AUTHENTIK_DB_TYPE must be resolved to postgres")
	}
	e["AUTHENTIK_REDIS__HOST"] = e["CONTAINER_PREFIX"] + "authentik_redis"
	e["AUTHENTIK_LOG_LEVEL"] = defaultValue(e["AUTHENTIK_LOG_LEVEL"], "warn")

	key, err := secrets.Ensure("AUTHENTIK_SECRET_KEY", func() (string, error) { return randomHexErr(32) })
	if err != nil {
		return err
	}
	e["AUTHENTIK_SECRET_KEY"] = key
	e["AUTHENTIK_BOOTSTRAP_PASSWORD"] = e["AUTHENTIK_PASSWORD"]
	e["AUTHENTIK_BOOTSTRAP_EMAIL"] = e["EMAIL"]

	if err := ensureSigningKeypair(e, secrets); err != nil {
		return err
	}
	return publishIAMEndpoints(e)
}

func renderAuthentik(e map[string]string) (map[string]string, error) {
	blueprint, err := renderClientBlueprint(e)
	if err != nil {
		return nil, err
	}
	if blueprint == "" {
		return map[string]string{}, nil
	}
	return map[string]string{"blueprints/anas-clients.yaml": blueprint}, nil
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

func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func randomHexErr(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func splitCSV(s string) []string {
	out := []string{}
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
