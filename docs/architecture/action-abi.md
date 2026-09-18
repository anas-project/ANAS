# 统一动作 ABI `anas.action/v1`（设计）

> 状态：**设计；协议、存储与 Module 执行前置实施中，未验收或接入生产执行入口（2026-09-18）**。它取代 [Module 专属命令能力设计](module-command-capability-design.md)
> §7 的 executor 协议与 §10 的取消语义。那套 ABI 尚未发布，因此这里不做兼容层，直接按当前需求
> 重新设计一次。
>
> 同一套协议同时服务两类执行者：部署内的 **Module Command**（无特权）与宿主上的
> **[特权动作](host-action-channel.md)**（root）。协议共用，注册表与授权不共用。

## 1. 为什么重新设计

原 ABI 是「一次调用 = 一次前台执行」：调用方连着、进程跑着、断了就没了。加进来三条需求之后
这个模型不成立：

1. **Web 端发起的任务必须留在服务端跑完**，关掉浏览器再打开还要看得到进度与状态；
2. **`btrfs send` 这类操作会输出大块数据**，与 JSONL 控制流互斥；
3. **破坏性动作需要 plan/apply 两段**，中间隔着一次用户确认。

三条都在说同一件事：**执行的生命周期不该绑在连接的生命周期上。**

## 2. 核心决定：每次调用都是一个 job

不是「长任务才建 job」。**每次 invoke 都创建 job**，快动作只是在调用方读完之前就结束了。

理由是消掉模式分叉。如果只有长任务建 job，就得回答「多长算长」、「快动作断连怎么办」、
「客户端怎么知道这次是哪种」——这些问题在统一模型里根本不存在。代价只是给快动作也记一条记录，
而那条记录本来也是审计需要的。

## 3. 领域模型

```text
Action      具名的工作单元。Module Command 与宿主特权动作都是 Action。
Executor    实现它的进程。信任域不同，协议相同。
Job         一次调用的服务端记录，生命周期独立于任何连接。
Event       job 产生的一条带序号的记录，追加写入 job 的事件日志。
```

Job 状态机：

```text
pending ──▶ running ──┬──▶ succeeded
                      ├──▶ failed
                      ├──▶ cancelled
                      └──▶ unknown
```

终态 job 保留一段有界时间后清理，保留期内可以重复查看。

## 4. 连接不拥有执行

**执行进程是服务端的子进程，不是连接的子进程。**

- 调用方断开 → job 继续跑。这是产品要求：Web 端发起的任务留在服务端执行；
- 要停止只有一条路：显式 `cancel`；
- 调用方重连 → `attach` 到同一个 job，从它上次读到的位置继续。

这一条决定了下面的事件日志必须可重放。

## 5. 事件日志与重放

每个 job 有一条**追加写、带序号**的事件日志：

```text
{"abi":"anas.action/v1","job_id":"job-example","invocation_id":"call-example","seq":1,"type":"progress","progress":{"phase":"draining","current":1,"total":2,"unit":"runners"}}
{"abi":"anas.action/v1","job_id":"job-example","invocation_id":"call-example","seq":2,"type":"warning","warning":{"code":"retrying","message":"Waiting for the managed service"}}
{"abi":"anas.action/v1","job_id":"job-example","invocation_id":"call-example","seq":3,"type":"result","result":{"outcome":"succeeded","changed":true,"value":{}}}
```

- `attach(job_id, from_seq)` 从任意位置重放，因此「关掉浏览器再打开」就是一次
  `attach(job, from_seq=0)`，不需要任何特殊路径；
- 日志有**总量上限**。超限必须写入一条显式的 `truncated` 事件再继续，**不得静默丢弃**——
  一份看起来完整、实际上中间少了一段的日志，比明说截断了更糟；
- `result` 是终态事件，每个 job 恰好一条（失败时是 `error`）。

## 6. 客户端操作

```text
invoke(action, params, idempotency_key?)  → job_id，并从 seq 0 开始推送
attach(job_id, from_seq)                  → 从该位置推送
cancel(job_id)                            → 请求取消
get(job_id)                               → job 记录（状态、动作、脱敏参数、时间）
list(filter)                              → job 列表
```

`idempotency_key` 让重试的 invoke 收敛到同一个 job，而不是跑第二遍——网络抖动导致的重发不该
变成两次装包。

**身份是 `(action, key)`，不是 key 本身。** 全局 key 空间下，两个不同动作凑巧用了同一个 key
（例如都取控制台的 request id）会让第二个动作拿回第一个动作的 job——一个很难排查的正确性 bug。

**同 action、同 key、但参数不同时必须报冲突错误**，不得静默返回原 job：调用方要的显然不是那一个，
静默返回等于把它的 bug 藏起来。

`idempotency_key` 与 §6bis 的合流覆盖范围重叠，边界是：

| 场景 | 谁覆盖 |
| --- | --- |
| 重发时 job **还在跑** | `coalesce` |
| 响应在 job **跑完之后**丢失，客户端重发 | 只有 `idempotency_key` |

