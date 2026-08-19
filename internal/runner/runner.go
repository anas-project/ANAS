package runner

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/dns"
)

type app struct {
	root string
	// workspace is the deployment root; base is its .anas state directory.
	workspace      string
	base           string
	cfgPath        string
	verbose        bool
	yes            bool
	jsonMode       bool
	skipStartGuard bool
	useFrozenHooks bool
	// registryOnlyResolution is used by read-only schema boundaries that already
	// received a validated registry. They need the exact dependency/capability/
	// contract/Dynamic-DNS graph, but must not make resolution depend on a second
	// physical module.yml existence check. Production deployment paths leave it
	// false and retain the installed-artifact check.
	registryOnlyResolution bool
	// allowUnresolvedInputBindings lets an import/config-plan schema boundary
	// inspect every binding the generic resolver can determine without turning
	// an otherwise importable, intentionally incomplete configuration into a
	// full deployment-resolution check. Deterministic dependencies/providers
	// are still included; an ambiguous or not-yet-configured selector is skipped.
	allowUnresolvedInputBindings bool
	compose                      compose.CLI
	reg                          map[string]Module
	contracts                    map[string]Contract
	cfg                          *config.File
	env                          map[string]string
	envOwner                     map[string]string
	// callerInputEnv is captured before workspace/runner-derived values are
	// published. input_required is a caller contract, so those derived values
	// must never be able to satisfy it during deployment materialization.
	callerInputEnv map[string]string
	deps           map[string][]string
	order          []string
	secrets        *secretStore
	localAdmins    *localAdminState
	lock           *moduleLock
	hookBins       map[string]string
	sensitiveKeys  map[string]bool
	// narrowFileScope restricts a rendered .env to what its module declares --
	// global values, its own prefix, and manifest `config.consumes` -- instead
	// of everything its dependency closure happens to own. It is a field rather
	// than an outright replacement of the old rule so that one rendering can be
	// diffed against the other, which is how the missing declarations are found
	// rather than guessed.
	narrowFileScope bool
	// runnerSensitive holds keys the runner itself derived from user secrets,
	// which nothing else would recognise as sensitive. See markSensitive.
	runnerSensitive  map[string]bool
	resolvedBindings map[string]map[string]string
	resourceRequests []ResourceRequest
	// iamProvider is the single IAM module serving this deployment, and
	// iamBindings maps each consumer to its resolved protocol. Both are empty
	// until a consumer is actually reached during ordering, so an unused
	// iam.provider never starts an IAM.
	iamProvider string
	iamBindings map[string]string
	// capabilityProviders records the module bound for each capability this
	// deployment actually resolved, keyed by capability name.
	capabilityProviders map[string]string
	// dynamicDNSProvider is the module holding the records this deployment
	// declares. Other DDNS modules may still run alongside it, self-managed.
	dynamicDNSProvider string
	// artifactRoot is the final immutable deployment module directory. Hooks use
	// this path while a deployment is rendered in staging so values they derive
	// never contain the temporary staging path.
	artifactRoot string
	// dnsReg is the DNS platform registry, loaded on first use.
	dnsReg *dns.Registry
}

// Main is the single place the contract's output rules are applied. Every
// command is dispatched through it, so a failure anywhere ends up as one JSON
// document on stdout under --json and as an exit code from the contract's
// table, without each command having to remember to do it.
func Main(args []string) error {
	if len(args) == 0 {
		args = []string{"help"}
	}
	command, rest := args[0], args[1:]
	jsonMode := wantsJSON(rest)
	err := dispatch(command, rest, jsonMode)
	// A command that already emitted its own failure document is left alone.
	// Emitting a second one here would put two JSON values on stdout, which is
	// exactly what the "exactly one document" rule exists to prevent.
	if err != nil && jsonMode && !Reported(err) {
		return emitJSONError(err)
	}
	return err
}

func dispatch(command string, args []string, jsonMode bool) error {
	switch command {
	case "init":
		return runInit(args, jsonMode)
	case "plan":
		return runPlan(args, jsonMode)
	case "render", "build":
		return runPrepare(command, args, jsonMode)
	case "apply":
		return runApply(args, jsonMode)
	case "start", "restart", "stop":
		return runActive(command, args, jsonMode)
	case "rollback":
		return runDeploymentRollback(args, jsonMode)
	case "snapshot":
		return runSnapshot(args)
	case "backup":
		return runBackup(args)
	case "status":
		return runStatus(args, jsonMode)
	case "deployments":
		return runDeployments(args, jsonMode)
	case "lock":
		return runLock(args, jsonMode)
	case "config":
		return runConfig(args, jsonMode)
	case "admin":
		return runAdmin(args, jsonMode)
	case "module":
		return runModule(args, jsonMode)
	case "version":
		return runVersion(args, jsonMode)
	case "help", "-h", "--help":
		return runHelp(jsonMode)
	default:
		// A mistyped command is a usage error, not an execution failure. It was
		// exit 1, which told a caller nothing it could act on.
		return usageErrorf("unknown command %q", command)
	}
}

// commandNames is the enumeration `help --json` reports. Help text itself is
// prose and deliberately not a contract, so the machine-readable form lists
// what can be invoked rather than trying to render the same paragraphs.
var commandNames = []string{
	"init", "plan", "lock", "render", "build", "apply", "start", "restart",
	"stop", "rollback", "status", "deployments", "snapshot", "backup", "config", "admin", "module",
	"version",
}

