package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/deployment"
	"gopkg.in/yaml.v3"
)

const (
	deploymentAPIVersion  = deployment.ManifestAPIVersion
	activeStateVersion    = deployment.StateAPIVersion
	runtimeLockRetryDelay = 25 * time.Millisecond
)

// The json tags are not decoration: `deployments inspect --json` emits these
// structs directly, and without them the document would carry Go field names
// while the YAML on disk carried snake_case ones. Two spellings of the same
// manifest is exactly the kind of thing a caller ends up hard-coding around.
type deploymentManifest = deployment.Manifest
type deploymentModule = deployment.Module
type deploymentResource = deployment.Resource
type deploymentCredential = deployment.Credential
type deploymentCredentialProjection = deployment.CredentialProjection
type deploymentSetting = deployment.Setting
type deploymentSnapshotPolicy = deployment.SnapshotPolicy
type activeDeploymentState = deployment.ActiveState
type deploymentState = deployment.State

type deploymentIndex struct {
	APIVersion  string            `yaml:"api_version"`
	GeneratedAt string            `yaml:"generated_at"`
	Deployments []deploymentState `yaml:"deployments"`
}

type prepareOptions struct {
	workspace                    string
	base                         string
	cfgPath                      string
	moduleRoot                   string
	verbose                      bool
	updateLock                   bool
	context                      context.Context
	events                       application.EventSink
	restrictedProcessEnvironment bool
	// modules narrows which images `build` builds. Empty means all of them.
	modules []string
}

type runtimeRecoveryOptions struct {
	ctx                          context.Context
	events                       application.EventSink
	restrictedProcessEnvironment bool
}

func projectLockPath(configPath string) string {
	ext := filepath.Ext(configPath)
	return strings.TrimSuffix(configPath, ext) + ".lock.yml"
}

func parsePrepareOptions(name string, args []string) (prepareOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var out prepareOptions
	fs.StringVar(&out.cfgPath, "c", "", "config file")
	fs.StringVar(&out.cfgPath, "config", "", "config file")
	fs.StringVar(&out.workspace, "w", "", "workspace path")
	fs.StringVar(&out.workspace, "workspace", "", "workspace path")
	fs.StringVar(&out.moduleRoot, "module-root", "", "directory containing module bundles")
	fs.StringVar(&out.moduleRoot, "root", "", "project root or module bundle directory")
	fs.BoolVar(&out.verbose, "verbose", false, "debug logging")
	fs.BoolVar(&out.updateLock, "update-lock", false, "create or update the config lock")
	registerJSONFlag(fs)
	// parseInterspersed for the same reason runActive uses it: `anas build lego
	// -w <workspace>` must see the flag that follows the module name, rather than
	// silently building in whatever workspace the cwd happens to name.
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return out, usageErrorf("%s", err.Error())
	}
	// `build` accepts module names: rendering the whole deployment is cheap and
	// has to happen anyway, but building every image to pick up a change in one
	// Dockerfile is minutes of work for nothing. `render` takes none, because a
	// partial rendering is not a deployment.
	if len(positional) != 0 && name != "build" {
		return out, usageErrorf("usage: anas %s [-w <workspace>] [-c config.yml] [--update-lock] [--json]", name)
	}
	out.modules = positional
	workspace, err := resolveWorkspace(out.workspace)
	if err != nil {
		return out, usageErrorf("%s", err.Error())
	}
	out.workspace = workspace
	out.base = stateDir(workspace)
	// A workspace owns its config, so -c is only needed to point at one that
	// lives elsewhere.
	out.cfgPath = absolutePath(configPathFor(workspace, out.cfgPath))
	return out, nil
}

// finalizePrepareOptions reads the managed configuration and workspace Module
// view only after the caller holds the runtime lock. Keeping these checks out
// of flag parsing prevents a config PUT transaction from being observed
// between its config, Secret Store, and managed-digest publishes.
func finalizePrepareOptions(out prepareOptions) (prepareOptions, error) {
	if !exists(out.cfgPath) {
		return out, preconditionErrorf("config_missing", "config %s does not exist", out.cfgPath)
	}
	if err := validateManagedConfig(out.workspace, out.cfgPath); err != nil {
		return out, preconditionErrorf("config_not_managed", "%s", err.Error())
	}
	root, err := locateModuleRootForWorkspace(out.moduleRoot, out.workspace)
	if err != nil {
		return out, preconditionErrorf("module_root_missing", "%s", err.Error())
	}
	out.moduleRoot = absolutePath(root)
	return out, nil
}

func runLock(args []string, jsonMode bool) error {
	opts, err := parsePrepareOptions("lock", args)
	if err != nil {
		return err
	}
	unlock, err := acquireRuntimeLock(opts.base)
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	opts, err = finalizePrepareOptions(opts)
	if err != nil {
		return err
	}
	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	reg, err := loadRegistryDir(opts.moduleRoot)
	if err != nil {
		return preconditionErrorf("module_root_invalid", "%s", err.Error())
	}
	if err := validateConfigRuntimeKeyCollisions(opts.cfgPath, reg); err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	privateStore, err := loadSecretStore(opts.base)
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if err := rejectUnimportedConfigSecrets(opts.cfgPath, reg, privateStore.values); err != nil {
		return preconditionErrorf("config_requires_import", "%s", err.Error())
	}
	lockPath := absolutePath(projectLockPath(opts.cfgPath))
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return preconditionErrorf("lock_invalid", "%s", err.Error())
	}
	contracts, err := loadContractRegistry(opts.moduleRoot)
	if err != nil {
		return preconditionErrorf("contract_root_invalid", "%s", err.Error())
	}
	a := &app{workspace: opts.workspace, cfg: cfg, cfgPath: opts.cfgPath, reg: reg, contracts: contracts, lock: lock, resolvedBindings: map[string]map[string]string{}}
	a.env, a.envOwner = configBaseEnvWithRegistry(cfg, reg)
	a.base = opts.base
	if err := a.loadImportedSecrets(); err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if err := a.validateContractRegistry(); err != nil {
		return preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return preconditionErrorf("resolution_failed", "%s", err.Error())
	}
	if err := a.validateVersions(lock); err != nil {
		return preconditionErrorf("version_conflict", "%s", err.Error())
	}
	if err := a.updateModuleLock(lock, true); err != nil {
		return failuref("lock_update_failed", "%s", err.Error())
	}
	lock.Snapshot, err = resolveSnapshotLock(opts.workspace, cfg)
	if err != nil {
		return preconditionErrorf("snapshot_policy_invalid", "%s", err.Error())
	}
	a.applyModuleDefaults()
	if err := a.validateModules(); err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	if err := saveModuleLockFile(lockPath, lock); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": opts.workspace, "config": opts.cfgPath, "lock_path": lockPath,
			"module_order": a.order, "modules": moduleLockDocument(lock),
			"contracts": contractLockDocument(lock),
			"iam":       a.iamPlanDocument(), "capability_bindings": cloneNestedMap(a.resolvedBindings),
			"snapshot": lock.Snapshot,
		})
	}
	fmt.Println(lockPath)
	return nil
}

// moduleLockDocument shapes the lock for JSON. The lock file's own YAML shape is
// an on-disk format that may change; this is the contract's view of it.
func moduleLockDocument(lock *moduleLock) []map[string]any {
	if lock == nil {
		return []map[string]any{}
	}
	names := make([]string, 0, len(lock.Modules))
	for name := range lock.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		record := lock.Modules[name]
		out = append(out, map[string]any{
			"name": name, "version": record.Version,
			"revision": record.Revision, "app_version": record.AppVersion, "digest": record.Digest,
			"source": record.Source, "repository": record.Repository,
			"oci_digest": record.OCIDigest, "content_digest": record.ContentDigest,
		})
	}
	return out
}

func contractLockDocument(lock *moduleLock) []map[string]any {
	if lock == nil {
		return []map[string]any{}
	}
	names := make([]string, 0, len(lock.Contracts))
	for name := range lock.Contracts {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		record := lock.Contracts[name]
		out = append(out, map[string]any{"name": name, "version": record.Version, "digest": record.Digest})
	}
	return out
}

func runPlan(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	moduleRoot := fs.String("module-root", "", "directory containing module bundles")
	rootAlias := fs.String("root", "", "project root or module bundle directory")
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	if err := fs.Parse(args); err != nil {
		return usageErrorf("%s", err.Error())
	}
	if fs.NArg() != 0 {
		return usageErrorf("usage: anas plan [-w workspace] [-c config.yml] [--module-root modules] [--json]")
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	configPath := absolutePath(configPathFor(workspace, *cfgPath))
	explicit := *moduleRoot
	if explicit == "" {
		explicit = *rootAlias
	}
	service := newCLIDeploymentPlanService(workspace, configPath, explicit, moduleCommandCLISink{jsonMode: jsonMode})
	result, err := service.Plan(context.Background(), application.PlanRequest{})
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(planResultCLIMap(result))
	}
	fmt.Print(planResultCLIText(result))
	return nil
}

