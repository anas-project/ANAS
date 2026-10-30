package runner

import (
	"context"
	"errors"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

// MailOutboundSnapshot is an in-process-only view for the restricted host
// installer. It must not be returned to a browser, logged or serialized.
type MailOutboundSnapshot struct {
	Routing   string `json:"-"`
	Passwords string `json:"-"`
	Validator string `json:"-"`
}

func (s MailOutboundSnapshot) String() string   { return "[private mail outbound snapshot]" }
func (s MailOutboundSnapshot) GoString() string { return s.String() }

// ReadMailOutboundSnapshot holds the workspace read lock for the entire read.
// It does not authorize the caller or apply any changes. The host must still
// authorize and audit, and compare the snapshot in the write transaction.
func ReadMailOutboundSnapshot(ctx context.Context, workspace string) (MailOutboundSnapshot, error) {
	service, ok := NewWorkspaceConfigService(workspace).(*workspaceConfigApplication)
	if !ok {
		return MailOutboundSnapshot{}, errors.New("mail outbound snapshot unavailable")
	}
	return readMailOutboundSnapshot(ctx, service)
}

func readMailOutboundSnapshot(ctx context.Context, service *workspaceConfigApplication) (MailOutboundSnapshot, error) {
	unavailable := errors.New("mail outbound snapshot unavailable")
	conflict := errors.New("mail outbound configuration conflict")
	unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(service.workspace))
	if err != nil {
		return MailOutboundSnapshot{}, unavailable
	}
	defer unlock()
	reg, err := service.registry()
	if err != nil {
		return MailOutboundSnapshot{}, unavailable
	}
	module, exists := reg["mail"]
	if !exists {
		return MailOutboundSnapshot{}, unavailable
	}
	target, err := resolveConfigTarget("mail.outbound_passwords", reg)
	policy := policyForTarget(target, reg)
	if err != nil || target.Display != "mail.outbound_passwords" || !policy.Sensitive || !policy.StoreInput || moduleParamEnvKey("mail", module.EnvPrefix, module.Exports, target.Parameter) != "MAIL_OUTBOUND_PASSWORDS" {
		return MailOutboundSnapshot{}, unavailable
	}
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil || !current.managed || current.validator == "" {
		return MailOutboundSnapshot{}, unavailable
	}
	store, err := loadSecretStore(stateDir(service.workspace))
	if err != nil {
		return MailOutboundSnapshot{}, unavailable
	}
	document, root, _, err := privateConfigDocument(current.configBytes, reg, store)
	if err != nil {
		return MailOutboundSnapshot{}, unavailable
	}
	// Do not adopt legacy inline passwords or environment overrides silently.
	for _, path := range sensitiveConfigYAMLPaths(target, "MAIL_OUTBOUND_PASSWORDS") {
		if nodeAtYAMLPath(root, path) != nil {
			return MailOutboundSnapshot{}, conflict
		}
	}
	for _, key := range []string{"MAIL_OUTBOUND_ROUTING", "MAIL_OUTBOUND_PASSWORDS"} {
		if nodeAtYAMLPath(root, []string{"env", key}) != nil {
			return MailOutboundSnapshot{}, conflict
		}
	}
	modules, ok := document["modules"].(map[string]any)
	if !ok {
		return MailOutboundSnapshot{}, unavailable
	}
	mail, ok := modules["mail"].(map[string]any)
	if !ok {
		return MailOutboundSnapshot{}, unavailable
	}
	values, ok := mail["config"].(map[string]any)
	if !ok && mail["config"] != nil {
		return MailOutboundSnapshot{}, unavailable
	}
	routing := ""
	if value, exists := values["outbound_routing"]; exists {
		routing, ok = value.(string)
		if !ok || len(routing) > 32768 {
			return MailOutboundSnapshot{}, conflict
		}
	}
	passwords, present := store.values["MAIL_OUTBOUND_PASSWORDS"]
	if present && (passwords == "" || len(passwords) > 8192 || store.metadata["MAIL_OUTBOUND_PASSWORDS"].Owner != "mail" || store.metadata["MAIL_OUTBOUND_PASSWORDS"].Kind != "managed_input") {
		return MailOutboundSnapshot{}, conflict
	}
	if _, err := smtpconfig.Parse(routing, passwords); err != nil {
		return MailOutboundSnapshot{}, conflict
	}
	return MailOutboundSnapshot{Routing: routing, Passwords: passwords, Validator: current.validator}, nil
}
