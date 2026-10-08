# relational_database Contract

为 Module 提供幂等的关系数据库、账号与凭据生命周期。根 README 是本 Contract 中文语义说明的单一来源；版本、接口、操作、Schema 和使用方清单由生成器维护。

## 语义与生命周期

- 资源标识是 `(consumer, resource_id)`；同一请求重复执行 `ensure` 必须收敛到同一数据库与 principal，不能创建重复对象。
- `ensure` 返回连接信息和 Secret Store 中的密码引用，不把明文密码写入 deployment manifest 或普通日志。
- `inspect` 只观察存在性与就绪状态，不修改资源。
- `rotate_credential` 需要先更新 provider，再原子提交 Secret，失败必须保留旧凭据。
- `delete` 遵守资源的 `deletion_policy`；`retain` 是默认安全边界。

## PostgreSQL 扩展

`postgres` interface 的 Resource 可以声明必需的 SQL 扩展名称；普通数据库不需要 `postgres` 块。

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

`postgres` 只接受 `extensions`，名称列表必须非空、无重复，名称符合 `^[a-z][a-z0-9_]{0,62}$`。
Runner 拒绝未知字段及非法表示；MariaDB 拒绝整个 `postgres` 块。Provider 在创建数据库或扩展前拒绝其
固定发布组合不支持的名称。列表中的扩展均为必需项，扩展版本、依赖、preload 和升级由 PostgreSQL
Module 管理，Consumer 不指定版本或任意 SQL。

应用角色保持普通权限。扩展就绪需要实际应用凭据连接、权限、固定扩展及依赖版本和 preload 检查；
预装二进制不能证明 ready。已有版本不符时普通 `ensure` 应失败，由 PostgreSQL Module 的受控维护处理。
移除声明不会自动删除扩展。升级和恢复复用 ANAS 全 workspace 恢复集合，覆盖共享数据库、耦合文件、
配置、Secret 和匹配镜像；真实升级与恢复验收状态以 PostgreSQL Module 文档和配套实施记录为准。

## 兼容性与限制

当前 `postgres` 与 `mariadb` 提供实现，Provider/Consumer 清单见下方生成参考。ANAS 尚未发布，扩展名称列表直接
完善当前 `1.0.0`，不建立旧接口兼容层或旧 lock 转换。正式发布后的不兼容 schema 或语义变化需要提升
major version；新增可选 operation 或字段可以提升 minor version。

Provider operation、Secret 边界、Schema 展开和文档生成流程见[技术实现](docs/technical.md)。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`implemented`（reviewed 2026-10-03）
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
| `schemas/resource.yml` | `object` | `name`, `principal`, `credential`, `deletion_policy` | `credential`, `deletion_policy`, `name`, `postgres`, `principal` |
| `schemas/rotate-request.yml` | `object` | `consumer`, `resource_id`, `provider`, `interface`, `spec` | `consumer`, `interface`, `provider`, `resource_id`, `spec` |

### 当前 Provider 与 Consumer

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