func runPrepare(action string, args []string, jsonMode bool) error {
	opts, err := parsePrepareOptions(action, args)
	if err != nil {
		return err
	}
	announceWorkspace(opts.workspace)
	unlock, err := acquireRuntimeLock(opts.base)
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	opts, err = finalizePrepareOptions(opts)
	if err != nil {
		return err
	}
	id, err := materializeDeployment(opts, action == "build", jsonMode)
	if err != nil {
		return err
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": opts.workspace, "config": opts.cfgPath,
			"deployment_id": id, "built": action == "build",
			"deployment_path": filepath.Join(opts.base, "deployments", id),
		})
	}
	fmt.Println(id)
	return nil
}

func materializeDeployment(opts prepareOptions, build, jsonMode bool) (string, error) {
	if err := ensureRuntimeLayout(opts.base); err != nil {
		return "", failuref("layout_failed", "%s", err.Error())
	}
	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return "", preconditionErrorf("config_invalid", "%s", err.Error())
	}
	reg, err := loadRegistryDir(opts.moduleRoot)
	if err != nil {
		return "", preconditionErrorf("module_root_invalid", "%s", err.Error())
	}
	if err := validateConfigRuntimeKeyCollisions(opts.cfgPath, reg); err != nil {
		return "", preconditionErrorf("config_invalid", "%s", err.Error())
	}
	privateStore, err := loadSecretStore(opts.base)
	if err != nil {
		return "", preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if err := rejectUnimportedConfigSecrets(opts.cfgPath, reg, privateStore.values); err != nil {
		return "", preconditionErrorf("config_requires_import", "%s", err.Error())
	}
	contracts, err := loadContractRegistry(opts.moduleRoot)
	if err != nil {
		return "", preconditionErrorf("contract_root_invalid", "%s", err.Error())
	}
	lockPath := projectLockPath(opts.cfgPath)
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return "", preconditionErrorf("lock_invalid", "%s", err.Error())
	}
	if len(lock.Modules) == 0 && !opts.updateLock {
		return "", preconditionErrorf("lock_missing",
			"missing config lock %s; run `anas lock -c %s` or pass --update-lock", lockPath, opts.cfgPath)
	}

	id, err := newDeploymentID()
	if err != nil {
		return "", failuref("id_generation_failed", "%s", err.Error())
	}
	stagingRoot := filepath.Join(opts.base, "staging", id)
	finalRoot := filepath.Join(opts.base, "deployments", id)
	stagingModules := filepath.Join(stagingRoot, "modules")
	finalModules := filepath.Join(finalRoot, "modules")
	if exists(stagingRoot) || exists(finalRoot) {
		return "", failuref("deployment_id_collision", "deployment id collision %s", id)
	}
	// Every failure between here and the promote below leaves a partially built
	// artifact under staging/, and nothing else ever sweeps it: the runtime lock
	// cleans snapshot temporaries and container transactions, not this tree. A
	// deployment that fails on a bad credential or an unreachable registry would
	// otherwise leave its whole rendered tree, secrets included, on disk forever.
	// Removal is safe even after sealDeployment, which clears write bits on
	// regular files but not on the directories that actually govern unlink.
	promoted := false
	defer func() {
		if !promoted {
			_ = os.RemoveAll(stagingRoot)
		}
	}()
	a := &app{
		workspace: opts.workspace,
		base:      opts.base, cfgPath: opts.cfgPath, verbose: opts.verbose, jsonMode: jsonMode,
		reg: reg, contracts: contracts, cfg: cfg, lock: lock, artifactRoot: finalModules,
		commandContext: opts.context, events: opts.events,
		restrictedProcessEnvironment: opts.restrictedProcessEnvironment,
		resolvedBindings:             map[string]map[string]string{},
	}
	a.env, a.envOwner = configBaseEnvWithRegistry(cfg, reg)
	a.env["ANAS_DEPLOYMENT_ID"] = id
	a.setEnvOwner("ANAS_DEPLOYMENT_ID", runnerScope)
	if err := a.loadImportedSecrets(); err != nil {
		return "", preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	a.applyWorkspaceEnv()
	if err := a.validateContractRegistry(); err != nil {
		return "", preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return "", preconditionErrorf("resolution_failed", "%s", err.Error())
	}
	total := int64(len(a.order))
	a.applyModuleDefaults()
	if opts.updateLock {
		if err := a.validateVersions(lock); err != nil {
			return "", preconditionErrorf("version_conflict", "%s", err.Error())
		}
		if err := a.updateModuleLock(lock, true); err != nil {
			return "", failuref("lock_update_failed", "%s", err.Error())
		}
		lock.Snapshot, err = resolveSnapshotLock(opts.workspace, cfg)
		if err != nil {
			return "", preconditionErrorf("snapshot_policy_invalid", "%s", err.Error())
		}
	} else if err := validateLockedResolution(a, lock); err != nil {
		return "", preconditionErrorf("lock_stale", "%s", err.Error())
	}
	// An ordinary operation must establish bundle trust before executing an
	// opt-in Hook. --update-lock is the explicit trust transition, but its new
	// lock is still kept in memory until validation succeeds.
	if err := a.validateModules(); err != nil {
		return "", preconditionErrorf("config_invalid", "%s", err.Error())
	}
	if opts.updateLock {
		if err := saveModuleLockFile(lockPath, lock); err != nil {
			return "", failuref("write_failed", "%s", err.Error())
		}
	}
	if err := os.MkdirAll(stagingModules, 0700); err != nil {
		return "", failuref("mkdir_failed", "%s", err.Error())
	}
	if err := a.materializeDNSCredentials(); err != nil {
		return "", preconditionErrorf("dns_credentials_invalid", "%s", err.Error())
	}
	a.reportDynamicDNSOverlaps()
	secrets, err := loadSecretStore(opts.base)
	if err != nil {
		return "", preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	a.secrets = secrets
	a.sensitiveKeys = nil
	if err := a.materializeResourceSecrets(); err != nil {
		return "", preconditionErrorf("resource_invalid", "%s", err.Error())
	}
	if err := a.materializeLocalAccounts(); err != nil {
		return "", preconditionErrorf("local_admin_invalid", "%s", err.Error())
	}
	a.progress(jsonMode, "calculate", 0, total, "modules")
	// Part of the calculate stage, and reported as such: it is the derivation
	// every module hook reads from, so a host it cannot describe is a failure of
	// the same step rather than a new contract code.
	if err := a.applyHostNetwork(); err != nil {
		return "", failuref("calculate_failed", "%s", err.Error())
	}
	if err := a.calculate(); err != nil {
		return "", failuref("calculate_failed", "%s", err.Error())
	}
	if err := a.prepareDeploymentCredentials(); err != nil {
		return "", preconditionErrorf("credential_inventory_invalid", "%s", err.Error())
	}
	if err := a.secrets.Save(); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	if err := a.localAdmins.Save(); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	a.progress(jsonMode, "render", 0, total, "modules")
	if err := a.renderAll(stagingModules); err != nil {
		return "", failuref("render_failed", "%s", err.Error())
	}
	a.progress(jsonMode, "render", total, total, "modules")

	if build {
		cli, err := detectComposeForExecution(opts.context, opts.restrictedProcessEnvironment)
		if err != nil {
			return "", preconditionErrorf("compose_missing", "%s", err.Error())
		}
		a.compose = cli
		selection, err := selectModules(a, opts.modules)
		if err != nil {
			return "", usageErrorf("%s", err.Error())
		}
		total = int64(len(selection))
		done := int64(0)
		if err := a.eachOf(stagingModules, selection, func(run moduleRun) error {
			done++
			a.progress(jsonMode, "build-images", done, total, "modules")
			if run.mod.RuntimeType != "compose" {
				return nil
			}
			args := append([]string{"build"}, run.services...)
			return a.runCompose(run.dir, run.mod.Name, run.mod.ComposeFile, run.env, args...)
		}); err != nil {
			return "", failuref("build_failed", "%s", err.Error())
		}
	}

	manifest, err := buildDeploymentManifest(a, id, opts.cfgPath, build)
	if err != nil {
		return "", failuref("manifest_failed", "%s", err.Error())
	}
	if err := writeYAMLAtomic(filepath.Join(stagingRoot, "deployment.yml"), manifest, 0600); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	if err := saveModuleLockFile(filepath.Join(stagingRoot, "lock.yml"), lock); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	if err := copyFileMode(opts.cfgPath, deploymentConfigSourcePath(stagingRoot), 0600); err != nil {
		return "", failuref("write_failed", "preserve the config this deployment was built from: %v", err)
	}
	a.progress(jsonMode, "seal", 0, 0, "deployments")
	if err := sealDeployment(stagingRoot); err != nil {
		return "", failuref("seal_failed", "%s", err.Error())
	}
	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return "", failuref("promote_failed", "%s", err.Error())
	}
	promoted = true
	state := deploymentState{
		APIVersion: activeStateVersion, ID: id, Status: "ready",
		CreatedAt: manifest.CreatedAt,
	}
	if err := saveDeploymentState(opts.base, state); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	return id, nil
}

