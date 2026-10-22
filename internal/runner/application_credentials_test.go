package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

type applicationCredentialFixture struct {
	t                   *testing.T
	workspace, id, root string
	manifest            *deploymentManifest
	catalog             appcatalog.Catalog
}

func newApplicationCredentialFixture(t *testing.T, local bool) *applicationCredentialFixture {
	t.Helper()
	workspace, id := newSnapshotWorkspace(t)
	f := &applicationCredentialFixture{t: t, workspace: workspace, id: id, root: deploymentArtifactDir(stateDir(workspace), id)}
	ref := appcatalog.CredentialReference{UsernameFrom: "CORE_ADMIN_USERNAME", PasswordFrom: "CORE_ADMIN_PASSWORD"}
	if local {
		ref = appcatalog.CredentialReference{LocalAccount: "primary"}
	}
	f.manifest = &deploymentManifest{APIVersion: deploymentAPIVersion, ID: id, ModuleOrder: []string{"core"}, Modules: map[string]deploymentModule{
		"core": {Name: "core", EnvPrefix: "CORE", RuntimeType: "builtin", LocalAccounts: []LocalAccount{{ID: "primary"}}, ApplicationCredentials: map[string]appcatalog.CredentialReference{"core-admin": ref}},
	}}
	icons := map[string]appcatalog.Icon{}
	for _, size := range appcatalog.IconSizes {
		digest := strings.Repeat("0", 64)
		icons[fmt.Sprint(size)] = appcatalog.Icon{Path: "assets/" + digest + ".png", SHA256: digest, MediaType: "image/png", Width: size, Height: size}
	}
	f.catalog = appcatalog.Catalog{Schema: appcatalog.Schema, Applications: []appcatalog.Application{{
		Metadata: appcatalog.Metadata{Name: "core-admin", Kind: "admin", URL: "https://core.example/admin"},
		Enabled:  true, CredentialsAvailable: true, Access: appcatalog.Access{Mode: "local"}, Source: appcatalog.Source{Kind: "module", Module: "core"}, Icons: icons,
	}}}
	f.persist()
	f.setLocalPassword("initial-local-password")
	f.setEnvironment("applied-admin", "applied-password")
	return f
}

