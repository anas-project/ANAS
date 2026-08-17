> 本页由 Contract 根目录 README 生成，请勿直接编辑。

# identity Contract

本 Contract 描述 OIDC/SAML 客户端注册资源，但当前状态是 `pending`。现行 IAM 由 Module capability、环境变量与各应用 Hook 直接实现；存在 `contract.yml` 不等于 Runner 已经调度它。

## 计划语义

- 资源标识是 `(consumer, resource_id)`，接口为 `oidc` 或 `saml`。
- `ensure` 应幂等创建/更新客户端、redirect URI、允许组和 scope/claim 映射。
- 客户端密钥只能通过 Secret Store 传递；metadata、deployment manifest 和日志不得含明文。
- `inspect` 应验证 provider 与 consumer 的 observed state，而不是只检查 manifest。
- `rotate_credential` 与 `delete` 必须有应用级验证和失败补偿。

## 实现门槛

在 Runner 支持 provider operation、至少一个 IAM provider 实现并通过真实登录 E2E 前，本文档和生成页面必须保持 `pending`，Module 也不得声明依赖本 Contract。现有 IAM capability 继续作为当前实现。

Secret 边界、Schema 展开和文档生成流程见[技术实现](./identity-technical.md)。

<!-- generated:contract-reference:start -->
## 生成的 Contract 参考

> 本节由 `contract.yml`、schemas、Module manifests 与 `documentation.yml` 生成，请勿手工编辑。

- Version / 版本：`1.0.0`
- Status / 状态：`pending`（reviewed 2026-08-14）
- Interfaces / 接口：`oidc`, `saml`
- Resource identity / 资源标识：`consumer`, `resource_id`
- Resource schema / 资源 Schema：`schemas/client.yml`

### Operations / 操作

| Operation | Required | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | `false` | - | - |
| `ensure` | `true` | - | - |
| `inspect` | `true` | - | - |
| `rotate_credential` | `false` | - | - |

### Schemas / 字段

| Schema | Type | Required fields | All fields |
| --- | --- | --- | --- |
| `schemas/client.yml` | `object` | `interface`, `redirect_uris` | `allowed_groups`, `interface`, `redirect_uris` |

### 当前 Provider 与 Consumer

| Role | Module | Version constraint | Interface | Implementation |
| --- | --- | --- | --- | --- |
| - | - | - | - | - |
<!-- generated:contract-reference:end -->
