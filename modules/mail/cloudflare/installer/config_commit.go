package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

// CommitFirstSource is the first-source form of CommitSource.
func (p PreparedSource) CommitFirstSource(ctx context.Context, service application.ConfigService, observer application.ConfigCommitObserver, operationID string, allowed []string, capacity int64) error {
	return p.CommitSource(ctx, service, observer, operationID, nil, allowed, capacity)
}

// CommitSource atomically adds one source while retaining existing active and
// draining sources. The caller supplies the current private runtime sources;
// both their public descriptions and private credentials are compared against
// the workspace under the transaction lock. A commit is not a deployment.
func (p PreparedSource) CommitSource(ctx context.Context, service application.ConfigService, observer application.ConfigCommitObserver, operationID string, existing []ingress.RuntimeSource, allowed []string, capacity int64) error {
	return p.CommitSourceWithDraining(ctx, service, observer, operationID, existing, nil, allowed, capacity)
}

// CommitSourceWithDraining explicitly marks selected existing active sources
// as draining in the same configuration transaction. It never withdraws routes
// itself; the existing runtime cutover checks remain responsible for that.
func (p PreparedSource) CommitSourceWithDraining(ctx context.Context, service application.ConfigService, observer application.ConfigCommitObserver, operationID string, existing []ingress.RuntimeSource, drainIDs []string, allowed []string, capacity int64) error {
	if service == nil || observer == nil || operationID == "" {
		return ErrJournal
	}
	planned, err := planSourceDrains(existing, drainIDs, p.spec.Domains)
	if err != nil {
		return err
	}
	merged, err := p.Merge(planned, allowed, capacity)
	if err != nil {
		return err
	}
	return p.commitMerged(ctx, service, observer, operationID, existing, merged, capacity)
}

// planSourceDrains validates explicit replacement without mutating the caller.
// It is also used before provisioning to reject a known incompatible plan.
func planSourceDrains(existing []ingress.RuntimeSource, drainIDs, newDomains []string) ([]ingress.RuntimeSource, error) {
	if len(drainIDs) > 8 {
		return nil, ErrConflict
	}
	planned := append([]ingress.RuntimeSource(nil), existing...)
	seen := map[string]bool{}
	for _, id := range drainIDs {
		if id == "" || seen[id] {
			return nil, ErrConflict
		}
		seen[id] = true
		found := false
		for i := range planned {
			if planned[i].ID != id {
				continue
			}
			if found || planned[i].Mode != "active" {
				return nil, ErrConflict
			}
			if len(planned[i].Domains) == 0 {
				return nil, ErrConflict
			}
			for _, oldDomain := range planned[i].Domains {
				covered := false
				for _, newDomain := range newDomains {
					if oldDomain == newDomain {
						covered = true
					}
				}
				if !covered {
					return nil, ErrConflict
				}
			}
			planned[i].Mode = "draining"
			found = true
		}
		if !found {
			return nil, ErrConflict
		}
	}
	return planned, nil
}

