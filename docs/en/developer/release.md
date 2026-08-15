# Container image releases

Runtime images are published to GHCR and CNB from the dedicated `image-release` branch by [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml). Ordinary pushes to `master` do not publish images; only merges into `image-release` enter revision calculation and publication. `.github/images.json` registers ANAS-derived builds; `.github/mirrors.json` registers unchanged upstream images pinned by digest.

## End-to-end synchronization

GitHub is the source repository, GHCR is the primary container publication registry, and CNB provides both a mainland China code mirror and container distribution. Daily development lands on `master`. To publish images, merge the intended `master` state into `image-release`. The release workflow creates a revision commit, builds from that exact commit, and fast-forwards it back to `master` only after publication succeeds.

```text
Code: feature branch ──> master ──> image-release
                                      │
                                      ├──> generated revision commit
                                      ├──> build and verify images
                                      └──> fast-forward master on success

Derived image:  Dockerfile ──> GHCR ──> CNB Registry
Upstream image: pinned digest ──> GHCR ──> CNB Registry
Recovery:                       CNB Registry ──> GHCR
```

### Code repository

`.github/workflows/cnb-sync.yml` runs for pushes and deletions of any GitHub branch or tag, and can also be dispatched manually. It fetches every GitHub branch and tag and pushes them with `--prune` to `https://cnb.cool/anas.dev/ANAS.git`, so deletions on GitHub are propagated to CNB. Synchronization is always from GitHub to CNB; CNB is not the source of truth for code.

