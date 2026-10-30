// TEST_CASES: MAILMOD-T-080
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// The controller supplies only disposable test values; Cloudflare account
// tokens and the one-time destination-verification message stay off finance.
type nativePublicVPSInput struct {
	Source, Generation, Reader, Control, RelayCA  string
	Notification                                  string
	Address, MailboxID, OwnerAnchor, ClaimVersion string
	Nonce                                         string
}

type nativePublicVPSReady struct {
	VerificationID string
	MessageID      string
}

func parseNativePublicVPSReady(raw []byte) (nativePublicVPSReady, error) {
	var ready nativePublicVPSReady
	if len(raw) == 0 || len(raw) > 1024 || !uniqueInstallJSON(raw, "VerificationID MessageID") {
		return ready, errors.New("invalid public fixture signal")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	ids := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	if d.Decode(&ready) != nil || d.Decode(new(any)) != io.EOF || !ids.MatchString(ready.VerificationID) || !ids.MatchString(ready.MessageID) || ready.VerificationID == ready.MessageID {
		return nativePublicVPSReady{}, errors.New("invalid public fixture signal")
	}
	return ready, nil
}

// Publish a fresh readiness signal after the database-backed route operation
// has succeeded. The controller only reads presence; no credentials are sent.
func nativePublicVPSSignal(dir, name string) error {
	if name != "routes-ready" {
		return errors.New("invalid public fixture signal")
	}
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// The controller writes an atomic 0600 file containing two opaque source
// identifiers only after the destination is confirmed and SMTP accepted.
func nativePublicVPSAwait(ctx context.Context, dir string) (nativePublicVPSReady, error) {
	for {
		if _, err := os.Lstat(filepath.Join(dir, "abort")); err == nil {
			return nativePublicVPSReady{}, errors.New("public fixture aborted")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nativePublicVPSReady{}, errors.New("public fixture abort state unknown")
		}
		path := filepath.Join(dir, "public-ready.json")
		info, err := os.Lstat(path)
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
				return nativePublicVPSReady{}, errors.New("invalid public fixture signal")
			}
			f, err := os.Open(path)
			if err != nil {
				return nativePublicVPSReady{}, errors.New("public fixture signal unavailable")
			}
			opened, statErr := f.Stat()
			if statErr != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
				f.Close()
				return nativePublicVPSReady{}, errors.New("public fixture signal changed")
			}
			raw, err := io.ReadAll(io.LimitReader(f, 1025))
			f.Close()
			if err != nil {
				return nativePublicVPSReady{}, errors.New("public fixture signal unavailable")
			}
			return parseNativePublicVPSReady(raw)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nativePublicVPSReady{}, errors.New("public fixture signal unknown")
		}
		select {
		case <-ctx.Done():
			return nativePublicVPSReady{}, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (v nativePublicVPSInput) route() ingress.Route {
	return ingress.Route{Address: v.Address, MailboxID: v.MailboxID, OwnerAnchor: v.OwnerAnchor, ClaimVersion: v.ClaimVersion}
}

func parseNativePublicVPS(raw []byte) (nativePublicVPSInput, error) {
	var v nativePublicVPSInput
	const fields = "Source Generation Reader Control RelayCA Notification Address MailboxID OwnerAnchor ClaimVersion Nonce"
	if len(raw) == 0 || len(raw) > 16384 || !uniqueInstallJSON(raw, fields) {
		return v, errors.New("invalid public fixture input")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || !webmailKeyPattern.MatchString(v.Reader) || !webmailKeyPattern.MatchString(v.Control) || v.Reader == v.Control || v.Notification != "" && (!webmailKeyPattern.MatchString(v.Notification) || v.Notification == v.Reader || v.Notification == v.Control) || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(v.Nonce) || !regexp.MustCompile(`^cf-vps-test-[a-f0-9]{12}@mail-poc\.bengbu\.cc\.cd$`).MatchString(v.Address) {
		return nativePublicVPSInput{}, errors.New("invalid public fixture input")
	}
	if _, err := ingress.ForwardTarget(v.Source, v.Generation, v.route(), "vps-target.mail-poc.bengbu.cc.cd"); err != nil {
		return nativePublicVPSInput{}, errors.New("invalid public fixture route")
	}
	roots := x509.NewCertPool()
	if strings.TrimSpace(v.RelayCA) == "" || !roots.AppendCertsFromPEM([]byte(v.RelayCA)) {
		return nativePublicVPSInput{}, errors.New("invalid public fixture trust")
	}
	return v, nil
}

func nativePublicVPSConfig(t *testing.T) *nativePublicVPSInput {
	t.Helper()
	path := os.Getenv("MAIL_NATIVE_VPS_PUBLIC_CONFIG")
	if path == "" {
		return nil
	}
	if path != "/input/vps-public.json" {
		t.Fatal("fixed public fixture input required")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		t.Fatal("private public fixture input required")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal("public fixture input unavailable")
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() || opened.Mode().Perm()&0077 != 0 {
		f.Close()
		t.Fatal("public fixture input changed")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	f.Close()
	if err != nil {
		t.Fatal("public fixture input unavailable")
	}
	defer clear(raw)
	v, err := parseNativePublicVPS(raw)
	if err != nil {
		t.Fatal("invalid public fixture input")
	}
	bridge, err := os.Lstat("/bridge")
	if err != nil || !bridge.IsDir() || bridge.Mode()&os.ModeSymlink != 0 || bridge.Mode().Perm()&0077 != 0 {
		t.Fatal("private public fixture bridge required")
	}
	for _, name := range []string{"routes-ready", "public-ready.json", "abort", "notify.sock", "notify-cert.pem"} {
		if _, err = os.Lstat(filepath.Join("/bridge", name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("stale public fixture signal")
		}
	}
	return &v
}

// This opt-in test joins the actual PostgreSQL route operation and consumer
// to a disposable cross-host relay. The caller supplies two opaque message
// IDs only after verifying the provider destination and synthetic message.
func nativePublicVPSIngress(t *testing.T, cfg *nativePublicVPSInput, data store.Store, backend *dms.Client, resource identity.MailboxResource) string {
	t.Helper()
	if cfg == nil || resource.ID != cfg.MailboxID || resource.OwnerAnchor != cfg.OwnerAnchor || len(resource.Addresses) == 0 || resource.Addresses[0].Address != cfg.Address || resource.Addresses[0].Version != cfg.ClaimVersion {
		t.Fatal("public fixture mailbox does not match")
	}
	ctx := t.Context()
	projection, err := data.MailboxProjection(ctx, resource.ID)
	if err != nil || !projection.Applied {
		t.Fatal("public fixture projection unavailable")
	}
	var wakeCount atomic.Int32
	if cfg.Notification != "" {
		handler := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{cfg.Source: {Generation: cfg.Generation, Secret: cfg.Notification}}, Wake: func(string) { wakeCount.Add(1) }}
		server := nativeVPSCrossHostNotify(t, "/bridge/notify.sock", handler)
		t.Cleanup(server.Close)
		cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
		f, err := os.OpenFile("/bridge/notify-cert.pem", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			t.Fatal("public notification certificate unavailable")
		}
		_, writeErr := f.Write(cert)
		syncErr, closeErr := f.Sync(), f.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			t.Fatal("public notification certificate not published")
		}
	}
	if cfg.Notification == "" {
		nativeVPSWaitSignal(t, "relay-ready")
	} else {
		deadline := time.Now().Add(600 * time.Second)
		for {
			if _, err := os.Stat("/bridge/abort"); err == nil {
				t.Fatal("public fixture aborted")
			}
			if _, err := os.Stat("/bridge/relay-ready"); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("public relay signal unavailable")
			}
			time.Sleep(time.Second)
		}
	}
	address := nativeVPSBridge(t, "relay.sock")
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: cfg.Source, Generation: cfg.Generation, Origin: "https://" + address, Token: cfg.Reader, ControlToken: cfg.Control, Domains: []string{"mail-poc.bengbu.cc.cd"}, AdditionalCA: []byte(cfg.RelayCA)})
	if err != nil {
		t.Fatal("public source configuration invalid")
	}
	t.Cleanup(client.Close)
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.ingress_notice WHERE source_id=$1`, `DELETE FROM mail.ingress_message WHERE source_id=$1`, `DELETE FROM mail.ingress_poll WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(clean, q, cfg.Source); e != nil {
				t.Error("public fixture cleanup", e)
			}
		}
	})
	state, err := client.ReadRoutes(ctx)
	if err != nil || state.Revision != 0 || state.Sequence != 0 || len(state.Bindings) != 0 {
		t.Fatal("public relay is not fresh")
	}
	sync := ingress.RouteSync{Ledger: data, Source: client, ID: cfg.Source, Generation: cfg.Generation}
	if err = sync.Synchronize(ctx, []ingress.Route{cfg.route()}, map[string]string{resource.ID: projection.Revision}); err != nil {
		t.Fatal("public route intent not confirmed", err)
	}
	state, err = client.ReadRoutes(ctx)
	if err != nil || state.Revision != 1 || state.Sequence != 0 || len(state.Bindings) != 1 || state.Bindings[0].Route != cfg.route() || state.Bindings[0].ClosedSequence != nil || state.Bindings[0].FirstSequence != 1 {
		t.Fatal("public route readback mismatch")
	}
	if err = nativePublicVPSSignal("/bridge", "routes-ready"); err != nil {
		t.Fatal("public route signal unavailable")
	}
	wait, cancel := context.WithTimeout(ctx, 1050*time.Second)
	ready, err := nativePublicVPSAwait(wait, "/bridge")
	cancel()
	if err != nil {
		t.Fatal("public message signal unavailable", err)
	}
	guard := cloudflareVPSFixtureSource{source: client, sourceID: cfg.Source, generation: cfg.Generation, verification: ready.VerificationID, expected: ready.MessageID, route: cfg.route()}
	page, err := guard.List(ctx, "")
	if err != nil || page.Cursor != nil || len(page.Messages) != 1 || page.Messages[0].Metadata.MessageID != ready.MessageID {
		t.Fatal("verified public message unavailable", err)
	}
	staging := t.TempDir()
	if err = os.Chmod(staging, 0700); err != nil {
		t.Fatal("public fixture staging unavailable")
	}
	native := &countedNativeIntake{Client: backend}
	consumer := ingress.Consumer{Ledger: data, Source: guard, Native: native, Directory: staging, SourceID: cfg.Source, Generation: cfg.Generation}
	if cfg.Notification != "" {
		noticeDeadline := time.Now().Add(90 * time.Second)
		for {
			err = consumer.Notices(ctx)
			if err != nil && !errors.Is(err, ingress.ErrIdentity) {
				t.Fatal("public notification processing failed", err)
			}
			var accepted, protected int
			if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE message_id=$2 AND state='accepted'),count(*) FILTER (WHERE message_id=$3 AND state='pending') FROM mail.ingress_notice WHERE source_id=$1`, cfg.Source, ready.MessageID, ready.VerificationID).Scan(&accepted, &protected); err != nil {
				t.Fatal("public notification status unavailable")
			}
			if accepted == 1 && protected == 1 && wakeCount.Load() >= 2 {
				t.Log("public VPS notifications recorded before native import")
				break
			}
			if time.Now().After(noticeDeadline) {
				t.Fatal("public notification unconfirmed", accepted, protected, wakeCount.Load())
			}
			time.Sleep(time.Second)
		}
	}
	deadline := time.Now().Add(150 * time.Second)
	var firstPollErr, firstWorkErr error
	for {
		var pollErr error
		if cfg.Notification == "" {
			pollErr = consumer.Poll(ctx)
		}
		workErr := consumer.Work(ctx)
		if firstPollErr == nil && pollErr != nil {
			firstPollErr = pollErr
		}
		if firstWorkErr == nil && workErr != nil {
			firstWorkErr = workErr
		}
		var done int
		if err = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_message WHERE source_id=$1 AND phase='complete'`, cfg.Source).Scan(&done); err != nil {
			t.Fatal("public fixture status unavailable")
		}
		if done == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("public native delivery unconfirmed", firstPollErr, firstWorkErr)
		}
		time.Sleep(time.Second)
	}
	if native.posts.Load() != 1 {
		t.Fatal("unexpected public native submission count", native.posts.Load())
	}
	receiptDeadline := time.Now().Add(30 * time.Second)
	for {
		page, err = client.List(ctx, "")
		if err != nil || page.Cursor != nil || len(page.Messages) != 2 {
			t.Fatal("public source receipt unavailable")
		}
		var protected, delivered bool
		for _, m := range page.Messages {
			switch m.Metadata.MessageID {
			case ready.VerificationID:
				protected = m.Delivery == nil && (cfg.Notification == "" || m.Notification != nil && m.Notification.State == "notified")
			case ready.MessageID:
				delivered = m.Delivery != nil && m.Delivery.State == "stored" && (cfg.Notification == "" || m.Notification != nil && m.Notification.State == "notified")
			}
		}
		if protected && delivered {
			break
		}
		if time.Now().After(receiptDeadline) {
			t.Fatal("public source receipt mismatch")
		}
		time.Sleep(time.Second)
	}
	if err = consumer.Poll(ctx); err != nil {
		t.Fatal("public replay poll", err)
	}
	if err = consumer.Work(ctx); err != nil || native.posts.Load() != 1 {
		t.Fatal("public replay submitted again", err)
	}
	return "ANAS isolated forwarding " + cfg.Nonce
}

