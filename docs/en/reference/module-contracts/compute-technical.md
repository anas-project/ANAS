> This page is generated from the Contract technical documentation. Do not edit it directly.

# compute Contract technical notes

## It delivers a fence, not an instance

`compute` 1.x delivers an **isolation sandbox lease** at `anas apply` time: one restricted project, a
set of quotas enforced on the provider side, a pinned image allowlist, and a client certificate bound
to that project alone. Instance `create`/`start`/`exec`/`delete` are **not** contract operations. They
are runtime behaviour the consumer drives itself, inside the fence.

That boundary follows from the model rather than from convenience. The only runtime for a contract
operation is `compose_run`, executed once by the Runner at apply time as `docker compose run --rm`, and
ANAS has no "module container to Core" channel at runtime. One-shot instances are a per-job hot path
whose initiator is a long-running consumer container. Forcing per-job instance lifecycle through a
per-apply cold path buys one container cold start per operation and leaves no stdin stream for secret
injection.

Responsibilities therefore split as:

| Layer | Mechanism | When | Initiator |
| --- | --- | --- | --- |
| Fence provisioning | this contract's `ensure` | apply, once | Runner |
| Use inside the fence | consumer talks to the provider daemon directly | per job | consumer container |
| Fence operations | Module Command | on demand | administrator |

A restricted project is to `compute` what a database plus role is to `relational_database`: the
provider creates it at apply time, hands over the credential, and leaves the data path.

## Declaration model

Local host provisioning also projects `CONTROL_NETWORK_NAME` and `CONTROL_NETWORK_EXTERNAL` under
each consumer's private Resource prefix so the Provider and compute clients join the same managed
control bridge. The name comes from the Provider's validated host connection, not a consumer choice.
Without a local control bridge only `false` is projected, never an empty required network name.
This internal environment projection does not change the Contract request/result schema or expose
the daemon management private key. The business network keeps its default gateway and host
provisioning, not consumer Compose, owns the external network's lifecycle.

`compute` 1.x offers the `incus_vm` and `incus_container` interfaces. Their schemas and operation
semantics are identical; only isolation strength differs, as a system container shares the host kernel
while a VM has its own guest kernel. Which tier applies is a deployment decision selected by the
consumer's `binding` parameter, and the provider never downgrades automatically.

```yaml
dependencies:
  contracts:
    - name: compute
      version: ">=1.0.0 <2.0.0"
      selected_by: actions_isolation
      interfaces: [incus_container, incus_vm]
      default: incus_container
resources:
  requires:
    - id: runners
      contract: compute
      binding: actions_isolation
      spec:
        sandbox: anas-forgejo-runners
        instance_prefix: anas-fj-
        quota: {max_instances: 8, cpu: 4, memory_mib: 8192, disk_gib: 40}
        image_allowlist: [{fingerprint: "<64 lowercase SHA-256 hex characters>"}]
        credential: {policy: generated}
        deletion_policy: retain
```

A module that drops these two declarations takes no part in resource resolution and receives no
sandbox credential.

## Lifecycle

The Runner generates a stable client certificate and private key per resource, stores them as a single
secret, and calls the provider's idempotent `ensure` before the consumer starts. The provider must:

1. verify the endpoint is reachable and its server certificate matches the pinned fingerprint, failing
   closed otherwise;
2. ensure the project named by `sandbox` exists with `restricted=true` and belongs to this lease alone:
   a project that belongs to another lease, or that another restricted credential can drive, must be
   refused rather than taken over (the sandbox name repeats in every workspace and proves nothing);
3. write `quota` onto the project's own limits rather than trusting the caller to stay within them;
4. create a dedicated bridge in the default project and a lease profile in the consumer project, then **read the profile back**
   and assert it carries exactly one root disk (on the managed pool, with no host source) and one NIC
   attached to that managed network;
5. register the Runner's client certificate as a restricted certificate bound to that project only,
   never using a global administrative credential;
