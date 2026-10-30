// TEST_CASES: MAILMOD-T-017
package dms

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/smtpconfig"
)

func makeTestQueue(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"active", "deferred", "hold", "incoming", "maildrop"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOutboundQueueCannotBeSilentlyMovedOrLostOnRestart(t *testing.T) {
	root := t.TempDir()
	configDir, postfixDir, stateDir := filepath.Join(root, "config"), filepath.Join(root, "postfix"), filepath.Join(root, "state")
	for _, path := range []string{configDir, postfixDir} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	makeTestQueue(t, filepath.Join(root, "persistent-spool"))
	queue := filepath.Join(root, "persistent-spool", "deferred", "A")
	if err := os.MkdirAll(queue, 0700); err != nil {
		t.Fatal(err)
	}
	c := smtpconfig.Config{Default: "relay", Profiles: []smtpconfig.Profile{{ID: "relay", Provider: "smtp", Host: "smtp.example.test", Port: 465, TLS: "tls", Username: "sender", Password: "synthetic-private-key"}}}
	apply := func() error {
		return prepareOutbound(c, configDir, postfixDir, stateDir, []string{filepath.Join(root, "persistent-spool")})
	}
	if err := apply(); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(postfixDir, "anas-sasl-password")
	info, err := os.Stat(secretFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("SASL map is not private")
	}
	public, _ := os.ReadFile(filepath.Join(stateDir, "public.json"))
	if bytes.Contains(public, []byte(c.Profiles[0].Password)) {
		t.Fatal("password persisted in public route metadata")
	}
	message := filepath.Join(queue, "CONTROLLED_QUEUE_ITEM")
	if err := os.WriteFile(message, []byte("original queued message"), 0600); err != nil {
		t.Fatal(err)
	}
	// Restart and credential refresh preserve the route and original queue item.
	c.Profiles[0].Password = "rotated-synthetic-private-key"
	if err := apply(); err != nil {
		t.Fatal("unchanged route or password rotation blocked", err)
	}
	before, _ := os.ReadFile(secretFile)
	old := c
	c = smtpconfig.Config{Default: "direct", Profiles: []smtpconfig.Profile{{ID: "direct", Provider: "direct"}}}
	if err := apply(); !errors.Is(err, ErrOutboundQueue) {
		t.Fatal("queued message silently moved to direct delivery", err)
	}
	c = old
	c.Profiles[0].Host = "replacement.example.test"
	if err := apply(); !errors.Is(err, ErrOutboundQueue) {
		t.Fatal("queued message silently moved to a different service", err)
	}
	after, _ := os.ReadFile(secretFile)
	if !bytes.Equal(before, after) {
		t.Fatal("blocked route change modified credentials")
	}
	if raw, err := os.ReadFile(message); err != nil || string(raw) != "original queued message" {
		t.Fatal("queue content lost")
	}
	if err := os.Remove(message); err != nil {
		t.Fatal(err)
	}
	if err := apply(); err != nil {
		t.Fatal("empty-queue route change blocked", err)
	}
	// Unknown or redirected queue state cannot be treated as an empty queue.
	if err := os.Symlink(root, filepath.Join(queue, "unexpected-link")); err != nil {
		t.Fatal(err)
	}
	c.Profiles[0].Host = "third.example.test"
	if err := apply(); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unknown queue state accepted", err)
	}
}

func TestUnconfiguredQueueCannotBeSilentlyReleasedToDirect(t *testing.T) {
	root := t.TempDir()
	configDir, postfixDir, stateDir, spool := filepath.Join(root, "config"), filepath.Join(root, "postfix"), filepath.Join(root, "state"), filepath.Join(root, "spool")
	for _, path := range []string{configDir, postfixDir} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	makeTestQueue(t, spool)
	apply := func(c smtpconfig.Config) error {
		return prepareOutbound(c, configDir, postfixDir, stateDir, []string{spool})
	}
	if err := apply(smtpconfig.Config{}); err != nil {
		t.Fatal(err)
	}
	queue := filepath.Join(spool, "deferred", "fixture")
	if err := os.WriteFile(queue, []byte("pending message"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(configDir, "postfix-main.cf"))
	if err != nil {
		t.Fatal(err)
	}
	direct := smtpconfig.Config{Default: "direct", Profiles: []smtpconfig.Profile{{ID: "direct", Provider: "direct"}}}
	if err := apply(direct); !errors.Is(err, ErrOutboundQueue) {
		t.Fatal("unconfigured queue was silently released to direct delivery", err)
	}
	after, _ := os.ReadFile(filepath.Join(configDir, "postfix-main.cf"))
	message, _ := os.ReadFile(queue)
	if !bytes.Equal(before, after) || string(message) != "pending message" {
		t.Fatal("blocked change modified route or queued message")
	}
}

func TestOutboundQueueUnknownDoesNotPermitRouteChange(t *testing.T) {
	for _, mode := range []string{"missing-root", "missing-queue", "broken-root-link", "queue-link"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			spool := filepath.Join(root, "spool")
			if mode != "missing-root" && mode != "broken-root-link" {
				makeTestQueue(t, spool)
			}
			switch mode {
			case "missing-queue":
				if err := os.Remove(filepath.Join(spool, "deferred")); err != nil {
					t.Fatal(err)
				}
			case "broken-root-link":
				if err := os.Symlink(filepath.Join(root, "missing"), spool); err != nil {
					t.Fatal(err)
				}
			case "queue-link":
				if err := os.Remove(filepath.Join(spool, "deferred")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(spool, "active"), filepath.Join(spool, "deferred")); err != nil {
					t.Fatal(err)
				}
			}
			pending, err := queuePending([]string{spool})
			if pending || !errors.Is(err, ErrUnavailable) {
				t.Fatal("incomplete queue was treated as empty", pending, err)
			}
		})
	}
}

