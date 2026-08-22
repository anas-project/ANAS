package runner

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/config"
	"gopkg.in/yaml.v3"
)

func TestResourceEnsureComposeRunIsNonInteractive(t *testing.T) {
	got := resourceEnsureComposeArgs("anas_postgres_provision", []string{"ensure"})
	want := []string{"run", "--rm", "--no-deps", "--no-TTY", "anas_postgres_provision", "ensure"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resource ensure compose args = %q, want %q", got, want)
	}
}

func TestObjectStorageResourcesAreOptionalAndIsolatedPerModule(t *testing.T) {
	a := &app{
		order: []string{"photos", "backup", "unrelated"},
		reg: map[string]Module{
			"photos": {
				Name: "photos", Resources: []ResourceRequirement{{
					ID: "objects", Contract: "object_storage", Binding: "object_storage_type",
					Spec: map[string]any{"bucket": "photos-data", "credential": map[string]any{"policy": "generated"}, "deletion_policy": "retain"},
				}},
			},
			"backup": {
				Name: "backup", Resources: []ResourceRequirement{{
					ID: "objects", Contract: "object_storage", Binding: "object_storage_type",
					Spec: map[string]any{"bucket": "backup-data", "credential": map[string]any{"policy": "generated"}, "deletion_policy": "retain"},
				}},
			},
			"unrelated": {Name: "unrelated"},
		},
		contracts: map[string]Contract{"object_storage": {Name: "object_storage", Version: "1.0.0", Interfaces: []string{"s3"}}},
		resolvedBindings: map[string]map[string]string{
			"photos": {"object_storage": "versitygw", "object_storage.interface": "s3"},
			"backup": {"object_storage": "versitygw", "object_storage.interface": "s3"},
		},
		env: map[string]string{
			"ANAS_OBJECT_STORAGE_S3_ENDPOINT":   "https://s3.example.test",
			"ANAS_OBJECT_STORAGE_S3_REGION":     "us-east-1",
			"ANAS_OBJECT_STORAGE_S3_PATH_STYLE": "true",
		},
		envOwner: map[string]string{},
		secrets:  &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if len(a.resourceRequests) != 2 {
		t.Fatalf("resource requests = %+v", a.resourceRequests)
	}
	first, second := a.resourceRequests[0], a.resourceRequests[1]
	if first.SecretKey == second.SecretKey || first.Credential == second.Credential {
		t.Fatal("object storage resources did not receive independent credentials")
	}
	if first.Spec["access_key_id"] == second.Spec["access_key_id"] {
		t.Fatal("object storage resources did not receive independent access key IDs")
	}
	firstCredential := first.Credential
	if err := a.materializeResourceSecrets(); err != nil {
		t.Fatal(err)
	}
	if a.resourceRequests[0].Credential != firstCredential {
		t.Fatal("object storage resource replaced its stable generated secret")
	}
	for _, consumer := range []string{"photos", "backup", "unrelated"} {
		if err := a.publishModuleResources(consumer); err != nil {
			t.Fatal(err)
		}
	}
	photosPrefix := objectStorageResourcePrefix("photos", "objects")
	backupPrefix := objectStorageResourcePrefix("backup", "objects")
	if a.env[photosPrefix+"BUCKET"] != "photos-data" || a.env[backupPrefix+"BUCKET"] != "backup-data" {
		t.Fatalf("object storage resource projection = %#v", a.env)
	}
	if a.envOwner[photosPrefix+"SECRET_ACCESS_KEY"] != "photos" || !a.runnerSensitive[photosPrefix+"SECRET_ACCESS_KEY"] {
		t.Fatal("photos resource secret is not scoped and marked sensitive")
	}
	if a.scopedEnv("photos")[backupPrefix+"SECRET_ACCESS_KEY"] != "" || a.scopedEnv("backup")[photosPrefix+"SECRET_ACCESS_KEY"] != "" {
		t.Fatal("object storage resource credential leaked between consumers")
	}
	for key := range a.env {
		if strings.Contains(key, "UNRELATED") && strings.HasPrefix(key, "ANAS_OBJECT_STORAGE_RESOURCE__") {
			t.Fatalf("undeclared module received object storage key %s", key)
		}
	}
}

func TestObjectStorageResourceRejectsDuplicateBucket(t *testing.T) {
	resource := func(bucket string) ResourceRequirement {
		return ResourceRequirement{ID: "objects", Contract: "object_storage", Spec: map[string]any{
			"bucket": bucket, "credential": map[string]any{"policy": "generated"}, "deletion_policy": "retain",
		}}
	}
	a := &app{
		order: []string{"one", "two"},
		reg: map[string]Module{
			"one": {Name: "one", Resources: []ResourceRequirement{resource("shared-bucket")}},
			"two": {Name: "two", Resources: []ResourceRequirement{resource("shared-bucket")}},
		},
		contracts: map[string]Contract{"object_storage": {Name: "object_storage", Version: "1.0.0"}},
		resolvedBindings: map[string]map[string]string{
			"one": {"object_storage": "versitygw", "object_storage.interface": "s3"},
			"two": {"object_storage": "versitygw", "object_storage.interface": "s3"},
		},
		secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}},
	}
	if err := a.materializeResourceSecrets(); err == nil || !strings.Contains(err.Error(), "same bucket") {
		t.Fatalf("duplicate bucket error = %v", err)
	}
}

