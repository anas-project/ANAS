# Incus compute provider technical notes

## Mainland package mirrors

Host install plans resolve effective `CHINESE_SPEEDUP` from managed workspace configuration,
including top-level `env:` overrides, and freeze it as `request.chinese_speedup` before confirmation.
Enabled plans use the fixed `https://mirrors.aliyun.com/debian`, `debian-security`, `ubuntu`, or
`ubuntu-ports` mirror for distribution dependencies. Incus packages still come exclusively from pinned
Zabbly `lts-7.0`. Distribution signing keys, suites and architectures remain unchanged; APT origin pins
follow the mirror. Installation accepts only the two compiled variants of the same recipe, allowing a
new install plan to switch back while rejecting unrecognized drift. Uninstall accepts either complete
policy without downloading or switching sources. Fully installed packages are not reinstalled merely
to change mirrors. Existing confirmations retain their frozen choice; new plans use new configuration.

Guest baking uses separate `CHINESE_BUILD_SPEEDUP`. The release script passes the recipe CLI's
`--chinese-build-speedup` option, freezing the Debian bootstrap URL and a `post-unpack` hook that
rewrites Debian main/security URLs in `.list` and `.sources` files before APT installation. Suites,
components and signature configuration remain intact. Manual `runner-image/provision.sh` uses the
same script. The choice enters recipe bytes and the digest; a changed source cannot rebuild the same
revision, and runtime `CHINESE_SPEEDUP` does not rebake published guests. Neither path accepts arbitrary
`APT_MIRROR_URL` values. Real Linux downloads, installation and guest baking were not run for this change.

## Removed implementation (2026-09-30)

The following implementation is outside the requirements and has been deleted. The design history and
earlier validation records remain in `dev-docs/reviews/` and in the historical sections of the host
provisioning design; the deletion list is in the
[ingress and host-channel simplification record](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md):

- Per-instance forwarding permits (egress): `internal/incusingresshost/forwarding_*`,
  `internal/incusprovision/forwarding_*`, the host actions `incus.forwarding.permission(.plan)` and
  `incus.forwarding.withdraw`, the pre-deployment forwarding withdrawal, and `forwarding_scopes` in the host
  `state.json`. Egress is now lease-level tiers (`INCUS-R-112`–`R-127`, M10a).
- The per-publication HTTP ingress runtime: `internal/computeingressruntime`, the rest of
  `internal/incusingresshost`, `ingress_observation*` and `observer_configuration*` in
  `internal/incusprovision`, the host actions `incus.ingress.observe_http` and `incus.ingress.observer(.plan)`,
  the console and CLI `observer` phase, the job executor's ingress drain and workspace fence,
  `cmd/incus-network-prototype`, and `observer_scopes` in the host `state.json`. Ingress is now the lease ACL,
  an HTTP publication mediator inside anasd and port bindings (`INCUS-R-130`–`R-164`, M11/M11b/M11c).
- Kept: Core's parsing and freezing of HTTP authorizations, the request files and authorization types in
  `internal/computeingress`, and the lease naming key. Since 2026-10-03 the declaration is `publish.http`, the
  start-time block on consumers that publish HTTP is gone, and requests carry `{instance, address, port, label?}`;
  see "Host side of HTTP publication and port bindings" below.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `7.3.0-r2` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Module, capability and contract dependencies

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `compute` | Provided contract | `1.0.0` / `incus_vm` |
| `compute` | Provided contract | `1.0.0` / `incus_container` |

