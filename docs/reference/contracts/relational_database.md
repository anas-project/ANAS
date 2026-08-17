> 本页由 Contract 根目录 README 生成，请勿直接编辑。

# relational_database Contract

为 Module 提供幂等的关系数据库、账号与凭据生命周期。根 README 是本 Contract 中文语义说明的单一来源；版本、接口、操作、Schema 和使用方清单由生成器维护。

## 语义与生命周期

- 资源标识是 `(consumer, resource_id)`；同一请求重复执行 `ensure` 必须收敛到同一数据库与 principal，不能创建重复对象。
- `ensure` 返回连接信息和 Secret Store 中的密码引用，不把明文密码写入 deployment manifest 或普通日志。
- `inspect` 只观察存在性与就绪状态，不修改资源。
- `rotate_credential` 需要先更新 provider，再原子提交 Secret，失败必须保留旧凭据。
- `delete` 遵守资源的 `deletion_policy`；`retain` 是默认安全边界。

## 兼容性与限制

当前 `postgres` 与 `mariadb` 提供实现，Nextcloud、MeshCentral、Authentik 和 LLNG 是 consumer。Contract schema 或语义发生不兼容变化时必须提升 major version；新增可选 operation 或字段可以提升 minor version。

Provider operation、Secret 边界、Schema 展开和文档生成流程见[技术实现](./relational_database-technical.md)。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-08-14）
- Interfaces / 接口：`postgres`, `mariadb`
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
| `schemas/connection-result.yml` | `object` | `host`, `port`, `database`, `username`, `password_secret`, `network` | `database`, `host`, `network`, `password_secret`, `port`, `username` |
| `schemas/delete-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/delete-result.yml` | `object` | `deleted` | `deleted` |
| `schemas/ensure-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |
| `schemas/inspect-result.yml` | `object` | `exists`, `ready` | `exists`, `ready` |
| `schemas/resource.yml` | `object` | `name`, `principal`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `name`, `principal` |
| `schemas/rotate-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| provider | `mariadb` | `1.0.0` | `mariadb` | `providers/relational_database/provider.yml` |
| provider | `postgres` | `1.0.0` | `postgres` | `providers/relational_database/provider.yml` |
| consumer | `authentik` | `>=1.0.0 <2.0.0` | `postgres` | - |
| consumer | `llng` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `meshcentral` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
| consumer | `nextcloud` | `>=1.0.0 <2.0.0` | `postgres`, `mariadb` | - |
<!-- generated:contract-reference:end -->
