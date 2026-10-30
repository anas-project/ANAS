// TEST_CASES: MAILMOD-T-066
package cloudrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapFixture(t *testing.T) (RuntimeConfig, []byte, string) {
	t.Helper()
	parent := t.TempDir()
	cfg := runtimeFixture(t, filepath.Join(parent, "source"), "127.0.0.1:18448")
	cfg.SMTPListen = "127.0.0.1:18449"
	cfg.SMTPHostname = "relay.example.test"
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, raw, parent
}
func bootstrapMessage(t *testing.T, action string, config []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(bootstrapRequest{Schema: "anas.mail-systemd-bootstrap/v1", Action: action, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestBootstrapFixedOperationsAndPrivateCleanup(t *testing.T) {
	cfg, config, parent := bootstrapFixture(t)
	var captured [][]byte
	calls := 0
	verify := func(path string) error {
		calls++
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Fatal("unsafe configuration permissions", err)
		}
		if info, err := os.Stat(filepath.Dir(path)); err != nil || info.Mode().Perm() != 0700 {
			t.Fatal("unsafe temporary directory", err)
		}
		c, err := LoadRuntimeConfig(path)
		if err != nil || c.Reader != cfg.Reader || c.Control != cfg.Control {
			t.Fatal("configuration changed", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		captured = append(captured, raw)
		return nil
	}
	actions := bootstrapActions{publish: verify, activate: func(_ context.Context, path string) error { return verify(path) }, inspect: func(_ context.Context, path string) (string, error) { return "inactive", verify(path) }}
	for _, action := range []string{"publish", "activate", "inspect"} {
		var out bytes.Buffer
		err := runBootstrap(context.Background(), bytes.NewReader(bootstrapMessage(t, action, config)), &out, parent, actions)
		if err != nil {
			t.Fatal(action, err)
		}
		for _, secret := range []string{cfg.Reader, cfg.Control, cfg.TLSPrivateKey} {
			if strings.Contains(out.String(), secret) {
				t.Fatal("secret in reply")
			}
		}
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, config, "", "  "); err != nil {
		t.Fatal(err)
	}
	if err := runBootstrap(context.Background(), bytes.NewReader(bootstrapMessage(t, "publish", indented.Bytes())), &bytes.Buffer{}, parent, actions); err != nil {
		t.Fatal(err)
	}
	if calls != 4 || !bytes.Equal(captured[0], captured[3]) {
		t.Fatal("canonical retry changed bytes")
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("temporary configuration remains", err)
	}
	if strings.Contains(fmt.Sprintf("%+v", bootstrapRequest{Config: config}), cfg.Reader) {
		t.Fatal("request formatting leaked")
	}
}
func TestBootstrapRejectsUnsafeRequestsAndPreservesUnknown(t *testing.T) {
	cfg, config, parent := bootstrapFixture(t)
	calls := 0
	actions := bootstrapActions{publish: func(string) error { calls++; return ErrPublishUnknown }}
	valid := bootstrapMessage(t, "publish", config)
	for name, input := range map[string][]byte{
		"unknown action": bootstrapMessage(t, "shell", config), "extra field": bytes.Replace(valid, []byte(`"schema":`), []byte(`"extra":1,"schema":`), 1), "duplicate field": bytes.Replace(valid, []byte(`"action":`), []byte(`"action":"publish","action":`), 1), "duplicate config": bytes.Replace(valid, []byte(`"reader":`), []byte(`"reader":"`+cfg.Reader+`","reader":`), 1), "trailing": append(append([]byte(nil), valid...), []byte(` {}`)...), "oversized": bytes.Repeat([]byte("x"), 96<<10+1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := runBootstrap(context.Background(), bytes.NewReader(input), &bytes.Buffer{}, parent, actions); !errors.Is(err, ErrPublishConflict) {
				t.Fatal("accepted", err)
			}
		})
	}
	if calls != 0 {
		t.Fatal("unsafe input executed")
	}
	var output bytes.Buffer
	if err := runBootstrap(context.Background(), bytes.NewReader(valid), &output, parent, actions); !errors.Is(err, ErrPublishUnknown) || output.String() != "{\"state\":\"unknown\"}\n" {
		t.Fatal("unknown outcome lost", err, output.String())
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("private temporary config remains", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runBootstrap(ctx, bytes.NewReader(valid), &bytes.Buffer{}, parent, actions); !errors.Is(err, ErrPublishUnknown) || calls != 1 {
		t.Fatal("canceled operation executed")
	}
}
