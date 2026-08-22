package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestObjectStorageProviderEnsureIsIdempotentAndRejectsOwnerConflict(t *testing.T) {
	temp := t.TempDir()
	fake := `#!/bin/sh
set -eu
[ "$1" = admin ]
shift
command="$1"
shift
case "$command" in
  list-users)
    echo "Account Role"
    [ ! -f "$FAKE_USERS" ] || cat "$FAKE_USERS"
    ;;
  create-user)
    while [ "$#" -gt 0 ]; do
      case "$1" in --access) access="$2"; shift 2;; *) shift;; esac
    done
    printf '%s user\n' "$access" >"$FAKE_USERS"
    echo create-user >>"$FAKE_CALLS"
    ;;
  update-user)
    echo update-user >>"$FAKE_CALLS"
    ;;
  list-buckets)
    echo "Bucket Owner"
    [ ! -f "$FAKE_BUCKETS" ] || cat "$FAKE_BUCKETS"
    ;;
  create-bucket)
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --bucket) bucket="$2"; shift 2;;
        --owner) owner="$2"; shift 2;;
        *) shift;;
      esac
    done
    printf '%s %s\n' "$bucket" "$owner" >"$FAKE_BUCKETS"
    echo create-bucket >>"$FAKE_CALLS"
    ;;
  *) exit 2;;
esac
`
	fakePath := filepath.Join(temp, "versitygw")
	if err := os.WriteFile(fakePath, []byte(fake), 0755); err != nil {
		t.Fatal(err)
	}
	users := filepath.Join(temp, "users")
	buckets := filepath.Join(temp, "buckets")
	calls := filepath.Join(temp, "calls")
	script := filepath.Join("..", "providers", "object_storage", "provision.sh")
	run := func(operation string) ([]byte, error) {
		command := exec.Command("/bin/sh", script, operation)
		command.Env = append(os.Environ(),
			"PATH="+temp+":"+os.Getenv("PATH"),
			"FAKE_USERS="+users,
			"FAKE_BUCKETS="+buckets,
			"FAKE_CALLS="+calls,
			"ANAS_RESOURCE_BUCKET=photos-data",
			"ANAS_RESOURCE_ACCESS_KEY_ID=ANAS_PHOTOS_OBJECTS",
			"ANAS_RESOURCE_SECRET_ACCESS_KEY=private-resource-secret",
		)
		return command.CombinedOutput()
	}
	if output, err := run("ensure"); err != nil {
		t.Fatalf("first ensure: %v: %s", err, output)
	}
	if output, err := run("ensure"); err != nil {
		t.Fatalf("second ensure: %v: %s", err, output)
	}
	body, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); strings.Count(got, "create-user") != 1 || strings.Count(got, "create-bucket") != 1 || strings.Count(got, "update-user") != 1 {
		t.Fatalf("provider calls = %q", got)
	}
	if output, err := run("inspect"); err != nil || strings.TrimSpace(string(output)) != "ready" {
		t.Fatalf("inspect: %v: %s", err, output)
	}
	if err := os.WriteFile(buckets, []byte("photos-data ANAS_OTHER\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := run("ensure")
	if err == nil || !strings.Contains(string(output), "belongs to another principal") {
		t.Fatalf("owner conflict: %v: %s", err, output)
	}
	afterConflict, readErr := os.ReadFile(calls)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(afterConflict) != string(body) {
		t.Fatalf("owner conflict mutated provider state: before=%q after=%q", body, afterConflict)
	}
	if strings.Contains(string(output), "private-resource-secret") {
		t.Fatal("provider failure leaked the resource secret")
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"BASE_DOMAIN":               "nas.test",
		"CONTAINER_PREFIX":          "anas_",
		"DATA_PATH":                 "/srv/anas/data",
		"TRAEFIK_BASE_PORT":         "443",
		"VERSITYGW_DOMAIN_PREFIX":   "s3",
		"VERSITYGW_READ_ONLY":       "false",
		"VERSITYGW_REGION":          "us-east-1",
		"VERSITYGW_ROOT_ACCESS_KEY": "ANASROOT",
		"VERSITYGW_ROOT_SECRET_KEY": "",
	}
}

func TestCalculateDerivesEndpointPathAndStableSecret(t *testing.T) {
	env := baseEnv()
	secrets := &secretStore{values: map[string]string{}}
	if err := calculate("versitygw", env, "", secrets); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		"VERSITYGW_HOSTNAME":                   "anas_versitygw",
		"VERSITYGW_DOMAIN":                     "s3.nas.test",
		"VERSITYGW_DOMAIN_PORT":                "s3.nas.test:443",
		"VERSITYGW_ENDPOINT":                   "https://s3.nas.test:443",
		"VERSITYGW_OBJECTS_PATH":               "/srv/anas/data/versitygw/objects",
		"VERSITYGW_IAM_PATH":                   "/srv/anas/data/versitygw/iam",
		"ANAS_OBJECT_STORAGE_S3_ENDPOINT":      "https://s3.nas.test:443",
		"ANAS_OBJECT_STORAGE_S3_REGION":        "us-east-1",
		"ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID": "ANASROOT",
		"ANAS_OBJECT_STORAGE_S3_PATH_STYLE":    "true",
	}
	for key, value := range want {
		if env[key] != value {
			t.Errorf("%s = %q, want %q", key, env[key], value)
		}
	}
	secret := env["VERSITYGW_ROOT_SECRET_KEY"]
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(secret) {
		t.Fatal("generated root secret has unexpected shape")
	}
	if secrets.values["VERSITYGW_ROOT_SECRET_KEY"] != secret {
		t.Fatal("generated root secret was not persisted")
	}
	if env["ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"] != secret {
		t.Fatal("provider-neutral S3 output does not carry the generated secret")
	}

	second := baseEnv()
	secondSecrets := &secretStore{values: cloneMap(secrets.values)}
	if err := calculate("versitygw", second, "", secondSecrets); err != nil {
		t.Fatal(err)
	}
	if second["VERSITYGW_ROOT_SECRET_KEY"] != secret {
		t.Fatal("calculate replaced an existing generated root secret")
	}
}

