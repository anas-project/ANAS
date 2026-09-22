package incushost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestOSReleaseStrictDataOnly(t *testing.T) {
	for _, text := range []string{
		"ID=ubuntu\nVERSION_ID=24.04\nVERSION_CODENAME=noble\n",
		"# comment\nNAME=\"Ubuntu Linux\"\nID='ubuntu'\nVERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n",
		"ID=ubuntu\nVERSION_ID=24.04\nVERSION_CODENAME=noble\nVENDOR=\"a\\$b\\`c\\\"d\\\\e\"\n",
	} {
		got, err := ParseOSRelease([]byte(text))
		if err != nil || got != (Release{ID: "ubuntu", Version: "24.04", Codename: "noble"}) {
			t.Fatalf("valid identity: %#v, %v", got, err)
		}
	}
	for name, text := range map[string]string{
		"duplicate":           "ID=ubuntu\nID=debian\nVERSION_ID=13",
		"equal duplicate":     "ID=ubuntu\nID=ubuntu\nVERSION_ID=24.04",
		"command":             "ID=$(touch /tmp/secret-marker)\nVERSION_ID=24.04",
		"backtick":            "ID=`secret-marker`",
		"expansion":           "ID=\"$secret-marker\"",
		"concatenation":       "ID=\"ubu\"\"ntu\"",
		"shell tail":          "ID=ubuntu;secret-marker",
		"unrelated bad field": "ID=ubuntu\nPRETTY_NAME=$(secret-marker)",
		"mixed quotes":        "ID='ubuntu\"",
		"unterminated":        "ID=\"ubuntu",
		"lowercase key":       "id=ubuntu",
		"export":              "export ID=ubuntu",
		"null":                "ID=ubuntu\x00",
		"carriage return":     "ID=ubuntu\r\n",
		"utf8":                "ID=ubuntu\nNAME=\"\xff\"",
		"oversize":            strings.Repeat("x", MaxOSReleaseBytes+1),
		"long line":           "ID=ubuntu\nNAME=\"" + strings.Repeat("x", 4097) + "\"",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseOSRelease([]byte(text))
			if !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "secret-marker") {
				t.Fatalf("unsafe parse: %v", err)
			}
		})
	}
	// Rolling/unknown releases without VERSION_ID are valid observations,
	// but cannot acquire another distro's recipe by ID_LIKE.
	r, err := ParseOSRelease([]byte("ID=derivative\nID_LIKE=ubuntu\n"))
	if err != nil || r.Version != "" {
		t.Fatal(r, err)
	}
}

func TestCompiledRecipesAreStrictAndDetached(t *testing.T) {
	rows, err := Recipes()
	if err != nil || len(rows) != 3 {
		t.Fatal(rows, err)
	}
	rows[0].Packages[0] = "changed"
	rows[0].Architectures[0] = "changed"
	other, err := Recipes()
	if err != nil || other[0].Packages[0] != "incus" || other[0].Architectures[0] != "amd64" {
		t.Fatal("catalog aliasing")
	}
	for _, edit := range []func([]byte) []byte{
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"id":`), []byte(`"command":"sh","id":`), 1) },
		func(b []byte) []byte { return bytes.Replace(b, []byte(`"id":`), []byte(`"id":"other","id":`), 1) },
		func(b []byte) []byte {
			return bytes.ReplaceAll(b, []byte(`"incus-client"`), []byte(`"--allow-unauthenticated"`))
		},
		func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"official"`), []byte(`"ppa"`)) },
		func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"2026-09-19"`), []byte(`"unknown"`)) },
	} {
		if _, err := parseRecipes(edit(bytes.Clone(recipeData))); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad compiled recipe accepted: %v", err)
		}
	}
}

func TestRecipesIncludeManagedBridgeRuntimeDependency(t *testing.T) {
	rows, err := Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if !slices.Contains(row.Packages, "dnsmasq-base") {
			t.Errorf("%s omits dnsmasq-base required by managed bridge NAT/DHCP; no-install-recommends cannot supply it", row.ID)
		}
	}
}

func TestPreflightDistinguishesPackagingFromRuntimeReadiness(t *testing.T) {
	rows, err := Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		for _, arch := range row.Architectures {
			facts := Facts{OS: "linux", Architecture: arch, Release: Release{ID: row.Distribution, Version: row.Version, Codename: row.Codename}, Systemd: true}
			result, err := Preflight(facts, Options{})
			if err != nil || !result.DistributionMatched || result.ComputeReady || result.RuntimeVerified || result.Interface != "incus_container" || result.Disposition != "disabled" || slices.Contains(result.Blockers, "kvm_not_observed") {
				t.Fatalf("false readiness/default: %#v, %v", result, err)
			}
			if !slices.Contains(result.Blockers, "daemon_compatibility_unverified") {
				t.Fatal("package version treated as runtime acceptance")
			}
			vm, err := Preflight(facts, Options{Interface: "incus_vm"})
			if err != nil || vm.Interface != "incus_vm" || !slices.Contains(vm.Blockers, "kvm_not_observed") {
				t.Fatal("VM downgraded", vm, err)
			}
			facts.KVMDevice = true
			container, err := Preflight(facts, Options{})
			if err != nil || container.Interface != "incus_container" {
				t.Fatal("container upgraded")
			}
		}
	}
}

func TestPreflightUnsupportedSkippedAndInvalid(t *testing.T) {
	base := Facts{OS: "linux", Architecture: "amd64", Release: Release{ID: "ubuntu", Version: "26.04", Codename: "resolute"}, Systemd: true}
	for name, test := range map[string]struct {
		change  func(*Facts)
		blocker string
	}{
		"old release":       {func(f *Facts) { f.Release = Release{ID: "ubuntu", Version: "22.04"} }, "distribution_not_adapted"},
		"derivative":        {func(f *Facts) { f.Release = Release{ID: "mint", Version: "26.04"} }, "distribution_not_adapted"},
		"codename conflict": {func(f *Facts) { f.Release.Codename = "noble" }, "release_identity_conflict"},
		"no version":        {func(f *Facts) { f.Release.Version = "" }, "distribution_not_adapted"},
		"architecture":      {func(f *Facts) { f.Architecture = "riscv64" }, "architecture_not_adapted"},
		"init":              {func(f *Facts) { f.Systemd = false }, "init_not_adapted"},
		"mac":               {func(f *Facts) { f.OS = "darwin"; f.Release = Release{} }, "linux_required"},
	} {
		t.Run(name, func(t *testing.T) {
			facts := base
			test.change(&facts)
			r, err := Preflight(facts, Options{})
			if err != nil || r.Disposition != "disabled" || r.ComputeReady || !slices.Contains(r.Blockers, test.blocker) {
				t.Fatal(r, err)
			}
		})
	}
	skip, err := Preflight(base, Options{Skip: true})
	if err != nil || skip.Disposition != "skipped" || skip.Recipe != nil || skip.ComputeReady {
		t.Fatal(skip, err)
	}
	if _, err := Preflight(base, Options{Interface: "automatic"}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LocalPreflight(ctx, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := LocalPreflight(nil, Options{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if skipped, err := LocalPreflight(context.Background(), Options{Skip: true}); err != nil || skipped.Disposition != "skipped" || skipped.Facts.Release.ID != "" {
		t.Fatal("skip consulted OS files", skipped, err)
	}
	body, err := json.Marshal(skip)
	if err != nil || !bytes.Contains(body, []byte(`"compute_ready":false`)) {
		t.Fatal("readiness omitted")
	}
}
