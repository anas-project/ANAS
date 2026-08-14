package runner

import (
	"strings"
	"testing"
)

func declared(versions ...string) *[]string {
	list := append([]string{}, versions...)
	return &list
}

// The distinction the whole guard rests on. An undeclared list and a
// declared-empty one differ on every input where the versions are not equal,
// and they differ in opposite directions: unknown blocks, empty permits.
func TestModuleDataVerdictUnsetIsNotEmpty(t *testing.T) {
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
			if got, _ := moduleDataVerdict(tc.from, tc.to, nil); got != tc.unsetWant {
				t.Fatalf("unset: verdict = %v, want %v", got, tc.unsetWant)
			}
			if got, _ := moduleDataVerdict(tc.from, tc.to, declared()); got != tc.emptyWant {
				t.Fatalf("declared empty: verdict = %v, want %v", got, tc.emptyWant)
			}
		})
	}
}

func TestModuleDataVerdict(t *testing.T) {
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
			got, crossed := moduleDataVerdict(tc.from, tc.to, tc.declared)
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
	// If the older module.yml were consulted, this transition would read as
	// compatible and the rollback would be let through.
	from := deploymentModule{Version: "31.0.0", DataBreaking: recent}
	to := deploymentModule{Version: "30.0.1", DataBreaking: old}
	if got, at := moduleTransitionVerdict(from, to); got != dataBreaking || at != "31.0.0" {
		t.Fatalf("transition verdict = %v/%q, want breaking at 31.0.0", got, at)
	}
}

func TestModuleTransitionVerdictComparesRevisionWithinVersion(t *testing.T) {
	empty := declared()
	from := deploymentModule{Version: "34.0.2", Revision: 1, DataBreaking: empty}
	to := deploymentModule{Version: "34.0.2", Revision: 2, DataBreaking: empty}
	if verdict, _ := moduleTransitionVerdict(from, to); verdict != dataCompatible {
		t.Fatalf("revision transition verdict = %v, want compatible", verdict)
	}
	to.DataBreaking = nil
	if verdict, _ := moduleTransitionVerdict(from, to); verdict != dataUnknown {
		t.Fatalf("undeclared revision transition verdict = %v, want unknown", verdict)
	}
}

func TestDeploymentRollbackVersionGuard(t *testing.T) {
	manifest := func(modules ...deploymentModule) *deploymentManifest {
		m := &deploymentManifest{Modules: map[string]deploymentModule{}}
		for _, c := range modules {
			m.Modules[c.Name] = c
		}
		return m
	}
	module := func(name, version string, breaks *[]string) deploymentModule {
		return deploymentModule{Name: name, Version: version, AppVersion: version, DataBreaking: breaks}
	}

	t.Run("identical versions are a config-only rollback", func(t *testing.T) {
		guard := deploymentRollbackVersionGuard(
			manifest(module("nextcloud", "30.0.1", nil)),
			manifest(module("nextcloud", "30.0.1", nil)))
		if len(guard.Blocked) != 0 || len(guard.Crossings) != 0 {
			t.Fatalf("a config-only rollback was guarded: %+v", guard)
		}
	})

	t.Run("declared and not crossing is permitted", func(t *testing.T) {
		guard := deploymentRollbackVersionGuard(
			manifest(module("nextcloud", "30.0.2", declared("31.0.0"))),
			manifest(module("nextcloud", "30.0.1", declared("31.0.0"))))
		if len(guard.Blocked) != 0 || len(guard.Crossings) != 0 {
			t.Fatalf("a declared non-breaking version rollback was guarded: %+v", guard)
		}
	})

	t.Run("crossing in reverse has no override", func(t *testing.T) {
		guard := deploymentRollbackVersionGuard(
			manifest(module("nextcloud", "31.0.0", declared("31.0.0"))),
			manifest(module("nextcloud", "30.0.1", declared())))
		if len(guard.Blocked) != 0 {
			t.Fatalf("a crossing must be fatal, not merely blocked: %v", guard.Blocked)
		}
		err := guard.breakingError()
		if err == nil {
			t.Fatal("a reverse crossing produced no error")
		}
		want := []string{
			"cannot roll back nextcloud 31.0.0 -> 30.0.1: crosses data-breaking version 31.0.0",
			"data written by 31.0.0 cannot be read by 30.0.1",
			"to return to that state, restore a snapshot instead:",
			"anas snapshot list",
			"anas snapshot restore <id>",
		}
		for _, line := range want {
			if !strings.Contains(err.Error(), line) {
				t.Fatalf("message is missing %q:\n%s", line, err.Error())
			}
		}
		// The message has to be machine-branchable as a precondition, not a
		// generic failure, so a caller can tell it apart from --allow-risky.
		if ExitCode(err) != exitPrecondition {
			t.Fatalf("exit code = %d, want %d", ExitCode(err), exitPrecondition)
		}
	})

	t.Run("addition and removal stay blocked", func(t *testing.T) {
		guard := deploymentRollbackVersionGuard(
			manifest(module("nextcloud", "30.0.1", declared()), module("collabora", "1.0.0", declared())),
			manifest(module("nextcloud", "30.0.1", declared()), module("eturnal", "1.0.0", declared())))
		if len(guard.Blocked) != 2 {
			t.Fatalf("module addition and removal must stay blocked, got %v", guard.Blocked)
		}
		if len(guard.Crossings) != 0 {
			t.Fatalf("module addition and removal are not crossings: %+v", guard.Crossings)
		}
	})

	t.Run("one breaking module is enough", func(t *testing.T) {
		guard := deploymentRollbackVersionGuard(
			manifest(module("nextcloud", "31.0.0", declared("31.0.0")), module("traefik", "1.0.1", declared())),
			manifest(module("nextcloud", "30.0.1", declared()), module("traefik", "1.0.0", declared())))
		if len(guard.Crossings) != 1 || guard.Crossings[0].Module != "nextcloud" {
			t.Fatalf("crossings = %+v", guard.Crossings)
		}
	})
}

