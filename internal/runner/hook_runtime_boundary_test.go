package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"github.com/anas-project/ANAS/internal/configschema"
)

func TestRenderEnvHookRevalidatesDeclaredParametersAndPreservesPrivateKeys(t *testing.T) {
	minimum := 1
	moduleDir := t.TempDir()
	mod := Module{
		Name:       "demo",
		EnvPrefix:  "DEMO",
		SourceDir:  moduleDir,
		Parameters: []string{"limit"},
		Types: map[string]ParamType{
			"limit": {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
		},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' '{"env":{"DEMO_LIMIT":" 2 ","PRIVATE_RENDER_KEY":"preserve exactly"}}'`,
		}},
	}
	a := hookBoundaryApp(t, mod, nil, nil)
	work := filepath.Join(t.TempDir(), "rendered")
	if err := a.renderAll(work); err != nil {
		t.Fatal(err)
	}
	rendered, err := parseEnvFile(filepath.Join(work, mod.Name, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if got := rendered["DEMO_LIMIT"]; got != "2" {
		t.Fatalf("render Hook int = %q, want canonical 2", got)
	}
	if got := rendered["PRIVATE_RENDER_KEY"]; got != "preserve exactly" {
		t.Fatalf("undeclared render-private value = %q", got)
	}
}

func TestRenderEnvHookRedactsSensitiveConstraintErrors(t *testing.T) {
	minimum := 1
	for _, test := range []struct {
		name       string
		secret     string
		changes    map[string]ChangePolicy
		consumes   []string
		storeValue bool
	}{
		{
			name:    "manifest-sensitive int",
			secret:  "-987654321",
			changes: map[string]ChangePolicy{"limit": {Sensitive: true}},
		},
		{
			name:       "secret-store value copied without canonical env key",
			secret:     "-123456789",
			consumes:   []string{"HIDDEN_TOKEN"},
			storeValue: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			moduleDir := t.TempDir()
			mod := Module{
				Name:       "demo",
				EnvPrefix:  "DEMO",
				SourceDir:  moduleDir,
				Parameters: []string{"limit"},
				Types: map[string]ParamType{
					"limit": {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
				},
				Changes:  test.changes,
				Consumes: test.consumes,
				Hook: HookConfig{Command: []string{
					"sh", "-c", `printf '%s' "$1"`, "anas-test-hook",
					`{"env":{"DEMO_LIMIT":"` + test.secret + `"}}`,
				}},
			}
			store := &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}
			if test.storeValue {
				store.values["HIDDEN_TOKEN"] = test.secret
				store.metadata["HIDDEN_TOKEN"] = secretMetadata{Owner: "demo", Kind: "generated", Provenance: "module-hook"}
			}
			a := hookBoundaryApp(t, mod, store, nil)
			err := a.renderAll(filepath.Join(t.TempDir(), "rendered"))
			if err == nil || !strings.Contains(err.Error(), "does not satisfy its declared type or constraints") {
				t.Fatalf("render Hook constraint error = %v", err)
			}
			if strings.Contains(err.Error(), test.secret) {
				t.Fatalf("render Hook leaked sensitive value: %v", err)
			}
		})
	}
}

