# Forgejo one-job Runner VM image

This directory is the reproducible guest-side input for the M3 Incus VM image. Build an Ubuntu/Debian VM image on the independent Incus host, copy these files and a pinned Forgejo Runner binary into it, then run:

```sh
sudo ./provision.sh ./forgejo-runner-linux-amd64 <published-sha256>
```

Publish the resulting Incus image, record its 64-character fingerprint as `forgejo.actions_runner_image`, and never use an alias such as `latest`. Repeat the build per architecture.

The image starts only a rootless Podman API. It does **not** start a persistent Forgejo Runner. For a waiting job, the controller creates an ephemeral registration and VM, then invokes `anas-forgejo-runner-start` through the Incus agent. The helper reads the 40-character token from stdin into guest tmpfs and executes:

```text
forgejo-runner -c /etc/forgejo-runner/config.yml one-job --url ... --uuid ... --token-url file:///run/... --label ... --handle ... --wait
```

`runner-agent` owns the token and Runner process; `runner-engine` owns the rootless Podman daemon. Jobs may control only that disposable VM's rootless engine. The image exposes no `host` label, privileged container, arbitrary Runner volume, inbound listener, ANAS mount, or host socket. Network egress and project quotas are enforced by the restricted Incus profile/project, not by image metadata.

The repository assets and unit tests do not replace the external image build and real Incus/KVM isolation E2E. Those remain release gates in `dev-docs/plans/forgejo-module.md`.
