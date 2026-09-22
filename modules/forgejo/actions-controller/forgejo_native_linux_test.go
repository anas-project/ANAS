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
