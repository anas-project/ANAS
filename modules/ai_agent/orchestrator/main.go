// Command anas-ai-agent is the AI Agent orchestration control plane. It accepts
// signed Forgejo deliveries, keeps the agents' identities and the system
// webhook in the state configuration asks for, and reconciles what the webhook
// missed. It holds no host privilege, no Docker socket and no host mount: every
// piece of model-generated code runs in a work instance leased through the
// compute contract, never here (AGENT-R-002).
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	command := ""
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	var err error
	switch command {
	case "":
		err = run()
	case "healthcheck":
		err = healthcheck()
	default:
		err = fmt.Errorf("unsupported command %q", command)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "anas-ai-agent:", err)
		os.Exit(1)
	}
}

// healthcheck asks the process's own listener whether it is serving. It does
// not touch Forgejo or the database: a health probe that fails because a
// dependency is briefly unavailable would restart a control plane that is
// working correctly and waiting.
func healthcheck() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", cfg.Listen, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %d", resp.StatusCode)
	}
	return nil
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	redactor := NewRedactor(cfg.AdminPassword, cfg.WebhookSecret, cfg.DatabaseURL)
	logf := func(line string) { fmt.Fprintln(os.Stderr, "anas-ai-agent:", redactor.String(line)) }

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if !cfg.Enabled {
		// The one switch is off. The container stays up and serves health so
		// the deployment topology does not change when it is turned on, but it
		// registers nothing, mints nothing and accepts no deliveries.
		logf("ai_agent is disabled; serving health only")
		return serve(ctx, cfg, disabledHandler(), logf)
	}

	store, err := OpenPostgres(ctx, cfg.DatabaseURL, redactor)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	admin := NewForgejoAdmin(cfg.ForgejoURL, cfg.AdminUsername, cfg.AdminPassword, redactor)
	ingress := &Ingress{Config: cfg, Store: store, Redactor: redactor, Log: logf}
	bootstrapper := &Bootstrapper{
		Config: cfg, Admin: admin, Store: store, Redactor: redactor,
		Scoping: ScopingRepositories, Log: logf,
	}
	reconciler := &Reconciler{Config: cfg, Admin: admin, Store: store, Ingress: ingress, Log: logf}

	// The first pass runs before the listener opens. A webhook that arrives
	// before the accounts exist is recorded and handled later, but registering
	// the hook before the identities exist would advertise a control plane that
	// cannot yet act.
	if err := bootstrapper.Reconcile(ctx); err != nil {
		return err
	}
	if err := bootstrapper.EnsureWebhook(ctx); err != nil {
		return err
	}

	go sweepLoop(ctx, cfg, reconciler, bootstrapper, logf)
	return serve(ctx, cfg, ingress.Handler(), logf)
}

func sweepLoop(ctx context.Context, cfg Config, reconciler *Reconciler, bootstrapper *Bootstrapper, logf func(string)) {
	ticker := time.NewTicker(cfg.ReconcileEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cycle, cancel := context.WithTimeout(ctx, cfg.ReconcileEvery)
		if err := bootstrapper.Reconcile(cycle); err != nil && ctx.Err() == nil {
			logf("identity reconciliation: " + err.Error())
		}
		if err := bootstrapper.EnsureWebhook(cycle); err != nil && ctx.Err() == nil {
			logf("webhook reconciliation: " + err.Error())
		}
		if _, err := reconciler.Sweep(cycle); err != nil && ctx.Err() == nil {
			logf("event reconciliation: " + err.Error())
		}
		cancel()
	}
}

func disabledHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("disabled\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "ai_agent is disabled", http.StatusServiceUnavailable)
	})
	return mux
}

func serve(ctx context.Context, cfg Config, handler http.Handler, logf func(string)) error {
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	shutdown := make(chan error, 1)
	go func() {
		<-ctx.Done()
		// The shutdown context is deliberately fresh: ctx is already cancelled
		// by the time this runs, and reusing it would abort the drain instead
		// of granting it.
		grace, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		shutdown <- server.Shutdown(grace)
	}()
	logf("listening on " + cfg.Listen)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return <-shutdown
}