因此 key 真正独占的只有「响应在途中丢失」这一个窗口，是秒到分钟量级。**保留期定为 job 到达终态
后 1 小时**——覆盖得住一次丢失响应后的重试，又不至于长到把「重跑」变成「拒绝重跑」。

留太久有语义问题：明天用同一个 key 再跑一次备份，**应当跑**，因为要的是一份新备份。1 小时之后
同一个 key 就是一次新请求。

## 6bis. 并发与合流：不是时间窗防抖

同一个动作被短时间内触发多次是常态——用户双击按钮、控制台在网络抖动后重发、两个管理员同时点了
同一个「安装」。服务端要处理它，但**不该用时间窗防抖**。

时间窗是个拍脑袋的数：设 2 秒，1.5 秒后一次合理的重新触发被吞掉；设 0.5 秒，3 秒后的重复点击又
漏过去。两头都错。

正确的键是**「有没有一个参数相同的同名动作正在跑」**，这是精确的，不需要调参。动作自己声明遇到
这种情况怎么办：

| 声明 | 行为 | 适用 |
| --- | --- | --- |
| `coalesce` | 新 invoke **加入正在跑的那个 job**，不新建 | 幂等的建立性动作。双击「安装」只会看到一个 job，两个管理员看到的也是同一个 |
| `reject` | 拒绝并指向正在跑的 job | 再跑一次没有意义、或有害的动作 |
| `queue` | 排队，前一个结束后用新状态再跑一次 | 需要按新状态重算的动作 |

三者与 `idempotency_key` 是互补的，不是重复：

- `idempotency_key` 是**调用方说**「这两次是同一个逻辑请求」，解决重发；
- 并发声明是**服务端看出**「这两次实际上是同一件事」，解决双击与并发操作者——调用方什么都不用做。

与锁的关系也要分清：锁决定**能不能同时跑**，合流决定**要不要变成同一个 job**。一个持
`workspace_write` 锁的动作被触发两次，靠锁只能让第二次等着然后再跑一遍；`coalesce` 才是「用户
期望的那一个 job」。

## 7. 取消语义

取消是**协作式**的，并且动作要声明自己支持到什么程度（沿用原设计的三档）：

| 声明 | 含义 |
| --- | --- |
| `false` | 不可取消。`cancel` 被拒绝，说明理由 |
| `true` | 任意时刻可取消 |
| `safe_points` | 只在动作声明的安全点取消 |

流程：`cancel` → 通知 executor → 宽限期内自行收敛 → 超时则杀进程组。

**杀掉之后 job 必须标记 `outcome: unknown`**，而不是 `cancelled`：动作没能确认自己收敛，外部状态
处于什么样子无人知道。把未知报成已取消，是这类系统里最容易造成后续误判的一种谎。

## 8. 大块数据不进控制流

控制流是 JSONL，小而结构化。**批量数据永远不复用它。** 三种处置，按优先级：

1. **动作自己打开目的地**（**唯一实现的一条**）。目的地由调用方**命名**、由动作**校验并打开**，
   数据根本不经过通道，也不经过浏览器。**今天「目的地」只能是本地文件系统路径**——挂载点算，
   所以外接盘、NFS/SMB、rclone 挂载都能用，但那些挂载 ANAS 管不了。让目的地能是另一台机器，
   见[远端备份、异地容灾与分布式部署](remote-backup-and-dr.md)。
2. **（不实现）产出制品，另行下载。** 经浏览器取回制品需要一个独立的下载端点，而一个 URL 就
   等于一份完整备份。第 1 条已覆盖绝大多数场景，在有真实需求之前不开。见 §13。
3. **（永久拒绝）在控制流里内联 base64。** 它把一个 GB 级负载塞进一条本该是几百字节的记录，
   同时毁掉日志重放与总量上限。

第 1 条覆盖绝大多数场景，也是 `btrfs send` 的答案，见 §10。

## 9. 两段确认

破坏性动作分 `plan` 与 `apply` 两步，机制与理由见
[宿主特权动作通道](host-action-channel.md) §3.4。在本 ABI 里它体现为：`plan` 也是一个 job，其
`result` 携带摘要与一次性 token；`apply` 是另一个 job，请求里带上该 token。

两个 job 在记录里互相引用，因此事后审计能看到「用户当时看到的是什么」和「实际执行了什么」。

**token 有效期 5 分钟，一次性消费。** 它是 ABI 层的东西，不是 HTTP 层——CLI 的
`plan` / `apply` 与控制台的确认对话框用的是同一个机制，留下的也是同一对 job 记录。客户端在用户
停留过久时应当重新 `plan` 换新 token，而不是把有效期调长；`apply` 时 token 已过期必须重新展示
新摘要并要求再次确认，不得沿用旧摘要执行。

## 10. 工作示例：`btrfs send`

它同时具备本 ABI 要处理的三个特征：长时、大数据、特权。

