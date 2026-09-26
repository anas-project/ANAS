package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type actionsAccountFixture struct {
	input           actionsAccountInput
	store           actionsAccountStore
	account         actionsAccountUser
	password        string
	exists          bool
	mutations       int
	lookups         int
	denyMutation    bool
	privateResponse bool
}

func newActionsAccountFixture(t *testing.T) *actionsAccountFixture {
	t.Helper()
	enabled := true
	f := &actionsAccountFixture{input: actionsAccountInput{Schema: actionsAccountSchema, Enabled: &enabled,
		ControllerPassword: strings.Repeat("c", 64), ManagerUsername: "admin_forgejo", ManagerPassword: strings.Repeat("b", 64)},
		store:   actionsAccountStore{root: filepath.Join(t.TempDir(), "receipt"), uid: os.Geteuid()},
		account: actionsAccountUser{ID: 27, Login: actionsAccountName, Email: actionsAccountEmail, IsAdmin: true},
		exists:  true, password: strings.Repeat("c", 64)}
	previous, cli := httpClient, runForgejoCommand
	t.Cleanup(func() { httpClient, runForgejoCommand = previous, cli })
	runForgejoCommand = func(...string) ([]byte, error) {
		t.Fatal("must not reset or delete a user through CLI")
		return nil, nil
	}
	httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "127.0.0.1:3000" {
			t.Fatal("credential request left the fixed local service")
		}
		username, password, basic := r.BasicAuth()
		if r.URL.Path == "/api/v1/user" {
			if basic && username == f.input.ManagerUsername && password == f.input.ManagerPassword {
				return response(200, `{"id":1,"login":"admin_forgejo","is_admin":true,"email":"admin@localhost.invalid"}`), nil
			}
			if f.exists && username == actionsAccountName && password == f.password {
				body, _ := json.Marshal(f.account)
				return response(200, string(body)), nil
			}
			return response(401, `{"message":"credential rejected"}`), nil
		}
		if r.URL.Path == "/api/v1/users/"+actionsAccountName && r.Method == http.MethodGet {
			f.lookups++
			if !f.exists {
				return response(404, `{"message":"not found"}`), nil
			}
			body, _ := json.Marshal(f.account)
			return response(200, string(body)), nil
		}
		if r.URL.Path == "/api/v1/admin/users/"+actionsAccountName && r.Method == http.MethodPatch {
			if username != f.input.ManagerUsername || password != f.input.ManagerPassword {
				t.Fatal("mutation is not authenticated by separate managed owner")
			}
			if f.denyMutation {
				return response(500, `{"message":"private-server-response"}`), nil
			}
			var patch map[string]any
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				t.Fatal(err)
			}
			if len(patch) != 2 || patch["must_change_password"] != false {
				t.Fatalf("password operation modified other user properties: %v", patch)
			}
			value, ok := patch["password"].(string)
			if !ok || len(value) < 40 {
				t.Fatal("unbounded or weak replacement password")
			}
			f.mutations++
			f.password = value
			body, _ := json.Marshal(f.account)
			return response(200, string(body)), nil
		}
		t.Fatalf("unexpected user operation %s %s", r.Method, r.URL.Path)
		return nil, errors.New("unreachable")
	})}
	return f
}

func (f *actionsAccountFixture) run(enabled bool) error {
	f.input.Enabled = &enabled
	return reconcileManagedActionsAccount(f.input, forgejoAPI, f.store)
}

