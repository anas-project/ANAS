package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnershipAndComposeUseSameEndpointDespiteModuleOverrides(t *testing.T) {
	for _, daemon := range []bool{false, true} {
		t.Run(map[bool]string{false: "cli", true: "daemon"}[daemon], func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			script := "#!/bin/sh\nprintf '%s|%s|%s\\n' \"$DOCKER_HOST\" \"$DOCKER_CONTEXT\" \"$*\" >> '" + log + "'\nexit 0\n"
			if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("DOCKER_HOST", "ssh://operator")
			t.Setenv("DOCKER_CONTEXT", "")
			cli, err := detectComposeForExecution(context.Background(), daemon)
			if err != nil {
				t.Fatal(err)
			}
			a := &app{workspace: dir, compose: cli, restrictedProcessEnvironment: daemon}
			if err := a.runCompose(dir, "example", "", map[string]string{"DOCKER_HOST": "tcp://wrong", "DOCKER_CONTEXT": "wrong"}, "up", "-d"); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			lines := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(lines) < 3 {
				t.Fatalf("missing ownership or mutation: %s", data)
			}
			endpoint := strings.SplitN(lines[0], "|", 3)
			for _, line := range lines[1:] {
				fields := strings.SplitN(line, "|", 3)
				if fields[0] != endpoint[0] || fields[1] != endpoint[1] || strings.Contains(line, "wrong") {
					t.Fatalf("endpoint diverged: %s", data)
				}
			}
		})
	}
}
