// Package appcatalog defines the provider-independent application directory.
// It contains no IAM credentials or provider-specific authorization shortcuts.
package appcatalog

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/anas-project/ANAS/internal/apptheme"
)

const Schema = "anas.app-catalog/v1"

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)

type Text map[string]string

type Metadata struct {
	Name             string   `yaml:"name" json:"name"`
	DisplayName      Text     `yaml:"display_name" json:"display_name"`
	URL              string   `yaml:"url" json:"url"`
	Summary          Text     `yaml:"summary" json:"summary"`
	Description      Text     `yaml:"description" json:"description"`
	DocumentationURL Text     `yaml:"documentation_url" json:"documentation_url"`
	Weight           *int     `yaml:"weight" json:"weight"`
	Categories       []string `yaml:"categories" json:"categories"`
	Kind             string   `yaml:"kind" json:"kind"`
	Hidden           bool     `yaml:"hidden" json:"hidden"`
}

type Declaration struct {
	Metadata    `yaml:",inline"`
	URLFrom     string               `yaml:"url_from"`
	EnabledBy   string               `yaml:"enabled_by"`
	Enabled     *bool                `yaml:"enabled"`
	AllowHTTP   bool                 `yaml:"allow_http"`
	Icons       IconInput            `yaml:"icons"`
	Access      Access               `yaml:"access"`
	Visibility  Visibility           `yaml:"visibility"`
	Credentials *CredentialReference `yaml:"credentials,omitempty"`
}

// CredentialReference is module-owned metadata, never a secret value. It is
// frozen into the private deployment manifest, not the application catalog.
type CredentialReference struct {
	LocalAccount string `yaml:"local_account,omitempty" json:"local_account,omitempty"`
	UsernameFrom string `yaml:"username_from,omitempty" json:"username_from,omitempty"`
	PasswordFrom string `yaml:"password_from,omitempty" json:"password_from,omitempty"`
}

type IconInput struct {
	Source string         `yaml:"source" json:"source,omitempty"`
	Sizes  map[int]string `yaml:"sizes" json:"sizes,omitempty"`
}

type Access struct {
	Mode       string `yaml:"mode" json:"mode"`
	Client     string `yaml:"client" json:"client,omitempty"`
	Role       string `yaml:"role" json:"role,omitempty"`
	Binding    string `yaml:"binding" json:"binding,omitempty"`
	GroupsFrom string `yaml:"groups_from" json:"groups_from,omitempty"`
}

type Subject struct {
	Group string `yaml:"group" json:"group,omitempty"`
	Role  string `yaml:"role" json:"role,omitempty"`
}

type Visibility struct {
	Mode  string    `yaml:"mode" json:"mode"`
	AnyOf []Subject `yaml:"any_of" json:"any_of,omitempty"`
}

type Category struct {
	ID          string `yaml:"id" json:"id"`
	DisplayName Text   `yaml:"display_name" json:"display_name"`
	Description Text   `yaml:"description" json:"description,omitempty"`
	Weight      *int   `yaml:"weight" json:"weight"`
}

// Override deliberately has no identity, URL, access, kind or visibility keys.
type Override struct {
	DisplayName      Text       `yaml:"display_name"`
	Summary          Text       `yaml:"summary"`
	Description      Text       `yaml:"description"`
	DocumentationURL Text       `yaml:"documentation_url"`
	Weight           *int       `yaml:"weight"`
	Categories       []string   `yaml:"categories"`
	Hidden           *bool      `yaml:"hidden"`
	Icons            *IconInput `yaml:"icons"`
}

type Config struct {
	Categories []Category          `yaml:"categories"`
	Overrides  map[string]Override `yaml:"overrides"`
	Entries    []Declaration       `yaml:"entries"`
	IconTheme  string              `yaml:"icon_theme"`
	ThemeFile  string              `yaml:"theme_file"`
}

type Consumer struct {
	Consumes    bool     `yaml:"consumes"`
	Limitations []string `yaml:"limitations"`
}

