package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/anas-project/ANAS/internal/localization"
	"golang.org/x/text/language"
)

var supportedHookABIs = []string{"anas.module-hook/v1"}

const (
	iamClientPrefix  = "ANAS_IAM_CLIENT__FORGEJO__"
	iamBindingPrefix = "ANAS_IAM_BINDING__FORGEJO__"
)

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
	Env      map[string]string `json:"env,omitempty"`
	Secrets  map[string]string `json:"secrets,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

type secretStore struct {
	values map[string]string
}

func (s *secretStore) Ensure(key string, generate func() (string, error)) (string, error) {
	if value := s.values[key]; value != "" {
		return value, nil
	}
	value, err := generate()
	if err != nil {
		return "", err
	}
	s.values[key] = value
	return value, nil
}

func main() {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var req hookRequest
	if err := json.Unmarshal(body, &req); err != nil {
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

func supportedABI(value string) bool {
	for _, abi := range supportedHookABIs {
		if value == abi {
			return true
		}
	}
	return false
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func handle(req hookRequest) (hookResponse, error) {
	if req.Module != "forgejo" {
		return hookResponse{}, nil
	}
	env := cloneMap(req.Env)
	secrets := &secretStore{values: cloneMap(req.Secrets)}
	switch req.Phase {
	case "calculate":
		warnings, err := calculate(env, secrets)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values), Warnings: warnings}, nil
	case "render_env":
		if err := renderEnv(env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env)}, nil
	case "after_start":
		if err := reconcileOIDC(env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{}, reconcileActionsAccount(env)
	case "local_account_apply":
		return hookResponse{}, handleLocalAccount(req)
	default:
		return hookResponse{}, nil
	}
}

func calculate(e map[string]string, secrets *secretStore) ([]string, error) {
	explicitLanguage := e["FORGEJO_LANGUAGE"]
	selectedLanguage, confidence, err := localization.Match(
		defaultValue(explicitLanguage, e["DEFAULT_LANGUAGE"]), forgejoLanguages, "en-US",
	)
	if err != nil {
		return nil, fmt.Errorf("Forgejo language: %w", err)
	}
	var warnings []string
	if confidence == language.No {
		source, requested := "inherited global language", e["DEFAULT_LANGUAGE"]
		if explicitLanguage != "" {
			source, requested = "configured language", explicitLanguage
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q is unsupported; continuing with fallback %q", source, requested, selectedLanguage,
		))
	}
	e["FORGEJO_LANGUAGE"] = selectedLanguage

	if e["FORGEJO_DOMAIN_PREFIX"] == "" || e["BASE_DOMAIN"] == "" || e["TRAEFIK_BASE_PORT"] == "" {
		return nil, fmt.Errorf("Forgejo domain inputs are incomplete")
	}
	e["FORGEJO_DOMAIN"] = e["FORGEJO_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["FORGEJO_DOMAIN_PORT"] = e["FORGEJO_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["FORGEJO_DOMAIN_FULL"] = "https://" + e["FORGEJO_DOMAIN_PORT"]
	e["FORGEJO_LOCAL_RECOVERY_URL"] = e["FORGEJO_DOMAIN_FULL"] + "/user/login"
	e["FORGEJO_SSH_CLONE_BASE"] = "ssh://git@" + e["FORGEJO_DOMAIN"] + ":" + e["FORGEJO_SSH_PORT"] + "/"

	switch e["FORGEJO_DB_TYPE"] {
	case "postgres", "mariadb":
	default:
		return nil, fmt.Errorf("FORGEJO_DB_TYPE must be resolved to postgres or mariadb")
	}
	protocol := defaultValue(defaultValue(e[iamBindingPrefix+"INTERFACE"], e["FORGEJO_IAM_PROTOCOL"]), "oidc")
	if protocol != "oidc" {
		return nil, fmt.Errorf("forgejo requires an oidc IAM binding, got %q", protocol)
	}
	e["FORGEJO_IAM_PROTOCOL"] = protocol

	oidcSecret, err := secrets.Ensure("FORGEJO_OIDC_CLIENT_SECRET", func() (string, error) { return randomHex(32) })
	if err != nil {
		return nil, err
	}
	appSecret, err := secrets.Ensure("FORGEJO_SECRET_KEY", func() (string, error) { return randomHex(32) })
	if err != nil {
		return nil, err
	}
	e["FORGEJO_OIDC_CLIENT_ID"] = "forgejo"
	e["FORGEJO_OIDC_CLIENT_SECRET"] = oidcSecret
	e["FORGEJO_SECRET_KEY"] = appSecret
	actionsPassword, err := secrets.Ensure("FORGEJO_ACTIONS_CONTROLLER_PASSWORD", func() (string, error) { return randomHex(32) })
	if err != nil {
		return nil, err
	}
	e["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"] = actionsPassword

	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_forgejo,APP_all," + defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins")
	}
	e[iamClientPrefix+"INTERFACE"] = "oidc"
	e[iamClientPrefix+"CLIENT_ID"] = e["FORGEJO_OIDC_CLIENT_ID"]
	e[iamClientPrefix+"CLIENT_SECRET"] = oidcSecret
	e[iamClientPrefix+"REDIRECT_URIS"] = e["FORGEJO_DOMAIN_FULL"] + "/user/oauth2/anas/callback"
	e[iamClientPrefix+"SCOPES"] = "openid,profile,email,groups"
	e[iamClientPrefix+"ATTRIBUTES"] = "name:displayName:1,preferred_username:sAMAccountName:1,email:mail:1,groups:groups:0"
	e[iamClientPrefix+"ALLOW_GROUPS"] = allowGroups
	e[iamClientPrefix+"DOMAIN"] = e["FORGEJO_DOMAIN"]

	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "forgejo")
	e["APPS_LIST__FORGEJO__NAME"] = defaultValue(e["APPS_LIST__FORGEJO__NAME"], "Forgejo")
	e["APPS_LIST__FORGEJO__DESC"] = defaultValue(e["APPS_LIST__FORGEJO__DESC"], "Git repositories, code review, packages, and collaboration")
	e["APPS_LIST__FORGEJO__URI"] = e["FORGEJO_DOMAIN_FULL"] + "/user/oauth2/anas"
	e["APPS_LIST__FORGEJO__ALLOW_GROUPS"] = allowGroups
	return warnings, nil
}

func renderEnv(e map[string]string) error {
	if iface := e[iamBindingPrefix+"INTERFACE"]; iface != "oidc" {
		return fmt.Errorf("forgejo requires an oidc IAM binding, got %q", iface)
	}
	if e[iamBindingPrefix+"OIDC_ISSUER_URL"] == "" || e[iamBindingPrefix+"OIDC_DISCOVERY_URL"] == "" {
		return fmt.Errorf("forgejo OIDC issuer or discovery URL is empty")
	}
	if e["FORGEJO_OIDC_CLIENT_ID"] == "" || e["FORGEJO_OIDC_CLIENT_SECRET"] == "" || e["FORGEJO_SECRET_KEY"] == "" {
		return fmt.Errorf("forgejo managed secrets are empty")
	}
	for _, key := range []string{
		"FORGEJO_DB_HOST", "FORGEJO_DB_PORT", "FORGEJO_DB_NAME", "FORGEJO_DB_USERNAME",
		"FORGEJO_DB_PASSWORD", "FORGEJO_NETWORK_DB", "FORGEJO_DOMAIN", "FORGEJO_DOMAIN_FULL",
		"FORGEJO_SSH_PORT", "FORGEJO_LANGUAGE", "FORGEJO_CUSTOM_GIT_HOOKS_ENABLED",
		"FORGEJO_LOCAL_PATH_IMPORT_ENABLED", "FORGEJO_ACTIONS_ENABLED", "TZ",
	} {
		if e[key] == "" {
			return fmt.Errorf("%s is empty", key)
		}
	}
	for _, key := range []string{
		"FORGEJO_CUSTOM_GIT_HOOKS_ENABLED", "FORGEJO_LOCAL_PATH_IMPORT_ENABLED", "FORGEJO_ACTIONS_ENABLED",
	} {
		if e[key] != "true" && e[key] != "false" {
			return fmt.Errorf("%s must be true or false", key)
		}
	}
	if err := validateActionsConfig(e); err != nil {
		return err
	}
	switch e["FORGEJO_DB_TYPE"] {
	case "postgres":
		e["FORGEJO_DATABASE_TYPE"] = "postgres"
	case "mariadb":
		e["FORGEJO_DATABASE_TYPE"] = "mysql"
	default:
		return fmt.Errorf("FORGEJO_DB_TYPE must be resolved to postgres or mariadb")
	}

	e["FORGEJO__SERVER__PROTOCOL"] = "http"
	e["FORGEJO__SERVER__DOMAIN"] = e["FORGEJO_DOMAIN"]
	e["FORGEJO__SERVER__ROOT_URL"] = e["FORGEJO_DOMAIN_FULL"] + "/"
	e["FORGEJO__SERVER__HTTP_PORT"] = "3000"
	e["FORGEJO__SERVER__START_SSH_SERVER"] = "true"
	e["FORGEJO__SERVER__SSH_DOMAIN"] = e["FORGEJO_DOMAIN"]
	e["FORGEJO__SERVER__SSH_PORT"] = e["FORGEJO_SSH_PORT"]
	e["FORGEJO__SERVER__SSH_LISTEN_PORT"] = "2222"
	e["FORGEJO__SERVER__LFS_START_SERVER"] = "true"
	e["FORGEJO__DATABASE__DB_TYPE"] = e["FORGEJO_DATABASE_TYPE"]
	e["FORGEJO__DATABASE__HOST"] = e["FORGEJO_DB_HOST"] + ":" + e["FORGEJO_DB_PORT"]
	e["FORGEJO__DATABASE__NAME"] = e["FORGEJO_DB_NAME"]
	e["FORGEJO__DATABASE__USER"] = e["FORGEJO_DB_USERNAME"]
	e["FORGEJO__DATABASE__PASSWD"] = e["FORGEJO_DB_PASSWORD"]
	e["FORGEJO__DATABASE__SSL_MODE"] = "disable"
	e["FORGEJO__SECURITY__INSTALL_LOCK"] = "true"
	e["FORGEJO__SECURITY__SECRET_KEY"] = e["FORGEJO_SECRET_KEY"]
	// Forgejo v15 container images otherwise trust X-Forwarded-* from every
	// source. The Web port is private to Compose; accept only loopback and RFC
	// 1918 container networks, never the upstream wildcard default.
	e["FORGEJO__SECURITY__REVERSE_PROXY_TRUSTED_PROXIES"] = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
	e["FORGEJO__SECURITY__DISABLE_GIT_HOOKS"] = invertBool(e["FORGEJO_CUSTOM_GIT_HOOKS_ENABLED"])
	e["FORGEJO__SECURITY__IMPORT_LOCAL_PATHS"] = e["FORGEJO_LOCAL_PATH_IMPORT_ENABLED"]
	e["FORGEJO__SECURITY__PASSWORD_HASH_ALGO"] = "bcrypt"
	e["FORGEJO__SERVICE__DISABLE_REGISTRATION"] = "true"
	e["FORGEJO__SERVICE__ALLOW_ONLY_EXTERNAL_REGISTRATION"] = "true"
	e["FORGEJO__SERVICE__ENABLE_BASIC_AUTHENTICATION"] = "true"
	e["FORGEJO__SERVICE__ENABLE_INTERNAL_SIGNIN"] = "true"
	e["FORGEJO__SERVICE__REGISTER_EMAIL_CONFIRM"] = "false"
	e["FORGEJO__SERVICE__DEFAULT_ALLOW_CREATE_ORGANIZATION"] = "false"
	e["FORGEJO__SERVICE__DEFAULT_USER_VISIBILITY"] = "limited"
	e["FORGEJO__SERVICE__EXTERNAL_USER_DISABLE_FEATURES"] = "deletion,manage_password"
	e["FORGEJO__OAUTH2_CLIENT__ENABLE_AUTO_REGISTRATION"] = "true"
	e["FORGEJO__OAUTH2_CLIENT__REGISTER_EMAIL_CONFIRM"] = "false"
	e["FORGEJO__OAUTH2_CLIENT__OPENID_CONNECT_SCOPES"] = "profile email groups"
	e["FORGEJO__OAUTH2_CLIENT__USERNAME"] = "nickname"
	e["FORGEJO__OAUTH2_CLIENT__ACCOUNT_LINKING"] = "disabled"
	e["FORGEJO__OAUTH2_CLIENT__UPDATE_AVATAR"] = "false"
	e["FORGEJO__SESSION__PROVIDER"] = "db"
	// Actions is one product feature: this same value is also passed to the
	// controller. There is deliberately no Runner-specific enable switch.
	e["FORGEJO__ACTIONS__ENABLED"] = e["FORGEJO_ACTIONS_ENABLED"]
	e["FORGEJO__MAILER__ENABLED"] = "false"
	e["FORGEJO__TIME__DEFAULT_UI_LOCATION"] = e["TZ"]
	e["FORGEJO__LOG__MODE"] = "console"
	e["FORGEJO__I18N__LANGS"], e["FORGEJO__I18N__NAMES"] = forgejoLocaleLists(e["FORGEJO_LANGUAGE"])
	return nil
}

func validateActionsConfig(e map[string]string) error {
	if e["FORGEJO_ACTIONS_ENABLED"] != "true" {
		return nil
	}
	// The Incus endpoint and certificates are no longer Forgejo settings: the
	// compute contract provisions the sandbox and the runner projects the lease
	// into the controller. What is left here is what Forgejo itself owns.
	for _, key := range []string{
		"FORGEJO_ACTIONS_ALLOWED_SCOPES", "FORGEJO_ACTIONS_CONTROLLER_PASSWORD",
		"FORGEJO_ACTIONS_RUNNER_IMAGE",
	} {
		if strings.TrimSpace(e[key]) == "" {
			return fmt.Errorf("%s is required when Forgejo Actions is enabled", key)
		}
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(e["FORGEJO_ACTIONS_RUNNER_IMAGE"]) {
		return fmt.Errorf("FORGEJO_ACTIONS_RUNNER_IMAGE must be a pinned SHA-256 fingerprint")
	}
	for _, scope := range splitCSV(e["FORGEJO_ACTIONS_ALLOWED_SCOPES"]) {
		parts := strings.Split(scope, "/")
		if len(parts) < 1 || len(parts) > 2 {
			return fmt.Errorf("Forgejo Actions scope %q must be {owner} or {owner}/{repo}", scope)
		}
		for _, part := range parts {
			if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`).MatchString(part) {
				return fmt.Errorf("Forgejo Actions scope %q is invalid", scope)
			}
		}
	}
	return nil
}

