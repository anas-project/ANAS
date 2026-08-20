package runner

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestBundledModuleParametersDeclareTypes(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(reg))
	for name := range reg {
		names = append(names, name)
	}
	sort.Strings(names)

	parameters := 0
	for _, name := range names {
		mod := reg[name]
		parameters += len(mod.Parameters)
		for _, parameter := range mod.Parameters {
			spec, ok := mod.Types[parameter]
			if !ok || !spec.Declared() {
				t.Errorf("%s.%s has no explicit config.types declaration", name, parameter)
			}
		}
	}

	if got, want := len(reg), 18; got != want {
		t.Errorf("bundled module count = %d, want %d", got, want)
	}
	if got, want := parameters, 125; got != want {
		t.Errorf("bundled module parameter count = %d, want %d", got, want)
	}
}

func TestBundledModuleDefaultsMatchDeclaredTypes(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	defaults := 0
	for name, mod := range reg {
		for _, parameter := range mod.Parameters {
			key := parameterEnvKey(name, parameter, reg)
			value, ok := mod.Defaults[key]
			if !ok {
				continue
			}
			defaults++
			if err := validateParameterValue(name, parameter, value, reg); err != nil {
				t.Errorf("%s default %q is invalid: %v", name+"."+parameter, value, err)
			}
		}
	}

	if got, want := defaults, 106; got != want {
		t.Errorf("bundled module default count = %d, want %d", got, want)
	}
}
