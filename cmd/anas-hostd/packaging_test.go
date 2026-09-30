package main

import (
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/hostaction"
)

func TestHostReleaseAndUnitMatchObserverAssumptions(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, e := os.ReadFile("../../" + path)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	unit := read("packaging/systemd/anas-hostd@.service")
	for _, line := range []string{"Type=exec", "User=root", "Group=root", "Restart=no", "KillMode=control-group", "ExecStart=/usr/local/lib/anas/anas-hostd --serve", "StateDirectory=anas-hostd", "StateDirectoryMode=0700", "ProtectSystem=strict", "RestrictAddressFamilies=AF_UNIX AF_NETLINK AF_INET AF_INET6"} {
		if !strings.Contains("\n"+unit, "\n"+line+"\n") {
			t.Errorf("unit no longer supplies %s", line)
		}
	}
	var writable []string
	for _, line := range strings.Split(unit, "\n") {
		if value, ok := strings.CutPrefix(line, "ReadWritePaths="); ok {
			writable = append(writable, strings.Fields(value)...)
		}
	}
	for _, path := range []string{"/etc/anas", "/etc/passwd", "/var/lib/anas", "/var/lib/dpkg", "/var/lib/apt", "/var/cache/apt", "/var/cache/anas", "/run/anas/confirmations", "/usr", "/opt/incus"} {
		covered := false
		for _, root := range writable {
			if root == "/" {
				t.Fatal("host service must not grant a blanket writable root filesystem")
			}
			root = strings.TrimPrefix(root, "-")
			if path == root || strings.HasPrefix(path, root+"/") {
				covered = true
			}
		}
		if !covered {
			t.Errorf("unit no longer grants required package/managed write path %s", path)
		}
	}
	for _, forbidden := range []string{"ExecStartPre=", "ExecStartPost=", "ExecStop=", "Delegate=yes", "EnvironmentFile="} {
		if strings.Contains(unit, forbidden) {
			t.Errorf("unreviewed subprocess/environment in unit: %s", forbidden)
		}
	}
	socket := read("packaging/systemd/anas-hostd.socket")
	for _, line := range []string{"ListenStream=/run/anas/hostd.sock", "Accept=yes", "SocketUser=root", "SocketGroup=root", "SocketMode=0600"} {
		if !strings.Contains("\n"+socket, "\n"+line+"\n") {
			t.Errorf("activation lacks %s", line)
		}
	}
	build := read("scripts/ci/build-anas-release.sh")
	marker := strings.Index(build, `-o "${stage_dir}/anas-hostd"`)
	if marker < 0 {
		t.Fatal("host executable not packaged")
	}
	start := strings.LastIndex(build[:marker], "CGO_ENABLED=0")
	if start < 0 {
		t.Fatal("no host build block")
	}
	block := build[start:marker]
	for _, pin := range []string{"GOOS=linux", `GOARCH="$arch"`, "internal/buildinfo.Version=${version}", "internal/buildinfo.Commit=${commit}", "internal/buildinfo.Date=${build_date}"} {
		if !strings.Contains(block, pin) {
			t.Errorf("host build identity missing %s", pin)
		}
	}
	// Incus listens on the control bridge gateway itself since 2026-09-30.
	if strings.Contains(build, "control-relay") {
		t.Fatal("the retired control relay is still packaged with the release")
	}
}

func TestHostPackageTriggersCanWriteBootWithoutBlanketMountAccess(t *testing.T) {
	// Debian's official Incus dependencies invoke initramfs-tools. The native
	// packaged action failed at /boot/initrd*.dpkg-bak under ProtectSystem=strict.
	// Do not skip package triggers or turn off filesystem hardening to pass.
	body, err := os.ReadFile("../../packaging/systemd/anas-hostd@.service")
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, line := range strings.Split(string(body), "\n") {
		if value, ok := strings.CutPrefix(line, "ReadWritePaths="); ok {
			paths = append(paths, strings.Fields(value)...)
		}
	}
	if !slices.Contains(paths, "-/boot") {
		t.Fatal("official package initramfs trigger cannot complete; missing optional /boot write path")
	}
	slices.Sort(paths)
	want := []string{"-/boot", "-/opt", "/etc", "/run", "/tmp", "/usr", "/var"}
	if !slices.Equal(paths, want) {
		t.Fatal("package-trigger repair broadened unrelated writable trees or reset path restrictions")
	}
	for _, line := range []string{"ProtectSystem=strict", "ProtectHome=true", "NoNewPrivileges=true"} {
		if !strings.Contains("\n"+string(body), "\n"+line+"\n") {
			t.Errorf("package-trigger repair lost %s", line)
		}
	}
	other, err := os.ReadFile("../../packaging/systemd/anasd.service")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(other), "/boot") {
		t.Fatal("boot artifacts belong only to confirmed package actions, not the console")
	}
}

func TestHostUnitRuntimeCoversCompiledActions(t *testing.T) {
	body, err := os.ReadFile("../../packaging/systemd/anas-hostd@.service")
	if err != nil {
		t.Fatal(err)
	}
	longest := 0
	for _, descriptor := range hostaction.Catalog() {
		spec, ok := hostaction.LookupAction(descriptor.Name)
		if !ok || spec.Timeout <= 0 {
			t.Fatal("every host action needs a finite compiled deadline")
		}
		if spec.Timeout > longest {
			longest = spec.Timeout
		}
	}
	// The watchdog follows the handler deadline, leaving a bounded allowance
	// for activation/transport, final audit and independently observed exit.
	// A source-only timeout increase must not ship with the old service unit.
	want := strconv.Itoa(longest+30) + "s"
	seen := 0
	for _, line := range strings.Split(string(body), "\n") {
		if value, ok := strings.CutPrefix(line, "RuntimeMaxSec="); ok {
			seen++
			if value != want {
				t.Errorf("executor watchdog = %q; compiled action catalog requires %s", value, want)
			}
		}
	}
	if seen != 1 {
		t.Fatal("exactly one explicit finite executor watchdog is required")
	}
}

func TestHostPackageSandboxCanDropUIDWithoutDisablingHardening(t *testing.T) {
	body, err := os.ReadFile("../../packaging/systemd/anas-hostd@.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := "\n" + string(body)
	for _, required := range []string{
		"User=root", "Group=root", "NoNewPrivileges=true", "PrivateTmp=true",
		"ProtectHome=true", "ProtectSystem=strict", "AmbientCapabilities=CAP_SETUID",
	} {
		if !strings.Contains(unit, "\n"+required+"\n") {
			t.Errorf("fixed root executor lost package sandbox prerequisite %q", required)
		}
	}
	other, err := os.ReadFile("../../packaging/systemd/anasd.service")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(other), "AmbientCapabilities=CAP_SETUID") {
		t.Fatal("package-installation capability must not be added to the console")
	}
}
