package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestResourceEnsureComposeRunIsNonInteractive(t *testing.T) {
	got := resourceEnsureComposeArgs("anas_postgres_provision", []string{"ensure"})
	want := []string{"run", "--rm", "--no-deps", "--no-TTY", "anas_postgres_provision", "ensure"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource ensure compose args = %q, want %q", got, want)
	}
}

func TestResourceSpecFromUsesResolvedModuleParameter(t *testing.T) {
	a := &app{
		order: []string{"nextcloud"},
		reg: map[string]Module{"nextcloud": {
			Name: "nextcloud", EnvPrefix: "NEXTCLOUD",
			Resources: []ResourceRequirement{{
				ID: "primary_database", Contract: "relational_database", Binding: "db_type",
				SpecFrom: map[string]string{"name": "db_name"},
				Spec: map[string]any{
					"principal": "nextcloud", "credential": map[string]any{"policy": "generated"},
					"deletion_policy": "retain",
				},
			}},
		}},
		env: map[string]string{"NEXTCLOUD_DB_NAME": "cloud_data"},
		resolvedBindings: map[string]map[string]string{
			"nextcloud": {"relational_database": "postgres", "relational_database.interface": "postgres"},
		},
		secrets: &secretStore{values: map[string]string{}},
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 1 || a.resourceRequests[0].Spec["name"] != "cloud_data" {
		t.Fatalf("resource requests = %+v", a.resourceRequests)
	}
	if a.resourceRequests[0].Spec["principal"] != "nextcloud" {
		t.Fatal("resource principal was not kept independent from the database name")
	}
}

func TestRemovedResourceIsRetainedWithoutProviderDeletion(t *testing.T) {
	base := t.TempDir()
	statePath := filepath.Join(base, "state", "resources", "nextcloud.primary_database.yml")
	state := resourceState{
		APIVersion: resourceStateAPIVersion, Consumer: "nextcloud", ResourceID: "primary_database",
		Contract: "relational_database", ContractVersion: "1.0.0", Provider: "postgres",
		Interface: "postgres", Status: "ready", DeletionPolicy: "retain",
	}
	if err := writeYAMLAtomic(statePath, state, 0600); err != nil {
		t.Fatal(err)
	}
	current := &deploymentManifest{Resources: []deploymentResource{{
		Consumer: "nextcloud", ID: "primary_database", Contract: "relational_database", Provider: "postgres",
	}}}
	if err := retainRemovedResources(base, current, &deploymentManifest{}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var got resourceState
	if err := yaml.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "retained" || got.Provider != "postgres" {
		t.Fatalf("resource state = %+v", got)
	}
}

func TestBundledDatabaseProvidersImplementEnsureAsOneShotServices(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	reg, err := loadRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := loadContractRegistry(filepath.Join(root, "modules"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{reg: reg, contracts: contracts}
	if err := a.validateContractRegistry(); err != nil {
		t.Fatal(err)
	}
	for module, iface := range map[string]string{"postgres": "postgres", "mariadb": "mariadb"} {
		provider, ok := reg[module].providedContract("relational_database", iface)
		if !ok {
			t.Fatalf("%s does not provide relational_database/%s", module, iface)
		}
		ensure, ok := provider.Operations["ensure"]
		if !ok || ensure.Runtime != "compose_run" || ensure.Service == "" {
			t.Fatalf("%s ensure operation = %+v", module, ensure)
		}
		if !contains(provider.OperationSvcs, ensure.Service) {
			t.Fatalf("%s ensure service is not excluded from normal module startup", module)
		}
	}
}
