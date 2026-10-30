//go:build linux

// TEST_CASES: MAILMOD-T-072, MAILMOD-T-073, MAILMOD-T-081
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/audit"
	"github.com/anas-project/ANAS/internal/consoleaudit"
	"github.com/anas-project/ANAS/internal/consoleconfig"
	"github.com/anas-project/ANAS/internal/consolestate"
	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/internal/serviceauth"
	"github.com/anas-project/ANAS/internal/tempconsolecert"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type fixtureNoticeRecorder struct {
	notices chan store.IngressNotice
	persist interface {
		RecordIngressNotice(context.Context, store.IngressNotice) error
	}
}

func (f fixtureNoticeRecorder) RecordIngressNotice(ctx context.Context, n store.IngressNotice) error {
	if f.persist != nil {
		if err := f.persist.RecordIngressNotice(ctx, n); err != nil {
			return err
		}
	}
	select {
	case f.notices <- n:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Fixture-only PostgreSQL, with no business mounts and a loopback-only port.
func adminFixturePostgres(t *testing.T, root, label string, run func(...string) (string, error)) *sql.DB {
	t.Helper()
	const img = "ghcr.io/anas-project/anas-postgres:18.4.0-r5"
	const expected = "sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362"
	if id, err := run("image", "inspect", img, "--format", "{{.Id}}"); err != nil || id != expected {
		t.Fatal("pinned PostgreSQL image unavailable")
	}
	random := make([]byte, 64)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	bootstrapPassword, password := hex.EncodeToString(random[:32]), hex.EncodeToString(random[32:])
	clear(random)
	envPath := filepath.Join(root, "postgres.env")
	if err := os.WriteFile(envPath, []byte("POSTGRES_USER=fixture_bootstrap\nPOSTGRES_DB=postgres\nPOSTGRES_PASSWORD="+bootstrapPassword+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := run("create", "--pull", "never", "--label", "anas.mail.admin-vps-pg="+label, "--network", "bridge", "-p", "127.0.0.1::5432", "--memory", "256m", "--pids-limit", "128", "--env-file", envPath, img)
	if err != nil || len(id) != 64 {
		t.Fatal("fixture PostgreSQL creation failed")
	}
	t.Cleanup(func() {
		observed, err := run("inspect", "--format", `{{index .Config.Labels "anas.mail.admin-vps-pg"}}`, id)
		if err != nil || observed != label {
			t.Error("fixture PostgreSQL ownership unknown")
			return
		}
		if _, err := run("rm", "-f", "-v", id); err != nil {
			t.Error("fixture PostgreSQL cleanup failed")
		}
	})
	if _, err := run("start", id); err != nil {
		t.Fatal("fixture PostgreSQL start failed")
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, err = run("exec", id, "pg_isready", "-h", "127.0.0.1", "-U", "fixture_bootstrap", "-d", "postgres")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture PostgreSQL readiness timeout")
		}
		time.Sleep(time.Second)
	}
	ports, err := run("inspect", "--format", `{{json .NetworkSettings.Ports}}`, id)
	if err != nil {
		t.Fatal("fixture PostgreSQL port unavailable")
	}
	var bindings map[string][]struct{ HostIP, HostPort string }
	if json.Unmarshal([]byte(ports), &bindings) != nil || len(bindings["5432/tcp"]) != 1 || bindings["5432/tcp"][0].HostIP != "127.0.0.1" {
		t.Fatal("fixture PostgreSQL port is not loopback-only")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	u := url.URL{Scheme: "postgres", User: url.UserPassword("fixture_bootstrap", bootstrapPassword), Host: net.JoinHostPort("127.0.0.1", bindings["5432/tcp"][0].HostPort), Path: "/postgres", RawQuery: "sslmode=disable"}
	bootstrap, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal("fixture bootstrap open failed")
	}
	if _, err := bootstrap.ExecContext(ctx, "CREATE ROLE anas_mail_model LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '"+password+"'"); err != nil {
		bootstrap.Close()
		t.Fatal("fixture role creation failed")
	}
	if _, err := bootstrap.ExecContext(ctx, "CREATE DATABASE anas_mail_model_1025 OWNER anas_mail_model"); err != nil {
		bootstrap.Close()
		t.Fatal("fixture database creation failed")
	}
	if err := bootstrap.Close(); err != nil {
		t.Fatal("fixture bootstrap close failed")
	}
	u.User, u.Path = url.UserPassword("anas_mail_model", password), "/anas_mail_model_1025"
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal("fixture database open failed")
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error("fixture database close failed")
		}
	})
	var database, user string
	var superuser bool
	if db.QueryRowContext(ctx, "SELECT current_database(), current_user, rolsuper FROM pg_roles WHERE rolname=current_user").Scan(&database, &user, &superuser) != nil || database != "anas_mail_model_1025" || user != "anas_mail_model" || superuser {
		t.Fatal("fixture database identity not verified")
	}
	if err := (store.Store{DB: db}).Migrate(ctx); err != nil {
		t.Fatal("fixture migration failed")
	}
	return db
}

