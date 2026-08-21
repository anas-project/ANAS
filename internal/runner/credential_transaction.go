package runner

// The credential transaction is the durable boundary shared by the CLI,
// executor, and crash recovery. It never records a value, hash, or verifier.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	credentialTransactionAPIVersion = "anas.credential-transaction/v1"
	credentialTransactionKind       = "credential_rotation"

	credentialPhasePlanned          = "planned"
	credentialPhaseCandidateCreated = "candidate_created"
	credentialPhasePreviousStopped  = "previous_stopped"
	credentialPhaseActivating       = "candidate_activating"
	credentialPhaseVerified         = "candidate_verified"
	credentialPhaseStoreCommitted   = "store_committed"
	credentialPhasePromoted         = "candidate_promoted"
	credentialPhaseRestoring        = "restoring_previous"
	credentialPhasePreviousRestored = "previous_restored"
	credentialPhaseRecoveryRequired = "recovery_required"
	credentialPhaseComplete         = "complete"
)

var credentialTransactionPhases = map[string]bool{
	credentialPhasePlanned: true, credentialPhaseCandidateCreated: true,
	credentialPhasePreviousStopped: true, credentialPhaseActivating: true,
	credentialPhaseVerified: true, credentialPhaseStoreCommitted: true,
	credentialPhasePromoted: true, credentialPhaseRestoring: true,
	credentialPhasePreviousRestored: true, credentialPhaseRecoveryRequired: true,
	credentialPhaseComplete: true,
}

type credentialTransactionTarget struct {
	ID             string `yaml:"id" json:"id"`
	FromGeneration uint64 `yaml:"from_generation" json:"from_generation"`
	ToGeneration   uint64 `yaml:"to_generation" json:"to_generation"`
}

// credentialRotationTransaction contains operational identities only. It must
// remain safe to persist even if a Hook or application rejects a candidate.
type credentialRotationTransaction struct {
	APIVersion          string                        `yaml:"api_version" json:"api_version"`
	ID                  string                        `yaml:"id" json:"id"`
	Kind                string                        `yaml:"kind" json:"kind"`
	PreviousDeployment  string                        `yaml:"previous_deployment" json:"previous_deployment"`
	CandidateDeployment string                        `yaml:"candidate_deployment" json:"candidate_deployment"`
	Targets             []credentialTransactionTarget `yaml:"targets" json:"targets"`
	All                 bool                          `yaml:"all" json:"all"`
	AffectedModules     []string                      `yaml:"affected_modules,omitempty" json:"affected_modules,omitempty"`
	Phase               string                        `yaml:"phase" json:"phase"`
	CompletedModules    []string                      `yaml:"completed_modules,omitempty" json:"completed_modules,omitempty"`
	FailedModule        string                        `yaml:"failed_module,omitempty" json:"failed_module,omitempty"`
	StoreCommitted      bool                          `yaml:"store_committed" json:"store_committed"`
	CandidatePromoted   bool                          `yaml:"candidate_promoted" json:"candidate_promoted"`
	RecoveryStatus      string                        `yaml:"recovery_status,omitempty" json:"recovery_status,omitempty"`
	StartedAt           string                        `yaml:"started_at" json:"started_at"`
	UpdatedAt           string                        `yaml:"updated_at" json:"updated_at"`
	UID                 int                           `yaml:"uid" json:"uid"`
}

type credentialCandidateValue struct {
	Value     string
	Authority string
}

type credentialStoreCandidate struct {
	Value    string
	Metadata secretMetadata
}

func credentialTransactionJournalPath(base, id string) string {
	return transactionPath(base, id)
}

