# Incus Image Prune Integration Review

日期：2026-09-19

## 结论

本次接入完成 `INCUS-R-072` 的显式镜像清理纵向路径：新增编译动作
`incus.image-prune.plan` 与 `incus.image-prune`，通过既有 owner 认证的 console job
队列、五分钟一次性确认和 root host action executor 执行。普通 apply、镜像 ensure 和
Provider 导入路径未加入自动删除。

## 已实现边界

- HTTP 新增 `POST /api/v1/workspaces/{ws}/host/actions/incus/image-prune/plan` 与
  `/apply`。plan 请求只接受空对象；apply 请求只接受 `plan_job_id` 与确认 token。
- 非 root daemon 从已认证 `{ws}` 路由生成 opaque workspace 参数；客户端不能提交
  fingerprint、delete/retain 列表、project、endpoint、路径、脚本或命令。
- root 端固定读取 `/etc/anas/anasd.yml`，只接受 service config 注册的 workspace。
- root 端在读取 deployment 状态和执行前持有 `.anas/state/lock` 共享锁；活动
  transaction、缺失当前 deployment 或损坏 manifest 均 fail closed。
- prune 候选只来自历史 deployment 中 `compute` / `incus` resource 冻结的
  `ComputeImages` 和对应 sandbox project；当前 deployment、previous deployment、
  Incus instance `volatile.base_image` 引用和未知外部镜像均保留。
- Incus inventory/delete 复用同一 `incusUnixClient` 类型和固定 Unix socket envelope。
- apply 重新计算 plan 并比对 plan digest、state digest 与原批准 delete 集合，变化时拒绝。

## 验证

- `GOCACHE=/private/tmp/anas-gocache go test ./internal/incusprovision ./internal/jobexecutor -run 'TestImagePrune|TestConfirmedImagePrune|TestConfirmedIncusPlan'`
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/hostaction ./internal/api/httpapi ./internal/incusprovision ./internal/jobexecutor -run 'Test.*ImagePrune|TestOpenAPITracksImplementedSurface|TestEveryRouteDeclaresSecurityMetadata|TestProvisionProgressAllowsOnlyCompiledPhaseWithoutFreeText'`
- `GOCACHE=/private/tmp/anas-gocache go test ./internal/consoleclient ./internal/runner ./cmd/anasd`

## 未完成验收

未在真实 Linux/systemd/Incus 宿主上执行 destructive prune。当前 macOS 环境不能证明
`/var/lib/incus/unix.socket`、systemd root/root 激活身份、原生 Incus operation wait、
或真实镜像删除 readback 在目标宿主上的端到端行为。
