package main

import (
	"strings"
	"testing"
)

func TestCalcDirectoryWatchSubscribesToThePublishedJournal(t *testing.T) {
	env := map[string]string{
		"ANAS_DIRECTORY_EVENTS_DIR":          "/srv/anas/data/samba_dc/events",
		"ANAS_DIRECTORY_EVENTS_FILE_NAME":    "events.jsonl",
		"SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE": "anasIdentityAnchor",
	}
	calcDirectoryWatch(env)
	if got, want := env["AUTHENTIK_DIRWATCH_EVENTS_DIR"], "/srv/anas/data/samba_dc/events"; got != want {
		t.Fatalf("AUTHENTIK_DIRWATCH_EVENTS_DIR = %q, want %q", got, want)
	}
	if got, want := env["AUTHENTIK_DIRWATCH_EVENT_FILE"],
		"/var/lib/anas-directory-events/events.jsonl"; got != want {
		t.Fatalf("AUTHENTIK_DIRWATCH_EVENT_FILE = %q, want %q", got, want)
	}
	// Only what changes an authorization decision earns a full source sync.
	attributes := env["AUTHENTIK_DIRWATCH_ATTRIBUTES"]
	for _, want := range []string{"member", "userAccountControl", "anasIdentityAnchor"} {
		if !strings.Contains(attributes, want) {
			t.Fatalf("subscriber attributes %q missing %q", attributes, want)
		}
	}
	if strings.Contains(attributes, "displayName") {
		t.Fatalf("cosmetic attributes must not trigger a sync: %q", attributes)
	}
	if env["AUTHENTIK_DIRWATCH_DEBOUNCE_SECONDS"] == "" ||
		env["AUTHENTIK_DIRWATCH_MIN_INTERVAL_SECONDS"] == "" {
		t.Fatal("debounce window and minimum interval must both be set")
	}
}

func TestCalcDirectoryWatchStaysRenderableWithoutAProvider(t *testing.T) {
	// compose has no conditional services, so the mount source still needs a
	// value when nothing published a journal.
	env := map[string]string{"DATA_PATH": "/srv/anas/data"}
	calcDirectoryWatch(env)
	if got, want := env["AUTHENTIK_DIRWATCH_EVENTS_DIR"],
		"/srv/anas/data/authentik/directory-events"; got != want {
		t.Fatalf("fallback events dir = %q, want %q", got, want)
	}
}
