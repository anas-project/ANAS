package runner

import (
	"bytes"
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

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	deploymentAPIVersion = "anas.deployment/v1"
	activeStateVersion   = "anas.state/v2"
)

// The json tags are not decoration: `deployments inspect --json` emits these
// structs directly, and without them the document would carry Go field names
// while the YAML on disk carried snake_case ones. Two spellings of the same
// manifest is exactly the kind of thing a caller ends up hard-coding around.
type deploymentManifest struct {
	APIVersion        string                       `yaml:"api_version" json:"api_version"`
	ID                string                       `yaml:"id" json:"id"`
	CreatedAt         string                       `yaml:"created_at" json:"created_at"`
	ConfigFingerprint string                       `yaml:"config_fingerprint" json:"config_fingerprint"`
	ImagesBuilt       bool                         `yaml:"images_built,omitempty" json:"images_built"`
	BuildAcceleration bool                         `yaml:"build_acceleration,omitempty" json:"build_acceleration"`
	ModuleOrder       []string                     `yaml:"module_order" json:"module_order"`
	Bindings          map[string]map[string]string `yaml:"capability_bindings,omitempty" json:"capability_bindings,omitempty"`
	Modules           map[string]deploymentModule  `yaml:"modules" json:"modules"`
	Settings          map[string]deploymentSetting `yaml:"settings,omitempty" json:"settings,omitempty"`
	Resources         []deploymentResource         `yaml:"resources,omitempty" json:"resources,omitempty"`
	Snapshot          deploymentSnapshotPolicy     `yaml:"snapshot,omitempty" json:"snapshot"`
}

