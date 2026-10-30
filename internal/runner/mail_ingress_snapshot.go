package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// MailIngressSnapshot is an in-process-only view for the mail installer. It
// must never be returned through an API, written to a browser, or logged.
type MailIngressSnapshot struct {
	Sources     string `json:"-"`
	Credentials string `json:"-"`
	Validator   string `json:"-"`
}

func (s MailIngressSnapshot) String() string   { return "[private mail ingress snapshot]" }
func (s MailIngressSnapshot) GoString() string { return s.String() }

// ReadMailIngressSnapshot reads the declared mail source input and its
// module-owned Secret Store value under the same workspace lock. It is not an
// authorization boundary. The host caller must authenticate, authorize and
// audit access, and must use the configuration transaction's CAS before write.
func ReadMailIngressSnapshot(ctx context.Context, workspace string) (MailIngressSnapshot, error) {
	service, ok := NewWorkspaceConfigService(workspace).(*workspaceConfigApplication)
	if !ok {
		return MailIngressSnapshot{}, errors.New("mail ingress snapshot unavailable")
	}
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return MailIngressSnapshot{}, errors.New("mail ingress snapshot unavailable")
	}
	defer unlock()
	reg, err := service.registry()
	if err != nil {
		return MailIngressSnapshot{}, errors.New("mail ingress snapshot unavailable")
	}
	module, exists := reg["mail"]
	if !exists {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	target, err := resolveConfigTarget("mail.ingress_credentials", reg)
	policy := policyForTarget(target, reg)
	if err != nil || target.Display != "mail.ingress_credentials" || !policy.Sensitive || !policy.StoreInput || moduleParamEnvKey("mail", module.EnvPrefix, module.Exports, target.Parameter) != "MAIL_INGRESS_CREDENTIALS" {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil || !current.managed || current.validator == "" {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	store, err := loadSecretStore(stateDir(service.workspace))
	if err != nil {
		return MailIngressSnapshot{}, errors.New("mail ingress snapshot unavailable")
	}
	document, root, _, err := privateConfigDocument(current.configBytes, reg, store)
	if err != nil {
		return MailIngressSnapshot{}, errors.New("mail ingress snapshot unavailable")
	}
	// Old inline credentials and alternative environment spellings are not
	// silently adopted; migration must use the normal configuration transaction.
	for _, path := range sensitiveConfigYAMLPaths(target, "MAIL_INGRESS_CREDENTIALS") {
		if nodeAtYAMLPath(root, path) != nil {
			return MailIngressSnapshot{}, errors.New("mail ingress configuration conflict")
		}
	}
	for _, key := range []string{"MAIL_INGRESS_SOURCES", "MAIL_INGRESS_CAPACITY_BYTES", "MAIL_INGRESS_CREDENTIALS"} {
		if nodeAtYAMLPath(root, []string{"env", key}) != nil {
			return MailIngressSnapshot{}, errors.New("mail ingress configuration conflict")
		}
	}
	modules, ok := document["modules"].(map[string]any)
	if !ok {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	mail, ok := modules["mail"].(map[string]any)
	if !ok {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	values, ok := mail["config"].(map[string]any)
	if !ok && mail["config"] != nil {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
	}
	sources := ""
	if value, exists := values["ingress_sources"]; exists {
		sources, ok = value.(string)
		if !ok || len(sources) > 65536 {
			return MailIngressSnapshot{}, errors.New("mail ingress configuration unavailable")
		}
	}
	credential, present := store.values["MAIL_INGRESS_CREDENTIALS"]
	if present && (credential == "" || len(credential) > 65536 || store.metadata["MAIL_INGRESS_CREDENTIALS"].Owner != "mail" || store.metadata["MAIL_INGRESS_CREDENTIALS"].Kind != "managed_input") {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration conflict")
	}
	var count int
	if strings.TrimSpace(sources) != "" {
		var list []json.RawMessage
		if json.Unmarshal([]byte(sources), &list) != nil || list == nil || len(list) > 8 {
			return MailIngressSnapshot{}, errors.New("mail ingress configuration conflict")
		}
		count = len(list)
	}
	if (count == 0 && present) || (count != 0 && !present) {
		return MailIngressSnapshot{}, errors.New("mail ingress configuration conflict")
	}
	return MailIngressSnapshot{Sources: sources, Credentials: credential, Validator: current.validator}, nil
}
