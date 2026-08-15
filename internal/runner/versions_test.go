package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestVersionConstraintHyphenRange(t *testing.T) {
	constraint, err := parseVersionConstraint("1.0.1 - 1.5.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"1.0.1", "1.2.0", "1.5.1"} {
		v, err := parseSemver(version)
		if err != nil {
			t.Fatal(err)
		}
		if !constraint.Check(v) {
			t.Fatalf("%s should match range", version)
		}
	}
	for _, version := range []string{"0.9.1", "2.0.1"} {
		v, err := parseSemver(version)
		if err != nil {
			t.Fatal(err)
		}
		if constraint.Check(v) {
			t.Fatalf("%s should not match range", version)
		}
	}
}

func TestUpdateModuleLockDropsUnselectedResolution(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "module.yml"), []byte("name: selected\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{
		order: []string{"selected"},
		reg: map[string]Module{"selected": {
			Name: "selected", Version: "1.0.0", SourceDir: source,
		}},
		resolvedBindings: map[string]map[string]string{},
	}
	lock := &moduleLock{
		Modules:  map[string]moduleLockRecord{"removed": {Version: "1.0.0"}},
		IAM:      &moduleLockIAM{Provider: "removed"},
		Bindings: map[string]map[string]string{"removed": {"relational_database": "mariadb"}},
	}
	if err := a.updateModuleLock(lock, true); err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Modules["removed"]; ok {
		t.Fatal("removed module remained in updated lock")
	}
	if _, ok := lock.Modules["selected"]; !ok {
		t.Fatal("selected module is missing from updated lock")
	}
	if lock.IAM != nil || len(lock.Bindings) != 0 {
		t.Fatalf("stale resolution remained: IAM=%#v bindings=%#v", lock.IAM, lock.Bindings)
	}
}

func TestValidateUpgradeRejectsUnsupportedSource(t *testing.T) {
	mod := Module{Name: "example", Version: "1.5.2", UpgradeFrom: ">=1.0.1 <=1.5.1"}
	mod.Revision = 2
	if err := validateUpgrade(mod, "1.5.1", 1); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgrade(mod, "0.9.1", 1); err == nil {
		t.Fatal("expected unsupported source version error")
	}
	if err := validateUpgrade(mod, "2.0.1", 1); err == nil {
		t.Fatal("expected downgrade error")
	}
	if err := validateUpgrade(mod, "1.5.2", 3); err == nil {
		t.Fatal("expected revision downgrade error")
	}
}

func TestDependencyVersionConstraints(t *testing.T) {
	tests := []struct {
		name       string
		constraint string
		actual     string
		wantErr    bool
	}{
		{name: "caret allows same major", constraint: "^2.5.1", actual: "2.9.0"},
		{name: "caret rejects next major", constraint: "^2.5.1", actual: "3.0.0", wantErr: true},
		{name: "range allows inner", constraint: ">=15.0.0 <16.0.0", actual: "15.3.0"},
		{name: "range rejects outer", constraint: ">=15.0.0 <16.0.0", actual: "16.0.0", wantErr: true},
	}
	for _, tt := range tests {
		err := validateDependencyVersion("owner", Dependency{Name: "dep", Version: tt.constraint}, tt.actual)
		if tt.wantErr && err == nil {
			t.Fatalf("%s: expected error", tt.name)
		}
		if !tt.wantErr && err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
	}
}

func TestValidateVersionsEnforcesConfiguredExactRelease(t *testing.T) {
	selection := config.NewModuleSelection("selected")
	selected := selection.Values["selected"]
	selected.Version = "1.2.3-r2"
	selection.Values["selected"] = selected
	a := &app{
		cfg:   &config.File{Modules: selection},
		order: []string{"selected"},
		reg:   map[string]Module{"selected": {Name: "selected", Version: "1.2.3", Revision: 2}},
	}
	if err := a.validateVersions(&moduleLock{Modules: map[string]moduleLockRecord{}}); err != nil {
		t.Fatal(err)
	}
	a.reg["selected"] = Module{Name: "selected", Version: "1.2.3", Revision: 1}
	if err := a.validateVersions(&moduleLock{Modules: map[string]moduleLockRecord{}}); err == nil || !strings.Contains(err.Error(), "pinned to 1.2.3-r2") {
		t.Fatalf("configured release mismatch error = %v", err)
	}
}

func TestModuleLockPersistsResolvedBindings(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime")
	lock := &moduleLock{
		APIVersion: "anas.module-lock/v1",
		Modules:    map[string]moduleLockRecord{"nextcloud": {Version: "30.0.1", Revision: 2}},
		Bindings:   map[string]map[string]string{"nextcloud": {"relational_database": "mariadb"}},
	}
	if err := lock.Save(base); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadModuleLock(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Bindings["nextcloud"]["relational_database"]; got != "mariadb" {
		t.Fatalf("binding = %q, want mariadb", got)
	}
	if got := loaded.Modules["nextcloud"].Revision; got != 2 {
		t.Fatalf("revision = %d, want 2", got)
	}
}
