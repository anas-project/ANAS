package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
)

// Compose receives instance paths at execution time. Config/build/down need
// only syntactically valid sources; create_host_path:false prevents those
// placeholders from becoming a runtime allocation.
func (a *app) composeTemporaryEnvironment(module string, environment map[string]string, allowPlaceholder bool) (map[string]string, error) {
	out := cloneMap(environment)
	for key := range out {
		if strings.HasPrefix(key, "ANAS_TEMP_") {
			delete(out, key)
		}
	}
	if len(a.reg[module].TemporaryDirectories) == 0 {
		return out, nil
	}
	values, err := a.temporaryComposeEnvironment(module)
	if err != nil {
		if !allowPlaceholder {
			return nil, err
		}
		values = map[string]string{}
		for _, directory := range a.reg[module].TemporaryDirectories {
			values[temporaryDirectoryEnvironmentKey(directory.Name)] = filepath.Join("/anas-unallocated-temporary", module, directory.Name)
		}
	}
	for key, value := range values {
		out[key] = value
	}
	return out, nil
}

func frozenTemporaryRoot(workspace, base, id string) (string, error) {
	if id == "" {
		return resolveTemporaryRoot(workspace, "")
	}
	env, err := parseEnvFile(filepath.Join(base, "deployments", id, "modules", globalEnvFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return resolveTemporaryRoot(workspace, "")
		}
		return "", err
	}
	a := &app{workspace: workspace, base: base}
	env = a.relocateDeploymentEnv(env)
	return resolveTemporaryRoot(workspace, env["TEMP_PATH"])
}

// Restored artifacts retain the source workspace's immutable environment. That
// environment describes configuration, not authority to operate source projects.
// Inspect its original binding before relocateDeploymentEnv projects its paths.
func deploymentWorkspaceIsForeign(base, id string) (bool, error) {
	if id == "" {
		return false, nil
	}
	if err := validateDeploymentID(id); err != nil {
		return false, err
	}
	root := filepath.Join(base, "deployments", id)
	env, err := parseEnvFile(filepath.Join(root, "modules", globalEnvFile))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	data := strings.TrimSpace(env["DATA_PATH"])
	if data == "" {
		manifest, err := loadDeploymentManifest(root)
		if err != nil {
			return false, err
		}
		for _, module := range manifest.Modules {
			if len(module.TemporaryDirectories) != 0 {
				return false, fmt.Errorf("deployment %s has managed temporary directories but no frozen workspace binding", id)
			}
		}
		// Legacy artifacts without managed temporary directories still pass
		// the frozen loader and daemon-observed Compose ownership checks.
		return false, nil
	}
	data = filepath.Clean(data)
	if !filepath.IsAbs(data) || filepath.Base(data) != workspaceDataDir {
		return false, errors.New("frozen deployment workspace binding is invalid")
	}
	recorded := filepath.Dir(data)
	if canonical, err := filepath.EvalSymlinks(recorded); err == nil {
		recorded = canonical
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read frozen deployment workspace binding: %w", err)
	}
	workspace, err := filepath.EvalSymlinks(workspaceOf(base))
	if err != nil {
		return false, err
	}
	return filepath.Clean(recorded) != filepath.Clean(workspace), nil
}

func requireLocalDeploymentWorkspace(base, id string) error {
	foreign, err := deploymentWorkspaceIsForeign(base, id)
	if err != nil {
		return err
	}
	if foreign {
		return preconditionErrorf("deployment_workspace_mismatch", "deployment %s was imported from another workspace; run anas apply without --deployment to create a local deployment before operating it", id)
	}
	return nil
}