type Source struct {
	Kind   string `json:"kind"`
	Module string `json:"module,omitempty"`
	Path   string `json:"path"`
}

type Icon struct {
	Path      string `json:"path"`
	MediaType string `json:"media_type"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	SHA256    string `json:"sha256"`
}

type Application struct {
	Metadata
	CredentialsAvailable bool            `json:"credentials_available,omitempty"`
	Enabled              bool            `json:"enabled"`
	Access               Access          `json:"access"`
	Visibility           Visibility      `json:"visibility"`
	Groups               []string        `json:"groups"`
	Icons                map[string]Icon `json:"icons"`
	OriginalIcons        map[string]Icon `json:"original_icons,omitempty"`
	Source               Source          `json:"source"`
}

type Catalog struct {
	Schema          string                          `json:"schema"`
	Languages       []string                        `json:"languages"`
	DefaultLanguage string                          `json:"default_language"`
	Categories      []Category                      `json:"categories"`
	Applications    []Application                   `json:"applications"`
	IconTheme       string                          `json:"icon_theme,omitempty"`
	ThemeName       Text                            `json:"theme_name,omitempty"`
	ThemeColors     map[string]apptheme.Palette     `json:"theme_colors,omitempty"`
	ThemeSources    map[string]apptheme.Attribution `json:"theme_sources,omitempty"`
}

// Input is resolved by Runner from actual execution-point authorization facts.
// Root and Icons are build-time sources and never appear in the public catalog.
type Input struct {
	Application Application
	Icons       IconInput
	Root        string
	Builtin     bool
	AllowHTTP   bool
}

func Weight(value *int) int {
	if value == nil {
		return 100
	}
	return *value
}

func validateWeight(value *int) error {
	if Weight(value) < 0 || Weight(value) > 10000 {
		return fmt.Errorf("weight must be an integer between 0 and 10000")
	}
	return nil
}

func validateText(field string, text Text, limit int, required, bilingual, isURL bool) error {
	if len(text) == 0 && required {
		return fmt.Errorf("%s needs at least one translation", field)
	}
	for lang, value := range text {
		if !SupportedKey(lang) {
			return fmt.Errorf("%s has an unsupported language key", field)
		}
		if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > limit {
			return fmt.Errorf("%s.%s is empty or exceeds %d characters", field, lang, limit)
		}
		for _, r := range value {
			if (unicode.IsControl(r) && r != '\n' && r != '\t') || r == '<' || r == '>' {
				return fmt.Errorf("%s.%s must be plain text", field, lang)
			}
		}
		if isURL {
			if err := ValidateURL(value, false); err != nil {
				return fmt.Errorf("%s.%s: %w", field, lang, err)
			}
		}
	}
	if bilingual && (required || len(text) > 0) && (text["zh-CN"] == "" || text["en"] == "") {
		return fmt.Errorf("%s needs both zh-CN and en translations", field)
	}
	return nil
}

func ValidateURL(value string, allowHTTP bool) error {
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || strings.ContainsAny(value, "\x00\r\n\t ") || (u.Scheme != "https" && !(allowHTTP && u.Scheme == "http")) {
		return fmt.Errorf("URL must be absolute HTTPS without credentials (HTTP requires allow_http)")
	}
	return nil
}

func validateMetadata(meta Metadata, bilingual bool, allowHTTP bool, resolved bool) error {
	if !namePattern.MatchString(meta.Name) {
		return fmt.Errorf("invalid application name")
	}
	for _, field := range []struct {
		name  string
		text  Text
		limit int
		url   bool
	}{
		{"display_name", meta.DisplayName, 100, false}, {"summary", meta.Summary, 160, false},
		{"description", meta.Description, 4000, false}, {"documentation_url", meta.DocumentationURL, 4096, true},
	} {
		if err := validateText(field.name, field.text, field.limit, true, bilingual, field.url); err != nil {
			return err
		}
	}
	if resolved || meta.URL != "" {
		if err := ValidateURL(meta.URL, allowHTTP); err != nil {
			return err
		}
	}
	if err := validateWeight(meta.Weight); err != nil {
		return err
	}
	if meta.Kind != "user" && meta.Kind != "admin" {
		return fmt.Errorf("kind must be user or admin")
	}
	if len(meta.Categories) == 0 {
		return fmt.Errorf("categories must not be empty")
	}
	seen := map[string]bool{}
	for _, id := range meta.Categories {
		if !namePattern.MatchString(id) || seen[id] {
			return fmt.Errorf("categories must contain distinct valid IDs")
		}
		seen[id] = true
	}
	return nil
}

func ValidateDeclaration(d Declaration, builtin bool) error {
	if c := d.Credentials; c != nil {
		if !builtin || d.Kind != "admin" || d.Access.Mode != "local" {
			return fmt.Errorf("credentials require a built-in local administration entry")
		}
		local := c.LocalAccount != "" && namePattern.MatchString(c.LocalAccount) && c.UsernameFrom == "" && c.PasswordFrom == ""
		env := c.LocalAccount == "" && c.UsernameFrom != "" && c.PasswordFrom != "" && c.UsernameFrom != c.PasswordFrom
		if !local && !env {
			return fmt.Errorf("credentials require exactly one local account or username/password sources")
		}
	}
	if err := validateMetadata(d.Metadata, builtin, d.AllowHTTP, false); err != nil {
		return err
	}
	if (d.URL == "") == (d.URLFrom == "") {
		return fmt.Errorf("exactly one of url and url_from is required")
	}
	if d.Enabled != nil && d.EnabledBy != "" {
		return fmt.Errorf("enabled and enabled_by are mutually exclusive")
	}
	if !builtin && (d.URLFrom != "" || d.EnabledBy != "" || d.Access.Mode != "external") {
		return fmt.Errorf("external entries require explicit URL and external access")
	}
	switch d.Access.Mode {
	case "iam":
		if !namePattern.MatchString(d.Access.Client) || d.Access.Role != "" || d.Access.Binding != "" || d.Access.GroupsFrom != "" {
			return fmt.Errorf("iam access requires only client")
		}
	case "forward_auth":
		if d.Access.Role != "platform_admin" || d.Access.Client != "" || d.Access.GroupsFrom != "" {
			return fmt.Errorf("forward_auth access requires platform_admin and its binding")
		}
	case "native":
		if d.Access.GroupsFrom == "" || d.Access.Client != "" || d.Access.Role != "" || d.Access.Binding != "" {
			return fmt.Errorf("native access requires only groups_from")
		}
	case "local", "external":
		if d.Access.Client != "" || d.Access.Role != "" || d.Access.Binding != "" || d.Access.GroupsFrom != "" {
			return fmt.Errorf("local/external access cannot declare an IAM execution point")
		}
	default:
		return fmt.Errorf("unsupported access mode")
	}
	switch d.Visibility.Mode {
	case "access":
		if d.Access.Mode == "local" || d.Access.Mode == "external" || len(d.Visibility.AnyOf) > 0 {
			return fmt.Errorf("access visibility requires an existing execution point and no subjects")
		}
	case "subjects":
		if (d.Access.Mode != "local" && d.Access.Mode != "external") || len(d.Visibility.AnyOf) == 0 {
			return fmt.Errorf("subjects visibility needs local/external access and a nonempty allow list")
		}
		for _, subject := range d.Visibility.AnyOf {
			if (subject.Group == "") == (subject.Role == "") || (subject.Role != "" && subject.Role != "platform_admin") || strings.ContainsAny(subject.Group, "\x00\r\n,") {
				return fmt.Errorf("subject needs one group or platform_admin role")
			}
			if d.Kind == "admin" && subject.Role != "platform_admin" {
				return fmt.Errorf("admin entries require platform_admin exclusively")
			}
		}
	case "authenticated":
		// A hidden IAM entry supplies branding for an ungated login client,
		// such as the portal itself. It cannot become a visible directory card.
		hiddenIAM := d.Hidden && d.Access.Mode == "iam"
		if d.Kind != "user" || (!hiddenIAM && d.Access.Mode != "local" && d.Access.Mode != "external") || len(d.Visibility.AnyOf) > 0 {
			return fmt.Errorf("authenticated visibility is only for local/external user entries without an ANAS gate")
		}
	default:
		return fmt.Errorf("visibility must explicitly declare access, subjects or authenticated")
	}
	return validateIconInput(d.Icons)
}

func validateCategory(c Category, builtin bool) error {
	if !namePattern.MatchString(c.ID) {
		return fmt.Errorf("invalid category ID")
	}
	if err := validateText("category.display_name", c.DisplayName, 100, true, builtin, false); err != nil {
		return err
	}
	if err := validateText("category.description", c.Description, 4000, false, builtin, false); err != nil {
		return err
	}
	return validateWeight(c.Weight)
}

func (c Config) Validate() error {
	if c.IconTheme != "" && c.IconTheme != "original" && c.IconTheme != "soft" && c.IconTheme != "custom" {
		return fmt.Errorf("icon_theme must be original, soft or custom")
	}
	if c.IconTheme == "custom" {
		if err := validateAssetPath(c.ThemeFile); err != nil {
			return fmt.Errorf("theme_file: %w", err)
		}
	} else if c.ThemeFile != "" {
		return fmt.Errorf("theme_file requires icon_theme custom")
	}
	seen := map[string]bool{}
	for _, category := range c.Categories {
		if seen[category.ID] {
			return fmt.Errorf("duplicate category ID")
		}
		seen[category.ID] = true
		if err := validateCategory(category, false); err != nil {
			return err
		}
	}
	seen = map[string]bool{}
	for _, entry := range c.Entries {
		if seen[entry.Name] {
			return fmt.Errorf("duplicate external application name")
		}
		seen[entry.Name] = true
		if err := ValidateDeclaration(entry, false); err != nil {
			return fmt.Errorf("entry %s: %w", entry.Name, err)
		}
	}
	for name, override := range c.Overrides {
		if !namePattern.MatchString(name) {
			return fmt.Errorf("invalid override application name")
		}
		for _, field := range []struct {
			name  string
			text  Text
			limit int
			url   bool
		}{
			{"display_name", override.DisplayName, 100, false}, {"summary", override.Summary, 160, false},
			{"description", override.Description, 4000, false}, {"documentation_url", override.DocumentationURL, 4096, true},
		} {
			if field.text != nil {
				if err := validateText(field.name, field.text, field.limit, true, false, field.url); err != nil {
					return err
				}
			}
		}
		if err := validateWeight(override.Weight); err != nil {
			return err
		}
		if override.Icons != nil {
			if err := validateIconInput(*override.Icons); err != nil {
				return err
			}
		}
		if override.Categories != nil && len(override.Categories) == 0 {
			return fmt.Errorf("override categories cannot be empty")
		}
	}
	return nil
}

func mergeText(base, override Text) Text {
	result := Text{}
	for key, value := range base {
		result[key] = value
	}
	for key, value := range override {
		result[key] = value
	}
	return result
}

// Compile freezes complete language maps; it never projects to one language.
func Compile(config Config, inputs []Input, configRoot, defaultLanguage string) (Catalog, []Input, error) {
	if err := config.Validate(); err != nil {
		return Catalog{}, nil, err
	}
	categories := BuiltinCategories()
	for _, category := range config.Categories {
		previous, exists := categories[category.ID]
		if exists {
			category.DisplayName = mergeText(previous.DisplayName, category.DisplayName)
			category.Description = mergeText(previous.Description, category.Description)
			if category.Weight == nil {
				category.Weight = previous.Weight
			}
		}
		categories[category.ID] = category
	}
	result := Catalog{Schema: Schema, Languages: []string{"zh-CN", "en"}, DefaultLanguage: SelectLanguage("", "", defaultLanguage), Categories: []Category{}, Applications: []Application{}}
	for _, c := range categories {
		value := Weight(c.Weight)
		c.Weight = &value
		result.Categories = append(result.Categories, c)
	}
	sort.Slice(result.Categories, func(i, j int) bool {
		a, b := result.Categories[i], result.Categories[j]
		if Weight(a.Weight) != Weight(b.Weight) {
			return Weight(a.Weight) < Weight(b.Weight)
		}
		return a.ID < b.ID
	})
	seen := map[string]bool{}
	usedOverrides := map[string]bool{}
	for i := range inputs {
		input := &inputs[i]
		app := &input.Application
		if seen[app.Name] {
			return Catalog{}, nil, fmt.Errorf("duplicate application name %s", app.Name)
		}
		seen[app.Name] = true
		if override, ok := config.Overrides[app.Name]; ok {
			if app.Source.Kind != "module" {
				return Catalog{}, nil, fmt.Errorf("external entries must be edited directly, not through overrides")
			}
			usedOverrides[app.Name] = true
			app.DisplayName = mergeText(app.DisplayName, override.DisplayName)
			app.Summary = mergeText(app.Summary, override.Summary)
			app.Description = mergeText(app.Description, override.Description)
			app.DocumentationURL = mergeText(app.DocumentationURL, override.DocumentationURL)
			if override.Weight != nil {
				app.Weight = override.Weight
			}
			if override.Categories != nil {
				app.Categories = append([]string{}, override.Categories...)
			}
			if override.Hidden != nil {
				app.Hidden = *override.Hidden
			}
			if override.Icons != nil {
				input.Icons = *override.Icons
				input.Root = configRoot
			}
		}
		if err := validateMetadata(app.Metadata, input.Builtin, input.AllowHTTP, true); err != nil {
			return Catalog{}, nil, fmt.Errorf("application %s: %w", app.Name, err)
		}
		for _, id := range app.Categories {
			if _, ok := categories[id]; !ok {
				return Catalog{}, nil, fmt.Errorf("application %s references unknown category %s", app.Name, id)
			}
		}
		if app.Enabled && app.Visibility.Mode != "authenticated" && len(app.Groups) == 0 {
			return Catalog{}, nil, fmt.Errorf("application %s resolved to an empty allow list", app.Name)
		}
		if app.Access.Mode == "iam" && app.Visibility.Mode == "authenticated" && !app.Hidden {
			return Catalog{}, nil, fmt.Errorf("ungated IAM branding entry must remain hidden")
		}
		value := Weight(app.Weight)
		app.Weight = &value
		app.Groups = sortedUnique(app.Groups)
		result.Applications = append(result.Applications, *app)
	}
	for name := range config.Overrides {
		if !usedOverrides[name] {
			return Catalog{}, nil, fmt.Errorf("override references undeclared application %s", name)
		}
	}
	sort.Slice(result.Applications, func(i, j int) bool { return result.Applications[i].Name < result.Applications[j].Name })
	return result, inputs, nil
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func BuiltinCategories() map[string]Category {
	result := map[string]Category{}
	for i, labels := range [][3]string{
		{"files", "文件", "Files"}, {"collaboration", "协作", "Collaboration"}, {"media", "影音", "Media"},
		{"development", "开发", "Development"}, {"database", "数据库", "Databases"}, {"network", "网络", "Network"},
		{"identity", "身份与权限", "Identity and access"}, {"monitoring", "监控", "Monitoring"}, {"security", "安全", "Security"}, {"other", "其他", "Other"},
	} {
		weight := (i + 1) * 10
		result[labels[0]] = Category{ID: labels[0], DisplayName: Text{"zh-CN": labels[1], "en": labels[2]}, Weight: &weight}
	}
	return result
}
