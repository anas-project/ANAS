package runner

import (
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

	"github.com/whlsxl/anas/internal/compose"
	"github.com/whlsxl/anas/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	deploymentAPIVersion = "anas.deployment/v1"
	activeStateVersion   = "anas.state/v2"
)

type deploymentManifest struct {
	APIVersion        string                       `yaml:"api_version"`
	ID                string                       `yaml:"id"`
	CreatedAt         string                       `yaml:"created_at"`
	ConfigFingerprint string                       `yaml:"config_fingerprint"`
	ModuleOrder       []string                     `yaml:"module_order"`
	Bindings          map[string]map[string]string `yaml:"capability_bindings,omitempty"`
	Casks             map[string]deploymentCask    `yaml:"casks"`
	Settings          map[string]deploymentSetting `yaml:"settings,omitempty"`
	Snapshot          deploymentSnapshotPolicy     `yaml:"snapshot,omitempty"`
}

type deploymentCask struct {
	Name         string                  `yaml:"name"`
	Version      string                  `yaml:"version"`
	AppVersion   string                  `yaml:"app_version,omitempty"`
	RuntimeType  string                  `yaml:"runtime"`
	ComposeFile  string                  `yaml:"compose_file,omitempty"`
	Hook         HookConfig              `yaml:"hook,omitempty"`
	EnvPrefix    string                  `yaml:"env_prefix,omitempty"`
	Consumes     []string                `yaml:"consumes,omitempty"`
	Dependencies []string                `yaml:"dependencies,omitempty"`
	UseHostLAN   string                  `yaml:"host_lan,omitempty"`
	Changes      map[string]ChangePolicy `yaml:"changes,omitempty"`
}

type deploymentSetting struct {
	Fingerprint string `yaml:"fingerprint"`
	Module      string `yaml:"module"`
	Parameter   string `yaml:"parameter"`
	Effect      string `yaml:"effect"`
	Apply       string `yaml:"apply,omitempty"`
}

type deploymentSnapshotPolicy struct {
	Backend string `yaml:"backend,omitempty"`
	Source  string `yaml:"source,omitempty"`
	Root    string `yaml:"root,omitempty"`
}

type dataSnapshot struct {
	APIVersion     string `yaml:"api_version"`
	ID             string `yaml:"id"`
	Backend        string `yaml:"backend"`
	CreatedAt      string `yaml:"created_at"`
	Source         string `yaml:"source"`
	Path           string `yaml:"path"`
	FromDeployment string `yaml:"from_deployment"`
	ToDeployment   string `yaml:"to_deployment"`
	RecoveryPath   string `yaml:"recovery_path,omitempty"`
}

type activeDeploymentState struct {
	APIVersion          string   `yaml:"api_version"`
	ActiveDeployment    string   `yaml:"active_deployment,omitempty"`
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
	SnapshotID    string `yaml:"snapshot_id,omitempty"`
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
	caskRoot   string
	verbose    bool
	updateLock bool
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
	fs.StringVar(&out.caskRoot, "cask-root", "", "directory containing cask bundles")
	fs.StringVar(&out.caskRoot, "root", "", "project root or cask bundle directory")
	fs.BoolVar(&out.verbose, "verbose", false, "debug logging")
	fs.BoolVar(&out.updateLock, "update-lock", false, "create or update the config lock")
	if err := fs.Parse(args); err != nil {
		return out, err
	}
	if fs.NArg() != 0 {
		return out, fmt.Errorf("usage: anas %s [-w <workspace>] [-c config.yml] [--update-lock]", name)
	}
	workspace, err := resolveWorkspace(out.workspace)
	if err != nil {
		return out, err
	}
	out.workspace = workspace
	out.base = stateDir(workspace)
	// A workspace owns its config, so -c is only needed to point at one that
	// lives elsewhere.
	out.cfgPath = configPathFor(workspace, out.cfgPath)
	if !exists(out.cfgPath) {
		return out, fmt.Errorf("config %s does not exist", out.cfgPath)
	}
	if out.caskRoot != "" && exists(filepath.Join(out.caskRoot, "casks", "mods")) {
		out.caskRoot = filepath.Join(out.caskRoot, "casks", "mods")
	}
	root, err := locateCaskRoot(out.caskRoot)
	if err != nil {
		return out, err
	}
	out.caskRoot = root
	return out, nil
}

