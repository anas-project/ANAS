// TEST_CASES: MAILMOD-T-052
package cloudrelay

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

func runtimeCertificate(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(17), Subject: pkix.Name{CommonName: "isolated mail test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"relay.example.test"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}
func runtimeFixture(t *testing.T, dir, listen string) RuntimeConfig {
	t.Helper()
	cert, key := runtimeCertificate(t)
	return RuntimeConfig{Schema: "anas.mail-vps-runtime/v1", Directory: dir, SourceID: routeSource, Generation: routeGeneration, Domains: []string{"example.test"}, CapacityBytes: routeMax + 1000, DeliveredDays: 7, Listen: listen, CertificateHost: "127.0.0.1", Reader: strings.Repeat("a", 64), Control: strings.Repeat("b", 64), TLSCertificate: cert, TLSPrivateKey: key}
}
func writeRuntimeConfig(t *testing.T, cfg RuntimeConfig) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runtime.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestRuntimeTLSWorkersRestartAndExclusiveLock(t *testing.T) {
	_, source, dir := originalFixture(t, routeMax+1000)
	if _, err := source.Store(routeOp3, "", []string{"alice@example.test"}, []byte("real runtime original")); err != nil {
		t.Fatal(err)
	}
	var count atomic.Int32
	registrar := &recordedNotices{notices: map[string]store.IngressNotice{}}
	nas := httptest.NewTLSServer(ingress.NotificationHandler{Store: registrar, Sources: map[string]ingress.NotificationSource{routeSource: {Generation: routeGeneration, Secret: strings.Repeat("c", 64)}}, Wake: func(id string) { registrar.wake(id); count.Add(1) }})
	defer nas.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := runtimeFixture(t, dir, listener.Addr().String())
	cfg.NotificationOrigin = nas.URL
	cfg.NotificationSecret = strings.Repeat("c", 64)
	cfg.NotificationCA = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: nas.Certificate().Raw}))
	path := writeRuntimeConfig(t, cfg)
	loaded, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareRuntime(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if second, e := prepareRuntime(loaded); !errors.Is(e, ErrRuntimeBusy) {
		if second != nil {
			second.close()
		}
		t.Fatalf("instance lock: %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- prepared.serve(ctx, listener, nil, nil) }()
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: "https://" + listener.Addr().String(), Token: cfg.Reader, ControlToken: cfg.Control, Domains: cfg.Domains, AdditionalCA: []byte(cfg.TLSCertificate)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(5 * time.Second)
	var status ingress.SourceStatus
	for {
		status, err = client.Status(context.Background())
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runtime status: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.Notifications != "configured" || status.DeliveredDays != 7 || status.CapacityBytes != cfg.CapacityBytes {
		t.Fatalf("runtime config: %+v", status)
	}
	for count.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if count.Load() != 1 {
		t.Fatal("runtime notification scheduler did not send")
	}
	var page ingress.SourcePage
	for {
		page, err = client.List(context.Background(), "")
		if err == nil && len(page.Messages) == 1 && page.Messages[0].Notification != nil && page.Messages[0].Notification.State == "notified" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("notification not persisted: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var body bytes.Buffer
	if _, err = client.Download(context.Background(), page.Messages[0], &body); err != nil || body.String() != "real runtime original" {
		t.Fatalf("download: %v", err)
	}
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	prepared.close()
	prepared, err = prepareRuntime(loaded)
	if err != nil {
		t.Fatalf("restart lock: %v", err)
	}
	defer prepared.close()
	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- prepared.serve(ctx2, listener2, nil, nil) }()
	client2, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: "https://" + listener2.Addr().String(), Token: cfg.Reader, ControlToken: cfg.Control, Domains: cfg.Domains, AdditionalCA: []byte(cfg.TLSCertificate)})
	if err != nil {
		t.Fatal(err)
	}
	defer client2.Close()
	deadline = time.Now().Add(5 * time.Second)
	for {
		page, err = client2.List(context.Background(), "")
		if err == nil && len(page.Messages) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("restart source: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if page.Messages[0].Notification == nil || page.Messages[0].Notification.State != "notified" {
		t.Fatal("notification state lost on restart")
	}
	time.Sleep(200 * time.Millisecond)
	if count.Load() != 1 {
		t.Fatal("restart resent confirmed notice")
	}
	cancel2()
	if err = <-done2; err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeConfigRejectsUnsafeInputs(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	cfg := runtimeFixture(t, dir, "127.0.0.1:18448")
	path := writeRuntimeConfig(t, cfg)
	loaded, err := LoadRuntimeConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"permissions", "symlink", "duplicate", "wrong-host", "wrong-key", "missing-source", "shared-secret", "listen-name", "bad-notify-ca", "smtp-missing-host", "smtp-bad-listen", "smtp-wrong-cert"} {
		t.Run(mode, func(t *testing.T) {
			bad := loaded
			file := path
			switch mode {
			case "permissions":
				file = writeRuntimeConfig(t, bad)
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				file = filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(path, file); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				raw, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				raw = append([]byte(`{"schema":"anas.mail-vps-runtime/v1",`), raw[1:]...)
				file = filepath.Join(t.TempDir(), "duplicate.json")
				if err = os.WriteFile(file, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "wrong-host":
				bad.CertificateHost = "wrong.example.test"
				file = writeRuntimeConfig(t, bad)
			case "wrong-key":
				_, bad.TLSPrivateKey = runtimeCertificate(t)
				file = writeRuntimeConfig(t, bad)
			case "missing-source":
				bad.SourceID = routeOp2
				file = writeRuntimeConfig(t, bad)
			case "shared-secret":
				bad.Control = bad.Reader
				file = writeRuntimeConfig(t, bad)
			case "listen-name":
				bad.Listen = "localhost:18448"
				file = writeRuntimeConfig(t, bad)
			case "bad-notify-ca":
				bad.NotificationOrigin = "https://example.test"
				bad.NotificationSecret = strings.Repeat("c", 64)
				bad.NotificationCA = "bad"
				file = writeRuntimeConfig(t, bad)
			case "smtp-missing-host":
				bad.SMTPListen = "127.0.0.1:1025"
				file = writeRuntimeConfig(t, bad)
			case "smtp-bad-listen":
				bad.SMTPListen, bad.SMTPHostname = "localhost:1025", "relay.example.test"
				file = writeRuntimeConfig(t, bad)
			case "smtp-wrong-cert":
				bad.SMTPListen, bad.SMTPHostname = "127.0.0.1:1025", "other.example.test"
				file = writeRuntimeConfig(t, bad)
			}
			parsed, e := LoadRuntimeConfig(file)
			if e == nil {
				p, prepErr := prepareRuntime(parsed)
				if p != nil {
					p.close()
				}
				e = prepErr
			}
			if e == nil || strings.Contains(e.Error(), bad.Reader) || strings.Contains(e.Error(), bad.TLSPrivateKey) {
				t.Fatalf("unsafe configuration accepted or leaked: %v", e)
			}
		})
	}
}

func TestRunRuntimeFilePortConflictReleasesLock(t *testing.T) {
	_, _, dir := originalFixture(t, routeMax+1000)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	cfg := runtimeFixture(t, dir, address)
	path := writeRuntimeConfig(t, cfg)
	if err = RunRuntimeFile(context.Background(), path, nil); !errors.Is(err, ErrRuntimeUnavailable) {
		listener.Close()
		t.Fatalf("occupied listener: %v", err)
	}
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunRuntimeFile(ctx, path, nil) }()
	client, err := ingress.NewSourceClient(ingress.SourceConfig{ID: routeSource, Generation: routeGeneration, Origin: "https://" + address, Token: cfg.Reader, ControlToken: cfg.Control, Domains: cfg.Domains, AdditionalCA: []byte(cfg.TLSCertificate)})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err = client.Status(context.Background())
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("restart after port conflict: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("runtime did not stop")
	}
}
