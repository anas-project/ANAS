# object_storage Contract 技术说明

## 声明模型

`object_storage` 1.x 当前提供 `s3` interface。Consumer 以 Resource opt-in：

```yaml
dependencies:
  contracts:
    - name: object_storage
      version: ">=1.0.0 <2.0.0"
      selected_by: object_storage_type
      interfaces: [s3]
      default: s3
resources:
  requires:
    - id: objects
      contract: object_storage
      binding: object_storage_type
      spec_from:
        bucket: object_bucket
      spec:
        credential: {policy: generated}
        deletion_policy: retain
config:
  defaults:
    object_storage_type: auto
    object_bucket: example-objects
  types:
    object_storage_type: {enum: [auto, s3]}
    object_bucket: string
```

删除这两段声明的 Module 不参与 Resource 解析，也不会收到对象存储凭据。`access_key_id` 可在
spec 中显式指定；省略时 Runner 从 `consumer + resource_id` 确定性派生一个不超过 64 字符的
唯一值。

## 生命周期

Runner 为每个 Resource 生成并稳定保存独立 Secret，在 Consumer 启动前调用 Provider 的幂等
`ensure`。Provider 必须创建或校正 IAM user、Secret、bucket owner，并实现只读 `inspect`。
`rotate_credential` 与 `delete` 是 1.x 可选 operation；当前移除 Module 或 Resource 声明只把状态
标为 `retained`，不会隐式删除 bucket 或对象。

就绪连接通过 Consumer 私有命名空间发布：

```text
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__REGION
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__BUCKET
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__ACCESS_KEY_ID
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__SECRET_ACCESS_KEY
ANAS_OBJECT_STORAGE_RESOURCE__<MODULE>__<RESOURCE_ID>__PATH_STYLE
```

其中 `SECRET_ACCESS_KEY` 标为敏感值，只属于目标 Consumer。部署清单和 Resource state 只保存
Secret Store 引用，不保存明文。

## 安全边界

独立凭据限制 Consumer 只能访问分配给自己的 bucket；它不是 STS、细粒度 IAM policy、quota、
versioning 或 object lock。Provider 的管理 API 必须保留在私有容器网络，不得经 Traefik 或宿主
端口公开。`deletion_policy: delete` 仅表达显式删除意图；在 Core 提供破坏性 Resource delete
入口前仍按 retain 处理。
