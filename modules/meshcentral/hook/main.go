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
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "meshcentral" {
		return nil
	}
	return calcMeshcentral(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "meshcentral" {
		return map[string]string{}, nil
	}
	return map[string]string{}, moduleMeshcentral(env)
}
func disabledServices(module string, env map[string]string) []string {
	if module != "meshcentral" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "meshcentral" {
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
func calcMeshcentral(e map[string]string, _ string, secrets *secretStore) error {
	e["MESHCENTRAL_DOMAIN"] = e["MESHCENTRAL_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["MESHCENTRAL_DOMAIN_PORT"] = e["MESHCENTRAL_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["MESHCENTRAL_DOMAIN_FULL"] = "https://" + e["MESHCENTRAL_DOMAIN_PORT"]
	e["MESHCENTRAL_TITLE"] = defaultValue(e["MESHCENTRAL_TITLE"], e["SERVER_NAME"])
	e["MESHCENTRAL_SUBTITLE"] = defaultValue(e["MESHCENTRAL_SUBTITLE"], " ")
	switch e["MESHCENTRAL_DB_TYPE"] {
	case "postgres", "mariadb":
	default:
		return fmt.Errorf("MESHCENTRAL_DB_TYPE must be resolved to postgres or mariadb")
	}
	if e["MESHCENTRAL_USER_FILTER"] == "" {
		if e["SAMBA_DC_APP_FILTER"] == "true" {
			e["MESHCENTRAL_USER_FILTER"] = "(&" + e["SAMBA_DC_USER_CLASS_FILTER"] + "(" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + "=*)(|(memberOf:1.2.840.113556.1.4.1941:=CN=APP_meshcentral," + e["SAMBA_DC_BASE_APP_DN"] + ")(memberOf:1.2.840.113556.1.4.1941:=" + e["SAMBA_DC_APP_ALL_DN"] + ")(memberOf:1.2.840.113556.1.4.1941:=" + e["SAMBA_DC_ADMIN_GROUP_DN"] + ")))"
		} else {
			e["MESHCENTRAL_USER_FILTER"] = "(&" + e["SAMBA_DC_USER_CLASS_FILTER"] + "(" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + "=*))"
		}
	}
	if e["MESHCENTRAL_USER_LOGIN_FILTER"] == "" {
		parts := []string{}
		for _, attr := range splitCSV(e["SAMBA_DC_USER_LOGIN_ATTRS"]) {
			parts = append(parts, "("+attr+"={{username}})")
		}
		e["MESHCENTRAL_USER_LOGIN_FILTER"] = "(&" + e["MESHCENTRAL_USER_FILTER"] + e["SAMBA_DC_USER_ENABLED_FILTER"] + "(|" + strings.Join(parts, "") + "))"
	}
	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_meshcentral,APP_all," + defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins")
	}
	if err := publishMeshcentralOIDCRegistration(e, allowGroups, secrets); err != nil {
		return err
	}
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "meshcentral")
	e["APPS_LIST__MESHCENTRAL__NAME"] = defaultValue(e["APPS_LIST__MESHCENTRAL__NAME"], "MeshCentral")
	e["APPS_LIST__MESHCENTRAL__DESC"] = defaultValue(e["APPS_LIST__MESHCENTRAL__DESC"], "Remote device management")
	e["APPS_LIST__MESHCENTRAL__URI"] = e["MESHCENTRAL_DOMAIN_FULL"]
	e["APPS_LIST__MESHCENTRAL__ALLOW_GROUPS"] = allowGroups
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
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
	return strings.Join(append(items, item), ",")
}

func randomHexErr(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