Both interfaces share the executor and validation path, selecting project/profile fences for the
requested isolation tier without exposing low-level devices or privilege settings to callers.

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_incus_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-incus-provisioner:7.3.0-r2` | `incus` | 0 |
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
It must contain `go.mod`, `go.sum`, `internal/computeclient`, `internal/computeimage`,
`internal/computeingress` and `internal/securefs`, not just
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
from source and staging were separately accepted on 2026-09-23 in an isolated Ubuntu 26.04 / Docker
29.1.3 / Compose 2.40.3 VM. All six no-cache builds passed with matching input and business binary
digests, identical pinned Incus CLI binaries and `7.3` output, the missing-override negative control,
and removal of all test containers/images. This verifies build-only staging, not a complete Core
deployment or actual consumer jobs.

Both builder and runtime bases of the three compute images honor the repository's existing
`DOCKER_HUB_REGISTRY` setting, including a registry repository prefix. An explicit
`GO_BUILDER_REGISTRY` overrides only the Go builder. The separately pinned upstream Incus CLI and
AI Agent's third-party modules use `GO_MODULE_PROXY`, falling back to `GOPROXY_URL` and then the
official source. These build settings are not projected into the service runtime environment. They
do not disable Go's checksum database or network-resolve the Provider/controller's shared code.

`go run ./cmd/check-shared-build --json` emits verified static input metadata and explicitly states
`docker_executed: false`. For staging, also provide `--staging-root`, `--source-root` and the absolute
shared override described above. The report binds file bytes, executable modes and build declarations,
rejecting mixed revisions, unknown build fields and credential inputs without reading runtime `.env`.
It is not proof that a Docker build succeeded.

The repository's `test-env/fixtures/incus-shared-build/README.md` defines six native builds in an
isolated QEMU VM, checking actual Compose resolution, the missing-override negative control and
non-root image binaries. Its staging fixture preserves real build inputs but is not a complete Core
deployment; it does not establish `anas build/apply`, Provider/guest lifecycle or signed publication.

## Control connection (Incus listens on the control gateway since 2026-09-30)

Consumers and the Provider reach the host's Incus through the Docker control bridge. `incus.configure`
sets Incus `core.https_address` to port `8443` on the control bridge gateway, its only HTTPS listener,
and installs a drop-in that orders `incus.service` after `docker.service`: the gateway address exists
only once Docker has created the bridge, and Incus 7.0 retries a failed bind only once, after 30
seconds. The host firewall admits only the control bridge subnet to that port; mTLS and the pinned
server certificate are unchanged. The former non-root relay `modules/incus/control-relay`, its unit,
account and configuration are removed. See section 3.9 of the Chinese host design.

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
audit; full daemon state and actual host capabilities have separate acceptance gates. The root socket,
shared job/CLI/Web and plan/one-time-confirmation/execution paths are wired. Local tests do not replace
native Linux identity or actual installation acceptance. Linux peer/filesystem execution is separately
tracked. See [the host provisioning design](../../../docs/architecture/incus-host-provisioning.md),
sections 2.1 and 2.2, for the package source, upstream version differences and remaining acceptance limits.

### Host action execution (simplified 2026-09-30)

The Module settings below expose no private host-channel inputs. After a job starts in the shared job
store, anasd connects to the root:root 0600 `/run/anas/hostd.sock`, writes one `anas.action/v1` request and
reads the event stream. hostd admits only a root/root peer and keeps a record of each invocation under
`/var/lib/anas-hostd/invocations`: an invocation id can run once, and the terminal is written before the
terminal frame is sent. When the stream ends before a terminal, anasd reads that record through the
read-only `host.invocation.status` action. There is no private broker, PID 1 private-bus check or systemd
exit observation any more. The service option `host_actions` defaults to false and is not compute
readiness. `bash test-env/scripts/test-host-action-native.sh` requires the invocation-record, activation
and peer tests to run on Linux; it is not systemd or Incus acceptance. See
[the host-channel design](../../../docs/architecture/host-action-channel.md), section 14.

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
above describe prior slices. Incus serves its API only on the control bridge gateway; no relay is
packaged since 2026-09-30. Native systemd/Incus/KVM acceptance remains outstanding and production ingress
is disabled. See the current boundary in [host-action architecture](../../../docs/architecture/host-action-channel.md), section 13.

### Module parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | — | `host` | `INCUS_ADMIN_CERTIFICATE_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Provisioning-only administrative client certificate, never handed to a consumer |
| `incus.admin_key_b64` | string | — | — | `host` | `INCUS_ADMIN_KEY_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Private key for the administrative certificate |
| `incus.endpoint` | string | `pattern: ^https://[A-Za-z0-9.:_-]+$` | — | `host` | `INCUS_ENDPOINT` | no | yes | yes | yes | `reconcile` | HTTPS address of the remote Incus daemon |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | `host` | `INCUS_IMAGE_ARCHITECTURE` | no | yes | no | yes | `container_recreate` | Explicit guest image architecture on the target daemon; never inferred from the CLI host |
| `incus.lan_extra_subnets` | string | `pattern: ^[0-9A-Fa-f:./, ]*$` | `""` | `static` | `INCUS_LAN_EXTRA_SUBNETS` | no | no | no | yes | `reconcile` | Extra LAN subnets as comma-separated CIDRs, merged with the default-route interface subnets Core computes at apply; the `internet_lan` and `internet_lan_host` tiers allow them. The Hook refuses default, loopback, link-local and multicast ranges |
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
schema is `anas.incus-connection-bundle/v2` (`relay_service` removed on 2026-09-30); automatic wiring
requires `architecture` (`amd64`/`arm64`) and `storage_pool` (`anas-btrfs`); old bundles are not inferred.

The automatic bundle must pin `endpoint=https://<control_gateway>:8443`,
`control_network=anas-incus-control`, and the management
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
   project may hold running instances and operator-added `user.*` keys. Ownership is verified before
   the merge (see "Lease ownership" below). An existing project with
   `features.networks=true` is refused: changing its network ownership requires explicit migration.
2. **Read back** and assert every managed project setting matches its requested value, including `restricted=true`, the exact four quota totals, the tier's instance-type limits, the complete `restricted.*` set (see below), storage admission read back again, network features disabled,
   managed NICs, and `restricted.networks.access` equal to exactly this lease's bridge. Any failure returns an
   error and does **not** go on to register the certificate. This step is the contract's only source of
   trust: a successful write does not count, only the daemon's own copy does.
