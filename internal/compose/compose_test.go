package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuietComposeMethodsDiscardUntrustedStderr(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "compose.sh")
	body := "#!/bin/sh\nprintf '%s\\n' service\nprintf '%s\\n' candidate-secret >&2\n"
	if err := os.WriteFile(script, []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
	captured, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	realStderr := os.Stderr
	os.Stderr = captured
	defer func() {
		os.Stderr = realStderr
		_ = captured.Close()
	}()

	cli := CLI{Bin: []string{script}}
	if err := cli.RunFileQuiet(dir, "anas_demo", "", map[string]string{"DEMO_SECRET": "candidate-secret"}, "up", "-d"); err != nil {
		t.Fatal(err)
	}
	out, err := cli.OutputFileQuiet(dir, "anas_demo", "", map[string]string{"DEMO_SECRET": "candidate-secret"}, "config", "--services")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "service" {
		t.Fatalf("quiet output = %q", out)
	}
	if _, err := captured.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	leaked, err := os.ReadFile(captured.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(leaked), "candidate-secret") {
		t.Fatalf("quiet Compose leaked stderr: %q", leaked)
	}
}
