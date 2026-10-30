// Isolated ingestion experiment; this is NOT a production mail service.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type config struct {
	URL          string            `json:"url"`
	Source       string            `json:"source"`
	Token        string            `json:"token"`
	DatabaseURL  string            `json:"database_url"`
	Spool        string            `json:"spool"`
	Workspace    string            `json:"workspace"`
	DockerRoot   string            `json:"docker_root"`
	DockerHost   string            `json:"docker_host"`
	Container    string            `json:"container"`
	Targets      map[string]string `json:"targets"`
	IPv4Only     bool              `json:"ipv4_only"`
	HTTP1Only    bool              `json:"http1_only"`
	NotifySecret string            `json:"notify_secret"`
	NotifyCert   string            `json:"notify_cert"`
	NotifyKey    string            `json:"notify_key"`
}
type message struct {
	ID        string `json:"id"`
	Target    string `json:"target"`
	Source    string `json:"source"`
	Delivered bool   `json:"delivered"`
}
type page struct {
	Source   string    `json:"source"`
	Messages []message `json:"messages"`
	Cursor   *string   `json:"cursor"`
}
type consumer struct {
	cfg                               config
	db                                *sql.DB
	http                              *http.Client
	fault                             string
	saved, reconciled, acked, skipped int
}

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func digest(b []byte) string    { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func canonical(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
func cloudClient(cfg config) *http.Client {
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
	if cfg.IPv4Only || cfg.HTTP1Only {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if cfg.IPv4Only {
			dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				return dialer.DialContext(ctx, "tcp4", address)
			}
		}
		if cfg.HTTP1Only {
			// Clone can retain a warmed default transport's h2 ALPN advertisement.
			transport.ForceAttemptHTTP2 = false
			transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = new(tls.Config)
			}
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
			transport.Protocols = new(http.Protocols)
			transport.Protocols.SetHTTP1(true)
		}
		client.Transport = transport
	}
	return client
}

