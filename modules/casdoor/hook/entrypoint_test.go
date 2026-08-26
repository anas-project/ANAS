package main

import (
	"os"
	"strings"
	"testing"
)

func TestEntrypointBootstrapsBeforeStartingLDAPSynchronizer(t *testing.T) {
	b, err := os.ReadFile("../casdoor/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(b)
	bootstrap := `/opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@" &`
	longRunning := `exec /opt/anas/bin/casdoor-helper exec-as 1000 1000 "$@"`
	bootstrapAt, longRunningAt := strings.Index(script, bootstrap), strings.Index(script, longRunning)
	if bootstrapAt < 0 || longRunningAt < 0 || bootstrapAt >= longRunningAt {
		t.Fatalf("entrypoint must import init data in a bootstrap process before the long-running process:\n%s", script)
	}
	if !strings.Contains(script, "casdoor-helper healthcheck") || !strings.Contains(script, `kill -TERM "$bootstrap_pid"`) {
		t.Fatal("entrypoint does not verify and stop the bootstrap process")
	}
	if !strings.Contains(script, "mkdir -p /data/anas-dirwatch") || !strings.Contains(script, "chown 1000:1000 /data/anas-dirwatch") {
		t.Fatal("entrypoint does not prepare the unprivileged directory watcher state")
	}
}

func TestHelperBuildUsesConfiguredGoProxy(t *testing.T) {
	dockerfile, err := os.ReadFile("../casdoor/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"ARG GOPROXY_URL", `go env -w GOPROXY="$GOPROXY_URL"`, "go mod download"} {
		if !strings.Contains(string(dockerfile), required) {
			t.Fatalf("Casdoor helper Dockerfile does not contain %q", required)
		}
	}

	compose, err := os.ReadFile("../docker-compose.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), "GOPROXY_URL: ${GOPROXY_URL:-}") {
		t.Fatal("Casdoor Compose build does not forward GOPROXY_URL")
	}
}

func TestImageDisablesCasdoorOldInstanceLookup(t *testing.T) {
	dockerfile, err := os.ReadFile("../casdoor/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "rm -f /usr/bin/lsof") {
		t.Fatal("Casdoor image can kill its bootstrap child through the old-instance lookup")
	}
}
