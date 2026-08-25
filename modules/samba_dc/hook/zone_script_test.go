package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnasZoneScriptSyntax(t *testing.T) {
	script := anasZoneScriptPath()
	if output, err := exec.Command("bash", "-n", script).CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", script, err, output)
	}
}

func TestAnasZoneRelativeName(t *testing.T) {
	tests := []struct {
		name string
		fqdn string
		zone string
		want string
	}{
		{
			name: "ad_zone keeps a multi-label owner",
			fqdn: "nc.nas.lnnj.com.cn",
			zone: "lnnj.com.cn",
			want: "nc.nas",
		},
		{
			name: "separate_zone uses the short owner",
			fqdn: "nc.nas.example.net",
			zone: "nas.example.net",
			want: "nc",
		},
		{
			name: "zone apex uses at",
			fqdn: "nas.example.net.",
			zone: "NAS.EXAMPLE.NET",
			want: "@",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, err := callRelativeName(anasZoneScriptPath(), test.fqdn, test.zone)
			if err != nil {
				t.Fatalf("relative_name(%q, %q): %v\n%s", test.fqdn, test.zone, err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("relative_name(%q, %q) = %q, want %q", test.fqdn, test.zone, got, test.want)
			}
		})
	}
}

func TestAnasZoneRelativeNameRejectsFalseSuffix(t *testing.T) {
	output, err := callRelativeName(anasZoneScriptPath(), "nc.notexample.com", "example.com")
	if err == nil {
		t.Fatalf("relative_name accepted a false suffix; output %q", output)
	}
}

func TestAnasZoneFindsDelegatingChildZoneAtAnyLabelDepth(t *testing.T) {
	bin := t.TempDir()
	sambaTool := filepath.Join(bin, "samba-tool")
	fixture := `#!/bin/sh
cat <<'EOF'
  pszZoneName                 : example.test
  pszZoneName                 : unrelated.test
  pszZoneName                 : eu.example.test
  pszZoneName                 : apps.eu.example.test
EOF
`
	if err := os.WriteFile(sambaTool, []byte(fixture), 0o700); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export BASE_DOMAIN=nas.eu.example.test
export SAMBA_DC_HOST=nas.eu.example.test
export PATH="$2:$PATH"
. "$1"
conflicting_child_zone
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin).CombinedOutput()
	if err != nil {
		t.Fatalf("conflicting_child_zone: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "eu.example.test" {
		t.Fatalf("conflicting_child_zone = %q, want eu.example.test", got)
	}
}

func TestAnasZoneRejectsChildZoneInterceptingManagedFQDN(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      string
		zone      string
		zoneState string
		zoneList  string
	}{
		{
			name:     "ad zone",
			mode:     "ad_zone",
			zone:     "example.test",
			zoneList: "example.test\nnc.nas.example.test\n",
		},
		{
			name:      "separate zone",
			mode:      "separate_zone",
			zone:      "nas.example.test",
			zoneState: "separate_zone\tnas.example.test\tanas\n",
			zoneList:  "nas.example.test\nnc.nas.example.test\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0o700); err != nil {
				t.Fatal(err)
			}
			fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = zonelist ]; then
  while IFS= read -r zone; do
    [ -n "$zone" ] && printf '  pszZoneName                 : %s\n' "$zone"
  done < "$FAKE_ZONE_LIST"
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = zonecreate ]; then
  : > "$FAKE_ZONE_MUTATED"
  exit 0
fi
exit 2
`
			if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			zoneListPath := filepath.Join(root, "zones")
			zoneStatePath := filepath.Join(root, "zone-state")
			mutatedPath := filepath.Join(root, "mutated")
			if err := os.WriteFile(zoneListPath, []byte(test.zoneList), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.zoneState != "" {
				if err := os.WriteFile(zoneStatePath, []byte(test.zoneState), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export BASE_DOMAIN=nas.example.test
export SAMBA_DC_HOST=nas.example.test
export DOMAINS=inner/nc.nas.example.test/nextcloud
export SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED="$3"
export SAMBA_DC_APPLICATION_DNS_ZONE="$4"
export FAKE_ZONE_LIST="$5"
export FAKE_ZONE_MUTATED="$7"
export PATH="$2:$PATH"
. "$1"
zone_state="$6"
prepare_zone
`
			output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, test.mode, test.zone, zoneListPath, zoneStatePath, mutatedPath).CombinedOutput()
			if err == nil || !strings.Contains(string(output), "intercepts managed name nc.nas.example.test") {
				t.Fatalf("intercepting child zone was accepted: err=%v output=%s", err, output)
			}
			if _, statErr := os.Stat(mutatedPath); !os.IsNotExist(statErr) {
				t.Fatalf("child-zone rejection mutated DNS: %v", statErr)
			}
		})
	}
}

