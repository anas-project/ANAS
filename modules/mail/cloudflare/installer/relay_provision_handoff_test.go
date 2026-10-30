// TEST_CASES: MAILMOD-T-070
package installer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/runner"
	"github.com/anas-project/ANAS/modules/mail/cloudrelay"
	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestRelayProvisionHandoffCommitsGeneratedCredentials(t *testing.T) {
	input := provisionFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	input.Origin = "https://127.0.0.1:" + port
	input.Template.CertificateHost = "127.0.0.1"
	input.Template.Listen = "127.0.0.1:" + port
	provisionDir := provisionDir(t)
	provision, err := OpenRelayProvisionJournal(provisionDir)
	if err != nil {
		t.Fatal(err)
	}
	defer provision.Close()
	relayDir := provisionDirForHandoff(t)
	relay, err := OpenRelayJournal(relayDir)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	auth := &provisionObserver{}
	audit := &relayAudit{}
	service, workspace := installWorkspace(t)
	if _, err = provision.handoffPrepare(context.Background(), input, nil, nil, 8<<20, auth, relay, audit, nil); !errors.Is(err, ErrUnknown) {
		t.Fatal("unactivated source prepared", err)
	}
	remote := func(_ context.Context, _ RelaySSHConfig, action string, _ cloudrelay.RuntimeConfig) (string, error) {
		if action == "publish" {
			return "published", nil
		}
		return "active-local-verified", nil
	}
	if _, _, err = provision.remote(context.Background(), input, "publish", auth, remote); err != nil {
		t.Fatal(err)
	}
	prepared, _, err := provision.remote(context.Background(), input, "activate", auth, remote)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := provisionDirForHandoff(t)
	ledger, err := cloudrelay.InitializeRouteLedger(dataDir, prepared.Runtime.SourceID, prepared.Runtime.Generation, prepared.Runtime.Domains)
	if err != nil {
		t.Fatal(err)
	}
	store, err := cloudrelay.InitializeOriginalStore(ledger, prepared.Runtime.CapacityBytes, prepared.Runtime.DeliveredDays)
	if err != nil {
		t.Fatal(err)
	}
	notifier, err := cloudrelay.NewNotifier(cloudrelay.NotificationConfig{Origin: prepared.Runtime.NotificationOrigin, SourceID: prepared.Runtime.SourceID, Generation: prepared.Runtime.Generation, Secret: prepared.Runtime.NotificationSecret, Reader: prepared.Runtime.Reader, Control: prepared.Runtime.Control})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := cloudrelay.NewSourceHandler(store, prepared.Runtime.Reader, prepared.Runtime.Control, notifier)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair([]byte(input.Template.TLSCertificate), []byte(input.Template.TLSPrivateKey))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	ca := []byte(input.Template.TLSCertificate)
	source, err := provision.handoffPrepare(context.Background(), input, nil, nil, 8<<20, auth, relay, audit, ca)
	if err != nil || source.ID != prepared.Runtime.SourceID {
		t.Fatal("handoff prepare", err)
	}
	marker := filepath.Join(provisionDir, "remote-activate.done")
	saved, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provision.handoffCommit(context.Background(), input, nil, nil, 8<<20, auth, relay, service, audit, noopObserver{}, ca); !errors.Is(err, ErrUnknown) {
		t.Fatal("unconfirmed commit", err)
	}
	after, err := os.ReadFile(filepath.Join(workspace, "config.yml"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("unconfirmed commit changed workspace", err)
	}
	if err = os.WriteFile(marker, saved, 0600); err != nil {
		t.Fatal(err)
	}
	source, err = provision.handoffCommit(context.Background(), input, nil, nil, 8<<20, auth, relay, service, audit, noopObserver{}, ca)
	if err != nil || source.ID != prepared.Runtime.SourceID {
		t.Fatal("handoff commit", err)
	}
	snapshot, err := runner.ReadMailIngressSnapshot(context.Background(), workspace)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, input.AllowedDomains)
	if err != nil || len(sources) != 1 || sources[0].ID != prepared.Runtime.SourceID || sources[0].Credentials.Reader != prepared.Runtime.Reader || sources[0].Credentials.Control != prepared.Runtime.Control || sources[0].Credentials.Notification != prepared.Runtime.NotificationSecret {
		t.Fatal("source handoff mismatch", err)
	}
	if audit.prepares == 0 || audit.commits == 0 {
		t.Fatal("missing authorization audits")
	}
}

