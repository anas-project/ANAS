package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/deployment"
	"github.com/anas-project/ANAS/internal/modulesource"
	"github.com/anas-project/ANAS/internal/modulestore"
)

type workspaceModuleManagementApplication struct {
	workspace string
	events    application.EventSink
	daemon    bool
}

var _ application.ModuleManagementService = (*workspaceModuleManagementApplication)(nil)

// NewWorkspaceModuleManagementService returns the daemon boundary. It never
// accepts a caller-provided cache directory or source profile through HTTP;
// both are selected from root-managed process policy and managed config.
func NewWorkspaceModuleManagementService(workspace string, events application.EventSink) application.ModuleManagementService {
	return newWorkspaceModuleManagementService(workspace, events, true)
}

func newCLIModuleManagementService(workspace string, events application.EventSink) application.ModuleManagementService {
	return newWorkspaceModuleManagementService(workspace, events, false)
}

func newWorkspaceModuleManagementService(workspace string, events application.EventSink, daemon bool) *workspaceModuleManagementApplication {
	if strings.TrimSpace(workspace) != "" {
		workspace = filepath.Clean(workspace)
	}
	if events == nil {
		events = application.NopEventSink{}
	}
	return &workspaceModuleManagementApplication{workspace: workspace, events: events, daemon: daemon}
}

func (service *workspaceModuleManagementApplication) ListModules(ctx context.Context) (application.ModuleListResult, error) {
	if service == nil || strings.TrimSpace(service.workspace) == "" {
		return application.ModuleListResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "workspace_required", "workspace is required", nil)
	}
	configSnapshot, err := NewWorkspaceConfigService(service.workspace).GetConfig(ctx)
	if err != nil {
		return application.ModuleListResult{}, err
	}
	view, err := loadWorkspaceModuleView(service.workspace)
	if err != nil {
		return application.ModuleListResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_view_unavailable", "workspace Module view is unavailable", err)
	}
	active, err := deployment.NewReader(service.workspace).Active(ctx)
	if err != nil {
		return application.ModuleListResult{}, moduleApplicationError(application.ErrorKindInternal, "state_unreadable", "active deployment state is unavailable", err)
	}

	configured, desiredRelease := configuredModuleState(configSnapshot.Config)
	names := make(map[string]bool, len(configSnapshot.AvailableModules)+len(view.Installations))
	for _, name := range configSnapshot.AvailableModules {
		names[name] = true
	}
	for name := range view.Installations {
		names[name] = true
	}
	for name := range configured {
		names[name] = true
	}

	deployed := map[string]deployment.Module{}
	if active.ActiveDeployment != "" {
		manifest, _, manifestErr := deployment.NewReader(service.workspace).Manifest(ctx, active.ActiveDeployment)
		if manifestErr != nil {
			return application.ModuleListResult{}, moduleApplicationError(application.ErrorKindInternal, "deployment_unreadable", "active deployment is unavailable", manifestErr)
		}
		deployed = manifest.Modules
		for name := range deployed {
			names[name] = true
		}
	}

	runtimeByModule := map[string]application.ModuleRuntimeStatus{}
	if active.ActiveDeployment != "" {
		status, statusErr := NewWorkspaceQueryService(service.workspace).Status(ctx)
		if statusErr == nil {
			for _, item := range status.ModuleRuntime {
				runtimeByModule[item.Module] = item
			}
		}
	}

	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	items := make([]application.ModuleState, 0, len(ordered))
	for _, name := range ordered {
		configurationState := "available"
		if configured[name] {
			configurationState = "selected"
		} else if _, ok := deployed[name]; ok {
			configurationState = "dependency"
		}
		item := application.ModuleState{
			Name: name, ConfigurationState: configurationState,
			Runtime: "not_deployed", Health: "unknown", Dependencies: []string{}, EntryPoints: []application.ModuleManagementSurface{},
		}
		if release := desiredRelease[name]; release != "" {
			item.DesiredRelease = applicationString(release)
		}
		if installation, ok := view.Installations[name]; ok && installation.Release != "" {
			item.InstalledRelease = applicationString(installation.Release)
		}
		if module, ok := deployed[name]; ok {
			item.DeployedRelease = applicationString(formatModuleRelease(module.Version, module.Revision))
			item.Dependencies = append([]string{}, module.Dependencies...)
			for _, surface := range module.ManagementSurfaces {
				item.EntryPoints = append(item.EntryPoints, application.ModuleManagementSurface{
					ID: surface.ID, URI: surface.URI, Authentication: surface.Authentication,
				})
			}
			item.Runtime, item.Health = "unknown", "unknown"
		}
		if runtime, ok := runtimeByModule[name]; ok {
			item.Runtime, item.Health, item.Containers = runtime.Runtime, runtime.Health, runtime.Containers
		}
		items = append(items, item)
	}
	return application.ModuleListResult{
		Workspace: service.workspace, ConfigValidator: configSnapshot.Validator,
		ActiveDeployment: applicationString(active.ActiveDeployment), Modules: items,
	}, nil
}