func validateLockedResolution(a *app, lock *moduleLock) error {
	if err := validateLockedSnapshot(a.cfg, lock); err != nil {
		return err
	}
	if err := validateLockedModuleBundles(a, lock); err != nil {
		return err
	}
	usedContracts := map[string]bool{}
	for _, name := range a.order {
		for _, dependency := range a.reg[name].RequiresContracts {
			usedContracts[dependency.Name] = true
		}
		for _, provider := range a.reg[name].ContractProviders {
			usedContracts[provider.Name] = true
		}
	}
	for name := range usedContracts {
		installed, ok := a.contracts[name]
		if !ok {
			return fmt.Errorf("installed contract %s is missing", name)
		}
		locked, ok := lock.Contracts[name]
		if !ok || locked.Version != installed.Version || locked.Digest != installed.Digest {
			return fmt.Errorf("contract %s does not match the config lock; run anas lock", name)
		}
	}
	// Switching IAM or protocol rewrites client registrations and callback
	// URLs, so it must go through an explicit lock update rather than being
	// applied silently by an ordinary start.
	if a.iamProvider != "" {
		locked := ""
		if lock.IAM != nil {
			locked = lock.IAM.Provider
		}
		if locked != a.iamProvider {
			return fmt.Errorf("iam.provider resolved to %s but lock records %q; run anas lock", a.iamProvider, locked)
		}
	}
	for module, bindings := range a.resolvedBindings {
		for capability, provider := range bindings {
			if lock.Bindings[module][capability] != provider {
				return fmt.Errorf("%s capability %s resolved to %s but lock records %s; run anas lock", module, capability,
					a.resolvedBindingValueForError(module, capability, provider),
					a.resolvedBindingValueForError(module, capability, lock.Bindings[module][capability]))
			}
		}
	}
	return nil
}

// validateLockedModuleBundles is the executable-code trust gate shared by
// plan/config-set/materialization. It intentionally runs before any Module
// Hook; contract/binding checks remain in validateLockedResolution once the
// caller has the full contract registry.
func validateLockedModuleBundles(a *app, lock *moduleLock) error {
	for _, name := range a.order {
		record, ok := lock.Modules[name]
		if !ok {
			// "run anas lock" is actionable only once the operator knows why the
			// module is here at all. Turning on a database console and being told
			// the lock is missing an authentication proxy reads like a bug unless
			// the sentence names the switch that pulled it in.
			if reason := a.conditionalPullReason(name); reason != "" {
				return &lockedResolutionError{message: fmt.Sprintf(
					"config lock has no module %q; it entered this deployment because %s; run anas lock", name, reason)}
			}
			return &lockedResolutionError{message: fmt.Sprintf("config lock has no module %q; run anas lock", name)}
		}
		if record.Version != a.reg[name].Version || record.Revision != a.reg[name].Revision {
			return &lockedResolutionError{message: fmt.Sprintf("module %q is locked at %s-r%d but source provides %s-r%d; run anas lock to update explicitly", name, record.Version, record.Revision, a.reg[name].Version, a.reg[name].Revision)}
		}
		digest, err := moduleBundleDigest(a.reg[name].SourceDir)
		if err != nil {
			return err
		}
		if record.Digest != digest {
			return &lockedResolutionError{message: fmt.Sprintf("module %q bundle digest does not match config lock; run anas lock to update explicitly", name)}
		}
	}
	return nil
}

type lockedResolutionError struct{ message string }

func (e *lockedResolutionError) Error() string { return e.message }

func buildDeploymentManifest(a *app, id, cfgPath string, imagesBuilt bool) (*deploymentManifest, error) {
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return nil, err
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: id,
		CreatedAt:         time.Now().UTC().Format(time.RFC3339),
		ImagesBuilt:       imagesBuilt,
		BuildAcceleration: strings.EqualFold(strings.TrimSpace(a.env["CHINESE_BUILD_SPEEDUP"]), "true"),
		ModuleOrder:       append([]string{}, a.order...), Bindings: cloneNestedMap(a.resolvedBindings),
		Modules: map[string]deploymentModule{}, Settings: map[string]deploymentSetting{},
		Credentials: append([]deploymentCredential{}, a.credentials...),
		Snapshot: deploymentSnapshotPolicy{
			Source: strings.TrimSpace(a.cfg.Rollback.Snapshot.Source),
			Root:   strings.TrimSpace(a.cfg.Rollback.Snapshot.Root),
		},
	}
	if a.lock != nil && a.lock.Snapshot != nil {
		manifest.Snapshot.Backend = a.lock.Snapshot.Backend
		manifest.Snapshot.KeepAuto = a.lock.Snapshot.KeepAuto
	} else {
		manifest.Snapshot.Backend = strings.ToLower(strings.TrimSpace(a.cfg.Rollback.Snapshot.Backend))
		manifest.Snapshot.KeepAuto, _ = a.cfg.Rollback.Snapshot.KeepAuto.Value()
	}
	// The data location is fixed by the workspace layout, so the manifest no
	// longer records it: a manifest that named an absolute path would pin the
	// deployment to the machine it was rendered on.
	if manifest.Snapshot.Source == "" && a.workspace != "" {
		manifest.Snapshot.Source = dataDir(a.workspace)
	}
	if manifest.Snapshot.Source != "" {
		if absolute, err := filepath.Abs(manifest.Snapshot.Source); err == nil {
			manifest.Snapshot.Source = absolute
		}
	}
	if manifest.Snapshot.Root != "" {
		if absolute, err := filepath.Abs(manifest.Snapshot.Root); err == nil {
			manifest.Snapshot.Root = absolute
		}
	}
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	manifest.ConfigFingerprint = fmt.Sprintf("sha256:%x", sha256.Sum256(b))
	for _, name := range a.order {
		mod := a.reg[name]
		commandExecutor, err := frozenModuleCommandExecutor(mod, filepath.Join(a.base, "staging", id, "modules", name))
		if err != nil {
			return nil, err
		}
		digest, err := normalizedModuleDigest(
			filepath.Join(a.base, "staging", id, "modules", name),
			filepath.Join(a.base, "deployments", id),
		)
		if err != nil {
			return nil, fmt.Errorf("digest rendered module %s: %w", name, err)
		}
		manifest.Modules[name] = deploymentModule{
			Name: name, Version: mod.Version, Revision: mod.Revision, AppVersion: mod.AppVersion,
			Lifecycle:          mod.Lifecycle,
			ArtifactDeployment: id, RenderDigest: digest,
			DataBreaking: cloneStringListPointer(mod.DataBreaking),
			RuntimeType:  mod.RuntimeType, ComposeFile: mod.ComposeFile,
			Hook:           mod.Hook,
			ValidationPlan: cloneMap(a.modulePlans[name]),
			EnvPrefix:      mod.EnvPrefix,
			Consumes:       append([]string{}, mod.Consumes...),
			Dependencies:   append([]string{}, a.deps[name]...),
			UseHostLAN:     mod.UseHostLAN, Changes: mod.Changes,
			Providers:           cloneContractProviders(mod.ContractProviders),
			LocalAccounts:       append([]LocalAccount{}, mod.LocalAccounts...),
			CredentialProviders: cloneCredentialProviders(mod.CredentialProviders),
			CredentialConsumers: cloneCredentialConsumers(mod.CredentialConsumers),
			CommandExecutor:     commandExecutor,
			Commands:            cloneModuleCommands(mod.Commands),
		}
	}
	for _, request := range a.resourceRequests {
		resource := deploymentResource{
			Consumer: request.Consumer, ID: request.ID, Contract: request.Contract, ContractVersion: request.ContractVersion,
			Provider: request.Provider, Interface: request.Interface,
			Spec: request.Spec,
		}
		if request.Contract == "relational_database" {
			resource.SecretKey = request.SecretKey
		} else {
			resource.CredentialSecretKey = request.SecretKey
		}
		manifest.Resources = append(manifest.Resources, resource)
	}
	for path, value := range settings {
		target := targetForSettingPath(path, a.reg)
		policy := policyForTarget(target, a.reg)
		manifest.Settings[path] = deploymentSetting{
			Fingerprint: hashSetting(value), Module: target.Module,
			Parameter: target.Parameter, Effect: policy.Effect, Apply: policy.Apply,
		}
	}
	inheritUnchangedModuleArtifacts(a.base, manifest)
	return manifest, nil
}

