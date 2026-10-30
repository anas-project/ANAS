// Isolated Postfix/filesystem source experiment, not the production cloud deployment.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type config struct {
	Source       string            `json:"source"`
	Token        string            `json:"token"`
	Root         string            `json:"root"`
	Routes       map[string]string `json:"routes"`
	NotifyURL    string            `json:"notify_url"`
	NotifySecret string            `json:"notify_secret"`
	Expires      int64             `json:"expires"`
}
type message struct {
	ID        string `json:"id"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	From      string `json:"from"`
	Created   int64  `json:"created"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	Delivered bool   `json:"delivered"`
}

var uuid = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func syncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func jsonBytes(value any) []byte {
	b, e := json.Marshal(value)
	if e != nil {
		panic(e)
	}
	return b
}
func writeNew(path string, value any) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".pending-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(jsonBytes(value))
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	// Publish only a complete file, and never replace the first receipt.
	e = os.Link(f.Name(), path)
	if errors.Is(e, os.ErrExist) {
		if syncErr := syncDir(filepath.Dir(path)); syncErr != nil {
			return syncErr
		}
		return e
	}
	if e != nil {
		return e
	}
	return syncDir(filepath.Dir(path))
}
func (c config) directory(id string) string { return filepath.Join(c.Root, "mail", id) }
func (c config) accept(recipient, sender, queueID string, input io.Reader) (string, error) {
	if c.Expires > 0 && time.Now().UnixMilli() >= c.Expires {
		return "", errors.New("fixture expired")
	}
	target := c.Routes[recipient]
	if target == "" {
		return "", errors.New("unregistered envelope recipient")
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9]{5,80}$`).MatchString(queueID) {
		return "", errors.New("trusted Postfix queue identity required")
	}
	key := sha256.Sum256([]byte(c.Source + "\x00" + queueID + "\x00" + recipient))
	b := key[:16]
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
	tmp, e := os.MkdirTemp(filepath.Join(c.Root, "staging"), "mail-")
	if e != nil {
		return "", e
	}
	defer os.RemoveAll(tmp)
	f, e := os.OpenFile(filepath.Join(tmp, "raw.eml"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	size, e := io.Copy(io.MultiWriter(f, h), io.LimitReader(input, 25*1024*1024+1))
	if e == nil && size > 25*1024*1024 {
		e = errors.New("raw message exceeds fixture limit")
	}
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return "", e
	}
	m := message{ID: id, Source: c.Source, Target: target, From: sender, Created: time.Now().UnixMilli(), Size: size, SHA256: hex.EncodeToString(h.Sum(nil))}
	if e = writeNew(filepath.Join(tmp, "metadata.json"), m); e != nil {
		return "", e
	}
	if e = os.Rename(tmp, c.directory(id)); e != nil {
		if errors.Is(e, os.ErrExist) || errors.Is(e, syscall.ENOTEMPTY) {
			old, readErr := c.metadata(id)
			if readErr != nil || old.Target != m.Target || old.From != m.From || old.SHA256 != m.SHA256 || old.Size != m.Size {
				return "", errors.New("existing queue identity has different envelope or bytes")
			}
			raw, readErr := os.ReadFile(filepath.Join(c.directory(id), "raw.eml"))
			sum := sha256.Sum256(raw)
			if readErr != nil || hex.EncodeToString(sum[:]) != m.SHA256 {
				return "", errors.New("existing queue source is corrupt")
			}
			if e = syncDir(filepath.Join(c.Root, "mail")); e != nil {
				return "", e
			}
			return id, nil
		}
		return "", e
	}
	// Both raw and envelope metadata become visible in one directory rename.
	if e = syncDir(filepath.Join(c.Root, "mail")); e != nil {
		return "", e
	}
	return id, nil
}
func (c config) metadata(id string) (message, error) {
	var m message
	b, e := os.ReadFile(filepath.Join(c.directory(id), "metadata.json"))
	if e != nil {
		return m, e
	}
	if e = json.Unmarshal(b, &m); e != nil {
		return m, e
	}
	if m.ID != id || m.Source != c.Source {
		return m, errors.New("metadata identity mismatch")
	}
	_, e = os.Stat(filepath.Join(c.directory(id), "ack.json"))
	m.Delivered = e == nil
	return m, nil
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(jsonBytes(value))
}
func (c config) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c.Expires > 0 && time.Now().UnixMilli() >= c.Expires {
			respond(w, 410, map[string]string{"error": "fixture expired"})
			return
		}
		if !hmac.Equal([]byte(r.Header.Get("Authorization")), []byte("Bearer "+c.Token)) {
			respond(w, 401, map[string]string{"error": "unauthorized"})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/messages" {
			cursor := r.URL.Query().Get("cursor")
			if cursor != "" && !uuid.MatchString(cursor) {
				respond(w, 400, map[string]string{"error": "invalid cursor"})
				return
			}
			limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
			if limit < 1 || limit > 100 {
				limit = 20
			}
			entries, e := os.ReadDir(filepath.Join(c.Root, "mail"))
			if e != nil {
				respond(w, 503, map[string]string{"error": "storage unavailable"})
				return
			}
			var ids []string
			for _, x := range entries {
				if x.IsDir() && uuid.MatchString(x.Name()) && x.Name() > cursor {
					ids = append(ids, x.Name())
				}
			}
			sort.Strings(ids)
			var next *string
			if len(ids) > limit {
				ids = ids[:limit]
				last := ids[len(ids)-1]
				next = &last
			}
			messages := []message{}
			for _, id := range ids {
				m, e := c.metadata(id)
				if e != nil {
					respond(w, 503, map[string]string{"error": "metadata unavailable"})
					return
				}
				messages = append(messages, m)
			}
			respond(w, 200, map[string]any{"source": c.Source, "messages": messages, "cursor": next})
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) != 3 || parts[0] != "messages" || !uuid.MatchString(parts[1]) {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		m, e := c.metadata(parts[1])
		if e != nil {
			respond(w, 404, map[string]string{"error": "not found"})
			return
		}
		if r.Method == "GET" && parts[2] == "raw" {
			f, e := os.Open(filepath.Join(c.directory(m.ID), "raw.eml"))
			if e != nil {
				respond(w, 503, map[string]string{"error": "raw unavailable"})
				return
			}
			defer f.Close()
			w.Header().Set("Content-Type", "message/rfc822")
			w.Header().Set("Cache-Control", "no-store")
			io.Copy(w, f)
			return
		}
		if r.Method == "POST" && parts[2] == "ack" {
			var receipt struct {
				Source  string `json:"source"`
				Target  string `json:"target"`
				Receipt string `json:"receipt"`
			}
			body, e := io.ReadAll(io.LimitReader(r.Body, 4097))
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if e != nil || len(body) > 4096 || decoder.Decode(&receipt) != nil || decoder.Decode(new(any)) != io.EOF || receipt.Source != c.Source || receipt.Target != m.Target || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,80}$`).MatchString(receipt.Receipt) {
				respond(w, 400, map[string]string{"error": "invalid receipt"})
				return
			}
			e = writeNew(filepath.Join(c.directory(m.ID), "ack.json"), map[string]any{"receipt": receipt.Receipt, "target": m.Target, "at": time.Now().UnixMilli()})
			if e != nil && !errors.Is(e, os.ErrExist) {
				respond(w, 503, map[string]string{"error": "receipt persistence failed"})
				return
			}
			respond(w, 200, map[string]any{"id": m.ID, "delivered": true})
			return
		}
		respond(w, 405, map[string]string{"error": "method"})
	})
}
func (c config) notify(ctx context.Context, m message) {
	if c.NotifyURL == "" || c.NotifySecret == "" || m.Delivered {
		return
	}
	dir := c.directory(m.ID)
	if _, e := os.Stat(filepath.Join(dir, "notified.json")); e == nil {
		return
	}
	delays := []int64{0, 60000, 300000}
	for attempt, delay := range delays {
		if time.Now().UnixMilli() < m.Created+delay {
			return
		}
		claim := filepath.Join(dir, fmt.Sprintf("attempt-%d.json", attempt))
		if e := writeNew(claim, map[string]any{"at": time.Now().UnixMilli()}); errors.Is(e, os.ErrExist) {
			continue
		} else if e != nil {
			return
		}
		body := jsonBytes(map[string]string{"source": c.Source, "id": m.ID, "target": m.Target})
		stamp := strconv.FormatInt(time.Now().UnixMilli(), 10)
		mac := hmac.New(sha256.New, []byte(c.NotifySecret))
		mac.Write([]byte(stamp + "."))
		mac.Write(body)
		req, e := http.NewRequestWithContext(ctx, "POST", c.NotifyURL, bytes.NewReader(body))
		if e != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Mail-Time", stamp)
		req.Header.Set("X-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect rejected") }}
		response, e := client.Do(req)
		if e == nil {
			var ack struct {
				ID string `json:"id"`
			}
			decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&ack)
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 && decodeErr == nil && ack.ID == m.ID {
				writeNew(filepath.Join(dir, "notified.json"), map[string]int64{"at": time.Now().UnixMilli()})
			}
			if response.StatusCode >= 300 && response.StatusCode < 500 && response.StatusCode != 429 {
				writeNew(filepath.Join(dir, "blocked.json"), map[string]int{"status": response.StatusCode})
			}
		}
		return
	}
}
func (c config) notifyLoop(ctx context.Context) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		entries, e := os.ReadDir(filepath.Join(c.Root, "mail"))
		if e == nil {
			for _, x := range entries {
				if !uuid.MatchString(x.Name()) {
					continue
				}
				if _, e = os.Stat(filepath.Join(c.directory(x.Name()), "blocked.json")); e == nil {
					continue
				}
				m, e := c.metadata(x.Name())
				if e == nil {
					c.notify(ctx, m)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func main() {
	if len(os.Args) < 3 {
		os.Exit(64)
	}
	b, e := os.ReadFile(os.Args[1])
	var c config
	if e != nil || json.Unmarshal(b, &c) != nil || c.Root != "/var/lib/anas-relay" || c.Source == "" || len(c.Token) < 32 {
		os.Exit(78)
	}
	for _, name := range []string{"mail", "staging"} {
		if os.MkdirAll(filepath.Join(c.Root, name), 0700) != nil {
			os.Exit(75)
		}
	}
	if os.Args[2] == "accept" {
		if len(os.Args) != 6 {
			os.Exit(64)
		}
		id, e := c.accept(os.Args[3], os.Args[4], os.Args[5], os.Stdin)
		if e != nil {
			fmt.Fprintln(os.Stderr, "relay persistence failed")
			os.Exit(75)
		}
		// Explicit one-shot failure used only by this private fixture. The cloud
		// copy exists, but Postfix will retry this same queue item after EX_TEMPFAIL.
		if e = os.Remove(filepath.Join(c.Root, "fault-after-publish")); e == nil {
			syncDir(c.Root)
			os.Exit(75)
		}
		fmt.Println(id)
		return
	}
	if os.Args[2] != "serve" {
		os.Exit(64)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	srv := &http.Server{Addr: "0.0.0.0:18447", Handler: c.handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	go c.notifyLoop(ctx)
	go func() {
		<-ctx.Done()
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		srv.Shutdown(stop)
	}()
	if e = srv.ListenAndServeTLS("/fixture/tls/cert.pem", "/fixture/tls/key.pem"); e != nil && !errors.Is(e, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "relay HTTPS unavailable")
		os.Exit(1)
	}
}