func TestObjectStorageResourceStateStoresSecretReferenceOnly(t *testing.T) {
	base := t.TempDir()
	a := &app{base: base}
	request := ResourceRequest{
		Consumer: "photos", ID: "objects", Contract: "object_storage", ContractVersion: "1.0.0",
		Provider: "versitygw", Interface: "s3", SecretKey: "RESOURCE_PHOTOS_OBJECTS_SECRET_ACCESS_KEY",
		Credential: "must-not-appear", Spec: map[string]any{
			"bucket": "photos-data", "access_key_id": "ANAS_PHOTOS_OBJECTS", "deletion_policy": "retain",
		},
	}
	if err := a.saveResourceReady(request, map[string]string{
		"ANAS_OBJECT_STORAGE_S3_ENDPOINT":   "https://s3.example.test",
		"ANAS_OBJECT_STORAGE_S3_REGION":     "us-east-1",
		"ANAS_OBJECT_STORAGE_S3_PATH_STYLE": "true",
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(base, "state", "resources", "photos.objects.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if strings.Contains(text, request.Credential) || !strings.Contains(text, "secret_access_key_secret: "+request.SecretKey) {
		t.Fatalf("unsafe object resource state:\n%s", text)
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

func TestBundledVersityGWImplementsObjectStorageLifecycle(t *testing.T) {
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
	provider, ok := reg["versitygw"].providedContract("object_storage", "s3")
	if !ok {
		t.Fatal("versitygw does not provide object_storage/s3")
	}
	for _, name := range []string{"ensure", "inspect", "rotate_credential"} {
		operation, ok := provider.Operations[name]
		if !ok || operation.Runtime != "compose_run" || operation.Service != "anas_versitygw_provision" {
			t.Fatalf("versitygw operation %s = %+v", name, operation)
		}
	}
}

func TestObjectStorageResourcePullsVersityGWIntoConsumerOrder(t *testing.T) {
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
	reg["archive"] = Module{
		Name: "archive", EnvPrefix: "ARCHIVE",
		RequiresContracts: []ContractDependency{{
			Name: "object_storage", Version: ">=1.0.0 <2.0.0", SelectedBy: "object_storage_type",
			Interfaces: []string{"s3"}, Default: "s3",
		}},
		Resources: []ResourceRequirement{{
			ID: "objects", Contract: "object_storage", Binding: "object_storage_type",
			Spec: map[string]any{
				"bucket": "archive-data", "credential": map[string]any{"policy": "generated"}, "deletion_policy": "retain",
			},
		}},
	}
	a := &app{
		cfg: &config.File{Modules: config.NewModuleSelection("archive")}, reg: reg, contracts: contracts,
		env: map[string]string{"ARCHIVE_OBJECT_STORAGE_TYPE": "auto"}, envOwner: map[string]string{},
		lock: &moduleLock{Bindings: map[string]map[string]string{}}, registryOnlyResolution: true,
	}
	order, err := a.resolveOrder([]string{"archive"})
	if err != nil {
		t.Fatal(err)
	}
	if !contains(order, "versitygw") || index(order, "versitygw") > index(order, "archive") {
		t.Fatalf("resource order = %v, want versitygw before archive", order)
	}
	if got := a.resolvedBindings["archive"]["object_storage"]; got != "versitygw" {
		t.Fatalf("resource provider binding = %q, want versitygw", got)
	}
}