func TestManagedActionsPasswordDisableReenableAndRepeat(t *testing.T) {
	f := newActionsAccountFixture(t)
	for _, enabled := range []bool{true, true, false, false, true, true, false} {
		if err := f.run(enabled); err != nil {
			t.Fatal("known account lifecycle failed", err)
		}
		if (f.password == f.input.ControllerPassword) != enabled {
			t.Fatal("controller credential remained usable or could not be restored")
		}
		body, err := os.ReadFile(filepath.Join(f.store.root, "account.json"))
		if err != nil || bytes.Contains(body, []byte(f.input.ControllerPassword)) || bytes.Contains(body, []byte(f.input.ManagerPassword)) || bytes.Contains(body, []byte(f.password)) {
			t.Fatal("receipt missing or contains a credential", err)
		}
		var receipt actionsAccountReceipt
		if json.Unmarshal(body, &receipt) != nil || receipt.UserID != 27 || receipt.State != map[bool]string{true: "enabled", false: "disabled"}[enabled] {
			t.Fatal("receipt is not bound to exact account and result")
		}
	}
	if f.mutations != 3 {
		t.Fatal("repeated reconciliation unnecessarily changed a password", f.mutations)
	}
}

func TestDisabledFreshDeploymentDoesNotAdoptUnknownAccount(t *testing.T) {
	for _, present := range []bool{false, true} {
		f := newActionsAccountFixture(t)
		f.exists, f.password = present, "external-owner-secret"
		err := f.run(false)
		if present && err == nil {
			t.Fatal("foreign same-name account was accepted")
		}
		if !present && err != nil {
			t.Fatal("fresh disabled deployment must not create a user", err)
		}
		if f.mutations != 0 {
			t.Fatal("unowned account was modified")
		}
	}
}

func TestOwnedAccountIDOrCredentialBindingDriftBlocksMutation(t *testing.T) {
	for _, mode := range []string{"replacement id", "renamed", "email", "changed secret", "broken receipt", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			f := newActionsAccountFixture(t)
			if err := f.run(true); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(f.store.root, "account.json")
			switch mode {
			case "replacement id":
				f.account.ID++
			case "renamed":
				f.account.Login = "human_admin"
			case "email":
				f.account.Email = "someone@else.invalid"
			case "changed secret":
				f.input.ControllerPassword = strings.Repeat("d", 64)
			case "broken receipt":
				if err := os.WriteFile(path, []byte(`{"schema":"invalid"}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".original", path); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.run(false); err == nil || f.mutations != 0 {
				t.Fatal("drift acquired account reset authority", err)
			}
		})
	}
}

func TestActionsPasswordFailurePreservesPendingReceiptAndDoesNotLeak(t *testing.T) {
	f := newActionsAccountFixture(t)
	if err := f.run(true); err != nil {
		t.Fatal(err)
	}
	f.denyMutation = true
	err := f.run(false)
	if err == nil || strings.Contains(err.Error(), "private-server-response") || f.password != f.input.ControllerPassword {
		t.Fatal("failed mutation was reported as completed or leaked response")
	}
	body, err := os.ReadFile(filepath.Join(f.store.root, "account.json"))
	if err != nil || !bytes.Contains(body, []byte(`"state":"disabling"`)) {
		t.Fatal("failed attempt lost its pending receipt", err)
	}
	f.denyMutation = false
	if err := f.run(false); err != nil || f.password == f.input.ControllerPassword {
		t.Fatal("owned pending transition could not safely converge", err)
	}
}

func TestActionsAccountInputRejectsMissingAmbiguousOrSecretEcho(t *testing.T) {
	f := newActionsAccountFixture(t)
	valid, _ := json.Marshal(f.input)
	if _, err := decodeActionsAccountInput(bytes.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"schema":"anas.actions-account/v1","enabled":false,"enabled":true}`, `{"private-marker":true}`,
		string(valid) + `{}`, strings.Replace(string(valid), `"enabled":true`, `"Enabled":true`, 1), strings.Repeat("x", 8193)} {
		if _, err := decodeActionsAccountInput(strings.NewReader(body)); err == nil || strings.Contains(err.Error(), "private-marker") {
			t.Fatal("unsafe or ambiguous command input accepted")
		}
	}
	_, _ = io.Copy(io.Discard, bytes.NewReader(valid))
}
