// Package outbound prepares an audited workspace configuration transaction.
// It does not deploy DMS or release, retry, delete or reroute queued mail.
package outbound

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

var (
	ErrConflict = errors.New("mail outbound configuration conflict")
	ErrUnknown  = errors.New("mail outbound configuration result unknown")
	ErrInvalid  = errors.New("mail outbound configuration invalid")
)

// Previous is obtained by an authorized host from one locked workspace
// snapshot. Passwords is empty only when the managed input is absent.
type Previous struct {
	Routing   string `json:"-"`
	Passwords string `json:"-"`
	Validator string `json:"-"`
}

func (p Previous) String() string   { return "[private mail outbound snapshot]" }
func (p Previous) GoString() string { return p.String() }

// Commit replaces the complete explicitly supplied routing configuration and
// its private password map atomically. The host must authorize the caller and
// audit the operation. A successful commit only saves configuration; the DMS
// queue guard must still run when that configuration is actually deployed.
// A lost reply remains unknown and must not be retried without a fresh read.
func Commit(ctx context.Context, service application.ConfigService, observer application.ConfigCommitObserver, operationID string, previous Previous, desired smtpconfig.Config) error {
	if service == nil || observer == nil || operationID == "" || previous.Validator == "" || desired.Validate() != nil || len(desired.Profiles) == 0 {
		return ErrInvalid
	}
	if _, err := smtpconfig.Parse(previous.Routing, previous.Passwords); err != nil {
		return ErrConflict
	}
	public, err := json.Marshal(desired.Public())
	if err != nil || len(public) > 32768 {
		return ErrInvalid
	}
	passwords := make(map[string]string)
	for _, profile := range desired.Profiles {
		if profile.Provider != "direct" {
			passwords[profile.ID] = profile.Password
		}
	}
	secretBytes, err := json.Marshal(passwords)
	if err != nil || len(secretBytes) > 8192 {
		return ErrInvalid
	}
	secret := string(secretBytes)
	if _, err := smtpconfig.Parse(string(public), secret); err != nil {
		return ErrInvalid
	}
	snapshot, err := service.GetConfig(ctx)
	if err != nil {
		return ErrUnknown
	}
	if !snapshot.Managed || snapshot.Validator != previous.Validator {
		return ErrConflict
	}
	fields := map[string]application.ConfigField{}
	for _, field := range snapshot.Fields {
		if field.Path == "mail.outbound_routing" || field.Path == "mail.outbound_passwords" {
			if _, exists := fields[field.Path]; exists {
				return ErrConflict
			}
			fields[field.Path] = field
		}
	}
	routing, found := fields["mail.outbound_routing"]
	private, privateFound := fields["mail.outbound_passwords"]
	if !found || !privateFound || !routing.Editable || routing.Sensitive || !private.Editable || !private.Sensitive || !private.StoreInput || (private.SensitiveState != "set" && private.SensitiveState != "unset") || (private.SensitiveState == "set") != (previous.Passwords != "") {
		return ErrConflict
	}
	raw, err := json.Marshal(snapshot.Config)
	if err != nil || len(raw) > 4<<20 {
		return ErrUnknown
	}
	var document application.ConfigDocument
	if json.Unmarshal(raw, &document) != nil {
		return ErrUnknown
	}
	if env, ok := document["env"].(map[string]any); ok {
		for _, key := range []string{"MAIL_OUTBOUND_ROUTING", "MAIL_OUTBOUND_PASSWORDS"} {
			if _, exists := env[key]; exists {
				return ErrConflict
			}
		}
	}
	modules, ok := document["modules"].(map[string]any)
	if !ok {
		return ErrConflict
	}
	mail, ok := modules["mail"].(map[string]any)
	if !ok {
		return ErrConflict
	}
	values, ok := mail["config"].(map[string]any)
	if !ok {
		if mail["config"] != nil {
			return ErrConflict
		}
		values = map[string]any{}
		mail["config"] = values
	}
	current := ""
	if value, exists := values["outbound_routing"]; exists {
		current, ok = value.(string)
		if !ok {
			return ErrConflict
		}
	}
	if current != previous.Routing {
		return ErrConflict
	}
	values["outbound_routing"] = string(public)
	candidate := application.ConfigCandidate{Document: document, Sensitive: map[string]application.ConfigSensitiveMutation{
		"mail.outbound_passwords": {Operation: application.ConfigSensitiveSet, Value: &secret},
	}}
	var expectedSecret *string
	if previous.Passwords != "" {
		expectedSecret = &previous.Passwords
	}
	_, err = service.PutConfig(ctx, application.ConfigPutRequest{
		OperationID: operationID, Candidate: candidate, Precondition: application.ConfigPreconditionMatch,
		ExpectedValidator: previous.Validator, ExpectedStoredInputs: map[string]*string{"mail.outbound_passwords": expectedSecret},
	}, observer)
	if err != nil {
		if e, ok := application.ErrorOf(err); ok && e.Code == "config_precondition_failed" {
			return ErrConflict
		}
		return ErrUnknown
	}
	return nil
}