func TestAnasZoneLegacyAdoptionMarkerSurvivesReadinessReset(t *testing.T) {
	zoneScript, err := os.ReadFile(anasZoneScriptPath())
	if err != nil {
		t.Fatal(err)
	}
	initScript, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "etc", "cont-init.d", "11-samba_dc.sh"))
	if err != nil {
		t.Fatal(err)
	}
	const marker = ".anas-legacy-zone-adoption"
	if !strings.Contains(string(zoneScript), marker) || !strings.Contains(string(initScript), marker) {
		t.Fatalf("legacy adoption marker must be produced by init and consumed by the reconciler")
	}
	for _, line := range strings.Split(string(initScript), "\n") {
		if strings.Contains(line, "rm -f") && strings.Contains(line, marker) {
			t.Fatalf("init removes the durable legacy adoption marker: %s", line)
		}
	}
}

func TestAnasZoneDoesNotClaimZoneWhenInventoryProbeFails(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = zonelist ]; then
  exit 42
fi
if [ "$1" = dns ] && [ "$2" = zonecreate ]; then
  : > "$FAKE_ZONE_CREATED"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	zoneState := filepath.Join(root, "zone-state")
	created := filepath.Join(root, "zone-created")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=ad.example.test
export SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED=separate_zone
export SAMBA_DC_APPLICATION_DNS_ZONE=nas.example.test
export FAKE_ZONE_CREATED="$4"
export PATH="$2:$PATH"
. "$1"
zone_state="$3"
prepare_zone
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, zoneState, created).CombinedOutput()
	if err == nil {
		t.Fatalf("transient zonelist failure was treated as zone absence; output=%s", output)
	}
	if _, statErr := os.Stat(zoneState); !os.IsNotExist(statErr) {
		t.Fatalf("zone probe failure created ownership state: %v", statErr)
	}
	if _, statErr := os.Stat(created); !os.IsNotExist(statErr) {
		t.Fatalf("zone probe failure attempted zonecreate: %v", statErr)
	}
}

func TestAnasZoneDoesNotTreatMalformedInventoryAsZoneAbsence(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = zonelist ]; then
  printf 'successful response with no recognizable zone fields\n'
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = zonecreate ]; then
  : > "$FAKE_ZONE_CREATED"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	zoneState := filepath.Join(root, "zone-state")
	created := filepath.Join(root, "zone-created")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=ad.example.test
export SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED=separate_zone
export SAMBA_DC_APPLICATION_DNS_ZONE=nas.example.test
export FAKE_ZONE_CREATED="$4"
export PATH="$2:$PATH"
. "$1"
zone_state="$3"
prepare_zone
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, zoneState, created).CombinedOutput()
	if err == nil {
		t.Fatalf("malformed zonelist was treated as zone absence; output=%s", output)
	}
	if _, statErr := os.Stat(zoneState); !os.IsNotExist(statErr) {
		t.Fatalf("malformed zone inventory created ownership state: %v", statErr)
	}
	if _, statErr := os.Stat(created); !os.IsNotExist(statErr) {
		t.Fatalf("malformed zone inventory attempted zonecreate: %v", statErr)
	}
}