func TestOutboundQueueRootAliasMustReferToDeclaredRoot(t *testing.T) {
	root := t.TempDir()
	persistent := filepath.Join(root, "persistent")
	alias := filepath.Join(root, "current")
	foreign := filepath.Join(root, "foreign")
	makeTestQueue(t, persistent)
	makeTestQueue(t, foreign)
	if err := os.Symlink(persistent, alias); err != nil {
		t.Fatal(err)
	}
	if pending, err := queuePending([]string{persistent, alias}); err != nil || pending {
		t.Fatal("legitimate spool alias rejected", pending, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, alias); err != nil {
		t.Fatal(err)
	}
	if pending, err := queuePending([]string{persistent, alias}); pending || !errors.Is(err, ErrUnavailable) {
		t.Fatal("unrelated spool alias accepted", pending, err)
	}
	otherAlias := filepath.Join(root, "second-link")
	if err := os.Symlink(foreign, otherAlias); err != nil {
		t.Fatal(err)
	}
	if pending, err := queuePending([]string{alias, otherAlias}); pending || !errors.Is(err, ErrUnavailable) {
		t.Fatal("two redirected spool roots accepted", pending, err)
	}
}

func TestOutboundFirstBootSpoolAndInterruptedInitialization(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	postfixDir := filepath.Join(root, "postfix")
	stateDir := filepath.Join(root, "state")
	persistent := filepath.Join(root, "persistent-spool")
	image := filepath.Join(root, "image-spool")
	for _, path := range []string{configDir, postfixDir, image} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	routes := []string{persistent, image}
	initial := smtpconfig.Config{Default: "direct", Profiles: []smtpconfig.Profile{{ID: "direct", Provider: "direct"}}}
	if err := prepareOutbound(initial, configDir, postfixDir, stateDir, routes); err != nil {
		t.Fatal("fresh image initialization rejected", err)
	}
	if err := prepareOutbound(initial, configDir, postfixDir, stateDir, routes); err != nil {
		t.Fatal("same route retry rejected", err)
	}
	newRoute := smtpconfig.Config{Default: "relay", Profiles: []smtpconfig.Profile{{ID: "relay", Provider: "smtp", Host: "smtp.example.test", Port: 587, TLS: "starttls", Username: "sender", Password: "synthetic"}}}
	before, err := os.ReadFile(filepath.Join(stateDir, "public.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = prepareOutbound(newRoute, configDir, postfixDir, stateDir, routes); !errors.Is(err, ErrUnavailable) {
		t.Fatal("incomplete initialized queue permitted route change", err)
	}
	after, err := os.ReadFile(filepath.Join(stateDir, "public.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed route change modified state", err)
	}
	makeTestQueue(t, image)
	if err = prepareOutbound(newRoute, configDir, postfixDir, stateDir, routes); err != nil {
		t.Fatal("completed empty queue blocked route change", err)
	}
}

func TestOutboundFirstBootRejectsAmbiguousState(t *testing.T) {
	for _, mode := range []string{"partial-image", "state-directory", "persistent-directory", "persistent-link", "missing-image"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(root, "state")
			persistent := filepath.Join(root, "persistent")
			image := filepath.Join(root, "image")
			if mode != "missing-image" {
				if err := os.Mkdir(image, 0700); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "partial-image":
				if err := os.Mkdir(filepath.Join(image, "deferred"), 0700); err != nil {
					t.Fatal(err)
				}
			case "state-directory":
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
			case "persistent-directory":
				if err := os.Mkdir(persistent, 0700); err != nil {
					t.Fatal(err)
				}
			case "persistent-link":
				if err := os.Symlink(filepath.Join(root, "missing"), persistent); err != nil {
					t.Fatal(err)
				}
			}
			if virginQueue(state, []string{persistent, image}) {
				t.Fatal("ambiguous queue claimed fresh")
			}
		})
	}
}