func recordTemporaryRecoveryPhase(previous *app, failure error) {
	if previous == nil || !exists(temporaryStatePath(previous.base)) {
		return
	}
	registry, err := previous.loadTemporaryRegistry(false)
	if err != nil || registry.Transition == nil {
		return
	}
	if registry.Transition.CleanupModule != "" || registry.Transition.Phase == "cleanup_pending" {
		// Preserve write-ahead cleanup evidence even when compensation also
		// fails. An error report is not proof that the old cleaner can restart.
		return
	}
	phase := "recovery_failed"
	var reported *CLIError
	if errors.As(failure, &reported) && reported.Detail != nil {
		if outcomes, ok := reported.Detail["recovery"].([]map[string]any); ok {
			previousRestored, allSucceeded := false, len(outcomes) > 0
			for _, outcome := range outcomes {
				if outcome["status"] != "succeeded" {
					allSucceeded = false
				}
				previousRestored = previousRestored || outcome["phase"] == "previous_restore" && outcome["status"] == "succeeded"
			}
			if previousRestored && allSucceeded {
				phase = "restored"
			}
		}
	}
	if err := previous.recordTemporaryTransitionPhase(phase); err != nil {
		previous.warning("temp_recovery_state_failed", "temporary recovery result could not be recorded: %v", err)
	}
}

func failTemporaryActivation(base, id, code string, cause error, candidate *app, candidateRoot string, previous *app, previousRoot string, jsonMode bool) *CLIError {
	failure := recordActivationFailure(base, id, code, cause, func() []map[string]any {
		outcomes := []map[string]any{}
		var cleanupError error
		if candidate != nil {
			// The old deployment's now-unbound leases are still compensation
			// authority. A candidate stop must not run module-wide release across
			// roots; release only its own deployment after all users are gone.
			candidate.retainTemporaryLeases = true
			cleanupError = candidate.stopRelease(candidateRoot, jsonMode)
			if cleanupError == nil && exists(temporaryStatePath(base)) {
				cleanupError = candidate.confirmTemporaryCandidateReleased()
			}
			if cleanupError == nil {
				cleanupError = candidate.releaseDeploymentTemporaryStorage(candidate.temporaryDeploymentID())
			}
			outcomes = append(outcomes, recoveryResult("candidate_stop", cleanupError))
		}
		if previous != nil {
			var restoreError error
			if cleanupError != nil {
				restoreError = fmt.Errorf("not attempted: candidate container or cleanup release remains unconfirmed: %w", cleanupError)
			} else {
				restoreError = startDeployment(previous, previousRoot, previous.order, jsonMode)
			}
			outcomes = append(outcomes, recoveryResult("previous_restore", restoreError))
		}
		return outcomes
	})
	recordTemporaryRecoveryPhase(previous, failure)
	return failure
}

func (a *app) confirmTemporaryCandidateReleased() error {
	observation, err := temporaryDockerObservation(a)
	if err != nil {
		return err
	}
	for _, name := range a.order {
		project, err := composeProjectName(name, a.env)
		if err != nil {
			return err
		}
		for _, container := range observation.Containers {
			if container.Config.Labels["com.docker.compose.project"] == project {
				return fmt.Errorf("candidate Compose project %s still has container %s", project, container.ID)
			}
		}
	}
	return a.confirmTemporaryStorageReleased()
}

