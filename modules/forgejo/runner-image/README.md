# Forgejo one-job Runner guest image

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
