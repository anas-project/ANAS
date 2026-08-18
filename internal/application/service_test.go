package application

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/deployment"
)

func TestVersionAndFreshWorkspaceStatus(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	service := NewService(workspace)

	version, err := service.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version.Version == "" || version.Commit == "" || version.Date == "" {
		t.Fatalf("version = %#v", version)
	}

	status, err := service.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Workspace != workspace || status.ActiveDeployment != nil || status.ActivatedAt != nil || status.VerifiedAt != nil {
		t.Fatalf("status = %#v", status)
	}
	if status.PreviousDeployments == nil || len(status.PreviousDeployments) != 0 {
		t.Fatalf("previous deployments = %#v", status.PreviousDeployments)
	}
}

func TestStatusMapsPersistedEmptyStringsToNull(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "state", "active.yml"), []byte("api_version: anas.state/v2\nactive_deployment: dep-2\nruntime_status: running\nprevious_deployments: [dep-1]\nactivated_at: 2026-08-18T01:02:03Z\n"))

	status, err := NewService(workspace).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ActiveDeployment == nil || *status.ActiveDeployment != "dep-2" || status.RuntimeStatus == nil || *status.RuntimeStatus != "running" {
		t.Fatalf("status = %#v", status)
	}
	if status.VerifiedAt != nil || status.Transaction != nil {
		t.Fatalf("empty optional values were not null: %#v", status)
	}
	if !reflect.DeepEqual(status.PreviousDeployments, []string{"dep-1"}) {
		t.Fatalf("previous = %v", status.PreviousDeployments)
	}
}

func TestListDeploymentsPaginatesStableDescendingOrder(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".anas", "state", "deployments")
	writeApplicationFile(t, filepath.Join(dir, "c.yml"), applicationStateYAML("c", "2026-08-18T02:00:00Z"))
	writeApplicationFile(t, filepath.Join(dir, "b.yml"), applicationStateYAML("b", "2026-08-18T03:00:00Z"))
	writeApplicationFile(t, filepath.Join(dir, "a.yml"), applicationStateYAML("a", "2026-08-18T03:00:00Z"))
	service := NewService(workspace)

	first, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateIDs(first.Deployments); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("first page = %v", got)
	}
	if first.NextCursor == nil {
		t.Fatal("first page has no next cursor")
	}
	hugeLimit, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{
		Limit:  int(^uint(0) >> 1),
		Cursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateIDs(hugeLimit.Deployments); !reflect.DeepEqual(got, []string{"c"}) || hugeLimit.NextCursor != nil {
		t.Fatalf("huge-limit page = %v, cursor %#v", got, hugeLimit.NextCursor)
	}
	second, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{Limit: 2, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateIDs(second.Deployments); !reflect.DeepEqual(got, []string{"c"}) {
		t.Fatalf("second page = %v", got)
	}
	if second.NextCursor != nil {
		t.Fatalf("last page cursor = %q", *second.NextCursor)
	}

	all, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Deployments) != 3 || all.Deployments == nil || all.NextCursor != nil {
		t.Fatalf("unpaged result = %#v", all)
	}
}

func TestListDeploymentsPaginatesLegacyStatesWithoutCreatedAt(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".anas", "state", "deployments")
	writeApplicationFile(t, filepath.Join(dir, "a.yml"), applicationStateYAML("a", ""))
	writeApplicationFile(t, filepath.Join(dir, "b.yml"), applicationStateYAML("b", ""))
	service := NewService(workspace)

	first, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateIDs(first.Deployments); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("first page = %v", got)
	}
	if first.NextCursor == nil {
		t.Fatal("first page has no next cursor")
	}

	second, err := service.ListDeployments(context.Background(), ListDeploymentsRequest{Limit: 1, Cursor: *first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateIDs(second.Deployments); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("second page = %v", got)
	}
	if second.NextCursor != nil {
		t.Fatalf("last page cursor = %q", *second.NextCursor)
	}
}

func TestListDeploymentsRejectsInvalidPagination(t *testing.T) {
	t.Parallel()
	service := NewService(t.TempDir())
	for _, req := range []ListDeploymentsRequest{{Limit: -1}, {Cursor: "not-base64!"}} {
		_, err := service.ListDeployments(context.Background(), req)
		appErr := requireApplicationError(t, err)
		if appErr.Kind != ErrorKindInvalidArgument {
			t.Errorf("kind = %q, want %q", appErr.Kind, ErrorKindInvalidArgument)
		}
	}
}

