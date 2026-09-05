# Incus compute provider technical notes

This document records the provider implementation and security boundary of the `incus` module.
Configuration and operation are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `7.3.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Module, capability and contract dependencies

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `compute` | Provided contract | `1.0.0` / `incus_vm` |
| `compute` | Provided contract | `1.0.0` / `incus_container` |

Both interfaces share one executor and one validation path, diverging only in a single container
privilege restriction on the project configuration.

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_incus_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-incus-provisioner:7.3.0-r1` | `incus` | 0 |
<!-- generated:compose-topology:end -->

There is a single run-only service. It has no `ports`, no Traefik label, no volumes, and mounts no host
socket: this module is a client control plane for a remote daemon, and nothing inside the container
should be reachable. The Runner starts it once with `docker compose run --rm --no-deps --no-TTY`, and
it ends when the process exits.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | `""` | `static` | `INCUS_ADMIN_CERTIFICATE_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Provisioning-only administrative client certificate, never handed to a consumer |
| `incus.admin_key_b64` | string | — | `""` | `static` | `INCUS_ADMIN_KEY_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Private key for the administrative certificate |
| `incus.endpoint` | string | `pattern: ^(?:https://[A-Za-z0-9.:_-]+)?$` | `""` | `static` | `INCUS_ENDPOINT` | no | yes | no | yes | `reconcile` | HTTPS address of the remote Incus daemon |
| `incus.server_certificate_b64` | string | — | `""` | `static` | `INCUS_SERVER_CERTIFICATE_B64` | no | yes | yes | yes | `reconcile` | Pinned daemon server certificate; a mismatch fails outright with no fallback |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | `default` | `static` | `INCUS_STORAGE_POOL` | no | no | no | yes | `reconcile` | Storage pool backing every lease root disk |

All four reach the run-only container through `.env`; the three credentials travel as base64 PEM and the hook checks their type early in apply.

## Contract resource lifecycle

The order inside `ensure` is deliberate:

1. `GET /1.0/projects/{sandbox}`. If it exists, merge the desired configuration into the existing one
   and `PUT`; otherwise `POST` a new project. Merging rather than overwriting matters because the
   project may hold running instances and operator-added `user.*` keys.
2. **Read back** and assert `restricted=true` with all four limits non-empty. Any failure returns an
   error and does **not** go on to register the certificate. This step is the contract's only source of
   trust: a successful write does not count, only the daemon's own copy does.
3. Create the managed network and the lease profile, then read the profile back and verify it. This
   comes before the certificate: handing out a credential for a lease that has no root disk and no NIC
   yet would achieve nothing.
4. Register the consumer certificate. If that fingerprint is already trusted, verify it is restricted
   and that `projects` contains exactly this sandbox; if it is unrestricted or bound elsewhere, exit
   with an error and change nothing.

## Network and profile

The network name is derived from the first 10 hex characters of the sandbox name's SHA-256 (`anas` plus
10 hex = 14 characters). The sandbox name cannot be used directly: a Linux bridge interface is capped
at 15 characters and `anas-forgejo-runners` is 20. Deriving keeps it short, stable, and free of
collisions between leases.

The profile is fixed as `anas-lease` and carries exactly two devices:

| Device | Contents |
| --- | --- |
| `root` | `type=disk`, `path=/`, `pool=<storage_pool>`, no `source` |
| `eth0` | `type=nic`, `network=<derived bridge name>`, no `parent`/`nictype` |

The network's IPv6 follows the host. The hook sets `INCUS_NETWORK_IPV6=true` only when the IPv6 switch
is not off **and** `HOST_HAS_IPV6=true`; the bridge then gets `ipv6.address=auto` with `ipv6.nat=true`.
Otherwise it is written explicitly as `ipv6.address=none`. Writing `none` rather than leaving it unset
is deliberate: an unset value lets the daemon apply its own default, and handing a guest a v6 address
the host cannot route makes every outbound connection wait for a timeout before falling back to v4,
which reads as a hung job rather than a misconfiguration. Both families are NATed through the same
managed bridge, so enabling v6 widens what a guest can reach without changing how it gets out.

`ensureProfile` does a whole-object PUT rather than a merge, and `verifyProfile` then reads it back and
requires exactly two devices. Together they are the only enforcement point for this constraint: the
daemon does not stop anyone attaching devices to a profile, so "no extra devices on the profile" holds
only because this code checks.

The two refusals in step 3 are the provider-side privilege-escalation defence: silently accepting a
certificate that is already trusted with global rights would hand the consumer the whole daemon.

`inspect` is read-only and reports `exists`, `ready`, `restricted` and `quota_enforced` separately. A
missing project returns zero values rather than an error, because "absent" is a normal observable
state.

`revoke` deletes the consumer certificate and keeps the project. Deleting the project would destroy the
instances inside it, and those instances were never owned by this contract. Deleting a fingerprint that
does not exist succeeds idempotently.

