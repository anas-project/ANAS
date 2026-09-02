package runner

import (
	"reflect"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
)

func TestParseComposePSRecordsSupportsArrayAndJSONLines(t *testing.T) {
	t.Parallel()
	want := []composePSRecord{
		{Service: "api", State: "running", Health: "healthy"},
		{Service: "worker", State: "exited", Health: ""},
	}
	for _, body := range []string{
		`[{"Service":"api","State":"running","Health":"healthy","Name":"ignored"},{"Service":"worker","State":"exited","Health":""}]`,
		"{\"Service\":\"api\",\"State\":\"running\",\"Health\":\"healthy\",\"Name\":\"ignored\"}\n{\"Service\":\"worker\",\"State\":\"exited\",\"Health\":\"\"}\n",
	} {
		got, err := parseComposePSRecords([]byte(body))
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("records = %#v, want %#v", got, want)
		}
	}
}

func TestSummarizeRuntimeUsesLiveContainersAndHealth(t *testing.T) {
	t.Parallel()
	healthy := summarizeModuleRuntime("healthy", []composePSRecord{{State: "running", Health: "healthy"}})
	unhealthy := summarizeModuleRuntime("unhealthy", []composePSRecord{{State: "running", Health: "unhealthy"}})
	partial := summarizeModuleRuntime("partial", []composePSRecord{{State: "running"}, {State: "exited"}})
	stopped := summarizeModuleRuntime("stopped", nil)
	if healthy.Runtime != "running" || healthy.Health != "healthy" || unhealthy.Health != "unhealthy" || partial.Runtime != "degraded" || stopped.Runtime != "stopped" {
		t.Fatalf("summaries = %#v %#v %#v %#v", healthy, unhealthy, partial, stopped)
	}

	deployment := summarizeDeploymentRuntime([]application.ModuleRuntimeStatus{healthy, unhealthy})
	if deployment.Status != "running" || deployment.Healthy == nil || *deployment.Healthy {
		t.Fatalf("deployment = %#v", deployment)
	}
	degraded := summarizeDeploymentRuntime([]application.ModuleRuntimeStatus{healthy, partial, stopped})
	if degraded.Status != "degraded" {
		t.Fatalf("degraded deployment = %#v", degraded)
	}
}
