# 批量数据路径安全审阅（INCUS-R-083）

状态：审阅结论与新增门禁。日期：2026-09-26。基线：`fe85e510` 加本轮工作树。

`INCUS-R-083` 要求批量数据只走「动作自己打开目的地」一条路径：不提供经浏览器取回的制品下载端点，
不在控制流内联 base64；将来开放下载前必须一并定案有效期、可否重复使用与非浏览器客户端，且不得使用
URL 内明文 token。这是[统一动作 ABI](../../docs/architecture/action-abi.md) §8、§13 的约束，本次按
计划 M13 的剩余项做整体审阅。

## 审阅范围与结论

| 面 | 检查 | 结论 |
| --- | --- | --- |
| HTTP 管理面 | `internal/api/httpapi` 的路由清单（`RouteInventory`）与 `api/openapi.yaml` 的响应媒体类型 | 除公开内部 CA 证书外没有附件响应；无 `application/octet-stream`、归档或任意文件响应；`/backups` 只列出记录，快照只有动作端点 |
| 公开 CA 下载 | `GET /api/v1/system/ca`（`downloadInternalCA`） | 小于 1 页的公开 PEM，不是 job 产物，也不带 token；已有入站与隐藏边界测试 |
| 控制流帧 | `internal/actionabi` 帧上限 64 KiB、`internal/consolejobs` 载荷上限 1 MiB | 超限帧被拒，事件尾部截断会显式标记；执行器无法借事件或结果内联大块数据 |
| 镜像产物 | `incus-image-artifacts export`/`bundle`、`ArtifactArchive.ExportBundle` | 由命令自行打开显式本地目的地写入，stdout 只输出元数据 JSON；拒绝 `--download-url` 与 `--image-base64` 且不回显 |
| Provider 镜像供给 | Core 只读挂载描述符与副本，Provider 经 Incus API 导入 | 字节从本地文件直达 daemon，不经控制台或 job 事件 |
| 宿主动作 | `incus.image-prune` 等 plan/apply 与确认 | 只有元数据参数与结果；删除在 hostd 内执行 |
| Web 前端 | `web/src` 中的下载链接 | 只有指向公开 CA 的一处 `download` 链接 |

未发现违反 R-083 的路径。控制台里另有一处带 `url` 字段的响应（本地管理员 reveal 返回服务登录地址），
它不是制品，也不携带 token。

## 新增门禁

`internal/api/httpapi/bulk_data_boundary_test.go` 的 `TestNoArtifactDownloadResponseMediaTypes` 把
OpenAPI 的响应媒体类型钉在 JSON、problem+json、事件流与静态前端资源上，证书附件只允许
`GET /api/v1/system/ca` 一处。今后新增制品下载端点必须先改这条测试，而改它之前须按 action ABI §13
一起定案有效期、重复使用与非浏览器客户端。

## 验证

- `go test ./cmd/incus-image-artifacts/ ./internal/computeimage/ ./internal/actionabi/ ./internal/consolejobs/ -count=1` 通过；
- `go test ./internal/api/httpapi/ -count=1` 通过（含新门禁、`TestInternalCADownload*` 与 OpenAPI 路由一致性）。

这些是审阅加单元层证据。它们不证明统一动作 ABI 的其他部分（取消、合流、宿主通道授权）已经验收，
那些仍归各自计划。
