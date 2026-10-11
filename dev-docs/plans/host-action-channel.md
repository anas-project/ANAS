---
doc_type: plan
status: implementing
created: 2026-09-04
updated: 2026-10-10
---

# 宿主特权动作通道实施计划

2026-09-23 最新：Ubuntu 26.04 amd64 的实际已安装 CLI/HTTPS→共享 job→systemd hostd
已通过完整 23 项门禁，含真实确认后安装/配置/登记/卸载、跨工作区拒绝、重放拒绝、真实
五分钟过期及新计划执行，并有 Docker 控制桥传输与拒绝验证。最新 VM 正常关机，独立
宿主对照一致，见[完整接续记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-23-incus-consumer-control-bridge.md)。
此前第十一轮 17 项通过与 VM 看门狗退出/只读证据恢复分别见
[已安装审批记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-23-incus-installed-approval.md)。这是主要发行版的通道
验收，不是正式发行安装器的完整升级矩阵、非 systemd 支持或全部动作/浏览器交互验收。

2026-09-21 接续新增观察范围配置的具名 plan/执行动作，操作限定 refresh/disable，沿用共享
确认与审计，不增加 socket 或特权入口；客户端不能提交 scope 内容。中断后的 pending 状态
阻止继续观察，明确撤销保留墓碑。CLI/HTTP 同步接线，真实 root/systemd 验收仍待执行。
实现与五问审阅见[观察配置交付核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-21-incus-observer-configuration.md)。

验收依据是[宿主特权动作通道要求](../requirements/host-action-channel.md)；设计见
[同名架构文档](../../docs/architecture/host-action-channel.md)。

**2026-09-30：身份链已简化（§14）——对端只认 root/root，调用记录取代回连 broker 与 systemd 退出观察；下一行是简化前的状态。**

**当前状态：保留现有 root/root anasd，按固定 systemd 服务身份准入；共享队列、独立退出观察、Incus 计划/确认/执行、CLI/Web 和安装器已编码。主要发行版已通过真实 Linux/systemd/Incus 审批链路；完整发行版/升级/浏览器交互矩阵、非 systemd 支持及生产 ingress 仍未完成。** 下文按日期保留历史切片；其中“必须迁移非 root anasd”的前置判断已撤回，不再是当前实施方向。它依赖[统一动作 ABI](action-abi.md)——通道复用那套
线格式与 job 语义，不另起一套。截至 2026-09-18，ABI 前置已有共用 journal、执行 recorder、
非特权 Module 注册表/Linux 进程适配、ModuleActionWorker 及调用/查询/订阅服务的内部代码；
动作级幂等、终态后 1 小时保留和 coalesce/reject/queue 已接入同一 journal，仍无 root 注册表。
当时这些代码与回归源尚未编译/运行；之后已完成的本机回归与交叉编译分别记录于 §3、§8，
主 daemon 与旧 CLI/HTTP 执行入口仍未迁移。Module worker 不领取宿主
命名空间的动作，不能据此注册 Incus HTTP 宿主动作或宣称 root 通道完成。没有新增 root RPC、
脚本入口或 CAP_SYS_ADMIN helper。

## 1. 需求归属与状态

普通部署/维护 worker 的后续接续已共享同一个协调器，防止它在宿主队列之外更换旧中介依赖。
普通写任务在领取前非阻塞等待，旧清理仍可进入宿主队列；本轮不新增 root 动作或批准方式。
独立 CLI、生产启动器与跨进程持久恢复仍待接入，见
[工作区任务核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-21-incus-workspace-mutation-gates.md)。

2026-09-21 接续将已确认配置任务与中介排空屏障连接到共享队列及实际 broker 授权回调。
等待清理不占用 root 执行位置；正常停机保留只读队列/租约至清理完成，失败须可信所有者显式
重试，不增加 root 动作或 HTTP 接口。生产 launcher、异常跨进程恢复和实机证明仍缺，不能提升
需求完成状态。实施与验证见[配置排空协调记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-21-incus-ingress-coordination.md)。

2026-09-21 增加编译只读动作 `incus.ingress.observe_http`，对应 INCUS-R-063/R-087/R-088 和
HOSTACT-R-001/R-002/R-005/R-012/R-013。它复用本通道而非另起 root 服务；未自动安装 observer
scope，也未开启 ingress。以下五问是本次需求级动作评审，不代表真实宿主验收：

| 问题 | 本动作的约束 |
| --- | --- |
| 能否不用 root | 远端路径可采用服务端真正只读身份，但现有本机 TLS 管理凭据不能交给中介；本候选由既有 hostd 持有并只返回选定事实，不改变 daemon 全局授权 |
| 是否最小权限 | 请求只含固定 schema、逐次 observation ID、scope/epoch/deployment、租约、实例、workload 与端口；没有路径、endpoint、证书、设备名或 argv。scope 必须等于获授权 job 的工作区 ID |
| 留下什么特权产物 | 观察不创建 scope、锁、服务、证书、网络、规则或镜像；只产生既有 job 与审计记录。安装 scope 是独立受保护输入，不由观察请求创建 |
| 如何撤销 | 停止/切换活动部署、删除 scope、撤销或改变连接 bundle 都使后续观察失败；观察没有网络变更需要反向执行。已有路由仍须走原独立撤销链 |
| 半途失败与重跑 | 任一读、复核、关闭或审计失败不返回可用身份；新观察使用新 job/nonce，不 coalesce、不读历史结果。等待方取消不伪装为宿主任务取消，执行监督沿用原通道 |

