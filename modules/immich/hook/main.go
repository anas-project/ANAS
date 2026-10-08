package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const clientPrefix = "ANAS_IAM_CLIENT__IMMICH__"
const bindingPrefix = "ANAS_IAM_BINDING__IMMICH__"

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}
type hookResponse struct {
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	DisableServices []string          `json:"disable_services,omitempty"`
}

func main() {
	var req hookRequest
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&req); err != nil {
		fail(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		fail(fmt.Errorf("expected one hook request"))
	}
	if req.ABI != "anas.module-hook/v1" {
		fail(fmt.Errorf("unsupported ABI %q", req.ABI))
	}
	resp, err := handle(req)
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func handle(req hookRequest) (hookResponse, error) {
	if req.Module != "immich" {
		return hookResponse{}, nil
	}
	e, secrets := clone(req.Env), clone(req.Secrets)
	switch req.Phase {
	case "validate":
		return hookResponse{}, validate(e)
	case "calculate":
		if err := calculate(e, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, e), Secrets: changed(req.Secrets, secrets)}, nil
	case "render_env":
		config, err := render(e)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, e), Files: map[string]string{"config.json": config}}, nil
	case "services":
		if e["IMMICH_MACHINE_LEARNING"] == "false" {
			return hookResponse{DisableServices: []string{"anas_immich_machine_learning"}}, nil
		}
	}
	return hookResponse{}, nil
}
func validate(e map[string]string) error {
	if db := e["IMMICH_DB_TYPE"]; db != "postgres" && db != "auto" {
		return fmt.Errorf("Immich requires PostgreSQL")
	}
	if protocol := e["IMMICH_IAM_PROTOCOL"]; protocol != "oidc" && protocol != "auto" {
		return fmt.Errorf("Immich requires OIDC")
	}
	if value := e["IMMICH_MACHINE_LEARNING"]; value != "true" && value != "false" {
		return fmt.Errorf("IMMICH_MACHINE_LEARNING must be true or false")
	}
	for key, maximum := range map[string]int{"IMMICH_JOB_CONCURRENCY": 16, "IMMICH_VIDEO_CONCURRENCY": 8} {
		value, err := strconv.Atoi(e[key])
		if err != nil || value < 1 || value > maximum {
			return fmt.Errorf("%s must be between 1 and %d", key, maximum)
		}
	}
	return nil
}
func calculate(e, secrets map[string]string) error {
	if err := validate(e); err != nil {
		return err
	}
	if e["IMMICH_DB_TYPE"] != "postgres" {
		return fmt.Errorf("IMMICH_DB_TYPE must resolve to postgres")
	}
	anchor := e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"]
	if anchor == "" {
		return fmt.Errorf("directory identity anchor attribute is empty")
	}
	if strings.ContainsAny(anchor, ",:") {
		return fmt.Errorf("invalid directory identity anchor attribute")
	}
	if secrets["IMMICH_OIDC_CLIENT_SECRET"] == "" {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		secrets["IMMICH_OIDC_CLIENT_SECRET"] = hex.EncodeToString(bytes)
	}
	e["IMMICH_OIDC_CLIENT_SECRET"] = secrets["IMMICH_OIDC_CLIENT_SECRET"]
	e["IMMICH_IAM_PROTOCOL"] = "oidc"
	e["IMMICH_DOMAIN"] = e["IMMICH_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["IMMICH_DOMAIN_FULL"] = "https://" + e["IMMICH_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	allowGroups := ""
	if e["SAMBA_DC_APP_FILTER"] == "true" {
		allowGroups = "APP_immich,APP_all," + defaultValue(e["SAMBA_DC_ADMIN_GROUP_NAME"], "Admins")
	}
	for key, value := range map[string]string{
		"INTERFACE": "oidc", "CLIENT_ID": "immich", "CLIENT_SECRET": e["IMMICH_OIDC_CLIENT_SECRET"],
		"REDIRECT_URIS":             e["IMMICH_DOMAIN_FULL"] + "/auth/login," + e["IMMICH_DOMAIN_FULL"] + "/user-settings,app.immich:///oauth-callback",
		"POST_LOGOUT_REDIRECT_URIS": e["IMMICH_DOMAIN_FULL"] + "/auth/login",
		"SCOPES":                    "openid,profile,email", "ATTRIBUTES": "sub:" + anchor + ":1,name:displayName:1,email:mail:1,anas_role:anasRole:1",
		"ALLOW_GROUPS": allowGroups, "DOMAIN": e["IMMICH_DOMAIN"],
		"OIDC_LOGOUT_URI": e["IMMICH_DOMAIN_FULL"] + "/api/oauth/backchannel-logout", "OIDC_LOGOUT_METHODS": "backchannel", "OIDC_LOGOUT_SESSION_REQUIRED": "false",
		"OIDC_CAEP_EVENTS": "session-revoked",
	} {
		e[clientPrefix+key] = value
	}
	e["APPS_LIST"] = addCSV(e["APPS_LIST"], "immich")
	e["APPS_LIST__IMMICH__NAME"] = "Immich"
	e["APPS_LIST__IMMICH__DESC"] = "Photo and video library"
	e["APPS_LIST__IMMICH__URI"] = e["IMMICH_DOMAIN_FULL"]
	e["APPS_LIST__IMMICH__ALLOW_GROUPS"] = allowGroups
	return nil
}
func render(e map[string]string) (string, error) {
	if err := validate(e); err != nil {
		return "", err
	}
	if e[bindingPrefix+"INTERFACE"] != "oidc" {
		return "", fmt.Errorf("Immich requires its own OIDC binding")
	}
	for _, key := range []string{bindingPrefix + "OIDC_ISSUER_URL", bindingPrefix + "OIDC_DISCOVERY_URL", "IMMICH_OIDC_CLIENT_SECRET", "IMMICH_DB_HOST", "IMMICH_DB_NAME", "IMMICH_DB_USERNAME", "IMMICH_DB_PASSWORD", "IMMICH_NETWORK_DB"} {
		if e[key] == "" {
			return "", fmt.Errorf("%s is empty", key)
		}
	}
	e["DB_HOSTNAME"], e["DB_DATABASE_NAME"], e["DB_USERNAME"], e["DB_PASSWORD"] = e["IMMICH_DB_HOST"], e["IMMICH_DB_NAME"], e["IMMICH_DB_USERNAME"], e["IMMICH_DB_PASSWORD"]
	e["DB_PORT"] = "5432"
	e["DB_VECTOR_EXTENSION"] = "pgvector"
	e["IMMICH_ALLOW_SETUP"] = "false"
	e["IMMICH_CONFIG_FILE"] = "/etc/immich/anas-config.json"
	jobs := map[string]any{}
	concurrency, _ := strconv.Atoi(e["IMMICH_JOB_CONCURRENCY"])
	video, _ := strconv.Atoi(e["IMMICH_VIDEO_CONCURRENCY"])
	for _, queue := range []string{"thumbnailGeneration", "metadataExtraction", "faceDetection", "smartSearch", "backgroundTask", "migration", "search", "sidecar", "library", "notifications", "ocr", "workflow", "editor", "integrityCheck"} {
		jobs[queue] = map[string]int{"concurrency": concurrency}
	}
	jobs["videoConversion"] = map[string]int{"concurrency": video}
	config := map[string]any{
		"passwordLogin":   map[string]any{"enabled": false},
		"oauth":           map[string]any{"enabled": true, "autoRegister": true, "autoLaunch": true, "buttonText": "ANAS", "clientId": "immich", "clientSecret": e["IMMICH_OIDC_CLIENT_SECRET"], "issuerUrl": e[bindingPrefix+"OIDC_ISSUER_URL"], "scope": "openid profile email", "roleClaim": "anas_role", "storageLabelClaim": "", "mobileOverrideEnabled": false},
		"backup":          map[string]any{"database": map[string]any{"enabled": false}},
		"machineLearning": map[string]any{"enabled": e["IMMICH_MACHINE_LEARNING"] == "true", "urls": []string{"http://anas_immich_machine_learning:3003"}},
		"job":             jobs,
	}
	bytes, err := json.MarshalIndent(config, "", "  ")
	return string(bytes) + "\n", err
}
func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func clone(in map[string]string) map[string]string {
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
func addCSV(value, item string) string {
	for _, v := range strings.Split(value, ",") {
		if strings.TrimSpace(v) == item {
			return value
		}
	}
	if value == "" {
		return item
	}
	return value + "," + item
}
