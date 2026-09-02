package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/netip"
	"os"
	"sort"
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
	RuntimeFiles    map[string]string `json:"runtime_files,omitempty"`
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
	case "local_account_apply", "local_account_rotate", "local_account_rollback":
		if err := handleLocalAccount(req); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{}, nil
	case "calculate":
		if err := calculate(req.Module, env, req.Workdir, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	case "render_env", "runtime_restore":
		files, err := renderRuntimeEnv(req.Module, env, secrets.values)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), RuntimeFiles: files}, nil
	case "services":
		return hookResponse{DisableServices: disabledServices(req.Module, env)}, nil
	case "after_start":
		return hookResponse{DockerCopies: afterStart(req.Module, env)}, nil
	default:
		return hookResponse{}, nil
	}
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "traefik" {
		return nil
	}
	if err := domainCalc("TRAEFIK", "traefik")(env, workdir, secrets); err != nil {
		return err
	}
	if err := validateTrustedProxyCIDRs(env["TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS"]); err != nil {
		return err
	}
	for _, name := range transportNames(env) {
		key := transportIdentitySecretKey(name)
		encoded, err := secrets.Ensure(key, func() (string, error) {
			identity, err := newTransportIdentity(name, time.Now().UTC())
			if err != nil {
				return "", err
			}
			body, err := json.Marshal(identity)
			return string(body), err
		})
		if err != nil {
			return err
		}
		if _, err := parseTransportIdentity(encoded); err != nil {
			return fmt.Errorf("traefik servers transport %s identity: %w", name, err)
		}
	}
	env["TRAEFIK_DASHBOARD_URL"] = env["TRAEFIK_DOMAIN_FULL"] + "/dashboard/"
	return nil
}

type transportIdentity struct {
	CACertificatePEM string `json:"ca_certificate_pem"`
	CAPrivateKeyPEM  string `json:"ca_private_key_pem"`
	ClientCertPEM    string `json:"client_certificate_pem"`
	ClientKeyPEM     string `json:"client_private_key_pem"`
	ClientSPKISHA256 string `json:"client_spki_sha256"`
}

func transportNames(env map[string]string) []string {
	const prefix = "ANAS_TRAEFIK_SERVERS_TRANSPORT__"
	const suffix = "__SERVER_NAME"
	var names []string
	for key, value := range env {
		if value == "" || !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		if name == "" || strings.ContainsFunc(name, func(char rune) bool {
			return !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_')
		}) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func transportIdentitySecretKey(name string) string {
	return "TRAEFIK_SERVERS_TRANSPORT__" + name + "__CLIENT_IDENTITY"
}

func newTransportIdentity(name string, now time.Time) (transportIdentity, error) {
	caPublic, caPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return transportIdentity{}, err
	}
	caSerial, err := randomCertificateSerial()
	if err != nil {
		return transportIdentity{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: "ANAS Traefik transport " + name + " CA", Organization: []string{"ANAS"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caPrivate)
	if err != nil {
		return transportIdentity{}, err
	}
	clientPublic, clientPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return transportIdentity{}, err
	}
	clientSerial, err := randomCertificateSerial()
	if err != nil {
		return transportIdentity{}, err
	}
	clientTemplate := &x509.Certificate{
		SerialNumber: clientSerial, Subject: pkix.Name{CommonName: "ANAS Traefik transport " + name, Organization: []string{"ANAS"}},
		NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(2, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, clientPublic, caPrivate)
	if err != nil {
		return transportIdentity{}, err
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caPrivate)
	if err != nil {
		return transportIdentity{}, err
	}
	clientKeyDER, err := x509.MarshalPKCS8PrivateKey(clientPrivate)
	if err != nil {
		return transportIdentity{}, err
	}
	clientSPKI, err := x509.MarshalPKIXPublicKey(clientPublic)
	if err != nil {
		return transportIdentity{}, err
	}
	digest := sha256.Sum256(clientSPKI)
	return transportIdentity{
		CACertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		CAPrivateKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER})),
		ClientCertPEM:    string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})),
		ClientKeyPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: clientKeyDER})),
		ClientSPKISHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func randomCertificateSerial() (*big.Int, error) {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, limit)
}

func parseTransportIdentity(encoded string) (transportIdentity, error) {
	var identity transportIdentity
	if err := json.Unmarshal([]byte(encoded), &identity); err != nil {
		return transportIdentity{}, err
	}
	if identity.CACertificatePEM == "" || identity.CAPrivateKeyPEM == "" || identity.ClientCertPEM == "" || identity.ClientKeyPEM == "" || len(identity.ClientSPKISHA256) != 64 {
		return transportIdentity{}, fmt.Errorf("credential bundle is incomplete")
	}
	return identity, nil
}

func validateTrustedProxyCIDRs(value string) error {
	for _, raw := range strings.Split(value, ",") {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		if _, err := netip.ParsePrefix(item); err == nil {
			continue
		}
		if _, err := netip.ParseAddr(item); err == nil {
			continue
		}
		return fmt.Errorf("traefik.forwarded_headers_trusted_ips contains invalid IP or CIDR %q", item)
	}
	return nil
}
func renderRuntimeEnv(module string, env map[string]string, secrets map[string]string) (map[string]string, error) {
	if module != "traefik" {
		return map[string]string{}, nil
	}
	user, password := env["TRAEFIK_LOCAL_ADMIN_USERNAME"], env["TRAEFIK_LOCAL_ADMIN_PASSWORD"]
	if user == "" || password == "" {
		return nil, fmt.Errorf("managed Traefik administrator credential is missing")
	}
	content, err := traefikAuthConfig(user, password)
	if err != nil {
		return nil, err
	}
	files := map[string]string{"dynamic/dashboard-auth.yml": content}
	for _, name := range transportNames(env) {
		identity, err := parseTransportIdentity(secrets[transportIdentitySecretKey(name)])
		if err != nil {
			return nil, fmt.Errorf("managed identity for servers transport %s is missing or invalid: %w", name, err)
		}
		root := "dynamic/client-identities/" + name + "/"
		files[root+"ca.crt"] = identity.CACertificatePEM
		files[root+"client.crt"] = identity.ClientCertPEM
		files[root+"client.key"] = identity.ClientKeyPEM
		files[root+"client.spki-sha256"] = identity.ClientSPKISHA256 + "\n"
	}
	return files, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "traefik" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "traefik" {
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
func domainCalc(prefix, service string) func(map[string]string, string, *secretStore) error {
	return func(e map[string]string, _ string, _ *secretStore) error {
		e[prefix+"_HOSTNAME"] = e["CONTAINER_PREFIX"] + service
		e[prefix+"_DOMAIN"] = e[prefix+"_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
		e[prefix+"_DOMAIN_PORT"] = e[prefix+"_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
		e[prefix+"_DOMAIN_FULL"] = "https://" + e[prefix+"_DOMAIN_PORT"]
		return nil
	}
}
