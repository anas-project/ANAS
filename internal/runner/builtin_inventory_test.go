package runner

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"
)

func TestBuiltinInventoryMatchesApprovedSurface(t *testing.T) {
	inventory, err := LoadBuiltinInventory("../..")
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile("testdata/builtin-inventory.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var want BuiltinInventorySurface
	if err := json.Unmarshal(body, &want); err != nil {
		t.Fatal(err)
	}
	if got := inventory.Surface(); !reflect.DeepEqual(got, want) {
		t.Fatalf("built-in inventory surface changed\n got: %#v\nwant: %#v\nrun: go run ./cmd/gen-module-docs", got, want)
	}
}

func TestBuiltinInventoryIsSortedAndSummaryIsDerived(t *testing.T) {
	inventory, err := LoadBuiltinInventory("../..")
	if err != nil {
		t.Fatal(err)
	}
	if inventory.APIVersion != BuiltinInventoryAPIVersion {
		t.Fatalf("api_version = %q, want %q", inventory.APIVersion, BuiltinInventoryAPIVersion)
	}
	if !sort.SliceIsSorted(inventory.Modules, func(i, j int) bool {
		return inventory.Modules[i].Name < inventory.Modules[j].Name
	}) {
		t.Fatal("Module inventory is not sorted by name")
	}
	if !sort.SliceIsSorted(inventory.Parameters, func(i, j int) bool {
		return inventory.Parameters[i].Path < inventory.Parameters[j].Path
	}) {
		t.Fatal("parameter inventory is not sorted by path")
	}
	for _, module := range inventory.Modules {
		if module.APIVersion == "" || module.Name == "" || module.Title == "" ||
			module.Version == "" || module.Revision < 1 || module.Status == "" ||
			module.Category == "" || module.RuntimeType == "" {
			t.Errorf("Module inventory entry is incomplete: %#v", module)
		}
		if got := inventory.Summary.ByOwner[module.Name]; got != module.ParameterCount {
			t.Errorf("%s parameter_count = %d, owner summary = %d", module.Name, module.ParameterCount, got)
		}
	}

	wantSummary := summarizeBuiltinInventory(inventory.Modules, inventory.Parameters)
	if !reflect.DeepEqual(inventory.Summary, wantSummary) {
		t.Fatalf("summary is not derived from the returned inventory\n got: %#v\nwant: %#v", inventory.Summary, wantSummary)
	}
	if got, want := inventory.Summary.GlobalParameterCount+inventory.Summary.ModuleParameterCount, len(inventory.Parameters); got != want {
		t.Errorf("global + Module parameters = %d, want inventory length %d", got, want)
	}
	if got, want := inventory.Summary.StructuredModuleParameterCount+inventory.Summary.BareEnvParameterCount, inventory.Summary.ModuleParameterCount; got != want {
		t.Errorf("structured + bare Module parameters = %d, want Module parameter count %d", got, want)
	}
}

func TestConfigParameterInventoryUsesBuiltinProjection(t *testing.T) {
	builtin, err := LoadBuiltinInventory("../..")
	if err != nil {
		t.Fatal(err)
	}
	parameters, err := LoadConfigParameterInventory("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(parameters, builtin.Parameters) {
		t.Fatal("compatibility parameter inventory differs from built-in inventory projection")
	}
}
