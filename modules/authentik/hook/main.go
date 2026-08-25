package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	ABI          string                 `json:"abi"`
	Phase        string                 `json:"phase"`
	Module       string                 `json:"module"`
	Workdir      string                 `json:"workdir"`
	Env          map[string]string      `json:"env"`
	Secrets      map[string]string      `json:"secrets"`
	LocalAccount *localAccountOperation `json:"local_account,omitempty"`
}

type localAccountOperation struct {
	Handler            string `json:"handler"`
	AccountID          string `json:"account_id"`
	Username           string `json:"username"`
	SecretKey          string `json:"secret_key"`
	CandidateSecretKey string `json:"candidate_secret_key"`
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
	case "local_account_apply", "local_account_rotate", "local_account_rollback":
		if err := handleLocalAccount(req); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{}, nil
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
	case "after_start":
		if err := waitForBlueprintReadiness(env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{}, nil
	default:
		return hookResponse{}, nil
	}
}

const blueprintReadyAttempts = 120

var runBlueprintReadyProbe = func(container string) error {
	cmd := exec.Command("docker", "exec", container, "test", "-f", "/tmp/anas-blueprints.ready")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

var blueprintReadyRetryPause = func() { time.Sleep(5 * time.Second) }

func waitForBlueprintReadiness(env map[string]string) error {
	container := env["CONTAINER_PREFIX"] + "authentik_worker"
	for attempt := 0; attempt < blueprintReadyAttempts; attempt++ {
		if err := runBlueprintReadyProbe(container); err == nil {
			return nil
		}
		if attempt+1 < blueprintReadyAttempts {
			blueprintReadyRetryPause()
		}
	}
	return fmt.Errorf("authentik blueprints did not become ready in container %s", container)
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
	e["AUTHENTIK_BREAK_GLASS_URL"] = e["AUTHENTIK_DOMAIN_FULL"] + "/if/flow/default-authentication-flow/"
	switch e["AUTHENTIK_DB_TYPE"] {
	case "postgres":
		e["AUTHENTIK_POSTGRESQL__HOST"] = e["AUTHENTIK_DB_HOST"]
		e["AUTHENTIK_POSTGRESQL__PORT"] = e["AUTHENTIK_DB_PORT"]
		e["AUTHENTIK_POSTGRESQL__USER"] = e["AUTHENTIK_DB_USERNAME"]
		e["AUTHENTIK_POSTGRESQL__PASSWORD"] = e["AUTHENTIK_DB_PASSWORD"]
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
			"SAMBA_DC_USER_COMPLEX_PASS", "SAMBA_DC_USER_MIN_PASS_AGE",
			"SAMBA_DC_USER_MIN_PASS_LENGTH", "SAMBA_DC_USER_PASSWORD_HISTORY",
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
		if err := calcPasswordPolicy(e); err != nil {
			return fmt.Errorf("configure authentik Samba AD password policy: %w", err)
		}
	}
	calcDirectoryWatch(e)

	key, err := secrets.Ensure("AUTHENTIK_SECRET_KEY", func() (string, error) { return randomHexErr(32) })
	if err != nil {
		return err
	}
	e["AUTHENTIK_SECRET_KEY"] = key
	e["AUTHENTIK_BOOTSTRAP_EMAIL"] = e["EMAIL"]

	if err := ensureSigningKeypair(e, secrets); err != nil {
		return err
	}
	return publishIAMEndpoints(e)
}

func calcPasswordPolicy(e map[string]string) error {
	minLength, err := nonNegativeInt(e["SAMBA_DC_USER_MIN_PASS_LENGTH"])
	if err != nil {
		return fmt.Errorf("SAMBA_DC_USER_MIN_PASS_LENGTH: %w", err)
	}
	history, err := nonNegativeInt(e["SAMBA_DC_USER_PASSWORD_HISTORY"])
	if err != nil {
		return fmt.Errorf("SAMBA_DC_USER_PASSWORD_HISTORY: %w", err)
	}
	minAge, err := nonNegativeInt(e["SAMBA_DC_USER_MIN_PASS_AGE"])
	if err != nil {
		return fmt.Errorf("SAMBA_DC_USER_MIN_PASS_AGE: %w", err)
	}

	e["AUTHENTIK_PASSWORD_MIN_LENGTH"] = strconv.Itoa(minLength)
	e["AUTHENTIK_PASSWORD_POLICY_GUIDANCE"] = passwordPolicyGuidance(
		e["DEFAULT_LANGUAGE"], minLength, history, minAge, varTrue(e["SAMBA_DC_USER_COMPLEX_PASS"]),
	)
	e["AUTHENTIK_PASSWORD_POLICY_ERROR"] = passwordPolicyLengthError(
		e["DEFAULT_LANGUAGE"], minLength,
	)
	return nil
}

func nonNegativeInt(value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative integer, got %q", value)
	}
	return n, nil
}

func varTrue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func passwordPolicyGuidance(language string, minLength, history, minAge int, complex bool) string {
	if strings.HasPrefix(strings.ToLower(language), "zh") {
		parts := []string{fmt.Sprintf("新密码至少 %d 个字符", minLength)}
		if complex {
			parts = append(parts, "至少包含以下五类中的三类：大写字母、小写字母、数字、符号、其他 Unicode 字母，并且不能包含用户名或姓名")
		}
		if history > 0 {
			parts = append(parts, fmt.Sprintf("不能重复最近 %d 个密码", history))
		}
		if minAge > 0 {
			parts = append(parts, fmt.Sprintf("普通用户两次主动改密至少间隔 %d 天", minAge))
		}
		return strings.Join(parts, "；") + "。两次输入必须完全一致。"
	}

	parts := []string{fmt.Sprintf("Use at least %d characters", minLength)}
	if complex {
		parts = append(parts, "use at least three of uppercase letters, lowercase letters, digits, symbols, and other Unicode letters, and do not include the username or display name")
	}
	if history > 0 {
		parts = append(parts, fmt.Sprintf("do not reuse the last %d passwords", history))
	}
	if minAge > 0 {
		parts = append(parts, fmt.Sprintf("ordinary user-initiated changes must be at least %d days apart", minAge))
	}
	return strings.Join(parts, "; ") + ". Enter the same new password twice."
}

func passwordPolicyLengthError(language string, minLength int) string {
	if strings.HasPrefix(strings.ToLower(language), "zh") {
		return fmt.Sprintf("新密码太短，至少需要 %d 个字符。请同时满足页面列出的 Samba 域密码规则。", minLength)
	}
	return fmt.Sprintf("The new password must contain at least %d characters and satisfy the Samba domain rules shown on this page.", minLength)
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