func configuredModuleState(document application.ConfigDocument) (map[string]bool, map[string]string) {
	configured := map[string]bool{}
	releases := map[string]string{}
	modules, _ := document["modules"].(map[string]any)
	for name, raw := range modules {
		configured[name] = true
		module, _ := raw.(map[string]any)
		release, _ := module["version"].(string)
		releases[name] = strings.TrimSpace(release)
	}
	return configured, releases
}

func (service *workspaceModuleManagementApplication) CatalogModules(ctx context.Context, request application.ModuleCatalogRequest) (application.ModuleCatalogResult, error) {
	profile, _, err := resolveModuleSourceForApplication(ctx, request.Source, service.workspace, false)
	if err != nil {
		return application.ModuleCatalogResult{}, deploymentApplicationErrorFromCLI(err)
	}
	store, err := newModuleStore(moduleRequestCacheDir(request.CacheDir, service.daemon))
	if err != nil {
		return application.ModuleCatalogResult{}, deploymentApplicationErrorFromCLI(err)
	}
	result, err := store.FetchCatalog(ctx, profile)
	if err != nil {
		return application.ModuleCatalogResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_source_unavailable", "Module catalog is unavailable", err)
	}
	modules := make([]application.ModuleCatalogEntry, 0, len(result.Catalog.Modules))
	for _, module := range result.Catalog.Modules {
		modules = append(modules, application.ModuleCatalogEntry{
			Module: module.Module, Release: module.Release, Repository: module.Repository,
			Platforms: append([]string{}, module.Platforms...),
		})
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Module < modules[j].Module })
	return application.ModuleCatalogResult{
		Source: profile.Name, CatalogReference: result.Reference, CatalogDigest: result.OCIDigest,
		SourceCommit: result.Catalog.SourceCommit, Modules: modules,
	}, nil
}

func (service *workspaceModuleManagementApplication) SyncModules(ctx context.Context, request application.ModuleSyncRequest) (application.ModuleSyncResult, error) {
	if service == nil || service.workspace == "" {
		return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "workspace_required", "workspace is required", nil)
	}
	unlock, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, service.daemon)
	if err != nil {
		return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, service.lockErrorCode(), "workspace runtime lock is unavailable", err)
	}
	defer unlock()
	profile, _, err := resolveModuleSourceForApplication(ctx, request.Source, service.workspace, true)
	if err != nil {
		return application.ModuleSyncResult{}, deploymentApplicationErrorFromCLI(err)
	}
	lockPath := projectLockPath(workspaceConfigPath(service.workspace))
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return application.ModuleSyncResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("lock_invalid", "%s", err.Error()))
	}
	if len(lock.Modules) == 0 {
		return application.ModuleSyncResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("lock_missing", "config lock has no Modules; run `anas module update -w %s`", service.workspace))
	}
	store, err := newModuleStore(moduleRequestCacheDir(request.CacheDir, service.daemon))
	if err != nil {
		return application.ModuleSyncResult{}, deploymentApplicationErrorFromCLI(err)
	}
	installations := map[string]modulestore.Installation{}
	names := sortedLockModuleNames(lock)
	for index, name := range names {
		if err := ctx.Err(); err != nil {
			return application.ModuleSyncResult{}, err
		}
		record := lock.Modules[name]
		if record.OCIDigest == "" || record.ContentDigest == "" || !strings.HasPrefix(record.Source, "oci://") {
			return application.ModuleSyncResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("module_lock_local", "module %s is locked to a local bundle; module sync will not replace it with a Registry package", name))
		}
		service.progress("module-sync", int64(index), int64(len(names)))
		installation, cached, cacheErr := store.Cached(record.OCIDigest)
		if cacheErr != nil {
			return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindInternal, "module_cache_corrupt", "Module cache is corrupt", cacheErr)
		}
		if !cached {
			installation, err = store.InstallLocked(ctx, profile, record.Source, name,
				formatModuleRelease(record.Version, record.Revision), record.Repository, record.OCIDigest)
			if err != nil {
				return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_sync_failed", "Module synchronization failed", err)
			}
		}
		if installation.Name != name || installation.Repository != record.Repository || installation.ContentDigest != record.ContentDigest ||
			installation.Metadata.Version != record.Version || installation.Metadata.Revision != record.Revision {
			return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_lock_mismatch", "installed Module does not match the config lock", nil)
		}
		installations[name] = installation
	}
	view, err := store.BuildView(installations)
	if err != nil {
		return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindInternal, "module_view_failed", "build Module view", err)
	}
	if err := validateRemoteViewAgainstLock(view, lock); err != nil {
		return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_lock_mismatch", "Module view does not match the config lock", err)
	}
	if err := saveWorkspaceModuleView(service.workspace, view); err != nil {
		return application.ModuleSyncResult{}, moduleApplicationError(application.ErrorKindInternal, "write_failed", "write workspace Module view", err)
	}
	service.progress("module-sync", int64(len(names)), int64(len(names)))
	return application.ModuleSyncResult{
		Workspace: service.workspace, Source: profile.Name, ViewDigest: view.Digest,
		Modules: applicationModuleInstallations(installations), ModuleRoot: view.ModuleRoot,
	}, nil
}