// normalizedModuleDigest identifies the effective rendered module while
// ignoring the deployment directory embedded in bind-mount paths. Without
// this normalization, two byte-for-byte equivalent renders under different
// immutable deployment IDs would look changed and Compose would be invoked
// for every module on every apply.
func normalizedModuleDigest(root, deploymentRoot string) (string, error) {
	h := sha256.New()
	paths := []string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, path := range paths {
		rel, _ := filepath.Rel(root, path)
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		body = bytes.ReplaceAll(body, []byte(deploymentRoot), []byte("$ANAS_DEPLOYMENT"))
		runtimeState := filepath.ToSlash(filepath.Join("runtime-state", "deployments", filepath.Base(deploymentRoot)))
		body = bytes.ReplaceAll(body, []byte(runtimeState), []byte("runtime-state/deployments/$ANAS_DEPLOYMENT"))
		// A deployment ID may also be rendered as a bare environment value
		// (for example, DNS ownership audit metadata). It is an artifact
		// identity, not effective module configuration, so normalize it just
		// like deployment-root bind paths.
		body = bytes.ReplaceAll(body, []byte(filepath.Base(deploymentRoot)), []byte("$ANAS_DEPLOYMENT_ID"))
		_, _ = h.Write([]byte(filepath.ToSlash(rel)))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write(body)
		_, _ = h.Write([]byte{0})
	}
	return fmt.Sprintf("sha256:%x", h.Sum(nil)), nil
}

// inheritUnchangedModuleArtifacts makes the runtime reference stable across a
// deployment that did not change that module. A later `anas restart` therefore
// uses the same Compose working directory and bind-mount paths; the old
// deployment stays retained as an immutable artifact dependency.
func inheritUnchangedModuleArtifacts(base string, target *deploymentManifest) {
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" {
		return
	}
	current, err := loadDeploymentManifest(filepath.Join(base, "deployments", active.ActiveDeployment))
	if err != nil {
		return
	}
	for name, next := range target.Modules {
		previous, ok := current.Modules[name]
		if !ok || previous.RenderDigest == "" || previous.RenderDigest != next.RenderDigest {
			continue
		}
		artifact := previous.ArtifactDeployment
		if artifact == "" {
			artifact = current.ID
		}
		next.ArtifactDeployment = artifact
		target.Modules[name] = next
	}
}

func changedOrAddedModules(current, target *deploymentManifest) []string {
	if target == nil {
		return nil
	}
	if current == nil {
		return append([]string{}, target.ModuleOrder...)
	}
	selection := []string{}
	for _, name := range target.ModuleOrder {
		next, ok := target.Modules[name]
		if !ok {
			continue
		}
		previous, existed := current.Modules[name]
		if !existed || next.RenderDigest == "" || previous.RenderDigest == "" || next.RenderDigest != previous.RenderDigest {
			selection = append(selection, name)
		}
	}
	return selection
}

// changedOrRemovedModules is the old-deployment side of an artifact switch.
// A Compose project is stable across deployments, so every existing service
// whose rendered artifact changes must be removed before the target is brought
// up. Otherwise Compose may reuse a container whose env_file still belongs to
// the previous deployment. Removed modules share the same teardown path.
func changedOrRemovedModules(current, target *deploymentManifest) []string {
	if current == nil {
		return nil
	}
	selection := []string{}
	for _, name := range current.ModuleOrder {
		previous, ok := current.Modules[name]
		if !ok {
			continue
		}
		next, exists := target.Modules[name]
		if !exists || next.RenderDigest == "" || previous.RenderDigest == "" || next.RenderDigest != previous.RenderDigest {
			selection = append(selection, name)
		}
	}
	return selection
}

// activationStartModules preserves the activation diff while the active
// deployment is running. After an explicit whole-deployment stop, however,
// every target module must be restored: unchanged modules and their external
// networks no longer exist at runtime even though their artifacts are equal.
func activationStartModules(current, target *deploymentManifest, runtimeStatus string) []string {
	if runtimeStatus == "stopped" {
		return append([]string{}, target.ModuleOrder...)
	}
	return changedOrAddedModules(current, target)
}

func cloneNestedMap(in map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for k, values := range in {
		out[k] = cloneMap(values)
	}
	return out
}

func runApply(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	moduleRoot := fs.String("module-root", "", "directory containing module bundles")
	rootAlias := fs.String("root", "", "project root or module bundle directory")
	deploymentID := fs.String("deployment", "", "existing ready deployment id")
	build := fs.Bool("build", false, "build images before activation")
	updateLock := fs.Bool("update-lock", false, "create or update the config lock")
	allowRisky := fs.Bool("allow-risky", false, "allow changes requiring explicit migration or credential rotation")
	snapshot := fs.Bool("snapshot", false, "snapshot the data first even when nothing requires it")
	noSnapshot := fs.Bool("no-snapshot", false, "skip the automatic pre-apply snapshot (requires -y)")
	yes := fs.Bool("y", false, "confirm without prompting")
	fs.BoolVar(yes, "yes", false, "confirm without prompting")
	registerJSONFlag(fs)
	if err := fs.Parse(args); err != nil {
		return usageErrorf("%s", err.Error())
	}
	if fs.NArg() != 0 {
		return usageErrorf("usage: anas apply [-w <workspace>] [-c config.yml | --deployment ID] [--json]")
	}
	if *snapshot && *noSnapshot {
		return usageErrorf("apply accepts either --snapshot or --no-snapshot, not both")
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	announceWorkspace(workspace)
	configPath := ""
	if *deploymentID == "" {
		configPath = absolutePath(configPathFor(workspace, *cfgPath))
	} else if *cfgPath != "" {
		return usageErrorf("apply accepts either -c config.yml or --deployment ID, not both")
	} else {
		configPath = workspaceConfigPath(workspace)
	}
	explicit := *moduleRoot
	if explicit == "" {
		explicit = *rootAlias
	}
	service := newCLIDeploymentPlanService(workspace, configPath, explicit, moduleCommandCLISink{jsonMode: jsonMode})
	result, err := service.Apply(context.Background(), application.ApplyRequest{
		DeploymentID: *deploymentID, Build: *build, UpdateLock: *updateLock,
		AllowRisky: *allowRisky, Snapshot: *snapshot, NoSnapshot: *noSnapshot, Confirmed: *yes,
	})
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": result.Workspace, "deployment_id": result.DeploymentID,
			"previous_deployment": result.PreviousDeployment,
			"activated_at":        result.ActivatedAt,
			"deployment_path":     result.DeploymentPath,
		})
	}
	fmt.Println(result.DeploymentID)
	return nil
}

// nullableString renders an unset identifier as JSON null. "" and null both
// say "there was none", but only one of them says it in a way a caller cannot
// confuse with a legitimately empty value.
func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func runActive(action string, args []string, jsonMode bool) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	// parseInterspersed, not fs.Parse: the standard parser stops at the first
	// non-flag argument, so `anas restart lego -w <workspace>` would take lego
	// as positional and never see -w. That does not fail loudly -- it falls
	// back to the cwd or ANAS_WORKSPACE and acts on whichever deployment that
	// names, which is the failure `config secret get` already had to fix.
	requested, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	announceWorkspace(workspace)
	service := newCLIDeploymentPlanService(workspace, workspaceConfigPath(workspace), "", moduleCommandCLISink{jsonMode: jsonMode})
	result, err := service.ExecuteLifecycle(context.Background(), application.LifecycleRequest{
		Action: application.LifecycleAction(action), Modules: requested,
	})
	if err != nil {
		return applicationCLIError(err)
	}
	// start, restart and stop have no natural result: they either put the
	// deployment in the requested state or they did not. The envelope plus the
	// identifiers is the whole document, rather than a payload invented so it
	// would look substantial.
	return emitEmptyOK(jsonMode, map[string]any{
		"workspace": result.Workspace, "action": result.Action,
		"deployment_id": result.DeploymentID, "modules": result.Modules,
	})
}

func startDeployment(a *app, modulesRoot string, selection []string, jsonMode bool) error {
	for _, name := range selection {
		dir := filepath.Join(modulesRoot, name)
		if mod := a.reg[name]; a.useFrozenHooks && mod.SourceDir != "" {
			dir = mod.SourceDir
		}
		env, err := parseEnvFile(filepath.Join(dir, ".env"))
		if err != nil {
			return fmt.Errorf("deployment module %s env: %w", name, err)
		}
		env = a.relocateDeploymentEnv(env)
		if err := a.restoreModuleRuntimeState(a.reg[name], dir, env); err != nil {
			return fmt.Errorf("deployment module %s runtime state: %w", name, err)
		}
	}
	a.adoptReleaseEnv(modulesRoot)
	// hostLANRequired is asked of the whole deployment, not of the selection: a
	// module that does not itself need the bridge may still be started alongside
	// one that does, and the bridge has to exist either way.
	if err := a.ensureHostLAN(); err != nil {
		return err
	}
	total := int64(len(selection))
	done := int64(0)
	if err := a.eachOf(modulesRoot, selection, func(run moduleRun) error {
		done++
		a.progress(jsonMode, "activate-modules", done, total, "modules")
		if err := a.ensureResourcesFor(run.mod.Name, modulesRoot); err != nil {
			return err
		}
		if run.mod.RuntimeType == "compose" {
			args := append([]string{"up", "-d", "--remove-orphans"}, run.services...)
			if err := a.runCompose(run.dir, run.mod.Name, run.mod.ComposeFile, run.env, args...); err != nil {
				return err
			}
		}
		if err := a.coordinateModuleCredentials(run.mod, run.dir, run.env); err != nil {
			return fmt.Errorf("deployment module %s credential barrier: %w", run.mod.Name, err)
		}
		// The after-start Hook and local-account reconciliation are the current
		// Module ready barrier together with credential convergence. Running them here, rather than after every
		// container is up, prevents a downstream Module from observing an owner
		// that has started but has not finished its activation work.
		return a.runAfterStartOf(modulesRoot, []string{run.mod.Name})
	}); err != nil {
		return err
	}
	return nil
}

