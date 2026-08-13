package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/anas-project/ANAS/internal/localization"
	"golang.org/x/text/language"
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
	Warnings        []string          `json:"warnings,omitempty"`
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
		warnings, err := calculate(req.Module, env, req.Workdir, secrets)
		if err != nil {
			return hookResponse{}, err
		}
		return hookResponse{Env: changed(req.Env, env), Secrets: changed(req.Secrets, secrets.values), Warnings: warnings}, nil
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
func calculate(module string, env map[string]string, workdir string, secrets *secretStore) ([]string, error) {
	if module != "lam" {
		return nil, nil
	}
	return calcLAM(env, workdir, secrets)
}
func renderEnv(module string, env map[string]string, workdir string) (map[string]string, error) {
	if module != "lam" {
		return map[string]string{}, nil
	}
	return map[string]string{}, nil
}
func disabledServices(module string, env map[string]string) []string {
	if module != "lam" {
		return nil
	}
	return nil
}
func afterStart(module string, env map[string]string) []dockerCopy {
	if module != "lam" {
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
func calcLAM(e map[string]string, _ string, _ *secretStore) ([]string, error) {
	explicitLanguage := e["LAM_LANGUAGE"]
	languageValue, confidence, err := localization.Match(defaultValue(explicitLanguage, e["DEFAULT_LANGUAGE"]), lamLanguages, "en-US")
	if err != nil {
		return nil, fmt.Errorf("LAM language: %w", err)
	}
	var warnings []string
	if confidence == language.No {
		source, requested := "inherited global language", e["DEFAULT_LANGUAGE"]
		if explicitLanguage != "" {
			source, requested = "configured language", explicitLanguage
		}
		warnings = append(warnings, fmt.Sprintf("%s %q is unsupported; continuing with fallback %q", source, requested, languageValue))
	}
	e["LAM_LANGUAGE"] = languageValue
	e["LAM_DOMAIN"] = e["LAM_DOMAIN_PREFIX"] + "." + e["BASE_DOMAIN"]
	e["LAM_ADMIN_PASSWORD"] = defaultValue(e["LAM_ADMIN_PASSWORD"], e["SAMBA_DC_ADMIN_PASSWORD"])
	return warnings, nil
}

// The list is the active (non-commented) set in LAM 9.6's config/language.
// Values are deliberately not derived by replacing '-' with '_': LAM consumes
// POSIX locale identifiers and the image must generate every listed locale.
var lamLanguages = []localization.Target{
	{Language: "de-DE", Value: "de_DE.utf8"},
	{Language: "en-GB", Value: "en_GB.utf8"},
	{Language: "en-US", Value: "en_US.utf8"},
	{Language: "es-ES", Value: "es_ES.utf8"},
	{Language: "fr-FR", Value: "fr_FR.utf8"},
	{Language: "el-GR", Value: "el_GR.utf8"},
	{Language: "it-IT", Value: "it_IT.utf8"},
	{Language: "nl-NL", Value: "nl_NL.utf8"},
	{Language: "pl-PL", Value: "pl_PL.utf8"},
	{Language: "pt-BR", Value: "pt_BR.utf8"},
	{Language: "sk-SK", Value: "sk_SK.utf8"},
	{Language: "uk-UA", Value: "uk_UA.utf8"},
	{Language: "ja-JP", Value: "ja_JP.utf8"},
	{Language: "zh-TW", Value: "zh_TW.utf8"},
	{Language: "zh-CN", Value: "zh_CN.utf8"},
}

func defaultValue(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
