package computeimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// These builders write opaque fixture bytes; they test release orchestration,
// not distrobuilder provenance, archive format validity or guest bootability.
func fixtureArtifactBuilder(t *testing.T, calls *int) ArtifactBuildFunc {
	t.Helper()
	return func(ctx context.Context, request ArtifactBuildRequest) error {
		*calls++
		if _, err := os.Stat(filepath.Join(filepath.Dir(request.RecipeFile), "attempt.json")); err != nil {
			t.Fatal("builder started before its durable attempt existed")
		}
		if _, err := os.Stat(filepath.Join(request.SourcesDirectory, "forgejo-runner")); err != nil {
			t.Fatal("builder started without a frozen forgejo-runner input")
		}
		body, err := os.ReadFile(request.RecipeFile)
		if err != nil || !bytes.HasPrefix(body, []byte("reviewed-recipe\n# anas-provenance-builder-sha256: ")) || !bytes.Contains(body, []byte("# anas-provenance-input-forgejo-runner-sha256: ")) {
			t.Fatal("builder did not receive the frozen recipe")
		}
		rootfs := "rootfs.squashfs"
		if request.Target.Interface == "incus_vm" {
			rootfs = "disk.qcow2"
		}
		for name, body := range map[string]string{"incus.tar.xz": "fixture metadata", rootfs: "fixture rootfs"} {
			if err := os.WriteFile(filepath.Join(request.OutputDirectory, name), []byte(body), 0600); err != nil {
				return err
			}
		}
		return nil
	}
}

func fixtureRunnerInput(t *testing.T) ArtifactBuildInput {
	t.Helper()
	path := filepath.Join(t.TempDir(), "forgejo-runner")
	body := append([]byte("\x7fELF"), bytes.Repeat([]byte("runner"), 1024)...)
	if err := os.WriteFile(path, body, 0500); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return ArtifactBuildInput{Name: "forgejo-runner", Path: path, SHA256: fmt.Sprintf("%x", sum[:])}
}