func runVersion(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	registerJSONFlag(fs)
	if err := fs.Parse(args); err != nil {
		return usageErrorf("version: %v", err)
	}
	if fs.NArg() != 0 {
		return usageErrorf("usage: anas version [--json]")
	}
	result, err := application.NewService("").Version(context.Background())
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"version": result.Version,
			"commit":  result.Commit,
			"date":    result.Date,
		})
	}
	fmt.Printf("anas %s (commit %s, built %s)\n", result.Version, result.Commit, result.Date)
	return nil
}

func runHelp(jsonMode bool) error {
	if jsonMode {
		return emitOK(map[string]any{"commands": commandNames, "module_abi": currentModuleABI})
	}
	usage()
	return nil
}

func usage() {
	fmt.Printf(`anas - NAS service launcher

Usage:
  anas init [PATH] [-c CONFIG] [--module-root modules] [--shell-init write|remove] [-y]
  anas plan    [-w WORKSPACE] [--module-root modules]
  anas lock    [-w WORKSPACE] [-c config.yml]
  anas render  [-w WORKSPACE] [-c config.yml]
  anas build   [-w WORKSPACE] [-c config.yml]
  anas apply   [-w WORKSPACE] [-c config.yml [--build] | --deployment ID]
  anas start   [MODULE...] [-w WORKSPACE]
  anas restart [MODULE...] [-w WORKSPACE]
  anas stop    [MODULE...] [-w WORKSPACE]
  anas rollback [DEPLOYMENT_ID] -w WORKSPACE
  anas status [-w WORKSPACE]
  anas deployments list|inspect [ID] [-w WORKSPACE]
  anas snapshot list|show|create|pin|unpin|delete|prune|verify|path [-w WORKSPACE]
  anas snapshot restore ID -w WORKSPACE [--dry-run] [-y]
  anas backup capabilities [--to DEST] [-w WORKSPACE]
  anas backup plan|create  --to DEST [--mode MODE] [--snapshot ID] [--no-stop]
  anas backup list|verify  --to DEST [--backup-id ID]
  anas backup restore --from SRC -w WORKSPACE [--backup-id ID] [--dry-run] [-y]
  anas config list    [global|MODULE] [-w WORKSPACE]
  anas config import  SOURCE [-w WORKSPACE]
  anas config migrate [-w WORKSPACE]
  anas config set     [-w WORKSPACE] <module.parameter> <value>
  anas config explain <module.parameter>
  anas config plan    [-w WORKSPACE]
  anas config secret  list | get <KEY>   [-w WORKSPACE]
  anas admin local list | credential | rotate MODULE [ACCOUNT] [-w WORKSPACE]
  anas module list [--source NAME] [-w WORKSPACE]
  anas module versions NAME [--source NAME] [-w WORKSPACE]
  anas module install NAME@VERSION-rN [--source NAME] [--digest sha256:...]
  anas module sync [-w WORKSPACE]
  anas module update [MODULE...] [-w WORKSPACE]
  anas version [--json]

Workspace:
  A workspace holds the config, data, snapshots and runtime state of one
  deployment, so backing up that single directory backs up everything.

    <workspace>/config.yml   CLI-managed normalized desired state (do not edit)
    <workspace>/data/        application state; replaced by a restore
    <workspace>/userdata/    files people store; never touched by a rollback
    <workspace>/snapshots/   point-in-time copies
    <workspace>/.anas/       runtime state

  It is resolved from -w, then $ANAS_WORKSPACE, then the current directory
  when that directory already contains .anas/. Commands never create one
  implicitly; run "anas init" for that. "rollback" and "snapshot restore"
  accept only -w.

Snapshot versus backup:
  A snapshot is local, instant, and exists so an upgrade can be undone. A
  backup is the same thing sent somewhere else, so it survives the disk.
  "anas backup capabilities" reports which transfer modes this host and
  destination can actually manage, and why the others cannot.

Rollback versus restore:
  "rollback" switches the artifact and never touches data — the right answer
  when a config change broke the service and the data is fine. Rewinding data
  is only ever "snapshot restore", which puts config, lock, secrets, state,
  artifact and data back to one point in time.

Module ABI:
  %s

Machine-readable output:
  Every command above accepts --json: one JSON document on stdout, progress
  and warnings on stderr, and an exit code from the table in
  docs/reference/contracts/index.md. Without --json the output is prose and is not a
  contract; do not parse it.

The Go CLI reads only the structured YAML format documented in README.md.
`, currentModuleABI)
}

