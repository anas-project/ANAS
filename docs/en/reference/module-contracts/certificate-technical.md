> This page is generated from the Contract technical documentation. Do not edit it directly.

# certificate Contract technical implementation

This page records the contract manifest, schemas, Provider/Consumer boundaries, and documentation-generation constraints. See the [English README](./certificate.md) for semantics and operations.

> Status: `proposal`; Reviewed: `2026-08-14`.

## Manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: certificate
version: 1.0.0
interfaces:
  - acme
  - internal_ca
resource:
  schema: schemas/resource.yml
  identity: [consumer, resource_id]
operations:
  ensure: {required: true}
  inspect: {required: true}
  renew: {required: false}
  revoke: {required: false}
```

## Provider / Consumer

- Providers: —
- Consumers: —

## Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `ensure` | yes | `—` | `—` |
| `inspect` | yes | `—` | `—` |
| `renew` | no | `—` | `—` |
| `revoke` | no | `—` | `—` |

## Schemas

### `resource.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `domains` | `array<string>` | yes | min_items=1 |
| `renewal` | enum (`managed`, `manual`) | yes |  |
| `usage` | enum (`server`, `client`, `ca`) | yes |  |

## Runtime invariants

- Resource identity is stable and unique within a consumer.
- Provider operations are idempotent; repeated `ensure` must preserve existing resources.
- Consumers receive only their least-privilege credentials.
- Administrator credentials never enter long-running consumer containers.
- If `delete` is optional, a missing implementation must not be reported as a successful deletion.
- Contract version, interface, provider binding, and resource identity enter deployment/lock state.

## Documentation generation pipeline

1. Read `contract.yml` and validate `anas.contract/v1`.
2. Resolve the resource schema and every operation request/result schema.
3. Generate version, interface, identity, operation, and field tables.
4. Scan `modules/*/module.yml` for the Provider/Consumer matrix.
5. Merge manually reviewed semantic, security, and status sections.
6. Check bilingual structure, broken links, unknown `$ref` values, and undocumented fields in CI.

Generated output never replaces source files: schemas are authoritative for fields, while technical documentation explains why and how.