代码与本机回归见[受限宿主观察接线](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-21-incus-host-observation-wiring.md)。只读动作
最长 30 秒；周期性 root 观察的开销、队列争用与日志容量必须实测后才可默认启用，不能把安装动作的
罕见调用预算直接当作运行时控制循环的吞吐保证。M0/M1/M2 状态及真实宿主退出条件不因此提前完成。

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：通道形态、授权与审计 | R-001—R-004、R-018 | 实施中；主要发行版的实际 CLI/HTTPS、共享队列、root 身份/退出及供给闭环已通过 23 项原生门禁；R-018 单元与 finance 实机通过（2026-10-11，§15）；完整矩阵与其他动作独立验收 |
| M1：入口划分与升级绑定 | R-005、R-006、R-017 | 实施中；同版本打包、CLI/HTTP 与安装器已接线，升级拒绝覆盖活动宿主动作；真实安装/升级/卸载未验收 |
| M2：长时动作与非 systemd 可移植性 | R-007、R-008 | 实施中；按编译动作预算超时并投影固定阶段事件，非 systemd 启动和实机长时动作待办 |
| M3：二段确认 | R-009—R-011 | 实施中；真实 root 执行、跨工作区及重启后重放拒绝、五分钟自然过期拒绝旧计划并要求新计划均通过；浏览器页面重新展示与确认组合仍待验收；2026-10-10 修复计划参数规范化与控制台丢失端口范围，2026-10-11 新构建 23 项门禁通过（§15） |
| M4：动作清单治理 | R-012、R-013 | 实施中；anas host actions 已提供本机编译清单；服务端清单、定期复审与特权写动作评审仍待办 |
| M5：边界内同步动作 | R-014、R-015 | 实施中；2026-10-03 实现 `incus.traefik.sync` 与 `incus.ports.sync`：参数恒为 `{}`，需 `incus.configure` 的批准，hostd 自己读取 Docker 与冻结部署，原子替换并读回，经共享队列与审计，由 anasd 以系统身份触发；单元与拒绝调用方条目的测试通过，实机运行随 Incus M10a/M11c 的 e2e（[实施记录](../reviews/2026-10-03-incus-lease-network-implementation.md)），设计见[宿主通道架构](../../docs/architecture/host-action-channel.md) §15 |
| M6：身份链简化与调用记录 | R-016 | 实施中；2026-09-30 实现完成：对端只认 root/root，调用记录与 `host.invocation.status` 取代回连 broker 和 systemd 退出观察，见 §14；Linux 原生门禁通过，实机审批门禁在 Ubuntu 26.04 与 Debian 13 上各 23/23 通过；anasd 重启时的结清尚未实现 |

覆盖统计：18 项需求全部有且只有一个里程碑归属。

### 1.1 当前集成核对（2026-09-19）

已修复计划参数从 struct 转入共享 job map 后因 JSON 键排序而被拒绝的问题；保留未知字段、
重复键、大小写别名、嵌套参数和冻结版本校验。新增从成功 plan 的公开投影经确认签发、消费、
持久化、执行绑定与独立 Claim 的存储往返回归，不以这个夹具代替实际进程或包安装。

控制台不再要求粘贴 token/参数 JSON；展示服务端步骤后显式确认，过期重新计划并清除同意，
切换 workspace/选项或卸载组件会结束本地等待但不会自动取消服务端 job。CLI 凭据与确认请求
通过单一 stdin 信封传递，不通过进程参数传原始 token。入站发布与未验收的环境能力继续拒绝。

本轮实际门禁和保留缺口统一见[中断恢复与集成回归](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-incus-integration-recovery.md)。

## 2. 顺序理由

M0 先于其余，因为「只接受动作 id 与类型化参数」这条不变量一旦在实现里被破坏，后面每个里程碑都
建立在一个可以被塞进脚本的通道上。M3 依赖 ABI 的 job 记录（`plan` 与 `apply` 是两个互相引用的
job），因此排在 M2 之后。

## 3. CI 门禁

2026-09-19 新增 `internal/hostaction` 和 `internal/incushost`，本机单元及 race 通过；
Linux 专属 peer/文件系统测试须分开记录，交叉编译不等于原生运行。完整验证与外部包来源见
[本轮核对记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-incus-host-preflight-implementation.md)。没有 root 服务安装或通道 E2E 通过记录。

以下为先前历史基线：前置[统一动作 ABI](action-abi.md)
的内部实现已保存为 `3abe464` 并参与本次合并，但未接入 root 通道，也没有通过记录。
下述为 2026-09-13 保存的历史 CI/本地文档基线，不代表当前合并版本：