Under [GitHub's `GITHUB_TOKEN` triggering rules](https://docs.github.com/en/actions/concepts/security/github_token#when-github_token-triggers-workflow-runs), ordinary pushes made with the workflow's own token do not recursively create another Actions run. The successful release finalize job therefore uses `CNB_TOKEN` to push the corresponding `image-release`, `master`, and successful tag directly to CNB.

After code reaches CNB `master`, `.cnb.yml` validates image metadata and Compose references. It does not rebuild or republish containers.

### Container images

`.github/workflows/container-images.yml` handles container publication:

- pull requests targeting `image-release` calculate candidate revisions and build for validation without committing or pushing images;
- after a merge to `image-release`, the workflow calculates and commits revisions per Module;
- ANAS-derived images are built from registered Dockerfiles, pushed to GHCR first, and then have their runtime platform manifests copied to CNB;
- unchanged upstream images are validated against the digest pinned in `.github/mirrors.json`, copied to GHCR, and then copied to CNB;
- if neither registry has an immutable tag, the normal publication path runs; if only one registry has it, the missing side is restored from the existing side; if both have it, they are verified without replacement;
- the normal direction is GHCR to CNB. CNB to GHCR is only a recovery path when GHCR is missing a fixed tag that CNB still has.

Neither registry pushes to the other by itself. Cross-registry copies are performed by `scripts/ci/runtime-image.sh` with Crane from GitHub Actions.

## Release identity

Each module declares:

- `version`: normalized upstream SemVer;
- `revision`: the ANAS packaging revision for that upstream version, starting at `1`;
- `app_version`: the original upstream display version.

The immutable image tag is `<version>-r<revision>`. A build-context-only change increments `revision` by exactly one. An upstream `version` change resets it to `1`. Existing fixed tags are never overwritten, and `latest` is not published.

Normal releases do not require a manual revision command. The workflow finds the newest reachable successful `image-release/*` tag and uses its commit as the base for:

```bash
bash scripts/ci/module-revisions.sh --base "$LAST_SUCCESSFUL_RELEASE_SHA" --write
```

The script only manages ANAS-derived images registered in `.github/images.json`. It compares the registered build contexts independently for each Module. A change in any context since the last successful release increments that Module's base revision once; simultaneous changes to multiple contexts still increment it only once and do not affect other Modules. A new Module or upstream `version` uses revision `1`. `--write` synchronizes `module.yml`, `localization.yml` when present, and every derived-image tag in the Module's `docker-compose.yml`. Unmodified upstream mirrors do not have `-r<revision>` image tags and are outside this calculation.

`github-actions[bot]` commits generated metadata to `image-release`; downstream jobs explicitly check out that commit, and its SHA is stored in `org.opencontainers.image.revision`. After every image is verified in GHCR and CNB, the workflow creates an `image-release/<run>-<attempt>` tag. Only successful releases receive this tag, making it the stable base for the next calculation. A failed build creates no tag and does not update `master`; a rerun or later merge continues from the preceding successful tag.

For local diagnosis, the calculation can still be previewed or written explicitly, but this is not part of the normal release operation:

```bash
bash scripts/ci/module-revisions.sh --base <successful-release-sha> --print
bash scripts/ci/module-revisions.sh --base <successful-release-sha> --write
```

```text
ghcr.io/anas-project/anas-<software>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/anas-<software>:<version>-r<revision>
```

Unmodified upstream images use `anas-mirror-<software>:<fixed-version>`. The catalog records the upstream reference, manifest digest, and runtime platforms. Even when an upstream only publishes `latest`, the source is digest-pinned and the ANAS target never uses `latest`.

`ANAS_IMAGE_REGISTRY` selects the source for every runtime image. `global.chinese_speedup: true` defaults it to CNB, so a deployment host only needs `docker.cnb.cool`. Source builders separately enable `global.chinese_build_speedup: true` for Docker Hub, GHCR, and package-manager mirrors.

## CI behavior

- Ordinary pushes to `master` do not run container publication.
- Pull requests targeting `image-release` calculate candidate revisions and build affected images without committing or pushing.
- Pushes to `image-release` commit generated revisions and process Modules whose contexts changed since the last successful release.
- Before the first successful tag exists, all derived images and mirrors are processed; failed attempts continue to process every target.
- A change to any registered context of one Module increments that Module once and builds every image registered to it.
- Mirrors validate the upstream digest, select the `linux/amd64` and `linux/arm64` runtime manifests, and publish them unchanged as `anas-mirror-*`.
- If neither registry has the tag, CI builds once, publishes GHCR, and copies runtime platform manifests to CNB.
- If only one side exists, CI restores the missing side.
- Existing fixed tags are verified, never replaced.
- Images target `linux/amd64` and `linux/arm64`; GHCR retains provenance and SBOM.
- If `image-release` advances during a build, the completed commit still receives a successful tag, but the branch is never moved backward. The queued release continues from that tag and performs the eventual `master` synchronization.
- If `master` advanced independently and cannot be fast-forwarded, synchronization stops until the latest `master` is merged into `image-release`; history is never force-pushed.

## First release and recovery

Create `image-release` once from the current `master`:

```bash
git fetch origin
git switch -c image-release origin/master
git push origin image-release
```

For the first publication, dispatch `Container images` from the `image-release` ref with `module=all`, or merge a later `master` change into that branch. The first success publishes all derived images and upstream mirrors and creates the initial successful tag. Later releases only require merging the intended `master` state into `image-release`; revision generation, commit, build, tagging, and post-success `master` synchronization are automatic.

A manual dispatch must select the `image-release` ref. `module=all` can complete a failed release and advance the successful tag and `master`. Selecting one Module is only for republishing or Registry recovery and does not advance the global successful tag or synchronize `master`.

The CNB `master` page also provides a **Mirror all Cask images from GHCR** button. Defined by `.cnb/web_trigger.yml` and `.cnb.yml`, it runs `scripts/ci/cnb-container-images.sh mirror-all` and copies fixed-version images that exist in GHCR but are missing from CNB. It neither builds images nor overwrites tags already present in CNB.

Repository Actions must have read/write workflow permissions. Force pushes to `image-release` must be disabled, while the [ruleset bypass list](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository#granting-bypass-permissions-for-your-branch-or-tag-ruleset) must allow the GitHub Actions App to commit generated revisions. Rules for `master` must allow that App to perform the workflow's safe fast-forward. If the repository cannot grant this bypass, replace the checkout/push credential with a dedicated GitHub App installation token carrying equivalent rights; the workflow never force-pushes. Publishing needs `CNB_REGISTRY_TOKEN`; successful releases use `CNB_TOKEN` to synchronize `image-release`, `master`, and the successful tag to CNB Git.
