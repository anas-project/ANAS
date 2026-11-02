# Forgejo one-job Runner guest image

The accepted amd64 pending-publication bundle contains the byte-identical
`native-20261031-r2` container image and `native-20261101-vm-r4` VM image. Both
have real workflow and repo/org container-build evidence; ARM64 remains deferred.
The delivery manifest, native baselines and checksums are recorded in the paired
[implementation plan](../dev-docs/plans/forgejo-module.md).
These are pending-publication artifacts, not an uploaded registry or GitHub release.
The tooling verifies immutable bytes and history; it does not create a catalog
cryptographic signature. Stage the accepted `images/` tree into the matching
Incus Module packaging source before using `package-module`, and verify the
unpacked Module payload and image descriptors. Keep the release archive and
trusted provenance separately. Do not add catalog entries without their bytes.

The Runner process connects to the guest-only rootless engine at
`unix:///run/anas-podman/podman.sock`. Forgejo Runner maps that socket into each job
container at `/var/run/docker.sock`, so `runner.envs.DOCKER_HOST` uses
`unix:///var/run/docker.sock`. These paths differ intentionally. This exposes only
the disposable one-job guest engine, never the ANAS host Docker socket. Recipe
changes require a fresh immutable revision and a real container-build workflow;
a successful shell workflow cannot validate the job-side engine endpoint.

For mainland package downloads, set `CHINESE_BUILD_SPEEDUP=true` when running the Incus image
release build script, or pass `--chinese-build-speedup` to `incus-image-artifacts recipe`.
This freezes the fixed Aliyun Debian bootstrap URL and the pre-package APT mirror transform in the
recipe. Manual `provision.sh` uses the same `configure-build-mirrors` script when the build switch is
`true`. Existing suites, components and signing keys are preserved. Arbitrary `APT_MIRROR_URL` is not
accepted by these guest build paths. Changing the mirror requires a new revision; runtime
`CHINESE_SPEEDUP` does not rebake a published image. Native mirror downloads/baking remain unverified.

The image now carries an executable-scoped guest AppArmor policy for `/usr/bin/podman`.
Ubuntu's unprivileged-userns mediation can reject an unprofiled process inside the guest's
AppArmor namespace even though the outer Incus nesting policy permits user namespaces.
The fixed policy grants this program `userns` while preserving the previously unconfined
inner baseline; it does not remove the enforced outer Incus policy or change a host sysctl.
The ordinary guest `unshare` program must not acquire this permission.

An official `apparmor` parser package and a fixed conditional oneshot load the public policy
before the engine user manager. The policy stays outside the generic autoload directory;
kernels without this mediation retain their existing behavior. On a mediated kernel, a
loader failure prevents the engine from starting. Recipe changes require a new immutable
revision and native acceptance, including automatic loading, the negative generic-userns
probe and actual rootless OCI execution. Independent diagnosis and current results are in
[dev-docs/reviews/2026-09-24-forgejo-runner-userns-policy.md](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-24-forgejo-runner-userns-policy.md).

Installing the parser package also enabled a distribution autoloader and its
permissive `unprivileged_userns` fallback. The first newly baked policy image
failed the generic-userns refusal gate; it is not accepted for deployment.
`anas-apparmor-loader.conf` now constrains the packaged service's boot **and reload**
commands to the same fixed Podman policy. The AppArmor service stays enabled and
the package's no-unload stop behavior remains. No kernel setting, outer Incus
confinement, package file or existing published image is changed. Stock profile
files remain installed but are not implicitly loaded as a new grant source.
This loader change requires its own immutable revision and complete native run.

The new `policy-loader-r1` revision has now passed an actual Ubuntu 26.04 amd64 build/export/reuse,
all ten native image gates and the five real workflow scenarios. The original JSONL also passes the
stricter one-run/one-pass evidence validator. Ordinary userns denial, automatic fixed boot/reload
policy, rootless OCI limits, normal VM shutdown and the physical-host baseline were verified without
manual guest policy or trust changes. The source/export digests and scope are recorded in
`dev-docs/reviews/2026-09-25-forgejo-fixed-policy-loader.md`; no production catalog was signed or modified.

