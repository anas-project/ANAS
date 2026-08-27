package runner

import (
	"fmt"
	"sort"
	"time"
)

const credentialBarrierProbeAttempts = 20

var credentialBarrierRetryPause = func() { time.Sleep(250 * time.Millisecond) }

func credentialStoreBlocker(store *secretStore, credential deploymentCredential) string {
	if store == nil {
		return "Secret Store is unavailable"
	}
	metadata, present := store.metadata[credential.SecretKey]
	if !present || store.values[credential.SecretKey] == "" {
		return "Secret Store record is missing"
	}
	if metadata.Generation != credential.Generation {
		return "Secret Store generation differs from the deployment"
	}
	if credential.Authority == "anas" &&
		(metadata.Owner != credential.Owner || metadata.Kind != "generated" || metadata.Provenance != "module-hook") {
		return "Secret Store authority provenance differs from the deployment"
	}
	return ""
}

func deploymentCredentialStoreFindings(base string, manifest *deploymentManifest) ([]credentialPlanFinding, error) {
	store, err := loadSecretStore(base)
	if err != nil {
		return nil, err
	}
	findings := []credentialPlanFinding{}
	if manifest != nil {
		for _, credential := range manifest.Credentials {
			if reason := credentialStoreBlocker(store, credential); reason != "" {
				findings = append(findings, credentialPlanFinding{ID: credential.ID, Reason: reason})
			}
		}
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].ID < findings[j].ID })
	return findings, nil
}

func credentialStoreConsistencyError(base string, manifest *deploymentManifest) error {
	findings, err := deploymentCredentialStoreFindings(base, manifest)
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if len(findings) == 0 {
		return nil
	}
	return &CLIError{
		Code:    "credential_store_mismatch",
		Message: "deployment credential generations or authority do not match the Secret Store; restore a matching snapshot before activation",
		Detail:  map[string]any{"credentials": findings}, Exit: exitPrecondition,
	}
}

// coordinateModuleCredentials is the credential portion of the Module ready
// barrier. It is run only after the authority service has started and must
// complete before after_start/local-account work and downstream activation.
func (a *app) coordinateModuleCredentials(mod Module, workdir string, env map[string]string) error {
	owned := map[string]deploymentCredential{}
	for _, credential := range a.credentials {
		if credential.Owner == mod.Name &&
			(credential.RotationMode == "reconcile" || credential.RotationMode == "overlap") {
			owned[credential.ID] = credential
		}
	}
	if len(owned) == 0 {
		return nil
	}
	order, err := credentialControlOrder(owned)
	if err != nil {
		return err
	}
	for _, id := range order {
		credential := owned[id]
		probe, err := a.runCredentialHookEventually(mod, "credential_probe", workdir, env, credential)
		if err != nil {
			return err
		}
		if probe.Changed {
			return fmt.Errorf("credential %s probe reported a mutation", id)
		}
		switch probe.Status {
		case "match":
			// Already converged. Verification below remains mandatory.
		case "missing", "mismatch":
			if credential.Authority != "anas" {
				return fmt.Errorf("credential %s is %s but authority is external; manual action is required", id, probe.Status)
			}
			reconciled, err := a.runCredentialHook(mod, "credential_reconcile", workdir, env, credential)
			if err != nil {
				return err
			}
			if reconciled.Status != "reconciled" && reconciled.Status != "match" {
				return fmt.Errorf("credential %s reconcile ended with status %s", id, reconciled.Status)
			}
			if reconciled.Status == "reconciled" && !reconciled.Changed {
				return fmt.Errorf("credential %s reconcile reported reconciled without a mutation", id)
			}
			if reconciled.Status == "match" && reconciled.Changed {
				return fmt.Errorf("credential %s reconcile reported a mutation for an already matching value", id)
			}
		case "unavailable":
			return fmt.Errorf("credential %s authority service is unavailable", id)
		case "unsupported":
			return fmt.Errorf("credential %s lifecycle is unsupported", id)
		default:
			return fmt.Errorf("credential %s probe returned invalid status %s", id, probe.Status)
		}

		verified, err := a.runCredentialHookEventually(mod, "credential_verify", workdir, env, credential)
		if err != nil {
			return err
		}
		if verified.Status != "match" || verified.Changed {
			return fmt.Errorf("credential %s verification ended with status %s", id, verified.Status)
		}
	}
	return nil
}

// Compose up is asynchronous. A provider may be running while its entrypoint
// has not yet materialized the credential authority/config. Give that startup
// window a bounded retry before treating missing as drift or unavailable as a
// hard ready-barrier failure.
func (a *app) runCredentialHookEventually(mod Module, phase, workdir string, env map[string]string, credential deploymentCredential) (credentialHookResult, error) {
	var result credentialHookResult
	for attempt := 0; attempt < credentialBarrierProbeAttempts; attempt++ {
		var err error
		result, err = a.runCredentialHook(mod, phase, workdir, env, credential)
		if err != nil {
			return credentialHookResult{}, err
		}
		if result.Status != "missing" && result.Status != "unavailable" {
			return result, nil
		}
		if attempt+1 < credentialBarrierProbeAttempts {
			credentialBarrierRetryPause()
		}
	}
	return result, nil
}
