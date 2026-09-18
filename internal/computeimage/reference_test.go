package computeimage

import (
	"encoding/json"
	"strings"
	"testing"
)

func named() map[string]any {
	return map[string]any{"catalog": "anas", "name": "forgejo-runner", "revision": "r3"}
}
func entry(arch, iface, digest string) Entry {
	return Entry{Catalog: "anas", Name: "forgejo-runner", Revision: "r3", Target: Target{arch, iface}, Fingerprint: strings.Repeat(digest, 64), RecipeDigest: strings.Repeat("f", 64)}
}

func TestParseRejectsOldFormatsAndMalformedObjects(t *testing.T) {
	for name, value := range map[string]any{
		"scalar":            strings.Repeat("a", 64),
		"old array":         []any{strings.Repeat("a", 64)},
		"prefixed":          []any{"anas:forgejo-runner@r3"},
		"prefixed digest":   []any{"fingerprint:" + strings.Repeat("a", 64)},
		"empty list":        []any{},
		"empty object":      []any{map[string]any{}},
		"mixed":             []any{map[string]any{"catalog": "anas", "name": "runner", "revision": "r1", "fingerprint": strings.Repeat("a", 64)}},
		"empty extra field": []any{map[string]any{"fingerprint": strings.Repeat("a", 64), "catalog": ""}},
		"unknown field":     []any{map[string]any{"fingerprint": strings.Repeat("a", 64), "url": "secret-input"}},
		"null":              []any{map[string]any{"fingerprint": nil}},
		"short digest":      []any{map[string]any{"fingerprint": "abc"}},
		"uppercase digest":  []any{map[string]any{"fingerprint": strings.Repeat("A", 64)}},
		"missing revision":  []any{map[string]any{"catalog": "anas", "name": "runner"}},
		"unknown catalog":   []any{map[string]any{"catalog": "remote", "name": "runner", "revision": "r1"}},
		"path":              []any{map[string]any{"catalog": "anas", "name": "../runner", "revision": "r1"}},
		"too long":          []any{map[string]any{"catalog": "anas", "name": strings.Repeat("x", 64), "revision": "r1"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(value)
			if err == nil {
				t.Fatal("invalid declaration accepted")
			}
			if strings.Contains(err.Error(), "secret-input") {
				t.Fatal("error echoes input")
			}
		})
	}
}

func TestResolveBothFormsAndExactTargets(t *testing.T) {
	refs, err := Parse([]any{named(), map[string]any{"fingerprint": strings.Repeat("d", 64)}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewCatalog([]Entry{entry("amd64", "incus_vm", "a"), entry("arm64", "incus_container", "b")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		target Target
		want   string
	}{{Target{"amd64", "incus_vm"}, "a"}, {Target{"arm64", "incus_container"}, "b"}} {
		resolved, err := Resolve(refs, test.target, catalog)
		if err != nil {
			t.Fatal(err)
		}
		if resolved[0].Fingerprint != strings.Repeat(test.want, 64) || len(resolved[0].CatalogDigest) != 64 || resolved[0].RecipeDigest != strings.Repeat("f", 64) {
			t.Fatal("named image not pinned to matching catalog record")
		}
		if resolved[1].Fingerprint != strings.Repeat("d", 64) || resolved[1].CatalogDigest != "" {
			t.Fatal("direct fingerprint should not depend on catalog")
		}
	}
	for _, target := range []Target{{"arm64", "incus_vm"}, {"amd64", "incus_container"}, {"amd64", "auto"}, {"unknown", "incus_vm"}} {
		if _, err := Resolve(refs, target, catalog); err == nil {
			t.Fatal("target silently fell back")
		}
	}
	if _, err := Resolve(refs, Target{"amd64", "incus_vm"}, nil); err == nil {
		t.Fatal("missing catalog accepted")
	}
	if _, err := Resolve(refs[1:], Target{"amd64", "incus_vm"}, nil); err != nil {
		t.Fatal(err)
	}
	refs[0].Revision = "r4"
	if _, err := Resolve(refs, Target{"amd64", "incus_vm"}, catalog); err == nil {
		t.Fatal("unknown revision accepted")
	}
}

func TestCatalogRejectsDuplicatesAndChangedHistory(t *testing.T) {
	original := entry("amd64", "incus_vm", "a")
	previous, err := NewCatalog([]Entry{original}, nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Fingerprint = strings.Repeat("b", 64)
	recipeChanged := original
	recipeChanged.RecipeDigest = strings.Repeat("c", 64)
	for _, entries := range [][]Entry{{original, original}, {original, changed}, {changed}, {recipeChanged}, {}} {
		if _, err := NewCatalog(entries, previous); err == nil {
			t.Fatal("duplicate, changed or removed published key accepted")
		}
	}
	if _, err := NewCatalog([]Entry{original, entry("arm64", "incus_vm", "b")}, previous); err != nil {
		t.Fatal(err)
	}
}

func TestFrozenResultSurvivesCatalogMutationAndRoundTrip(t *testing.T) {
	entries := []Entry{entry("amd64", "incus_vm", "a"), entry("arm64", "incus_vm", "b")}
	catalog, err := NewCatalog(entries, nil)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := NewCatalog([]Entry{entries[1], entries[0]}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.digest != reversed.digest {
		t.Fatal("catalog order changed digest")
	}
	refs, err := Parse([]any{named()})
	if err != nil {
		t.Fatal(err)
	}
	first, err := Resolve(refs, Target{"amd64", "incus_vm"}, catalog)
	if err != nil {
		t.Fatal(err)
	}
	entries[0].Fingerprint = strings.Repeat("c", 64)
	refs[0].Name = "other"
	again, err := Resolve([]Reference{first[0].Reference}, first[0].Target, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if again[0] != first[0] {
		t.Fatal("caller mutation changed frozen resolution")
	}
	body, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var restored []Resolution
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0] != first[0] {
		t.Fatal("freeze round trip lost identity or digest")
	}
}

func TestResolveRevalidatesConstructedReferences(t *testing.T) {
	for _, ref := range []Reference{{}, {Fingerprint: strings.Repeat("a", 64), Catalog: "anas"}, {Fingerprint: "invalid"}} {
		if _, err := Resolve([]Reference{ref}, Target{"amd64", "incus_vm"}, nil); err == nil {
			t.Fatal("constructed reference bypassed validation")
		}
	}
}
