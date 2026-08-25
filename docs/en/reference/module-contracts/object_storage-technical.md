> This page is generated from the Contract technical documentation. Do not edit it directly.

# object_storage Contract technical notes

## Declaration model

`object_storage` 1.x currently defines the `s3` interface. Consumers opt in with a Resource:

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

A module that omits these sections does not participate in Resource resolution and receives no
object-storage credential. `access_key_id` may be set explicitly; when omitted, the runner derives a
stable unique value of at most 64 characters from `consumer + resource_id`.

## Lifecycle

The runner generates and persists one independent secret for each Resource, then calls the provider's
idempotent `ensure` before starting the consumer. A provider must create or reconcile the IAM user,
secret, and bucket owner, and implement read-only `inspect`. `rotate_credential` and `delete` are optional
1.x operations. Removing a module or Resource declaration currently marks its state `retained`; it never
implicitly deletes the bucket or its objects.

The ready connection is published in the consumer-private namespace:

```text
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__REGION
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__BUCKET
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__ACCESS_KEY_ID
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__SECRET_ACCESS_KEY
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__PATH_STYLE
```

`SECRET_ACCESS_KEY` is sensitive and owned only by the target consumer. Deployment manifests and
Resource state store only a Secret Store reference, never the plaintext value.

## Security boundary

Independent credentials restrict a consumer to its assigned bucket. They do not provide STS,
fine-grained IAM policies, quotas, versioning, or object lock. The provider management API must remain
on the private container network and must not be exposed through Traefik or a host port.
`deletion_policy: delete` records explicit deletion intent; until Core exposes a destructive Resource
delete entry point, removal is still retained.
