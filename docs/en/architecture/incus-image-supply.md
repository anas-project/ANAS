# Incus Image Supply

> Status: **Artifact supply is wired in code; real bakes, signed release distribution, guest boot and destructive prune remain unaccepted or incomplete.** Updated: 2026-09-19.

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

`incus-image-artifacts build` still runs distrobuilder only on an independent
release builder. Existing revisions are verified and reused; missing/corrupt
objects or recipe changes fail instead of rebuilding the same revision.
`record` accepts completed split artifacts, and `export` restores the recorded
bytes for one revision into a new private directory with `artifact.json`.
None of these commands connects to the target Incus daemon, serves downloads, or
inlines image bytes in JSON.

`scripts/ci/incus-image-release-build.sh` writes the release output in the
layout the Incus Provider bundle consumes directly: `images/catalog.json` plus
`images/artifacts/anas/forgejo-runner/<revision>/<architecture>/<interface>/...`.
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
and digest mismatches against the release record. If no local artifact exists,
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