3. Create and verify the bridge in the default project, then create and verify the profile in the lease project. This
   comes before the certificate: handing out a credential for a lease that has no root disk and no NIC
   yet would achieve nothing.
4. Read each frozen image in the target project and verify fingerprint, architecture and type.
   An absent image may only be imported from a matching frozen supply and read back; missing supply or mismatched metadata fails. No alias lookup is used.
5. Register the consumer certificate. If that fingerprint is already trusted, verify it is restricted
   and that `projects` contains exactly this sandbox; if it is unrestricted or bound elsewhere, exit
   with an error and change nothing. Finally recheck the complete live dependency chain read-only before returning ready.

## Lease ownership

The sandbox name is written in the consumer's manifest, so every workspace that installs the same
consumer uses the same name, and the name alone cannot prove ownership. The Provider writes ownership
markers on the project and on the managed bridge: `user.anas.consumer`, `user.anas.sandbox` and
`user.anas.lease_credential` (the SHA-256 fingerprint of this lease's restricted client certificate).
Core generates one certificate per workspace, consumer and resource, so a second workspace declaring
the same sandbox on the same daemon has a different fingerprint.

Before writing, `ensure` refuses a project in two cases: its markers belong to another lease (even when
that lease's certificate has been revoked, since `revoke` keeps the project), or a restricted `client`
certificate other than this lease's can drive it. An unmarked project (created by the Provider before
these markers existed, by an older controller, or by hand) is adopted and marked only when the second
condition does not hold. Unrestricted certificates belong to the daemon administrator and metrics
certificates cannot write, so neither counts. `inspect` also requires both project and bridge to carry
this lease's markers and no other restricted certificate before reporting `ready`; it stays read-only
and repairs nothing. The `default` project can never be a sandbox; Core and the Provider both refuse it.

The fingerprint changes with the certificate: a future overlapping certificate rotation (CRED-R-006)
must rewrite the markers in the same transaction. When a workspace is copied wholesale together with
its Secret Store, both copies hold the same certificate; that is duplicated identity, which this
mechanism does not distinguish.

## Network and profile

The network name is `lease` followed by the first 10 hex characters of the sandbox name's SHA-256
(15 characters). The sandbox name cannot be used directly: a Linux bridge interface is capped at 15
characters and `anas-forgejo-runners` is 20. The `lease` prefix lets the host's static forwarding rules match
every lease bridge exactly while staying outside the `anas*` interfaces `anas-helper` may operate on
(`INCUS-R-127`). Leases before 2026-10 used `anas` with the same digest; once `ensure` has created the new bridge
and pointed the profile at it, it deletes the old bridge and its ACL when no instance uses them any more, and
keeps and reports them otherwise.

The Provider owns the bridge and explicitly uses `project=default` in network API requests. Each lease
sets `features.networks=false`, `restricted.devices.nic=managed`, and an exact `restricted.networks.access`
allowlist. Incus 7.3 does not support bridges in non-default projects. Instances, profiles, certificate
scope, and quotas remain in the consumer's separate project.

Networks carry `user.anas.consumer` and `user.anas.sandbox`. A same-name network with missing or conflicting
ownership, an incompatible type, or external interfaces is refused without adoption or certificate
registration. Names alone do not establish ownership. Repeated applies preserve allocated subnets and
unrelated configuration; disabling IPv6 writes `none` and removes its old NAT setting. Network writes
are read back for type, ownership, addressing and NAT before a profile is created.

**Addressing.** The IPv4 DHCP range runs from `.2` to the 33rd address below broadcast; the top 32 addresses of
the subnet are for slots, assigned in slot-name order and kept across applies, and released when a slot is no
longer declared. When the lease has IPv6 and declares slots, the bridge runs stateful DHCPv6 with the range
`<prefix>::1:0`-`<prefix>::1:ffff`, and slot addresses start at `<prefix>::ff00`. The slot assignment lives in the
bridge's `user.anas.slot.<name>.{instance,ipv4,ipv6}` keys, and the profile carries a copy for the shared client.
The last JSON line of `ensure` reports the bridge name, IPv4/IPv6 subnets and gateways and every slot's addresses,
which Core records in resource state (`INCUS-R-159`).

**ACL.** Every lease bridge carries a Provider-owned network ACL named like the bridge (default project, with the
consumer/sandbox marks and the lease certificate digest). The bridge sets `security.acls=<ACL>` with default action
`drop` in both directions (`INCUS-R-130`). Incus admits DNS, DHCP and core ICMPv6 first, and conntrack admits
replies. The rules come from the frozen tiers:

| Tier | Egress rules (every source is limited to the lease's own subnets, so forged off-subnet sources are dropped) |
| --- | --- |
| `internet` | allow public addresses; drop the LAN, host addresses and `$anas-leases` |
| `internet_lan` | allow public addresses and the LAN; drop host addresses and `$anas-leases` |
| `internet_lan_host` | allow public addresses, the LAN, host addresses and the private ranges (where Docker's published ports land after DNAT); drop `$anas-leases` |
| `modules_only` | allow only the Traefik entrypoint port on `$anas-traefik` |

`module_access` adds an allow to the `$anas-traefik` entrypoint port for `internet` and `internet_lan`. Only the
`published` ingress tier has ingress rules: first drop connections from `$anas-leases`, then allow `$anas-traefik`
to the HTTP publication ports and any source to each slot's bound guest ports. Incus applies drop before allow, so
an instance reaching its own lease's slot through a host port is blocked too; direct traffic inside a lease is
`intra_lease`'s decision -- when it is off the profile NIC sets `security.port_isolation=true`, and instances on one
bridge cannot reach each other at layer 2 (`INCUS-R-123`). The LAN is computed by Core at apply from the host's
default-route interface subnets plus `lan_extra_subnets`, and host addresses are the host's interface addresses;
both reach the Provider through `ANAS_RESOURCE_NETWORK`, `ANAS_RESOURCE_LAN_SUBNETS` and
`ANAS_RESOURCE_HOST_ADDRESSES`.

`$anas-leases` is a global address set every `ensure` rewrites to the union of all lease bridge subnets;
`$anas-traefik` is written only by hostd's sync action, and the Provider only names it -- when it is missing,
`ensure` fails and points at `incus.configure`. Both need the daemon's `network_address_set` API extension (Incus
7.0 and later); without it `ensure` fails before writing. `inspect.ready` requires the ACL rules to match the current
tiers, subnets and slots exactly and `$anas-leases` to cover this lease's subnets; extra, disabled or widened rules
are drift that `ensure` repairs with a whole-object PUT. Host package uninstall inventory treats any network ACL or
address set as the daemon still being in use.

The profile is fixed as `anas-lease` and carries exactly two devices:

| Device | Contents |
| --- | --- |
| `root` | `type=disk`, `path=/`, `pool=<storage_pool>`, no `source` |
| `eth0` | `type=nic`, `network=<lease bridge>`, `security.mac_filtering=true`, `security.ipv4_filtering=true`, plus `security.port_isolation=true` when `intra_lease` is off; no `parent`/`nictype` |

The profile configuration is `user.anas.managed=true`, the tier's nesting/privileged keys and the slot copy. When
the shared client creates a slot instance it overrides two keys on the instance with
`--device=eth0,ipv4.address=…` (and `ipv6.address`); every other NIC setting stays the profile's.

The network's IPv6 follows the host. The hook sets `INCUS_NETWORK_IPV6=true` only when the IPv6 switch
is not off **and** `HOST_HAS_IPV6=true`; the bridge then gets `ipv6.address=auto` with `ipv6.nat=true`.
Otherwise it is written explicitly as `ipv6.address=none`. Writing `none` rather than leaving it unset
is deliberate: an unset value lets the daemon apply its own default, and handing a guest a v6 address
the host cannot route makes every outbound connection wait for a timeout before falling back to v4,
which reads as a hung job rather than a misconfiguration.

`ensureProfile` does a whole-object PUT rather than a merge, and `verifyProfile` then reads it back and
requires exactly two devices, exactly the managed template's configuration keys, and the exact
device property maps. Additional raw configuration or device properties are refused.
Together they are the only enforcement point for this constraint: the
daemon does not stop anyone attaching devices to a profile, so "no extra devices on the profile" holds
only because this code checks.

The two refusals in step 5 are the provider-side privilege-escalation defence: silently accepting a
certificate that is already trusted with global rights would hand the consumer the whole daemon.

`inspect.ready` requires the complete project fence, admitted pool, network ownership/NAT, source fence ACL, profile,
restricted certificate and frozen images to remain valid. Project existence or network scope alone
is insufficient; revocation preserves the project but clears readiness. Inspection never reads supply
files, imports images, repairs configuration or grants trust. It reports `exists`, `ready`, `restricted` and `quota_enforced` separately. A
missing project returns zero values rather than an error, because "absent" is a normal observable
state.

`revoke` deletes the consumer certificate, empties the lease ACL's rules (the default actions still drop, so
nothing enters or leaves any more) and stops the project's running instances: the ACL admits replies on
established connections, and only stopping the instances ends them (`INCUS-R-124`). The project, instance disks
and bridge stay; deleting the project would destroy the instances inside it, and those instances were never owned
by this contract. Deleting a fingerprint that does not exist succeeds idempotently, and an ACL or project that
belongs to another lease is left alone.

When a consumer is removed or its capability is switched off and the target deployment no longer declares
the lease, Core calls `revoke` through this Module's artifact frozen in the previous deployment. A failure
aborts the activation by default; only `--allow-risky` records an unconfirmed revocation. The resource
state stays `retained` and records `revocation`; see "Ending a lease" in the compute Contract technical notes.

## Quota mapping

The contract states per-instance limits while an Incus project states project-wide totals.
`projectConfig` reconciles them by multiplying through `max_instances`:

| Contract | Incus project key | Value |
| --- | --- | --- |
| `quota.max_instances` | `limits.instances` | as given |
| `quota.cpu` | `limits.cpu` | `max_instances × cpu` |
| `quota.memory_mib` | `limits.memory` | `max_instances × memory_mib` MiB |
| `quota.disk_gib` | `limits.disk` | containers `max_instances × disk_gib` GiB; VMs `max_instances × (disk_gib GiB + 500 MiB)` |

The extra 500 MiB on the VM tier is the state filesystem volume Incus attaches to every VM root block volume
(`size.state`): the daemon adds it to the root size when it totals `limits.disk`. Budgeting only the root made a
VM using its full per-instance disk quota impossible to create (Incus 7.0.1 measured `Reached maximum aggregate
value` on 2026-09-26; the 6.0.5 source counts it too). The VM-tier profile therefore pins `size.state=500MiB` on
its root device so the budget cannot drift with a daemon default.

Project `limits.disk` bounds declared allocation; it does not prove a root disk has a working quota.
A real daemon probe found that `dir` can warn and continue when filesystem project quotas are absent.
This provider therefore admits only `Created` btrfs/zfs pools. It refuses dir, including dir pools that
might have filesystem quotas configured, and other drivers until capability evidence is added. This
is a provider limitation, not a claim that upstream lacks those drivers. It neither probes host
filesystems nor converts pools or migrates existing instances.
`inspect` rereads pool admission: missing, unready or unsupported storage yields `quota_enforced=false`
and `ready=false`, while preserving project `exists`/`restricted`. Read errors remain errors. This
check does not replace a guest disk-fill test or monitor later administrator changes. Failed admission
does not automatically revoke existing certificates. See the [Incus dir quota prerequisites](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas).

The Provider owns every `restricted.*` key the target daemon knows: the full Incus 6.0 LTS set
(6.0.0 through 6.0.5, the versions in the distributions' official repositories) plus the 7.x keys the
daemon advertises through API extensions. Host provisioning installs Zabbly `lts-7.0` by default (see
[the host provisioning design](../../../docs/architecture/incus-host-provisioning.md) section 2.2); the
Provider still supports existing 6.0 daemons. Each key is either written with a strict value or required
to be absent:

| Handling | Keys |
| --- | --- |
| written as `block` | `restricted.backups`, `restricted.snapshots`, `restricted.cluster.target`, `restricted.containers.interception`, `restricted.{containers,virtual-machines}.lowlevel`, `restricted.devices.disk\|gpu\|infiniband\|pci\|proxy\|usb\|unix-block\|unix-char\|unix-hotplug` |
| written with another fixed value | `restricted=true`, `restricted.containers.privilege=unprivileged` (both tiers), `restricted.devices.nic=managed`, `restricted.networks.access=<this lease's bridge>`, `restricted.containers.nesting` (`block` for VM, `allow` for container) |
| must be absent | `restricted.idmap.uid\|gid`, `restricted.networks.integrations\|subnets\|uplinks\|zones`, `restricted.devices.disk.paths` (only effective with disk=allow), `restricted.cluster.groups` (only effective when a cluster target may be chosen), `restricted.images.servers` (7.0+, see below) |
| written when the daemon supports it | `restricted.storage-pools.access=<storage_pool>` (`projects_restricted_storage_pool_access`, 7.0+) and `restricted.virtual-machines.nesting=block` (`projects_restricted_virtual_machines_nesting`, 7.x, present in 7.5.1), for both tiers |

`ensure` and `inspect` first read `api_extensions` from `GET /1.0` instead of inferring from a version
number. The daemon rejects unknown project keys, so 7.x keys are written only when the daemon
advertises them; where the daemon supports them and they are left unset, the defaults are permissive
(`restricted.virtual-machines.nesting` defaults to `allow`, `restricted.storage-pools.access` to every
pool). A daemon that does not report its extensions fails closed rather than being treated as 6.0.

A daemon without that extension -- including the default Incus 7.0 LTS -- cannot block nested virtualization
for VMs at the project level: there `security.nesting` is a container key that a VM accepts without effect,
and on a nested-KVM host a VM lease guest was measured to see `vmx` on 2026-09-26. This is a version limit,
not configuration the Provider can add; deployments that need the guarantee should use a daemon advertising
`projects_restricted_virtual_machines_nesting` or disable nested KVM on the host.

A daemon that supports the VM nesting restriction enables nested virtualization on VMs by default and,
once the restriction is `block`, refuses any VM that does not set `security.nesting=false` explicitly
(`checkRestrictions` in 7.5.1). On such a daemon the VM-tier profile therefore also writes
`security.nesting=false`; on earlier releases the key is container-only and a VM rejects it, so it is not
written. After upgrading to such a daemon, VMs created in a VM-tier project before the profile update
conflict with the tightened restriction and `ensure` fails; it converges once those one-job instances
are reclaimed.
After the pool is narrowed, the daemon refuses the update if existing instances still use another pool
(for example after changing the `storage_pool` setting), and `ensure` fails instead of moving them.

`restricted.images.servers` cannot serve as "no remote images". According to the Incus 7.0.1 and 7.5.1
source (`internal/server/project/permissions.go`), when it is non-empty, a request to create an instance
from an image already in the project names no image server, so its empty host is rejected as well; and
it only gates URL downloads, not simplestreams copies. Setting it would stop a lease from booting its own
images without stopping image imports, so the Provider removes it and the per-fingerprint allowlist stays
consumer-side (R-085). On 2026-09-26 this was reproduced on real Incus 7.0.1 and 7.5.1 daemons: with the key set,
creating an instance from a local project image is refused (`Image server "" isn't allowed in this project`). That
simplestreams copies are not gated still rests on reading the source.

`restricted.devices.disk=block` still permits the root disk and forbids attaching any other disk. For
its fixed inner OCI namespaces, `incus_container` writes `restricted.containers.nesting=allow` with
`security.nesting=true` in the managed profile. Consumers cannot change these per job — the system
container tier is a weaker **isolation** boundary than a VM, never a weaker **privilege** boundary.

The isolation tier also lives on the project rather than only in the shared client's `--vm` flag: the
VM tier writes `limits.containers=0` and `limits.virtual-machines=<max_instances>`, the container tier
the reverse. The daemon therefore refuses a system container, which shares the host kernel, inside a VM
lease. The permitted type is written explicitly so a stale `0` from a previous tier cannot survive the merge.

These keys are explicit rather than left to the defaults implied by `restricted=true`: ensure merges
existing project settings, so an unmanaged key would preserve a previous `allow` or range. Ensure
tightens the written keys, removes the must-be-absent keys and reads the result back; read-only inspect
rejects missing, relaxed or extra keys without repairing them. When an existing project carries a
`restricted.*` key this version does not manage (one added by an Incus release newer than this
Provider, or a 7.x key the daemon does not advertise), its semantics are unknown: ensure refuses before
writing and names only the keys, so an operator must remove them or migrate the project explicitly. Unrelated settings such as `user.*` are still preserved.
The change does not delete existing instances or devices to force convergence: when existing instances
violate a tightened limit (for example, containers already in a VM-tier project), the daemon refuses
the update and the Provider fails closed.

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
are `credential_rotate`. Rotation must not recreate running instances or replace consumer certificates.
The disposable-VM lifecycle fixture now requires overlapping old/new management access, old-certificate
revocation and unchanged guest lifetimes. The new case has no complete native result as of 2026-09-23;
helper unit tests and cross-compilation are not acceptance of that requirement.

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

The 2026-09-22 isolated tests passed the default container tier's two-lease lifecycle, actual btrfs
root-disk exhaustion and selected direct forbidden mutations. The real distrobuilder `lab-r11` image
and Forgejo success, failure, controller SIGTERM, and SIGKILL recovery with retained state also passed,
including reclamation. These results do not cover VM, ARM64, ZFS, the complete device/dual-stack matrix,
lost state volumes, signed publication or production ingress. The 2026-09-23 explicit proxy-fence fix
passed local and designated Linux-host Provider regressions; its new proxy/management-rotation native
cases remain unexecuted. Distribution-daemon evidence is not full 7.3.0 platform acceptance.
The status remains `developing`.

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
| isolation tier (a VM lease cannot create containers, and vice versa) | daemon (`limits.containers` / `limits.virtual-machines`) | yes |
| image fingerprint allowlist | consumer's shared client | **no** |

Even then, a compromised consumer can only boot an unplanned image inside **its own restricted,
quotaed, device-less** project, so every other constraint still bounds the blast radius.

> [!NOTE]
> The upstream main configuration reference was reviewed on 2026-09-10: image-server domain restrictions
> and project image isolation are not per-fingerprint allowlists. Reading the 7.0.1 and 7.5.1 source on
> 2026-09-26 showed that a non-empty `restricted.images.servers` also rejects creating instances from local
> project images and does not gate simplestreams copies, so it cannot carry this constraint; the Provider
> removes it (see "Network and profile" above). Measured on Incus 7.0.1 on 2026-09-27 (container lifecycle
> r11): the shared client refuses a fingerprint outside the allowlist, while the lease certificate itself can
> import that image into its own project and create an instance from it with the lease profile. The "no" row
> above is observed behaviour, not inference.

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
`modules/incus/images/catalog.json`. Back up the archive and trusted history independently. Local supply,
Provider import and guarded prune have code implementations; signed distribution, real guest startup and
destructive prune acceptance remain incomplete. The shipped catalog stays empty; M12/M13 remain unaccepted.

## Complete release bundles and cancellable supply (2026-09-21)

Export a verified archive into the Provider's `images/` layout with explicit history and a **new** destination:

```sh
go run ./cmd/incus-image-artifacts bundle \
  --archive "$ARCHIVE_DIR" \
  --previous-catalog "$TRUSTED_PREVIOUS_CATALOG" \
  --output-dir "$NEW_RELEASE_DIR/images"
```

The parent `NEW_RELEASE_DIR` must already exist. Only an actual first release may replace the history
argument with `--first-release`; both cannot be supplied. One archive lock covers history verification
and export of every committed revision/architecture/interface. Bytes go under
`artifacts/<catalog>/<name>/<revision>/<architecture>/<interface>/`, and `catalog.json` is written last.
Empty archives, missing history, corrupt objects and unified artifacts are refused before output.
Split files retain their original bytes and are never rebuilt. Writes use pinned directory handles,
rechecking copied size and SHA-256; a replaced destination cannot be reported as successful.
Failure can leave a private candidate directory for explicit inspection. Retrying must use a new
destination rather than adopting an existing candidate.

`scripts/ci/incus-image-release-build.sh` checks explicit history and a fresh output directory before
building, then calls the same bundle entrypoint. It no longer exports only the current two targets
while omitting historical artifacts. The tool returns only count and catalog digest on stdout. It
does not sign, connect to Incus, offer a download URL or inline base64; release trust remains the
responsibility of the publication process.

Core validates each frozen reference/target/fingerprint/recipe before deduplicating identical physical
bytes shared by runtime names or named revisions. Deployment bindings do not change. The split-only
path rejects unified descriptors and applies a shared 1 MiB supply-JSON limit. Hashing and copying use
the apply cancellation context; cancellation cleans partial copies and temporary staging. Synthetic-byte
regressions cover archive-to-staging integration, history, aliases, directory replacement and mid-copy
cancellation, not actual guest boot.

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
The dedicated rotation command remains pending.


## Host side of HTTP publication and port bindings

HTTP publication does not go through this Module's Provider operations: the Provider only writes the ACL ingress
rule for the frozen publication ports. The consumer writes request files into `HTTP_REQUEST_DIR`, and the mediator
inside anasd checks them and writes the `compute-http/` subdirectory of Traefik's dynamic directory; the full
semantics are in the [compute Contract technical notes](../../../contracts/compute/docs/technical.en.md#http-publication).

hostd carries the host side. Everything is installed through the `incus.configure` two-step confirmation and
removed by `incus.uninstall`:

| Artifact | Contents | Rewritten by |
| --- | --- | --- |
| FORWARD static rules (`anas-lease-forward-1`-`6`) | Appended after Docker's rules: lease bridge forwarding is left to the ACL; from a lease bridge to a Docker bridge only connections Docker translated are admitted (`INCUS-R-126`) | configure and boot restore only |
| address set `anas-traefik` | The Traefik containers' current addresses, which hostd reads from Docker itself | sync action `incus.traefik.sync`, triggered by anasd at start and on Traefik container starts (`INCUS-R-118`) |
| `/var/lib/anas/incus-host/network.json` | The port binding range the operator approved; the incus Hook reads it for Core (`INCUS_PORT_BINDING_RANGE`) | configure only |
| nft table `inet anas_incus_ports` | The Docker-style chain: traffic addressed to the host itself (`fib daddr type local`, loopback excepted) is DNATed through four port maps | the chain by configure; the maps only by the sync action `incus.ports.sync` |
| systemd units `anas-port-{tcp,udp}@<port>.{socket,service}` | One pair per binding in effect: an `Accept=no` socket holds the port and a resident `sleep infinity` (`DynamicUser=yes`) keeps it | the sync action, taking new holds before releasing old ones |
| `anas-incus-network.service` | Runs `anas-hostd --restore-network` at boot, after Docker and before Incus | configure only |

`incus.ports.sync` takes no parameters: hostd reads the active deployment of every workspace registered in
`/etc/anas/anasd.yml`, the frozen `compute_network` and the slot addresses resource state records, and checks every
entry -- protocol and ports, the approved range, a target inside the subnet of its own `lease*` bridge that is not
the network, gateway or broadcast address, and a port no other binding, host process or Docker publication holds
(`INCUS-R-158`). It enables holds for new bindings first, replaces the four port maps in one nft transaction and
reads them back, and only then disables holds no binding needs. An entry that fails a check does not take effect and
becomes a host runtime issue (`/var/lib/anas-hostd/runtime-issues.json`). The entries in effect are written to
`/var/lib/anas/incus-host/ports.json`; at boot the hold units start with `sockets.target`, before Docker, and
`--restore-network` then restores the maps from that file, leaving out and recording any binding whose hold did not
come up (`INCUS-R-161`). anasd triggers the sync at start and after every deployment activation; it also checks the
bindings in effect read-only every minute and on container starts, and records a lost hold, a Docker publication
conflict or drifted maps in the workspace's runtime issues without repairing anything (`INCUS-R-162`).

On a host without hostd the incus Hook publishes `INCUS_PORT_BINDING_BLOCKER` (`hostd_missing`,
`host_not_configured` or `remote_daemon`), and an apply that declares port bindings fails during Core preparation
with the reason (`INCUS-R-164`).

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

## Console confirmation for image prune

The interface consumes the actual public job DTO (`kind`, `mutating`, workspace/id and result),
not internal `job.action`. The displayed plan must match its approval binding's schema,
workspace, plan/state digests and deletion set. A valid empty inventory displays no changes and
cannot execute. Changed input, plan expiry or disposal cannot reuse prior consent. Confirmation
tokens are not exposed in public state, and uncertain apply results are never retried automatically.
Missing public fields or inconsistent bindings fail closed rather than relying on type assertions.

## Isolated daemon storage validation (2026-09-21; production disabled)

The host provisioning client no longer misclassifies synchronous HTTP 201 creation as failure. Only POST
with a complete successful synchronous envelope receives this allowance; reads, async waits and independent
resource readback remain strict. Actual isolated Incus 6.0.5 testing reproduces the old client's failure and
passes create/read/delete with the fix. The Provider repeatedly rejects dir/missing pools, reads a missing
project without mutation, rejects a wrong server pin and preserves project/network/profile/trust inventory.

The native entry point uses distribution binaries in private namespaces with tmpfs state. It installs no
default service, does not admit dir as a quota backend, creates no guest and does not use existing Docker.
Actual btrfs/zfs limits, container/VM/one-job lifecycles, 7.3.0 and automatic installation remain unverified.
See the [Chinese host design](../../../docs/architecture/incus-host-provisioning.md), section 7.16.

## Bridge dependency and real container lifecycle (2026-09-22)

The declarative host recipes explicitly include `dnsmasq-base`, so `--no-install-recommends` cannot
omit the Incus bridge runtime dependency. The first Ubuntu 26.04 / Incus 6.0.5 run could not create
its bridge; supplying that dependency allowed the real Provider's repeated two-lease ensure/inspect
and the shared client's container lifecycle to pass. Preinstalled helpers are not adopted or removed.

`test-env/scripts/server-incus-lifecycle-e2e.py` accepts only a disposable QEMU VM with an exact
cloud-init identity, no Docker and initially empty Incus inventory. Real btrfs volumes, restricted
certificates and identically named instances in two leases exercise stdin, disk exhaustion, independent
deletion after cancellation and idempotent cleanup. The measured minimal native fixture is not a
released Runner image or a production catalog entry. Distrobuilder, ZFS, VM-tier workloads, one-job
execution and production ingress remain separate acceptance gates. See the
[Chinese host design](../../../docs/architecture/incus-host-provisioning.md), section 7.18.

Since 2026-09-26 the same script takes `--interface container|vm`. The VM tier needs nested KVM inside the lab
VM; its fixture starts from an upstream Debian VM image, receives the same fixture program through the agent,
and is published and exported as a measured VM fixture with a 4 GiB root (still not a product image). Both
tiers share one matrix plus device and low-level refusals (containers: unix-char, unix-block, privileged,
`raw.lxc`; VMs: pci, `raw.qemu`), `typical-job-wall-time` timings and a version-aware VM nesting check.
`server-incus-network-e2e.py` uses a separate network namespace as upstream and verifies that leases with
IPv6 enabled and disabled both leave masqueraded. Each guest then forges off-subnet sources on both
families, handed straight to the bridge by static neighbour entries; upstream nft counters must stay at
zero. The IPv6 lease must also show the forged IPv6 packet reaching the lab VM's routing stack (so the
fence is what stopped it), and one deliberate drift (an extra allow-all rule) must leak visibly, make the
Provider's `inspect` report not ready, and stop leaking after `ensure`. The host-side owner script `server-incus-lifecycle-lab.py`
runs all three in one disposable VM; a `hold-*` generation keeps the VM for diagnosis but never counts as
acceptance. Results and the defects found are in the
[two-tier lifecycle review](../../../dev-docs/reviews/2026-09-26-incus-vm-tier-lifecycle.md) and the
[source fence review](../../../dev-docs/reviews/2026-09-27-incus-source-fence-acl.md).

## Native default Runner copy validation (2026-09-22)

The frozen Runner executable is at `sources/forgejo-runner` relative to the recipe directory. The default
copy generator now names that path. Actual distrobuilder 3.2 rejected the old bare name; the corrected
squashfs contains the exact input bytes. Recipe bytes changed, so existing revisions must not be replaced.
An actual cancelled build also refused a same-revision retry without changing its attempt record.

The complete Debian/Podman bake did not finish. The small native pack control is a synthetic rootfs, not
a released Runner image. The new baked-image gate requires real Provider import, complete lease readback,
shared-client guest startup, one-job options and access by runner-agent to a rootless Podman API. That
complete gate was not executed on a finished image; one-job and signed publication remain outstanding.
See the [Chinese host design](../../../docs/architecture/incus-host-provisioning.md), section 7.19.

## Runner build recovery and diagnostics (2026-09-22)

Release-side diagnostics retain bounded, fixed stage labels rather than raw distrobuilder output;
incomplete revisions remain protected against implicit rebuilding. The default Runner configures its
resolver symlink through a guest-boot tmpfiles rule, not a build-chroot replacement, and declares the
systemd init package explicitly. See the
[image supply design](../../../docs/architecture/incus-image-supply.md).