6. converge on repeat invocation, producing no second project, network, or trust entry.

## The provider owns the profile and the network

Incus bridges are Provider-owned resources in the default project. Consumer projects disable
`features.networks` and set `restricted.networks.access` to exactly their bridge, with managed NICs.
Instances, profiles, quotas and certificate scope remain in each consumer project. Different bridges
alone do not establish packet-level isolation; real-host validation remains necessary.


An instance's numeric limits come from the consumer; **everything else comes from the profile** --
which storage pool its root disk lives on and what it is plugged into. The profile name is fixed by
the contract as `anas-lease`, and a consumer may reference it but never write it: a caller able to name
a profile is a caller able to point at one somebody else authored, which is exactly what provider
ownership prevents.

Each lease gets its own managed bridge, so one consumer's instances do not share an egress path with
another's. The network name is not the sandbox name: a Linux bridge interface is capped at 15
characters while `anas-forgejo-runners` is already 20, so it is derived by hashing the sandbox name --
short, stable across applies, and distinct between leases.

A guest may leave only with its lease network's own addresses. Egress NAT rewrites sources inside the
lease subnets only; a forged off-subnet source is not rewritten and, if forwarded, reaches the outside
under that forged identity. The Provider must therefore have the host drop such packets and include
that constraint in `ready`; the Incus Provider does so with a Provider-owned bridge network ACL (see the
Incus Module technical documentation).

Every `ensure` **replaces** the profile's devices rather than merging them. A host path attached out of
band is precisely what must not survive an ensure.

`inspect` is read-only and must report `exists`, `ready`, `restricted`, and `quota_enforced`
separately: a project that exists but is unrestricted or unquotaed is a fence with no fence in it, and
that state has to be visible on its own.  `revoke` is an optional 1.x operation.

### Ending a lease

When a consumer is removed, or the capability that requested the lease is switched off (an
`enabled_by` toggle, for example), the target deployment no longer declares the resource. Retaining a
database or bucket retains data; retaining a compute lease the same way would retain a still-trusted
access grant. After the new deployment starts, Core therefore runs `revoke` through the **previous
deployment's frozen Provider artifact**: the lease's restricted certificate is withdrawn while the
project, its instances and its network stay in place (`INCUS-R-014`). An apply that removes the Provider
in the same step can still revoke through the old artifact. A Provider that declares no `revoke` is
recorded as `unsupported` with a warning, and its certificate stays trusted.

A failed revocation is never recorded as done. By default it aborts the activation and restores the
previous deployment; only an explicit `--allow-risky` accepts an unconfirmed revocation (a daemon that
is gone for good, say), recording `revocation: unconfirmed` on the resource state with a warning. The
state's `status` stays `retained`; `revocation` is one of `confirmed`, `unconfirmed` or `unsupported`.
Declaring the same resource again lets `ensure` re-register the stable certificate, and the ownership
markers show it is still the same lease.

`deletion_policy` accepts only `retain`: no Provider deletes a lease project, and accepting `delete`
would promise a cleanup that never happens.

### The result schema describes the lease Core records

As with the other resource contracts, an operation's `result_schema` describes what Core records and
projects once the operation succeeds, not the Provider's stdout: the Runner judges a Provider operation by
its exit status alone. `ensure`'s `sandbox-result.yml` matches the lease facts in resource state and the
private projection above. The endpoint and pinned server certificate come from the Provider Module's own
exported configuration, `<PROVIDER>_ENDPOINT` and `<PROVIDER>_SERVER_CERT_B64`, and the optional
`<PROVIDER>_CONTROL_NETWORK_NAME` names the control bridge consumers attach to (`<PROVIDER>` is the
upper-cased Provider Module name). Core refuses to publish a lease when a required key is missing. Whatever
a Provider prints for `inspect` or `revoke` is for human diagnosis and never enters Core state.

