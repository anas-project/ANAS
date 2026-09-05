package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleauth"
	"github.com/anas-project/ANAS/internal/consolestate"
)

const passwordEnvironment = "ANAS_E2E_OWNER_PASSWORD"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: console-auth-fixture seed-full|set-owner|revoke-sessions|report-sessions ABSOLUTE_CONSOLE_STORE")
	}
	needsPassword := args[0] == "seed-full" || args[0] == "set-owner"
	switch args[0] {
	case "seed-full", "set-owner", "revoke-sessions", "report-sessions":
	default:
		return errors.New("usage: console-auth-fixture seed-full|set-owner|revoke-sessions|report-sessions ABSOLUTE_CONSOLE_STORE")
	}
	// report-sessions never opens the audit or capability stores: it must be
	// safe to run repeatedly beside a live daemon without recording anything
	// that would look like console activity.
	if args[0] == "report-sessions" {
		return reportSessions(args[1])
	}
	password := os.Getenv(passwordEnvironment)
	if needsPassword && password == "" {
		return fmt.Errorf("%s is required", passwordEnvironment)
	}
	os.Unsetenv(passwordEnvironment)

	ctx := context.Background()
	writer, err := audit.Open(args[1])
	if err != nil {
		return fmt.Errorf("open audit store: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = writer.Close()
		}
	}()
	stateStore, err := consolestate.Open(ctx, args[1], consoleaudit.StateSink{Writer: writer})
	if err != nil {
		return fmt.Errorf("open capability state: %w", err)
	}
	currentAuthState := func(ctx context.Context) (consoleauth.ConsoleState, error) {
		state, err := stateStore.Current(ctx)
		return consoleauth.ConsoleState(state), err
	}
	authStore, err := consoleauth.Open(args[1], consoleaudit.AuthSink{Writer: writer, Actor: "r102-e2e"}, consoleauth.StoreOptions{
		CurrentState: currentAuthState,
	})
	if err != nil {
		return fmt.Errorf("open authentication store: %w", err)
	}
	state, err := stateStore.Current(ctx)
	if err != nil {
		return fmt.Errorf("read capability state: %w", err)
	}

	switch args[0] {
	case "seed-full":
		if state != consolestate.StateBootstrap {
			return fmt.Errorf("seed-full requires bootstrap state, got %s", state)
		}
		if err := authStore.SetOwnerPassword(ctx, password); err != nil {
			return fmt.Errorf("set initial owner password: %w", err)
		}
		password = ""
		if _, err := stateStore.Transition(ctx, consolestate.StateEnrollment, "r102-e2e"); err != nil {
			return fmt.Errorf("advance to enrollment: %w", err)
		}
		if _, err := stateStore.Transition(ctx, consolestate.StateFull, "r102-e2e"); err != nil {
			return fmt.Errorf("advance to full: %w", err)
		}
		state = consolestate.StateFull
	case "set-owner":
		if state != consolestate.StateFull {
			return fmt.Errorf("set-owner requires full state, got %s", state)
		}
		if err := authStore.SetOwnerPassword(ctx, password); err != nil {
			return fmt.Errorf("replace owner password: %w", err)
		}
		password = ""
	case "revoke-sessions":
		if state != consolestate.StateFull {
			return fmt.Errorf("revoke-sessions requires full state, got %s", state)
		}
		if err := authStore.RevokeLocalSessions(ctx); err != nil {
			return fmt.Errorf("revoke local sessions: %w", err)
		}
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("close audit store: %w", err)
	}
	closed = true
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"api_version": "anas.console-auth-fixture/v1",
		"command":     args[0],
		"state":       state,
	})
}

// localSessionReport mirrors only the two instants consoleauth reports to the
// browser. The full record layout stays private to that package; a test that
// needs to prove an idle window did or did not slide needs nothing more, and a
// narrow view here cannot drift into asserting the store's internals.
type localSessionReport struct {
	Sessions map[string]struct {
		CreatedAt     time.Time `json:"created_at"`
		ExpiresAt     time.Time `json:"expires_at"`
		IdleExpiresAt time.Time `json:"idle_expires_at"`
	} `json:"sessions"`
}

func reportSessions(directory string) error {
	if !filepath.IsAbs(directory) {
		return errors.New("console store path must be absolute")
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Clean(directory), "local.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{
				"api_version": "anas.console-auth-fixture/v1",
				"command":     "report-sessions",
				"sessions":    []any{},
			})
		}
		return fmt.Errorf("read local authentication state: %w", err)
	}
	var report localSessionReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return fmt.Errorf("decode local authentication state: %w", err)
	}
	digests := make([]string, 0, len(report.Sessions))
	for digest := range report.Sessions {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	sessions := make([]map[string]string, 0, len(digests))
	for _, digest := range digests {
		record := report.Sessions[digest]
		sessions = append(sessions, map[string]string{
			// The digest is already a one-way value; it identifies a record
			// across two readings without exposing the session token.
			"digest":          digest,
			"created_at":      record.CreatedAt.UTC().Format(time.RFC3339Nano),
			"expires_at":      record.ExpiresAt.UTC().Format(time.RFC3339Nano),
			"idle_expires_at": record.IdleExpiresAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{
		"api_version": "anas.console-auth-fixture/v1",
		"command":     "report-sessions",
		"sessions":    sessions,
	})
}
