package runner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
)

type credentialRotationResult struct {
	TransactionID       string   `json:"transaction_id"`
	PreviousDeployment  string   `json:"previous_deployment"`
	CandidateDeployment string   `json:"candidate_deployment"`
	Credentials         []string `json:"credentials"`
	AffectedModules     []string `json:"affected_modules"`
	Status              string   `json:"status"`
}

type credentialRotationExecutionError struct {
	Status string
	Cause  error
}

func (e *credentialRotationExecutionError) Error() string { return e.Cause.Error() }
func (e *credentialRotationExecutionError) Unwrap() error { return e.Cause }

func credentialRotationFailure(status string, cause error) error {
	return &credentialRotationExecutionError{Status: status, Cause: cause}
}

func credentialRotationFailureStatus(err error) string {
	var failure *credentialRotationExecutionError
	if errors.As(err, &failure) {
		return failure.Status
	}
	return "not_started"
}

func executeCredentialRotation(base string, cli compose.CLI, manifest *deploymentManifest, plan credentialRotationPlan, all, force, jsonMode bool) (credentialRotationResult, error) {
	result := credentialRotationResult{PreviousDeployment: manifest.ID, Credentials: append([]string{}, plan.CredentialOrder...), AffectedModules: append([]string{}, plan.ActivationOrder...), Status: "not_started"}
	fail := func(err error) (credentialRotationResult, error) {
		result.Status = credentialRotationFailureStatus(err)
		return result, err
	}
	active, err := loadActiveState(base)
	if err != nil {
		return fail(err)
	}
	if active.ActiveDeployment != manifest.ID {
		return fail(fmt.Errorf("active deployment changed from %s to %s", manifest.ID, active.ActiveDeployment))
	}
	if active.RuntimeStatus != "running" {
		return fail(fmt.Errorf("active deployment runtime is not running"))
	}
	selected, err := credentialsForRotationPlan(manifest, plan)
	if err != nil {
		return fail(err)
	}
	store, err := loadSecretStore(base)
	if err != nil {
		return fail(err)
	}
	for _, credential := range selected {
		if reason := credentialStoreBlocker(store, credential); reason != "" {
			return fail(fmt.Errorf("credential %s: %s", credential.ID, reason))
		}
	}
	candidateValues, storeCandidates, err := generateCredentialRotationCandidates(selected, store, force)
	if err != nil {
		return fail(err)
	}
	// Candidate values are generated before the first durable side effect. A
	// dry-run never reaches this function.
	txn, err := beginCredentialRotationTransaction(base, manifest.ID, selected)
	if err != nil {
		return fail(err)
	}
	result.TransactionID = txn.ID
	result.CandidateDeployment = txn.CandidateDeployment
	txn.All = all
	txn.AffectedModules = append([]string{}, plan.ActivationOrder...)
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(abandonCredentialRotation(base, txn, err))
	}
	active.Transaction = txn.ID
	if err := saveActiveState(base, active); err != nil {
		return fail(abandonCredentialRotation(base, txn, err))
	}
	candidate, err := materializeCredentialCandidate(base, txn, candidateValues)
	if err != nil {
		return fail(abandonCredentialRotation(base, txn, fmt.Errorf("candidate creation failed: %w", err)))
	}
	previousApp, previousRoot, _, err := loadDeploymentApp(base, manifest.ID, cli)
	if err != nil {
		return fail(abandonCredentialRotation(base, txn, err))
	}
	previousApp.suppressSensitiveOutput = true
	candidateApp, candidateRoot, _, err := loadDeploymentApp(base, candidate.ID, cli)
	if err != nil {
		return fail(abandonCredentialRotation(base, txn, err))
	}
	candidateApp.suppressSensitiveOutput = true

	// Persist intent before the first container is stopped. A crash midway is
	// therefore recovered as a conservative previous-deployment restore.
	txn.Phase = credentialPhasePreviousStopped
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(abandonCredentialRotation(base, txn, err))
	}
	if err := stopCredentialModules(previousApp, previousRoot, txn, jsonMode); err != nil {
		return fail(compensateCredentialRotation(base, txn, candidateApp, candidateRoot, previousApp, previousRoot, err, jsonMode))
	}
	txn.Phase = credentialPhaseActivating
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(compensateCredentialRotation(base, txn, candidateApp, candidateRoot, previousApp, previousRoot, err, jsonMode))
	}
	if err := activateCredentialCandidate(base, txn, candidateApp, candidateRoot, jsonMode); err != nil {
		return fail(compensateCredentialRotation(base, txn, candidateApp, candidateRoot, previousApp, previousRoot, err, jsonMode))
	}
	txn.Phase = credentialPhaseVerified
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(compensateCredentialRotation(base, txn, candidateApp, candidateRoot, previousApp, previousRoot, err, jsonMode))
	}
	if err := commitCredentialStoreRotation(store, txn.ID, storeCandidates); err != nil {
		return fail(compensateCredentialRotation(base, txn, candidateApp, candidateRoot, previousApp, previousRoot, err, jsonMode))
	}
	txn.StoreCommitted = true
	txn.Phase = credentialPhaseStoreCommitted
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(credentialRotationFailure("recovery_required", fmt.Errorf("credential values committed; recovery must finish candidate promotion: %w", err)))
	}
	if err := promoteCredentialCandidate(base, txn); err != nil {
		txn.Phase = credentialPhaseRecoveryRequired
		txn.RecoveryStatus = "store committed; candidate promotion pending"
		_ = saveCredentialRotationTransaction(base, txn)
		return fail(credentialRotationFailure("recovery_required", fmt.Errorf("credential values committed; recovery must finish candidate promotion: %w", err)))
	}
	txn.CandidatePromoted = true
	txn.Phase = credentialPhasePromoted
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return fail(credentialRotationFailure("recovery_required", err))
	}
	if err := completeCredentialRotation(base, txn); err != nil {
		return fail(credentialRotationFailure("recovery_required", err))
	}
	result.Status = "complete"
	return result, nil
}

