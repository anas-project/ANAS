//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/securefs"
)

// Launched and supervised as a separate process by the explicit disposable-VM
// harness. Normal termination exercises the production loop's cleanup; the
// crash case kills only this child and then reopens its real file state store.
func TestNativeOneJobControllerProcess(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_FORGEJO_ONEJOB_NATIVE") != "1" {
		t.Skip("requires explicit disposable QEMU one-job harness")
	}
	const root = "/run/anas-forgejo-onejob"
	var fixture struct {
		VMID     string              `json:"vm_id"`
		BaseURL  string              `json:"base_url"`
		Username string              `json:"username"`
		Password string              `json:"password"`
		CA       string              `json:"ca"`
		Label    string              `json:"label"`
		Lease    computeclient.Lease `json:"lease"`
	}
	info, err := os.Lstat(root + "/fixture.json")
	if err != nil || os.Geteuid() != 0 || securefs.ValidateFileInfo(info, "one-job fixture") != nil || info.Size() > 64<<10 {
		t.Fatal("protected one-job fixture required")
	}
	body, err := os.ReadFile(root + "/fixture.json")
	if err != nil || json.Unmarshal(body, &fixture) != nil {
		t.Fatal("invalid one-job fixture")
	}
	clear(body)
	identity, ie := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if ie != nil || ve != nil || strings.TrimSpace(string(identity)) != fixture.VMID || !strings.HasPrefix(fixture.VMID, "anas-runner-bake-") || strings.TrimSpace(string(vendor)) != "QEMU" {
		t.Fatal("exact disposable QEMU identity required")
	}
	for _, path := range []string{"/var/run/docker.sock", "/var/lib/docker"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("business Docker must not be available")
		}
	}
	if fixture.BaseURL != "https://10.0.2.15:13001" || fixture.Username != "anas-onejob-lab" || fixture.Password == "" || fixture.CA != root+"/fixture-ca.crt" || fixture.Lease.Validate() != nil || fixture.Lease.Sandbox != "anas-onejob-fixture" || fixture.Lease.InstancePrefix != "anas-fj-" || fixture.Lease.Interface != computeclient.InterfaceContainer || len(fixture.Lease.ImageAllowlist) != 1 || fixture.Label != "anas-native:docker://public.ecr.aws/docker/library/busybox@sha256:7a3ebe5bfd1a4a19797d20b0c0bb39d44393e9a03fd852c0865b0f540d868df0" {
		t.Fatal("invalid isolated service or lease binding")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	cfg := Config{Enabled: true, ForgejoURL: fixture.BaseURL, RunnerURL: fixture.BaseURL + "/", Username: fixture.Username, Password: fixture.Password,
		Scopes: []Scope{{Owner: fixture.Username, Repo: "onejob"}}, RunnerImage: fixture.Lease.ImageAllowlist[0], RunnerLabel: fixture.Label,
		StatePath: filepath.Join(root, "state", "state.json"), ConfigDir: filepath.Join(root, "client"), Lease: fixture.Lease,
		PollInterval: 15 * time.Second, OperationTTL: 2 * time.Minute, WaitingTTL: 10 * time.Minute, JobTimeout: time.Hour,
		MaxConcurrent: 1, MaxPerScope: 1, CPU: 1, MemoryMiB: 768, DiskGiB: 8}
	publicCA, err := readRunnerTrustFile(fixture.CA, 0)
	if err != nil {
		t.Fatal("read explicit public fixture CA")
	}
	cfg.RunnerTrustPEM, err = normalizeRunnerTrust(publicCA, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	provider, err := newCompute(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	// The production controller now supplies public trust in the bounded
	// stdin frame. Do not use the historical file-push/guest trust-store fixture.
	controller := NewController(cfg, NewForgejoClient(cfg.ForgejoURL, cfg.Username, cfg.Password), provider, FileStateStore{Path: cfg.StatePath})
	if err := runControllerLoop(ctx, cfg, controller); err != nil {
		t.Fatal(err)
	}
}
