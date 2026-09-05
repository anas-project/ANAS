package main

import (
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
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
	switch req.Phase {
	case "calculate":
		if err := calculate(req.Module, env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env)}, nil
	default:
		return hookResponse{}, nil
	}
}

func calculate(module string, e map[string]string) error {
	if module != "incus" {
		return nil
	}
	e["INCUS_NETWORK_NAME"] = defaultValue(e["INCUS_NETWORK_NAME"], e["NETWORK_PREFIX"]+"incus")

	// A lease network gets IPv6 only when the operator wants it and the host
	// actually has a global address to source it from. Handing a guest a v6
	// network the host cannot route makes every outbound connection wait for a
	// timeout before falling back, which reads as a hung job rather than a
	// misconfiguration.
	e["INCUS_NETWORK_IPV6"] = boolValue(e["IPv6"] != "false" && e["HOST_HAS_IPV6"] == "true")

	// Refuse at apply time rather than at provision time. Every one of these is
	// required before a single lease can be ensured, and a half-configured
	// provider that fails midway through apply is harder to reason about than
	// one that never starts.
	if !strings.HasPrefix(strings.TrimSpace(e["INCUS_ENDPOINT"]), "https://") {
		return fmt.Errorf("incus endpoint must be configured as an HTTPS URL")
	}
	for key, kind := range map[string]string{
		"INCUS_SERVER_CERT_B64": "CERTIFICATE",
		"INCUS_ADMIN_CERT_B64":  "CERTIFICATE",
		"INCUS_ADMIN_KEY_B64":   "PRIVATE KEY",
	} {
		if err := validatePEM(key, e[key], kind); err != nil {
			return err
		}
	}
	return nil
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
