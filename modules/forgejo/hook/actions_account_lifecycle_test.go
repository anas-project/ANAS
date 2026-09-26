package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestControllerTemplateRequiresOneExactDisabledFlag(t *testing.T) {
	for _, test := range []struct {
		name  string
		env   []string
		valid bool
	}{
		{"disabled", []string{"FORGEJO_ACTIONS_ENABLED=false", "PASSWORD=private-never-project"}, true},
		{"absent", []string{"PASSWORD=private-never-project"}, false},
		{"enabled", []string{"FORGEJO_ACTIONS_ENABLED=true"}, false},
		{"conflicting", []string{"FORGEJO_ACTIONS_ENABLED=false", "FORGEJO_ACTIONS_ENABLED=true"}, false},
		{"alternate true", []string{"FORGEJO_ACTIONS_ENABLED=1", "FORGEJO_ACTIONS_ENABLED=false"}, false},
		{"duplicate", []string{"FORGEJO_ACTIONS_ENABLED=false", "FORGEJO_ACTIONS_ENABLED=false"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			old := runContainerHelper
			t.Cleanup(func() { runContainerHelper = old })
			runContainerHelper = func(_ context.Context, _ []byte, _ string, args ...string) ([]byte, error) {
				if len(args) != 6 || args[0] != "inspect" {
					t.Fatal("unexpected command")
				}
				renderer, err := template.New("docker-inspect").Funcs(template.FuncMap{"json": func(value any) string { body, _ := json.Marshal(value); return string(body) }}).Parse(args[4])
				if err != nil {
					t.Fatal(err)
				}
				var body bytes.Buffer
				data := map[string]any{"Id": strings.Repeat("a", 64), "Config": map[string]any{"Env": test.env, "User": "65532:65532"},
					"State": map[string]any{"Status": "exited", "ExitCode": 0, "Restarting": false}, "Path": "/usr/local/bin/anas-forgejo-actions-controller", "Args": []string(nil)}
				if err := renderer.Execute(&body, data); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(body.String(), "private-never-project") {
					t.Fatal("inspect leaked another environment value")
				}
				return body.Bytes(), nil
			}
			id, exited, err := disabledControllerObservation(context.Background(), "anas_")
			if test.valid && (err != nil || !exited || id == "") {
				t.Fatal("exact disabled observation rejected", err)
			}
			if !test.valid && err == nil {
				t.Fatal("ambiguous switch authorized password invalidation")
			}
		})
	}
}

func controllerObservation(id, status string, code int) []byte {
	body, _ := json.Marshal(map[string]any{"id": id, "matches_mode": true, "status": status, "exit_code": code,
		"restarting": false, "user": "65532:65532", "path": "/usr/local/bin/anas-forgejo-actions-controller", "args": []string{}})
	return body
}

func TestActionsDisableWaitsForSameProcessCleanupNotReplacement(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		old := runContainerHelper
		t.Cleanup(func() { runContainerHelper = old })
		id, other := strings.Repeat("a", 64), strings.Repeat("b", 64)
		rows := [][]byte{controllerObservation(id, "running", 0), controllerObservation(id, "exited", 0), controllerObservation(id, "exited", 0)}
		if replaced {
			rows[1] = controllerObservation(other, "exited", 0)
		}
		calls := 0
		runContainerHelper = func(_ context.Context, payload []byte, name string, args ...string) ([]byte, error) {
			if len(args) == 0 || args[0] != "inspect" || calls >= len(rows) || len(payload) != 0 {
				t.Fatal("unexpected cleanup operation")
			}
			body := rows[calls]
			calls++
			return body, nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := waitDisabledController(ctx, "anas_", time.Nanosecond)
		cancel()
		if (!replaced && (err != nil || calls != 3)) || (replaced && (err == nil || calls != 2)) {
			t.Fatal("cleanup did not await the same container or accepted replacement", err, calls)
		}
	}
}

