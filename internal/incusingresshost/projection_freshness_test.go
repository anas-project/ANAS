package incusingresshost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func freshnessProjectionRequest() ProjectionRequest {
	return ProjectionRequest{Schema: ProjectionSchema, ScopeID: "scope_one", Epoch: strings.Repeat("a", 64), Deployment: "deployment-one", Lease: Lease{Consumer: "forgejo", Resource: "runners"}, InstanceID: "anas-fj-job1", WorkloadID: "job:123", GuestPort: 7000}
}

func TestProjectionRejectsReplayOfPreviouslyValidObservation(t *testing.T) {
	var prior []byte
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, _ string, body []byte) ([]byte, error) {
		if prior == nil {
			var request ProjectionRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			var err error
			prior, err = json.Marshal(validProjectionResponse(request))
			if err != nil {
				t.Fatal(err)
			}
		}
		return prior, nil
	})}
	request := freshnessProjectionRequest()
	if _, err := client.ObserveHTTP(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("previous valid observation replayed as fresh host identity")
	}
}

func TestProjectionCancellationCannotReturnFreshIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, _ string, body []byte) ([]byte, error) {
		var request ProjectionRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		cancel()
		return json.Marshal(validProjectionResponse(request))
	})}
	if _, err := client.ObserveHTTP(ctx, freshnessProjectionRequest()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled observation accepted or cancellation hidden: %v", err)
	}
}

func TestProjectionRequiresFreshGeneratedBindingAndDeadline(t *testing.T) {
	request := freshnessProjectionRequest()
	seen := map[string]bool{}
	client := ProjectionClient{Invoker: projectionInvokerFunc(func(ctx context.Context, _ string, body []byte) ([]byte, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("observation has no bounded deadline")
		}
		var request ProjectionRequest
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.Validate() != nil || seen[request.ObservationID] {
			t.Fatal("invocations share observation identity")
		}
		seen[request.ObservationID] = true
		return json.Marshal(validProjectionResponse(request))
	})}
	for range 4 {
		if _, err := client.ObserveHTTP(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	}
	request.ObservationID = strings.Repeat("f", 64)
	if _, err := client.ObserveHTTP(context.Background(), request); err == nil {
		t.Fatal("accepted caller-supplied replay key")
	}
	if len(seen) != 4 {
		t.Fatal("invalid call reached the host action channel")
	}
}

func TestProjectionRejectsMissingAndMismatchedInvocationEvidence(t *testing.T) {
	for _, scenario := range []string{"missing", "changed", "old-schema", "aliased-field", "unknown-field"} {
		t.Run(scenario, func(t *testing.T) {
			client := ProjectionClient{Invoker: projectionInvokerFunc(func(_ context.Context, _ string, body []byte) ([]byte, error) {
				var request ProjectionRequest
				if err := json.Unmarshal(body, &request); err != nil {
					t.Fatal(err)
				}
				response := validProjectionResponse(request)
				switch scenario {
				case "missing":
					response.ObservationID = ""
				case "changed":
					response.ObservationID = strings.Repeat("f", 64)
				case "old-schema":
					response.Schema = "anas.incus-http-host-projection/v1"
				}
				out, err := json.Marshal(response)
				if scenario == "aliased-field" {
					out = []byte(strings.Replace(string(out), `"observation_id":`, `"Observation_ID":`, 1))
				}
				if scenario == "unknown-field" {
					out = append([]byte(`{"caller_claim":"ignore",`), out[1:]...)
				}
				return out, err
			})}
			if _, err := client.ObserveHTTP(context.Background(), freshnessProjectionRequest()); err == nil {
				t.Fatal("invalid invocation evidence accepted")
			}
		})
	}
}