func beginCredentialRotationTransaction(base, previous string, credentials []deploymentCredential) (*credentialRotationTransaction, error) {
	if err := validateDeploymentID(previous); err != nil {
		return nil, err
	}
	if existing, err := unfinishedCredentialRotation(base); err != nil {
		return nil, err
	} else if existing != nil {
		return nil, credentialRecoveryRequiredError(existing)
	}
	active, err := loadActiveState(base)
	if err != nil {
		return nil, err
	}
	if active.ActiveDeployment != previous {
		return nil, fmt.Errorf("active deployment changed from %s to %s", previous, active.ActiveDeployment)
	}
	candidateID, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	journalSuffix, err := newDeploymentID()
	if err != nil {
		return nil, err
	}
	targets := make([]credentialTransactionTarget, 0, len(credentials))
	seen := map[string]bool{}
	for _, credential := range credentials {
		if strings.TrimSpace(credential.ID) == "" || seen[credential.ID] {
			return nil, fmt.Errorf("credential transaction target %q is empty or duplicated", credential.ID)
		}
		seen[credential.ID] = true
		targets = append(targets, credentialTransactionTarget{
			ID: credential.ID, FromGeneration: credential.Generation, ToGeneration: credential.Generation + 1,
		})
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("credential transaction has no targets")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ID < targets[j].ID })
	now := nowUTC()
	txn := &credentialRotationTransaction{
		APIVersion: credentialTransactionAPIVersion,
		ID:         "credential-" + journalSuffix, Kind: credentialTransactionKind,
		PreviousDeployment: previous, CandidateDeployment: candidateID,
		Targets: targets, Phase: credentialPhasePlanned,
		StartedAt: now, UpdatedAt: now, UID: os.Geteuid(),
	}
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return nil, err
	}
	return txn, nil
}

func saveCredentialRotationTransaction(base string, txn *credentialRotationTransaction) error {
	if err := validateCredentialRotationTransaction(txn); err != nil {
		return err
	}
	txn.UpdatedAt = nowUTC()
	return writeYAMLAtomic(credentialTransactionJournalPath(base, txn.ID), txn, 0600)
}

func validateCredentialRotationTransaction(txn *credentialRotationTransaction) error {
	if txn == nil {
		return fmt.Errorf("credential transaction is nil")
	}
	if txn.APIVersion != credentialTransactionAPIVersion || txn.Kind != credentialTransactionKind {
		return fmt.Errorf("unsupported credential transaction schema")
	}
	if !strings.HasPrefix(txn.ID, "credential-") || strings.ContainsAny(txn.ID, `/\\`) {
		return fmt.Errorf("invalid credential transaction id %q", txn.ID)
	}
	if err := validateDeploymentID(txn.PreviousDeployment); err != nil {
		return err
	}
	if err := validateDeploymentID(txn.CandidateDeployment); err != nil {
		return err
	}
	if !credentialTransactionPhases[txn.Phase] {
		return fmt.Errorf("unsupported credential transaction phase %q", txn.Phase)
	}
	if len(txn.Targets) == 0 {
		return fmt.Errorf("credential transaction has no targets")
	}
	seen := map[string]bool{}
	for _, target := range txn.Targets {
		if target.ID == "" || seen[target.ID] || target.ToGeneration != target.FromGeneration+1 {
			return fmt.Errorf("invalid credential transaction target %q", target.ID)
		}
		seen[target.ID] = true
	}
	for label, modules := range map[string][]string{
		"affected_modules":  txn.AffectedModules,
		"completed_modules": txn.CompletedModules,
	} {
		seenModules := map[string]bool{}
		for _, module := range modules {
			if module == "" || filepath.Base(module) != module || strings.ContainsAny(module, `/\\`) || seenModules[module] {
				return fmt.Errorf("invalid credential transaction %s entry %q", label, module)
			}
			seenModules[module] = true
		}
	}
	affected := map[string]bool{}
	for _, module := range txn.AffectedModules {
		affected[module] = true
	}
	for _, module := range txn.CompletedModules {
		if !affected[module] {
			return fmt.Errorf("credential transaction completed module %q is not affected", module)
		}
	}
	if txn.FailedModule != "" && !affected[txn.FailedModule] {
		return fmt.Errorf("credential transaction failed module %q is not affected", txn.FailedModule)
	}
	return nil
}

