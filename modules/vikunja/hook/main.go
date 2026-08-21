package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/anas-project/ANAS/internal/localization"
	"golang.org/x/text/language"
)

var supportedHookABIs = []string{"anas.module-hook/v1"}

const (
	iamClientPrefix  = "ANAS_IAM_CLIENT__VIKUNJA__"
	iamBindingPrefix = "ANAS_IAM_BINDING__VIKUNJA__"
)

type hookRequest struct {
	ABI        string               `json:"abi"`
	Phase      string               `json:"phase"`
	Module     string               `json:"module"`
	Workdir    string               `json:"workdir"`
	Env        map[string]string    `json:"env"`
	Secrets    map[string]string    `json:"secrets"`
	Credential *credentialOperation `json:"credential,omitempty"`
}

type hookResponse struct {
	Env        map[string]string `json:"env,omitempty"`
	Secrets    map[string]string `json:"secrets,omitempty"`
	Warnings   []string          `json:"warnings,omitempty"`
	Credential *credentialResult `json:"credential,omitempty"`
}

type credentialOperation struct {
	Handler          string `json:"handler"`
	CredentialID     string `json:"credential_id"`
	SecretKey        string `json:"secret_key"`
	DesiredSecretKey string `json:"desired_secret_key"`
	Authority        string `json:"authority"`
	Generation       uint64 `json:"generation"`
}

type credentialResult struct {
	CredentialID string `json:"credential_id"`
	Status       string `json:"status"`
	Changed      bool   `json:"changed,omitempty"`
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
	if strings.HasPrefix(req.Phase, "credential_") {
		result, err := handleCredential(req)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Credential: &result}, nil
	}
	if req.Module != "vikunja" {
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
		return hookResponse{
			Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values), Warnings: warnings,
		}, nil
	case "render_env":
		if err := renderEnv(env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env)}, nil
	default:
		return hookResponse{}, nil
	}
}

var credentialDockerInspect = func(container string) ([]byte, error) {
	return exec.Command("docker", "inspect", "--format", "{{json .Config.Env}}", container).Output()
}

func handleCredential(req hookRequest) (credentialResult, error) {
	operation := req.Credential
	definitions := map[string]struct {
		secretKey string
		envKey    string
		handlers  map[string]string
	}{
		"vikunja.service_secret": {
			secretKey: "VIKUNJA_SERVICE_SECRET", envKey: "VIKUNJA_SERVICE_SECRET",
			handlers: map[string]string{
				"credential_probe": "probe-vikunja-service-secret", "credential_reconcile": "reconcile-vikunja-service-secret", "credential_verify": "verify-vikunja-service-secret",
			},
		},
		"vikunja.oidc_client_secret": {
			secretKey: "VIKUNJA_OIDC_CLIENT_SECRET", envKey: "VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTSECRET",
			handlers: map[string]string{
				"credential_probe": "probe-vikunja-oidc-client-secret", "credential_reconcile": "reconcile-vikunja-oidc-client-secret", "credential_verify": "verify-vikunja-oidc-client-secret",
			},
		},
	}
	definition, ok := definitions[operationID(operation)]
	if req.Module != "vikunja" || operation == nil || !ok || operation.SecretKey != definition.secretKey || operation.Handler != definition.handlers[req.Phase] {
		return credentialResult{}, fmt.Errorf("invalid Vikunja credential operation")
	}
	desired := req.Secrets[operation.DesiredSecretKey]
	if desired == "" {
		return credentialResult{}, fmt.Errorf("missing Vikunja desired credential")
	}
	container := req.Env["CONTAINER_PREFIX"] + "vikunja"
	if container == "vikunja" {
		return credentialResult{}, fmt.Errorf("missing Vikunja container prefix")
	}
	status := probeContainerEnv(container, definition.envKey, desired)
	if req.Phase == "credential_reconcile" {
		if operation.Authority != "anas" {
			return credentialResult{}, fmt.Errorf("Vikunja credential authority is external")
		}
		if status != "match" {
			return credentialResult{}, fmt.Errorf("Vikunja candidate container did not receive the desired credential")
		}
	}
	return credentialResult{CredentialID: operation.CredentialID, Status: status}, nil
}

func operationID(operation *credentialOperation) string {
	if operation == nil {
		return ""
	}
	return operation.CredentialID
}

func probeContainerEnv(container, key, desired string) string {
	body, err := credentialDockerInspect(container)
	if err != nil {
		return "unavailable"
	}
	var entries []string
	if err := json.Unmarshal(body, &entries); err != nil {
		return "unavailable"
	}
	prefix := key + "="
	for _, entry := range entries {
		if strings.HasPrefix(entry, prefix) {
			if strings.TrimPrefix(entry, prefix) == desired {
				return "match"
			}
			return "mismatch"
		}
	}
	return "missing"
}