The ready lease is published into the consumer's private namespace:

```text
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SANDBOX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INSTANCE_PREFIX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__PROFILE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT_FINGERPRINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_KEY
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__LEASE_SECRET
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__IMAGE_ALLOWLIST
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MAX_INSTANCES
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CPU
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MEMORY_MIB
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__DISK_GIB
```

`ENDPOINT`, `SERVER_CERT`, `CLIENT_CERT` and `CLIENT_KEY` are all sensitive and belong only to the
target consumer. Deployment manifests and resource state keep credential references to the secret
store, never plaintext credentials. Publicly verifiable certificates do not authorize exposing this
deployment's connection configuration in logs or public manifests.

## Multi-consumer isolation

One lease owns exactly one project and one certificate; two consumers share neither.
**Cross-consumer isolation rests entirely on the project**: a restricted certificate cannot leave its
own project, so two leases on the same provider cannot see each other's instances.

The instance prefix solves a different problem and should not be confused with that one. It marks which
instances **inside a single project** are ANAS-managed. An operator may have created instances in the
same project by hand, and the janitor has to recognise which ones are not its to reclaim. The prefix is
a consumer-side filter, not a security boundary.

What matters is that the cross-project boundary is enforced by the **provider daemon itself** rather
than observed voluntarily by consumer code. The certificate's scope lives in the daemon's trust store
and the quota lives on the project. If a consumer container is fully compromised, the daemon still
refuses the out-of-bounds request.

## Two kinds of fingerprint

Incus calls any SHA-256 hex digest a fingerprint, and this contract uses the word in two **unrelated**
places. Do not confuse them:

| Field | SHA-256 of what | Purpose |
| --- | --- | --- |
| `image_allowlist[]` | **image content** (the FINGERPRINT column of `incus image list`) | pins which images this lease may boot |
| `server_certificate_fingerprint` | **the daemon certificate's DER** | lets the consumer pin the same daemon |

Wherever the word appears below, which one is meant is stated explicitly.

## Security boundary

An image fingerprint identifies image **content**, whereas an alias such as `images:debian/13` is only
a pointer the remote may repoint at new content tomorrow. A pinned image-fingerprint allowlist
therefore closes the set of things a lease can boot at apply time: tags, aliases, and remote URLs are
refused, because otherwise a reviewed lease would drift as the remote publishes updates. The provider owns profiles, networks, and storage pools, and accepts no
caller-supplied devices, raw configuration, mounts, or host sockets.

The contract does not install, configure, or host the provider daemon, and does not require the ANAS
host to support virtualization: it is a client control plane for that daemon. `deletion_policy` accepts
only `retain`; removing the declaration revokes the certificate and keeps the project, see "Ending a lease".

How a one-time secret (a runner token, for instance) reaches a guest is outside this contract. That
happens after the lease is delivered: the consumer uses the shared `computeclient.ExecStdin` directly
against Incus, not a Provider operation. The secret must not appear in argv, environment variables, cloud-init, images, persistent
state, or logs.

### Shared-client credential preparation and subprocess boundary

`New` keeps its existing signature. `NewWithContext` applies caller cancellation and a maximum 30-second
initialization budget to credential preparation, lock waiting and initial CLI connection. Initialization
copies the image allowlist; changing the caller's original slice cannot change the constructed client's
image authority. Directly constructed leases and environment input share endpoint, project, profile,
quota, isolation-tier and fingerprint validation. Guest entrypoints must be distinct canonical absolute
paths; invalid inputs are rejected before file writes. Initialization neither starts nor cleans up guests.