**目的地由动作打开。** 调用方给的是一个**经校验的目的地描述**（备份目标路径，或已配置的远端
传输），不是一个 fd，也不是「把字节流给我」。动作自己打开它并写入。因此这条操作不需要通道支持
第二个数据 fd——§8 里被判为「最实际的一处工作量」的那件事，在正确的分工下不存在。

**已写字节是精确的，总量是可估的。** `btrfs send` 不提供原生进度，但动作持有写入端，所以
**已写字节数与速率**随时可知且精确。总量则可以从文件系统自己的记账推出来，只是都算估算：

| 场景 | 估算来源 | 代价与偏差 |
| --- | --- | --- |
| 增量发送（`-p parent`） | `btrfs subvolume find-new` 列出自某代以来变化的 extent，求和 | 快；与实际流最接近 |
| 全量发送，已启用 quota | `btrfs qgroup show` 的 referenced | 近乎瞬时；quota 本身有运行开销，多数部署未开 |
| 全量发送，未启用 quota | `btrfs filesystem du -s` | 结果可用，但**这一步自己就要走一遍 extent**，大 subvolume 上可能几分钟——在慢操作前面再加一个慢步骤 |

因此规则是：

1. **机会性估算，绝不阻塞传输**。能便宜地拿到就拿（增量的 `find-new`、已开 quota 的 referenced）；
   要花几分钟才能算出来的（未开 quota 的全量 `du -s`）默认不做，除非部署显式打开；
2. **估算值放在独立字段**（`total_estimated`），与精确的 `written` 分开，客户端据此决定显示
   百分比还是纯速率；
3. **实际超出估算时不得钳制**。压缩、元数据开销、发送期间的写入都会让实际值偏离；宁可显示
   超过 100%、或切回不确定态，也不要把进度条卡在 99% 撒谎。

一个会走到 90% 然后停很久的进度条比没有进度条更糟——但那是**假装精确**的错，不是估算本身的错。

**取消是 `safe_points`，且必须清理。** 中途取消会留下一个不完整的目的文件，动作必须删除它或
标记为不完整。**`btrfs send` 不支持断点续传**，被取消的传输只能重来——这一点必须在取消确认时
就告诉用户，而不是等他发现进度回到零。

**归属**：它是特权动作（需要 `CAP_SYS_ADMIN`），因此跑在 `anas-hostd` 里，走本 ABI，逐次审计。

## 11. 两个信任域，一套协议

| | Module Command | 宿主特权动作 |
| --- | --- | --- |
| 执行者 | 部署内的 module executor | `anas-hostd` |
| 权限 | 无特权 | 完整 root |
| 动作从哪来 | Module manifest 声明，随部署冻结 | 编译进二进制，随 anas 升级 |
| 授权 | 部署内的锁与角色 | socket 权限 + `SO_PEERCRED` |
| 协议 | **相同** | **相同** |

共用的是**线格式与 job 语义**，不是代码路径与注册表。信任域不同的两段逻辑共用一个进程，是这类
设计最常见的失手方式。

## 12. job 的可见性：一份 job 存储，一份审计

**控制台能看到 CLI 发起的 job，反之亦然。** 这不是可选项：

- 入口不同不该导致看到的系统状态不同。管理员在终端起了一个备份，另一个管理员在控制台上必须
  看得到它正在跑，否则会重复触发；
- 审计只有一份才有意义。两份各记一半的记录，事后没有任何一份能回答「那天到底发生了什么」。

因此 job 存储是**单一的**，不按入口分区。`list` 返回的是同一批 job，只按查看者的权限过滤，
不按谁创建过滤。

## 13. 制品下载端点：默认不提供

§8 第 2 条（job 产出制品、经浏览器另行取回）**不实现**。第 1 条覆盖了绝大多数场景——备份写到
一个路径、推到一台远端，数据从源直接流到目的地，不经过控制台也不经过 HTTP。

不提供是有意的，不是没排上：一旦开出这个端点，**一个 URL 就等于一份完整备份**，泄漏链接和泄漏
备份没有区别。在没有真实需求逼着开之前，不开就是最省事的正确答案。

将来真要开（例如管理员必须把备份下载到自己的机器上带走），下列几条必须**一起**定案，不能先开
端点再补：

| 方式 | 代价 |
| --- | --- |
| 复用控制台会话 Cookie | 浏览器可行，CLI / `curl` 拿不到；链接不能转交 |
| URL 内明文 token | 会进浏览器历史、代理日志、`Referer`——**已排除** |
| 请求头带 token | 安全，但浏览器点击下载设不了请求头 |
| 短时效签名 URL | 折中：泄漏窗口由有效期限制 |

要一起回答的是：有效期多长、能否重复使用、**是否允许把制品交给非浏览器客户端**。三者相互牵制。

§8 第 3 条（控制流内联 base64）是**永久拒绝**，不是推迟：它把 GB 级负载塞进一条本该几百字节的
记录，同时毁掉日志重放与总量上限。将来即使开了第 2 条，第 3 条依然不开。

## 14. 待决

- 终态 job 的最终保留/清理与全 store 限额（持久化已复用 console job store，见 §16）；
- 事件日志与应用日志、审计日志的关系，见[日志与可观测性](observability-and-logs.md)。

