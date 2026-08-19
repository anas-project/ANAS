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

func TestSecretStoreCanonicalizesKeysAndRejectsCanonicalCollisions(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		base := t.TempDir()
		if err := os.WriteFile(filepath.Join(base, "secrets.yml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return base
	}

	canonical, err := loadSecretStore(write(t, `api_version: anas.secrets/v2
secrets:
  demo_token:
    value: retained
    owner: demo
    kind: lifecycle_managed
    provenance: legacy-lowercase
`))
	if err != nil {
		t.Fatal(err)
	}
	if canonical.values["DEMO_TOKEN"] != "retained" || canonical.metadata["DEMO_TOKEN"].Owner != "demo" {
		t.Fatalf("canonical store = %#v / %#v", canonical.values, canonical.metadata)
	}

	_, err = loadSecretStore(write(t, `api_version: anas.secrets/v2
secrets:
  demo_token: {value: one, owner: demo, kind: lifecycle_managed}
  DEMO_TOKEN: {value: two, owner: demo, kind: lifecycle_managed}
`))
	if err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("canonical collision error = %v", err)
	}

	_, err = loadSecretStore(write(t, `api_version: anas.secrets/v2
secrets:
  bad-key: {value: one, owner: demo, kind: lifecycle_managed}
`))
	if err == nil || !strings.Contains(err.Error(), "environment key") {
		t.Fatalf("invalid secret key error = %v", err)
	}
}

func TestSecretStoreMergeCanonicalizesAtomically(t *testing.T) {
	store := &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}
	if err := store.Merge("demo", map[string]string{"demo_token": "generated"}); err != nil {
		t.Fatal(err)
	}
	if store.values["DEMO_TOKEN"] != "generated" || store.metadata["DEMO_TOKEN"] != (secretMetadata{Owner: "demo", Kind: "generated", Provenance: "module-hook"}) {
		t.Fatalf("canonical hook secret = %#v / %#v", store.values, store.metadata)
	}

	beforeValues := store.clone()
	beforeDirty := store.dirty
	for name, patch := range map[string]map[string]string{
		"canonical collision": {"next_token": "one", "NEXT_TOKEN": "two"},
		"invalid key":         {"bad-key": "value", "SAFE_KEY": "must-not-land"},
	} {
		t.Run(name, func(t *testing.T) {
			err := store.Merge("demo", patch)
			if err == nil {
				t.Fatal("invalid Hook secret patch was accepted")
			}
			if got := store.clone(); len(got) != len(beforeValues) || got["DEMO_TOKEN"] != beforeValues["DEMO_TOKEN"] {
				t.Fatalf("rejected Hook patch partially mutated store: %#v", got)
			}
			if store.dirty != beforeDirty {
				t.Fatalf("rejected Hook patch changed dirty state to %t", store.dirty)
			}
		})
	}
}

func TestModuleHookSecretOwnerPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.yml")
	store := &secretStore{path: path, values: map[string]string{}, metadata: map[string]secretMetadata{}}
	if err := store.Merge("publisher", map[string]string{"PUBLISHER_TOKEN": "generated"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded, err := loadSecretStoreFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := secretMetadata{Owner: "publisher", Kind: "generated", Provenance: "module-hook"}
	if reloaded.values["PUBLISHER_TOKEN"] != "generated" || reloaded.metadata["PUBLISHER_TOKEN"] != want {
		t.Fatalf("persisted module Hook secret = %#v / %#v", reloaded.values, reloaded.metadata)
	}
}