type deploymentModule struct {
	Name               string `yaml:"name" json:"name"`
	Version            string `yaml:"version" json:"version"`
	Revision           int    `yaml:"revision" json:"revision"`
	AppVersion         string `yaml:"app_version,omitempty" json:"app_version,omitempty"`
	ArtifactDeployment string `yaml:"artifact_deployment" json:"artifact_deployment"`
	RenderDigest       string `yaml:"render_digest" json:"render_digest"`
	// DataBreaking is frozen from the module's upgrade.data_breaking so that a
	// rollback can be judged against what the module claimed when it was rendered,
	// not against whatever the bundle on disk says today. omitempty distinguishes
	// the two states that matter: an undeclared list is absent, a declared-empty
	// one is written out as `[]`.
	DataBreaking *[]string               `yaml:"data_breaking,omitempty" json:"data_breaking,omitempty"`
	RuntimeType  string                  `yaml:"runtime" json:"runtime"`
	ComposeFile  string                  `yaml:"compose_file,omitempty" json:"compose_file,omitempty"`
	Hook         HookConfig              `yaml:"hook,omitempty" json:"hook,omitempty"`
	EnvPrefix    string                  `yaml:"env_prefix,omitempty" json:"env_prefix,omitempty"`
	Consumes     []string                `yaml:"consumes,omitempty" json:"consumes,omitempty"`
	Dependencies []string                `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	UseHostLAN   string                  `yaml:"host_lan,omitempty" json:"host_lan,omitempty"`
	Changes      map[string]ChangePolicy `yaml:"changes,omitempty" json:"changes,omitempty"`
	Providers    []ContractProvider      `yaml:"contract_providers,omitempty" json:"contract_providers,omitempty"`
}

type deploymentResource struct {
	Consumer        string         `yaml:"consumer" json:"consumer"`
	ID              string         `yaml:"id" json:"id"`
	Contract        string         `yaml:"contract" json:"contract"`
	ContractVersion string         `yaml:"contract_version" json:"contract_version"`
	Provider        string         `yaml:"provider" json:"provider"`
	Interface       string         `yaml:"interface" json:"interface"`
	Spec            map[string]any `yaml:"spec" json:"spec"`
	SecretKey       string         `yaml:"password_secret" json:"password_secret"`
}

type deploymentSetting struct {
	Fingerprint string `yaml:"fingerprint" json:"fingerprint"`
	Module      string `yaml:"module" json:"module"`
	Parameter   string `yaml:"parameter" json:"parameter"`
	Effect      string `yaml:"effect" json:"effect"`
	Apply       string `yaml:"apply,omitempty" json:"apply,omitempty"`
}

type deploymentSnapshotPolicy struct {
	Backend  string `yaml:"backend,omitempty" json:"backend"`
	Source   string `yaml:"source,omitempty" json:"source"`
	Root     string `yaml:"root,omitempty" json:"root"`
	KeepAuto int    `yaml:"keep_auto,omitempty" json:"keep_auto"`
}

type activeDeploymentState struct {
	APIVersion          string   `yaml:"api_version"`
	ActiveDeployment    string   `yaml:"active_deployment,omitempty"`
	RuntimeStatus       string   `yaml:"runtime_status,omitempty"`
	PreviousDeployments []string `yaml:"previous_deployments,omitempty"`
	ActivatedAt         string   `yaml:"activated_at,omitempty"`
	VerifiedAt          string   `yaml:"verified_at,omitempty"`
	Transaction         string   `yaml:"transaction,omitempty"`
}

type deploymentState struct {
	APIVersion    string `yaml:"api_version"`
	ID            string `yaml:"id"`
	Status        string `yaml:"status"`
	CreatedAt     string `yaml:"created_at"`
	ActivatedAt   string `yaml:"activated_at,omitempty"`
	DeactivatedAt string `yaml:"deactivated_at,omitempty"`
	VerifiedAt    string `yaml:"verified_at,omitempty"`
	Predecessor   string `yaml:"predecessor,omitempty"`
	Failure       string `yaml:"failure,omitempty"`
	// There is deliberately no snapshot_id. A single field could only ever name
	// one snapshot, and it existed to answer "which snapshot do I use to get
	// from Y back to X" — a question that stops being asked once a snapshot is
	// a self-sufficient point in time rather than one leg of a transition.
	// Keeping it would only open a window for the two writes to disagree.
}

type deploymentIndex struct {
	APIVersion  string            `yaml:"api_version"`
	GeneratedAt string            `yaml:"generated_at"`
	Deployments []deploymentState `yaml:"deployments"`
}

type prepareOptions struct {
	workspace  string
	base       string
	cfgPath    string
	moduleRoot string
	verbose    bool
	updateLock bool
	// modules narrows which images `build` builds. Empty means all of them.
	modules []string
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
	if !exists(out.cfgPath) {
		return out, preconditionErrorf("config_missing", "config %s does not exist", out.cfgPath)
	}
	root, err := locateModuleRoot(out.moduleRoot)
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
	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	reg, err := loadRegistryDir(opts.moduleRoot)
	if err != nil {
		return preconditionErrorf("module_root_invalid", "%s", err.Error())
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
	a := &app{cfg: cfg, cfgPath: opts.cfgPath, reg: reg, contracts: contracts, lock: lock, resolvedBindings: map[string]map[string]string{}}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	if err := a.validateContractRegistry(); err != nil {
		return preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrder(cfg.Modules.Order)
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
	// Accepted for command-line symmetry, but plan intentionally never creates
	// or reads runtime state, so it neither requires nor validates a workspace.
	_ = fs.String("w", "", "unused workspace path")
	_ = fs.String("workspace", "", "unused workspace path")
	registerJSONFlag(fs)
	if err := fs.Parse(args); err != nil {
		return usageErrorf("%s", err.Error())
	}
	if *cfgPath == "" || fs.NArg() != 0 {
		return usageErrorf("usage: anas plan -c config.yml [--module-root modules] [--json]")
	}
	explicit := *moduleRoot
	if explicit == "" {
		explicit = *rootAlias
	}
	located, err := locateModuleRoot(explicit)
	if err != nil {
		return preconditionErrorf("module_root_missing", "%s", err.Error())
	}
	configPath := absolutePath(*cfgPath)
	if !exists(configPath) {
		return preconditionErrorf("config_missing", "config %s does not exist", configPath)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	reg, err := loadRegistryDir(located)
	if err != nil {
		return preconditionErrorf("module_root_invalid", "%s", err.Error())
	}
	contracts, err := loadContractRegistry(located)
	if err != nil {
		return preconditionErrorf("contract_root_invalid", "%s", err.Error())
	}
	a := &app{cfg: cfg, cfgPath: configPath, reg: reg, contracts: contracts, resolvedBindings: map[string]map[string]string{}}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	if err := a.validateContractRegistry(); err != nil {
		return preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrder(cfg.Modules.Order)
	if err != nil {
		return preconditionErrorf("resolution_failed", "%s", err.Error())
	}
	if err := a.validateVersions(&moduleLock{APIVersion: "anas.module-lock/v1", Modules: map[string]moduleLockRecord{}}); err != nil {
		return preconditionErrorf("version_conflict", "%s", err.Error())
	}
	// Defaults first: a module may supply the DNS vendor its hook would use, and
	// resolving credentials against a half-populated environment would report
	// a platform as unset when it is merely defaulted.
	a.applyModuleDefaults()
	if err := a.materializeDNSCredentials(); err != nil {
		return preconditionErrorf("dns_credentials_invalid", "%s", err.Error())
	}
	a.reportDynamicDNSOverlaps()
	if jsonMode {
		return emitOK(map[string]any{
			"config": configPath, "module_root": absolutePath(located),
			"modules": a.order, "iam": a.iamPlanDocument(),
			"capability_bindings": cloneNestedMap(a.resolvedBindings),
			"dns_platforms":       a.dnsPlanDocument(),
			"dynamic_dns":         a.dynamicDNSPlanDocument(),
		})
	}
	fmt.Println(strings.Join(a.order, "\n"))
	fmt.Print(a.iamPlanSummary())
	fmt.Print(a.dnsPlanSummary())
	fmt.Print(a.dynamicDNSPlanSummary())
	return nil
}

func runPrepare(action string, args []string, jsonMode bool) error {
	opts, err := parsePrepareOptions(action, args)
	if err != nil {
		return err
	}
	announceWorkspace(opts.workspace)
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
	if err := os.MkdirAll(stagingModules, 0700); err != nil {
		return "", failuref("mkdir_failed", "%s", err.Error())
	}

	a := &app{
		workspace: opts.workspace,
		base:      opts.base, cfgPath: opts.cfgPath, verbose: opts.verbose,
		reg: reg, contracts: contracts, cfg: cfg, lock: lock, artifactRoot: finalModules,
		resolvedBindings: map[string]map[string]string{},
	}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	a.applyWorkspaceEnv()
	if err := a.validateContractRegistry(); err != nil {
		return "", preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrder(cfg.Modules.Order)
	if err != nil {
		return "", preconditionErrorf("resolution_failed", "%s", err.Error())
	}
	total := int64(len(a.order))
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
		if err := saveModuleLockFile(lockPath, lock); err != nil {
			return "", failuref("write_failed", "%s", err.Error())
		}
	} else if err := validateLockedResolution(a, lock); err != nil {
		return "", preconditionErrorf("lock_stale", "%s", err.Error())
	}
	a.applyModuleDefaults()
	if err := a.materializeDNSCredentials(); err != nil {
		return "", preconditionErrorf("dns_credentials_invalid", "%s", err.Error())
	}
	a.reportDynamicDNSOverlaps()
	secrets, err := loadSecretStore(opts.base)
	if err != nil {
		return "", preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	a.secrets = secrets
	if err := a.materializeResourceSecrets(); err != nil {
		return "", preconditionErrorf("resource_invalid", "%s", err.Error())
	}
	if err := a.materializeLocalAccounts(); err != nil {
		return "", preconditionErrorf("local_admin_invalid", "%s", err.Error())
	}
	emitProgress(jsonMode, "calculate", 0, total, "modules")
	// Part of the calculate stage, and reported as such: it is the derivation
	// every module hook reads from, so a host it cannot describe is a failure of
	// the same step rather than a new contract code.
	if err := a.applyHostNetwork(); err != nil {
		return "", failuref("calculate_failed", "%s", err.Error())
	}
	if err := a.calculate(); err != nil {
		return "", failuref("calculate_failed", "%s", err.Error())
	}
	if err := a.secrets.Save(); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	if err := a.localAdmins.Save(); err != nil {
		return "", failuref("write_failed", "%s", err.Error())
	}
	emitProgress(jsonMode, "render", 0, total, "modules")
	if err := a.renderAll(stagingModules); err != nil {
		return "", failuref("render_failed", "%s", err.Error())
	}
	emitProgress(jsonMode, "render", total, total, "modules")

	if build {
		cli, err := compose.Detect()
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
			emitProgress(jsonMode, "build-images", done, total, "modules")
			if run.mod.RuntimeType != "compose" {
				return nil
			}
			args := append([]string{"build"}, run.services...)
			return a.compose.RunFile(run.dir, "anas_"+run.mod.Name, run.mod.ComposeFile, run.env, args...)
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
	emitProgress(jsonMode, "seal", 0, 0, "deployments")
	if err := sealDeployment(stagingRoot); err != nil {
		return "", failuref("seal_failed", "%s", err.Error())
	}
	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return "", failuref("promote_failed", "%s", err.Error())
	}
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
	for _, name := range a.order {
		record, ok := lock.Modules[name]
		if !ok {
			return fmt.Errorf("config lock has no module %q; run anas lock", name)
		}
		if record.Version != a.reg[name].Version || record.Revision != a.reg[name].Revision {
			return fmt.Errorf("module %q is locked at %s-r%d but source provides %s-r%d; run anas lock to update explicitly", name, record.Version, record.Revision, a.reg[name].Version, a.reg[name].Revision)
		}
		digest, err := moduleBundleDigest(a.reg[name].SourceDir)
		if err != nil {
			return err
		}
		if record.Digest != digest {
			return fmt.Errorf("module %q bundle digest does not match config lock; run anas lock to update explicitly", name)
		}
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
				return fmt.Errorf("%s capability %s resolved to %s but lock records %s; run anas lock", module, capability, provider, lock.Bindings[module][capability])
			}
		}
	}
	return nil
}

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
		digest, err := normalizedModuleDigest(
			filepath.Join(a.base, "staging", id, "modules", name),
			filepath.Join(a.base, "deployments", id),
		)
		if err != nil {
			return nil, fmt.Errorf("digest rendered module %s: %w", name, err)
		}
		manifest.Modules[name] = deploymentModule{
			Name: name, Version: mod.Version, Revision: mod.Revision, AppVersion: mod.AppVersion,
			ArtifactDeployment: id, RenderDigest: digest,
			DataBreaking: cloneStringListPointer(mod.DataBreaking),
			RuntimeType:  mod.RuntimeType, ComposeFile: mod.ComposeFile,
			Hook: mod.Hook, EnvPrefix: mod.EnvPrefix,
			Consumes:     append([]string{}, mod.Consumes...),
			Dependencies: append([]string{}, a.deps[name]...),
			UseHostLAN:   mod.UseHostLAN, Changes: mod.Changes,
			Providers: cloneContractProviders(mod.ContractProviders),
		}
	}
	for _, request := range a.resourceRequests {
		manifest.Resources = append(manifest.Resources, deploymentResource{
			Consumer: request.Consumer, ID: request.ID, Contract: request.Contract, ContractVersion: request.ContractVersion,
			Provider: request.Provider, Interface: request.Interface,
			Spec: request.Spec, SecretKey: request.SecretKey,
		})
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
	base := stateDir(workspace)
	if *deploymentID == "" {
		*cfgPath = absolutePath(configPathFor(workspace, *cfgPath))
	} else if *cfgPath != "" {
		return usageErrorf("apply accepts either -c config.yml or --deployment ID, not both")
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	before, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	if *deploymentID == "" {
		explicit := *moduleRoot
		if explicit == "" {
			explicit = *rootAlias
		}
		located, err := locateModuleRoot(explicit)
		if err != nil {
			return preconditionErrorf("module_root_missing", "%s", err.Error())
		}
		if !exists(*cfgPath) {
			return preconditionErrorf("config_missing", "config %s does not exist", *cfgPath)
		}
		opts := prepareOptions{workspace: workspace, base: base, cfgPath: *cfgPath, moduleRoot: located, updateLock: *updateLock}
		id, err := materializeDeployment(opts, *build, jsonMode)
		if err != nil {
			return err
		}
		*deploymentID = id
	} else {
		if err := validateDeploymentID(*deploymentID); err != nil {
			return usageErrorf("%s", err.Error())
		}
		state, err := loadDeploymentState(base, *deploymentID)
		if err != nil {
			return preconditionErrorf("deployment_missing", "%s", err.Error())
		}
		if state.Status != "ready" {
			return preconditionErrorf("deployment_not_ready",
				"deployment %s has status %q; apply --deployment requires ready", *deploymentID, state.Status)
		}
	}
	if err := activateDeployment(base, *deploymentID, activateOptions{
		allowRisky: *allowRisky, snapshot: *snapshot, noSnapshot: *noSnapshot,
		yes: *yes, json: jsonMode,
	}); err != nil {
		return err
	}
	after, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": workspace, "deployment_id": *deploymentID,
			"previous_deployment": nullableString(before.ActiveDeployment),
			"activated_at":        after.ActivatedAt,
			"deployment_path":     filepath.Join(base, "deployments", *deploymentID),
		})
	}
	fmt.Println(*deploymentID)
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
	base := stateDir(workspace)
	unlock, err := acquireRuntimeSharedLock(base)
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	active, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	if active.ActiveDeployment == "" {
		return preconditionErrorf("no_active_deployment", "no active deployment; run anas apply first")
	}
	cli, err := compose.Detect()
	if err != nil {
		return preconditionErrorf("compose_missing", "%s", err.Error())
	}
	a, root, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
	if err != nil {
		return preconditionErrorf("deployment_unreadable", "%s", err.Error())
	}
	selection, err := selectLifecycleModules(a, action, requested)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	// A named selection is expanded to its dependency-safe chain and leaves the
	// macvlan bridge alone; the whole-deployment path still tears it down,
	// because then there is nothing left to use it.
	partial := len(requested) > 0
	wholeDeployment := !partial || len(selection) == len(a.order)
	stop := func() error {
		if partial {
			return a.stopModules(root, selection, jsonMode)
		}
		return a.stopRelease(root, jsonMode)
	}
	switch action {
	case "start":
		if err := startDeployment(a, root, selection, jsonMode); err != nil {
			return failuref("start_failed", "%s", err.Error())
		}
	case "restart":
		if err := stop(); err != nil {
			return failuref("stop_failed", "%s", err.Error())
		}
		if err := startDeployment(a, root, selection, jsonMode); err != nil {
			return failuref("start_failed", "%s", err.Error())
		}
	case "stop":
		if err := stop(); err != nil {
			return failuref("stop_failed", "%s", err.Error())
		}
	default:
		return usageErrorf("unknown lifecycle action %q", action)
	}
	if wholeDeployment {
		if action == "stop" {
			active.RuntimeStatus = "stopped"
		} else {
			active.RuntimeStatus = "running"
		}
		if err := saveActiveState(base, active); err != nil {
			return failuref("write_failed", "record runtime status: %s", err.Error())
		}
	}
	// start, restart and stop have no natural result: they either put the
	// deployment in the requested state or they did not. The envelope plus the
	// identifiers is the whole document, rather than a payload invented so it
	// would look substantial.
	return emitEmptyOK(jsonMode, map[string]any{
		"workspace": workspace, "action": action,
		"deployment_id": active.ActiveDeployment, "modules": selection,
	})
}

func startDeployment(a *app, modulesRoot string, selection []string, jsonMode bool) error {
	for _, name := range selection {
		dir := filepath.Join(modulesRoot, name)
		if mod := a.reg[name]; a.useFrozenHooks && mod.SourceDir != "" {
			dir = mod.SourceDir
		}
		if _, err := parseEnvFile(filepath.Join(dir, ".env")); err != nil {
			return fmt.Errorf("deployment module %s env: %w", name, err)
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
		emitProgress(jsonMode, "start-containers", done, total, "modules")
		if err := a.ensureResourcesFor(run.mod.Name, modulesRoot); err != nil {
			return err
		}
		if run.mod.RuntimeType != "compose" {
			return nil
		}
		args := append([]string{"up", "-d", "--remove-orphans"}, run.services...)
		return a.compose.RunFile(run.dir, "anas_"+run.mod.Name, run.mod.ComposeFile, run.env, args...)
	}); err != nil {
		return err
	}
	emitProgress(jsonMode, "after-start-hooks", 0, total, "modules")
	return a.runAfterStartOf(modulesRoot, selection)
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
	json bool
}

func activateDeployment(base, id string, opts activateOptions) error {
	cli, err := compose.Detect()
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
	if active.ActiveDeployment != "" {
		oldApp, oldRoot, current, err = loadDeploymentApp(base, active.ActiveDeployment, cli)
		if err != nil {
			return preconditionErrorf("deployment_unreadable", "%s", err.Error())
		}
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
			if err := snapshotBeforeApply(base, opts, oldApp, oldRoot, current, target); err != nil {
				return err
			}
		}
		if err := stopRemovedDeployments(oldApp, oldRoot, target, opts.json); err != nil {
			return failuref("stop_failed", "%s", err.Error())
		}
	}
	if active.ActiveDeployment == "" && target.BuildAcceleration && !target.ImagesBuilt {
		return imageRebuildRequiredError([]string{"global.chinese_build_speedup"})
	}

	selection := activationStartModules(current, target, active.RuntimeStatus)
	if err := startDeployment(newApp, newRoot, selection, opts.json); err != nil {
		_ = saveDeploymentFailure(base, id, err)
		if oldApp != nil {
			_ = startDeployment(oldApp, oldRoot, oldApp.order, opts.json)
		}
		return failuref("start_failed", "%s", err.Error())
	}
	if err := retainRemovedResources(base, current, target); err != nil {
		if oldApp != nil {
			_ = startDeployment(oldApp, oldRoot, oldApp.order, opts.json)
		}
		return failuref("resource_state_failed", "%s", err.Error())
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
			_ = startDeployment(oldApp, oldRoot, oldApp.order, opts.json)
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
	collectAutomaticSnapshots(workspaceOf(base))
	return nil
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
func snapshotBeforeApply(base string, opts activateOptions, oldApp *app, oldRoot string, current, target *deploymentManifest) error {
	trigger := deploymentSnapshotTrigger(current, target)
	if trigger == nil {
		if !opts.snapshot {
			return nil
		}
		trigger = &applySnapshotTrigger{reason: snapshotReasonPreApply, detail: "--snapshot was requested"}
	}
	workspace := workspaceOf(base)
	if opts.noSnapshot {
		emitWarning(opts.json, trigger.reason, "%s", trigger.detail)
		emitWarning(opts.json, "no_snapshot_requested",
			"--no-snapshot gives up the only way back to the current data")
		return confirmDestructive("Apply without a data snapshot", opts.yes)
	}
	if code, reason := snapshotUnavailable(workspace, target); code != "" {
		emitWarning(opts.json, trigger.reason, "%s", trigger.detail)
		emitWarning(opts.json, code,
			"%s, so no snapshot can be taken and the current data cannot be recovered afterwards", reason)
		return confirmDestructive("Apply anyway, with no way back to the current data", opts.yes)
	}
	// Quiescing first: a snapshot taken while services are mid-write captures a
	// crash-consistent database rather than a clean one.
	if err := oldApp.stopRelease(oldRoot, opts.json); err != nil {
		return failuref("quiesce_failed", "quiesce active deployment before data snapshot: %v", err)
	}
	// The snapshot is not recorded against this deployment. It stands on its
	// own, carrying the artifact and config it belongs to, and is found by
	// listing snapshots rather than by following a pointer.
	if _, err := createSnapshot(workspace, snapshotOptions{
		kind: snapshotKindAuto, reason: trigger.reason,
		from: current.ID, to: target.ID, json: opts.json,
	}); err != nil {
		_ = startDeployment(oldApp, oldRoot, oldApp.order, opts.json)
		return err
	}
	return nil
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
func collectAutomaticSnapshots(workspace string) {
	all, err := listSnapshots(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not scan snapshots for collection: %v\n", err)
		return
	}
	collect, _, _ := snapshotsToPrune(all, workspaceKeepAuto(workspace))
	for _, meta := range collect {
		if err := deleteSnapshot(workspace, meta.ID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not reclaim snapshot %s: %v\n", meta.ID, err)
		}
	}
}

func stopRemovedDeployments(oldApp *app, oldRoot string, target *deploymentManifest, jsonMode bool) error {
	var errs []error
	removed := int64(0)
	for i := len(oldApp.order) - 1; i >= 0; i-- {
		name := oldApp.order[i]
		if contains(target.ModuleOrder, name) {
			continue
		}
		mod := oldApp.reg[name]
		if mod.RuntimeType != "compose" {
			continue
		}
		removed++
		emitProgress(jsonMode, "stop-removed-modules", removed, 0, "modules")
		dir := filepath.Join(oldRoot, name)
		if err := oldApp.compose.RunFile(dir, "anas_"+name, mod.ComposeFile, oldApp.moduleEnv(dir), "down"); err != nil {
			errs = append(errs, fmt.Errorf("stop removed module %s: %w", name, err))
		}
	}
	return errorsJoin(errs)
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
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	target := ""
	if len(positional) == 1 {
		target = positional[0]
		if err := validateDeploymentID(target); err != nil {
			return usageErrorf("%s", err.Error())
		}
	} else if len(active.PreviousDeployments) > 0 {
		target = active.PreviousDeployments[0]
	}
	if target == "" {
		return preconditionErrorf("no_previous_deployment", "no previous deployment to roll back to")
	}
	if target == active.ActiveDeployment {
		return preconditionErrorf("already_active", "deployment %s is already active", target)
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	// Rollback never touches data. It swaps the artifact, which is the right
	// answer whenever the data is fine and the configuration or the version is
	// not — by far the most common reason to roll back. Rewinding data is
	// `anas snapshot restore`, a different operation with a different blast
	// radius, and offering it here as a flag made the destructive case one
	// typo away from the safe one.
	if err := activateDeployment(base, target, activateOptions{
		allowRisky: *allowRisky, rollback: true, json: jsonMode,
	}); err != nil {
		return err
	}
	after, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	return emitEmptyOK(jsonMode, map[string]any{
		"workspace": workspace, "deployment_id": target,
		"previous_deployment": nullableString(active.ActiveDeployment),
		"activated_at":        after.ActivatedAt,
		"data_touched":        false,
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
// even though `btrfs subvolume create`, `snapshot` and `delete` all succeed
// unprivileged. Using it as a precondition put the entire snapshot feature
// behind root for no reason.
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
	workspace, base, err := parseBaseOnly("status", args)
	if err != nil {
		return err
	}
	active, err := loadActiveState(base)
	if err != nil {
		return preconditionErrorf("state_unreadable", "%s", err.Error())
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace":            workspace,
			"active_deployment":    nullableString(active.ActiveDeployment),
			"activated_at":         nullableString(active.ActivatedAt),
			"verified_at":          nullableString(active.VerifiedAt),
			"previous_deployments": append([]string{}, active.PreviousDeployments...),
		})
	}
	if active.ActiveDeployment == "" {
		fmt.Println("active: none")
		return nil
	}
	fmt.Printf("active: %s\nactivated_at: %s\nverified_at: %s\n", active.ActiveDeployment, active.ActivatedAt, active.VerifiedAt)
	if len(active.PreviousDeployments) > 0 {
		fmt.Println("previous: " + strings.Join(active.PreviousDeployments, ","))
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
	workspace, base, rest, err := parseBaseArgs("deployments "+sub, args[1:])
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		if len(rest) != 0 {
			return usageErrorf("usage: anas deployments list [-w <workspace>] [--json]")
		}
		states, err := listDeploymentStates(base)
		if err != nil {
			return preconditionErrorf("state_unreadable", "%s", err.Error())
		}
		if jsonMode {
			return emitOK(map[string]any{
				"workspace": workspace, "deployments": deploymentStateDocuments(states),
			})
		}
		for _, state := range states {
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
		root := filepath.Join(base, "deployments", rest[0])
		if jsonMode {
			// The YAML the human form prints is an on-disk format. Re-reading it
			// into the manifest type and emitting that is what makes `inspect
			// --json` a JSON document rather than YAML on stdout, which no
			// amount of JSON.parse would have accepted.
			manifest, err := loadDeploymentManifest(root)
			if err != nil {
				return preconditionErrorf("deployment_missing", "%s", err.Error())
			}
			state, err := loadDeploymentState(base, rest[0])
			if err != nil {
				return preconditionErrorf("state_unreadable", "%s", err.Error())
			}
			return emitOK(map[string]any{
				"workspace": workspace, "deployment_path": root,
				"deployment": manifest, "state": deploymentStateDocument(state),
			})
		}
		b, err := os.ReadFile(filepath.Join(root, "deployment.yml"))
		if err != nil {
			return preconditionErrorf("deployment_missing", "%s", err.Error())
		}
		fmt.Print(string(b))
		return nil
	default:
		return usageErrorf("unknown deployments command %q; expected list or inspect", sub)
	}
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
	for name, module := range manifest.Modules {
		artifactDeployment := module.ArtifactDeployment
		if artifactDeployment == "" {
			artifactDeployment = manifest.ID
		}
		artifactRoot := filepath.Join(base, "deployments", artifactDeployment, "modules")
		reg[name] = Module{
			Name: name, Version: module.Version, Revision: module.Revision, AppVersion: module.AppVersion,
			SourceDir: filepath.Join(artifactRoot, name), EnvPrefix: module.EnvPrefix,
			Consumes: append([]string{}, module.Consumes...), Changes: module.Changes,
			UseHostLAN: module.UseHostLAN, Hook: module.Hook,
			RuntimeType: module.RuntimeType, ComposeFile: module.ComposeFile,
			ContractProviders: cloneContractProviders(module.Providers),
		}
		deps[name] = append([]string{}, module.Dependencies...)
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		return nil, "", nil, err
	}
	a := &app{
		base: base, compose: cli, reg: reg, deps: deps,
		order: append([]string{}, manifest.ModuleOrder...),
		env:   map[string]string{}, envOwner: map[string]string{},
		secrets: secrets, useFrozenHooks: true, artifactRoot: modulesRoot,
		resolvedBindings: cloneNestedMap(manifest.Bindings),
	}
	for _, resource := range manifest.Resources {
		password := secrets.values[resource.SecretKey]
		if password == "" {
			return nil, "", nil, fmt.Errorf("resource %s.%s secret %s is missing", resource.Consumer, resource.ID, resource.SecretKey)
		}
		a.resourceRequests = append(a.resourceRequests, ResourceRequest{
			Consumer: resource.Consumer, ID: resource.ID, Contract: resource.Contract, ContractVersion: resource.ContractVersion,
			Provider: resource.Provider, Interface: resource.Interface,
			Spec: resource.Spec, SecretKey: resource.SecretKey, Password: password,
		})
	}
	a.adoptReleaseEnv(modulesRoot)
	return a, modulesRoot, manifest, nil
}

func validateDeploymentID(id string) error {
	if id == "" || filepath.Base(id) != id || id == "." || id == ".." || strings.ContainsAny(id, `/\\`) {
		return fmt.Errorf("invalid deployment id %q", id)
	}
	return nil
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
		filepath.Join(base, "staging"),
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
	unlock, err := acquireRuntimeLockMode(base, syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	cleanStaleSnapshotTemp(workspaceOf(base))
	compensateContainerTransactions(base)
	return unlock, nil
}

func acquireRuntimeSharedLock(base string) (func(), error) {
	return acquireRuntimeLockMode(base, syscall.LOCK_SH)
}

func acquireRuntimeLockMode(base string, mode int) (func(), error) {
	if err := ensureRuntimeLayout(base); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(base, "state", "lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), mode); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock runtime state: %w", err)
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
