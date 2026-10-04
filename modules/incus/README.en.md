# Incus compute provider

Turns a separate Incus host into a `compute` contract provider: at apply time it builds a restricted
project, quotas and a dedicated certificate for each consumer, then stays out of instance creation and
destruction entirely.

The system-container tier permits inner OCI namespaces through its fixed provider-owned profile.
The outer container remains unprivileged, and host devices, host-path mounts and raw configuration
remain denied. This is distinct from nested virtual machines and does not itself prove the inner
engine is rootless. Account for the shared-kernel attack surface; select the explicit VM tier for
cross-trust or untrusted workloads. No consumer nesting switch or automatic rewrite/restart of old
instances is introduced.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `incus` |
| Version / revision | `7.3.0-r2` |
| Status | `developing` |
| Category | `compute` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## What this module does and does not do

It delivers a **fence**, not a machine. `ensure` creates a restricted project at apply time, writes the
quota onto the project, and registers the consumer's client certificate as a restricted certificate
bound to that project alone. From then on the consumer uses that certificate to talk to the Incus
daemon itself and to create and destroy one-shot instances. ANAS is not on that runtime path.

It therefore does **not**:

- install, configure, or host the Incus daemon; the daemon runs on a separate KVM-capable host;
- require the ANAS host to support virtualization, or mount host virtualization devices or a Docker socket;
- expose an HTTP service, a Traefik route, or a host port itself; HTTP publication of lease instances is
  written as Traefik routes by the mediator inside anasd and port bindings take effect through hostd, see
  "Lease network and publication" below;
- proxy instance `create`/`start`/`exec`/`delete`, or hold a consumer's one-time secrets.

## Module, capability and contract dependencies

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `compute` | Provided contract | `1.0.0` / `incus_vm` |
| `compute` | Provided contract | `1.0.0` / `incus_container` |

There are no module dependencies and no provided capability: this module is only ever called by the
Runner through contract provider operations.

## Prerequisite: prepare the Incus host first

> [!NOTE]
> This section describes the **advanced path**: pointing at an Incus host you run yourself, outside
> ANAS.
>
> The default path should be ANAS installing the daemon locally and issuing certificates itself, with
> the user doing none of this. That automation is not implemented yet -- see the Chinese design note
> `docs/architecture/incus-host-provisioning.md`. Until it lands this is the only available path,
> which is part of why this module is still `developing`.

For a self-hosted daemon this happens outside ANAS; the module only consumes the result.

```bash
incus config set core.https_address :8443
```

Fetch the daemon's server certificate, which will be pinned:

```bash
openssl s_client -connect INCUS_HOST:8443 </dev/null 2>/dev/null | openssl x509 > server.crt
```

Issue an administrative certificate for ANAS **provisioning** and add it to the trust store. Only this
module holds it; it creates projects and registers consumer certificates, and is never handed to a
consumer:

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 -subj "/CN=anas-provisioner" -keyout admin.key -out admin.crt
```

```bash
incus config trust add-certificate admin.crt
```

Root disks require a `Created` btrfs or zfs pool. This provider refuses dir and other drivers pending
capability verification, so configured `limits.disk` alone is not reported as enforced disk capacity.
It does not convert pools or migrate existing data.

## Minimal configuration

All four are required. If any is missing the hook refuses during apply rather than failing halfway
through provisioning:

```yaml
modules:
  incus:
    endpoint: https://incus.example:8443
    server_certificate_b64: "<base64 of server.crt>"
    admin_certificate_b64: "<base64 of admin.crt>"
    admin_key_b64: "<base64 of admin.key>"
```

Producing a base64 value:

```bash
base64 -w0 server.crt
```

## Use from another module

Consumers do not depend on `incus`. They declare the contract and the resource in their own manifest:

```yaml
dependencies:
  contracts:
    - name: compute
      version: ">=1.0.0 <2.0.0"
      selected_by: isolation
      interfaces: [incus_container, incus_vm]
      default: incus_container
