// TEST_CASES: MAILMOD-T-031
package installer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type fakeDeployment struct {
	fakeCloud
	creates                 int
	createErr, verifyErr    error
	vacant, matches, active bool
}

func (f *fakeDeployment) versionVacant(context.Context, string, string, string) (bool, error) {
	return f.vacant, f.verifyErr
}
func (f *fakeDeployment) createVersion(context.Context, string, string, versionConfig) (string, error) {
	f.creates++
	return "44444444-4444-4444-8444-444444444444", f.createErr
}
func (f *fakeDeployment) versionMatches(context.Context, string, string, string, versionConfig) (bool, error) {
	return f.matches, f.verifyErr
}
func (f *fakeDeployment) activeVersion(context.Context, string, string, string, string) (bool, error) {
	return f.active, f.verifyErr
}

func versionFixture() versionConfig {
	return versionConfig{Bucket: "anas-bucket", SourceID: "11111111-1111-4111-8111-111111111111",
		Generation: "22222222-2222-4222-8222-222222222222", OperationID: "33333333-3333-4333-8333-333333333333",
		Domains: []string{"example.test"}, CapacityBytes: 2 << 30, DeliveredDays: 7,
		Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64)}
}

func TestVersionPayloadHasFixedSourcesAndSeparatedCredentials(t *testing.T) {
	cfg := versionFixture()
	payload, err := versionPayload(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	// Decode the wire names rather than relying on Go's field mapping for snake_case.
	var body map[string]any
	if json.Unmarshal(b, &body) != nil || body["main_module"] != "worker.mjs" {
		t.Fatal("invalid main module")
	}
	modules, ok := body["modules"].([]any)
	if !ok || len(modules) != 2 {
		t.Fatal("fixed modules missing")
	}
	bindings, ok := body["bindings"].([]any)
	if !ok || len(bindings) != 8 {
		t.Fatal("unexpected bindings")
	}
	seen := map[string]map[string]any{}
	for _, value := range bindings {
		item, ok := value.(map[string]any)
		if !ok {
			t.Fatal("malformed binding")
		}
		name, _ := item["name"].(string)
		if seen[name] != nil {
			t.Fatal("duplicate binding")
		}
		seen[name] = item
	}
	if seen["MAIL"]["bucket_name"] != cfg.Bucket || seen["READER_TOKEN"]["type"] != "secret_text" ||
		seen["CONTROL_TOKEN"]["type"] != "secret_text" || seen["READER_TOKEN"]["text"] != cfg.Reader || seen["CONTROL_TOKEN"]["text"] != cfg.Control {
		t.Fatal("storage or credential separation missing")
	}
}

func TestVersionRejectsInvalidConfigurationBeforeCloudRequest(t *testing.T) {
	for _, mutate := range []func(*versionConfig){
		func(c *versionConfig) { c.Control = c.Reader },
		func(c *versionConfig) { c.Domains = []string{"Example.test"} },
		func(c *versionConfig) { c.Domains = []string{"example.test", "example.test"} },
		func(c *versionConfig) { c.CapacityBytes = 0 },
		func(c *versionConfig) { c.OperationID = "unknown" },
	} {
		cfg := versionFixture()
		mutate(&cfg)
		calls := 0
		c := cloudFixture(t, func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("should not call") })
		if _, err := c.createVersion(t.Context(), strings.Repeat("a", 32), strings.Repeat("b", 32), cfg); !errors.Is(err, ErrJournal) || calls != 0 {
			t.Fatal("invalid deployment reached provider", err, calls)
		}
	}
}

