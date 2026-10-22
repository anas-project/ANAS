package runner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"gopkg.in/yaml.v3"
)

const applicationCatalogRelative = "shared/application-catalog"

func validCatalogAssetTarget(target string) bool {
	return strings.HasPrefix(target, "/") && filepath.Clean(target) == target && target != "/" && target != "/run/anas/application-catalog"
}
func validCatalogAssetMount(value string) bool {
	parts := strings.Split(value, ":")
	return len(parts) == 3 && parts[0] == "${ANAS_APP_CATALOG_DIR}/assets" && validCatalogAssetTarget(parts[1]) && parts[2] == "ro"
}

func iamClientKey(name, field string) string {
	return "ANAS_IAM_CLIENT__" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "__" + field
}

func validateCatalogComposeMount(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	found := false
	for _, service := range document.Services {
		for _, volume := range service.Volumes {
			switch value := volume.(type) {
			case string:
				if !strings.Contains(value, "ANAS_APP_CATALOG_DIR") {
					continue
				}
				if value != "${ANAS_APP_CATALOG_DIR}:/run/anas/application-catalog:ro" && value != "$ANAS_APP_CATALOG_DIR:/run/anas/application-catalog:ro" && !validCatalogAssetMount(value) {
					return fmt.Errorf("catalog mounts must use fixed read-only private/asset targets")
				}
				if strings.Contains(value, ":/run/anas/application-catalog:ro") {
					found = true
				}
			case map[string]any:
				source, _ := value["source"].(string)
				if !strings.Contains(source, "ANAS_APP_CATALOG_DIR") {
					continue
				}
				target, _ := value["target"].(string)
				ro, _ := value["read_only"].(bool)
				if value["type"] != "bind" || !ro || !((source == "${ANAS_APP_CATALOG_DIR}" && target == "/run/anas/application-catalog") || (source == "${ANAS_APP_CATALOG_DIR}/assets" && validCatalogAssetTarget(target))) {
					return fmt.Errorf("catalog bind mounts must have fixed targets and read_only: true")
				}
				if target == "/run/anas/application-catalog" {
					found = true
				}
			}
		}
	}
	if !found {
		return fmt.Errorf("catalog consumer must mount ${ANAS_APP_CATALOG_DIR} at /run/anas/application-catalog read-only")
	}
	return nil
}

func catalogDirectory(modulesRoot string) string {
	if filepath.Base(modulesRoot) == "modules" {
		return filepath.Join(filepath.Dir(modulesRoot), applicationCatalogRelative)
	}
	return filepath.Join(modulesRoot, applicationCatalogRelative)
}

func applicationReferenceAllowed(mod Module, key string) bool {
	return envKeyPattern.MatchString(key) && (strings.HasPrefix(key, mod.EnvPrefix+"_") || matchEnvPattern(mod.Exports, key) || matchEnvPattern(mod.Consumes, key))
}

func validateApplicationManifest(mod Module) error {
	if err := validateIdentityGroupManifest(mod); err != nil {
		return err
	}
	if len(mod.Applications) > 0 && mod.ApplicationsNotPublished != "" {
		return fmt.Errorf("module %s declares applications and a no-entry reason", mod.Name)
	}
	seen := map[string]bool{}
	for _, entry := range mod.Applications {
		if err := appcatalog.ValidateDeclaration(entry, true); err != nil {
			return fmt.Errorf("module %s application %s: %w", mod.Name, entry.Name, err)
		}
		if seen[entry.Name] {
			return fmt.Errorf("module %s has duplicate application names", mod.Name)
		}
		seen[entry.Name] = true
		for _, key := range []string{entry.URLFrom, entry.Access.GroupsFrom} {
			if key != "" && !applicationReferenceAllowed(mod, key) {
				return fmt.Errorf("module %s application references undeclared environment key", mod.Name)
			}
		}
		if entry.EnabledBy != "" {
			parameter, ok := mod.Types[entry.EnabledBy]
			if !ok || parameter.Kind != "bool" {
				return fmt.Errorf("module %s application enabled_by must reference its declared boolean parameter", mod.Name)
			}
		}
	}
	for role, key := range mod.IdentityRoles {
		if role != "platform_admin" || !envKeyPattern.MatchString(key) || !strings.HasPrefix(key, mod.EnvPrefix+"_") {
			return fmt.Errorf("module %s identity.roles must map platform_admin to its own group export", mod.Name)
		}
	}
	if mod.IdentityGroupsFrom != "" && (!envKeyPattern.MatchString(mod.IdentityGroupsFrom) || !strings.HasPrefix(mod.IdentityGroupsFrom, mod.EnvPrefix+"_")) {
		return fmt.Errorf("module %s identity.groups_from must reference its own group export", mod.Name)
	}
	if mod.ApplicationCatalog.Consumes {
		if mod.RuntimeType != "compose" {
			return fmt.Errorf("module %s application_catalog requires a compose runtime with a read-only mount", mod.Name)
		}
		if err := validateCatalogComposeMount(filepath.Join(mod.SourceDir, mod.ComposeFile)); err != nil {
			return fmt.Errorf("module %s: %w", mod.Name, err)
		}
	}
	return nil
}

