---
doc_type: plan
status: implementing
created: 2026-09-04
updated: 2026-09-18
---

# 统一动作 ABI 实施计划

验收依据是[统一动作 ABI 要求](../requirements/action-abi.md)；设计见
[同名架构文档](../../docs/architecture/action-abi.md)。

**当前状态：协议、共用 journal 与 Module 执行原语正在实施，已补持久化失联阻断；未编译/测试，尚未接入生产执行入口。** 它是[宿主特权动作通道](host-action-channel.md)的前置——通道复用本 ABI
的线格式与 job 语义。同时它取代 [Module 专属命令能力](module-command-capability.md)已实现的
executor 协议与取消语义，因此落地时要一并处理那部分既有实现。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：job 模型、事件日志与重放 | R-001、R-002 | 实施中；协议、共用存储与 recorder/内部队列适配已编码，生产入口及验证未完成 |
| M1：取消语义与并发合流 | R-003、R-007、R-009 | 实施中；协作取消、持久意图、动作级幂等及 coalesce/reject/queue 已编码，产品入口迁移及验证未完成 |
| M2：大块数据边界 | R-004—R-006 | 未开始 |
| M3：job 可见性 | R-008 | 实施中；共用 dispatcher 与按当前权限读取/订阅的内部接口已编码，真实 CLI/HTTP 入口及 e2e 未完成 |

覆盖统计：9 项需求全部有且只有一个里程碑归属。

## 2. 与既有实现的关系

`anas.module-command/v1` 的 M1/M2 已实现（约 2100 行）。本 ABI 取代其 executor 协议与取消语义，
但**不取代**其 manifest 声明模型、descriptor 冻结、锁冲突表与发现路径。迁移前必须先在
[Module 专属命令能力要求](../requirements/module-command-capability.md)中标出哪几条被取代，
避免两份文档各自宣称有效。

### 2.1 2026-09-16：Incus 宿主盘点的协议前置

新增 `internal/actionabi`，只提供 `anas.action/v1` 请求/事件编解码、执行流与重放流校验，以及
结合实际进程退出证据的 outcome 判定。没有注册动作、启动进程、创建第二份 job store 或更换现有
Module Command/控制台协议；`anas-hostd` 仍不存在。实现细节见架构文档 §15。

- 请求和事件限 64 KiB（含 LF），参数/成功结果对象各 32 KiB、JSON 深度 16；拒绝重复/未知字段、
  大小写别名、显式 null 控制字段、尾随 JSON、缺 LF、空行、非法 UTF-8 和超限帧，不回显载荷。
- 绑定 job 与 invocation；执行器不提供 seq 或 truncated，序号与截断由受信日志层分配。
  重放从已消费序号继续，只有覆盖缺口的显式 truncated marker 可以跨序号。
- 单个终态、终态后数据和失败后继续输入均拒绝；强杀、输出损坏/不完整、未收割进程或终态与退出
  不一致归 unknown。cancelled 必须有显式取消请求、执行器确认和零退出码；没有杀进程即已取消的推断。
- 进度使用 uint64 精确计数，total 与 total_estimated 互斥，current 可超过估算值且不钳制。
  帧校验不是动作参数授权或脱敏器，公有投影必须先于持久化。

下一阶段复用 `internal/consolejobs` 与 `internal/jobexecutor` 的既有存储/执行租约，补单一 job
存储的 ABI 事件投影与序号分配，再迁移 Module Command executor 和 CLI/HTTP。现有 journal 版本、
截断/压缩、审计、权限与未知结果模型须一并核对，不能直接把新帧写进旧 store 或复制一套存储。
协作取消、声明式 coalesce/reject/queue、终态后 1 小时幂等 key 和两段确认 token 仍待实现。

本轮按用户要求只编码、格式化和文档/索引生成，不执行测试或门禁。待测项目先登记于
[Incus 网络原型清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md) 的同日 ABI 前置段；
不会因此把本计划任一需求或 Incus M6/M11 标记完成。

### 2.2 2026-09-17：共用 journal 与执行事件适配

在既有 `consolejobs.Store` 增加内部动作创建/启动/追加/终态/排队取消/重放边界，没有第二份存储。
`Job.Action` 保存不可变 ABI/name/invocation 和 job 内 LastSeq/outcome，`Event.ID` 仍为全局 console
游标。新 `action_event` 单行同时提交 job、事件以及必要的截断；不再先持久化成功帧再单独写终态。

- 动作只接受受信 dispatcher 创建；启动与运行中写入须持有现有 execution lease，生命周期提交
  须提供审计 observer。普通 worker 跳过动作 job，普通 append/transition 不能绕过专用边界。