制品下载端点不在此列：它是**明确的不做**（§13），不是待决。

## Module 队列与取消适配（2026-09-18，已编码、未验收）

`jobexecutor.ModuleActionWorker` 是显式装配的服务端执行适配器，调用方必须已经取得并核验共用
execution lease、提供冻结注册表、workspace 范围及真实权限/审计适配。构造不启动 goroutine；
`Run` 只能使用 daemon context，且一个 worker 只运行一次。轮询读取同一 journal，Start 仍由存储
原子仲裁；一次执行后重读状态。容量或工作区阻断不会重排同工作区动作；未知定义不自动执行。
启动时发现未对账的 running action 会停止，不把磁盘记录当成可恢复进程。

`Cancel` 使用取消者当前身份授权，而非假定创建者才可取消。排队任务在 jobs.lock 内与 Start
竞争；运行任务先通过 `RequestActionCancelObserved` 将取消者与时间写入 `Action.Cancellation`，
再向本次调用的私有 channel 发出一次通知。意图不放在有界事件尾部，避免截断抹去取消证据。
提交 observer 在锁内重新调用授权适配器并记录取消者审计；这些
回调不得重入同一 Store。重复取消重新检查权限，但不重复通知或追加事件。审计/持久化失败不
发送取消；持久化意图不是 cancelled，执行器仍须在实际清理后输出确认终态并正常退出。

Linux 执行器使用单独的取消 fd，stdin EOF 只表示请求交付结束。强杀、异常输出、退出证据不符
或清理失联不能转成已取消/成功。后者保留应用锁及 execution lease，并停止注册表/worker 接纳；
外层执行所有者仍负责全局停机和独立恢复。受信 Module executor 不得 daemonize、逃离进程组或
更换权限；本适配不构成恶意代码 sandbox，也不能装载宿主 root 动作。

原有 Module Command、主 daemon、CLI/HTTP 还没有迁移为此 worker 的产品入口，生产 ingress
仍关闭。队列/取消/权限/终态回归源已添加但没有执行，真实 Linux 进程、宿主通道及 Incus E2E
不能由这些内部适配器或测试文件推定通过。后续各节保留按日期记录的实现边界。

## 15. 协议原语实现边界（2026-09-16，未运行）

`internal/actionabi` 开始实现共用线格式，不包含动作注册表、权限、socket、进程启动或 job 存储。
它是 Incus HTTP 宿主盘点/恢复接线的前置；宿主动作仍需受限通道及逐动作需求评审。现有
`anas.module-command/v1`、CLI/HTTP 调用继续按原实现工作，没有兼容桥。后续在共用 journal 增加的
内部动作记录见 §16；尚未编译、测试或用实际 executor 运行。

### 15.1 请求和有界 JSONL

可信 dispatcher 先创建 job、解析具名动作并完成该注册表的类型化参数校验，再向 executor stdin
交付一个请求并关闭输入。请求仅包含 `abi`、`job_id`、`invocation_id`、`action`、`parameters`。
它不是客户端 invoke/attach/cancel API，也不能用它指定 handler、argv、env、secret、UID 或路径。
参数对象里的值仍必须由动作注册表核验；帧的 JSON 合法不代表获准执行。注入凭据的窄范围交付
留在各自信任域，不能借 parameters 回填或将旧 Module Command secrets 原样并入。

Module 注册表内部适配器在动作专属参数校验后，对参数对象重新编码，稳定嵌套对象的键顺序与
空白，避免同一请求因回调返回的 JSON 顺序不同而得到不同摘要。解码使用 `UseNumber` 保留整数
精度；输出与回调内存分离，再次验证大小边界。此步骤不替代动作授权，也不实现并发合流或幂等 key
的一小时保留。排队执行仍重新校验完整参数与冻结 descriptor，不允许新增默认值或切换 deployment。

每帧上限 64 KiB（含唯一结尾 LF），参数对象与成功结果对象各限 32 KiB，JSON 深度不超过 16。
读取拒绝多行、CRLF、空行、缺少 LF、尾随 JSON、重复字段（含转义别名）、未知字段、结构字段
大小写别名、控制字段显式 null 和无效 UTF-8。任意 action 专属对象仍受重复字段/深度/大小限制，
其具体字段属于注册表的 schema。请求/事件的格式化字符串隐藏载荷，错误不转述解析片段；这不能
替代 registry 公有投影和敏感值过滤，事件持久化前仍须完成脱敏。

`ReadRequest` 有界读取一个请求到 EOF。`Decoder.Next` 一次读取一帧，错误后拒绝继续接受后续行。
codec 不持有连接或进程，I/O deadline、关闭 stdin、取消宽限期和进程组控制由实际适配器负责。
字节上限不是时间上限，不能让不关闭连接的调用方占住无超时读取。

### 15.2 执行器帧、日志事件与重放