resources:
  requires:
    - id: runners
      contract: compute
      binding: isolation
      spec:
        sandbox: anas-forgejo-runners
        instance_prefix: anas-fj-
        quota: {max_instances: 8, cpu: 4, memory_mib: 8192, disk_gib: 40}
        image_allowlist: [{fingerprint: "<64hex>"}]
        credential: {policy: generated}
        deletion_policy: retain
```

The Runner generates a certificate for that consumer, calls this module's `ensure`, and publishes the
endpoint, project, instance prefix, image allowlist, quota and certificate into the consumer's
container. The field list is in the
[compute contract technical documentation](/en/reference/module-contracts/compute-technical).

## Lease network and publication

Every lease has its own bridge (`lease` plus 10 hex digits) and a network ACL whose default is drop in both
directions. The consumer's lease declaration picks the egress tier (`internet` by default, `internet_lan`,
`internet_lan_host`, `modules_only`), whether to reach ANAS Modules through Traefik (`module_access`), whether
instances of the lease may reach each other (`intra_lease`), and the ingress tier (`none` by default,
`published`). A `published` lease may declare HTTP publication (Traefik terminates HTTPS and forwards to an
instance) and port bindings (a host port forwarded unchanged to a slot instance). Fields and semantics are in
the [compute Contract technical notes](/en/reference/module-contracts/compute-technical).

This Module needs Incus 7.0 or later: the ACL names global address sets (the `network_address_set` API
extension) to exclude other leases and admit Traefik. The operator configures only two values:
`lan_extra_subnets` (extra LAN subnets, see the parameter table) and the port binding range approved in the
`incus.configure` two-step confirmation (default 30000-32767).

## Two isolation tiers

| Interface | Instance form | Boundary |
| --- | --- | --- |
| `incus_vm` | QEMU/KVM virtual machine | separate guest kernel; the project refuses containers with `limits.containers=0` |
| `incus_container` | unprivileged system container (LXC) | shares the host kernel; the project forces `unprivileged` and refuses VMs with `limits.virtual-machines=0` |

Schemas and operation semantics are identical, so choosing a tier is a deployment decision.
**`incus_container` is the default**: a NAS host is not guaranteed to have KVM, and a default that
needs it would make the service uninstallable on the hardware this product targets. A VM is an
explicit upgrade, never a silent downgrade -- the provider does not quietly swap a VM for a container
because the host is short on memory or lacks KVM; it fails and says why.

## Identity, users and groups

Not applicable. This module has no users, no web UI, no IAM integration and no sign-in entry point. Its
only identities are two TLS certificates: one administrative provisioning certificate held by the
module, and one restricted per-consumer certificate bound to that consumer's project, generated by the
Runner and held by the consumer.

## Administrator sign-in and IAM recovery

Not applicable; there is no sign-in entry point. An IAM outage does not affect this module, because
provisioning uses mTLS and has nothing to do with IAM.

If the administrative certificate leaks or is due for rotation, revoke it on the Incus host, issue a
replacement, and update `admin_certificate_b64` and `admin_key_b64`:

```bash
incus config trust remove <fingerprint>
```

Rotating the administrative certificate **does not affect running instances**: instances are driven by
consumer certificates, which are unrelated to it.

## Database support

Not applicable. This module has no database and no persistent volume; all state lives in the remote
Incus daemon.

## All configuration parameters

The list below comes from the current `module.yml` and `anas config list`. `Environment variable` is
the rendered module-private key; do not treat it as the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | — | `host` | `INCUS_ADMIN_CERTIFICATE_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Provisioning-only administrative client certificate, never handed to a consumer |
| `incus.admin_key_b64` | string | — | — | `host` | `INCUS_ADMIN_KEY_B64` | no | yes | yes | no: `rotate-incus-admin-credential` | `credential_rotate` | Private key for the administrative certificate |
| `incus.endpoint` | string | `pattern: ^https://[A-Za-z0-9.:_-]+$` | — | `host` | `INCUS_ENDPOINT` | no | yes | yes | yes | `reconcile` | HTTPS address of the remote Incus daemon |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | `host` | `INCUS_IMAGE_ARCHITECTURE` | no | yes | no | yes | `container_recreate` | Explicit guest image architecture on the target daemon; never inferred from the CLI host |
| `incus.lan_extra_subnets` | string | `pattern: ^[0-9A-Fa-f:./, ]*$` | `""` | `static` | `INCUS_LAN_EXTRA_SUBNETS` | no | no | no | yes | `reconcile` | LAN subnets, besides those directly on the host's default-route interface, that the `internet_lan` tiers may also reach; comma-separated CIDRs |
| `incus.server_certificate_b64` | string | — | — | `host` | `INCUS_SERVER_CERTIFICATE_B64` | no | yes | yes | yes | `reconcile` | Pinned daemon server certificate; a mismatch fails outright with no fallback |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | — | `runtime` | `INCUS_STORAGE_POOL` | no | yes | no | yes | `reconcile` | Incus storage pool backing every lease root disk; changing it does not move existing instances |