func TestNativePublicVPSInputRejectsDrift(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	input := nativePublicVPSInput{
		Source: "10000000-0000-4000-8000-000000000001", Generation: "10000000-0000-4000-8000-000000000002",
		Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64), RelayCA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Address: "cf-vps-test-012345abcdef@mail-poc.bengbu.cc.cd", MailboxID: "10000000-0000-4000-8000-000000000003", OwnerAnchor: "10000000-0000-4000-8000-000000000004", ClaimVersion: "10000000-0000-4000-8000-000000000005", Nonce: strings.Repeat("c", 32),
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := parseNativePublicVPS(raw); err != nil || got != input {
		t.Fatal("valid public fixture rejected", err)
	}
	withNotify := input
	withNotify.Notification = strings.Repeat("d", 64)
	encoded, err := json.Marshal(withNotify)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := parseNativePublicVPS(encoded); err != nil || got != withNotify {
		t.Fatal("valid notification fixture rejected", err)
	}
	for name, mutate := range map[string]func(*nativePublicVPSInput){
		"source":        func(v *nativePublicVPSInput) { v.Source = "not-a-uuid" },
		"generation":    func(v *nativePublicVPSInput) { v.Generation = "bad" },
		"mailbox":       func(v *nativePublicVPSInput) { v.MailboxID = "bad" },
		"owner":         func(v *nativePublicVPSInput) { v.OwnerAnchor = "bad" },
		"claim":         func(v *nativePublicVPSInput) { v.ClaimVersion = "bad" },
		"address":       func(v *nativePublicVPSInput) { v.Address = "victim@example.test" },
		"nonce":         func(v *nativePublicVPSInput) { v.Nonce = "bad" },
		"reader":        func(v *nativePublicVPSInput) { v.Reader = "bad" },
		"same-token":    func(v *nativePublicVPSInput) { v.Control = v.Reader },
		"notify-token":  func(v *nativePublicVPSInput) { v.Notification = "invalid" },
		"notify-reused": func(v *nativePublicVPSInput) { v.Notification = v.Reader },
		"certificate":   func(v *nativePublicVPSInput) { v.RelayCA = "bad" },
	} {
		t.Run(name, func(t *testing.T) {
			v := input
			mutate(&v)
			encoded, _ := json.Marshal(v)
			if _, err := parseNativePublicVPS(encoded); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
	for name, bad := range map[string][]byte{
		"duplicate": append([]byte(`{"Source":"duplicate",`), raw[1:]...),
		"unknown":   append([]byte(`{"Unexpected":true,`), raw[1:]...),
		"trailing":  append(append([]byte(nil), raw...), []byte(`{}`)...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNativePublicVPS(bad); err == nil {
				t.Fatal("ambiguous input accepted")
			}
		})
	}
}

func TestNativePublicVPSSignals(t *testing.T) {
	dir := t.TempDir()
	if err := nativePublicVPSSignal(dir, "routes-ready"); err != nil {
		t.Fatal(err)
	}
	if err := nativePublicVPSSignal(dir, "routes-ready"); err == nil {
		t.Fatal("readiness was overwritten")
	}
	if err := nativePublicVPSSignal(dir, "other"); err == nil {
		t.Fatal("unexpected signal accepted")
	}
	path := filepath.Join(dir, "public-ready.json")
	if err := os.WriteFile(path, []byte(`{"VerificationID":"one","MessageID":"two"}`), 0600); err != nil {
		t.Fatal(err)
	}
	ready, err := nativePublicVPSAwait(context.Background(), dir)
	if err != nil || ready.VerificationID != "one" || ready.MessageID != "two" {
		t.Fatal("valid readiness not accepted", err)
	}
	for name, raw := range map[string]string{
		"same":      `{"VerificationID":"one","MessageID":"one"}`,
		"unknown":   `{"VerificationID":"one","MessageID":"two","body":"private"}`,
		"duplicate": `{"VerificationID":"one","VerificationID":"two","MessageID":"three"}`,
		"oversized": strings.Repeat("x", 1025),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNativePublicVPSReady([]byte(raw)); err == nil {
				t.Fatal("ambiguous readiness accepted")
			}
		})
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = nativePublicVPSAwait(context.Background(), dir); err == nil {
		t.Fatal("readiness with public permissions accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "routes-ready"), path); err != nil {
		t.Fatal(err)
	}
	if _, err = nativePublicVPSAwait(context.Background(), dir); err == nil {
		t.Fatal("symlink readiness accepted")
	}
	if err := os.WriteFile(filepath.Join(dir, "abort"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = nativePublicVPSAwait(context.Background(), dir); err == nil {
		t.Fatal("abort ignored")
	}
	if err := os.Remove(filepath.Join(dir, "abort")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = nativePublicVPSAwait(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled wait continued", err)
	}
}
