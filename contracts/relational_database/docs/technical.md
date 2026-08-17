# relational_database Contract 技术实现

本文记录 Contract manifest、schema、Provider/Consumer 边界以及文档生成约束。语义与运维入口见[中文 README](../README.md)。

> 状态: `implemented`; 复核日期: `2026-08-14`.

## Manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: relational_database
version: 1.0.0
interfaces:
  - postgres
  - mariadb
resource:
  schema: schemas/resource.yml
  identity:
    - consumer
    - resource_id
operations:
  ensure:
    request_schema: schemas/ensure-request.yml
    result_schema: schemas/connection-result.yml
    required: true
  inspect:
    request_schema: schemas/inspect-request.yml
    result_schema: schemas/inspect-result.yml
    required: true
  rotate_credential:
    request_schema: schemas/rotate-request.yml
    result_schema: schemas/connection-result.yml
    required: false
  delete:
    request_schema: schemas/delete-request.yml
    result_schema: schemas/delete-result.yml
    required: false
```

## Provider / Consumer

- Providers: `mariadb`, `postgres`
- Consumers: `authentik`, `llng`, `meshcentral`, `nextcloud`

## Operations

| Operation | 必需 | Request schema | Result schema |
| --- | --- | --- | --- |
| `delete` | 否 | `schemas/delete-request.yml` | `schemas/delete-result.yml` |
| `ensure` | 是 | `schemas/ensure-request.yml` | `schemas/connection-result.yml` |
| `inspect` | 是 | `schemas/inspect-request.yml` | `schemas/inspect-result.yml` |
| `rotate_credential` | 否 | `schemas/rotate-request.yml` | `schemas/connection-result.yml` |

## Schemas

### `connection-result.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `database` | string | 是 |  |
| `host` | string | 是 |  |
| `network` | string | 是 |  |
| `password_secret` | string | 是 |  |
| `port` | integer | 是 | minimum=1; maximum=65535 |
| `username` | string | 是 |  |

### `delete-request.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `consumer` | string | 是 |  |
| `interface` | enum (`postgres`, `mariadb`) | 是 |  |
| `provider` | string | 是 |  |
| `resource_id` | string | 是 |  |
| `spec` | ref | 是 | $ref=resource.yml |

### `delete-result.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `deleted` | boolean | 是 |  |

### `ensure-request.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `consumer` | string | 是 |  |
| `interface` | enum (`postgres`, `mariadb`) | 是 |  |
| `provider` | string | 是 |  |
| `resource_id` | string | 是 |  |
| `spec` | ref | 是 | $ref=resource.yml |

### `inspect-request.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `consumer` | string | 是 |  |
| `interface` | enum (`postgres`, `mariadb`) | 是 |  |
| `provider` | string | 是 |  |
| `resource_id` | string | 是 |  |
| `spec` | ref | 是 | $ref=resource.yml |

### `inspect-result.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `exists` | boolean | 是 |  |
| `ready` | boolean | 是 |  |

### `resource.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `credential` | object | 是 | additional_properties=false |
| `credential.policy` | enum (`generated`) | 是 |  |
| `deletion_policy` | enum (`retain`, `delete`) | 是 |  |
| `name` | string | 是 | pattern=^[a-z][a-z0-9_]{0,62}$ |
| `principal` | string | 是 | pattern=^[a-z][a-z0-9_]{0,62}$ |

### `rotate-request.yml`

| 字段 | 类型/取值 | 必需 | 约束 |
| --- | --- | --- | --- |
| `consumer` | string | 是 |  |
| `interface` | enum (`postgres`, `mariadb`) | 是 |  |
| `provider` | string | 是 |  |
| `resource_id` | string | 是 |  |
| `spec` | ref | 是 | $ref=resource.yml |

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
