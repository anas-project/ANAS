package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/modules/mail/dms"
)

func TestPrivateDMSConfigRejectsUnsafeInputsWithoutSecrets(t *testing.T) {
	cfg := config{Host: "mail.example.test:9443", Token: strings.Repeat("a", 64), CertFile: "/certs/internal.crt", KeyFile: "/certs/internal.key", CapacityBytes: 2 << 30, Runtime: dms.RuntimeConfig{GoURL: "https://manage.example.test:8443", DovecotToken: strings.Repeat("b", 64), SenderToken: strings.Repeat("c", 64), CAFile: "/certs/trust.crt"}}
	path := filepath.Join(t.TempDir(), "private.json")
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = load(path); err != nil {
		t.Fatal("valid private config denied", err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = load(path); err == nil || strings.Contains(err.Error(), cfg.Token) {
		t.Fatal("public file accepted or token leaked")
	}
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{string(raw) + " {}", `{"Token":"synthetic-private-secret","Unknown":true}`, strings.Repeat("x", 16385)} {
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = load(path); err == nil || strings.Contains(err.Error(), "synthetic-private-secret") {
			t.Fatal("unsafe config accepted or secret leaked")
		}
	}
	link := filepath.Join(t.TempDir(), "link.json")
	if err = os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err = load(link); err == nil {
		t.Fatal("symlink configuration accepted")
	}
}
