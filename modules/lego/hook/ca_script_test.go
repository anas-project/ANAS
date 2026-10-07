package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCABootstrapPreservesValidShortLivedCertificate(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl unavailable")
	}
	script, err := os.ReadFile("../lego/ca.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, domain, action string
		replace              bool
		expired              bool
		missingKey           bool
	}{
		{"public leaf below renewal threshold", "example.test", "bootstrap", false, false, false},
		{"changed domain", "other.test", "bootstrap", true, false, false},
		{"expired leaf", "example.test", "bootstrap", true, true, false},
		{"internal renewal below threshold", "example.test", "renew", true, false, false},
		{"public expiry during renewal", "example.test", "renew", true, true, false},
		{"missing private key", "example.test", "bootstrap", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ca, out := filepath.Join(root, "ca"), filepath.Join(root, "certificates")
			for _, p := range []string{ca, out} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			run := func(args ...string) {
				t.Helper()
				if b, e := exec.Command("openssl", args...).CombinedOutput(); e != nil {
					t.Fatalf("openssl: %v: %s", e, b)
				}
			}
			run("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "3650", "-keyout", filepath.Join(ca, "ca.key"), "-out", filepath.Join(ca, "ca.crt"), "-subj", "/CN=Internal test CA")
			leaf := filepath.Join(out, "example.test.crt")
			run("req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "30", "-keyout", filepath.Join(out, "example.test.key"), "-out", leaf, "-subj", "/CN=External test certificate", "-addext", "subjectAltName=DNS:"+tc.domain+",DNS:*."+tc.domain)
			if tc.expired {
				run("x509", "-in", leaf, "-signkey", filepath.Join(out, "example.test.key"), "-days", "0", "-out", leaf)
			}
			if tc.missingKey {
				if err := os.Remove(filepath.Join(out, "example.test.key")); err != nil {
					t.Fatal(err)
				}
			}
			before, e := os.ReadFile(leaf)
			if e != nil {
				t.Fatal(e)
			}
			issuer := "acme"
			if tc.action == "renew" && !tc.expired {
				issuer = "internal"
			}
			if e := os.WriteFile(filepath.Join(out, ".issuer"), []byte(issuer), 0600); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(root, "ca.sh")
			if e := os.WriteFile(path, []byte(strings.ReplaceAll(string(script), "/certs/", root+"/")), 0700); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command("sh", path, tc.action)
			cmd.Env = append(os.Environ(), "BASE_DOMAIN=example.test", "LEGO_CERT_NAME=example.test.crt", "LEGO_KEY_NAME=example.test.key", "LEGO_CA_CERT_NAME=example.test.issuer.crt")
			if b, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("ca script: %v: %s", e, b)
			}
			internal := filepath.Join(out, "anas-internal.crt")
			saved, e := os.ReadFile(internal)
			if e != nil {
				t.Fatal(e)
			}
			repeat := exec.Command("sh", path, tc.action)
			repeat.Env = cmd.Env
			if b, e := repeat.CombinedOutput(); e != nil {
				t.Fatalf("repeat ca script: %v: %s", e, b)
			}
			reused, e := os.ReadFile(internal)
			if e != nil || string(saved) != string(reused) {
				t.Fatal("internal certificate was not retained")
			}
			after, e := os.ReadFile(leaf)
			if e != nil {
				t.Fatal(e)
			}
			if (string(before) != string(after)) != tc.replace {
				t.Fatalf("certificate replacement = %t; want %t", string(before) != string(after), tc.replace)
			}
		})
	}
}
