package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/compose"
	"gopkg.in/yaml.v3"
)

func relationalDatabaseSpecForTest() map[string]any {
	return map[string]any{
		"name": "photos", "principal": "photos",
		"credential": map[string]any{"policy": "generated"}, "deletion_policy": "retain",
	}
}

func relationalDatabaseAppForTest(spec map[string]any, iface string) *app {
	return &app{
		order: []string{"photos"},
		reg: map[string]Module{"photos": {Name: "photos", Resources: []ResourceRequirement{{
			ID: "database", Contract: "relational_database", Spec: spec,
		}}}},
		contracts: map[string]Contract{"relational_database": {Name: "relational_database", Version: "1.0.0"}},
		resolvedBindings: map[string]map[string]string{"photos": {
			"relational_database": iface, "relational_database.interface": iface,
		}},
		env: map[string]string{}, secrets: &secretStore{values: map[string]string{}},
	}
}

func TestRelationalDatabaseAcceptsOrdinaryInterfacesAndExtensionNames(t *testing.T) {
	for _, iface := range []string{"postgres", "mariadb"} {
		a := relationalDatabaseAppForTest(relationalDatabaseSpecForTest(), iface)
		if err := a.materializeResourceSecrets(); err != nil || len(a.resourceRequests) != 1 {
			t.Fatalf("ordinary %s resource failed: %v", iface, err)
		}
	}
	var spec map[string]any
	if err := yaml.Unmarshal([]byte(`name: photos
principal: photos
credential: {policy: generated}
deletion_policy: retain
postgres: {extensions: [vector, earthdistance]}
`), &spec); err != nil {
		t.Fatal(err)
	}
	a := relationalDatabaseAppForTest(spec, "postgres")
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	first := a.resourceRequests[0]
	if err := a.materializeResourceSecrets(); err != nil || a.resourceRequests[0].Credential != first.Credential {
		t.Fatal("repeat materialization changed a database credential", err)
	}
	names, err := validateRelationalDatabaseSpec("photos", "database", "postgres", a.resourceRequests[0].Spec)
	if err != nil || !reflect.DeepEqual(names, []string{"vector", "earthdistance"}) {
		t.Fatalf("extension declaration changed: %v, %v", names, err)
	}
}

func TestRelationalDatabaseRejectsInvalidSpecBeforeSecrets(t *testing.T) {
	tests := []struct {
		name   string
		iface  string
		change func(map[string]any)
	}{
		{"unknown root field", "postgres", func(s map[string]any) { s["server_version"] = "18" }},
		{"unknown credential field", "postgres", func(s map[string]any) { s["credential"].(map[string]any)["password"] = "value" }},
		{"missing credential", "postgres", func(s map[string]any) { delete(s, "credential") }},
		{"empty database", "postgres", func(s map[string]any) { s["name"] = "" }},
		{"invalid principal", "postgres", func(s map[string]any) { s["principal"] = "admin; --" }},
		{"invalid deletion policy", "postgres", func(s map[string]any) { s["deletion_policy"] = "purge" }},
		{"null postgres block", "postgres", func(s map[string]any) { s["postgres"] = nil }},
		{"empty postgres block", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{} }},
		{"unknown postgres field", "postgres", func(s map[string]any) {
			s["postgres"] = map[string]any{"extensions": []any{"vector"}, "preload": "vector"}
		}},
		{"extensions missing", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{"versions": []any{"1"}} }},
		{"empty extensions", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{"extensions": []any{}} }},
		{"scalar extensions", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{"extensions": "vector"} }},
		{"nonstring extension", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{"extensions": []any{1}} }},
		{"version object", "postgres", func(s map[string]any) {
			s["postgres"] = map[string]any{"extensions": []any{map[string]any{"name": "vector", "version": "0.8.2"}}}
		}},
		{"duplicate extensions", "postgres", func(s map[string]any) { s["postgres"] = map[string]any{"extensions": []any{"vector", "vector"}} }},
		{"postgres on mariadb", "mariadb", func(s map[string]any) { s["postgres"] = map[string]any{"extensions": []any{"vector"}} }},
		{"empty postgres on mariadb", "mariadb", func(s map[string]any) { s["postgres"] = map[string]any{} }},
		{"invalid interface", "sqlite", func(map[string]any) {}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := relationalDatabaseSpecForTest()
			test.change(spec)
			a := relationalDatabaseAppForTest(spec, test.iface)
			if err := a.materializeResourceSecrets(); err == nil {
				t.Fatal("accepted invalid relational_database request")
			}
			if len(a.secrets.values) != 0 || len(a.resourceRequests) != 0 {
				t.Fatal("invalid request minted a credential or froze a resource")
			}
		})
	}
	for _, name := range []string{"", "Vector", "_vector", "../vector", "https://example.test/vector", "vector,earthdistance", "vector; DROP DATABASE photos", " vector", strings.Repeat("v", 64)} {
		t.Run("invalid name "+name, func(t *testing.T) {
			spec := relationalDatabaseSpecForTest()
			spec["postgres"] = map[string]any{"extensions": []string{name}}
			if _, err := validateRelationalDatabaseSpec("photos", "database", "postgres", spec); err == nil {
				t.Fatal("accepted invalid extension name")
			}
		})
	}
}

