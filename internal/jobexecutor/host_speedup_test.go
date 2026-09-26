package jobexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestInstallPlanFreezesWorkspaceChineseSpeedup(t *testing.T) {
	for _, want := range []bool{false, true} {
		s, _, _, _ := serviceFixture(t, nil)
		s.available = true
		calls := 0
		s.options.ChineseSpeedup = func(_ context.Context, workspace string) (bool, error) {
			calls++
			if workspace != "main" {
				t.Fatal("wrong configuration scope")
			}
			return want, nil
		}
		input, _ := json.Marshal(hostaction.IncusPlanParameters{Schema: "anas.host-action.incus/v1", Request: incusprovision.Request{ChineseSpeedup: !want}})
		job, err := s.InvokePlan(context.Background(), "alice", "main", hostaction.ActionInstallPlan, input, "plan")
		if err != nil {
			t.Fatal(err)
		}
		wire, err := parametersForExecution(job.Job)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := hostaction.DecodePlanParameters(hostaction.ActionInstallPlan, wire)
		if err != nil || plan.Request.ChineseSpeedup != want || calls != 1 {
			t.Fatalf("workspace policy did not reach durable job: %+v, %v, calls=%d", plan, err, calls)
		}
	}
}

func TestInstallPlanConfigFailureCannotQueueAndSkipNeedsNoConfig(t *testing.T) {
	s, _, _, _ := serviceFixture(t, nil)
	s.available = true
	calls := 0
	s.options.ChineseSpeedup = func(context.Context, string) (bool, error) { calls++; return false, errors.New("private config path") }
	_, err := s.InvokePlan(context.Background(), "alice", "main", hostaction.ActionInstallPlan, json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{}}`), "bad")
	if !errors.Is(err, hostaction.ErrUnavailable) {
		t.Fatalf("config failure not closed: %v", err)
	}
	_, err = s.InvokePlan(context.Background(), "alice", "main", hostaction.ActionInstallPlan, json.RawMessage(`{"schema":"anas.host-action.incus/v1","request":{"skip":true}}`), "skip")
	if err != nil || calls != 1 {
		t.Fatalf("skip depends on config: %v", err)
	}
}
