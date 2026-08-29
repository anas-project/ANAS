---
doc_type: standard
status: current
created: 2026-08-18
updated: 2026-08-28
---

# Changelog standard

This standard defines where ANAS Core and Module changelogs live, how entries are written, when they must be written, and how releases process them. Goals, hard constraints, and acceptance criteria are in the [changelog requirements](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/changelog.md) (Chinese); the delivery order is in the [changelog implementation plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/changelog.md) (Chinese).

The mechanism described here is **not implemented yet** and is not executable today; the plan document tracks progress.

## 1. Source of truth

The changelog is part of the documentation, not a separate intermediate format. The source of truth is a set of Markdown files named after release identities: unreleased content goes in a fixed `master.md`, and the release pipeline renames it to the release identity and recreates an empty `master.md`.

**The filename is the release identity, and the rename completes version attribution.** No tag-range algorithm is needed, so the differing cadences of the Core and Module release lines add no complexity here — each line renames its own files.

## 2. Location: the two release lines differ

```text
docs/changelog/
  master.md      master.en.md      unreleased
  v0.1.2.md      v0.1.2.en.md      released, read-only

modules/<name>/changelog/
  master.md      master.en.md
  34.0.2-r7.md   34.0.2-r7.en.md
```

The two directories are read through **two entirely different retrieval paths** and cannot be swapped:

| | Core | Module |
| --- | --- | --- |
| Location | `docs/changelog/` | `modules/<name>/changelog/` |
| Release branch | `anas-release` | `image-release` |
| How the site reads it | Release builds run `git archive <coreTag> -- docs`, so the whole `docs/` tree comes from the Core tag | `materialize-module-docs` runs `git show <module-tag>:modules/<name>/changelog/...`, reading each Module tag separately |
| Coupled to the Core tag | Yes | No |

This difference has a useful side effect: the Core side needs no extra release-mode logic. In release mode `docs/` comes from the Core tag, whose `master.md` is exactly the empty file recreated by the rename, so the published site naturally shows no unreleased content. In development and pull-request builds `docs/` comes from the working tree, so accumulated entries display normally.

Module filenames must use the full release identity `version-rrevision`. Using only the upstream version would let successive ANAS revisions of the same upstream version overwrite each other.

## 3. Entry format

`master.md` has the same structure as a released file; only the heading is fixed to `Unreleased`:

```markdown
# Unreleased

### Fixed

- Clarify errors returned when the domain password policy rejects a password. (#123)

### Changed

- Preserve structured error codes when a Module update fails.

### Breaking changes and migration

- `identity.password_policy` is renamed to `identity.password.policy`. Deployments that
  set this key by hand must rename it before upgrading, or startup reports an unknown
  configuration key.
```

| Item | Rule |
| --- | --- |
| Sections | `Added`, `Changed`, `Fixed`, `Deprecated`, `Removed`, `Security`, `Breaking changes and migration`; omit empty sections |
| Order | Append within a section; never sort or merge similar entries |
| Wording | Complete user-facing sentences describing behavior change, not implementation |
| Bilingual | `master.md` and `master.en.md` must change in the same commit |
| Breaking | Must also state, under `Breaking changes and migration`, what to do before and after upgrading |
| Traceability | An entry may end with `(#123)` referencing an issue or pull request |
| Security | `Security` entries may carry a CVE or advisory link; leave it out until public disclosure, then fill it in |

Upstream application versions are not written into entries. The generator reads the upstream version for a Module release page from `modules/<name>/module.yml` at that tag.

## 4. What to record

Record only what users, administrators, deployers, or Module developers can perceive: new capabilities, behavior changes, bug fixes; changes to configuration, CLI, API, contracts, or data formats; upgrade compatibility, migration steps, deprecations, and removals; security fixes; upstream application upgrades and ANAS integration changes for a Module.

Do not record by default: pure refactoring, added tests, formatting, CI adjustments, documentation fixes with no behavior change, and Modules repackaged only because a shared context changed (see §8). Record them when they themselves affect releasing, installing, or compatibility.

## 5. Timing: summarize the branch when it merges into master

Individual commits are not required to carry entries. A branch may write them at any point during development, but **the one mandatory moment is when the branch merges back into `master`**: whoever performs the merge reads through the user-visible changes the branch introduces, summarizes them into `master.md` and `master.en.md`, and lands them with the merge commit.

