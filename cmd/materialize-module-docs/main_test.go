package main

import (
	"strings"
	"testing"

	"github.com/Masterminds/semver/v3"
)

func release(version string, revision int, complete bool, marker string) moduleRelease {
	docs := documentBundle{
		ReadmeZH:    "# 中文\n\n" + marker,
		ReadmeEN:    "# English\n\n" + marker,
		TechnicalZH: "# 技术\n\n" + marker,
		TechnicalEN: "# Technical\n\n" + marker,
	}
	return moduleRelease{Module: "demo", Version: version, Revision: revision, Parsed: semver.MustParse(version), Complete: complete, Docs: docs}
}

func TestSelectReleasesKeepsHighestRevisionAndFiveVersions(t *testing.T) {
	all := []moduleRelease{
		release("6.0.0", 1, true, "six"), release("5.0.0", 1, true, "five"),
		release("5.0.0", 3, true, "five-r3"), release("4.0.0", 2, true, "four"),
		release("3.0.0", 1, true, "three"), release("2.0.0", 1, true, "two"),
		release("1.0.0", 9, true, "one"),
	}
	selected, aliases := selectReleases(all, 5)
	if got := len(selected); got != 5 {
		t.Fatalf("selected %d releases, want 5", got)
	}
	if selected[1].ID() != "5.0.0-r3" {
		t.Fatalf("selected %s, want highest revision", selected[1].ID())
	}
	if aliases["5.0.0-r1"] != "5.0.0-r3" {
		t.Fatalf("old revision alias = %q", aliases["5.0.0-r1"])
	}
	if selected[4].Version != "2.0.0" {
		t.Fatalf("oldest selected version = %s, want 2.0.0", selected[4].Version)
	}
}

func TestDeduplicateReleasesKeepsNewestIdenticalDocumentation(t *testing.T) {
	selected := []moduleRelease{
		release("3.0.0", 2, true, "same"),
		release("2.0.0", 4, true, "same"),
		release("1.0.0", 1, true, "different"),
	}
	pages, aliases := deduplicateReleases(selected)
	if len(pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(pages))
	}
	if aliases["2.0.0-r4"] != "3.0.0-r2" {
		t.Fatalf("duplicate alias = %q", aliases["2.0.0-r4"])
	}
}

func TestFingerprintIgnoresOnlyGeneratedReleaseLine(t *testing.T) {
	left := release("2.0.0", 1, true, "- Module version / 版本：`2.0.0-r1`（reviewed 2026-01-01）").Docs
	right := release("1.0.0", 9, true, "- Module version / 版本：`1.0.0-r9`（reviewed 2025-01-01）").Docs
	if documentFingerprint(left, "2.0.0-r1") != documentFingerprint(right, "1.0.0-r9") {
		t.Fatal("release-only generated line changed the fingerprint")
	}
	right.TechnicalEN += "\nBehavior changed."
	if documentFingerprint(left, "2.0.0-r1") == documentFingerprint(right, "1.0.0-r9") {
		t.Fatal("technical content change did not change the fingerprint")
	}
}

