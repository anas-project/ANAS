//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/computeclient"
	"github.com/anas-project/ANAS/internal/securefs"
)

// The fixture installs only its public TLS trust anchor into a new guest. It
// never changes the image artifact, engine service, Incus fence or token path.
// All workload operations still go through the product's shared client.
type nativePublicTrustCompute struct {
	ComputeProvider
	project string
	ca      string
}

type nativeTrustDiagnostic struct{ bytes.Buffer }

func (d *nativeTrustDiagnostic) Write(body []byte) (int, error) {
	_, _ = d.Buffer.Write(body[:min(len(body), 4096-d.Len())])
	return len(body), nil
}

func (p nativePublicTrustCompute) command(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "incus", append([]string{"--force-local", "--project", p.project}, args...)...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/run/anas-forgejo-onejob"}
	var diagnostic nativeTrustDiagnostic
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, io.Discard, &diagnostic
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// These commands transfer only a public fixture CA, before a Runner
		// token is delivered. Keep bounded failure details in the root-private
		// fixture, never in controller logs or the portable evidence bundle.
		if f, e := os.OpenFile("/run/anas-forgejo-onejob/public-trust-error.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600); e == nil {
			_, _ = f.Write(diagnostic.Bytes())
			_ = f.Close()
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			label := "unclassified"
			for _, candidate := range []string{"not running", "no such file", "permission denied", "unknown flag", "not found"} {
				if strings.Contains(strings.ToLower(diagnostic.String()), candidate) {
					label = candidate
					break
				}
			}
			return fmt.Errorf("fixture public TLS trust operation %s exited %d (%s)", args[0], exit.ExitCode(), label)
		}
		return errors.New("fixture public TLS trust program unavailable")
	}
	return nil
}

type nativeTrustFixtureCompute struct{ fakeCompute }

func (p *nativeTrustFixtureCompute) Inspect(_ context.Context, id string) (Instance, error) {
	for _, spec := range p.created {
		if spec.ID == id {
			return Instance{ID: id, State: "stopped", WorkloadID: spec.WorkloadID}, nil
		}
	}
	return Instance{ID: id, State: "missing"}, nil
}

func TestPublicFixtureTrustIsCopiedAfterStartWithReadableMode(t *testing.T) {
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	program := []byte(fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\n", trace))
	if err := os.WriteFile(filepath.Join(dir, "incus"), program, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	base := &nativeTrustFixtureCompute{}
	p := nativePublicTrustCompute{ComputeProvider: base, project: "fixture", ca: "/public-test-ca.crt"}
	ctx := context.Background()
	if err := p.Create(ctx, InstanceSpec{ID: "anas-test", WorkloadID: "job"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatal("CA copy ran before guest start")
	}
	if err := p.Start(ctx, "anas-test"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(trace)
	if err != nil || len(base.started) != 1 {
		t.Fatal("guest was not started", err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "file push --mode=0644 /public-test-ca.crt anas-test/usr/local/share/ca-certificates/") || !strings.HasSuffix(lines[1], "exec anas-test -- /usr/bin/env TMPDIR=/run /usr/sbin/update-ca-certificates") {
		t.Fatalf("unexpected fixture calls: %v", lines)
	}
}

func TestPublicFixtureDiagnosticIsBounded(t *testing.T) {
	var d nativeTrustDiagnostic
	for i := 0; i < 3; i++ {
		if n, err := d.Write(bytes.Repeat([]byte("x"), 3000)); err != nil || n != 3000 {
			t.Fatal(n, err)
		}
	}
	if d.Len() != 4096 {
		t.Fatal("diagnostic exceeded budget")
	}
}

func (p nativePublicTrustCompute) Create(ctx context.Context, spec InstanceSpec) error {
	if err := p.ComputeProvider.Create(ctx, spec); err != nil {
		return err
	}
	view, err := p.ComputeProvider.Inspect(ctx, spec.ID)
	if err != nil || view.ID != spec.ID || view.WorkloadID != spec.WorkloadID || view.State != "stopped" {
		return errors.New("new fixture instance identity was not confirmed")
	}
	return nil
}

func (p nativePublicTrustCompute) Start(ctx context.Context, id string) error {
	if err := p.ComputeProvider.Start(ctx, id); err != nil {
		return err
	}
	// The fixture CA is public, and only guest boot is complete here. File
	// transfer must not depend on stopped-instance file-server support. Keep
	// the controller's private source private, but make its guest copy readable
	// by the actual non-root Runner and registry client.
	if err := p.command(ctx, "file", "push", "--mode=0644", p.ca, id+"/usr/local/share/ca-certificates/anas-onejob-fixture.crt"); err != nil {
		return err
	}
	// Incus exec can become usable before boot-time /tmp cleanup finishes.
	// The CA tool creates multiple temporary files which must survive until
	// it commits the public bundle; use guest tmpfs outside the /tmp cleanup.
	return p.command(ctx, "exec", id, "--", "/usr/bin/env", "TMPDIR=/run", "/usr/sbin/update-ca-certificates")
}

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
	provider, err := newCompute(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	provider = nativePublicTrustCompute{ComputeProvider: provider, project: fixture.Lease.Sandbox, ca: fixture.CA}
	controller := NewController(cfg, NewForgejoClient(cfg.ForgejoURL, cfg.Username, cfg.Password), provider, FileStateStore{Path: cfg.StatePath})
	if err := runControllerLoop(ctx, cfg, controller); err != nil {
		t.Fatal(err)
	}
}