// activateOptions carries the operator's answers to the questions activation
// may have to ask. They are grouped rather than passed positionally because
// three of the five are booleans that only differ by name at the call site.
type activateOptions struct {
	allowRisky bool
	rollback   bool
	// snapshot forces a snapshot even when nothing triggers one.
	snapshot bool
	// noSnapshot suppresses the automatic snapshot. It needs yes, because
	// declining it throws away the only way back from the change being applied.
	noSnapshot bool
	yes        bool
	// json selects JSON Lines for the progress and warning records activation
	// writes to stderr. It never changes what activation does.
	json                         bool
	ctx                          context.Context
	events                       application.EventSink
	restrictedProcessEnvironment bool
}

func activateDeployment(base, id string, opts activateOptions) error {
	cli, err := detectComposeForExecution(opts.ctx, opts.restrictedProcessEnvironment)
	if err != nil {
		return preconditionErrorf("compose_missing", "%s", err.Error())
	}
	active, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	newApp, newRoot, target, err := loadDeploymentApp(base, id, cli)
	if err != nil {
		return preconditionErrorf("deployment_unreadable", "%s", err.Error())
	}
	newApp.commandContext, newApp.events = opts.ctx, opts.events
	newApp.restrictedProcessEnvironment = opts.restrictedProcessEnvironment
	if err := credentialStoreConsistencyError(base, target); err != nil {
		return err
	}
	if active.ActiveDeployment == id {
		if err := startDeployment(newApp, newRoot, newApp.order, opts.json); err != nil {
			return failuref("start_failed", "%s", err.Error())
		}
		active.RuntimeStatus = "running"
		if err := saveActiveState(base, active); err != nil {
			return failuref("write_failed", "record runtime status: %s", err.Error())
		}
		return nil
	}
	var oldApp *app
	var oldRoot string
	var current *deploymentManifest
	quiesced := false
	if active.ActiveDeployment != "" {
		oldApp, oldRoot, current, err = loadDeploymentApp(base, active.ActiveDeployment, cli)
		if err != nil {
			return preconditionErrorf("deployment_unreadable", "%s", err.Error())
		}
		oldApp.commandContext, oldApp.events = opts.ctx, opts.events
		oldApp.restrictedProcessEnvironment = opts.restrictedProcessEnvironment
		if !opts.rollback {
			changes := settingChangesWithEffect(current, target, "image_rebuild")
			if len(changes) > 0 && !target.ImagesBuilt {
				return imageRebuildRequiredError(changes)
			}
		}
		blockers := deploymentChangeBlockers(current, target)
		if opts.rollback {
			guard := deploymentRollbackVersionGuard(current, target)
			// Checked before the --allow-risky gate on purpose. The other
			// blockers describe something the runner does not know and an
			// operator might; this one describes something it does know.
			if err := guard.breakingError(); err != nil {
				return &CLIError{
					Code: "rollback_data_breaking", Message: err.Error(), Exit: exitPrecondition,
				}
			}
			blockers = append(blockers, guard.Blocked...)
			sort.Strings(blockers)
		}
		if len(blockers) > 0 && !opts.allowRisky {
			verb, code := "apply", "guarded_changes"
			if opts.rollback {
				verb, code = "rollback", "rollback_guarded_changes"
			}
			return &CLIError{
				Code: code,
				Message: fmt.Sprintf("%s crosses guarded state changes:\n  %s\nrun the declared migration/rotation or repeat with --allow-risky",
					verb, strings.Join(blockers, "\n  ")),
				Detail: map[string]any{"blocked": blockers},
				Exit:   exitPrecondition,
			}
		}
		if !opts.rollback {
			quiesced, err = snapshotBeforeApply(base, opts, oldApp, oldRoot, current, target)
			if err != nil {
				return err
			}
		}
	}
	if active.ActiveDeployment == "" && target.BuildAcceleration && !target.ImagesBuilt {
		return imageRebuildRequiredError([]string{"global.chinese_build_speedup"})
	}

	runtimeStatus := active.RuntimeStatus
	if quiesced {
		// snapshotBeforeApply stopped the whole current deployment to take a
		// clean snapshot. Render digests still describe unchanged modules, but
		// none of their containers are running anymore, so all targets must be
		// restored rather than only the changed subset.
		runtimeStatus = "stopped"
	}
	selection := activationStartModules(current, target, runtimeStatus)
	if oldApp != nil && runtimeStatus != "stopped" {
		stopSelection := changedOrRemovedModules(current, target)
		if err := oldApp.stopModules(oldRoot, stopSelection, opts.json); err != nil {
			restoreErr := startDeployment(oldApp, oldRoot, stopSelection, opts.json)
			if restoreErr != nil {
				return failuref("stop_failed", "%v; previous deployment restore failed: %v", err, restoreErr)
			}
			return failuref("stop_failed", "%v; previous deployment restored", err)
		}
	}
	if len(selection) > 0 {
		if err := startDeployment(newApp, newRoot, selection, opts.json); err != nil {
			_ = saveDeploymentFailure(base, id, err)
			return failuref("start_failed", "%s", restorePreviousAfterActivationFailure(newApp, newRoot, oldApp, oldRoot, err, opts.json))
		}
	}
	if err := retainRemovedResources(base, current, target); err != nil {
		return failuref("resource_state_failed", "%s", restorePreviousAfterActivationFailure(newApp, newRoot, oldApp, oldRoot, err, opts.json))
	}

	now := time.Now().UTC().Format(time.RFC3339)
	previous := removeString(active.PreviousDeployments, id)
	if active.ActiveDeployment != "" {
		previous = append([]string{active.ActiveDeployment}, previous...)
		oldState, _ := loadDeploymentState(base, active.ActiveDeployment)
		oldState.Status = "previous"
		oldState.DeactivatedAt = now
		_ = saveDeploymentState(base, oldState)
	}
	active = &activeDeploymentState{
		APIVersion: activeStateVersion, ActiveDeployment: id,
		RuntimeStatus:       "running",
		PreviousDeployments: previous, ActivatedAt: now, VerifiedAt: now,
	}
	if err := saveActiveState(base, active); err != nil {
		if oldApp != nil {
			return failuref("write_failed", "%s", restorePreviousAfterActivationFailure(newApp, newRoot, oldApp, oldRoot, err, opts.json))
		}
		return failuref("write_failed", "%s", err.Error())
	}
	state, _ := loadDeploymentState(base, id)
	state.Status = "active"
	state.ActivatedAt = now
	state.VerifiedAt = now
	if current != nil {
		state.Predecessor = current.ID
	}
	if err := saveDeploymentState(base, state); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	// Retention runs only after active is committed, so a failed apply never
	// spends a keep_auto slot and never reclaims the snapshot that would have
	// been the way back from it.
	collectAutomaticSnapshots(workspaceOf(base), opts.events, opts.json, opts.ctx, opts.restrictedProcessEnvironment)
	return nil
}

// restorePreviousAfterActivationFailure stops every candidate project before
// reactivating the immutable previous deployment. Starting previous on top of
// partially activated candidate containers leaves the real runtime ambiguous,
// especially once credential reconciliation becomes part of the ready barrier.
func restorePreviousAfterActivationFailure(candidate *app, candidateRoot string, previous *app, previousRoot string, cause error, jsonMode bool) error {
	parts := []string{cause.Error()}
	if candidate != nil {
		if err := candidate.stopRelease(candidateRoot, jsonMode); err != nil {
			parts = append(parts, "candidate stop failed: "+err.Error())
		}
	}
	if previous != nil {
		if err := startDeployment(previous, previousRoot, previous.order, jsonMode); err != nil {
			parts = append(parts, "previous deployment restore failed: "+err.Error())
		} else {
			parts = append(parts, "previous deployment restored")
		}
	}
	return fmt.Errorf("%s", strings.Join(parts, "; "))
}

