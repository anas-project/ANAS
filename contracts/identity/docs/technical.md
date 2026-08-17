# identity Contract 技术实现

本文记录 Contract manifest、schema、Provider/Consumer 边界以及文档生成约束。语义与运维入口见[中文 README](../README.md)。

> 状态: `pending`; 复核日期: `2026-08-14`.

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

| Operation | 必需 | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | 否 | `—` | `—` |
| `ensure` | 是 | `—` | `—` |
| `inspect` | 是 | `—` | `—` |
| `rotate_credential` | 否 | `—` | `—` |

## Schemas

### `client.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `allowed_groups` | `array<string>` | 否 |  |
| `interface` | enum (`oidc`, `saml`) | 是 |  |
| `redirect_uris` | `array<string>` | 是 |  |

## 运行时不变量

- Resource identity 必须稳定且在 Consumer 内唯一。
- Provider operation 必须幂等；重复 `ensure` 不得破坏已有资源。
- Consumer 只能获得自己的最小权限凭据。
- 管理员凭据不得进入长期 Consumer 容器。
- `delete` 为可选 operation 时，缺失实现不能被伪装成成功删除。
- Contract 版本、interface、Provider 绑定和 Resource 身份必须进入 deployment/lock 状态。

## 文档生成管线

1. 读取 `contract.yml` 并校验 `anas.contract/v1`。
2. 解析 Resource schema 和每个 operation 的 request/result schema。
3. 生成版本、interface、identity、operation 和字段表。
4. 扫描 `modules/*/module.yml` 生成 Provider/Consumer 矩阵。
5. 合并人工维护的语义、安全和状态段落。
6. 对中英文结构、断链、未知 `$ref` 和未记录字段执行 CI 检查。

生成输出不能取代源文件；schema 是字段事实源，技术文档负责解释为什么以及如何实现。
