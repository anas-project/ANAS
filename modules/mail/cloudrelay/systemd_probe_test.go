// TEST_CASES: MAILMOD-T-064
package cloudrelay

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/ingress"
)

func TestSystemdInspectionAuthenticatedLocalStatus(t *testing.T) {
	reader := strings.Repeat("a", 64)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/status" || r.Header.Get("Authorization") != "Bearer "+reader {
			http.Error(w, "unauthorized", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(ingress.SourceStatus{Schema: "anas.mail-source/v1", SourceID: routeSource, Generation: routeGeneration, CapacityBytes: 8 << 20, DeliveredDays: 7, UsedBytes: 4 << 20, AccountQuota: "unknown", Notifications: "unconfigured"})
	}))
	defer server.Close()
	cfg := RuntimeConfig{Listen: server.Listener.Addr().String(), CertificateHost: "127.0.0.1", Reader: reader, SourceID: routeSource, Generation: routeGeneration, CapacityBytes: 8 << 20, DeliveredDays: 7}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	if err := localSourceStatus(context.Background(), cfg, roots); err != nil {
		t.Fatal(err)
	}
	changedMode := cfg
	changedMode.ForwardDomain = "in.example.test"
	if err := localSourceStatus(context.Background(), changedMode, roots); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("status with a different delivery mode accepted", err)
	}
	changed := cfg
	changed.Reader = strings.Repeat("b", 64)
	if err := localSourceStatus(context.Background(), changed, roots); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("wrong token accepted", err)
	}
	changed = cfg
	changed.Generation = "00000000-0000-4000-8000-000000000000"
	if err := localSourceStatus(context.Background(), changed, roots); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("wrong generation accepted", err)
	}
	if err := localSourceStatus(context.Background(), cfg, x509.NewCertPool()); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("untrusted certificate accepted", err)
	}
	changed = cfg
	changed.Listen = "8.8.8.8:443"
	if err := localSourceStatus(context.Background(), changed, roots); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("external IP accepted", err)
	}
}
func TestSystemdInspectionProcessIdentity(t *testing.T) {
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("requires Linux non-root process")
	}
	binary, err := os.ReadFile("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	pid := strconv.Itoa(os.Getpid())
	if err := verifySystemdProcess(pid, binary); err != nil {
		t.Fatal(err)
	}
	if err := verifySystemdProcess(pid, []byte("other")); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("wrong binary accepted", err)
	}
	if err := verifySystemdProcess("0", binary); !errors.Is(err, ErrPublishUnknown) {
		t.Fatal("zero PID accepted", err)
	}
}

func TestSystemdInspectionLiveUnit(t *testing.T) {
	pid := os.Getenv("ANAS_MAIL_TEST_UNIT_PID")
	if pid == "" {
		t.Skip("requires isolated systemd fixture")
	}
	binary, err := os.ReadFile(os.Getenv("ANAS_MAIL_TEST_UNIT_BINARY"))
	if err != nil {
		t.Fatal(err)
	}
	if err = verifySystemdProcess(pid, binary); err != nil {
		t.Fatalf("process identity: %v", err)
	}
	cfg, err := LoadRuntimeConfig(os.Getenv("ANAS_MAIL_TEST_UNIT_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(os.Getenv("ANAS_MAIL_TEST_UNIT_CA"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("certificate")
	}
	if err = localSourceStatus(context.Background(), cfg, roots); err != nil {
		t.Fatalf("local TLS status: %v", err)
	}
	unit := os.Getenv("ANAS_MAIL_TEST_UNIT_NAME")
	status, err := systemdShowUnit(context.Background(), unit)
	if err != nil || status.ID != unit || status.Fragment != "/run/systemd/system/"+unit || status.LoadState != "loaded" || status.DynamicUser != "yes" || status.DropIns != "" || status.Reload != "no" || status.Transient != "no" || status.Active != "active" || status.SubState != "running" || status.MainPID != pid {
		t.Fatalf("manager state mismatch: %+v %v", status, err)
	}
}
