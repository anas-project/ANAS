> 本页由 Contract 根目录 README 生成，请勿直接编辑。

# object_storage Contract

为 Module 提供独立 bucket、独立访问凭据与幂等对象存储生命周期。

Consumer 只有在 `dependencies.contracts` 和 `resources.requires` 中显式声明时才创建资源；未声明
Contract 的 Module 不会获得 bucket、访问密钥或任何对象存储环境变量。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-08-22）
- Interfaces / 接口：`s3`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations / 操作

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | `false` | `schemas/delete-request.yml` | `schemas/delete-result.yml` |
| `ensure` | `true` | `schemas/ensure-request.yml` | `schemas/connection-result.yml` |
| `inspect` | `true` | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `rotate_credential` | `false` | `schemas/rotate-request.yml` | `schemas/connection-result.yml` |

### Schemas / 字段

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

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `versitygw` | `1.0.0` | `s3` | `providers/object_storage/provider.yml` |
<!-- generated:contract-reference:end -->
实现与安全边界见[技术文档](./object_storage-technical.md)。