Two reasons for this moment:

- **The information is still fresh.** The person merging just finished the branch and knows why it changed, who it breaks, and whether migration is needed. That is exactly what Git history cannot express and what is most easily lost when reconstructing from diffs at release time.
- **It produces no conflicts.** A branch never modifies `master.md`; only merge commits do. When the second branch merges, the file has already been changed by the first branch's merge, but because the second branch made no change to it, Git reports no conflict — the merger simply appends. Writing an entry per commit would instead conflict on every merge, which at this repository's rate of 9–21 branch merges into `master` per week means a dozen or more pointless resolutions weekly.

### 5.1 Where entries go

| Change affects | File |
| --- | --- |
| ANAS Core behavior | `docs/changelog/master.md` + `.en.md` |
| A single Module | `modules/<name>/changelog/master.md` + `.en.md` |
| Several Modules | One entry per Module; identical wording is fine |
| Both Core and a Module | One entry on each side, each describing that side's impact |

There is no cross-component aggregate target, which would otherwise show readers an entry that does not belong to the Module page they are on. A shared contract change that genuinely affects several Modules is expanded to the affected set reported by `scripts/ci/module-revisions.sh --print`.

### 5.2 Tidy up before the rename

Before renaming, read through the entries accumulated this round: merge duplicate descriptions, make wording consistent, and delete entries that were changed and then reverted. **This pass is part of the standard, not optional** — entries summarized independently by separate branches are not guaranteed to be coherent, especially when one feature was delivered across several branches.

## 6. Release: the rename happens in CI

```text
Core     docs/changelog/master.md      -> docs/changelog/v0.1.2.md
         docs/changelog/master.en.md   -> docs/changelog/v0.1.2.en.md
         recreate empty files containing only # Unreleased

Module   modules/<n>/changelog/master.md -> modules/<n>/changelog/34.0.2-r7.md
         only for Modules that actually receive a new tag in this batch
         recreate empty files containing only # Unreleased
```

The rename **must be performed by the release pipeline**, because the target filename does not exist before the release branch is pushed:

```text
Module   revision is computed by module-revisions.sh --write in prepare
         before pushing, nobody knows whether this is r7 or r8
Core     the version is computed by anas-release-version.sh from the bump rule
         before pushing, nobody knows whether this is v0.1.2 or v0.2.0
```

Renaming locally means predicting the version; guessing wrong means rolling back an immutable release that was already published. So the flow is: write only `master.md` locally, let CI derive the release identity, rename, commit, and push back to `master`. The rename commit must be contained in the commit the tag points at, so that `git show <tag>:<changelog-path>` always returns that version's text.

Renamed version files are read-only afterwards. Two exceptions: filling in a CVE or advisory link after public disclosure, and clearly noted errata (wording mistakes, dead links) that must not change the description of released behavior.

## 7. Pushing back to master is load-bearing

The rename is destructive: once `master.md` has been renamed and that rename fails to reach `master`, the `master.md` on `master` still holds already-released entries, and the next release republishes them.

The existing push-back path **does fail in practice**. The `finalize` sync in `container-images.yml` is fast-forward only:

```text
release_head != RELEASE_SHA        skip; the queued next release will synchronize
master is not an ancestor of       fail and exit
RELEASE_SHA                        "master advanced independently; merge master into
                                    image-release before synchronizing it back"
```

At 9–21 branch merges into `master` per week, `master` advancing during the window it takes to build multi-architecture images for every catalog Module is routine. The `image-release` branch already contains a `Merge branch 'master' into image-release`, which is evidence that this recovery path has been used. A failed sync is tolerable for revision recalculation — it is recomputed next time — but not for the rename.

Therefore:

- **The Core side must also push back to `master`.** `anas-release.yml` currently does not push at all; it needs the same two-stage ancestor check used by `sync_master` in `container-images.yml`.
- **Self-heal idempotently at the start of each release run.** Check whether the `master.md` on `master` contains entries that already appear in a released version file; if so, a previous sync was lost, so apply the missing rename to `master` before continuing. This is safer than reordering `finalize` to sync before tagging, which would instead produce the reverse inconsistency of a version file with no corresponding tag.
- **Detect duplicate publication as a backstop.** Abort the release when the `master.md` about to be renamed shares entries with any released version file.

## 8. Gates