func (f *applicationCredentialFixture) persist() {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.root, applicationCatalogRelative), 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := writeYAMLAtomic(filepath.Join(f.root, "deployment.yml"), f.manifest, 0600); err != nil {
		f.t.Fatal(err)
	}
	b, err := json.Marshal(f.catalog)
	if err != nil {
		f.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(f.root, applicationCatalogRelative, "catalog.json"), b, 0600); err != nil {
		f.t.Fatal(err)
	}
	if err = saveActiveState(stateDir(f.workspace), &activeDeploymentState{ActiveDeployment: f.id, RuntimeStatus: "running"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *applicationCredentialFixture) setLocalPassword(password string) {
	f.t.Helper()
	base := stateDir(f.workspace)
	key := localAdminSecretKey("core", "primary")
	admins := localAdminState{APIVersion: localAdminStateVersion, Accounts: map[string]localAdminRecord{"core.primary": {Module: "core", ID: "primary", Username: "current-admin", SecretKey: key}}}
	if err := writeYAMLAtomic(localAdminStatePath(base), &admins, 0600); err != nil {
		f.t.Fatal(err)
	}
	store := &secretStore{path: filepath.Join(base, "secrets.yml"), values: map[string]string{key: password}, dirty: true}
	if err := store.Save(); err != nil {
		f.t.Fatal(err)
	}
}

func (f *applicationCredentialFixture) setEnvironment(username, password string) {
	f.t.Helper()
	dir := filepath.Join(f.root, "modules", "core")
	if err := os.MkdirAll(dir, 0700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("CORE_ADMIN_USERNAME="+username+"\nCORE_ADMIN_PASSWORD="+password+"\n"), 0600); err != nil {
		f.t.Fatal(err)
	}
}

func TestApplicationCredentialsReadCurrentSecretAndActiveDeployment(t *testing.T) {
	f := newApplicationCredentialFixture(t, true)
	service := NewWorkspaceApplicationCredentialService(f.workspace)
	first, err := service.ReadApplicationCredential(context.Background(), "core-admin")
	if err != nil || first.Username != "current-admin" || first.Password != "initial-local-password" {
		t.Fatalf("initial credential unavailable: %v", err)
	}
	f.setLocalPassword("rotated-local-password")
	next, err := service.ReadApplicationCredential(context.Background(), "core-admin")
	if err != nil || next.Password != "rotated-local-password" {
		t.Fatalf("rotation was not observed: %v", err)
	}
	// Switching to a new active deployment changes both the declared source and
	// the applied environment. Desired workspace configuration must not win.
	f.id = "20260102T000000Z-deadbeef"
	f.root = deploymentArtifactDir(stateDir(f.workspace), f.id)
	f.manifest.ID = f.id
	mod := f.manifest.Modules["core"]
	mod.ApplicationCredentials["core-admin"] = appcatalog.CredentialReference{UsernameFrom: "CORE_ADMIN_USERNAME", PasswordFrom: "CORE_ADMIN_PASSWORD"}
	f.manifest.Modules["core"] = mod
	f.persist()
	f.setEnvironment("next-admin", "next-applied-password")
	if err := os.WriteFile(workspaceConfigPath(f.workspace), []byte("modules:\n  core:\n    admin_password: desired-unapplied-password\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err = service.ReadApplicationCredential(context.Background(), "core-admin")
	if err != nil || next.Username != "next-admin" || next.Password != "next-applied-password" {
		t.Fatalf("active environment was not observed: %v", err)
	}
}

func TestApplicationCredentialsRejectUnavailableOrUndeclaredSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*applicationCredentialFixture)
	}{
		{"disabled", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Enabled = false }},
		{"hidden", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Hidden = true }},
		{"iam", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Access.Mode = "iam" }},
		{"user", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Kind = "user" }},
		{"external", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Source.Kind = "external" }},
		{"unavailable", func(f *applicationCredentialFixture) { f.catalog.Applications[0].CredentialsAvailable = false }},
		{"no-reference", func(f *applicationCredentialFixture) {
			delete(f.manifest.Modules["core"].ApplicationCredentials, "core-admin")
		}},
		{"foreign-module", func(f *applicationCredentialFixture) { f.catalog.Applications[0].Source.Module = "other" }},
		{"foreign-env", func(f *applicationCredentialFixture) {
			f.manifest.Modules["core"].ApplicationCredentials["core-admin"] = appcatalog.CredentialReference{UsernameFrom: "CORE_ADMIN_USERNAME", PasswordFrom: "OTHER_PASSWORD"}
		}},
		{"undeclared-account", func(f *applicationCredentialFixture) {
			f.manifest.Modules["core"].ApplicationCredentials["core-admin"] = appcatalog.CredentialReference{LocalAccount: "other"}
		}},
		{"ambiguous-reference", func(f *applicationCredentialFixture) {
			f.manifest.Modules["core"].ApplicationCredentials["core-admin"] = appcatalog.CredentialReference{LocalAccount: "primary", PasswordFrom: "CORE_ADMIN_PASSWORD"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newApplicationCredentialFixture(t, true)
			tc.change(f)
			f.persist()
			got, err := NewWorkspaceApplicationCredentialService(f.workspace).ReadApplicationCredential(context.Background(), "core-admin")
			if err == nil || got.Password != "" || got.Username != "" {
				t.Fatal("unavailable source disclosed credentials")
			}
			if strings.Contains(err.Error(), "initial-local-password") {
				t.Fatal("error disclosed password")
			}
		})
	}
	f := newApplicationCredentialFixture(t, false)
	if err := saveActiveState(stateDir(f.workspace), &activeDeploymentState{ActiveDeployment: f.id, RuntimeStatus: "stopped"}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWorkspaceApplicationCredentialService(f.workspace).ReadApplicationCredential(context.Background(), "core-admin"); err == nil {
		t.Fatal("stopped deployment accepted")
	}
}

