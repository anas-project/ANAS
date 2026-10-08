# relational_database Contract

This Contract provides modules with an idempotent relational database, principal, and credential lifecycle. The root README is the source of truth for English semantics; the generator maintains versions, interfaces, operations, schemas, and usage inventory.

## Semantics and lifecycle

- The resource identity is `(consumer, resource_id)`. Repeated `ensure` calls must converge on the same database and principal without creating duplicates.
- `ensure` returns connection data and a Secret Store reference; plaintext passwords never enter the deployment manifest or routine logs.
- `inspect` observes existence and readiness without mutating the resource.
- `rotate_credential` updates the provider before committing the new Secret atomically; failure preserves the prior credential.
- `delete` honors `deletion_policy`, with `retain` as the safe boundary.

## PostgreSQL extensions

A Resource using the `postgres` interface can declare required SQL extension names. Ordinary databases omit the `postgres` block.

```yaml
spec:
  name: photos
  principal: photos
  credential:
    policy: generated
  deletion_policy: retain
  postgres:
    extensions: [vector, earthdistance]
```

The `postgres` block accepts only `extensions`: a nonempty list of unique names matching `^[a-z][a-z0-9_]{0,62}$`.
Runner rejects unknown fields and invalid representations; MariaDB rejects the entire `postgres` block. Before creating a database
or extension, the Provider rejects names unsupported by its fixed release combination. Every listed extension is required.
The PostgreSQL Module manages versions, dependencies, preload, and upgrades; Consumers specify neither versions nor arbitrary SQL.

Application roles retain ordinary privileges. Readiness requires a connection with the actual application credential and checks of
role privileges, fixed extension and dependency versions, and preload. Bundled binaries alone do not prove readiness. Ordinary
`ensure` must fail on an installed version mismatch so controlled PostgreSQL Module maintenance can handle it. Removing a declaration
does not automatically drop extensions. Upgrades and recovery reuse ANAS whole-workspace recovery sets, covering shared databases,
coupled files, configuration, Secrets, and matching images. Consult PostgreSQL Module documentation and implementation records for
the status of real upgrade and recovery acceptance.

## Compatibility and limitations

`postgres` and `mariadb` currently implement the Contract; the generated reference below lists Providers and Consumers. ANAS has not
been released, so the extension-name list completes the current `1.0.0` without an old-interface compatibility layer or lock
conversion. After release, incompatible schema or semantic changes require a major version; new optional operations or fields may
use a minor version.

See the [technical implementation](docs/technical.en.md) for provider operations, secret boundaries, expanded schemas, and documentation generation.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-10-03）
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
| `schemas/resource.yml` | `object` | `name`, `principal`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `name`, `postgres`, `principal` |
| `schemas/rotate-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `mariadb` | `1.0.0` | `mariadb` | `providers/relational_database/provider.yml` |
| provider | `postgres` | `1.0.0` | `postgres` | `providers/relational_database/provider.yml` |
| consumer | `ai_agent` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `authentik` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `casdoor` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `forgejo` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `immich` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `llng` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `meshcentral` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `nextcloud` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `vikunja` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
<!-- generated:contract-reference:end -->
