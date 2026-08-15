# ANAS, Module, and container releases

ANAS has two release paths. Core binaries are released manually from `master` with a SemVer. Module
bundles, derived container images, and pinned upstream mirrors are released together from
`image-release`. The [Chinese release guide](../../developer/release.md) is the normative, detailed
runbook.

## ANAS Core

Dispatch `.github/workflows/anas-release.yml` from `master` with a version such as `0.4.0`. The
workflow rejects an existing immutable tag, runs `go test ./...`, builds static Linux `amd64` and
`arm64` binaries, writes `SHA256SUMS`, and creates GitHub Release `v0.4.0`.

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
