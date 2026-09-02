package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/dns"
)

type workspaceDeploymentPlanApplication struct {
	workspace  string
	configPath string
	moduleRoot func() (string, error)
	events     application.EventSink
	lockCode   string
	daemon     bool
	jsonMode   bool
}

var _ application.DeploymentPlanService = (*workspaceDeploymentPlanApplication)(nil)
var _ application.DeploymentApplyService = (*workspaceDeploymentPlanApplication)(nil)
var _ application.DeploymentLifecycleService = (*workspaceDeploymentPlanApplication)(nil)
var _ application.DeploymentRollbackService = (*workspaceDeploymentPlanApplication)(nil)
var _ application.DeploymentCompensationService = (*workspaceDeploymentPlanApplication)(nil)

// NewWorkspaceDeploymentPlanService returns the daemon-facing deployment plan
// service. It resolves Modules only through the workspace's persisted immutable
// view and never consults cwd, ANAS_MODULE_ROOT, or a caller-provided path.
func NewWorkspaceDeploymentPlanService(workspace string) application.DeploymentPlanService {
	return NewWorkspaceDeploymentService(workspace)
}

// NewWorkspaceDeploymentService returns the daemon-facing plan/apply service.
// Apply requires a server-validated plan binding before it can materialize or
// activate anything.
func NewWorkspaceDeploymentService(workspace string) application.DeploymentService {
	return NewWorkspaceDeploymentServiceWithEvents(workspace, application.NopEventSink{})
}

// NewWorkspaceDeploymentServiceWithEvents binds durable job progress/warning
// adapters without letting them influence path or execution policy.
func NewWorkspaceDeploymentServiceWithEvents(workspace string, events application.EventSink) application.DeploymentService {
	workspace = filepath.Clean(workspace)
	if events == nil {
		events = application.NopEventSink{}
	}
	return &workspaceDeploymentPlanApplication{
		workspace:  workspace,
		configPath: workspaceConfigPath(workspace),
		moduleRoot: func() (string, error) {
			view, err := loadWorkspaceModuleView(workspace)
			if err != nil {
				return "", err
			}
			root := filepath.Clean(view.ModuleRoot)
			if !filepath.IsAbs(root) {
				return "", errors.New("workspace Module view does not contain an absolute root")
			}
			return root, nil
		},
		events:   events,
		lockCode: "runtime_lock_unavailable",
		daemon:   true,
	}
}

func newCLIDeploymentPlanService(workspace, configPath, explicitModuleRoot string, events application.EventSink) *workspaceDeploymentPlanApplication {
	jsonMode := false
	if sink, ok := events.(moduleCommandCLISink); ok {
		jsonMode = sink.jsonMode
	}
	return &workspaceDeploymentPlanApplication{
		workspace:  workspace,
		configPath: configPath,
		moduleRoot: func() (string, error) {
			return locateModuleRootForWorkspace(explicitModuleRoot, workspace)
		},
		events:   events,
		lockCode: "lock_failed",
		jsonMode: jsonMode,
	}
}

func (service *workspaceDeploymentPlanApplication) Plan(ctx context.Context, _ application.PlanRequest) (application.PlanResult, error) {
	if ctx == nil {
		return application.PlanResult{}, deploymentApplicationError("plan_canceled", errors.New("nil context"))
	}
	if service == nil || service.moduleRoot == nil {
		return application.PlanResult{}, deploymentApplicationError("plan_unavailable", errors.New("deployment plan service is unavailable"))
	}
	base := stateDir(service.workspace)
	unlock, err := acquireWorkspaceConfigReadLock(ctx, base)
	if err != nil {
		code := service.lockCode
		if code == "" {
			code = "runtime_lock_unavailable"
		}
		return application.PlanResult{}, deploymentApplicationError(code, err)
	}
	defer unlock()
	return service.planLocked(ctx)
}