func run(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	base := fs.String("b", "", "runtime base path")
	fs.StringVar(base, "base", "", "runtime base path")
	buildBeforeStart := fs.Bool("build", false, "build before start")
	verbose := fs.Bool("verbose", false, "debug logging")
	yes := fs.Bool("y", false, "accept defaults")
	rootFlag := fs.String("root", "", "project root or module bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := locateModuleRoot(*rootFlag)
	if err != nil {
		return err
	}
	if *base == "" {
		home, _ := os.UserHomeDir()
		*base = filepath.Join(home, ".anas")
	}
	reg, err := loadRegistryDir(root)
	if err != nil {
		return err
	}
	a := &app{root: root, base: *base, cfgPath: *cfgPath, verbose: *verbose, yes: *yes, reg: reg, resolvedBindings: map[string]map[string]string{}}
	if action != "plan" && action != "render" {
		cli, err := compose.Detect()
		if err != nil {
			return err
		}
		a.compose = cli
	}

	actions := []string{action}
	if action == "start" && *buildBeforeStart {
		actions = []string{"build", "start"}
	}
	return a.execute(actions)
}

// runRollback swaps the release with release.previous, restores the matching
// module lock snapshot, and starts the restored artifact without re-rendering.
func runRollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	base := fs.String("b", "", "runtime base path")
	fs.StringVar(base, "base", "", "runtime base path")
	verbose := fs.Bool("verbose", false, "debug logging")
	rootFlag := fs.String("root", "", "project root or module bundle directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	root, err := locateModuleRoot(*rootFlag)
	if err != nil {
		return err
	}
	if *base == "" {
		home, _ := os.UserHomeDir()
		*base = filepath.Join(home, ".anas")
	}
	reg, err := loadRegistryDir(root)
	if err != nil {
		return err
	}
	release := filepath.Join(*base, "release")
	previous := release + ".previous"
	if !exists(filepath.Join(previous, "config.yml")) {
		return fmt.Errorf("no previous release to roll back to at %s", previous)
	}
	cli, err := compose.Detect()
	if err != nil {
		return err
	}
	newApp := func() *app {
		return &app{root: root, base: *base, verbose: *verbose, reg: reg, compose: cli, resolvedBindings: map[string]map[string]string{}}
	}
	// Stop the current release while its rendered directories still exist.
	if exists(filepath.Join(release, "config.yml")) {
		if err := newApp().execute([]string{"stop"}); err != nil {
			return fmt.Errorf("stop current release: %w", err)
		}
	}
	// Swap the directories; the demoted release becomes the new .previous so
	// a mistaken rollback can be rolled forward again.
	swap := release + ".rollback-tmp"
	if err := os.RemoveAll(swap); err != nil {
		return err
	}
	hadRelease := exists(release)
	if hadRelease {
		if err := os.Rename(release, swap); err != nil {
			return err
		}
	}
	if err := os.Rename(previous, release); err != nil {
		if hadRelease {
			_ = os.Rename(swap, release)
		}
		return err
	}
	if hadRelease {
		if err := os.Rename(swap, previous); err != nil {
			return err
		}
	}
	if snapshot := filepath.Join(release, ".module.lock.snapshot"); exists(snapshot) {
		if err := copyFile(snapshot, moduleLockPath(*base)); err != nil {
			return err
		}
	}
	startApp := newApp()
	startApp.skipStartGuard = true
	if err := startApp.execute([]string{"start"}); err != nil {
		return err
	}
	return saveAppliedConfig(*base, filepath.Join(release, "config.yml"))
}

func (a *app) execute(actions []string) error {
	release := filepath.Join(a.base, "release")
	tmp := filepath.Join(a.base, "tmp")
	// A config file means a new render pipeline through tmp with an atomic
	// promotion. Without one, start/restart run the existing release as an
	// immutable artifact: no recalculation, no re-render.
	renders := contains(actions, "build") || contains(actions, "render") || (contains(actions, "start") && a.cfgPath != "")
	work := release
	if renders {
		work = tmp
	}
	cfgPath := a.cfgPath
	if cfgPath == "" {
		if contains(actions, "build") {
			cfgPath = "config.yml"
		} else {
			if !exists(filepath.Join(release, "config.yml")) && !contains(actions, "plan") {
				return fmt.Errorf("no rendered release at %s; run `anas start -c config.yml` first", release)
			}
			cfgPath = filepath.Join(release, "config.yml")
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	if err := validateConfigRuntimeKeyCollisions(cfgPath, a.reg); err != nil {
		return err
	}
	lock, err := loadModuleLock(a.base)
	if err != nil {
		return err
	}
	a.lock = lock
	a.cfg = cfg
	a.env, a.envOwner = configBaseEnvWithRegistry(cfg, a.reg)
	if err := a.loadImportedSecrets(); err != nil {
		return err
	}
	a.order, err = a.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return err
	}
	if renders || contains(actions, "plan") {
		if err := a.validateVersions(lock); err != nil {
			return err
		}
	}
	a.applyModuleDefaults()
	// Ahead of the plan output, so a platform typo or a half-configured
	// credential is reported by `plan` rather than by a container in a
	// restart loop.
	if err := a.materializeDNSCredentials(); err != nil {
		return err
	}
	a.reportDynamicDNSOverlaps()
	if contains(actions, "plan") {
		fmt.Println(strings.Join(a.order, "\n"))
		fmt.Print(a.iamPlanSummary())
		fmt.Print(a.dnsPlanSummary())
		fmt.Print(a.dynamicDNSPlanSummary())
		fmt.Print(a.moduleLifecyclePlanSummary())
		return nil
	}
	if contains(actions, "stop") {
		a.adoptReleaseEnv(release)
		return a.stopRelease(release, false)
	}
	if contains(actions, "start") && !a.skipStartGuard {
		if err := validateOrdinaryStartChanges(a.base, cfgPath, a.reg); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(a.base, 0700); err != nil {
		return err
	}
	if err := os.Chmod(a.base, 0700); err != nil {
		return err
	}
	secrets, err := loadSecretStore(a.base)
	if err != nil {
		return err
	}
	a.secrets = secrets
	a.sensitiveKeys = nil
	if renders {
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
		// Ahead of calculate so every hook sees a settled host environment,
		// and only on a render: an artifact start reuses the values the
		// release was built with rather than re-probing the machine.
		if err := a.applyHostNetwork(); err != nil {
			return err
		}
		if err := a.calculate(); err != nil {
			return err
		}
		if err := a.secrets.Save(); err != nil {
			return err
		}
		if err := a.renderAll(work); err != nil {
			return err
		}
	} else {
		// Artifact start/restart: the rendered per-module environments and
		// frozen hook binaries are the contract. Refuse to guess when a
		// release predates the current format instead of starting with
		// incomplete values.
		a.useFrozenHooks = true
		a.adoptReleaseEnv(release)
		for _, name := range a.order {
			envPath := filepath.Join(release, name, ".env")
			if !exists(envPath) {
				return fmt.Errorf("release module %s has no rendered .env; re-render with `anas start -c <config>`", name)
			}
			if _, err := parseEnvFile(envPath); err != nil {
				return fmt.Errorf("release module %s was rendered by an older anas (%v); re-render with `anas start -c <config>`", name, err)
			}
		}
	}
	if contains(actions, "render") {
		if err := copyFile(cfgPath, filepath.Join(work, "config.yml")); err != nil {
			return err
		}
		return promoteRelease(tmp, release)
	}
	if contains(actions, "build") {
		if err := a.each(work, func(run moduleRun) error {
			if run.mod.RuntimeType != "compose" {
				return nil
			}
			args := append([]string{"build"}, run.services...)
			return a.runCompose(run.dir, run.mod.Name, run.mod.ComposeFile, run.env, args...)
		}); err != nil {
			return err
		}
	}
	if contains(actions, "restart") {
		if err := a.stopRelease(release, false); err != nil {
			return err
		}
	}
	if contains(actions, "start") || contains(actions, "restart") {
		if contains(actions, "start") && renders {
			if err := a.stopRemoved(release); err != nil {
				return err
			}
		}
		if err := a.ensureHostLAN(); err != nil {
			return err
		}
		if err := a.each(work, func(run moduleRun) error {
			if run.mod.RuntimeType != "compose" {
				return nil
			}
			args := append([]string{"up", "-d", "--remove-orphans"}, run.services...)
			return a.runCompose(run.dir, run.mod.Name, run.mod.ComposeFile, run.env, args...)
		}); err != nil {
			return err
		}
	}
	if renders {
		if err := copyFile(cfgPath, filepath.Join(work, "config.yml")); err != nil {
			return err
		}
		if err := promoteRelease(tmp, release); err != nil {
			return err
		}
	}
	// after_start hooks run against the promoted release so paths derived
	// from the hook workdir stay valid for later artifact starts.
	if contains(actions, "start") || contains(actions, "restart") {
		if err := a.runAfterStart(release); err != nil {
			return err
		}
	}
	if renders {
		if contains(actions, "build") || contains(actions, "start") {
			if err := snapshotModuleLock(a.base, release+".previous"); err != nil {
				return err
			}
			if err := a.updateModuleLock(a.lock, contains(actions, "start")); err != nil {
				return err
			}
			if err := a.lock.Save(a.base); err != nil {
				return err
			}
		}
		if contains(actions, "start") {
			if err := saveAppliedConfig(a.base, cfgPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *app) runAfterStart(release string) error {
	return a.runAfterStartOf(release, a.order)
}

// runAfterStartOf runs hooks only for modules started by this operation. A
// partial lifecycle command must not rerun hooks belonging to untouched modules.
func (a *app) runAfterStartOf(release string, names []string) error {
	for _, name := range names {
		mod := a.reg[name]
		if mod.RuntimeType != "compose" {
			continue
		}
		dir := filepath.Join(release, name)
		if a.useFrozenHooks && mod.SourceDir != "" {
			dir = mod.SourceDir
		}
		resp, err := a.runHook(mod, "after_start", dir, a.localAdminHookEnv(name, a.moduleEnv(dir)))
		if err != nil {
			return err
		}
		if err := a.applyLocalAdministrators(mod, dir, a.moduleEnv(dir)); err != nil {
			return err
		}
		if err := runDockerCopies(resp.DockerCopies); err != nil {
			return err
		}
	}
	return nil
}

// releaseDirFor returns the absolute path a module occupies once promoted. Hook
// requests always carry this stable path, so any value a hook derives from
// its workdir stays valid after promotion and across artifact starts.
func (a *app) releaseDirFor(name string) string {
	root := a.artifactRoot
	if root == "" {
		root = filepath.Join(a.base, "release")
	}
	path := filepath.Join(root, name)
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// adoptReleaseEnv replaces the config-derived base env with the deployment-wide
// environment the release was rendered with, so artifact start/stop/restart use
// the values it was actually built with.
func (a *app) adoptReleaseEnv(release string) {
	env, err := parseEnvFile(filepath.Join(release, globalEnvFile))
	if err != nil {
		return
	}
	env = a.relocateDeploymentEnv(env)
	for k, v := range env {
		a.env[k] = v
	}
}

func (a *app) stopRelease(release string, jsonMode bool) error {
	if !exists(release) {
		return nil
	}
	modules := a.releaseModules(release)
	total := int64(len(modules))
	var stopErrors []error
	for i := len(modules) - 1; i >= 0; i-- {
		name := modules[i]
		emitProgress(jsonMode, "stop-containers", int64(len(modules)-i), total, "modules")
		dir := filepath.Join(release, name)
		if err := a.runCompose(dir, name, a.releaseComposeFile(name), a.moduleEnv(dir), "down"); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop %s: %w", name, err))
		}
	}
	if a.hostLANRequired() {
		if err := removeMacvlan(a.env); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

// releaseModules lists the compose modules physically present in a rendered
// release. Modules selected by the current config keep their dependency order;
// modules that are no longer selected are appended, so iterating the reversed
// slice stops removed modules first.
func (a *app) releaseModules(release string) []string {
	entries, err := os.ReadDir(release)
	if err != nil {
		return nil
	}
	present := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if mod, ok := a.reg[name]; ok && mod.RuntimeType != "compose" {
			continue
		}
		if exists(filepath.Join(release, name, a.releaseComposeFile(name))) {
			present[name] = true
		}
	}
	ordered := []string{}
	for _, name := range a.order {
		if present[name] {
			ordered = append(ordered, name)
			delete(present, name)
		}
	}
	extras := make([]string, 0, len(present))
	for name := range present {
		extras = append(extras, name)
	}
	sort.Strings(extras)
	return append(ordered, extras...)
}

func (a *app) releaseComposeFile(name string) string {
	if mod, ok := a.reg[name]; ok && mod.ComposeFile != "" {
		return mod.ComposeFile
	}
	return "docker-compose.yml"
}

// stopRemoved downs release modules that the new configuration no longer
// selects, so a config change cannot leave orphaned compose projects behind.
func (a *app) stopRemoved(release string) error {
	var stopErrors []error
	stoppedAny := false
	for _, name := range a.releaseModules(release) {
		if contains(a.order, name) {
			continue
		}
		dir := filepath.Join(release, name)
		if err := a.runCompose(dir, name, a.releaseComposeFile(name), a.moduleEnv(dir), "down"); err != nil {
			stopErrors = append(stopErrors, fmt.Errorf("stop removed module %s: %w", name, err))
			continue
		}
		stoppedAny = true
	}
	if stoppedAny && len(stopErrors) == 0 && !a.hostLANRequired() {
		if err := removeMacvlan(a.env); err != nil {
			stopErrors = append(stopErrors, err)
		}
	}
	return errors.Join(stopErrors...)
}

func (a *app) hostLANRequired() bool {
	for _, name := range a.order {
		if a.reg[name].UseHostLAN == "required" {
			return true
		}
	}
	return false
}

func (a *app) resolveOrder(mods []string) ([]string, error) {
	if err := validateConfiguredParameterDeclarations(a.cfg, a.reg); err != nil {
		return nil, err
	}
	if err := normalizeConfiguredParameterEnvWithSensitive(a.env, a.reg, a.sensitiveEnvKeySet()); err != nil {
		return nil, err
	}
	if err := a.checkSingleIAM(); err != nil {
		return nil, err
	}
	// Dynamic DNS belongs to the deployment rather than to any module, so no
	// dependency edge would ever pull its implementation in. Resolving it here
	// and adding it as a root is what makes `dynamic_dns.provider` sufficient
	// on its own, without also listing the module in `modules`.
	dynamicDNS, err := a.resolveDynamicDNS()
	if err != nil {
		if !canDeferUnresolvedBinding(a.allowUnresolvedInputBindings, err) {
			return nil, err
		}
		dynamicDNS = ""
	}
	if dynamicDNS != "" && !contains(mods, dynamicDNS) {
		mods = append(append([]string{}, mods...), dynamicDNS)
	}
	seen := map[string]bool{}
	temp := map[string]bool{}
	resolvedDeps := map[string][]string{}
	var out []string
	var visit func(string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		if temp[name] {
			return fmt.Errorf("dependency cycle at %s", name)
		}
		mod, ok := a.reg[name]
		if !ok {
			return fmt.Errorf("unknown module %q", name)
		}
		if !a.moduleEnabled(name) {
			return fmt.Errorf("module %q is disabled but required by an enabled module", name)
		}
		if !a.registryOnlyResolution {
			if err := a.requireModuleManifest(name); err != nil {
				return err
			}
		}
		temp[name] = true
		deps := []string{}
		for _, dep := range mod.Requires {
			if !dep.Optional {
				deps = append(deps, dep.Name)
			}
		}
		for _, dep := range mod.RequiresOne {
			provider, err := a.resolveAlternativeDependency(name, mod, dep)
			if err != nil {
				if canDeferUnresolvedBinding(a.allowUnresolvedInputBindings, err) {
					continue
				}
				return err
			}
			deps = append(deps, provider)
		}
		for _, dep := range mod.RequiresContracts {
			provider, err := a.resolveContractDependency(name, mod, dep)
			if err != nil {
				if canDeferUnresolvedBinding(a.allowUnresolvedInputBindings, err) {
					continue
				}
				return err
			}
			deps = append(deps, provider)
		}
		for _, dep := range mod.RequiresCapabilities {
			provider, err := a.resolveCapabilityDependency(name, mod, dep)
			if err != nil {
				if canDeferUnresolvedBinding(a.allowUnresolvedInputBindings, err) {
					continue
				}
				return err
			}
			deps = append(deps, provider)
		}
		if svc, ok := a.cfg.Modules.Values[name]; ok {
			deps = append(deps, svc.DependsOn...)
		}
		deps = uniqueStrings(deps)
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		resolvedDeps[name] = deps
		temp[name] = false
		seen[name] = true
		out = append(out, name)
		return nil
	}
	for _, mod := range mods {
		if !a.moduleEnabled(mod) {
			continue
		}
		if err := visit(mod); err != nil {
			return nil, err
		}
	}
	a.deps = resolvedDeps
	a.publishIAMEnv(out)
	a.applyDynamicDNSBinding(dynamicDNS)
	return stableModuleOrder(out, resolvedDeps, a.reg)
}

func stableModuleOrder(initial []string, dependencies map[string][]string, reg map[string]Module) ([]string, error) {
	selected := map[string]bool{}
	position := map[string]int{}
	indegree := map[string]int{}
	next := map[string][]string{}
	for i, name := range initial {
		selected[name] = true
		position[name] = i
		indegree[name] = 0
	}
	addEdge := func(before, after string) {
		if !selected[before] || !selected[after] || contains(next[before], after) {
			return
		}
		next[before] = append(next[before], after)
		indegree[after]++
	}
	for name, deps := range dependencies {
		for _, dep := range deps {
			addEdge(dep, name)
		}
	}
	for _, name := range initial {
		for _, after := range reg[name].RunAfter {
			addEdge(after, name)
		}
	}
	ready := []string{}
	for _, name := range initial {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	ordered := make([]string, 0, len(initial))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		ordered = append(ordered, name)
		for _, dependent := range next[name] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				ready = append(ready, dependent)
				sort.SliceStable(ready, func(i, j int) bool { return position[ready[i]] < position[ready[j]] })
			}
		}
	}
	if len(ordered) != len(initial) {
		blocked := []string{}
		for _, name := range initial {
			if indegree[name] > 0 {
				blocked = append(blocked, name)
			}
		}
		return nil, fmt.Errorf("dependency ordering cycle among: %s", strings.Join(blocked, ", "))
	}
	return ordered, nil
}

func uniqueStrings(in []string) []string {
	out := make([]string, 0, len(in))
	for _, item := range in {
		if item != "" && !contains(out, item) {
			out = append(out, item)
		}
	}
	return out
}

func (a *app) resolveAlternativeDependency(moduleName string, mod Module, dep AlternativeDependency) (string, error) {
	key := paramEnvKey(moduleName, mod.EnvPrefix, dep.SelectedBy)
	if err := a.rejectSourceSensitiveSelector(key, moduleName+"."+dep.SelectedBy); err != nil {
		return "", err
	}
	requested := strings.ToLower(strings.TrimSpace(a.env[key]))
	provider := requested
	if provider == "" || provider == "auto" {
		provider = dep.Default
		if a.lock != nil && contains(dep.Providers, a.lock.Bindings[moduleName][dep.Capability]) {
			provider = a.lock.Bindings[moduleName][dep.Capability]
		} else if configured := a.configuredProviders(dep.Providers); len(configured) == 1 {
			provider = configured[0]
		}
	}
	if !contains(dep.Providers, provider) {
		return "", fmt.Errorf("%s.%s must be auto or one of %s, got %q", moduleName, dep.SelectedBy, strings.Join(dep.Providers, ", "), a.resolvedValueForError(key, requested))
	}
	if _, ok := a.reg[provider]; !ok {
		return "", fmt.Errorf("%s requires unknown %s provider %q", moduleName, dep.Capability, a.resolvedValueForError(key, provider))
	}
	if a.cfg != nil && !a.moduleEnabled(provider) {
		return "", fmt.Errorf("%s requires disabled %s provider %q", moduleName, dep.Capability, a.resolvedValueForError(key, provider))
	}
	a.env[key] = provider
	if a.resolvedBindings == nil {
		a.resolvedBindings = map[string]map[string]string{}
	}
	if a.resolvedBindings[moduleName] == nil {
		a.resolvedBindings[moduleName] = map[string]string{}
	}
	a.resolvedBindings[moduleName][dep.Capability] = provider
	return provider, nil
}

func (a *app) configuredProviders(providers []string) []string {
	out := []string{}
	if a.cfg == nil {
		return out
	}
	for _, name := range a.cfg.Modules.Order {
		if contains(providers, name) && a.moduleEnabled(name) && !contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

func (a *app) requireModuleManifest(name string) error {
	mod, ok := a.reg[name]
	if !ok {
		return fmt.Errorf("unknown module %q", name)
	}
	path := filepath.Join(mod.SourceDir, "module.yml")
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("module %q is missing module.yml", name)
		}
		return err
	}
	if _, err := os.Stat(filepath.Join(mod.SourceDir, "runner.rb")); err == nil {
		return fmt.Errorf("module %q still contains unsupported runner.rb", name)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (a *app) applyModuleDefaults() {
	// The deployment's own parameters come first and are globally owned, so a
	// module default can never shadow one and every module can read them.
	for k, v := range globalConfig.defaultValues() {
		if a.env[k] == "" {
			a.env[k] = v
		}
		a.setEnvOwner(k, globalScope)
	}
	for _, name := range a.order {
		for k, v := range a.reg[name].Defaults {
			if a.env[k] == "" {
				a.env[k] = v
			}
			a.setEnvOwner(k, name)
		}
	}
	a.env["ALL_MODS_NAME"] = strings.Join(a.order, ",")
	a.setEnvOwner("ALL_MODS_NAME", runnerScope)
	hostReq := []string{}
	hostOpt := []string{}
	for _, name := range a.order {
		mod := a.reg[name]
		if mod.UseHostLAN == "required" {
			hostReq = append(hostReq, name)
		}
		if mod.UseHostLAN == "optional" {
			hostOpt = append(hostOpt, name)
		}
	}
	a.env["USE_HOST_LAN_REQUIRED_MODS_NAME"] = strings.Join(hostReq, ",")
	a.env["USE_HOST_LAN_OPTIONAL_MODS_NAME"] = strings.Join(hostOpt, ",")
	a.setEnvOwner("USE_HOST_LAN_REQUIRED_MODS_NAME", runnerScope)
	a.setEnvOwner("USE_HOST_LAN_OPTIONAL_MODS_NAME", runnerScope)
}

func (a *app) calculate() error {
	// Defaults plus host/runtime resolvers are materialized before calculate.
	// Normalize that data through the same schema boundary as caller input so a
	// source cannot bypass type canonicalization or constraints.
	if err := normalizeConfiguredParameterEnvWithSensitive(a.env, a.reg, a.sensitiveEnvKeySet()); err != nil {
		return fmt.Errorf("resolved deployment config: %w", err)
	}
	if err := requireKeys(a.env, globalConfig.finalRequirements()); err != nil {
		return fmt.Errorf("deployment config: %w", err)
	}
	for _, name := range a.order {
		mod := a.reg[name]
		if err := a.publishModuleResources(name); err != nil {
			return err
		}
		// Legacy config.required is deliberately checked before calculate, after
		// defaults and generic resolvers have run. This is distinct from both the
		// earlier caller-input check and the post-Hook must_resolve invariant.
		if err := requireKeys(a.env, mod.preHookRequirements()); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		resp, err := a.runHook(mod, "calculate", a.releaseDirFor(name), a.localAdminHookEnv(name, a.env))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		secretPatch, err := canonicalizeHookSecretPatch(resp.Secrets)
		if err != nil {
			return fmt.Errorf("%s calculate secrets: %w", name, err)
		}
		if err := a.validateCalculateSecretPatch(mod, secretPatch); err != nil {
			return fmt.Errorf("%s calculate secrets: %w", name, err)
		}
		if err := a.applyCalculatePatch(mod, resp.Env); err != nil {
			return err
		}
		// Secret provenance is established by the Hook response, not by the
		// current manifest's sensitive bit. Mark the canonical key and any env
		// alias carrying the same value before schema errors can format it.
		sensitive := a.hookPatchSensitiveEnv(a.env, secretPatch)
		// Hook output is another parameter source, not an escape hatch around the
		// common schema. Re-run normalization before enforcing the final invariant.
		if err := normalizeConfiguredParameterEnvWithSensitive(a.env, a.reg, sensitive); err != nil {
			return fmt.Errorf("%s calculate patch: %w", name, err)
		}
		if err := requireKeys(a.env, mod.finalRequirements()); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		a.secrets.mergeCanonicalHookSecrets(name, secretPatch)
		// The committed secret patch becomes a new source for later Hook and
		// render aliases; force the next scope calculation to include it.
		a.sensitiveKeys = nil
		if name == a.iamProvider {
			if err := a.validateIAMEndpoints(); err != nil {
				return err
			}
		}
	}
	domains := []string{}
	for _, name := range a.order {
		key := strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_DOMAIN"
		if a.env[key] != "" {
			domains = append(domains, "inner/"+strings.Split(a.env[key], ".")[0]+"/"+name)
		}
	}
	a.env["DOMAINS"] = strings.Join(domains, ",")
	// DOMAINS is an input to Samba's zone reconciler, not a deployment-wide
	// setting. Keep it runner-owned so only an explicit consumer changes when
	// the enabled module set changes. Treating it as global made adding
	// Nextcloud rewrite PostgreSQL's .env and recreate PostgreSQL itself.
	a.setEnvOwner("DOMAINS", runnerScope)
	return nil
}

func (a *app) renderAll(work string) error {
	if err := os.MkdirAll(work, 0755); err != nil {
		return err
	}
	if err := writeEnv(filepath.Join(work, globalEnvFile), a.globalEnv()); err != nil {
		return err
	}
	for _, name := range a.order {
		mod := a.reg[name]
		dir := filepath.Join(work, name)
		if err := copyDir(mod.SourceDir, dir); err != nil {
			return err
		}
		if err := a.freezeHookBinary(mod, dir); err != nil {
			return err
		}
		env := a.scopedEnv(name)
		env["MODULE_NAME"] = name
		env["ANAS_MODULE_RUNTIME_STATE_PATH"] = a.moduleRuntimeStatePath(name)
		resp, err := a.runHook(mod, "render_env", a.releaseDirFor(name), a.localAdminHookEnv(name, env))
		if err != nil {
			return err
		}
		if err := applyHookEnv(env, resp.Env); err != nil {
			return fmt.Errorf("%s render_env patch: %w", name, err)
		}
		sensitive := a.hookPatchSensitiveEnv(env, nil)
		if err := normalizeConfiguredParameterEnvWithSensitive(env, a.reg, sensitive); err != nil {
			return fmt.Errorf("%s render_env patch: %w", name, err)
		}
		if err := applyHookFiles(dir, resp.Files); err != nil {
			return err
		}
		if err := applyHookRuntimeFiles(a.resolveModuleRuntimeStatePath(name, env), resp.RuntimeFiles); err != nil {
			return err
		}
		if len(resp.RuntimeFiles) == 0 {
			// Only modules that actually own mutable runtime files carry this
			// generated bind path into Compose and their render digest.
			delete(env, "ANAS_MODULE_RUNTIME_STATE_PATH")
		}
		fileEnv := env
		if len(resp.InternalEnv) > 0 {
			fileEnv = cloneMap(env)
			for _, key := range resp.InternalEnv {
				delete(fileEnv, key)
			}
		}
		if err := writeEnv(filepath.Join(dir, ".env"), fileEnv); err != nil {
			return err
		}
	}
	return nil
}

// moduleRuntimeStatePath is deliberately relative to the rendered module.
// A deployment artifact can therefore be copied to another workspace without
// retaining an absolute path to the source machine. The deployment id remains
// in the path, so two rollback targets never share mutable state.
func (a *app) moduleRuntimeStatePath(name string) string {
	moduleDir := a.releaseDirFor(name)
	target := filepath.Join(a.base, "runtime-state", "release", name)
	if a.artifactRoot != "" {
		deploymentID := filepath.Base(filepath.Dir(a.artifactRoot))
		target = filepath.Join(a.base, "runtime-state", "deployments", deploymentID, name)
	}
	rel, err := filepath.Rel(moduleDir, target)
	if err != nil {
		return target
	}
	return filepath.ToSlash(rel)
}

func (a *app) resolveModuleRuntimeStatePath(name string, env map[string]string) string {
	path := strings.TrimSpace(env["ANAS_MODULE_RUNTIME_STATE_PATH"])
	if path == "" {
		path = a.moduleRuntimeStatePath(name)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(a.releaseDirFor(name), filepath.FromSlash(path)))
}

// restoreModuleRuntimeState reconstructs runtime-only configuration before a
// container is started. Runtime state is intentionally excluded from sealed
// artifacts and backups; the generated Secret Store remains authoritative.
func (a *app) restoreModuleRuntimeState(mod Module, dir string, env map[string]string) error {
	resp, err := a.runHook(mod, "runtime_restore", dir, a.localAdminHookEnv(mod.Name, env))
	if err != nil {
		return err
	}
	return applyHookRuntimeFiles(a.resolveModuleRuntimeStatePath(mod.Name, env), resp.RuntimeFiles)
}

func (a *app) ensureHostLAN() error {
	if !a.hostLANRequired() {
		return nil
	}
	return ensureMacvlan(a.env, a.compose)
}

type moduleRun struct {
	mod      Module
	dir      string
	env      map[string]string
	services []string
}

func (a *app) each(work string, fn func(moduleRun) error) error {
	return a.eachOf(work, a.order, fn)
}

// eachOf is `each` over a chosen subset, which is what lets start, restart and
// build act on named modules. The names are already in deployment order.
func (a *app) eachOf(work string, names []string, fn func(moduleRun) error) error {
	for _, name := range names {
		mod := a.reg[name]
		dir := filepath.Join(work, name)
		if a.useFrozenHooks && mod.SourceDir != "" {
			dir = mod.SourceDir
		}
		env := a.moduleEnv(dir)
		if mod.RuntimeType != "compose" {
			// Nothing to enumerate services for, but the callback still runs:
			// callers count modules for progress, and a runtime that starts no
			// containers is still part of the deployment.
			if err := fn(moduleRun{mod: mod, dir: dir, env: env}); err != nil {
				return err
			}
			continue
		}
		services, err := a.services(mod, dir, env)
		if err != nil {
			return err
		}
		if err := fn(moduleRun{mod: mod, dir: dir, env: env, services: services}); err != nil {
			return err
		}
	}
	return nil
}

// moduleEnv returns the environment for operating one rendered module. The
// rendered .env is authoritative when it exists; the in-memory environment is
// only a fallback for directories that were never rendered.
func (a *app) moduleEnv(dir string) map[string]string {
	if env, err := parseEnvFile(filepath.Join(dir, ".env")); err == nil {
		return a.relocateDeploymentEnv(env)
	}
	return a.env
}

// relocateDeploymentEnv projects workspace-derived absolute paths onto the
// workspace that currently owns the artifact. Backup and snapshot restore copy
// the sealed artifact verbatim, so its .env still names the source workspace;
// treating those bytes as authoritative without relocating them would mount
// the old workspace's data into a restored deployment.
func (a *app) relocateDeploymentEnv(env map[string]string) map[string]string {
	if a.base == "" {
		return env
	}
	oldData := filepath.Clean(strings.TrimSpace(env["DATA_PATH"]))
	if oldData == "." || filepath.Base(oldData) != workspaceDataDir {
		return env
	}
	oldWorkspace := filepath.Dir(oldData)
	newWorkspace := workspaceOf(a.base)
	if oldWorkspace == newWorkspace {
		return env
	}
	out := cloneMap(env)
	oldPrefix := oldWorkspace + string(os.PathSeparator)
	for key, value := range out {
		clean := filepath.Clean(value)
		switch {
		case clean == oldWorkspace:
			out[key] = newWorkspace
		case strings.HasPrefix(clean, oldPrefix):
			suffix := strings.TrimPrefix(clean, oldPrefix)
			out[key] = filepath.Join(newWorkspace, suffix)
			if strings.HasSuffix(value, string(os.PathSeparator)) {
				out[key] += string(os.PathSeparator)
			}
		}
	}
	return out
}

func (a *app) services(mod Module, dir string, env map[string]string) ([]string, error) {
	out, err := a.outputCompose(dir, mod.Name, mod.ComposeFile, env, "config", "--services")
	if err != nil {
		return nil, err
	}
	services := fieldsLines(out)
	resp, err := a.runHook(mod, "services", dir, env)
	if err != nil {
		return nil, err
	}
	if len(resp.DisableServices) > 0 {
		services = remove(services, resp.DisableServices...)
	}
	for _, provider := range mod.ContractProviders {
		services = remove(services, provider.OperationSvcs...)
	}
	sort.Strings(services)
	return services, nil
}

func (a *app) moduleEnabled(name string) bool {
	service, ok := a.cfg.Modules.Values[name]
	return !ok || service.Enabled == nil || *service.Enabled
}

// promoteRelease atomically replaces the release with the staged render. The
// demoted release is kept as `release.previous` until the next promotion so
// `anas rollback` has an artifact to return to.
func promoteRelease(staging, release string) error {
	backup := release + ".previous"
	if err := os.RemoveAll(backup); err != nil {
		return err
	}
	hadRelease := exists(release)
	if hadRelease {
		if err := os.Rename(release, backup); err != nil {
			return err
		}
	}
	if err := os.Rename(staging, release); err != nil {
		if hadRelease {
			_ = os.Rename(backup, release)
		}
		return err
	}
	return nil
}

// snapshotModuleLock copies the pre-upgrade lock file into the demoted release
// so a rollback restores module versions and capability bindings together with
// the rendered artifact.
func snapshotModuleLock(base, backup string) error {
	lockPath := moduleLockPath(base)
	if !exists(lockPath) || !exists(backup) {
		return nil
	}
	return copyFile(lockPath, filepath.Join(backup, ".module.lock.snapshot"))
}

func copyDir(src, dst string) error {
	if !exists(src) {
		return fmt.Errorf("module directory not found: %s", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileMode(path, target, info.Mode())
	})
}

func copyFile(src, dst string) error {
	return copyFileMode(src, dst, 0644)
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fieldsLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func contains[T comparable](items []T, item T) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func index(items []string, item string) int {
	for i, v := range items {
		if v == item {
			return i
		}
	}
	return -1
}
