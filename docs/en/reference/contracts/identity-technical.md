> This page is generated from the Contract technical documentation. Do not edit it directly.

# identity Contract technical implementation

This page records the contract manifest, schemas, Provider/Consumer boundaries, and documentation-generation constraints. See the [English README](./identity.md) for semantics and operations.

> Status: `pending`; Reviewed: `2026-08-14`.

## Manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: identity
version: 1.0.0
interfaces:
  - oidc
  - saml
resource:
  schema: schemas/client.yml
  identity: [consumer, resource_id]
operations:
  ensure: {required: true}
  inspect: {required: true}
  rotate_credential: {required: false}
  delete: {required: false}
```

## Provider / Consumer

- Providers: —
- Consumers: —

## Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | no | `—` | `—` |
| `ensure` | yes | `—` | `—` |
| `inspect` | yes | `—` | `—` |
| `rotate_credential` | no | `—` | `—` |

## Schemas

### `client.yml`

| Field | Type/values | Required | Constraints |
| --- | --- | --- | --- |
| `allowed_groups` | `array<string>` | no |  |
| `interface` | enum (`oidc`, `saml`) | yes |  |
| `redirect_uris` | `array<string>` | yes |  |

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