Bounded base64, single X.509 certificates, the server-certificate digest and the matching client keypair
are validated before directories are created. On Linux/macOS, the configuration root and `servercerts`
must be owned by the runtime user with mode 0700. The three credential files, frozen `config.yml` and initialization lock must
be same-user, mode-0600, single-link regular files. Directory descriptors, no-follow opens, exclusive
creation, fsync and readback protect publication. Conflicting contents, unsafe modes, special files and
identity drift reject initialization without truncation, replacement or automatic chmod. A different
identity requires a separate private directory. The lock is never deleted; waits are cancellable and
identical files are read, not rewritten. Private partial files remain for explicit recovery rather than
deleting paths another process may use. Other platforms have no unprotected write fallback.

`config.yml` uses standard JSON encoding for YAML-compatible CLI configuration, fixing `protocol: incus`, the remote,
endpoint, TLS authentication and project. It is checked, committed and read back alongside the credentials.
Old experimental `protocol: lxd` configuration is not overwritten or silently migrated; use a new private
configuration directory. Initialization no longer calls `remote add`: matching files are reused, followed by a read-only instance
listing in the restricted project. Another project or pre-existing configuration is never overwritten.
The initialization lock is created exclusively; an existing entry is opened without a creation flag.
Disappearance therefore fails rather than creating another lock inode. Errors contain only fixed stage
labels and system-error categories, not paths or credentials.

This serializes initialization only; it is not a lifetime credential lock or rotation coordinator.
A successful read-only listing does not replace the Provider's complete fence/profile/quota readiness
checks or establish guest-lifecycle acceptance.

The Incus child receives only a fixed system PATH/locale and this lease's HOME, INCUS_CONF and INCUS_PROJECT,
not other lease secrets, default Incus sockets, proxies or loader overrides. The executable is still
resolved from the trusted consumer's own PATH. stdout is capped at 4 MiB and stderr at 64 KiB; overflow
cancels the child and returns a fixed error without partial output. Diagnostics do not echo stderr/stdin.
`WaitDelay` bounds post-exit pipe waiting, caller cancellation retains its error identity and absent stdin
is closed. These constraints do not establish real guest cleanup after cancellation. Instance inventories
reject `null` and duplicate managed identities; managed filtering uses the same name rules as creation. A successful delete command must
also be followed by a read confirming that the instance is absent. Reading one instance lists the whole lease
project and matches the exact name client-side: the Incus 7.x CLI parses `<remote>:<name>` as a remote with no
filter and returns an empty list (6.0 treated it as a name-prefix filter). The previous form read every instance as
absent on 7.x, so a delete confirmation could pass falsely; the 2026-09-26 Incus 7.0.1 native lifecycle found it.

## Structured declarations and frozen deployment images

Image declarations must be mutually exclusive `{fingerprint: <64hex>}` or
`{catalog: anas, name: <name>, revision: <revision>}` objects. All old string spellings,
mixed/unknown fields, aliases and URLs are refused. The runtime ABI still carries bare fingerprints.

Core resolves images while preparing the deployment, before hooks render and before Provider ensure.
The Provider's `image_architecture` parameter explicitly selects `amd64` or `arm64`; it never defaults
to the CLI host architecture. Catalog data comes only from `images/catalog.json` in the already trusted
Provider bundle, with exact architecture/interface lookup. The shipped catalog is empty: named references
require a release with real catalog entries; no sample fingerprints are treated as usable images.

`spec_from` keeps its string shorthand for scalar parameters. Structured sources declare
`{parameter: actions_runner_image, projection: singleton}` for an object or
`{parameter: agent_runtime_images, projection: values}` for a map. `values` sorts keys and preserves
runtime→fingerprint bindings; `value` passes decoded JSON through. No Module-name branch is used.
Configuration objects cross the existing string ABI as canonical JSON (`format: json_object`).

`deployment.yml` resources freeze `compute_images`: the catalog, each reference, target, image fingerprint,
catalog/recipe digests and optional bindings. Core state retains an append-only `compute-image-history.yml`
so deployment retention cannot erase published version claims. A later apply refuses changed version keys.
Rollback validates the frozen snapshot without reading a newer catalog. Legacy deployments without a
snapshot fail before start/replay. Their metadata remains readable so a new structured apply can replace
them; there is no string execution compatibility path.
The resource state also records the snapshot and keeps credentials as Secret Store references only.

