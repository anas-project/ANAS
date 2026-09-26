//go:build linux

package incusprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Explicitly driven by server-incus-forwarding-e2e.py inside its disposable
// VM. All production operations here are read-only; packet/namespace setup is
// owned by the separate test driver, never enabled by a production switch.
func TestNativeForwardingDiagnostics(t *testing.T) {
	identity := os.Getenv("ANAS_REQUIRE_FORWARDING_NATIVE")
	if identity == "" {
		t.Skip("requires exact disposable forwarding VM identity")
	}
	if !regexp.MustCompile(`^anas-incus-host-[a-f0-9]{6}$`).MatchString(identity) || os.Getuid() != 0 || os.Geteuid() != 0 {
		t.Fatal("invalid native fixture identity")
	}
	instance, ie := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/class/dmi/id/sys_vendor")
	if ie != nil || ve != nil || strings.TrimSpace(string(instance)) != identity || strings.TrimSpace(string(vendor)) != "QEMU" {
		t.Fatal("not the independently owned QEMU VM")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/docker", "info", "--format", "{{.DockerRootDir}}")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	var output limitedBuffer
	output.limit = 512
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil || output.Truncated() || !bytes.Equal(bytes.TrimSpace(output.Bytes()), []byte("/var/lib/anas-forwarding-docker")) {
		t.Fatal("test is not bound to its experimental Docker daemon")
	}
	observed, err := newLocalRuntime().observeForwarding(ctx)
	if err != nil || observed.IPv4Routing != "enabled" || observed.IPv4Filter != "drop_observed" || !observed.DockerUserChain || observed.IPv4BaseChains < 2 {
		t.Fatal("production observation did not retain the later drop and earlier accept", observed, err)
	}
	obs := newFakeRuntime(t).obs
	obs.Forwarding = observed
	plan, err := buildPlan(Request{}, obs, State{})
	if err != nil || plan.ComputeReady || !slices.Contains(plan.Warnings, "ipv4_forward_filter_drop_observed") {
		t.Fatal("the captured forwarding facts did not reach the plan diagnostic")
	}
	encoded, _ := json.Marshal(observed)
	t.Logf("read-only production forwarding diagnostic: %s", encoded)
}
