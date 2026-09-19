# Incus compute provider technical notes

This document records the provider implementation and security boundary of the `incus` module.
Configuration and operation are in the [English README](../README.en.md).

2026-09-18 verification: local Go regressions pass for the Provider, Hook, artifact archive/build
orchestration, HTTP policy and publication transactions. Earlier dated "unrun" notes describe their
original snapshots, not new production authority. The test machine is macOS arm64: Linux-specific
identity checks, real distrobuilder bakes, Docker/Incus, quotas and ingress still require native acceptance.
The Module remains `developing` and production ingress remains disabled. See the
[verification record](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-18-incus-implementation-verification.md)
for the exact scope and outstanding work.

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

## Local builds and shared source in staging

Compose sets `build.additional_contexts.shared` to
`${ANAS_SHARED_BUILD_CONTEXT:-../..}`. The default `../..` assumes the repository
layout of `modules/incus/docker-compose.yml`; it does not discover the source root.
After copying the module into deployment staging, do not assume the relative path
still identifies ANAS source.

For a local staging build, explicitly set `ANAS_SHARED_BUILD_CONTEXT` to the absolute
path of a trusted ANAS source checkout matching the module's build-input version.
It must contain at least `go.mod`, `go.sum`, and `internal/computeclient`, not just
`modules/incus`, its `provisioner` directory, or a workspace containing runtime data.
Do not mix a frozen module's source with a shared package from another version.

The following path is an example. First enter the selected module's Compose directory
and prepare its `provisioner/` build directory and the `.env` referenced by Compose.
This example was not executed in this work; it is not evidence of a successful staging build:

```sh
ANAS_SHARED_BUILD_CONTEXT=/srv/src/ANAS \
  docker compose build anas_incus_provision
```

The variable selects a build context only; it does not change the daemon endpoint,
consumer credentials, or runtime privileges. The Dockerfile copies the shared package
through the named `shared` context and retains `GOPROXY=off` for the Go build. This does
not make the entire Docker build offline: base images and distribution packages must
still be available locally or obtainable from their configured sources.
Runtime artifacts without the corresponding source are insufficient for a local build.
Use the matching prebuilt image or prepare complete trusted source first. Actual builds
from both a source checkout and deployment staging still require separate acceptance.

## Fixed control relay component (not installed or accepted)

`modules/incus/control-relay` is a Linux non-root transport component for the proposed host-loopback
connection path. **It is not a current Compose service and has not been packaged, automatically
installed or enabled.** It forwards bytes from the installation-bound control-bridge IPv4/high port
only to the compiled-in `127.0.0.1:8443`. There is no upstream override, HTTP CONNECT, SOCKS, TLS key
or caller-supplied command. The original endpoints retain Incus mTLS and server-certificate pinning.

The component requires root-owned configuration and ancestors without group/other write access, a
dedicated non-root UID/GID, no extra supplementary groups and no capabilities. Configuration binds
interface name/index, subnet and gateway; interface drift stops the service rather than rebinding a
wildcard. Connection count, dial time and shared bidirectional idle time are bounded. Half-closes are
preserved, and shutdown closes existing connections. These are future host-installation fields, not
the `incus.*` Module settings below; existing remote-daemon connections are unchanged.

Host installation actions, trusted binary distribution, service units, INPUT/FORWARD ingress-interface
rules, interface-recreation reconciliation, endpoint projection, and real mTLS/pin/project/cross-network
and uninstall acceptance remain pending. A source-CIDR check is neither firewall authorization nor
authentication. Locally runnable configuration/transport tests pass; Linux identity checks have only
been cross-compiled. Production ingress remains blocked; the host-provisioning design, section 3.8,
records the detailed boundary.

## Configuration contract

### Host preflight (2026-09-19, standalone diagnostic)

Run `go run ./cmd/incus-host-preflight` from the source checkout for read-only local inspection.
`--recipes` prints the compiled distribution table; `--skip` avoids OS-file inspection entirely.
Container isolation is the default; choose VM explicitly with `--interface incus_vm`. There are no
install, script, arbitrary-path, package-source or endpoint options. The tool does not contact an Incus
socket that could activate a stopped daemon, and is not the production `anas host` or Web API.

`internal/incushost` matches exact Debian 13 / Ubuntu 24.04 / Ubuntu 26.04 release IDs, versions and
architectures. `ID_LIKE` does not authorize derivatives. The Linux reader verifies root-owned
non-writable ancestors and file identity around bounded reads, accepting only the documented
os-release fallback/link forms. It never sources shell. Official package metadata has been checked,
but package signatures/origins, service units, retries, uninstall and runtime compatibility remain due.

Every preflight keeps `compute_ready: false` and `runtime_verified: false`, distinguishing unsupported
systems, explicit skips and unfinished gates. A missing KVM device never silently downgrades a VM.
The internal read-only `incus.status` handler reuses this path with strict action input and execution
audit; the root socket, full daemon status, shared job/CLI/Web wiring and installation/removal are not
implemented. Pure-logic/audit tests passed on macOS; Linux peer/filesystem execution is separately
tracked. See [the host provisioning design](../../../docs/architecture/incus-host-provisioning.md),
section 2.1, for the official-package version difference and remaining acceptance limits.

### Host job binding (internal implementation)

The Module settings below do not expose private host-job broker inputs. `Activation.ServeBrokered`
and `HostJobBinding.ServeBroker` connect the internal read-only preflight through a fixed private Unix
endpoint, kernel-authenticated peers, frozen job/release data and permission checks before and after
execution. A completed handshake or closed socket cannot release a live executor's retained lease.
This transport adds no job store, root-readable user scripts/databases or installation/removal/ingress
write actions.

The private listener and `HostJobBroker` routing are implemented: only the fixed socket is created in
an installed private directory, without adopting stale entries. At most 32 running-job bindings and 8
connections are admitted; input cannot register jobs. Retirement needs terminal state and remote cleanup,
and listener shutdown does not release a job's execution lease. The latest slice adds an independent
systemd exit observer and shared-recorder finalization: pin the real unit/invocation before granting,
then check exit status and an empty unit process inventory. Missing evidence retains the lease and
the existing durable containment barrier. `anas-hostd` and candidate units are packaged with the same
release identity. Optional read-only HTTP admission and the same-daemon queue are wired, but non-root
TLS/state/permission migration, the installer and CLI invoke remain pending. The service option
`host_actions` defaults to false and is not compute readiness.
Actual systemd and exit-status acceptance has not run.
`bash test-env/scripts/test-host-job-broker-native.sh` runs isolated Linux socket/child
fixtures and requires key tests to execute rather than skip. It is not systemd, actual root-peer or
Incus acceptance, and does not cover the new native D-Bus/systemd path. See
[the host-channel design](../../../docs/architecture/host-action-channel.md), sections 11–12.

### Automatic host connection and control network

When all four sensitive connection settings are omitted, the Hook reads only the fixed root-owned
mode-0600 `/var/lib/anas/incus-host/connection.json`. It validates the schema, observed target
architecture, owned pool, subnet/gateway, certificate/key pair and management fingerprint before
projecting values into the existing Secret Store. Partial explicit settings, path overrides, links,
duplicate JSON and unsafe permissions are rejected. Complete explicit remote settings do not read
the local file. Automatic provenance and a binding digest persist with the Secret; restoring all four
environment values never bypasses revalidation, revocation or drift checks.

