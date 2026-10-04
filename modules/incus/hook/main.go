package main

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
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
}

type dockerCopy struct {
	Source      string `json:"source"`
	Container   string `json:"container"`
	Destination string `json:"destination"`
}

type secretStore struct {
	values map[string]string
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
		if err := calculate(req.Module, env, secrets); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values)}, nil
	default:
		return hookResponse{}, nil
	}
}

func calculate(module string, e map[string]string, secrets *secretStore) error {
	if module != "incus" {
		return nil
	}
	// Derived from this calculation only; a stale raw environment value is not
	// authority to join an administrator-owned host network.
	e["INCUS_NETWORK_EXTERNAL"] = "false"
	e["INCUS_CONTROL_NETWORK_NAME"] = ""
	e["INCUS_NETWORK_NAME"] = defaultValue(e["INCUS_NETWORK_NAME"], e["NETWORK_PREFIX"]+"incus")

	// A lease network gets IPv6 only when the operator wants it and the host
	// actually has a global address to source it from. Handing a guest a v6
	// network the host cannot route makes every outbound connection wait for a
	// timeout before falling back, which reads as a hung job rather than a
	// misconfiguration.
	e["INCUS_NETWORK_IPV6"] = boolValue(e["IPv6"] != "false" && e["HOST_HAS_IPV6"] == "true")

	automatic, err := resolveConnection(e, secrets)
	if err != nil {
		return err
	}
	if e["INCUS_STORAGE_POOL"] == "" {
		e["INCUS_STORAGE_POOL"] = "default"
	}
	if err := validateLANExtraSubnets(e["INCUS_LAN_EXTRA_SUBNETS"]); err != nil {
		return err
	}
	publishPortBindingRange(e, automatic)
	return nil
}

func resolveConnection(e map[string]string, secrets *secretStore) (bool, error) {
	connectionKeys := []string{
		"INCUS_ENDPOINT",
		"INCUS_SERVER_CERTIFICATE_B64",
		"INCUS_ADMIN_CERTIFICATE_B64",
		"INCUS_ADMIN_KEY_B64",
	}
	set := 0
	for _, key := range connectionKeys {
		if strings.TrimSpace(e[key]) != "" {
			set++
		}
	}
	if set > 0 && set != len(connectionKeys) {
		return false, fmt.Errorf("incus connection settings must be either all explicit or all omitted for host auto-connection")
	}
	automatic := false
	if secrets != nil {
		source, binding := secrets.values[autoSourceSecretKey], secrets.values[autoBindingSecretKey]
		if source != "" || binding != "" {
			if source != autoSourceValue || !validAutoBindingDigest(binding) {
				return false, fmt.Errorf("incus automatic connection binding is incomplete or invalid")
			}
			automatic = true
			if set == len(connectionKeys) {
				for _, key := range connectionKeys {
					if secrets.values[key] == "" || secrets.values[key] != e[key] {
						return false, fmt.Errorf("incus explicit values conflict with the existing automatic binding; reconcile the connection source before applying")
					}
				}
			}
		}
	}
	if set == 0 || automatic {
		bundle, err := loadDefaultHostConnectionBundle()
		if err != nil {
			return false, err
		}
		if e["INCUS_IMAGE_ARCHITECTURE"] != "" && e["INCUS_IMAGE_ARCHITECTURE"] != bundle.Architecture {
			return false, fmt.Errorf("incus image_architecture does not match the host connection bundle")
		}
		if e["INCUS_STORAGE_POOL"] != "" && e["INCUS_STORAGE_POOL"] != bundle.StoragePool {
			return false, fmt.Errorf("incus storage_pool does not match the host connection bundle")
		}
		if err := applyHostConnectionBundle(e, secrets, bundle); err != nil {
			return false, err
		}
		e["INCUS_NETWORK_NAME"] = bundle.ControlNetwork
		e["INCUS_NETWORK_EXTERNAL"] = "true"
		e["INCUS_CONTROL_NETWORK_NAME"] = bundle.ControlNetwork
		automatic = true
	} else if e["INCUS_IMAGE_ARCHITECTURE"] == "" {
		return false, fmt.Errorf("incus image_architecture is required for an explicit remote daemon")
	}

	// Refuse at apply time rather than at provision time. Every one of these is
	// required before a single lease can be ensured, and a half-configured
	// provider that fails midway through apply is harder to reason about than
	// one that never starts.
	endpoint, err := url.Parse(e["INCUS_ENDPOINT"])
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawPath != "" {
		return false, fmt.Errorf("incus endpoint must be an HTTPS origin without credentials, path, query or fragment")
	}
	for key, kind := range map[string]string{
		"INCUS_SERVER_CERTIFICATE_B64": "CERTIFICATE",
		"INCUS_ADMIN_CERTIFICATE_B64":  "CERTIFICATE",
		"INCUS_ADMIN_KEY_B64":          "PRIVATE KEY",
	} {
		if err := validatePEM(key, e[key], kind); err != nil {
			return false, err
		}
	}
	// Config uses the manifest's canonical *_CERTIFICATE_B64 names. The
	// compute provider wire projection uses *_CERT_B64. Derive it only from
	// validated canonical input, never accept a stale raw-env alias instead.
	// Core's sensitive-value propagation taints these equal-value aliases.
	e["INCUS_SERVER_CERT_B64"] = e["INCUS_SERVER_CERTIFICATE_B64"]
	e["INCUS_ADMIN_CERT_B64"] = e["INCUS_ADMIN_CERTIFICATE_B64"]
	return automatic, nil
}

// validatePEM checks shape only. It never returns the value, and never says
// more about a malformed credential than which parameter is wrong.
func validatePEM(key, value, kind string) error {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return fmt.Errorf("%s is required before the Incus provider can be enabled", strings.ToLower(strings.TrimSuffix(key, "_B64")))
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return fmt.Errorf("%s must be base64-encoded PEM", strings.ToLower(strings.TrimSuffix(key, "_B64")))
	}
	block, _ := pem.Decode(decoded)
	if block == nil {
		return fmt.Errorf("%s does not decode to PEM", strings.ToLower(strings.TrimSuffix(key, "_B64")))
	}
	if kind == "CERTIFICATE" && block.Type != "CERTIFICATE" {
		return fmt.Errorf("%s must be a PEM certificate", strings.ToLower(strings.TrimSuffix(key, "_B64")))
	}
	if kind == "PRIVATE KEY" && !strings.HasSuffix(block.Type, "PRIVATE KEY") {
		return fmt.Errorf("%s must be a PEM private key", strings.ToLower(strings.TrimSuffix(key, "_B64")))
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

func boolValue(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
