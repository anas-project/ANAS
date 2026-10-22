//go:build unix

package serviceauth

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func testConfig(t *testing.T) Config {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Config{Directory: filepath.Join(parent, "credentials"), Clients: []Client{{
		ID: "portal-main", Enabled: true, UID: os.Geteuid(), GID: os.Getegid(),
		Grants: []Grant{{Permission: "application.credentials.read", Workspace: "main", Resources: []string{"traefik-admin"}}},
	}}}
}

func testManager(t *testing.T, config *Config) *Manager {
	t.Helper()
	manager, err := New(Options{LoadConfig: func() (Config, error) { return *config, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func readDelivered(t *testing.T, config Config) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(config.Directory, config.Clients[0].ID, "token"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(body), "\n")
}

func reconcile(t *testing.T, manager *Manager) {
	t.Helper()
	if err := manager.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func authenticate(manager *Manager, token string) (string, error) {
	return manager.Authenticate(context.Background(), token, "application.credentials.read", "main", "traefik-admin")
}

func TestLifecycleRenewsButRestartAndExpiryReissue(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	now := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }
	reconcile(t, manager)
	first := readDelivered(t, config)
	if id, err := authenticate(manager, first); id != "portal-main" || err != nil {
		t.Fatalf("initial authentication = %q, %v", id, err)
	}
	// The portal may remain stopped indefinitely; daemon renewal retains its
	// credential independently of incoming requests.
	for range 8 {
		now = now.Add(10 * time.Minute)
		reconcile(t, manager)
	}
	if readDelivered(t, config) != first {
		t.Fatal("ordinary maintenance unexpectedly rotated credential")
	}
	if _, err := authenticate(manager, first); err != nil {
		t.Fatalf("renewed credential: %v", err)
	}
	// A stopped daemon cannot renew credentials. Even if the old delivery file
	// remains, an expired value is refused before and after recovery.
	now = now.Add(24 * time.Hour)
	if _, err := authenticate(manager, first); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired credential = %v", err)
	}
	reconcile(t, manager)
	second := readDelivered(t, config)
	if second == first {
		t.Fatal("expired credential was resurrected")
	}
	if _, err := authenticate(manager, first); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("old value after recovery = %v", err)
	}
	restarted := testManager(t, &config)
	restarted.now = manager.now
	if _, err := authenticate(restarted, second); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disk token adopted before reconciliation = %v", err)
	}
	reconcile(t, restarted)
	third := readDelivered(t, config)
	if third == second {
		t.Fatal("restart adopted an old credential")
	}
	if _, err := authenticate(restarted, third); err != nil {
		t.Fatalf("restart replacement = %v", err)
	}
}

func TestExpiryReissuesWithoutPriorAuthentication(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	now := time.Now()
	manager.now = func() time.Time { return now }
	reconcile(t, manager)
	old := readDelivered(t, config)
	now = now.Add(15 * time.Minute)
	reconcile(t, manager)
	if readDelivered(t, config) == old {
		t.Fatal("maintenance renewed an already expired credential")
	}
}

func TestRevocationAndNarrowingApplyWithoutMaintenance(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	reconcile(t, manager)
	token := readDelivered(t, config)
	config.Clients[0].Grants[0].Resources = []string{"ddns-admin"}
	if _, err := authenticate(manager, token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("narrowed scope = %v", err)
	}
	config.Clients[0].Enabled = false
	if _, err := authenticate(manager, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("disabled client = %v", err)
	}
	reconcile(t, manager)
	path := filepath.Join(config.Directory, "portal-main", "token")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked delivery retained: %v", err)
	}
	config.Clients[0].Enabled = true
	config.Clients[0].Grants[0].Resources = []string{"traefik-admin"}
	if _, err := authenticate(manager, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("reenabling resurrected old token: %v", err)
	}
	reconcile(t, manager)
	if readDelivered(t, config) == token {
		t.Fatal("reenabling reused revoked token")
	}
	config.Clients = nil
	// A fresh manager must also remove delivery for clients deleted while down.
	reconcile(t, testManager(t, &config))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted client's delivery retained after restart: %v", err)
	}
}

func TestGrantDimensionsCannotBeCombined(t *testing.T) {
	config := testConfig(t)
	config.Clients[0].Grants = append(config.Clients[0].Grants, Grant{
		Permission: "health.read", Workspace: "other", Resources: []string{"ddns-admin"},
	})
	manager := testManager(t, &config)
	reconcile(t, manager)
	token := readDelivered(t, config)
	for _, request := range []struct{ permission, workspace, resource string }{
		{"health.read", "main", "traefik-admin"},
		{"application.credentials.read", "other", "ddns-admin"},
		{"application.credentials.read", "main", "ddns-admin"},
		{"unknown", "main", "traefik-admin"},
		{"application.credentials.read", "main", "*"},
		{"application.credentials.read", "", "traefik-admin"},
	} {
		if _, err := manager.Authenticate(context.Background(), token, request.permission, request.workspace, request.resource); !errors.Is(err, ErrForbidden) {
			t.Errorf("grant combination %#v = %v", request, err)
		}
	}
	for _, bad := range []string{"", "portal-main.bad", "Bearer " + token, token + "x", strings.Repeat("x", 10000)} {
		if _, err := authenticate(manager, bad); !errors.Is(err, ErrUnauthenticated) {
			t.Errorf("bad token accepted: %v", err)
		}
	}
}

