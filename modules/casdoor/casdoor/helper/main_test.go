package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRenderInitHashesBreakGlassPassword(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "template.json")
	passwordPath := filepath.Join(dir, "password")
	outputPath := filepath.Join(dir, "out", "init.json")
	if err := os.WriteFile(templatePath, []byte(`{"password":"`+passwordPlaceholder+`"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordPath, []byte("very-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := renderInit(templatePath, passwordPath, outputPath); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), passwordPlaceholder) || strings.Contains(string(b), "very-secret") {
		t.Fatalf("rendered file contains plaintext or placeholder: %s", b)
	}
	var doc map[string]string
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(doc["password"]), []byte("very-secret")); err != nil {
		t.Fatal(err)
	}
}

func TestRenderInitRejectsAmbiguousTemplate(t *testing.T) {
	dir := t.TempDir()
	templatePath := filepath.Join(dir, "template.json")
	passwordPath := filepath.Join(dir, "password")
	if err := os.WriteFile(templatePath, []byte(passwordPlaceholder+passwordPlaceholder), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(passwordPath, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := renderInit(templatePath, passwordPath, filepath.Join(dir, "out.json")); err == nil {
		t.Fatal("expected ambiguous placeholder error")
	}
}
