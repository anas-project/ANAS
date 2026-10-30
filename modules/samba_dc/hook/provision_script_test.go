package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvisionUsesConfiguredDCIdentityAndAddress(t *testing.T) {
	script, err := os.ReadFile(filepath.Join("..", "samba_dc", "root", "etc", "cont-init.d", "11-samba_dc.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	start, end := strings.Index(text, "  INTERFACE_OPTS="), strings.Index(text, "  rm -f /etc/samba/smb.conf")
	if start < 0 || end <= start {
		t.Fatal("provision options unavailable")
	}
	cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+text[start:end]+`
printf '%s\n' "$PROVISION_OPTS"
`)
	cmd.Env = append(os.Environ(), "SAMBA_DC_DOMAIN_ACTION=provision", "SAMBA_DC_WORKGROUP=FJIAM", "SAMBA_DC_REALM=FJ-IAM.TEST", "SAMBA_DC_ADMINISTRATOR_PASSWORD=fixture", "SAMBA_DC_BIND_INTERFACES_ONLY=yes", "SAMBA_DC_INTERFACES=lo eth0", "SAMBA_DC_DC_NAME=fjiam", "SAMBA_DC_HOST_IP=10.253.31.2")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provision options: %s %v", out, err)
	}
	for _, want := range []string{"--host-name=fjiam", "--host-ip=10.253.31.2"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("configured identity missing %s: %s", want, out)
		}
	}
}