// planLocked computes against one coherent managed-config/Secret Store/Module
// generation. The caller holds either the shared read lock (Plan) or the
// exclusive runtime lock (Apply drift revalidation).
func (service *workspaceDeploymentPlanApplication) planLocked(ctx context.Context) (application.PlanResult, error) {
	base := stateDir(service.workspace)
	if err := validateManagedConfig(service.workspace, service.configPath); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_not_managed", "%s", err.Error()))
	}
	moduleRoot, err := service.moduleRoot()
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("module_root_missing", "%s", err.Error()))
	}
	moduleRoot = filepath.Clean(moduleRoot)
	if !filepath.IsAbs(moduleRoot) {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("module_root_invalid", "module root is not absolute"))
	}
	if !exists(service.configPath) {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_missing", "config %s does not exist", service.configPath))
	}
	cfg, err := config.Load(service.configPath)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "%s", err.Error()))
	}
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("module_root_invalid", "%s", err.Error()))
	}
	if err := validateConfigRuntimeKeyCollisions(service.configPath, reg); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "%s", err.Error()))
	}
	privateStore, err := loadSecretStore(base)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("secrets_unreadable", "%s", err.Error()))
	}
	if err := rejectUnimportedConfigSecrets(service.configPath, reg, privateStore.values); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_requires_import", "%s", err.Error()))
	}
	contracts, err := loadContractRegistry(moduleRoot)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("contract_root_invalid", "%s", err.Error()))
	}
	lock, err := loadModuleLockFile(projectLockPath(service.configPath))
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("lock_invalid", "%s", err.Error()))
	}
	a := &app{
		base: base, cfg: cfg, cfgPath: service.configPath,
		reg: reg, contracts: contracts, lock: lock, commandContext: ctx, events: service.events,
		restrictedProcessEnvironment: service.daemon,
		resolvedBindings:             map[string]map[string]string{},
	}
	a.env, a.envOwner = configBaseEnvWithRegistry(cfg, reg)
	if err := a.loadImportedSecrets(); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("secrets_unreadable", "%s", err.Error()))
	}
	if err := a.validateContractRegistry(); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("contract_invalid", "%s", err.Error()))
	}
	a.order, err = a.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("resolution_failed", "%s", err.Error()))
	}
	if len(lock.Modules) > 0 {
		if err := validateLockedResolution(a, lock); err != nil {
			return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("lock_stale", "%s", err.Error()))
		}
	} else if err := a.validateVersions(lock); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("version_conflict", "%s", err.Error()))
	}
	a.applyModuleDefaults()
	resolvedOrder := a.order
	a.order = trustedModuleValidationOrder(resolvedOrder, lock)
	if err := a.validateModules(); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "%s", err.Error()))
	}
	a.order = resolvedOrder
	if err := a.materializeDNSCredentials(); err != nil {
		return application.PlanResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("dns_credentials_invalid", "%s", err.Error()))
	}
	a.reportDynamicDNSOverlaps()
	current, err := readManagedConfigSnapshot(service.workspace)
	if err != nil {
		return application.PlanResult{}, configServiceBoundaryError("config_state_invalid", err)
	}
	result := deploymentPlanResult(a)
	result.Workspace = service.workspace
	result.ConfigValidator = current.validator
	result.ConfigPath = service.configPath
	result.ModuleRoot = moduleRoot
	result.Digest, err = deploymentPlanDigest(result)
	if err != nil {
		return application.PlanResult{}, deploymentApplicationError("plan_digest_failed", err)
	}
	return result, nil
}