The new `trust-r2` revision passed an actual Debian 13 amd64 bake/export/reuse, nine native image/engine
pass events and all five workflow cases (normal, deliberate failure, SIGTERM cancellation, retained-state
SIGKILL recovery, and an unapproved repository kept waiting). The production stdin trust path was used
without manual guest CA installation. Source fingerprints, normal VM exit and physical-host preservation
are recorded in [dev-docs/reviews/2026-09-24-forgejo-runner-trust-projection.md](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-24-forgejo-runner-trust-projection.md). This is not a signed
catalog release, complete business deployment or trust provisioning inside arbitrary workflow containers.

The fixed starter supports deployment public CA projection after the 40-byte stdin token. Only the
validated public length and SHA-256 appear in arguments. Its companion `anas-forgejo-runner-input`
must be baked into the same immutable revision: it runs unprivileged, checks complete input and writes
only exclusive 0600 files below the private `/run/anas-actions-token` directory. System public roots and
the supplied CA are combined for this one-job's `SSL_CERT_FILE`, never installed in the guest system
store, engine or workflow image. The token and temporary bundle are removed on exit. The readiness
gate still runs before token consumption. An old revision rejects the new flags; do not patch its rootfs.

The separate workflow fixture now uses the production controller's stdin projection rather than
installing its public CA into a new guest. A historical `lab-r11` workflow pass is not evidence that
this changed recipe has been baked or booted. Native results are recorded by recipe and fingerprint.

The API service and socket run in **runner-engine's systemd user manager**, using the systemd cgroup
manager. Only that account enables the socket; no persistent Runner daemon is enabled. The fixed
`anas-engine-user.conf` drop-in for `user@1002.service` establishes the inherited read-only filesystem
and private temporary directory. The agent sees only the group-restricted API socket, not the engine's
private runtime or user bus. These three unit inputs must be kept with the recipe.

The earlier system-service/cgroupfs candidate is not accepted: it allowed exec but ignored effective
per-container limits. The immutable gate now executes a real digest-pinned OCI container and requires
effective 0.5 CPU, 128 MiB, 32 PID and no-new-privileges=1. Configuration fields and podman info alone
cannot establish this. Candidate-specific real workflow results are recorded in the
[closeout review](../../../dev-docs/reviews/2026-09-22-incus-onejob-closeout.md).

Public configuration and entrypoint directories are explicitly root-owned 0755 in both the generated
recipe and the provisioning input. A private build umask must not make `/etc/forgejo-runner` unreadable
to `runner-agent`. The native image gate checks this as the real account. This does not relax private
home/token permissions and requires a new image revision rather than editing recorded artifacts.

The current recipe fixes the guest root to 0755 without changing private archive modes. It creates
runner-engine with actions-engine as its primary group, matching the service identity required by
newuidmap. The API listener is provided by `anas-podman.socket` with mode 0660 and the shared group;
the service inherits that FD rather than binding a 0600 socket. Fixed runtime directories come from
`anas-podman.conf`, with the engine's XDG directory remaining 0700. Both systemd units and tmpfiles
data are reviewed image inputs and must be included when generating or provisioning the image.

This directory is the guest-side input mirrored into the audited default
distrobuilder recipe for the Forgejo ephemeral runner image. Generate the recipe
per architecture and isolation tier from the release tool:

```sh
incus-image-artifacts recipe \
  --image forgejo-runner \
  --architecture amd64 \
  --interface incus_container > forgejo-runner-amd64-container.yml
```

The release builder must stage a pinned `forgejo-runner` binary as the recipe's
release-owned `forgejo-runner` file, run `incus-image-artifacts build` on an
isolated native Linux builder, publish the resulting catalog entry, and keep the
artifact archive. Repeat for `amd64`/`arm64` and `incus_container`/`incus_vm`.

VM package sets are selected directly for the target, without redundant raw
architecture filters that can be skipped after Debian architecture mapping.
VM recipes explicitly install Debian-signed GRUB and shim, then populate both
`EFI/BOOT` (the firmware fallback path) and `EFI/debian` in the post-files phase.
The GRUB root argument uses distrobuilder's image root UUID; no build-host device
path or firmware NVRAM entry is retained. Installing EFI packages alone leaves
the partition without a loader and cannot boot the exported image. These packages
are required only by the VM tier because the compute client retains Secure Boot.
An existing artifact is immutable: regenerate the recipe and build a new revision
after this change. Container recipe bytes remain unchanged.