func runLock(args []string) error {
	opts, err := parsePrepareOptions("lock", args)
	if err != nil {
		return err
	}
	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return err
	}
	reg, err := loadRegistryDir(opts.caskRoot)
	if err != nil {
		return err
	}
	lock, err := loadCaskLockFile(projectLockPath(opts.cfgPath))
	if err != nil {
		return err
	}
	a := &app{cfg: cfg, cfgPath: opts.cfgPath, reg: reg, lock: lock, resolvedBindings: map[string]map[string]string{}}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	a.order, err = a.resolveOrder(cfg.Modules)
	if err != nil {
		return err
	}
	if err := a.validateVersions(lock); err != nil {
		return err
	}
	if err := a.updateCaskLock(lock, true); err != nil {
		return err
	}
	if err := saveCaskLockFile(projectLockPath(opts.cfgPath), lock); err != nil {
		return err
	}
	fmt.Println(projectLockPath(opts.cfgPath))
	return nil
}

func runPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	caskRoot := fs.String("cask-root", "", "directory containing cask bundles")
	rootAlias := fs.String("root", "", "project root or cask bundle directory")
	// Accepted for command-line symmetry, but plan intentionally never creates
	// or reads runtime state, so it neither requires nor validates a workspace.
	_ = fs.String("w", "", "unused workspace path")
	_ = fs.String("workspace", "", "unused workspace path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || fs.NArg() != 0 {
		return fmt.Errorf("usage: anas plan -c config.yml [--cask-root casks/mods]")
	}
	explicit := *caskRoot
	if explicit == "" {
		explicit = *rootAlias
	}
	if explicit != "" && exists(filepath.Join(explicit, "casks", "mods")) {
		explicit = filepath.Join(explicit, "casks", "mods")
	}
	located, err := locateCaskRoot(explicit)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	reg, err := loadRegistryDir(located)
	if err != nil {
		return err
	}
	a := &app{cfg: cfg, cfgPath: *cfgPath, reg: reg, resolvedBindings: map[string]map[string]string{}}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	a.order, err = a.resolveOrder(cfg.Modules)
	if err != nil {
		return err
	}
	if err := a.validateVersions(&caskLock{APIVersion: "anas.dev/v1", Casks: map[string]caskLockRecord{}}); err != nil {
		return err
	}
	fmt.Println(strings.Join(a.order, "\n"))
	fmt.Print(a.iamPlanSummary())
	return nil
}