事件绑定 `job_id` 与 `invocation_id`，只接受匹配本次调用的记录。`type` 与唯一同名 payload
对应：`progress`、`warning`、`result`、`error`，日志还可有 `truncated`。结构错误或终态后的
任意额外事件都会使流失效。§5 示例是持久化/订阅 envelope，**执行器输出必须省略 seq**。
可信 job writer 在公有投影后分配序号；执行器不能自选序号或用 truncated 掩盖丢失输出。

`NewReplayStream` 接收最后已消费的 from_seq；普通事件须严格递增一位。缺口必须由受信日志的
`truncated: {from_seq, through_seq}` 覆盖：marker 的 seq 等于 through_seq + 1，范围必须覆盖
当前期待序号，之后从 marker 的下一位继续。若调用方已消费到 through_seq，marker 恰好是下一条，
仍可消费它。重复、回退、溢出及未覆盖缺口都拒绝。实际持久化和限额见 §16，不能由执行器或普通
订阅者伪造 marker。
空重放尾部或订阅断连不产生终态判断；状态仍从 job 读取，不能将浏览器断连映射成 cancelled。

进度支持只有 phase 的阶段通知，或带 current/unit 的计数。current、total、total_estimated
均为 uint64，避免浮点舍入；精确 total 与估算 total_estimated 互斥。精确 total 不得小于 current，
估算值可以被超过，编码器不钳制。跨 JSON 客户端必须保留整数精度，前端接线仍待完成。

### 15.3 终态与真实进程退出

成功 result 必须为 `outcome: succeeded`，显式 changed 和对象 value；协作取消的 result 为
`outcome: cancelled`，不能附带成功结果/changed。error 只允许 failed 或 unknown，并提供稳定
code 和有界 message。语法上收到 result 不会立即把 job 标为成功。

进程适配器使用 `ExecutionReader` 读到实际 EOF、收割子进程后再 `Finish`，EOF 由 decoder 自己
确认，不采用调用方自报。零退出码与 succeeded 才能成功；正退出码与 failed 才能确认为失败；
cancelled 还需确有显式取消请求且零退出码。强杀（含取消宽限期超时）、未退出、缺少/损坏终态、
尾随坏数据或退出与终态不一致都返回 unknown，不能推断已收敛。这里只校验证据，不发送取消或杀进程。

§16 开始复用现有 `consolejobs`/`jobexecutor` 的存储、执行租约与审计，后续在同一 job 模型上补协作取消、
并发合流、幂等保留、确认 token 和 CLI/HTTP 可见性。不能新建按入口分开的 job 存储，也不能把
本节原语视为 `anas-hostd` 已可运行。所有待测项先记入 Incus 实验清单；生产 ingress 仍关闭。

## 16. 共用 journal 与终态适配（2026-09-17，未运行）

`consolejobs` 增加内部 `action.invoke` job，仍使用同一 `jobs.jsonl`、`jobs.lock`、execution lease、
审计 observer、队列/运行容量和 workspace mutation/compensation 约束。动作 binding 保存在
`Job.Action`；ABI/name/invocation 不变，LastSeq 和 outcome 随原子记录更新。现有状态拼写不变：
`canceled` 对应动作 `cancelled`，`interrupted` 对应动作 `unknown`，不能将 interrupted 解释为已取消。

`CreateActionObserved` 只供后续受信 dispatcher 使用：调用方负责授权及参数公有投影，存储再校验
有界协议和通用脱敏。`StartActionObserved` 与运行中事件写入须持有对应 store 的 execution lease；
创建、启动、终态、排队取消和动作重启恢复要求非空审计 observer，observer 拒绝则不提交生命周期。
普通 worker 不领取动作 job；普通 append/update/transition/cancel 也不能替代动作边界。尚无动作
dispatcher、进程启动器或新增客户端入口；这些防护不是动作注册表/授权已经完成的声明。

### 16.1 两种游标与一次提交

每条 ABI 事件有 job 内独立 seq，同时其外层 console Event 保留全局 ID。不能把全局 ID 作为
attach 的 from_seq，也不更改现有 SSE 游标。新增 `action_event` journal record 将本次事件、可选
截断 marker 和完整新 job 状态放在**同一行**；追加与 fsync、半行恢复、压缩 generation 继续复用
已有机制。成功帧与 succeeded 状态不会分两行写入。持久化失败仍可能具有不确定提交结果，调用方
不得启动第二次执行；应读取/恢复同一 job。

恢复与压缩同时核对事件 binding、连续 seq/显式缺口、终态 payload、LastSeq/outcome 和全局 cursor。
压缩不能回退动作序号或改写已完成 outcome。schema_version 仍为 1，扩展 typed 字段与 record kind；
旧 reader 会拒绝新字段/record，含动作日志不能直接交给旧二进制读取。未实现降级转换。

### 16.2 有界尾部与显式截断

沿用每 job 的 EventCapacity（默认 1024），每 ABI 帧仍最多 64 KiB；至少需 2 个槽位。追加将超过
容量，或追加时发现最老事件超过 EventRetention（默认 7 天）时，丢弃此前完整保留前缀，并在同一
原子 record 写入 `truncated {from_seq: 1, through_seq: 旧 LastSeq}` 和新的业务事件。marker 自身
使用旧 LastSeq + 1，新事件为 +2。后续再截断会覆盖旧 marker，范围仍从 1 累计，不静默抹去缺口。

