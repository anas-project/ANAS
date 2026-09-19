---
doc_type: review
status: current
created: 2026-09-19
updated: 2026-09-19
---

# Incus 宿主供给后端实现核对

## 结论

本轮复核并修正 `internal/incusprovision` 的安全边界。该后端仍**不得接入生产写动作**：没有在真实
Linux/Incus/Docker/nft/apt/systemd 宿主执行验收，也没有接入 HTTP、CLI、`anasd` 或 hostaction
registry。当前结果是 fail-closed 的本机单元层实现，不是默认可用证明。

新增 systemd 单元 `packaging/systemd/anas-incus-control-relay.service`，固定运行
`/usr/local/lib/anas/anas-incus-control-relay --config /etc/anas/incus-control-relay.json`，使用
专用非 root `anas-incus-relay` 身份，并收紧 no-new-privileges、无 capabilities、只读配置和 systemd
sandboxing。发布打包仍需安装 relay 二进制和 root-owned 配置文件；本轮没有启动服务。

## 导出 API

`internal/incusprovision.NewLocalBackend()` 返回生产后端。公开方法：

- `Inspect(ctx, Request) (InspectResult, error)`
- `Plan(ctx, Request) (Plan, error)`
- `Install(ctx, Request, Binding) (ApplyResult, error)`
- `Configure(ctx, Request, Binding) (ApplyResult, error)`
- `Enroll(ctx, Request, Binding) (ApplyResult, error)`
- `Uninstall(ctx, Request, Binding) (ApplyResult, error)`

`Request` 只包含 `skip`、`interface`、`storage_size_gib`、`remove_packages`。没有命令、argv、脚本、
路径、URL、firewall 文本或外部 callback。所有 mutating 方法都要求 `Binding{schema, phase,
plan_digest, destructive:true}`；执行前会重新 observe 并重算 digest，漂移返回 `ErrDrift`。

公开 `Inspect`/`Plan`/mutating result 不再返回 connection bundle、endpoint、证书或私钥。root 侧如需
读取持久化连接材料，必须显式调用 `ReadPrivateConnectionBundle`，并承担本地 root-only 状态文件边界。

## 已实现边界

- 发行版来源继续复用 `internal/incushost` 的声明式 recipe 表；未知发行版、架构、init 或缺 KVM 的
  VM 请求保持 disabled，不安装第三方源。`LocalPreflight` 的旧诊断 blocker
  `host_actions_not_installed`、`package_origin_unverified`、`daemon_compatibility_unverified`、
  `storage_network_unverified` 不再阻断 install plan，但仍以 warning 形式保留，且不让 compute ready。
- 固定 root-owned 状态：`/var/lib/anas/incus-host/state.json`；连接 bundle：
  `/var/lib/anas/incus-host/connection.json`。mutating phase 获取进程间文件锁；每个 effect 前先持久化
  intent 和稳定 ownership id，读回确认后再持久化 receipt。保存失败会与 effect 失败一起返回。
- 包状态不再以 `/usr/bin/incus` 是否存在判断；生产路径使用 `dpkg-query` 读取安装状态。apt install/remove
  只接受固定 operation enum，并要求私有 apt policy/source 文件存在、root-owned、包含 signed-by 官方源；
  缺失时 fail closed。
- Incus 本地配置只在 `incus.service` 明确 active 后访问固定 `/var/lib/incus/unix.socket`，并检查 socket
  root ownership；Inspect 不通过 Unix socket 激活 inactive daemon。Incus 404 只有真实 Incus error
  envelope 才表示 absent。
- server certificate 不再从 unauthenticated TCP 读 TOFU；改读固定 root-owned `/var/lib/incus/server.crt`。
- 默认存储池只创建带稳定 owner id 的 `anas-btrfs` btrfs pool；同名外部 pool 或 driver/config 不匹配会拒绝。
- Docker 控制 bridge 创建带稳定 owner id 的 `anas-incus-control`，并枚举 Docker/Incus/host CIDR；重试复用
  持久化 subnet/gateway/interface identity，创建后重新 observe 再生成 relay config。
- 防火墙生成固定 nft ruleset，不接受调用方文本；规则限定 managed listener address/bridge，并加入 IPv6
  deny 与 ownership marker。当前 readback 仍是文本级 canonical marker 检查，真实 nft 兼容性未验收。
- 管理证书使用 ECDSA P-256 生成，先持久化 root-only 状态，再 trust；trust 后读回，连接 bundle 只写
  root-only 文件，不进入公开 result。host-side pinned check 只产生 `connection_ready`，不宣称
  `compute_ready`。
- 卸载先检查无 running managed guests；只删除 ownership 记录中的 trust、relay、firewall、Docker
  network、空的 storage pool 和 bundle。running guest 判断改为 all-projects inventory 中是否使用 owned
  pool/network，不再依赖 `anas-` 名字前缀。包删除必须显式 `remove_packages:true`，且不删除外部 daemon 包。

外部预存 Incus daemon/package 只会被标记 preserved；当前实现拒绝修改原本外部 daemon，而不是承诺卸载时可
恢复外部 daemon 设置。镜像导入本轮不实现；`Enroll` 只报告 image import 未实现，不返回镜像 ready。

## 仍有限制

- 未实现或未验收真实 apt policy 文件发布、真实官方 package origin 检查、native Linux 安装/卸载验收。
- nft readback 仍需在真实 Linux 上验证语法、table ownership marker、atomic replacement、Docker/Incus 规则优先级和 IPv6 行为。
- Docker/Incus inventory parser 已进入单元测试，但真实 Docker daemon、Incus daemon、storage volume 空池删除和 all-project guest 阻断未跑。
- relay 连接的实际 bridge 可达性、mTLS pin、跨 project 拒绝、LAN/guest 不可达和服务重启行为未跑。
- 该 backend 因上述真实宿主证据缺口仍不应接入 host action registry。

## 验证

已运行：

```text
GOCACHE=/private/tmp/anas-gocache go test ./internal/incusprovision
GOCACHE=/private/tmp/anas-gocache go test ./internal/incushost
```

结果均通过。默认 Go cache 在本 sandbox 中不可写，改用 `/private/tmp/anas-gocache`。

`GOCACHE=/private/tmp/anas-gocache go test ./modules/incus/control-relay` 未通过环境门禁：sandbox 禁止
`listen tcp4 127.0.0.1:0`，失败发生在测试 fixture bind 阶段，未触发业务断言。

未执行真实 Docker、Incus、apt、systemd、nft 或 root host 操作；本轮证据是纯单元/fake runtime 与
编译层证据，不是实机验收。