func TestInspectDeploymentPreservesRawManifest(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	raw := []byte("# exact bytes for human output\napi_version: anas.deployment/v1\nid: dep-1\ncreated_at: 2026-08-18T01:00:00Z\nconfig_fingerprint: sha256:abc\nmodule_order: []\nmodules: {}\n")
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "deployments", "dep-1", "deployment.yml"), raw)

	result, err := NewService(workspace).InspectDeployment(context.Background(), InspectDeploymentRequest{DeploymentID: "dep-1"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Workspace != workspace || result.DeploymentPath != filepath.Join(workspace, ".anas", "deployments", "dep-1") {
		t.Fatalf("paths = %#v", result)
	}
	if result.Deployment == nil || result.Deployment.ID != "dep-1" {
		t.Fatalf("deployment = %#v", result.Deployment)
	}
	if !bytes.Equal(result.RawManifest, raw) {
		t.Fatalf("raw manifest changed: %q", result.RawManifest)
	}
	if result.State.APIVersion != deployment.StateAPIVersion || result.State.ID != "dep-1" {
		t.Fatalf("state = %#v", result.State)
	}
}

func TestInspectDeploymentClassifiesInvalidAndMissingIDs(t *testing.T) {
	t.Parallel()
	service := NewService(t.TempDir())
	tests := []struct {
		id   string
		kind ErrorKind
		code string
	}{
		{"../outside", ErrorKindInvalidArgument, "invalid_deployment_id"},
		{"missing", ErrorKindNotFound, "deployment_missing"},
	}
	for _, test := range tests {
		_, err := service.InspectDeployment(context.Background(), InspectDeploymentRequest{DeploymentID: test.id})
		appErr := requireApplicationError(t, err)
		if appErr.Kind != test.kind || appErr.Code != test.code {
			t.Errorf("%q: error = %#v", test.id, appErr)
		}
	}
}

func TestInspectDeploymentReturnsRawBytesWithManifestError(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	raw := []byte("api_version: [broken\n")
	writeApplicationFile(t, filepath.Join(workspace, ".anas", "deployments", "broken", "deployment.yml"), raw)

	result, err := NewService(workspace).InspectDeployment(context.Background(), InspectDeploymentRequest{DeploymentID: "broken"})
	appErr := requireApplicationError(t, err)
	if appErr.Code != "deployment_missing" || appErr.Kind != ErrorKindFailedPrecondition {
		t.Fatalf("error = %#v", appErr)
	}
	if !bytes.Equal(result.RawManifest, raw) {
		t.Fatalf("raw = %q, want %q", result.RawManifest, raw)
	}
}

func TestServiceHonorsCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := NewService(t.TempDir())

	if _, err := service.Version(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Version error = %v", err)
	}
	if _, err := service.Status(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Status error = %v", err)
	}
	if _, err := service.ListDeployments(ctx, ListDeploymentsRequest{}); !errors.Is(err, context.Canceled) {
		t.Errorf("ListDeployments error = %v", err)
	}
	if _, err := service.InspectDeployment(ctx, InspectDeploymentRequest{DeploymentID: "dep"}); !errors.Is(err, context.Canceled) {
		t.Errorf("InspectDeployment error = %v", err)
	}
}

func TestErrorOfFindsWrappedApplicationError(t *testing.T) {
	t.Parallel()
	cause := errors.New("disk failed")
	original := newError(ErrorKindInternal, "state_unreadable", "read state", cause)
	got, ok := ErrorOf(errors.Join(errors.New("outer"), original))
	if !ok || got != original || !errors.Is(got, cause) {
		t.Fatalf("ErrorOf = %#v, %v", got, ok)
	}
}

func requireApplicationError(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("operation succeeded")
	}
	appErr, ok := ErrorOf(err)
	if !ok {
		t.Fatalf("error %T is not *application.Error: %v", err, err)
	}
	return appErr
}

func stateIDs(states []deployment.State) []string {
	out := make([]string, 0, len(states))
	for _, state := range states {
		out = append(out, state.ID)
	}
	return out
}

func writeApplicationFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func applicationStateYAML(id, createdAt string) []byte {
	return []byte("api_version: anas.state/v2\nid: " + id + "\nstatus: active\ncreated_at: " + createdAt + "\n")
}
