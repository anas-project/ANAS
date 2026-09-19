# Incus HTTP ingress host backend correction review

日期：2026-09-19

## 结果

本轮修正 `internal/incusingresshost` 和
`internal/computeingressruntime.IncusHostBackend` 的边界。当前代码仍**不允许生产发布**，也没有接线
`anas-hostd` 写动作、安装器、前端、真实 Incus/Docker/Traefik/nft/ip/conntrack/systemd 或远端操作。
本轮所有验证只使用私有 fixture。

## 导出集成合同

- `incusingresshost.NewLocalInstalledBackend(ctx, scopeID, resolver)`：生产 root 侧唯一构造入口。
  调用方只能给 opaque `scopeID` 和 resolver；固定读取
  `/etc/anas/incus-ingress/scopes/<scopeID>.json`。安装文件 DTO 为
  `InstalledScope` / `ProductionGate`，不包含命令路径、nft 文本、namespace 路径、Incus socket 或证书。
  非 Linux fail closed；Linux 路径逐级 root-owned/不可写校验，二进制固定为 `/usr/sbin/ip`、
  `/usr/sbin/nft`、`/usr/sbin/conntrack` 并在执行前重新校验。
- `incusingresshost.ProjectionClient`：非 root mediator 只能调用
  `incus.ingress.observe_http`，请求/响应 schema 为
  `anas.incus-http-host-projection/v1`。响应必须包含完整授权白名单和 fresh identity；重复 lease、
  未授权端口、消费者自报 UUID/IP 都不能授权发布。
- `computeingressruntime.IncusHostBackend`：非 root host-action client adapter，只序列化
  `anas.compute-http-incus-host-action/v1` typed request。它不再持有或直接调用
  `incusingresshost.Backend`；父级必须经 trusted job/host-action channel 连接到编译进 hostd 的 root 动作。

## 已修正的缺陷

- 移除了公开 `Config.FixtureMode` / `New(Config)` / exported arbitrary binaries。fixture 构造仅在包内测试使用。
- receipt 目录有跨进程 `.lock` guard；每个 host effect 前有 durable intent，读回确认后才更新 ready receipt。
- route 不再 `replace` 覆盖；写入前证明目标 route 为空或已是同一 owned tuple。解析支持 IPv4 host route
  与数字 protocol，外部/foreign route 阻断。
- route namespace 读回现在校验 ifindex、namespace cookie/device/inode、Docker container id/startedAt、
  peer ifindex/MAC、Traefik source/gateway，并在 route effect 前后复核。
- nft baseline 改为解析 JSON，校验 chain hook/policy、counter/drop verdict 和 owner marker；comment 中伪造
  “drop” 不再算证明。permit 创建按库存 add/renew，重复运行不 append duplicate；删除前校验完整 owned
  rule/set identity，不能只按 comment 删除。
- command output 溢出变错误；错误不回显任意 stderr。JSON 观察加大小、深度、元素和整数范围边界。
- conntrack 改为读取 extended inventory 并解析 exact tuple；命令失败不是“空”。合法 active candidate
  connection 不触发撤销，permit/route 已撤销后的同 tuple connection 会阻断 release。
- 多个 publication 共用同一 guest IP 时 route ownership 按 active receipt refcount 解释，释放其中一个端口
  不删除另一个端口仍需的 `/32`。

## 仍未完成

生产 publish 仍 fail closed。原因是本仓库尚未在真实宿主证明以下合同：

- durable Incus DHCP reservation 或等价的 veth/ifindex/MAC/incarnation lifetime 证明能阻断快速 IP 复用；
- opened namespace fd / thread-locked setns 执行器，而不是可替换的 namespace path/name；
- Docker/Incus base chain 共存、IPv4/IPv6 fence、长连接撤销和 startup orphan recovery 的实机证据；
- 生产 health identity。现有 `FixtureHTTPProbe` 只适用于 `.example.test` fixture，不是通用 app health 合同。

因此不得把本 backend 注册为生产 `anas-hostd` 写动作，也不得把 native harness 结果当真实验收。

## 验证

已运行：

```text
GOCACHE=/Users/whl/Documents/anas/.cache/go-build go test ./internal/incusingresshost ./internal/computeingressruntime
```

结果：通过。未运行真实 host nft/ip/conntrack/Incus/Docker/systemd/sudo/remote 操作，未运行全仓门禁或 Linux
原生验收。
