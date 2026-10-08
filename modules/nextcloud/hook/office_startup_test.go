package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TEST_CASES: TEMP-T-021
func officeStartupFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	programs := map[string]string{
		"curl": `#!/bin/bash
set -eu
output=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --insecure|-k) exit 99 ;;
    --output) output=$2; shift ;;
  esac
  shift
done
test -n "$output"
counter="$OFFICE_TEST_ROOT/curl-count"
n=$(cat "$counter" 2>/dev/null || echo 0)
n=$((n + 1)); echo "$n" > "$counter"
if [ "$OFFICE_TEST_MODE" = unavailable ] || [ "$n" -eq 1 ]; then
  printf '<html>startup</html>' > "$output"
  exit 22
fi
if [ "$n" -eq 2 ]; then
  printf '<html>not discovery</html>' > "$output"
else
  printf '<wopi-discovery><net-zone/></wopi-discovery>' > "$output"
fi
`,
		"runuser": `#!/bin/bash
set -eu
test "$*" = '-u www-data -- php /var/www/html/occ richdocuments:activate-config'
counter="$OFFICE_TEST_ROOT/activation-count"
n=$(cat "$counter" 2>/dev/null || echo 0)
n=$((n + 1)); echo "$n" > "$counter"
test "$n" -gt 1
`,
		"timeout": "#!/bin/bash\nset -eu\ntest \"$1\" = 30s\nshift\nexec \"$@\"\n",
		"sleep":   "#!/bin/bash\nexec /bin/sleep 0.02\n",
	}
	for name, contents := range programs {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OFFICE_TEST_ROOT", root)
	t.Setenv("OFFICE_TEST_MODE", "recover")
	t.Setenv("COLLABORA_DOMAIN_FULL", "https://collabora.example.test:19071")
	script, err := filepath.Abs("../nextcloud/root/usr/local/bin/anas-office-activate.sh")
	if err != nil {
		t.Fatal(err)
	}
	return script, root
}

func TestOfficeActivationWaitsForValidDiscoveryAndOfficialActivation(t *testing.T) {
	script, root := officeStartupFixture(t)
	ready := filepath.Join(root, "ready")
	out, err := exec.Command("bash", script, ready, "10").CombinedOutput()
	if err != nil {
		t.Fatalf("activation failed: %v: %s", err, out)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatal("successful official activation did not publish readiness", err)
	}
	for file, expected := range map[string]string{"curl-count": "4\n", "activation-count": "2\n"} {
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil || string(b) != expected {
			t.Fatalf("%s = %q, %v; expected %q", file, b, err, expected)
		}
	}
	if !strings.Contains(string(out), "Nextcloud Office configuration activated") {
		t.Fatal("successful activation was not reported")
	}
}

func TestOfficeActivationTimeoutClearsStaleReadiness(t *testing.T) {
	script, root := officeStartupFixture(t)
	t.Setenv("OFFICE_TEST_MODE", "unavailable")
	ready := filepath.Join(root, "ready")
	if err := os.WriteFile(ready, nil, 0600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("bash", script, ready, "1").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "initialization timed out") {
		t.Fatalf("missing explicit timeout failure: %v: %s", err, out)
	}
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatal("failed activation retained stale readiness", err)
	}
	if _, err := os.Stat(filepath.Join(root, "activation-count")); !os.IsNotExist(err) {
		t.Fatal("invalid discovery was passed to the official activation command", err)
	}
}

func TestOfficeActivationRejectsInvalidArguments(t *testing.T) {
	script, root := officeStartupFixture(t)
	for _, args := range [][]string{{"relative", "10"}, {filepath.Join(root, "ready"), "0"}, {filepath.Join(root, "ready"), "bad"}} {
		out, err := exec.Command("bash", append([]string{script}, args...)...).CombinedOutput()
		if err == nil || !strings.Contains(string(out), "Invalid Office startup arguments") {
			t.Fatalf("invalid arguments accepted: %q: %v: %s", args, err, out)
		}
	}
}
