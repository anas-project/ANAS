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