func TestApplyCalculatePatchInvalidatesSensitivityAndTaintsSecretAliases(t *testing.T) {
	const secret = "-246813579"
	for _, test := range []struct {
		name  string
		setup func(*app)
	}{
		{
			name: "config secret",
			setup: func(a *app) {
				a.cfg = &config.File{Secrets: map[string]any{"SOURCE_SECRET": secret}}
			},
		},
		{
			name: "lifecycle secret",
			setup: func(a *app) {
				a.secrets.values["SOURCE_SECRET"] = secret
				a.secrets.metadata["SOURCE_SECRET"] = secretMetadata{Owner: "demo", Kind: "lifecycle_managed", Provenance: "config-import:test"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mod := Module{Name: "demo", EnvPrefix: "DEMO"}
			a := hookBoundaryApp(t, mod, nil, map[string]string{"SOURCE_SECRET": secret})
			test.setup(a)
			if a.sensitiveEnvKeySet()["DEMO_ALIAS"] {
				t.Fatal("alias was sensitive before the Hook created it")
			}
			if err := a.applyCalculatePatch(mod, map[string]string{"DEMO_ALIAS": secret}); err != nil {
				t.Fatal(err)
			}
			if !a.sensitiveEnvKeySet()["DEMO_ALIAS"] {
				t.Fatal("successful calculate patch kept a stale sensitivity cache")
			}
		})
	}
}

func TestCalculateRedactsConfigAndLifecycleSecretAliases(t *testing.T) {
	const secret = "-135792468"
	minimum := 1
	for _, test := range []struct {
		name  string
		setup func(*app)
	}{
		{
			name: "config secret",
			setup: func(a *app) {
				a.cfg = &config.File{Secrets: map[string]any{"SOURCE_SECRET": secret}}
			},
		},
		{
			name: "lifecycle secret",
			setup: func(a *app) {
				a.secrets.values["SOURCE_SECRET"] = secret
				a.secrets.metadata["SOURCE_SECRET"] = secretMetadata{Owner: "demo", Kind: "lifecycle_managed", Provenance: "config-import:test"}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			mod := Module{
				Name:       "demo",
				EnvPrefix:  "DEMO",
				SourceDir:  t.TempDir(),
				Parameters: []string{"alias"},
				Types: map[string]ParamType{
					"alias": {Kind: "int", Constraints: configschema.Constraints{Minimum: &minimum}},
				},
				Hook: HookConfig{Command: []string{
					"sh", "-c", `printf '%s' "$1"`, "anas-test-hook",
					`{"env":{"DEMO_ALIAS":"` + secret + `"}}`,
				}},
			}
			a := hookBoundaryApp(t, mod, nil, map[string]string{"SOURCE_SECRET": secret})
			test.setup(a)
			seedCalculateGlobalRequirements(a.env)
			// Warm the pre-Hook cache to prove the patch cannot reuse it.
			_ = a.sensitiveEnvKeySet()
			err := a.calculate()
			if err == nil || !strings.Contains(err.Error(), "does not satisfy its declared type or constraints") {
				t.Fatalf("calculate alias constraint error = %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("calculate Hook leaked source-sensitive alias: %v", err)
			}
		})
	}
}

func TestHookSecretPatchProtectsNonHookManagedRecordsAtomically(t *testing.T) {
	const (
		existingSecret = "existing-secret-must-not-leak"
		incomingSecret = "incoming-secret-must-not-leak"
		freshSecret    = "fresh-secret-must-not-land"
		metadataSecret = "metadata-must-not-leak"
	)
	for _, test := range []struct {
		name string
		meta secretMetadata
	}{
		{name: "lifecycle managed", meta: secretMetadata{Owner: "demo", Kind: "lifecycle_managed", Provenance: metadataSecret}},
		{name: "local administrator", meta: secretMetadata{Owner: "demo", Kind: "local_admin", Provenance: metadataSecret}},
		{name: "runner generated", meta: secretMetadata{Owner: "runner", Kind: "generated", Provenance: metadataSecret}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &secretStore{
				values:   map[string]string{"PROTECTED_SECRET": existingSecret},
				metadata: map[string]secretMetadata{"PROTECTED_SECRET": test.meta},
			}
			err := store.Merge("demo", map[string]string{
				"PROTECTED_SECRET": incomingSecret,
				"FRESH_SECRET":     freshSecret,
			})
			if err == nil || !strings.Contains(err.Error(), "PROTECTED_SECRET") || !strings.Contains(err.Error(), "non-hook-managed") {
				t.Fatalf("protected Hook secret error = %v", err)
			}
			for _, forbidden := range []string{existingSecret, incomingSecret, freshSecret, metadataSecret} {
				if strings.Contains(err.Error(), forbidden) {
					t.Fatalf("protected Hook secret error leaked %q: %v", forbidden, err)
				}
			}
			if len(store.values) != 1 || store.values["PROTECTED_SECRET"] != existingSecret {
				t.Fatalf("rejected secret patch partially mutated values: %#v", store.values)
			}
			if len(store.metadata) != 1 || store.metadata["PROTECTED_SECRET"] != test.meta {
				t.Fatalf("rejected secret patch partially mutated metadata: %#v", store.metadata)
			}
			if store.dirty {
				t.Fatal("rejected secret patch marked the store dirty")
			}
		})
	}
}

func TestHookSecretPatchCanRefreshModuleHookGeneratedRecord(t *testing.T) {
	meta := secretMetadata{Owner: "demo", Kind: "generated", Provenance: "module-hook"}
	store := &secretStore{
		values:   map[string]string{"DEMO_TOKEN": "old-generated"},
		metadata: map[string]secretMetadata{"DEMO_TOKEN": meta},
	}
	if err := store.Merge("demo", map[string]string{"DEMO_TOKEN": "new-generated"}); err != nil {
		t.Fatal(err)
	}
	if got := store.values["DEMO_TOKEN"]; got != "new-generated" {
		t.Fatalf("module Hook secret = %q", got)
	}
	if store.metadata["DEMO_TOKEN"] != meta {
		t.Fatalf("module Hook metadata changed: %#v", store.metadata["DEMO_TOKEN"])
	}
	if !store.dirty {
		t.Fatal("updated module Hook secret did not mark the store dirty")
	}

	protectedMeta := secretMetadata{Owner: "demo", Kind: "lifecycle_managed", Provenance: "config-import:test"}
	protected := &secretStore{
		values:   map[string]string{"DEMO_TOKEN": "same-value"},
		metadata: map[string]secretMetadata{"DEMO_TOKEN": protectedMeta},
	}
	if err := protected.Merge("demo", map[string]string{"DEMO_TOKEN": "same-value"}); err != nil {
		t.Fatalf("idempotent protected patch: %v", err)
	}
	if protected.dirty || protected.metadata["DEMO_TOKEN"] != protectedMeta {
		t.Fatalf("idempotent patch overwrote protected record: dirty=%t meta=%#v", protected.dirty, protected.metadata["DEMO_TOKEN"])
	}
}

func TestHookSecretPatchRejectsAnotherModuleOwnerAtomically(t *testing.T) {
	const (
		existingSecret = "existing-secret-must-not-leak"
		incomingSecret = "incoming-secret-must-not-leak"
		freshSecret    = "fresh-secret-must-not-land"
	)
	meta := secretMetadata{Owner: "publisher", Kind: "generated", Provenance: "module-hook"}
	store := &secretStore{
		values:   map[string]string{"PUBLISHER_TOKEN": existingSecret},
		metadata: map[string]secretMetadata{"PUBLISHER_TOKEN": meta},
	}
	err := store.Merge("consumer", map[string]string{
		"CONSUMER_TOKEN":  freshSecret,
		"PUBLISHER_TOKEN": incomingSecret,
	})
	if err == nil || !strings.Contains(err.Error(), "PUBLISHER_TOKEN") || !strings.Contains(err.Error(), "differently owned") {
		t.Fatalf("cross-module Hook secret error = %v", err)
	}
	for _, forbidden := range []string{existingSecret, incomingSecret, freshSecret} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("cross-module Hook secret error leaked %q: %v", forbidden, err)
		}
	}
	if len(store.values) != 1 || store.values["PUBLISHER_TOKEN"] != existingSecret {
		t.Fatalf("rejected cross-module patch partially mutated values: %#v", store.values)
	}
	if len(store.metadata) != 1 || store.metadata["PUBLISHER_TOKEN"] != meta {
		t.Fatalf("rejected cross-module patch partially mutated metadata: %#v", store.metadata)
	}
	if store.dirty {
		t.Fatal("rejected cross-module patch marked the store dirty")
	}

	if err := store.Merge("consumer", map[string]string{"PUBLISHER_TOKEN": existingSecret}); err != nil {
		t.Fatalf("identical value should remain idempotent: %v", err)
	}
	if store.dirty || store.metadata["PUBLISHER_TOKEN"] != meta {
		t.Fatalf("idempotent cross-module patch changed the record: dirty=%t meta=%#v", store.dirty, store.metadata["PUBLISHER_TOKEN"])
	}
}