func invertBool(value string) string {
	if value == "true" {
		return "false"
	}
	return "true"
}

type oidcInput struct {
	Name         string `json:"name"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DiscoveryURL string `json:"discovery_url"`
	AdminGroup   string `json:"admin_group"`
}

type localAdminInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

var runContainerHelper = func(payload []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(payload)
	return cmd.CombinedOutput()
}

func reconcileOIDC(e map[string]string) error {
	for _, key := range []string{"CONTAINER_PREFIX", "FORGEJO_OIDC_CLIENT_ID", "FORGEJO_OIDC_CLIENT_SECRET", iamBindingPrefix + "OIDC_DISCOVERY_URL"} {
		if e[key] == "" {
			return fmt.Errorf("forgejo OIDC reconciliation is missing %s", key)
		}
	}
	payload, err := json.Marshal(oidcInput{
		Name: "anas", ClientID: e["FORGEJO_OIDC_CLIENT_ID"], ClientSecret: e["FORGEJO_OIDC_CLIENT_SECRET"],
		DiscoveryURL: e[iamBindingPrefix+"OIDC_DISCOVERY_URL"], AdminGroup: defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins"),
	})
	if err != nil {
		return err
	}
	container := e["CONTAINER_PREFIX"] + "forgejo"
	if _, err := runContainerHelper(payload, "docker", "exec", "-i", "--user", "1000:1000", container, "/usr/local/bin/anas-forgejo-entrypoint", "oidc"); err != nil {
		return fmt.Errorf("forgejo OIDC reconciliation failed: %w", err)
	}
	return nil
}

func reconcileActionsAccount(e map[string]string) error {
	if e["FORGEJO_ACTIONS_ENABLED"] != "true" {
		return nil
	}
	for _, key := range []string{"CONTAINER_PREFIX", "FORGEJO_ACTIONS_CONTROLLER_PASSWORD"} {
		if e[key] == "" {
			return fmt.Errorf("forgejo Actions account reconciliation is missing %s", key)
		}
	}
	payload, err := json.Marshal(localAdminInput{
		Username: "anas_actions_controller", Email: "anas_actions_controller@localhost.invalid",
		Password: e["FORGEJO_ACTIONS_CONTROLLER_PASSWORD"],
	})
	if err != nil {
		return err
	}
	container := e["CONTAINER_PREFIX"] + "forgejo"
	if _, err := runContainerHelper(payload, "docker", "exec", "-i", "--user", "1000:1000", container, "/usr/local/bin/anas-forgejo-entrypoint", "local-admin"); err != nil {
		return fmt.Errorf("forgejo Actions controller account reconciliation failed: %w", err)
	}
	return nil
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if op == nil || op.AccountID != "break_glass" || op.Handler != "apply-forgejo-break-glass" {
		return fmt.Errorf("forgejo: unsupported local account handler")
	}
	password := req.Secrets[op.CandidateSecretKey]
	if password == "" || op.Username == "" {
		return fmt.Errorf("forgejo: local recovery username or secret is missing")
	}
	if req.Env["CONTAINER_PREFIX"] == "" {
		return fmt.Errorf("forgejo: container prefix is missing")
	}
	payload, err := json.Marshal(localAdminInput{
		Username: op.Username, Email: op.Username + "@localhost.invalid", Password: password,
	})
	if err != nil {
		return err
	}
	container := req.Env["CONTAINER_PREFIX"] + "forgejo"
	if _, err := runContainerHelper(payload, "docker", "exec", "-i", "--user", "1000:1000", container, "/usr/local/bin/anas-forgejo-entrypoint", "local-admin"); err != nil {
		return fmt.Errorf("forgejo local recovery reconciliation failed: %w", err)
	}
	return nil
}

type forgejoLocale struct {
	language string
	code     string
	name     string
}

var forgejoLocales = []forgejoLocale{
	{"en", "en-US", "English"}, {"zh-CN", "zh-CN", "简体中文"}, {"zh-HK", "zh-HK", "繁體中文（香港）"},
	{"zh-TW", "zh-TW", "繁體中文（台灣）"}, {"da", "da", "Dansk"}, {"de-DE", "de-DE", "Deutsch"},
	{"nds", "nds", "Plattdüütsch"}, {"fr-FR", "fr-FR", "Français"}, {"nl-NL", "nl-NL", "Nederlands"},
	{"lv-LV", "lv-LV", "Latviešu"}, {"ru-RU", "ru-RU", "Русский"}, {"uk-UA", "uk-UA", "Українська"},
	{"ja-JP", "ja-JP", "日本語"}, {"es-ES", "es-ES", "Español"}, {"pt-BR", "pt-BR", "Português do Brasil"},
	{"pt-PT", "pt-PT", "Português de Portugal"}, {"pl-PL", "pl-PL", "Polski"}, {"bg", "bg", "Български"},
	{"it-IT", "it-IT", "Italiano"}, {"fi-FI", "fi-FI", "Suomi"}, {"fil", "fil", "Filipino"},
	{"eo", "eo", "Esperanto"}, {"tr-TR", "tr-TR", "Türkçe"}, {"cs-CZ", "cs-CZ", "Čeština"},
	{"sl", "sl", "Slovenščina"}, {"sv-SE", "sv-SE", "Svenska"}, {"ko-KR", "ko-KR", "한국어"},
	{"el-GR", "el-GR", "Ελληνικά"}, {"fa-IR", "fa-IR", "فارسی"}, {"hu-HU", "hu-HU", "Magyar nyelv"},
	{"id-ID", "id-ID", "Bahasa Indonesia"},
}

var forgejoLanguages = func() []localization.Target {
	targets := make([]localization.Target, 0, len(forgejoLocales))
	for _, locale := range forgejoLocales {
		targets = append(targets, localization.Target{Language: locale.language, Value: locale.code})
	}
	return targets
}()

func forgejoLocaleLists(selected string) (string, string) {
	locales := append([]forgejoLocale{}, forgejoLocales...)
	for i, locale := range locales {
		if locale.code == selected {
			locales = append([]forgejoLocale{locale}, append(locales[:i], locales[i+1:]...)...)
			break
		}
	}
	codes, names := make([]string, 0, len(locales)), make([]string, 0, len(locales))
	for _, locale := range locales {
		codes, names = append(codes, locale.code), append(names, locale.name)
	}
	return strings.Join(codes, ","), strings.Join(names, ",")
}

func randomHex(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func splitCSV(value string) []string {
	var values []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			values = append(values, item)
		}
	}
	return values
}

func addCSV(value, item string) string {
	items := splitCSV(value)
	for _, existing := range items {
		if existing == item {
			return strings.Join(items, ",")
		}
	}
	return strings.Join(append(items, item), ",")
}

func cloneMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func changed(old, current map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range current {
		if old[key] != value {
			out[key] = value
		}
	}
	return out
}