Automatic connections use `anas-btrfs` and the host-observed architecture. Remote architecture remains
explicit and a remote pool defaults to `default`. Ordinary calculate does not silently rotate an
existing automatic binding; a changed binding requires explicit recovery/reconciliation.

The Provider uses the externally owned `anas-incus-control` bridge. Per-resource
`CONTROL_NETWORK_NAME` / `CONTROL_NETWORK_EXTERNAL` projections connect Forgejo's two compute services
and the AI Agent orchestrator to the same bridge. Their business network keeps gateway priority 1;
this requires Compose 2.33.1+. Remote mode clears automatic host-network markers. Static configuration
validation does not replace container-origin reachability, default-route, mTLS, isolation or IPv6 tests.

The current host channel retains root/root anasd and unchanged TLS permissions. Plan/one-use approval/
apply, CLI/Web and the installer are wired in code; older non-root-migration and preflight-only notes
above describe prior slices. Hostd and the non-root relay are packaged together, but installation does
not enable the relay. Native systemd/Incus/KVM acceptance remains outstanding and production ingress
is disabled. See the current boundary in [host-action architecture](../../../docs/architecture/host-action-channel.md), section 13.

### Module parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | — | `host` | `INCUS_ADMIN_CERTIFICATE_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Provisioning-only administrative client certificate, never handed to a consumer |
| `incus.admin_key_b64` | string | — | — | `host` | `INCUS_ADMIN_KEY_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Private key for the administrative certificate |
| `incus.endpoint` | string | `pattern: ^https://[A-Za-z0-9.:_-]+$` | — | `host` | `INCUS_ENDPOINT` | no | yes | yes | yes | `reconcile` | HTTPS address of the remote Incus daemon |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | `host` | `INCUS_IMAGE_ARCHITECTURE` | no | yes | no | yes | `container_recreate` | Explicit guest image architecture on the target daemon; never inferred from the CLI host |
| `incus.server_certificate_b64` | string | — | — | `host` | `INCUS_SERVER_CERTIFICATE_B64` | no | yes | yes | yes | `reconcile` | Pinned daemon server certificate; a mismatch fails outright with no fallback |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | — | `runtime` | `INCUS_STORAGE_POOL` | no | yes | no | yes | `reconcile` | Storage pool backing every lease root disk; the hook keeps `default` for explicit remote daemons and uses `anas-btrfs` for the host bundle |

All four reach the run-only container through `.env`; the three credentials travel as base64 PEM and the hook checks their type early in apply.

All four connection settings are sensitive. Configuration parameters `server_certificate_b64` and
`admin_certificate_b64` first produce `INCUS_SERVER_CERTIFICATE_B64` and
`INCUS_ADMIN_CERTIFICATE_B64`. After validation, the Hook derives the Provider wire variables
`INCUS_SERVER_CERT_B64` and `INCUS_ADMIN_CERT_B64`. A stale raw-environment alias cannot replace
missing canonical input; sensitive-value propagation also covers the aliases. Consumer projections
`ENDPOINT`, `SERVER_CERT`, `CLIENT_CERT` and `CLIENT_KEY` are all sensitive, not only the private key.

When `endpoint`, `server_certificate_b64`, `admin_certificate_b64` and `admin_key_b64` are all explicit,
the hook uses the advanced remote path and does not read the host file. A partial explicit set fails
closed; explicit values are never mixed with automatic values. The explicit remote path still requires
`image_architecture`, and an empty `storage_pool` keeps the previous `default` behavior.

When all four connection settings are empty, the hook reads only the installed Linux fixed file
`/var/lib/anas/incus-host/connection.json`; no module setting, environment variable or user argument can
override that path. The file must be root-owned, `0600`, a single-link regular file under safe root-owned
ancestors, with stable identity before and after the read. Symlinks, FIFOs, writable ancestors,
oversized files, unknown fields, duplicate JSON fields and old schema payloads are rejected. The bundle
schema remains `anas.incus-connection-bundle/v1`, but automatic wiring now requires `architecture`
(`amd64`/`arm64`) and `storage_pool` (`anas-btrfs`); old bundles without those fields are not inferred.

The automatic bundle must pin `endpoint=https://<control_gateway>:18443`,
`control_network=anas-incus-control`, `relay_service=anas-incus-control-relay.service`, and the management
certificate/private-key pair plus `management_fingerprint`. Bundle values are projected to both hook Env
and the module Secret Store. The Secret Store also carries a module-private source marker and binding
digest. Repeated apply for an automatic binding re-reads the fixed file and requires the same digest; a
deleted, replaced or drifted bundle cannot silently fall back to historical secrets or rotate to new
automatic values. Explicit remote inputs are not overwritten by automatic values.

Both Hook and Provider reject endpoint user information, non-root paths, queries and fragments. The
Provider never follows HTTP redirects, limits responses to 4 MiB and requires synchronous success:
an asynchronous acknowledgment does not prove provisioning completed. Errors expose only trusted
operation/status categories, not endpoints, daemon error text or invalid metadata values. X.509 parser
errors are also replaced with a fixed category, preventing rejected SAN URIs from exposing certificate content.

## Contract resource lifecycle

The order inside `ensure` is deliberate:

1. Read `GET /1.0/storage-pools/{pool}` in the default project first. Require the requested name,
   status `Created`, and a `btrfs` or `zfs` driver admitted by this version; refuse before any lease
   mutation otherwise. Then `GET /1.0/projects/{sandbox}`. If it exists, merge the desired configuration into the existing one
   and `PUT`; otherwise `POST` a new project. Merging rather than overwriting matters because the
   project may hold running instances and operator-added `user.*` keys. An existing project with
   `features.networks=true` is refused: changing its network ownership requires explicit migration.
2. **Read back** and assert `restricted=true`, all four limits non-empty, storage admission read back again, network features disabled,
   managed NICs, and `restricted.networks.access` equal to exactly this lease's bridge. Any failure returns an
   error and does **not** go on to register the certificate. This step is the contract's only source of
   trust: a successful write does not count, only the daemon's own copy does.
3. Create and verify the bridge in the default project, then create and verify the profile in the lease project. This
   comes before the certificate: handing out a credential for a lease that has no root disk and no NIC
   yet would achieve nothing.
4. Read each frozen image in the target project and verify fingerprint, architecture and type.
   Missing or mismatched metadata fails; no alias lookup is used.
5. Register the consumer certificate. If that fingerprint is already trusted, verify it is restricted
   and that `projects` contains exactly this sandbox; if it is unrestricted or bound elsewhere, exit
   with an error and change nothing.

## Network and profile

The network name is derived from the first 10 hex characters of the sandbox name's SHA-256 (`anas` plus
10 hex = 14 characters). The sandbox name cannot be used directly: a Linux bridge interface is capped
at 15 characters and `anas-forgejo-runners` is 20. Deriving keeps it short and stable; ownership checks reject reuse if a truncated name collides.