// An interrupted transition is reconciled under the same workspace lock as
// activation. A committed target is verified and cleaned; otherwise the
// candidate is stopped before restoring the previous frozen deployment.
func recoverTemporaryTransition(base string, cli compose.CLI, opts activateOptions) error {
	if !exists(temporaryStatePath(base)) {
		active, err := loadActiveState(base)
		if err != nil {
			return err
		}
		foreign, err := deploymentWorkspaceIsForeign(base, active.ActiveDeployment)
		if err != nil {
			return err
		}
		if foreign {
			// Backups intentionally omit source leases. Absence of their registry
			// must not let the imported active pointer operate source projects.
			probe := &app{workspace: workspaceOf(base), base: base}
			return probe.detachCopiedTemporaryRuntime("imported-" + active.ActiveDeployment)
		}
		return nil
	}
	probe := &app{workspace: workspaceOf(base), base: base, compose: cli, commandContext: opts.ctx, restrictedProcessEnvironment: opts.restrictedProcessEnvironment}
	registry, err := probe.loadTemporaryRegistry(false)
	if errors.Is(err, errCopiedTemporaryWorkspace) {
		// A copy's active runtime and interrupted transition describe the source
		// workspace. Re-register locally before interpreting either record;
		// this path never observes or stops the source's Docker projects.
		_, err = probe.loadTemporaryRegistry(true)
		return err
	}
	if err != nil {
		return err
	}
	transition := registry.Transition
	if transition == nil || transition.Phase == "complete" || transition.Phase == "restored" || transition.Phase == "cleanup_deferred" {
		return nil
	}
	if transition.Phase == "cleanup_pending" || transition.CleanupModule != "" {
		return fmt.Errorf("temporary path switch cleanup for module %s in deployment %s remains unconfirmed; recovery requires inspection", transition.CleanupModule, transition.CleanupDeployment)
	}
	switch transition.Phase {
	case "prepared", "stopping", "starting", "committed", "recovery_failed":
	default:
		return fmt.Errorf("unrecognized temporary transition phase %q; recovery requires inspection", transition.Phase)
	}
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	load := func(id string) (*app, string, error) {
		a, root, _, err := loadDeploymentApp(base, id, cli)
		if err == nil {
			a.commandContext, a.events = opts.ctx, opts.events
			a.restrictedProcessEnvironment = opts.restrictedProcessEnvironment
		}
		return a, root, err
	}
	if active.ActiveDeployment == transition.TargetDeployment {
		target, _, err := load(transition.TargetDeployment)
		if err != nil {
			return err
		}
		if err := target.verifyDeploymentTemporaryStorage(); err != nil {
			return fmt.Errorf("reconcile committed temporary mounts: %w", err)
		}
		if err := target.commitTemporaryStorage(); err != nil {
			return err
		}
		return target.cleanupCommittedTemporaryStorage(transition.FromDeployment, opts.json)
	}
	if active.ActiveDeployment != transition.FromDeployment {
		return errors.New("temporary transition does not match the active deployment; recovery requires inspection")
	}
	if transition.Phase == "prepared" {
		// No lifecycle effects have begun. Snapshot quiescing records stopping
		// before invoking a Hook, so abandoning a prepared plan cannot restart
		// running services or depend on an old disk merely to repair config.
		return probe.recordTemporaryTransitionPhase("restored")
	}
	candidate, candidateRoot, err := load(transition.TargetDeployment)
	if err != nil {
		return err
	}
	previous, previousRoot, err := load(transition.FromDeployment)
	if err != nil {
		return err
	}
	previous.retainTemporaryLeases = true
	candidate.retainTemporaryLeases = true
	if transition.Phase != "stopping" {
		observation, err := temporaryDockerObservation(candidate)
		if err != nil {
			return err
		}
		selection := []string{}
		for _, name := range candidate.order {
			project, err := composeProjectName(name, candidate.env)
			if err != nil {
				return err
			}
			for _, container := range observation.Containers {
				if container.Config.Labels["com.docker.compose.project"] == project && container.Config.Labels["com.docker.compose.project.working_dir"] == candidate.reg[name].SourceDir {
					selection = append(selection, name)
					break
				}
			}
		}
		if err := candidate.stopModules(candidateRoot, selection, opts.json); err != nil {
			return fmt.Errorf("stop interrupted candidate: %w", err)
		}
		if err := candidate.confirmTemporaryCandidateReleased(); err != nil {
			return err
		}
		for i := range registry.Leases {
			lease := &registry.Leases[i]
			if lease.Root == transition.TargetRoot && lease.State != "deleted" {
				current, err := temporaryDockerObservation(candidate)
				if err != nil {
					return err
				}
				if references := temporaryLeaseReferences(lease, current); len(references) != 0 {
					return fmt.Errorf("interrupted candidate temporary lease still referenced: %v", references)
				}
			}
		}
	}
	if err := startDeployment(previous, previousRoot, previous.order, opts.json); err != nil {
		return fmt.Errorf("restore interrupted previous deployment: %w", err)
	}
	if err := previous.recordTemporaryTransitionPhase("restored"); err != nil {
		return err
	}
	return candidate.releaseDeploymentTemporaryStorage(transition.TargetDeployment)
}

func (a *app) detachCopiedTemporaryRuntime(sourceIdentity string) error {
	active, err := loadActiveState(a.base)
	if err != nil {
		return err
	}
	if active.ActiveDeployment == "" {
		return nil
	}
	body, err := readTemporaryStateFile(filepath.Join(a.base, "state", "active.yml"))
	if err != nil {
		return err
	}
	if err := writeTemporaryStateFile(filepath.Join(a.base, "temp", "foreign-active-"+sourceIdentity+".yml"), body); err != nil {
		return err
	}
	// Persist this before the fresh registry identity. If either write fails,
	// retrying must still identify the copied registry and cannot stop source
	// containers based on an inherited active_deployment field.
	return saveActiveState(a.base, &activeDeploymentState{APIVersion: activeStateVersion, RuntimeStatus: "stopped"})
}

