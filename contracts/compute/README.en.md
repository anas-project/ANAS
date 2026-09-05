# compute Contract

Provisions a fenced isolation sandbox lease at apply time (project, quota, image allowlist and a restricted client certificate); the consumer drives instance lifecycle itself within that fence.

<!-- generated:contract-reference:start -->
## Generated contract reference

> Generated from `contract.yml`, schemas, Module manifests, and `documentation.yml`; do not edit this block manually.

- Version / 版本：`1.0.0`
- Status / 状态：`proposal`（reviewed 2026-08-30）
- Interfaces / 接口：`incus_vm`, `incus_container`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `ensure` | `true` | `schemas/ensure-request.yml` | `schemas/sandbox-result.yml` |
| `inspect` | `true` | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `revoke` | `false` | `schemas/revoke-request.yml` | `schemas/revoke-result.yml` |

### Schemas

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/ensure-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-result.yml` | `object` | `exists`, `ready`, `restricted`, `quota_enforced` | `exists`, `quota_enforced`, `ready`, `restricted` |
| `schemas/resource.yml` | `object` | `sandbox`, `instance_prefix`, `quota`, `image_allowlist`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `image_allowlist`, `image_policy`, `instance_prefix`, `quota`, `sandbox` |
| `schemas/revoke-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/revoke-result.yml` | `object` | `revoked` | `revoked` |
| `schemas/sandbox-result.yml` | `object` | `endpoint`, `sandbox`, `instance_prefix`, `profile`, `server_certificate_fingerprint`, `client_certificate_secret`, `quota` | `client_certificate_secret`, `endpoint`, `instance_prefix`, `profile`, `quota`, `sandbox`, `server_certificate_fingerprint` |

### Current providers and consumers

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `incus` | `1.0.0` | `incus_vm` | `providers/compute/incus_vm.yml` |
| provider | `incus` | `1.0.0` | `incus_container` | `providers/compute/incus_container.yml` |
| consumer | `forgejo` | `>=1.0.0 <2.0.0` | `incus_container`, `incus_vm` | - |
<!-- generated:contract-reference:end -->
See the [technical documentation](docs/technical.en.md) for lifecycle and security boundaries.