`master` has no branch protection, roughly half of all changes are merged locally rather than through pull requests, and Git hooks are not distributed with the repository. "Write a changelog entry when you merge" therefore cannot be blocked mechanically at merge time; `AGENTS.md` and `CONTRIBUTING.md` constrain the writer, and CI backstops where it can.

| Level | Trigger | Check | On failure |
| --- | --- | --- | --- |
| Coverage | Pull requests and release workflows | For every merge in the range that introduces release-relevant changes, did it also change the matching `master.md`? | Report on pull requests; abort on release |
| Completeness | Release workflows | Every component receiving a new tag has a non-empty entry set or is classified as a repackage; the rename happened; the new `master.md` is empty | Abort the release; create no tags |

Coverage is judged per **merge commit**, so local merges and pull-request merges are treated alike: take `git diff <first-parent>...<second-parent>` for the merge to obtain the paths the branch introduced, then decide whether an entry is required.

Four exemptions, sharing the same change context used for revision calculation:

- The branch only touches `shared_contexts`, the packager, or catalog entries — classified automatically as a repackage.
- The branch only changes tests, formatting, CI, generated artifacts, or documentation with no behavior change — follows the existing `runtime_path()` ignore rules.
- Synchronizing merges whose second parent is already an ancestor of `master` introduce no new work and are skipped.
- Dependabot branches fall under the first rule automatically and need no extra configuration.

The affected set is taken directly from `scripts/ci/module-revisions.sh --print` rather than a second, approximate set of path rules, so "needs a revision bump" and "needs a changelog entry" are always the same set. No new workflow is required on the pull-request side: `docs.yml` already runs on every pull request, so the coverage check belongs in its existing "Validate documentation sources" step.

## 9. Prerequisite exclusions

`modules/<name>/changelog/` sits inside the Module directory and falls under two existing rules. Both must be excluded first, or the model deadlocks:

| Location | Consequence of not changing it |
| --- | --- |
| `runtime_path()` in `scripts/ci/module-revisions.sh` | Writing the changelog itself triggers `revision + 1` → a new tag → another changelog entry, without end |
| `excludedRuntimeDirectory()` in `internal/modulepackage` | The changelog enters the Module OCI artifact and its digest, so every entry changes the artifact identity |

Both should exclude the directory names `docs` and `changelog`.

## 10. Documentation site

Generated pages are written into the disposable VitePress source tree; the real pages in the repository are never modified:

```text
/reference/changelog                        Core, Chinese
/en/reference/changelog                     Core, English
/reference/modules/<name>/changelog         Module, Chinese
/en/reference/modules/<name>/changelog      Module, English
```

Three constraints:

- **Every registered Module must get a page**, with an explicit placeholder when it has no entries. `materialize-module-docs` runs first and the changelog materializer second, so the "changelog" link the former emits depends on whether the latter produced a page. A missing page is a dead link, and `ignoreDeadLinks` is `false` for the current version, so the build fails outright.
- **Link to the Module root** (`/reference/modules/<name>/changelog`), never appended to `pageBase` in `renderVersionNavigation` — on snapshot pages `pageBase` carries the release segment, which yields a dead link.
- **Keep a separate section per revision of the same upstream version.** Existing Module documentation merges pages by body fingerprint; the changelog must not reuse that deduplication.

The generator must escape Vue interpolation inside entry text. VitePress compiles Markdown as a Vue template: fenced code blocks carry `v-pre` by default, but double braces in inline code and prose are evaluated as expressions. The two failure modes differ:

```text
${{ github.event.pull_request.base.sha }}
  syntactically valid JavaScript -> throws TypeError during SSR rendering,
  yet vitepress build still prints build complete and npm run docs:build exits 0

{{ '{{' }}
  syntactically invalid -> vite:vue reports Error parsing JavaScript expression
  at compile time and the build exits non-zero
```

The first is the dangerous one: CI silently accepts a page that failed to render. Documentation CI must therefore inspect the build log rather than trusting the exit code alone.

## 11. Related documents

- [Changelog requirements](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/changelog.md) (Chinese): goals, hard constraints, requirement matrix
- [Changelog implementation plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/changelog.md) (Chinese): milestones and remaining work
- [ANAS, Module and container releases](/en/developer/release): changelog steps in the release procedure
- [Documentation standard](/en/developer/documentation-standard): bilingual and directory rules
