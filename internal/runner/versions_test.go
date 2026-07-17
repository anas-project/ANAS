package runner

import (
	"path/filepath"
	"testing"
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

func TestValidateUpgradeRejectsUnsupportedSource(t *testing.T) {
	mod := Module{Name: "example", Version: "1.5.2", UpgradeFrom: ">=1.0.1 <=1.5.1"}
	if err := validateUpgrade(mod, "1.5.1"); err != nil {
		t.Fatal(err)
	}
	if err := validateUpgrade(mod, "0.9.1"); err == nil {
		t.Fatal("expected unsupported source version error")
	}
	if err := validateUpgrade(mod, "2.0.1"); err == nil {
		t.Fatal("expected downgrade error")
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

func TestCaskLockPersistsResolvedBindings(t *testing.T) {
	base := filepath.Join(t.TempDir(), "runtime")
	lock := &caskLock{
		APIVersion: "anas.dev/v1",
		Casks:      map[string]caskLockRecord{"nextcloud": {Version: "30.0.1"}},
		Bindings:   map[string]map[string]string{"nextcloud": {"relational_database": "mariadb"}},
	}
	if err := lock.Save(base); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadCaskLock(base)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Bindings["nextcloud"]["relational_database"]; got != "mariadb" {
		t.Fatalf("binding = %q, want mariadb", got)
	}
}