func TestApplicationCredentialReferencesAreExplicitAndModuleOwned(t *testing.T) {
	mod := Module{Name: "core", EnvPrefix: "CORE", LocalAccounts: []LocalAccount{{ID: "primary"}}, ManagementSurfaces: []ManagementSurface{{Authentication: "local", URIFrom: "CORE_URL"}}}
	entry := catalogDeclaration()
	entry.Name = "core-admin"
	entry.Kind = "admin"
	entry.URLFrom = "CORE_URL"
	entry.Access = appcatalog.Access{Mode: "local"}
	entry.Visibility = appcatalog.Visibility{Mode: "subjects", AnyOf: []appcatalog.Subject{{Role: "platform_admin"}}}
	for _, tc := range []struct {
		ref   appcatalog.CredentialReference
		valid bool
	}{
		{appcatalog.CredentialReference{LocalAccount: "primary"}, true},
		{appcatalog.CredentialReference{LocalAccount: "other"}, false},
		{appcatalog.CredentialReference{UsernameFrom: "CORE_USER", PasswordFrom: "CORE_PASSWORD"}, true},
		{appcatalog.CredentialReference{UsernameFrom: "CORE_USER", PasswordFrom: "OTHER_PASSWORD"}, false},
		{appcatalog.CredentialReference{UsernameFrom: "CORE_USER", PasswordFrom: "CORE_PASSWORD\n"}, false},
	} {
		entry.Credentials = &tc.ref
		if err := validateApplicationCredentialReference(mod, entry); (err == nil) != tc.valid {
			t.Fatalf("reference validation = %v, want %t", err, tc.valid)
		}
	}
	entry.Credentials = &appcatalog.CredentialReference{LocalAccount: "primary"}
	mod.Applications = []appcatalog.Declaration{entry}
	frozen := freezeApplicationCredentials(mod)
	entry.Credentials.LocalAccount = "changed"
	if frozen["core-admin"].LocalAccount != "primary" {
		t.Fatal("frozen credential reference aliases source")
	}
}

func TestApplicationCredentialsNeverRecoverPendingConfiguration(t *testing.T) {
	f := newApplicationCredentialFixture(t, true)
	directory := configTransactionDirectory(f.workspace)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(directory, "pending")
	if err := os.WriteFile(marker, []byte("must remain untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(stateDir(f.workspace), "secrets.yml"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewWorkspaceApplicationCredentialService(f.workspace).ReadApplicationCredential(context.Background(), "core-admin")
	if err == nil || got.Password != "" {
		t.Fatal("pending configuration did not block credential read")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "must remain untouched" {
		t.Fatal("read attempted configuration recovery")
	}
	after, err := os.ReadFile(filepath.Join(stateDir(f.workspace), "secrets.yml"))
	if err != nil || string(after) != string(before) {
		t.Fatal("read changed secrets")
	}
}

func TestApplicationCredentialsBlockUnfinishedRotation(t *testing.T) {
	f := newApplicationCredentialFixture(t, true)
	base := stateDir(f.workspace)
	txn, err := beginCredentialRotationTransaction(base, f.id, []deploymentCredential{{ID: "core.admin", Generation: 1}})
	if err != nil {
		t.Fatal(err)
	}
	path := credentialTransactionJournalPath(base, txn.ID)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewWorkspaceApplicationCredentialService(f.workspace).ReadApplicationCredential(context.Background(), "core-admin")
	if err == nil || got.Password != "" {
		t.Fatal("unfinished rotation disclosed a possibly stale password")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("read changed rotation journal")
	}
}
