package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

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
	if len(args) != 2 || args[0] != "seed-full" && args[0] != "set-owner" {
		return errors.New("usage: console-auth-fixture seed-full|set-owner ABSOLUTE_CONSOLE_STORE")
	}
	password := os.Getenv(passwordEnvironment)
	if password == "" {
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
