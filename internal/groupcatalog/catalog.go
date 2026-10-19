// Package groupcatalog defines the immutable, deployment-scoped description of
// directory groups. It never supplies membership or grants directory rights.
package groupcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/anas-project/ANAS/internal/appcatalog"
	"github.com/go-ldap/ldap/v3"
)

const Schema = "anas.identity-groups/v1"

type Text = appcatalog.Text

type Declaration struct {
	ID           string `yaml:"id" json:"id"`
	Name         string `yaml:"name" json:"name"`
	NameFrom     string `yaml:"name_from" json:"name_from"`
	NamesFrom    string `yaml:"names_from" json:"names_from"`
	SelectPrefix string `yaml:"select_prefix" json:"select_prefix"`
	NamePrefix   string `yaml:"name_prefix" json:"name_prefix"`
	ParentDNFrom string `yaml:"parent_dn_from" json:"parent_dn_from"`
	SID          string `yaml:"sid" json:"sid"`
	DomainRID    int    `yaml:"domain_rid" json:"domain_rid"`
	When         string `yaml:"when" json:"when"`
	Managed      bool   `yaml:"managed" json:"managed"`
	Category     string `yaml:"category" json:"category"`
	Order        int    `yaml:"order" json:"order"`
	DisplayName  Text   `yaml:"display_name" json:"display_name"`
	Description  Text   `yaml:"description" json:"description"`
	Risk         string `yaml:"risk" json:"risk"`
	HelpURL      string `yaml:"help_url" json:"help_url"`
}

type Group struct {
	ID          string `json:"id"`
	Module      string `json:"module"`
	Name        string `json:"name"`
	DN          string `json:"dn,omitempty"`
	SID         string `json:"sid,omitempty"`
	DomainRID   int    `json:"domain_rid,omitempty"`
	Managed     bool   `json:"managed"`
	Category    string `json:"category"`
	Order       int    `json:"order"`
	DisplayName Text   `json:"display_name"`
	Description Text   `json:"description"`
	Risk        string `json:"risk"`
	HelpURL     string `json:"help_url,omitempty"`
}

