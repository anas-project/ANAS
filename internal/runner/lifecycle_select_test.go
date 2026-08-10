package runner

import (
	"strings"
	"testing"
)

func selectionApp() *app {
	return &app{order: []string{"lego", "traefik", "postgres", "nextcloud"}}
}

// The command line is not a dependency order. Whatever order the names are
// typed in, postgres has to start before nextcloud.
func TestSelectCasksKeepsDeploymentOrder(t *testing.T) {
	a := selectionApp()
	got, err := selectCasks(a, []string{"nextcloud", "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "postgres" || got[1] != "nextcloud" {
		t.Fatalf("selection = %v, want [postgres nextcloud]", got)
	}
}

// No names means the whole deployment, which is what these commands did before
// they could be narrowed.
func TestSelectCasksDefaultsToEverything(t *testing.T) {
	a := selectionApp()
	got, err := selectCasks(a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(a.order) {
		t.Fatalf("selection = %v, want the whole deployment", got)
	}
	// A copy, so a caller reordering the selection cannot reorder the
	// deployment it came from.
	got[0] = "mutated"
	if a.order[0] == "mutated" {
		t.Fatal("the selection aliases a.order")
	}
}

// A name that is not in this deployment is a usage error naming what is, rather
// than a silent no-op that reports success having done nothing.
func TestSelectCasksRejectsUnknownNames(t *testing.T) {
	a := selectionApp()
	_, err := selectCasks(a, []string{"traefik", "nosuchcask"})
	if err == nil {
		t.Fatal("an unknown cask was accepted")
	}
	if !strings.Contains(err.Error(), "nosuchcask") {
		t.Errorf("the error does not name the bad cask: %v", err)
	}
	// It also says what the deployment does carry, so the reader does not have
	// to go and look it up.
	if !strings.Contains(err.Error(), "traefik") {
		t.Errorf("the error does not list the available casks: %v", err)
	}
}
