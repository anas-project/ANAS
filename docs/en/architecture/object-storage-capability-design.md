# Object storage capability design

Status: the M1.1 capability and M1.2 resource work is implemented; real S3 and
restore end-to-end validation is outstanding
Date: 2026-08-22

## 1. Goal

`object_storage` expresses that a module needs a replaceable object storage
service. A consumer declares only the capability, and depends on neither
`versitygw`, nor a compose service name, nor a provider's private environment
variables. The only interface today is `s3`, and the only provider is
`versitygw`.

The shortest consumer declaration is:

```yaml
dependencies:
  requires_capabilities:
    - name: object_storage
```

`s3` is the single interface the runner registry records for this capability, so
a consumer is not required to add a selector parameter. Should a second
interface be added later, new consumers must declare the supported set
explicitly, while existing name-only consumers stay fixed on `s3`.

## 2. Binding and execution order

The runner binds automatically to the only enabled provider, and records:

```yaml
capability_bindings:
  example_consumer:
    object_storage: versitygw
    object_storage.interface: s3
```

The provider enters the consumer's dependency closure automatically and runs
`calculate` before it. Resolution fails when there is no provider, when the
provider is disabled, or when several candidates exist; it does not guess from a
product name or directory order.

## 3. The unified S3 output ABI

The provider hook publishes the following provider-neutral fields:

| Provider output | Meaning |
| --- | --- |
| `ANAS_OBJECT_STORAGE_S3_ENDPOINT` | The absolute HTTPS S3 endpoint |
| `ANAS_OBJECT_STORAGE_S3_REGION` | The SigV4 region |
| `ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID` | The access key ID |
| `ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | The secret access key; sensitive |
| `ANAS_OBJECT_STORAGE_S3_PATH_STYLE` | Whether path-style is forced; currently `true` |

After the provider's `calculate` completes, the runner validates the required
fields and projects them only to bound consumers:

```text
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__INTERFACE
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ENDPOINT
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__REGION
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ACCESS_KEY_ID
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__SECRET_ACCESS_KEY
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__PATH_STYLE
```

These keys belong to the target consumer's runtime environment and are reserved
by the runner; a consumer needs no `config.consumes` entry for them, and cannot
override them from caller input. The consumer hook is responsible only for
translating its own binding into the upstream application's configuration.

## 4. Secrets and the trust boundary

`SECRET_ACCESS_KEY`, in both the provider output and the consumer binding,
inherits secret sensitivity and enters neither plan, lock, deployment manifest,
ordinary errors, nor unrelated modules. A consumer never receives the
`ANAS_OBJECT_STORAGE_S3_*` provider-side namespace, and never receives another
consumer's binding.

The capability path provides only one set of root credentials, and every bound
consumer effectively shares those permissions. What this capability solves is
provider decoupling and a uniform configuration shape — not least privilege,
bucket isolation, or multi-tenant security. Compromising any bound consumer may
affect every bucket.

## 5. The capability/resource boundary

A capability publishes service-level connection information only, and runs no
persistent resource lifecycle:

- it does not create, delete, or retain buckets automatically;
- it does not create per-consumer AK/SK, policies, or STS tokens;
- it does not rotate an individual consumer's credential;
- it promises nothing about bucket ownership, quota, versioning, or object lock.

A module that needs an independent bucket and credential declares the
`object_storage` 1.0.0 contract plus a resource explicitly. The runner generates
a unique AK/SK for `<consumer>.<resource_id>` and projects only
`ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__*`; a module that does
not declare one gets no resource side effects. The VersityGW provider implements
`ensure`, `inspect`, and `rotate_credential` through a private admin API, and
removal defaults to `retained`. The `delete` schema is kept as an optional
operation, and no destructive deletion is performed implicitly today.

## 6. Validation

Synthetic runner tests cover the name-only declaration, automatic binding to the
sole provider, dependency order, the binding record, required outputs, secret
isolation, and fail-closed behavior on a missing output. Module hook tests cover
the consistency of the unified fields with the private ones. Real AWS CLI/SDK
usage, persistence across restarts, and bare-machine restore are still to be
carried out under M2 of the
[VersityGW implementation plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/versitygw-module.md).