func TestCalculateBindsSecretPatchToCurrentModuleBeforeEnvMutation(t *testing.T) {
	const (
		existingSecret = "publisher-secret-must-not-leak"
		incomingSecret = "consumer-secret-must-not-leak"
	)
	mod := Module{
		Name:      "consumer",
		EnvPrefix: "CONSUMER",
		SourceDir: t.TempDir(),
		// Cross-prefix Hook outputs are legal when explicitly exported. The
		// Secret Store owner still prevents an export from rotating another
		// module's record.
		Exports: []string{"PUBLISHER_TOKEN"},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' "$1"`, "anas-test-hook",
			`{"env":{"CONSUMER_SAFE":"must-not-land"},"secrets":{"CONSUMER_FRESH":"must-not-land","PUBLISHER_TOKEN":"` + incomingSecret + `"}}`,
		}},
	}
	meta := secretMetadata{Owner: "publisher", Kind: "generated", Provenance: "module-hook"}
	store := &secretStore{
		values:   map[string]string{"PUBLISHER_TOKEN": existingSecret},
		metadata: map[string]secretMetadata{"PUBLISHER_TOKEN": meta},
	}
	a := hookBoundaryApp(t, mod, store, nil)
	seedCalculateGlobalRequirements(a.env)
	err := a.calculate()
	if err == nil || !strings.Contains(err.Error(), "PUBLISHER_TOKEN") || !strings.Contains(err.Error(), "differently owned") {
		t.Fatalf("calculate cross-module secret error = %v", err)
	}
	for _, forbidden := range []string{existingSecret, incomingSecret} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("calculate cross-module secret error leaked %q: %v", forbidden, err)
		}
	}
	if _, landed := a.env["CONSUMER_SAFE"]; landed {
		t.Fatal("rejected Secret patch partially applied its Env patch")
	}
	if len(store.values) != 1 || store.values["PUBLISHER_TOKEN"] != existingSecret || store.metadata["PUBLISHER_TOKEN"] != meta {
		t.Fatalf("rejected calculate response mutated Secret Store: %#v / %#v", store.values, store.metadata)
	}
	if store.dirty {
		t.Fatal("rejected calculate response marked Secret Store dirty")
	}
}

