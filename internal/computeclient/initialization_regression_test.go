//go:build linux || darwin

package computeclient

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This models CLI configuration mutation, not real daemon acceptance.
func initializationCLI(t *testing.T) {
	t.Helper()
	fixtureIncusExecutable(t, `
if [ "$1 $2" = "remote add" ]; then
  if [ -e "$INCUS_CONF/config.yml" ]; then exit 7; fi
  printf 'legacy-cli-config\n' > "$INCUS_CONF/config.yml"
  exit 0
fi
if [ "$1" = list ]; then printf '[]\n'; exit 0; fi
exit 9
`)
}

func TestClientInitializationReusesCompleteConfigurationWithoutCLIWrite(t *testing.T) {
	initializationCLI(t)
	l, dir := credentialBoundaryLease(t), credentialDirectory(t)
	if _, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir); err != nil {
		t.Fatal("restarted client cannot reuse its remote", err)
	}
	after, err := os.Stat(filepath.Join(dir, "config.yml"))
	if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("reinitialization rewrote configuration", err)
	}
}

func TestClientConfigurationUsesIncusProtocolAndExactLease(t *testing.T) {
	initializationCLI(t)
	l, dir := credentialBoundaryLease(t), credentialDirectory(t)
	if _, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		DefaultRemote string `json:"default-remote"`
		Remotes       map[string]struct {
			Address  string `json:"addr"`
			AuthType string `json:"auth_type"`
			Project  string `json:"project"`
			Protocol string `json:"protocol"`
			Public   bool   `json:"public"`
		} `json:"remotes"`
	}
	if err := json.Unmarshal(body, &config); err != nil {
		t.Fatal("invalid generated CLI configuration")
	}
	remote, ok := config.Remotes[remoteName]
	if !ok || len(config.Remotes) != 1 || config.DefaultRemote != remoteName ||
		remote.Address != l.Endpoint || remote.Project != l.Sandbox || remote.AuthType != "tls" ||
		remote.Protocol != "incus" || remote.Public {
		t.Fatal("generated CLI remote does not use the Incus protocol and exact private lease")
	}
}

func TestClientInitializationConcurrentSameLease(t *testing.T) {
	initializationCLI(t)
	l, dir := credentialBoundaryLease(t), credentialDirectory(t)
	var wg sync.WaitGroup
	errors := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		wg.Go(func() {
			<-start
			_, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir)
			errors <- err
		})
	}
	close(start)
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error("matching initializers must converge", err)
		}
	}
}

func TestClientInitializationRejectsUnvalidatedLeaseBeforeFilesystemEffects(t *testing.T) {
	initializationCLI(t)
	for name, change := range map[string]func(*Lease){
		"unknown-tier":       func(l *Lease) { l.Interface = "unknown" },
		"credentials-in-url": func(l *Lease) { l.Endpoint = "https://private:secret@incus.example:8443" },
		"query-in-url":       func(l *Lease) { l.Endpoint += "?project=default" },
		"non-https-url":      func(l *Lease) { l.Endpoint = "unix:///var/lib/incus/unix.socket" },
		"wrong-profile":      func(l *Lease) { l.Profile = "default" },
		"empty-prefix":       func(l *Lease) { l.InstancePrefix = "" },
		"other-project":      func(l *Lease) { l.Sandbox = "../../default" },
		"alias-image":        func(l *Lease) { l.ImageAllowlist = []string{"images:latest"} },
		"oversized-cpu":      func(l *Lease) { l.CPU = 65 },
		"undersized-disk":    func(l *Lease) { l.DiskGiB = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			l := credentialBoundaryLease(t)
			change(&l)
			dir := filepath.Join(credentialDirectory(t), "not-created")
			if _, err := NewWithContext(context.Background(), l, []string{"/usr/local/bin/entry"}, dir); err == nil {
				t.Error("invalid directly constructed lease accepted")
			}
			if _, err := os.Lstat(dir); !os.IsNotExist(err) {
				t.Error("invalid lease created state", err)
			}
		})
	}
}

func TestClientInitializationRejectsInvalidEntrypointsBeforeEffects(t *testing.T) {
	initializationCLI(t)
	for _, entries := range [][]string{{""}, {"relative"}, {"/bin/../bin/sh"}, {"/bin/sh\n"}, {"/bin/sh", "/bin/sh"}} {
		dir := filepath.Join(credentialDirectory(t), "not-created")
		if _, err := NewWithContext(context.Background(), credentialBoundaryLease(t), entries, dir); err == nil {
			t.Error("invalid entrypoint allowlist accepted")
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Error("invalid allowlist created state", err)
		}
	}
}

func TestClientListDoesNotTurnInvalidInventoryIntoAbsence(t *testing.T) {
	c, run := testClient(t, testLease())
	for _, body := range []string{"null", `[{"name":"anas-fj-a","status":"Running","config":{"user.anas.managed":"true"}},{"name":"anas-fj-a","status":"Stopped","config":{"user.anas.managed":"true"}}]`} {
		run.reply["list"] = []byte(body)
		if _, err := c.ListManaged(context.Background()); err == nil {
			t.Error("invalid inventory accepted", body)
		}
	}
	for _, id := range []string{"anas-fj-../../outside", "anas-fj-" + strings.Repeat("x", 33), "anas-fj-UPPER"} {
		if c.lease.OwnsInstance(id) {
			t.Error("janitor accepts identity Create rejects", id)
		}
	}
}

func TestClientDeleteRequiresObservedAbsenceAfterSuccessfulCLI(t *testing.T) {
	c, run := testClient(t, testLease())
	run.reply["list"] = []byte(`[{"name":"anas-fj-a","status":"Running","config":{"user.anas.managed":"true"}}]`)
	if err := c.Delete(context.Background(), "anas-fj-a"); err == nil {
		t.Fatal("CLI success alone was accepted as deletion")
	}
}

func TestClientInitializationCannotReuseAnotherProjectConfiguration(t *testing.T) {
	initializationCLI(t)
	l, dir := credentialBoundaryLease(t), credentialDirectory(t)
	if _, err := NewWithContext(context.Background(), l, []string{"/usr/bin/entry"}, dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	l.Sandbox = "another-project"
	if _, err := NewWithContext(context.Background(), l, []string{"/usr/bin/entry"}, dir); err == nil {
		t.Fatal("same keypair silently rebound to another project")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil || string(before) != string(after) {
		t.Fatal("conflicting initializer overwrote configuration")
	}
}