`ReplayAction` 从任意已有 seq 分页，落在已删除范围内时返回该真实 marker；不临时捏造序号或改写
事件。未来游标拒绝。终态后的空页需读 job outcome，不代表取消。终态之后不追加截断事件；保留
有界尾部与唯一终态，动作记录不使用旧路径按时间静默删除。时间保留目前只在下一次追加时检查，
终态 job 的最终清理以及全 store 保留策略尚未实现，不宣称已有严格 TTL 或全局磁盘配额。

### 16.3 执行流到持久化终态

`jobexecutor.ActionRecorder` 接收新启动的动作 job、对应 lease、实际输出 reader、审计 observer
和**必须提供的动作公有投影**。投影不能改变事件身份、类型、精确计数、单位、outcome 或 changed；
动作 schema 决定哪些标签、消息与结果可公开。回调错误只返回稳定错误，不保存原始 executor 错误。
通用 secret 脱敏只作第二道检查，不能识别所有普通字符串中的凭据。

progress/warning 通过既有 store 持久化，精确计数不折成 job 的整数百分比，也不无限累计到 Warnings。
result/error 先暂存内存；实际 reader 读到 EOF、进程适配器收割并提供退出证据后，Finish 才提交。
成功后仍有额外数据、强杀、缺终态、投影/写入失败或退出不符，均以稳定 `execution_unconfirmed`
unknown 事件替代暂存终态。守护进程重启在同一 execution lease 下补 `daemon_restarted` unknown。
失败/未知的 mutating 动作保留 compensation 阻断；排队取消则在 jobs.lock 下与启动竞争，只有确实
尚未执行时才能直接提交 cancelled。

recorder 不启动、发送信号或收割进程。受监督适配器仍须使用 daemon 所有的 context、有限 I/O 和
清理超时，在 Next 失败后停止/排空并收割子进程，再用独立的有限清理 context 写终态；不能把浏览器
断连传入此执行 context。Module Command、CLI/HTTP DTO、动作权限/注册表及 root 通道均未迁移。
并发合流、action/key 幂等与终态后 1 小时过期、协作取消通知/宽限期及共用两段确认仍待接入。
本轮只编码与文档生成；所有故障/竞态用例和真实独立宿主验收仍待执行。

### 16.4 执行清理失联后的持久化阻断（2026-09-18，未运行）

只在 registry 内存中保存 poison 标记不足以跨重启保护执行。因此，共用 store 的 `canStart`
在容量和 workspace 判断之前读取现有 action job：unknown 且原因为
`execution_containment_lost` 或 `daemon_restarted` 时，返回 `ErrActionExecutionBlocked`，
并附按稳定顺序排列的相关 job ID。`Start`、`ClaimNextObserved` 和 `StartActionObserved` 均经
同一判断，覆盖只读任务及同一 store 的其他 workspace。

拦截来源是已有 journal 中不可改写的终态/原因，不另存旁路锁标记，也不设 TTL。普通业务补偿
确认不改变原错误，因此不会解除进程清理拦截；压缩、重开 store 或重建 registry 同样不能解除。
允许继续读取和重放回执，以及取消尚未启动的排队任务。若进程清理已确认、只是执行结果无法确认，
`execution_unconfirmed` 不新增此全存储阻断，原有 mutating compensation 规则仍有效。

它只阻止新的执行，不证明旧执行已停止。执行所有者仍须在 writer 未结束时持有执行租约、停止
接纳任务，不得与仍在写入的 recorder 并发另写终态。独立核验残留进程及 writer 的受限恢复动作
仍待实施；不能用清空 journal、普通补偿确认或原地修改错误码作为恢复。回归测试源已覆盖阻断
来源、两类启动入口、跨 workspace、只读、压缩/重开及补偿确认，但尚未运行。

### 未启动调用的终态与恢复（2026-09-18，未运行）

`RejectQueuedActionObserved` 为预检失败提供独立的内部存储入口：必须仍处于 queued，调用身份
必须匹配，并在审计允许后将 failed job 与唯一 `action_not_started` 事件写入同一 journal record。
它不是客户端任意填写失败状态的 API；固定错误码/消息不携带原始权限、参数或可执行路径错误。
与 Start 的竞争由 jobs.lock 决定；一旦 Start 已提交，预检拒绝必须冲突，不能反向消除执行证据。

没有 StartedAt 的终态只允许排队取消或这一类预检失败，不允许 succeeded/unknown，也不设置
业务补偿要求。已经运行的执行器不能用 `action_not_started` 伪装未启动。快照恢复同时检查 job、
终态事件及 StartedAt，恢复终态时按实际是否启动选择前态，不把排队拒绝重建成运行中失败。