func TestControllerWaitCancellationDoesNotAuthorizeCredentialMutation(t *testing.T) {
	old := runContainerHelper
	t.Cleanup(func() { runContainerHelper = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	runContainerHelper = func(_ context.Context, payload []byte, name string, args ...string) ([]byte, error) {
		calls++
		if args[0] != "inspect" || len(payload) != 0 {
			t.Fatal("canceled cleanup mutated a credential")
		}
		cancel()
		return controllerObservation(strings.Repeat("a", 64), "running", 0), nil
	}
	if err := waitDisabledController(ctx, "anas_", time.Nanosecond); err == nil || calls != 1 {
		t.Fatal("cancellation authorized a credential change", err, calls)
	}
}

func TestControllerObservationReceivesTheWaitDeadline(t *testing.T) {
	old := runContainerHelper
	t.Cleanup(func() { runContainerHelper = old })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	runContainerHelper = func(got context.Context, payload []byte, name string, args ...string) ([]byte, error) {
		if got != ctx || len(payload) != 0 || args[0] != "inspect" {
			t.Fatal("observation lost its deadline or executed a mutation")
		}
		<-got.Done()
		return nil, got.Err()
	}
	if err := waitDisabledController(ctx, "anas_", time.Nanosecond); err == nil {
		t.Fatal("canceled observation granted mutation permission")
	}
}

func TestControllerObservationRejectsDuplicateAndAliasedFacts(t *testing.T) {
	good := string(controllerObservation(strings.Repeat("a", 64), "exited", 0))
	for _, body := range []string{
		strings.Replace(good, `"matches_mode":true`, `"matches_mode":false,"matches_mode":true`, 1),
		strings.Replace(good, `"status":"exited"`, `"Status":"running","status":"exited"`, 1),
		strings.Replace(good, `"exit_code":0`, `"exit_code":null`, 1),
		strings.Replace(good, `"args":[]`, `"args":null`, 1),
	} {
		old := runContainerHelper
		t.Cleanup(func() { runContainerHelper = old })
		runContainerHelper = func(context.Context, []byte, string, ...string) ([]byte, error) { return []byte(body), nil }
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := waitDisabledController(ctx, "anas_", time.Nanosecond)
		cancel()
		if err == nil {
			t.Fatal("ambiguous process facts authorized credential revocation")
		}
	}
}

func TestActionsDisableWaitsForVerifiedControllerExitBeforeRevokingPassword(t *testing.T) {
	old := runContainerHelper
	t.Cleanup(func() { runContainerHelper = old })
	calls := []string{}
	runContainerHelper = func(_ context.Context, payload []byte, name string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		calls = append(calls, joined)
		if name != "docker" {
			t.Fatal("unexpected executable")
		}
		if len(args) > 0 && args[0] == "inspect" {
			if len(payload) != 0 || !strings.Contains(joined, "anas_forgejo_actions_controller") {
				t.Fatal("not the fixed controller observation")
			}
			return []byte(`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":true,"status":"exited","exit_code":0,"restarting":false,"user":"65532:65532","path":"/usr/local/bin/anas-forgejo-actions-controller","args":[]}`), nil
		}
		if !strings.HasSuffix(joined, "actions-account") {
			t.Fatal("unexpected account operation")
		}
		var input struct {
			Enabled            bool   `json:"enabled"`
			ControllerPassword string `json:"controller_password"`
			ManagerPassword    string `json:"manager_password"`
		}
		if json.Unmarshal(payload, &input) != nil || input.Enabled || input.ControllerPassword != "controller-private" || input.ManagerPassword != "owner-private" {
			t.Fatal("account request lacks its exact disabled state and private credentials")
		}
		if strings.Contains(joined, "private") {
			t.Fatal("password in host argv")
		}
		return nil, nil
	}
	if err := reconcileActionsAccount(map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "false", "FORGEJO_ACTIONS_CONTROLLER_PASSWORD": "controller-private"}, localAdminInput{Username: "admin_forgejo", Password: "owner-private"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 || !strings.HasPrefix(calls[0], "inspect ") || calls[0] != calls[1] {
		t.Fatal("account was changed without stable exit readback", calls)
	}
}

func TestActionsDisableCannotRevokeCredentialsWhenCleanupIsUncertain(t *testing.T) {
	for _, body := range []string{
		`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":true,"status":"running","exit_code":0,"restarting":false,"user":"65532:65532"}`,
		`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":true,"status":"exited","exit_code":1,"restarting":false,"user":"65532:65532"}`,
		`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":false,"status":"exited","exit_code":0,"restarting":false,"user":"65532:65532"}`,
		`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":true,"status":"exited","exit_code":0,"restarting":true,"user":"65532:65532"}`,
		`{"id":"` + strings.Repeat("a", 64) + `","matches_mode":true,"status":"exited","exit_code":0,"restarting":false,"user":"root"}`,
		`{}`, `private-upstream-failure`,
	} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			old := runContainerHelper
			t.Cleanup(func() { runContainerHelper = old })
			runContainerHelper = func(_ context.Context, payload []byte, name string, args ...string) ([]byte, error) {
				if len(args) == 0 || args[0] != "inspect" || len(payload) != 0 {
					t.Fatal("uncertain cleanup invoked credential mutation")
				}
				return []byte(body), nil
			}
			err := reconcileActionsAccount(map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "false", "FORGEJO_ACTIONS_CONTROLLER_PASSWORD": "controller-private"}, localAdminInput{Username: "admin_forgejo", Password: "owner-private"})
			if err == nil || strings.Contains(err.Error(), "private-upstream") {
				t.Fatal("uncertain cleanup accepted or leaked")
			}
		})
	}
}

func TestActionsAccountRunsAfterLocalRecoveryAccountNotBeforeIt(t *testing.T) {
	old := runContainerHelper
	t.Cleanup(func() { runContainerHelper = old })
	calls := []string{}
	runContainerHelper = func(_ context.Context, payload []byte, name string, args ...string) ([]byte, error) {
		calls = append(calls, args[len(args)-1])
		if args[len(args)-1] == "local-admin" {
			return nil, errors.New("owner not ready")
		}
		t.Fatal("account control ran before its separate owner was verified")
		return nil, nil
	}
	req := hookRequest{Env: map[string]string{"CONTAINER_PREFIX": "anas_", "FORGEJO_ACTIONS_ENABLED": "true", "FORGEJO_ACTIONS_CONTROLLER_PASSWORD": "controller-private"},
		Secrets: map[string]string{"candidate": "owner-private"}, LocalAccount: &localAccountOperation{AccountID: "break_glass", Handler: "apply-forgejo-break-glass", Username: "admin_forgejo", CandidateSecretKey: "candidate"}}
	if err := handleLocalAccount(req); err == nil || len(calls) != 1 || calls[0] != "local-admin" {
		t.Fatal("owner failure did not block program credential change", err)
	}
}