func (service *workspaceDeploymentPlanApplication) Apply(ctx context.Context, request application.ApplyRequest) (application.ApplyResult, error) {
	if ctx == nil {
		return application.ApplyResult{}, deploymentApplicationError("apply_canceled", errors.New("nil context"))
	}
	if service == nil || service.moduleRoot == nil {
		return application.ApplyResult{}, deploymentApplicationError("apply_unavailable", errors.New("deployment apply service is unavailable"))
	}
	if request.Snapshot && request.NoSnapshot {
		return application.ApplyResult{}, &application.Error{
			Kind: application.ErrorKindInvalidArgument, Code: "invalid_snapshot_policy",
			Message: "apply accepts either snapshot or no_snapshot, not both",
		}
	}
	if service.daemon {
		if !validManagedConfigValidator(request.ExpectedConfigValidator) || !validDeploymentPlanDigest(request.ExpectedPlanDigest) {
			return application.ApplyResult{}, &application.Error{
				Kind: application.ErrorKindFailedPrecondition, Code: "plan_binding_invalid",
				Message: "apply requires a valid config validator and plan digest",
			}
		}
		if !request.Confirmed {
			return application.ApplyResult{}, &application.Error{
				Kind: application.ErrorKindPreconditionRequired, Code: "confirmation_required",
				Message: "apply requires a consumed server confirmation",
			}
		}
	}
	base := stateDir(service.workspace)
	unlock, err := acquireRuntimeLockForApplication(ctx, base, service.events, service.daemon)
	if err != nil {
		code := service.lockCode
		if code == "" {
			code = "runtime_lock_unavailable"
		}
		return application.ApplyResult{}, deploymentApplicationError(code, err)
	}
	defer unlock()
	if service.daemon {
		plan, err := service.planLocked(ctx)
		if err != nil {
			return application.ApplyResult{}, err
		}
		if plan.ConfigValidator != request.ExpectedConfigValidator || plan.Digest != request.ExpectedPlanDigest {
			return application.ApplyResult{}, &application.Error{
				Kind: application.ErrorKindFailedPrecondition, Code: "plan_changed",
				Message: "configuration or deployment plan changed after confirmation",
			}
		}
		if err := service.rejectDaemonNetworkNamespace(plan.ModuleRoot); err != nil {
			return application.ApplyResult{}, err
		}
	}
	before, err := loadActiveState(base)
	if err != nil {
		return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	deploymentID := request.DeploymentID
	if deploymentID == "" {
		if !exists(service.configPath) {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_missing", "config %s does not exist", service.configPath))
		}
		if err := validateManagedConfig(service.workspace, service.configPath); err != nil {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_not_managed", "%s", err.Error()))
		}
		moduleRoot, err := service.moduleRoot()
		if err != nil {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("module_root_missing", "%s", err.Error()))
		}
		opts := prepareOptions{
			workspace: service.workspace, base: base, cfgPath: service.configPath,
			moduleRoot: moduleRoot, updateLock: request.UpdateLock, context: ctx, events: service.events,
			restrictedProcessEnvironment: service.daemon,
		}
		deploymentID, err = materializeDeployment(opts, request.Build, service.jsonMode)
		if err != nil {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(err)
		}
	} else {
		if err := validateDeploymentID(deploymentID); err != nil {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(usageErrorf("%s", err.Error()))
		}
		state, err := loadDeploymentState(base, deploymentID)
		if err != nil {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_missing", "%s", err.Error()))
		}
		if state.Status != "ready" {
			return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_not_ready",
				"deployment %s has status %q; apply requires ready", deploymentID, state.Status))
		}
	}
	if err := activateDeployment(base, deploymentID, activateOptions{
		allowRisky: request.AllowRisky, snapshot: request.Snapshot, noSnapshot: request.NoSnapshot,
		yes: request.Confirmed, json: service.jsonMode, ctx: ctx, events: service.events,
		restrictedProcessEnvironment: service.daemon,
	}); err != nil {
		return application.ApplyResult{}, deploymentApplicationErrorFromCLI(err)
	}
	after, err := loadActiveState(base)
	if err != nil {
		return application.ApplyResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	return application.ApplyResult{
		Workspace: service.workspace, DeploymentID: deploymentID,
		PreviousDeployment: applicationNullableString(before.ActiveDeployment),
		ActivatedAt:        after.ActivatedAt, DeploymentPath: filepath.Join(base, "deployments", deploymentID),
	}, nil
}

func (service *workspaceDeploymentPlanApplication) PreviewLifecycle(ctx context.Context, request application.LifecyclePreviewRequest) (application.LifecyclePreviewResult, error) {
	if ctx == nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationError("lifecycle_canceled", errors.New("nil context"))
	}
	if service == nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationError("lifecycle_unavailable", errors.New("deployment lifecycle service is unavailable"))
	}
	if !validLifecycleAction(request.Action) {
		return application.LifecyclePreviewResult{}, &application.Error{
			Kind: application.ErrorKindInvalidArgument, Code: "lifecycle_action_invalid", Message: "lifecycle action must be start, stop, or restart",
		}
	}
	base := stateDir(service.workspace)
	unlock, err := acquireWorkspaceConfigReadLock(ctx, base)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationError(service.applicationLockCode(), err)
	}
	defer unlock()
	return service.previewLifecycleLocked(ctx, request)
}

