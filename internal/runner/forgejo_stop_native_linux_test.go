//go:build linux

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/anas-project/ANAS/internal/compose"
)

// Called only by the VM-owning native driver while real Forgejo/Incus/Compose
// are running. The workspace is a declared lifecycle fixture, not a fabricated
// successful init/import/render deployment or an IAM/database-stack acceptance.
// It exercises the production Core stop method and the production frozen Hook;
// Docker process ownership and the API/guest cleanup are independently checked
// by the outer driver. No Compose/Hook/compute backend is replaced in this test.
func TestNativeForgejoCoreStop(t *testing.T) {
	if os.Getenv("ANAS_REQUIRE_FORGEJO_STOP_NATIVE") != "1" {
		t.Skip("requires an independently supervised disposable Forgejo stop VM")
	}
	const inputs = "/opt/anas-forgejo-stop-inputs"
	const workspace = "/srv/anas/native-forgejo-stop"
	identity, err := os.ReadFile("/var/lib/cloud/data/instance-id")
	vendor, ve := os.ReadFile("/sys/devices/virtual/dmi/id/sys_vendor")
	if err != nil || ve != nil || os.Getuid() != 0 || os.Geteuid() != 0 || strings.TrimSpace(string(vendor)) != "QEMU" ||
		!regexp.MustCompile(`^anas-incus-host-[a-f0-9]{6}$`).MatchString(strings.TrimSpace(string(identity))) {
		t.Fatal("exact disposable root/QEMU environment required")
	}
	read := func(path string) []byte {
		t.Helper()
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() > 96<<20 ||
			info.Sys().(*syscall.Stat_t).Uid != 0 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
			t.Fatal("untrusted native input")
		}
		for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
			st, e := os.Lstat(dir)
			if e != nil || !st.IsDir() || st.Mode().Perm()&0022 != 0 || st.Sys().(*syscall.Stat_t).Uid != 0 {
				t.Fatal("untrusted input ancestor")
			}
			if dir == "/" {
				break
			}
		}
		body, err := os.ReadFile(path)
		if err != nil || int64(len(body)) != info.Size() {
			t.Fatal("input changed")
		}
		return body
	}
	var manifest struct {
		Files map[string]string `json:"files"`
	}
	if json.Unmarshal(read(inputs+"/source-manifest.json"), &manifest) != nil {
		t.Fatal("input manifest missing")
	}
	for _, name := range []string{"core.test", "hook"} {
		body := read(inputs + "/" + name)
		digest := sha256.Sum256(body)
		if manifest.Files[name] != hex.EncodeToString(digest[:]) {
			t.Fatal("compiled native input identity mismatch")
		}
	}
	self, err := os.Executable()
	if err != nil || self != inputs+"/core.test" {
		t.Fatal("unexpected test executable")
	}
	which := os.Getenv("ANAS_NATIVE_STOP_CASE")
	if which != "active" && which != "repeated" && which != "disabled" && which != "reenabled" && which != "failed" {
		t.Fatal("unknown native stop case")
	}
	releaseName := "enabled"
	if which == "disabled" {
		releaseName = "disabled"
	}
	if which == "reenabled" {
		releaseName = "reenabled"
	}
	if which == "failed" {
		releaseName = "failed"
	}
	release := filepath.Join(workspace, ".anas", "deployments", releaseName, "modules")
	dir := filepath.Join(release, "forgejo")
	read(filepath.Join(dir, ".env"))
	read(filepath.Join(dir, "docker-compose.yml"))
	ctx, cancel := context.WithTimeout(context.Background(), 175*time.Second)
	defer cancel()
	a := &app{workspace: workspace, base: stateDir(workspace), commandContext: ctx, useFrozenHooks: true,
		suppressSensitiveOutput: true, restrictedProcessEnvironment: true,
		compose: compose.CLI{Bin: []string{"/usr/bin/docker", "compose"}},
		reg: map[string]Module{"forgejo": {Name: "forgejo", RuntimeType: "compose", ComposeFile: "docker-compose.yml", SourceDir: dir,
			Hook: HookConfig{Command: []string{inputs + "/hook"}, Phases: []string{"before_stop"}}}}, order: []string{"forgejo"},
		env: map[string]string{}, envOwner: map[string]string{}, secrets: &secretStore{values: map[string]string{}, metadata: map[string]secretMetadata{}}}
	err = a.stopModules(release, a.order, false)
	if which == "failed" {
		if err == nil || !strings.Contains(err.Error(), "before stopping forgejo") {
			t.Fatal("failed cleanup did not block the Core stop boundary")
		}
	} else if err != nil {
		t.Fatal("real Core/Compose stop did not complete its declared cleanup barrier")
	}
	if ctx.Err() != nil {
		t.Fatal("native test exceeded its parent deadline")
	}
}