// snapshotBeforeApply takes the automatic pre-apply snapshot, when the change
// being applied is one the operator cannot take back on their own.
//
// The trigger is deliberately narrow — see deploymentSnapshotTrigger. Applies
// that only recreate a container or reload a config leave the data exactly as
// it was, and spending a keep_auto slot on each of them would push the snapshot
// that actually matters out of the retention window.
//
// Every path that ends up not taking a snapshot asks first. Continuing quietly
// would leave the operator believing they had a way back at the one moment they
// do not.
func snapshotBeforeApply(base string, opts activateOptions, oldApp *app, oldRoot string, current, target *deploymentManifest) (bool, error) {
	trigger := deploymentSnapshotTrigger(current, target)
	if trigger == nil {
		if !opts.snapshot {
			return false, nil
		}
		trigger = &applySnapshotTrigger{reason: snapshotReasonPreApply, detail: "--snapshot was requested"}
	}
	workspace := workspaceOf(base)
	if opts.noSnapshot {
		deploymentWarning(opts.events, opts.json, trigger.reason, "%s", trigger.detail)
		deploymentWarning(opts.events, opts.json, "no_snapshot_requested",
			"--no-snapshot gives up the only way back to the current data")
		return false, confirmDestructive("Apply without a data snapshot", opts.yes)
	}
	if code, reason := snapshotUnavailable(workspace, target); code != "" {
		deploymentWarning(opts.events, opts.json, trigger.reason, "%s", trigger.detail)
		deploymentWarning(opts.events, opts.json, code,
			"%s, so no snapshot can be taken and the current data cannot be recovered afterwards", reason)
		return false, confirmDestructive("Apply anyway, with no way back to the current data", opts.yes)
	}
	// Quiescing first: a snapshot taken while services are mid-write captures a
	// crash-consistent database rather than a clean one.
	if err := oldApp.stopRelease(oldRoot, opts.json); err != nil {
		return false, failuref("quiesce_failed", "quiesce active deployment before data snapshot: %v", err)
	}
	// The snapshot is not recorded against this deployment. It stands on its
	// own, carrying the artifact and config it belongs to, and is found by
	// listing snapshots rather than by following a pointer.
	if _, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: trigger.reason,
		from: current.ID, to: target.ID, json: opts.json, events: opts.events,
		ctx: opts.ctx, restrictedProcessEnvironment: opts.restrictedProcessEnvironment,
	}); err != nil {
		_ = startDeployment(oldApp, oldRoot, oldApp.order, opts.json)
		return false, err
	}
	return true, nil
}

// snapshotUnavailable reports why this host cannot snapshot: an enumerated code
// for a caller to branch on and a sentence fit for an operator. Both are empty
// when it can.
func snapshotUnavailable(workspace string, target *deploymentManifest) (string, string) {
	backend := strings.ToLower(strings.TrimSpace(target.Snapshot.Backend))
	if backend == "" || backend == "none" {
		return "no_snapshot_backend", "no snapshot backend is configured (rollback.snapshot.backend)"
	}
	if err := btrfsSubvolumeShow(dataDir(workspace)); err != nil {
		return "data_not_subvolume", fmt.Sprintf("%s is not a Btrfs subvolume", dataDir(workspace))
	}
	return "", ""
}

// collectAutomaticSnapshots applies the keep_auto policy. Failures are reported
// and swallowed: a deployment that is up and verified must not be reported as
// failed because some older snapshot could not be reclaimed.
func collectAutomaticSnapshots(workspace string, events application.EventSink, jsonMode bool, ctx context.Context, restricted bool) {
	all, err := listSnapshots(workspace)
	if err != nil {
		deploymentWarning(events, jsonMode, "snapshot_collection_failed", "could not scan snapshots for collection: %v", err)
		return
	}
	collect, _, _ := snapshotsToPrune(all, workspaceKeepAuto(workspace))
	for _, meta := range collect {
		if err := deleteSnapshotWithOptions(workspace, meta.ID, snapshotOptions{
			ctx: ctx, events: events, restrictedProcessEnvironment: restricted,
		}); err != nil {
			deploymentWarning(events, jsonMode, "snapshot_reclaim_failed", "could not reclaim snapshot %s: %v", meta.ID, err)
		}
	}
}

func deploymentWarning(events application.EventSink, jsonMode bool, code, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if events != nil {
		events.Warning(application.WarningEvent{Code: code, Message: message})
		return
	}
	emitWarning(jsonMode, code, "%s", message)
}