文档门禁也**没有 CI 记录**：本计划与要求文档随 `40a8b2e`（2026-09-09）加入，而 GitHub CI 在 master
上最近一次运行是 `5306b63`（2026-09-05），其后的提交均未推送。只有本地记录：`6823232` 的提交说明记载
`docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status` 与
`docs:check-status` 通过，2026-09-13 在该提交上复核一致。

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | `test-env/scripts/server-incus-host-action-e2e.py` | 独立 Ubuntu 26.04 amd64 / 已安装 root systemd 服务 | 2026-09-23 | 第十一轮 17 项通过，真实确认安装/配置/登记/卸载与退出；新扩展门禁另记 |
| R-004 | `test-env/scripts/server-incus-host-action-e2e.py` | CLI stdin 会话及真实 HTTPS API，共享 job/确认/执行 | 2026-09-23 | 实际链路通过；不声称已操作浏览器 UI 或已验全部动作 |
| R-011 | `test-env/scripts/server-incus-host-action-e2e.py` | 真实五分钟到期，不改时钟/ledger，旧计划不续签，新计划可执行 | 2026-09-23 | API/CLI 原生通过；浏览器重新展示和勾选的视觉交互仍待验收 |
| R-016 | `test-env/scripts/server-incus-host-action-e2e.py`（`shared_jobs_and_invocation_records`）与 `test-env/scripts/test-host-action-native.sh` | 全新 Ubuntu 26.04 与 Debian 13 amd64 VM，已安装 root systemd 服务，`0.0.0-native.31`；一次性 Debian 13 VM 里的非 root Linux 原生用例 | 2026-09-30 | 审批门禁 23/23 通过：14 个共享 job 各有一份带终态的 hostd 调用记录且结果一致，另有 14 次激活；控制桥四项直接连 Incus 网关；Debian 13 重试同样 23/23，见 [本轮记录](../reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md) §5 |
| R-018 | 手工核验（安装后的 anasd/hostd 与 owner 会话驱动脚本，未入库） | finance.hlong.wang（Ubuntu 26.04，已安装 root systemd 服务），`0.0.0-native.20261011.2`（master `6dd38a34`） | 2026-10-11 | 通过：换装后第一轮调度清除工作区 `a`、`b` 上两个失败作业的补偿检查，之后在 `a` 上完成卸载与重装；见 §15 |
| R-003、R-004、R-010、R-011、R-014、R-016 | `test-env/scripts/server-incus-host-action-e2e.py` | ln 一次性 QEMU VM，Ubuntu 26.04 cloud image（`4908fb59…`），VM 内 `docker.io` 29.1.3，已安装 root systemd 服务，`0.0.0-native.20261011.1`（master `d0cb28c3`） | 2026-10-11 | **23/23 通过**（§15）：全部计划请求都不带端口范围，`incus.configure` 确认的是默认范围；14 个共享 job 成功且各有一份调用记录；卸载删除 4 个受管包；证据归档 `8f182de4…6229`；正常关机，宿主 10 项基线一致。实验根 `/data/anas-incus-hostaction-20261011.v3BUw2`（不入 Git） |

## 5. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Incus compute Provider Module 集成要求](../requirements/incus-module.md) | 迁出的 `INCUS-R-058` 等标为由 `HOSTACT-R-*` 取代 | 已完成 |
| [特权操作与 helper（草案）](../../docs/architecture/privilege-helper-draft.md) | §3 按本通道重新划分：btrfs 与备份的特权操作改归 `anas-hostd` | 已完成 |
| [宿主特权动作通道设计](../../docs/architecture/host-action-channel.md)与[架构索引](../../docs/architecture/index.md) | 激活校验、内部 job 绑定与生产未接入状态 | 已同步；后续随实现继续维护 |
| [英文架构索引](../../docs/en/architecture/index.md) | 只读、激活/job 边界与 CLI 本机清单摘要，链接中文源 | 已同步 |
| [日志与可观测性](../../docs/architecture/observability-and-logs.md) | 内部执行/拒绝审计已编码，root 服务与 broker 未接入 | 已同步 |
| [安装](../../docs/getting-started/installation.md)与英文镜像 | M0/M1：安装期那一次授权，以及 `anas-hostd` 与 socket 的安装、卸载（R-003） | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | host actions 的本机清单、未核验安装标记与不支持写动作；二段确认尚未交付 | 部分同步；清单已同步，执行与确认待办 |

## 6. 待决

- 非 systemd 发行版的 accept 启动器形态（OpenRC 服务脚本细节）。

## 7. 2026-09-19：只读供给预检与通道边界

`hostaction.Catalog` 只列编译进代码的 `incus.status`，范围为 `installation-preflight`，
不是完整 daemon 状态。它不需要 root，所以没有为此安装 root 服务或增加 helper。
安装、配置、登记、卸载和 prune 尚无处理器，请求一律拒绝；不打印为已支持动作。