func (service *workspaceDeploymentPlanApplication) previewLifecycleLocked(ctx context.Context, request application.LifecyclePreviewRequest) (application.LifecyclePreviewResult, error) {
	base := stateDir(service.workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	if active.ActiveDeployment == "" {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("no_active_deployment", "no active deployment; run anas apply first"))
	}
	cli, err := detectComposeForExecution(ctx, service.daemon)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("compose_missing", "%s", err.Error()))
	}
	a, _, manifest, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_unreadable", "%s", err.Error()))
	}
	if err := credentialStoreConsistencyError(base, manifest); err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(err)
	}
	requested := normalizeLifecycleModules(request.Modules)
	affected, err := selectLifecycleModules(a, string(request.Action), requested)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationErrorFromCLI(usageErrorf("%s", err.Error()))
	}
	result := application.LifecyclePreviewResult{
		Workspace: service.workspace, DeploymentID: active.ActiveDeployment, Action: request.Action,
		RequestedModules: requested, AffectedModules: affected,
	}
	result.Digest, err = lifecyclePreviewDigest(result)
	if err != nil {
		return application.LifecyclePreviewResult{}, deploymentApplicationError("lifecycle_digest_failed", err)
	}
	return result, nil
}

func (service *workspaceDeploymentPlanApplication) ExecuteLifecycle(ctx context.Context, request application.LifecycleRequest) (application.LifecycleResult, error) {
	if ctx == nil {
		return application.LifecycleResult{}, deploymentApplicationError("lifecycle_canceled", errors.New("nil context"))
	}
	if service == nil || !validLifecycleAction(request.Action) {
		return application.LifecycleResult{}, &application.Error{
			Kind: application.ErrorKindInvalidArgument, Code: "lifecycle_action_invalid", Message: "lifecycle action must be start, stop, or restart",
		}
	}
	if service.daemon && (!request.Confirmed || request.ExpectedDeploymentID == "" || !validDeploymentPlanDigest(request.ExpectedDigest)) {
		return application.LifecycleResult{}, &application.Error{
			Kind: application.ErrorKindPreconditionRequired, Code: "lifecycle_confirmation_required",
			Message: "lifecycle execution requires a confirmed dependency-chain preview",
		}
	}
	base := stateDir(service.workspace)
	unlock, err := acquireRuntimeLockForApplication(ctx, base, service.events, service.daemon)
	if err != nil {
		return application.LifecycleResult{}, deploymentApplicationError(service.applicationLockCode(), err)
	}
	defer unlock()
	preview, err := service.previewLifecycleLocked(ctx, application.LifecyclePreviewRequest{Action: request.Action, Modules: request.Modules})
	if err != nil {
		return application.LifecycleResult{}, err
	}
	if service.daemon && (preview.DeploymentID != request.ExpectedDeploymentID || preview.Digest != request.ExpectedDigest || !equalStringSlices(preview.AffectedModules, request.ExpectedModules)) {
		return application.LifecycleResult{}, &application.Error{
			Kind: application.ErrorKindFailedPrecondition, Code: "lifecycle_preview_changed",
			Message: "active deployment or lifecycle dependency chain changed after confirmation",
		}
	}
	cli, err := detectComposeForExecution(ctx, service.daemon)
	if err != nil {
		return application.LifecycleResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("compose_missing", "%s", err.Error()))
	}
	active, err := loadActiveState(base)
	if err != nil {
		return application.LifecycleResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	a, root, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
	if err != nil {
		return application.LifecycleResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_unreadable", "%s", err.Error()))
	}
	a.commandContext, a.events = ctx, service.events
	a.restrictedProcessEnvironment = service.daemon
	partial := len(preview.RequestedModules) > 0
	wholeDeployment := !partial || len(preview.AffectedModules) == len(a.order)
	stop := func() error {
		if partial {
			return a.stopModules(root, preview.AffectedModules, service.jsonMode)
		}
		return a.stopRelease(root, service.jsonMode)
	}
	switch request.Action {
	case application.LifecycleStart:
		err = startDeployment(a, root, preview.AffectedModules, service.jsonMode)
		if err != nil {
			err = failuref("start_failed", "%s", err.Error())
		}
	case application.LifecycleRestart:
		if err = stop(); err != nil {
			err = failuref("stop_failed", "%s", err.Error())
		} else if err = startDeployment(a, root, preview.AffectedModules, service.jsonMode); err != nil {
			err = failuref("start_failed", "%s", err.Error())
		}
	case application.LifecycleStop:
		if err = stop(); err != nil {
			err = failuref("stop_failed", "%s", err.Error())
		}
	}
	if err != nil {
		return application.LifecycleResult{}, deploymentApplicationErrorFromCLI(err)
	}
	if wholeDeployment {
		if request.Action == application.LifecycleStop {
			active.RuntimeStatus = "stopped"
		} else {
			active.RuntimeStatus = "running"
		}
		if err := saveActiveState(base, active); err != nil {
			return application.LifecycleResult{}, deploymentApplicationErrorFromCLI(failuref("write_failed", "record runtime status: %s", err.Error()))
		}
	}
	return application.LifecycleResult{
		Workspace: service.workspace, DeploymentID: active.ActiveDeployment, Action: request.Action,
		Modules: append([]string{}, preview.AffectedModules...),
	}, nil
}

