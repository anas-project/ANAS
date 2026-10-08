> This page is generated from the Contract technical documentation. Do not edit it directly.

# relational_database Contract technical implementation

This page records the contract manifest, schemas, Provider/Consumer boundaries, and documentation-generation constraints. See the [English README](./relational_database.md) for semantics and operations.

> Status: Ordinary database support and the extension-field/Runner projection are implemented; see PostgreSQL Module records for real-host extension acceptance. Reviewed: `2026-10-03`.

## Manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: relational_database
version: 1.0.0
interfaces:
  - postgres
  - mariadb
resource:
  schema: schemas/resource.yml
  identity:
    - consumer
    - resource_id
operations:
  ensure:
    request_schema: schemas/ensure-request.yml
    result_schema: schemas/connection-result.yml
    required: true
  inspect:
    request_schema: schemas/inspect-request.yml
    result_schema: schemas/inspect-result.yml
    required: true
  rotate_credential:
    request_schema: schemas/rotate-request.yml
    result_schema: schemas/connection-result.yml
    required: false
  delete:
    request_schema: schemas/delete-request.yml
    result_schema: schemas/delete-result.yml
    required: false
```

## Provider / Consumer

- Providers: `mariadb`, `postgres`
- Consumers: see the [generated README inventory](./relational_database.md).

## Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | no | `schemas/delete-request.yml` | `schemas/delete-result.yml` |
| `ensure` | yes | `schemas/ensure-request.yml` | `schemas/connection-result.yml` |
| `inspect` | yes | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `rotate_credential` | no | `schemas/rotate-request.yml` | `schemas/connection-result.yml` |

## Schemas

### `connection-result.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `database` | string | yes |  |
| `host` | string | yes |  |
| `network` | string | yes |  |
| `password_secret` | string | yes |  |
| `port` | integer | yes | minimum=1; maximum=65535 |
| `username` | string | yes |  |

### `delete-request.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `consumer` | string | yes |  |
| `interface` | enum (`postgres`, `mariadb`) | yes |  |
| `provider` | string | yes |  |
| `resource_id` | string | yes |  |
| `spec` | ref | yes | $ref=resource.yml |

### `delete-result.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `deleted` | boolean | yes |  |

### `ensure-request.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `consumer` | string | yes |  |
| `interface` | enum (`postgres`, `mariadb`) | yes |  |
| `provider` | string | yes |  |
| `resource_id` | string | yes |  |
| `spec` | ref | yes | $ref=resource.yml |

### `inspect-request.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `consumer` | string | yes |  |
| `interface` | enum (`postgres`, `mariadb`) | yes |  |
| `provider` | string | yes |  |
| `resource_id` | string | yes |  |
| `spec` | ref | yes | $ref=resource.yml |

### `inspect-result.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `exists` | boolean | yes |  |
| `ready` | boolean | yes |  |

### `resource.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `credential` | object | yes | additional_properties=false |
| `credential.policy` | enum (`generated`) | yes |  |
| `deletion_policy` | enum (`retain`, `delete`) | yes |  |
| `name` | string | yes | pattern=^[a-z][a-z0-9_]{0,62}$ |
| `postgres` | object | no | additional_properties=false; postgres interface only |
| `postgres.extensions` | `array<string>` | required within postgres | min_items=1; unique_items=true; item pattern=^[a-z][a-z0-9_]{0,62}$ |
| `principal` | string | yes | pattern=^[a-z][a-z0-9_]{0,62}$ |

### `rotate-request.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `consumer` | string | yes |  |
| `interface` | enum (`postgres`, `mariadb`) | yes |  |
| `provider` | string | yes |  |
| `resource_id` | string | yes |  |
| `spec` | ref | yes | $ref=resource.yml |

## Runtime invariants

- Resource identity is stable and unique within a consumer.
- Provider operations are idempotent; repeated `ensure` must preserve existing resources.
- Consumers receive only their least-privilege credentials.
- Administrator credentials never enter long-running consumer containers.
- If `delete` is optional, a missing implementation must not be reported as a successful deletion.
- Contract version, interface, provider binding, and resource identity enter deployment/lock state.

## Extension requests and maintenance boundaries

The current unreleased Contract remains `1.0.0`. `internal/runner/relational_database.go` strictly validates Resource fields and
the `credential` and `postgres` objects. `contracts.go` validates before generating Secrets, and `resources.go` checks again before
Provider execution. A `postgres` block on MariaDB fails; the name list accepts no version, dependency, preload, or SQL fields.

Runner projects the declared list, in order, as comma-separated `ANAS_RESOURCE_POSTGRES_EXTENSIONS`. Compose explicitly forwards
it to the one-shot provision service. An absent declaration produces an empty value so inherited environment cannot add requests.
The request uses the existing Resource spec and fingerprint, without request files, an extension catalog protocol, or version solving.

The PostgreSQL Module fixes supported names, exact versions, and required dependencies. The Provider validates the entire supported
request before writes. Ensure enables missing extensions at fixed versions and checks the real application identity; inspect only
observes. Checks cover a non-superuser role, extension and dependency versions, and instance preload. A failed process cannot save
ready, and an earlier ready state does not replace the current checks. Ordinary ensure fails on an installed version mismatch;
only controlled PostgreSQL Module maintenance upgrades it.

A shared-instance upgrade must list every affected Consumer, stop writes, and obtain an ANAS recovery set containing databases,
coupled files, configuration, Secrets, and matching images. Privileged maintenance and verification precede Consumer startup.
Changed data formats or uncertain results must not be opened directly with an older image. Removing an extension declaration
retains the resource and does not issue DROP EXTENSION. Authentication, upgrades, and consistent recovery require real-host
acceptance; targeted Runner tests prove input rejection, parameter projection, and failure-state behavior only.

Targeted validation: `go test ./internal/runner -run 'TestRelationalDatabase'`. PostgreSQL Module source documentation and test
scripts cover real Provider checks and controlled upgrades; a Contract schema cannot establish those results.

## Documentation generation pipeline

1. Read `contract.yml` and validate `anas.contract/v1`.
2. Resolve the resource schema and every operation request/result schema.
3. Generate version, interface, identity, operation, and field tables.
4. Scan `modules/*/module.yml` for the Provider/Consumer matrix.
5. Merge manually reviewed semantic, security, and status sections.
6. Check bilingual structure, broken links, unknown `$ref` values, and undocumented fields in CI.

Generated output never replaces source files: schemas are authoritative for fields, while technical documentation explains why and how.