func (c *consumer) command(ctx context.Context, args []string, body []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"-H", c.cfg.DockerHost}, args...)...)
	cmd.Stdin = bytes.NewReader(body)
	result, err := cmd.Output()
	if err != nil {
		return nil, errors.New("scoped Docker/Dovecot command failed")
	}
	return result, nil
}
func (c *consumer) guard(ctx context.Context) error {
	if c.cfg.DockerHost != "unix:///run/anas-mail-test-1024.sock" || c.cfg.Workspace != "/home/whl/anas-mail-ingress-test-20261025" ||
		c.cfg.DockerRoot != "/home/whl/anas-mail-go-test-20261024/docker" || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(c.cfg.Container) {
		return errors.New("refusing non-fixture Docker configuration")
	}
	b, err := c.command(ctx, []string{"info", "--format", "{{.DockerRootDir}}"}, nil)
	if err != nil || strings.TrimSpace(string(b)) != c.cfg.DockerRoot {
		return errors.New("Docker root mismatch")
	}
	b, err = c.command(ctx, []string{"inspect", c.cfg.Container}, nil)
	if err != nil {
		return err
	}
	var rows []struct {
		Config struct{ Labels map[string]string }
		Mounts []struct{ Type, Source string }
	}
	if json.Unmarshal(b, &rows) != nil || len(rows) != 1 {
		return errors.New("invalid container inspection")
	}
	l := rows[0].Config.Labels
	if l["com.docker.compose.project"] != "anas_mail_ingress_1025" || l["com.docker.compose.project.working_dir"] != c.cfg.Workspace || l["anas.mail-ingress-poc"] != "true" {
		return errors.New("container scope mismatch")
	}
	for _, m := range rows[0].Mounts {
		if m.Type == "volume" || (m.Type == "bind" && !strings.HasPrefix(m.Source, c.cfg.Workspace+"/")) {
			return errors.New("container mount scope mismatch")
		}
	}
	return nil
}
func (c *consumer) request(ctx context.Context, path, method string, body []byte) ([]byte, error) {
	// These source operations are idempotent, including the immutable final receipt.
	for attempt := 0; ; attempt++ {
		b, retry, err := c.requestOnce(ctx, path, method, body)
		if err == nil || !retry || attempt == 2 || ctx.Err() != nil {
			return b, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
}
func (c *consumer) requestOnce(ctx context.Context, path, method string, body []byte) ([]byte, bool, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, false, err
	}
	r.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	r.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(r)
	if err != nil {
		// URL has no credentials/query; preserve DNS/TLS/timeout details, never body/header data.
		return nil, true, fmt.Errorf("cloud request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, response.StatusCode == 429 || response.StatusCode >= 500, fmt.Errorf("cloud HTTP %d", response.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(response.Body, 25*1024*1024+1))
	if err != nil {
		return nil, true, err
	}
	if len(b) > 25*1024*1024 {
		return nil, false, errors.New("message exceeds limit")
	}
	return b, false, nil
}
func (c *consumer) lookup(ctx context.Context, user, guid string, raw []byte) (bool, error) {
	b, err := c.command(ctx, []string{"exec", "-i", c.cfg.Container, "doveadm", "-f", "json", "fetch", "-u", user, "guid text", "GUID", guid}, nil)
	if err != nil {
		return false, err
	}
	var messages []map[string]string
	if json.Unmarshal(b, &messages) != nil {
		return false, errors.New("invalid doveadm JSON")
	}
	if len(messages) == 0 {
		return false, nil
	}
	if len(messages) != 1 || messages[0]["guid"] != guid || digest(canonical([]byte(messages[0]["text"]))) != digest(canonical(raw)) {
		return false, errors.New("DMS identity/content mismatch")
	}
	return true, nil
}
func persist(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".mail-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(name, path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (c *consumer) consume(ctx context.Context, conn *sql.Conn, m message) error {
	user, ok := c.cfg.Targets[m.Target]
	if !ok || m.Source != c.cfg.Source || !uuid.MatchString(m.ID) {
		return errors.New("untrusted source, target or delivery ID")
	}
	if user != "box-a@ingress.invalid" && user != "box-b@ingress.invalid" {
		return errors.New("non-fixture mailbox")
	}
	key := digest([]byte(c.cfg.Source + "\x00" + m.ID + "\x00" + m.Target))
	guid := key[:32]
	var state, storedHash string
	var cloudAcked bool
	err := conn.QueryRowContext(ctx, `SELECT state,sha256,cloud_acked FROM ingress_delivery WHERE source=$1 AND id=$2 AND target=$3`, c.cfg.Source, m.ID, m.Target).Scan(&state, &storedHash, &cloudAcked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if state == "complete" && cloudAcked && m.Delivered {
		c.skipped++
		return nil
	}
	if state != "complete" {
		raw, err := c.request(ctx, "/messages/"+m.ID+"/raw", "GET", nil)
		if err != nil {
			return err
		}
		hash := digest(raw)
		if storedHash != "" && storedHash != hash {
			return errors.New("source bytes changed for existing delivery")
		}
		if err = persist(filepath.Join(c.cfg.Spool, key+".eml"), raw); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO ingress_delivery(source,id,target,sha256,guid,state) VALUES($1,$2,$3,$4,$5,'prepared') ON CONFLICT DO NOTHING`, c.cfg.Source, m.ID, m.Target, hash, guid)
		if err != nil {
			return err
		}
		found, err := c.lookup(ctx, user, guid, raw)
		if err != nil {
			return err
		}
		if found {
			c.reconciled++
		} else {
			if state == "uncertain" {
				return errors.New("uncertain DMS result; absent GUID is not permission to resend")
			}
			_, err = conn.ExecContext(ctx, `UPDATE ingress_delivery SET state='uncertain' WHERE source=$1 AND id=$2 AND target=$3`, c.cfg.Source, m.ID, m.Target)
			if err != nil {
				return err
			}
			if c.fault == "before-save" {
				os.Exit(71)
			}
			_, err = c.command(ctx, []string{"exec", "-i", c.cfg.Container, "doveadm", "save", "-u", user, "-m", "INBOX", "-g", guid}, raw)
			if err != nil {
				return err
			}
			if c.fault == "after-save" {
				os.Exit(72)
			}
			found, err = c.lookup(ctx, user, guid, raw)
			if err != nil {
				return err
			}
			if !found {
				return errors.New("DMS save has no matching durable message")
			}
			c.saved++
		}
		_, err = conn.ExecContext(ctx, `UPDATE ingress_delivery SET state='complete' WHERE source=$1 AND id=$2 AND target=$3`, c.cfg.Source, m.ID, m.Target)
		if err != nil {
			return err
		}
		if c.fault == "after-complete" {
			os.Exit(73)
		}
	}
	body, _ := json.Marshal(map[string]string{"source": c.cfg.Source, "target": m.Target, "receipt": guid})
	b, err := c.request(ctx, "/messages/"+m.ID+"/ack", "POST", body)
	if err != nil {
		return err
	}
	var receipt struct {
		ID        string `json:"id"`
		Delivered bool   `json:"delivered"`
	}
	if json.Unmarshal(b, &receipt) != nil || receipt.ID != m.ID || !receipt.Delivered {
		return errors.New("invalid cloud receipt")
	}
	if c.fault == "after-ack" {
		os.Exit(74)
	}
	_, err = conn.ExecContext(ctx, `UPDATE ingress_delivery SET cloud_acked=true WHERE source=$1 AND id=$2 AND target=$3`, c.cfg.Source, m.ID, m.Target)
	if err == nil {
		c.acked++
	}
	return err
}
func (c *consumer) run(ctx context.Context) error {
	if err := c.guard(ctx); err != nil {
		return err
	}
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return errors.New("PostgreSQL unavailable")
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1,0))`, c.cfg.Source)
	if err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1,0))`, c.cfg.Source)
	_, err = conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS ingress_delivery(source text,id text,target text,sha256 text NOT NULL,guid text NOT NULL,state text NOT NULL CHECK(state IN ('prepared','uncertain','complete')),cloud_acked boolean NOT NULL DEFAULT false,PRIMARY KEY(source,id,target))`)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	cursor := ""
	var failures []error
	for n := 0; n < 100; n++ {
		path := "/messages?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		b, err := c.request(ctx, path, "GET", nil)
		if err != nil {
			return err
		}
		var p page
		if json.Unmarshal(b, &p) != nil || p.Source != c.cfg.Source {
			return errors.New("invalid source page")
		}
		for _, m := range p.Messages {
			if err := c.consume(ctx, conn, m); err != nil {
				failures = append(failures, err)
			}
		}
		if p.Cursor == nil {
			return errors.Join(failures...)
		}
		if *p.Cursor == "" || seen[*p.Cursor] {
			return errors.New("non-progressing pagination")
		}
		seen[*p.Cursor] = true
		cursor = *p.Cursor
	}
	return errors.New("page budget exceeded")
}
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: consumer PRIVATE_CONFIG")
		os.Exit(2)
	}
	info, err := os.Stat(os.Args[1])
	if err != nil || info.Mode().Perm()&0077 != 0 {
		fmt.Fprintln(os.Stderr, "private configuration required")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	var cfg config
	if err != nil || json.Unmarshal(b, &cfg) != nil {
		os.Exit(2)
	}
	u, err := url.Parse(cfg.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || cfg.Token == "" || cfg.Source == "" {
		fmt.Fprintln(os.Stderr, "invalid HTTPS source")
		os.Exit(2)
	}
	db, err := sql.Open("pgx", cfg.DatabaseURL)
	if err != nil {
		os.Exit(2)
	}
	defer db.Close()
	c := &consumer{cfg: cfg, db: db, fault: os.Getenv("MAIL_POC_FAULT"), http: cloudClient(cfg)}
	if os.Getenv("MAIL_POC_SERVE") == "1" {
		if err := c.serve(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err = c.run(ctx)
	result := map[string]any{"saved": c.saved, "reconciled": c.reconciled, "acked": c.acked, "skipped": c.skipped, "ok": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	json.NewEncoder(os.Stdout).Encode(result)
	if err != nil {
		os.Exit(1)
	}
}