func unfinishedCredentialRotation(base string) (*credentialRotationTransaction, error) {
	entries, err := os.ReadDir(transactionsDir(base))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var unfinished *credentialRotationTransaction
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "credential-") || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		path := filepath.Join(transactionsDir(base), entry.Name())
		var txn credentialRotationTransaction
		if err := readYAML(path, &txn); err != nil {
			return nil, fmt.Errorf("read credential transaction journal %s: %w", path, err)
		}
		if err := validateCredentialRotationTransaction(&txn); err != nil {
			return nil, fmt.Errorf("validate credential transaction journal %s: %w", path, err)
		}
		if txn.Phase != credentialPhaseComplete {
			if unfinished != nil {
				return nil, fmt.Errorf("multiple unfinished credential transactions %s and %s require manual recovery", unfinished.ID, txn.ID)
			}
			copy := txn
			unfinished = &copy
		}
	}
	return unfinished, nil
}

func credentialRecoveryRequiredError(txn *credentialRotationTransaction) error {
	return fmt.Errorf("credential rotation %s requires recovery at phase %s before another write operation", txn.ID, txn.Phase)
}

// materializeCredentialCandidate copies the effective previous deployment into
// a new immutable deployment and changes only the selected projections. The
// previous artifact, active pointer, and Secret Store remain untouched.
func materializeCredentialCandidate(base string, txn *credentialRotationTransaction, candidates map[string]credentialCandidateValue) (*deploymentManifest, error) {
	if err := validateCredentialRotationTransaction(txn); err != nil {
		return nil, err
	}
	if txn.Phase != credentialPhasePlanned {
		return nil, fmt.Errorf("credential transaction %s is in phase %s, expected %s", txn.ID, txn.Phase, credentialPhasePlanned)
	}
	active, err := loadActiveState(base)
	if err != nil {
		return nil, err
	}
	if active.ActiveDeployment != txn.PreviousDeployment {
		return nil, fmt.Errorf("active deployment changed from %s to %s", txn.PreviousDeployment, active.ActiveDeployment)
	}
	previousRoot := filepath.Join(base, "deployments", txn.PreviousDeployment)
	previous, err := loadDeploymentManifest(previousRoot)
	if err != nil {
		return nil, err
	}
	candidate, err := cloneDeploymentManifest(previous)
	if err != nil {
		return nil, err
	}
	candidate.ID = txn.CandidateDeployment
	candidate.CreatedAt = nowUTC()

	targetByID := map[string]credentialTransactionTarget{}
	for _, target := range txn.Targets {
		targetByID[target.ID] = target
	}
	credentialIndex := map[string]int{}
	for index, credential := range candidate.Credentials {
		if _, duplicate := credentialIndex[credential.ID]; duplicate {
			return nil, fmt.Errorf("credential %s is declared more than once in deployment %s", credential.ID, previous.ID)
		}
		credentialIndex[credential.ID] = index
	}
	for id := range targetByID {
		if _, ok := credentialIndex[id]; !ok {
			return nil, fmt.Errorf("credential %s is absent from deployment %s", id, previous.ID)
		}
		value, ok := candidates[id]
		if !ok || value.Value == "" {
			return nil, fmt.Errorf("credential %s has no candidate value", id)
		}
	}
	if len(candidates) != len(targetByID) {
		return nil, fmt.Errorf("candidate set does not match transaction targets")
	}

	stagingRoot := filepath.Join(base, "staging", candidate.ID)
	finalRoot := filepath.Join(base, "deployments", candidate.ID)
	if exists(stagingRoot) || exists(finalRoot) {
		return nil, fmt.Errorf("candidate deployment id collision %s", candidate.ID)
	}
	if err := copyCandidateArtifact(previousRoot, stagingRoot); err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(stagingRoot)
		}
	}()

	for _, name := range candidate.ModuleOrder {
		module, ok := candidate.Modules[name]
		if !ok {
			return nil, fmt.Errorf("deployment module order references missing module %s", name)
		}
		sourceDeployment := module.ArtifactDeployment
		if sourceDeployment == "" {
			sourceDeployment = previous.ID
		}
		sourceDir := filepath.Join(base, "deployments", sourceDeployment, "modules", name)
		targetDir := filepath.Join(stagingRoot, "modules", name)
		if sourceDeployment != previous.ID {
			if err := os.RemoveAll(targetDir); err != nil {
				return nil, err
			}
			if err := copyCandidateArtifact(sourceDir, targetDir); err != nil {
				return nil, err
			}
		}
		if err := rewriteCandidateTextTree(targetDir, sourceDeployment, candidate.ID, base); err != nil {
			return nil, err
		}
		if err := rewriteCandidateEnvIdentity(filepath.Join(targetDir, ".env"), candidate.ID); err != nil {
			return nil, err
		}
		module.ArtifactDeployment = candidate.ID
		candidate.Modules[name] = module
	}
	globalEnv := filepath.Join(stagingRoot, globalEnvFile)
	if exists(globalEnv) {
		if err := rewriteCandidateTextFile(globalEnv, previous.ID, candidate.ID, base); err != nil {
			return nil, err
		}
		if err := rewriteCandidateEnvIdentity(globalEnv, candidate.ID); err != nil {
			return nil, err
		}
	}

	for id, value := range candidates {
		index := credentialIndex[id]
		credential := candidate.Credentials[index]
		target := targetByID[id]
		credential.Generation = target.ToGeneration
		credential.DesiredProjection = "deployment-secret://" + credential.ID
		if value.Authority != "" {
			if value.Authority != "anas" {
				return nil, fmt.Errorf("credential %s candidate authority must be anas", id)
			}
			credential.Authority = value.Authority
		}
		if err := writeCredentialCandidateProjection(stagingRoot, candidate, credential, value.Value); err != nil {
			return nil, err
		}
		candidate.Credentials[index] = credential
	}
	for _, name := range candidate.ModuleOrder {
		module := candidate.Modules[name]
		digest, err := normalizedModuleDigest(filepath.Join(stagingRoot, "modules", name), finalRoot)
		if err != nil {
			return nil, fmt.Errorf("digest candidate module %s: %w", name, err)
		}
		module.RenderDigest = digest
		candidate.Modules[name] = module
	}
	if err := writeYAMLAtomic(filepath.Join(stagingRoot, "deployment.yml"), candidate, 0600); err != nil {
		return nil, err
	}
	if err := sealDeployment(stagingRoot); err != nil {
		return nil, err
	}
	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return nil, err
	}
	cleanup = false
	state := deploymentState{
		APIVersion: activeStateVersion, ID: candidate.ID, Status: "candidate",
		CreatedAt: candidate.CreatedAt, Predecessor: previous.ID,
	}
	if err := saveDeploymentState(base, state); err != nil {
		return nil, err
	}
	txn.Phase = credentialPhaseCandidateCreated
	if err := saveCredentialRotationTransaction(base, txn); err != nil {
		return nil, err
	}
	return candidate, nil
}

