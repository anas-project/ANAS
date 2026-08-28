package runner

// TEST_CASES: MCO-T-001

import (
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
)

func TestServerIdentityFixturesUsePublicConfiguration(t *testing.T) {
	root := repoRoot(t)
	reg, err := loadRegistryDir(filepath.Join(root, "modules"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"server-identity-app-e2e.yml",
		"server-identity-app-llng-e2e.yml",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "test-env", name)
			if _, err := config.Load(path); err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			if err := validateConfigImportSource(path, reg); err != nil {
				t.Fatalf("fixture is not importable: %v", err)
			}
		})
	}
}
