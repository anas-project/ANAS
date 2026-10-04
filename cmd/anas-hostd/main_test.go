package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestHostExecutableInventoryAndClosedOptions(t *testing.T) {
	for _, arg := range []string{"--actions", "--version", "--help"} {
		var out, diag bytes.Buffer
		code := run(context.Background(), []string{arg}, &out, &diag, func(context.Context) error { t.Fatal("inventory started execution"); return nil })
		if code != 0 || out.Len() == 0 || diag.Len() != 0 {
			t.Fatal(code, out.String(), diag.String())
		}
		if arg == "--actions" && (!strings.Contains(out.String(), `"source":"compiled-host"`) || !strings.Contains(out.String(), `"installation_verified":false`)) {
			t.Fatal(out.String())
		}
	}
	for _, args := range [][]string{nil, {"--command=private-marker"}, {"--serve", "--path=/private-marker"}, {"incus.install"}} {
		var out, diag bytes.Buffer
		if code := run(context.Background(), args, &out, &diag, nil); code != 2 || strings.Contains(diag.String(), "private-marker") {
			t.Fatal(code, diag.String())
		}
	}
}
func TestHostExecutableExitCodeIsIndependentOfPayload(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var out, diag bytes.Buffer
		code := run(context.Background(), []string{"--serve"}, &out, &diag, func(context.Context) error {
			if fail {
				return errors.New("private-marker")
			}
			return nil
		})
		if (code == 0) == fail || out.Len() != 0 || strings.Contains(diag.String(), "private-marker") {
			t.Fatal(code, diag.String())
		}
	}
}

// The boot unit's option runs the fixed restore and nothing else; a failure
// is reported without its detail.
func TestRestoreNetworkOptionRunsOnlyTheFixedRestore(t *testing.T) {
	previous := restoreNetwork
	t.Cleanup(func() { restoreNetwork = previous })
	for _, fail := range []bool{false, true} {
		calls := 0
		restoreNetwork = func(context.Context) error {
			calls++
			if fail {
				return errors.New("private-marker")
			}
			return nil
		}
		var out, diag bytes.Buffer
		code := run(context.Background(), []string{"--restore-network"}, &out, &diag, func(context.Context) error { t.Fatal("restore served an action"); return nil })
		if calls != 1 || (code == 0) == fail || out.Len() != 0 || strings.Contains(diag.String(), "private-marker") {
			t.Fatal(code, calls, diag.String())
		}
	}
	var out, diag bytes.Buffer
	if code := run(context.Background(), []string{"--restore-network", "extra"}, &out, &diag, nil); code != 2 {
		t.Fatal("the restore option accepted an argument")
	}
}
