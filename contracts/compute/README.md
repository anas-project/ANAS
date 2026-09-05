# compute Contract

在 apply 时交付受限的隔离沙箱租约（project、配额、镜像 allowlist 与受限客户端证书），实例生命周期由消费者在租约边界内自行驱动。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`proposal`（reviewed 2026-08-30）
- Interfaces / 接口：`incus_vm`, `incus_container`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations / 操作

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `ensure` | `true` | `schemas/ensure-request.yml` | `schemas/sandbox-result.yml` |
| `inspect` | `true` | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `revoke` | `false` | `schemas/revoke-request.yml` | `schemas/revoke-result.yml` |

### Schemas / 字段

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/ensure-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-result.yml` | `object` | `exists`, `ready`, `restricted`, `quota_enforced` | `exists`, `quota_enforced`, `ready`, `restricted` |
| `schemas/resource.yml` | `object` | `sandbox`, `instance_prefix`, `quota`, `image_allowlist`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `image_allowlist`, `image_policy`, `instance_prefix`, `quota`, `sandbox` |
| `schemas/revoke-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/revoke-result.yml` | `object` | `revoked` | `revoked` |
| `schemas/sandbox-result.yml` | `object` | `endpoint`, `sandbox`, `instance_prefix`, `profile`, `server_certificate_fingerprint`, `client_certificate_secret`, `quota` | `client_certificate_secret`, `endpoint`, `instance_prefix`, `profile`, `quota`, `sandbox`, `server_certificate_fingerprint` |

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `incus` | `1.0.0` | `incus_vm` | `providers/compute/incus_vm.yml` |
| provider | `incus` | `1.0.0` | `incus_container` | `providers/compute/incus_container.yml` |
| consumer | `forgejo` | `>=1.0.0 <2.0.0` | `incus_container`, `incus_vm` | - |
<!-- generated:contract-reference:end -->
实现与安全边界见[技术文档](docs/technical.md)。