## Quota mapping

The contract states per-instance limits while an Incus project states project-wide totals.
`projectConfig` reconciles them by multiplying through `max_instances`:

| Contract | Incus project key | Value |
| --- | --- | --- |
| `quota.max_instances` | `limits.instances` | as given |
| `quota.cpu` | `limits.cpu` | `max_instances × cpu` |
| `quota.memory_mib` | `limits.memory` | `max_instances × memory_mib` MiB |
| `quota.disk_gib` | `limits.disk` | `max_instances × disk_gib` GiB |

Fixed restrictions are written alongside: `restricted.devices.disk|gpu|pci|usb|unix-block|unix-char=block`,
`restricted.containers.nesting=block`, and `restricted.{containers,virtual-machines}.lowlevel=block`.
`incus_container` additionally writes `restricted.containers.privilege=unprivileged` — the system
container tier is a weaker **isolation** boundary than a VM, never a weaker **privilege** boundary.

## Certificate pinning

`newClient` combines `InsecureSkipVerify: true` with a `VerifyPeerCertificate` that performs an **exact
DER comparison**. This looks dangerous and is in fact stricter than chain verification: an Incus daemon
uses a self-signed certificate whose SAN rarely matches the address an operator configures, so chain
verification would either fail on a correct deployment or have to be relaxed into accepting any
certificate. DER equality accepts only the certificate pinned at apply time, and no branch in the code
continues on a mismatch.

The same `tls.Config` carries the administrative provisioning certificate, so request identity lives at
the TLS layer rather than in a header, and no error path can echo the credential.

## Sensitive value boundary

The four credentials enter the container environment as base64 PEM through `.env`, and the hook
verifies during `calculate` that each decodes to a PEM block of the right type. Every failure message
names only the offending parameter and never echoes its value; `decodeBase64Env` and `validatePEM` both
have tests holding that line.

The consumer's **private key never passes through this module**: the Runner supplies only the
certificate, for registration, and projects the key straight to the consumer.

## Hook, changes and rollback

The hook implements `calculate` only: it derives `INCUS_NETWORK_NAME` and refuses when any of the four
credentials is incomplete. The refusal happens early in apply rather than midway through provisioning,
because a half-configured provider is harder to diagnose than one that never started.

Changes to `endpoint` and `server_certificate_b64` are `reconcile`; the two administrative credentials
are `credential_rotate`, and rotating them does not affect running instances.

## Tests and implementation locations

| Location | Contents |
| --- | --- |
| `provisioner/client.go` | REST envelope parsing, certificate pinning, not-found normalization |
| `provisioner/ops.go` | `ensure`/`inspect`/`revoke` and quota mapping |
| `provisioner/main.go` | argument and environment validation, isolation tier dispatch |
| `provisioner/provisioner_test.go` | fake daemon covering idempotence, fail-closed paths, over-scoped certificate refusal, pin mismatch, input validation and non-echo of sensitive values |
| `hook/main_test.go` | credential completeness and non-echo |

The fake daemon is an `httptest.NewTLSServer`, so the pinning logic runs through a real TLS handshake
rather than a stub.

## Current limitations

The unit-level boundaries are complete, but none of them has been verified against a real Incus/KVM
host: whether the daemon truly enforces the project and quota, whether a restricted certificate can
escape its project, whether two parallel consumers stay invisible to each other, and how leftover
instances are reclaimed after the provider is interrupted. Only end-to-end testing answers those, and
the `developing` status records exactly that gap.

`ANAS_RESOURCE_IMAGE_ALLOWLIST` is currently format-checked on the provider side and handed to the
consumer, so image pinning is ultimately enforced in the consumer's shared client rather than by the
daemon. **It is the one constraint in this design the daemon does not backstop** — the one that stops
holding if the consumer is compromised.

"Backstopped by the daemon" means the constraint lives on the project or the certificate, is enforced
by the daemon, and survives consumer code failing completely. This module's scorecard:

| Constraint | Enforced by | Holds if consumer is compromised |
| --- | --- | --- |
| access confined to its own project | daemon (restricted certificate) | yes |
| quota | daemon (project limits) | yes |
| no devices / mounts / raw config / lowlevel | daemon (`restricted.*`) | yes |
| unprivileged containers | daemon (`restricted.containers.privilege`) | yes |
| image fingerprint allowlist | consumer's shared client | **no** |

Even then, a compromised consumer can only boot an unplanned image inside **its own restricted,
quotaed, device-less** project, so every other constraint still bounds the blast radius.

> [!NOTE]
> The claim that an Incus project has no native image-allowlist key has not been checked against the
> Incus 7.3 project configuration reference. If an equivalent key exists upstream, this constraint
> should move onto the project and this table should be updated.
