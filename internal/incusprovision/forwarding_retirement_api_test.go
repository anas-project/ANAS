package incusprovision

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/computeclient"
)

func TestForwardingRetirementNeedsCompleteRevokedAndEmptyScope(t *testing.T) {
	for _, scenario := range []string{"valid", "revoked-still-listed", "replacement-credential", "foreign-global-credential", "missing-restricted", "missing-manager", "duplicate-credential", "null-certificates", "stopped-instance", "foreign-named-instance", "null-instances", "pending-operation", "completed-operation", "failed-operation", "null-operations", "null-operation-group", "project-owner", "network-owner", "wrong-subnet", "aliased-field", "api-failure"} {
		t.Run(scenario, func(t *testing.T) {
			_, store, _, _, _ := retiredPermissionFixture(t)
			var grant ForwardingLeaseGrant
			for _, r := range store.state.ForwardingScopes {
				grant = r.Grant
			}
			grant.Network.BridgeName = computeclient.NetworkName(grant.Lease.Project)
			manager := strings.Repeat("a", 64)
			project := map[string]any{"name": grant.Lease.Project, "config": map[string]string{"restricted": "true", "features.networks": "false", "restricted.networks.access": grant.Network.BridgeName, "user.anas.consumer": grant.Lease.Consumer, "user.anas.sandbox": grant.Lease.Project}}
			network := map[string]any{"name": grant.Network.BridgeName, "type": "bridge", "managed": true, "status": "Created", "config": map[string]string{"user.anas.consumer": grant.Lease.Consumer, "user.anas.sandbox": grant.Lease.Project, "ipv4.address": grant.Network.BridgeCIDR}}
			certs := []map[string]any{{"fingerprint": manager, "type": "client", "restricted": false, "projects": []string{}}}
			var instances any = []any{}
			var operations any = map[string]any{}
			switch scenario {
			case "revoked-still-listed":
				certs = append(certs, map[string]any{"fingerprint": grant.Lease.CredentialFingerprint, "type": "client", "restricted": true, "projects": []string{grant.Lease.Project}})
			case "replacement-credential":
				certs = append(certs, map[string]any{"fingerprint": strings.Repeat("b", 64), "type": "client", "restricted": true, "projects": []string{grant.Lease.Project}})
			case "foreign-global-credential":
				certs = append(certs, map[string]any{"fingerprint": strings.Repeat("b", 64), "type": "client", "restricted": false, "projects": []string{}})
			case "missing-restricted":
				delete(certs[0], "restricted")
			case "missing-manager":
				certs = []map[string]any{}
			case "duplicate-credential":
				certs = append(certs, certs[0])
			case "null-certificates":
				certs = nil
			case "stopped-instance":
				instances = []any{map[string]any{"name": "anas-fj-job1", "status": "Stopped"}}
			case "foreign-named-instance":
				instances = []any{map[string]any{"name": "operator-guest", "status": "Stopped"}}
			case "null-instances":
				instances = nil
			case "pending-operation":
				operations = map[string]any{"running": []any{map[string]any{"id": "creation"}}}
			case "completed-operation":
				operations = map[string]any{"success": []any{map[string]any{"id": "retained-deletion"}}}
			case "failed-operation":
				operations = map[string]any{"failure": []any{map[string]any{"id": "failed-deletion"}}}
			case "null-operations":
				operations = nil
			case "null-operation-group":
				operations = map[string]any{"running": nil}
			case "project-owner":
				project["config"].(map[string]string)["user.anas.consumer"] = "other"
			case "network-owner":
				network["config"].(map[string]string)["user.anas.sandbox"] = "other"
			case "wrong-subnet":
				network["config"].(map[string]string)["ipv4.address"] = "10.99.0.1/24"
			case "aliased-field":
				network["Managed"] = network["managed"]
				delete(network, "managed")
			}
			calls := 0
			client := &incusUnixClient{transport: incusRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != http.MethodGet {
					t.Fatal("retirement observation mutated Incus")
				}
				var value any
				switch r.URL.RequestURI() {
				case "/1.0/projects/" + grant.Lease.Project:
					value = project
				case "/1.0/certificates?recursion=1":
					value = certs
				case "/1.0/instances?project=" + grant.Lease.Project + "&recursion=1":
					value = instances
				case "/1.0/operations?project=" + grant.Lease.Project + "&recursion=1":
					value = operations
				case "/1.0/networks/" + grant.Network.BridgeName + "?project=default":
					value = network
				default:
					t.Fatal("retirement escaped exact API scope", r.URL.RequestURI())
				}
				if scenario == "api-failure" {
					return incusTestResponse(500, `{"type":"error","error_code":500}`), nil
				}
				body, _ := json.Marshal(value)
				return incusTestResponse(200, `{"type":"sync","status_code":200,"metadata":`+string(body)+`}`), nil
			})}
			digest, err := observeForwardingRetirement(context.Background(), client, grant, manager)
			if scenario == "valid" {
				if err != nil || !digestPattern.MatchString(digest) || calls != 5 {
					t.Fatal(digest, err, calls)
				}
			} else if err == nil || digest != "" {
				t.Fatal("incomplete or live scope admitted", scenario)
			}
		})
	}
}
