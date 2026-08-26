package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var supportedHookABIs = []string{"anas.module-hook/v1"}

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
	Env     map[string]string `json:"env,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
	Files   map[string]string `json:"files,omitempty"`
}

type secretStore struct{ values map[string]string }

func (s *secretStore) Ensure(key string, gen func() (string, error)) (string, error) {
	if value := s.values[key]; value != "" {
		return value, nil
	}
	value, err := gen()
	if err != nil {
		return "", err
	}
	s.values[key] = value
	return value, nil
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

func supportedABI(value string) bool {
	for _, supported := range supportedHookABIs {
		if value == supported {
			return true
		}
	}
	return false
}

func handle(req hookRequest) (hookResponse, error) {
	if req.Module != "casdoor" {
		return hookResponse{}, nil
	}
	switch req.Phase {
	case "local_account_apply", "local_account_rotate", "local_account_rollback":
		return hookResponse{}, handleLocalAccount(req)
	case "calculate":
		env := cloneMap(req.Env)
		secrets := &secretStore{values: cloneMap(req.Secrets)}
		if err := calcCasdoor(env, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env":
		files, err := renderCasdoor(req.Env)
		return hookResponse{Files: files}, err
	default:
		return hookResponse{}, nil
	}
}

func calcCasdoor(e map[string]string, secrets *secretStore) error {
	e["CASDOOR_DOMAIN"] = e["CASDOOR_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["CASDOOR_DOMAIN_PORT"] = e["CASDOOR_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["CASDOOR_DOMAIN_FULL"] = "https://" + e["CASDOOR_DOMAIN_PORT"]
	e["CASDOOR_LOCAL_RECOVERY_URL"] = e["CASDOOR_DOMAIN_FULL"] + "/login"

	if e["CASDOOR_DB_TYPE"] != "postgres" {
		return fmt.Errorf("CASDOOR_DB_TYPE must be resolved to postgres")
	}
	if err := requireKeys(e, []string{
		"CASDOOR_DB_HOST", "CASDOOR_DB_PORT", "CASDOOR_DB_USERNAME", "CASDOOR_DB_PASSWORD", "CASDOOR_DB_NAME",
		"SAMBA_DC_LDAPS_SERVER_URL", "SAMBA_DC_LDAPS_PORT", "SAMBA_DC_LDAP_BIND_DN",
		"SAMBA_DC_LDAP_BIND_PASSWORD", "SAMBA_DC_BASE_USERS_DN", "SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE",
	}); err != nil {
		return err
	}

	e["CASDOOR_LDAP_HOST"] = strings.TrimPrefix(strings.TrimSuffix(e["SAMBA_DC_LDAPS_SERVER_URL"], "/"), "ldaps://")
	e["CASDOOR_LDAP_PORT"] = e["SAMBA_DC_LDAPS_PORT"]
	e["CASDOOR_LDAP_BIND_DN"] = e["SAMBA_DC_LDAP_BIND_DN"]
	e["CASDOOR_LDAP_BIND_PASSWORD"] = e["SAMBA_DC_LDAP_BIND_PASSWORD"]
	e["CASDOOR_LDAP_BASE_DN"] = e["SAMBA_DC_BASE_USERS_DN"]
	e["CASDOOR_LDAP_FILTER"] = "(&" + e["SAMBA_DC_USER_CLASS_FILTER"] + e["SAMBA_DC_USER_ENABLED_FILTER"] + "(" + e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"] + "=*))"

	if _, err := strconv.Atoi(e["CASDOOR_LDAP_AUTO_SYNC_MINUTES"]); err != nil {
		return fmt.Errorf("CASDOOR_LDAP_AUTO_SYNC_MINUTES must be an integer: %w", err)
	}
	if strings.HasPrefix(strings.ToLower(e["DEFAULT_LANGUAGE"]), "zh") {
		e["CASDOOR_DEFAULT_LANGUAGE"] = "zh"
	} else {
		e["CASDOOR_DEFAULT_LANGUAGE"] = "en"
	}

	portalID, err := secrets.Ensure("CASDOOR_PORTAL_CLIENT_ID", func() (string, error) { return randomHex(16) })
	if err != nil {
		return err
	}
	portalSecret, err := secrets.Ensure("CASDOOR_PORTAL_CLIENT_SECRET", func() (string, error) { return randomHex(32) })
	if err != nil {
		return err
	}
	e["CASDOOR_PORTAL_CLIENT_ID"], e["CASDOOR_PORTAL_CLIENT_SECRET"] = portalID, portalSecret
	calcDirectoryWatch(e)
	if err := ensureSigningKeypair(e, secrets); err != nil {
		return err
	}
	return publishIAMEndpoints(e)
}

// calcDirectoryWatch subscribes Casdoor to Samba's normalized event journal.
// It accelerates convergence after a directory write; Casdoor's configured
// periodic LDAP auto-sync remains enabled as the authoritative fallback.
func calcDirectoryWatch(e map[string]string) {
	e["CASDOOR_DIRWATCH_EVENTS_DIR"] = defaultValue(
		e["ANAS_DIRECTORY_EVENTS_DIR"],
		filepath.Join(e["DATA_PATH"], "casdoor", "directory-events"),
	)
	e["CASDOOR_DIRWATCH_EVENT_FILE"] = "/var/lib/anas-directory-events/" +
		defaultValue(e["ANAS_DIRECTORY_EVENTS_FILE_NAME"], "events.jsonl")
	e["CASDOOR_DIRWATCH_CURSOR_FILE"] = "/data/anas-dirwatch/cursor.json"
	e["CASDOOR_DIRWATCH_HEALTH_FILE"] = "/data/anas-dirwatch/health.json"
	e["CASDOOR_DIRWATCH_ENDPOINT"] = "http://anas_casdoor:8000"
	e["CASDOOR_DIRWATCH_LDAP_ID"] = "anas/anas-samba-ad"
	e["CASDOOR_DIRWATCH_CLIENT_ID"] = e["CASDOOR_PORTAL_CLIENT_ID"]
	e["CASDOOR_DIRWATCH_CLIENT_SECRET"] = e["CASDOOR_PORTAL_CLIENT_SECRET"]
	e["CASDOOR_DIRWATCH_OPERATIONS"] = defaultValue(
		e["CASDOOR_DIRWATCH_OPERATIONS"], "Add,Modify,Delete")
	e["CASDOOR_DIRWATCH_ATTRIBUTES"] = defaultValue(
		e["CASDOOR_DIRWATCH_ATTRIBUTES"],
		"member,memberOf,userAccountControl,sAMAccountName,userPrincipalName,displayName,mail,"+
			e["SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"],
	)
	e["CASDOOR_DIRWATCH_DEBOUNCE_SECONDS"] = defaultValue(
		e["CASDOOR_DIRWATCH_DEBOUNCE_SECONDS"], "5")
	e["CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS"] = defaultValue(
		e["CASDOOR_DIRWATCH_MIN_INTERVAL_SECONDS"], "60")
	e["CASDOOR_DIRWATCH_POLL_SECONDS"] = defaultValue(
		e["CASDOOR_DIRWATCH_POLL_SECONDS"], "1")
}

func ensureSigningKeypair(e map[string]string, secrets *secretStore) error {
	privateKey, err := secrets.Ensure("CASDOOR_SIGNING_KEY", func() (string, error) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), nil
	})
	if err != nil {
		return err
	}
	certificate, err := secrets.Ensure("CASDOOR_SIGNING_CERT", func() (string, error) {
		block, _ := pem.Decode([]byte(privateKey))
		if block == nil {
			return "", fmt.Errorf("invalid Casdoor signing key")
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: e["CASDOOR_DOMAIN"]},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
	})
	if err != nil {
		return err
	}
	e["CASDOOR_SIGNING_KEY"], e["CASDOOR_SIGNING_CERT"] = privateKey, certificate
	return nil
}

func renderCasdoor(e map[string]string) (map[string]string, error) {
	appConf, err := renderAppConf(e)
	if err != nil {
		return nil, err
	}
	initData, err := renderInitData(e)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"conf/app.conf":                appConf,
		"conf/init_data.template.json": initData,
	}, nil
}

func renderAppConf(e map[string]string) (string, error) {
	if err := requireKeys(e, []string{"CASDOOR_DOMAIN_FULL", "CASDOOR_DB_HOST", "CASDOOR_DB_PORT", "CASDOOR_DB_USERNAME", "CASDOOR_DB_PASSWORD", "CASDOOR_DB_NAME"}); err != nil {
		return "", err
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable", e["CASDOOR_DB_HOST"], e["CASDOOR_DB_PORT"], e["CASDOOR_DB_USERNAME"], e["CASDOOR_DB_PASSWORD"], e["CASDOOR_DB_NAME"])
	return strings.Join([]string{
		"appname = casdoor",
		"httpport = 8000",
		"runmode = prod",
		"copyrequestbody = true",
		"driverName = postgres",
		"dataSourceName = " + strconv.Quote(dsn),
		"dbName = " + strconv.Quote(e["CASDOOR_DB_NAME"]),
		"tableNamePrefix =",
		"showSql = false",
		"origin = " + strconv.Quote(e["CASDOOR_DOMAIN_FULL"]),
		"originFrontend = " + strconv.Quote(e["CASDOOR_DOMAIN_FULL"]),
		"defaultApplication = \"app-built-in\"",
		"defaultLanguage = " + strconv.Quote(e["CASDOOR_DEFAULT_LANGUAGE"]),
		"showGithubCorner = false",
		"isDemoMode = false",
		"enableErrorMask = true",
		"enableGzip = true",
		"logPostOnly = true",
		"quota = {\"organization\": -1, \"user\": -1, \"application\": -1, \"provider\": -1}",
		"logConfig = {\"adapter\":\"console\"}",
		"initDataNewOnly = false",
		"initDataFile = \"/tmp/init_data.json\"",
		"frontendBaseDir = \"../cc_0\"",
		"",
	}, "\n"), nil
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
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

func defaultValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
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

func splitCSV(value string) []string {
	out := []string{}
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}