- 容量或追加时发现过期前缀，会原子写入累计 `truncated(1..旧 LastSeq)`、下一事件和新 job 状态。
  默认 1024 条，每帧 64 KiB；容量至少 2，marker 也占容量。终态之后保留有界尾部，不追加 marker，
  不走旧事件的定时静默裁剪；整个终态 job 的最终清理策略仍待完成。
- `ReplayAction` 按独立 seq 分页，返回真实持久化 marker；从 marker 前一位继续的在线订阅者也可
  消费 marker，不会误判缺口。保留全局游标、snapshot digest 和既有压缩，并核对动作尾部与终态。
- 重启恢复在同一 execution lease 下为 running 动作追加 `unknown` 终态，失败或未知的 mutating
  动作保留 compensation 门禁。旧状态 `canceled/interrupted` 分别投影为动作 `cancelled/unknown`。
- `jobexecutor.ActionRecorder` 必须注入动作公有投影；先验证、投影、脱敏再落盘。成功/失败/取消
  帧暂存在内存，读到实际 EOF、校验退出证据后才原子提交。坏输出、投影/持久化失败、强杀或
  未确认退出统一 unknown，不保存原始错误文本；终态之后的字节不能被忽略。

以上是**可供后续 dispatcher 接线的内部实现**，没有注册动作、执行进程、修改 CLI/HTTP DTO 或切换
Module Command。共用 journal schema 仍为 v1，新增 typed 字段/record kind；旧读者会拒绝含新动作的
日志，不能把它当作可直接降级的格式。既有非动作路径保持原协议。action/key 幂等与 1 小时过期、
coalesce/reject/queue、协作取消通知/宽限期、两段确认及权限映射仍待实现；当前动作创建暂复用
旧 store 的幂等身份与保留期，不能对外宣称已满足 ACTABI-R-007/R-009。

本轮未运行新代码、测试、门禁或服务器操作；仅格式化和文档生成。追加待测场景见 Incus 实验清单
同日段，9 项需求均未改变验收状态。下一阶段接入受信动作注册表和受监督进程适配器，再推进现有
Module Command/CLI/HTTP 迁移与宿主通道。

### 2026-09-18：失联执行的持久化启动阻断

`consolejobs` 在共用 `canStart` 增加 `actionExecutionBarrier`。动作的 unknown 回执若包含
`execution_containment_lost` 或 `daemon_restarted`，同一存储的全部新执行均被阻止，不仅是写操作
或原 workspace；普通 `Start`、worker 的 `ClaimNextObserved` 和 `StartActionObserved` 复用这一
检查。错误只暴露稳定错误及排序后的 job ID，不带执行器原始输出。

阻断由现有 journal 回执重建，不新增可与回执失步的旁路状态。普通 compensation acknowledgement
只处理业务补偿，不证明残留进程/writer 已结束，所以不能解除本阻断。读取、重放和排队取消仍允许。
新测试源覆盖普通/动作入口、只读和跨 workspace、journal 压缩与重新打开及补偿确认之后的行为；
本轮没有执行这些测试，也没有因此完成任一 ACTABI 需求。

与进程监督的接线边界：写入上述终态之前必须先停止 recorder，不能同一个仍占有执行租约的 writer
并发提交另一个终态。writer 尚未停止时，执行所有者必须停止接纳并保留租约；重启恢复落下的 unknown
继续形成持久阻断。独立残留进程/writer 检验及受限解锁动作仍待设计/实施，没有 reset、删状态或仅
凭“已人工补偿”继续执行的分支。注册表的内存 poison 不能替代此存储级恢复边界。

### 2026-09-18：Module 队列消费、显式取消与回归源

`jobexecutor.ModuleActionWorker` 已接到既有 `consolejobs` 与同一 execution lease。它只消费显式
登记 workspace 的动作队列，不创建第二份存储、不随 invoke/attach/SSE 启动。每次执行重新核对
当前权限、冻结部署/descriptor 与标准化参数，并在持有声明锁后复核。未知动作、权限漂移、存储
失败不自动重试；容量/工作区阻断保留排队，同一工作区不因跳过阻断项而重排。

运行中取消经 `RequestActionCancelObserved` 将取消者/时间持久化到 `Action.Cancellation`，不依赖
有界事件尾部。pre-commit observer 再次校验取消者权限并记录审计，成功后才关闭私有通知 channel。
重复取消保留原始取消证据且不重复通知；排队取消
与启动在同一 jobs.lock 下竞争。通知不是 cancelled，终态仍由实际 EOF、进程退出和执行器确认决定。
`deploymentaudit` 对动作 job 记录具体 action name，而不是只记通用 `action.invoke`。