func errorsJoin(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// guardedSettingChange is a changed setting whose effect cannot be undone by
// editing config.yml back.
type guardedSettingChange struct {
	Key    string
	Effect string
	Apply  string
}

func settingChangesWithEffect(current, target *deploymentManifest, effect string) []string {
	if current == nil || target == nil {
		return nil
	}
	keys := map[string]bool{}
	for key := range current.Settings {
		keys[key] = true
	}
	for key := range target.Settings {
		keys[key] = true
	}
	changed := []string{}
	for key := range keys {
		from, fromOK := current.Settings[key]
		to, toOK := target.Settings[key]
		if fromOK && toOK && from.Fingerprint == to.Fingerprint {
			continue
		}
		setting := to
		if !toOK {
			setting = from
		}
		if setting.Effect == effect {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func imageRebuildRequiredError(changes []string) error {
	return &CLIError{
		Code:    "image_rebuild_required",
		Message: fmt.Sprintf("configuration changes image build inputs:\n  %s\nrun `anas apply --build -w <workspace>` to rebuild images before activation", strings.Join(changes, "\n  ")),
		Detail:  map[string]any{"settings": changes},
		Exit:    exitPrecondition,
	}
}

// guardedSettingChanges is the one place the "not automatically reversible"
// effect set is spelled out on the deployment side. The apply blocker and the
// automatic snapshot trigger both read it, so the two cannot drift apart into
// disagreeing about what counts as irreversible.
func guardedSettingChanges(current, target *deploymentManifest) []guardedSettingChange {
	if current == nil || target == nil {
		return nil
	}
	keys := map[string]bool{}
	for key := range current.Settings {
		keys[key] = true
	}
	for key := range target.Settings {
		keys[key] = true
	}
	changes := []guardedSettingChange{}
	for key := range keys {
		from, fromOK := current.Settings[key]
		to, toOK := target.Settings[key]
		if fromOK && toOK && from.Fingerprint == to.Fingerprint {
			continue
		}
		setting := to
		if !toOK {
			setting = from
		}
		switch setting.Effect {
		case "credential_rotate", "data_migrate", "immutable":
			changes = append(changes, guardedSettingChange{Key: key, Effect: setting.Effect, Apply: setting.Apply})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes
}

func deploymentChangeBlockers(current, target *deploymentManifest) []string {
	changes := guardedSettingChanges(current, target)
	if changes == nil {
		return nil
	}
	blocked := []string{}
	for _, change := range changes {
		blocked = append(blocked, fmt.Sprintf("%s (%s; %s)", change.Key, change.Effect, change.Apply))
	}
	sort.Strings(blocked)
	return blocked
}

func runDeploymentRollback(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	allowRisky := fs.Bool("allow-risky", false, "allow guarded rollback")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	if len(positional) > 1 {
		return usageErrorf("usage: anas rollback [DEPLOYMENT_ID] -w <workspace> [--json]")
	}
	// Rollback replaces the running artifact, and a workspace inherited from the
	// environment is the easiest thing to leave stale and pointed somewhere
	// else. It accepts only the flag.
	workspace, err := resolveWorkspaceStrict(*workspaceFlag, "rollback")
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	announceWorkspace(workspace)
	target := ""
	if len(positional) == 1 {
		target = positional[0]
		if err := validateDeploymentID(target); err != nil {
			return usageErrorf("%s", err.Error())
		}
	}
	service := newCLIDeploymentPlanService(workspace, workspaceConfigPath(workspace), "", moduleCommandCLISink{jsonMode: jsonMode})
	result, err := service.Rollback(context.Background(), application.RollbackRequest{
		DeploymentID: target, AllowRisky: *allowRisky,
	})
	if err != nil {
		return applicationCLIError(err)
	}
	return emitEmptyOK(jsonMode, map[string]any{
		"workspace": result.Workspace, "deployment_id": result.DeploymentID,
		"previous_deployment": result.PreviousDeployment,
		"activated_at":        result.ActivatedAt,
		"data_touched":        result.DataTouched,
	})
}

var btrfsCommand = func(args ...string) error {
	cmd := exec.Command("btrfs", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("%w: %s", err, message)
		}
		return err
	}
	return nil
}

func runBtrfs(args ...string) error { return btrfsCommand(args...) }

// btrfsSubvolumeShow verifies that path is a Btrfs subvolume.
//
// It deliberately does not run `btrfs subvolume show`, which needs
// CAP_SYS_ADMIN for its tree-search ioctl and so fails for an ordinary user
// even though `btrfs subvolume create` and `snapshot` both succeed
// unprivileged. Using it as a precondition put the entire snapshot feature
// behind root for no reason.
//
// Deletion is the exception and does not belong in that list: BTRFS_IOC_SNAP_DESTROY
// requires CAP_SYS_ADMIN unless the filesystem was mounted with
// user_subvol_rm_allowed, which is why a workspace can take snapshots it cannot
// reclaim. See describeSubvolumeDeleteFailure, which exists for that asymmetry.
//
// A subvolume root is identified without any privilege by two facts: it lives
// on Btrfs, and its inode number is 256.
//
// Both conditions are required, not belt-and-braces. Inode numbers in Btrfs are
// per-subvolume rather than filesystem-wide, which makes the number decisive on
// Btrfs and meaningless anywhere else, where 256 is just an ordinary inode some
// file will eventually get.
//
// 256 is not a magic value: Btrfs reserves objectids 0-255 for internal trees
// (the top-level FS_TREE is 5, which is why the toplevel subvolume is subvolid
// 5), and BTRFS_FIRST_FREE_OBJECTID is simply the first number after that
// block. Each subvolume is its own tree with its own objectid space, and its
// root directory is the first object created in it, so the root always lands on
// 256 and the next object on 257. That makes this a structural guarantee rather
// than an observed regularity — the distinction that makes it safe to validate
// against.
//
// The number is deliberately not unique on disk: two sibling subvolumes both
// report inode 256. Btrfs keeps the POSIX guarantee that (st_dev, st_ino)
// identifies a file by giving each subvolume its own anonymous device number,
// so the collision is confined to st_ino. This check asks whether a path is
// *a* subvolume root, never *which* one, so the duplication is the mechanism
// rather than a hazard. The anonymous st_dev is not stable across mounts and
// must never be persisted.
//
// Verified against `btrfs subvolume show` on a real filesystem: subvolumes,
// nested subvolumes, read-only snapshots and the top-level mount all report
// 256, while plain directories and files report 257 upward.
const btrfsSubvolumeRootInode = 256

// Overridable for the same reason as btrfsCommand: tests exercise the snapshot
// bookkeeping on whatever filesystem the temp directory happens to be.
var btrfsSubvolumeCheck = checkBtrfsSubvolume

func btrfsSubvolumeShow(path string) error { return btrfsSubvolumeCheck(path) }

func checkBtrfsSubvolume(path string) error {
	btrfs, err := filesystemIsBtrfs(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !btrfs {
		return fmt.Errorf("%s is not on a Btrfs filesystem", path)
	}
	// Lstat, not Stat: a symlink pointing at a subvolume root must not pass.
	// The restore path renames this location aside, and renaming a symlink
	// moves the link rather than the data it names, so accepting one would
	// silently restore into the wrong place.
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect %s", path)
	}
	if st.Ino != btrfsSubvolumeRootInode {
		return fmt.Errorf("%s is a directory on Btrfs but not a subvolume root", path)
	}
	return nil
}

// runStatus answers a question, so "nothing is deployed" is a successful
// answer and exits 0. Reporting it as a failure would make a caller unable to
// distinguish a fresh workspace from an unreadable one.
func runStatus(args []string, jsonMode bool) error {
	workspace, _, err := parseBaseOnly("status", args)
	if err != nil {
		return err
	}
	status, err := application.NewService(workspace).Status(context.Background())
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace":            workspace,
			"active_deployment":    status.ActiveDeployment,
			"activated_at":         status.ActivatedAt,
			"verified_at":          status.VerifiedAt,
			"previous_deployments": append([]string{}, status.PreviousDeployments...),
		})
	}
	if status.ActiveDeployment == nil {
		fmt.Println("active: none")
		return nil
	}
	fmt.Printf("active: %s\nactivated_at: %s\nverified_at: %s\n",
		*status.ActiveDeployment, optionalString(status.ActivatedAt), optionalString(status.VerifiedAt))
	if len(status.PreviousDeployments) > 0 {
		fmt.Println("previous: " + strings.Join(status.PreviousDeployments, ","))
	}
	return nil
}

func runDeployments(args []string, jsonMode bool) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		// A subcommand is a word, never a flag: `anas deployments --json` must
		// not take "--json" as the name of a subcommand.
		return usageErrorf("usage: anas deployments list|inspect [ID] [-w <workspace>] [--json]")
	}
	sub := args[0]
	workspace, _, rest, err := parseBaseArgs("deployments "+sub, args[1:])
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		if len(rest) != 0 {
			return usageErrorf("usage: anas deployments list [-w <workspace>] [--json]")
		}
		result, err := application.NewService(workspace).ListDeployments(
			context.Background(), application.ListDeploymentsRequest{},
		)
		if err != nil {
			return applicationCLIError(err)
		}
		if jsonMode {
			return emitOK(map[string]any{
				"workspace": workspace, "deployments": deploymentStateDocuments(result.Deployments),
			})
		}
		for _, state := range result.Deployments {
			fmt.Printf("%s\t%s\t%s\n", state.ID, state.Status, state.CreatedAt)
		}
		return nil
	case "inspect":
		if len(rest) != 1 {
			return usageErrorf("usage: anas deployments inspect ID [-w <workspace>] [--json]")
		}
		if err := validateDeploymentID(rest[0]); err != nil {
			return usageErrorf("%s", err.Error())
		}
		result, err := application.NewService(workspace).InspectDeployment(
			context.Background(), application.InspectDeploymentRequest{DeploymentID: rest[0]},
		)
		if err != nil {
			return applicationCLIError(err)
		}
		if jsonMode {
			return emitOK(map[string]any{
				"workspace": workspace, "deployment_path": result.DeploymentPath,
				"deployment": result.Deployment, "state": deploymentStateDocument(result.State),
			})
		}
		fmt.Print(string(result.RawManifest))
		return nil
	default:
		return usageErrorf("unknown deployments command %q; expected list or inspect", sub)
	}
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func deploymentStateDocument(state deploymentState) map[string]any {
	return map[string]any{
		"id": state.ID, "status": state.Status, "created_at": state.CreatedAt,
		"activated_at":   nullableString(state.ActivatedAt),
		"deactivated_at": nullableString(state.DeactivatedAt),
		"verified_at":    nullableString(state.VerifiedAt),
		"predecessor":    nullableString(state.Predecessor),
		"failure":        nullableString(state.Failure),
	}
}

func deploymentStateDocuments(states []deploymentState) []map[string]any {
	out := make([]map[string]any, 0, len(states))
	for _, state := range states {
		out = append(out, deploymentStateDocument(state))
	}
	return out
}

func parseBaseOnly(name string, args []string) (string, string, error) {
	workspace, base, rest, err := parseBaseArgs(name, args)
	if err != nil {
		return "", "", err
	}
	if len(rest) != 0 {
		return "", "", usageErrorf("usage: anas %s [-w <workspace>] [--json]", name)
	}
	return workspace, base, nil
}

// parseBaseArgs resolves the workspace and returns it alongside its state
// directory, which is what every read-only inspection command actually needs.
func parseBaseArgs(name string, args []string) (string, string, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return "", "", nil, usageErrorf("%s", err.Error())
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return "", "", nil, usageErrorf("%s", err.Error())
	}
	return workspace, stateDir(workspace), positional, nil
}

func loadDeploymentApp(base, id string, cli compose.CLI) (*app, string, *deploymentManifest, error) {
	if err := validateDeploymentID(id); err != nil {
		return nil, "", nil, err
	}
	root := filepath.Join(base, "deployments", id)
	manifest, err := loadDeploymentManifest(root)
	if err != nil {
		return nil, "", nil, err
	}
	modulesRoot := filepath.Join(root, "modules")
	reg := map[string]Module{}
	deps := map[string][]string{}
	modulePlans := map[string]map[string]string{}
	for name, module := range manifest.Modules {
		artifactDeployment := module.ArtifactDeployment
		if artifactDeployment == "" {
			artifactDeployment = manifest.ID
		}
		artifactRoot := filepath.Join(base, "deployments", artifactDeployment, "modules")
		reg[name] = Module{
			Name: name, Version: module.Version, Revision: module.Revision, AppVersion: module.AppVersion,
			Lifecycle: module.Lifecycle,
			SourceDir: filepath.Join(artifactRoot, name), EnvPrefix: module.EnvPrefix,
			Consumes: append([]string{}, module.Consumes...), Changes: module.Changes,
			UseHostLAN: module.UseHostLAN, Hook: module.Hook,
			RuntimeType: module.RuntimeType, ComposeFile: module.ComposeFile,
			ContractProviders:   cloneContractProviders(module.Providers),
			LocalAccounts:       append([]LocalAccount{}, module.LocalAccounts...),
			CredentialProviders: cloneCredentialProviders(module.CredentialProviders),
			CredentialConsumers: cloneCredentialConsumers(module.CredentialConsumers),
			CommandExecutor:     module.CommandExecutor,
			Commands:            cloneModuleCommands(module.Commands),
		}
		deps[name] = append([]string{}, module.Dependencies...)
		if len(module.ValidationPlan) > 0 {
			modulePlans[name] = cloneMap(module.ValidationPlan)
		}
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		return nil, "", nil, err
	}
	localAdmins, err := loadLocalAdminState(base)
	if err != nil {
		return nil, "", nil, err
	}
	a := &app{
		workspace: workspaceOf(base), base: base, compose: cli, reg: reg, deps: deps,
		order: append([]string{}, manifest.ModuleOrder...),
		env:   map[string]string{}, envOwner: map[string]string{},
		secrets: secrets, localAdmins: localAdmins, useFrozenHooks: true, artifactRoot: modulesRoot,
		resolvedBindings: cloneNestedMap(manifest.Bindings),
		modulePlans:      modulePlans,
		credentials:      append([]deploymentCredential{}, manifest.Credentials...),
	}
	for _, resource := range manifest.Resources {
		secretKey := resource.CredentialSecretKey
		if secretKey == "" {
			secretKey = resource.SecretKey
		}
		credential := secrets.values[secretKey]
		if credential == "" {
			return nil, "", nil, fmt.Errorf("resource %s.%s secret %s is missing", resource.Consumer, resource.ID, secretKey)
		}
		a.resourceRequests = append(a.resourceRequests, ResourceRequest{
			Consumer: resource.Consumer, ID: resource.ID, Contract: resource.Contract, ContractVersion: resource.ContractVersion,
			Provider: resource.Provider, Interface: resource.Interface,
			Spec: resource.Spec, SecretKey: secretKey, Credential: credential,
		})
	}
	if err := a.restoreLocalAdminPasswordFiles(); err != nil {
		return nil, "", nil, err
	}
	a.adoptReleaseEnv(modulesRoot)
	return a, modulesRoot, manifest, nil
}

