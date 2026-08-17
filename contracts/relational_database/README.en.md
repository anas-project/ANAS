# relational_database Contract

This Contract provides modules with an idempotent relational database, principal, and credential lifecycle. The root README is the source of truth for English semantics; the generator maintains versions, interfaces, operations, schemas, and usage inventory.

## Semantics and lifecycle

- The resource identity is `(consumer, resource_id)`. Repeated `ensure` calls must converge on the same database and principal without creating duplicates.
- `ensure` returns connection data and a Secret Store reference; plaintext passwords never enter the deployment manifest or routine logs.
- `inspect` observes existence and readiness without mutating the resource.
- `rotate_credential` updates the provider before committing the new Secret atomically; failure preserves the prior credential.
- `delete` honors `deletion_policy`, with `retain` as the safe boundary.

## Compatibility and limitations

`postgres` and `mariadb` currently implement the Contract. Nextcloud, MeshCentral, Authentik, and LLNG consume it. Incompatible schema or semantic changes require a major version; new optional operations or fields may use a minor version.

See the [technical implementation](docs/technical.en.md) for provider operations, secret boundaries, expanded schemas, and documentation generation.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-08-14）
- Interfaces / 接口：`postgres`, `mariadb`
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
| `schemas/connection-result.yml` | `object` | `host`, `port`, `database`, `username`, `password_secret`, `network` | `database`, `host`, `network`, `password_secret`, `port`, `username` |
| `schemas/delete-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/delete-result.yml` | `object` | `deleted` | `deleted` |
| `schemas/ensure-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-result.yml` | `object` | `exists`, `ready` | `exists`, `ready` |
| `schemas/resource.yml` | `object` | `name`, `principal`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `name`, `principal` |
| `schemas/rotate-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `mariadb` | `1.0.0` | `mariadb` | `providers/relational_database/provider.yml` |
| provider | `postgres` | `1.0.0` | `postgres` | `providers/relational_database/provider.yml` |
| consumer | `authentik` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `llng` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `meshcentral` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `nextcloud` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
<!-- generated:contract-reference:end -->
