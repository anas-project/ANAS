> This page is generated from the Contract root README. Do not edit it directly.

# certificate Contract

This is a certificate-resource Contract proposal with status `proposal`. Certificates are currently handled directly by the `lego` Module, DNS providers, and the `ANAS_TLS_*` directory/environment convention; the Runner does not dispatch this Contract.

## Is a Contract necessary?

A Contract becomes worthwhile when interchangeable providers exist, independent consumers need resource isolation, or renew/revoke observed state needs one lifecycle. The current single-lego flow already creates deployment-wide shared certificates, so immediate conversion would add a second state model without a clear benefit.

## Proposed semantics

- Resource identity is `(consumer, resource_id)`, using `acme` or `internal_ca`.
- `ensure` and `inspect` cover domains, usage, renewal policy, and file permissions.
- Private keys may exist only in protected Secret/certificate storage, never in deployment manifests or CLI JSON.
- Before `renew` or `revoke` becomes an implementation requirement, an ADR must define provider selection, trust distribution, rollback, and consumer reload behavior.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`proposal`（reviewed 2026-08-14）
- Interfaces / 接口：`acme`, `internal_ca`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `ensure` | `true` | - | - |
| `inspect` | `true` | - | - |
| `renew` | `false` | - | - |
| `revoke` | `false` | - | - |

### Schemas

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/resource.yml` | `object` | `domains`, `usage`, `renewal` | `domains`, `renewal`, `usage` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| - | - | - | - | - |
<!-- generated:contract-reference:end -->
