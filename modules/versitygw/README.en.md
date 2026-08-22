# VersityGW S3 gateway

Provides an S3-compatible API backed by a dedicated POSIX directory. This Module is a single-host
filesystem gateway, not a distributed object store.

## Quick information

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `versitygw` |
| Version / revision | `1.7.0-r2` |
| Status | `developing` |
| Category | `storage` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required Modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | HTTPS reverse proxy |
| `object_storage` | provided Capability | `s3` |
| `object_storage` | provided Contract | `1.0.0` / `s3` |

The Capability publishes one shared root S3 connection record. The Contract + Resource path creates
an independent bucket, AK/SK pair, and persistent state for each declaring consumer. Neither path is
implicitly converted into the other.

## Minimal configuration

```yaml
modules:
  versitygw: {}
```

The default endpoint is `https://s3.<base_domain>:<traefik_port>`, the region is `us-east-1`, and
the access key is `ANASROOT`. Configure clients for path-style addressing.

## Using the capability from another Module

A consumer does not depend on `versitygw`; its manifest only declares:

```yaml
dependencies:
  requires_capabilities:
    - name: object_storage
```

Runner binds the only provider and publishes this private consumer record:

```text
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__INTERFACE=s3
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ENDPOINT=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__REGION=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ACCESS_KEY_ID=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__SECRET_ACCESS_KEY=...
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__PATH_STYLE=true
```

The consumer Hook translates its own binding into application configuration and never reads
`VERSITYGW_*` or provider-side `ANAS_OBJECT_STORAGE_S3_*` keys. All current consumers share the root
credential; a binding does not provide bucket isolation or least privilege.

A consumer that needs a private bucket and credential explicitly declares the Contract and Resource:

```yaml
dependencies:
  contracts:
    - name: object_storage
      version: ">=1.0.0 <2.0.0"
      selected_by: object_storage_type
      interfaces: [s3]
      default: s3
resources:
  requires:
    - id: objects
      contract: object_storage
      binding: object_storage_type
      spec_from:
        bucket: object_bucket
      spec:
        credential: {policy: generated}
        deletion_policy: retain
config:
  defaults:
    object_storage_type: auto
    object_bucket: example-objects
  types:
    object_storage_type: {enum: [auto, s3]}
    object_bucket: string
```

Runner derives a unique access key, generates a separate secret, and publishes
`ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__OBJECTS__*`. A module that omits these declarations creates
no bucket or user and receives none of these environment values.

## Identity, users, and groups

S3 requests use AWS Signature Version 4 access and secret keys, not browser login, OIDC, LDAP, or
Samba identities. Capability consumers use the shared root credential. Resource consumers use an
independent internal-IAM `user` principal that can see only its assigned bucket. The Admin API listens
only on private container-network port 7071 and has no Traefik route or host port. WebUI, automatic
bucket-policy orchestration, and STS credentials remain unsupported.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported / not applicable |
| IAM / OIDC / SAML | unsupported / not applicable |
| S3 authentication | SigV4 root AK/SK or per-Resource AK/SK |
| Group / password writeback | unsupported / not applicable |

There is no browser session or Module-/IAM-initiated logout. Replacing the root credential and
recreating the container revokes access; zero-downtime rotation is not implemented.

## Administrator access and IAM recovery

There is no Web console or account managed by `anas admin local`. The private Admin API is only a
Provider-operation channel, not an operator endpoint. Read the root access key from the
configuration inventory. When `root_secret_key` is omitted, retrieve the Hook-generated value only
through the explicit Secret Store command:

```bash
anas config list versitygw -w /srv/anas
anas config secret list -w /srv/anas
anas config secret get VERSITYGW_ROOT_SECRET_KEY -w /srv/anas
```

`secret get` prints clear text and belongs only in a controlled terminal. A user-supplied secret
stays in protected configuration and is not revealed by `config secret get`. Runner stores each
Resource secret separately in the Secret Store; do not edit the plaintext JSON files under
`${DATA_PATH}/versitygw/iam` directly.

## Database support