// The active deployment and its mounts have already been committed. Keep old
// leases as retry authority without making their cleanup a lifecycle barrier.
// A later switch may replace this transition; all roots and leases remain in
// the registry and GC still checks their ownership and actual references.
func (a *app) cleanupCommittedTemporaryStorage(previousID string, jsonMode bool) error {
	var releaseErr error
	if previousID != "" {
		releaseErr = a.releaseDeploymentTemporaryStorage(previousID)
	}
	_, gcErr := a.gcTemporaryStorage(false)
	if err := errors.Join(releaseErr, gcErr); err != nil {
		registry, stateErr := a.loadTemporaryRegistry(false)
		if stateErr != nil {
			return errors.Join(err, stateErr)
		}
		if registry.Transition != nil && registry.Transition.Phase == "committed" {
			registry.Transition.Phase = "cleanup_deferred"
			if stateErr := a.saveTemporaryRegistry(registry); stateErr != nil {
				return errors.Join(err, stateErr)
			}
		}
		deploymentWarning(a.events, jsonMode, "temp_cleanup_pending", "deployment is running; temporary cleanup retained for retry: %s", err.Error())
	}
	return nil
}

// before_stop can itself mutate application state. Its intent therefore must
// survive SIGKILL before Core can record the Hook's result. Like backup's
// cleanup marker, a pending record is deliberately not automatically cleared.
func (a *app) beginTemporaryStopCleanup(module string) (bool, error) {
	if !exists(temporaryStatePath(a.base)) {
		return false, nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return false, err
	}
	transition := registry.Transition
	if transition == nil || transition.Phase == "complete" || transition.Phase == "restored" || transition.Phase == "cleanup_deferred" {
		return false, nil
	}
	if transition.CleanupModule != "" || transition.Phase == "cleanup_pending" {
		return false, errors.New("previous temporary transition cleanup remains unconfirmed")
	}
	deploymentID := a.temporaryDeploymentID()
	if deploymentID == "" {
		return false, errors.New("temporary stop cleanup requires a deployment identity")
	}
	transition.CleanupModule, transition.CleanupDeployment = module, deploymentID
	transition.CleanupPreviousPhase, transition.Phase = transition.Phase, "cleanup_pending"
	if err := a.saveTemporaryRegistry(registry); err != nil {
		return false, err
	}
	return true, nil
}

func (a *app) requireTemporaryCleanupConfirmed() error {
	if !exists(temporaryStatePath(a.base)) {
		return nil
	}
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	if registry.Transition != nil && (registry.Transition.Phase == "cleanup_pending" || registry.Transition.CleanupModule != "") {
		return fmt.Errorf("temporary transition cleanup for module %s remains unconfirmed; automatic startup is blocked", registry.Transition.CleanupModule)
	}
	return nil
}

func (a *app) finishTemporaryStopCleanup(module string) error {
	registry, err := a.loadTemporaryRegistry(false)
	if err != nil {
		return err
	}
	transition := registry.Transition
	if transition == nil || transition.CleanupModule != module || transition.CleanupDeployment != a.temporaryDeploymentID() || transition.Phase != "cleanup_pending" {
		return errors.New("temporary stop cleanup binding changed")
	}
	transition.Phase = transition.CleanupPreviousPhase
	transition.CleanupModule, transition.CleanupDeployment, transition.CleanupPreviousPhase = "", "", ""
	return a.saveTemporaryRegistry(registry)
}

