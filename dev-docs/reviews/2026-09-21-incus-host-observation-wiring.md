---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 受限宿主观察处理器与共享调用接线

基线：`claude/forgejo-docs-audit-20260920` / `3f5242e`，加前两轮未提交的镜像供给和观测校验修改。
直接修改原 checkout，保留已有暂存、未暂存与未跟踪文件；未切换分支、提交或推送。
前轮结果分别见[镜像供给](2026-09-21-incus-image-supply-readiness.md)和
[观测生命周期](2026-09-21-incus-observation-lifecycle.md)，不计为本轮新增实现。

## 本轮实际补齐的路径

此前只有宿主投影客户端，没有注册的 root 观察处理器。本轮加入 `incus.ingress.observe_http`，
使用既有 `anas-hostd` 的编译清单、严格参数、已认证对端、共享 job 绑定、审计与终态监督。
它是 30 秒预算的只读动作，采用 reject 并发，不需要破坏性确认，不新增监听器、通用 root RPC、
脚本执行入口或第二份 job 数据库。HOSTACT-R-012 的五问记录在[宿主通道计划](../plans/host-action-channel.md)。

Linux 后端读取固定 root 所有 observer scope、受管宿主 state/connection bundle 和已登记
workspace。调用方只能指定 scope ID，不能提供工作区路径、endpoint、证书、guest IP、设备名、
命令或 argv。scope 必须等于 job 获授权的 workspace ID，入队、队列恢复与执行绑定分别核对。
安装 scope 固定完整活动 HTTP 快照、显式 daemon 版本、随机宿主所有权 ID 与 bundle 摘要。

操作期间持有已存在的宿主状态锁、工作区共享锁，不创建锁或安装目录。受保护文件在有界读取
前后验证描述符和路径身份、祖先、权限、长度及内容，观察期间再次核对。当前 Core snapshot
必须与 scope 完全一致；非活动部署、未完成宿主意图、外部 daemon 或远端 Provider 不能进入。
Incus 必须已经运行，观察不能把停止服务隐式激活。

管理证书仅留在 root 进程中，通过固定 `https://127.0.0.1:8443` 与原证书 pin 使用。后端复用
`IncusFactReader.ObserveHostHTTP` 的双采样，不另造一套 Incus 数据校验。随后通过既有固定
可执行文件描述符机制运行只读 `ip` 查询，检查获授权 bridge 上的实际 veth、MAC、数值 ifindex
和 peer index；两组 API/内核样本均须相等。任一校验、取消、关闭或审计失败均不返回可用身份。
当前只接受 container；VM/TAP 明确拒绝，不降级为 container 证据。

## 投影与中介

内部投影为 `anas.incus-http-host-projection/v3`，补入请求/响应 workload 和授权 interface。
每次客户端生成新 observation ID；处理器收到本次调用后才读状态，返回值必须精确绑定请求。
响应仅含一个选定租约及该实例身份，不返回整个 manifest、原始 API 元数据、私密路径或凭据。
旧 v1/v2 拒绝；本次没有改变 executor journal 或宿主 publication receipt 的持久 schema。

历史字段名 `server_uuid` 在这条本机投影路径明确定义为既有 ANAS 随机 ownership ID 的 UUID
形表示，不是声称 Incus API 提供了此 UUID。宿主 scope 同时固定 ownership ID 和完整 bundle
摘要；中介必须预先固定该安装身份，不从第一次响应学习。

`HostProjectionReader` 实现 `FactReader` / `Observer`，只持有冻结非敏感授权和投影客户端，
不持有 Incus 凭据或 socket。它精确核对安装 ID、epoch、project、interface、完整端口/auth
策略和 workload。`HostObservationInvoker` 复用共享服务，每次发起无重试键的新 job，并在
等待期间重新核验权限、工作区、invocation 和最终 nonce。历史成功 job 不能充当当前观察；
放弃等待不取消已由宿主执行监督接管的只读任务，也不伪报 cancelled。

这提供了独立观察的受限宿主替代路径，**不是签发了一张服务端只读证书**。中介仍不能收到管理
凭据。观察是点时证据，不是持续分配、快速暂停未发生或 ifindex 永不复用的证明。

## 实际验证

新增 10 个顶层 Go 测试入口及多组子用例。后端测试使用明确的安装/Incus/kernel 接缝；native
JSON 测试不执行 `ip`；中介测试复用实际本机 mTLS 与合成 daemon 响应；共享 job 测试使用真实
Store、执行租约、审计和队列，但 root/systemd 执行器为测试适配器。不能将这些拼成已执行的
Linux 完整链路证明。

| 检查 | 本轮结果 |
| --- | --- |
| 窄请求、授权/类型边界、双采样漂移、审计与结果投影专项 | 通过 |
| 共享 job 新建、旧结果拒绝、跨工作区入队/恢复、取消等待专项 | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 通过；未变化的包允许 Go 缓存 |
| 五包 `go test -race -count=1` | incusprovision、incusingresshost、computeingressruntime、hostaction、jobexecutor 全部通过 |
| Linux amd64/arm64 五包测试二进制 | 十份交叉编译通过，没有在 Linux 上执行 |
| Module/Contract 生成、需求覆盖、需求与计划索引、文档状态 | 生成和最终检查全部通过；Incus 完成统计仍为 30/75 |
| 共享构建、升级目录 | 静态检查通过，不代表 Docker build 或实际升级 E2E |
| 双语文档构建、`git diff --check HEAD` | 通过；文档为 v0.1.1，仅有非阻断 chunk 大小警告 |
| 实际 root/Incus/Docker/Traefik/systemd/KVM 联合链路 | 未执行 |

开发中共享 Store 返回结构误写为 `Created`，编译器报错后改为实际 `Existing` 字段，再完成
专项、全仓和竞态检查。没有跳过编译错误或扩大协议来通过测试。第一次 Linux 编译会话回收后
未能取得完整终态，因此重新编译并保存完整结果日志，未把二进制存在直接当作执行通过。

## 未完成的安装与验收

固定 observer scope 仍需显式、受保护的安装输入；**自动交付、epoch 切换/轮换与撤销协调、生产
中介启动/UID/挂载尚未实现**。新增动作可经已有共享服务执行，但不等于部署默认会自动创建
上述配置或启动控制循环。没有新增公开下载/凭据端点，没有安装或启动实际服务。

原生 root 文件读取、真实 mTLS/Incus/native veth 联合路径和队列/审计长期吞吐仍需验收。
health 身份、VM/TAP、内核持续生命周期、采样间快速暂停、IP/ifindex 复用、双向旧 TCP 会话、
真实崩溃与事件丢失恢复未完成；签名镜像发布、烘焙/启动、配额、双栈、one-job、回滚及 prune
实机矩阵仍按原计划跟踪。没有改动生产 publication gate；模块保持 `7.3.0-r2 / developing`，
需求完成统计不得仅因新增代码而增加。

最终读回确认 HEAD 仍为 `3f5242e`、分支未改变、正式镜像目录仍为空；环境是 macOS arm64 / Go
1.26.6，未发现 `test-env/targets.local.yml`。未自行选择生产 SSH 主机，未运行包安装、守护进程
启停或宿主网络变更。前两轮暂存/未暂存文件继续保留，没有执行 Git commit 或 push。