func cloneDeploymentManifest(in *deploymentManifest) (*deploymentManifest, error) {
	body, err := yaml.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out deploymentManifest
	if err := yaml.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func copyCandidateArtifact(source, target string) error {
	if !exists(source) {
		return fmt.Errorf("candidate source artifact does not exist: %s", source)
	}
	return copyTree(source, target, func(from, to string) error {
		info, err := os.Stat(from)
		if err != nil {
			return err
		}
		return copyFileMode(from, to, info.Mode().Perm()|0200)
	})
}

func rewriteCandidateTextTree(root, previousID, candidateID, base string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		return rewriteCandidateTextFile(path, previousID, candidateID, base)
	})
}

func rewriteCandidateTextFile(path, previousID, candidateID, base string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	probe := body
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	if bytes.IndexByte(probe, 0) >= 0 {
		return nil
	}
	previousRoot := filepath.Join(base, "deployments", previousID)
	candidateRoot := filepath.Join(base, "deployments", candidateID)
	updated := bytes.ReplaceAll(body, []byte(previousRoot), []byte(candidateRoot))
	updated = bytes.ReplaceAll(updated,
		[]byte(filepath.ToSlash(filepath.Join("runtime-state", "deployments", previousID))),
		[]byte(filepath.ToSlash(filepath.Join("runtime-state", "deployments", candidateID))),
	)
	if bytes.Equal(body, updated) {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, info.Mode().Perm()|0200)
}

