---
doc_type: review
created: 2026-09-19
updated: 2026-09-19
---

# Incus 宿主连接 bundle 自动投影核对

## 结论

本次在 `modules/incus/hook` 内完成宿主私有连接 bundle 到 Incus Provider calculate Hook 的自动接线。
显式远端配置保持原有高级路径；只有四项连接配置全部为空时，Hook 才读取固定宿主文件
`/var/lib/anas/incus-host/connection.json`，并把 endpoint、server certificate、管理证书和管理私钥
同时投影到 Env 与 module Secret Store。

## 已核对边界

- 固定文件路径不是配置项，也不接受环境变量覆盖；测试 seam 只允许单测替换临时路径和可信 UID。
- 自动读取要求安全祖先、单链接普通文件、`0600`、读前读后身份一致、大小上限、严格 JSON、
  无未知字段和无重复字段。
- 自动 bundle 必须声明新增字段 `architecture`（`amd64`/`arm64`）和 `storage_pool`（`anas-btrfs`）；
  缺字段的旧 bundle 拒绝，不猜宿主架构或存储池。
- bundle 约束固定为 `endpoint=https://<control_gateway>:18443`、
  `control_network=anas-incus-control`、`relay_service=anas-incus-control-relay.service`，并校验管理证书、
  私钥 pair 与 `management_fingerprint`。
- Secret Store 保存 module 私有来源标记和绑定摘要；重复 apply 重新读取固定 bundle 并要求摘要一致，
  删除、替换或漂移不会回退到历史 Secret，也不会自动轮换。
- 四项显式远端完整时不读取宿主 bundle；部分显式输入 fail closed，避免显式值和自动值混用。
- `storage_pool` 的 manifest 默认改为空值；显式远端路径仍在 Hook 内补 `default`，自动宿主路径使用
  bundle 的 `anas-btrfs`，以便显式不一致时可拒绝。

## 需要父级同步

宿主供给写 bundle 的代码仍需把 `architecture` 与 `storage_pool` 写入
`anas.incus-connection-bundle/v1`。在父级新增前，当前 Hook 会按设计拒绝旧 bundle 自动接入。

## 验证

已运行：

- `gofmt`：仅 `modules/incus/hook/main.go`、`modules/incus/hook/host_bundle.go`、
  `modules/incus/hook/main_test.go` 与 `internal/runner/config_schema_inventory_test.go`。
- `GOCACHE=/private/tmp/anas-gocache go test -count=1 ./modules/incus/hook`
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/runner -run TestBundledParameterSchemaEvidenceInventory`

未执行真实 sudo、网络、Incus 或系统文件修改。
