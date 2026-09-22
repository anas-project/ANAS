package runner

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type observerCLIClient struct {
	fakeHostConsoleClient
	plans int
}

func (c *observerCLIClient) InvokeIncusPlan(_ context.Context, workspace, phase string, request json.RawMessage, key string) (map[string]any, string, error) {
	c.plans++
	if workspace != "main" || phase != "observer" || key != "cli-test-key" || string(request) != `{"operation":"refresh"}` {
		return nil, "", errors.New("invalid observer fixture request")
	}
	return map[string]any{"job": map[string]any{"id": "observer-plan", "status": "queued"}}, "/api/v1/jobs/observer-plan", nil
}
func TestObserverConfigurationCLIUsesExistingHTTPSPlanPath(t *testing.T) {
	client := &observerCLIClient{}
	restore := replaceHostConsoleClient(t, client, nil)
	defer restore()
	out, stderr, code := captureWithStdin(t, "", "host", "incus-plan", "-w", "main", "--phase", "observer", "--request-json", `{"operation":"refresh"}`, "--session-json", "-", "--idempotency-key", "cli-test-key", "--json")
	if code != 0 || stderr != "" || client.plans != 1 || !strings.Contains(out, "incus.ingress.observer.plan") {
		t.Fatal(code, out, stderr)
	}
	if !validCLIIncusAction("incus.ingress.observer") || validCLIIncusPhase("../observer") {
		t.Fatal("invalid CLI action boundary")
	}
}