func TestRenderEnvRejectsInvalidHookKeyBeforeWritingDotEnv(t *testing.T) {
	mod := Module{
		Name:      "demo",
		EnvPrefix: "DEMO",
		SourceDir: t.TempDir(),
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' "$1"`, "anas-test-hook",
			`{"env":{"DEMO_NEWLINE\nINJECTED":"malicious","DEMO_SAFE":"must-not-land"}}`,
		}},
	}
	a := hookBoundaryApp(t, mod, nil, nil)
	a.deps = map[string][]string{}
	work := t.TempDir()
	err := a.renderAll(work)
	if err == nil || !strings.Contains(err.Error(), "render_env patch") || !strings.Contains(err.Error(), "invalid env keys") {
		t.Fatalf("invalid render_env Hook key error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(work, "demo", ".env")); !os.IsNotExist(statErr) {
		t.Fatalf("invalid render_env Hook response wrote .env: %v", statErr)
	}
	if _, landed := a.env["DEMO_SAFE"]; landed {
		t.Fatal("invalid render_env Hook response mutated deployment env")
	}
}

func TestCalculateRejectsProtectedSecretResponseBeforeEnvMutation(t *testing.T) {
	const (
		existingSecret = "existing-lifecycle-secret"
		incomingSecret = "incoming-hook-secret"
		metadataSecret = "secret-provenance"
	)
	mod := Module{
		Name:      "demo",
		EnvPrefix: "DEMO",
		SourceDir: t.TempDir(),
		Exports:   []string{"PROTECTED_SECRET", "FRESH_SECRET"},
		Hook: HookConfig{Command: []string{
			"sh", "-c", `printf '%s' "$1"`, "anas-test-hook",
			`{"env":{"DEMO_SAFE":"must-not-land"},"secrets":{"PROTECTED_SECRET":"` + incomingSecret + `","FRESH_SECRET":"must-not-land"}}`,
		}},
	}
	store := &secretStore{
		values:   map[string]string{"PROTECTED_SECRET": existingSecret},
		metadata: map[string]secretMetadata{"PROTECTED_SECRET": {Owner: "demo", Kind: "lifecycle_managed", Provenance: metadataSecret}},
	}
	a := hookBoundaryApp(t, mod, store, nil)
	seedCalculateGlobalRequirements(a.env)
	err := a.calculate()
	if err == nil || !strings.Contains(err.Error(), "non-hook-managed") {
		t.Fatalf("protected Hook response error = %v", err)
	}
	for _, forbidden := range []string{existingSecret, incomingSecret, metadataSecret} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("protected Hook response leaked %q: %v", forbidden, err)
		}
	}
	if _, landed := a.env["DEMO_SAFE"]; landed {
		t.Fatal("rejected Hook secret response partially applied its env patch")
	}
	if len(store.values) != 1 || store.values["PROTECTED_SECRET"] != existingSecret {
		t.Fatalf("rejected Hook response mutated secret store: %#v", store.values)
	}
	if store.dirty {
		t.Fatal("rejected Hook response marked secret store dirty")
	}
}

func hookBoundaryApp(t *testing.T, mod Module, store *secretStore, values map[string]string) *app {
	t.Helper()
	if store == nil {
		store = &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}
	}
	if values == nil {
		values = map[string]string{}
	}
	return &app{
		base:     t.TempDir(),
		reg:      map[string]Module{mod.Name: mod},
		order:    []string{mod.Name},
		env:      values,
		envOwner: map[string]string{},
		secrets:  store,
	}
}