`Receive` 只处理受信启动器交来的已接受 Unix stream 连接，读取内核 `SO_PEERCRED`，
以安装方固定 UID/GID 策略校验后读取一条有界 ABI 请求。只接受空对象参数；拒绝客户端声明
uid/gid、命令、argv、路径、URL、认证或隔离档覆盖。读侧最多三秒，要求 LF 与 EOF，取消可打断读取。
该代码没有创建监听 socket、安装策略文件或更改权限。

最初的 `Execute`（后于 §8 收为私有 `executePreflight`）对已验证调用复用既有 `audit.Writer` 的 `AppendContext`：先写含规范化参数、内核
peer 与 job/invocation ID 的开始记录，再探测，最后记录结果。前置审计失败不执行；尾部审计失败
只返回 unknown，不泄漏原始错误或给出成功。返回的 ABI 终态仍须由共享 `ActionRecorder` 核验
实际 EOF/退出码后入 job store，没有新增 job 日志或序号分配器。

五问：该预检无需 root；只读固定系统文件且不连接可能 socket 激活 daemon 的接口；无特权产物；
没有需要撤销的变更；失败保留 compute 关闭、可以重新探测。未来增加行政 socket 查询或修改动作，
必须重新评审权限和撤销方案，不继承本段的只读判断。

仍缺：安装身份配置与 socket 权限校验、被拒连接审计、job 身份与执行租约交接、与调用方连接
解耦的实际执行所有者、可信发布/安装/升级、CLI/HTTP 接线，以及破坏性动作的二段确认。
当前 peer 策略只认实际 primary GID；仅通过 supplementary group 获得 socket 文件访问权的用户
尚无准入路径，不能通过读取客户端传入的组列表或 NSS 名称来绕过。M0/M4 不据此验收完成。

## 8. 2026-09-19：激活校验、job 绑定与本机清单

新增固定版本安装配置、受信路径 pin、已接受 Unix fd 验证、拒绝审计及 `Activation.Serve`。
新增 `consolejobs.RetainActionExecution` 与 job 所有者侧 `HostJobBinding`，只绑定已运行的
只读预检 job；不另建 store，不允许请求自报 job id 即执行，也不从 socket EOF 编造成功退出。
CLI 新增 `anas host actions`，明确只是当前客户端编译清单、没有核验安装。

现有 `anasd.service` 为 root/root，与非 root peer 政策不兼容；没有为此改宽政策或更改用户
服务账号。跨进程 broker、实际退出/回收监督、单一存储的执行者装配及迁移须先交付，才能安装
root 二进制和 socket。写动作、二段确认、包安装/卸载、真实 Incus/KVM 仍未完成。

本次验证及固定上游依据登记到
[激活与 job 绑定核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-host-action-activation-job-binding.md)。
M0/M1/M4 保持实施中，不能以本机测试或本机清单替代服务端能力和 Linux 原生验收。

本次全仓 Go 测试/vet、hostaction/jobexecutor/consolejobs 的 race、Linux 双架构源码及测试
交叉编译、共享构建静态检查、文档/索引门禁和站点构建通过。末尾资源关闭错误传播已另补回归并
重跑相关包；未运行真实 systemd/root 进程、安装/卸载或 Incus/KVM E2E。

## 9. 2026-09-19：跨进程绑定与执行租约保留

实现 `Activation.ServeBrokered`、`BrokerSession` 及 `HostJobBinding.ServeBroker`，复用 §8
的同一 Store/lease/actor 检查，不以 root 读取用户可写 job 目录，不创建第二套日志或注册表。
固定私有 endpoint、两端实际身份、逐会话有界握手及授权后的二次权限检查已编码。
握手成功不释放执行租约；关闭需要连接关联的进程退出证据，不能由 EOF 或自报 PID 代替。
具体机制归[宿主通道架构](../../docs/architecture/host-action-channel.md) §9。

新增协议/错误路径测试和 Linux 真实子进程 fixture；原生入口
`bash test-env/scripts/test-host-job-broker-native.sh` 检查关键用例确实执行，不把 skip 计为通过。
验证结果与未运行部分见[跨进程核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-host-job-broker-implementation.md)。
仍缺私有 listener、非 root 所有者服务装配/迁移、root 可执行程序及发布安装、实际退出码与后代
清理证据、公共执行入口和二段确认；不得将本轮握手当作生产通道或 M0 验收完成。

## 10. 2026-09-19：私有 listener 与有界任务分派

接续 §9 补 `OpenJobBrokerListener` 和 `HostJobBroker`，只在已安装的非 root 私有目录创建
固定 socket。未知、旧有和不安全路径不接管；独占目录锁、身份检查和关闭清理独立于任务租约。
监督者先注册共享 Store 中已运行的只读任务，连接按 job/invocation 查找；32 绑定/8 连接上限，
无自动注册、驱逐或重建。退休要求实际终态及远端清理，授权后失败停止准入并保留执行所有权。

