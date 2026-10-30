// TEST_CASES: MAILMOD-T-076
package cloudrelay

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

func accessInstallFixture(t *testing.T) (bootstrapAccessLayout, BootstrapAccessPlan, *bootstrapAccountState, *int, *bool) {
	t.Helper()
	root := t.TempDir()
	layout := bootstrapAccessLayout{journal: filepath.Join(root, "journal"), homeParent: filepath.Join(root, "homes"), sudoers: filepath.Join(root, "sudoers")}
	for _, p := range []string{layout.journal, layout.homeParent, layout.sudoers} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PrepareBootstrapAccess(ssh.MarshalAuthorizedKey(key))
	if err != nil {
		t.Fatal(err)
	}
	state := bootstrapAccountAbsent
	calls := 0
	sudoOK := true
	return layout, plan, &state, &calls, &sudoOK
}
func accessTestOps(layout bootstrapAccessLayout, state *bootstrapAccountState, calls *int, sudoOK *bool) bootstrapAccessOps {
	return bootstrapAccessOps{account: bootstrapAccountOps{observe: func(context.Context, string) (bootstrapAccountState, error) { return *state, nil }, create: func(context.Context, string) error { *calls++; *state = bootstrapAccountOwned; return nil }}, sshd: func(context.Context) error { return nil }, sudo: func(context.Context) error {
		if !*sudoOK {
			return ErrPublishConflict
		}
		return nil
	}, validateSudo: func(_ context.Context, content []byte) error {
		if string(content) == "" {
			return ErrPublishConflict
		}
		return nil
	}}
}
func TestBootstrapAccessInstallAndRecovery(t *testing.T) {
	for _, mode := range []string{"normal", "existing-home", "existing-rule", "foreign-user", "sudo-denied", "key-change", "removed-key", "changed-rule", "extra-key", "directory-unknown", "identity-changed", "complete-key-stage", "missing-key-stage"} {
		t.Run(mode, func(t *testing.T) {
			layout, plan, state, calls, sudoOK := accessInstallFixture(t)
			ops := accessTestOps(layout, state, calls, sudoOK)
			home := filepath.Join(layout.homeParent, "anas-mail-relay-bootstrap")
			key := filepath.Join(home, ".ssh", "authorized_keys")
			rule := filepath.Join(layout.sudoers, "anas-mail-relay-bootstrap")
			switch mode {
			case "existing-home":
				if err := os.Mkdir(home, 0755); err != nil {
					t.Fatal(err)
				}
			case "existing-rule":
				if err := os.WriteFile(rule, []byte("external"), 0440); err != nil {
					t.Fatal(err)
				}
			case "foreign-user":
				*state = bootstrapAccountForeign
			case "sudo-denied":
				*sudoOK = false
			}
			err := installBootstrapAccess(context.Background(), layout, plan, ops)
			switch mode {
			case "existing-home", "existing-rule", "foreign-user":
				if !errors.Is(err, ErrPublishConflict) || *calls != 0 {
					t.Fatal("preexisting resources adopted", err, *calls)
				}
				return
			case "sudo-denied":
				if !errors.Is(err, ErrPublishConflict) {
					t.Fatal(err)
				}
				if _, err := os.Lstat(key); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("key enabled before sudo verification", err)
				}
				*sudoOK = true
				if err := installBootstrapAccess(context.Background(), layout, plan, ops); err != nil {
					t.Fatal("resume sudo denial", err)
				}
				return
			default:
				if err != nil {
					t.Fatal("initial installation", err)
				}
			}
			if raw, err := os.ReadFile(key); err != nil || string(raw) != plan.AuthorizedKeys {
				t.Fatal("key differs", err)
			}
			if raw, err := os.ReadFile(rule); err != nil || string(raw) != plan.Sudoers {
				t.Fatal("rule differs", err)
			}
			switch mode {
			case "key-change":
				plan.AuthorizedKeys += "different"
			case "removed-key":
				if err := os.Remove(key); err != nil {
					t.Fatal(err)
				}
			case "changed-rule":
				if err := os.Chmod(rule, 0644); err != nil {
					t.Fatal(err)
				}
			case "extra-key":
				if err := os.WriteFile(filepath.Join(home, ".ssh", "authorized_keys2"), []byte("foreign"), 0644); err != nil {
					t.Fatal(err)
				}
			case "directory-unknown":
				if err := os.Remove(filepath.Join(layout.journal, "ssh-directory.done")); err != nil {
					t.Fatal(err)
				}
			case "identity-changed":
				*state = bootstrapAccountForeign
			case "complete-key-stage", "missing-key-stage":
				if err := os.Remove(filepath.Join(layout.journal, "publish-access-key.done")); err != nil {
					t.Fatal(err)
				}
				if mode == "complete-key-stage" {
					if err := os.Rename(key, filepath.Join(home, ".ssh", ".authorized_keys.anas-stage")); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(key); err != nil {
					t.Fatal(err)
				}
			}
			err = installBootstrapAccess(context.Background(), layout, plan, ops)
			if mode == "normal" || mode == "complete-key-stage" {
				if err != nil || *calls != 1 {
					t.Fatal("repeat changed account", err, *calls)
				}
				if raw, err := os.ReadFile(key); err != nil || string(raw) != plan.AuthorizedKeys {
					t.Fatal("key not recovered", err)
				}
				return
			}
			if mode == "directory-unknown" || mode == "missing-key-stage" {
				if !errors.Is(err, ErrPublishUnknown) {
					t.Fatal("ambiguous directory accepted", err)
				}
				return
			}
			if !errors.Is(err, ErrPublishConflict) {
				t.Fatal("drift accepted", err)
			}
		})
	}
}
