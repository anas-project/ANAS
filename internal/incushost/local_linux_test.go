//go:build linux

package incushost

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalReleaseSelectionAndFilesystemBoundary(t *testing.T) {
	for _, test := range []struct {
		name  string
		setup func(string)
		want  string
		fail  bool
	}{
		{"etc wins", func(root string) { writeReleaseFixture(t, root, "etc/os-release", "ID=ubuntu\nVERSION_ID=26.04\n") }, "ubuntu", false},
		{"vendor fallback", func(string) {}, "debian", false},
		{"relative symlink", func(root string) {
			if err := os.Symlink("../usr/lib/os-release", filepath.Join(root, "etc/os-release")); err != nil {
				t.Fatal(err)
			}
		}, "debian", false},
		{"absolute canonical symlink", func(root string) {
			if err := os.Symlink("/usr/lib/os-release", filepath.Join(root, "etc/os-release")); err != nil {
				t.Fatal(err)
			}
		}, "debian", false},
		{"unsafe symlink", func(root string) {
			if err := os.Symlink("/etc/passwd", filepath.Join(root, "etc/os-release")); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"FIFO", func(root string) {
			if err := unix.Mkfifo(filepath.Join(root, "etc/os-release"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"directory", func(root string) {
			if err := os.Mkdir(filepath.Join(root, "etc/os-release"), 0700); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"writable etc", func(root string) {
			if err := os.Chmod(filepath.Join(root, "etc"), 0777); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"hardlink", func(root string) {
			if err := os.Link(filepath.Join(root, "usr/lib/os-release"), filepath.Join(root, "etc/os-release")); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"writable vendor file", func(root string) {
			if err := os.Chmod(filepath.Join(root, "usr/lib/os-release"), 0666); err != nil {
				t.Fatal(err)
			}
		}, "", true},
		{"bad etc no fallback", func(root string) { writeReleaseFixture(t, root, "etc/os-release", "") }, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			for _, part := range []string{"etc", "usr/lib"} {
				if err := os.MkdirAll(filepath.Join(root, part), 0755); err != nil {
					t.Fatal(err)
				}
			}
			writeReleaseFixture(t, root, "usr/lib/os-release", "ID=debian\nVERSION_ID=13\n")
			test.setup(root)
			fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer unix.Close(fd)
			body, err := readOSReleaseAt(context.Background(), fd, uint32(os.Geteuid()))
			if test.fail {
				if err == nil {
					t.Fatal("unsafe source accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r, err := ParseOSRelease(body)
			if err != nil || r.ID != test.want {
				t.Fatal(r, err)
			}
		})
	}
}

func writeReleaseFixture(t *testing.T, root, path, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