func (service *workspaceDeploymentPlanApplication) PreviewRollback(ctx context.Context, request application.RollbackPreviewRequest) (application.RollbackPreviewResult, error) {
	if ctx == nil {
		return application.RollbackPreviewResult{}, deploymentApplicationError("rollback_canceled", errors.New("nil context"))
	}
	base := stateDir(service.workspace)
	unlock, err := acquireWorkspaceConfigReadLock(ctx, base)
	if err != nil {
		return application.RollbackPreviewResult{}, deploymentApplicationError(service.applicationLockCode(), err)
	}
	defer unlock()
	return service.previewRollbackLocked(request)
}

func (service *workspaceDeploymentPlanApplication) previewRollbackLocked(request application.RollbackPreviewRequest) (application.RollbackPreviewResult, error) {
	base := stateDir(service.workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	target := request.DeploymentID
	if target == "" && len(active.PreviousDeployments) > 0 {
		target = active.PreviousDeployments[0]
	}
	if target == "" {
		return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("no_previous_deployment", "no previous deployment to roll back to"))
	}
	if err := validateDeploymentID(target); err != nil {
		return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(usageErrorf("%s", err.Error()))
	}
	if target == active.ActiveDeployment {
		return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("already_active", "deployment %s is already active", target))
	}
	targetManifest, err := loadDeploymentManifest(filepath.Join(base, "deployments", target))
	if err != nil {
		return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_unreadable", "%s", err.Error()))
	}
	var guarded []string
	if active.ActiveDeployment != "" {
		current, err := loadDeploymentManifest(filepath.Join(base, "deployments", active.ActiveDeployment))
		if err != nil {
			return application.RollbackPreviewResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("deployment_unreadable", "%s", err.Error()))
		}
		guarded = deploymentChangeBlockers(current, targetManifest)
	}
	result := application.RollbackPreviewResult{
		Workspace: service.workspace, ActiveDeployment: active.ActiveDeployment, TargetDeployment: target,
		GuardedChanges: guarded, DataTouched: false,
	}
	result.Digest, err = rollbackPreviewDigest(result)
	if err != nil {
		return application.RollbackPreviewResult{}, deploymentApplicationError("rollback_digest_failed", err)
	}
	return result, nil
}

func (service *workspaceDeploymentPlanApplication) Rollback(ctx context.Context, request application.RollbackRequest) (application.RollbackResult, error) {
	if ctx == nil {
		return application.RollbackResult{}, deploymentApplicationError("rollback_canceled", errors.New("nil context"))
	}
	if service.daemon && (!request.Confirmed || request.ExpectedActiveDeployment == "" || !validDeploymentPlanDigest(request.ExpectedDigest)) {
		return application.RollbackResult{}, &application.Error{
			Kind: application.ErrorKindPreconditionRequired, Code: "rollback_confirmation_required",
			Message: "rollback requires a confirmed impact preview",
		}
	}
	base := stateDir(service.workspace)
	unlock, err := acquireRuntimeLockForApplication(ctx, base, service.events, service.daemon)
	if err != nil {
		return application.RollbackResult{}, deploymentApplicationError(service.applicationLockCode(), err)
	}
	defer unlock()
	preview, err := service.previewRollbackLocked(application.RollbackPreviewRequest{DeploymentID: request.DeploymentID})
	if err != nil {
		return application.RollbackResult{}, err
	}
	if service.daemon && (preview.ActiveDeployment != request.ExpectedActiveDeployment || preview.Digest != request.ExpectedDigest) {
		return application.RollbackResult{}, &application.Error{
			Kind: application.ErrorKindFailedPrecondition, Code: "rollback_preview_changed",
			Message: "active deployment or rollback impact changed after confirmation",
		}
	}
	if err := activateDeployment(base, preview.TargetDeployment, activateOptions{
		allowRisky: request.AllowRisky, rollback: true, yes: request.Confirmed, json: service.jsonMode,
		ctx: ctx, events: service.events, restrictedProcessEnvironment: service.daemon,
	}); err != nil {
		return application.RollbackResult{}, deploymentApplicationErrorFromCLI(err)
	}
	after, err := loadActiveState(base)
	if err != nil {
		return application.RollbackResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("state_unreadable", "%s", err.Error()))
	}
	return application.RollbackResult{
		Workspace: service.workspace, DeploymentID: preview.TargetDeployment,
		PreviousDeployment: applicationNullableString(preview.ActiveDeployment), ActivatedAt: after.ActivatedAt, DataTouched: false,
	}, nil
}

