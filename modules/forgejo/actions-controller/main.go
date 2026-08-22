package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var err error
	if len(os.Args) == 2 && os.Args[1] == "preflight" {
		err = runPreflight()
	} else if len(os.Args) == 1 {
		err = run()
	} else {
		err = fmt.Errorf("unsupported command")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "anas-forgejo-actions:", err)
		os.Exit(1)
	}
}

func runPreflight() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return nil
	}
	_, err = NewIncusProvider(cfg.Incus)
	return err
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	store := FileStateStore{Path: cfg.StatePath}
	// A fresh disabled deployment has no compute credential and nothing to
	// clean. After an enabled deployment is switched off, the retained settings
	// let this one-shot container remove its registrations and VMs before it
	// exits. The managed service account is retained for a later re-enable.
	if !cfg.Enabled && cfg.Incus.Endpoint == "" {
		state, loadErr := store.Load()
		if loadErr != nil {
			return loadErr
		}
		if len(state.Workloads) > 0 {
			return fmt.Errorf("Actions is disabled but Incus settings are unavailable for cleanup")
		}
		return nil
	}
	provider, err := NewIncusProvider(cfg.Incus)
	if err != nil {
		return err
	}
	controller := NewController(
		cfg, NewForgejoClient(cfg.ForgejoURL, cfg.Username, cfg.Password), provider, store,
	)
	if !cfg.Enabled {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		return controller.CleanupAll(ctx)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()
	for {
		cycle, cancel := context.WithTimeout(ctx, cfg.OperationTTL)
		err := controller.Reconcile(cycle)
		cancel()
		if err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "anas-forgejo-actions: reconcile:", err)
		}
		select {
		case <-ctx.Done():
			cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			return controller.CleanupAll(cleanup)
		case <-ticker.C:
		}
	}
}