注册表仅在启动之前的身份、冻结参数、授权、锁获取或可执行文件准备失败时使用该入口；明确的
容量/锁忙/补偿阻断仍保留排队。Start 提交不确定或执行清理失联不适用此拒绝路径，继续保留
unknown 与执行阻断的边界。回归源覆盖拒绝原子性、审计失败、身份不符、启动竞争、重放和压缩重开；
代码与测试尚未运行，不能据此宣称 Module Command 迁移或宿主通道已完成。

### 持久取消意图（2026-09-18，未运行）

当前 worker 使用 `RequestActionCancelObserved`，在同一 job 的 `ActionState.Cancellation`
保存首次取消操作者及时间。操作者由应用授权适配器确认，提交使用绑定该操作者的审计 observer；
不是以原创建者身份代替取消者。首次记录只在 running 状态提交，和终态写入在同一 jobs.lock 下
竞争；重复请求保留原值。worker 在持久化成功后才关闭自己的取消通知通道。

这份控制状态不是执行器 warning，也不使用执行器 seq；截断、压缩和重开不能移除它。普通
`job_updated` 只允许第一次加入取消记录，并保持其余字段不变；既有记录的操作者/时间不得改写
或删除。job/observer 的深拷贝同步包含该字段，外部修改返回值不能改掉已保存的请求。

运行中的 cancelled 终态需同时有该请求和受监督执行器的 EOF/退出/确认检查；请求不是结果，
执行可能抢先成功，超时强杀仍是 unknown。排队取消没有执行进程，因此不要求这份 running 控制
记录。新增持久化回归测试源覆盖审计失败、重复请求、身份不符、无请求的 cancelled 拒绝，以及
事件截断、压缩和重开后的保留；测试尚未执行，现有客户端及宿主特权通道仍需独立接入与验收。

## Module 调用与订阅服务（2026-09-18，未运行）

`jobexecutor.ModuleActionDispatcher` 是后续 CLI/HTTP 共用的内部服务，不是新增公网路由。
构造不会启动执行；daemon 单独调用 Run，调用方只调用 Invoke/Get/List/Attach/Cancel。
它将队列、进程控制与取消委托给唯一的 `ModuleActionWorker`，不维护另一份取消记录或任务存储。
主 daemon、冻结 manifest 的应用工厂、真实角色适配器与现有客户端仍需显式迁移。

可信应用适配器提供当前操作者的 invoke/read/cancel 授权、生命周期审计和绑定取消者的审计。
创建与启动在提交前再授权；worker 的附加 CheckQueued 在可执行文件准备前检查入口权限，
注册表原有 Check 与声明锁内复核继续负责冻结绑定。回调须遵守 context，并且在 pre-commit
阶段不得重入同一 job store。权限失效走未启动拒绝，不能将可能已启动的调用改报成安全拒绝。

读取按当前权限而不是原创建者过滤；已删除或下线动作的历史 job 仍可读。Module 视图和消费器
都排除宿主命名空间，不能因为共用 journal 而消费、取消或预检拒绝宿主动作。Attach 每页重新
授权，使用真正持久化的 seq/truncated；订阅断连、回调错误或读权限撤销只终止订阅，不取消 job。
服务不会把空尾页当作取消，也不会把控制台全局 SSE ID 当成动作序号。

Invoke 保留调用方的可选 key，跨 actor/入口的幂等与合流由下节共用 store 处理；无键调用不创建
随机重试别名。加入时在提交前以实际加入者再授权，不冒用原创建者；返回既有 job 或带 job ID 的
冲突前检查该操作者当前读取权限。key 不构成访问授权，跨 workspace 冲突也不能泄露隐藏任务。
Cancel 复用 worker 的持久请求和审计，只有提交成功才通知执行器；返回 running 记录不表示
cancelled。执行清理失联继续由持久阻断及租约保留处理，不能通过重建服务恢复执行。回归源未运行。

## 动作级幂等、并发策略与键归属恢复（2026-09-18，未运行）

`CreateActionWithPolicyObserved` 是注册表和 dispatcher 当前使用的内部创建边界，复用原
`consolejobs` 的 journal、锁、审计和执行租约。`CreateActionObserved` 保留为不带新策略的
内部旧记录边界，不能据此对外承诺新重试语义；当前 Module 执行不静默接纳缺少策略的旧 job。
尚未迁移的 CLI/HTTP 产品入口、宿主注册表、二段确认与批量数据动作不由此自动具备这些能力。

### 请求身份与公有投影

重试身份是同一 store 的 `(action, key)`，不包含操作者或传输路径。workspace、mutating、
并发模式、ABI/name 和规范化完整参数属于请求摘要；Module 包装同时包含冻结 deployment、
descriptor 和模块名。同 key 请求这些字段不同则冲突，不能只信任调用方自报摘要。
严格协议校验后用 `UseNumber` 统一对象键序与空白，保留精确整数；此过程不将不同数值或默认值
折为同一请求。存储的通用脱敏若改变参数，拒绝创建，不把脱敏占位符送给执行器。

