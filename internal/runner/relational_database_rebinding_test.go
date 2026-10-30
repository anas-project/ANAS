package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseEngineRebindingFailsBeforeSecrets(t *testing.T) {
	for _, source := range []string{"ready-resource", "retained-resource", "active-deployment"} {
		t.Run(source, func(t *testing.T) {
			a := relationalDatabaseAppForTest(relationalDatabaseSpecForTest(), "postgres")
			a.base = t.TempDir()
			if source == "active-deployment" {
				id := "20261030T000000Z-00000001"
				manifest := deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, Resources: []deploymentResource{{Consumer: "photos", ID: "database", Contract: "relational_database", Interface: "mariadb", Provider: "mariadb"}}}
				if err := writeYAMLAtomic(filepath.Join(a.base, "deployments", id, "deployment.yml"), manifest, 0600); err != nil {
					t.Fatal(err)
				}
				if err := saveActiveState(a.base, &activeDeploymentState{ActiveDeployment: id}); err != nil {
					t.Fatal(err)
				}
			} else {
				status := strings.TrimSuffix(source, "-resource")
				state := resourceState{APIVersion: resourceStateAPIVersion, Consumer: "photos", ResourceID: "database", Contract: "relational_database", Interface: "mariadb", Provider: "mariadb", Status: status}
				if err := writeYAMLAtomic(filepath.Join(a.base, "state", "resources", "photos.database.yml"), state, 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := a.materializeResourceSecrets()
			if err == nil || !strings.Contains(err.Error(), "database engine change") {
				t.Fatalf("cross-engine rebinding accepted: %v", err)
			}
			if len(a.secrets.values) != 0 || len(a.resourceRequests) != 0 {
				t.Fatal("rejected rebinding generated credentials or prepared a Provider write")
			}
			a.resourceRequests = []ResourceRequest{{Consumer: "photos", ID: "database", Contract: "relational_database", Interface: "postgres", Provider: "postgres"}}
			if err := a.ensureResourcesFor("photos", t.TempDir()); err == nil || !strings.Contains(err.Error(), "database engine change") {
				t.Fatalf("frozen replay bypassed the engine guard: %v", err)
			}
		})
	}
}

func TestDatabaseEngineCheckPreservesSameEngineAndRejectsUnreadableState(t *testing.T) {
	a := relationalDatabaseAppForTest(relationalDatabaseSpecForTest(), "postgres")
	a.base = t.TempDir()
	path := filepath.Join(a.base, "state", "resources", "photos.database.yml")
	state := resourceState{APIVersion: resourceStateAPIVersion, Consumer: "photos", ResourceID: "database", Contract: "relational_database", Interface: "postgres", Provider: "postgres", Status: "ready"}
	if err := writeYAMLAtomic(path, state, 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeResourceSecrets(); err == nil {
		t.Fatal("unreadable prior resource treated as a new database")
	}
}