func TestDeliveryLossAndTamperingCannotAdoptValues(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	reconcile(t, manager)
	old := readDelivered(t, config)
	path := filepath.Join(config.Directory, "portal-main", "token")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	reconcile(t, manager)
	if readDelivered(t, config) == old {
		t.Fatal("lost delivery restored an old credential")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(old+"\n"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	reconcile(t, manager)
	if readDelivered(t, config) == old {
		t.Fatal("tampered delivery was adopted")
	}
}

func TestDeliveryPermissionsAndFailureInvalidate(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	reconcile(t, manager)
	token := readDelivered(t, config)
	for path, mode := range map[string]os.FileMode{
		config.Directory: 0o700, filepath.Join(config.Directory, "portal-main"): 0o710,
		filepath.Join(config.Directory, "portal-main", "token"): 0o400,
	} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("incorrect delivery permissions for %s: %v", filepath.Base(path), err)
		}
	}
	path := filepath.Join(config.Directory, "portal-main", "token")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reconcile(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unsafe delivery = %v", err)
	}
	if _, err := authenticate(manager, token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("failed delivery left token valid: %v", err)
	}
}

func TestStrictDaemonUmask(t *testing.T) {
	// Umask belongs to the entire process. Exercise the real systemd setting in
	// a subprocess so concurrent packages and tests retain their own policy.
	const helper = "ANAS_SERVICEAUTH_UMASK_TEST"
	if os.Getenv(helper) != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestStrictDaemonUmask$")
		command.Env = append(os.Environ(), helper+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("strict umask credential delivery: %v\n%s", err, output)
		}
		return
	}
	syscall.Umask(0o077)
	config := testConfig(t)
	manager := testManager(t, &config)
	reconcile(t, manager)
	info, err := os.Stat(filepath.Join(config.Directory, "portal-main"))
	if err != nil || info.Mode().Perm() != 0o710 {
		t.Fatalf("client traversal permissions under umask 0077: %v", err)
	}
	if _, err := authenticate(manager, readDelivered(t, config)); err != nil {
		t.Fatal(err)
	}
}

func TestUnsafeDirectoriesAreRejected(t *testing.T) {
	for _, kind := range []string{"root-symlink", "ancestor-symlink", "client-symlink", "token-symlink", "reused-root", "reused-client", "writable-root"} {
		t.Run(kind, func(t *testing.T) {
			config := testConfig(t)
			manager := testManager(t, &config)
			outside := filepath.Join(filepath.Dir(config.Directory), "outside")
			if err := os.Mkdir(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "root-symlink":
				must(os.Symlink(outside, config.Directory))
			case "ancestor-symlink":
				must(os.Symlink(outside, config.Directory))
				config.Directory = filepath.Join(config.Directory, "child")
			case "reused-root":
				must(os.Mkdir(config.Directory, 0o700))
				must(os.WriteFile(filepath.Join(config.Directory, "unrelated"), []byte("preserve"), 0o600))
			case "writable-root":
				must(os.Mkdir(config.Directory, 0o755))
			default:
				reconcile(t, manager)
				dir := filepath.Join(config.Directory, "portal-main")
				must(os.Remove(filepath.Join(dir, "token")))
				if kind == "token-symlink" {
					must(os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "token")))
				} else {
					must(os.Remove(filepath.Join(dir, markerName)))
					if kind == "client-symlink" {
						must(os.Remove(dir))
						must(os.Symlink(outside, dir))
					} else {
						must(os.WriteFile(filepath.Join(dir, "unrelated"), []byte("preserve"), 0o600))
					}
				}
			}
			if err := manager.Reconcile(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("unsafe path accepted: %v", err)
			}
		})
	}
}

func TestConfigFailuresAndUnsupportedScopeFailClosed(t *testing.T) {
	config := testConfig(t)
	manager := testManager(t, &config)
	reconcile(t, manager)
	token := readDelivered(t, config)
	manager.options.LoadConfig = func() (Config, error) { return Config{}, errors.New("unreadable") }
	if _, err := authenticate(manager, token); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("configuration failure = %v", err)
	}
	for _, edit := range []func(*Config){
		func(c *Config) { c.Directory = "relative" },
		func(c *Config) { c.Directory = "/" },
		func(c *Config) { c.Clients[0].ID = "../escape" },
		func(c *Config) { c.Clients[0].UID = -1 },
		func(c *Config) { c.Clients[0].Grants[0].Permission = "*" },
		func(c *Config) { c.Clients[0].Grants[0].Workspace = "*" },
		func(c *Config) { c.Clients[0].Grants[0].Resources = nil },
		func(c *Config) { c.Clients[0].Grants[0].Resources = []string{"*"} },
	} {
		invalid := testConfig(t)
		edit(&invalid)
		if err := ValidateConfig(invalid); err == nil {
			t.Error("invalid configuration accepted")
		}
	}
}

func TestRunRetriesAndStops(t *testing.T) {
	config := testConfig(t)
	var attempts atomic.Int32
	reported := make(chan error, 1)
	manager, err := New(Options{
		Lifetime: time.Second, Interval: 10 * time.Millisecond,
		LoadConfig: func() (Config, error) {
			if attempts.Add(1) == 1 {
				return Config{}, errors.New("temporarily unavailable")
			}
			return config, nil
		},
		OnError: func(err error) {
			select {
			case reported <- err:
			default:
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	select {
	case err := <-reported:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("maintenance failure was not reported")
	}
	deadline := time.After(time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(config.Directory, "portal-main", "token")); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline:
			t.Fatal("maintenance did not recover")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop")
	}
}
