package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/localization"
	"golang.org/x/text/language"
)

var supportedHookABIs = []string{"anas.module-hook/v1"}

// adminAccount is the Forgejo administrator this module acts as. It is a
// dedicated account, not a shared one: AGENT-R-008 requires the administrative
// credential to be auditable and rotatable on its own, which is only true if no
// other subsystem signs in with it.
const adminAccount = "anas_ai_agent"

// computeLeasePrefix is the resource namespace the compute contract publishes
// this module's sandbox lease under.
const computeLeasePrefix = "ANAS_COMPUTE_RESOURCE__AI_AGENT__WORK_INSTANCES__"

var (
	fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repoPattern        = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}/[A-Za-z0-9._-]{1,100}$`)
	runtimeIDPattern   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
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
	if req.Module != "ai_agent" {
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
	case "after_start":
		if err := reconcileAdminAccount(env); err != nil {
			return hookResponse{}, err
		}
		return hookResponse{}, nil
	default:
		return hookResponse{}, nil
	}
}

func calculate(e map[string]string, secrets *secretStore) ([]string, error) {
	explicitLanguage := e["AI_AGENT_LANGUAGE"]
	selected, confidence, err := localization.Match(
		defaultValue(explicitLanguage, e["DEFAULT_LANGUAGE"]), agentLanguages, "en",
	)
	if err != nil {
		return nil, fmt.Errorf("ai_agent language: %w", err)
	}
	var warnings []string
	if confidence == language.No {
		source, requested := "inherited global language", e["DEFAULT_LANGUAGE"]
		if explicitLanguage != "" {
			source, requested = "configured language", explicitLanguage
		}
		warnings = append(warnings, fmt.Sprintf(
			"%s %q is unsupported; the agent will write its issue comments in %q", source, requested, selected,
		))
	}
	e["AI_AGENT_LANGUAGE"] = selected

	e["AI_AGENT_DOMAIN"] = e["AI_AGENT_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["AI_AGENT_DOMAIN_PORT"] = e["AI_AGENT_DOMAIN"] + ":" + e["TRAEFIK_BASE_PORT"]
	e["AI_AGENT_DOMAIN_FULL"] = "https://" + e["AI_AGENT_DOMAIN_PORT"]
	// The registered webhook URL is derived, never configured. An operator who
	// could set it by hand could point Forgejo at a host this deployment does
	// not control, and every signed delivery would follow.
	e["AI_AGENT_WEBHOOK_URL"] = e["AI_AGENT_DOMAIN_FULL"] + "/forgejo/webhook"
	e["AI_AGENT_FORGEJO_ADMIN_USERNAME"] = adminAccount

	adminPassword, err := secrets.Ensure("AI_AGENT_FORGEJO_ADMIN_PASSWORD", func() (string, error) {
		return randomHex(32)
	})
	if err != nil {
		return nil, err
	}
	e["AI_AGENT_FORGEJO_ADMIN_PASSWORD"] = adminPassword
	webhookSecret, err := secrets.Ensure("AI_AGENT_WEBHOOK_SECRET", func() (string, error) {
		return randomHex(32)
	})
	if err != nil {
		return nil, err
	}
	e["AI_AGENT_WEBHOOK_SECRET"] = webhookSecret

	publishCapabilityGroups(e)

	if e["AI_AGENT_DB_TYPE"] != "" && e["AI_AGENT_DB_TYPE"] != "postgres" {
		return nil, fmt.Errorf("AI_AGENT_DB_TYPE must resolve to postgres, got %q", e["AI_AGENT_DB_TYPE"])
	}
	if err := validateEnabled(e); err != nil {
		return nil, err
	}
	return warnings, nil
}

// capabilityGroups are the directory groups that grant use of this module.
// They are appended to the deployment-wide list the directory module reads, the
// same way an application appends itself to APPS_LIST -- so enabling a runtime
// creates its group and no group is written down twice.
//
// Two of them are properties of the module: `execute` gates approving an
// execution, `terminal` lets a non-administrator attach to a running agent.
// The rest are one per enabled runtime, taken from configuration rather than
// from a list here, so this file names no runtime (AGENT-R-059, AGENT-R-060).
func publishCapabilityGroups(e map[string]string) {
	groups := splitCSV(e["ANAS_IDENTITY_CAPABILITY_GROUPS"])
	add := func(capability string) {
		name := "ai_agent_" + capability
		for _, existing := range groups {
			if existing == name {
				return
			}
		}
		groups = append(groups, name)
	}
	// A disabled deployment publishes nothing: a group nobody can use is a
	// group an administrator has to wonder about.
	if e["AI_AGENT_ENABLED"] != "true" {
		return
	}
	add("execute")
	add("terminal")
	for _, runtime := range splitCSV(e["AI_AGENT_RUNTIMES"]) {
		if runtimeIDPattern.MatchString(runtime) {
			add(runtime)
		}
	}
	sort.Strings(groups)
	e["ANAS_IDENTITY_CAPABILITY_GROUPS"] = strings.Join(groups, ",")
}

// validateEnabled is AGENT-R-004 at apply time. `enabled` is the only feature
// switch, so it must not be possible to turn it on and get a control plane that
// can receive a webhook but cannot create an account, cannot decide which
// repositories participate, or has no instance to run a job in. Each missing
// input is named, because "misconfigured" sends an operator hunting.
func validateEnabled(e map[string]string) error {
	if e["AI_AGENT_ENABLED"] != "true" {
		return nil
	}
	for _, key := range []string{
		"AI_AGENT_FORGEJO_ADMIN_PASSWORD", "AI_AGENT_WEBHOOK_SECRET",
		"AI_AGENT_REPOSITORY_ALLOWLIST", "AI_AGENT_RUNTIMES", "AI_AGENT_RUNTIME_IMAGES",
	} {
		if strings.TrimSpace(e[key]) == "" {
			return fmt.Errorf("%s is required when ai_agent is enabled", key)
		}
	}
	for _, repo := range splitCSV(e["AI_AGENT_REPOSITORY_ALLOWLIST"]) {
		if !repoPattern.MatchString(repo) {
			return fmt.Errorf("repository %q in AI_AGENT_REPOSITORY_ALLOWLIST is not owner/repo", repo)
		}
	}
	pinned, err := runtimeImages(e["AI_AGENT_RUNTIME_IMAGES"])
	if err != nil {
		return err
	}
	for _, runtime := range splitCSV(e["AI_AGENT_RUNTIMES"]) {
		if !runtimeIDPattern.MatchString(runtime) {
			return fmt.Errorf("agent runtime %q is not a usable runtime id", runtime)
		}
		if pinned[runtime] == "" {
			return fmt.Errorf("agent runtime %q has no pinned image in AI_AGENT_RUNTIME_IMAGES", runtime)
		}
	}
	// The compute lease is the execution face. Without it an approved job has
	// nowhere to run, and the module would accept approvals it can never honour.
	for _, key := range []string{"INTERFACE", "ENDPOINT", "SANDBOX", "CLIENT_CERT", "CLIENT_KEY"} {
		if strings.TrimSpace(e[computeLeasePrefix+key]) == "" {
			return fmt.Errorf("the compute binding is incomplete: %s is empty", computeLeasePrefix+key)
		}
	}
	return nil
}

func runtimeImages(value string) (map[string]string, error) {
	pinned := map[string]string{}
	for _, entry := range splitCSV(value) {
		id, digest, found := strings.Cut(entry, "=")
		id, digest = strings.TrimSpace(id), strings.TrimSpace(digest)
		if !found || !runtimeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("AI_AGENT_RUNTIME_IMAGES entry %q is not runtime=fingerprint", entry)
		}
		// A tag would let the image behind an approved runtime change without a
		// configuration change; only a pinned digest is accepted.
		if !fingerprintPattern.MatchString(digest) {
			return nil, fmt.Errorf("agent runtime %q must be pinned to a SHA-256 fingerprint", id)
		}
		if pinned[id] != "" {
			return nil, fmt.Errorf("agent runtime %q is pinned more than once", id)
		}
		pinned[id] = digest
	}
	return pinned, nil
}

func renderEnv(e map[string]string) error {
	for _, key := range []string{
		"AI_AGENT_DOMAIN", "AI_AGENT_WEBHOOK_URL", "AI_AGENT_WEBHOOK_SECRET",
		"AI_AGENT_FORGEJO_ADMIN_USERNAME", "AI_AGENT_FORGEJO_ADMIN_PASSWORD",
		"AI_AGENT_DB_HOST", "AI_AGENT_DB_NAME", "AI_AGENT_DB_USERNAME", "AI_AGENT_DB_PASSWORD",
		"AI_AGENT_NETWORK_DB", "AI_AGENT_LANGUAGE", "TZ",
	} {
		if strings.TrimSpace(e[key]) == "" {
			return fmt.Errorf("%s is empty", key)
		}
	}
	if strings.TrimSpace(e["FORGEJO_DOMAIN_FULL"]) == "" {
		return fmt.Errorf("FORGEJO_DOMAIN_FULL is empty; ai_agent has no collaboration surface to talk to")
	}
	if !strings.HasPrefix(e["FORGEJO_DOMAIN_FULL"], "https://") {
		return fmt.Errorf("FORGEJO_DOMAIN_FULL must be an https URL, got %q", e["FORGEJO_DOMAIN_FULL"])
	}
	return validateEnabled(e)
}

type localAdminInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

var runContainerHelper = func(payload []byte, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = bytes.NewReader(payload)
	return cmd.CombinedOutput()
}

// reconcileAdminAccount provisions the Forgejo administrator this module signs
// in as, using the same managed-account entrypoint Forgejo already exposes for
// its own Actions controller. It runs on the ANAS host, not inside the
// orchestrator container: the container never holds the means to create its own
// privileges.
func reconcileAdminAccount(e map[string]string) error {
	if e["AI_AGENT_ENABLED"] != "true" {
		return nil
	}
	for _, key := range []string{"CONTAINER_PREFIX", "AI_AGENT_FORGEJO_ADMIN_PASSWORD"} {
		if strings.TrimSpace(e[key]) == "" {
			return fmt.Errorf("ai_agent administrator reconciliation is missing %s", key)
		}
	}
	payload, err := json.Marshal(localAdminInput{
		Username: adminAccount, Email: adminAccount + "@localhost.invalid",
		Password: e["AI_AGENT_FORGEJO_ADMIN_PASSWORD"],
	})
	if err != nil {
		return err
	}
	container := e["CONTAINER_PREFIX"] + "forgejo"
	if _, err := runContainerHelper(payload, "docker", "exec", "-i", "--user", "1000:1000",
		container, "/usr/local/bin/anas-forgejo-entrypoint", "local-admin"); err != nil {
		// The error is reported without the output: the payload carried the
		// password, and a helper that echoes its input would put it in the
		// deployment log (AGENT-R-010).
		return fmt.Errorf("ai_agent administrator account reconciliation failed")
	}
	return nil
}

func handleCredential(req hookRequest) (credentialResult, error) {
	operation := req.Credential
	handlers := map[string]string{
		"credential_probe":     "probe-ai-agent-webhook-secret",
		"credential_reconcile": "reconcile-ai-agent-webhook-secret",
		"credential_verify":    "verify-ai-agent-webhook-secret",
	}
	if req.Module != "ai_agent" || operation == nil ||
		operation.CredentialID != "ai_agent.webhook_secret" ||
		operation.SecretKey != "AI_AGENT_WEBHOOK_SECRET" ||
		operation.Handler != handlers[req.Phase] {
		return credentialResult{}, fmt.Errorf("invalid ai_agent credential operation")
	}
	desired := req.Secrets[operation.DesiredSecretKey]
	if desired == "" {
		return credentialResult{}, fmt.Errorf("missing ai_agent desired credential")
	}
	container := req.Env["CONTAINER_PREFIX"] + "ai_agent"
	if container == "ai_agent" {
		return credentialResult{}, fmt.Errorf("missing ai_agent container prefix")
	}
	status := probeContainerEnv(container, "AI_AGENT_WEBHOOK_SECRET", desired)
	if req.Phase == "credential_reconcile" {
		if operation.Authority != "anas" {
			return credentialResult{}, fmt.Errorf("ai_agent webhook secret authority is external")
		}
		if status != "match" {
			return credentialResult{}, fmt.Errorf("the ai_agent candidate container did not receive the desired webhook secret")
		}
	}
	return credentialResult{CredentialID: operation.CredentialID, Status: status}, nil
}

var credentialDockerInspect = func(container string) ([]byte, error) {
	return exec.Command("docker", "inspect", "--format", "{{json .Config.Env}}", container).Output()
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

// agentLanguages is what the orchestrator itself can write. It is short and
// honest: these are the languages its status comments and refusal messages are
// translated into, not the languages a model can converse in.
var agentLanguages = []localization.Target{
	{Language: "en", Value: "en"},
	{Language: "zh-CN", Value: "zh-CN"},
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
