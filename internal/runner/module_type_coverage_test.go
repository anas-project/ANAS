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

	for _, name := range names {
		mod := reg[name]
		for _, parameter := range mod.Parameters {
			spec, ok := mod.Types[parameter]
			if !ok || !spec.Declared() {
				t.Errorf("%s.%s has no explicit config.types declaration", name, parameter)
			}
		}
	}
}

func TestBundledModuleDefaultsMatchDeclaredTypes(t *testing.T) {
	reg, err := loadRegistry(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	for name, mod := range reg {
		for _, parameter := range mod.Parameters {
			key := parameterEnvKey(name, parameter, reg)
			value, ok := mod.Defaults[key]
			if !ok {
				continue
			}
			if err := validateParameterValue(name, parameter, value, reg); err != nil {
				t.Errorf("%s default %q is invalid: %v", name+"."+parameter, value, err)
			}
		}
	}
}
