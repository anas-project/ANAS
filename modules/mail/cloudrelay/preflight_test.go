// TEST_CASES: MAILMOD-T-060
package cloudrelay

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func spareListen(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestInstallPreflightLocalObservationsAndNoMutation(t *testing.T) {
	cfg, _ := initializationFixture(t)
	cfg.Listen = spareListen(t)
	cfg.SMTPListen = spareListen(t)
	for cfg.SMTPListen == cfg.Listen {
		cfg.SMTPListen = spareListen(t)
	}
	cfg.SMTPHostname = "relay.example.test"
	path := writeRuntimeConfig(t, cfg)
	result := PreflightRuntimeFile(path)
	if result.Configuration != "passed" || result.Certificate != "passed" || result.CertificateTrust != "failed" || result.Directory != "available" || result.Disk != "sufficient-now" || result.HTTPSPort != "available-now" || result.SMTPPort != "available-now" || result.PublicReachability != "unknown" {
		t.Fatalf("preflight: %+v", result)
	}
	entries, err := os.ReadDir(filepath.Dir(cfg.Directory))
	if err != nil || len(entries) != 0 {
		t.Fatalf("preflight changed source parent: %v %v", entries, err)
	}
	raw, err := json.Marshal(result)
	if err != nil || strings.Contains(string(raw), cfg.Reader) || strings.Contains(string(raw), cfg.Control) || strings.Contains(string(raw), cfg.TLSPrivateKey) || strings.Contains(string(raw), cfg.Directory) {
		t.Fatal("preflight exposed private configuration")
	}
	if err := InitializeRuntimeFile(path); err != nil {
		t.Fatal(err)
	}
	result = PreflightRuntimeFile(path)
	if result.Directory != "occupied" {
		t.Fatalf("existing source was claimed: %+v", result)
	}
}

func TestInstallPreflightConflictsAndInvalidInputs(t *testing.T) {
	cfg, _ := initializationFixture(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	cfg.Listen = busy.Addr().String()
	cfg.SMTPListen = cfg.Listen
	cfg.SMTPHostname = "relay.example.test"
	result := PreflightRuntimeFile(writeRuntimeConfig(t, cfg))
	if result.HTTPSPort != "unavailable" || result.SMTPPort != "unavailable" || result.PublicReachability != "unknown" {
		t.Fatalf("port conflict: %+v", result)
	}
	cfg.Listen = spareListen(t)
	cfg.SMTPListen = cfg.Listen
	result = PreflightRuntimeFile(writeRuntimeConfig(t, cfg))
	if result.HTTPSPort != "available-now" || result.SMTPPort != "unavailable" {
		t.Fatalf("overlap: %+v", result)
	}
	cfg.SMTPListen = ""
	cfg.SMTPHostname = ""
	cfg.CapacityBytes = routeMaxInteger
	result = PreflightRuntimeFile(writeRuntimeConfig(t, cfg))
	if result.Disk != "insufficient-now" || result.SMTPPort != "not-configured" {
		t.Fatalf("capacity: %+v", result)
	}
	cfg.TLSPrivateKey = "invalid"
	result = PreflightRuntimeFile(writeRuntimeConfig(t, cfg))
	if result.Configuration != "passed" || result.Certificate != "failed" || result.Directory != "unknown" {
		t.Fatalf("bad cert: %+v", result)
	}
	if result = PreflightRuntimeFile("/nonexistent"); result.Configuration != "failed" || result.PublicReachability != "unknown" {
		t.Fatalf("bad input: %+v", result)
	}
}
