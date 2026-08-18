package httpapi

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestOpenAPITracksImplementedReadOnlySurface(t *testing.T) {
	document := readOpenAPIDocument(t)
	if got := document["openapi"]; got != "3.1.0" {
		t.Fatalf("openapi = %v", got)
	}

	paths := objectAt(t, document, "paths")
	gotPaths := make([]string, 0, len(paths))
	for path, raw := range paths {
		gotPaths = append(gotPaths, path)
		pathItem := raw.(map[string]any)
		if _, ok := pathItem["get"]; !ok {
			t.Errorf("%s does not declare GET", path)
		}
		for method := range pathItem {
			if method != "get" && method != "parameters" {
				t.Errorf("%s unexpectedly declares %s in read-only M0", path, method)
			}
		}
	}
	sort.Strings(gotPaths)
	wantPaths := []string{
		"/api/v1/system",
		"/api/v1/workspaces/{ws}/deployments",
		"/api/v1/workspaces/{ws}/deployments/{id}",
		"/api/v1/workspaces/{ws}/status",
		"/healthz",
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("OpenAPI paths = %v, want %v", gotPaths, wantPaths)
	}
	wantResponses := map[string][]string{
		"/healthz":                                 {"200", "400", "405"},
		"/api/v1/system":                           {"200", "400", "405", "408", "500", "504"},
		"/api/v1/workspaces/{ws}/status":           {"200", "400", "404", "405", "408", "500", "504"},
		"/api/v1/workspaces/{ws}/deployments":      {"200", "400", "404", "405", "408", "500", "504"},
		"/api/v1/workspaces/{ws}/deployments/{id}": {"200", "400", "404", "405", "408", "412", "500", "504"},
	}
	for path, want := range wantResponses {
		pathItem := objectAt(t, paths, path)
		operation := objectAt(t, pathItem, "get")
		responses := objectAt(t, operation, "responses")
		got := make([]string, 0, len(responses))
		for status := range responses {
			got = append(got, status)
		}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s responses = %v, want %v", path, got, want)
		}
	}

	components := objectAt(t, document, "components")
	schemas := objectAt(t, components, "schemas")
	assertRequiredPropertiesAreUnique(t, schemas)
	apiVersion := objectAt(t, schemas, "APIVersion")
	if got := apiVersion["const"]; got != APIVersion {
		t.Fatalf("OpenAPI API version = %v, handler = %q", got, APIVersion)
	}
	responses := objectAt(t, components, "responses")
	for _, name := range []string{
		"BadRequestProblem", "NotFoundProblem", "PreconditionProblem",
		"MethodNotAllowedProblem", "RequestCanceledProblem", "InternalProblem", "DeadlineProblem",
	} {
		response := objectAt(t, responses, name)
		content := objectAt(t, response, "content")
		if _, ok := content["application/problem+json"]; !ok {
			t.Errorf("components.responses.%s does not use application/problem+json", name)
		}
	}
}

func assertRequiredPropertiesAreUnique(t *testing.T, schemas map[string]any) {
	t.Helper()
	for schemaName, rawSchema := range schemas {
		schema, ok := rawSchema.(map[string]any)
		if !ok {
			continue
		}
		required, ok := schema["required"].([]any)
		if !ok {
			continue
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s declares required fields without properties", schemaName)
			continue
		}
		seen := make(map[string]struct{}, len(required))
		for _, rawName := range required {
			name, ok := rawName.(string)
			if !ok {
				t.Errorf("%s has non-string required field %v", schemaName, rawName)
				continue
			}
			if _, duplicate := seen[name]; duplicate {
				t.Errorf("%s repeats required field %q", schemaName, name)
			}
			seen[name] = struct{}{}
			if _, exists := properties[name]; !exists {
				t.Errorf("%s requires undeclared property %q", schemaName, name)
			}
		}
	}
}

func TestOpenAPIDeploymentProjectionOmitsSensitivePersistedFields(t *testing.T) {
	document := readOpenAPIDocument(t)
	schemas := objectAt(t, objectAt(t, document, "components"), "schemas")

	assertPropertiesAbsent(t, schemas, "Deployment", "config_fingerprint")
	assertPropertiesAbsent(t, schemas, "DeploymentModule",
		"render_digest", "compose_file", "hook", "changes", "contract_providers", "local_accounts")
	assertPropertiesAbsent(t, schemas, "DeploymentSetting", "fingerprint")
	assertPropertiesAbsent(t, schemas, "DeploymentResource", "spec", "password_secret")
	assertPropertiesAbsent(t, schemas, "DeploymentSnapshotPolicy", "source", "root")
	assertPropertiesAbsent(t, schemas, "DeploymentDetailResponse", "workspace", "deployment_path")
}

func assertPropertiesAbsent(t *testing.T, schemas map[string]any, schemaName string, names ...string) {
	t.Helper()
	properties := objectAt(t, objectAt(t, schemas, schemaName), "properties")
	for _, name := range names {
		if _, ok := properties[name]; ok {
			t.Errorf("%s exposes persisted field %q", schemaName, name)
		}
	}
}

func readOpenAPIDocument(t *testing.T) map[string]any {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate OpenAPI test source")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "api", "openapi.yaml"))
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(body, &document); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return document
}

func objectAt(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want object", key, object[key])
	}
	return value
}