func runPrepare(action string, args []string) error {
	opts, err := parsePrepareOptions(action, args)
	if err != nil {
		return err
	}
	id, err := materializeDeployment(opts, action == "build")
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func materializeDeployment(opts prepareOptions, build bool) (string, error) {
	if err := ensureRuntimeLayout(opts.base); err != nil {
		return "", err
	}
	cfg, err := config.Load(opts.cfgPath)
	if err != nil {
		return "", err
	}
	reg, err := loadRegistryDir(opts.caskRoot)
	if err != nil {
		return "", err
	}
	lockPath := projectLockPath(opts.cfgPath)
	lock, err := loadCaskLockFile(lockPath)
	if err != nil {
		return "", err
	}
	if len(lock.Casks) == 0 && !opts.updateLock {
		return "", fmt.Errorf("missing config lock %s; run `anas lock -c %s` or pass --update-lock", lockPath, opts.cfgPath)
	}

	id, err := newDeploymentID()
	if err != nil {
		return "", err
	}
	stagingRoot := filepath.Join(opts.base, "staging", id)
	finalRoot := filepath.Join(opts.base, "deployments", id)
	stagingCasks := filepath.Join(stagingRoot, "casks")
	finalCasks := filepath.Join(finalRoot, "casks")
	if exists(stagingRoot) || exists(finalRoot) {
		return "", fmt.Errorf("deployment id collision %s", id)
	}
	if err := os.MkdirAll(stagingCasks, 0700); err != nil {
		return "", err
	}

	a := &app{
		workspace: opts.workspace,
		base:      opts.base, cfgPath: opts.cfgPath, verbose: opts.verbose,
		reg: reg, cfg: cfg, lock: lock, artifactRoot: finalCasks,
		resolvedBindings: map[string]map[string]string{},
	}
	a.env, a.envOwner = cfg.BaseEnvWithOwners()
	a.applyWorkspaceEnv()
	a.order, err = a.resolveOrder(cfg.Modules)
	if err != nil {
		return "", err
	}
	if opts.updateLock {
		if err := a.validateVersions(lock); err != nil {
			return "", err
		}
		if err := a.updateCaskLock(lock, true); err != nil {
			return "", err
		}
		if err := saveCaskLockFile(lockPath, lock); err != nil {
			return "", err
		}
	} else if err := validateLockedResolution(a, lock); err != nil {
		return "", err
	}
	a.applyModuleDefaults()
	secrets, err := loadSecretStore(opts.base)
	if err != nil {
		return "", err
	}
	a.secrets = secrets
	if err := a.calculate(); err != nil {
		return "", err
	}
	if err := a.secrets.Save(); err != nil {
		return "", err
	}
	if err := a.renderAll(stagingCasks); err != nil {
		return "", err
	}

	if build {
		cli, err := compose.Detect()
		if err != nil {
			return "", err
		}
		a.compose = cli
		if err := a.each(stagingCasks, func(run caskRun) error {
			if run.mod.Name == "core" {
				return nil
			}
			args := append([]string{"build"}, run.services...)
			return a.compose.RunFile(run.dir, "anas_"+run.mod.Name, run.mod.ComposeFile, run.env, args...)
		}); err != nil {
			return "", err
		}
	}

	manifest, err := buildDeploymentManifest(a, id, opts.cfgPath)
	if err != nil {
		return "", err
	}
	if err := writeYAMLAtomic(filepath.Join(stagingRoot, "deployment.yml"), manifest, 0600); err != nil {
		return "", err
	}
	if err := saveCaskLockFile(filepath.Join(stagingRoot, "lock.yml"), lock); err != nil {
		return "", err
	}
	if err := copyFileMode(opts.cfgPath, deploymentConfigSourcePath(stagingRoot), 0600); err != nil {
		return "", fmt.Errorf("preserve the config this deployment was built from: %w", err)
	}
	if err := sealDeployment(stagingRoot); err != nil {
		return "", err
	}
	if err := os.Rename(stagingRoot, finalRoot); err != nil {
		return "", err
	}
	state := deploymentState{
		APIVersion: activeStateVersion, ID: id, Status: "ready",
		CreatedAt: manifest.CreatedAt,
	}
	if err := saveDeploymentState(opts.base, state); err != nil {
		return "", err
	}
	return id, nil
}

func validateLockedResolution(a *app, lock *caskLock) error {
	for _, name := range a.order {
		record, ok := lock.Casks[name]
		if !ok {
			return fmt.Errorf("config lock has no cask %q; run anas lock", name)
		}
		if record.Version != a.reg[name].Version {
			return fmt.Errorf("cask %q is locked at %s but source provides %s; run anas lock to update explicitly", name, record.Version, a.reg[name].Version)
		}
		digest, err := caskBundleDigest(a.reg[name].SourceDir)
		if err != nil {
			return err
		}
		if record.Digest != digest {
			return fmt.Errorf("cask %q bundle digest does not match config lock; run anas lock to update explicitly", name)
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

func buildDeploymentManifest(a *app, id, cfgPath string) (*deploymentManifest, error) {
	settings, err := config.Settings(cfgPath)
	if err != nil {
		return nil, err
	}
	manifest := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: id,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ModuleOrder: append([]string{}, a.order...), Bindings: cloneNestedMap(a.resolvedBindings),
		Casks: map[string]deploymentCask{}, Settings: map[string]deploymentSetting{},
		Snapshot: deploymentSnapshotPolicy{
			Backend: strings.ToLower(strings.TrimSpace(a.cfg.Rollback.Snapshot.Backend)),
			Source:  strings.TrimSpace(a.cfg.Rollback.Snapshot.Source),
			Root:    strings.TrimSpace(a.cfg.Rollback.Snapshot.Root),
		},
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
		manifest.Casks[name] = deploymentCask{
			Name: name, Version: mod.Version, AppVersion: mod.AppVersion,
			RuntimeType: mod.RuntimeType, ComposeFile: mod.ComposeFile,
			Hook: mod.Hook, EnvPrefix: mod.EnvPrefix,
			Consumes:     append([]string{}, mod.Consumes...),
			Dependencies: append([]string{}, a.deps[name]...),
			UseHostLAN:   mod.UseHostLAN, Changes: mod.Changes,
		}
	}
	for path, value := range settings {
		target := targetForSettingPath(path, a.reg)
		policy := policyForTarget(target, a.reg)
		manifest.Settings[path] = deploymentSetting{
			Fingerprint: hashSetting(value), Module: target.Module,
			Parameter: target.Parameter, Effect: policy.Effect, Apply: policy.Apply,
		}
	}
	return manifest, nil
}

func cloneNestedMap(in map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for k, values := range in {
		out[k] = cloneMap(values)
	}
	return out
}

func runApply(args []string) error {
	fs := flag.NewFlagSet("apply", flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	caskRoot := fs.String("cask-root", "", "directory containing cask bundles")
	rootAlias := fs.String("root", "", "project root or cask bundle directory")
	deploymentID := fs.String("deployment", "", "existing ready deployment id")
	build := fs.Bool("build", false, "build images before activation")
	updateLock := fs.Bool("update-lock", false, "create or update the config lock")
	allowRisky := fs.Bool("allow-risky", false, "allow changes requiring explicit migration or credential rotation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: anas apply [-w <workspace>] [-c config.yml | --deployment ID]")
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	announceWorkspace(workspace)
	base := stateDir(workspace)
	if *deploymentID == "" {
		*cfgPath = configPathFor(workspace, *cfgPath)
	} else if *cfgPath != "" {
		return fmt.Errorf("apply accepts either -c config.yml or --deployment ID, not both")
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		return err
	}
	defer unlock()
	if *deploymentID == "" {
		explicit := *caskRoot
		if explicit == "" {
			explicit = *rootAlias
		}
		if explicit != "" && exists(filepath.Join(explicit, "casks", "mods")) {
			explicit = filepath.Join(explicit, "casks", "mods")
		}
		located, err := locateCaskRoot(explicit)
		if err != nil {
			return err
		}
		opts := prepareOptions{workspace: workspace, base: base, cfgPath: *cfgPath, caskRoot: located, updateLock: *updateLock}
		id, err := materializeDeployment(opts, *build)
		if err != nil {
			return err
		}
		*deploymentID = id
	} else {
		state, err := loadDeploymentState(base, *deploymentID)
		if err != nil {
			return err
		}
		if state.Status != "ready" {
			return fmt.Errorf("deployment %s has status %q; apply --deployment requires ready", *deploymentID, state.Status)
		}
	}
	if err := activateDeployment(base, *deploymentID, *allowRisky, false); err != nil {
		return err
	}
	fmt.Println(*deploymentID)
	return nil
}

func runActive(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: anas %s [-w <workspace>]", action)
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	announceWorkspace(workspace)
	base := stateDir(workspace)
	unlock, err := acquireRuntimeSharedLock(base)
	if err != nil {
		return err
	}
	defer unlock()
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	if active.ActiveDeployment == "" {
		return fmt.Errorf("no active deployment; run anas apply first")
	}
	cli, err := compose.Detect()
	if err != nil {
		return err
	}
	a, root, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
	if err != nil {
		return err
	}
	switch action {
	case "start":
		return startDeployment(a, root)
	case "restart":
		if err := a.stopRelease(root); err != nil {
			return err
		}
		return startDeployment(a, root)
	case "stop":
		return a.stopRelease(root)
	default:
		return fmt.Errorf("unknown lifecycle action %q", action)
	}
}

func startDeployment(a *app, casksRoot string) error {
	for _, name := range a.order {
		if _, err := parseEnvFile(filepath.Join(casksRoot, name, ".env")); err != nil {
			return fmt.Errorf("deployment cask %s env: %w", name, err)
		}
	}
	a.adoptReleaseEnv(casksRoot)
	if err := a.ensureHostLAN(); err != nil {
		return err
	}
	if err := a.each(casksRoot, func(run caskRun) error {
		if run.mod.Name == "core" {
			return nil
		}
		args := append([]string{"up", "-d", "--remove-orphans"}, run.services...)
		return a.compose.RunFile(run.dir, "anas_"+run.mod.Name, run.mod.ComposeFile, run.env, args...)
	}); err != nil {
		return err
	}
	return a.runAfterStart(casksRoot)
}

func activateDeployment(base, id string, allowRisky, rollback bool) error {
	cli, err := compose.Detect()
	if err != nil {
		return err
	}
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	newApp, newRoot, target, err := loadDeploymentApp(base, id, cli)
	if err != nil {
		return err
	}
	if active.ActiveDeployment == id {
		return startDeployment(newApp, newRoot)
	}
	var oldApp *app
	var oldRoot string
	var current *deploymentManifest
	if active.ActiveDeployment != "" {
		oldApp, oldRoot, current, err = loadDeploymentApp(base, active.ActiveDeployment, cli)
		if err != nil {
			return err
		}
		blockers := deploymentChangeBlockers(current, target)
		if rollback {
			blockers = append(blockers, deploymentRollbackVersionBlockers(current, target)...)
			sort.Strings(blockers)
		}
		if len(blockers) > 0 && !allowRisky {
			verb := "apply"
			if rollback {
				verb = "rollback"
			}
			return fmt.Errorf("%s crosses guarded state changes:\n  %s\nrun the declared migration/rotation or repeat with --allow-risky", verb, strings.Join(blockers, "\n  "))
		}
		if !rollback && target.Snapshot.Backend != "" && target.Snapshot.Backend != "none" {
			if err := oldApp.stopRelease(oldRoot); err != nil {
				return fmt.Errorf("quiesce active deployment before data snapshot: %w", err)
			}
			snapshotID, err := createDeploymentSnapshot(base, current, target)
			if err != nil {
				_ = startDeployment(oldApp, oldRoot)
				return err
			}
			state, _ := loadDeploymentState(base, id)
			state.SnapshotID = snapshotID
			if err := saveDeploymentState(base, state); err != nil {
				_ = startDeployment(oldApp, oldRoot)
				return err
			}
		}
		if err := stopRemovedDeployments(oldApp, oldRoot, target); err != nil {
			return err
		}
	}

	if err := startDeployment(newApp, newRoot); err != nil {
		_ = saveDeploymentFailure(base, id, err)
		if oldApp != nil {
			_ = startDeployment(oldApp, oldRoot)
		}
		return err
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
		PreviousDeployments: previous, ActivatedAt: now, VerifiedAt: now,
	}
	if err := saveActiveState(base, active); err != nil {
		if oldApp != nil {
			_ = startDeployment(oldApp, oldRoot)
		}
		return err
	}
	state, _ := loadDeploymentState(base, id)
	state.Status = "active"
	state.ActivatedAt = now
	state.VerifiedAt = now
	if current != nil {
		state.Predecessor = current.ID
	}
	return saveDeploymentState(base, state)
}

func stopRemovedDeployments(oldApp *app, oldRoot string, target *deploymentManifest) error {
	var errs []error
	for i := len(oldApp.order) - 1; i >= 0; i-- {
		name := oldApp.order[i]
		if name == "core" || contains(target.ModuleOrder, name) {
			continue
		}
		mod := oldApp.reg[name]
		if mod.RuntimeType != "compose" {
			continue
		}
		dir := filepath.Join(oldRoot, name)
		if err := oldApp.compose.RunFile(dir, "anas_"+name, mod.ComposeFile, oldApp.caskEnv(dir), "down"); err != nil {
			errs = append(errs, fmt.Errorf("stop removed cask %s: %w", name, err))
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

func deploymentChangeBlockers(current, target *deploymentManifest) []string {
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
	blocked := []string{}
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
			blocked = append(blocked, fmt.Sprintf("%s (%s; %s)", key, setting.Effect, setting.Apply))
		}
	}
	sort.Strings(blocked)
	return blocked
}

// A cask version transition can include an application schema migration that
// is invisible in the YAML setting diff. Until a cask declares and implements
// a richer rollback contract, every reverse version/module transition is
// conservatively guarded.
func deploymentRollbackVersionBlockers(current, target *deploymentManifest) []string {
	if current == nil || target == nil {
		return nil
	}
	names := map[string]bool{}
	for name := range current.Casks {
		names[name] = true
	}
	for name := range target.Casks {
		names[name] = true
	}
	blocked := []string{}
	for name := range names {
		from, fromOK := current.Casks[name]
		to, toOK := target.Casks[name]
		switch {
		case !fromOK:
			blocked = append(blocked, fmt.Sprintf("cask %s removal (data compatibility unknown)", name))
		case !toOK:
			blocked = append(blocked, fmt.Sprintf("cask %s addition (data compatibility unknown)", name))
		case from.Version != to.Version || from.AppVersion != to.AppVersion:
			blocked = append(blocked, fmt.Sprintf("cask %s %s/%s -> %s/%s (data compatibility unknown)", name, from.Version, from.AppVersion, to.Version, to.AppVersion))
		}
	}
	sort.Strings(blocked)
	return blocked
}

func runDeploymentRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	allowRisky := fs.Bool("allow-risky", false, "allow guarded rollback without data restore")
	restoreData := fs.Bool("restore-data", false, "restore the recorded data snapshot")
	yes := fs.Bool("yes", false, "confirm destructive data restore")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(positional) > 1 {
		return fmt.Errorf("usage: anas rollback [DEPLOYMENT_ID] -w <workspace>")
	}
	// Rollback is the one command that can replace live data, and a workspace
	// inherited from the environment is the easiest thing to leave stale and
	// pointed somewhere else. It accepts only the flag.
	workspace, err := resolveWorkspaceStrict(*workspaceFlag, "rollback")
	if err != nil {
		return err
	}
	announceWorkspace(workspace)
	base := stateDir(workspace)
	active, err := loadActiveState(base)
	if err != nil {
		return err
	}
	target := ""
	if len(positional) == 1 {
		target = positional[0]
	} else if len(active.PreviousDeployments) > 0 {
		target = active.PreviousDeployments[0]
	}
	if target == "" {
		return fmt.Errorf("no previous deployment to roll back to")
	}
	if target == active.ActiveDeployment {
		return fmt.Errorf("deployment %s is already active", target)
	}
	unlock, err := acquireRuntimeLock(base)
	if err != nil {
		return err
	}
	defer unlock()
	if *restoreData {
		if !*yes {
			return fmt.Errorf("--restore-data is destructive and requires --yes")
		}
		cli, err := compose.Detect()
		if err != nil {
			return err
		}
		currentApp, currentRoot, _, err := loadDeploymentApp(base, active.ActiveDeployment, cli)
		if err != nil {
			return err
		}
		if err := currentApp.stopRelease(currentRoot); err != nil {
			return fmt.Errorf("stop active deployment before data restore: %w", err)
		}
		if err := restoreDeploymentSnapshot(base, active.ActiveDeployment, target); err != nil {
			_ = startDeployment(currentApp, currentRoot)
			return err
		}
		*allowRisky = true
	}
	return activateDeployment(base, target, *allowRisky, true)
}

func createDeploymentSnapshot(base string, current, target *deploymentManifest) (string, error) {
	policy := target.Snapshot
	backend := strings.ToLower(strings.TrimSpace(policy.Backend))
	if backend != "btrfs" {
		return "", fmt.Errorf("unsupported rollback.snapshot.backend %q (supported: btrfs, none)", policy.Backend)
	}
	source, err := filepath.Abs(policy.Source)
	if err != nil {
		return "", err
	}
	if err := btrfsSubvolumeShow(source); err != nil {
		return "", fmt.Errorf("snapshot source %s is not an accessible Btrfs subvolume: %w", source, err)
	}
	id, err := newDeploymentID()
	if err != nil {
		return "", err
	}
	root := policy.Root
	if root == "" {
		root = snapshotsDir(workspaceOf(base))
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	snapshotRoot := filepath.Join(root, id)
	if err := os.MkdirAll(snapshotRoot, 0700); err != nil {
		return "", err
	}
	snapshotPath := filepath.Join(snapshotRoot, "data")
	if err := runBtrfs("subvolume", "snapshot", "-r", source, snapshotPath); err != nil {
		return "", fmt.Errorf("create Btrfs data snapshot: %w", err)
	}
	meta := dataSnapshot{
		APIVersion: activeStateVersion, ID: id, Backend: backend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339), Source: source,
		Path: snapshotPath, FromDeployment: current.ID, ToDeployment: target.ID,
	}
	if err := writeYAMLAtomic(filepath.Join(snapshotRoot, "snapshot.yml"), &meta, 0600); err != nil {
		return "", err
	}
	return id, nil
}

func restoreDeploymentSnapshot(base, currentID, targetID string) error {
	state, err := loadDeploymentState(base, currentID)
	if err != nil {
		return err
	}
	if state.SnapshotID == "" {
		return fmt.Errorf("deployment %s has no recorded data snapshot", currentID)
	}
	currentManifest, err := loadDeploymentManifest(filepath.Join(base, "deployments", currentID))
	if err != nil {
		return err
	}
	snapshotRoot := currentManifest.Snapshot.Root
	if snapshotRoot == "" {
		snapshotRoot = snapshotsDir(workspaceOf(base))
	}
	metaPath := filepath.Join(snapshotRoot, state.SnapshotID, "snapshot.yml")
	var snapshot dataSnapshot
	if err := readYAML(metaPath, &snapshot); err != nil {
		return err
	}
	if snapshot.Backend != "btrfs" {
		return fmt.Errorf("snapshot %s uses unsupported backend %q", snapshot.ID, snapshot.Backend)
	}
	if snapshot.ToDeployment != currentID || snapshot.FromDeployment != targetID {
		return fmt.Errorf("snapshot %s covers %s -> %s, not requested rollback %s -> %s", snapshot.ID, snapshot.FromDeployment, snapshot.ToDeployment, currentID, targetID)
	}
	if err := btrfsSubvolumeShow(snapshot.Path); err != nil {
		return fmt.Errorf("recorded Btrfs snapshot is unavailable: %w", err)
	}
	if err := btrfsSubvolumeShow(snapshot.Source); err != nil {
		return fmt.Errorf("current data source is not an accessible Btrfs subvolume: %w", err)
	}
	recovery := snapshot.Source + ".rollback-recovery-" + snapshot.ID
	if exists(recovery) {
		return fmt.Errorf("recovery path already exists: %s", recovery)
	}
	if err := os.Rename(snapshot.Source, recovery); err != nil {
		return fmt.Errorf("preserve current data at %s: %w", recovery, err)
	}
	if err := runBtrfs("subvolume", "snapshot", snapshot.Path, snapshot.Source); err != nil {
		_ = os.Rename(recovery, snapshot.Source)
		return fmt.Errorf("restore Btrfs data snapshot: %w", err)
	}
	snapshot.RecoveryPath = recovery
	if err := writeYAMLAtomic(metaPath, &snapshot, 0600); err != nil {
		return err
	}
	return nil
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

func runStatus(args []string) error {
	base, err := parseBaseOnly("status", args)
	if err != nil {
		return err
	}
	active, err := loadActiveState(base)
	if err != nil {
		return err
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

func runDeployments(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("deployments requires list or inspect")
	}
	sub := args[0]
	base, rest, err := parseBaseArgs("deployments "+sub, args[1:])
	if err != nil {
		return err
	}
	switch sub {
	case "list":
		if len(rest) != 0 {
			return fmt.Errorf("usage: anas deployments list [-b ~/.anas]")
		}
		states, err := listDeploymentStates(base)
		if err != nil {
			return err
		}
		for _, state := range states {
			fmt.Printf("%s\t%s\t%s\n", state.ID, state.Status, state.CreatedAt)
		}
		return nil
	case "inspect":
		if len(rest) != 1 {
			return fmt.Errorf("usage: anas deployments inspect ID [-b ~/.anas]")
		}
		if err := validateDeploymentID(rest[0]); err != nil {
			return err
		}
		b, err := os.ReadFile(filepath.Join(base, "deployments", rest[0], "deployment.yml"))
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	default:
		return fmt.Errorf("unknown deployments command %q", sub)
	}
}

func parseBaseOnly(name string, args []string) (string, error) {
	base, rest, err := parseBaseArgs(name, args)
	if err != nil {
		return "", err
	}
	if len(rest) != 0 {
		return "", fmt.Errorf("usage: anas %s [-w <workspace>]", name)
	}
	return base, nil
}

// parseBaseArgs resolves the workspace and returns its state directory, which
// is what every read-only inspection command actually needs.
func parseBaseArgs(name string, args []string) (string, []string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return "", nil, err
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return "", nil, err
	}
	return stateDir(workspace), positional, nil
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
	casksRoot := filepath.Join(root, "casks")
	reg := map[string]Module{}
	deps := map[string][]string{}
	for name, cask := range manifest.Casks {
		reg[name] = Module{
			Name: name, Version: cask.Version, AppVersion: cask.AppVersion,
			SourceDir: filepath.Join(casksRoot, name), EnvPrefix: cask.EnvPrefix,
			Consumes: append([]string{}, cask.Consumes...), Changes: cask.Changes,
			UseHostLAN: cask.UseHostLAN, Hook: cask.Hook,
			RuntimeType: cask.RuntimeType, ComposeFile: cask.ComposeFile,
		}
		deps[name] = append([]string{}, cask.Dependencies...)
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		return nil, "", nil, err
	}
	a := &app{
		base: base, compose: cli, reg: reg, deps: deps,
		order: append([]string{}, manifest.ModuleOrder...),
		env:   map[string]string{}, envOwner: map[string]string{},
		secrets: secrets, useFrozenHooks: true, artifactRoot: casksRoot,
		resolvedBindings: cloneNestedMap(manifest.Bindings),
	}
	a.adoptReleaseEnv(casksRoot)
	return a, casksRoot, manifest, nil
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

func acquireRuntimeLock(base string) (func(), error) {
	return acquireRuntimeLockMode(base, syscall.LOCK_EX)
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