func (service *workspaceModuleManagementApplication) UpdateModules(ctx context.Context, request application.ModuleUpdateRequest) (application.ModuleUpdateResult, error) {
	if service == nil || service.workspace == "" {
		return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "workspace_required", "workspace is required", nil)
	}
	unlock, err := acquireRuntimeLockForApplication(ctx, stateDir(service.workspace), service.events, service.daemon)
	if err != nil {
		return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, service.lockErrorCode(), "workspace runtime lock is unavailable", err)
	}
	defer unlock()
	configPath := workspaceConfigPath(service.workspace)
	if err := validateManagedConfig(service.workspace, configPath); err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_not_managed", "%s", err.Error()))
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "%s", err.Error()))
	}
	profile, _, err := resolveModuleSourceForApplication(ctx, request.Source, service.workspace, true)
	if err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(err)
	}
	store, err := newModuleStore(moduleRequestCacheDir(request.CacheDir, service.daemon))
	if err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(err)
	}
	catalogResult, err := store.FetchCatalog(ctx, profile)
	if err != nil {
		return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_source_unavailable", "Module catalog is unavailable", err)
	}
	lockPath := projectLockPath(configPath)
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("lock_invalid", "%s", err.Error()))
	}
	targets := map[string]bool{}
	if len(request.Modules) == 0 {
		for _, name := range cfg.Modules.Order {
			targets[name] = true
		}
	} else {
		for _, raw := range request.Modules {
			name := canonicalConfigName(raw)
			if raw != name {
				return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "module_name_invalid", "Module name is not canonical", nil)
			}
			if _, exists := cfg.Modules.Values[name]; !exists {
				return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "module_not_selected", "Module is not selected in config.yml", nil)
			}
			targets[name] = true
		}
	}
	installations := map[string]modulestore.Installation{}
	for index, catalogModule := range catalogResult.Catalog.Modules {
		if err := ctx.Err(); err != nil {
			return application.ModuleUpdateResult{}, err
		}
		release, expectedDigest := catalogModule.Release, ""
		if existing, ok := lock.Modules[catalogModule.Module]; ok && existing.OCIDigest != "" && !targets[catalogModule.Module] {
			release, expectedDigest = formatModuleRelease(existing.Version, existing.Revision), existing.OCIDigest
		} else if selected, ok := cfg.Modules.Values[catalogModule.Module]; ok && strings.TrimSpace(selected.Version) != "" {
			release = strings.TrimSpace(selected.Version)
			if _, parseErr := modulestore.ParseRelease(release); parseErr != nil {
				return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(preconditionErrorf("config_invalid", "modules.%s.version: %s", catalogModule.Module, parseErr.Error()))
			}
		}
		service.progress("module-update", int64(index), int64(len(catalogResult.Catalog.Modules)))
		if expectedDigest != "" {
			if cached, ok, cacheErr := store.Cached(expectedDigest); cacheErr != nil {
				return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInternal, "module_cache_corrupt", "Module cache is corrupt", cacheErr)
			} else if ok {
				installations[catalogModule.Module] = cached
				continue
			}
			existing := lock.Modules[catalogModule.Module]
			installation, installErr := store.InstallLocked(ctx, profile, existing.Source, catalogModule.Module, release, existing.Repository, expectedDigest)
			if installErr != nil {
				return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_update_failed", "Module update failed", installErr)
			}
			installations[catalogModule.Module] = installation
			continue
		}
		installation, installErr := store.Install(ctx, profile, catalogModule.Module, release, "")
		if installErr != nil {
			return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_update_failed", "Module update failed", installErr)
		}
		installations[catalogModule.Module] = installation
	}
	for name, record := range lock.Modules {
		if _, present := installations[name]; present || targets[name] {
			continue
		}
		if record.OCIDigest == "" || record.ContentDigest == "" || !strings.HasPrefix(record.Source, "oci://") {
			continue
		}
		installation, cached, cacheErr := store.Cached(record.OCIDigest)
		if cacheErr != nil {
			return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInternal, "module_cache_corrupt", "Module cache is corrupt", cacheErr)
		}
		if !cached {
			installation, err = store.InstallLocked(ctx, profile, record.Source, name,
				formatModuleRelease(record.Version, record.Revision), record.Repository, record.OCIDigest)
			if err != nil {
				return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_update_failed", "Module update failed", err)
			}
		}
		installations[name] = installation
	}
	for target := range targets {
		if _, ok := installations[target]; !ok {
			return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "module_not_found", fmt.Sprintf("Module %s is not present in source %s", target, profile.Name), nil)
		}
	}
	provisional, err := store.BuildView(installations)
	if err != nil {
		return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInternal, "module_view_failed", "build provisional Module view", err)
	}
	resolvedOrder, updatedLock, err := resolveRemoteModuleLock(service.workspace, configPath, provisional.ModuleRoot, lock)
	if err != nil {
		return application.ModuleUpdateResult{}, deploymentApplicationErrorFromCLI(err)
	}
	resolvedInstallations := map[string]modulestore.Installation{}
	for _, name := range resolvedOrder {
		resolvedInstallations[name] = installations[name]
	}
	// Keep the complete catalog view available to the configuration and Module
	// management surfaces. The lock remains the exact selected dependency graph;
	// disabled catalog entries do not become deployment truth merely by existing
	// in the immutable view.
	view := provisional
	for _, name := range resolvedOrder {
		installation := resolvedInstallations[name]
		record := updatedLock.Modules[name]
		record.Source, record.OCIDigest = installation.ImmutableReference, installation.OCIDigest
		record.ContentDigest, record.Repository = installation.ContentDigest, installation.Repository
		updatedLock.Modules[name] = record
	}
	if err := commitWorkspaceModuleState(lockPath, service.workspace, updatedLock, view); err != nil {
		return application.ModuleUpdateResult{}, moduleApplicationError(application.ErrorKindInternal, "write_failed", "write Module lock and view", err)
	}
	service.progress("module-update", int64(len(catalogResult.Catalog.Modules)), int64(len(catalogResult.Catalog.Modules)))
	return application.ModuleUpdateResult{
		Workspace: service.workspace, Source: profile.Name, ViewDigest: view.Digest,
		Modules: applicationModuleInstallations(resolvedInstallations), LockPath: lockPath, ModuleRoot: view.ModuleRoot,
	}, nil
}

