package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func nextcloudCronEntrypointPath() string {
	return filepath.Join("..", "nextcloud", "root", "usr", "local", "bin", "anas-cron-entrypoint.sh")
}

func TestNextcloudCronInstallsInternalCABeforeStarting(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "updated")
	update := "#!/bin/sh\n: >\"$UPDATE_CA_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(bin, "update-ca-certificates"), []byte(update), 0o700); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(root, "internal-ca.crt")
	destination := filepath.Join(root, "installed-ca.crt")
	cronStarted := filepath.Join(root, "cron-started")
	if err := os.WriteFile(ca, []byte("internal-ca\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cron := filepath.Join(root, "cron.sh")
	cronScript := `#!/bin/sh
set -eu
test -s "$EXPECTED_CA_DESTINATION"
test -f "$UPDATE_CA_MARKER"
: >"$CRON_STARTED_MARKER"
`
	if err := os.WriteFile(cron, []byte(cronScript), 0o700); err != nil {
		t.Fatal(err)
	}

	const command = `set -eu
export ANAS_NEXTCLOUD_CRON_LIB_ONLY=true
export UPDATE_CA_MARKER="$5"
export EXPECTED_CA_DESTINATION="$4"
export CRON_STARTED_MARKER="$7"
export PATH="$2:$PATH"
. "$1"
start_nextcloud_cron "$3" "$4" "$6"
`
	output, err := exec.Command("sh", "-c", command, "nextcloud-cron-tls-test", nextcloudCronEntrypointPath(), bin, ca, destination, marker, cron, cronStarted).CombinedOutput()
	if err != nil {
		t.Fatalf("start_nextcloud_cron: %v\n%s", err, output)
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "internal-ca\n" {
		t.Fatalf("installed CA = %q", got)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("update-ca-certificates was not called: %v", err)
	}
	if _, err := os.Stat(cronStarted); err != nil {
		t.Fatalf("cron was not started after CA installation: %v", err)
	}
}

func TestNextcloudCronStartsWithSystemTrustWhenInternalCAIsAbsent(t *testing.T) {
	root := t.TempDir()
	cronStarted := filepath.Join(root, "cron-started")
	updateCalled := filepath.Join(root, "update-called")
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	update := "#!/bin/sh\n: >\"$UPDATE_CA_MARKER\"\n"
	if err := os.WriteFile(filepath.Join(bin, "update-ca-certificates"), []byte(update), 0o700); err != nil {
		t.Fatal(err)
	}
	cron := filepath.Join(root, "cron.sh")
	cronScript := "#!/bin/sh\n: >\"$CRON_STARTED_MARKER\"\n"
	if err := os.WriteFile(cron, []byte(cronScript), 0o700); err != nil {
		t.Fatal(err)
	}

	const command = `set -eu
export ANAS_NEXTCLOUD_CRON_LIB_ONLY=true
export CRON_STARTED_MARKER="$5"
export UPDATE_CA_MARKER="$6"
export PATH="$7:$PATH"
. "$1"
start_nextcloud_cron "$2" "$3" "$4"
`
	missingCA := filepath.Join(root, "missing-ca.crt")
	destination := filepath.Join(root, "installed-ca.crt")
	if output, err := exec.Command("sh", "-c", command, "nextcloud-cron-tls-test", nextcloudCronEntrypointPath(), missingCA, destination, cron, cronStarted, updateCalled, bin).CombinedOutput(); err != nil {
		t.Fatalf("cron did not start with system trust: %v\n%s", err, output)
	}
	if _, err := os.Stat(cronStarted); err != nil {
		t.Fatalf("cron did not start when internal CA was absent: %v", err)
	}
	if _, err := os.Stat(updateCalled); !os.IsNotExist(err) {
		t.Fatalf("missing internal CA unexpectedly updated the trust store: %v", err)
	}
}

func TestNextcloudCronMountsCertificateContract(t *testing.T) {
	compose, err := os.ReadFile(filepath.Join("..", "docker-compose.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	start := strings.Index(text, "  anas_nextcloud-cron:")
	end := strings.Index(text[start+1:], "\n  anas_nextcloud-push:")
	if start < 0 || end < 0 {
		t.Fatal("cannot locate Nextcloud cron service in compose")
	}
	cron := text[start : start+1+end]
	if !strings.Contains(cron, `entrypoint: ["/usr/local/bin/anas-cron-entrypoint.sh"]`) {
		t.Fatal("Nextcloud cron bypasses the CA-installing entrypoint")
	}
	if !strings.Contains(cron, `"${ANAS_TLS_CERTS_DIR}:/certs:ro"`) {
		t.Fatal("Nextcloud cron does not mount the certificate contract")
	}
}