func TestAnasZoneCreateFailureWithdrawsPendingOwnership(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = zonelist ]; then
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = zonecreate ]; then
  exit 42
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	zoneState := filepath.Join(root, "zone-state")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=ad.example.test
export SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED=separate_zone
export SAMBA_DC_APPLICATION_DNS_ZONE=nas.example.test
export PATH="$2:$PATH"
. "$1"
zone_state="$3"
prepare_zone
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, zoneState).CombinedOutput()
	if err == nil {
		t.Fatalf("failed zonecreate unexpectedly succeeded; output=%s", output)
	}
	if _, statErr := os.Stat(zoneState); !os.IsNotExist(statErr) {
		t.Fatalf("failed zonecreate retained pending ownership: %v", statErr)
	}
}

func TestAnasZoneDeleteFailureRetainsManagedOwnership(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  printf '    A: 10.0.0.9 (flags=0, serial=1, ttl=900)\n'
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = delete ]; then
  exit 42
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	desired := filepath.Join(root, "desired.tsv")
	const state = "example.test\tstale.example.test\tA\t10.0.0.9\tnextcloud\tdep-old\n"
	if err := os.WriteFile(managed, []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(desired, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
desired_state="$4"
delete_removed_managed_records
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, desired).CombinedOutput()
	if err == nil {
		t.Fatalf("delete failure was ignored; output=%s", output)
	}
	body, readErr := os.ReadFile(managed)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != state {
		t.Fatalf("delete failure changed managed ownership state:\n%s", body)
	}
}

func TestAnasZonePendingJournalClosesInitialAddCrashWindow(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  if [ "$5" = @ ]; then exit 0; fi
  if [ -f "$FAKE_DNS_STATE" ]; then
    printf '    A: 10.0.0.8 (flags=0, serial=1, ttl=900)\n'
    exit 0
  fi
  exit 1
fi
if [ "$1" = dns ] && [ "$2" = add ]; then
  : > "$FAKE_DNS_STATE"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "pending.tsv")
	desired := filepath.Join(root, "desired.tsv")
	dnsState := filepath.Join(root, "dns-state")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export ANAS_DEPLOYMENT_ID=dep-new
export FAKE_DNS_STATE="$5"
export PATH="$2:$PATH"
. "$1"
pending_state="$3"
desired_state="$4"
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.8 nextcloud
test -s "$pending_state"
# Simulate a crash before the managed manifest commit. On restart the exact
# record is adopted from the durable intent instead of rejected as unmanaged.
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.8 nextcloud
grep -Fq $'example.test\tapp.example.test\tA\t10.0.0.8' "$desired_state"
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, pending, desired, dnsState).CombinedOutput()
	if err != nil {
		t.Fatalf("pending journal reconciliation: %v\n%s", err, output)
	}
}

func TestAnasZonePendingJournalLetsOldDesiredRollbackInterruptedReplacement(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  if [ "$5" = @ ]; then exit 0; fi
  if [ -s "$FAKE_DNS_STATE" ]; then
    while IFS= read -r value; do
      [ -n "$value" ] && printf '    A: %s (flags=0, serial=1, ttl=900)\n' "$value"
    done < "$FAKE_DNS_STATE"
    exit 0
  fi
  exit 1
fi
if [ "$1" = dns ] && [ "$2" = delete ]; then
  grep -Fxv "$7" "$FAKE_DNS_STATE" > "$FAKE_DNS_STATE.next" || true
  mv "$FAKE_DNS_STATE.next" "$FAKE_DNS_STATE"
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = add ]; then
  printf '%s\n' "$7" >> "$FAKE_DNS_STATE"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	pending := filepath.Join(root, "pending.tsv")
	desired := filepath.Join(root, "desired.tsv")
	dnsState := filepath.Join(root, "dns-state")
	if err := os.WriteFile(managed, []byte("example.test\tapp.example.test\tA\t10.0.0.8\tnextcloud\tdep-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dnsState, []byte("10.0.0.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export FAKE_DNS_STATE="$6"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
pending_state="$4"
desired_state="$5"
: > "$desired_state"
# New deployment replaces A with B, then crashes before committing managed_state.
ensure_managed_a_record app.example.test 10.0.0.9 nextcloud
grep -Fq $'example.test\tapp.example.test\tA\t10.0.0.9' "$pending_state"
test "$(cat "$FAKE_DNS_STATE")" = 10.0.0.9
# Compensation starts the old deployment. The pending B is owned strongly
# enough to delete, while managed_state still proves A is the desired old value.
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.8 nextcloud
test "$(cat "$FAKE_DNS_STATE")" = 10.0.0.8
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, pending, desired, dnsState).CombinedOutput()
	if err != nil {
		t.Fatalf("replacement rollback journal: %v\n%s", err, output)
	}
}

