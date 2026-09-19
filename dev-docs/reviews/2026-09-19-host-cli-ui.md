# 宿主预检 CLI 与控制台入口接续核对

本轮只补入口与能力披露，不改变 `internal/hostaction`、job executor、安装器或宿主写动作。
`incus.status` 仍是只读 installation-preflight；成功入队或执行成功都不表示 Incus daemon、
KVM、存储、网络或 compute ready。

## 已实现

| 范围 | 结果 |
| --- | --- |
| CLI | `anas host incus-preflight -w WORKSPACE --session-json - [--idempotency-key KEY] [--json]` 调用现有 `POST /api/v1/workspaces/{ws}/host/actions/incus.status`；`anas host job JOB_ID --session-json - [--json]` 复用公开 job 查询；`anas host actions` 保持本机编译清单兼容 |
| CLI 认证 | 会话信封只从 stdin 读取，要求规范 HTTPS origin 与 CA PEM；按 `local` / `oidc_proxy` 映射既有 session cookie，写请求带 Origin、CSRF 与 Idempotency-Key |
| CLI 传输边界 | 不打开 root socket、不读 job store、不启动独立执行器；禁用代理环境、拒绝重定向、限制响应大小；未知结果返回 `unknown_execution` 且不自动重试 |
| HTTP 能力披露 | `/api/v1/system` 的 `capabilities.host_actions.incus_status` 仅在 handler 实际装配 HostActions 时出现；direct 与 trusted_proxy 走同一 Options 投影 |
| 控制台 | 维护页在 capability 存在时显示 Incus 宿主预检按钮，只创建 durable job，不显示 install/uninstall/prune，不要求 root 密码，不把预检标为 compute-ready |
| 文档 | 中英文命令契约记录 stdin 信封、安全用法、无 TLS 绕过、无代理/重定向、未知执行不重试与 job 查询方式 |

## 验证

```sh
GOCACHE=/private/tmp/anas-go-build go test ./internal/consoleclient ./internal/runner ./internal/api/httpapi
npm run generate:api
npm run typecheck && npm run test && npm run build:main && npm run build:emergency
```

Go 默认 cache 位于 sandbox 不可写的用户缓存目录，已将 `GOCACHE` 指向 `/private/tmp/anas-go-build`
后执行。`internal/consoleclient` 使用 `net.Pipe` + TLS 的本地夹具，不监听端口、不访问真实网络。

## 未验证 / 未完成

- 未执行真实 systemd、root `anas-hostd`、Incus/KVM、软件包安装、服务控制、控制网络或入站验收。
- 未实现安装、卸载、configure、prune 或二段确认动作。
- 未改变 host action POST 路由契约；OpenAPI 只增加 system capability schema。
- 未更新需求/计划状态统计；Incus 与宿主通道里程碑仍按现有计划继续跟踪。
