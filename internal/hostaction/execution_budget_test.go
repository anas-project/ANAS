package hostaction

import (
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestInstallExecutionBudgetMatchesBackend(t *testing.T) {
	spec, ok := LookupAction(ActionInstall)
	if !ok || time.Duration(spec.Timeout)*time.Second != incusprovision.InstallTimeout {
		t.Fatalf("install action cannot accommodate its bounded package/service workflow: %ds", spec.Timeout)
	}
	if got := ExecutionTimeout(ActionInstall); got != incusprovision.InstallTimeout+5*time.Second+2*ioTimeout {
		t.Fatalf("client deadline would abandon the accepted installation: %v", got)
	}
}

func TestInstallBudgetDoesNotExtendReadOnlyOrOtherActions(t *testing.T) {
	for action, seconds := range map[string]int{
		ActionStatus: 5, ActionInstallPlan: 30,
		ActionConfigurePlan: 30, ActionEnrollPlan: 30, ActionUninstallPlan: 30,
		ActionConfigure: 600, ActionEnroll: 300, ActionUninstall: 600,
		ActionImagePrune: 600,
	} {
		spec, ok := LookupAction(action)
		if !ok || spec.Timeout != seconds {
			t.Errorf("unrelated action %s changed its execution budget: %ds", action, spec.Timeout)
		}
	}
}