type Catalog struct {
	Schema string  `json:"schema"`
	Groups []Group `json:"groups"`
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
var namePattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_ .-]{0,127}$`)
var envPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

func ValidateDeclaration(d Declaration) error {
	sources := 0
	for _, v := range []string{d.Name, d.NameFrom, d.NamesFrom} {
		if v != "" {
			sources++
		}
	}
	if !idPattern.MatchString(d.ID) || sources != 1 {
		return fmt.Errorf("group needs a stable id and exactly one name source")
	}
	for _, v := range []string{d.NameFrom, d.NamesFrom, d.ParentDNFrom, d.When} {
		if v != "" && !envPattern.MatchString(v) {
			return fmt.Errorf("invalid group environment reference")
		}
	}
	if d.Name != "" && !namePattern.MatchString(d.Name) {
		return fmt.Errorf("invalid group name")
	}
	if d.NamesFrom == "" && (d.SelectPrefix != "" || d.NamePrefix != "") {
		return fmt.Errorf("group list prefixes require names_from")
	}
	if d.Managed {
		if d.ParentDNFrom == "" || d.SID != "" || d.DomainRID != 0 || d.Category == "system" {
			return fmt.Errorf("managed groups need an exact directory parent and a managed category")
		}
	} else if d.ParentDNFrom != "" || (d.SID == "") == (d.DomainRID == 0) || d.Category != "system" {
		return fmt.Errorf("system descriptions need exactly one SID or domain RID")
	}
	switch d.Category {
	case "applications", "capabilities", "access", "roles", "system":
	default:
		return fmt.Errorf("invalid group category")
	}
	switch d.Risk {
	case "normal", "resource", "domain":
	default:
		return fmt.Errorf("invalid group risk")
	}
	if strings.TrimSpace(d.DisplayName["en"]) == "" || strings.TrimSpace(d.Description["en"]) == "" {
		return fmt.Errorf("group English display name and description are required")
	}
	for _, t := range []Text{d.DisplayName, d.Description} {
		for lang, v := range t {
			if lang != "en" && lang != "zh-CN" || len(v) > 4096 {
				return fmt.Errorf("invalid group translation")
			}
		}
	}
	if d.DomainRID < 0 || d.SID != "" && !regexp.MustCompile(`^S-1-5-32-[0-9]+$`).MatchString(d.SID) {
		return fmt.Errorf("invalid system group locator")
	}
	if d.HelpURL != "" {
		if err := appcatalog.ValidateURL(d.HelpURL, false); err != nil {
			return fmt.Errorf("invalid group help URL")
		}
	}
	return nil
}

func Expand(module string, d Declaration, env map[string]string) ([]Group, error) {
	if err := ValidateDeclaration(d); err != nil {
		return nil, err
	}
	if d.When != "" {
		switch env[d.When] {
		case "false":
			return nil, nil
		case "true":
		default:
			return nil, fmt.Errorf("group condition is not a resolved boolean")
		}
	}
	names := []string{d.Name}
	if d.NameFrom != "" {
		names = []string{env[d.NameFrom]}
	}
	if d.NamesFrom != "" {
		names = strings.Split(env[d.NamesFrom], ",")
	}
	out := []Group{}
	seen := map[string]bool{}
	for _, value := range names {
		value = strings.TrimSpace(value)
		if d.NamesFrom != "" && (value == "" || !strings.HasPrefix(value, d.SelectPrefix)) {
			continue
		}
		name := d.NamePrefix + value
		if !namePattern.MatchString(name) || seen[name] {
			return nil, fmt.Errorf("invalid or duplicate resolved group name")
		}
		seen[name] = true
		id := module + "." + d.ID
		if d.NamesFrom != "" {
			id += "." + strings.ToLower(strings.ReplaceAll(value, "_", "-"))
		}
		text := func(in Text) Text {
			out := Text{}
			for lang, v := range in {
				out[lang] = strings.ReplaceAll(v, "{name}", name)
			}
			return out
		}
		g := Group{ID: id, Module: module, Name: name, SID: d.SID, DomainRID: d.DomainRID, Managed: d.Managed, Category: d.Category, Order: d.Order, DisplayName: text(d.DisplayName), Description: text(d.Description), Risk: d.Risk, HelpURL: d.HelpURL}
		if d.Managed {
			parent := env[d.ParentDNFrom]
			if _, err := ldap.ParseDN(parent); err != nil || parent == "" {
				return nil, fmt.Errorf("group directory parent unavailable")
			}
			g.DN = "CN=" + ldap.EscapeDN(name) + "," + parent
		}
		out = append(out, g)
	}
	return out, nil
}

func (c *Catalog) Validate() error {
	if c.Schema != Schema || len(c.Groups) > 5000 {
		return fmt.Errorf("invalid identity group catalog")
	}
	ids, names, locators := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, g := range c.Groups {
		if !namePattern.MatchString(g.Name) || g.ID == "" || ids[g.ID] || g.Module == "" || g.DisplayName["en"] == "" || g.Description["en"] == "" {
			return fmt.Errorf("invalid or duplicate group metadata")
		}
		ids[g.ID] = true
		locator := g.SID
		if g.DomainRID != 0 {
			locator = fmt.Sprintf("domain:%d", g.DomainRID)
		}
		if g.Managed {
			parsed, err := ldap.ParseDN(g.DN)
			if err != nil || len(parsed.RDNs) < 2 || len(parsed.RDNs[0].Attributes) != 1 || !strings.EqualFold(parsed.RDNs[0].Attributes[0].Type, "CN") || parsed.RDNs[0].Attributes[0].Value != g.Name || locator != "" || g.Category == "system" {
				return fmt.Errorf("invalid managed group locator")
			}
			locator = strings.ToLower(parsed.String())
			name := strings.ToLower(g.Name)
			if names[name] {
				return fmt.Errorf("duplicate managed group name")
			}
			names[name] = true
		} else if locator == "" || g.DN != "" || g.Category != "system" {
			return fmt.Errorf("invalid system group locator")
		}
		if locators[locator] {
			return fmt.Errorf("duplicate group locator")
		}
		locators[locator] = true
	}
	return nil
}

func (c *Catalog) Sort() {
	sort.Slice(c.Groups, func(i, j int) bool {
		a, b := c.Groups[i], c.Groups[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.ID < b.ID
	})
}
func (c Catalog) ManagedNames() []string {
	out := []string{}
	for _, g := range c.Groups {
		if g.Managed {
			out = append(out, g.Name)
		}
	}
	sort.Strings(out)
	return out
}
func Read(path string) (Catalog, error) {
	var c Catalog
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() > 4<<20 {
		return c, fmt.Errorf("group catalog exceeds size limit")
	}
	dec := json.NewDecoder(io.LimitReader(f, 4<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("trailing group catalog data")
	}
	return c, c.Validate()
}