func credentialsForRotationPlan(manifest *deploymentManifest, plan credentialRotationPlan) ([]deploymentCredential, error) {
	byID := map[string]deploymentCredential{}
	for _, credential := range manifest.Credentials {
		byID[credential.ID] = credential
	}
	out := make([]deploymentCredential, 0, len(plan.CredentialOrder))
	for _, id := range plan.CredentialOrder {
		credential, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("credential %s disappeared from the active deployment", id)
		}
		out = append(out, credential)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("credential rotation has no executable targets")
	}
	return out, nil
}

func generateCredentialRotationCandidates(selected []deploymentCredential, store *secretStore, force bool) (map[string]credentialCandidateValue, map[string]credentialStoreCandidate, error) {
	values := map[string]credentialCandidateValue{}
	storeValues := map[string]credentialStoreCandidate{}
	for _, credential := range selected {
		value, err := generateCredentialValue(credential)
		if err != nil {
			return nil, nil, err
		}
		candidate := credentialCandidateValue{Value: value}
		metadata := store.metadata[credential.SecretKey]
		if credential.Authority != "anas" {
			if !force {
				return nil, nil, fmt.Errorf("credential %s authority is external", credential.ID)
			}
			candidate.Authority = "anas"
			metadata.Owner = credential.Owner
			metadata.Kind = "generated"
			metadata.Provenance = "module-hook"
		}
		metadata.Generation = credential.Generation + 1
		metadata.RotationID = ""
		values[credential.ID] = candidate
		storeValues[credential.SecretKey] = credentialStoreCandidate{Value: value, Metadata: metadata}
	}
	return values, storeValues, nil
}

func generateCredentialValue(credential deploymentCredential) (string, error) {
	switch credential.Generator.Kind {
	case "password":
		return randomPassword(credential.Generator.Length)
	case "hex":
		body := make([]byte, credential.Generator.Length)
		if _, err := rand.Read(body); err != nil {
			return "", fmt.Errorf("generate credential %s: %w", credential.ID, err)
		}
		return hex.EncodeToString(body), nil
	default:
		return "", fmt.Errorf("credential %s has unsupported generator %q", credential.ID, credential.Generator.Kind)
	}
}

func stopCredentialModules(a *app, root string, txn *credentialRotationTransaction, jsonMode bool) error {
	if txn.All {
		return a.stopRelease(root, jsonMode)
	}
	return a.stopModules(root, txn.AffectedModules, jsonMode)
}

func activateCredentialCandidate(base string, txn *credentialRotationTransaction, candidate *app, root string, jsonMode bool) error {
	txn.CompletedModules = nil
	txn.FailedModule = ""
	for _, module := range txn.AffectedModules {
		if err := startDeployment(candidate, root, []string{module}, jsonMode); err != nil {
			txn.FailedModule = module
			_ = saveCredentialRotationTransaction(base, txn)
			return err
		}
		txn.CompletedModules = append(txn.CompletedModules, module)
		if err := saveCredentialRotationTransaction(base, txn); err != nil {
			return err
		}
	}
	return nil
}