Linux 适配执行冻结摘要匹配的 sealed ELF；固定环境、独立取消 fd、受监督进程组与有界输出。
失去清理确认时保留应用锁和 execution lease，停止 registry/worker 接纳；不能把重建 worker 当作
恢复。它面向受信、非特权 Module executor，不是恶意代码 sandbox，也不是 `anas-hostd` 注册表。
原有 Module Command/CLI/HTTP 及主 daemon 尚未迁移或默认实例化此 worker。

新增 `module_action_queue_test.go` 覆盖取消审计失败、持久化先于通知、重复取消、权限撤销、
启动前再授权、未对账 running job、EOF/终态/强杀与公有投影失败；Linux 进程身份解析另有测试源。
测试均未执行，不以新增测试文件代表通过。实际 Linux 进程/内核竞态、跨入口可见性、宿主通道及
Incus 完整 E2E 仍待验收。统一幂等/合流实现状态以本计划相应记录为准，不在本节重复宣称完成。

### 2026-09-18：未启动调用的原子拒绝终态

新增 `consolejobs.RejectQueuedActionObserved`，将预检失败的 queued job 与唯一
`action_not_started` 错误事件一次提交。错误消息固定，不保存权限检查、描述符或可执行文件路径的
原始错误。写入仍须审计，invocation 绑定和 queued/start 竞争由同一 jobs.lock 决定；已启动的 job
不能走此路径，不能借它掩盖执行不确定性。

`actionJobAfterEvent` 与恢复校验区分两类失败：从未启动的拒绝没有 StartedAt，不要求业务补偿；
已经运行的失败仍保留原补偿规则，执行器不得自报保留的预检错误码。压缩快照重建终态时也按
StartedAt 选择 queued/running 前态，不把无执行证据的 job 还原为已运行。注册表 Run 的预检分支
已调用该边界；Start 提交之后不再允许拒绝，存储提交不确定或清理失联仍走阻断而不是自动重跑。

新增 `internal/consolejobs/action_preflight_recovery_test.go`，覆盖压缩/重开及重放、审计拒绝、
invocation 不符、启动竞争、执行器伪造预检终态，以及拒绝后不阻断独立任务。这些仅为测试源，
尚未编译或执行；没有改变任何需求/里程碑的验收状态，也未打开生产入口。

### 2.4 2026-09-18：参数规范化与协议边界回归源

修正 `normalizeModuleAction` 的摘要输入：回调返回的参数对象通过 `UseNumber` 解码并重新
编码，统一嵌套对象的键顺序与空白，保留大整数精度；规范化后再次执行帧与对象大小检查。
这防止等价参数仅因回调序列化顺序变化而出现幂等摘要冲突，不把不同数值或新默认值当成同一请求。
此修正不等于 action/key 的一小时保留或并发合流已经接入。

新增以下测试源，均未运行：

- `internal/actionabi/protocol_boundary_test.go`：严格字段、重复/转义字段、超限/深度/UTF-8、
  执行器不能自报 seq/truncated、精确计数、真实 EOF/退出证据、尾随数据、截断重放与输出脱敏；
- `internal/jobexecutor/action_recorder_boundary_test.go`：成功终态延迟提交、公有投影、不可改写
  调用身份/计数、投影或落盘失败归 unknown、拒绝重复终态；
- `internal/jobexecutor/module_action_parameters_test.go`：嵌套键顺序、uint64 精度、回调内存隔离、
  转义后大小边界、排队期间禁止新增默认值或改绑 deployment。

本轮仅静态核对和格式整理，不提供测试通过记录，不改变需求完成数或生产 ingress 开关。

### 2026-09-18：取消管道与 Linux 原生回归夹具

新增 `actionabi.ReadModuleCancellation`，只解释 Module 专用继承管道的 EOF，不把请求输入结束、
订阅断连、字节或 I/O 故障解释为取消成功。执行器仍负责安全点清理与 cancelled 终态；接口与
职责边界已同步架构原文及英文摘要。

补充 `protocol_regression_test.go`、`module_cancellation_test.go`、
`action_recorder_regression_test.go` 和 `module_action_native_regression_linux_test.go`。
原生夹具复用测试二进制作为冻结 ELF，覆盖目录/环境绑定、成功与失败、确认/忽略取消、成功后
挂起、缺终态、尾随坏数据、stderr 超限及 proc 名称解析。全部为测试源码，没有编译或执行。
Linux 夹具要求支持 pidfd 的非 root、无 capabilities 环境；skip 不作为通过证据，不替代 Incus
网络、宿主通道或 CLI/HTTP 验收。具体断言和后续执行入口见 Incus E2E 清单同名小节。