Consumers receive `IMAGE_ALLOWLIST` (CSV digests) and, for a map, `IMAGE_BINDINGS` (JSON runtime→digest).
Forgejo uses its single frozen allowlist entry; AI Agent receives frozen bindings. Provider ensure reads
image metadata in the lease project and verifies exact fingerprint, architecture and image type before
registering a new consumer certificate. Missing images fail; importing identical artifacts, distrobuilder
baking, release distribution and pruning remain unimplemented. Existing consumer trust is not automatically
revoked on a failed re-apply. Unit tests do not establish real daemon enforcement or host acceptance.

## Independent lease naming key

On a new apply, Core generates an independent 32-byte random `lease_secret` for each compute resource.
It stores canonical base64 in a separate `.anas/secrets.yml` record named
`ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__LEASE_SECRET`. The client certificate bundle stays
separate: upgrading an older lease preserves its certificate, and repeated applies or certificate changes
preserve the naming key. Existing empty, malformed or incorrectly owned records fail validation. If a
historical deployment or resource state references a missing entry, apply fails and requires restoring
the original entry rather than silently changing derived URLs.

The matching runtime projection is sensitive and belongs only to its consumer. Forgejo controller/executor
services and the AI Agent orchestrator receive it through Compose; Provider ensure and guests do not.
`config list`, read-only validation Hooks and cross-consumer environment filtering use Secret Store
confidentiality provenance. The `lease_secret` fields in deployment and resource state hold references
only. Frozen deployments without a reference do not generate a key on load or replay; a new apply adds
one. Legacy image strings remain rejected on startup. Backup restores the Store together with the
deployment, preserving the key and HMAC-derived result.

This is a naming key for domain derivation, not request authentication or publishing authority.
It is excluded from credential rotation and `--all`; a module cannot declare it as a rotatable credential.
The dedicated key rotation command and production publication remain unimplemented. Authorization freezing
and experimental mediation are described below. File backup/restore regression tests are not real Incus or HTTP ingress
acceptance.

## Frozen HTTP authorization (runtime closed)

Declaration parsing and deployment preparation were added on 2026-09-12; new-code tests are deferred.
Omitting `spec.ingress` grants no publication authority. A declaration can produce a frozen deployment,
but start/activation explicitly refuses the affected compute consumer. Built-in consumers do not yet
have ingress settings or request-directory mounts. Schema acceptance is not production availability.

On 2026-09-30 the old per-publication permit runtime, the request-directory registration adapter, the mediation
planner and the lab command were deleted. In the new design a request carries `{instance, address, port, label?}`
and the publication mediator inside anasd validates it before writing Traefik route files (`INCUS-R-141`–`R-149`);
see the Incus requirements, section 7septies. The parsing and freezing rules below move to `publish.http` when M11
is implemented.

```yaml
# Inside compute spec; preparation only, startup remains blocked.
ingress:
  allowed_ports: [7000]
  # Default none. Unpredictable URLs are not access control: SNI, Referer and logs
  # can disclose URLs. Do not publish sensitive data or writable services this way.
  auth: none
  domain: {mode: random, prefix: ci}
```

Ports are distinct integers from 1 to 65535, at most 64, sorted when frozen. Unknown fields, null,
string/duplicate ports and non-HTTP protocol declarations are refused. `fixed` uses
`<prefix>.<base_domain>`; `named` uses `<prefix>-<label>.<base_domain>`; `random` computes
HMAC-SHA256 over the exact `workload_id` using the independent lease key and keeps 32 hexadecimal
characters (128 bits). Final labels are limited to 63 characters; prefix limits are 63/61/30 respectively.
Named labels must be lowercase DNS labels; other modes reject labels. Workload IDs are at most 256 ASCII
characters matching `[A-Za-z0-9][A-Za-z0-9._:-]*`, without case or whitespace normalization.

