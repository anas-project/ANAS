# Object Storage Capability 设计

状态：**当前模型**。Capability 声明与 Resource 模型已实施；真实 S3 客户端互操作与恢复
E2E 只能在真实环境判定，尚未执行。
日期：2026-08-22

## 1. 目标

`object_storage` 表达 Module 需要一个可替换的对象存储服务。Consumer 只声明能力，不依赖
`versitygw`、Compose service 名或 Provider 私有环境变量。当前唯一 interface 是 `s3`，唯一
Provider 是 `versitygw`。

最短 Consumer 声明为：

```yaml
dependencies:
  requires_capabilities:
    - name: object_storage
```

`s3` 是 Runner registry 为该能力登记的单 interface，因此不要求 Consumer 再增加 selector
参数。若未来增加第二个 interface，新的 Consumer 必须显式声明支持集合，既有 name-only
Consumer 仍固定为 `s3`。

## 2. 绑定与执行顺序

Runner 对唯一启用的 Provider 自动绑定，并记录：

```yaml
capability_bindings:
  example_consumer:
    object_storage: versitygw
    object_storage.interface: s3
```

Provider 自动进入 Consumer 的依赖闭包并在其前执行 `calculate`。没有 Provider、Provider
被禁用或存在多个候选时解析失败，不按产品名或目录顺序猜测。

## 3. 统一 S3 输出 ABI

Provider Hook 发布以下 Provider-neutral 字段：

| Provider 输出 | 含义 |
| --- | --- |
| `ANAS_OBJECT_STORAGE_S3_ENDPOINT` | 绝对 HTTPS S3 endpoint |
| `ANAS_OBJECT_STORAGE_S3_REGION` | SigV4 region |
| `ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID` | access key ID |
| `ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY` | secret access key；敏感 |
| `ANAS_OBJECT_STORAGE_S3_PATH_STYLE` | 是否强制 path-style；当前为 `true` |

Provider `calculate` 完成后，Runner 校验必填字段，并只向绑定 Consumer 投影：

```text
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__INTERFACE
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ENDPOINT
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__REGION
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__ACCESS_KEY_ID
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__SECRET_ACCESS_KEY
ANAS_OBJECT_STORAGE_BINDING__<MODULE>__PATH_STYLE
```

这些 key 属于目标 Consumer 的运行环境，由 Runner 保留；Consumer 无需写 `config.consumes`，
也不能从调用方输入预置覆盖。Consumer Hook 只负责把自己的 binding 翻译成上游应用配置。

## 4. Secret 与信任边界

Provider 输出和 Consumer binding 中的 `SECRET_ACCESS_KEY` 都继承 Secret 敏感性，不进入
plan、lock、deployment manifest、普通错误或无关 Module。Consumer 不会收到
`ANAS_OBJECT_STORAGE_S3_*` Provider-side namespace，也不会收到其他 Consumer 的 binding。

Capability 路径只提供一组 root credential；所有绑定 Consumer 实际共享这组权限。该能力
解决的是 Provider 解耦和配置形状统一，不是最小权限、bucket 隔离或多租户安全。任何绑定
Consumer 被攻陷都可能影响全部 bucket。

## 5. Capability 与 Resource 边界

Capability 只发布服务级连接信息，不执行持久资源生命周期：

- 不自动创建、删除或保留 bucket；
- 不为 Consumer 创建独立 AK/SK、policy 或 STS token；
- 不旋转单个 Consumer credential；
- 不承诺 bucket ownership、quota、versioning 或 object lock。

需要独立 bucket 与凭据的 Module 显式声明 `object_storage` 1.0.0 Contract + Resource。
Runner 为 `<consumer>.<resource_id>` 生成唯一 AK/SK 并只投影
`ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__*`；未声明的 Module 没有 Resource
副作用。VersityGW Provider 经私有 Admin API 实现 `ensure`、`inspect` 与
`rotate_credential`，移除声明默认 `retained`。`delete` schema 保留为可选 operation，当前不
隐式执行破坏性删除。

## 6. 验证

synthetic Runner 测试覆盖 name-only 声明、唯一 Provider 自动绑定、依赖顺序、binding 记录、
必填输出、Secret 隔离和缺失输出 fail-closed。Module Hook 测试覆盖统一字段与私有字段一致性。
真实 AWS CLI/SDK 互操作、重启持久性和空机恢复只能在真实环境判定，不在 synthetic 测试的
覆盖范围内。
