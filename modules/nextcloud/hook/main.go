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

	"github.com/anas-project/ANAS/internal/localization"
	"golang.org/x/text/language"
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
	Warnings        []string          `json:"warnings,omitempty"`
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
		warnings, err := calculate(req.Module, env, req.Workdir, secrets)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values), Warnings: warnings}, nil
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
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) ([]string, error) {
	if module != "nextcloud" {
		return nil, nil
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
func calcNextcloud(e map[string]string, workdir string, secrets *secretStore) ([]string, error) {
	explicitLanguage := e["NEXTCLOUD_LANGUAGE"]
	languageValue, confidence, err := localization.Match(defaultValue(explicitLanguage, e["DEFAULT_LANGUAGE"]), nextcloudLanguages, "en")
	if err != nil {
		return nil, fmt.Errorf("Nextcloud language: %w", err)
	}
	var warnings []string
	if confidence == language.No {
		source, requested := "inherited global language", e["DEFAULT_LANGUAGE"]
		if explicitLanguage != "" {
			source, requested = "configured language", explicitLanguage
		}
		warnings = append(warnings, fmt.Sprintf("%s %q is unsupported; continuing with fallback %q", source, requested, languageValue))
	}
	e["NEXTCLOUD_LANGUAGE"] = languageValue
	localeValue, err := localization.Underscore(defaultValue(e["NEXTCLOUD_LOCALE"], e["DEFAULT_LOCALE"]))
	if err != nil {
		return nil, fmt.Errorf("Nextcloud locale: %w", err)
	}
	e["NEXTCLOUD_LOCALE"] = localeValue
	e["NEXTCLOUD_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud"
	e["NEXTCLOUD_PUSH_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud_push"
	e["NEXTCLOUD_BASE_PATH"] = defaultValue(e["NEXTCLOUD_BASE_PATH"], filepath.Join(e["DATA_PATH"], "nextcloud"))
	e["NEXTCLOUD_DOMAIN"] = e["NEXTCLOUD_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["NEXTCLOUD_DOMAIN_PORT"] = e["NEXTCLOUD_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["NEXTCLOUD_DOMAIN_FULL"] = "https://" + e["NEXTCLOUD_DOMAIN_PORT"]
	e["NEXTCLOUD_TALK_SIGNALING_DOMAIN_FULL"] = e["NEXTCLOUD_DOMAIN_FULL"] + "/talk"
	e["NEXTCLOUD_REDIS_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud_redis"
	e["NEXTCLOUD_REDIS_PORT"] = "6379"
	switch e["NEXTCLOUD_DB_TYPE"] {
	case "mariadb", "postgres":
	default:
		return nil, fmt.Errorf("NEXTCLOUD_DB_TYPE must be resolved to postgres or mariadb")
	}
	e["NEXTCLOUD_IMAGINARY_HOSTNAME"] = e["CONTAINER_PREFIX"] + "nextcloud_imaginary"
	e["NEXTCLOUD_ADMIN_USERNAME"] = defaultValue(e["NEXTCLOUD_ADMIN_USERNAME"], e["SAMBA_DC_ADMIN_NAME"]+"_nc")
	e["NEXTCLOUD_ADMIN_PASSWORD"] = defaultValue(e["NEXTCLOUD_ADMIN_PASSWORD"], e["SAMBA_DC_ADMIN_PASSWORD"])
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		e["NEXTCLOUD_USER_FILTER"] = defaultValue(e["NEXTCLOUD_USER_FILTER"], "(&"+e["SAMBA_DC_USER_CLASS_FILTER"]+"("+e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]+"=*)(|(memberOf=CN=APP_nextcloud,"+e["SAMBA_DC_BASE_APP_DN"]+")(memberOf="+e["SAMBA_DC_APP_ALL_DN"]+")(memberOf="+e["SAMBA_DC_ADMIN_GROUP_DN"]+")))")
	} else {
		e["NEXTCLOUD_USER_FILTER"] = defaultValue(e["NEXTCLOUD_USER_FILTER"], "(&"+e["SAMBA_DC_USER_CLASS_FILTER"]+"("+e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]+"=*))")
	}
	if e["NEXTCLOUD_USER_LOGIN_FILTER"] == "" {
		attrs := splitCSV(e["SAMBA_DC_USER_LOGIN_ATTRS"])
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
	publishClientRegistration(e, allowGroups)
	if err := ensureSPKeypair(e, secrets); err != nil {
		return nil, err
	}
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "nextcloud")
	e["APPS_LIST__NEXTCLOUD__NAME"] = defaultValue(e["APPS_LIST__NEXTCLOUD__NAME"], "Nextcloud")
	e["APPS_LIST__NEXTCLOUD__DESC"] = defaultValue(e["APPS_LIST__NEXTCLOUD__DESC"], "Self hosted file sharing and communication")
	e["APPS_LIST__NEXTCLOUD__LOGO_PATH"] = defaultValue(e["APPS_LIST__NEXTCLOUD__LOGO_PATH"], filepath.Join(workdir, "assets", "nextcloud.png"))
	e["APPS_LIST__NEXTCLOUD__URI"] = e["NEXTCLOUD_DOMAIN_FULL"]
	e["APPS_LIST__NEXTCLOUD__ALLOW_GROUPS"] = allowGroups
	if e["NEXTCLOUD_TALK_INTERNAL_SECRET"] == "" {
		v, err := secrets.Ensure("NEXTCLOUD_TALK_INTERNAL_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return nil, err
		}
		e["NEXTCLOUD_TALK_INTERNAL_SECRET"] = v
	}
	if e["TALK_SIGNALING_SECRET"] == "" {
		v, err := secrets.Ensure("TALK_SIGNALING_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return nil, err
		}
		e["TALK_SIGNALING_SECRET"] = v
	}
	if e["NEXTCLOUD_IMAGINARY_SECRET"] == "" {
		v, err := secrets.Ensure("NEXTCLOUD_IMAGINARY_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return nil, err
		}
		e["NEXTCLOUD_IMAGINARY_SECRET"] = v
	}
	return warnings, nil
}

// Extracted from Nextcloud server v34.0.2 core/l10n. These are defaults only:
// Nextcloud still gives a signed-in user's choice and the browser language
// precedence over default_language.
var nextcloudLanguages = []localization.Target{
	{Language: "en", Value: "en"}, {Language: "ar", Value: "ar"}, {Language: "ast", Value: "ast"},
	{Language: "be", Value: "be"}, {Language: "bg", Value: "bg"}, {Language: "ca", Value: "ca"},
	{Language: "cs", Value: "cs"}, {Language: "da", Value: "da"}, {Language: "de", Value: "de"},
	{Language: "de-DE", Value: "de_DE"}, {Language: "el", Value: "el"}, {Language: "en-GB", Value: "en_GB"},
	{Language: "eo", Value: "eo"}, {Language: "es", Value: "es"}, {Language: "es-EC", Value: "es_EC"},
	{Language: "es-MX", Value: "es_MX"}, {Language: "et-EE", Value: "et_EE"}, {Language: "eu", Value: "eu"},
	{Language: "fa", Value: "fa"}, {Language: "fi", Value: "fi"}, {Language: "fr", Value: "fr"},
	{Language: "ga", Value: "ga"}, {Language: "gl", Value: "gl"}, {Language: "hr", Value: "hr"},
	{Language: "hu", Value: "hu"}, {Language: "id", Value: "id"}, {Language: "is", Value: "is"},
	{Language: "it", Value: "it"}, {Language: "ja", Value: "ja"}, {Language: "ka", Value: "ka"},
	{Language: "ko", Value: "ko"}, {Language: "lo", Value: "lo"}, {Language: "lt-LT", Value: "lt_LT"},
	{Language: "lv", Value: "lv"}, {Language: "mk", Value: "mk"}, {Language: "mn", Value: "mn"},
	{Language: "nb", Value: "nb"}, {Language: "nl", Value: "nl"}, {Language: "pl", Value: "pl"},
	{Language: "pt-BR", Value: "pt_BR"}, {Language: "pt-PT", Value: "pt_PT"}, {Language: "ro", Value: "ro"},
	{Language: "ru", Value: "ru"}, {Language: "sc", Value: "sc"}, {Language: "sk", Value: "sk"},
	{Language: "sl", Value: "sl"}, {Language: "sr", Value: "sr"}, {Language: "sv", Value: "sv"},
	{Language: "sw", Value: "sw"}, {Language: "th", Value: "th"}, {Language: "tr", Value: "tr"},
	{Language: "ug", Value: "ug"}, {Language: "uk", Value: "uk"}, {Language: "uz", Value: "uz"},
	{Language: "vi", Value: "vi"}, {Language: "zh-CN", Value: "zh_CN"}, {Language: "zh-HK", Value: "zh_HK"},
	{Language: "zh-TW", Value: "zh_TW"},
}

func moduleNextcloud(e map[string]string, _ string) error {
	if err := applyIAMBinding(e); err != nil {
		return err
	}
	e["NEXTCLOUD_SAML_IDP_CERT"] = unquotePEM(e["NEXTCLOUD_SAML_IDP_CERT"])
	e["NEXTCLOUD_ADMIN_USER"] = e["NEXTCLOUD_ADMIN_USERNAME"]
	e["NEXTCLOUD_DATA_DIR"] = "/var/www/html/data"
	e["NEXTCLOUD_TRUSTED_DOMAINS"] = e["NEXTCLOUD_DOMAIN_PORT"]
	e["OVERWRITEPROTOCOL"] = "https"
	e["OVERWRITEHOST"] = e["NEXTCLOUD_DOMAIN_PORT"]
	e["OVERWRITECLIURL"] = e["NEXTCLOUD_DOMAIN_FULL"]
	e["PHP_MEMORY_LIMIT"] = e["NEXTCLOUD_MEMORY_LIMIT"]
	e["PHP_UPLOAD_LIMIT"] = e["NEXTCLOUD_UPLOAD_MAX_SIZE"]
	e["APACHE_BODY_LIMIT"] = "0"
	e["NEXTCLOUD_UPDATE"] = "1"
	e["NEXTCLOUD_INIT_HTACCESS"] = "true"
	e["REDIS_HOST"] = e["NEXTCLOUD_REDIS_HOSTNAME"]
	if e["NEXTCLOUD_DB_TYPE"] == "postgres" {
		e["POSTGRES_DB"] = e["NEXTCLOUD_DB_NAME"]
		e["POSTGRES_HOST"] = e["NEXTCLOUD_DB_HOST"]
		e["POSTGRES_USER"] = e["NEXTCLOUD_DB_USERNAME"]
		e["POSTGRES_PASSWORD"] = e["NEXTCLOUD_DB_PASSWORD"]
	} else {
		e["MYSQL_HOST"] = e["NEXTCLOUD_DB_HOST"]
		e["MYSQL_DATABASE"] = e["NEXTCLOUD_DB_NAME"]
		e["MYSQL_USER"] = e["NEXTCLOUD_DB_USERNAME"]
		e["MYSQL_PASSWORD"] = e["NEXTCLOUD_DB_PASSWORD"]
	}
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
