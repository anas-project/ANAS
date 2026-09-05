package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSambaFSRuntimeUsesOnlyADDomainInputs(t *testing.T) {
	artifacts := map[string]string{
		"compose":  "../docker-compose.yml",
		"init":     "../samba_fs/root/etc/cont-init.d/11-samba_fs.sh",
		"join":     "../samba_fs/root/usr/local/bin/join_ad.sh",
		"kerberos": "../samba_fs/root/etc/krb5.conf.envsubst",
		"register": "../samba_fs/root/usr/local/bin/register_ad_dns.sh",
		"samba":    "../samba_fs/root/etc/samba/smb.conf.envsubst",
	}
	contents := make(map[string]string, len(artifacts))
	for name, path := range artifacts {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		contents[name] = string(b)
	}

	// These values belong to the application namespace or obsolete DNS aliases.
	// None is a valid source for AD realm discovery or member trust. The one
	// intentional host-network input is SAMBA_FS_DC_TRANSPORT_IP, derived from
	// VLAN_BRIDGE_IP by the hook solely to cross the macvlan host boundary.
	for _, forbidden := range []string{
		"BASE_DOMAIN",
		"SAMBA_DC_HOST",
		"SAMBA_FS_DNS_SERVER",
		"LOCAL_DNS_SERVER",
		"HOST_DNS_SERVER",
		"net ads leave",
	} {
		for name, content := range contents {
			if strings.Contains(content, forbidden) {
				t.Errorf("%s contains forbidden AD input/action %q", name, forbidden)
			}
		}
	}

	requireFragments(t, "Kerberos template", contents["kerberos"],
		"default_realm = ${SAMBA_DC_REALM}",
		"kdc = ${SAMBA_DC_DC_DOMAIN}",
		"default_domain = ${SAMBA_DC_DOMAIN}",
		".${SAMBA_DC_DOMAIN} = ${SAMBA_DC_REALM}",
	)
	requireFragments(t, "Samba template", contents["samba"],
		"workgroup = ${SAMBA_DC_WORKGROUP}",
		"realm = ${SAMBA_DC_REALM}",
	)
	requireFragments(t, "container initialization", contents["init"],
		". /usr/local/bin/join_ad.sh",
		"nameserver $SAMBA_DC_DNS_SERVER",
		"search $SAMBA_DC_DNS_SEARCH",
		"$SAMBA_FS_DC_TRANSPORT_IP  $SAMBA_DC_DC_DOMAIN",
		`ip route replace "$SAMBA_DC_DNS_SERVER/32" via "$SAMBA_FS_DC_TRANSPORT_IP"`,
		"$fs_hostname.$SAMBA_DC_DOMAIN",
		`register_ad_dns.sh "$HOST_IP"`,
		"refusing to start with an unverified member address",
	)
	requireFragments(t, "Compose", contents["compose"],
		"- ${SAMBA_DC_DNS_SERVER}",
		"- ${SAMBA_DC_DNS_SEARCH}",
		`test: ["CMD", "wbinfo", "-t"]`,
	)
	requireFragments(t, "join helper", contents["join"],
		"net ads testjoin",
		"net ads join",
		"Joined AD %s and verified the machine trust",
		"$SAMBA_DC_DOMAIN",
		"$SAMBA_DC_ADMIN_NAME",
		"$SAMBA_DC_ADMIN_PASSWORD",
	)
	requireFragments(t, "DNS registration helper", contents["register"],
		`KRB5CCNAME="FILE:$ccache_path"`,
		`kinit "$SAMBA_DC_ADMIN_NAME"`,
		`"$SAMBA_DC_DC_DOMAIN" "$SAMBA_DC_DOMAIN" "$record_name" A`,
		"--use-kerberos=required",
		`host "$fqdn" "$SAMBA_DC_DNS_SERVER"`,
	)
}

func TestADRegistrationReconcilesAddressAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace")
	statePath := filepath.Join(dir, "addresses")
	if err := os.WriteFile(statePath, []byte("192.0.2.99\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	installFakeDNSCommands(t, dir)
	scriptPath, err := filepath.Abs("../samba_fs/root/usr/local/bin/register_ad_dns.sh")
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(),
		"PATH="+dir+":"+os.Getenv("PATH"),
		"TRACE_FILE="+tracePath,
		"DNS_STATE_FILE="+statePath,
		"XDG_RUNTIME_DIR="+dir,
		"SAMBA_DC_ADMIN_NAME=Administrator",
		"SAMBA_DC_ADMIN_PASSWORD=test-password",
		"SAMBA_DC_DC_DOMAIN=dc.ad.internal.example",
		"SAMBA_DC_DNS_SERVER=192.0.2.10",
		"SAMBA_DC_DOMAIN=ad.internal.example",
		"SAMBA_FS_DC_TRANSPORT_IP=192.0.2.11",
		"SAMBA_FS_HOSTNAME=SambaFS",
	)
	run := func() string {
		t.Helper()
		cmd := exec.Command("bash", scriptPath, "192.0.2.42")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("DNS registration failed: %v\n%s", err, out)
		}
		if strings.Contains(string(out), "test-password") {
			t.Fatal("domain administrator password was logged")
		}
		return string(out)
	}

	run()
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(state)); got != "192.0.2.42" {
		t.Fatalf("registered addresses = %q, want only current member address", got)
	}
	firstTrace := readTestTrace(t, tracePath)
	for _, fragment := range []string{
		"kinit Administrator",
		"query dc.ad.internal.example ad.internal.example sambafs",
		"delete 192.0.2.99",
		"add 192.0.2.42",
		"host sambafs.ad.internal.example 192.0.2.10",
	} {
		if !strings.Contains(firstTrace, fragment) {
			t.Errorf("registration trace is missing %q:\n%s", fragment, firstTrace)
		}
	}
	if strings.Contains(firstTrace, "test-password") {
		t.Fatal("domain administrator password reached the command trace")
	}

	run()
	secondTrace := readTestTrace(t, tracePath)
	if strings.Count(secondTrace, "add ") != 1 || strings.Count(secondTrace, "delete ") != 1 {
		t.Fatalf("second registration was not idempotent:\n%s", secondTrace)
	}
}