The Debian/debootstrap recipe also requires the distribution-provided
`debian-archive-keyring` on the **isolated build machine**, including when that
machine runs Ubuntu. Without its fixed root-owned keyring, debootstrap can warn
and continue without checking the Release signature; HTTPS and package hashes
from the same download do not replace that authentication. The artifact CLI now
rejects a missing/unsafe keyring, disabled verification or ambiguous verification
fields before reserving a build attempt. It does not install host packages or
accept a caller-selected keyring. Other reviewed downloaders retain their own
verification obligations; the release tool is not a sandbox for root recipes.
The fixed `.gpg` pathname may be the official package's exact relative alias to
`debian-archive-keyring.pgp` in the same verified directory. That regular target
still needs the required owner, non-writable mode and single-link identity;
arbitrary/absolute/chained links are rejected rather than followed.

The builder-output guard separately remembers debootstrap's explicit missing-
signature warning. A later successful packing phase or exit 0 cannot clear it
or authorize recording the output. This guard retains only a closed failure
reason, not raw builder logs or paths. Failed attempts and older artifacts are
not automatically rebuilt or retrospectively declared verified.

`BuildOnce` places that executable at `sources/forgejo-runner` relative to the
frozen recipe directory. The default recipe's copy generator must use that
relative path: distrobuilder's `--sources-dir` is a distribution-download setting,
not an implicit search directory for copy-generator inputs. The September 22
native distrobuilder 3.2 regression reproduces the old bare-path failure and
checks the corrected packed bytes. This changes recipe bytes and requires a new
revision; do not replace a published revision or reuse an incomplete attempt.

The recipe explicitly includes `systemd-sysv` and installs a guest-boot tmpfiles
rule linking `/etc/resolv.conf` to systemd-resolved. It must not replace that
path with `ln -sf` inside distrobuilder's post-files chroot. The native resolver
regression covers the old hook failure and the packed rule's actual application.

The native generator regression is not a complete Debian/Podman image bake.
`test-env/fixtures/incus-runner-image/README.md` describes the separate baked-image
Provider-import, boot, Runner-interface and rootless-engine smoke gate. A real
Forgejo one-job execution remains a further acceptance step.

Set `forgejo.actions_runner_image` to a structured catalog reference or an
explicit `{fingerprint: "<64hex>"}` object. Never use an alias such as `latest`
and never rebuild the same revision to replace missing bytes; restore the
identical recorded artifact or publish a new revision.

The image is configured to start only a rootless Podman API. It does **not** start a persistent Forgejo Runner. For a waiting job, the controller creates an ephemeral registration and guest, then invokes `anas-forgejo-runner-start` through Incus exec. The helper reads the 40-character token from stdin into guest tmpfs and executes:

```text
forgejo-runner -c /etc/forgejo-runner/config.yml one-job --url ... --uuid ... --token-url file:///run/... --label ... --handle ... --wait
```

`runner-agent` owns the token and Runner process; `runner-engine` owns the rootless Podman daemon. Jobs may control only that disposable VM's rootless engine. The image exposes no `host` label, privileged container, arbitrary Runner volume, inbound listener, ANAS mount, or host socket. Network egress and project quotas are enforced by the restricted Incus profile/project, not by image metadata.

The repository assets and unit tests do not replace the external image build,
Incus import, guest boot and real Incus/KVM isolation E2E. Those remain release
gates in `modules/forgejo/dev-docs/plans/forgejo-module.md`.

The September 22 `lab-r4` candidate completed a real bake, Provider import and
restricted-container boot. Runner CLI checks passed, but the rootless Podman API
check exited 125 and the overall image gate failed. Its immutable bytes are
retained; no signed production release or real one-job success is claimed.

The current source now gates token consumption on a successful read-only Podman
info as `runner-agent`, with a clean environment and the fixed guest socket.
The engine must report rootless=true. Up to eight two-second probes, each with
one second of termination grace, are separated by seven one-second sleeps.
Failure exits 69 before token directory creation or stdin consumption; it never
restarts the engine or relaxes the Incus lease. An already-active one-job still
does not consume another token. Host-side stdin delivery is not guest consumption.

The Go format expression uses adjacent shell literals so distrobuilder cannot
interpret its delimiters while embedding the script. Behavioral tests exercise
the original shell with test-only commands and real timeout; recipe tests check
that the same bytes are embedded for all four targets. This source change needs
a new baked revision: it does not modify or claim to repair archived `lab-r4`.