func rewriteCandidateEnvIdentity(path, candidateID string) error {
	env, err := parseEnvFile(path)
	if err != nil {
		return fmt.Errorf("read candidate deployment identity from %s: %w", path, err)
	}
	env["ANAS_DEPLOYMENT_ID"] = candidateID
	if err := writeEnv(path, env); err != nil {
		return fmt.Errorf("write candidate deployment identity to %s: %w", path, err)
	}
	return nil
}

func writeCredentialCandidateProjection(root string, manifest *deploymentManifest, credential deploymentCredential, value string) error {
	if len(credential.Projections) > 0 {
		return writeFrozenCredentialCandidateProjections(root, manifest, credential, value)
	}
	// Backward compatibility for deployments created before frozen projection
	// sets were added to the v1 manifest.
	modules := uniqueStrings(append(append([]string{}, credential.Consumers...), credential.Owner))
	sort.Strings(modules)
	if len(modules) == 0 {
		return fmt.Errorf("credential %s has no projection owner or consumers", credential.ID)
	}
	for _, module := range modules {
		if _, ok := manifest.Modules[module]; !ok {
			return fmt.Errorf("credential %s references missing module %s", credential.ID, module)
		}
		path := filepath.Join(root, "modules", module, ".env")
		env, err := parseEnvFile(path)
		if err != nil {
			return fmt.Errorf("read credential %s projection for %s: %w", credential.ID, module, err)
		}
		projection := credential.SecretKey
		if module != credential.Owner {
			projection = ""
			for _, consumer := range manifest.Modules[module].CredentialConsumers {
				if consumer.Credential == credential.ID {
					projection = consumer.Projection
					break
				}
			}
			// Deployment manifests created by the stage-A foundation predate the
			// frozen Module-side projection table and used SecretKey for every
			// consumer. Preserve their value-free candidate semantics.
			if projection == "" && len(manifest.Modules[module].CredentialConsumers) == 0 {
				if _, legacy := env[credential.SecretKey]; legacy {
					projection = credential.SecretKey
				}
			}
			if projection == "" {
				return fmt.Errorf("credential %s has no frozen projection for consumer %s", credential.ID, module)
			}
		}
		if _, ok := env[projection]; !ok {
			return fmt.Errorf("credential %s projection key %s is absent from module %s", credential.ID, projection, module)
		}
		env[projection] = value
		if err := writeEnv(path, env); err != nil {
			return fmt.Errorf("write credential %s projection for %s: %w", credential.ID, module, err)
		}
	}
	return nil
}

