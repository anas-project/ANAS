# Container image releases

Runtime images are published to GHCR and CNB by [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml). `.github/images.json` registers ANAS-derived builds; `.github/mirrors.json` registers unchanged upstream images pinned by digest.

## End-to-end synchronization

GitHub is the source repository, GHCR is the primary container publication registry, and CNB provides both a mainland China code mirror and container distribution. Code synchronization and container publication are separate GitHub Actions workflows. The same GitHub push may trigger both, but neither workflow depends on the other.

```text
Code:           GitHub ──> CNB Git repository

Derived image:  Dockerfile ──> GHCR ──> CNB Registry
Upstream image: pinned digest ──> GHCR ──> CNB Registry
Recovery:                       CNB Registry ──> GHCR
```

### Code repository

`.github/workflows/cnb-sync.yml` runs for pushes and deletions of any GitHub branch or tag, and can also be dispatched manually. It fetches every GitHub branch and tag and pushes them with `--prune` to `https://cnb.cool/anas.dev/ANAS.git`, so deletions on GitHub are propagated to CNB. Synchronization is always from GitHub to CNB; CNB is not the source of truth for code.

After code reaches CNB `master`, `.cnb.yml` validates image metadata and Compose references. It does not rebuild or republish containers.

### Container images

`.github/workflows/container-images.yml` handles container publication:

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

```text
ghcr.io/anas-project/anas-<software>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/anas-<software>:<version>-r<revision>
```

Unmodified upstream images use `anas-mirror-<software>:<fixed-version>`. The catalog records the upstream reference, manifest digest, and runtime platforms. Even when an upstream only publishes `latest`, the source is digest-pinned and the ANAS target never uses `latest`.

`ANAS_IMAGE_REGISTRY` selects the source for every runtime image. `global.chinese_speedup: true` defaults it to CNB, so a deployment host only needs `docker.cnb.cool`; `DOCKER_HUB_REGISTRY`, `GHCR_REGISTRY`, and `QUAY_REGISTRY` remain build-time base-image controls.

## CI behavior

- Pull requests build affected images without pushing.
- `master` processes only modules whose registered build contexts changed.
- Mirrors validate the upstream digest, select the `linux/amd64` and `linux/arm64` runtime manifests, and publish them unchanged as `anas-mirror-*`.
- If neither registry has the tag, CI builds once, publishes GHCR, and copies runtime platform manifests to CNB.
- If only one side exists, CI restores the missing side.
- Existing fixed tags are verified, never replaced.
- Images target `linux/amd64` and `linux/arm64`; GHCR retains provenance and SBOM.

For the first release, manually run the `Container images` workflow with `module=all`. This publishes both derived images and every upstream mirror. Make GHCR packages publicly readable and verify anonymous pulls from both registries.

The CNB `master` page also provides a **Mirror all Cask images from GHCR** button. Defined by `.cnb/web_trigger.yml` and `.cnb.yml`, it runs `scripts/ci/cnb-container-images.sh mirror-all` and copies fixed-version images that exist in GHCR but are missing from CNB. It neither builds images nor overwrites tags already present in CNB.

Publishing needs `CNB_REGISTRY_TOKEN`; Git mirroring to CNB uses `CNB_TOKEN`.
