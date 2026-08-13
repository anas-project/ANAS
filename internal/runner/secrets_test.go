package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretStoreV2PersistsMetadataAndMode(t *testing.T) {
	base := t.TempDir()
	store, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store.SetWithMetadata("ANAS_LOCAL_ADMIN__APP__PRIMARY__PASSWORD", "candidate", secretMetadata{
		Owner: "app", Kind: "local_admin", Provenance: "generated-local-admin",
	})
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(store.path) != "secrets.yml" {
		t.Fatalf("store path = %s", store.path)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	b, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"api_version: anas.secrets/v2", "owner: app", "kind: local_admin", "provenance: generated-local-admin"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("secret store lacks %q:\n%s", want, b)
		}
	}
	reloaded, err := loadSecretStore(base)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.values["ANAS_LOCAL_ADMIN__APP__PRIMARY__PASSWORD"] != "candidate" || reloaded.metadata["ANAS_LOCAL_ADMIN__APP__PRIMARY__PASSWORD"].Owner != "app" {
		t.Fatalf("round trip lost value or metadata: %#v %#v", reloaded.values, reloaded.metadata)
	}
}

func TestLegacySecretStoreIsExplicitlyUnsupported(t *testing.T) {
	base := t.TempDir()
	legacy := filepath.Join(base, "secrets.generated.yml")
	if err := os.WriteFile(legacy, []byte("OLD: value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecretStore(base); err == nil || !strings.Contains(err.Error(), "unsupported legacy secret store") {
		t.Fatalf("legacy error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "secrets.yml")); !os.IsNotExist(err) {
		t.Fatal("legacy rejection unexpectedly created secrets.yml")
	}
}
