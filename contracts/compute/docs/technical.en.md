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
        image_allowlist: ["<64-character SHA-256 fingerprint>"]
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
2. ensure the project named by `sandbox` exists with `restricted=true`;
3. write `quota` onto the project's own limits rather than trusting the caller to stay within them;
4. create the managed network and the lease profile inside the project, then **read the profile back**
   and assert it carries exactly one root disk (on the managed pool, with no host source) and one NIC
   attached to that managed network;
5. register the Runner's client certificate as a restricted certificate bound to that project only,
   never using a global administrative credential;
6. converge on repeat invocation, producing no second project, network, or trust entry.

## The provider owns the profile and the network

An instance's numeric limits come from the consumer; **everything else comes from the profile** --
which storage pool its root disk lives on and what it is plugged into. The profile name is fixed by
the contract as `anas-lease`, and a consumer may reference it but never write it: a caller able to name
a profile is a caller able to point at one somebody else authored, which is exactly what provider
ownership prevents.

Each lease gets its own managed bridge, so one consumer's instances do not share an egress path with
another's. The network name is not the sandbox name: a Linux bridge interface is capped at 15
characters while `anas-forgejo-runners` is already 20, so it is derived by hashing the sandbox name --
short, stable across applies, and distinct between leases.

Every `ensure` **replaces** the profile's devices rather than merging them. A host path attached out of
band is precisely what must not survive an ensure.

`inspect` is read-only and must report `exists`, `ready`, `restricted`, and `quota_enforced`
separately: a project that exists but is unrestricted or unquotaed is a fence with no fence in it, and
that state has to be visible on its own.  `revoke` is an optional 1.x operation.

The ready lease is published into the consumer's private namespace:

```text
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SANDBOX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INSTANCE_PREFIX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__PROFILE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT_FINGERPRINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_KEY
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__IMAGE_ALLOWLIST
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MAX_INSTANCES
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CPU
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MEMORY_MIB
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__DISK_GIB
```

`CLIENT_KEY` is marked sensitive and belongs to the target consumer alone. Deployment manifests and
resource state keep only secret store references, never plaintext.

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
host to support virtualization: it is a client control plane for that daemon. `deletion_policy: delete`
expresses explicit deletion intent only, and is still handled as retain until Core offers a destructive
resource delete entry point.

How a one-time secret (a runner token, for instance) reaches a guest is outside this contract. That
happens after the lease is delivered, injected by the consumer through the provider's exec stdin
channel, and the secret must not appear in argv, environment variables, cloud-init, images, persistent
state, or logs.
