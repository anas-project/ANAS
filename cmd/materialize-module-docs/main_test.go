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
	if documentFingerprint(left) != documentFingerprint(right) {
		t.Fatal("release-only generated line changed the fingerprint")
	}
	right.TechnicalEN += "\nBehavior changed."
	if documentFingerprint(left) == documentFingerprint(right) {
		t.Fatal("technical content change did not change the fingerprint")
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
