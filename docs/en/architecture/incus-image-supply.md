# Incus Image Supply

> Status: **Archive, complete bundle export and artifact supply are wired in code. A laboratory candidate completed a real bake, import and container boot, but its rootless engine gate failed. Signed releases, one-job execution, other architectures/tiers and destructive prune remain unaccepted.** Updated: 2026-09-22.

This document records the current ANAS Incus guest image supply boundary. The
deployment consumes fingerprints already frozen in `image_allowlist`; the
Provider does not rebuild during apply, resolve aliases, or return fingerprints
to the Runner through a result channel.

## Release Side

`incus-image-artifacts recipe --image forgejo-runner --architecture amd64|arm64 --interface incus_container|incus_vm`
prints the audited default distrobuilder recipe for the Forgejo one-job runner.
The recipe fixes `image.architecture` per target. VM targets additionally enable
the Incus agent and a 10 GiB ext4 VM target. ANAS-owned guest helpers, the
systemd unit, and runner config are embedded in the recipe; the release pipeline
supplies only an independently pinned `forgejo-runner` binary. Consumers cannot
provide script paths, URLs, aliases, or root hooks.

The recipe command accepts `--chinese-build-speedup` to freeze the Aliyun Debian bootstrap URL and
rewrite Debian main/security URLs in `.list`/`.sources` files during `post-unpack`, before package
installation, preserving signature configuration, suites and components. This ordering follows
[distrobuilder's package workflow](https://github.com/lxc/distrobuilder/blob/main/distrobuilder/main_incus.go).
The release script passes this option when `CHINESE_BUILD_SPEEDUP=true`; manual guest `provision.sh`
uses the same mirror script. The default remains off. Arbitrary `APT_MIRROR_URL` values and runtime
`CHINESE_SPEEDUP` do not select this build policy. Mirror selection enters recipe bytes and the digest,
so changing it requires a new revision and never causes rebaking during apply. Native mirror downloads
and guest baking were not validated for this change.

`incus-image-artifacts build` still runs distrobuilder only on an independent
release builder. Existing revisions are verified and reused; missing/corrupt
objects or recipe changes fail instead of rebuilding the same revision.
`record` accepts completed split artifacts, and `export` restores the recorded
bytes for one revision into a new private directory with `artifact.json`.
None of these commands connects to the target Incus daemon, serves downloads, or
inlines image bytes in JSON.

`incus-image-artifacts bundle --archive DIR --previous-catalog FILE --output-dir NEW_DIRECTORY`
verifies history and exports every committed split revision, architecture and isolation target under
one archive lock, writing `catalog.json` last. An actual first release must explicitly use
`--first-release` instead; the history options are mutually exclusive. The destination must not exist
and its parent must already be prepared. Empty archives, unified artifacts, missing history and corrupt
objects fail without implicit repair or rebuilding. Writes use opened directory handles and recheck the
size and SHA-256 of the bytes actually copied; a replaced destination cannot redirect later writes or
be reported as successful. Failed private candidates need explicit inspection and cannot be adopted on retry.

`scripts/ci/incus-image-release-build.sh` now uses this entrypoint to produce the
layout the Incus Provider bundle consumes directly: `images/catalog.json` plus
`images/artifacts/<catalog>/<name>/<revision>/<architecture>/<interface>/...`.
The script checks explicit history, a fresh output directory and existing archive history before
building, then exports every committed record. It no longer omits historical bytes by exporting only
the current two targets, or truncates a catalog through shell redirection. CLI stdout contains only
the image count and catalog digest, not image payloads.
That directory is release input. Deployments must still receive it through an
explicitly installed release artifact or equivalent trusted release medium; the
Provider must not generate it during apply, and an artifact descriptor's
fingerprint is not a first-trust source.

## Deployment Side

Before running compute Provider `ensure`, the Runner only combines frozen
deployment `compute_images` with release artifacts from the Provider bundle into
Provider supply JSON. It does not re-read the latest catalog and does not learn
trust from an artifact descriptor. When a matching artifact exists, the Runner
mounts the supply JSON and staged artifact bytes read-only into
`anas-incus-provisioner`, then sets:

```text
ANAS_RESOURCE_IMAGE_SUPPLY_FILE=/run/anas/compute-image-supply.json
```

Copying is not a trust source: while opening descriptors and artifact bytes, the
Runner rejects symlinks, special files, writable files, replacement, size drift,
and digest mismatches against the release record. Metadata must match the complete frozen
reference, target, fingerprint and recipe before files are read. The split-only supply path rejects
unified records instead of indexing a nonexistent part. Multiple runtimes or named revisions may
share the same bytes: every reference is validated before physical copies are deduplicated, without
changing deployment images or bindings. The supply descriptor has a shared 1 MiB limit. Hashing and
copying use the apply cancellation context: pre-canceled operations do not create staging, and
in-progress cancellation removes the partial copy and releases its temporary supply directory.
If no local artifact exists,
no supply mount is added; the Provider still fails closed when the target daemon
lacks the frozen fingerprint.

## Provider Behavior

For each frozen allowlist fingerprint, the Provider:

1. reads image metadata in the target project and checks fingerprint,
   architecture, and isolation;
2. when absent, locates a split artifact with the same fingerprint,
   architecture, and interface in the supply JSON;
3. verifies bytes with `computeimage.VerifySuppliedImage` against the frozen
   `Resolution`, never trusting the descriptor fingerprint by itself;
4. streams `incus.tar.xz` and `rootfs.squashfs`/`disk.qcow2` through multipart
   import without base64;
5. accepts Incus async operations only for image import, validates the operation
   UUID/path, and waits with bounded polling;
6. reads image metadata again after completion and verifies architecture and
   isolation.

The existing Incus API helper remains synchronous-only; `202 Accepted` is not
treated as completion.
The Provider reads supply descriptors and file parts from fixed roots, rejecting
writable directories, symlinked ancestor directories, special files,
replacement, oversized content, and byte changes during import. Errors do not
include supply file content, certificates, private keys, or runner tokens.

Before granting certificate trust, `ensure` reads back every managed project setting and the exact
four requested quota values, not just nonempty strings. Profile configuration and device properties
must exactly match the managed template. A final read-only check covers the complete lease.
`inspect.ready` requires the current project fence, admitted pool, network ownership/NAT, profile,
independent restricted certificate and every frozen image. Missing dependencies or revoked trust
cannot remain ready; `exists`, `restricted` and `quota_enforced` remain separate observations.
Inspection does not import images, read supply files, repair configuration or grant trust. These
metadata checks do not constitute live daemon isolation or quota-enforcement acceptance.

## Prune

`incus.image-prune.plan` and `incus.image-prune` are wired through authenticated shared jobs,
one-time confirmation and the host executor. Clients cannot submit arbitrary deletion sets.
The root executor plans from installed workspace registrations, frozen deployments and Incus
inventory. It retains current/previous deployments, other workspaces, instance/snapshot base
images and unknown external images. Deletion candidates require compute consumers to be
already stopped; the operation does not pause workloads implicitly.

Apply recomputes state and plan digests under locks, rechecks references before each approved
deletion, persists an intent, deletes and verifies absence before recording a receipt. Ordinary
apply, ensure and import do not prune. Code and fixture coverage do not establish real
Linux/Incus destructive-deletion acceptance.


The interface consumes the actual public job DTO (`kind`, `mutating`, workspace/id and result),
not internal `job.action`. The displayed plan must match its approval binding's schema,
workspace, plan/state digests and deletion set. A valid empty inventory displays no changes and
cannot execute. Changed input, plan expiry or disposal cannot reuse prior consent. Confirmation
tokens are not exposed in public state, and uncertain apply results are never retried automatically.
Missing public fields or inconsistent bindings fail closed rather than relying on type assertions.

## Acceptance Boundary

Local unit tests prove byte verification, archive restore, Runner-to-Provider
read-only mount wiring, Provider control flow, and dry-run planning. They do
not prove real distrobuilder bootability, Forgejo runner guest equivalence,
actual Incus multipart import, VM/KVM or system-container isolation, or
destructive prune safety.

## September 22 build-diagnostic and resolver continuation

The release tool retains only fixed last-observed stage labels from complete output lines bounded to
4 KiB. Raw lines, paths, URLs and commands are never returned or persisted; overlong and partial records
are discarded. The archive preserves only the closed diagnostic type, not arbitrary wrapped errors.
These labels are observations, not proof of completion or authority to retry an incomplete revision.

The default Runner recipe now installs `systemd-sysv` explicitly and writes a guest tmpfiles rule for
`/etc/resolv.conf`, instead of replacing that path inside distrobuilder's `post-files` chroot. The native
synthetic-rootfs test verifies the old hook failure, actual packed rule and link creation by tmpfiles.
It is not a full Runner image, guest DNS or one-job acceptance test. See the
[Chinese design](/architecture/incus-image-supply).

The `lab-r4 / amd64 / incus_container` candidate completed a real distrobuilder bake, immutable archive,
repeat-build reuse, actual Provider multipart import and restricted-container boot. Runner 13.2.0 and
its one-job CLI checks passed, but the rootless Podman API subtest exited 125, failing the overall image
gate. No real workflow, signed production release or other architecture/tier acceptance is claimed.
See the [recovery record](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-22-incus-runner-build-recovery.md).
