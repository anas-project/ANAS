package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

type notification struct {
	Source string `json:"source"`
	ID     string `json:"id"`
	Target string `json:"target"`
}

// Authentication covers the exact body. Duplicate delivery events are harmless;
// old signatures are rejected even when their event was previously recorded.
func verifyNotification(r *http.Request, cfg config, now time.Time) (notification, int) {
	var n notification
	if r.Method != http.MethodPost || r.URL.Path != "/notify" || r.URL.RawQuery != "" {
		return n, http.StatusNotFound
	}
	if r.Header.Get("Content-Type") != "application/json" || len(r.Header.Values("X-Mail-Time")) != 1 || len(r.Header.Values("X-Mail-Signature")) != 1 {
		return n, http.StatusBadRequest
	}
	stamp := r.Header.Get("X-Mail-Time")
	ms, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil || ms < now.UnixMilli()-300000 || ms > now.UnixMilli()+300000 {
		return n, http.StatusUnauthorized
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(body) > 4096 {
		return n, http.StatusRequestEntityTooLarge
	}
	signature, err := hex.DecodeString(r.Header.Get("X-Mail-Signature"))
	mac := hmac.New(sha256.New, []byte(cfg.NotifySecret))
	mac.Write([]byte(stamp + "."))
	mac.Write(body)
	if err != nil || len(cfg.NotifySecret) < 32 || !hmac.Equal(mac.Sum(nil), signature) {
		return n, http.StatusUnauthorized
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&n) != nil || decoder.Decode(new(any)) != io.EOF || n.Source != cfg.Source || !uuid.MatchString(n.ID) || cfg.Targets[n.Target] == "" {
		return n, http.StatusBadRequest
	}
	return n, 0
}

func (c *consumer) notificationHandler(wake chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		n, status := verifyNotification(r, c.cfg, time.Now())
		if status != 0 {
			http.Error(w, "notification rejected", status)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		// The INSERT commits before 202. A lost wake-up is recovered at startup or
		// by the independent 60-second poll; no body is accepted from a notification.
		_, err := c.db.ExecContext(ctx, `INSERT INTO ingress_notification(source,id,target) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, n.Source, n.ID, n.Target)
		if err != nil {
			http.Error(w, "notification persistence unavailable", http.StatusServiceUnavailable)
			return
		}
		var pending bool
		err = c.db.QueryRowContext(ctx, `SELECT processed_at IS NULL FROM ingress_notification WHERE source=$1 AND id=$2 AND target=$3`, n.Source, n.ID, n.Target).Scan(&pending)
		if err != nil {
			http.Error(w, "notification persistence unavailable", http.StatusServiceUnavailable)
			return
		}
		if pending {
			select {
			case wake <- struct{}{}:
			default:
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"id": n.ID})
	})
}

func (c *consumer) initScheduler(ctx context.Context) error {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended('ingress-scheduler-schema',0))`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended('ingress-scheduler-schema',0))`)
	_, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ingress_notification(seq bigserial UNIQUE,source text NOT NULL,id text NOT NULL,target text NOT NULL,received_at timestamptz NOT NULL DEFAULT clock_timestamp(),processed_at timestamptz,PRIMARY KEY(source,id,target));
CREATE TABLE IF NOT EXISTS ingress_poll(seq bigserial PRIMARY KEY,source text NOT NULL,reason text NOT NULL,started_at timestamptz NOT NULL DEFAULT clock_timestamp(),finished_at timestamptz,result jsonb)`)
	return err
}

func (c *consumer) poll(ctx context.Context, reason string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var watermark, runID int64
	err := c.db.QueryRowContext(ctx, `SELECT COALESCE(max(seq),0) FROM ingress_notification WHERE source=$1 AND processed_at IS NULL`, c.cfg.Source).Scan(&watermark)
	if err != nil {
		return
	}
	err = c.db.QueryRowContext(ctx, `INSERT INTO ingress_poll(source,reason) VALUES($1,$2) RETURNING seq`, c.cfg.Source, reason).Scan(&runID)
	if err != nil {
		return
	}
	next := &consumer{cfg: c.cfg, db: c.db, http: c.http}
	err = next.run(ctx)
	if err == nil {
		// Only mark events visible before this pass. Notifications arriving during
		// the pass stay pending, including those arriving after the last source page.
		_, err = c.db.ExecContext(ctx, `UPDATE ingress_notification SET processed_at=clock_timestamp() WHERE source=$1 AND seq<=$2 AND processed_at IS NULL`, c.cfg.Source, watermark)
	}
	result := map[string]any{"ok": err == nil, "saved": next.saved, "reconciled": next.reconciled, "acked": next.acked, "skipped": next.skipped}
	if err != nil {
		result["error"] = err.Error()
	}
	b, _ := json.Marshal(result)
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer finishCancel()
	c.db.ExecContext(finishCtx, `UPDATE ingress_poll SET finished_at=clock_timestamp(),result=$2::jsonb WHERE seq=$1`, runID, string(b))
	json.NewEncoder(os.Stdout).Encode(map[string]any{"at": time.Now().UTC(), "reason": reason, "result": result})
}

func (c *consumer) schedule(ctx context.Context, wake <-chan struct{}) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	c.poll(ctx, "startup")
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			c.poll(ctx, "notification")
		case <-ticker.C:
			c.poll(ctx, "timer")
		}
	}
}

func (c *consumer) serve() error {
	// Test-only listener. Public reverse routing and production TLS lifecycle are
	// separate acceptance work; this binary never listens on all interfaces.
	if len(c.cfg.NotifySecret) < 32 || c.cfg.NotifyCert != filepath.Join(c.cfg.Workspace, "data/tls/notify-cert.pem") || c.cfg.NotifyKey != filepath.Join(c.cfg.Workspace, "data/tls/notify-key.pem") {
		return errors.New("private fixture notification configuration required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := c.guard(ctx); err != nil {
		return err
	}
	if err := c.initScheduler(ctx); err != nil {
		return errors.New("scheduler database unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:28443")
	if err != nil {
		return err
	}
	defer listener.Close()
	wake := make(chan struct{}, 1)
	srv := &http.Server{Handler: c.notificationHandler(wake), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	done := make(chan struct{})
	go func() { defer close(done); c.schedule(ctx, wake) }()
	go func() {
		<-ctx.Done()
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(stop)
	}()
	err = srv.ServeTLS(listener, c.cfg.NotifyCert, c.cfg.NotifyKey)
	cancel()
	<-done
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
