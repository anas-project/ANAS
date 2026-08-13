// Package dns holds the DNS platform registry: which vendor each engine
// (lego, ddns_go, ddns_updater) can address, under which provider code, and
// with which credential keys.
//
// The registry is data, not code, so that adding a vendor is a reviewable diff
// in one file rather than a new branch in three hooks. Module hooks cannot import
// this package -- they are self-contained programs shipped inside distributable
// bundles -- so a generator projects each engine's slice of the registry into
// its module, and a contract test fails when a projection drifts.
package dns

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Engine identifiers. These are module names, because the engine a credential
// belongs to and the module that receives it are the same thing.
const (
	EngineLego        = "lego"
	EngineDDNSGo      = "ddns_go"
	EngineDDNSUpdater = "ddns_updater"
)

// Credential roles map onto an engine's own configuration slots.
const (
	RoleID     = "id"
	RoleSecret = "secret"
	RoleExt    = "ext"
)

// Compatibility values describe whether two engines can be driven from one set
// of user-supplied secrets for a platform.
const (
	// CompatShared means both engines require the identical credential keys.
	CompatShared = "shared"
	// CompatSeparate means both engines support the platform but need
	// different credentials, so each is configured under its own prefix.
	CompatSeparate = "separate"
	// CompatUnsupported means at least one engine cannot address the platform.
	CompatUnsupported = "unsupported"
)

//go:embed providers.yml
var registrySource []byte

type Credential struct {
	Key      string `yaml:"key"`
	Role     string `yaml:"role"`
	Optional bool   `yaml:"optional"`
}

type EngineSupport struct {
	Provider    string       `yaml:"provider"`
	Credentials []Credential `yaml:"credentials"`
}

type Platform struct {
	Name    string                   `yaml:"name"`
	Title   string                   `yaml:"title"`
	Aliases []string                 `yaml:"aliases"`
	Engines map[string]EngineSupport `yaml:"engines"`
}

type Registry struct {
	Version   int        `yaml:"version"`
	Platforms []Platform `yaml:"platforms"`

	byName map[string]*Platform
}

// knownEngines is closed on purpose: an unrecognised engine key in the YAML is
// almost always a typo, and silently ignoring it would make a platform look
// unsupported for a engine that actually handles it.
var knownEngines = []string{EngineLego, EngineDDNSGo, EngineDDNSUpdater}

var knownRoles = []string{RoleID, RoleSecret, RoleExt}

// Engines lists the engine identifiers, which are also the module names that
// implement them. Callers use it to find which engines a deployment runs.
func Engines() []string {
	return append([]string{}, knownEngines...)
}

// Load parses the embedded registry. It is validated on every load rather than
// only in tests, so a malformed registry fails the run that uses it instead of
// producing a half-populated configuration.
func Load() (*Registry, error) {
	return parse(registrySource)
}

func parse(source []byte) (*Registry, error) {
	var reg Registry
	dec := yaml.NewDecoder(strings.NewReader(string(source)))
	dec.KnownFields(true)
	if err := dec.Decode(&reg); err != nil {
		return nil, fmt.Errorf("dns registry: %w", err)
	}
	if err := reg.index(); err != nil {
		return nil, err
	}
	return &reg, nil
}

