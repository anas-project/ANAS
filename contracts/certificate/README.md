# certificate Contract

这是证书资源 Contract 的提案，当前状态是 `proposal`。现行实现由 `lego` Module、DNS provider 和 `ANAS_TLS_*` 目录/环境约定直接完成；Runner 不通过本 Contract 调度证书。

## 是否需要 Contract

只有出现多个可替换 provider、多个独立 consumer 需要资源级隔离、或需要统一 renew/revoke observed state 时，Contract 才能提供足够收益。当前单一 lego 流程已经能为整套部署生成共享证书，把它立即 Contract 化只会引入第二套状态模型。

## 提案语义

- 资源标识是 `(consumer, resource_id)`，接口为 `acme` 或 `internal_ca`。
- `ensure`/`inspect` 必须覆盖域名、用途、续期策略与文件权限。
- private key 只能存在于 Secret/证书受保护目录，不能进入 deployment manifest 或 CLI JSON。
- `renew`/`revoke` 成为实现要求前，需要 ADR 说明 provider 选择、信任分发、回滚和 consumer reload 行为。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`proposal`（reviewed 2026-08-14）
- Interfaces / 接口：`acme`, `internal_ca`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/resource.yml`

### Operations / 操作

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `ensure` | `true` | - | - |
| `inspect` | `true` | - | - |
| `renew` | `false` | - | - |
| `revoke` | `false` | - | - |

### Schemas / 字段

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/resource.yml` | `object` | `domains`, `usage`, `renewal` | `domains`, `renewal`, `usage` |

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| - | - | - | - | - |
<!-- generated:contract-reference:end -->