func (service *workspaceDeploymentPlanApplication) CheckCompensation(ctx context.Context) error {
	if ctx == nil {
		return deploymentApplicationError("compensation_canceled", errors.New("nil context"))
	}
	if service == nil {
		return deploymentApplicationError("compensation_unavailable", errors.New("deployment compensation service is unavailable"))
	}
	unlocked, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, service.daemon)
	if err != nil {
		return deploymentApplicationError(service.applicationLockCode(), err)
	}
	unlocked()
	return nil
}

func (service *workspaceDeploymentPlanApplication) applicationLockCode() string {
	if service != nil && service.lockCode != "" {
		return service.lockCode
	}
	return "runtime_lock_unavailable"
}

func validLifecycleAction(action application.LifecycleAction) bool {
	switch action {
	case application.LifecycleStart, application.LifecycleStop, application.LifecycleRestart:
		return true
	default:
		return false
	}
}

func normalizeLifecycleModules(modules []string) []string {
	result := make([]string, 0, len(modules))
	seen := map[string]bool{}
	for _, module := range modules {
		module = strings.ToLower(strings.TrimSpace(module))
		if module != "" && !seen[module] {
			seen[module] = true
			result = append(result, module)
		}
	}
	return result
}

func lifecyclePreviewDigest(result application.LifecyclePreviewResult) (string, error) {
	return digestApplicationValue(struct {
		DeploymentID     string                      `json:"deployment_id"`
		Action           application.LifecycleAction `json:"action"`
		RequestedModules []string                    `json:"requested_modules"`
		AffectedModules  []string                    `json:"affected_modules"`
	}{result.DeploymentID, result.Action, result.RequestedModules, result.AffectedModules})
}

func rollbackPreviewDigest(result application.RollbackPreviewResult) (string, error) {
	return digestApplicationValue(struct {
		ActiveDeployment string   `json:"active_deployment"`
		TargetDeployment string   `json:"target_deployment"`
		GuardedChanges   []string `json:"guarded_changes"`
		DataTouched      bool     `json:"data_touched"`
	}{result.ActiveDeployment, result.TargetDeployment, result.GuardedChanges, result.DataTouched})
}

func digestApplicationValue(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (service *workspaceDeploymentPlanApplication) rejectDaemonNetworkNamespace(moduleRoot string) error {
	cfg, err := config.Load(service.configPath)
	if err != nil {
		return deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "%s", err.Error()))
	}
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		return deploymentApplicationErrorFromCLI(preconditionErrorf("module_root_invalid", "%s", err.Error()))
	}
	env, owners := configBaseEnvWithRegistry(cfg, reg)
	a := &app{cfg: cfg, reg: reg, env: env, envOwner: owners}
	a.applyModuleDefaults()
	if strings.TrimSpace(a.env["NETWORK_NAMESPACE_PATH"]) != "" {
		return &application.Error{
			Kind: application.ErrorKindFailedPrecondition, Code: "network_namespace_requires_tty",
			Message: "NETWORK_NAMESPACE_PATH requires sudo/nsenter and is unavailable to the non-interactive daemon",
		}
	}
	return nil
}

func validDeploymentPlanDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func applicationNullableString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}

