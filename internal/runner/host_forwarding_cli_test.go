package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type forwardingCLIClient struct { fakeHostConsoleClient; plans int }

func(c *forwardingCLIClient) InvokeIncusPlan(_ context.Context,workspace,phase string,request json.RawMessage,key string)(map[string]any,string,error){
	c.plans++
	var input struct {Consumer,Resource,Operation string;Destinations []any}
	if json.Unmarshal(request,&input)!=nil || input.Consumer!="forgejo" || input.Resource!="runners" || input.Operation!="disable" || workspace!="main" || phase!="forwarding-permission" || key!="cli-test-key" {return nil,"",errors.New("unexpected forwarding input")}
	return map[string]any{"job":map[string]any{"id":"forwarding-plan","status":"queued"}},"/api/v1/jobs/forwarding-plan",nil
}

func TestForwardingCLIUsesExistingAuthenticatedPlanTransport(t *testing.T){
	client:=&forwardingCLIClient{};restore:=replaceHostConsoleClient(t,client,nil);defer restore()
	out,stderr,code:=captureWithStdin(t,"","host","incus-plan","-w","main","--phase","forwarding-permission","--request-json",`{"consumer":"forgejo","resource":"runners","operation":"disable","destinations":[]}`,"--session-json","-","--idempotency-key","cli-test-key","--json")
	if code!=0 || stderr!="" || client.plans!=1 || !strings.Contains(out,"incus.forwarding.permission.plan"){t.Fatal(code,out,stderr)}
	if !validCLIIncusAction("incus.forwarding.permission") || validCLIIncusPhase("../forwarding-permission") || validCLIIncusAction("incus.forwarding.command"){t.Fatal("unbounded CLI action")}
}