func TestRelationalDatabaseProjectionClearsInheritedExtensionList(t *testing.T) {
	env := map[string]string{"ANAS_RESOURCE_POSTGRES_EXTENSIONS": "inherited"}
	request := ResourceRequest{Consumer: "photos", ID: "database", Interface: "postgres", Spec: relationalDatabaseSpecForTest(), Credential: "secret-value"}
	if err := projectRelationalDatabaseProviderEnv(env, request); err != nil {
		t.Fatal(err)
	}
	if env["ANAS_RESOURCE_POSTGRES_EXTENSIONS"] != "" || env["ANAS_RESOURCE_PASSWORD"] != request.Credential {
		t.Fatal("ordinary request inherited extensions or lost the credential")
	}
	request.Spec["postgres"] = map[string]any{"extensions": []string{"vector", "earthdistance"}}
	if err := projectRelationalDatabaseProviderEnv(env, request); err != nil || env["ANAS_RESOURCE_POSTGRES_EXTENSIONS"] != "vector,earthdistance" {
		t.Fatal("extension projection lost request order", err)
	}
}

func TestRelationalDatabaseEnsureProjectsExtensionsAndRejectsFailedReadiness(t *testing.T) {
	previous := inspectComposeProjectOwners
	t.Cleanup(func() { inspectComposeProjectOwners = previous })
	inspectComposeProjectOwners = func(string) ([]string, error) { return nil, nil }
	for _, succeeds := range []bool{true, false} {
		t.Run(map[bool]string{true: "ready", false: "provider failed"}[succeeds], func(t *testing.T) {
			spec := relationalDatabaseSpecForTest()
			spec["postgres"] = map[string]any{"extensions": []any{"vector", "earthdistance"}}
			a := relationalDatabaseAppForTest(spec, "postgres")
			a.base = t.TempDir()
			modules := t.TempDir()
			providerDir := filepath.Join(modules, "postgres")
			if err := os.MkdirAll(providerDir, 0700); err != nil {
				t.Fatal(err)
			}
			a.reg["postgres"] = Module{Name: "postgres", ComposeFile: "docker-compose.yml", ContractProviders: []ContractProvider{{
				Name: "relational_database", Interface: "postgres", Operations: map[string]ProviderOperation{
					"ensure": {Runtime: "compose_run", Service: "provision", Command: []string{"ensure"}},
				},
			}}}
			if err := a.materializeResourceSecrets(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(providerDir, ".env"), []byte("POSTGRES_HOST=postgres\nPOSTGRES_PORT=5432\nPOSTGRES_NETWORK_NAME=anas-postgres\n"), 0600); err != nil {
				t.Fatal(err)
			}
			script := filepath.Join(t.TempDir(), "compose-probe")
			body := `#!/bin/sh
set -eu
[ "$ANAS_RESOURCE_DATABASE" = photos ]
[ "$ANAS_RESOURCE_USERNAME" = photos ]
[ -n "$ANAS_RESOURCE_PASSWORD" ]
[ "$ANAS_RESOURCE_POSTGRES_EXTENSIONS" = vector,earthdistance ]
`
			if !succeeds {
				body += "exit 1\n"
			}
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			a.compose = compose.CLI{Bin: []string{script}}
			err := a.ensureResourcesFor("photos", modules)
			statePath := filepath.Join(a.base, "state", "resources", "photos.database.yml")
			if !succeeds {
				if err == nil || exists(statePath) {
					t.Fatal("failed Provider saved ready state", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var state resourceState
			if err := readYAML(statePath, &state); err != nil || state.Status != "ready" || state.Actual.PasswordSecret != a.resourceRequests[0].SecretKey {
				t.Fatal("successful Provider lost resource state or secret reference", err)
			}
		})
	}
}