### 2026-09-18：合并边界与补充回归源

`ActionState` 同时保留取消意图和并发/重试策略，`cloneActionState` 分别深复制取消记录与重试键切片，
避免读取者或审计回调通过指针别名改写已持久化状态。`job_updated` 只允许首次增加取消记录，
不允许删除、改写取消者/时间，或借该记录改写 outcome、策略及动作身份。

补充测试源：`internal/actionabi/protocol_boundary_regression_test.go`、
`internal/consolejobs/action_state_boundary_test.go`、
`internal/jobexecutor/module_action_proc_boundary_linux_test.go`。覆盖严格帧/真实 EOF、取消和退出证据、
uint64 边界、截断重放、取消状态深复制，以及 procfs 视图和含括号/换行的进程名称解析。
这些源文件尚未执行；格式化不是类型检查、测试通过或 Linux 进程验收。

### 2026-09-18：取消意图与终态分离

`RequestActionCancelObserved` 将首次取消操作者与时间写入 `ActionState.Cancellation`，先审计和
持久化，再由当前 worker 通知执行器。重复请求保留首次记录，不增加 revision/事件序号；普通
执行器 warning 不能生成这份控制证据。取消记录不占执行器事件序号，不因日志尾部截断而丢失。
`validActionJobUpdate` 只允许添加一次记录，禁止改写/删除操作者与时间或顺带修改其他 job 字段。

运行中 job 要成为 cancelled，既需要已持久化的取消请求，也仍需 recorder 核验实际 EOF、退出码和
执行器确认；请求成功不保证最终一定 cancelled。排队取消无需执行器确认，预检失败与执行未知仍
使用各自终态。补充 `internal/consolejobs/action_cancellation_journal_test.go`，覆盖审计拒绝、
调用身份、重复请求、observer/返回值防别名修改、截断/压缩/重开及无请求时拒绝 cancelled。
上述测试源尚未运行；客户端身份适配、宿主通道与真实进程/服务器验收仍不据此标为完成。

### 2026-09-18：共用调用与订阅服务（未运行）

新增 `jobexecutor.ModuleActionDispatcher`，向后续 CLI/HTTP 适配器提供统一的 Invoke、Get、List、
Attach、Cancel 与 daemon Run。它复用 `ModuleActionWorker`，不另设消费循环、进程映射、取消
语义、租约或 journal；当前主 daemon 与旧 Module Command 路由仍未迁移。

Invoke 只入队，两种入口保留调用方可选 key，不额外生成随机重试键；动作级幂等与合流由下节所述
共用 store 处理。创建、加入和启动提交前重新授权，worker 的附加
CheckQueued 在准备可执行文件之前复核入口权限；拒绝复用既有原子预检终态，与排队取消竞争时
只接受已确认的终态。订阅逐页复核权限，保留真实 seq/truncated，断连或权限撤销不取消执行。
历史动作下线后，已有 job 仍按当前权限可读，不按原创建者或当前注册表是否含该动作隐藏。

Cancel 复用 worker 的持久意图、操作者审计和通知顺序；不保留第二条仅内存取消路径。
Module worker 仅领取 module 命名空间的排队动作，不能预检拒绝宿主动作；宿主通道仍需自己的
注册表和权限边界。新增 `module_action_dispatcher_test.go` 覆盖跨调用重试、历史任务视图、
订阅断连/权限撤销、截断终态、取消审计/重复通知、预检拒绝、未恢复 running 及宿主动作隔离。
这些都是未运行的回归源，不改变任何需求或里程碑的验收状态。

### 2026-09-18：动作级幂等与并发策略（源码接线，未验证）

`consolejobs.CreateActionWithPolicyObserved` 已接入 Module 注册表和共用 dispatcher。策略来自
受信注册表，未指定时为 `reject`；调用方不能用请求字段覆盖。模式和完整冻结参数均参与摘要，
排队期间模式改变会在启动前被拒绝，不把旧 job 静默迁为新策略。

- key 作用域为同一 store 的 `(action, key)`，不按 actor、HTTP 路径或 CLI 入口分区；workspace、
  mutating、规范化参数及部署/descriptor 包装属于请求，相同 key 改变这些字段时冲突。对象顺序
  规范化保留 `json.Number` 精度；通用脱敏若会改变执行参数，拒绝创建而非执行脱敏占位符。