func deploymentPlanDigest(result application.PlanResult) (string, error) {
	body, err := json.Marshal(struct {
		ConfigValidator            string                                       `json:"config_validator"`
		Modules                    []string                                     `json:"modules"`
		IAM                        application.PlanIAM                          `json:"iam"`
		ModulePlans                map[string]map[string]string                 `json:"module_plans"`
		CapabilityBindings         map[string]map[string]string                 `json:"capability_bindings"`
		DNSPlatforms               map[string]string                            `json:"dns_platforms"`
		DNSCredentialCompatibility []application.PlanDNSCredentialCompatibility `json:"dns_credential_compatibility"`
		DynamicDNS                 application.PlanDynamicDNS                   `json:"dynamic_dns"`
		ModuleLifecycles           []application.PlanModuleLifecycle            `json:"module_lifecycles"`
	}{
		ConfigValidator: result.ConfigValidator, Modules: result.Modules, IAM: result.IAM,
		ModulePlans: result.ModulePlans, CapabilityBindings: result.CapabilityBindings,
		DNSPlatforms: result.DNSPlatforms, DNSCredentialCompatibility: result.DNSCredentialCompatibility,
		DynamicDNS: result.DynamicDNS, ModuleLifecycles: result.ModuleLifecycles,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:]), nil
}

func deploymentApplicationError(code string, err error) error {
	if _, ok := application.ErrorOf(err); ok {
		return err
	}
	return &application.Error{Kind: application.ErrorKindInternal, Code: code, Message: err.Error(), Cause: err}
}

func deploymentApplicationErrorFromCLI(err error) error {
	if _, ok := application.ErrorOf(err); ok {
		return err
	}
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		return deploymentApplicationError("plan_failed", err)
	}
	kind := application.ErrorKindInternal
	switch cliErr.Exit {
	case exitUsage:
		kind = application.ErrorKindInvalidArgument
	case exitConfirmation:
		kind = application.ErrorKindPreconditionRequired
	case exitPrecondition:
		kind = application.ErrorKindFailedPrecondition
	}
	return &application.Error{
		Kind: kind, Code: cliErr.Code, Message: cliErr.Message,
		Detail: cloneApplicationErrorDetail(cliErr.Detail), Cause: cliErr,
	}
}

