package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/anas-project/ANAS/internal/groupcatalog"
	"gopkg.in/yaml.v3"
)

func identityGroupDirectory(root string) string {
	if filepath.Base(root) == "modules" {
		root = filepath.Dir(root)
	}
	return filepath.Join(root, "shared", "identity-groups")
}

func validateIdentityGroupMount(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc struct {
		Services map[string]struct {
			Volumes []any `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	found := false
	for _, s := range doc.Services {
		for _, v := range s.Volumes {
			switch v := v.(type) {
			case string:
				if !strings.Contains(v, "ANAS_IDENTITY_GROUPS_DIR") {
					continue
				}
				if v != "${ANAS_IDENTITY_GROUPS_DIR}:/run/anas/identity-groups:ro" {
					return fmt.Errorf("identity group catalog requires its fixed read-only mount")
				}
				found = true
			case map[string]any:
				source, _ := v["source"].(string)
				if !strings.Contains(source, "ANAS_IDENTITY_GROUPS_DIR") {
					continue
				}
				if v["type"] != "bind" || source != "${ANAS_IDENTITY_GROUPS_DIR}" || v["target"] != "/run/anas/identity-groups" || v["read_only"] != true {
					return fmt.Errorf("identity group catalog requires its fixed read-only bind")
				}
				found = true
			}
		}
	}
	if !found {
		return fmt.Errorf("identity group catalog consumer has no read-only mount")
	}
	return nil
}
func validateIdentityGroupManifest(mod Module) error {
	seen := map[string]bool{}
	for _, d := range mod.IdentityGroups {
		if err := groupcatalog.ValidateDeclaration(d); err != nil {
			return fmt.Errorf("module %s group %s: %w", mod.Name, d.ID, err)
		}
		if seen[d.ID] {
			return fmt.Errorf("module %s has duplicate group ids", mod.Name)
		}
		seen[d.ID] = true
		// Names must originate in the declaring module. The one existing aggregate
		// capability export remains a list input, selected by the owner's prefix.
		for _, key := range []string{d.NameFrom, d.NamesFrom} {
			if key != "" && !applicationReferenceAllowed(mod, key) {
				return fmt.Errorf("module %s group references undeclared names", mod.Name)
			}
		}
		if d.NamesFrom != "" && (d.SelectPrefix != mod.Name+"_" || d.NamePrefix != "CAP_") {
			return fmt.Errorf("module %s capability family must select its own names", mod.Name)
		}
	}
	if mod.IdentityGroupCatalog {
		if mod.RuntimeType != "compose" {
			return fmt.Errorf("group catalog requires compose")
		}
		if err := validateIdentityGroupMount(filepath.Join(mod.SourceDir, mod.ComposeFile)); err != nil {
			return fmt.Errorf("module %s: %w", mod.Name, err)
		}
	}
	return nil
}

func (a *app) prepareIdentityGroupCatalog() error {
	enabled := false
	for _, name := range a.order {
		m := a.reg[name]
		enabled = enabled || m.IdentityGroupCatalog || len(m.IdentityGroups) > 0
	}
	if !enabled {
		return nil
	}
	c := groupcatalog.Catalog{Schema: groupcatalog.Schema, Groups: []groupcatalog.Group{}}
	provider := a.cfg.Identity.Directory.Provider
	// Config leaves the sole built-in directory provider implicit by default.
	if provider == "" {
		provider = "samba_dc"
	}
	for _, name := range a.order {
		mod := a.reg[name]
		for _, d := range mod.IdentityGroups {
			for _, key := range []string{d.NameFrom, d.NamesFrom, d.ParentDNFrom, d.When} {
				if key != "" && a.sensitiveEnvKeySet()[key] {
					return fmt.Errorf("group declaration references a sensitive value")
				}
			}
			// Only the selected directory provider can publish system descriptions or
			// choose the directory parent/switches for managed groups.
			if !d.Managed && name != provider {
				return fmt.Errorf("system group descriptions must belong to the directory provider")
			}
			if d.ParentDNFrom != "" && a.envOwner[d.ParentDNFrom] != provider {
				return fmt.Errorf("group parent is not owned by the selected directory provider")
			}
			if d.When != "" && a.envOwner[d.When] != provider && !applicationReferenceAllowed(mod, d.When) {
				return fmt.Errorf("group condition is not owned by its module or directory provider")
			}
			groups, err := groupcatalog.Expand(name, d, a.env)
			if err != nil {
				return fmt.Errorf("%s group %s: %w", name, d.ID, err)
			}
			c.Groups = append(c.Groups, groups...)
		}
	}
	// Application access names follow the already resolved identity topology.
	// Titles come from each application's existing module declaration.
	if a.env["SAMBA_DC_APP_FILTER"] == "true" {
		for _, name := range catalogGroupList(a.env[envIdentityAppClients]) {
			mod, ok := a.reg[name]
			if !ok {
				return fmt.Errorf("application group has no owning module")
			}
			title := appcatalog.Text{"en": mod.Title}
			if title["en"] == "" {
				title["en"] = name
			}
			for _, entry := range mod.Applications {
				if entry.Kind == "user" {
					title = entry.DisplayName
					break
				}
			}
			display, description := appcatalog.Text{}, appcatalog.Text{}
			for _, lang := range []string{"en", "zh-CN"} {
				label := title.Resolve(lang)
				if lang == "en" {
					display[lang] = label + " access"
					description[lang] = "Allows entry to " + label + ". Application-specific permissions and administration are controlled separately."
				} else {
					display[lang] = label + " 访问"
					description[lang] = "允许进入 " + label + "；应用内部权限与管理能力另行控制。"
				}
			}
			d := groupcatalog.Declaration{ID: "application-access", Name: "APP_" + name, ParentDNFrom: "SAMBA_DC_BASE_APP_DN", Managed: true, Category: "applications", Order: 10, DisplayName: display, Description: description, Risk: "normal"}
			groups, err := groupcatalog.Expand(name, d, a.env)
			if err != nil {
				return err
			}
			c.Groups = append(c.Groups, groups...)
		}
	}
	if err := c.Validate(); err != nil {
		return err
	}
	c.Sort()
	a.identityGroupCatalog = &c
	// Retained CSV outputs are projections, not a second source of group facts.
	if provider != "" && a.reg[provider].IdentityGroupsFrom != "" {
		key := a.reg[provider].IdentityGroupsFrom
		a.env[key] = strings.Join(c.ManagedNames(), ",")
		a.setEnvOwner(key, provider)
	}
	return nil
}
func (a *app) renderIdentityGroupCatalog(work string) error {
	if a.identityGroupCatalog == nil {
		return nil
	}
	// Bind mounts must refer to the same local Docker host as the deployment.
	if err := a.validateCatalogEndpoint(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(a.identityGroupCatalog, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := identityGroupDirectory(work)
	if err := os.MkdirAll(filepath.Dir(dir), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}
	if err := os.Chmod(path, 0644); err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	a.identityGroupDigest = hex.EncodeToString(digest[:])
	return nil
}
func (a *app) identityGroupRenderEnvironment(mod Module, work string, env map[string]string) {
	if !mod.IdentityGroupCatalog || a.identityGroupCatalog == nil {
		return
	}
	root := a.artifactRoot
	if root == "" {
		root = work
	}
	env["ANAS_IDENTITY_GROUPS_DIR"] = identityGroupDirectory(root)
	env["ANAS_IDENTITY_GROUPS_DIGEST"] = a.identityGroupDigest
}
