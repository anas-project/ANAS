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
	if module != "keycloak" {
		return nil
	}
	return calcKeycloak(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "keycloak" {
		return map[string]string{}, nil
	}
	return map[string]string{}, moduleKeycloak(env, workdir)
}
func disabledServices(module string, env map[string]string) []string {
	if module != "keycloak" {
		return nil
	}
	if env["KEYCLOAK_ADMINER_ENABLED"] != "true" {
		return []string{"KEYCLOAK_adminer", "anas_KEYCLOAK_adminer"}
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
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
func calcKeycloak(e map[string]string, _ string, secrets *secretStore) error {
	return identityCalc("KEYCLOAK")(e, secrets)
}
func identityCalc(prefix string) func(map[string]string, *secretStore) error {
	return func(e map[string]string, secrets *secretStore) error {
		e[prefix+"_PASSWORD"] = defaultValue(e[prefix+"_PASSWORD"], e["DEFAULT_SERVICE_ROOT_PASSWORD"])
		e[prefix+"_REALM"] = defaultValue(e[prefix+"_REALM"], "master")
		for _, part := range []string{"", "_TEST", "_MANAGER"} {
			keyPrefix := prefix + part
			domainPrefixKey := keyPrefix + "_DOMAIN_PREFIX"
			if part == "" {
				domainPrefixKey = prefix + "_DOMAIN_PREFIX"
			}
			if e[domainPrefixKey] == "" {
				continue
			}
			e[keyPrefix+"_DOMAIN"] = e[domainPrefixKey] + "." + e["BASE_DOMAIN"]
			e[keyPrefix+"_DOMAIN_PORT"] = e[keyPrefix+"_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
			e[keyPrefix+"_DOMAIN_FULL"] = "https://" + e[keyPrefix+"_DOMAIN_PORT"]
		}
		if e[prefix+"_DB_TYPE"] == "" {
			if e["POSTGRES_HOST"] != "" {
				e[prefix+"_DB_TYPE"] = "postgres"
			} else if e["MARIADB_HOST"] != "" {
				e[prefix+"_DB_TYPE"] = "mariadb"
			}
		}
		if e[prefix+"_DB_TYPE"] == "mariadb" {
			e[prefix+"_NETWORK_DB"] = e["MARIADB_NETWORK_NAME"]
			e[prefix+"_DB_HOST"] = e["MARIADB_HOST"]
			e[prefix+"_DB_PORT"] = e["MARIADB_PORT"]
			e[prefix+"_DB_USERNAME"] = e["MARIADB_USERNAME"]
			e[prefix+"_DB_PASSWORD"] = e["MARIADB_PASSWORD"]
		} else {
			e[prefix+"_NETWORK_DB"] = e["POSTGRES_NETWORK_NAME"]
			e[prefix+"_DB_HOST"] = e["POSTGRES_HOST"]
			e[prefix+"_DB_PORT"] = e["POSTGRES_PORT"]
			e[prefix+"_DB_USERNAME"] = e["POSTGRES_USERNAME"]
			e[prefix+"_DB_PASSWORD"] = e["POSTGRES_PASSWORD"]
		}
		e[prefix+"_HOST"] = "llng"
		e[prefix+"_HANDLER_SOCKET_PORT"] = "9000"
		e[prefix+"_LDAP_AUTH_FILTER"] = "(&" + e["SAMBA_DC_USER_CLASS_FILTER"] + e["SAMBA_DC_USER_ENABLED_FILTER"] + "(" + e["SAMBA_DC_USER_NAME"] + "=$user))"
		e[prefix+"_LDAP_MAIL_FILTER"] = "(&" + e["SAMBA_DC_USER_CLASS_FILTER"] + e["SAMBA_DC_USER_ENABLED_FILTER"] + "(" + e["SAMBA_DC_USER_EMAIL"] + "=$mail))"
		priv, cert, keyID, err := ensureServiceCert(secrets, prefix, e[prefix+"_DOMAIN"])
		if err != nil {
			return err
		}
		e[prefix+"_SAML_SERVICE_PRIVATE_KEY"] = priv
		e[prefix+"_SAML_SERVICE_PUBLIC_KEY"] = cert
		e[prefix+"_OIDC_SERVICE_PRIVATE_KEY"] = priv
		e[prefix+"_OIDC_SERVICE_PUBLIC_KEY"] = cert
		e[prefix+"_OIDC_SERVICE_KEY_ID"] = keyID
		e[prefix+"_SAML_IDP_ENTITY_ID"] = e[prefix+"_DOMAIN_FULL"] + "/saml/metadata"
		e[prefix+"_SAML_IDP_SSO"] = e[prefix+"_DOMAIN_FULL"] + "/saml/singleSignOn"
		e[prefix+"_SAML_IDP_SLO"] = e[prefix+"_DOMAIN_FULL"] + "/saml/singleLogout"
		e[prefix+"_SAML_IDP_SLO_RESPONSE"] = e[prefix+"_DOMAIN_FULL"] + "/saml/singleLogoutReturn"
		e[prefix+"_OIDC_CONFIGURATION_ENDPOINT"] = e[prefix+"_DOMAIN_FULL"] + "/realms/" + e[prefix+"_REALM"] + "/.well-known/openid-configuration"
		return nil
	}
}
func moduleKeycloak(e map[string]string, w string) error { return moduleIdentity("KEYCLOAK", e, w) }
func moduleIdentity(prefix string, e map[string]string, _ string) error {
	if e[prefix+"_DB_TYPE"] == "postgres" {
		e["DB_HOST"] = e["POSTGRES_HOST"]
		e["DB_POST"] = e["POSTGRES_PORT"]
		e["DB_USER"] = e["POSTGRES_USERNAME"]
		e["DB_PASSWORD"] = e["POSTGRES_PASSWORD"]
	} else {
		e["DB_HOST"] = e["MARIADB_HOST"]
		e["DB_POST"] = e["MARIADB_PORT"]
		e["DB_USER"] = e["MARIADB_USERNAME"]
		e["DB_PASSWORD"] = e["MARIADB_PASSWORD"]
	}
	for _, app := range splitCSV(e["APPS_LIST"]) {
		key := "APPS_LIST__" + strings.ToUpper(app) + "__LOGO_PATH"
		if e[key] != "" {
			e["APPS_LIST__"+strings.ToUpper(app)+"__LOGO_NAME"] = filepath.Base(e[key])
		}
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
func ensureServiceCert(secrets *secretStore, prefix, domain string) (string, string, string, error) {
	privKey := prefix + "_SERVICE_PRIVATE_KEY"
	certKey := prefix + "_SERVICE_PUBLIC_KEY"
	keyIDKey := prefix + "_OIDC_SERVICE_KEY_ID"
	priv, err := secrets.Ensure(privKey, func() (string, error) {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})), nil
	})
	if err != nil {
		return "", "", "", err
	}
	cert, err := secrets.Ensure(certKey, func() (string, error) {
		block, _ := pem.Decode([]byte(priv))
		if block == nil {
			return "", fmt.Errorf("invalid %s private key", prefix)
		}
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return "", err
		}
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(time.Now().UnixNano()),
			Subject:      pkix.Name{CommonName: domain},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(3650 * 24 * time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		}
		der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
	})
	if err != nil {
		return "", "", "", err
	}
	keyID, err := secrets.Ensure(keyIDKey, func() (string, error) { return randomHexErr(6) })
	if err != nil {
		return "", "", "", err
	}
	return strconv.Quote(priv), strconv.Quote(cert), keyID, nil
}