func TestVersionNotificationBinding(t *testing.T) {
	cfg := versionFixture()
	cfg.NotifyOrigin, cfg.NotifyToken = "https://notify.example.test:10000", strings.Repeat("c", 64)
	payload, err := versionPayload(cfg)
	if err != nil {
		t.Fatal(err)
	}
	object := payload.(map[string]any)
	bindings := object["bindings"].([]map[string]string)
	if len(bindings) != 10 || bindings[8]["name"] != "NOTIFY_ORIGIN" || bindings[8]["text"] != cfg.NotifyOrigin || bindings[9]["name"] != "NOTIFY_TOKEN" || bindings[9]["type"] != "secret_text" || bindings[9]["text"] != cfg.NotifyToken {
		t.Fatal("notification bindings invalid")
	}
	object["id"] = "44444444-4444-4444-8444-444444444444"
	wire, _ := json.Marshal(map[string]any{"success": true, "result": object})
	client := cloudFixture(t, func(*http.Request) (*http.Response, error) { return responseWith(200, string(wire)), nil })
	if ok, err := client.versionMatches(t.Context(), strings.Repeat("a", 32), strings.Repeat("b", 32), object["id"].(string), cfg); err != nil || !ok {
		t.Fatal("notification readback rejected", err)
	}
	bindings[8]["text"] = "https://other.example.test"
	wire, _ = json.Marshal(map[string]any{"success": true, "result": object})
	if ok, err := client.versionMatches(t.Context(), strings.Repeat("a", 32), strings.Repeat("b", 32), object["id"].(string), cfg); err != nil || ok {
		t.Fatal("notification origin drift accepted", err)
	}
	for _, mutate := range []func(*versionConfig){
		func(c *versionConfig) { c.NotifyOrigin = "" },
		func(c *versionConfig) { c.NotifyToken = "" },
		func(c *versionConfig) { c.NotifyToken = c.Reader },
		func(c *versionConfig) { c.NotifyOrigin = "http://notify.example.test" },
		func(c *versionConfig) { c.NotifyOrigin = "https://notify.example.test/api/ingress/notify" },
		func(c *versionConfig) { c.NotifyOrigin = "https://notify.example.test?token=secret" },
		func(c *versionConfig) { c.NotifyOrigin = "https://notify.example.test:65536" },
		func(c *versionConfig) { c.NotifyOrigin = "https://notify.example.test:0" },
	} {
		bad := cfg
		mutate(&bad)
		if bad.valid() {
			t.Fatal("invalid notification configuration accepted")
		}
	}
}

func TestVersionUsesImmutableWorkerIDAndDoesNotExposeErrors(t *testing.T) {
	account, worker := strings.Repeat("a", 32), strings.Repeat("b", 32)
	version := "44444444-4444-4444-8444-444444444444"
	requests := 0
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		requests++
		if r.URL.Host != "api.cloudflare.com" || r.URL.Path != "/client/v4/accounts/"+account+"/workers/workers/"+worker+"/versions" || r.URL.RawQuery != "deploy=true" {
			t.Fatal("unexpected version target")
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !strings.Contains(string(body), "secret_text") {
			t.Fatal("version payload missing")
		}
		return responseWith(200, `{"success":true,"result":{"id":"`+version+`"}}`), nil
	})
	id, err := c.createVersion(t.Context(), account, worker, versionFixture())
	if err != nil || id != version || requests != 1 {
		t.Fatal("version request failed", err)
	}
	c = cloudFixture(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("synthetic-provider-secret") })
	if _, err := c.createVersion(t.Context(), account, worker, versionFixture()); !errors.Is(err, ErrUnknown) || strings.Contains(err.Error(), "synthetic-provider-secret") {
		t.Fatal("provider error leaked or misclassified", err)
	}
}

func TestVersionReadbackChecksCodeAndBindings(t *testing.T) {
	account, worker := strings.Repeat("a", 32), strings.Repeat("b", 32)
	version := "44444444-4444-4444-8444-444444444444"
	cfg := versionFixture()
	payload, err := versionPayload(cfg)
	if err != nil {
		t.Fatal(err)
	}
	object := payload.(map[string]any)
	object["id"] = version
	wire, _ := json.Marshal(map[string]any{"success": true, "result": object})
	c := cloudFixture(t, func(*http.Request) (*http.Response, error) { return responseWith(200, string(wire)), nil })
	if ok, err := c.versionMatches(t.Context(), account, worker, version, cfg); err != nil || !ok {
		t.Fatal("matching readback rejected", ok, err)
	}
	modules := object["modules"].([]map[string]string)
	modules[0]["content_base64"] = "Y2hhbmdlZA=="
	wire, _ = json.Marshal(map[string]any{"success": true, "result": object})
	if ok, err := c.versionMatches(t.Context(), account, worker, version, cfg); err != nil || ok {
		t.Fatal("changed source accepted", ok, err)
	}
}