- pending/running key 不超时；终态后满 1 小时可绑定新请求。每次绑定时间随 job 持久化，避免
  将新键绑定到较老的合流 job 后按创建时间选错归属，也避免时钟回拨复活旧归属。
- 相同请求的 `coalesce` 返回原 job，新 key 经审计后以单个 `action_key_bound` record 加入；
  `reject` 返回冲突 job，`queue` 创建独立 job，领取时阻止同参数任务越过较早的 pending/running
  任务，包含只读动作。有效同 key 重试先于并发策略处理。每 job 最多 64 个重试键，超额显式拒绝，
  不淘汰尚有效的旧键；无 key 合流不占这些槽位。
- 沿用同一 journal、锁、执行租约和每 job 一条旧创建回执；逻辑重试键不进入旧永久幂等身份。
  原子合流、深拷贝、压缩快照与恢复检查均包含策略、绑定时间和重叠归属验证。键的逻辑过期不是
  删除 job 或日志；终态 job 的物理回收和全 store 容量仍待完成。
- 合流审计使用实际加入者，不改写 CreatedBy；dispatcher 在提交前复核加入权限，返回既有任务
  或含 job ID 的冲突之前检查当前读取权限。相同 key 不是授权凭证，隐藏任务不能由冲突泄露。

已补 `action_invocations_test.go`、`action_retry_history.go` 对应恢复用例、
`module_action_idempotency_test.go`、`module_action_dispatcher_policy_test.go`、
`module_action_queue_policy_test.go` 和 `deploymentaudit/action_join_test.go`。覆盖跨 actor/入口、冲突、无键合流、并发、审计失败、
精确到期边界、时钟回拨、较老 job 重新绑定、只读排队、64 键上限、深拷贝、压缩重开和权限遮蔽。
测试源码尚未执行；M1 不据此标记已完成。主 daemon/CLI/HTTP 迁移、宿主通道、二段确认、
批量数据路径及真实断连/进程/宿主验收仍未交付或未验证。

## 3. CI 门禁

2026-09-18，内部实现已保存为 `3abe464`，并与主目录基线 `658fe40` 及迁入检查点 `8264dc5` 做三方合并。
本次沿用测试暂停约束，仅进行合并核对、格式整理及文档生成；没有新增运行时编译、测试、CI 或实机通过记录。
下述为 2026-09-13 保存的历史 CI/本地文档基线，不代表当前合并版本：

文档门禁也**没有 CI 记录**：本计划与要求文档随 `40a8b2e`（2026-09-09）加入，而 GitHub CI 在
master 上最近一次运行是 `5306b63`（2026-09-05），其后的提交均未推送。只有本地记录：`6823232`
的提交说明记载 `docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status`
与 `docs:check-status` 通过，2026-09-13 在该提交上复核一致。

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | 断连后任务继续、重连可见 | — | 待执行 |
| R-006 | 待新增 `test-env/scripts/server-backup-send-e2e.sh` | 取消后不完整目的文件被清理 | — | 待执行 |
| R-008 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | CLI 发起的 job 在控制台可见 | — | 待执行 |

## 5. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Module 专属命令能力要求](../requirements/module-command-capability.md) | 开头已整体声明 executor 协议与取消语义被取代；§2 要求迁移前逐条标出被取代的需求 ID，矩阵行尚无标注 | 部分完成 |
| [Module 专属命令能力设计](../../docs/architecture/module-command-capability-design.md) §7、§10 | 标注 executor 协议与取消语义已被本 ABI 取代 | 已完成 |
| [统一动作 ABI 设计](../../docs/architecture/action-abi.md)与[架构索引](../../docs/architecture/index.md) | 已同步内部实现、未验收和生产入口未迁移的边界；完整迁移后继续更新 | 部分完成 |
| [英文架构索引](../../docs/en/architecture/index.md) | 已补英文摘要并链接中文原文，明确内部实现尚未验收 | 已完成 |
| [Module 专属命令参考](../../docs/reference/module-commands.md)与英文镜像 | 目前描述 `anas.module-command/v1` 的现行实现；M0/M1 落地后改写为 job、事件重放与取消语义 | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | `module_command_abi` 取值与 `module_command_*` 错误语义随 ABI 更新 | 未开始 |
| [日志与可观测性](../../docs/architecture/observability-and-logs.md) | 「job 事件日志」一行标注未实现，M0 落地后更新；下文待决的持久化位置与保留期在该文档定案 | 未开始 |

## 6. 待决

- 持久化位置已复用 console job store；终态 job 的最终保留/清理及全 store 限额仍待定，见
  [日志与可观测性](../../docs/architecture/observability-and-logs.md)。