func (r *Registry) index() error {
	r.byName = map[string]*Platform{}
	declared := map[string]bool{}
	for i := range r.Platforms {
		p := &r.Platforms[i]
		if p.Name == "" {
			return fmt.Errorf("dns registry: platform %d has no name", i)
		}
		if len(p.Engines) == 0 {
			return fmt.Errorf("dns registry: platform %q supports no engine", p.Name)
		}
		for _, name := range append([]string{p.Name}, p.Aliases...) {
			key := normalize(name)
			if existing, ok := r.byName[key]; ok && existing != p {
				return fmt.Errorf("dns registry: %q is claimed by both %s and %s", name, existing.Name, p.Name)
			}
			r.byName[key] = p
			declared[key] = true
		}
		for engine, support := range p.Engines {
			if !contains(knownEngines, engine) {
				return fmt.Errorf("dns registry: platform %q names unknown engine %q; known engines: %s",
					p.Name, engine, strings.Join(knownEngines, ", "))
			}
			if support.Provider == "" {
				return fmt.Errorf("dns registry: platform %q engine %q has no provider code", p.Name, engine)
			}
			if len(support.Credentials) == 0 {
				return fmt.Errorf("dns registry: platform %q engine %q declares no credentials", p.Name, engine)
			}
			seen := map[string]bool{}
			for _, cred := range support.Credentials {
				if cred.Key == "" {
					return fmt.Errorf("dns registry: platform %q engine %q has a credential without a key", p.Name, engine)
				}
				if !contains(knownRoles, cred.Role) {
					return fmt.Errorf("dns registry: platform %q engine %q credential %s has unknown role %q; known roles: %s",
						p.Name, engine, cred.Key, cred.Role, strings.Join(knownRoles, ", "))
				}
				if seen[cred.Key] {
					return fmt.Errorf("dns registry: platform %q engine %q repeats credential %s", p.Name, engine, cred.Key)
				}
				seen[cred.Key] = true
			}
		}
	}
	// Engine provider codes double as lookup names, because a user is at least
	// as likely to write the code they saw in ddns-go's interface (name_com,
	// nsone) as lego's canonical one. A declared name always wins; two
	// platforms auto-claiming the same code is a registry bug.
	for i := range r.Platforms {
		p := &r.Platforms[i]
		for _, support := range p.Engines {
			key := normalize(support.Provider)
			if declared[key] {
				continue
			}
			if existing, ok := r.byName[key]; ok && existing != p {
				return fmt.Errorf("dns registry: provider code %q is used by both %s and %s; give one of them an explicit alias",
					support.Provider, existing.Name, p.Name)
			}
			r.byName[key] = p
		}
	}
	return nil
}

// Lookup resolves a platform by canonical name or alias. The second return
// value reports whether the name was recognised at all, which callers turn
// into a configuration error naming the supported platforms.
func (r *Registry) Lookup(name string) (*Platform, bool) {
	p, ok := r.byName[normalize(name)]
	return p, ok
}

// Names lists every canonical platform name in a stable order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.Platforms))
	for _, p := range r.Platforms {
		out = append(out, p.Name)
	}
	sort.Strings(out)
	return out
}

// NamesFor lists the platforms one engine can address.
func (r *Registry) NamesFor(engine string) []string {
	out := []string{}
	for _, p := range r.Platforms {
		if _, ok := p.Engines[engine]; ok {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Supports reports whether an engine can address this platform.
func (p *Platform) Supports(engine string) bool {
	_, ok := p.Engines[engine]
	return ok
}

// RequiredKeys lists the credential keys an engine cannot work without.
func (p *Platform) RequiredKeys(engine string) []string {
	out := []string{}
	for _, cred := range p.Engines[engine].Credentials {
		if !cred.Optional {
			out = append(out, cred.Key)
		}
	}
	sort.Strings(out)
	return out
}

// AllKeys lists every credential key an engine reads, optional ones included.
func (p *Platform) AllKeys(engine string) []string {
	out := []string{}
	for _, cred := range p.Engines[engine].Credentials {
		out = append(out, cred.Key)
	}
	sort.Strings(out)
	return out
}

// Compatibility reports whether two engines can be driven from one set of
// secrets for this platform. It is derived from the credential keys rather
// than declared, so it cannot fall out of step with the keys it describes.
func (p *Platform) Compatibility(a, b string) string {
	if !p.Supports(a) || !p.Supports(b) {
		return CompatUnsupported
	}
	if equalStrings(p.AllKeys(a), p.AllKeys(b)) {
		return CompatShared
	}
	return CompatSeparate
}

// normalize accepts the spellings a user is likely to write in config: case is
// irrelevant, and hyphen and underscore are interchangeable because the engines
// themselves disagree (ddns-go writes name_com, lego writes namedotcom).
func normalize(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "-", "_")
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
