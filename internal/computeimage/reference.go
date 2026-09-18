// Package computeimage validates structured compute image declarations and
// resolves trusted catalog entries to immutable image fingerprints. It performs
// no network access or image import; catalog trust is the caller's responsibility.
package computeimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

var (
	fingerprintPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	identifierPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
)

type Reference struct {
	Catalog     string `json:"catalog,omitempty" yaml:"catalog,omitempty"`
	Name        string `json:"name,omitempty" yaml:"name,omitempty"`
	Revision    string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"`
}

// Parse accepts the object array produced by decoding a resource spec. All
// string shorthand and unknown fields are rejected, including empty extra keys.
func Parse(value any) ([]Reference, error) {
	entries, ok := value.([]any)
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("image_allowlist must be a non-empty array of objects")
	}
	refs := make([]Reference, 0, len(entries))
	for i, entry := range entries {
		raw, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("image_allowlist[%d] must be an object", i)
		}
		ref := Reference{}
		for key, value := range raw {
			var target *string
			switch key {
			case "catalog":
				target = &ref.Catalog
			case "name":
				target = &ref.Name
			case "revision":
				target = &ref.Revision
			case "fingerprint":
				target = &ref.Fingerprint
			default:
				return nil, fmt.Errorf("image_allowlist[%d] contains an unknown field", i)
			}
			str, ok := value.(string)
			if !ok || str == "" {
				return nil, fmt.Errorf("image_allowlist[%d] fields must be non-empty strings", i)
			}
			*target = str
		}
		if err := ref.validate(); err != nil {
			return nil, fmt.Errorf("image_allowlist[%d]: %w", i, err)
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func (r Reference) validate() error {
	if r.Fingerprint != "" {
		if r.Catalog != "" || r.Name != "" || r.Revision != "" {
			return fmt.Errorf("fingerprint and catalog fields are mutually exclusive")
		}
		if !fingerprintPattern.MatchString(r.Fingerprint) {
			return fmt.Errorf("fingerprint must be a lowercase SHA-256 digest")
		}
		return nil
	}
	if r.Catalog != "anas" {
		return fmt.Errorf("catalog must be anas")
	}
	if !identifierPattern.MatchString(r.Name) || !identifierPattern.MatchString(r.Revision) {
		return fmt.Errorf("name and revision must contain 1-63 lowercase letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	return nil
}

type Target struct {
	Architecture string `json:"architecture" yaml:"architecture"`
	Interface    string `json:"interface" yaml:"interface"`
}

func (t Target) validate() error {
	if t.Architecture != "amd64" && t.Architecture != "arm64" {
		return fmt.Errorf("image architecture must be amd64 or arm64")
	}
	if t.Interface != "incus_vm" && t.Interface != "incus_container" {
		return fmt.Errorf("image interface must be incus_vm or incus_container")
	}
	return nil
}

// Entry is a record from a trusted release catalog, not a consumer-supplied URL.
type Entry struct {
	Catalog      string `json:"catalog" yaml:"catalog"`
	Name         string `json:"name" yaml:"name"`
	Revision     string `json:"revision" yaml:"revision"`
	Target       `yaml:",inline"`
	Fingerprint  string `json:"fingerprint" yaml:"fingerprint"`
	RecipeDigest string `json:"recipe_digest" yaml:"recipe_digest"`
}

type key struct{ Catalog, Name, Revision, Architecture, Interface string }

func (e Entry) key() key { return key{e.Catalog, e.Name, e.Revision, e.Architecture, e.Interface} }

// Catalog owns a copy of its entries. Neither caller mutation nor a later
// catalog changes a resolution made against this snapshot.
type Catalog struct {
	entries map[key]Entry
	digest  string
}

// NewCatalog validates a snapshot. Passing the previous trusted snapshot also
// enforces append-only history: old version keys may not disappear or change.
func NewCatalog(entries []Entry, previous *Catalog) (*Catalog, error) {
	c := &Catalog{entries: make(map[key]Entry, len(entries))}
	canonical := append([]Entry{}, entries...)
	for i, entry := range canonical {
		if err := (Reference{Catalog: entry.Catalog, Name: entry.Name, Revision: entry.Revision}).validate(); err != nil {
			return nil, fmt.Errorf("catalog entry %d: %w", i, err)
		}
		if err := entry.Target.validate(); err != nil {
			return nil, fmt.Errorf("catalog entry %d: %w", i, err)
		}
		if !fingerprintPattern.MatchString(entry.Fingerprint) || !fingerprintPattern.MatchString(entry.RecipeDigest) {
			return nil, fmt.Errorf("catalog entry %d requires image and recipe SHA-256 digests", i)
		}
		if _, exists := c.entries[entry.key()]; exists {
			return nil, fmt.Errorf("catalog entry %d duplicates a version/target key", i)
		}
		c.entries[entry.key()] = entry
	}
	if previous != nil {
		for k, old := range previous.entries {
			if current, exists := c.entries[k]; !exists || current != old {
				return nil, fmt.Errorf("catalog update removes or changes a published version/target key")
			}
		}
	}
	sort.Slice(canonical, func(i, j int) bool {
		a, b := canonical[i], canonical[j]
		av, bv := []string{a.Catalog, a.Name, a.Revision, a.Architecture, a.Interface}, []string{b.Catalog, b.Name, b.Revision, b.Architecture, b.Interface}
		for n := range av {
			if av[n] != bv[n] {
				return av[n] < bv[n]
			}
		}
		return false
	})
	body, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	c.digest = hex.EncodeToString(digest[:])
	return c, nil
}

// Resolution is a value suitable for freezing in a deployment. It does not
// assert that the image has been imported or passed runtime verification.
type Resolution struct {
	Reference     Reference `json:"reference" yaml:"reference"`
	Target        Target    `json:"target" yaml:"target"`
	Fingerprint   string    `json:"fingerprint" yaml:"fingerprint"`
	CatalogDigest string    `json:"catalog_digest,omitempty" yaml:"catalog_digest,omitempty"`
	RecipeDigest  string    `json:"recipe_digest,omitempty" yaml:"recipe_digest,omitempty"`
}

func Resolve(refs []Reference, target Target, catalog *Catalog) ([]Resolution, error) {
	if len(refs) == 0 {
		return nil, fmt.Errorf("image references must not be empty")
	}
	if err := target.validate(); err != nil {
		return nil, err
	}
	out := make([]Resolution, 0, len(refs))
	for i, ref := range refs {
		if err := ref.validate(); err != nil {
			return nil, fmt.Errorf("image reference %d: %w", i, err)
		}
		result := Resolution{Reference: ref, Target: target, Fingerprint: ref.Fingerprint}
		if ref.Fingerprint == "" {
			if catalog == nil {
				return nil, fmt.Errorf("image reference %d requires a trusted catalog", i)
			}
			entry, ok := catalog.entries[key{ref.Catalog, ref.Name, ref.Revision, target.Architecture, target.Interface}]
			if !ok {
				return nil, fmt.Errorf("image reference %d has no catalog entry for its version and target", i)
			}
			result.Fingerprint, result.CatalogDigest, result.RecipeDigest = entry.Fingerprint, catalog.digest, entry.RecipeDigest
		}
		out = append(out, result)
	}
	return out, nil
}
