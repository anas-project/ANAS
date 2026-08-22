# compute Contract technical notes

## Lifecycle

`compute` 1.x defines `create`, `inspect`, `start`, `exec_stdin`, `stop`, `delete`, and `list_managed` for
one-shot virtual machines. Callers supply a stable instance identity, a pinned image fingerprint, and numeric
limits. The provider owns profiles, networks, storage pools, credentials, and project policy; callers cannot
inject arbitrary Incus devices, raw configuration, mounts, or host sockets.

`exec_stdin` is the only operation that may carry a one-time secret into a guest. The secret is a non-replayable
stream and must not enter metadata, argv, cloud-init, images, persistent state, or logs. The guest may only write
it to tmpfs.

## Incus provider boundary

The first interface is `incus_vm`. It connects to a separate KVM host and is fixed to the restricted
`anas-forgejo-runners` project. It verifies project quotas before mutation, only manages VM names beginning with
`anas-`, and cannot enumerate or delete instances in another project.

The first implementation targets Forgejo one-job runners. A real Incus/KVM host, network egress policy, and
project-scoped credential are still required for end-to-end acceptance.
