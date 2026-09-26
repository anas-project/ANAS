package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestStopBarrierSignalsOnlyTheObservedControllerAndChecksExit(t *testing.T) {
	for _, mode := range []string{"true", "false"} {
		t.Run(mode, func(t *testing.T) {
			previous := runContainerHelper
			t.Cleanup(func() { runContainerHelper = previous })
			id := strings.Repeat("a", 64)
			calls, observed := []string{}, 0
			runContainerHelper = func(ctx context.Context, payload []byte, name string, args ...string) ([]byte, error) {
				if name != "docker" || len(payload) != 0 || ctx == nil {
					t.Fatal("not a fixed credential-free Docker operation")
				}
				calls = append(calls, args[0])
				switch args[0] {
				case "container":
					return []byte(id + "\n"), nil
				case "inspect":
					if args[len(args)-1] != id {
						t.Fatal("inspect followed a reusable container name")
					}
					observed++
					state := "running"
					if observed > 1 {
						state = "exited"
					}
					return controllerObservation(id, state, 0), nil
				case "stop":
					if strings.Join(args, " ") != "stop --time 125 "+id || observed != 1 {
						t.Fatal("unbounded or unobserved stop", args)
					}
					return []byte(id + "\n"), nil
				default:
					t.Fatal("stop barrier modified account, network or unrelated service", args)
				}
				return nil, errors.New("unexpected call")
			}
			if err := drainActionsController(context.Background(), map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": mode}); err != nil {
				t.Fatal(err)
			}
			if strings.Join(calls, ",") != "container,inspect,stop,inspect,inspect" {
				t.Fatal("cleanup exit not independently read back twice", calls)
			}
		})
	}
}

func TestStopBarrierPreservesUncertainControllerAndApplication(t *testing.T) {
	for _, failure := range []string{"exit-failed", "replaced", "stop-failed", "still-running"} {
		t.Run(failure, func(t *testing.T) {
			previous := runContainerHelper
			t.Cleanup(func() { runContainerHelper = previous })
			id, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
			observations := 0
			runContainerHelper = func(ctx context.Context, payload []byte, name string, args ...string) ([]byte, error) {
				if len(payload) != 0 || name != "docker" {
					t.Fatal("unexpected privileged request")
				}
				switch args[0] {
				case "container":
					return []byte(id + "\n"), nil
				case "inspect":
					observations++
					state, code, actual := "running", 0, id
					if observations > 1 {
						state = "exited"
					}
					if failure == "exit-failed" {
						code = 1
					}
					if failure == "replaced" && observations > 1 {
						actual = other
					}
					if failure == "still-running" {
						state = "running"
					}
					return controllerObservation(actual, state, code), nil
				case "stop":
					if failure == "exit-failed" {
						t.Fatal("failed historical exit cannot be retried implicitly")
					}
					if failure == "stop-failed" {
						return nil, errors.New("private Docker response")
					}
					return []byte(id), nil
				default:
					t.Fatal("failure removed a process, network or credential", args)
				}
				return nil, errors.New("unexpected call")
			}
			err := drainActionsController(context.Background(), map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "true"})
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("uncertain exit authorized removal or leaked", err)
			}
		})
	}
}

func TestStopBarrierIsIdempotentOnlyForAnAlreadyAbsentModule(t *testing.T) {
	for _, appPresent := range []bool{true, false} {
		previous := runContainerHelper
		t.Cleanup(func() { runContainerHelper = previous })
		calls := 0
		runContainerHelper = func(ctx context.Context, payload []byte, name string, args ...string) ([]byte, error) {
			calls++
			if args[0] != "container" || len(payload) != 0 {
				t.Fatal("absent controller led to a mutation")
			}
			if calls == 2 && appPresent {
				return []byte(strings.Repeat("b", 64)), nil
			}
			return nil, nil
		}
		err := drainActionsController(context.Background(), map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "false"})
		if (err != nil) != appPresent || calls != 2 {
			t.Fatal("missing controller was treated as proof of cleanup", err, calls)
		}
	}
}

func TestStopBarrierDoesNotAdmitCanceledOrUnboundTargets(t *testing.T) {
	previous := runContainerHelper
	t.Cleanup(func() { runContainerHelper = previous })
	runContainerHelper = func(context.Context, []byte, string, ...string) ([]byte, error) {
		t.Fatal("invalid input reached Docker")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if drainActionsController(ctx, map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "true"}) == nil {
		t.Fatal("canceled stop succeeded")
	}
	for _, env := range []map[string]string{{}, {"CONTAINER_PREFIX": "../other", "FORGEJO_ACTIONS_ENABLED": "true"}, {"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "1"}} {
		if drainActionsController(context.Background(), env) == nil {
			t.Fatal("unbound stop accepted")
		}
	}
}

func TestStopLookupRequiresComposeProjectAndServiceNotOnlyName(t *testing.T) {
	previous := runContainerHelper
	t.Cleanup(func() { runContainerHelper = previous })
	calls := 0
	runContainerHelper = func(ctx context.Context, payload []byte, executable string, args ...string) ([]byte, error) {
		calls++
		joined := strings.Join(args, " ")
		service := "anas_forgejo_actions_controller"
		if calls == 2 {
			service = "anas_forgejo"
		}
		if args[0] != "container" || !strings.Contains(joined, "label=com.docker.compose.project=anas_forgejo") ||
			!strings.Contains(joined, "label=com.docker.compose.service="+service) {
			t.Fatal("a standalone same-name process could enter the managed stop path")
		}
		return nil, nil
	}
	if err := drainActionsController(context.Background(), map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "false"}); err != nil || calls != 2 {
		t.Fatal("absent managed module did not remain a no-op", err)
	}
}
