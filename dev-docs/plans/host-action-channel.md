---
doc_type: plan
status: implementing
created: 2026-09-04
updated: 2026-09-19
---

# 宿主特权动作通道实施计划

验收依据是[宿主特权动作通道要求](../requirements/host-action-channel.md)；设计见
[同名架构文档](../../docs/architecture/host-action-channel.md)。

**当前状态：保留现有 root/root anasd，按固定 systemd 服务身份准入；共享队列、独立退出观察、Incus 计划/确认/执行、CLI/Web 和安装器已编码。真实 Linux/systemd/Incus 验收、非 systemd 支持及生产 ingress 仍未完成。** 下文按日期保留历史切片；其中“必须迁移非 root anasd”的前置判断已撤回，不再是当前实施方向。它依赖[统一动作 ABI](action-abi.md)——通道复用那套
线格式与 job 语义，不另起一套。截至 2026-09-18，ABI 前置已有共用 journal、执行 recorder、
非特权 Module 注册表/Linux 进程适配、ModuleActionWorker 及调用/查询/订阅服务的内部代码；
动作级幂等、终态后 1 小时保留和 coalesce/reject/queue 已接入同一 journal，仍无 root 注册表。
当时这些代码与回归源尚未编译/运行；之后已完成的本机回归与交叉编译分别记录于 §3、§8，
主 daemon 与旧 CLI/HTTP 执行入口仍未迁移。Module worker 不领取宿主
命名空间的动作，不能据此注册 Incus HTTP 宿主动作或宣称 root 通道完成。没有新增 root RPC、
脚本入口或 CAP_SYS_ADMIN helper。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：通道形态、授权与审计 | R-001—R-004 | 实施中；固定 root 服务身份、私有 broker、共享授权/审计/退出证据已接线，实机验收未完成 |
| M1：入口划分与升级绑定 | R-005、R-006 | 实施中；同版本打包、CLI/HTTP 与安装器已接线，升级拒绝覆盖活动宿主动作；真实安装/升级/卸载未验收 |
| M2：长时动作与非 systemd 可移植性 | R-007、R-008 | 实施中；按编译动作预算超时并投影固定阶段事件，非 systemd 启动和实机长时动作待办 |
| M3：二段确认 | R-009—R-011 | 实施中；固定 ledger、五分钟 TTL、共享 Store、执行前 Claim 和页面重新展示已编码，真实 root 执行验收待办 |
| M4：动作清单治理 | R-012、R-013 | 实施中；anas host actions 已提供本机编译清单；服务端清单、定期复审与特权写动作评审仍待办 |

覆盖统计：13 项需求全部有且只有一个里程碑归属。

### 1.1 当前集成核对（2026-09-19）

已修复计划参数从 struct 转入共享 job map 后因 JSON 键排序而被拒绝的问题；保留未知字段、
重复键、大小写别名、嵌套参数和冻结版本校验。新增从成功 plan 的公开投影经确认签发、消费、
持久化、执行绑定与独立 Claim 的存储往返回归，不以这个夹具代替实际进程或包安装。

控制台不再要求粘贴 token/参数 JSON；展示服务端步骤后显式确认，过期重新计划并清除同意，
切换 workspace/选项或卸载组件会结束本地等待但不会自动取消服务端 job。CLI 凭据与确认请求
通过单一 stdin 信封传递，不通过进程参数传原始 token。入站发布与未验收的环境能力继续拒绝。

本轮实际门禁和保留缺口统一见[中断恢复与集成回归](../reviews/2026-09-19-incus-integration-recovery.md)。

## 2. 顺序理由

M0 先于其余，因为「只接受动作 id 与类型化参数」这条不变量一旦在实现里被破坏，后面每个里程碑都
建立在一个可以被塞进脚本的通道上。M3 依赖 ABI 的 job 记录（`plan` 与 `apply` 是两个互相引用的
job），因此排在 M2 之后。

## 3. CI 门禁

