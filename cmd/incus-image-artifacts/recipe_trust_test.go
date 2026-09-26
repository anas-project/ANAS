package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const debianBootstrapRecipe = "image:\n  distribution: debian\nsource:\n  downloader: debootstrap\n  suite: trixie\n"

func TestDebianBuildRequiresBootstrapTrustBeforeAnyBuilder(t *testing.T) {
	for _, available := range []bool{false, true} {
		calls := 0
		err := validateRecipeBuildTrust(context.Background(), []byte(debianBootstrapRecipe), func(context.Context) error {
			calls++
			if !available {
				return errors.New("private-file-path-or-upstream-error")
			}
			return nil
		})
		if calls != 1 || available != (err == nil) || (err != nil && (!errors.Is(err, errDebianBootstrapTrust) || strings.Contains(err.Error(), "private"))) {
			t.Fatalf("bootstrap trust precondition: available=%t calls=%d error=%v", available, calls, err)
		}
	}
}

func TestDebianBuildCannotBypassTrustThroughAmbiguousRecipe(t *testing.T) {
	for _, recipe := range []string{
		debianBootstrapRecipe + "  skip_verification: true\n",
		debianBootstrapRecipe + "  skip_verification: true\n  skip_verification: false\n",
		debianBootstrapRecipe + "  skip_verification: null\n",
		debianBootstrapRecipe + "  Skip_Verification: true\n",
		debianBootstrapRecipe + "source: {downloader: debootstrap, skip_verification: false}\n",
		debianBootstrapRecipe + "---\nsource: {skip_verification: true}\n",
	} {
		err := validateRecipeBuildTrust(context.Background(), []byte(recipe), func(context.Context) error { return nil })
		if err == nil {
			t.Fatal("ambiguous or verification-skipping Debian recipe was accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := validateRecipeBuildTrust(ctx, []byte(debianBootstrapRecipe), func(context.Context) error {
		t.Fatal("canceled preflight inspected the filesystem")
		return nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal("bootstrap precondition lost caller cancellation", err)
	}
}

func TestSignaturePreflightDoesNotReserveTheRevision(t *testing.T) {
	requireArtifactArchivePlatform(t)
	base := t.TempDir()
	archive, recipe := filepath.Join(base, "archive"), filepath.Join(base, "recipe.yml")
	if err := os.WriteFile(recipe, []byte(debianBootstrapRecipe+"  skip_verification: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if run(context.Background(), []string{"init", "--archive", archive}, &out, &diagnostic) != 0 {
		t.Fatal("initialize isolated archive")
	}
	out.Reset()
	diagnostic.Reset()
	args := []string{"build", "--archive", archive, "--name", "forgejo-runner", "--revision", "verification-required",
		"--architecture", runtime.GOARCH, "--interface", "incus_container", "--recipe", recipe,
		"--distrobuilder", filepath.Join(base, "must-not-execute"), "--distrobuilder-sha256", strings.Repeat("a", 64)}
	if code := run(context.Background(), args, &out, &diagnostic); code == 0 || out.Len() != 0 ||
		!strings.Contains(diagnostic.String(), errDebianBootstrapTrust.Error()) || strings.Contains(diagnostic.String(), base) {
		t.Fatalf("signature preflight did not precede tool/revision effects: code=%d, diagnostic=%s", code, diagnostic.String())
	}
	files, err := filepath.Glob(filepath.Join(archive, "build-*"))
	if err != nil || len(files) != 0 {
		t.Fatal("missing signature assurance consumed a revision", err)
	}
}
