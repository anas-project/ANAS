package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	// Generic client registration. The selected IAM translates this into its
	// own configuration format, so nothing here names an IAM implementation.
	const client = "ANAS_IAM_CLIENT__NETBIRD__"
	e[client+"INTERFACE"] = "oidc"
	e[client+"CLIENT_ID"] = "netbird"
	if e[client+"CLIENT_SECRET"] == "" {
		v, err := secrets.Ensure(client+"CLIENT_SECRET", func() (string, error) { return randomHexErr(6) })
		if err != nil {
			return err
		}
		e[client+"CLIENT_SECRET"] = v
	}
	e[client+"REDIRECT_URIS"] = e["NETBIRD_DOMAIN_FULL"] + "/auth," + e["NETBIRD_DOMAIN_FULL"] + "/silent-auth"
	e[client+"POST_LOGOUT_REDIRECT_URIS"] = e["NETBIRD_DOMAIN_FULL"]
	e[client+"SCOPES"] = "openid,profile,email"
	e[client+"ATTRIBUTES"] = "cn:cn:1,sAMAccountName:sAMAccountName:1,email:email:1"
	e[client+"ALLOW_GROUPS"] = allowGroups
	e[client+"DOMAIN"] = e["NETBIRD_DOMAIN"]
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "netbird")
	e["APPS_LIST__NETBIRD__NAME"] = defaultValue(e["APPS_LIST__NETBIRD__NAME"], "Netbird")
	e["APPS_LIST__NETBIRD__DESC"] = defaultValue(e["APPS_LIST__NETBIRD__DESC"], "Connect and Secure Your IT Infrastructure in Minutes")
	e["APPS_LIST__NETBIRD__LOGO_PATH"] = defaultValue(e["APPS_LIST__NETBIRD__LOGO_PATH"], filepath.Join(workdir, "assets", "netbird.png"))
	e["APPS_LIST__NETBIRD__URI"] = e["NETBIRD_DOMAIN_FULL"]
	e["APPS_LIST__NETBIRD__ALLOW_GROUPS"] = allowGroups
	if !validBase64Secret(e["NETBIRD_DATASTORE_ENC_KEY"], 32) {
		value, err := randomBase64Err(32)
		if err != nil {
			return err
		}
		secrets.values["NETBIRD_DATASTORE_ENC_KEY"] = value
		e["NETBIRD_DATASTORE_ENC_KEY"] = value
	}
	if e["NETBIRD_RELAY_AUTH_SECRET"] == "" {
		value, err := secrets.Ensure("NETBIRD_RELAY_AUTH_SECRET", func() (string, error) { return randomHexErr(32) })
		if err != nil {
			return err
		}
		e["NETBIRD_RELAY_AUTH_SECRET"] = value
	}
	return nil
}
func moduleNetbird(e map[string]string, _ string) error {
	e["AUTH_AUDIENCE"] = "netbird"
	e["NETBIRD_DASHBOARD_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_GRPC_API_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_MGMT_API_PORT"] = "33073"
	e["AUTH_CLIENT_ID"] = e["ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID"]
	e["AUTH_CLIENT_SECRET"] = e["ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET"]
	e["NETBIRD_SIGNAL_ENDPOINT"] = e["NETBIRD_DOMAIN_FULL"]
	e["NETBIRD_SIGNAL_PORT"] = e["TRAEFIK_BASE_PORT"]
	e["AUTH_REDIRECT_URI"] = "/auth"
	e["AUTH_SILENT_REDIRECT_URI"] = "/silent-auth"
	// Read this cask's own binding rather than branching on which IAM is
	// deployed: the runner already resolved provider and protocol, and the
	// provider published the endpoint under this cask's binding prefix.
	const binding = "ANAS_IAM_BINDING__NETBIRD__"
	if iface := e[binding+"INTERFACE"]; iface != "oidc" {
		return fmt.Errorf("netbird requires an oidc IAM binding, got %q", iface)
	}
	oidcEndpoint := e[binding+"OIDC_DISCOVERY_URL"]
	if oidcEndpoint == "" {
		return fmt.Errorf("%sOIDC_DISCOVERY_URL is empty", binding)
	}
	e["NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT"] = oidcEndpoint
	e["AUTH_AUTHORITY"] = e[binding+"OIDC_ISSUER_URL"]
	e["NETBIRD_AUTH_AUTHORITY"] = e["AUTH_AUTHORITY"]
	e["NETBIRD_AUTH_USER_ID_CLAIM"] = "sub"
	e["NETBIRD_AUTH_DEVICE_AUTH_USE_ID_TOKEN"] = "false"
	e["NETBIRD_RELAY_ENDPOINT"] = "rels://" + e["NETBIRD_DOMAIN_PORT"] + "/relay"
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
func randomBase64Err(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
func validBase64Secret(value string, size int) bool {
	decoded, err := base64.StdEncoding.DecodeString(value)
	return err == nil && len(decoded) == size
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