func TestAnasZoneReplacementRejectsUnjournaledManualTarget(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  if [ "$5" = @ ]; then exit 0; fi
  while IFS= read -r value; do
    [ -n "$value" ] && printf '    A: %s (flags=0, serial=1, ttl=900)\n' "$value"
  done < "$FAKE_DNS_STATE"
  exit 0
fi
if [ "$1" = dns ] && { [ "$2" = add ] || [ "$2" = delete ]; }; then
  : > "$FAKE_DNS_MUTATED"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	pending := filepath.Join(root, "pending.tsv")
	desired := filepath.Join(root, "desired.tsv")
	dnsState := filepath.Join(root, "dns-state")
	mutated := filepath.Join(root, "mutated")
	if err := os.WriteFile(managed, []byte("example.test\tapp.example.test\tA\t10.0.0.8\tnextcloud\tdep-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dnsState, []byte("10.0.0.8\n10.0.0.9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export FAKE_DNS_STATE="$6"
export FAKE_DNS_MUTATED="$7"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
pending_state="$4"
desired_state="$5"
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.9 nextcloud
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, pending, desired, dnsState, mutated).CombinedOutput()
	if err == nil {
		t.Fatalf("unjournaled manual replacement target was claimed; output=%s", output)
	}
	if _, statErr := os.Stat(mutated); !os.IsNotExist(statErr) {
		t.Fatalf("manual target rejection mutated DNS: %v", statErr)
	}
	if _, statErr := os.Stat(pending); !os.IsNotExist(statErr) {
		t.Fatalf("manual target rejection wrote pending ownership: %v", statErr)
	}
}

func TestAnasZoneAddFailureWithdrawsPendingRecordOwnership(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  if [ "$5" = @ ]; then exit 0; fi
  if [ -f "$FAKE_DNS_STATE" ]; then
    printf '    A: 10.0.0.9 (flags=0, serial=1, ttl=900)\n'
    exit 0
  fi
  exit 1
fi
if [ "$1" = dns ] && [ "$2" = add ]; then
  # Model an already-exists race: the value appears, but the RPC does not
  # prove that this invocation created it.
  : > "$FAKE_DNS_STATE"
  exit 42
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(root, "pending.tsv")
	desired := filepath.Join(root, "desired.tsv")
	dnsState := filepath.Join(root, "dns-state")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export FAKE_DNS_STATE="$5"
export PATH="$2:$PATH"
. "$1"
pending_state="$3"
desired_state="$4"
: > "$desired_state"
if ensure_managed_a_record app.example.test 10.0.0.9 nextcloud; then
  exit 90
fi
test ! -e "$pending_state"
# The raced value is now observed, but without a successful mutation journal
# it remains manual and must not be promoted on retry.
if ensure_managed_a_record app.example.test 10.0.0.9 nextcloud; then
  exit 91
fi
test ! -e "$pending_state"
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, pending, desired, dnsState).CombinedOutput()
	if err != nil {
		t.Fatalf("failed add ownership withdrawal: %v\n%s", err, output)
	}
}

func TestAnasZoneJournalWriteFailurePreventsDNSMutation(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  if [ "$5" = @ ]; then exit 0; fi
  printf '    A: 10.0.0.8 (flags=0, serial=1, ttl=900)\n'
  exit 0
fi
if [ "$1" = dns ] && { [ "$2" = add ] || [ "$2" = delete ]; }; then
  : > "$FAKE_DNS_MUTATED"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	desired := filepath.Join(root, "desired.tsv")
	readOnly := filepath.Join(root, "read-only")
	if err := os.WriteFile(managed, []byte("example.test\tapp.example.test\tA\t10.0.0.8\tnextcloud\tdep-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	mutated := filepath.Join(root, "mutated")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export FAKE_DNS_MUTATED="$7"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
pending_state="$4/pending.tsv"
desired_state="$5"
: > "$desired_state"
retry 1 journal-test ensure_managed_a_record app.example.test 10.0.0.9 nextcloud
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, readOnly, desired, "unused", mutated).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "cannot persist pending DNS ownership intent") {
		t.Fatalf("journal failure did not stop reconciliation: err=%v output=%s", err, output)
	}
	if _, statErr := os.Stat(mutated); !os.IsNotExist(statErr) {
		t.Fatalf("journal failure allowed DNS mutation: %v", statErr)
	}
}