增加 Store 集成生命周期测试与 Linux listener 回归，并扩展原生脚本、接入 Go CI。最新机制见
[架构 §10](../../docs/architecture/host-action-channel.md)；实际门禁和限制见
[本轮核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-host-job-broker-listener.md)。上述 listener 缺口已编码，
但非 root 服务迁移/装配、正式 root 程序与发布安装、真实退出状态及生产 recorder、公共执行入口、
二段确认和 Incus 安装/卸载仍未交付。M0 保持实施中，不以新增测试或工作流接线计作生产验收。

## 11. 2026-09-19：独立退出观察、终态接线与 root 程序

新增 `systemd_exit*`：生产 broker 在授权前固定 PID 1 的 D-Bus 身份、单元与 invocation，
保留单元引用，结束时核对独立退出码和无残留进程。新增 `ExecutePreflight`，从已运行 job 注册、
固定 socket 请求、真实 EOF、broker 结果到共享 recorder 终态与退休连成一条内部执行路径。
未知退出或清理失败沿用 Store 的持久 containment barrier，不创建另一份 job 日志。

`cmd/anas-hostd` 只提供 `--serve`、`--actions`、`--version`，服务模式固定 root 审计目录并复用
原激活/broker 校验；失败帧必须对应非零退出码。候选 socket/service 单元及二进制加入同版本
发布打包，未接入 `install.sh`，未改变现有 `anasd` 身份或任意系统文件。

新增依赖 `github.com/godbus/dbus/v5 v5.2.2` 用于真实 D-Bus 客户端；不自行实现协议、不启动
shell 或外部 systemctl。相关代码测试、发行包核对与真实未执行范围见
[本轮记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-host-action-exit-completion.md)。Linux/systemd 实机、非 root
执行者服务迁移、公共请求入队、二段确认及安装/配置/对称卸载仍待交付；M0/M1 未标完成。

## 12. 2026-09-19：恢复中断、共享队列与可选 HTTP 入口

连接恢复后读回确认，上轮仅 `host_action_service.go` 与 `job_owner.go` 写入成功，后续 HTTP/
配置补丁未落盘。本轮在原工作树补齐 `HostActionService`、只读 POST、默认关闭的 `host_actions`、
同一 anasd 的启动/停机装配、排队取消及关闭该能力后的启动恢复审计。没有另外启动近 root 后端，
也没有给 CLI 直连 root socket 或按请求传入代码的能力。

纯逻辑、真实 Store 队列、HTTP 权限/CSRF/投影与 daemon 生命周期测试覆盖这条代码接线；
停机同时收到 runtime 结束和取消时误报失败、队列扫描后被取消导致 owner 停止两个竞态已修复。
HTTP 只允许 full/TLS/owner 的空参数预检，具体契约见服务配置参考与 OpenAPI；CLI invoke 和页面
按钮仍未提供。实际执行、验证与限制见[本轮核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-19-host-action-queue-http.md)。

非 root Linux 配置读取新增 root-owned 0640/指定 primary group 的独立路径，原 root 0600 政策
不变。**这不解决 TLS 私钥读取**：现有证书加载器仍要求 root 所有且私钥无组/其他读权限。
TLS 安全供给/热更新、原 Store/workspace 属主与 Docker 权限，以及安装器和升级/卸载必须成套
迁移。仅改 User/Group 或设置开关不能作为生产启用步骤，不放宽 TLS、不改默认 unit、不批量 chown。
M0/M1 和 Incus M10 保持实施中；单元通过不登记 Linux/systemd/Incus/KVM 验收完成。

## 13. 2026-09-29：端口表同步动作（需求级评审）

