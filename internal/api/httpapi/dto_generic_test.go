package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/application"
	"github.com/anas-project/ANAS/internal/deployment"
)

// The daemon projects deployment data by schema, not by a catalogue of known
// Module names. A newly installed or third-party Module must therefore appear
// without requiring an anasd release or an adapter dedicated to that Module.
func TestDeploymentDTOProjectsArbitraryModulesGenerically(t *testing.T) {
	manifest := &deployment.Manifest{
		Modules: map[string]deployment.Module{
			"future-module": {
				Name: "future-module", Version: "9.1.0", Revision: 7,
				RuntimeType: "compose", Dependencies: []string{"third_party"},
			},
			"third_party": {
				Name: "third_party", Version: "1.2.3", Revision: 4,
				RuntimeType: "builtin", Consumes: []string{"SHARED_*"},
			},
		},
	}

	document := newDeploymentDTO(manifest)
	if len(document.Modules) != len(manifest.Modules) {
		t.Fatalf("projected %d Modules, want %d", len(document.Modules), len(manifest.Modules))
	}
	for name, source := range manifest.Modules {
		projected, ok := document.Modules[name]
		if !ok {
			t.Fatalf("arbitrary Module %q was omitted", name)
		}
		if projected.Name != source.Name || projected.Version != source.Version ||
			projected.Revision != source.Revision || projected.RuntimeType != source.RuntimeType {
			t.Fatalf("Module %q projection = %+v, want generic projection of %+v", name, projected, source)
		}
	}
}

func TestHandlerSerializesArbitraryModulesWithoutDedicatedAdapters(t *testing.T) {
	registry, _ := testRegistry(t, "main")
	dataBreaking := []string{}
	manifest := &deployment.Manifest{
		APIVersion:        deployment.ManifestAPIVersion,
		ID:                "dep-1",
		ConfigFingerprint: "sha256:config-secret",
		ModuleOrder:       []string{"future-module", "third_party"},
		Modules: map[string]deployment.Module{
			"future-module": {
				Name: "future-module", Version: "9.1.0", Revision: 7,
				RuntimeType: "compose", Dependencies: []string{"third_party"},
				Consumes: []string{"SHARED_*"}, DataBreaking: &dataBreaking,
				ComposeFile: "/private/future/compose.yml", RenderDigest: "sha256:render-secret",
			},
			"third_party": {Name: "third_party", Version: "1.2.3", RuntimeType: "builtin"},
		},
		Settings: map[string]deployment.Setting{
			"future-module.token": {
				Module: "future-module", Parameter: "token", Effect: "container_recreate",
				Fingerprint: "sha256:setting-secret",
			},
		},
	}
	service := &fakeQueryService{inspect: application.InspectDeploymentResult{
		Deployment: manifest,
		State:      deployment.State{ID: "dep-1", Status: "ready"},
	}}
	handler := NewHandler(registry, func(string) QueryService { return service })
	response := serveRequest(handler, http.MethodGet, "/api/v1/workspaces/main/deployments/dep-1")
	if response.Code != http.StatusOK {
		t.Fatalf("detail response = %d, %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{
		"/private/future", "config_fingerprint", "sha256:config-secret",
		"render_digest", "sha256:render-secret", `"fingerprint"`, "sha256:setting-secret",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("generic detail leaked %q: %s", forbidden, body)
		}
	}
	var wire map[string]any
	decodeResponse(t, response, &wire)
	deploymentWire, ok := wire["deployment"].(map[string]any)
	if !ok {
		t.Fatalf("wire deployment = %#v", wire["deployment"])
	}
	modulesWire, ok := deploymentWire["modules"].(map[string]any)
	if !ok {
		t.Fatalf("wire modules = %#v", deploymentWire["modules"])
	}
	for _, name := range []string{"future-module", "third_party"} {
		moduleWire, ok := modulesWire[name].(map[string]any)
		if !ok || moduleWire["name"] != name || moduleWire["runtime"] == nil {
			t.Fatalf("generic wire Module %q = %#v", name, modulesWire[name])
		}
	}
	var document deploymentDetailResponse
	decodeResponse(t, response, &document)
	if len(document.Deployment.Modules) != 2 || len(document.Deployment.ModuleOrder) != 2 {
		t.Fatalf("generic module projection = %#v", document.Deployment)
	}
	future := document.Deployment.Modules["future-module"]
	if future.AppVersion != nil || future.DataBreaking == nil || *future.DataBreaking == nil ||
		len(future.Consumes) != 1 || len(future.Dependencies) != 1 {
		t.Fatalf("generic nullable/slice projection = %#v", future)
	}
	thirdParty := document.Deployment.Modules["third_party"]
	if thirdParty.Consumes == nil || thirdParty.Dependencies == nil {
		t.Fatalf("empty generic slices serialized from nil: %#v", thirdParty)
	}
}