### Inspect and change

```bash
anas config list incus -w /srv/anas
```

```bash
anas config set incus.image_architecture amd64 -w /srv/anas
anas config set incus.endpoint https://incus.example:8443 -w /srv/anas
```

Do not put certificates or private keys directly in shell argv; set them through the protected
configuration import flow.

## Troubleshooting

Provisioning failures surface in apply output. Common causes:

| Error | Meaning |
| --- | --- |
| `does not match the pinned certificate` | the daemon's certificate changed, or the endpoint points at a different host. Confirm, then update `server_certificate_b64`; do not bypass verification |
| `is not restricted after ensure` | the daemon accepted the write but did not apply `restricted`. Provisioning failed closed and did not go on to register a certificate |
| `must be a Created btrfs or zfs pool` | the pool is missing, unready or uses a driver not yet admitted; prepare a supported pool and plan existing-data migration separately |
| `has no enforced quota after ensure` | the project exists but no quota took effect; also fails closed |
| `is already trusted without a project restriction` | that certificate was previously added to the trust store with global rights. Remove the old entry, then apply again |
| `is scoped to ... not to ...` | the same certificate is already bound to a different project; cross-project reuse is refused |
| `belongs to another lease` | a project with this name already belongs to another lease (typically a second workspace on the same daemon declaring the same sandbox). The provider will not take it over, even after the original lease's certificate is revoked. After review, delete that project or use a different daemon |
| `is also trusted by other restricted certificates` | a restricted certificate other than this lease's can drive the project (only fingerprint prefixes are shown). Once you confirm they are unused, remove them from the trust store and apply again |
| `carries restriction keys this provider does not manage` | an existing project holds `restricted.*` keys this version does not manage (only the key names are shown; typically keys added by an Incus release newer than this provider). After review, remove them by hand or migrate the project explicitly, then apply again |
| `is too low: there currently are ... instances of type ...` | the isolation tier conflicts with instances already in the project (for example, containers in a VM-tier project) and the daemon refuses to tighten it. Deal with those instances first; the provider will not delete them for you |

## Current limitations

The status is `developing`: an isolated Incus 6.0.5 provisioning probe exposed a dir disk-quota gap.
Guest startup and full quota, certificate and two-consumer E2E have not passed; 7.3.0/KVM acceptance
remains separate.
Until then it is not a `release` capability.

`revoke` withdraws the consumer certificate, empties the lease ACL and stops the lease's running instances
(ending established connections); it does not delete the project or instance disks -- the instances inside it
never belonged to this Contract.

## Technical documentation

REST boundary, certificate pinning, idempotence and tests are in the
[technical documentation](docs/technical.en.md).

<!-- generated:localization:start -->
<!-- generated:localization:end -->
