# Incus 固定控制转发接续核对

> 状态：历史记录；2026-09-18，基线 `f7642c5` 加未提交工作树。这里只记录代码自检和待验收边界，
> 不是需求级评审批准、CI 通过或实机验收。工作期间存在并行写入，仅凭基线提交不能复现全部工作树。

## 核对来源与范围

已核对需求/计划索引、两份 `incus-module.md` 与宿主供给架构；进度仍以
[Incus 计划](../plans/incus-module.md)的里程碑表为准。本记录聚焦
[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §3.6—§3.8 的非 root 固定控制转发，
以及进程清理未确认时不得交接执行租约的保护。不把已有但未运行的代码标为已验收。

## 本次新增与补强

| 位置 | 内容 | 验证状态 |
| --- | --- | --- |
| `modules/incus/control-relay/config.go` | 有界严格安装配置；拒绝任意目标、TLS key、通配/回环/IPv6/特权端口及不匹配的接口网段 | 仅源代码与格式核对 |
| `modules/incus/control-relay/platform_linux.go` | root 所有配置及逐级目录描述符；拒绝符号链接、多硬链接、可写祖先、元数据漂移；专用非 root UID/GID、附加组与 capabilities 核对 | 未做 Linux 身份/文件竞态验收 |
| `modules/incus/control-relay/relay.go` | 编译期固定的 `127.0.0.1:8443`；连接容量、拨号/空闲期限、双向半关闭、关闭连接及接口漂移停机 | 未编译、未运行传输测试 |
| `modules/incus/control-relay/main.go`、`platform_other.go` | 显式安装配置入口；错误不回显配置/网络值；非 Linux 拒绝运行 | 未安装或启动服务 |
| `internal/consolejobs/execution_lease.go` | `Retain` 守卫；未释放的监督执行拒绝 `Close` 交出执行所有权，释放幂等 | 回归测试源码已补，未运行 |
| `internal/jobexecutor/module_action_group_linux.go` | 固定 procfs 视图、PID 绑定与 hidepid 拒绝；有界进程组盘点；不可把受限视图当成空组 | Linux 解析回归源码已补，未运行 |

原工作树中的动作协议、journal、dispatcher、镜像等续作继续保留。曾在连续读取之间出现并行修改
及重复错误常量；本记录前最后一次静态读取中已不再重复。该观察不等于整个变更集已经通过编译。

## 新受管服务的五问自检

本表回应 `HOSTACT-R-012` 的设计问题，不代替后续宿主动作清单评审。

| 问题 | 当前边界 |
| --- | --- |
| 能否不用 root？ | 转发进程不需要 root；代码拒绝 root、身份不一致与 capabilities。安装网络和管理系统服务仍须已有宿主特权通道，未在此新增提权入口。 |
| 权限是否最小？ | 仅固定目的地 TCP 字节复制；不读取 Incus 管理/消费者私钥，不使用 Docker/Incus Unix socket，不接受命令、upstream 或 CONNECT/SOCKS。专用服务账号和无额外附加组仍需安装器落实。 |
| 留下什么特权产物？ | 此源码不创建宿主产物。未来安装需要受信二进制、root 所有配置、服务单元、控制网络和规则；不能让 root 执行 Module 可写目录中的文件。 |
| 有无对称撤销？ | 进程停止会关闭监听与现存连接；endpoint、规则、网络和服务单元的所有权盘点及对称卸载尚未实现，不能据此把安装阶段打勾。 |
| 失败与重跑是否收敛？ | 配置/身份/接口不匹配时拒绝；运行中漂移停止而不接管新接口。跨安装阶段的状态机、部分失败恢复、重启时接口重建与配置再生成仍待实现。 |

## 不能越过的边界

源地址 CIDR 不是入接口证明或客户端认证。必须先由宿主动作建立控制 bridge、INPUT/FORWARD 默认
拒绝及来源接口限制，再启动传输，独立验证 mTLS、错误 pin、project 和跨网络隔离，最后才投影
endpoint。禁止用公网监听、host network 或 root Unix socket 弥补未完成的供给路径。

该组件只实现 IPv4 控制传输；IPv6 的拒绝规则与数据面双栈要求未因此取消。当前接口 index 来自
安装快照，接口重建不能自动信任同名新接口。进程组检查是可信 Module 执行器的排空核对，不是对
恶意执行器的 sandbox，也不是完整 daemon 崩溃恢复。执行清理失联时仍需保留锁/租约与独立恢复证据。

## 剩余工作与验收入口

M10 尚缺声明式发行版安装实现、受限宿主动作、受信二进制发布/服务单元、网络/配置所有权状态机、
endpoint 接线、重启恢复及对称卸载。M11 的宿主网络盘点/受限恢复和生产发布不能仅凭本组件启用。
M6/M9a、M8b、M12/M13 的既有剩余项继续按计划跟踪；本次没有替它们宣告完成。

新增低层回归入口如下，均未执行：

```sh
go test ./modules/incus/control-relay
go test ./internal/consolejobs -run 'TestRetainedExecutionLeaseCannotTransferOwnership|TestNilExecutionLeaseCannotBeRetained'
# 以下解析测试只在 Linux 构建中存在。
go test ./internal/jobexecutor -run 'TestModuleActionProcMountVisibilityBinding|TestModuleActionProcessStatDelimiterBinding'
```

完整宿主验收见 [E2E 待测清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md) 的固定控制
转发章节。需独立 Linux 宿主，覆盖 Debian 13、Ubuntu 24.04 和 Ubuntu 26.04；本地字节转发测试
不能替代防火墙、证书权限、故障恢复及卸载证据。生产 ingress 继续关闭。
