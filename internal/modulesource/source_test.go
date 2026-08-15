package modulesource

import "testing"

func TestBuiltinsKeepOfficialContentIdentityAcrossMirrors(t *testing.T) {
	official, ok := LookupBuiltin(Official)
	if !ok {
		t.Fatal("official source is missing")
	}
	cn, ok := LookupBuiltin("cn")
	if !ok {
		t.Fatal("cn source is missing")
	}
	if official.ChineseDefaults {
		t.Fatal("global source unexpectedly enables Chinese runtime defaults")
	}
	if !cn.ChineseDefaults || cn.Name != OfficialCN {
		t.Fatalf("cn source = %#v", cn)
	}
	if len(official.Mirrors) != 1 || official.Mirrors[0] != cn.Repository {
		t.Fatalf("official mirrors = %#v, cn repository = %q", official.Mirrors, cn.Repository)
	}
	if len(cn.Mirrors) != 1 || cn.Mirrors[0] != official.Repository {
		t.Fatalf("cn mirrors = %#v, official repository = %q", cn.Mirrors, official.Repository)
	}
	if len(official.CatalogMirrors) != 1 || official.CatalogMirrors[0] != cn.Catalog {
		t.Fatalf("official catalog mirrors = %#v, cn catalog = %q", official.CatalogMirrors, cn.Catalog)
	}
	if len(cn.CatalogMirrors) != 1 || cn.CatalogMirrors[0] != official.Catalog {
		t.Fatalf("cn catalog mirrors = %#v, official catalog = %q", cn.CatalogMirrors, official.Catalog)
	}
}

func TestDefaultName(t *testing.T) {
	for input, want := range map[string]string{
		"":            Official,
		"  OFFICIAL ": Official,
		"cn":          OfficialCN,
		"enterprise":  "enterprise",
	} {
		if got := DefaultName(input); got != want {
			t.Errorf("DefaultName(%q) = %q, want %q", input, got, want)
		}
	}
}
