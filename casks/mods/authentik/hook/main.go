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

// calcDirectoryWatch subscribes authentik to the directory event journal so a
// group change takes effect in seconds instead of waiting for the next
// scheduled sync. It stays a pure accelerator: the schedule is untouched, and
// with no journal published the watcher simply finds nothing to read.
//
// The debounce exists because authentik has no per-user refresh. The only
// entry point is a full source sync, so a bulk membership change has to
// collapse into one run rather than one run per member.
func calcDirectoryWatch(e map[string]string) {
	// Always renderable: compose has no conditional services, so the mount
	// source needs a value even when no directory provider published a
	// journal. The watcher treats an absent journal as nothing to do.
	e["AUTHENTIK_DIRWATCH_EVENTS_DIR"] = defaultValue(
		e["ANAS_DIRECTORY_EVENTS_DIR"],
		filepath.Join(e["DATA_PATH"], "authentik", "directory-events"),
	)
	e["AUTHENTIK_DIRWATCH_EVENT_FILE"] = "/var/lib/anas-directory-events/" +
		defaultValue(e["ANAS_DIRECTORY_EVENTS_FILE_NAME"], "events.jsonl")
	e["AUTHENTIK_DIRWATCH_CURSOR_FILE"] = "/data/anas-dirwatch/cursor.json"
	e["AUTHENTIK_DIRWATCH_HEALTH_FILE"] = "/data/anas-dirwatch/health.json"
	e["AUTHENTIK_DIRWATCH_SOURCE_SLUG"] = "samba-ad"
	// Where the image installs the authentik package. The watcher runs as a
	// plain script rather than through `ak`, so it has to put this on sys.path
	// itself before Django can be configured.
	e["AUTHENTIK_DIRWATCH_APP_ROOT"] = "/"
	e["AUTHENTIK_DIRWATCH_OPERATIONS"] = defaultValue(
		e["AUTHENTIK_DIRWATCH_OPERATIONS"], "Add,Modify,Delete")
	// Only what changes an authorization decision. displayName and mail are
	// published by the producer but do not justify a full sync on their own;
	// they ride along on the next scheduled run.
	e["AUTHENTIK_DIRWATCH_ATTRIBUTES"] = defaultValue(
		e["AUTHENTIK_DIRWATCH_ATTRIBUTES"],
		"member,memberOf,userAccountControl,sAMAccountName,"+
			e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"],
	)
	e["AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS"] = defaultValue(
		e["AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS"], "5")
	e["AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS"] = defaultValue(
		e["AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS"], "60")
	e["AUTHENTIK_DIRWATCH_POLL_SECONDS"] = defaultValue(
		e["AUTHENTIK_DIRWATCH_POLL_SECONDS"], "1")
}

func calcAuthentik(e map[string]string, secrets *secretStore) error {
	e["AUTHENTIK_DOMAIN"] = e["AUTHENTIK_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["AUTHENTIK_DOMAIN_PORT"] = e["AUTHENTIK_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["AUTHENTIK_DOMAIN_FULL"] = "https://" + e["AUTHENTIK_DOMAIN_PORT"]
	breakGlassPassword, err := secrets.Ensure("AUTHENTIK_BREAK_GLASS_PASSWORD", func() (string, error) {
		return randomHexErr(32)
	})
	if err != nil {
		return err
	}
	e["AUTHENTIK_BREAK_GLASS_PASSWORD"] = breakGlassPassword

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
	e["AUTHENTIK_LOG_LEVEL"] = defaultValue(e["AUTHENTIK_LOG_LEVEL"], "warn")
	if e["AUTHENTIK_LDAP_ENABLED"] == "true" {
		if err := requireKeys(e, []string{
			"SAMBA_DC_LDAPS_SERVER_URL_PORT", "SAMBA_DC_PASSWORD_BIND_DN",
			"SAMBA_DC_PASSWORD_BIND_PASSWORD", "SAMBA_DC_BASE_DN",
			"SAMBA_DC_BASE_USERS_DN_PREFIX", "SAMBA_DC_BASE_GROUPS_DN_PREFIX",
			"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE",
		}); err != nil {
			return fmt.Errorf("configure authentik Samba AD source: %w", err)
		}
		e["AUTHENTIK_LDAP_SERVER_URI"] = e["SAMBA_DC_LDAPS_SERVER_URL_PORT"]
		e["AUTHENTIK_LDAP_BIND_DN"] = e["SAMBA_DC_PASSWORD_BIND_DN"]
		e["AUTHENTIK_LDAP_BIND_PASSWORD"] = e["SAMBA_DC_PASSWORD_BIND_PASSWORD"]
		e["AUTHENTIK_LDAP_BASE_DN"] = e["SAMBA_DC_BASE_DN"]
		e["AUTHENTIK_LDAP_ADDITIONAL_USER_DN"] = e["SAMBA_DC_BASE_USERS_DN_PREFIX"]
		e["AUTHENTIK_LDAP_ADDITIONAL_GROUP_DN"] = e["SAMBA_DC_BASE_GROUPS_DN_PREFIX"]
		e["AUTHENTIK_LDAP_USER_OBJECT_FILTER"] = "(&(objectClass=user)(!(objectClass=computer))(" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + "=*))"
		e["AUTHENTIK_LDAP_GROUP_OBJECT_FILTER"] = e["SAMBA_DC_GROUP_CLASS_FILTER"]
		e["AUTHENTIK_LDAP_OBJECT_UNIQUENESS_FIELD"] = e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]
		e["AUTHENTIK_LDAP_GROUP_MEMBERSHIP_FIELD"] = "memberOf:1.2.840.113556.1.4.1941:"
		e["AUTHENTIK_LDAP_USER_MEMBERSHIP_ATTRIBUTE"] = "distinguishedName"
	}
	calcDirectoryWatch(e)

	key, err := secrets.Ensure("AUTHENTIK_SECRET_KEY", func() (string, error) { return randomHexErr(32) })
	if err != nil {
		return err
	}
	e["AUTHENTIK_SECRET_KEY"] = key
	e["AUTHENTIK_BOOTSTRAP_PASSWORD"] = breakGlassPassword
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
	files := map[string]string{}
	if blueprint != "" {
		files["blueprints/anas-clients.yaml"] = blueprint
	}
	if e["AUTHENTIK_LDAP_ENABLED"] == "true" {
		files["blueprints/anas-samba-ad.yaml"] = renderDirectoryBlueprint()
	}
	return files, nil
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

func requireKeys(e map[string]string, keys []string) error {
	missing := []string{}
	for _, key := range keys {
		if strings.TrimSpace(e[key]) == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
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
