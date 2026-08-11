package main

import (
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
	if module != "lego" {
		return nil
	}
	return calcLego(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "lego" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "lego" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "lego" {
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
func calcLego(e map[string]string, _ string, _ *secretStore) error {
	e["LEGO_EMAIL"] = defaultValue(e["LEGO_EMAIL"], e["EMAIL"])
	e["LEGO_DATA_PATH"] = defaultValue(e["LEGO_DATA_PATH"], filepath.Join(e["DATA_PATH"], "lego/certs"))
	e["LEGO_CERTS_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certificates") + string(os.PathSeparator)
	e["LEGO_CERTS_USER1000_PATH"] = filepath.Join(e["LEGO_DATA_PATH"], "certs1000") + string(os.PathSeparator)
	e["LEGO_CERT_NAME"] = e["BASE_DOMAIN"] + ".crt"
	e["LEGO_KEY_NAME"] = e["BASE_DOMAIN"] + ".key"
	e["LEGO_CA_CERT_NAME"] = e["BASE_DOMAIN"] + ".issuer.crt"

	// Issuer-neutral contract for consumers. LEGO_* stays this cask's private
	// vocabulary: naming the implementation in something every other cask
	// reads is the coupling the IAM binding just removed, and it would have to
	// be unpicked the day a different issuer appears.
	//
	// Consumers mount ANAS_TLS_CERTS_DIR at /certs and reference the names
	// below, matching the convention the existing casks already use.
	e["ANAS_TLS_CERTS_DIR"] = e["LEGO_CERTS_PATH"]
	e["ANAS_TLS_CERT_NAME"] = e["LEGO_CERT_NAME"]
	e["ANAS_TLS_KEY_NAME"] = e["LEGO_KEY_NAME"]
	// Whatever signed the serving certificate: the public intermediate under
	// ACME, the internal root otherwise. Consumers trust it without knowing
	// which, so no cask branches on the certificate source.
	e["ANAS_TLS_ISSUER_NAME"] = e["LEGO_CA_CERT_NAME"]
	// The internal root is published under a stable name even while ACME
	// serves traffic, because bootstrap and renewal failures fall back to it.
	e["ANAS_TLS_INTERNAL_CA_NAME"] = "anas-internal-ca.crt"
	// What a consumer that *verifies* TLS should pin. Neither name above works
	// for that on its own: the issuer chain ends at a public intermediate
	// whose root lives in the system store, and the internal root cannot
	// verify a publicly issued certificate. A consumer pinning either one
	// fails the moment the deployment switches issuers -- and it fails at the
	// consumer, far from here. This bundle carries the public roots and the
	// internal root together, so one path stays correct through the switch.
	e["ANAS_TLS_TRUST_BUNDLE_NAME"] = "anas-trust-bundle.crt"
	return resolveDNSPlatform(e)
}

// resolveDNSPlatform turns the configured DNS vendor into the provider code
// lego's CLI expects and the list of credential variables it reads.
//
// A vendor is only needed for the DNS-01 challenge, so a deployment that never
// attempts ACME does not need one. Demanding it unconditionally would force
// every .test and .lan deployment to name a vendor it will never contact.
func resolveDNSPlatform(e map[string]string) error {
	name := strings.TrimSpace(e["LEGO_DNS_PROVIDER"])
	if name == "" {
		if e["VIRTUAL_DOMAIN"] == "true" {
			return nil
		}
		return fmt.Errorf("lego: services.lego.env.dns_provider is not set, and %s is not a virtual domain so a certificate must be requested;\nset it to one of: %s\nor set global.virtual_domain: true to serve the internal certificate instead",
			e["BASE_DOMAIN"], strings.Join(supportedDNSPlatforms(), ", "))
	}
	platform, ok := lookupDNSPlatform(name)
	if !ok {
		return fmt.Errorf("lego: dns_provider %q is not a DNS platform lego can use for the ACME DNS-01 challenge;\nset services.lego.env.dns_provider to one of: %s",
			name, strings.Join(supportedDNSPlatforms(), ", "))
	}
	e["LEGO_PROVIDER_CODE"] = platform.Provider
	// The credential values arrive under this cask's env prefix; cert.sh
	// re-exports them under the names lego itself reads. Passing the key list
	// rather than the values keeps the translation in one place and keeps this
	// hook from handling secrets it has no use for.
	e["LEGO_DNS_CRED_KEYS"] = strings.Join(platform.Required, " ")
	return nil
}
func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