func TestSambaFSManifestClaimsADDomainInputs(t *testing.T) {
	b, err := os.ReadFile("../module.yml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(b)
	if !strings.Contains(manifest, "dependencies:\n  requires:\n    - samba_dc") {
		t.Error("samba_fs must keep samba_dc as an explicit startup dependency")
	}
	for _, key := range []string{
		"SAMBA_DC_ADMIN_NAME",
		"SAMBA_DC_ADMIN_PASSWORD",
		"SAMBA_DC_DC_DOMAIN",
		"SAMBA_DC_DNS_SEARCH",
		"SAMBA_DC_DNS_SERVER",
		"SAMBA_DC_DOMAIN",
		"SAMBA_DC_REALM",
		"SAMBA_DC_WORKGROUP",
	} {
		if !strings.Contains(manifest, "- "+key) {
			t.Errorf("module manifest does not consume %s", key)
		}
	}
}

func TestApplicationDomainChangePreservesValidMembership(t *testing.T) {
	binDir, tracePath := installFakeDomainCommands(t)
	joinPath, err := filepath.Abs("../samba_fs/root/usr/local/bin/join_ad.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `. "$1"
BASE_DOMAIN=old.apps.example
export BASE_DOMAIN
join_domain
BASE_DOMAIN=new.apps.example
export BASE_DOMAIN
join_domain`, "join-test", joinPath)
	cmd.Env = domainTestEnv(binDir, tracePath, "", "0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("join helper failed: %v\n%s", err, out)
	}
	trace := readTestTrace(t, tracePath)
	if got := strings.Count(trace, "testjoin\n"); got != 2 {
		t.Fatalf("testjoin count = %d, want 2; trace:\n%s", got, trace)
	}
	for _, unexpected := range []string{"join", "leave", "kinit"} {
		if traceHasLine(trace, unexpected) {
			t.Fatalf("valid trust unexpectedly performed %q; trace:\n%s", unexpected, trace)
		}
	}
	if strings.Contains(trace, "bad-") {
		t.Fatalf("valid trust produced an invalid command trace:\n%s", trace)
	}
	for _, applicationDomain := range []string{"old.apps.example", "new.apps.example"} {
		if strings.Contains(string(out), applicationDomain) || strings.Contains(trace, applicationDomain) {
			t.Fatalf("application domain %q reached join behavior", applicationDomain)
		}
	}
}

func TestTransientDCStartupDoesNotRejoinValidMembership(t *testing.T) {
	binDir, tracePath := installFakeDomainCommands(t)
	joinPath, err := filepath.Abs("../samba_fs/root/usr/local/bin/join_ad.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `. "$1"; join_domain`, "join-test", joinPath)
	// The first trust check represents a DC that has not started accepting
	// requests. The second succeeds after the helper's retry delay.
	cmd.Env = domainTestEnv(binDir, tracePath, "1", "0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("join helper failed: %v\n%s", err, out)
	}
	trace := readTestTrace(t, tracePath)
	if got := strings.Count(trace, "testjoin\n"); got != 2 {
		t.Fatalf("testjoin count = %d, want 2; trace:\n%s", got, trace)
	}
	for _, unexpected := range []string{"join", "leave", "kinit"} {
		if traceHasLine(trace, unexpected) {
			t.Fatalf("transient DC startup unexpectedly performed %q; trace:\n%s", unexpected, trace)
		}
	}
	if strings.Contains(string(out), "apps.example") || strings.Contains(trace, "apps.example") {
		t.Fatalf("application domain reached trust recovery behavior; output:\n%s\ntrace:\n%s", out, trace)
	}
}

func TestInvalidMembershipJoinsConfiguredADDomainWithoutLeave(t *testing.T) {
	binDir, tracePath := installFakeDomainCommands(t)
	joinPath, err := filepath.Abs("../samba_fs/root/usr/local/bin/join_ad.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `. "$1"; join_domain`, "join-test", joinPath)
	cmd.Env = domainTestEnv(binDir, tracePath, "", "1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("join helper failed: %v\n%s", err, out)
	}
	trace := readTestTrace(t, tracePath)
	for _, expected := range []string{"testjoin", "kinit", "join"} {
		if !traceHasLine(trace, expected) {
			t.Errorf("trace is missing %q:\n%s", expected, trace)
		}
	}
	if traceHasLine(trace, "leave") || strings.Contains(trace, "bad-") {
		t.Errorf("trace contains an invalid join action:\n%s", trace)
	}
	if !strings.Contains(string(out), "ad.internal.example") {
		t.Fatalf("join output does not identify the configured AD domain:\n%s", out)
	}
	if strings.Contains(string(out), "apps.example") || strings.Contains(trace, "apps.example") {
		t.Fatalf("application domain reached join behavior; output:\n%s\ntrace:\n%s", out, trace)
	}
	if strings.Contains(string(out), "test-password") || strings.Contains(trace, "test-password") {
		t.Fatal("domain administrator password was logged")
	}
	if got := strings.Count(trace, "testjoin\n"); got != 3 {
		t.Fatalf("testjoin count = %d, want preflight, retry, and post-join verification; trace:\n%s", got, trace)
	}
}

func TestJoinSuccessDoesNotReturnUntilMachineTrustVerifies(t *testing.T) {
	binDir, tracePath := installFakeDomainCommands(t)
	joinPath, err := filepath.Abs("../samba_fs/root/usr/local/bin/join_ad.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", "-c", `. "$1"; join_domain`, "join-test", joinPath)
	cmd.Env = append(domainTestEnv(binDir, tracePath, "", "1"), "TESTJOIN_POST_JOIN_FAILURES=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("join helper failed: %v\n%s", err, out)
	}
	trace := readTestTrace(t, tracePath)
	if got := traceLineCount(trace, "join"); got != 2 {
		t.Fatalf("join count = %d, want retry after failed post-join trust verification; trace:\n%s", got, trace)
	}
	if got := strings.Count(trace, "testjoin\n"); got != 5 {
		t.Fatalf("testjoin count = %d, want verification after each join; trace:\n%s", got, trace)
	}
	if !strings.Contains(string(out), "returned success but trust verification failed") ||
		!strings.Contains(string(out), "verified the machine trust") {
		t.Fatalf("join output does not expose verification and recovery:\n%s", out)
	}
}

func requireFragments(t *testing.T, name, content string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(content, fragment) {
			t.Errorf("%s is missing %q", name, fragment)
		}
	}
}

func installFakeDomainCommands(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace")
	writeTestExecutable(t, filepath.Join(dir, "net"), `#!/usr/bin/env bash
case "$1 $2" in
  "ads testjoin")
    printf 'testjoin\n' >> "$TRACE_FILE"
    join_count="$(grep -c '^join$' "$TRACE_FILE")"
    post_join_failures="${TESTJOIN_POST_JOIN_FAILURES:-0}"
    if [ "$join_count" -gt 0 ]; then
      if [ "$join_count" -le "$post_join_failures" ]; then
        exit 1
      fi
      exit 0
    fi
    count="$(grep -c '^testjoin$' "$TRACE_FILE")"
    if [ "$count" -eq 1 ] && [ -n "$TESTJOIN_FIRST_STATUS" ]; then
      exit "$TESTJOIN_FIRST_STATUS"
    fi
    exit "$TESTJOIN_STATUS"
    ;;
  "ads join")
    printf 'join\n' >> "$TRACE_FILE"
    last="${!#}"
    if [ "$last" != "$SAMBA_DC_ADMIN_NAME%$SAMBA_DC_ADMIN_PASSWORD" ]; then
      printf 'bad-join-credential\n' >> "$TRACE_FILE"
    fi
    exit 0
    ;;
  "ads leave")
    printf 'leave\n' >> "$TRACE_FILE"
    exit 0
    ;;