This Module neither consumes nor provides a relational-database Contract. Object payloads are stored
directly in the dedicated POSIX backend.

## All configuration parameters

This inventory comes from `module.yml` and `anas config list`. Rendered environment names are private
Module implementation details, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `versitygw.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `s3` | `static` | `VERSITYGW_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Domain prefix for the S3 HTTPS endpoint |
| `versitygw.read_only` | bool | — | `false` | `static` | `VERSITYGW_READ_ONLY` | no | no | no | yes | `container_recreate` | Enable the upstream global read-only guard |
| `versitygw.region` | string | `length: 1..64`; `pattern: ^[A-Za-z0-9][A-Za-z0-9._-]*$` | `us-east-1` | `static` | `VERSITYGW_REGION` | no | no | no | yes | `container_recreate` | SigV4 signing region |
| `versitygw.root_access_key` | string | `length: 3..64`; `pattern: ^[A-Za-z0-9._-]+$` | `ANASROOT` | `static` | `VERSITYGW_ROOT_ACCESS_KEY` | no | no | no | yes | `container_recreate` | Access-key identifier of the root S3 credential |
| `versitygw.root_secret_key` | string | `length: 16..128` | — | `generated` | `VERSITYGW_ROOT_SECRET_KEY` | no | yes | yes | yes | `container_recreate` | Secret key of the root S3 credential |

### Query and update

```bash
anas config list versitygw -w /srv/anas
anas config explain versitygw.read_only
anas config set versitygw.read_only true -w /srv/anas
anas config plan -w /srv/anas
```

A root-credential change invalidates old signatures immediately and is currently applied by container
recreation, not a transactional online rotation. Do not place a secret in shell argv; omit it for a
generated value or use a protected configuration-import workflow.

## S3 client smoke test

These commands avoid placing the clear-text secret in shell history, but the value remains in the
current process environment until it is unset:

```bash
export AWS_ACCESS_KEY_ID=ANASROOT
export AWS_SECRET_ACCESS_KEY="$(anas config secret get VERSITYGW_ROOT_SECRET_KEY -w /srv/anas)"
export AWS_DEFAULT_REGION=us-east-1
aws --endpoint-url https://s3.example.com s3api create-bucket --bucket smoke-test
aws --endpoint-url https://s3.example.com s3api put-object --bucket smoke-test --key hello.txt --body ./hello.txt
aws --endpoint-url https://s3.example.com s3api get-object --bucket smoke-test --key hello.txt ./restored.txt
unset AWS_SECRET_ACCESS_KEY
```

Replace the endpoint with the actual `VERSITYGW_ENDPOINT`. The client must support a custom endpoint
and path-style addressing. Virtual-host style needs `*.s3.<base_domain>` DNS and certificates, which
the first release does not provide.

## Storage, backup, and verification

Objects live at `${DATA_PATH}/versitygw/objects`, and internal-IAM data lives at
`${DATA_PATH}/versitygw/iam`; both enter workspace snapshots/backups. A recovery point must contain
both directories, `.anas/secrets.yml`, configuration, lock, and deployment metadata;
then a signed client must read the original object. File counts or a health response do not prove recovery.

The directory is an S3-only namespace and never mounts `${USER_DATA_PATH}` or a Samba share. Direct
host/SMB writes bypass S3 authorization, metadata, policies, and future version semantics and are unsupported.

## Current limitations

- all Capability consumers share the root credential, so one consumer leak can affect every bucket;
- Resource consumers have isolated buckets and long-lived AK/SK pairs, but no STS, quota, or fine-grained policy;
- the Admin API is private and lifecycle-only, with no public or WebUI management surface;
- experimental versioning/object lock are disabled, with no WORM claim;
- external IAM integration, STS, notifications, replication, and multi-node HA are absent;
- full AWS S3 parity, virtual-host style, real recovery, and upgrades remain unverified;
- status stays `developing` until real client and recovery acceptance passes.

## Technical documentation

See [technical documentation](docs/technical.en.md) for privilege dropping, Secrets, the SigV4 proxy
boundary, Hook behavior, and tests.