func compensateCredentialRotation(base string, txn *credentialRotationTransaction, candidate *app, candidateRoot string, previous *app, previousRoot string, cause error, jsonMode bool) error {
	txn.Phase = credentialPhaseRestoring
	txn.RecoveryStatus = "restoring previous deployment"
	_ = saveCredentialRotationTransaction(base, txn)
	parts := []string{cause.Error()}
	candidateStopped := true
	if candidate != nil {
		if err := stopCredentialModules(candidate, candidateRoot, txn, jsonMode); err != nil {
			candidateStopped = false
			parts = append(parts, "candidate stop failed: "+err.Error())
		}
	}
	previousRestored := false
	if previous == nil {
		parts = append(parts, "previous deployment restore failed: deployment unavailable")
	} else if err := startDeployment(previous, previousRoot, txn.AffectedModules, jsonMode); err != nil {
		parts = append(parts, "previous deployment restore failed: "+err.Error())
	} else {
		previousRestored = true
		parts = append(parts, "previous deployment restored")
		txn.Phase = credentialPhasePreviousRestored
		txn.RecoveryStatus = "previous deployment restored; Store unchanged"
		if err := saveCredentialRotationTransaction(base, txn); err == nil {
			_ = markCredentialCandidateFailed(base, txn.CandidateDeployment, cause)
			if err := completeCredentialRotation(base, txn); err == nil && candidateStopped {
				return credentialRotationFailure("previous_restored", fmt.Errorf("%s", strings.Join(parts, "; ")))
			}
		}
	}
	txn.Phase = credentialPhaseRecoveryRequired
	txn.RecoveryStatus = strings.Join(parts, "; ")
	_ = saveCredentialRotationTransaction(base, txn)
	if previousRestored {
		return credentialRotationFailure("recovery_required", fmt.Errorf("%s", strings.Join(parts, "; ")))
	}
	return credentialRotationFailure("previous_restore_failed", fmt.Errorf("%s", strings.Join(parts, "; ")))
}

func abandonCredentialRotation(base string, txn *credentialRotationTransaction, cause error) error {
	_ = markCredentialCandidateFailed(base, txn.CandidateDeployment, cause)
	txn.RecoveryStatus = "candidate failed before activation; previous and Store unchanged"
	if err := completeCredentialRotation(base, txn); err != nil {
		return credentialRotationFailure("recovery_required", fmt.Errorf("%v; close credential transaction: %w", cause, err))
	}
	return credentialRotationFailure("candidate_failed", cause)
}

func markCredentialCandidateFailed(base, id string, cause error) error {
	if !exists(filepath.Join(base, "deployments", id)) {
		return nil
	}
	state, _ := loadDeploymentState(base, id)
	state.Status = "failed"
	state.Failure = cause.Error()
	return saveDeploymentState(base, state)
}

func completeCredentialRotation(base string, txn *credentialRotationTransaction) error {
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	if active.Transaction == txn.ID {
		active.Transaction = ""
		if err := saveActiveState(base, active); err != nil {
			return err
		}
	}
	txn.Phase = credentialPhaseComplete
	if txn.RecoveryStatus == "" {
		txn.RecoveryStatus = "complete"
	}
	return saveCredentialRotationTransaction(base, txn)
}

func promoteCredentialCandidate(base string, txn *credentialRotationTransaction) error {
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	if active.ActiveDeployment != txn.PreviousDeployment && active.ActiveDeployment != txn.CandidateDeployment {
		return fmt.Errorf("active deployment changed to %s during credential rotation", active.ActiveDeployment)
	}
	now := nowUTC()
	if active.ActiveDeployment != txn.CandidateDeployment {
		previous := removeString(active.PreviousDeployments, txn.CandidateDeployment)
		previous = removeString(previous, txn.PreviousDeployment)
		previous = append([]string{txn.PreviousDeployment}, previous...)
		oldState, _ := loadDeploymentState(base, txn.PreviousDeployment)
		oldState.Status = "previous"
		oldState.DeactivatedAt = now
		if err := saveDeploymentState(base, oldState); err != nil {
			return err
		}
		active.ActiveDeployment = txn.CandidateDeployment
		active.PreviousDeployments = previous
	}
	active.RuntimeStatus = "running"
	active.ActivatedAt = now
	active.VerifiedAt = now
	active.Transaction = ""
	if err := saveActiveState(base, active); err != nil {
		return err
	}
	state, _ := loadDeploymentState(base, txn.CandidateDeployment)
	state.Status = "active"
	state.ActivatedAt = now
	state.VerifiedAt = now
	state.Predecessor = txn.PreviousDeployment
	return saveDeploymentState(base, state)
}

// recoverCredentialRotation is idempotent and runs under the workspace's
// exclusive lock. Before Store commit it restores previous; after an atomic
// Store commit it finishes candidate activation and promotion.
func recoverCredentialRotation(base string, txn *credentialRotationTransaction, jsonMode bool) error {
	return recoverCredentialRotationUsing(base, txn, nil, jsonMode)
}

