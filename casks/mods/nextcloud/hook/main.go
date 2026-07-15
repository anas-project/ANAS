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
	if module != "nextcloud" {
		return nil
	}
	return calcNextcloud(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "nextcloud" {
		return map[string]string{}, nil
	}
	return map[string]string{}, moduleNextcloud(env, workdir)
}
func disabledServices(module string, env map[string]string) []string {
	if module != "nextcloud" {
		return nil
	}
	if env["NEXTCLOUD_TALK_ENABLED"] != "true" {
		return []string{"talk", "anas_talk"}
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "nextcloud" {
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
func calcNextcloud(e map[string]string, workdir string, secrets *secretStore) error {
	e["NEXTCLOUD_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud"
	e["NEXTCLOUD_BASE_PATH"] = defaultValue(e["NEXTCLOUD_BASE_PATH"], filepath.Join(e["DATA_PATH"], "nextcloud"))
	e["NEXTCLOUD_DOMAIN"] = e["NEXTCLOUD_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["NEXTCLOUD_DOMAIN_PORT"] = e["NEXTCLOUD_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["NEXTCLOUD_DOMAIN_FULL"] = "https://" + e["NEXTCLOUD_DOMAIN_PORT"]
	e["NEXTCLOUD_TALK_SIGNALING_DOMAIN_FULL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/talk"
	e["NEXTCLOUD_REDIS_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud_redis"
	e["NEXTCLOUD_REDIS_PORT"] = "6379"
	// Preview Generator 5.12.x declares Nextcloud 30 compatibility but uses a
	// Symfony Console signature that is incompatible with the bundled runtime.
	e["NEXTCLOUD_PREVIEWGENERATOR_VERSION"] = defaultValue(e["NEXTCLOUD_PREVIEWGENERATOR_VERSION"], "5.7.0")
	if e["NEXTCLOUD_DB_TYPE"] == "" {
		if e["POSTGRES_HOST"] != "" {
			e["NEXTCLOUD_DB_TYPE"] = "postgres"
		} else if e["MARIADB_HOST"] != "" {
			e["NEXTCLOUD_DB_TYPE"] = "mariadb"
		}
	}
	if e["NEXTCLOUD_DB_TYPE"] == "mariadb" {
		e["NEXTCLOUD_NETWORK_DB"] = e["MARIADB_NETWORK_NAME"]
	} else {
		e["NEXTCLOUD_NETWORK_DB"] = e["POSTGRES_NETWORK_NAME"]
	}
	e["NEXTCLOUD_IMAGINARY_HOSTNAME"] = e["CONTAINER_PREFIX"] + "imaginary"
	e["NEXTCLOUD_ADMIN_USERNAME"] = defaultValue(e["NEXTCLOUD_ADMIN_USERNAME"], e["SAMBA_DC_ADMIN_NAME"]+"_nc")
	e["NEXTCLOUD_ADMIN_PASSWORD"] = defaultValue(e["NEXTCLOUD_ADMIN_PASSWORD"], e["SAMBA_DC_ADMIN_PASSWORD"])
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		e["NEXTCLOUD_USER_FILTER"] = defaultValue(e["NEXTCLOUD_USER_FILTER"], "(&"+e["SAMBA_DC_USER_CLASS_FILTER"]+"(|(memberOf=CN=APP_nextcloud,"+e["SAMBA_DC_BASE_APP_DN"]+")(memberOf="+e["SAMBA_DC_APP_ALL_DN"]+")(memberOf="+e["SAMBA_DC_ADMIN_GROUP_DN"]+")))")
	} else {
		e["NEXTCLOUD_USER_FILTER"] = defaultValue(e["NEXTCLOUD_USER_FILTER"], "(&"+e["SAMBA_DC_USER_CLASS_FILTER"]+")")
	}
	if e["NEXTCLOUD_USER_LOGIN_FILTER"] == "" {
		attrs := append(splitCSV(e["SAMBA_DC_USER_LOGIN_ATTRS"]), "objectGUID")
		parts := []string{}
		for _, attr := range attrs {
			parts = append(parts, "("+attr+"=%uid)")
		}
		e["NEXTCLOUD_USER_LOGIN_FILTER"] = "(&" + e["NEXTCLOUD_USER_FILTER"] + e["SAMBA_DC_USER_ENABLED_FILTER"] + "(|" + strings.Join(parts, "") + "))"
	}
	e["NEXTCLOUD_USER_COMPLEX_PASS"] = defaultValue(e["NEXTCLOUD_USER_COMPLEX_PASS"], defaultValue(e["SAMBA_DC_USER_COMPLEX_PASS"], "true"))
	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_nextcloud"
	}
	e["SMAL_SP_APPS"] = addCSV(e["SMAL_SP_APPS"], "nextcloud")
	e["SMAL_SP__NEXTCLOUD__METADATA_URL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/apps/user_saml/saml/metadata?idp=1"
	e["SMAL_SP__NEXTCLOUD__ATTR01"] = "cn,cn,1"
	e["SMAL_SP__NEXTCLOUD__ATTR02"] = "sAMAccountName,sAMAccountName,1"
	e["SMAL_SP__NEXTCLOUD__NAMEID_FORMAT"] = "windows"
	e["SMAL_SP__NEXTCLOUD__ALLOW_GROUPS"] = allowGroups
	e["SMAL_SP__NEXTCLOUD__DOMAIN"] = e["NEXTCLOUD_DOMAIN"]
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "nextcloud")
	e["APPS_LIST__NEXTCLOUD__NAME"] = defaultValue(e["APPS_LIST__NEXTCLOUD__NAME"], "Nextcloud")
	e["APPS_LIST__NEXTCLOUD__DESC"] = defaultValue(e["APPS_LIST__NEXTCLOUD__DESC"], "Self hosted file sharing and communication")
	e["APPS_LIST__NEXTCLOUD__LOGO_PATH"] = defaultValue(e["APPS_LIST__NEXTCLOUD__LOGO_PATH"], filepath.Join(workdir, "assets", "nextcloud.png"))
	e["APPS_LIST__NEXTCLOUD__URI"] = e["NEXTCLOUD_DOMAIN_FULL"]
	e["APPS_LIST__NEXTCLOUD__ALLOW_GROUPS"] = allowGroups
	if e["NEXTCLOUD_TALK_INTERNAL_SECRET"] == "" {
		v, err := secrets.Ensure("NEXTCLOUD_TALK_INTERNAL_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return err
		}
		e["NEXTCLOUD_TALK_INTERNAL_SECRET"] = v
	}
	if e["TALK_SIGNALING_SECRET"] == "" {
		v, err := secrets.Ensure("TALK_SIGNALING_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return err
		}
		e["TALK_SIGNALING_SECRET"] = v
	}
	return nil
}
func moduleNextcloud(e map[string]string, _ string) error {
	e["MEMORY_LIMIT"] = e["NEXTCLOUD_MEMORY_LIMIT"]
	e["UPLOAD_MAX_SIZE"] = e["NEXTCLOUD_UPLOAD_MAX_SIZE"]
	e["OPCACHE_MEM_SIZE"] = "128"
	e["APC_SHM_SIZE"] = "128M"
	e["REAL_IP_HEADER"] = "X-Forwarded-For"
	e["LOG_IP_VAR"] = "http_x_forwarded_for"
	e["HSTS_HEADER"] = "max-age=15768000; includeSubDomains"
	e["RP_HEADER"] = "strict-origin"
	e["SUBDIR"] = ""
	if e["NEXTCLOUD_DB_TYPE"] == "postgres" {
		// tiredofit/nextcloud expects DB_HOST and DB_PORT separately.
		e["DB_HOST"] = e["POSTGRES_HOST"]
		e["DB_PORT"] = e["POSTGRES_PORT"]
		e["DB_USER"] = e["POSTGRES_USERNAME"]
		e["DB_PASSWORD"] = e["POSTGRES_PASSWORD"]
		e["DB_TYPE"] = "pgsql"
	} else {
		e["DB_HOST"] = e["MARIADB_HOST"]
		e["DB_PORT"] = e["MARIADB_PORT"]
		e["DB_USER"] = e["MARIADB_USERNAME"]
		e["DB_PASSWORD"] = e["MARIADB_PASSWORD"]
		e["DB_TYPE"] = "mysql"
	}
	e["DB_NAME"] = e["NEXTCLOUD_DB_NAME"]
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
