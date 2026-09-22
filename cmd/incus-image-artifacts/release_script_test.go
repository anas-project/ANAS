package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// This is an orchestration fixture, not a real distrobuilder or guest test.
// The Go CLI and archive byte semantics are tested separately using real files.
func TestReleaseScriptUsesCompleteBundleAndValidatesBeforeBuild(t *testing.T) {
	for _, scenario := range []string{"first", "previous", "no-history", "ambiguous-history", "existing-output", "symlink-output", "invalid-history", "invalid-switch", "build-failure"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			bin, output, log := filepath.Join(base, "bin"), filepath.Join(base, "release"), filepath.Join(base, "calls")
			if err := os.Mkdir(bin, 0700); err != nil {
				t.Fatal(err)
			}
			stub := `#!/bin/bash
set -eu
[ "$1" = run ] && [ "$2" = ./cmd/incus-image-artifacts ] || exit 95
shift 2
command=$1
shift
printf '%s\n' "$command" >>"$FIXTURE_LOG"
case "$command" in
  catalog)
    [ "$FIXTURE_CASE" != invalid-history ] || exit 94
    ;;
  recipe) printf 'reviewed fixture recipe\n' ;;
  build) [ "$FIXTURE_CASE" != build-failure ] || exit 93 ;;
  bundle)
    destination=
    while [ "$#" -gt 0 ]; do
      case "$1" in
        --output-dir) destination=$2; shift ;;
        --previous-catalog) [ "$2" = "$ANAS_INCUS_IMAGE_PREVIOUS_CATALOG" ] || exit 92; shift ;;
      esac
      shift
    done
    [ "$destination" = "$ANAS_INCUS_IMAGE_OUTPUT/images" ] || exit 91
    [ ! -e "$destination" ] || exit 90
    mkdir "$destination"
    printf '["bundle fixture"]\n' >"$destination/catalog.json"
    ;;
  *) exit 89 ;;
esac
`
			if err := os.WriteFile(filepath.Join(bin, "go"), []byte(stub), 0700); err != nil {
				t.Fatal(err)
			}
			values := map[string]string{
				"ANAS_INCUS_IMAGE_ARCHIVE":       filepath.Join(base, "archive"),
				"ANAS_INCUS_IMAGE_OUTPUT":        output,
				"ANAS_INCUS_IMAGE_REVISION":      "r1",
				"ANAS_INCUS_IMAGE_ARCHITECTURE":  "amd64",
				"ANAS_DISTROBUILDER":             filepath.Join(base, "distrobuilder"),
				"ANAS_DISTROBUILDER_SHA256":      strings.Repeat("a", 64),
				"ANAS_FORGEJO_RUNNER":            filepath.Join(base, "runner"),
				"ANAS_FORGEJO_RUNNER_SHA256":     strings.Repeat("b", 64),
				"ANAS_INCUS_IMAGE_FIRST_RELEASE": "1",
				"FIXTURE_LOG":                    log,
				"FIXTURE_CASE":                   scenario,
			}
			switch scenario {
			case "previous":
				values["ANAS_INCUS_IMAGE_FIRST_RELEASE"] = "0"
				values["ANAS_INCUS_IMAGE_PREVIOUS_CATALOG"] = filepath.Join(base, "trusted previous catalog.json")
			case "no-history":
				delete(values, "ANAS_INCUS_IMAGE_FIRST_RELEASE")
			case "ambiguous-history":
				values["ANAS_INCUS_IMAGE_PREVIOUS_CATALOG"] = filepath.Join(base, "previous.json")
			case "existing-output":
				if err := os.Mkdir(output, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink-output":
				if err := os.Symlink(filepath.Join(base, "nonexistent"), output); err != nil {
					t.Fatal(err)
				}
			case "invalid-switch":
				values["ANAS_INCUS_IMAGE_INIT_ARCHIVE"] = "yes"
			}
			cmd := exec.Command("bash", filepath.Join("..", "..", "scripts", "ci", "incus-image-release-build.sh"))
			// Do not inherit real release settings or invoke a real Go/builder.
			cmd.Env = []string{"PATH=" + bin + ":" + os.Getenv("PATH")}
			for key, value := range values {
				cmd.Env = append(cmd.Env, key+"="+value)
			}
			body, err := cmd.CombinedOutput()
			calls, readErr := os.ReadFile(log)
			if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
			got := strings.Fields(string(calls))
			success := scenario == "first" || scenario == "previous"
			if success {
				want := []string{"catalog", "recipe", "build", "recipe", "build", "bundle"}
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("wrong release order: calls=%v err=%v output=%s", got, err, body)
				}
				if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0700 {
					t.Fatalf("release output is not private: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("invalid release succeeded: %s", body)
			}
			var want []string
			if scenario == "invalid-history" {
				want = []string{"catalog"}
			} else if scenario == "build-failure" {
				want = []string{"catalog", "recipe", "build"}
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("failure performed unexpected actions: %v want %v", got, want)
			}
			if _, err := os.Lstat(filepath.Join(output, "images", "catalog.json")); !os.IsNotExist(err) {
				t.Fatal("failed release published or truncated a catalog")
			}
		})
	}
}
