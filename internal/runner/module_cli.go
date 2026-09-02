package runner

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/modulesource"
	"github.com/anas-project/ANAS/internal/modulestore"
	"gopkg.in/yaml.v3"
)

var (
	lookupModuleSourceProfile = modulesource.LookupBuiltin
	createModuleStore         = modulestore.New
)

func runModule(args []string, jsonMode bool) error {
	if len(args) == 0 {
		return usageErrorf("usage: anas module list|versions|install|sync|update|commands|invoke ...")
	}
	switch args[0] {
	case "list":
		return runModuleList(args[1:], jsonMode)
	case "versions":
		return runModuleVersions(args[1:], jsonMode)
	case "install":
		return runModuleInstall(args[1:], jsonMode)
	case "sync":
		return runModuleSync(args[1:], jsonMode)
	case "update":
		return runModuleUpdate(args[1:], jsonMode)
	case "commands":
		return runModuleCommands(args[1:], jsonMode)
	case "invoke":
		return runModuleInvoke(args[1:], jsonMode)
	default:
		return usageErrorf("unknown module subcommand %q", args[0])
	}
}

type moduleCommandParamFlags []string

func (values *moduleCommandParamFlags) String() string { return strings.Join(*values, ",") }

func (values *moduleCommandParamFlags) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type moduleCommandCLISink struct{ jsonMode bool }

func (sink moduleCommandCLISink) Progress(event application.ProgressEvent) {
	emitProgress(sink.jsonMode, event.Phase, event.Current, event.Total, event.Unit)
}

func (sink moduleCommandCLISink) Warning(event application.WarningEvent) {
	emitWarning(sink.jsonMode, event.Code, "%s", event.Message)
}

func (moduleCommandCLISink) Log(application.LogEvent) {}

func runModuleInvoke(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module invoke", flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	var parameters moduleCommandParamFlags
	fs.Var(&parameters, "param", "module command parameter NAME=VALUE (repeatable)")
	yes := fs.Bool("y", false, "confirm a destructive command")
	fs.BoolVar(yes, "yes", false, "confirm a destructive command")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module invoke: %v", err)
	}
	if len(positional) != 2 {
		return usageErrorf("usage: anas module invoke MODULE COMMAND [-w WORKSPACE] [--param NAME=VALUE]... [-y] [--json]")
	}
	provided := make(map[string]any, len(parameters))
	for _, assignment := range parameters {
		name, value, ok := strings.Cut(assignment, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return usageErrorf("--param must use NAME=VALUE")
		}
		if _, duplicate := provided[name]; duplicate {
			return usageErrorf("--param %s was supplied more than once", name)
		}
		provided[name] = value
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	service := application.NewService(workspace).WithEventSink(moduleCommandCLISink{jsonMode: jsonMode})
	prepared, err := service.PrepareModuleCommand(context.Background(), application.PrepareModuleCommandRequest{
		Module: positional[0], Command: positional[1], Parameters: provided,
	})
	if err != nil {
		return applicationCLIError(err)
	}
	confirmed := prepared.Command.Risk != "destructive"
	if !confirmed {
		parameterJSON, marshalErr := json.Marshal(prepared.Parameters)
		if marshalErr != nil {
			return failuref("module_command_failed", "encode normalized parameters: %s", marshalErr.Error())
		}
		prompt := fmt.Sprintf(
			"%s\n%s\nTarget: deployment %s, %s %s\nParameters: %s",
			prepared.Command.Title, prepared.Command.Description, prepared.DeploymentID,
			prepared.Module, prepared.Release, parameterJSON,
		)
		if err := confirmDestructive(prompt, *yes); err != nil {
			return err
		}
		confirmed = true
	}
	result, err := service.InvokeModuleCommand(context.Background(), application.InvokeModuleCommandRequest{
		Module: prepared.Module, Command: prepared.Command.ID, Parameters: prepared.Parameters,
		CommandDigest: prepared.Command.Digest, Confirmed: confirmed,
	})
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": workspace, "deployment_id": result.DeploymentID,
			"module": result.Module, "command": result.Command,
			"changed": result.Changed, "result": result.Result,
		})
	}
	body, err := json.MarshalIndent(result.Result, "", "  ")
	if err != nil {
		return failuref("module_command_failed", "encode command result: %s", err.Error())
	}
	fmt.Printf("%s %s: changed=%t\n%s\n", result.Module, result.Command, result.Changed, body)
	return nil
}

