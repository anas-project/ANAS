# relational_database Contract 技术实现

本文记录 Contract manifest、schema、Provider/Consumer 边界以及文档生成约束。语义与运维入口见[中文 README](../README.md)。

> 状态: 普通数据库链路已实现，扩展字段与 Runner 传参已实现；扩展真实主机验收见 PostgreSQL Module 记录。复核日期: `2026-10-03`.

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
- Consumers: 以 [README 的生成清单](../README.md)为准。

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
| `postgres` | object | 否 | additional_properties=false；仅 postgres interface |
| `postgres.extensions` | `array<string>` | postgres 块内必需 | min_items=1；unique_items=true；元素 pattern=^[a-z][a-z0-9_]{0,62}$ |
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

## 扩展请求与维护边界

当前未发布的 Contract 保持 `1.0.0`。`internal/runner/relational_database.go` 严格校验 Resource 根字段、
`credential` 和 `postgres` 对象；`contracts.go` 在生成 Secret 前执行校验，`resources.go` 在 Provider
执行前再次复核。MariaDB 收到 `postgres` 即失败，名称列表不接受版本、依赖、preload 或 SQL 字段。

Runner 将列表按声明顺序投影为逗号分隔的 `ANAS_RESOURCE_POSTGRES_EXTENSIONS`，Compose 显式传入一次性
provision 服务。未声明扩展时投影为空值，不能由继承环境追加要求。请求沿用 Resource spec 及其指纹，
没有请求文件、扩展目录协议或版本求解器。

支持名称、精确版本及必要依赖由 PostgreSQL Module 固定；Provider 在写入前确认完整请求受支持。
ensure 负责按固定版本启用缺失扩展并检查真实应用身份；inspect 只检查，不改写资源。检查涵盖非超级用户、
扩展及依赖版本和实例 preload，失败退出不能保存 ready；旧 ready 状态不代替本次检查。
已有版本不符时普通 ensure 失败，只有 PostgreSQL Module 受控维护才能升级。

共享实例升级应列出全部受影响 Consumer，停写并通过 ANAS 获取包含数据库、耦合文件、配置、Secret 及匹配
镜像的恢复集合，完成特权维护和复查后再启动 Consumer。数据格式改变或结果不明时不得直接用旧镜像打开新
数据。移除扩展声明沿用 retain 边界，不执行 DROP EXTENSION。升级、认证和一致恢复需要真实主机验收，
Runner 的定向测试只证明输入拒绝、参数传递和失败状态行为。

定向验证：`go test ./internal/runner -run 'TestRelationalDatabase'`。真实 Provider 检查和受控升级验证见
PostgreSQL Module 的源文档与测试脚本，不由 Contract schema 推断通过。

## 文档生成管线

1. 读取 `contract.yml` 并校验 `anas.contract/v1`。
2. 解析 Resource schema 和每个 operation 的 request/result schema。
3. 生成版本、interface、identity、operation 和字段表。
4. 扫描 `modules/*/module.yml` 生成 Provider/Consumer 矩阵。
5. 合并人工维护的语义、安全和状态段落。
6. 对中英文结构、断链、未知 `$ref` 和未记录字段执行 CI 检查。

生成输出不能取代源文件；schema 是字段事实源，技术文档负责解释为什么以及如何实现。
