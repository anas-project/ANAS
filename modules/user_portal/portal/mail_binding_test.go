package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMailBindingPrivateCopyAndUnsafeInput(t *testing.T) {
	for _, mode := range []string{"private", "public", "symlink", "hardlink", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "input.json")
			output := filepath.Join(dir, "runtime", "config.json")
			if err := os.WriteFile(input, []byte("synthetic-private-binding"), 0600); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "public":
				if err := os.Chmod(input, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				other := input + ".real"
				if err := os.Rename(input, other); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, input); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(input, input+".link"); err != nil {
					t.Fatal(err)
				}
			case "oversized":
				if err := os.WriteFile(input, make([]byte, 65537), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := prepareMailBinding(input, output)
			if mode != "private" {
				if err == nil {
					t.Fatal("unsafe secret input accepted")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("unsafe input left runtime secret")
				}
				return
			}
			body, readErr := os.ReadFile(output)
			if err != nil || readErr != nil || string(body) != "synthetic-private-binding" {
				t.Fatal("private copy failed", err, readErr)
			}
			if err := prepareMailBinding(input, output); err != nil {
				t.Fatal("idempotent copy", err)
			}
			info, _ := os.Stat(output)
			if info.Mode().Perm() != 0600 {
				t.Fatal("runtime is not private")
			}
		})
	}
}

func TestMailBindingDisabledInputClearsPreviousRuntime(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	output := filepath.Join(dir, "runtime", "config.json")
	if err := os.WriteFile(input, []byte("synthetic-private-binding"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareMailBinding(input, output); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareMailBinding(input, output); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("disabled binding retained access")
	}
	if err := prepareMailBinding(input, output); err != nil {
		t.Fatal("repeated disable", err)
	}
}