func recoverCredentialRotationUsing(base string, txn *credentialRotationTransaction, suppliedCLI *compose.CLI, jsonMode bool) error {
	if txn == nil || txn.Phase == credentialPhaseComplete {
		return nil
	}
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	if (txn.Phase == credentialPhasePlanned || txn.Phase == credentialPhaseCandidateCreated) && active.ActiveDeployment == txn.PreviousDeployment {
		_ = markCredentialCandidateFailed(base, txn.CandidateDeployment, fmt.Errorf("credential rotation abandoned before activation"))
		txn.RecoveryStatus = "abandoned before activation; previous and Store unchanged"
		return completeCredentialRotation(base, txn)
	}
	previousRoot := filepath.Join(base, "deployments", txn.PreviousDeployment)
	previous, err := loadDeploymentManifest(previousRoot)
	if err != nil {
		return err
	}
	if len(txn.AffectedModules) == 0 {
		ids := make([]string, 0, len(txn.Targets))
		for _, target := range txn.Targets {
			ids = append(ids, target.ID)
		}
		plan := planCredentialRotation(previous, ids, false, true)
		if len(plan.Blockers) > 0 {
			return fmt.Errorf("rebuild credential recovery plan: %s", plan.Blockers[0].Reason)
		}
		txn.AffectedModules = append([]string{}, plan.ActivationOrder...)
	}
	candidateRoot := filepath.Join(base, "deployments", txn.CandidateDeployment)
	candidate, candidateErr := loadDeploymentManifest(candidateRoot)
	store, storeErr := loadSecretStore(base)
	if storeErr != nil {
		return storeErr
	}
	committed := candidateErr == nil && credentialStoreReflectsTransaction(store, candidate, txn)
	if txn.StoreCommitted && !committed {
		return fmt.Errorf("credential transaction %s says Store committed but Store generations do not match", txn.ID)
	}
	var cli compose.CLI
	if suppliedCLI == nil {
		cli, err = compose.Detect()
		if err != nil {
			return err
		}
	} else {
		cli = *suppliedCLI
	}
	previousApp, previousModules, _, err := loadDeploymentApp(base, txn.PreviousDeployment, cli)
	if err != nil {
		return err
	}
	previousApp.suppressSensitiveOutput = true
	if committed {
		candidateApp, candidateModules, _, err := loadDeploymentApp(base, txn.CandidateDeployment, cli)
		if err != nil {
			return err
		}
		candidateApp.suppressSensitiveOutput = true
		if err := startDeployment(candidateApp, candidateModules, txn.AffectedModules, jsonMode); err != nil {
			return err
		}
		txn.StoreCommitted = true
		txn.Phase = credentialPhaseStoreCommitted
		if err := saveCredentialRotationTransaction(base, txn); err != nil {
			return err
		}
		if err := promoteCredentialCandidate(base, txn); err != nil {
			return err
		}
		txn.CandidatePromoted = true
		txn.RecoveryStatus = "candidate promoted from committed Store"
		return completeCredentialRotation(base, txn)
	}
	var candidateApp *app
	var candidateModules string
	if candidateErr == nil {
		candidateApp, candidateModules, _, _ = loadDeploymentApp(base, txn.CandidateDeployment, cli)
		if candidateApp != nil {
			candidateApp.suppressSensitiveOutput = true
		}
	}
	recoveryErr := compensateCredentialRotation(base, txn, candidateApp, candidateModules, previousApp, previousModules,
		fmt.Errorf("recover interrupted credential rotation before Store commit"), jsonMode)
	if credentialRotationFailureStatus(recoveryErr) == "previous_restored" {
		return nil
	}
	return recoveryErr
}

func credentialStoreReflectsTransaction(store *secretStore, candidate *deploymentManifest, txn *credentialRotationTransaction) bool {
	if store == nil || candidate == nil {
		return false
	}
	byID := map[string]deploymentCredential{}
	for _, credential := range candidate.Credentials {
		byID[credential.ID] = credential
	}
	for _, target := range txn.Targets {
		credential, ok := byID[target.ID]
		if !ok || credential.Generation != target.ToGeneration {
			return false
		}
		metadata := store.metadata[credential.SecretKey]
		if store.values[credential.SecretKey] == "" || metadata.Generation != target.ToGeneration || metadata.RotationID != txn.ID {
			return false
		}
	}
	return len(txn.Targets) > 0
}

func credentialTransactionTargets(txn *credentialRotationTransaction) []string {
	ids := make([]string, 0, len(txn.Targets))
	for _, target := range txn.Targets {
		ids = append(ids, target.ID)
	}
	sort.Strings(ids)
	return ids
}