func cloneApplicationErrorDetail(source map[string]any) map[string]any {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func planResultCLIMap(result application.PlanResult) map[string]any {
	return map[string]any{
		"config": result.ConfigPath, "module_root": result.ModuleRoot,
		"modules": result.Modules, "iam": result.IAM,
		"module_plans": result.ModulePlans, "capability_bindings": result.CapabilityBindings,
		"dns_platforms": result.DNSPlatforms,
		"dynamic_dns": map[string]any{
			"provider": result.DynamicDNS.Provider, "self_managed": result.DynamicDNS.SelfManaged,
		},
	}
}

func deploymentPlanResult(a *app) application.PlanResult {
	result := application.PlanResult{
		Modules:            append([]string(nil), a.order...),
		IAM:                application.PlanIAM{Consumers: []application.PlanIAMConsumer{}},
		ModulePlans:        a.moduleValidationPlanDocument(),
		CapabilityBindings: cloneNestedMap(a.resolvedBindings),
		DNSPlatforms:       map[string]string{},
		DynamicDNS:         application.PlanDynamicDNS{SelfManaged: []string{}},
		ModuleLifecycles:   []application.PlanModuleLifecycle{},
	}
	if a.iamProvider != "" && len(a.iamBindings) != 0 {
		provider := a.iamProvider
		result.IAM.Provider = &provider
		for _, consumer := range a.iamConsumers() {
			result.IAM.Consumers = append(result.IAM.Consumers, application.PlanIAMConsumer{
				Module: consumer, Interface: a.iamBindings[consumer],
			})
		}
	}
	for _, engine := range dns.Engines() {
		if !contains(a.order, engine) {
			continue
		}
		if platform := a.env[a.envPrefixFor(engine)+"_DNS_PLATFORM"]; platform != "" {
			result.DNSPlatforms[engine] = platform
		}
	}
	engines := make([]string, 0, len(result.DNSPlatforms))
	for engine := range result.DNSPlatforms {
		engines = append(engines, engine)
	}
	sort.Strings(engines)
	if registry, err := a.dnsRegistry(); err == nil {
		for left := 0; left < len(engines); left++ {
			for right := left + 1; right < len(engines); right++ {
				platformName := result.DNSPlatforms[engines[left]]
				if platformName != result.DNSPlatforms[engines[right]] {
					continue
				}
				platform, ok := registry.Lookup(platformName)
				if !ok {
					continue
				}
				result.DNSCredentialCompatibility = append(result.DNSCredentialCompatibility,
					application.PlanDNSCredentialCompatibility{
						Left: engines[left], Right: engines[right], Platform: platformName,
						Compatibility: platform.Compatibility(engines[left], engines[right]),
					})
			}
		}
	}
	if a.dynamicDNSProvider != "" {
		provider := a.dynamicDNSProvider
		result.DynamicDNS.Provider = &provider
		result.DynamicDNS.Automatic = a.cfg != nil && a.cfg.DynamicDNS.Provider == dynamicDNSAuto
		for _, engine := range dns.Engines() {
			if engine == dns.EngineLego || engine == a.dynamicDNSProvider || !contains(a.order, engine) {
				continue
			}
			result.DynamicDNS.SelfManaged = append(result.DynamicDNS.SelfManaged, engine)
		}
		sort.Strings(result.DynamicDNS.SelfManaged)
	}
	for _, module := range a.order {
		status := a.reg[module].Lifecycle
		if status == "developing" || status == "deprecated" {
			result.ModuleLifecycles = append(result.ModuleLifecycles,
				application.PlanModuleLifecycle{Module: module, Status: status})
		}
	}
	sort.Slice(result.ModuleLifecycles, func(left, right int) bool {
		return result.ModuleLifecycles[left].Module < result.ModuleLifecycles[right].Module
	})
	return result
}

func planResultCLIText(result application.PlanResult) string {
	var out strings.Builder
	out.WriteString(strings.Join(result.Modules, "\n"))
	out.WriteByte('\n')
	if result.IAM.Provider != nil && len(result.IAM.Consumers) != 0 {
		fmt.Fprintf(&out, "\niam provider: %s\n", *result.IAM.Provider)
		for _, consumer := range result.IAM.Consumers {
			fmt.Fprintf(&out, "  %s -> %s/%s\n", consumer.Module, *result.IAM.Provider, consumer.Interface)
		}
	}
	if len(result.DNSPlatforms) != 0 {
		engines := make([]string, 0, len(result.DNSPlatforms))
		for engine := range result.DNSPlatforms {
			engines = append(engines, engine)
		}
		sort.Strings(engines)
		out.WriteString("\ndns platforms:\n")
		for _, engine := range engines {
			fmt.Fprintf(&out, "  %s -> %s\n", engine, result.DNSPlatforms[engine])
		}
		for _, compatibility := range result.DNSCredentialCompatibility {
			fmt.Fprintf(&out, "  %s/%s credentials: %s\n",
				compatibility.Left, compatibility.Right, compatibility.Compatibility)
		}
	}
	if result.DynamicDNS.Provider != nil {
		fmt.Fprintf(&out, "\ndynamic dns: %s", *result.DynamicDNS.Provider)
		if result.DynamicDNS.Automatic {
			out.WriteString(" (auto)")
		}
		out.WriteByte('\n')
		for _, engine := range result.DynamicDNS.SelfManaged {
			fmt.Fprintf(&out, "  %s runs with its own configuration\n", engine)
		}
	}
	for _, lifecycle := range result.ModuleLifecycles {
		switch lifecycle.Status {
		case "developing":
			fmt.Fprintf(&out, "module lifecycle: %s=developing (not release quality)\n", lifecycle.Module)
		case "deprecated":
			fmt.Fprintf(&out, "module lifecycle: %s=deprecated (do not use for new deployments)\n", lifecycle.Module)
		}
	}
	modules := make([]string, 0, len(result.ModulePlans))
	for module := range result.ModulePlans {
		modules = append(modules, module)
	}
	sort.Strings(modules)
	for _, module := range modules {
		keys := make([]string, 0, len(result.ModulePlans[module]))
		for key := range result.ModulePlans[module] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		fmt.Fprintf(&out, "module plan: %s", module)
		for _, key := range keys {
			fmt.Fprintf(&out, " %s=%s", key, result.ModulePlans[module][key])
		}
		out.WriteByte('\n')
	}
	return out.String()
}