func runModuleCommands(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module commands", flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module commands: %v", err)
	}
	if len(positional) > 1 {
		return usageErrorf("usage: anas module commands [MODULE] [-w WORKSPACE] [--json]")
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	request := application.ListModuleCommandsRequest{}
	if len(positional) == 1 {
		request.Module = positional[0]
	}
	result, err := application.NewService(workspace).ListModuleCommands(context.Background(), request)
	if err != nil {
		return applicationCLIError(err)
	}
	if jsonMode {
		return emitOK(map[string]any{
			"active_deployment": result.ActiveDeployment,
			"commands":          result.Commands,
		})
	}
	for _, command := range result.Commands {
		availability := "available"
		if !command.Available {
			availability = "unavailable:" + command.UnavailableReason
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", command.Module, command.Command.ID, command.Command.Mode, command.Command.Risk, availability)
		fmt.Printf("  %s — %s\n", command.Command.Title, command.Command.Description)
	}
	return nil
}

type moduleRemoteFlags struct {
	source    string
	workspace string
	cacheDir  string
}

func registerModuleRemoteFlags(fs *flag.FlagSet, flags *moduleRemoteFlags) {
	fs.StringVar(&flags.source, "source", "", "Module source profile (official, official-cn, or cn)")
	fs.StringVar(&flags.workspace, "w", "", "workspace used to read module_source")
	fs.StringVar(&flags.workspace, "workspace", "", "workspace used to read module_source")
	fs.StringVar(&flags.cacheDir, "cache-dir", "", "Module cache directory")
	registerJSONFlag(fs)
}

func resolveModuleSource(source, workspace string) (modulesource.Profile, string, error) {
	return resolveModuleSourceWithWorkspaceLock(source, workspace, false)
}

// resolveModuleSourceWithWorkspaceLock keeps workspace-backed source
// selection in the same consistency domain as config PUT. Callers that
// already hold the exclusive runtime lock pass workspaceLocked=true to avoid
// recursively acquiring the advisory lock.
func resolveModuleSourceWithWorkspaceLock(source, workspace string, workspaceLocked bool) (modulesource.Profile, string, error) {
	resolvedWorkspace := ""
	if strings.TrimSpace(source) == "" && strings.TrimSpace(workspace) != "" {
		var err error
		resolvedWorkspace, err = resolveWorkspace(workspace)
		if err != nil {
			return modulesource.Profile{}, "", usageErrorf("%s", err.Error())
		}
		if !workspaceLocked {
			unlock, lockErr := acquireWorkspaceConfigReadLock(context.Background(), stateDir(resolvedWorkspace))
			if lockErr != nil {
				return modulesource.Profile{}, "", failuref("lock_failed", "%s", lockErr.Error())
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

func newModuleStore(cacheDir string) (*modulestore.Store, error) {
	store, err := createModuleStore(cacheDir)
	if err != nil {
		return nil, failuref("module_cache_unavailable", "%s", err.Error())
	}
	return store, nil
}

func bootstrapRemoteModuleView(configSource string, jsonMode bool) (modulestore.View, error) {
	body, err := os.ReadFile(configSource)
	if err != nil {
		return modulestore.View{}, err
	}
	var bootstrap struct {
		ModuleSource string `yaml:"module_source"`
		Modules      map[string]struct {
			Version string `yaml:"version"`
		} `yaml:"modules"`
	}
	if err := yaml.Unmarshal(body, &bootstrap); err != nil {
		return modulestore.View{}, err
	}
	profile, ok := lookupModuleSourceProfile(modulesource.InstalledDefaultName(bootstrap.ModuleSource))
	if !ok {
		return modulestore.View{}, fmt.Errorf("module_source must be official, official-cn, or cn")
	}
	store, err := createModuleStore("")
	if err != nil {
		return modulestore.View{}, err
	}
	catalog, err := store.FetchCatalog(context.Background(), profile)
	if err != nil {
		return modulestore.View{}, err
	}
	installations := map[string]modulestore.Installation{}
	for index, module := range catalog.Catalog.Modules {
		release := module.Release
		if selected, exists := bootstrap.Modules[module.Module]; exists && strings.TrimSpace(selected.Version) != "" {
			release = strings.TrimSpace(selected.Version)
			if _, err := modulestore.ParseRelease(release); err != nil {
				return modulestore.View{}, fmt.Errorf("modules.%s.version: %w", module.Module, err)
			}
		}
		emitProgress(jsonMode, "module-bootstrap", int64(index), int64(len(catalog.Catalog.Modules)), "modules")
		installation, err := store.Install(context.Background(), profile, module.Module, release, "")
		if err != nil {
			return modulestore.View{}, err
		}
		installations[module.Module] = installation
	}
	view, err := store.BuildView(installations)
	if err != nil {
		return modulestore.View{}, err
	}
	emitProgress(jsonMode, "module-bootstrap", int64(len(catalog.Catalog.Modules)), int64(len(catalog.Catalog.Modules)), "modules")
	return view, nil
}

func runModuleList(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module list", flag.ContinueOnError)
	var flags moduleRemoteFlags
	registerModuleRemoteFlags(fs, &flags)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module list: %v", err)
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas module list [--source NAME] [-w WORKSPACE] [--json]")
	}
	profile, _, err := resolveModuleSource(flags.source, flags.workspace)
	if err != nil {
		return err
	}
	store, err := newModuleStore(flags.cacheDir)
	if err != nil {
		return err
	}
	result, fetchErr := store.FetchCatalog(context.Background(), profile)
	if fetchErr != nil {
		return failuref("module_source_unavailable", "%s", fetchErr.Error())
	}
	modules := append([]modulestore.CatalogModule(nil), result.Catalog.Modules...)
	sort.Slice(modules, func(i, j int) bool { return modules[i].Module < modules[j].Module })
	if jsonMode {
		return emitOK(map[string]any{
			"source": profile.Name, "catalog_reference": result.Reference,
			"catalog_digest": result.OCIDigest, "source_commit": result.Catalog.SourceCommit,
			"modules": modules,
		})
	}
	for _, module := range modules {
		fmt.Printf("%s\t%s\t%s\t%s\n", module.Module, module.Release, module.Repository, strings.Join(module.Platforms, ","))
	}
	return nil
}

func runModuleVersions(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module versions", flag.ContinueOnError)
	var flags moduleRemoteFlags
	registerModuleRemoteFlags(fs, &flags)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module versions: %v", err)
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas module versions NAME [--source NAME] [-w WORKSPACE] [--json]")
	}
	profile, _, err := resolveModuleSource(flags.source, flags.workspace)
	if err != nil {
		return err
	}
	store, err := newModuleStore(flags.cacheDir)
	if err != nil {
		return err
	}
	versions, catalog, fetchErr := store.Versions(context.Background(), profile, positional[0])
	if fetchErr != nil {
		return failuref("module_versions_unavailable", "%s", fetchErr.Error())
	}
	if jsonMode {
		return emitOK(map[string]any{
			"source": profile.Name, "module": positional[0], "versions": versions,
			"catalog_reference": catalog.Reference, "catalog_digest": catalog.OCIDigest,
		})
	}
	for _, version := range versions {
		marker := ""
		if version.Current {
			marker = "\tcurrent"
		}
		fmt.Printf("%s%s\n", version.Release, marker)
	}
	return nil
}

func runModuleInstall(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module install", flag.ContinueOnError)
	var flags moduleRemoteFlags
	registerModuleRemoteFlags(fs, &flags)
	expectedDigest := fs.String("digest", "", "expected immutable OCI manifest digest")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module install: %v", err)
	}
	if len(positional) != 1 {
		return usageErrorf("usage: anas module install NAME@VERSION-rN [--source NAME] [--digest sha256:...] [--json]")
	}
	name, release, ok := strings.Cut(positional[0], "@")
	if !ok || strings.Contains(release, "@") || name == "" || release == "" {
		return usageErrorf("Module install target must be NAME@VERSION-rN")
	}
	if _, parseErr := modulestore.ParseRelease(release); parseErr != nil {
		return usageErrorf("%s", parseErr.Error())
	}
	profile, _, err := resolveModuleSource(flags.source, flags.workspace)
	if err != nil {
		return err
	}
	store, err := newModuleStore(flags.cacheDir)
	if err != nil {
		return err
	}
	installed, installErr := store.Install(context.Background(), profile, name, release, *expectedDigest)
	if installErr != nil {
		return failuref("module_install_failed", "%s", installErr.Error())
	}
	if jsonMode {
		return emitOK(map[string]any{
			"source": profile.Name, "module": installed.Name, "release": installed.Release,
			"repository": installed.Repository, "reference": installed.Reference,
			"oci_digest": installed.OCIDigest, "layer_digest": installed.LayerDigest,
			"content_digest": installed.ContentDigest, "blob_path": absolutePath(installed.BlobPath),
			"path": absolutePath(installed.Path),
		})
	}
	fmt.Printf("%s@%s\n%s\n", installed.Name, installed.Release, installed.Path)
	return nil
}

func runModuleSync(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module sync", flag.ContinueOnError)
	var flags moduleRemoteFlags
	registerModuleRemoteFlags(fs, &flags)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module sync: %v", err)
	}
	if len(positional) != 0 {
		return usageErrorf("usage: anas module sync [-w WORKSPACE] [--source NAME] [--json]")
	}
	workspace, err := resolveWorkspace(flags.workspace)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	profile, _, err := resolveModuleSourceWithWorkspaceLock(flags.source, workspace, true)
	if err != nil {
		return err
	}
	lockPath := projectLockPath(workspaceConfigPath(workspace))
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return preconditionErrorf("lock_invalid", "%s", err.Error())
	}
	if len(lock.Modules) == 0 {
		return preconditionErrorf("lock_missing", "config lock has no Modules; run `anas module update -w %s`", workspace)
	}
	store, err := newModuleStore(flags.cacheDir)
	if err != nil {
		return err
	}
	installations := map[string]modulestore.Installation{}
	names := sortedLockModuleNames(lock)
	for index, name := range names {
		record := lock.Modules[name]
		if record.OCIDigest == "" || record.ContentDigest == "" || !strings.HasPrefix(record.Source, "oci://") {
			return preconditionErrorf("module_lock_local", "module %s is locked to a local bundle; module sync will not replace it with a Registry package", name)
		}
		emitProgress(jsonMode, "module-sync", int64(index), int64(len(names)), "modules")
		installation, cached, cacheErr := store.Cached(record.OCIDigest)
		if cacheErr != nil {
			return failuref("module_cache_corrupt", "%s", cacheErr.Error())
		}
		if !cached {
			installation, err = store.InstallLocked(context.Background(), profile, record.Source, name,
				formatModuleRelease(record.Version, record.Revision), record.Repository, record.OCIDigest)
			if err != nil {
				return failuref("module_sync_failed", "%s", err.Error())
			}
		}
		if installation.Name != name || installation.Repository != record.Repository ||
			installation.ContentDigest != record.ContentDigest || installation.Metadata.Version != record.Version || installation.Metadata.Revision != record.Revision {
			return preconditionErrorf("module_lock_mismatch", "installed Module %s does not match config lock", name)
		}
		installations[name] = installation
	}
	view, err := store.BuildView(installations)
	if err != nil {
		return failuref("module_view_failed", "%s", err.Error())
	}
	if err := validateRemoteViewAgainstLock(view, lock); err != nil {
		return preconditionErrorf("module_lock_mismatch", "%s", err.Error())
	}
	if err := saveWorkspaceModuleView(workspace, view); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	emitProgress(jsonMode, "module-sync", int64(len(names)), int64(len(names)), "modules")
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": workspace, "source": profile.Name, "module_root": absolutePath(view.ModuleRoot),
			"view_digest": view.Digest, "modules": moduleInstallationDocument(installations),
		})
	}
	fmt.Println(view.ModuleRoot)
	return nil
}