esac
printf 'bad-net-command\n' >> "$TRACE_FILE"
exit 2
`)
	writeTestExecutable(t, filepath.Join(dir, "kinit"), `#!/usr/bin/env bash
IFS= read -r password
printf 'kinit\n' >> "$TRACE_FILE"
if [ "$1" != "$SAMBA_DC_ADMIN_NAME" ] || [ "$password" != "$SAMBA_DC_ADMIN_PASSWORD" ]; then
  printf 'bad-kinit-credential\n' >> "$TRACE_FILE"
fi
exit 0
`)
	writeTestExecutable(t, filepath.Join(dir, "sleep"), `#!/usr/bin/env bash
exit 0
`)
	return dir, tracePath
}

func installFakeDNSCommands(t *testing.T, dir string) {
	t.Helper()
	writeTestExecutable(t, filepath.Join(dir, "kinit"), `#!/usr/bin/env bash
IFS= read -r password
printf 'kinit %s\n' "$1" >> "$TRACE_FILE"
[[ "$password" == "$SAMBA_DC_ADMIN_PASSWORD" ]]
`)
	writeTestExecutable(t, filepath.Join(dir, "kdestroy"), `#!/usr/bin/env bash
exit 0
`)
	writeTestExecutable(t, filepath.Join(dir, "samba-tool"), `#!/usr/bin/env bash
set -euo pipefail
action=$2
case "$action" in
  query)
    printf 'query %s %s %s\n' "$3" "$4" "$5" >> "$TRACE_FILE"
    while IFS= read -r address; do
      [[ -n "$address" ]] && printf '  A: %s (flags=600000f0, serial=1, ttl=900)\n' "$address"
    done < "$DNS_STATE_FILE"
    ;;
  delete)
    address=$7
    printf 'delete %s\n' "$address" >> "$TRACE_FILE"
    awk -v remove="$address" '$0 != remove' "$DNS_STATE_FILE" > "$DNS_STATE_FILE.tmp"
    mv "$DNS_STATE_FILE.tmp" "$DNS_STATE_FILE"
    ;;
  add)
    address=$7
    printf 'add %s\n' "$address" >> "$TRACE_FILE"
    printf '%s\n' "$address" >> "$DNS_STATE_FILE"
    ;;
  *) exit 2 ;;
esac
`)
	writeTestExecutable(t, filepath.Join(dir, "host"), `#!/usr/bin/env bash
printf 'host %s %s\n' "$1" "$2" >> "$TRACE_FILE"
while IFS= read -r address; do
  [[ -n "$address" ]] && printf '%s has address %s\n' "$1" "$address"
done < "$DNS_STATE_FILE"
`)
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func domainTestEnv(binDir, tracePath, testjoinFirstStatus, testjoinStatus string) []string {
	return append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"TRACE_FILE="+tracePath,
		"TESTJOIN_FIRST_STATUS="+testjoinFirstStatus,
		"TESTJOIN_STATUS="+testjoinStatus,
		"BASE_DOMAIN=apps.example",
		"SAMBA_DC_ADMIN_NAME=Administrator",
		"SAMBA_DC_ADMIN_PASSWORD=test-password",
		"SAMBA_DC_DOMAIN=ad.internal.example",
		"SAMBA_FS_LOG_LEVEL=1",
	)
}

func readTestTrace(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func traceHasLine(trace, want string) bool {
	for _, line := range strings.Split(strings.TrimSpace(trace), "\n") {
		if line == want {
			return true
		}
	}
	return false
}

func traceLineCount(trace, want string) int {
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(trace), "\n") {
		if line == want {
			count++
		}
	}
	return count
}