每次 invoke 仍先通过当前授权和冻结描述符检查，包括重试命中。读取权限按具体返回 job 再校验，
不因新调用者有权 invoke 就允许读取其他 workspace 的冲突任务。原创建者保留在 CreatedBy，
新重试键的审计记录使用实际加入者；审计拒绝不会消费键或修改原 job。

### 活跃任务合流与终态保留

并发模式由受信 Module 注册表提供，内部未显式设置时使用保守的 `reject`，不是客户端可改参数。
有效同 key 重试优先返回原 job，之后才判断等价 pending/running 请求。`coalesce` 返回原 job；
不同 key 作为新别名原子绑定，空 key 则不占别名槽位。`reject` 返回稳定冲突；`queue` 建立独立
job，并在共同领取边界按现有 CreatedAt/ID 顺序阻止等价任务越过较早的 pending/running 任务，
包含只读动作，不以时间窗防抖代替策略。已有应用锁、容量与 workspace 约束继续有效。

pending/running 的 key 不因时长过期。达到终态后保留 1 小时，在到期边界同 key 可用于新请求。
逻辑到期并不删除 job、结果或事件；全 store 磁盘限额与终态 job 最终回收仍未实现。每个 job
最多保存 64 个重试别名；达到上限显式返回容量错误，不静默丢弃还可重试的旧键。无键合流不
生成随机别名，因此不会仅因反复无键调用耗尽该上限。

### 单日志提交与恢复

`ActionState.Policy` 保存模式、请求摘要及每个 key 的摘要/绑定时间；不保存原始 key。最初
绑定时间采用实际 job 创建记录的时间，合流增加别名采用单个 `action_key_bound` 记录，同时
提交新 revision 与完整 job，不创建另一份 key 数据库。原有每 job 一条幂等创建回执保持不变，
其身份由新 invocation ID 建立；它不是 public key 的永久有效记录。

恢复检查新绑定必须晚于前一归属的终态保留期。压缩快照加载全部 job 后，按每个 key 的绑定
时间验证归属链，拒绝重叠或歧义。选择最近归属依据绑定时间而非 job 创建时间，允许过期 key
加入较早创建但仍在运行的合流 job；已经重新绑定后，时钟回拨不能复活旧归属。新增别名不能
改写冻结参数、取消意图、执行事件序号或 outcome，复制 job 时分别深拷贝取消记录和别名切片。
新字段/record 的旧 reader 拒绝行为仍适用，不提供直接降级转换。

相关测试源覆盖并发合流、无键调用、拒绝/只读排队、同键冲突、审计失败、精确到期、时钟回拨、
旧 job 新绑定、别名上限、快照重开、归属重叠、返回值隔离和权限遮蔽。代码和测试均未编译或
执行；这只是当前内部实现，不能作为 ACTABI-R-007/R-009 或 Incus 生产验收通过的证据。

## Module 执行器的取消管道与原生夹具（2026-09-18，未运行）

Linux Module 适配器通过单独的继承描述符交付取消通知：环境中
`ANAS_ACTION_CANCEL_FD=4` 指向只读管道，关闭其写端表示一次取消请求。stdin 的 EOF 仅表示
一个调用请求已交付完毕，stdout 的 EOF 仅表示输出结束，浏览器、attach 或宿主 socket 的断连
都不能替代这个通知。root 动作通道仍须在自己的授权与传输边界实现取消，不能复用此 fd 假定。

`actionabi.ReadModuleCancellation` 是执行器侧的同步读取辅助函数，只接受该专用管道的 EOF。
任何字节、读取错误或连续空读都返回稳定协议错误，不转述原始 I/O 错误，也不解释为已取消。
辅助函数不会读取环境、打开 fd、启动 goroutine 或取消业务 context；执行器自行持有并关闭
reader，避免正常结束后留下等待 goroutine。`safe_points` 执行器收到请求后仍需等待安全点、
完成清理，再输出 cancelled 终态并正常退出；父进程的宽限期和 unknown 判定没有改变。

新增回归源分别覆盖协议与真实进程边界：

- `internal/actionabi/protocol_regression_test.go` 与 `module_cancellation_test.go` 覆盖严格请求、
  字段/大小/深度限制、精确整数、真实 EOF、尾随数据、截断重放以及专用管道语义；
- `internal/jobexecutor/action_recorder_regression_test.go` 覆盖终态延迟提交、公有投影及其内存
  隔离、不可改写的计数与调用身份，以及落盘失败后的稳定 unknown；
- `internal/jobexecutor/module_action_native_regression_linux_test.go` 将测试二进制作为冻结 ELF
  夹具，覆盖工作目录绑定、环境隔离、正常失败、协作取消、忽略取消、成功后挂起、缺终态、
  尾随坏数据及 stderr 超限。只在支持 pidfd 的无特权、无 capabilities Linux 环境执行；
  因平台、权限或测试二进制超出大小限制而 skip 不等于验证通过。

本节只记录接口和测试源，没有运行记录。上述本地进程夹具不操作服务器、Incus 或宿主网络，
也不替代 CLI/HTTP 迁移、残留进程恢复、受限宿主通道或 Incus 真实 guest/网络的验收。
