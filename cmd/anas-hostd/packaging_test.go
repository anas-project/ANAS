package main

import (
	"os"
	"strings"
	"testing"
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
	for _, line := range []string{"Type=exec", "User=root", "Group=root", "Restart=no", "KillMode=control-group", "RuntimeMaxSec=615s", "ExecStart=/usr/local/lib/anas/anas-hostd --serve", "StateDirectory=anas-hostd", "StateDirectoryMode=0700", "ProtectSystem=strict", "RestrictAddressFamilies=AF_UNIX AF_NETLINK AF_INET AF_INET6"} {
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
	for _, path := range []string{"/etc/anas", "/etc/passwd", "/var/lib/anas", "/var/lib/dpkg", "/var/lib/apt", "/var/cache/apt", "/var/cache/anas", "/run/anas/confirmations", "/usr"} {
		covered := false
		for _, root := range writable {
			if root == "/" {
				t.Fatal("host service must not grant a blanket writable root filesystem")
			}
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
	relay := read("packaging/systemd/anas-incus-control-relay.service")
	if !strings.Contains(relay, "ExecStart=/usr/local/lib/anas/anas-incus-control-relay --config /etc/anas/incus-control-relay.json") ||
		!strings.Contains(relay, "RestrictAddressFamilies=AF_INET") {
		t.Fatal("relay service no longer uses fixed binary/config/network family")
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
	if !strings.Contains(build, `-o "${stage_dir}/anas-incus-control-relay"`) ||
		!strings.Contains(build, "./modules/incus/control-relay") ||
		!strings.Contains(build, "anas-incus-control-relay.service") {
		t.Fatal("relay binary/service not packaged with release")
	}
}