func TestAnasZoneRejectsUnexpectedAdditionalManagedARecord(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  printf '    A: 10.0.0.8 (flags=0, serial=1, ttl=900)\n'
  printf '    A: 10.0.0.99 (flags=0, serial=1, ttl=900)\n'
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	desired := filepath.Join(root, "desired.tsv")
	if err := os.WriteFile(managed, []byte("example.test\tapp.example.test\tA\t10.0.0.8\tnextcloud\tdep-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
desired_state="$4"
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.8 nextcloud
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, desired).CombinedOutput()
	if err == nil || !strings.Contains(string(output), "not proven by ANAS managed or pending state") {
		t.Fatalf("additional A record was accepted: err=%v output=%s", err, output)
	}
}

func TestAnasZoneDuplicateDirectoryNativeApexIsObservedNotDeleted(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  printf '    A: 10.0.0.8 (flags=0, serial=1, ttl=900)\n'
  printf '    A: 10.0.0.8 (flags=f0, serial=2, ttl=900)\n'
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = delete ]; then
  printf 'unexpected delete\n' >> "$FAKE_DELETE_LOG"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	desired := filepath.Join(root, "desired.tsv")
	deleteLog := filepath.Join(root, "delete.log")
	if err := os.WriteFile(managed, []byte("example.test\texample.test\tA\t10.0.0.8\tsamba_dc\tdep-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export SAMBA_DC_HOST=example.test
export FAKE_DELETE_LOG="$5"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
desired_state="$4"
: > "$desired_state"
ensure_directory_native_alias example.test 10.0.0.8
delete_removed_managed_records
grep -Fq $'example.test\texample.test\tA_NATIVE\t10.0.0.8\tsamba_directory' "$desired_state"
test ! -e "$FAKE_DELETE_LOG"
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, desired, deleteLog).CombinedOutput()
	if err != nil {
		t.Fatalf("directory-native apex observation: %v\n%s", err, output)
	}
}

func TestAnasZoneCanonicalDCCollisionIsObservedNotClaimed(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  printf '    A: %s (flags=0, serial=1, ttl=900)\n' "$FAKE_DNS_TARGET"
  exit 0
fi
if [ "$1" = dns ] && { [ "$2" = add ] || [ "$2" = delete ]; }; then
  printf '%s\n' "$*" >> "$FAKE_MUTATION_LOG"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	pending := filepath.Join(root, "pending.tsv")
	desired := filepath.Join(root, "desired.tsv")
	mutationLog := filepath.Join(root, "mutations.log")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export ANAS_DEPLOYMENT_ID=dep-new
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=test.example
export SAMBA_DC_DC_DOMAIN=nas.test.example
export SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED=ad_zone
export SAMBA_DC_APPLICATION_DNS_ZONE=test.example
export SAMBA_DC_HOST=nas.test.example
export SAMBA_DC_HOST_IP=10.0.0.8
export DOMAINS=
export FAKE_MUTATION_LOG="$6"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
pending_state="$4"
desired_state="$5"

# A directory-native observation accepts only the exact address Samba owns;
# it must never repair a mismatch by claiming or mutating the canonical DC A.
export FAKE_DNS_TARGET=10.0.0.9
: > "$desired_state"
if ensure_directory_native_alias "$SAMBA_DC_HOST" "$SAMBA_DC_HOST_IP"; then
  echo 'mismatched canonical DC A was accepted' >&2
  exit 20
fi
test ! -e "$pending_state"
test ! -e "$FAKE_MUTATION_LOG"

# Once Samba publishes the exact address, build_desired_records must select
# observe-only even though the name is below (not equal to) the AD zone apex.
export FAKE_DNS_TARGET=10.0.0.8
: > "$desired_state"
build_desired_records
grep -Fq $'test.example\tnas.test.example\tA_NATIVE\t10.0.0.8\tsamba_directory' "$desired_state"
test ! -e "$managed_state"
test ! -e "$pending_state"
test ! -e "$FAKE_MUTATION_LOG"

# Release stale ownership written by an older reconciler without deleting the
# A record that Samba registers for its canonical DC name.
printf 'test.example\tnas.test.example\tA\t10.0.0.8\tsamba_dc\tdep-old\n' > "$managed_state"
: > "$desired_state"
build_desired_records
delete_removed_managed_records
grep -Fq $'test.example\tnas.test.example\tA_NATIVE\t10.0.0.8\tsamba_directory' "$desired_state"
test ! -e "$FAKE_MUTATION_LOG"
`
	output, err := exec.Command(
		"bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin,
		managed, pending, desired, mutationLog,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("canonical DC collision observation: %v\n%s", err, output)
	}
}

func TestAnasZoneLegacySameTargetNeverAcquiresDeleteOwnership(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/sh
if [ "$1" = dns ] && [ "$2" = query ]; then
  printf '    A: 10.0.0.8 (flags=0, serial=1, ttl=900)\n'
  exit 0
fi
if [ "$1" = dns ] && [ "$2" = delete ]; then
  printf 'unexpected delete\n' >> "$FAKE_DELETE_LOG"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "samba-tool"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(root, "managed.tsv")
	desired := filepath.Join(root, "desired.tsv")
	deleteLog := filepath.Join(root, "delete.log")
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=example.test
export SAMBA_DC_APPLICATION_DNS_ZONE=example.test
export FAKE_DELETE_LOG="$5"
export PATH="$2:$PATH"
. "$1"
managed_state="$3"
desired_state="$4"
legacy_adoption=true
: > "$desired_state"
ensure_managed_a_record app.example.test 10.0.0.8 nextcloud
grep -Fq $'example.test\tapp.example.test\tA_LEGACY\t10.0.0.8' "$desired_state"
cp "$desired_state" "$managed_state"
: > "$desired_state"
delete_removed_managed_records
test ! -e "$FAKE_DELETE_LOG"
`
	output, err := exec.Command("bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, managed, desired, deleteLog).CombinedOutput()
	if err != nil {
		t.Fatalf("legacy observation ownership: %v\n%s", err, output)
	}
}

func TestAnasZonePublishesDirectoryNativeRecordsAfterBindStarts(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "dnsupdate.log")
	fake := `#!/bin/sh
printf '%s\n' "$*" > "$FAKE_DNSUPDATE_LOG"
`
	if err := os.WriteFile(filepath.Join(bin, "samba_dnsupdate"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=ad.example.test
export SAMBA_DC_HOST_IP=10.0.0.2
export FAKE_DNSUPDATE_LOG="$3"
export PATH="$2:$PATH"
. "$1"
publish_directory_native_records
test "$(cat "$FAKE_DNSUPDATE_LOG")" = "--all-names --use-samba-tool --current-ip=10.0.0.2 --rpc-server-ip=10.0.0.2"
`
	output, err := exec.Command(
		"bash", "-c", command, "anas-zone-test", anasZoneScriptPath(), bin, log,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("publish directory-native records: %v\n%s", err, output)
	}
}

func anasZoneScriptPath() string {
	return filepath.Join("..", "samba_dc", "root", "usr", "local", "bin", "anas_zone.sh")
}

func callRelativeName(script, fqdn, zone string) ([]byte, error) {
	const command = `set -eu
export ANAS_ZONE_LIB_ONLY=true
export SAMBA_DC_ADMINISTRATOR_NAME=test-administrator
export SAMBA_DC_ADMINISTRATOR_PASSWORD=test-password
export SAMBA_DC_DOMAIN=ad.example.test
. "$1"
relative_name "$2" "$3"
`
	return exec.Command("bash", "-c", command, "anas-zone-test", script, fqdn, zone).CombinedOutput()
}