func TestDeploymentSnapshotTrigger(t *testing.T) {
	modules := func(version string, breaks *[]string) map[string]deploymentModule {
		return map[string]deploymentModule{
			"nextcloud": {Name: "nextcloud", Version: version, DataBreaking: breaks},
		}
	}
	setting := func(fingerprint, effect string) map[string]deploymentSetting {
		return map[string]deploymentSetting{
			"nextcloud.admin_password": {Fingerprint: fingerprint, Effect: effect, Apply: "rotate"},
		}
	}

	cases := []struct {
		name            string
		current, target *deploymentManifest
		want            string
	}{
		{
			name:    "routine apply does not snapshot",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "container_recreate")},
			target:  &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("new", "container_recreate")},
			want:    "",
		},
		{
			name:    "reconcile does not snapshot",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "reconcile")},
			target:  &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("new", "reconcile")},
			want:    "",
		},
		{
			name:    "a non-breaking upgrade does not snapshot",
			current: &deploymentManifest{Modules: modules("30.0.1", declared("31.0.0"))},
			target:  &deploymentManifest{Modules: modules("30.0.2", declared("31.0.0"))},
			want:    "",
		},
		{
			name:    "an undeclared upgrade does not snapshot",
			current: &deploymentManifest{Modules: modules("30.0.1", nil)},
			target:  &deploymentManifest{Modules: modules("31.0.0", nil)},
			want:    "",
		},
		{
			name:    "a breaking upgrade snapshots",
			current: &deploymentManifest{Modules: modules("30.0.1", declared())},
			target:  &deploymentManifest{Modules: modules("31.0.0", declared("31.0.0"))},
			want:    snapshotReasonModuleUpgradeBreaking,
		},
		{
			name:    "data_migrate snapshots",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "data_migrate")},
			target:  &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("new", "data_migrate")},
			want:    snapshotReasonSettingDataMigrate,
		},
		{
			name:    "credential_rotate snapshots",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "credential_rotate")},
			target:  &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("new", "credential_rotate")},
			want:    snapshotReasonSettingDataMigrate,
		},
		{
			name:    "an explicitly overridden immutable change snapshots",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "immutable")},
			target:  &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("new", "immutable")},
			want:    snapshotReasonSettingDataMigrate,
		},
		{
			name:    "the upgrade reason wins over the setting reason",
			current: &deploymentManifest{Modules: modules("30.0.1", declared()), Settings: setting("old", "data_migrate")},
			target:  &deploymentManifest{Modules: modules("31.0.0", declared("31.0.0")), Settings: setting("new", "data_migrate")},
			want:    snapshotReasonModuleUpgradeBreaking,
		},
		{
			name:    "the first apply has no data to protect",
			current: nil,
			target:  &deploymentManifest{Modules: modules("31.0.0", declared("31.0.0"))},
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := deploymentSnapshotTrigger(tc.current, tc.target)
			if tc.want == "" {
				if got != nil {
					t.Fatalf("expected no snapshot, got %s (%s)", got.reason, got.detail)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected reason %s, got no snapshot", tc.want)
			}
			if got.reason != tc.want {
				t.Fatalf("reason = %s, want %s", got.reason, tc.want)
			}
			if !validSnapshotReason(got.reason) {
				t.Fatalf("reason %s is not in the contract's enumeration", got.reason)
			}
			if got.detail == "" {
				t.Fatal("the trigger has to say why, or the warning it prints is empty")
			}
		})
	}
}

// A module that appears in the target but not in the current deployment has no
// prior data, so there is nothing its upgrade could break.
func TestDeploymentSnapshotTriggerIgnoresAddedModules(t *testing.T) {
	current := &deploymentManifest{Modules: map[string]deploymentModule{}}
	target := &deploymentManifest{Modules: map[string]deploymentModule{
		"nextcloud": {Name: "nextcloud", Version: "31.0.0", DataBreaking: declared("31.0.0")},
	}}
	if got := deploymentSnapshotTrigger(current, target); got != nil {
		t.Fatalf("adding a module triggered %s", got.reason)
	}
}

// The frozen manifest has to preserve the nil/empty distinction across a YAML
// round trip, or the guard reads the opposite verdict off disk from the one it
// wrote.
func TestDeploymentModuleDataBreakingSurvivesYAML(t *testing.T) {
	path := t.TempDir() + "/deployment.yml"
	in := &deploymentManifest{
		APIVersion: deploymentAPIVersion, ID: "x",
		Modules: map[string]deploymentModule{
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
	if out.Modules["undeclared"].DataBreaking != nil {
		t.Fatal("an undeclared list came back declared")
	}
	empty := out.Modules["empty"].DataBreaking
	if empty == nil {
		t.Fatal("a declared-empty list came back undeclared; every rollback would block as unknown")
	}
	if len(*empty) != 0 {
		t.Fatalf("declared-empty list came back as %v", *empty)
	}
	listed := out.Modules["listed"].DataBreaking
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