func temporarySwitchPlan(workspace, desired string, targetOrder []string) (*application.TempSwitchPlan, error) {
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return nil, err
	}
	result := &application.TempSwitchPlan{StopModules: []string{}, StartModules: []string{}}
	if active.ActiveDeployment == "" {
		return result, nil
	}
	previous := ""
	if exists(temporaryStatePath(base)) {
		previous, err = (&app{workspace: workspace, base: base}).temporaryAppliedRoot()
		if errors.Is(err, errCopiedTemporaryWorkspace) {
			// A plan is read-only. A copied source deployment has no running
			// instances owned by this workspace, so there is no old path to stop.
			return result, nil
		}
		if err != nil {
			return nil, err
		}
	}
	foreign, err := deploymentWorkspaceIsForeign(base, active.ActiveDeployment)
	if err != nil {
		return nil, err
	}
	if foreign {
		return result, nil
	}
	if previous == "" {
		previous, err = frozenTemporaryRoot(workspace, base, active.ActiveDeployment)
		if err != nil {
			return nil, err
		}
	}
	if filepath.Clean(previous) == filepath.Clean(desired) {
		return result, nil
	}
	current, err := loadDeploymentManifest(filepath.Join(base, "deployments", active.ActiveDeployment))
	if err != nil {
		return nil, err
	}
	result.Required, result.SessionInterruption = true, true
	for i := len(current.ModuleOrder) - 1; i >= 0; i-- {
		result.StopModules = append(result.StopModules, current.ModuleOrder[i])
	}
	result.StartModules = append(result.StartModules, targetOrder...)
	return result, nil
}

// Historical rollback reuses frozen bytes and resolved bindings, never the
// mutable Module registry. The copy gets a new identity and its authorization
// records are rebound before sealing; historical files remain untouched.
func materializeTemporaryRollback(base, historicalID string) (string, error) {
	previousRoot := filepath.Join(base, "deployments", historicalID)
	previous, err := loadDeploymentManifest(previousRoot)
	if err != nil {
		return "", err
	}
	candidate, err := cloneDeploymentManifest(previous)
	if err != nil {
		return "", err
	}
	id, err := newDeploymentID()
	if err != nil {
		return "", err
	}
	candidate.ID, candidate.CreatedAt = id, nowUTC()
	staging := filepath.Join(base, "staging", id)
	final := filepath.Join(base, "deployments", id)
	if exists(staging) || exists(final) {
		return "", fmt.Errorf("temporary rollback deployment collision %s", id)
	}
	if err := copyCandidateArtifact(previousRoot, staging); err != nil {
		return "", err
	}
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(staging)
		}
	}()
	for _, name := range candidate.ModuleOrder {
		module := candidate.Modules[name]
		sourceID := module.ArtifactDeployment
		if sourceID == "" {
			sourceID = historicalID
		}
		dir := filepath.Join(staging, "modules", name)
		if sourceID != historicalID {
			if err := os.RemoveAll(dir); err != nil {
				return "", err
			}
			if err := copyCandidateArtifact(filepath.Join(base, "deployments", sourceID, "modules", name), dir); err != nil {
				return "", err
			}
		}
		if err := rewriteCandidateTextTree(dir, sourceID, id, base); err != nil {
			return "", err
		}
		if err := rewriteCandidateEnvIdentity(filepath.Join(dir, ".env"), id); err != nil {
			return "", err
		}
		module.ArtifactDeployment = id
		module.RenderDigest, err = normalizedModuleDigest(dir, final)
		if err != nil {
			return "", err
		}
		candidate.Modules[name] = module
	}
	global := filepath.Join(staging, "modules", globalEnvFile)
	if err := rewriteCandidateTextFile(global, historicalID, id, base); err != nil {
		return "", err
	}
	if err := rewriteCandidateEnvIdentity(global, id); err != nil {
		return "", err
	}
	for i := range candidate.Resources {
		resource := &candidate.Resources[i]
		if resource.ComputeIngress != nil {
			resource.ComputeIngress.Deployment = id
			if err := resource.ComputeIngress.Validate(); err != nil {
				return "", err
			}
		}
	}
	if err := writeYAMLAtomic(filepath.Join(staging, "deployment.yml"), candidate, 0600); err != nil {
		return "", err
	}
	if err := sealDeployment(staging); err != nil {
		return "", err
	}
	if err := os.Rename(staging, final); err != nil {
		return "", err
	}
	promoted = true
	if err := saveDeploymentState(base, deploymentState{APIVersion: activeStateVersion, ID: id, Status: "ready", CreatedAt: candidate.CreatedAt, Predecessor: historicalID}); err != nil {
		return "", err
	}
	return id, nil
}