func runModuleUpdate(args []string, jsonMode bool) error {
	fs := flag.NewFlagSet("module update", flag.ContinueOnError)
	var flags moduleRemoteFlags
	registerModuleRemoteFlags(fs, &flags)
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return usageErrorf("module update: %v", err)
	}
	workspace, err := resolveWorkspace(flags.workspace)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	unlock, err := acquireRuntimeLock(stateDir(workspace))
	if err != nil {
		return failuref("lock_failed", "%s", err.Error())
	}
	defer unlock()
	configPath := workspaceConfigPath(workspace)
	if err := validateManagedConfig(workspace, configPath); err != nil {
		return preconditionErrorf("config_not_managed", "%s", err.Error())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return preconditionErrorf("config_invalid", "%s", err.Error())
	}
	profile, _, err := resolveModuleSourceWithWorkspaceLock(flags.source, workspace, true)
	if err != nil {
		return err
	}
	store, err := newModuleStore(flags.cacheDir)
	if err != nil {
		return err
	}
	catalogResult, err := store.FetchCatalog(context.Background(), profile)
	if err != nil {
		return failuref("module_source_unavailable", "%s", err.Error())
	}
	lockPath := projectLockPath(configPath)
	lock, err := loadModuleLockFile(lockPath)
	if err != nil {
		return preconditionErrorf("lock_invalid", "%s", err.Error())
	}
	targets := map[string]bool{}
	if len(positional) == 0 {
		for _, name := range cfg.Modules.Order {
			targets[name] = true
		}
	} else {
		for _, name := range positional {
			if _, exists := cfg.Modules.Values[name]; !exists {
				return usageErrorf("module %q is not selected in config.yml", name)
			}
			targets[name] = true
		}
	}
	installations := map[string]modulestore.Installation{}
	for index, catalogModule := range catalogResult.Catalog.Modules {
		release := catalogModule.Release
		expectedDigest := ""
		if existing, ok := lock.Modules[catalogModule.Module]; ok && existing.OCIDigest != "" && !targets[catalogModule.Module] {
			release = formatModuleRelease(existing.Version, existing.Revision)
			expectedDigest = existing.OCIDigest
		} else if selected, ok := cfg.Modules.Values[catalogModule.Module]; ok && strings.TrimSpace(selected.Version) != "" {
			release = strings.TrimSpace(selected.Version)
			if _, parseErr := modulestore.ParseRelease(release); parseErr != nil {
				return preconditionErrorf("config_invalid", "modules.%s.version: %s", catalogModule.Module, parseErr.Error())
			}
		}
		emitProgress(jsonMode, "module-update", int64(index), int64(len(catalogResult.Catalog.Modules)), "modules")
		if expectedDigest != "" {
			if cached, ok, cacheErr := store.Cached(expectedDigest); cacheErr != nil {
				return failuref("module_cache_corrupt", "%s", cacheErr.Error())
			} else if ok {
				installations[catalogModule.Module] = cached
				continue
			}
			existing := lock.Modules[catalogModule.Module]
			installation, installErr := store.InstallLocked(context.Background(), profile, existing.Source,
				catalogModule.Module, release, existing.Repository, expectedDigest)
			if installErr != nil {
				return failuref("module_update_failed", "%s", installErr.Error())
			}
			installations[catalogModule.Module] = installation
			continue
		}
		installation, installErr := store.Install(context.Background(), profile, catalogModule.Module, release, expectedDigest)
		if installErr != nil {
			return failuref("module_update_failed", "%s", installErr.Error())
		}
		installations[catalogModule.Module] = installation
	}
	// An explicitly targeted update must not make unrelated locked Modules
	// unrestorable merely because current catalog discovery no longer lists them.
	// Keep those modules at their exact immutable lock identities.
	for name, record := range lock.Modules {
		if _, present := installations[name]; present || targets[name] {
			continue
		}
		if record.OCIDigest == "" || record.ContentDigest == "" || !strings.HasPrefix(record.Source, "oci://") {
			continue
		}
		installation, cached, cacheErr := store.Cached(record.OCIDigest)
		if cacheErr != nil {
			return failuref("module_cache_corrupt", "%s", cacheErr.Error())
		}
		if !cached {
			installation, err = store.InstallLocked(context.Background(), profile, record.Source, name,
				formatModuleRelease(record.Version, record.Revision), record.Repository, record.OCIDigest)
			if err != nil {
				return failuref("module_update_failed", "%s", err.Error())
			}
		}
		installations[name] = installation
	}
	for target := range targets {
		if _, ok := installations[target]; !ok {
			return preconditionErrorf("module_not_found", "Module %s is not present in source %s", target, profile.Name)
		}
	}
	provisional, err := store.BuildView(installations)
	if err != nil {
		return failuref("module_view_failed", "%s", err.Error())
	}
	resolvedOrder, updatedLock, err := resolveRemoteModuleLock(workspace, configPath, provisional.ModuleRoot, lock)
	if err != nil {
		return err
	}
	resolvedInstallations := map[string]modulestore.Installation{}
	for _, name := range resolvedOrder {
		resolvedInstallations[name] = installations[name]
	}
	view, err := store.BuildView(resolvedInstallations)
	if err != nil {
		return failuref("module_view_failed", "%s", err.Error())
	}
	for _, name := range resolvedOrder {
		installation := resolvedInstallations[name]
		record := updatedLock.Modules[name]
		record.Source = installation.ImmutableReference
		record.OCIDigest = installation.OCIDigest
		record.ContentDigest = installation.ContentDigest
		record.Repository = installation.Repository
		updatedLock.Modules[name] = record
	}
	if err := commitWorkspaceModuleState(lockPath, workspace, updatedLock, view); err != nil {
		return failuref("write_failed", "%s", err.Error())
	}
	emitProgress(jsonMode, "module-update", int64(len(catalogResult.Catalog.Modules)), int64(len(catalogResult.Catalog.Modules)), "modules")
	if jsonMode {
		return emitOK(map[string]any{
			"workspace": workspace, "source": profile.Name, "lock_path": lockPath,
			"module_root": absolutePath(view.ModuleRoot), "view_digest": view.Digest,
			"modules": moduleLockDocument(updatedLock),
		})
	}
	fmt.Println(lockPath)
	return nil
}

