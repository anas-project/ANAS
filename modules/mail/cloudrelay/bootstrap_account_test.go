// TEST_CASES: MAILMOD-T-076
package cloudrelay

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func accountTestJournal(t *testing.T) (int, string, []byte) {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := managedDirectory(path, 0700)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd, path, []byte("synthetic fixed public-key binding")
}
func TestBootstrapAccountCreationAndRecovery(t *testing.T) {
	for _, scenario := range []string{"normal", "lost-success", "lost-before", "foreign", "owned-without-intent", "remove-after", "changed-after", "bound-recovery"} {
		t.Run(scenario, func(t *testing.T) {
			fd, path, binding := accountTestJournal(t)
			state := bootstrapAccountAbsent
			created := 0
			ops := bootstrapAccountOps{observe: func(_ context.Context, tag string) (bootstrapAccountState, error) {
				if tag != bootstrapAccountTag(binding) {
					t.Fatal("account binding drift")
				}
				return state, nil
			}, create: func(_ context.Context, _ string) error {
				created++
				if scenario == "lost-before" {
					return ErrPublishUnknown
				}
				state = bootstrapAccountOwned
				if scenario == "lost-success" {
					return ErrPublishUnknown
				}
				return nil
			}}
			if scenario == "foreign" {
				state = bootstrapAccountForeign
			}
			if scenario == "owned-without-intent" {
				state = bootstrapAccountOwned
			}
			if scenario == "bound-recovery" {
				if err := recordActionMarker(fd, "account.intent", binding); err != nil {
					t.Fatal(err)
				}
				state = bootstrapAccountOwned
			}
			err := ensureBootstrapAccount(context.Background(), fd, binding, ops)
			switch scenario {
			case "foreign", "owned-without-intent":
				if !errors.Is(err, ErrPublishConflict) || created != 0 {
					t.Fatal(err, created)
				}
				if _, err := os.Stat(filepath.Join(path, "account.intent")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("wrote intent for foreign account")
				}
				return
			case "lost-before":
				if !errors.Is(err, ErrPublishUnknown) || created != 1 {
					t.Fatal(err, created)
				}
				if err = ensureBootstrapAccount(context.Background(), fd, binding, ops); !errors.Is(err, ErrPublishUnknown) || created != 1 {
					t.Fatal("unknown creation repeated", err, created)
				}
				return
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "bound-recovery" && created != 0 {
				t.Fatal("recovery repeated useradd")
			}
			if scenario == "remove-after" {
				state = bootstrapAccountAbsent
			}
			if scenario == "changed-after" {
				state = bootstrapAccountForeign
			}
			err = ensureBootstrapAccount(context.Background(), fd, binding, ops)
			if scenario == "remove-after" || scenario == "changed-after" {
				if !errors.Is(err, ErrPublishConflict) {
					t.Fatal("changed identity accepted", err)
				}
			} else if err != nil {
				t.Fatal("retry", err)
			}
		})
	}
}
