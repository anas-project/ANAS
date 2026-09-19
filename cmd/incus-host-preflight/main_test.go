package main

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/anas-project/ANAS/internal/incushost"
	"strings"
	"testing"
)

func TestPreflightCLICatalogAndHelp(t *testing.T) {
	var out, diag bytes.Buffer
	if code := run(context.Background(), []string{"--recipes"}, &out, &diag); code != 0 {
		t.Fatal(code, diag.String())
	}
	var rows []incushost.Recipe
	if json.Unmarshal(out.Bytes(), &rows) != nil || len(rows) != 3 {
		t.Fatal("catalog not printed")
	}
	out.Reset()
	diag.Reset()
	if code := run(context.Background(), []string{"--help"}, &out, &diag); code != 0 || !strings.Contains(diag.String(), "Read-only") {
		t.Fatal(code, diag.String())
	}
}

func TestPreflightCLIRejectsExternalEffects(t *testing.T) {
	for _, args := range [][]string{{"--root", "secret-marker"}, {"--install"}, {"--interface", "lan"}, {"--recipes", "--skip"}, {"--recipes", "--interface", "incus_container"}, {"unexpected"}} {
		var out, diag bytes.Buffer
		if code := run(context.Background(), args, &out, &diag); code != 2 || out.Len() != 0 || strings.Contains(diag.String(), "secret-marker") {
			t.Fatal(args, code, diag.String())
		}
	}
}
