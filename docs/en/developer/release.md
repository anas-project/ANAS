# Container image releases

Runtime images are published to GHCR and CNB by [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml). `.github/images.json` registers ANAS-derived builds; `.github/mirrors.json` registers unchanged upstream images pinned by digest.

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

For the first release, manually run the `Container images` workflow with `module=all`. This publishes both derived images and every upstream mirror. Make GHCR packages publicly readable and verify anonymous pulls from both registries. Publishing needs `CNB_REGISTRY_TOKEN`; Git mirroring to CNB uses `CNB_TOKEN`.
