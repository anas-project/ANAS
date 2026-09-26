package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/anas-project/ANAS/internal/hostaction"
	"github.com/anas-project/ANAS/internal/incusprovision"
)

func TestForwardingPlanTakesWorkspaceFromAuthorizedRoute(t *testing.T){
	for _,body:=range []string{
		`{"consumer":"forgejo","resource":"runners","operation":"enable","destinations":[{"ipv4":"192.0.2.12","port":8080}]}`,
		`{"consumer":"forgejo","resource":"runners","operation":"disable","destinations":[]}`,
		`{"consumer":"forgejo","resource":"runners","operation":"retire","destinations":[]}`,
	}{
		p,err:=hostPlanParameters(hostaction.ActionForwardingPlan,"main",json.RawMessage(body));if err!=nil{t.Fatal(err)}
		canonical,err:=hostaction.CanonicalParameters(hostaction.ActionForwardingPlan,p);if err!=nil{t.Fatal(err)}
		var r incusprovision.ForwardingPermissionRequest
		if json.Unmarshal(canonical,&r)!=nil || r.WorkspaceID!="main" || r.Validate()!=nil {t.Fatal("route lost authorization binding")}
	}
	for _,body:=range []string{
		`{"workspace_id":"other","consumer":"forgejo","resource":"runners","operation":"disable","destinations":[]}`,
		`{"consumer":"forgejo","resource":"runners","operation":"enable","destinations":[]}`,
		`{"consumer":"forgejo","resource":"runners","operation":"enable","destinations":[{"ipv4":"192.0.2.12/24","port":8080}]}`,
		`{"consumer":"forgejo","resource":"runners","operation":"disable","runtime_owner_ready":true}`,
		`{"consumer":"forgejo","resource":"runners","Operation":"disable"}`,
	}{if _,err:=hostPlanParameters(hostaction.ActionForwardingPlan,"main",json.RawMessage(body));err==nil {t.Fatal("injected or ambiguous input reached host action")}}
}