func catalogGroupList(value string) []string {
	out := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !contains(out, part) {
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func (a *app) catalogGroupFacts() (map[string]bool, map[string]string, error) {
	known := map[string]bool{}
	roles := map[string]string{}
	for _, name := range a.order {
		mod := a.reg[name]
		if mod.IdentityGroupsFrom != "" {
			if a.sensitiveEnvKeySet()[mod.IdentityGroupsFrom] {
				return nil, nil, fmt.Errorf("directory groups export is sensitive")
			}
			if a.cfg.Identity.Directory.Provider != "" && a.cfg.Identity.Directory.Provider != name {
				return nil, nil, fmt.Errorf("directory group facts must come from the selected directory provider")
			}
			if a.envOwner[mod.IdentityGroupsFrom] != name {
				return nil, nil, fmt.Errorf("directory groups export has no provider ownership")
			}
			for _, group := range catalogGroupList(a.env[mod.IdentityGroupsFrom]) {
				known[group] = true
			}
		}
		for role, key := range mod.IdentityRoles {
			if a.sensitiveEnvKeySet()[key] {
				return nil, nil, fmt.Errorf("directory role export is sensitive")
			}
			if a.cfg.Identity.Directory.Provider != "" && a.cfg.Identity.Directory.Provider != name {
				return nil, nil, fmt.Errorf("identity role facts must come from the selected directory provider")
			}
			groups := catalogGroupList(a.env[key])
			if a.envOwner[key] != name || len(groups) != 1 {
				return nil, nil, fmt.Errorf("identity role must resolve to one provider-owned group")
			}
			if prior := roles[role]; prior != "" && prior != groups[0] {
				return nil, nil, fmt.Errorf("conflicting identity role facts")
			}
			roles[role] = groups[0]
			known[groups[0]] = true
		}
	}
	return known, roles, nil
}

func (a *app) hasApplicationCatalog() bool {
	if a.cfg != nil && (len(a.cfg.ApplicationCatalog.Entries) > 0 || len(a.cfg.ApplicationCatalog.Categories) > 0 || len(a.cfg.ApplicationCatalog.Overrides) > 0) {
		return true
	}
	for _, name := range a.order {
		if mod := a.reg[name]; mod.ApplicationCatalog.Consumes {
			return true
		}
	}
	return false
}

func (a *app) resolveApplicationInput(mod Module, entry appcatalog.Declaration, root string, external bool, known map[string]bool, roles map[string]string) (appcatalog.Input, error) {
	if err := appcatalog.ValidateDeclaration(entry, !external); err != nil {
		return appcatalog.Input{}, err
	}
	metadata := entry.Metadata
	if entry.URLFrom != "" {
		if !applicationReferenceAllowed(mod, entry.URLFrom) || a.sensitiveEnvKeySet()[entry.URLFrom] {
			return appcatalog.Input{}, fmt.Errorf("URL source is undeclared or sensitive")
		}
		metadata.URL = a.env[entry.URLFrom]
	}
	if err := appcatalog.ValidateURL(metadata.URL, entry.AllowHTTP); err != nil {
		return appcatalog.Input{}, err
	}
	enabled := true
	if entry.Enabled != nil {
		enabled = *entry.Enabled
	}
	if entry.EnabledBy != "" {
		value := a.env[parameterEnvKey(mod.Name, entry.EnabledBy, a.reg)]
		if value != "true" && value != "false" {
			return appcatalog.Input{}, fmt.Errorf("enabled_by did not resolve to a boolean")
		}
		enabled = value == "true"
	}
	groups := []string{}
	if !enabled {
		source := appcatalog.Source{Kind: "module", Module: mod.Name, Path: "module.yml"}
		return appcatalog.Input{Application: appcatalog.Application{Metadata: metadata, Enabled: false, Access: entry.Access, Visibility: entry.Visibility, Source: source}, Icons: entry.Icons, Root: root, Builtin: !external, AllowHTTP: entry.AllowHTTP}, nil
	}
	if entry.Access.Mode == "iam" {
		if _, ok := a.iamBindings[entry.Access.Client]; !ok || mod.Name != entry.Access.Client {
			return appcatalog.Input{}, fmt.Errorf("application IAM client must be registered by its publishing module")
		}
	}
	switch entry.Visibility.Mode {
	case "access":
		switch entry.Access.Mode {
		case "iam":
			groups = catalogGroupList(a.env[iamClientKey(entry.Access.Client, "ALLOW_GROUPS")])
		case "forward_auth":
			provider := a.resolvedBindings[mod.Name]["forward_auth"]
			if provider == "" || a.env["ANAS_FORWARD_AUTH_PROVIDER"] != provider || a.envOwner["ANAS_FORWARD_AUTH_MIDDLEWARE"] != provider || a.env["ANAS_FORWARD_AUTH_MIDDLEWARE"] == "" {
				return appcatalog.Input{}, fmt.Errorf("forward_auth has no resolved protected binding")
			}
			if entry.Access.Binding != "" && entry.Access.Binding != provider {
				return appcatalog.Input{}, fmt.Errorf("forward_auth binding does not match the actual gateway")
			}
			groups = catalogGroupList(a.env["ANAS_PROXY_PLATFORM_ADMIN_GROUP"])
			if a.envOwner["ANAS_PROXY_PLATFORM_ADMIN_GROUP"] != provider {
				return appcatalog.Input{}, fmt.Errorf("forward_auth group fact is not owned by its bound gateway")
			}
			if len(groups) != 1 || groups[0] != roles["platform_admin"] {
				return appcatalog.Input{}, fmt.Errorf("forward_auth role differs from directory role facts")
			}
		case "native":
			if !applicationReferenceAllowed(mod, entry.Access.GroupsFrom) || a.sensitiveEnvKeySet()[entry.Access.GroupsFrom] {
				return appcatalog.Input{}, fmt.Errorf("native group source is undeclared or sensitive")
			}
			groups = catalogGroupList(a.env[entry.Access.GroupsFrom])
		}
	case "subjects":
		for _, subject := range entry.Visibility.AnyOf {
			group := subject.Group
			if subject.Role != "" {
				group = roles[subject.Role]
			}
			if group == "" || !known[group] {
				return appcatalog.Input{}, fmt.Errorf("application subject references an unknown group or role")
			}
			groups = append(groups, group)
		}
	}
	if entry.Kind == "admin" && (len(groups) != 1 || roles["platform_admin"] == "" || groups[0] != roles["platform_admin"]) {
		return appcatalog.Input{}, fmt.Errorf("admin application must resolve exclusively to platform_admin")
	}
	if entry.Visibility.Mode != "authenticated" && len(groups) == 0 {
		return appcatalog.Input{}, fmt.Errorf("application resolved to an empty allow list")
	}
	for _, group := range groups {
		if !known[group] {
			return appcatalog.Input{}, fmt.Errorf("application execution point references an unknown group")
		}
	}
	source := appcatalog.Source{Kind: "module", Module: mod.Name, Path: "module.yml"}
	if external {
		source = appcatalog.Source{Kind: "config", Path: "application_catalog.entries"}
	}
	return appcatalog.Input{Application: appcatalog.Application{Metadata: metadata, Enabled: enabled, Access: entry.Access, Visibility: entry.Visibility, Groups: groups, Source: source}, Icons: entry.Icons, Root: root, Builtin: !external, AllowHTTP: entry.AllowHTTP}, nil
}

func (a *app) prepareApplicationCatalog() error {
	if !a.hasApplicationCatalog() {
		return nil
	}
	known, roles, err := a.catalogGroupFacts()
	if err != nil {
		return err
	}
	inputs := []appcatalog.Input{}
	for _, name := range a.order {
		mod := a.reg[name]
		for _, entry := range mod.Applications {
			input, err := a.resolveApplicationInput(mod, entry, mod.SourceDir, false, known, roles)
			if err != nil {
				return fmt.Errorf("application %s: %w", entry.Name, err)
			}
			inputs = append(inputs, input)
		}
	}
	root := filepath.Dir(a.cfgPath)
	for _, entry := range a.cfg.ApplicationCatalog.Entries {
		input, err := a.resolveApplicationInput(Module{}, entry, root, true, known, roles)
		if err != nil {
			return fmt.Errorf("application %s: %w", entry.Name, err)
		}
		inputs = append(inputs, input)
	}
	catalog, inputs, err := appcatalog.Compile(a.cfg.ApplicationCatalog, inputs, root, a.env["DEFAULT_LANGUAGE"])
	if err != nil {
		return err
	}
	catalog, assets, files, err := appcatalog.BuildAssets(catalog, inputs)
	if err != nil {
		return err
	}
	catalog, assets, files, err = appcatalog.ApplyIconTheme(a.cfg.ApplicationCatalog, root, catalog, assets, files)
	if err != nil {
		return fmt.Errorf("application theme: %w", err)
	}
	a.applicationCatalog = &catalog
	a.applicationCatalogAssets = &assets
	a.applicationCatalogFiles = files
	return nil
}

func (a *app) planApplicationCatalog() (string, error) {
	if !a.hasApplicationCatalog() {
		return "", nil
	}
	if err := a.validateCatalogEndpoint(); err != nil {
		return "", err
	}
	inputs := []appcatalog.Input{}
	names := map[string]bool{}
	for _, name := range a.order {
		mod := a.reg[name]
		for _, entry := range mod.Applications {
			if names[entry.Name] {
				return "", fmt.Errorf("duplicate application name")
			}
			names[entry.Name] = true
			inputs = append(inputs, appcatalog.Input{Application: appcatalog.Application{Metadata: entry.Metadata}, Icons: entry.Icons, Root: mod.SourceDir})
		}
	}
	for _, entry := range a.cfg.ApplicationCatalog.Entries {
		if names[entry.Name] {
			return "", fmt.Errorf("external application name collides with a module")
		}
		names[entry.Name] = true
		inputs = append(inputs, appcatalog.Input{Application: appcatalog.Application{Metadata: entry.Metadata}, Icons: entry.Icons, Root: filepath.Dir(a.cfgPath)})
	}
	for name := range a.cfg.ApplicationCatalog.Overrides {
		if !names[name] {
			return "", fmt.Errorf("application override references an unknown entry")
		}
	}
	return appcatalog.SourcesDigest(a.cfg.ApplicationCatalog, inputs, filepath.Dir(a.cfgPath))
}

// Local bind paths cannot be passed to an SSH/TCP Docker daemon. Resolve the
// same process-owned selectors used by Compose, including the active context.
func (a *app) validateCatalogEndpoint() error {
	hasConsumer := false
	for _, name := range a.order {
		if a.reg[name].ApplicationCatalog.Consumes || a.reg[name].IdentityGroupCatalog {
			hasConsumer = true
		}
	}
	if !hasConsumer {
		return nil
	}
	values := map[string]string{}
	for _, assignment := range a.compose.Environment(a.commandEnvironment(nil), nil) {
		key, value, ok := strings.Cut(assignment, "=")
		if ok {
			values[key] = value
		}
	}
	endpoint := values["DOCKER_HOST"]
	if values["DOCKER_CONTEXT"] != "" || endpoint == "" {
		body, err := a.temporaryDockerOutput("context", "inspect")
		if err != nil {
			return fmt.Errorf("cannot confirm catalog bind endpoint: %w", err)
		}
		var contexts []struct {
			Endpoints map[string]struct {
				Host string `json:"Host"`
			} `json:"Endpoints"`
		}
		if json.Unmarshal(body, &contexts) != nil || len(contexts) != 1 {
			return fmt.Errorf("catalog Docker endpoint is unknown")
		}
		endpoint = contexts[0].Endpoints["docker"].Host
	}
	if !strings.HasPrefix(endpoint, "unix://") || !filepath.IsAbs(strings.TrimPrefix(endpoint, "unix://")) {
		return fmt.Errorf("application catalog requires a local Docker bind endpoint; remote projection is not implemented")
	}
	return nil
}

func (a *app) renderApplicationCatalog(work string) error {
	if a.applicationCatalog == nil {
		return nil
	}
	if err := a.validateCatalogEndpoint(); err != nil {
		return err
	}
	dir := catalogDirectory(work)
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	digest, err := appcatalog.WriteBundle(dir, *a.applicationCatalog, *a.applicationCatalogAssets, a.applicationCatalogFiles)
	if err != nil {
		return err
	}
	a.applicationCatalogDigest = digest
	return nil
}

func (a *app) catalogRenderEnvironment(mod Module, work string, env map[string]string) {
	if !mod.ApplicationCatalog.Consumes || a.applicationCatalog == nil {
		return
	}
	finalModules := a.artifactRoot
	if finalModules == "" {
		finalModules = work
	}
	env["ANAS_APP_CATALOG_FILE"] = filepath.Join(catalogDirectory(work), "catalog.json")
	env["ANAS_APP_CATALOG_DIR"] = catalogDirectory(finalModules)
	env["ANAS_APP_CATALOG_DIGEST"] = a.applicationCatalogDigest
}
