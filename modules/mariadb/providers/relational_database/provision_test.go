package relationaldatabase

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeMariaDB(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "mariadb.log")
	clientPath := filepath.Join(dir, "mariadb")
	client := `#!/bin/sh
set -eu
printf 'ARGS:%s\n' "$*" >>"$FAKE_MARIADB_LOG"
case "$*" in
  *information_schema.schemata*) printf '%s\n' "${FAKE_MARIADB_INSPECT_RESULT:-1}" ;;
esac
if [ ! -t 0 ]; then
  sed 's/^/SQL:/' >>"$FAKE_MARIADB_LOG"
fi
`
	if err := os.WriteFile(clientPath, []byte(client), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir, logPath
}

func runProvider(t *testing.T, operation, inspectResult string) (string, string) {
	t.Helper()
	binDir, logPath := fakeMariaDB(t)
	cmd := exec.Command("/bin/sh", "provision.sh", operation)
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"FAKE_MARIADB_LOG="+logPath,
		"FAKE_MARIADB_INSPECT_RESULT="+inspectResult,
		"MARIADB_HOST=mariadb",
		"MARIADB_PORT=3306",
		"MARIADB_USERNAME=root",
		"MARIADB_PASSWORD=root-secret",
		"ANAS_RESOURCE_DATABASE=lemonldap_ng",
		"ANAS_RESOURCE_USERNAME=lemonldap_ng",
		"ANAS_RESOURCE_PASSWORD=resourceSecret123",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("provider %s failed: %v\n%s", operation, err, out)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out)), string(log)
}

func TestEnsureCreatesDedicatedDatabaseUserAndGrant(t *testing.T) {
	_, log := runProvider(t, "ensure", "")
	for _, want := range []string{
		"CREATE DATABASE IF NOT EXISTS `lemonldap_ng`",
		"CREATE USER IF NOT EXISTS 'lemonldap_ng'@'%'",
		"ALTER USER 'lemonldap_ng'@'%' IDENTIFIED BY 'resourceSecret123'",
		"GRANT ALL PRIVILEGES ON `lemonldap_ng`.* TO 'lemonldap_ng'@'%'",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("provider SQL does not contain %q\n%s", want, log)
		}
	}
}

func TestInspectRequiresBothDatabaseAndUser(t *testing.T) {
	out, log := runProvider(t, "inspect", "1")
	if out != "ready" {
		t.Fatalf("inspect output = %q, want ready", out)
	}
	for _, want := range []string{"information_schema.schemata", "mysql.user", "User='lemonldap_ng'", "Host='%'"} {
		if !strings.Contains(log, want) {
			t.Errorf("inspect query does not contain %q\n%s", want, log)
		}
	}

	out, _ = runProvider(t, "inspect", "0")
	if out != "missing" {
		t.Fatalf("inspect output = %q, want missing", out)
	}
}