// Opt-in, disposable daemon and non-root container. The only remote endpoint
// is the separately leased and namespace-isolated SSH fixture.
func TestMailVPSAdminSocketLive(t *testing.T) {
	if os.Getenv("MAIL_VPS_ADMIN_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("requires isolated root and Docker fixture")
	}
	serviceBinary := os.Getenv("MAIL_VPS_ADMIN_SERVICE_TEST")
	sshPrivate := os.Getenv("ANAS_TEST_VPS_SSH_KEY")
	sshPublic := os.Getenv("ANAS_TEST_VPS_SSH_HOSTKEY")
	sshPort := os.Getenv("ANAS_TEST_VPS_SSH_PORT")
	if serviceBinary == "" || sshPrivate == "" || sshPublic == "" || sshPort == "" {
		t.Fatal("missing test fixture")
	}
	root, err := os.MkdirTemp("/run", "anas-mail-admin-vps-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	_, workspace := relayHostWorkspace(t)
	socketDir := filepath.Join(root, "socket")
	if err := os.Mkdir(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(socketDir, 0, 65532); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socketDir, 0750); err != nil {
		t.Fatal(err)
	}
	cert, err := tempconsolecert.Generate(tempconsolecert.Options{Directory: filepath.Join(root, "cert"), IPAddresses: []string{"127.0.0.1"}})
	if err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(cert.CertificatePath)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(root, "ca.pem")
	if err := os.WriteFile(caPath, ca, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(caPath, 0644); err != nil {
		t.Fatal(err)
	}
	console := filepath.Join(root, "console")
	writer, err := audit.Open(console)
	if err != nil {
		t.Fatal(err)
	}
	state, err := consolestate.Open(context.Background(), console, consoleaudit.StateSink{Writer: writer})
	if err != nil {
		t.Fatal(err)
	}
	for _, next := range []consolestate.State{consolestate.StateEnrollment, consolestate.StateFull} {
		if _, err := state.Transition(context.Background(), next, "test"); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := consoleconfig.Config{Mode: consoleconfig.ModeLoopback, Port: 18445, ConsoleStore: console, Workspaces: []consoleconfig.Workspace{{ID: "main", Path: workspace}}, TLS: consoleconfig.TLSConfig{Temporary: &consoleconfig.TemporaryTLSPaths{Certificate: cert.CertificatePath, PrivateKey: cert.PrivateKeyPath, IPAddresses: []string{"127.0.0.1"}}}, ServiceAPI: &serviceauth.Config{Directory: filepath.Join(root, "clients"), Clients: []serviceauth.Client{{ID: "mail-test", Enabled: true, UID: 65532, GID: 65532, Grants: []serviceauth.Grant{{Permission: "mail.ingress.install", Workspace: "main", Resources: []string{"mail"}}}}}}, MailInstallSocket: &consoleconfig.MailInstallSocketConfig{Directory: socketDir, GroupID: 65532}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runConfiguredWithServiceReload(ctx, cfg, nil, func(_ context.Context, network, _ string) (net.Listener, error) {
			if network == "tcp6" {
				return nil, syscall.EAFNOSUPPORT
			}
			return net.Listen("tcp4", "127.0.0.1:0")
		}, nil)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("daemon shutdown timed out")
		}
	})
	socket := filepath.Join(socketDir, "install.sock")
	tokenDir := filepath.Join(cfg.ServiceAPI.Directory, "mail-test")
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, se := os.Stat(socket)
		_, te := os.Stat(filepath.Join(tokenDir, "token"))
		if se == nil && te == nil {
			break
		}
		if time.Now().After(deadline) {
			select {
			case e := <-done:
				done <- e
				t.Fatalf("daemon startup %v", e)
			default:
				t.Fatal("daemon not ready")
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Docker bind mounts are root-resolved. The client runs under its true UID,
	// and the temporary test SSH key is explicitly owned by that UID only.
	sshDir := filepath.Join(root, "ssh")
	if err := os.Mkdir(sshDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(sshDir, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	files := []struct {
		src, name string
		mode      os.FileMode
	}{{sshPrivate, "client", 0400}, {sshPublic, "hostkey.pub", 0400}}
	full := os.Getenv("MAIL_VPS_ADMIN_FULL") == "1"
	if full {
		files = append(files, struct {
			src, name string
			mode      os.FileMode
		}{os.Getenv("ANAS_TEST_VPS_SOURCE_CERT"), "relay.crt", 0400}, struct {
			src, name string
			mode      os.FileMode
		}{os.Getenv("ANAS_TEST_VPS_SOURCE_KEY"), "relay.key", 0400})
	}
	for _, item := range files {
		data, err := os.ReadFile(item.src)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(sshDir, item.name)
		if err := os.WriteFile(target, data, item.mode); err != nil {
			t.Fatal(err)
		}
		clear(data)
		if err := os.Chown(target, 65532, 65532); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target, item.mode); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.ReadFile(serviceBinary)
	if err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(root, "service-test")
	if err := os.WriteFile(binaryPath, binary, 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(binaryPath, 0555); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/docker", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	const img = "ghcr.io/docker-mailserver/docker-mailserver:16.0.1"
	const expected = "sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86"
	if id, err := run("image", "inspect", img, "--format", "{{.Id}}"); err != nil || id != expected {
		t.Fatal("pinned image unavailable")
	}
	label := filepath.Base(root)
	id, err := run("create", "--pull", "never", "--label", "anas.mail.admin-vps-test="+label, "--network", "none", "--memory", "192m", "--pids-limit", "64", "--user", "65532:65532", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--env", "MAIL_VPS_ADMIN_CLIENT_LIVE=1", "--env", "MAIL_VPS_ADMIN_FULL="+os.Getenv("MAIL_VPS_ADMIN_FULL"), "--env", "MAIL_VPS_ADMIN_FORWARD="+os.Getenv("MAIL_VPS_ADMIN_FORWARD"), "--env", "ANAS_TEST_VPS_SOURCE_PORT="+os.Getenv("ANAS_TEST_VPS_SOURCE_PORT"), "--env", "ANAS_TEST_VPS_NOTIFY_PORT="+os.Getenv("ANAS_TEST_VPS_NOTIFY_PORT"), "--env", "ANAS_TEST_VPS_SSH_PORT="+sshPort, "--mount", "type=bind,src="+socketDir+",dst=/socket,readonly", "--mount", "type=bind,src="+tokenDir+",dst=/token,readonly", "--mount", "type=bind,src="+caPath+",dst=/ca.pem,readonly", "--mount", "type=bind,src="+sshDir+",dst=/ssh,readonly", "--mount", "type=bind,src="+binaryPath+",dst=/client,readonly", "--entrypoint", "/client", img, "-test.v", "-test.run", "^TestMailVPSAdminSocketClientLive$")
	if err != nil || len(id) != 64 {
		t.Fatal("container creation failed")
	}
	t.Cleanup(func() {
		observed, err := run("inspect", "--format", `{{index .Config.Labels "anas.mail.admin-vps-test"}}`, id)
		if err != nil || observed != label {
			t.Error("container ownership unknown")
			return
		}
		if _, err = run("rm", "-f", "-v", id); err != nil {
			t.Error("container cleanup failed")
		}
	})
	output, err := run("start", "-a", id)
	if err != nil || !strings.Contains(output, "--- PASS: TestMailVPSAdminSocketClientLive") {
		t.Fatalf("admin client failed: %v %s", err, output)
	}
	journal := filepath.Join(root, "mail-installations", "main", "cfg-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "vps-ssh", "provision")
	if _, err := os.Stat(filepath.Join(journal, "remote-publish.done")); err != nil {
		t.Fatal("publish completion marker absent", err)
	}
	if _, err := os.Stat(filepath.Join(journal, "remote-activate.done")); full && err != nil || !full && !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unexpected activation marker state", err)
	}
	if full {
		snapshot, err := runner.ReadMailIngressSnapshot(t.Context(), workspace)
		if err != nil {
			t.Fatal(err)
		}
		sources, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, []string{"example.test"})
		if err != nil || len(sources) != 1 || sources[0].Credentials.Reader == "" || sources[0].Credentials.Control == "" || sources[0].Credentials.Notification == "" {
			t.Fatal("source transaction not verified", err)
		}
		source := sources[0]
		forward := os.Getenv("MAIL_VPS_ADMIN_FORWARD") == "1"
		if forward && (source.CloudflareForward == nil || source.CloudflareForward.Domain != "delivery.example.test" || source.Credentials.Routing != strings.Repeat("r", 40) || source.Credentials.Destination != strings.Repeat("d", 40)) {
			t.Fatal("forwarding source or private credentials not committed")
		}
		certificate, err := tls.LoadX509KeyPair(os.Getenv("ANAS_TEST_VPS_SOURCE_CERT"), os.Getenv("ANAS_TEST_VPS_SOURCE_KEY"))
		if err != nil {
			t.Fatal(err)
		}
		listener, err := tls.Listen("tcp4", "127.0.0.1:"+os.Getenv("ANAS_TEST_VPS_NOTIFY_PORT"), &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		notices := make(chan store.IngressNotice, 4)
		wake := make(chan string, 4)
		recorder := fixtureNoticeRecorder{notices: notices}
		var db *sql.DB
		if os.Getenv("MAIL_VPS_ADMIN_POSTGRES") == "1" {
			db = adminFixturePostgres(t, root, label, run)
			recorder.persist = store.Store{DB: db}
		}
		handler := ingress.NotificationHandler{Store: recorder, Sources: map[string]ingress.NotificationSource{source.ID: {Generation: source.Generation, Secret: source.Credentials.Notification}}, Wake: func(s string) { wake <- s }}
		server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
		go server.Serve(listener)
		defer server.Close()
		ca, err := os.ReadFile(os.Getenv("ANAS_TEST_VPS_SOURCE_CERT"))
		if err != nil {
			t.Fatal(err)
		}
		client, err := ingress.NewSourceClient(source.ClientConfig(ca))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		box := "11111111-1111-4111-8111-111111111111"
		owner := "22222222-2222-4222-8222-222222222222"
		routes := []ingress.Route{{Address: "main@example.test", MailboxID: box, OwnerAnchor: owner, ClaimVersion: "33333333-3333-4333-8333-333333333333"}, {Address: "alias@example.test", MailboxID: box, OwnerAnchor: owner, ClaimVersion: "44444444-4444-4444-8444-444444444444"}}
		if _, err := client.ReplaceRoutes(t.Context(), 0, "55555555-5555-4555-8555-555555555555", routes); err != nil {
			t.Fatal("route write", err)
		}
		state, err := client.ReadRoutes(t.Context())
		if err != nil || len(state.Bindings) != 2 || state.Revision != 1 {
			t.Fatal("route readback", err)
		}
		message := []byte("From: sender@example.test\r\nTo: alias@example.test\r\nSubject: isolated alias verification\r\n\r\nSynthetic message.\r\n")
		recipient := "alias@example.test"
		if forward {
			var e error
			recipient, e = ingress.ForwardTarget(source.ID, source.Generation, routes[1], source.CloudflareForward.Domain)
			if e != nil {
				t.Fatal("invalid forwarding target", e)
			}
		}
		if err := smtp.SendMail("127.0.0.1:"+os.Getenv("ANAS_TEST_VPS_SMTP_PORT"), nil, "sender@example.test", []string{recipient}, message); err != nil {
			t.Fatal("SMTP delivery", err)
		}
		var received store.IngressNotice
		select {
		case notice := <-notices:
			received = notice
			if notice.SourceID != source.ID || notice.MailboxID != box {
				t.Fatal("wrong notification identity")
			}
		case <-time.After(30 * time.Second):
			t.Fatal("notification not received")
		}
		select {
		case id := <-wake:
			if id != source.ID {
				t.Fatal("wrong wake identity")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("notification did not wake consumer")
		}
		if db != nil {
			data := store.Store{DB: db}
			if err := data.RecordIngressNotice(t.Context(), received); err != nil {
				t.Fatal("duplicate notification rejected")
			}
			conflict := received
			conflict.MailboxID = "66666666-6666-4666-8666-666666666666"
			if err := data.RecordIngressNotice(t.Context(), conflict); !errors.Is(err, store.ErrIngressChanged) {
				t.Fatal("conflicting duplicate notification accepted")
			}
			var count int
			var status string
			if err := db.QueryRowContext(t.Context(), `SELECT count(*), min(state) FROM mail.ingress_notice WHERE source_id=$1 AND generation=$2 AND notification_id=$3 AND mailbox_id=$4`, source.ID, source.Generation, received.ID, box).Scan(&count, &status); err != nil || count != 1 || status != "pending" {
				t.Fatal("persistent notification mismatch")
			}
		}
		var page ingress.SourcePage
		deadline := time.Now().Add(15 * time.Second)
		for {
			page, err = client.List(t.Context(), "")
			if err == nil && len(page.Messages) == 1 && page.Messages[0].Notification != nil && page.Messages[0].Notification.State == "notified" && page.Messages[0].Notification.HTTPStatus == 202 && page.Messages[0].Notification.Attempts >= 1 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("source notification acknowledgement not recorded")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err != nil || len(page.Messages) != 1 || page.Messages[0].Metadata.MailboxID != box {
			t.Fatal("source message not found", err)
		}
		var original bytes.Buffer
		if _, err := client.Download(t.Context(), page.Messages[0], &original); err != nil || !bytes.Contains(original.Bytes(), []byte("Synthetic message.")) {
			t.Fatal("original download", err)
		}
	}
}