2026-09-19 新增 `internal/hostaction` 和 `internal/incushost`，本机单元及 race 通过；
Linux 专属 peer/文件系统测试须分开记录，交叉编译不等于原生运行。完整验证与外部包来源见
[本轮核对记录](../reviews/2026-09-19-incus-host-preflight-implementation.md)。没有 root 服务安装或通道 E2E 通过记录。

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
| R-003 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | 安装与卸载对称性 | — | 待执行 |
| R-004 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | CLI 与 Web 同一通道同一审计 | — | 待执行 |
| R-011 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | token 过期后重新展示而非沿用旧摘要 | — | 待执行 |

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
[激活与 job 绑定核对](../reviews/2026-09-19-host-action-activation-job-binding.md)。
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
验证结果与未运行部分见[跨进程核对](../reviews/2026-09-19-host-job-broker-implementation.md)。
仍缺私有 listener、非 root 所有者服务装配/迁移、root 可执行程序及发布安装、实际退出码与后代
清理证据、公共执行入口和二段确认；不得将本轮握手当作生产通道或 M0 验收完成。

## 10. 2026-09-19：私有 listener 与有界任务分派

接续 §9 补 `OpenJobBrokerListener` 和 `HostJobBroker`，只在已安装的非 root 私有目录创建
固定 socket。未知、旧有和不安全路径不接管；独占目录锁、身份检查和关闭清理独立于任务租约。
监督者先注册共享 Store 中已运行的只读任务，连接按 job/invocation 查找；32 绑定/8 连接上限，
无自动注册、驱逐或重建。退休要求实际终态及远端清理，授权后失败停止准入并保留执行所有权。

增加 Store 集成生命周期测试与 Linux listener 回归，并扩展原生脚本、接入 Go CI。最新机制见
[架构 §10](../../docs/architecture/host-action-channel.md)；实际门禁和限制见
[本轮核对](../reviews/2026-09-19-host-job-broker-listener.md)。上述 listener 缺口已编码，
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
[本轮记录](../reviews/2026-09-19-host-action-exit-completion.md)。Linux/systemd 实机、非 root
执行者服务迁移、公共请求入队、二段确认及安装/配置/对称卸载仍待交付；M0/M1 未标完成。

## 12. 2026-09-19：恢复中断、共享队列与可选 HTTP 入口

连接恢复后读回确认，上轮仅 `host_action_service.go` 与 `job_owner.go` 写入成功，后续 HTTP/
配置补丁未落盘。本轮在原工作树补齐 `HostActionService`、只读 POST、默认关闭的 `host_actions`、
同一 anasd 的启动/停机装配、排队取消及关闭该能力后的启动恢复审计。没有另外启动近 root 后端，
也没有给 CLI 直连 root socket 或按请求传入代码的能力。

纯逻辑、真实 Store 队列、HTTP 权限/CSRF/投影与 daemon 生命周期测试覆盖这条代码接线；
停机同时收到 runtime 结束和取消时误报失败、队列扫描后被取消导致 owner 停止两个竞态已修复。
HTTP 只允许 full/TLS/owner 的空参数预检，具体契约见服务配置参考与 OpenAPI；CLI invoke 和页面
按钮仍未提供。实际执行、验证与限制见[本轮核对](../reviews/2026-09-19-host-action-queue-http.md)。

非 root Linux 配置读取新增 root-owned 0640/指定 primary group 的独立路径，原 root 0600 政策
不变。**这不解决 TLS 私钥读取**：现有证书加载器仍要求 root 所有且私钥无组/其他读权限。
TLS 安全供给/热更新、原 Store/workspace 属主与 Docker 权限，以及安装器和升级/卸载必须成套
迁移。仅改 User/Group 或设置开关不能作为生产启用步骤，不放宽 TLS、不改默认 unit、不批量 chown。
M0/M1 和 Incus M10 保持实施中；单元通过不登记 Linux/systemd/Incus/KVM 验收完成。
