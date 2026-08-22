# VersityGW S3 gateway technical implementation

This document describes the first POSIX-backend implementation. See the
[English README](../README.en.md) for configuration and client operations.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `1.7.0-r3` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required Modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | HTTPS reverse proxy |
| `object_storage` | provided Capability | `s3` |
| `object_storage` | provided Contract | `1.0.0` / `s3` |

The Capability publishes normalized service-level connection information. The Contract provider
manages per-Resource IAM users, buckets, and credentials.

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_versitygw` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-versitygw:1.7.0-r3` | `default` | 2 |
| `anas_versitygw_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-versitygw:1.7.0-r3` | `default` | 1 |
<!-- generated:compose-topology:end -->

The resident `anas_versitygw` service and one-shot `anas_versitygw_provision` job join the external
Traefik network and publish no host `ports`. Traefik routes only the S3 listener on port 7070 by Host,
without path rewriting or ForwardAuth. The object bind mount is
`${DATA_PATH}/versitygw/objects:/data/objects`; IAM uses `${DATA_PATH}/versitygw/iam:/data/iam`.
Admin listener 7071 has no Traefik label and is reachable only by lifecycle jobs on the container
network. `/_anas_health` contains an
underscore, which legal S3 bucket names cannot contain, so it does not reserve a usable bucket.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `versitygw.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `s3` | `static` | `VERSITYGW_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Domain prefix for the S3 HTTPS endpoint |
| `versitygw.read_only` | bool | — | `false` | `static` | `VERSITYGW_READ_ONLY` | no | no | no | yes | `container_recreate` | Enable the upstream global read-only guard |
| `versitygw.region` | string | `length: 1..64`; `pattern: ^[A-Za-z0-9][A-Za-z0-9._-]*$` | `us-east-1` | `static` | `VERSITYGW_REGION` | no | no | no | yes | `container_recreate` | SigV4 signing region |
| `versitygw.root_access_key` | string | `length: 3..64`; `pattern: ^[A-Za-z0-9._-]+$` | `ANASROOT` | `static` | `VERSITYGW_ROOT_ACCESS_KEY` | no | no | no | yes | `container_recreate` | Access-key identifier of the root S3 credential |
| `versitygw.root_secret_key` | string | `length: 16..128` | — | `generated` | `VERSITYGW_ROOT_SECRET_KEY` | no | yes | yes | yes | `container_recreate` | Secret key of the root S3 credential |

`module.yml` owns the inventory. `read_only` maps directly to `VGW_READ_ONLY`; the region must match
the client's SigV4 request. Endpoint, container name, and object path are private Hook derivations.

## Capability output ABI

The Hook publishes endpoint, region, access key, secret access key, and path-style under the
`ANAS_OBJECT_STORAGE_S3_` prefix. Runner validates the record after provider calculate and creates
`ANAS_OBJECT_STORAGE_BINDING__<MODULE>__*` for every bound consumer. A consumer declares only
`name: object_storage`: no provider, selector, or `config.consumes` is needed, and the provider-side
namespace is not visible to it.

The binding `SECRET_ACCESS_KEY` belongs to the target consumer and remains sensitive; unrelated
Modules and other consumers cannot read it. The value is still one shared root credential, not a
per-bucket principal or permission boundary.

## Contract Resource lifecycle

The `object_storage/s3` Contract provider runs through `compose run --rm --no-deps --no-TTY
anas_versitygw_provision`. `ensure` waits for the private Admin API and then:

1. creates a `user` principal or reconciles the existing principal to the runner's desired secret;
2. creates the bucket with that principal as owner;
3. fails closed if an existing bucket belongs to another principal instead of taking ownership;
4. persists `anas.resource-state/v1` with only a Secret Store reference.

`inspect` verifies the user role and bucket owner. `rotate_credential` updates only that principal's
secret. Contract `delete` is optional and is not implemented: removing a declaration records
`retained` and never empties or deletes objects implicitly. Runner publishes
`ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__*` only to declaring consumers. An undeclared
module triggers no operation and receives no credential.

## Runtime identity and filesystem boundary