func calculate(e map[string]string, secrets *secretStore) ([]string, error) {
	explicitLanguage := e["VIKUNJA_LANGUAGE"]
	selectedLanguage, confidence, err := localization.Match(
		defaultValue(explicitLanguage, e["DEFAULT_LANGUAGE"]), vikunjaLanguages, "en",
	)
	if err != nil {
		return nil, fmt.Errorf("Vikunja language: %w", err)
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
	e["VIKUNJA_LANGUAGE"] = selectedLanguage

	e["VIKUNJA_DOMAIN"] = e["VIKUNJA_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["VIKUNJA_DOMAIN_PORT"] = e["VIKUNJA_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["VIKUNJA_DOMAIN_FULL"] = "https://" + e["VIKUNJA_DOMAIN_PORT"]
	e["VIKUNJA_SERVICE_PUBLICURL"] = e["VIKUNJA_DOMAIN_FULL"] + "/"

	switch e["VIKUNJA_DB_TYPE"] {
	case "postgres", "mariadb":
	default:
		return nil, fmt.Errorf("VIKUNJA_DB_TYPE must be resolved to postgres or mariadb")
	}
	protocol := defaultValue(defaultValue(e[iamBindingPrefix+"INTERFACE"], e["VIKUNJA_IAM_PROTOCOL"]), "oidc")
	if protocol != "oidc" {
		return nil, fmt.Errorf("vikunja requires an oidc IAM binding, got %q", protocol)
	}
	e["VIKUNJA_IAM_PROTOCOL"] = protocol

	serviceSecret, err := secrets.Ensure("VIKUNJA_SERVICE_SECRET", func() (string, error) {
		return randomHex(32)
	})
	if err != nil {
		return nil, err
	}
	e["VIKUNJA_SERVICE_SECRET"] = serviceSecret
	oidcSecret, err := secrets.Ensure("VIKUNJA_OIDC_CLIENT_SECRET", func() (string, error) {
		return randomHex(32)
	})
	if err != nil {
		return nil, err
	}
	e["VIKUNJA_OIDC_CLIENT_ID"] = "vikunja"
	e["VIKUNJA_OIDC_CLIENT_SECRET"] = oidcSecret

	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_vikunja,APP_all," + defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins")
	}
	e[iamClientPrefix+"INTERFACE"] = "oidc"
	e[iamClientPrefix+"CLIENT_ID"] = e["VIKUNJA_OIDC_CLIENT_ID"]
	e[iamClientPrefix+"CLIENT_SECRET"] = oidcSecret
	e[iamClientPrefix+"REDIRECT_URIS"] = e["VIKUNJA_DOMAIN_FULL"] + "/auth/openid/anas"
	e[iamClientPrefix+"POST_LOGOUT_REDIRECT_URIS"] = e["VIKUNJA_SERVICE_PUBLICURL"]
	e[iamClientPrefix+"SCOPES"] = "openid,profile,email"
	e[iamClientPrefix+"ATTRIBUTES"] = "name:displayName:1,preferred_username:sAMAccountName:1,email:mail:1"
	e[iamClientPrefix+"ALLOW_GROUPS"] = allowGroups
	e[iamClientPrefix+"DOMAIN"] = e["VIKUNJA_DOMAIN"]

	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "vikunja")
	e["APPS_LIST__VIKUNJA__NAME"] = defaultValue(e["APPS_LIST__VIKUNJA__NAME"], "Vikunja")
	e["APPS_LIST__VIKUNJA__DESC"] = defaultValue(e["APPS_LIST__VIKUNJA__DESC"], "Tasks, projects, and kanban boards")
	e["APPS_LIST__VIKUNJA__URI"] = e["VIKUNJA_DOMAIN_FULL"] + "/login?redirectToProvider=anas"
	e["APPS_LIST__VIKUNJA__ALLOW_GROUPS"] = allowGroups
	return warnings, nil
}