The Provider owns the bridge and explicitly uses `project=default` in network API requests. Each lease
sets `features.networks=false`, `restricted.devices.nic=managed`, and an exact `restricted.networks.access`
allowlist. Incus 7.3 does not support bridges in non-default projects. Instances, profiles, certificate
scope, and quotas remain in the consumer's separate project.

Networks carry `user.anas.consumer` and `user.anas.sandbox`. A same-name network with missing or conflicting
ownership, an incompatible type, or external interfaces is refused without adoption or certificate
registration. Names alone do not establish ownership. Repeated applies preserve allocated subnets and
unrelated configuration; disabling IPv6 writes `none` and removes its old NAT setting. Network writes
are read back for type, ownership, addressing and NAT before a profile is created. Separate bridges
alone do not prove traffic isolation; cross-lease network attachment, write permissions, and actual
egress still require real-host acceptance.

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

The two refusals in step 5 are the provider-side privilege-escalation defence: silently accepting a
certificate that is already trusted with global rights would hand the consumer the whole daemon.

`inspect` requires the exact network scope above for `ready`, while retaining independent restricted
and quota flags. It is read-only and reports `exists`, `ready`, `restricted` and `quota_enforced` separately. A
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

Project `limits.disk` bounds declared allocation; it does not prove a root disk has a working quota.
A real daemon probe found that `dir` can warn and continue when filesystem project quotas are absent.
This provider therefore admits only `Created` btrfs/zfs pools. It refuses dir, including dir pools that
might have filesystem quotas configured, and other drivers until capability evidence is added. This
is a provider limitation, not a claim that upstream lacks those drivers. It neither probes host
filesystems nor converts pools or migrates existing instances.
`inspect` rereads pool admission: missing, unready or unsupported storage yields `quota_enforced=false`
and `ready=false`, while preserving project `exists`/`restricted`. Read errors remain errors. This
check does not replace a guest disk-fill test or monitor later administrator changes. Failed admission
does not automatically revoke existing certificates. See the [Incus dir quota prerequisites](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas)
and the pending test inventory at `test-env/fixtures/incus-network-prototype/e2e-plan.md` in the repository.

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

The hook implements `calculate` only: it derives `INCUS_NETWORK_NAME`, selects the explicit remote or
fixed host-bundle connection path, and refuses incomplete credentials, unsafe automatic bundles and
automatic-binding drift. The refusal happens early in apply rather than midway through provisioning,
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
| `hook/main_test.go` | credential completeness, host-bundle projection, idempotent binding, unsafe-file rejection and non-echo |

The fake daemon is an `httptest.NewTLSServer`, so the pinning logic runs through a real TLS handshake
rather than a stub.

## Current limitations

An isolated distribution Incus 6.0.5 daemon was used to probe project, bridge, profile, image metadata
and certificate provisioning, and exposed skipped dir disk quotas. This is not full 7.3.0 acceptance.
Guest startup has not passed; actual quotas, restricted-certificate isolation, concurrent consumers and
janitor recovery still require E2E. The status remains `developing`.

`ANAS_RESOURCE_IMAGE_ALLOWLIST` is checked against image metadata in the lease project before being handed to the
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
> The upstream main configuration reference was reviewed on 2026-09-10: image-server domain restrictions
> and project image isolation are not per-fingerprint allowlists. Real import/start enforcement on the
> pinned daemon version remains unverified; see the host-provisioning architecture for the evidence boundary.

### Source-checkout and staging build contexts

Shared builds use `additional_contexts.shared`. Its `../..` default only applies to a source checkout
that retains the `modules/incus` layout; it does not discover a source root from a deployment staging
tree. CI's `check-shared-build` checks Dockerfile COPY paths, path existence, and revision triggers,
not whether an actual image build succeeds.

For a local build from staging, explicitly set `ANAS_SHARED_BUILD_CONTEXT` to the **absolute path to
the complete repository source root matching the Module revision**, not the staging root, the
`modules` directory, or `internal/computeclient`. The build side must be able to read that path and
the shared source files referenced by the Dockerfile. For example:

```sh
export ANAS_SHARED_BUILD_CONTEXT=/srv/src/ANAS
# Replace with the rendered Incus Compose file and its corresponding .env.
export INCUS_COMPOSE_FILE=/srv/anas/staging/modules/incus/docker-compose.yml
docker compose -f "$INCUS_COMPOSE_FILE" config --quiet
docker compose -f "$INCUS_COMPOSE_FILE" build anas_incus_provision
```

Both paths are illustrative, not fixed Runner directories. Source-checkout builds may use the same
explicit override instead of relying on relative layout after copying files. Normal deployment
rendering still prepares runtime settings and sensitive parameters; do not place an administration
private key in public command arguments or commit it to source just to perform a build.

A published Module artifact without the complete matching source is not sufficient for a local
shared-context build. Use that revision's published image, or prepare the matching source first;
do not substitute an arbitrary or empty directory. The override neither downloads source nor
bypasses the shared packages' offline-build boundary. These instructions were checked against the
Compose source, but neither checkout nor staging image builds were executed in this iteration.
R-084 remains pending acceptance.

A staging static-check entry point was added on 2026-09-18 (code and tests have not been run).
From the matching source checkout, use
`go run ./cmd/check-shared-build --source-root "$ANAS_SHARED_BUILD_CONTEXT" --staging-root "$STAGING_ROOT"`.
`STAGING_ROOT` is the rendered deployment root containing `modules/`. An explicit absolute shared
context override is required. The check compares selected Compose builds, Dockerfiles, module build
sources and shared-input bytes. It does not load the deployment environment, invoke Compose/Docker
or download source. Missing modules, source drift, enumerated symbolic links and missing/extra shared
files do not count as a valid build. This is a static preflight, not evidence of successful Docker builds.

## Frozen compute image configuration

Image settings now use structured objects (a single object for Forgejo, a runtime-keyed map for AI Agent).
Core projects these through explicit `spec_from` modes and resolves them before Hook calculation. Runtime
containers receive only frozen fingerprints: Forgejo reads the lease allowlist, AI Agent reads JSON image
bindings. The Agent hook reads `AI_AGENT_AGENT_RUNTIMES`, matching the manifest parameter; Compose passes
it to the orchestrator as `AI_AGENT_RUNTIMES`. No new dependency is introduced.

Incus requires explicit `image_architecture` for the daemon target. Its trusted bundle catalog is currently
empty. Ensure checks each existing image's fingerprint, architecture and type in the lease project before
registering trust; missing images fail instead of resolving aliases or rebuilding. Import/baking remains
pending. See the [compute contract](../../../contracts/compute/docs/technical.en.md) for snapshot and rollback semantics.

The HTTP network prototype only generates lab artifacts (`cmd/incus-network-prototype`): a guest /32 route
with explicit source, veth-bound ingress filtering, an expiring address/port set, and existing Traefik route
environment fields. It does not install rules or enable production ingress. Docker/Incus rule ordering,
source spoofing, address reuse and long-connection revocation still require real Linux evidence; TCP/UDP
publishing is not implemented.

On 2026-09-11, real Linux namespace checks passed for HTTP transport, source IP/MAC spoof rejection,
established-flow revocation and expiry. These use synthetic peers; Docker/Incus rule coexistence, real
guests, managed IP reuse and full revocation remain unverified.