After calculate and before render, Core freezes `compute_ingress`: deployment/lease identity,
project/instance prefix, ports, auth, domain mode, `BASE_DOMAIN`, naming-key reference and, when needed,
ForwardAuth provider/middleware. `forward_auth` requires a resolved consumer capability dependency on
`forward_auth/http` and output owned by that bound provider. Requests cannot override auth. Loading a
frozen deployment compares spec, identity, authentication binding and key reference rather than recomputing from current config.
Resource state can retain the same authorization and key reference; the startup guard prevents creating
an ingress-bearing ready state today.

Preparation checks overlapping lease namespaces, declared Module domains and literal `Host(…)` routes
in the deployment environment. Named/random modes conservatively reserve the entire `prefix-*` space.
Opaque file-provider matchers block preparation. Production still needs actual route-owner inventory
and durable reconciliation; the current checks do not provide those guarantees.

`internal/computeingress` implements strict JSON requests, Linux amd64/arm64 directory-relative reads and in-process
name reservations. `schemas/http-publication-request.yml` accepts only `action` (publish/revoke),
`instance_id`, `workload_id`, `guest_port` and optional `label`; this adds no Provider operation.
The trusted caller binds a directory to an active-deployment grant. Reads accept only flat JSON basenames,
pin inodes using `openat(O_PATH|O_NOFOLLOW)`, reject symlinks, devices/FIFOs and hard links before
opening for reads, then reopen regular files through trusted host `/proc/self/fd` with nonblocking flags. They cap files
at 4 KiB. Duplicate keys, case aliases, null, unknown fields and trailing data fail. Changed inode/content
metadata across a read also fails.

Consumer file-request APIs were added on 2026-09-18; the source and regression tests have not been
compiled or executed. `Client.OpenHTTPPublisher(HTTPPublicationConfig)` explicitly opens this lease's
already-installed private request directory. `HTTPPublisher.PublishPort` checks the exact Running
managed instance and its `user.anas.workload`, port and label, then submits only the existing small
request schema. Configuration is a minimal projection of lease scope, public policy and base domain,
not a complete frozen authorization, middleware, entrypoint or global Store reference. Random-mode
naming keys are delivered separately and do not enter requests or default JSON/formatted output.
`Policy.Host` only predicts a name; the mediator's `Authorization.Host` still validates the full grant.

The returned `HTTPPublication` acknowledges request submission only. `RequestedURL()` does not prove
route loading, TLS, authentication or backend readiness. `UnpublishPort(ctx, publication)` retracts the
original file receipt even after the instance stops or disappears; file removal does not prove network
revocation. Closing the publisher releases local handles without deleting durable requests. The
underlying `RequestWriter` uses a private directory, a cooperative lock, atomic rename and file/directory
fsync. Retained inode handles prevent old receipts from removing a cooperating writer's replacement.
Symlinks, hard links, FIFOs, directory substitution, conflicts and unsupported platforms fail closed.
Automatic projection/mounting, application lifecycle/crash recovery, trusted mediator assembly and
actual publication-state confirmation remain pending. These APIs do not remove the production startup guard.

## Active authorization reads

`deployment.Reader.HTTPAuthorizations` reads Core's existing `.anas/state/lock`, active/state records and
deployment manifest under the shared runtime lock. It requires running/active state, matching activation
time, resource identity, compute/ForwardAuth bindings and frozen images. It does not create workspaces,
perform recovery, run Hooks or read the Secret Store. Inconsistent, inactive, stopped, missing or oversized
metadata fails. The authorization epoch combines the resolved workspace-path digest, deployment ID,
activation time and manifest-byte digest. Manifest changes, deployment switches or restoring elsewhere
invalidate old directory registration; stable naming keys and URLs do not depend on this directory epoch.