func renderEnv(e map[string]string) error {
	if iface := e[iamBindingPrefix+"INTERFACE"]; iface != "oidc" {
		return fmt.Errorf("vikunja requires an oidc IAM binding, got %q", iface)
	}
	issuer := e[iamBindingPrefix+"OIDC_ISSUER_URL"]
	if issuer == "" {
		return fmt.Errorf("%sOIDC_ISSUER_URL is empty", iamBindingPrefix)
	}
	if e[iamBindingPrefix+"OIDC_DISCOVERY_URL"] == "" {
		return fmt.Errorf("%sOIDC_DISCOVERY_URL is empty", iamBindingPrefix)
	}
	if e["VIKUNJA_OIDC_CLIENT_ID"] == "" || e["VIKUNJA_OIDC_CLIENT_SECRET"] == "" {
		return fmt.Errorf("vikunja OIDC client credentials are empty")
	}
	if e["VIKUNJA_SERVICE_SECRET"] == "" {
		return fmt.Errorf("VIKUNJA_SERVICE_SECRET is empty")
	}

	switch e["VIKUNJA_DB_TYPE"] {
	case "postgres":
		e["VIKUNJA_DATABASE_TYPE"] = "postgres"
	case "mariadb":
		e["VIKUNJA_DATABASE_TYPE"] = "mysql"
	default:
		return fmt.Errorf("VIKUNJA_DB_TYPE must be resolved to postgres or mariadb")
	}
	for _, key := range []string{"VIKUNJA_DB_HOST", "VIKUNJA_DB_NAME", "VIKUNJA_DB_USERNAME", "VIKUNJA_DB_PASSWORD", "VIKUNJA_NETWORK_DB", "TZ", "VIKUNJA_LANGUAGE"} {
		if e[key] == "" {
			return fmt.Errorf("%s is empty", key)
		}
	}
	e["VIKUNJA_DATABASE_HOST"] = e["VIKUNJA_DB_HOST"]
	e["VIKUNJA_DATABASE_DATABASE"] = e["VIKUNJA_DB_NAME"]
	e["VIKUNJA_DATABASE_USER"] = e["VIKUNJA_DB_USERNAME"]
	e["VIKUNJA_DATABASE_PASSWORD"] = e["VIKUNJA_DB_PASSWORD"]
	e["VIKUNJA_DATABASE_SSLMODE"] = "disable"
	e["VIKUNJA_DATABASE_TLS"] = "false"
	e["VIKUNJA_FILES_BASEPATH"] = "/app/vikunja/files"
	e["VIKUNJA_SERVICE_TIMEZONE"] = e["TZ"]
	e["VIKUNJA_DEFAULTSETTINGS_TIMEZONE"] = e["TZ"]
	e["VIKUNJA_DEFAULTSETTINGS_LANGUAGE"] = e["VIKUNJA_LANGUAGE"]
	e["VIKUNJA_SERVICE_ENABLEREGISTRATION"] = "false"
	e["VIKUNJA_AUTH_LOCAL_ENABLED"] = "false"
	e["VIKUNJA_AUTH_OPENID_ENABLED"] = "true"
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_NAME"] = "ANAS"
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_AUTHURL"] = issuer
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTID"] = e["VIKUNJA_OIDC_CLIENT_ID"]
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_CLIENTSECRET"] = e["VIKUNJA_OIDC_CLIENT_SECRET"]
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_SCOPE"] = "openid profile email"
	e["VIKUNJA_AUTH_OPENID_PROVIDERS_ANAS_REQUIREAVAILABILITY"] = "true"
	return nil
}

var vikunjaLanguages = []localization.Target{
	{Language: "en", Value: "en"}, {Language: "de-DE", Value: "de-DE"},
	{Language: "de-CH", Value: "de-swiss"}, {Language: "ru-RU", Value: "ru-RU"},
	{Language: "fr-FR", Value: "fr-FR"}, {Language: "vi-VN", Value: "vi-VN"},
	{Language: "it-IT", Value: "it-IT"}, {Language: "cs-CZ", Value: "cs-CZ"},
	{Language: "pl-PL", Value: "pl-PL"}, {Language: "nl-NL", Value: "nl-NL"},
	{Language: "pt-PT", Value: "pt-PT"}, {Language: "zh-CN", Value: "zh-CN"},
	{Language: "zh-TW", Value: "zh-TW"}, {Language: "no-NO", Value: "no-NO"},
	{Language: "es-ES", Value: "es-ES"}, {Language: "da-DK", Value: "da-DK"},
	{Language: "ja-JP", Value: "ja-JP"}, {Language: "hu-HU", Value: "hu-HU"},
	{Language: "ar-SA", Value: "ar-SA"}, {Language: "fa-IR", Value: "fa-IR"},
	{Language: "sl-SI", Value: "sl-SI"}, {Language: "pt-BR", Value: "pt-BR"},
	{Language: "hr-HR", Value: "hr-HR"}, {Language: "uk-UA", Value: "uk-UA"},
	{Language: "lt-LT", Value: "lt-LT"}, {Language: "bg-BG", Value: "bg-BG"},
	{Language: "ko-KR", Value: "ko-KR"}, {Language: "tr-TR", Value: "tr-TR"},
	{Language: "fi-FI", Value: "fi-FI"}, {Language: "he-IL", Value: "he-IL"},
	{Language: "sv-SE", Value: "sv-SE"}, {Language: "el-GR", Value: "el-GR"},
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