func (service *workspaceModuleManagementApplication) SetModuleEnabled(ctx context.Context, request application.ModuleEnabledRequest, observer application.ConfigCommitObserver) (application.ModuleEnabledResult, error) {
	name := canonicalConfigName(request.Module)
	if request.Module == "" || request.Module != name {
		return application.ModuleEnabledResult{}, moduleApplicationError(application.ErrorKindInvalidArgument, "module_name_invalid", "Module name is not canonical", nil)
	}
	configService := NewWorkspaceConfigService(service.workspace)
	snapshot, err := configService.GetConfig(ctx)
	if err != nil {
		return application.ModuleEnabledResult{}, err
	}
	if request.ExpectedConfigValidator == "" || request.ExpectedConfigValidator != snapshot.Validator {
		return application.ModuleEnabledResult{}, moduleApplicationError(application.ErrorKindFailedPrecondition, "config_precondition_failed", "workspace configuration changed before Module action", nil)
	}
	available := false
	for _, candidate := range snapshot.AvailableModules {
		if candidate == name {
			available = true
			break
		}
	}
	if !available {
		return application.ModuleEnabledResult{}, moduleApplicationError(application.ErrorKindNotFound, "module_not_found", "Module is not available in the workspace Module view", nil)
	}
	document, err := cloneApplicationConfigDocument(snapshot.Config)
	if err != nil {
		return application.ModuleEnabledResult{}, moduleApplicationError(application.ErrorKindInternal, "config_unavailable", "clone managed configuration", err)
	}
	modules, _ := document["modules"].(map[string]any)
	if request.Enabled {
		if modules == nil {
			modules = map[string]any{}
			document["modules"] = modules
		}
		if _, exists := modules[name]; !exists {
			modules[name] = map[string]any{}
		}
	} else if modules != nil {
		delete(modules, name)
		if len(modules) == 0 {
			delete(document, "modules")
		}
	}
	result, err := configService.PutConfig(ctx, application.ConfigPutRequest{
		OperationID:  request.OperationID,
		Candidate:    application.ConfigCandidate{Document: document, Sensitive: map[string]application.ConfigSensitiveMutation{}},
		Precondition: application.ConfigPreconditionMatch, ExpectedValidator: request.ExpectedConfigValidator,
	}, observer)
	if err != nil {
		return application.ModuleEnabledResult{}, err
	}
	return application.ModuleEnabledResult{
		Workspace: service.workspace, Module: name, Enabled: request.Enabled,
		PreviousValidator: result.PreviousValidator, ConfigValidator: result.Validator,
		Changes: append([]application.ConfigChange{}, result.Changes...),
	}, nil
}