对应 `HOSTACT-R-012`、`R-014`、`R-015` 与 `INCUS-R-152`、`R-158`、`R-160`、`R-161`；设计见
[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §5.1.8。操作者在 `incus.configure` 的二段确认里
批准可用端口范围，并安装固定的 Docker 式规则链；之后每次 apply 由 hostd 按冻结部署同步端口表，范围内不再逐次确认。

| 问题 | 本动作的约束 |
| --- | --- |
| 能否不用 root | 不能。写 nftables、安装 systemd 套接字单元都需要 root；`anas-helper` 只有 `CAP_NET_ADMIN`、不留产物，而端口表与端口占位是留存的特权产物 |
| 是否最小权限 | 只改 ANAS 自有 nft 表里的端口表，以及 ANAS 命名的端口占位单元；条目只有协议、宿主端口、槽位地址与 guest 端口，不接受命令、规则文本或路径；超出批准范围的条目拒绝 |
| 留下什么特权产物 | 固定规则链（随 `incus.configure` 安装）、端口表条目、每个绑定一个端口占位单元，以及宿主状态里的同步回执 |
| 如何撤销 | 部署不再声明的绑定在下一次同步时移除；`incus.uninstall` 删除规则链与全部占位单元 |
| 半途失败与重跑 | 端口表以单个 nft 事务替换，失败时保持上一次生效的状态；占位单元先建后删，重跑按冻结部署收敛；开机时先恢复占位、后恢复转发，被其他进程占住的端口不生效并记录运行问题 |

### 13.1 2026-09-30：Traefik 地址清单同步

对应 `INCUS-R-118`、`R-119`。操作者在 `incus.configure` 的二段确认里批准一次；之后 apply 启动 Traefik 后、Traefik
容器重启后与 anasd 启动时，由 anasd 触发同步。放弃由 anasd 调 Provider 更新的理由见 Incus 需求 §7sexies 第 4 条。

| 问题 | 本动作的约束 |
| --- | --- |
| 能否不用 root | 不能。经本机 Incus 套接字写 address set 需要 root 或 `incus-admin`；改用 Incus 管理证书，又回到 anasd 在无人操作时使用管理凭据 |
| 是否最小权限 | 只替换 ANAS 命名的那一份 address set；请求不带任何地址，hostd 自己从 Docker 读取 ANAS 的 Traefik 容器当前的地址 |
| 留下什么特权产物 | 一份全局 address set，以及宿主状态里的同步回执 |
| 如何撤销 | `incus.uninstall` 删除这份 address set；Traefik 不存在时同步为空清单 |
| 半途失败与重跑 | 以单次 Incus API 替换写入，失败时保持上一次的内容；重跑按 Docker 当前状态收敛 |

## 14. 2026-09-30：身份链简化（M6）

依据[设计简化评审](../reviews/2026-09-28-incus-design-simplification-review.md)第 4 项与 `HOSTACT-R-016`。设计见
[宿主通道架构](../../docs/architecture/host-action-channel.md) §14；删除清单与验证见
[本轮记录](../reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md)。

- [x] 安装策略 v3 只绑定 release；对端只认 root/root；删除 `service_mode`、`service_unit`、`socket_gid`
- [x] 删除回连 broker（`/run/anas-job-broker`、握手协议、pidfd 夹持）、PID 1 私有总线核验、`Unit.Ref` 与 systemd 退出观察，
      以及 `github.com/godbus/dbus/v5`
- [x] hostd 调用记录：`/var/lib/anas-hostd/invocations` 下的 `.lock` 与 `.json`，重放拒绝，终态先落盘后发帧，保留 30 天
- [x] 只读动作 `host.invocation.status`；anasd 的执行器在事件流中断时按记录结束任务
- [x] 安装器、`anasd.service`、发行包与安装测试同步；原生门禁改为 `test-env/scripts/test-host-action-native.sh`
- [x] Linux 原生门禁实际运行：2026-09-30 在一次性 Debian 13 VM 里以非 root 用户通过 11 个必需用例；首次运行暴露并修复了一个未随简化更新的测试（[本轮记录](../reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md) §5）
- [x] 真实 Linux/systemd 审批链路（`server-incus-host-action-e2e.py`）在新实现上重跑：Ubuntu 26.04 与 Debian 13 各 23/23 通过（2026-09-30，[本轮记录](../reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md) §5）
- [ ] anasd 重启时用调用记录结清运行中的宿主任务（现仍记 `daemon_restarted` 并阻断宿主队列）

同日删除转发许可与观察配置/HTTP 观察三组宿主动作（`incus.forwarding.*`、`incus.ingress.observer*`、
`incus.ingress.observe_http`）；上文 §1 与 2026-09-21 各节里关于它们的五问评审只作历史记录。

## 15. 2026-10-10：计划参数规范化与端口范围确认

finance.hlong.wang（Ubuntu 26.04，master `c397e92e` 原生构建）上，不带端口范围的 `incus.install.plan` 在 hostd 成功，
anasd 却记为 `execution_containment_lost`，之后宿主队列被持久化拦截。根因是 Incus 计划把调用方原始请求放进待确认
参数，而 `5c75aa99` 起 hostd 的规范化请求会填入默认端口范围，公开投影要求二者相等；`efb3fa7c` 已改为确认规范化请求。
复核时另发现浏览器控制台重建 apply 参数会丢掉端口范围、OpenAPI 未写这两个字段，本次一并修复。经过与逐项测试见
[Incus 计划](incus-module.md) §14.3，设计见
[宿主通道架构](../../docs/architecture/host-action-channel.md) §13 与[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §5.1.8。

- **为什么 23 项门禁没有发现**：最近一次 `server-incus-host-action-e2e.py` 23/23 是 2026-09-30 的 `0.0.0-native.31`，
  早于 `5c75aa99`（2026-10-04）。该脚本的计划请求本来就不带端口范围，在 `5c75aa99` 之后、`efb3fa7c` 之前的构建上重跑
  会在第一个计划阶段失败；它之后没有再跑过。缺陷首次在 Incus 租约网络验收 r16（2026-10-08）中出现并修复。
- **结果（按需求 ID，本机单元，未在实机重跑）**：
  - `HOSTACT-R-010`：控制台与 CLI 都原样送回计划的 `parameters`；vitest 用例与 OpenAPI 字段契约测试通过，修复前失败；
  - `HOSTACT-R-014`：操作者确认的端口范围写在待确认参数里并进入参数摘要；四个 Incus 计划动作的投影、确认摘要与 hostd
    解码测试通过，去掉范围即摘要不符；
  - `HOSTACT-R-009`—`R-011` 的存储往返（`TestConfirmedIncusPlanSurvivesQueuePersistenceAndExecutorHandoff`）改用写明默认
    范围的规范化请求后通过。
- **实机结果（2026-10-11）**：
  - 构建：master `d0cb28c3`，`scripts/ci/build-anas-release.sh` amd64，`0.0.0-native.20261011.1`，归档 SHA-256
    `9f76547d…a34c`；`anas` `de51a3d5…`、`anasd` `d17f78c4…`、`anas-hostd` `4ff1f4c7…`；e2e 脚本 `d9f889c1…`。
  - 23 项审批门禁：ln 上的一次性 QEMU VM（Ubuntu 26.04，VM 内 `docker.io` 29.1.3）**23/23 通过**，第一轮即通过。
    `HOSTACT-R-003`、`R-004`、`R-010`、`R-011`、`R-016` 同 2026-09-30 的覆盖；`R-014`：脚本的 install/configure/enroll/
    uninstall 计划都不带端口范围，确认与执行均成功。14 个共享 job、14 份 hostd 调用记录；卸载删除 4 个受管包；证据
    归档 `8f182de4…6229`；正常关机，宿主 10 项基线一致，VM 磁盘已删。原始日志在 ln
    `/data/anas-incus-hostaction-20261011.v3BUw2/r1-n20261011-ubuntu2604/reports/`（不入 Git）。外层所有者脚本沿用
    2026-09-30 的 `hostaction-lab.py`，未改。
  - finance.hlong.wang：换装同一归档的 `anas`、`anasd`、`anas-hostd`，`/etc/anas/hostd.json` 改为新 release，单元文件
    不变（与归档一致），旧文件备份在 `/tmp/anas-deploy-20261011/prev/`。重启后 anasd 与 hostd socket 正常，c397e92e
    上每 30 秒一次的 `Traefik event stream stopped: exit status 64`（Docker 29 的 `docker events` 格式缺陷）不再出现。
    那台机器上的作业存储已在 2026-10-10 20:13 被另一会话换新，没有 `execution_containment_lost` 记录。之后按操作者要求
    把控制台存储与确认账本改名移走（`/var/lib/anas/console.reset-20261011T0552`、`/run/anas/confirmations.reset-20261011T0552`），
    用 `anas console token` 重新登记 owner（随机口令只存于 finance 的 `/root/anas-console-owner-password`，root 0600），
    在工作区 `a` 重试 `incus.configure`：不带端口范围的计划成功，确认参数写明 30000–32767（`HOSTACT-R-014` 在实机上
    通过）；但计划阻断为 `host_effect_recovery_required`、没有步骤，执行按设计失败。原因是宿主状态里 c397e92e 留下的
    `configure.traefik-address-set` intent 为 `failed`（2026-10-10 12:24 UTC），即 efb3fa7c 修复的 Incus 201 缺陷；
    设计不允许自动恢复或改写 failed intent，`incus.uninstall` 同样被阻断。
  - finance 重装（2026-10-11，操作者选择「拆除后重装」）：按 `incus.uninstall` 的步骤手工删除 address set、
    `core.https_address`、`incus.service` 的排序 drop-in、nft 表 `anas_incus_control`、两族 FORWARD 的
    `anas-lease-forward-*`、Docker 网络 `anas-incus-control`（无容器）与存储池 `anas-btrfs`（无卷），停止 Incus 单元，
    `dpkg --remove` 删除状态记录的 4 个受管包，把 `/var/lib/anas/incus-host`、`/var/lib/incus` 与 ANAS 私有 APT 目录
    改名为 `*.reset-20261011T2240`。之后在新构建上经计划、确认与执行完成 install → configure → enroll（工作区 `c`，
    请求都不带端口范围）：install `installed`；configure `configured`，9 个回执全部 ok，含 `configure.traefik-address-set`
    与批准默认范围的 `configure.port-binding-range`；enroll `connection_ready`。宿主状态没有未结的 intent，记录的批准
    范围为 30000–32767。`HOSTACT-R-010`、`R-014` 在 finance 实机通过。
  - 过程中发现的缺陷（第 1—3 项已于 2026-10-11 修复，见下方「缺陷修复」）：
    1. 失败的写宿主动作把作业标为需要补偿检查，而只有部署执行器会清除这个标记；该工作区此后所有写作业静默排队
       （finance 上工作区 `a`、`b` 先后被卡住，`a` 里还留着一个无法取消的排队 `incus.install`）；
    2. 取消非 `incus.status` 的排队宿主动作返回 500 `internal_error`：`publicJobStoreError` 没有映射 `ErrConflict`；
    3. `dpkg --remove` 留下 `incus-base` 的配置文件（`config-files`），install 据此判定存在外部 daemon 并拒绝
       （`external_daemon_service_not_modified`）；手工 `dpkg --purge` 这两个 ANAS 安装过的包后才能重装。产品的
       `incus.uninstall` 也只用 `--remove` 并清除包归属，推断卸载后在同一宿主重装同样会被拒绝，尚未用产品路径验证；
    4. 主机负载高时（finance 当时 load 17–22、内存换页）anasd 的 TLS 握手超时，CLI 调用失败，一次 `incus.enroll.plan`
       在 hostd 内失败，重试后通过。
- **缺陷修复（2026-10-11，本机单元，未上实机）**：
  1. `HostActionService` 每轮 `advance` 清除 hostd 明确报告失败（failed）的写宿主动作留下的补偿检查，并在 job 上
     记一条警告；结果未知（interrupted）的保留。宿主侧的恢复义务由 hostd 的 intent 承担。测试
     `TestFailedHostActionDoesNotBlockItsWorkspace`、`TestUnknownHostActionOutcomeKeepsCompensationCheck`，前者在去掉
     修复后失败（`HOSTACT-R-018`，2026-10-11 新增）；
  2. 任何尚未开始的宿主动作都可由 owner 取消（`CancelQueuedPreflight` 改为 `CancelQueued`，审计记实际动作名），
     取消冲突返回 409 `job_conflict`、非 owner 返回 403（`ACTABI-R-001`）。测试 `TestQueuedConfirmedHostActionCanBeCancelled`、
     `TestJobCancelReportsConflictAndDenial`，后者在去掉映射后失败；`cancelConsoleJob` 的路由改动只有一行条件，
     没有单独的 cmd 层测试；
  3. 见 [Incus 计划](incus-module.md) §14.3（`INCUS-R-048`）。
  `go vet ./...`、`GOOS=linux go vet` 与 incusprovision、jobexecutor、httpapi、consolejobs、hostaction、`cmd/anasd`、
  `cmd/anas-hostd` 的测试通过。finance 上已有的卡住状态需要换上新构建后由 `advance` 自动清除，尚未部署验证。
- **finance 实机验证（2026-10-11，master `6dd38a34`，`0.0.0-native.20261011.2`，归档 `cac81fcf…`）**：
  1. 换装后第一轮 `advance` 即清除工作区 `a`、`b` 上两个失败作业的补偿检查并记警告；`a` 里旧版本冻结的排队
     `incus.install` 因版本不符以 `action_not_started` 结束，未执行。之后在 `a` 上完成卸载与重装，证明工作区已解除阻断；
  2. 取消当前版本的已结束作业返回 409 `job_conflict`；卸载执行期间在工作区 `b` 排队的 `incus.install.plan` 由 queued
     取消为 canceled（202，`ACTABI-R-001`）。取消旧版本冻结的作业返回 403：`CancelQueued` 先核对冻结请求再看状态；
  3. 见 [Incus 计划](incus-module.md) §14.3：产品卸载后在同一宿主重装通过（`INCUS-R-048`）。
  结束时宿主状态无未结 intent，没有带补偿检查、排队或运行中的作业，`anasd`、`anas-hostd.socket`、`incus.service` 均在运行。
- **`incus.ports.sync` 在 finance 失败（已改，2026-10-11）**：anasd 启动触发的端口同步两次失败（失败作业的补偿检查同样被
  第 1 项清除）。逐步诊断（临时编译的 Linux 测试二进制，以 root 只运行同步的读取步骤）显示 Docker 未过滤的
  `/containers/json` 在该机耗时 17—40 秒（主机内存换页，load 17 左右），超过 hostd Docker 客户端 20 秒上限；带标签
  过滤的 Traefik 查询约 1.3 秒，所以 `incus.traefik.sync` 通过。诊断中有一次完整同步在上限内完成，在 hostd 之外
  执行了一次批准范围内的同步（清空四张空端口表、写入空的 `ports.json`），没有 hostd 审计记录。按操作者选择，
  端口同步改为只列出发布了端口的运行中容器（Docker `publish` 过滤取 `1-65535/tcp`、`1-65535/udp`；该过滤按容器
  自己的端口匹配，全范围即「有宿主绑定的容器」）。finance 上对照：过滤结果 10 个容器中的 5 个，发布端口集合与未过滤
  完全相同。测试 `TestDockerContainerReadsAreTheTwoListings` 断言过滤参数且未过滤的列表不可达，改回未过滤路径时失败。
  finance 换装验证（2026-10-11）：工作树快照提交 `01e9a454`（tree `c3cc6655`，父提交 `46669e59`，不在任何分支），
  `0.0.0-native.20261011.3`，归档 `50681b3e…`。anasd 启动触发的 `incus.ports.sync` 在 0.95 秒内成功，hostd 自己重写了
  `ports.json`（无绑定），`incus.traefik.sync` 同时成功（`INCUS-R-158`、`HOSTACT-R-014`）。允许清单已不放行未过滤的列表，
  所以成功即说明走的是过滤查询。换装时 finance 的 load 已降到 0.85，因此只证明了过滤查询在实机上正确可用，没有复现
  高负载下的超时，也就没有证明负载下的提速。
- **待办**：浏览器页面审批仍未实机验收（M3）；`execution_containment_lost` 与 failed host intent 的受限恢复动作都未实现；
  端口同步过滤查询在高负载下的效果未复现。「失败的宿主动作不得永久阻断工作区」已按操作者选择新增为 `HOSTACT-R-018`。

