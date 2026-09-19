package hostaction

import (
	"github.com/anas-project/ANAS/internal/actionabi"
	"testing"
)

func TestProvisionProgressAllowsOnlyCompiledPhaseWithoutFreeText(t *testing.T) {
	for _, action := range ApplyActionNames() {
		spec, _ := LookupAction(action)
		e := actionabi.Event{ABI: actionabi.Version, JobID: "job", InvocationID: "call", Type: "progress", Progress: &actionabi.Progress{Phase: string(spec.Phase)}}
		if _, err := ProjectActionEvent(action, e); err != nil {
			t.Fatal(action, err)
		}
		if _, err := ProjectActionEvent(ActionStatus, e); err == nil {
			t.Fatal("status inherited mutating progress")
		}
		for _, mode := range []string{"text", "unit", "counter"} {
			p := actionabi.Progress{Phase: string(spec.Phase)}
			switch mode {
			case "text":
				p.Phase = "PRIVATE-STDERR"
			case "unit":
				p.Unit = "PRIVATE-VALUE"
			case "counter":
				n := uint64(100)
				p.Current = &n
			}
			e.Progress = &p
			if _, err := ProjectActionEvent(action, e); err == nil {
				t.Fatal("unapproved progress reached journal")
			}
		}
	}
}