func TestFingerprintNormalizesReleaseOnlyInsideGeneratedBlocks(t *testing.T) {
	document := func(release, mirror, status string) documentBundle {
		marker := "<!-- generated:module-facts:start -->\n| Version / revision | `" + release + "` |\n| Status | `" + status + "` |\n<!-- generated:module-facts:end -->\n" +
			"<!-- generated:module-identity:start -->\n> Status: current implementation; based on `" + release + "` / `anas.module/v1`.\n<!-- generated:module-identity:end -->\n" +
			"<!-- generated:compose-topology:start -->\n| `demo` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-demo:" + release + "` | `` | 0 |\n| `mirror` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-demo:" + mirror + "` | `` | 0 |\n<!-- generated:compose-topology:end -->\n" +
			"outside " + release
		return documentBundle{ReadmeZH: marker, ReadmeEN: marker, TechnicalZH: marker, TechnicalEN: marker}
	}
	left := document("1.0.0-r1", "7.0", "release")
	right := document("1.0.0-r2", "7.0", "release")
	// The marker-external release mention is reviewed prose and therefore keeps
	// these fingerprints distinct.
	if documentFingerprint(left, "1.0.0-r1") == documentFingerprint(right, "1.0.0-r2") {
		t.Fatal("marker-external release text was incorrectly normalized")
	}
	right = document("1.0.0-r2", "7.0", "release")
	right.ReadmeZH = strings.ReplaceAll(right.ReadmeZH, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.ReadmeEN = strings.ReplaceAll(right.ReadmeEN, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.TechnicalZH = strings.ReplaceAll(right.TechnicalZH, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.TechnicalEN = strings.ReplaceAll(right.TechnicalEN, "outside 1.0.0-r2", "outside 1.0.0-r1")
	if documentFingerprint(left, "1.0.0-r1") != documentFingerprint(right, "1.0.0-r2") {
		t.Fatal("generated release-only fields changed the fingerprint")
	}
	right = document("1.0.0-r2", "7.1", "release")
	right.ReadmeZH = strings.ReplaceAll(right.ReadmeZH, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.ReadmeEN = strings.ReplaceAll(right.ReadmeEN, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.TechnicalZH = strings.ReplaceAll(right.TechnicalZH, "outside 1.0.0-r2", "outside 1.0.0-r1")
	right.TechnicalEN = strings.ReplaceAll(right.TechnicalEN, "outside 1.0.0-r2", "outside 1.0.0-r1")
	if documentFingerprint(left, "1.0.0-r1") == documentFingerprint(right, "1.0.0-r2") {
		t.Fatal("mirror tag change was incorrectly normalized")
	}
	left = document("1.0.0-r1", "1.0.0-r1", "release")
	right = document("1.0.0-r2", "1.0.0-r2", "release")
	for _, target := range []*string{&right.ReadmeZH, &right.ReadmeEN, &right.TechnicalZH, &right.TechnicalEN} {
		*target = strings.ReplaceAll(*target, "outside 1.0.0-r2", "outside 1.0.0-r1")
	}
	if documentFingerprint(left, "1.0.0-r1") == documentFingerprint(right, "1.0.0-r2") {
		t.Fatal("mirror tag equal to the Module release ID was incorrectly normalized")
	}
}

func TestFingerprintPreservesLegacyShapedLabelsOutsideCurrentMarkers(t *testing.T) {
	document := func(release string) documentBundle {
		body := "<!-- generated:module-facts:start -->\n| Version / revision | `" + release + "` |\n<!-- generated:module-facts:end -->\n" +
			"\n## Reviewed example\n\n| Version / revision | `" + release + "` |"
		return documentBundle{ReadmeZH: body, ReadmeEN: body, TechnicalZH: body, TechnicalEN: body}
	}
	left := document("1.0.0-r1")
	right := document("1.0.0-r2")
	if documentFingerprint(left, "1.0.0-r1") == documentFingerprint(right, "1.0.0-r2") {
		t.Fatal("legacy-shaped label outside a current generated block was incorrectly normalized")
	}
}

func TestRewriteTechnicalLinksUsesImmutableRef(t *testing.T) {
	got := rewriteTechnicalLinks("[manifest](../module.yml)", "demo", "deadbeef", false)
	if !strings.Contains(got, "/blob/deadbeef/modules/demo/module.yml") {
		t.Fatalf("rewritten link = %q", got)
	}
}

func TestRewriteUserLinksMapsRepositoryDocsIntoSite(t *testing.T) {
	got := rewriteUserLinks("[support](../../docs/reference/module-iam-support.md#password)")
	want := "[support](/reference/module-iam-support.md#password)"
	if got != want {
		t.Fatalf("rewritten link = %q, want %q", got, want)
	}
}

func TestRenderVersionNavigationLinksToDirectoryIndex(t *testing.T) {
	got := renderVersionNavigation("demo", "2.0.0-r1", []string{"2.0.0-r1"}, false, true, false)
	want := "](/reference/modules/demo/2.0.0-r1/)"
	if !strings.Contains(got, want) {
		t.Fatalf("version navigation = %q, want link containing %q", got, want)
	}
}