func TestActiveVersionRequiresCompleteUnambiguousReadback(t *testing.T) {
	account, worker := strings.Repeat("a", 32), strings.Repeat("b", 32)
	version := "44444444-4444-4444-8444-444444444444"
	deployment := "55555555-5555-4555-8555-555555555555"
	resultInfo := `,"result_info":{"total_pages":1,"total_count":1}`
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/workers/workers/"+worker) {
			return responseWith(200, `{"success":true,"result":{"id":"`+worker+`","name":"anas-worker"}}`), nil
		}
		if !strings.HasSuffix(r.URL.Path, "/workers/scripts/anas-worker/deployments") {
			t.Fatal("unexpected readback path")
		}
		return responseWith(200, `{"success":true,"result":{"deployments":[{"id":"`+deployment+`","created_on":"2026-10-27T11:00:00Z","versions":[{"version_id":"`+version+`","percentage":100}]}]}`+resultInfo+`}`), nil
	})
	if ok, err := c.activeVersion(t.Context(), account, worker, "anas-worker", version); err != nil || !ok {
		t.Fatal("valid deployment not confirmed", ok, err)
	}
	resultInfo = `,"result_info":{"total_pages":2,"total_count":1}`
	if ok, err := c.activeVersion(t.Context(), account, worker, "anas-worker", version); !errors.Is(err, ErrUnknown) || ok {
		t.Fatal("incomplete deployment history accepted", ok, err)
	}
}

func TestVacantWorkerRequiresCompleteEmptyDeploymentList(t *testing.T) {
	account, worker := strings.Repeat("a", 32), strings.Repeat("b", 32)
	body := `{"success":true,"result":{"deployments":[]},"result_info":{"total_pages":0,"total_count":0}}`
	c := cloudFixture(t, func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/workers/workers/"+worker) {
			return responseWith(200, `{"success":true,"result":{"id":"`+worker+`","name":"anas-worker"}}`), nil
		}
		return responseWith(200, body), nil
	})
	if ok, err := c.versionVacant(t.Context(), account, worker, "anas-worker"); err != nil || !ok {
		t.Fatal("empty Worker not confirmed", ok, err)
	}
	body = `{"success":true,"result":{"deployments":[]}}`
	if ok, err := c.versionVacant(t.Context(), account, worker, "anas-worker"); !errors.Is(err, ErrUnknown) || ok {
		t.Fatal("incomplete listing treated as vacant", ok, err)
	}
}

func TestVersionCoordinatorPersistsIntentAndNeverRetriesUnknown(t *testing.T) {
	path, spec := fixture(t)
	cfg := versionFixture()
	client := &fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, vacant: true, matches: true, active: true,
		createErr: errors.New("simulated lost response")}
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrUnknown) || client.creates != 1 {
		t.Fatal("lost reply not preserved")
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil || strings.Contains(string(content), cfg.Reader) || strings.Contains(string(content), cfg.Control) {
		t.Fatal("journal contains a source credential")
	}
	j, err = OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	client.createErr = nil
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrUnknown) || client.creates != 1 {
		t.Fatal("unknown version was deployed again")
	}
}

func TestVersionCoordinatorRequiresVacancyAndRechecksCompletedVersion(t *testing.T) {
	path, spec := fixture(t)
	cfg := versionFixture()
	client := &fakeDeployment{fakeCloud: fakeCloud{workerOK: true, bucketOK: true}, matches: true, active: true}
	j, err := OpenJournal(path, spec)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	if err = j.ReserveResources(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrConflict) || client.creates != 0 {
		t.Fatal("occupied Worker overwritten")
	}
	client.vacant = true
	if err = j.deployVersion(t.Context(), client, cfg); err != nil || client.creates != 1 {
		t.Fatal("version not confirmed", err)
	}
	if err = j.deployVersion(t.Context(), client, cfg); err != nil || client.creates != 1 {
		t.Fatal("version deployed again", err)
	}
	client.active = false
	if !errors.Is(j.deployVersion(t.Context(), client, cfg), ErrConflict) || client.creates != 1 {
		t.Fatal("active deployment drift ignored")
	}
	changed := cfg
	changed.DeliveredDays++
	if !errors.Is(j.deployVersion(t.Context(), client, changed), ErrConflict) {
		t.Fatal("changed plan silently adopted")
	}
}
