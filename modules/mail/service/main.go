// Mail runtime. Pending withdrawals must not depend on online login bootstrap.
package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/anas-project/ANAS/modules/mail/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func database() (*sql.DB, error) {
	for _, key := range []string{"HOST", "PORT", "NAME", "USERNAME", "PASSWORD"} {
		if os.Getenv("MAIL_DB_"+key) == "" {
			return nil, errors.New("mail database binding is incomplete")
		}
	}
	port, err := strconv.Atoi(os.Getenv("MAIL_DB_PORT"))
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("mail database port is invalid")
	}
	cfg, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, errors.New("mail database configuration is invalid")
	}
	cfg.Host = os.Getenv("MAIL_DB_HOST")
	cfg.Port = uint16(port)
	cfg.Database = os.Getenv("MAIL_DB_NAME")
	cfg.User = os.Getenv("MAIL_DB_USERNAME")
	cfg.Password = os.Getenv("MAIL_DB_PASSWORD")
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams = map[string]string{"application_name": "anas-mail"}
	return stdlib.OpenDB(*cfg), nil
}
func handler(db *sql.DB, login http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		// Internal service credentials are never accepted on the public listener.
		if r.URL.Path == "/internal" || strings.HasPrefix(r.URL.Path, "/internal/") {
			w.WriteHeader(404)
			json.NewEncoder(w).Encode(map[string]string{"error": "not-found"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/healthz" {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if db.PingContext(ctx) == nil {
				json.NewEncoder(w).Encode(map[string]string{"status": "foundation-running"})
				return
			}
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]string{"status": "database-unavailable"})
			return
		}
		if r.URL.Path == "/readyz" {
			w.WriteHeader(503)
			json.NewEncoder(w).Encode(map[string]string{"status": "mail-integration-incomplete"})
			return
		}
		if login != nil {
			login.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(404)
		json.NewEncoder(w).Encode(map[string]string{"error": "not-found"})
	})
}
func run() error {
	db, err := database()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	migrateCtx, stop := context.WithTimeout(ctx, 30*time.Second)
	err = (store.Store{DB: db}).Migrate(migrateCtx)
	stop()
	if err != nil {
		return errors.New("mail database initialization failed")
	}
	cfg, err := readLoginConfig()
	if err != nil {
		return err
	}
	data := store.Store{DB: db}
	var login http.Handler
	var certificate tls.Certificate
	var initialize func(context.Context) (http.Handler, error)
	var kicker sessionKicker
	var sources *ingressRuntime
	if cfg != nil {
		trust, client, err := prepareLoginConfig(*cfg)
		if err != nil {
			return err
		}
		if cfg.Protocol != nil {
			certificate, err = tls.LoadX509KeyPair(cfg.Protocol.CertFile, cfg.Protocol.KeyFile)
			if err != nil {
				return errors.New("mail internal TLS identity unavailable")
			}
		}
		if client != nil {
			kicker = client
		}
		sources, err = prepareIngress(*cfg, data, client)
		if err != nil {
			return err
		}
		defer sources.close()
		initialize = func(attempt context.Context) (http.Handler, error) {
			handler, err := loginFromPrepared(attempt, *cfg, data, trust, client)
			if err == nil && sources != nil {
				if h, ok := handler.(*loginHandler); ok && h.coordinator != nil {
					h.coordinator.sources = sources
					h.sources = sources
				}
			}
			return handler, err
		}
	}
	if err := dropPrivileges(); err != nil {
		return err
	}
	// The fixed DMS client and its trust were validated locally. Start this
	// worker even while LDAP-backed IAM discovery cannot yet finish. No login
	// or protocol listener exists until authorization initialization succeeds.
	if kicker != nil {
		go revocationLoop(ctx, data, kicker)
	}
	sources.start(ctx)
	if initialize != nil {
		login, err = awaitAuthorization(ctx, 5*time.Second, initialize)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	srv := &http.Server{Addr: ":8080", Handler: withIngressNotifications(handler(db, login), sources), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 25 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	var internal *http.Server
	internalErrors := make(chan error, 1)
	if h, ok := login.(*loginHandler); ok && h.protocol != nil {
		internal = &http.Server{Addr: ":8443", Handler: h.internalHandler(), TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}, ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	}
	if h, ok := login.(*loginHandler); ok && h.dms != nil {
		if h.coordinator != nil {
			if h.directoryEvents {
				go h.coordinator.eventLoop(ctx, store.Store{DB: db}, "/run/anas/directory-events/events.jsonl")
			} else {
				go h.coordinator.loop(ctx)
			}
		}
	}
	if internal != nil {
		go func() {
			if err := internal.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
				internalErrors <- errors.New("mail internal TLS listener failed")
				cancel()
			}
		}()
	}
	go func() {
		<-ctx.Done()
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		srv.Shutdown(c)
		if internal != nil {
			internal.Shutdown(c)
		}
	}()
	err = srv.ListenAndServe()
	if internal != nil {
		defer internal.Close()
	}
	if errors.Is(err, http.ErrServerClosed) {
		select {
		case err := <-internalErrors:
			return err
		default:
		}
		return nil
	}
	return err
}
func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		client := &http.Client{Timeout: 3 * time.Second}
		r, err := client.Get("http://127.0.0.1:8080/healthz")
		if err != nil {
			os.Exit(1)
		}
		r.Body.Close()
		if r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) != 1 {
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