func resolveModuleSourceForApplication(ctx context.Context, source, workspace string, workspaceLocked bool) (modulesource.Profile, string, error) {
	resolvedWorkspace := ""
	if strings.TrimSpace(source) == "" && strings.TrimSpace(workspace) != "" {
		resolvedWorkspace = filepath.Clean(workspace)
		if !workspaceLocked {
			unlock, err := acquireWorkspaceConfigReadLock(ctx, stateDir(resolvedWorkspace))
			if err != nil {
				return modulesource.Profile{}, "", failuref("lock_failed", "%s", err.Error())
			}
			defer unlock()
		}
		cfg, err := config.Load(workspaceConfigPath(resolvedWorkspace))
		if err != nil {
			return modulesource.Profile{}, "", preconditionErrorf("config_invalid", "%s", err.Error())
		}
		source = cfg.ModuleSource
	}
	if strings.TrimSpace(source) == "" {
		source = modulesource.InstalledDefaultName("")
	}
	profile, ok := lookupModuleSourceProfile(source)
	if !ok {
		return modulesource.Profile{}, resolvedWorkspace, usageErrorf("--source must be official, official-cn, or cn")
	}
	return profile, resolvedWorkspace, nil
}

func applicationModuleInstallations(installations map[string]modulestore.Installation) []application.ModuleInstallation {
	names := make([]string, 0, len(installations))
	for name := range installations {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]application.ModuleInstallation, 0, len(names))
	for _, name := range names {
		installation := installations[name]
		result = append(result, application.ModuleInstallation{
			Name: name, Release: installation.Release, OCIDigest: installation.OCIDigest, ContentDigest: installation.ContentDigest,
		})
	}
	return result
}

func cloneApplicationConfigDocument(document application.ConfigDocument) (application.ConfigDocument, error) {
	body, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var clone application.ConfigDocument
	if err := json.Unmarshal(body, &clone); err != nil {
		return nil, err
	}
	return clone, nil
}

func (service *workspaceModuleManagementApplication) progress(phase string, current, total int64) {
	service.events.Progress(application.ProgressEvent{Phase: phase, Current: current, Total: total, Unit: "modules"})
}

func (service *workspaceModuleManagementApplication) lockErrorCode() string {
	if service != nil && service.daemon {
		return "runtime_lock_unavailable"
	}
	return "lock_failed"
}

func moduleRequestCacheDir(cacheDir string, daemon bool) string {
	if daemon {
		return ""
	}
	return cacheDir
}

func moduleApplicationError(kind application.ErrorKind, code, message string, cause error) error {
	if cause != nil {
		if _, ok := application.ErrorOf(cause); ok {
			return cause
		}
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			return cause
		}
	}
	return &application.Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

func applicationString(value string) *string {
	if value == "" {
		return nil
	}
	copy := value
	return &copy
}
