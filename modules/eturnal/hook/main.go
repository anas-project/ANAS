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
	ABI        string               `json:"abi"`
	Phase      string               `json:"phase"`
	Module     string               `json:"module"`
	Workdir    string               `json:"workdir"`
	Env        map[string]string    `json:"env"`
	Secrets    map[string]string    `json:"secrets"`
	Credential *credentialOperation `json:"credential,omitempty"`
}

type hookResponse struct {
	Env             map[string]string `json:"env,omitempty"`
	Secrets         map[string]string `json:"secrets,omitempty"`
	Files           map[string]string `json:"files,omitempty"`
	DisableServices []string          `json:"disable_services,omitempty"`
	DockerCopies    []dockerCopy      `json:"docker_copies,omitempty"`
	Credential      *credentialResult `json:"credential,omitempty"`
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
	if strings.HasPrefix(req.Phase, "credential_") {
		result, err := handleCredential(req)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Credential: &result}, nil
	}
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

const eturnalCredentialID = "eturnal.secret"

var credentialDockerCommand = func(stdin []byte, args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdin = strings.NewReader(string(stdin))
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

var credentialRetryPause = func() { time.Sleep(250 * time.Millisecond) }

func handleCredential(req hookRequest) (credentialResult, error) {
	operation := req.Credential
	if req.Module != "eturnal" || operation == nil || operation.CredentialID != eturnalCredentialID {
		return credentialResult{}, fmt.Errorf("invalid Eturnal credential operation")
	}
	expectedHandler := map[string]string{
		"credential_probe":     "probe-eturnal-secret",
		"credential_reconcile": "reconcile-eturnal-secret",
		"credential_verify":    "verify-eturnal-secret",
	}[req.Phase]
	if expectedHandler == "" || operation.Handler != expectedHandler || operation.SecretKey != "TURN_SECRET" {
		return credentialResult{}, fmt.Errorf("invalid Eturnal credential handler")
	}
	desired := req.Secrets[operation.DesiredSecretKey]
	if desired == "" {
		return credentialResult{}, fmt.Errorf("missing Eturnal desired credential")
	}
	container := req.Env["CONTAINER_PREFIX"] + "eturnal"
	if container == "eturnal" {
		return credentialResult{}, fmt.Errorf("missing Eturnal container prefix")
	}
	if req.Phase == "credential_reconcile" {
		if operation.Authority != "anas" {
			return credentialResult{}, fmt.Errorf("Eturnal credential authority is external")
		}
		if err := credentialDockerCommand(nil, "restart", container); err != nil {
			return credentialResult{}, fmt.Errorf("restart Eturnal credential authority")
		}
		for attempt := 0; attempt < 20; attempt++ {
			status := probeEturnalCredential(container, desired)
			if status == "match" {
				return credentialResult{CredentialID: eturnalCredentialID, Status: "reconciled", Changed: true}, nil
			}
			if status != "unavailable" {
				return credentialResult{}, fmt.Errorf("Eturnal credential did not converge after restart")
			}
			credentialRetryPause()
		}
		return credentialResult{}, fmt.Errorf("Eturnal credential authority did not become ready")
	}
	return credentialResult{CredentialID: eturnalCredentialID, Status: probeEturnalCredential(container, desired)}, nil
}

func probeEturnalCredential(container, desired string) string {
	const script = `IFS= read -r desired || exit 5
config_dir=${ANAS_CONFIG_DIR:-${ETURNAL_ETC_DIR:-}}
[ -n "$config_dir" ] || exit 5
config=$config_dir/eturnal.yml
[ -f "$config" ] || exit 4
escaped=$(printf '%s' "$desired" | sed "s/'/''/g")
grep -Fqx "  secret: '$escaped'" "$config" || exit 3`
	err := credentialDockerCommand([]byte(desired+"\n"), "exec", "-i", container, "sh", "-c", script)
	if err == nil {
		return "match"
	}
	if exit, ok := err.(interface{ ExitCode() int }); ok {
		switch exit.ExitCode() {
		case 3:
			return "mismatch"
		case 4:
			return "missing"
		}
	}
	return "unavailable"
}
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) error {
	if module != "eturnal" {
		return nil
	}
	return calcEturnal(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "eturnal" {
		return map[string]string{}, nil
	}
	env["TURN_RELAY_MIN_PORT"] = "50000"
	env["TURN_RELAY_MAX_PORT"] = "51000"
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "eturnal" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "eturnal" {
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
func calcEturnal(e map[string]string, _ string, secrets *secretStore) error {
	e["TURN_HOSTNAME"] = e["CONTAINER_PREFIX"] + "eturnal"
	if e["TURN_SECRET"] == "" {
		v, err := secrets.Ensure("TURN_SECRET", func() (string, error) { return randomHexErr(16) })
		if err != nil {
			return err
		}
		e["TURN_SECRET"] = v
	}
	e["TURN_DOMAIN"] = defaultValue(e["TURN_DOMAIN"], e["TURN_DOMAIN_PREFIX"]+"."+e["BASE_DOMAIN"])
	e["ETURNAL_DOMAIN"] = e["TURN_DOMAIN"]
	e["TURN_DOMAIN_PORT"] = e["TURN_DOMAIN"] + ":" + e["TURN_PORT"]
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