func TestCalculateKeepsExplicitRootSecret(t *testing.T) {
	env := baseEnv()
	env["VERSITYGW_ROOT_SECRET_KEY"] = "an-explicit-root-secret"
	secrets := &secretStore{values: map[string]string{}}
	if err := calculate("versitygw", env, "", secrets); err != nil {
		t.Fatal(err)
	}
	if env["VERSITYGW_ROOT_SECRET_KEY"] != "an-explicit-root-secret" {
		t.Fatal("explicit root secret was replaced")
	}
	if len(secrets.values) != 0 {
		t.Fatal("explicit root secret was copied into generated Secret Store state")
	}
}

func TestCalculateIgnoresOtherModules(t *testing.T) {
	env := baseEnv()
	before := cloneMap(env)
	if err := calculate("another_module", env, "", &secretStore{values: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	for key, value := range before {
		if env[key] != value {
			t.Fatalf("other module changed %s", key)
		}
	}
}

func TestComposeAndEntrypointKeepTheS3BoundaryNarrow(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join("..", "module.yml"))
	if err != nil {
		t.Fatal(err)
	}
	manifestText := string(manifest)
	for _, required := range []string{
		"name: object_storage",
		"- s3",
		"ANAS_OBJECT_STORAGE_S3_ENDPOINT",
		"ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY",
	} {
		if !strings.Contains(manifestText, required) {
			t.Errorf("module manifest is missing %q", required)
		}
	}

	compose, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(compose)
	for _, required := range []string{
		"ghcr.io/anas-project}/anas-versitygw:1.7.0-r2",
		"VGW_BACKEND: posix",
		"VGW_IAM_DIR: /data/iam",
		`VGW_ADMIN_PORT: ":7071"`,
		"VGW_HEALTH: /_anas_health",
		`"${VERSITYGW_OBJECTS_PATH}:/data/objects"`,
		`"${VERSITYGW_IAM_PATH}:/data/iam"`,
		"anas_versitygw_provision:",
		"ADMIN_ENDPOINT_URL: http://${VERSITYGW_HOSTNAME}:7071",
		`"./providers/object_storage:/operations:ro"`,
		"cap_drop:",
		"- FOWNER",
		"no-new-privileges",
		"loadbalancer.server.port=7070",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("Compose is missing %q", required)
		}
	}
	for _, forbidden := range []string{"ports:", "USER_DATA_PATH", "traefik.http.routers.versitygw-admin", "VGW_WEBUI"} {
		if strings.Contains(content, forbidden) {
			t.Errorf("Compose unexpectedly contains %q", forbidden)
		}
	}

	entrypoint, err := os.ReadFile(filepath.Join("..", "versitygw", "anas-entrypoint.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(entrypoint)
	if !strings.Contains(script, `exec su-exec "$runtime_uid:$runtime_gid"`) {
		t.Fatal("entrypoint does not drop privileges before starting VersityGW")
	}
	if strings.Contains(script, "chown -R") {
		t.Fatal("entrypoint recursively rewrites object ownership")
	}

	dockerfile, err := os.ReadFile(filepath.Join("..", "versitygw", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	image := string(dockerfile)
	if !strings.Contains(image, "FROM ${GHCR_REGISTRY}/versity/versitygw:v1.7.0") ||
		!strings.Contains(image, "apk add --no-cache su-exec") {
		t.Fatal("derived image does not pin VersityGW v1.7.0 and its privilege-drop utility")
	}
	if strings.Contains(image, ":latest") {
		t.Fatal("derived image uses an unpinned latest tag")
	}

	provider, err := os.ReadFile(filepath.Join("..", "providers", "object_storage", "provision.sh"))
	if err != nil {
		t.Fatal(err)
	}
	providerScript := string(provider)
	for _, required := range []string{"create-user", "update-user", "create-bucket", "list-buckets", "--role user"} {
		if !strings.Contains(providerScript, required) {
			t.Errorf("object-storage provider is missing %q", required)
		}
	}
}