// Forwarding credentials stay on the NAS; the actual VPS runtime receives
// only its delivery domain and source credentials. Cloudflare is not called.
func TestRelayProvisionForwardHandoffUsesActualRelay(t *testing.T) {
	input := provisionFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	smtpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	smtpAddress := smtpListener.Addr().String()
	smtpListener.Close()
	input.Origin = "https://" + address
	input.Template.CertificateHost = "127.0.0.1"
	input.Template.Listen = address
	input.Template.ForwardDomain = "delivery.example.test"
	input.CloudflareForward = &ingress.CloudflareForwardSpec{AccountID: strings.Repeat("1", 32), ZoneID: strings.Repeat("2", 32), Domain: input.Template.ForwardDomain}
	input.Routing, input.Destination = strings.Repeat("r", 40), strings.Repeat("d", 40)
	provision, err := OpenRelayProvisionJournal(provisionDir(t))
	if err != nil {
		t.Fatal(err)
	}
	defer provision.Close()
	relay, err := OpenRelayJournal(provisionDirForHandoff(t))
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	auth, audit := &provisionObserver{}, &relayAudit{}
	p, err := provision.Prepare(input)
	if err != nil {
		t.Fatal(err)
	}
	plan := RelayInstallRequest{Runtime: p.Source, TotalCapacity: input.Template.CapacityBytes, OperationID: input.OperationID}
	if err = relay.BindRemotePlan(t.Context(), audit, plan, nil); err != nil {
		t.Fatal(err)
	}
	remote := func(_ context.Context, _ RelaySSHConfig, action string, cfg cloudrelay.RuntimeConfig) (string, error) {
		raw, e := json.Marshal(cfg)
		if e != nil || bytes.Contains(raw, []byte(input.Routing)) || bytes.Contains(raw, []byte(input.Destination)) {
			t.Fatal("provider credentials exposed to relay", e)
		}
		if cfg.ForwardDomain != input.CloudflareForward.Domain {
			t.Fatal("wrong relay delivery domain")
		}
		if action == "publish" {
			return "published", nil
		}
		return "active-local-verified", nil
	}
	if _, _, err = provision.remote(t.Context(), input, "publish", auth, remote); err != nil {
		t.Fatal(err)
	}
	if _, _, err = provision.remote(t.Context(), input, "activate", auth, remote); err != nil {
		t.Fatal(err)
	}
	root := provisionDir(t)
	cfg := p.Runtime
	cfg.Directory = filepath.Join(root, "source")
	cfg.SMTPListen = smtpAddress
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "runtime.json")
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	clear(raw)
	if err = cloudrelay.InitializeRuntimeFile(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- cloudrelay.RunRuntimeFile(ctx, path, nil) }()
	t.Cleanup(func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(10 * time.Second):
			t.Error("relay did not stop")
		}
	})
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: p.Source.SourceID, Generation: p.Source.Generation, Origin: input.Origin, Domains: p.Source.Domains, Token: p.Source.Reader, ControlToken: p.Source.Control, AdditionalCA: []byte(input.Template.TLSCertificate)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err = client.Status(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("relay did not start", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	ca := []byte(input.Template.TLSCertificate)
	spec, err := provision.handoffPrepare(ctx, input, nil, nil, input.Template.CapacityBytes, auth, relay, audit, ca)
	if err != nil || spec.CloudflareForward == nil || *spec.CloudflareForward != *input.CloudflareForward {
		t.Fatal("forward prepare failed", err)
	}
	service, workspace := installWorkspace(t)
	spec, err = provision.handoffCommit(ctx, input, nil, nil, input.Template.CapacityBytes, auth, relay, service, audit, noopObserver{}, ca)
	if err != nil || spec.CloudflareForward == nil {
		t.Fatal("forward commit failed", err)
	}
	snapshot, err := runner.ReadMailIngressSnapshot(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ingress.ParseSources(snapshot.Sources, snapshot.Credentials, input.AllowedDomains)
	if err != nil || len(sources) != 1 || sources[0].CloudflareForward == nil || *sources[0].CloudflareForward != *input.CloudflareForward || sources[0].Credentials.Routing != input.Routing || sources[0].Credentials.Destination != input.Destination {
		t.Fatal("forward config/secret transaction lost binding", err)
	}
	if strings.Contains(snapshot.Sources, input.Routing) || strings.Contains(snapshot.Sources, input.Destination) {
		t.Fatal("provider token exposed in public configuration")
	}
}
func provisionDirForHandoff(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0700); err != nil {
		t.Fatal(err)
	}
	return d
}