func validateDeploymentID(id string) error {
	return deployment.ValidateID(id)
}

func loadDeploymentManifest(root string) (*deploymentManifest, error) {
	var manifest deploymentManifest
	if err := readYAML(filepath.Join(root, "deployment.yml"), &manifest); err != nil {
		return nil, err
	}
	if manifest.APIVersion != deploymentAPIVersion {
		return nil, fmt.Errorf("unsupported deployment api_version %q", manifest.APIVersion)
	}
	if manifest.ID == "" || filepath.Base(root) != manifest.ID {
		return nil, fmt.Errorf("deployment manifest id %q does not match directory %s", manifest.ID, root)
	}
	return &manifest, nil
}

// ensureRuntimeLayout creates the state directory tree. snapshots/ is not part
// of it: snapshots live at <workspace>/snapshots, a sibling of .anas rather
// than a child, so that a data restore which replaces the data directory can
// never take the runtime state with it.
func ensureRuntimeLayout(base string) error {
	for _, dir := range []string{
		base, filepath.Join(base, "state"), filepath.Join(base, "state", "deployments"),
		filepath.Join(base, "state", "transactions"), filepath.Join(base, "deployments"),
		filepath.Join(base, "staging"), filepath.Join(base, "runtime-state"),
		filepath.Join(base, "runtime-state", "deployments"),
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(snapshotsDir(workspaceOf(base)), 0700); err != nil {
		return err
	}
	return os.Chmod(base, 0700)
}

// workspaceOf inverts stateDir. Several runtime helpers are handed only the
// state directory, and the layout guarantees its parent is the workspace.
func workspaceOf(base string) string { return filepath.Dir(base) }

// acquireRuntimeLock takes the exclusive lock, and is also where the debris of
// an interrupted operation is swept up. Cleaning under the exclusive lock is
// what makes it safe: a create holds the same lock for its entire duration, so
// a .tmp- directory visible here can never be one that is still being built,
// and an open container transaction can never belong to a backup still running.
//
// The container compensation is here rather than only in the backup path
// because the case it exists for is the one where the backup process no longer
// exists to do it: a backup killed between stopping the containers and starting
// them again leaves services down, and the next command to take this lock is
// the first opportunity anything has to notice.
func acquireRuntimeLock(base string) (func(), error) {
	return acquireRuntimeLockContext(context.Background(), base)
}

// acquireRuntimeLockContext is the service-facing variant of
// acquireRuntimeLock. It polls an advisory lock rather than entering a
// non-interruptible Flock call so an abandoned HTTP request or canceled job
// never leaves a goroutine waiting forever. Recovery still runs only after the
// exclusive lock is held.
func acquireRuntimeLockContext(ctx context.Context, base string) (func(), error) {
	return acquireRuntimeLockWithRecovery(ctx, base, runtimeRecoveryOptions{ctx: ctx})
}

func acquireRuntimeLockForApplication(ctx context.Context, base string, events application.EventSink, restricted bool) (func(), error) {
	return acquireRuntimeLockWithRecovery(ctx, base, runtimeRecoveryOptions{
		ctx: ctx, events: events, restrictedProcessEnvironment: restricted,
	})
}

func acquireRuntimeLockWithRecovery(ctx context.Context, base string, recovery runtimeRecoveryOptions) (func(), error) {
	unlock, err := acquireRuntimeLockModeContext(ctx, base, syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	if recoveryErr := recoverWorkspaceConfigTransaction(workspaceOf(base)); recoveryErr != nil {
		unlock()
		return nil, recoveryErr
	}
	cleanStaleSnapshotTempWithOptions(workspaceOf(base), snapshotOptions{
		ctx: recovery.ctx, events: recovery.events,
		restrictedProcessEnvironment: recovery.restrictedProcessEnvironment,
	})
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	compensateContainerTransactionsWithOptions(base, recovery)
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	if txn, recoveryErr := unfinishedCredentialRotation(base); recoveryErr != nil {
		unlock()
		return nil, recoveryErr
	} else if txn != nil {
		if recoveryErr := recoverCredentialRotationForApplication(base, txn, recovery); recoveryErr != nil {
			unlock()
			return nil, fmt.Errorf("%w: automatic recovery failed: %v", credentialRecoveryRequiredError(txn), recoveryErr)
		}
	}
	return unlock, nil
}

func acquireRuntimeSharedLock(base string) (func(), error) {
	return acquireRuntimeSharedLockContext(context.Background(), base)
}

func acquireRuntimeSharedLockContext(ctx context.Context, base string) (func(), error) {
	return acquireRuntimeLockModeContext(ctx, base, syscall.LOCK_SH)
}

func acquireRuntimeLockModeContext(ctx context.Context, base string, mode int) (func(), error) {
	if ctx == nil {
		return nil, errors.New("lock runtime state: nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("lock runtime state: %w", err)
	}
	if err := ensureRuntimeLayout(base); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(base, "state", "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime state: %w", err)
		}
		timer := time.NewTimer(runtimeLockRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, fmt.Errorf("lock runtime state: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, nil
}

func activeStatePath(base string) string { return filepath.Join(base, "state", "active.yml") }

func loadActiveState(base string) (*activeDeploymentState, error) {
	var state activeDeploymentState
	err := readYAML(activeStatePath(base), &state)
	if os.IsNotExist(err) {
		return &activeDeploymentState{APIVersion: activeStateVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	if state.APIVersion != activeStateVersion {
		return nil, fmt.Errorf("unsupported active state %q", state.APIVersion)
	}
	return &state, nil
}

func saveActiveState(base string, state *activeDeploymentState) error {
	state.APIVersion = activeStateVersion
	return writeYAMLAtomic(activeStatePath(base), state, 0600)
}

func deploymentStatePath(base, id string) string {
	return filepath.Join(base, "state", "deployments", id+".yml")
}

func loadDeploymentState(base, id string) (deploymentState, error) {
	var state deploymentState
	err := readYAML(deploymentStatePath(base, id), &state)
	if os.IsNotExist(err) {
		return deploymentState{APIVersion: activeStateVersion, ID: id}, nil
	}
	return state, err
}

func saveDeploymentState(base string, state deploymentState) error {
	state.APIVersion = activeStateVersion
	if err := writeYAMLAtomic(deploymentStatePath(base, state.ID), &state, 0600); err != nil {
		return err
	}
	return rebuildDeploymentIndex(base)
}

func saveDeploymentFailure(base, id string, cause error) error {
	state, _ := loadDeploymentState(base, id)
	state.Status = "failed"
	state.Failure = cause.Error()
	return saveDeploymentState(base, state)
}

func listDeploymentStates(base string) ([]deploymentState, error) {
	entries, err := os.ReadDir(filepath.Join(base, "state", "deployments"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	states := []deploymentState{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}
		var state deploymentState
		if err := readYAML(filepath.Join(base, "state", "deployments", entry.Name()), &state); err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].CreatedAt > states[j].CreatedAt })
	return states, nil
}

func rebuildDeploymentIndex(base string) error {
	states, err := listDeploymentStates(base)
	if err != nil {
		return err
	}
	index := deploymentIndex{APIVersion: activeStateVersion, GeneratedAt: time.Now().UTC().Format(time.RFC3339), Deployments: states}
	return writeYAMLAtomic(filepath.Join(base, "state", "index.yml"), &index, 0600)
}

func readYAML(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, out)
}

func writeYAMLAtomic(path string, value any, mode os.FileMode) error {
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func newDeploymentID() (string, error) {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random), nil
}

func removeString(items []string, target string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		if item != target {
			out = append(out, item)
		}
	}
	return out
}
