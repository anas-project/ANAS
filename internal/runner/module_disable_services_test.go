package runner

import (
	"strings"
	"testing"
)

var composeFixture = []string{"anas_postgres", "anas_postgres_adminer", "anas_postgres_provision"}

func TestResolveDisableServicesAcceptsEitherSpelling(t *testing.T) {
	// The manifest and the hook name services without the anas_ prefix; Compose
	// defines them with it. Both have to reach the same service, or a hook that
	// looks correct silently disables nothing.
	for _, name := range []string{"postgres_adminer", "anas_postgres_adminer"} {
		resolved, err := resolveDisableServices("postgres", composeFixture, []string{name})
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if len(resolved) != 1 || resolved[0] != "anas_postgres_adminer" {
			t.Fatalf("%q resolved to %v, want [anas_postgres_adminer]", name, resolved)
		}
	}
}

func TestResolveDisableServicesCollapsesDuplicates(t *testing.T) {
	// Hooks used to return both spellings because only one of them worked.
	// Both still resolve, and they must not remove the same service twice.
	resolved, err := resolveDisableServices("postgres", composeFixture,
		[]string{"postgres_adminer", "anas_postgres_adminer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0] != "anas_postgres_adminer" {
		t.Fatalf("resolved = %v, want one entry", resolved)
	}
}

func TestResolveDisableServicesRejectsUnknownName(t *testing.T) {
	// The failure this exists for: a service renamed in Compose leaves the hook
	// disabling a name nothing answers to, and the optional service comes up.
	_, err := resolveDisableServices("postgres", composeFixture, []string{"postgres_adminer_old"})
	if err == nil {
		t.Fatal("an unknown service name was accepted")
	}
	message := err.Error()
	if !strings.Contains(message, "postgres_adminer_old") {
		t.Fatalf("error does not name the unknown service: %s", message)
	}
	// The list of real names is what turns this from a puzzle into a fix.
	if !strings.Contains(message, "anas_postgres_adminer") {
		t.Fatalf("error does not list the available services: %s", message)
	}
}

func TestResolveDisableServicesReportsEveryUnknownName(t *testing.T) {
	_, err := resolveDisableServices("postgres", composeFixture, []string{"gone_b", "gone_a"})
	if err == nil {
		t.Fatal("unknown service names were accepted")
	}
	// Sorted, so a hook returning several stale names is fixed in one pass
	// instead of one error per run.
	if !strings.Contains(err.Error(), "gone_a, gone_b") {
		t.Fatalf("error = %s, want both names in sorted order", err.Error())
	}
}

func TestResolveDisableServicesPassesThroughEmpty(t *testing.T) {
	resolved, err := resolveDisableServices("postgres", composeFixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 0 {
		t.Fatalf("resolved = %v, want nothing", resolved)
	}
}
