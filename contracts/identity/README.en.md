# identity Contract

This Contract describes OIDC/SAML client-registration resources, but its current status is `pending`. IAM is presently implemented directly through Module capabilities, environment bindings, and application hooks; the presence of `contract.yml` does not mean the Runner dispatches it.

## Planned semantics

- Resource identity is `(consumer, resource_id)`, using either `oidc` or `saml`.
- `ensure` should idempotently create or update the client, redirect URIs, allowed groups, and scope/claim mappings.
- Client credentials may travel only through the Secret Store; metadata, deployment manifests, and logs must not contain plaintext.
- `inspect` must verify provider and consumer observed state instead of merely checking a manifest.
- `rotate_credential` and `delete` require application-level verification and failure compensation.

## Implementation gate

This page must remain `pending`, and modules must not depend on the Contract, until the Runner dispatches provider operations, at least one IAM provider implements them, and real login E2E passes. The existing IAM capability remains the current implementation.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`pending`（reviewed 2026-08-14）
- Interfaces / 接口：`oidc`, `saml`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/client.yml`

### Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | `false` | - | - |
| `ensure` | `true` | - | - |
| `inspect` | `true` | - | - |
| `rotate_credential` | `false` | - | - |

### Schemas

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/client.yml` | `object` | `interface`, `redirect_uris` | `allowed_groups`, `interface`, `redirect_uris` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| - | - | - | - | - |
<!-- generated:contract-reference:end -->