func (p PreparedSource) commitMerged(ctx context.Context, service application.ConfigService, observer application.ConfigCommitObserver, operationID string, existing []ingress.RuntimeSource, merged PreparedConfiguration, capacity int64) error {
	snapshot, err := service.GetConfig(ctx)
	if err != nil {
		return ErrUnknown
	}
	if !snapshot.Managed || snapshot.Validator == "" {
		return ErrConflict
	}
	fields := map[string]application.ConfigField{}
	for _, field := range snapshot.Fields {
		if field.Path == "mail.ingress_sources" || field.Path == "mail.ingress_capacity_bytes" || field.Path == "mail.ingress_credentials" {
			if _, duplicate := fields[field.Path]; duplicate {
				return ErrConflict
			}
			fields[field.Path] = field
		}
	}
	public, ok := fields["mail.ingress_sources"]
	limit, limitOK := fields["mail.ingress_capacity_bytes"]
	private, privateOK := fields["mail.ingress_credentials"]
	if !ok || !limitOK || !privateOK || !public.Editable || public.Sensitive || !limit.Editable || limit.Sensitive || !private.Editable || !private.Sensitive || !private.StoreInput || private.SensitiveState != "unset" && private.SensitiveState != "set" {
		return ErrConflict
	}
	raw, err := json.Marshal(snapshot.Config)
	if err != nil || len(raw) > 4<<20 {
		return ErrJournal
	}
	var document application.ConfigDocument
	if json.Unmarshal(raw, &document) != nil {
		return ErrJournal
	}
	if env, ok := document["env"].(map[string]any); ok {
		for _, key := range []string{"MAIL_INGRESS_SOURCES", "MAIL_INGRESS_CAPACITY_BYTES", "MAIL_INGRESS_CREDENTIALS"} {
			if _, exists := env[key]; exists {
				return ErrConflict
			}
		}
	}
	modules, ok := document["modules"].(map[string]any)
	if !ok {
		return ErrConflict
	}
	module, ok := modules["mail"].(map[string]any)
	if !ok {
		return ErrConflict
	}
	values, ok := module["config"].(map[string]any)
	if !ok {
		if module["config"] != nil {
			return ErrConflict
		}
		values = map[string]any{}
		module["config"] = values
	}
	currentSpecs := []ingress.SourceSpec{}
	if current, present := values["ingress_sources"]; present {
		text, ok := current.(string)
		if !ok {
			return ErrConflict
		}
		if strings.TrimSpace(text) != "" {
			decoder := json.NewDecoder(strings.NewReader(text))
			decoder.DisallowUnknownFields()
			var extra any
			if decoder.Decode(&currentSpecs) != nil || decoder.Decode(&extra) != io.EOF || currentSpecs == nil {
				return ErrConflict
			}
		}
	}
	if (len(currentSpecs) != 0) != (private.SensitiveState == "set") {
		return ErrConflict
	}
	desiredSpecs := merged.Specifications()
	alreadyPresent := len(currentSpecs) > 0 && reflect.DeepEqual(currentSpecs, desiredSpecs) && values["ingress_capacity_bytes"] == float64(capacity)
	if !alreadyPresent {
		expectedSpecs := make([]ingress.SourceSpec, len(existing))
		for i := range existing {
			expectedSpecs[i] = existing[i].SourceSpec
		}
		if !reflect.DeepEqual(currentSpecs, expectedSpecs) {
			return ErrConflict
		}
	}
	descriptions, err := json.Marshal(desiredSpecs)
	if err != nil {
		return ErrJournal
	}
	var credentials bytes.Buffer
	if err := merged.WriteCredentials(&credentials); err != nil {
		return ErrJournal
	}
	secret := strings.TrimSuffix(credentials.String(), "\n")
	values["ingress_sources"], values["ingress_capacity_bytes"] = string(descriptions), capacity
	candidate := application.ConfigCandidate{Document: document, Sensitive: map[string]application.ConfigSensitiveMutation{"mail.ingress_credentials": {Operation: application.ConfigSensitiveSet, Value: &secret}}}
	if alreadyPresent {
		validation, e := service.ValidateConfig(ctx, candidate)
		if e != nil {
			return ErrUnknown
		}
		if validation.BaseValidator != snapshot.Validator || len(validation.Changes) != 0 {
			return ErrConflict
		}
		return nil
	}
	var expectedSecret *string
	if len(existing) > 0 {
		oldCredentials := make(map[string]ingress.SourceCredentials, len(existing))
		for _, source := range existing {
			oldCredentials[source.ID] = source.Credentials
		}
		oldRaw, e := json.Marshal(oldCredentials)
		if e != nil {
			return ErrJournal
		}
		old := string(oldRaw)
		expectedSecret = &old
	}
	_, err = service.PutConfig(ctx, application.ConfigPutRequest{OperationID: operationID, Precondition: application.ConfigPreconditionMatch, ExpectedValidator: snapshot.Validator, Candidate: candidate, ExpectedStoredInputs: map[string]*string{"mail.ingress_credentials": expectedSecret}}, observer)
	if err != nil {
		if e, ok := application.ErrorOf(err); ok && e.Code == "config_precondition_failed" {
			return ErrConflict
		}
		return ErrUnknown
	}
	return nil
}
