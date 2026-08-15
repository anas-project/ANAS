# ANAS, Module, and container releases

ANAS has two release paths. Core binaries are released from the dedicated `anas-release` branch with an automatically
incremented or explicitly selected SemVer. Module
bundles, derived container images, and pinned upstream mirrors are released together from
`image-release`. The [Chinese release guide](../../developer/release.md) is the normative, detailed
runbook.

## ANAS Core

The first automatic `.github/workflows/anas-release.yml` publication is `0.1.0`. Later automatic
releases use the latest stable `vMAJOR.MINOR.PATCH` tag as the version source of truth and increment
patch by default. `scripts/ci/anas-release-version.sh` owns this calculation and has an isolated
fixture test.

Only the `anas-release` branch triggers an automatic Core publication. A push changing
`cmd/anas/`, `internal/`, `go.mod`, or `go.sum` starts the workflow. Neither a `master` push nor the
Module/container workflow triggers a Core release, so the two release paths advance independently.
Every run pins the triggering commit SHA, so a later branch update cannot change a queued build.

Development lands on `master`. Promote the selected, tested `master` commit to `anas-release` by PR
or fast-forward when Core is ready to publish:

```bash
git fetch origin
git switch anas-release
git merge --ff-only origin/master
git push origin anas-release
```

Create `anas-release` once from a verified `master` commit. It is a release gate rather than a
development branch and must not be synchronized back into `master`. If `master` contains commits
that are not ready, merge an explicit candidate into `anas-release` by PR instead of advancing it to
all of `master`.

An automatic run succeeds without publishing when the latest Core tag already identifies the
target commit or no Core input changed since the previous stable Core tag. Runs are serialized and
an immutable tag can never be replaced.

For explicit control, manually dispatch `ANAS release` from `anas-release`. Leave `version` empty to use
the selected `patch`, `minor`, or `major` increment, or provide an exact SemVer. Stable versions
cannot move backwards and a tag that identifies another commit cannot be reused:

```bash
# First release resolves to 0.1.0 when no Core tag exists.
gh workflow run anas-release.yml --ref anas-release -f version= -f bump=patch -f prerelease=false

# Request an exact release.
gh workflow run anas-release.yml --ref anas-release -f version=0.2.0 -f bump=patch -f prerelease=false
```

The publishing run executes `go test ./...`, cross-compiles static Linux binaries for `amd64` and
`arm64`, creates two `tar.gz` archives plus `SHA256SUMS`, and publishes an immutable GitHub Release.

Release builds expose their identity through:

```bash
anas version
anas version --json
```

Core archives do not embed Modules; Modules have independent versions and OCI identities.

## Module and container transaction

`.github/workflows/container-images.yml` handles the complete release transaction. The catalogs are:

- `.github/modules.json`: every first-party Module artifact repository, supported hook platform, and
  shared build context;
- `.github/images.json`: ANAS-derived image builds;
- `.github/mirrors.json`: unchanged upstream images pinned by digest.

A pull request targeting `image-release` calculates candidate revisions and builds validation
artifacts without publishing. A push to `image-release` commits generated metadata, publishes to
GHCR and CNB, verifies both sides, creates successful-release tags, and fast-forwards the released
commit to `master` only after every required artifact succeeds.

## Release identity and context

Each Module release is `<version>-r<revision>`. A version change resets revision to `1`; a change to
the Module runtime directory, its declared shared contexts, its modules/images/mirrors catalog
entries, or the established packager implementation increments the revision once. Documentation,
localization documentation, tests, caches, and local build residue do not increment it.

Preview or verify the result locally:

```bash
bash scripts/ci/module-revisions.sh --base <successful-release-sha> --print
bash scripts/ci/module-revisions.sh --base <successful-release-sha> --check
bash scripts/ci/module-revisions-test.sh
```

Successful publication records:

```text
image-release/<run>-<attempt>
module-release/<run>-<attempt>
module/<name>/<version>-r<revision>
```

## Module bundles

One reproducible bundle contains `package.yml`, `module.yml`, Compose, runtime/build files,
providers/assets, runtime Contract schemas, hook source, and precompiled Linux `amd64` and `arm64`
hooks. It excludes README, localization documentation, tests, caches, and host build residue.

```bash
go run ./cmd/package-module \
  --module nextcloud \
  --platform all \
  --output dist/nextcloud.tar.gz
```

Artifacts are published without `latest`:

```text
ghcr.io/anas-project/anas-module-<name>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/anas-module-<name>:<version>-r<revision>
```

Both registries must expose the expected artifact/layer media types and the same manifest digest.
The discovery catalog keeps an immutable `sha-<release-commit>` snapshot and a mutable `stable`
pointer; historical versions remain authoritative in each Module repository's standard tag list.

## Container linkage

When an owned image build context changes, CI builds the new image and copies it from GHCR to CNB.
When only Module metadata, Compose, a hook, or shared hook input changed, CI reuses the previous fixed
image manifest under the new Module revision tag instead of rebuilding identical content. Module
packaging begins only after every referenced fixed image exists in both registries.

Pinned upstream mirrors use `anas-mirror-<software>:<fixed-version>`. They are validated against the
catalog digest, copied unchanged, and restored from either registry when only one side exists.

## Source profiles

```yaml
module_source: official       # GHCR primary, CNB fallback
# module_source: official-cn  # CNB primary, GHCR fallback
# module_source: cn           # alias for official-cn
```

`official-cn`/`cn` automatically persists `global.chinese_speedup: true` when the user did not set
it. Explicit `false` wins, allowing CNB Module retrieval without changing runtime image/download
defaults. The CLI lists Registry tags, installs verified bundles into the content-addressed cache,
and records OCI/content digests in the lock. Local Module roots remain development overrides.

## First publication

Create `image-release` once from `master`, then dispatch **Module and container artifacts** from that
ref with `module=all`. Later releases merge the intended `master` state into `image-release`.
Single-Module manual dispatches are repair operations and do not advance the global successful base.

Publishing requires repository/package write permission, `CNB_REGISTRY_TOKEN` for CNB artifacts, and
`CNB_TOKEN` for successful Git ref synchronization. The workflow never force-pushes.
