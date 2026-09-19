# Forgejo one-job Runner VM image

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

Set `forgejo.actions_runner_image` to a structured catalog reference or an
explicit `{fingerprint: "<64hex>"}` object. Never use an alias such as `latest`
and never rebuild the same revision to replace missing bytes; restore the
identical recorded artifact or publish a new revision.

The image starts only a rootless Podman API. It does **not** start a persistent Forgejo Runner. For a waiting job, the controller creates an ephemeral registration and VM, then invokes `anas-forgejo-runner-start` through the Incus agent. The helper reads the 40-character token from stdin into guest tmpfs and executes:

```text
forgejo-runner -c /etc/forgejo-runner/config.yml one-job --url ... --uuid ... --token-url file:///run/... --label ... --handle ... --wait
```

`runner-agent` owns the token and Runner process; `runner-engine` owns the rootless Podman daemon. Jobs may control only that disposable VM's rootless engine. The image exposes no `host` label, privileged container, arbitrary Runner volume, inbound listener, ANAS mount, or host socket. Network egress and project quotas are enforced by the restricted Incus profile/project, not by image metadata.

The repository assets and unit tests do not replace the external image build,
Incus import, guest boot and real Incus/KVM isolation E2E. Those remain release
gates in `modules/forgejo/dev-docs/plans/forgejo-module.md`.