func TestArtifactBuildOnceReusesRecordedRevisionAndNeverRebuildsMissingBytes(t *testing.T) {
	for _, iface := range []string{"incus_container", "incus_vm"} {
		t.Run(iface, func(t *testing.T) {
			archive, ref, target, _ := newArtifactArchiveFixture(t)
			target.Interface = iface
			ctx := context.Background()
			calls := 0
			build := fixtureArtifactBuilder(t, &calls)
			recipe := []byte("reviewed-recipe")
			input := fixtureRunnerInput(t)
			first, existing, err := archive.BuildOnce(ctx, ref, target, recipe, strings.Repeat("a", 64), []ArtifactBuildInput{input}, build)
			if err != nil || existing || calls != 1 {
				t.Fatalf("first build: existing=%v calls=%d err=%v", existing, calls, err)
			}
			repeated, existing, err := archive.BuildOnce(ctx, ref, target, recipe, strings.Repeat("a", 64), []ArtifactBuildInput{input}, build)
			if err != nil || !existing || calls != 1 || !reflect.DeepEqual(first, repeated) {
				t.Fatalf("rebuild instead of reuse: %v", err)
			}
			if _, _, err := archive.BuildOnce(ctx, ref, target, []byte("changed recipe"), strings.Repeat("a", 64), []ArtifactBuildInput{input}, build); !errors.Is(err, ErrArtifactConflict) || calls != 1 {
				t.Fatalf("changed recipe was accepted: %v", err)
			}
			changedInput := input
			changedInput.SHA256 = strings.Repeat("b", 64)
			if _, _, err := archive.BuildOnce(ctx, ref, target, recipe, strings.Repeat("a", 64), []ArtifactBuildInput{changedInput}, build); !errors.Is(err, ErrArtifactConflict) || calls != 1 {
				t.Fatalf("changed runner input was accepted: %v", err)
			}
			if err := archive.root.Remove(artifactObjectFilename(first.Artifact.Parts[1].SHA256)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := archive.BuildOnce(ctx, ref, target, recipe, strings.Repeat("a", 64), []ArtifactBuildInput{input}, build); !errors.Is(err, ErrArtifactUnavailable) || calls != 1 {
				t.Fatalf("missing committed bytes triggered a new build: %v", err)
			}
			name, _ := artifactReleaseFilename(ref, target)
			directory := filepath.Join(archive.path, "build-"+strings.TrimSuffix(filepath.Base(name), ".json"), "output")
			rootfs := "rootfs.squashfs"
			if iface == "incus_vm" {
				rootfs = "disk.qcow2"
			}
			frozenRecipe, err := os.ReadFile(filepath.Join(filepath.Dir(directory), "recipe.yml"))
			if err != nil {
				t.Fatal(err)
			}
			restored, existed, err := archive.Record(ctx, ref, target, frozenRecipe, ArtifactSplit, []string{filepath.Join(directory, "incus.tar.xz"), filepath.Join(directory, rootfs)})
			if err != nil || !existed || !reflect.DeepEqual(restored, first) {
				t.Fatalf("identical-output recovery: %v", err)
			}
		})
	}
}

func TestArtifactBuildFailureSurvivesArchiveReopenWithoutRetry(t *testing.T) {
	archive, ref, target, _ := newArtifactArchiveFixture(t)
	calls := 0
	build := func(context.Context, ArtifactBuildRequest) error {
		calls++
		return errors.New("private-builder-stderr-marker")
	}
	_, _, err := archive.BuildOnce(context.Background(), ref, target, []byte("recipe"), strings.Repeat("a", 64), []ArtifactBuildInput{fixtureRunnerInput(t)}, build)
	if !errors.Is(err, ErrArtifactBuildIncomplete) || strings.Contains(err.Error(), "private-builder-stderr-marker") {
		t.Fatalf("unsafe failure: %v", err)
	}
	path := archive.path
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenArtifactArchive(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, _, err := reopened.BuildOnce(context.Background(), ref, target, []byte("recipe"), strings.Repeat("a", 64), []ArtifactBuildInput{fixtureRunnerInput(t)}, build); !errors.Is(err, ErrArtifactBuildIncomplete) || calls != 1 {
		t.Fatalf("interrupted revision silently rebuilt: calls=%d err=%v", calls, err)
	}
	if _, err := reopened.Inspect(context.Background(), ref, target); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("failed build published a revision: %v", err)
	}
}

func TestArtifactBuildPreservesOnlyClosedDiagnosticStages(t *testing.T) {
	for _, stage := range []BuildStage{BuildStagePackages, BuildStage(255)} {
		archive, ref, target, _ := newArtifactArchiveFixture(t)
		_, _, err := archive.BuildOnce(context.Background(), ref, target, []byte("recipe"), strings.Repeat("a", 64), []ArtifactBuildInput{fixtureRunnerInput(t)}, func(context.Context, ArtifactBuildRequest) error {
			return fmt.Errorf("private-builder-url-and-token: %w", BuildFailureAt(stage))
		})
		want := "packages"
		if stage == 255 {
			want = "unknown"
		}
		if !errors.Is(err, ErrArtifactBuildIncomplete) || !strings.HasSuffix(err.Error(), "stage: "+want) || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
	}
}

func TestArtifactBuildRejectsCancellationAndRecipeMutation(t *testing.T) {
	for _, scenario := range []string{"cancel", "mutation", "missing output"} {
		t.Run(scenario, func(t *testing.T) {
			archive, ref, target, _ := newArtifactArchiveFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			build := func(_ context.Context, r ArtifactBuildRequest) error {
				switch scenario {
				case "cancel":
					cancel()
				case "mutation":
					if err := os.Chmod(r.RecipeFile, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(r.RecipeFile, []byte("different"), 0400); err != nil {
						t.Fatal(err)
					}
				}
				return nil
			}
			if _, _, err := archive.BuildOnce(ctx, ref, target, []byte("recipe"), strings.Repeat("a", 64), []ArtifactBuildInput{fixtureRunnerInput(t)}, build); err == nil {
				t.Fatal("invalid build published a revision")
			} else if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation identity lost: %v", err)
			}
			if _, err := archive.Inspect(context.Background(), ref, target); !errors.Is(err, ErrArtifactNotFound) {
				t.Fatalf("unexpected revision: %v", err)
			}
		})
	}
}
