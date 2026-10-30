// TEST_CASES: MAILMOD-T-078, MAILMOD-T-079
// This explicitly configured test-only server runs the production notification
// handler against an isolated PostgreSQL database. It has no mailbox interface.
package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type config struct {
	DSN        string `json:"dsn"`
	Source     string `json:"source"`
	Generation string `json:"generation"`
	Secret     string `json:"secret"`
	TestToken  string `json:"test_token"`
	Cert       string `json:"cert"`
	Key        string `json:"key"`
	Port       int    `json:"port"`
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("explicit private configuration required")
	}
	st, e := os.Lstat(os.Args[1])
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return errors.New("private configuration required")
	}
	f, e := os.Open(os.Args[1])
	if e != nil {
		return errors.New("configuration unavailable")
	}
	defer f.Close()
	var cfg config
	dec := json.NewDecoder(io.LimitReader(f, 16<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil {
		return errors.New("invalid configuration")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || cfg.Port < 1024 || cfg.Port > 65535 || len(cfg.Secret) != 64 || len(cfg.TestToken) != 64 || cfg.TestToken == cfg.Secret {
		return errors.New("invalid configuration")
	}
	db, e := sql.Open("pgx", cfg.DSN)
	if e != nil {
		return errors.New("database unavailable")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var name string
	if db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name) != nil || name != "anas_mail_model_1025" {
		return errors.New("isolated database required")
	}
	data := store.Store{DB: db}
	if data.Migrate(ctx) != nil {
		return errors.New("database migration failed")
	}
	var wakes atomic.Int64
	receiver := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{cfg.Source: {Generation: cfg.Generation, Secret: cfg.Secret}}, Wake: func(string) { wakes.Add(1) }}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/api/ingress/notify" {
			receiver.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/__test/health" && r.Method == "GET" {
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != "/__test/status" || r.Method != "GET" || len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+cfg.TestToken)) != 1 {
			http.NotFound(w, r)
			return
		}
		rows, err := db.QueryContext(r.Context(), `SELECT notification_id,message_id,mailbox_id,state FROM mail.ingress_notice WHERE source_id=$1 AND generation=$2 ORDER BY message_id`, cfg.Source, cfg.Generation)
		if err != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		defer rows.Close()
		type item struct {
			ID        string `json:"id"`
			MessageID string `json:"message_id"`
			MailboxID string `json:"mailbox_id"`
			State     string `json:"state"`
		}
		items := []item{}
		for rows.Next() {
			var i item
			if rows.Scan(&i.ID, &i.MessageID, &i.MailboxID, &i.State) != nil {
				http.Error(w, "unavailable", 503)
				return
			}
			items = append(items, i)
		}
		if rows.Err() != nil {
			http.Error(w, "unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"notices": items, "wakes": wakes.Load()})
	})
	listener, e := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(cfg.Port)))
	if e != nil {
		return errors.New("fixture port unavailable")
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 10 * time.Second}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-signalCtx.Done()
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(c)
	}()
	e = server.ServeTLS(listener, cfg.Cert, cfg.Key)
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return errors.New("fixture TLS unavailable")
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, strings.ReplaceAll(e.Error(), "\n", " "))
		os.Exit(1)
	}
}
