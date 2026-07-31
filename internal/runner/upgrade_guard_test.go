package runner

import (
	"testing"
)

func declared(versions ...string) *[]string {
	list := append([]string{}, versions...)
	return &list
}

// The distinction the whole guard rests on. An undeclared list and a
// declared-empty one differ on every input where the versions are not equal,
// and they differ in opposite directions: unknown blocks, empty permits.
func TestCaskDataVerdictUnsetIsNotEmpty(t *testing.T) {
	cases := []struct {
		name      string
		from, to  string
		unsetWant dataVerdict
		emptyWant dataVerdict
	}{
		{"same version", "30.0.1", "30.0.1", dataUnchanged, dataUnchanged},
		{"patch upgrade", "30.0.1", "30.0.2", dataUnknown, dataCompatible},
		{"major upgrade", "30.0.1", "31.0.0", dataUnknown, dataCompatible},
		{"downgrade", "31.0.0", "30.0.1", dataUnknown, dataCompatible},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := caskDataVerdict(tc.from, tc.to, nil); got != tc.unsetWant {
				t.Fatalf("unset: verdict = %v, want %v", got, tc.unsetWant)
			}
			if got, _ := caskDataVerdict(tc.from, tc.to, declared()); got != tc.emptyWant {
				t.Fatalf("declared empty: verdict = %v, want %v", got, tc.emptyWant)
			}
		})
	}
}

func TestCaskDataVerdict(t *testing.T) {
	breaks := declared("31.0.0")
	twoBreaks := declared("31.0.0", "33.0.0")
	cases := []struct {
		name        string
		from, to    string
		declared    *[]string
		want        dataVerdict
		wantCrossed string
	}{
		// The upper bound is inclusive: arriving at the breaking version is the
		// act that rewrites the format.
		{"arrives at the break", "30.0.1", "31.0.0", breaks, dataBreaking, "31.0.0"},
		{"stops just below", "30.0.1", "30.9.9", breaks, dataCompatible, ""},
		{"starts at the break", "31.0.0", "31.5.0", breaks, dataCompatible, ""},
		{"passes the break", "30.0.1", "32.0.0", breaks, dataBreaking, "31.0.0"},
		{"entirely above", "31.2.0", "32.0.0", breaks, dataCompatible, ""},
		{"entirely below", "29.0.0", "30.0.0", breaks, dataCompatible, ""},
		{"same version at the break", "31.0.0", "31.0.0", breaks, dataUnchanged, ""},
		// Reverse: the interval is symmetric, so a rollback across the same
		// point is caught by the same comparison.
		{"rolls back over the break", "31.0.0", "30.0.1", breaks, dataBreaking, "31.0.0"},
		{"rolls back below the break", "31.5.0", "31.0.0", breaks, dataCompatible, ""},
		// Several break points at once; the lowest is reported.
		{"crosses two breaks", "30.0.1", "33.0.0", twoBreaks, dataBreaking, "31.0.0"},
		{"crosses two breaks in reverse", "33.0.0", "30.0.1", twoBreaks, dataBreaking, "31.0.0"},
		{"crosses only the upper break", "31.0.0", "33.0.0", twoBreaks, dataBreaking, "33.0.0"},
		// Anything unparseable degrades to unknown, never to compatible.
		{"unparseable current", "not-a-version", "31.0.0", breaks, dataUnknown, ""},
		{"unparseable target", "30.0.1", "banana", breaks, dataUnknown, ""},
		{"unparseable declaration", "30.0.1", "32.0.0", declared("thirty-one"), dataUnknown, ""},
		{"unparseable but identical", "banana", "banana", nil, dataUnchanged, ""},
		// Semantically equal spellings are the same version, not a change.
		{"equivalent spellings", "30.0.0", "v30.0.0", nil, dataUnchanged, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, crossed := caskDataVerdict(tc.from, tc.to, tc.declared)
			if got != tc.want || crossed != tc.wantCrossed {
				t.Fatalf("verdict = %v/%q, want %v/%q", got, crossed, tc.want, tc.wantCrossed)
			}
		})
	}
}

// Only the release that broke the format can know it did, so the higher
// version's declaration decides — in both directions of travel.
func TestGoverningDataBreakingPicksTheHigherVersion(t *testing.T) {
	old := declared()
	recent := declared("31.0.0")
	if got := governingDataBreaking("30.0.1", old, "31.0.0", recent); got != recent {
		t.Fatal("upgrade must be judged by the target's declaration")
	}
	if got := governingDataBreaking("31.0.0", recent, "30.0.1", old); got != recent {
		t.Fatal("rollback must be judged by the deployed version's declaration")
	}
	// If the older cask.yml were consulted, this transition would read as
	// compatible and the rollback would be let through.
	from := deploymentCask{Version: "31.0.0", DataBreaking: recent}
	to := deploymentCask{Version: "30.0.1", DataBreaking: old}
	if got, at := caskTransitionVerdict(from, to); got != dataBreaking || at != "31.0.0" {
		t.Fatalf("transition verdict = %v/%q, want breaking at 31.0.0", got, at)
	}
}

// The frozen manifest has to preserve the nil/empty distinction across a YAML
// round trip, or the guard reads the opposite verdict off disk from the one it
// wrote.
func TestDeploymentCaskDataBreakingSurvivesYAML(t *testing.T) {
	path := t.TempDir() + "/deployment.yml"
	in := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: "x",
		Casks: map[string]deploymentCask{
			"undeclared": {Name: "undeclared", Version: "1.0.0"},
			"empty":      {Name: "empty", Version: "1.0.0", DataBreaking: declared()},
			"listed":     {Name: "listed", Version: "1.0.0", DataBreaking: declared("2.0.0")},
		},
	}
	if err := writeYAMLAtomic(path, in, 0600); err != nil {
		t.Fatal(err)
	}
	var out deploymentManifest
	if err := readYAML(path, &out); err != nil {
		t.Fatal(err)
	}
	if out.Casks["undeclared"].DataBreaking != nil {
		t.Fatal("an undeclared list came back declared")
	}
	empty := out.Casks["empty"].DataBreaking
	if empty == nil {
		t.Fatal("a declared-empty list came back undeclared; every rollback would block as unknown")
	}
	if len(*empty) != 0 {
		t.Fatalf("declared-empty list came back as %v", *empty)
	}
	listed := out.Casks["listed"].DataBreaking
	if listed == nil || len(*listed) != 1 || (*listed)[0] != "2.0.0" {
		t.Fatalf("declared list came back as %v", listed)
	}
}

func TestCloneStringListPointerKeepsTheDistinction(t *testing.T) {
	if cloneStringListPointer(nil) != nil {
		t.Fatal("nil must clone to nil")
	}
	empty := cloneStringListPointer(declared())
	if empty == nil || len(*empty) != 0 {
		t.Fatalf("declared-empty cloned to %v", empty)
	}
	source := declared("1.0.0")
	clone := cloneStringListPointer(source)
	(*clone)[0] = "2.0.0"
	if (*source)[0] != "1.0.0" {
		t.Fatal("the clone shares its backing array with the source")
	}
}
