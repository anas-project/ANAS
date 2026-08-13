# Container image releases

ANAS-built images are produced by [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml) and published to GHCR and CNB. `.github/images.json` is the registry of Dockerfiles and release images.

## Release identity

Each module declares:

- `version`: normalized upstream SemVer;
- `revision`: the ANAS packaging revision for that upstream version, starting at `1`;
- `app_version`: the original upstream display version.

The immutable image tag is `<version>-r<revision>`. A build-context-only change increments `revision` by exactly one. An upstream `version` change resets it to `1`. Existing fixed tags are never overwritten, and `latest` is not published.

```text
ghcr.io/anas-project/anas-<image>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/<image>:<version>-r<revision>
```

`ANAS_IMAGE_REGISTRY` selects the ANAS image source. `global.chinese_speedup: true` defaults it to CNB; `GHCR_REGISTRY` remains specific to third-party GHCR images.

## CI behavior

- Pull requests build affected images without pushing.
- `master` processes only modules whose registered build contexts changed.
- If neither registry has the tag, CI builds once, publishes GHCR, and copies runtime platform manifests to CNB.
- If only one side exists, CI restores the missing side.
- Existing fixed tags are verified, never replaced.
- Images target `linux/amd64` and `linux/arm64`; GHCR retains provenance and SBOM.

For the first release, manually run the `Container images` workflow with `module=all`, make GHCR packages publicly readable, and verify anonymous pulls from both registries. Publishing needs `CNB_REGISTRY_TOKEN`; Git mirroring to CNB uses `CNB_TOKEN`.
