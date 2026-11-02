//go:build linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/securefs"
)

// Uses a new, loopback-only Forgejo created by the disposable QEMU harness.
// The real API is exercised without Incus or a claim of one-job acceptance.
func TestNativeForgejoScopedRunnerAPI(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_FORGEJO_API_NATIVE") != "1" {
		t.Skip("requires explicit disposable Forgejo API harness")
	}
	var fixture struct {
		VMID     string `json:"vm_id"`
		BaseURL  string `json:"base_url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	path := os.Getenv("ANAS_FORGEJO_API_FIXTURE")
	info, err := os.Lstat(path)
	if err != nil || securefs.ValidateFileInfo(info, "native Forgejo input") != nil || info.Size() > 64<<10 {
		t.Fatal("protected native Forgejo input required")
	}
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, &fixture) != nil {
		t.Fatal("invalid native input")
	}
	clear(body)
	identity, ie := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if ie != nil || ve != nil || strings.TrimSpace(string(identity)) != fixture.VMID || !strings.HasPrefix(fixture.VMID, "anas-runner-bake-") || strings.TrimSpace(string(vendor)) != "QEMU" || fixture.BaseURL != "http://127.0.0.1:13000" || fixture.Username != "anas-api-lab" || fixture.Password == "" {
		t.Fatal("exact disposable loopback Forgejo fixture required")
	}
	for _, p := range []string{"/var/run/docker.sock", "/var/lib/docker"} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatal("Docker must be absent")
		}
	}
	client := NewForgejoClient(fixture.BaseURL, fixture.Username, fixture.Password)
	for _, raw := range []string{"anas-api-lab/repo", "anas-api-org"} {
		t.Run(strings.ReplaceAll(raw, "/", "-"), func(t *testing.T) {
			scope, err := singleScope(raw)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			jobs, err := client.ListJobs(ctx, scope, "docker:docker://node:24")
			if err != nil || len(jobs) != 0 {
				t.Fatal("empty scoped job listing failed", err)
			}
			registration, err := client.CreateRunner(ctx, scope, "anas-api-fixture")
			if err != nil {
				t.Fatal("real ephemeral registration failed", err)
			}
			t.Cleanup(func() {
				clean, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				if err := client.DeleteRunner(clean, scope, registration.ID); err != nil {
					t.Error("native registration cleanup failed", err)
				}
			})
			if !runnerTokenPattern.MatchString(registration.Token) {
				t.Fatal("invalid ephemeral token")
			}
			registration.Token = ""
			if err := client.DeleteRunner(ctx, scope, registration.ID); err != nil {
				t.Fatal("real registration delete failed", err)
			}
			if err := client.DeleteRunner(ctx, scope, registration.ID); err != nil {
				t.Fatal("real registration repeat delete failed", err)
			}
		})
	}
}

// This separate fixture deliberately uses a fake ComputeProvider. It verifies
// real 17.0.1/PostgreSQL registration recovery, not Incus instance acceptance.
func TestIsolatedPostgresRunnerRecovery(t *testing.T) {
	if os.Getenv("ANAS_FORGEJO_POSTGRES_FIXTURE") != "1" {
		t.Skip("requires dedicated PostgreSQL fixture")
	}
	fixtureNS, err := os.Stat("/run/netns/anas-fj-1030")
	if err != nil {
		t.Fatal(err)
	}
	currentNS, err := os.Stat("/proc/self/ns/net")
	if err != nil || !os.SameFile(fixtureNS, currentNS) {
		t.Fatal("dedicated fixture network namespace required")
	}
	const passwordPath = "/home/whl/anas-forgejo-1030/fixture/password"
	info, err := os.Lstat(passwordPath)
	if err != nil || securefs.ValidateFileInfo(info, "PostgreSQL fixture") != nil {
		t.Fatal("private fixture credential required")
	}
	password, err := os.ReadFile(passwordPath)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(password)
	client := NewForgejoClient("http://127.0.0.1:13000", "anas-forgejo-upgrade-test", string(password))
	var version struct {
		Version string `json:"version"`
	}
	if err := client.(*forgejoClient).do(context.Background(), "GET", "/api/v1/version", nil, &version, 200); err != nil || !strings.HasPrefix(version.Version, "17.0.1") {
		t.Fatal("fixed Forgejo 17.0.1 fixture required")
	}
	for _, scope := range []Scope{{Owner: "anas-forgejo-upgrade-test", Repo: "upgrade-proof"}, {Owner: "upgrade-org"}} {
		t.Run(scope.String(), func(t *testing.T) {
			// This fixture performs multiple authenticated requests on a shared
			// test server. Keep individual client deadlines; allow a bounded
			// overall budget for two full reconciliation cycles plus probes.
			ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
			defer cancel()
			c, _, compute, _, _ := controllerFixture()
			c.cfg.Password = string(password)
			c.cfg.Scopes = []Scope{scope}
			c.cfg.Lease.Sandbox = "fixture-no-real-compute"
			c.forgejo = client
			id := instanceIDFor("lost-state-fixture")
			compute.managed = []Instance{{ID: id, WorkloadID: "lost-state-fixture", State: "running"}}
			owned, err := client.CreateRunner(ctx, scope, c.runnerName(scope, id))
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := client.CreateRunner(ctx, scope, "manual-fixture-runner")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				clean, stop := context.WithTimeout(context.Background(), 15*time.Second)
				defer stop()
				_ = client.DeleteRunner(clean, scope, owned.ID)
				_ = client.DeleteRunner(clean, scope, foreign.ID)
			})
			owned.Token = ""
			foreign.Token = ""
			for cycle := 0; cycle < 2; cycle++ {
				if err := c.Reconcile(ctx); err != nil {
					t.Fatal(err)
				}
			}
			runners, err := client.ListRunners(ctx, scope)
			if err != nil {
				t.Fatal(err)
			}
			foundForeign := false
			for _, r := range runners {
				if r.ID == owned.ID {
					t.Fatal("orphan registration retained")
				}
				if r.ID == foreign.ID {
					foundForeign = true
				}
			}
			if !foundForeign || len(compute.deleted) != 2 {
				t.Fatalf("foreign retained=%v fake compute deletions=%d", foundForeign, len(compute.deleted))
			}
		})
	}
}
