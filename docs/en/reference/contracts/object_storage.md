> This page is generated from the Contract root README. Do not edit it directly.

# object_storage Contract

Provides modules with an independent bucket, access credential, and idempotent object-storage lifecycle.

A resource is created only when a consumer explicitly declares both `dependencies.contracts` and
`resources.requires`. A module that omits the Contract receives no bucket, access key, secret, or
object-storage environment variables.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-08-22）
- Interfaces / 接口：`s3`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | `false` | `schemas/delete-request.yml` | `schemas/delete-result.yml` |
| `ensure` | `true` | `schemas/ensure-request.yml` | `schemas/connection-result.yml` |
| `inspect` | `true` | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `rotate_credential` | `false` | `schemas/rotate-request.yml` | `schemas/connection-result.yml` |

### Schemas

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/connection-result.yml` | `object` | `endpoint`, `region`, `bucket`, `access_key_id`, `secret_access_key_secret`, `path_style` | `access_key_id`, `bucket`, `endpoint`, `path_style`, `region`, `secret_access_key_secret` |
| `schemas/delete-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/delete-result.yml` | `object` | `deleted` | `deleted` |
| `schemas/ensure-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-result.yml` | `object` | `exists`, `ready` | `exists`, `ready` |
| `schemas/resource.yml` | `object` | `bucket`, `credential`, `deletion_policy` | `access_key_id`, `bucket`, `credential`, `deletion_policy` |
| `schemas/rotate-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `versitygw` | `1.0.0` | `s3` | `providers/object_storage/provider.yml` |
<!-- generated:contract-reference:end -->
See the [technical documentation](./object_storage-technical.md) for implementation and security boundaries.