The derived image is pinned to `ghcr.io/versity/versitygw:v1.7.0` and adds only `su-exec` plus the
ANAS entrypoint. Alpine's small `su-exec` utility directly replaces PID 1 while dropping identity,
avoiding a custom privilege-switching implementation or a root shell parent. Root enters the minimal
initialization phase with only `CHOWN`, `FOWNER`, `SETUID`,
and `SETGID`. The entrypoint creates/fixes the object and IAM mount roots without recursively chowning the object tree,
then execs upstream as `1000:1000`; the business process is non-root. The root filesystem is read-only,
`/tmp` is a bounded tmpfs, and `no-new-privileges` is active.

Mode 0700 and umask 0077 restrict the dedicated backend to the gateway runtime identity. This is not
a Samba-shared transparent file tree; direct host changes bypass S3 authentication and metadata.

## Identity, management, and sessions

The S3 data plane accepts SigV4 root or per-Resource AK/SK credentials. Internal IAM and the private
Admin API exist only for the Contract provider. There is no WebUI, public management surface, OIDC,
LDAP, groups, STS, password writeback, or browser session. Flat-file IAM contains plaintext JSON fields
and relies on its 0700 directory and workspace backup boundary; it must not be edited around Resource state.

## Secret lifecycle

When neither an explicit `VERSITYGW_ROOT_SECRET_KEY` nor an existing Secret Store value is present,
`calculate` reads 32 bytes from `crypto/rand` and hex-encodes them. The Secret Store and environment
use the same logical key; repeated rendering reuses it. Runner writes the sensitive Hook patch to
`.anas/secrets.yml` (0600); ordinary list, plan, lock, deployment manifests, and Hook errors omit it.

Compose maps the private Module key to upstream `ROOT_SECRET_KEY`, keeping clear text out of images,
documentation, and argv. Explicit configuration is not copied to generated Secret Store state. Root
credential changes currently recreate the container and do not claim transactional online rotation.
Provider-neutral and per-consumer aliases of the same value inherit sensitive provenance and stay out
of plan and lock output.
Each Resource secret has a stable `RESOURCE_<CONSUMER>_<ID>_SECRET_ACCESS_KEY` key whose Secret Store
owner is the consumer. Deployment manifests and Resource state persist only that reference.

## Database, persistence, and recovery

There is no database Contract. The recovery set is the object directory, IAM directory, Secret Store, configuration,
lock, and active deployment metadata at one point in time. `VGW_VERSIONING_DIR` is not enabled; a
workspace snapshot is not S3 object versioning or object lock.

## Environment ownership

Explicitly consumed:

- `TRAEFIK_BASE_PORT`

The private prefix is `VERSITYGW_`; five exact `ANAS_OBJECT_STORAGE_S3_*` Capability fields are
exported. Compose's `ROOT_*` and `VGW_*` values remain upstream adapters inside this service.
Runner-owned `BASE_DOMAIN`, `DATA_PATH`, and `CONTAINER_PREFIX` participate in Hook derivation.

## Hooks, changes, and rollback

The Hook implements only side-effect-free `calculate`: it generates/reuses the Secret, derives
hostname, domain, endpoint, object path, and IAM path, and publishes normalized S3 outputs. Runner then projects
consumer bindings. Other phases return an empty response. All five user
parameters apply through container recreation. A rollback requires matching Secret Store and data
snapshot state; rolling back a credential file alone is invalid.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go): derivation, stable Secrets, explicit values, Module isolation;
- [`docker-compose.yml`](../docker-compose.yml): pinned image, Traefik, health, and mount;
- [`anas-entrypoint.sh`](../versitygw/anas-entrypoint.sh): mount initialization and privilege drop;
- [`provision.sh`](../providers/object_storage/provision.sh): user, credential, and bucket-owner reconciliation;
- `go test ./modules/versitygw/hook`: Module unit tests;
- `go test ./internal/runner -run ObjectStorage`: name-only binding, per-Resource isolation, state, and Secret boundaries;
- M2 real S3/recovery scripts are still pending in the implementation plan.

## Current limitations

Only a core S3 PoC subset is claimed, not the full AWS API. Capability consumers still share the root
credential. Resource consumers have an isolated bucket and long-lived credential, but no STS, quota,
or fine-grained policy. WebUI, public Admin API,
versioning, object lock, notifications, replication, virtual-host style, and real HA are outside the
first release. Status remains `developing` until real client, recovery, and upgrade evidence exists.
