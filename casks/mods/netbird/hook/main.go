package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const hookABI = "anas.cask/v1"

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
	if req.ABI != hookABI {
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
	if module != "netbird" {
		return nil
	}
	return calcNetbird(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "netbird" {
		return map[string]string{}, nil
	}
	return map[string]string{}, moduleNetbird(env, workdir)
}
func disabledServices(module string, env map[string]string) []string {
	if module != "netbird" {
		return nil
	}
	if env["NETBIRD_ADMINER_ENABLED"] != "true" {
		return []string{"NETBIRD_adminer", "anas_NETBIRD_adminer"}
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "netbird" {
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
func calcNetbird(e map[string]string, workdir string, secrets *secretStore) error {
	e["NETBIRD_DOMAIN"] = e["NETBIRD_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["NETBIRD_DOMAIN_PORT"] = e["NETBIRD_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["NETBIRD_DOMAIN_FULL"] = "https://" + e["NETBIRD_DOMAIN_PORT"]
	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_netbird,APP_all,Admins"
	}
	e["OIDC_RP_APPS"] = addCSV(e["OIDC_RP_APPS"], "netbird")
	e["OIDC_RP__NETBIRD__ATTR01"] = "cn,cn,1"
	e["OIDC_RP__NETBIRD__ATTR02"] = "sAMAccountName,sAMAccountName,1"
	e["OIDC_RP__NETBIRD__ATTR03"] = "email,email,1"
	e["OIDC_RP__NETBIRD__CLIENT_ID"] = "netbird"
	if e["OIDC_RP__NETBIRD__CLIENT_SECRET"] == "" {
		v, err := secrets.Ensure("OIDC_RP__NETBIRD__CLIENT_SECRET", func() (string, error) { return randomHexErr(6) })
		if err != nil {
			return err
		}
		e["OIDC_RP__NETBIRD__CLIENT_SECRET"] = v
	}
	e["OIDC_RP__NETBIRD__REDIRECT_URI"] = e["NETBIRD_DOMAIN_FULL"] + "/auth, " + e["NETBIRD_DOMAIN_FULL"] + "/silent-auth"
	e["OIDC_RP__NETBIRD__LOGOUT_REDIRECT_URI"] = e["NETBIRD_DOMAIN_FULL"]
	e["OIDC_RP__NETBIRD__ALLOW_GROUPS"] = allowGroups
	e["OIDC_RP__NETBIRD__DOMAIN"] = e["NETBIRD_DOMAIN"]
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "netbird")
	e["APPS_LIST__NETBIRD__NAME"] = defaultValue(e["APPS_LIST__NETBIRD__NAME"], "Netbird")
	e["APPS_LIST__NETBIRD__DESC"] = defaultValue(e["APPS_LIST__NETBIRD__DESC"], "Connect and Secure Your IT Infrastructure in Minutes")
	e["APPS_LIST__NETBIRD__LOGO_PATH"] = defaultValue(e["APPS_LIST__NETBIRD__LOGO_PATH"], filepath.Join(workdir, "assets", "netbird.png"))
	e["APPS_LIST__NETBIRD__URI"] = e["NETBIRD_DOMAIN_FULL"]
	e["APPS_LIST__NETBIRD__ALLOW_GROUPS"] = allowGroups
	return nil
}
func moduleNetbird(e map[string]string, _ string) error {
	e["AUTH_AUDIENCE"] = "netbird"
	e["NETBIRD_DASHBOARD_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_GRPC_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_PORT"] = e["TRAEFIK_BASE_PORT"]
	e["AUTH_CLIENT_ID"] = e["OIDC_RP__NETBIRD__CLIENT_ID"]
	e["AUTH_CLIENT_SECRET"] = e["OIDC_RP__NETBIRD__CLIENT_SECRET"]
	e["NETBIRD_SIGNAL_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_SIGNAL_PORT"] = e["TRAEFIK_BASE_PORT"]
	e["AUTH_REDIRECT_URI"] = "/auth"
	e["AUTH_SILENT_REDIRECT_URI"] = "/silent-auth"
	// Prefer the SSO provider that is actually loaded: keycloak > llng.
	oidcEndpoint := e["KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT"]
	if oidcEndpoint == "" {
		oidcEndpoint = e["LLNG_OIDC_CONFIGURATION_ENDPOINT"]
	}
	e["NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT"] = oidcEndpoint
	e["AUTH_SUPPORTED_SCOPES"] = "openid profile email"
	e["AUTH_DEVICE_AUTH_PROVIDER"] = "false"
	e["USE_AUTH0"] = "false"
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
func randomHexErr(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
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
func addCSV(s, item string) string {
	items := splitCSV(s)
	for _, existing := range items {
		if existing == item {
			return strings.Join(items, ",")
		}
	}
	items = append(items, item)
	return strings.Join(items, ",")
}