func resolveRemoteModuleLock(workspace, configPath, moduleRoot string, lock *moduleLock) ([]string, *moduleLock, error) {
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, nil, preconditionErrorf("config_invalid", "%s", err.Error())
	}
	reg, err := loadRegistryDir(moduleRoot)
	if err != nil {
		return nil, nil, preconditionErrorf("module_root_invalid", "%s", err.Error())
	}
	if err := validateConfigRuntimeKeyCollisions(configPath, reg); err != nil {
		return nil, nil, preconditionErrorf("config_invalid", "%s", err.Error())
	}
	privateStore, err := loadSecretStore(stateDir(workspace))
	if err != nil {
		return nil, nil, preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if err := rejectUnimportedConfigSecrets(configPath, reg, privateStore.values); err != nil {
		return nil, nil, preconditionErrorf("config_requires_import", "%s", err.Error())
	}
	contracts, err := loadContractRegistry(moduleRoot)
	if err != nil {
		return nil, nil, preconditionErrorf("contract_root_invalid", "%s", err.Error())
	}
	a := &app{
		workspace: workspace, base: stateDir(workspace), cfgPath: configPath,
		cfg: cfg, reg: reg, contracts: contracts, lock: lock,
		resolvedBindings: map[string]map[string]string{},
	}
	a.env, a.envOwner = configBaseEnvWithRegistry(cfg, reg)
	if err := a.loadImportedSecrets(); err != nil {
		return nil, nil, preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	if err := a.validateContractRegistry(); err != nil {
		return nil, nil, preconditionErrorf("contract_invalid", "%s", err.Error())
	}
	a.order, err = a.resolveOrderWithInputValidation(cfg.Modules.Order)
	if err != nil {
		return nil, nil, preconditionErrorf("resolution_failed", "%s", err.Error())
	}
	if err := a.validateVersions(lock); err != nil {
		return nil, nil, preconditionErrorf("version_conflict", "%s", err.Error())
	}
	if err := a.updateModuleLock(lock, true); err != nil {
		return nil, nil, failuref("lock_update_failed", "%s", err.Error())
	}
	lock.Snapshot, err = resolveSnapshotLock(workspace, cfg)
	if err != nil {
		return nil, nil, preconditionErrorf("snapshot_policy_invalid", "%s", err.Error())
	}
	a.applyModuleDefaults()
	if err := a.validateModules(); err != nil {
		return nil, nil, preconditionErrorf("config_invalid", "%s", err.Error())
	}
	return append([]string(nil), a.order...), lock, nil
}

func validateRemoteViewAgainstLock(view modulestore.View, lock *moduleLock) error {
	reg, err := loadRegistryDir(view.ModuleRoot)
	if err != nil {
		return err
	}
	contracts, err := loadContractRegistry(view.ModuleRoot)
	if err != nil {
		return err
	}
	usedContracts := map[string]bool{}
	for _, name := range sortedLockModuleNames(lock) {
		mod, ok := reg[name]
		if !ok {
			return fmt.Errorf("Module view is missing %s", name)
		}
		record := lock.Modules[name]
		if mod.Version != record.Version || mod.Revision != record.Revision {
			return fmt.Errorf("Module %s release does not match config lock", name)
		}
		digest, err := moduleBundleDigest(mod.SourceDir)
		if err != nil {
			return err
		}
		if digest != record.Digest {
			return fmt.Errorf("Module %s installed-tree digest does not match config lock", name)
		}
		for _, dependency := range mod.RequiresContracts {
			usedContracts[dependency.Name] = true
		}
		for _, provider := range mod.ContractProviders {
			usedContracts[provider.Name] = true
		}
	}
	for name := range usedContracts {
		installed, ok := contracts[name]
		locked, lockedOK := lock.Contracts[name]
		if !ok || !lockedOK || installed.Version != locked.Version || installed.Digest != locked.Digest {
			return fmt.Errorf("contract %s does not match config lock", name)
		}
	}
	return nil
}

func sortedLockModuleNames(lock *moduleLock) []string {
	names := make([]string, 0, len(lock.Modules))
	for name := range lock.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func moduleInstallationDocument(installations map[string]modulestore.Installation) []map[string]any {
	names := make([]string, 0, len(installations))
	for name := range installations {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		installation := installations[name]
		out = append(out, map[string]any{
			"name": name, "release": installation.Release, "oci_digest": installation.OCIDigest,
			"content_digest": installation.ContentDigest, "path": absolutePath(installation.Path),
		})
	}
	return out
}
