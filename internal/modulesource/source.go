package modulesource

import "strings"

const (
	Official   = "official"
	OfficialCN = "official-cn"
)

// Profile describes a built-in Module distribution source. Version discovery
// uses Catalog; Repository is a template whose {name} placeholder is replaced
// with the Module name. Mirrors are ordered fallbacks for content whose digest
// must match the primary, not independent publishers.
type Profile struct {
	Name            string
	Catalog         string
	CatalogMirrors  []string
	Repository      string
	Mirrors         []string
	ChineseDefaults bool
}

var builtins = []Profile{
	{
		Name:    Official,
		Catalog: "oci://ghcr.io/anas-project/anas-module-catalog:stable",
		CatalogMirrors: []string{
			"oci://docker.cnb.cool/anas.dev/anas/anas-module-catalog:stable",
		},
		Repository: "oci://ghcr.io/anas-project/anas-module-{name}",
		Mirrors: []string{
			"oci://docker.cnb.cool/anas.dev/anas/anas-module-{name}",
		},
	},
	{
		Name:    OfficialCN,
		Catalog: "oci://docker.cnb.cool/anas.dev/anas/anas-module-catalog:stable",
		CatalogMirrors: []string{
			"oci://ghcr.io/anas-project/anas-module-catalog:stable",
		},
		Repository: "oci://docker.cnb.cool/anas.dev/anas/anas-module-{name}",
		Mirrors: []string{
			"oci://ghcr.io/anas-project/anas-module-{name}",
		},
		ChineseDefaults: true,
	},
}

// NormalizeName accepts cn as the short user-facing spelling while keeping one
// canonical value in normalized configuration and lock records.
func NormalizeName(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "cn" {
		return OfficialCN
	}
	return name
}

func DefaultName(raw string) string {
	if name := NormalizeName(raw); name != "" {
		return name
	}
	return Official
}

func Builtins() []Profile {
	out := make([]Profile, len(builtins))
	copy(out, builtins)
	for i := range out {
		out[i].CatalogMirrors = append([]string(nil), out[i].CatalogMirrors...)
		out[i].Mirrors = append([]string(nil), out[i].Mirrors...)
	}
	return out
}

func LookupBuiltin(raw string) (Profile, bool) {
	name := DefaultName(raw)
	for _, profile := range builtins {
		if profile.Name == name {
			profile.CatalogMirrors = append([]string(nil), profile.CatalogMirrors...)
			profile.Mirrors = append([]string(nil), profile.Mirrors...)
			return profile, true
		}
	}
	return Profile{}, false
}

func UsesChineseDefaults(raw string) bool {
	profile, ok := LookupBuiltin(raw)
	return ok && profile.ChineseDefaults
}
