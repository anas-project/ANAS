package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSealDeploymentRemovesWriteBitsWithoutWideningAccess(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "casks", "core"), 0700); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ before, after os.FileMode }{
		// A config asset stays readable by the service user inside a container.
		filepath.Join("casks", "core", "compose.yml"): {0644, 0444},
		// The frozen hook stays executable.
		filepath.Join("casks", "core", hookBinaryName): {0755, 0555},
		// Sealing must not turn an owner-only file into a world-readable one.
		filepath.Join("casks", "core", ".env"): {0600, 0400},
		"deployment.yml":                       {0600, 0400},
		deploymentConfigSourceName:             {0600, 0400},
	}
	for rel, mode := range cases {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), mode.before); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, rel), mode.before); err != nil {
			t.Fatal(err)
		}
	}
	if err := sealDeployment(root); err != nil {
		t.Fatal(err)
	}
	for rel, mode := range cases {
		info, err := os.Stat(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode.after {
			t.Errorf("%s sealed to %04o, want %04o", rel, info.Mode().Perm(), mode.after)
		}
	}
	// Directories stay writable: a read-only directory blocks unlink, and
	// unlink-and-replace allocates a new inode, which is the one mutation a
	// hard-linked snapshot copy is already safe against.
	info, err := os.Stat(filepath.Join(root, "casks", "core"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("directory sealed to %04o, want 0700", info.Mode().Perm())
	}
}

// A sealed file must reject an in-place write, because a snapshot that reuses
// the artifact by hard link shares the inode with it.
func TestSealedArtifactRejectsInPlaceWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write bit")
	}
	root := t.TempDir()
	path := filepath.Join(root, "compose.yml")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := sealDeployment(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0644); err == nil {
		t.Fatal("a sealed artifact file accepted an in-place write")
	}
}