func writeFrozenCredentialCandidateProjections(root string, manifest *deploymentManifest, credential deploymentCredential, value string) error {
	oldValue := ""
	for _, projection := range credential.Projections {
		if projection.Module != credential.Owner || projection.EnvKey != credential.SecretKey {
			continue
		}
		env, err := parseEnvFile(filepath.Join(root, "modules", projection.Module, ".env"))
		if err != nil {
			return fmt.Errorf("read credential %s owner projection: %w", credential.ID, err)
		}
		oldValue = env[projection.EnvKey]
		break
	}
	if oldValue == "" {
		return fmt.Errorf("credential %s frozen owner projection is empty", credential.ID)
	}

	modules := map[string]bool{}
	for _, projection := range credential.Projections {
		if _, ok := manifest.Modules[projection.Module]; !ok {
			return fmt.Errorf("credential %s references missing projection module %s", credential.ID, projection.Module)
		}
		path := filepath.Join(root, "modules", projection.Module, ".env")
		env, err := parseEnvFile(path)
		if err != nil {
			return fmt.Errorf("read credential %s projection for %s: %w", credential.ID, projection.Module, err)
		}
		current, ok := env[projection.EnvKey]
		if !ok || current != oldValue {
			return fmt.Errorf("credential %s frozen projection %s/%s differs from its owner", credential.ID, projection.Module, projection.EnvKey)
		}
		modules[projection.Module] = true
	}

	names := make([]string, 0, len(modules))
	for module := range modules {
		names = append(names, module)
	}
	sort.Strings(names)
	for _, module := range names {
		if err := rewriteCredentialValueTree(filepath.Join(root, "modules", module), oldValue, value); err != nil {
			return fmt.Errorf("rewrite credential %s rendered projection for %s: %w", credential.ID, module, err)
		}
	}
	for _, projection := range credential.Projections {
		path := filepath.Join(root, "modules", projection.Module, ".env")
		env, err := parseEnvFile(path)
		if err != nil {
			return fmt.Errorf("read rewritten credential %s projection for %s: %w", credential.ID, projection.Module, err)
		}
		env[projection.EnvKey] = value
		if err := writeEnv(path, env); err != nil {
			return fmt.Errorf("write credential %s projection for %s: %w", credential.ID, projection.Module, err)
		}
	}
	return nil
}

func rewriteCredentialValueTree(root, oldValue, newValue string) error {
	if oldValue == "" || newValue == "" || oldValue == newValue {
		return fmt.Errorf("credential projection replacement requires distinct non-empty values")
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		probe := body
		if len(probe) > 8192 {
			probe = probe[:8192]
		}
		if bytes.IndexByte(probe, 0) >= 0 || !bytes.Contains(body, []byte(oldValue)) {
			return nil
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		return os.WriteFile(path, bytes.ReplaceAll(body, []byte(oldValue), []byte(newValue)), info.Mode().Perm()|0200)
	})
}

// commitCredentialStoreRotation performs the only Store write in a successful
// rotation. Validation completes before mutation, and a failed atomic save
// restores the caller's in-memory view as well as leaving the file untouched.
func commitCredentialStoreRotation(store *secretStore, rotationID string, candidates map[string]credentialStoreCandidate) error {
	if store == nil || strings.TrimSpace(rotationID) == "" {
		return fmt.Errorf("credential Store commit requires a Store and rotation id")
	}
	canonical := make(map[string]credentialStoreCandidate, len(candidates))
	keys := make([]string, 0, len(candidates))
	for raw, candidate := range candidates {
		key := config.EnvKey(raw)
		if !envKeyPattern.MatchString(key) || candidate.Value == "" || candidate.Metadata.Owner == "" ||
			candidate.Metadata.Kind == "" || candidate.Metadata.Generation == 0 {
			return fmt.Errorf("invalid credential Store candidate %q", raw)
		}
		if _, duplicate := canonical[key]; duplicate {
			return fmt.Errorf("credential Store candidates collide at %s", key)
		}
		old, exists := store.values[key]
		if !exists || old == "" {
			return fmt.Errorf("credential Store value for %s is missing", key)
		}
		if old == candidate.Value {
			return fmt.Errorf("credential Store candidate for %s does not change the value", key)
		}
		if candidate.Metadata.Generation <= store.metadata[key].Generation {
			return fmt.Errorf("credential Store generation for %s does not advance", key)
		}
		canonical[key] = candidate
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return fmt.Errorf("credential Store commit has no candidates")
	}
	sort.Strings(keys)
	oldValues := store.clone()
	oldMetadata := make(map[string]secretMetadata, len(store.metadata))
	for key, metadata := range store.metadata {
		oldMetadata[key] = metadata
	}
	oldDirty := store.dirty
	for _, key := range keys {
		candidate := canonical[key]
		candidate.Metadata.RotationID = rotationID
		store.SetWithMetadata(key, candidate.Value, candidate.Metadata)
	}
	if err := store.Save(); err != nil {
		store.values, store.metadata, store.dirty = oldValues, oldMetadata, oldDirty
		return err
	}
	return nil
}