## Offline split-image artifact verification (local tests pass; native acceptance pending)

`internal/computeimage/artifact.go`, `release_verify.go` and `cmd/compute-image-artifact` reuse the
single `ArtifactRelease` representation for read-only inspection of completed image bakes. They do not run distrobuilder, import into Incus, download, sign or publish a
trusted catalog. The trusted catalog remains empty; the `guest_image` contract, first bake/distribution
and missing-image recovery are still pending.

The CLI supports split artifacts only; the shared library also describes unified artifacts. Under the [Incus image format](https://linuxcontainers.org/incus/docs/main/reference/image_format/),
the fingerprint hashes the actual **metadata bytes followed by the rootfs bytes**, not the concatenated
hexadecimal digests. The descriptor also records each part's SHA-256 and byte length, binding the
catalog/name/revision, architecture, isolation interface and recipe digest. Named verification in the
library checks the full version/target key and frozen recipe digest, requiring a catalog digest to be
present. The caller still supplies an authenticated frozen snapshot; the verifier does not validate
catalog signatures. These checks establish byte identity, not archive validity, bootability, quota
enforcement or security acceptance.

From the matching source checkout, inspect a completed bake to produce a **candidate descriptor**
(the following paths must be replaced; this command has not been run):

```sh
go run ./cmd/compute-image-artifact \
  --metadata /srv/images/example/incus.tar.xz \
  --rootfs /srv/images/example/disk.qcow2 \
  --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe-digest "$RECIPE_INPUTS_SHA256"
```

Standard output is canonical JSON with one trailing LF; the release process may save it as
`artifact.json`. The recipe digest must cover all pinned, trusted recipe inputs. The command does not
infer recipe trust from the image digest, an arbitrary YAML file or its download source. When checking
an existing revision, supply `--expected-fingerprint`: changed bytes fail instead of replacing its identity.
Publishing the candidate still requires the existing append-only catalog and release trust process;
successful inspection does not authorize publication.

Verification requires a separate fingerprint from the trusted catalog/frozen plan, never one copied
from the descriptor being checked:

```sh
go run ./cmd/compute-image-artifact \
  --verify /srv/images/example/artifact.json \
  --metadata /srv/images/example/incus.tar.xz \
  --rootfs /srv/images/example/disk.qcow2 \
  --architecture amd64 --interface incus_vm \
  --expected-fingerprint "$FROZEN_IMAGE_FINGERPRINT"
```

The CLI supports regular local files on Linux/macOS, rejecting final-component symbolic links,
special files and observable changes during the read. Limits are 16 KiB for the descriptor, 16 MiB for
metadata and 64 GiB for rootfs. Hashing uses 128 KiB chunks with cancellation checks; bulk bytes and
raw read errors do not enter output. Canonical decoding rejects duplicate/unknown fields, case aliases,
nulls, noncanonical encoding and trailing data. Local unit/CLI tests pass; this tool does
not constitute real-host acceptance for M12 or M13.

## Local image artifact archive (local tests pass; native acceptance pending)

`cmd/incus-image-artifacts` adds explicit local archive writes alongside the read-only verifier above.
It reuses the same `ArtifactRelease` representation and fingerprint algorithm, not a second image
protocol. It is a release-preparation tool, not an installer, Provider action, browser endpoint or
consumer API. It currently targets private local directories owned by the executing user on Linux/macOS.
Archive and CLI regressions pass locally; example paths do not identify published or bootable guest images.

The archive has a 0700 root and `objects/` and `releases/` subdirectories, a 0600 `.lock`, and 0400
`.format`, SHA-256-named objects and canonical revision records. A session holds an exclusive file
lock and checks directory/lock identities around operations. Objects are written to private temporary
files, synchronized and published without replacement before revision metadata is committed. Recording
an identical revision preserves its mapping; different bytes, format or recipe digest require an explicit
new revision instead of overwriting the original.

From the matching source checkout, initialize a **new local archive** for completed, trusted
distrobuilder outputs:

```sh
go run ./cmd/incus-image-artifacts init --archive "$ARCHIVE_DIR"
go run ./cmd/incus-image-artifacts record \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe "$PINNED_RECIPE_FILE" --format split \
  --metadata "$METADATA_FILE" --rootfs "$ROOTFS_FILE"
go run ./cmd/incus-image-artifacts inspect \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm
```

`init` requires a nonexistent directory; it does not adopt empty directories or repair a damaged
archive. `record` never runs a builder. `--recipe` reads the original bytes of a reviewed, self-contained
recipe, limited to 4 MiB; all external build inputs must be explicitly pinned in that recipe. Hashing a
recipe does not prove the build actually used it. The trusted release pipeline must supply provenance,
signatures and publication authorization. Multi-file recipes or additional inputs need a unified provenance
manifest first, rather than silently hashing only one file. Unified artifacts use
`--format unified --image FILE`, which cannot be mixed with split-image arguments.

`inspect` rechecks the complete artifact. Missing objects can be restored only by recording the identical
original outputs again, never by rebuilding different bytes under the same revision. Existing corrupt
objects, symbolic links and files of uncertain ownership are preserved and rejected. Failure can leave
complete unreferenced objects or crash temporaries; the tool does not automatically prune them or alter
published mappings.

Candidate catalog generation requires an explicit history source:

```sh
go run ./cmd/incus-image-artifacts catalog \
  --archive "$ARCHIVE_DIR" --previous-catalog "$TRUSTED_PREVIOUS_CATALOG"
```

Use `--first-release` only when there truly is no published history; it is mutually exclusive with
`--previous-catalog`. Catalog generation rechecks every artifact and rejects missing or changed prior
version keys. A missing or corrupt previous catalog is not empty history. Standard output contains JSON
metadata only, never image bytes, source paths or recipe content, and does not update
`modules/incus/images/catalog.json`. Back up the archive and trusted history independently. These local
records do not complete release signing, artifact distribution, Provider import, guest startup or pruning
that protects current/previous deployments. The shipped catalog remains empty; M12/M13 remain unaccepted.

## Release-side build-once archive preparation (real bakes not executed)

`cmd/incus-image-artifacts build` uses `ArtifactArchive.BuildOnce` to run distrobuilder explicitly,
separately from deployment preparation. It is not `anas apply`, Provider `ensure`, a Module Command
or a privileged host action, and registers no browser endpoint. `record`, `inspect` and `catalog`
still never launch a builder.

Use an **isolated, disposable native Linux release builder**, running as root with a reviewed recipe
and an independently verified distrobuilder ELF digest. Target and builder architectures must match;
additional software/devices needed for VM builds must be prepared there. Recipes can execute root
commands: this tool is not a recipe sandbox and must not run untrusted recipes on a production NAS.
Recipes must be self-contained and pin every external input. Forgejo/AI Agent recipes and published
image catalogs are not generated automatically.

Build this release tool from trusted source, then initialize a new archive explicitly on that builder
and invoke the following command (a real bake has not been validated):

```sh
incus-image-artifacts init --archive "$ARCHIVE_DIR"
incus-image-artifacts build \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe "$PINNED_RECIPE_FILE" \
  --distrobuilder "$TRUSTED_DISTROBUILDER_BINARY" \
  --distrobuilder-sha256 "$TRUSTED_DISTROBUILDER_SHA256" \
  --timeout 2h
```

Platform, privilege and binary preflight precede revision reservation. The executable must be a regular,
single-linked, native ELF owned by the executing user and not writable by group/others. It is measured,
copied into a sealed memfd and executed through a fixed descriptor; scripts and symlinks are rejected.
The environment is constructed explicitly, without caller-variable inheritance. Builder stdout/stderr
do not enter the result or logs. Arguments select the fixed
[distrobuilder split mode](https://linuxcontainers.org/distrobuilder/docs/latest/howto/build/): containers
use `incus.tar.xz` + `rootfs.squashfs`, VMs use `incus.tar.xz` + `disk.qcow2`. No
`--import-into-incus`, arbitrary extra flags, aliases or deployment daemon are accepted.

The session lock covers admission, build and commit. A recorded revision is verified against its
original objects and recipe digest and reused without a builder call; missing/corrupt objects fail
instead of rebaking the revision. The CLI's `build` command still requires builder/program preflight;
use cross-platform `inspect` for read-only access. Before the first build, the private
`build-<version-key-digest>/` attempt directory durably records the 0400 frozen recipe, recipe/builder
digests and version key. After building, the recipe is rechecked and the archive's streaming hashes
and immutable commit are reused. JSON stdout contains metadata, never image bytes.

Failures, cancellation and process loss retain the attempt directory, blocking silent retries even
after reopening the archive. Complete, trusted original outputs may be recovered explicitly through
`record`; otherwise use a new revision. Preserve the archive and independent history backup.
Cancellation attempts to terminate the build process group but does not prove mounts, descendants or
external resources have converged. The tool never recursively removes build directories. An operator
must inspect and clean up builder remnants before reuse; a failed build is not confirmed cancellation.

Regressions cover build-once reuse, recipe conflicts, missing-artifact rebuild refusal, interrupted
attempts across sessions, original-output recovery, cancellation and error redaction. Fixtures are opaque
test bytes and establish only orchestration and byte identity. Native Linux sealed-ELF tests, actual
formats, guest boot, security boundaries, signing/distribution and Provider import need separate validation.

## Lease naming key lifecycle

Core now generates and reuses an independent 32-byte compute `LEASE_SECRET`, separate from the client
certificate. Deployment/resource state store references; the consumer receives a sensitive base64 projection
and backup restores the same key. It is excluded from credential rotation. See the
[compute lifecycle contract](../../../contracts/compute/docs/technical.en.md#independent-lease-naming-key).
The dedicated rotation command and production HTTP publishing remain pending.


## HTTP request submission and recovery (unverified code)

`internal/computeingress.RequestWriter` provides atomic consumer request submission and exact
withdrawal. It only opens an installation-provided private 0700 lease directory; it creates no
authorization, registry or mount. On Linux amd64/arm64, directory locks coordinate writers and the
mediator's bounded strict reader is reused. Complete requests are published through private temporary
files, synchronization and rename. Directories are bounded to 256 entries including ignored temporary
files, and a writer retains at most 256 receipts. Unknown files are not deleted in bulk.

Identical requests reuse their files; a different workload cannot overwrite an occupied instance/port
slot. Receipts retain inode descriptors and withdrawal checks both identity and content, protecting a
replacement request using the same filename. `Resume` adopts only an existing request matching the
caller's persisted expectation and never republishes a missing request. `Withdraw` removes intent;
`Close` only releases local handles. Neither proves that routes, permissions, connections or address
reservations have been cleaned up. Failed synchronization or verification reports an uncertain result,
not successful publication.

`Client.OpenHTTPPublisher` explicitly configures `HTTPPublisher.PublishPort/UnpublishPort`. The
configuration contains only lease scope, public policy, base domain, private request directory and a
separately delivered naming key for random mode. It contains no complete frozen authorization,
middleware, entrypoint or global Store reference; default formatting and JSON serialization omit the
key. `Policy.Host` and the mediator's `Authorization.Host` share the naming algorithm, but only the
latter validates complete frozen authority. Submission checks the exact Running managed instance and
its `user.anas.workload`. `Inspect` no longer selects the first result of Incus's name filter and rejects
duplicate exact identities. A file receipt and `RequestedURL()` do not prove network readiness.
Withdrawal requires the original `HTTPPublication` receipt and works after the instance stops or is
deleted. Old receipts cannot withdraw newer requests.

These client checks are not an authorization boundary. Production assembly, automatic delivery of the
minimal configuration projection, application recovery, UID/mount setup, read-only observation
identities and host actions remain pending; automatic ingress stays disabled. The three shared-client
image builds and CI catalogs include `internal/computeingress`. In addition to
`request_writer_integrity_test.go`, new sources in `http_publication_contract_test.go`,
`http_publication_projection_test.go` and `request_writer_receipt_regression_linux_test.go` cover the
separation of name prediction from full authority, exact instance identity, name collisions, stale
receipts, cancellation, cross-writer replacement and filesystem boundaries. None has been run, and no
runtime build, image build or host acceptance was performed for this addition.

## HTTP prototype observation and lifecycle plans

The 2026-09-12 code adds `--capture` for an isolated Linux lab: GET requests to explicitly selected
Incus/Docker Unix sockets are cross-checked with host and Traefik-namespace `ip` queries. It matches
lease networks, configured/runtime guest MACs, IP allocations, Docker endpoints and reciprocal veth
indices, then compares two samples. Output contains selected facts and version/time metadata, not full
Docker environments or administrator credentials. This privileged lab entry point is not the production
mediator's restricted read-only identity and adds no socket mounts to Modules or consumers.

`--previous`/`--withdraw` generate ordered publish, replace, withdrawal and failure-cleanup plans.
Withdrawal retains deny filters; replacement on unchanged topology uses one nft transaction. Changed
topology yields withdrawal only and requires retiring the old lab first. Inputs reject duplicate/unknown
fields, oversized files and final-component symlinks; output uses exclusive creation in a new directory.
`publication.json` describes a proposed publication, not proof it was applied. Host reservation, HTTP
probing, IP holds and cleanup confirmation remain operator steps, with no executor or restart reconciler.

The scope remains one lab HTTP route under `.example.test`, with no production ingress or TCP/UDP
publishing. Build gates, tests and server validation are deferred at the operator's request;
earlier namespace results do not cover it. Usage and pending checks are in the repository's
`test-env/fixtures/incus-network-prototype/README.md` and `e2e-plan.md`.

## HTTP authorization and mediator progress

Core now parses optional `spec.ingress` and freezes ports, auth, domain, lease identity and naming-key
reference in deployment `compute_ingress`. Start/activation still blocks consumers with ingress.
Fixed/named/random naming is implemented; random uses 128 bits of HMAC-SHA256. Preparation rejects
cross-lease namespace and known service-domain conflicts. Default `auth: none` provides no access control:
SNI, Referer and logs can disclose URLs; do not publish sensitive or writable services without authentication.

The lab command's `--mediation` connects constrained Linux request reads, authorization checks and
in-process name reservations. Plans retain the frozen ForwardAuth middleware; withdrawal removes the
route before permits and connections. The command does not run the renderer or install rules. Production
read-only identity, directory mounts, IP holds and watchers remain pending. See the
[compute HTTP authorization notes](../../../contracts/compute/docs/technical.en.md#frozen-http-authorization-and-lab-mediation-runtime-closed).
New code has not been tested; bilingual documentation generation is not a build gate, unit-test result
or real-host acceptance.

The Core activity adapter now reads active deployment/bindings/frozen images under the shared runtime
lock. Lab --register-requests can create isolated per-lease directories; workspace mode validates Core
authority, resolves one naming key when required and rechecks the epoch after capture. Registration stores
no key and mounts nothing or changes networking. Production read-only identity, narrow key delivery and
continuous reconciliation remain pending. This code is untested; see the compute contract linked above.

`internal/computeingressruntime.Executor` now encodes the recoverable publication order: hold the address,
install the guest `/32`, install the narrow HTTP permit, probe the identified backend, then publish the
Traefik route. Withdrawal removes the route, permit, established connections and `/32` before releasing the
address. A `StateStore` records every completed step, and restart reconciliation first retires receipts
outside the intersection of the current Core epoch and fresh observations. The file renderer publishes
reservation-derived YAML with no-overwrite semantics and refuses to replace or remove different content.

This is a trusted execution kernel, not a running production daemon. Typed host-action implementations,
the restricted Incus observer, probing, event sources and consumer directory mounts remain pending.
Local locking and periodic control are described below. Production ingress remains disabled; code is untested.

The 2026-09-13 update persists `retiring` before cleanup starts. Restart recovery finishes withdrawal
even if the original request becomes valid again. Failed address, route or permit actions and invalid
observations before probing enter the same cleanup path. State validates its schema and canonical
64-character lowercase hexadecimal epoch. Persistence failure still requires recovery and does not
prove that networking has been closed.

## HTTP periodic reconciliation and rendering integration (unverified code)

`Controller.Run` holds the `FileStateStore` exclusive flock through startup retirement, periodic work and
shutdown cleanup. Every executor for an ingress must share one private local state root; separate roots
are not coordinated and there is no cross-host leader election. The lock file is never removed. Changes
to directory/lock inode, owner or permissions prevent further actions. Shutdown uses a separate bounded
cleanup context while retaining the lock; failures remain recorded. Retired tokens persist without TTL
pruning, and state has a 4 MiB limit.

`WorkspaceSource` rereads current Core grants and registered requests. It limits each lease to 256 directory
entries and each snapshot to 1024 JSON requests, rejecting duplicate instance ports. Missing/revoked
requests leave the desired set. Changed content or target identity gets a new token; unchanged intent
retains its token across polls. Only after all cleanup is confirmed may the controller discard token caches
and recover still-valid requests through a fresh full authorization/observation read, supporting restart or
temporary-failure recovery without reusing retired tokens. Every publication step rechecks request/auth/Host and independently checks
UUID/IP/MAC. An incomplete read withdraws all recorded prototype routes instead of renewing a cached
snapshot. Events only wake the loop; periodic reads continue even without events.

The file renderer now constructs `ANAS_TRAEFIK_ROUTE__*` and calls the trusted entrypoint's render-only
mode, reusing the existing template. It renders in a private temporary directory with a clean child
environment before exclusive publication into the dynamic directory. A changed shared template that
does not match an existing file causes a conflict instead of replacement/deletion. Durable file writes
do not prove Traefik consumed or withdrew configuration. A runtime `RouteConfirmation` adapter is mandatory;
the renderer refuses execution without it, and failed confirmation blocks subsequent address release.

Server-enforced read-only identity, actual external route inventory, host actions/IP holds, probing, orphan recovery
and narrow naming-key delivery are still required adapters. No daemon was started or production ingress
enabled. No Go dependency was added; these paths have not been compiled or tested.

## HTTP instance fact reader (unverified code)

`IncusFactReader` now supplies a GET implementation for `WorkspaceSource.Facts`. The trusted installation
configuration binds an HTTPS origin, certificates and lease scopes; consumer requests cannot choose them.
Connections require TLS 1.3, an exact server certificate pin and valid server/client certificate dates.
Redirects, environment proxies and unsuccessful/non-JSON responses are rejected. Each GET is limited to
8 seconds and 2 MiB; two complete samples have a 30-second deadline. Errors omit endpoints, private keys
and response bodies, and connection failure never falls back to a Unix socket.

Reads cover server identity, the selected project, its default-project bridge, one instance/state and that
bridge's allocations. They check the version, restricted fence, bridge ownership/NAT, a unique managed
NIC, its MAC and a unique private IPv4 allocation. Two selected samples must agree; subnet membership
alone is insufficient. An incarnation digest binds UUID, generation and last start time to executor targets,
with fresh checks before each step, covering a rapid restart that preserves UUID/IP/MAC. Missing fields
are rejected. The digest is not an address hold; host actions must independently verify current mappings.
Old experimental receipts lacking incarnation are rejected without automatic external cleanup.

Ordinary restricted Incus TLS certificates retain project write access. A GET-only client is not a
server-enforced read-only identity; provisioning that authorization remains pending. No global daemon
policy was changed or mediator enabled. Fields follow the Incus v7.3.0 API; an exact version pin is not
compatibility acceptance. The reader has not been instantiated, compiled or tested. Pending cases are in
`test-env/fixtures/incus-network-prototype/e2e-plan.md`.

## Traefik inventory and consumption confirmation (unverified code)

`TraefikReader` can now supply both `WorkspaceSource.Inventory` and the file renderer's `Confirmation`.
The controller passes its held Journal, which supplies ownership candidates. Exclusion requires the full
trusted renderer output, owner digest and matching loaded router/service/auth, not a filename alone.
The dynamic directory, entrypoint and artifacts must belong to root or the executing user and must not
be writable by others. Orphan recovery remains separate work.

Reads use the existing BasicAuth-protected HTTPS API, pin its certificate and require version 3.7.10.
No API or listener is added. GETs have an 8-second limit; complete rawdata is capped at 4 MiB. Within a
12-second window, two complete matching snapshots, 250 ms apart, must agree on the runtime start time
and include its live API router/auth. HTTPS Host inventory supports Host/Path/PathPrefix, parentheses
and Boolean combinations. Unbounded/unknown rules, multi-layer routing and TCP interception of HTTPS
are rejected; warning/disabled routers continue to reserve their configured Hosts.

Before publishing a file, the reader checks Host/slot conflicts and ForwardAuth. After loading, it checks
the unique HTTPS/TLS router, exact backend, service and authentication again. Only direct ForwardAuth
middleware is supported, with its complete dynamic definition pinned from trusted installation inputs
as canonical JSON, including version defaults and excluding runtime metadata. Live API observations
must not establish that trust. Missing pins, definition drift and chains are rejected. Withdrawal requires
the file, API router/service and direct router references to disappear; failures retain cleanup receipts
and address holds.

Traefik marks constructed backends UP by default; this is not an HTTP probe or real authorization proof.
Automatic credential provisioning, full service installation, host actions and real BackendProbe
acceptance remain pending. These paths have not been run or production ingress enabled.

## HTTP private configuration and fixture probes (unverified code)

`runner.DeliverComputeHTTPReaders` supplies a private Core-side file delivery primitive, and
`OpenWorkspaceReaders` connects it to Incus facts, Traefik inventory/confirmation and naming keys.
Only active random leases' keys, installer-supplied observer certificates/version, Traefik API credentials
and frozen ForwardAuth digests are included; missing or extra keys/pins are rejected. The mediator does
not read the full Secret Store. Fixed and named modes must also match the complete active snapshot.
Server-enforced read-only authorization, trusted pins and UID/mount provisioning remain installer work;
this code does not issue certificates, launch processes or expose an API.

The canonical JSON artifact is limited to 1 MiB. Its parent must be private and owned by the runtime
user; the file must be 0400 with one hard link. Existing destinations are never replaced. Delivery checks
Core authority and naming-key sources before and after publication. Every API GET checks the directory,
file identity, permissions and content digest before and after reading. A new epoch needs new delivery;
old credentials must remain valid through withdrawal of old routes. Missing/replaced configuration
blocks confirmation and retains address holds. Installation and rotation have not been wired or run.

`FixtureHTTPProbe` accepts only pre-registered `.example.test` fixtures. Each complete target, including
token, epoch, UUID/incarnation, MAC/IP and port, binds a distinct expected response length/SHA-256 and a
safe path. Limits are 1024 targets, 256 path bytes and 16 bytes to 64 KiB per response. First-response
trust, shared response digests, HTTP 200 alone and TCP connectivity alone cannot pass. Requests/current
authority and independent Incus facts are checked before and after the exchange. A response can be
copied, so the probe still depends on host address retention/mapping; it is not cryptographic proof
against a malicious guest.

A trusted launcher must place the probe in Traefik's network namespace beforehand and supply its visible
PID, start ticks, boot ID, namespace device/inode, independently obtained socket namespace cookie and
ingress IPv4. Linux checks actual procfs/nsfs identity and each socket's `SO_NETNS_COOKIE`. PID reuse,
process stop/restart, namespace drift, unsupported kernels and non-Linux systems fail. Requests bind the
source IPv4 and exact guest tuple, use credential-free GET and disable proxies, redirects, compression
and connection reuse. Connect/header deadlines are 3 seconds, the HTTP exchange 5 seconds, and the full
probe 25 seconds. No permit renewal, setns or host commands are performed. This does not prove Traefik's
own route/source selection or public TLS/authentication; those require real request validation.

The existing pinned `golang.org/x/sys v0.47.0` becomes a direct dependency without a version change.
Production launch identity/cookie delivery, in-guest service preparation, host actions, production
application probes, orphan recovery and service installation remain pending. Only code, formatting and
documentation generation are complete; tests, gates and server operations remain deferred. Cases are in
`test-env/fixtures/incus-network-prototype/e2e-plan.md`.

Two preparation modes now have code but have not been run. `--capture-probe` reads a selected lab
Docker container/network by full IDs, checks its PID/source allocation, and captures a kernel cookie on
a locked thread already in the target namespace. It rechecks process/namespace identity and emits source
IDs and a timestamp. No setns or service launch occurs; the administrator socket is not given to the
mediator. Production launch identity delivery remains pending.

`--prepare-fixtures` reads actual WorkspaceReaders under the same state lock, accepts only example.test
targets and refuses outstanding publications. It pre-generates distinct responses, installation guidance
and a private 0400 registry (1 MiB/1024 entries), with current authority, requests and independent instance
facts checked before and after registration. It does not probe or publish routes. Responses still need
installation in the selected guest's HTTP service. Registration failure leaves private preparation files
for inspection; their presence does not establish network readiness.

`NewRegisteredFixtureHTTPProbe` stores every stable target field except reservation and binds the current
valid token at probe time. It rechecks the file and Core around the exchange; epoch, request, instance
incarnation/MAC/IP, port, Host and authentication must all match. A mediator restart can use a fresh token;
a guest restart or identity change requires new preparation. Retired tokens remain forbidden. Static
`NewFixtureHTTPProbe` still binds complete targets for one session. Host actions, in-guest service
installation and full E2E remain pending; the lab directory has bilingual command documentation.

## Shared action invocation service (unverified code)

`internal/jobexecutor/module_action_dispatcher.go` provides shared invoke, query, attach and cancel
operations for future CLI/HTTP adapters. Execution and durable cancellation reuse ModuleActionWorker,
not a second queue. Subscriber disconnects do not cancel jobs, historical jobs remain permission-scoped,
and the Module service neither consumes nor exposes host-namespace actions. This internal code and its
regression sources remain unrun. The main daemon, existing clients and privileged host channel are not
wired to it, so Incus stays developing and production ingress stays disabled. Confirmation tokens,
product-entrypoint migration and real-host acceptance remain separate pending work.

The registry and dispatcher now use the same store for action-key retries and coalesce/reject/queue.
Keys are not partitioned by actor or entrypoint; a different workspace or complete frozen request
conflicts. Every retry is reauthorized, and an existing job or identifying conflict requires current
read permission. Pending/running keys do not expire; terminal keys remain valid for one hour.
Assignment timestamps and ownership chains survive compaction/recovery, preventing clock rollback
from resurrecting a superseded owner. A new alias is committed atomically after auditing its actual
joining actor. Each job permits 64 aliases, rejects overflow instead of evicting live keys, and creates
no alias for a keyless coalesced invocation. Logical expiry neither deletes jobs nor implements a
global storage quota. Regression sources were added but not run. These unprivileged Module facilities
do not replace the Incus host-network adapter.

## HTTP external artifact recovery (unverified code)

`Executor.Recover` retires outstanding receipts under the same state lock and confirms that the complete
external scope is clear. Controller startup, failure recovery and shutdown share this path. Mandatory
`HTTPArtifactInventory` checks run before/after opening or renewing routes and before releasing addresses.
Unknown artifacts block publication and address reuse while known routes/permits/connections can close;
failures retain retiring receipts. Tombstones only prevent replay and are not cleanup candidates. Missing
state does not prove a clean installation; corrupt state is not overwritten or unknown ownership imported.

File inventory requires a dedicated trusted directory, without unrelated auth configuration, and allows
4096 entries within 30 seconds. Complete bytes, directory identity and entry sets are checked around real
API reads. Only complete journal/file/loaded HTTP router/service/auth matches are accounted for. Other
providers, protocols and service references using the reserved `anas-compute-` prefix are rejected; only
verified ForwardAuth `usedBy` edges are excluded. Conservative scanning may reject unrelated strings with
that prefix. Errors do not include raw API configuration.

Withdrawal also cleans canonical temporary files only when the name and complete rendered bytes match
an outstanding receipt. It rechecks identity before removal, confirms absence and syncs the directory.
Partial writes, template changes and unknown artifacts are not removed by prefix. API withdrawal checks
also cover service-to-service and other dynamic-section references; deleting a file cannot release an
address by itself.

Host inventory is still a mandatory interface without a real adapter. Independent network/allocation
evidence, the deny baseline, IP holds and restricted administrator recovery actions depend on the host
channel; there is no empty implementation or third privilege entrypoint. File/API/kernel observations are
not atomic. Races, failures, both guest types and public dual-stack traffic need real acceptance. Code has
not been run, cases are recorded, and production ingress remains disabled.

The host-action prerequisite now has bounded request/event framing, job/invocation binding, replay
sequence checks and process-outcome validation in `internal/actionabi`. Forced termination or incomplete
output cannot report cancellation or success. The existing `consolejobs` journal now has internal action
bindings, per-job sequences, atomic event/truncation/terminal records, replay and unknown outcomes on
restart. `jobexecutor.ActionRecorder` requires an action-specific public projection and defers terminal
commit until actual EOF and exit validation. There is no second job store or new dependency. Production
dispatch, Module Command/CLI/HTTP migration, authorization/audit policy and host-channel installation
still need integration and acceptance. The Module registry and Linux supervised-process adapter are
internal implementation work, not a host execution channel. Legacy workers skip action jobs; the HTTP
host adapter cannot yet be enabled.
All new code remains uncompiled and untested. Only code and documentation generation have proceeded;
gates and server operations remain deferred, and production ingress remains disabled.

On 2026-09-18 the shared job store gained an execution-loss barrier. Unknown actions with the cause
`execution_containment_lost` or `daemon_restarted` prevent new execution through both legacy and action
start paths, including read-only jobs and other workspaces sharing that store. Reading, replay and
queued cancellation remain available. The durable receipt survives compaction, reopening the store,
recreating a registry and ordinary business-compensation acknowledgement. It is not evidence that
remaining processes or writers have stopped; restricted recovery and service wiring remain pending.
Regression test source was added but has not been run.

## Ingress host kernel identity and receipts (2026-09-20)

The host backend binds the complete Target and installed topology in v2 receipts, uses directory-
descriptor-relative persistence and supports cancellable guard acquisition. Namespace execution
independently verifies Docker identity/start time, PID/start ticks/boot ID, nsfs device/inode and
socket cookie. Fixed ip commands run in the opened namespace without externally supplied paths
or commands. Container source IP and host bridge gateway are observed separately; fixture-only
fields are not production ip output. Ports share only the same complete allocation; an unreleased
old hold blocks a restarted or different instance from reusing that IP.

Receipts use `anas.incus-http-host-receipt/v2`; v1 evidence is not silently migrated or removed.
The actual allocator lifecycle, health identity, production wiring and native acceptance remain
incomplete. Production ingress stays disabled. Prior scoped/full Go tests and Linux dual-architecture
compilation passed, but do not establish native CI or real-host acceptance. See the
[recovery review](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-ingress-recovery.md).

## Scoped nft firewall and installation ownership (2026-09-20)

The ingress backend fences only managed bridge paths, without a global `policy drop`.
A regular `http_permits` chain runs before the scoped deny rules. Readback checks complete
ordered ASTs, table/chain/handle identity, every dynamic object and native JSON top-level
comments, numeric timeouts, concatenations and expiry. Unknown rules cannot disappear through
name filtering. Empty/expired sets may be revoked but cannot report readiness.

An independent `.nft-baseline.json` records installing/installed/removing/removed states and
actual table handles. Installation verifies absence, persists intent before effects and confirms
readback before completion. Removal requires publications, routes, permits and connections to
be cleared. Existing tables without independent receipts are not adopted; unresolved intents
are not retried silently. Existing private dirfd primitives are reused, with no new Go dependency.

Local scoped/full Go and race regressions passed. The native namespace/nft CI cases were written
with skip rejection but were not run here. Their isolated-namespace nft syntax, JSON and lifecycle
checks still use IP/allocator/conntrack fixtures, not actual HTTP traffic, Docker/Incus coexistence,
dual-stack or address-reuse acceptance. Production ingress stays disabled.

The interface consumes the actual public job DTO (`kind`, `mutating`, workspace/id and result),
not internal `job.action`. The displayed plan must match its approval binding's schema,
workspace, plan/state digests and deletion set. A valid empty inventory displays no changes and
cannot execute. Changed input, plan expiry or disposal cannot reuse prior consent. Confirmation
tokens are not exposed in public state, and uncertain apply results are never retried automatically.
Missing public fields or inconsistent bindings fail closed rather than relying on type assertions.


## Device-bound host address routing (candidate; production disabled)

`address_routing` is a root-owned installation projection, not consumer configuration. A separate host
routing table and terminal unreachable rule apply only to the selected Traefik source, guest subnet and
Docker ingress interface. A permanent neighbor and `/32` bind the independently checked container host
veth. An old reservation never recreates a route after device deletion or falls back to the ordinary bridge
route. This is not a DHCP reservation and does not change Incus pools or guest devices.

`.address-routing.json` records the complete scope, ifindex, allocation intent, shared ports and retired
tokens. Readiness requires both durable evidence and kernel readback. `address_intent` is saved before
external effects; normal and failed publications use the same withdrawal path. Hold, renewal, inventory and
release are wired into the Backend; a production call cannot claim readiness from a journal-only hold.
Existing tables, priorities and neighbors are not adopted. Unknown earlier policies, local destinations,
replacement devices and unaccounted artifacts fail closed.

Limits are 32 allocations, 64 users per allocation and 256 live plus retired tokens. Admission reserves
future cleanup capacity, including a 60 KiB encoded-JSON admission budget below the 64 KiB file limit;
apply and reinstall do not erase retirement history. The address layer itself remains a forward-path
container-veth candidate; the reply-side addition is described below. VM/TAP, full stale TCP sessions,
real Incus observation, health identity and production service wiring remain incomplete. The native FIB test is required by the gate but was not run
in this work. Production ingress remains disabled. See the
[address-routing review](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-address-routing.md).


## Reply origin and bidirectional connection cleanup (candidate; production disabled)

With `address_routing`, bridge chain `http_reply_origins` checks the original numeric ifindex, veth name,
guest MAC and approved IP/service port before admitting replies to the Traefik backend source address;
unmatched traffic is explicitly denied. The index comes from the independent address hold. Cleanup never
learns a replacement device's identity from a reused name. Inet requests/replies additionally require the
original/reply conntrack direction respectively.

The two expiring permission families are created, renewed and revoked in one nft transaction; both must
be read back before readiness. Missing, expired or foreign objects cannot report ready. Connection deletion
requires verified revocation on both sides, selects each observed original/reply address, port and default
zone, and confirms absence afterward. A batch is limited to 256 entries. Translated tuples, nonzero zones,
offload and malformed inventory block cleanup: the initial scope is directly routed IPv4/TCP without backend NAT.
Old policy evidence is incompatible with `bidirectional-origin-v1`; receipts and orphaned objects are not
silently migrated or adopted.

Native packet and conntrack test sources are required by the gate but were not run here. They test source
rules and kernel records separately, not full TCP sessions, Incus guests or Docker coexistence. Forced/wrapped
ifindex reuse, stop/pause with a surviving interface, VM/TAP, independent Incus identity supply, health and
production wiring remain outstanding. Production ingress remains disabled. See the
[reply-origin review](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-reply-origin.md).
