---
doc_type: review
status: current
created: 2026-09-27
updated: 2026-09-27
---

# Incus 转发续期与入站中介的运行 owner：三个候选对比

状态：决策输入，未定案。本文不改变实现、需求或 M10a/M11 的暂停状态（[计划](../plans/incus-module.md) §12、
[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §7.1）。基线：`dddae0c2` 加当前未提交工作树。
「现状」「既定约束」两节逐项对照当前源码、systemd 单元与需求核对；标为分析或推断的内容是本文判断，
没有运行测试或实机实验。

## 1. owner 要承担什么

下面四件事都是持续性的，任何一件没有 owner，相应的生产门禁都不能解除：

| 职责 | 现状 | 依据 |
| --- | --- | --- |
| 转发许可续期 | 许可是 nft 集合 `flows`/`sources`/`arp_sources` 与 ipset 兼容集合中带 30 秒 timeout 的逐实例元素，键为宿主 veth ifindex、MAC、IPv4 与目的 IPv4/TCP（`internal/incusingresshost/forwarding_nft.go`、`forwarding_types.go` 的 `ForwardingPermitTTL`）。没有续期，30 秒后流量回到 Docker 默认 DROP | `INCUS-R-102`：每次新增或续期都要重新核验实例 UUID、运行代际、地址、MAC、物理接口与目的路由。续期不是刷新计时器，要读 Incus 实例事实（管理证书）和内核 |
| 实例增减与停止撤销 | Forgejo controller 每 15 秒轮询，每个作业用租约证书创建一个 one-job 实例、结束后销毁（[Forgejo 设计](../../docs/architecture/forgejo-module-design.md) §4.3、[Forgejo 技术文档](../../modules/forgejo/docs/technical.md)）。新实例要及时拿到许可，消失的实例要撤销；TTL 过期只关闭新连接许可，不能证明既有连接已撤销 | `INCUS-R-103` |
| 重启与崩溃后对账 | 宿主重启后 nft/ipset 消失，`/var/lib/anas/incus-host/state.json` 的 `forwarding_scopes` 回执还在；要按回执对账，不能自动认领同名对象 | `INCUS-R-104` |
| ingress 请求中介（M11） | 消费者运行时往租约目录写请求文件（`computeingress.RequestWriter`），中介校验后渲染 Traefik 文件 provider 配置，重启时对账撤销过期请求 | 宿主供给设计 §5.1.5；越权反例归 `INCUS-R-087`/`R-088` |

当前门禁：`ForwardingPermissionBackend.runtimeOwnerReady` 在生产构造中恒为 false，enable 计划必带
`forwarding_lifecycle_integration_unavailable` 阻止项和 `automatic_forwarding_renewal_unavailable` 警告
（`internal/incusprovision/forwarding_permissions.go`）。M11 侧，`cmd/anasd/main.go` 已经构造
`ControllerCoordinator` 与 `HostActionService`，`HostActionService.StartIngressWorkspace` 已实现但没有生产
调用方；中介的宿主观察经 `HostObservationInvoker`，每次新建一个 `incus.ingress.observe_http` job。

## 2. 既定约束

### 2.1 常驻进程与后台任务模型

- [workspace 与备份体系（已归档）](../plans/archived/workspace-backup.md)「守护进程：推迟到 web 服务之后」：
  考虑过 Docker 式的常驻 root 守护进程，结论是不在下面再垫一层，**让 web 服务（anasd）本身充当常驻进程**：
  以 root 运行、持同一把 `state/lock`、读写同一批文件。如果将来仍单独做守护进程，有两条不可放弃：不得成为
  第二个真相源（决策重读磁盘、走同一把锁、守护进程不在时 CLI 仍能独立工作）；API 必须区分只读查询与变更。
  定时工作交给 systemd timer，容器拉起交给 restart policy。
- [统一动作 ABI](../../docs/architecture/action-abi.md) §2、§4、§12：每次 invoke 都建 job，执行归服务端、
  不归连接，job 存储只有一份。终态 job 的保留、清理与全 store 限额仍是 §14 的待决项。
  [日志与可观测性](../../docs/architecture/observability-and-logs.md) §3 据此写下「不引入常驻进程做日志维护，
  除非有别的理由」。
- [控制台要求](../requirements/web-api-admin-console.md) §4.3：anasd 内的异步任务用 job 自有的 context 执行，
  daemon 持有 `jobs.execution.lock` 排他租约，每个 workspace 同时只有一个变更任务。

### 2.2 特权边界

- `HOSTACT-R-001`/`R-002`/`R-005`/`R-008`：特权动作编译进 `anas-hostd`；通道 socket 激活，不留常驻 root
  进程，逐次审计；特权入口只有 `anas-helper`（`CAP_NET_ADMIN`、无产物）和 `anas-hostd` 两个；非 systemd
  发行版上常驻部分只做 accept。
- 确认模型：当前 Incus 的建立性与破坏性宿主动作都要 plan 加五分钟一次性确认；唯一不要求确认的是只读的
  `incus.ingress.observe_http`（[宿主通道](../../docs/architecture/host-action-channel.md) §13、§13.1）。

### 2.3 anasd 的身份

- `packaging/systemd/anasd.service`：`User=root`、`Group=root`，未设 `CapabilityBoundingSet`；
  `NoNewPrivileges`、`ProtectSystem=strict`，可写 `/var/lib/anas`（含上面的宿主 `state.json`）、`/srv/anas`、
  `/srv/anas-backups`，允许 `AF_NETLINK`；`Restart=on-failure`、`RestartSec=3s`。
- 控制台要求 §7.5：首版 anasd 以 root 运行（读 root-only TLS 私钥、Docker socket、workspace）；宿主通道
  设计 2026-09-23 的结论是不迁移 anasd 的非 root 身份。控制台要求 §7.4：不给 anasd 配
  `AmbientCapabilities`，避免把显式的一次性提权变成隐式的常驻提权。
- `packaging/systemd/anas-hostd.socket`：root/root `0600`、`Accept=yes`、`MaxConnections=8`，每条连接激活
  一个短命的 `anas-hostd@` 实例。

所以 **anasd 承担 owner 不需要新增 root，它现在就是 root。** 真正要定的是：特权内核操作放在 anasd 进程内
做（B2），还是 anasd 只调度、仍由 hostd 执行（B1）。

## 3. 候选 A：独立最小能力 systemd 服务

形态：新增一个常驻单元，例如专用 UID 加 `AmbientCapabilities=CAP_NET_ADMIN`，或 root 加
`CapabilityBoundingSet` 收窄；进程内循环续期、清理连接。中介可以同进程，也可以另起。

利：

- 与控制台解耦：anasd 升级、重启或崩溃不打断续期，运行中 guest 的出网不受影响。
- 生命周期交给 systemd：开机启动、`Restart=`、`ExecStartPre` 做开机对账，形态直观。
- 进程内续期不产生 job 记录，没有高频 job 的存储问题。
- 能力集可以由单元声明收窄，攻击面比 root anasd 小——前提是真的能收窄（见下）。

弊：

- 与既定约束正面冲突：新增常驻特权进程和第三个特权入口，违反 `HOSTACT-R-002`、`R-005`，要先改宿主通道
  需求并走 `HOSTACT-R-012` 五问评审；也违背归档计划「不再垫一层守护进程」的方向。
- 「最小能力」难兑现：按 `INCUS-R-102`，每次续期都要读 Incus 实例事实。现在只有 root 持有的管理证书能做，
  设计上刻意不把它交给中介（宿主供给设计 §7.8）。要么把管理证书给这个服务，它就近似 Incus 全权；要么另造
  受限证书，而 §7.8 已明确不为此切换 daemon 全局授权。此外还要读写 `state.json` 回执与宿主状态锁，执行
  nft、ipset 和 conntrack。
- 真相源分裂：`forwarding_scopes` 回执现在只由 hostd 事务写入。第二个常驻写者必须共享同一把锁和事务语义，
  否则违反「不得成为第二个真相源」。
- 与 M11 现有装配方向相反：ingress 协调器、宿主队列、排空屏障、工作区围栏（宿主供给设计 §7.11—§7.14）都在
  anasd 进程内，屏障是内存结构。中介搬出去要重做跨进程协调；只搬续期则变成两个常驻 owner，停止和排空顺序
  要跨进程协调。
- 安装、升级、卸载和非 systemd 发行版都要多管一个常驻单元。

变体 A′：用 systemd timer 定时激活一次性动作，不常驻。它保住「做完即退」，也不依赖 anasd；但 10 秒级 timer
要调 `AccuracySec`，每次激活仍是一次有审计的特权执行，记录量问题与 B1 相同；它也持有不了中介需要的排空
屏障和会话状态。只能用于续期，不能用于 M11。

## 4. 候选 B：由 anasd 承担

两种做法差别很大，要分开定：

- **B1 anasd 调度、hostd 执行**：anasd 内的 owner 定时经现有共享队列调用一个编译的续期动作，hostd 按
  `INCUS-R-102` 核验、刷新元素后退出。
- **B2 anasd 进程内直接做**：anasd 用自己的 root 权限读管理证书、改 nft/ipset、清 conntrack。

两者共有的利：

- 符合归档计划「让 web 服务本身充当常驻进程」，不新增单元。
- M11 的 owner 骨架已经在 anasd：`ControllerCoordinator`、`HostActionService` 队列、`StartIngressWorkspace`、
  排空屏障和工作区围栏。转发续期可以作为同一 owner 的另一类周期工作接入，与部署任务的排空协调直接复用。
- 单一真相源：`state.json` 事务、宿主状态锁、审计、执行租约和退出监督都沿用。
- anasd 已是 root，不涉及提权。

B1 额外的利：

- 特权执行仍只在短命 hostd 里，`HOSTACT-R-001`/`R-002` 不变。将来 anasd 若改为非 root，B1 只需调整 socket
  属组。
- 与 M11 走同一条路：中介的观察已经是「anasd 经队列调 hostd 只读动作」。续期与观察共用一套基础设施，
  要解决的问题也是同一组。

B1 的弊，也就是恢复前必须先解决的事：

- 授权语义要新定。续期是变更动作，不能每次人工确认，需要一条新规则：已确认的 enable 批准派生出自动续期，
  只限同一 grant 摘要、同一目的地、实例数不超过 `MaxInstances`，每次仍按 `INCUS-R-102` 核验；范围一旦变化，
  必须回到人工确认。现有不经确认的先例只有只读观察，这属于宿主通道层面的新类别。
- 记录量。续期周期要明显短于 TTL，按 TTL/3 估约 10 秒，每个租约每天约 8,640 次 hostd 激活和 job（推断）。
  当前 ABI job 的事件不按保留期修剪，只由原子 action 记录修剪（`internal/consolejobs/store.go`）；终态保留与
  限额仍是 ACTABI §14 的待决项。需要一次激活续全部租约的批量动作，并先定这类 job 的合并与保留策略，例如只
  记录失败和状态变化。M11 的周期观察有同样的问题，应一起定。
- 可用性耦合。anasd 停止超过 TTL，所有租约的转发许可都会失效：这是 fail-closed，安全，但会打断运行中的作业。
  崩溃后 3 秒重启在 TTL 之内，升级维护可能超过。TTL 可以放宽到几分钟来换容忍窗口，代价是 owner 失联后许可
  多存活这么久；撤销仍由显式动作负责，TTL 只是失联兜底。
- 启动时延。新实例要等下一轮续期才有许可，one-job 首次连接 Forgejo 最多多等一个周期；需要 runner 侧重试，
  或在实例出现时触发一次即时续期。
- 开销。每次激活都要经过 systemd socket 激活、PID 1 身份核验与 broker 交接，比进程内循环重；
  `MaxConnections=8` 与其他宿主动作共用。

B2 的弊：

- 绕过特权通道：特权内核写入不经编译动作与逐次审计，违背 `HOSTACT-R-001`/`R-002` 的意图。控制台信任域和
  宿主特权执行进了同一个进程，正是 ACTABI §11 点名的最常见失手，也让控制台要求 §7.4 担心的隐式常驻提权成为
  事实。
- 技术上完全可行（root、无能力边界集、允许 `AF_NETLINK`、可写 `/var/lib/anas`），这正是风险：边界只剩代码
  约定。root anasd 本来就近似宿主管理员（Docker socket），但 B2 会把现在刻意在代码层隔开的两件事——中介
  不持有 Incus 管理证书、内核写入只在 hostd——合到一起，控制台 HTTP 面的漏洞会直接拿到这两样。

## 5. 候选 C：租约绑定许可加开机对账

形态：许可不再带 TTL。enable 时装上，disable、retire 或租约撤销时拆除；宿主重启后由一次性对账（systemd
oneshot，或 anasd 启动时调一次 hostd 动作）按回执重建。候选只有一句描述，许可绑到什么粒度决定它是否成立：

- **C1 逐实例、无 TTL**：仍按 ifindex、MAC、IPv4 绑定实例。one-job 实例按作业增减，新实例仍要有人及时加
  许可、停掉的实例仍要有人撤，常驻 owner 并没有省掉；停掉实例的元素还会滞留（Linux ifindex 递增分配、回绕前
  不复用，风险低但不为零，推断）。单独不成立。
- **C2 租约 bridge 级**：规则只写「来自本租约自有 bridge → 已批准目的 IPv4/TCP」，不逐实例。实例增减无需
  干预；来源真实性交给 Provider 侧围栏（profile 的 MAC/IPv4 过滤、租约自有 bridge 与 network ACL），跨租约
  隔离靠每租约独立 bridge。

C2 的利：

- 转发不再需要常驻进程：没有周期续期、没有高频 job，anasd 停机不影响运行中 guest 的出网。
- 状态只在显式事件时变化：enable、disable、retire（都是已有确认动作）、租约撤销、开机对账（一次性、做完即退，
  与 `HOSTACT-R-002` 一致）。
- 新实例立即可用，没有续期周期带来的启动时延；内核对象更少、更稳定，读回和对账更简单。

C2 的弊：

- 要改需求。`INCUS-R-102`「每次新增或续期都核验实例 UUID、运行代际、地址、MAC、物理接口」不再成立，改为
  依赖 Incus 侧来源过滤；而宿主供给设计 §1 明确写着 Provider 的 MAC/IPv4 过滤与 expanded NIC 检查「仍不能
  代替独立物理来源验证」。需要重新论证同租约 guest 共享一份许可是否可接受：同一消费者本就同权，但对身份替换
  的拒绝会变弱。
- 失去 TTL 的失联兜底：撤销完全依赖显式动作成功。撤销失败时残留许可持续放行；现在只有「残留阻止依赖拆除」
  兜底，流量不会自动关闭。
- 租约撤销要自动触发转发拆除：`INCUS-R-111` 的 Core 撤销路径现在不碰转发许可（`internal/runner` 中没有
  转发调用），需要接上，或让残留许可阻止撤销完成。
- `INCUS-R-103` 的连接撤销仍要做：租约撤销时精确清理 conntrack（现有 disable 已实现）。同 bridge 新实例
  复用旧地址时可能命中旧连接跟踪条目，但只在同一租约内。
- 返工：已完成并原生验收的 30 秒集合、Refresh、失败撤回和退役要部分重做，kernel receipt schema 会变，现有
  原生证据不能直接沿用。
- **不覆盖 M11**：ingress 请求由消费者在运行时写入，中介必须常驻。C 只能缩小常驻 owner 的职责，不能替代
  A 或 B。

## 6. 对比

| 维度 | A 独立服务 | B1 anasd 调度 + hostd 执行 | B2 anasd 进程内 | C2 bridge 级 + 开机对账 |
| --- | --- | --- | --- | --- |
| 新增常驻特权进程或入口 | 是 | 否 | 否，但 anasd 承担特权写入 | 否 |
| 与 `HOSTACT-R-002`/`R-005` | 冲突，需改需求 | 兼容 | 违背意图 | 兼容 |
| 与归档的守护进程结论 | 冲突 | 一致 | 一致 | 一致 |
| `INCUS-R-102` 逐次核验 | 保留 | 保留 | 保留 | 需修改 |
| owner 失联时的许可 | TTL 内失效 | TTL 内失效 | TTL 内失效 | 持续有效 |
| anasd 停机的影响 | 无 | 转发中断 | 转发中断 | 无 |
| 高频 job 与审计 | 无 | 需批量动作与保留策略 | 无 | 无 |
| 新授权规则 | 需要（服务自身的凭据） | 需要（批准派生续期） | 不需要（绕过了通道） | 不需要 |
| 覆盖 M11 中介 | 要搬出中介或双 owner | 是，与现有装配一致 | 是 | 否，仍需 A 或 B |
| 主要返工 | 新单元、凭据、锁协议、跨进程排空 | 续期动作、授权规则、job 保留 | 最少 | 需求、内核实现与原生验收 |

## 7. 建议（分析意见，待操作者定案）

1. M11 的中介无论如何都要常驻，现有代码已经把 owner 放在 anasd。按既定约束，owner 定为 **anasd（B1）**：
   anasd 负责调度、排空和对账，特权执行仍只在 hostd。
2. 不采用 B2。A 只在「anasd 停机期间转发必须保持」成为硬需求时再考虑，并先修改宿主通道需求。
3. B1 恢复前先定三件事，M10a 与 M11 共用：
   - 批准派生的自动续期授权规则（宿主通道需求新增一条）；
   - 续期、观察这类高频 job 的批量执行与保留策略（ACTABI §14）；
   - TTL 与续期周期相对 anasd 重启、升级窗口的取值，以及新实例的即时续期触发。
4. C2 作为 B1 的备选：如果第 3 条的授权规则或记录量不可接受，再以修改 `INCUS-R-102` 为代价去掉续期。
   它不改变 M11 需要 anasd owner 的结论。
5. 无论选哪个候选，都还要先定 §8.2 的两件事：续期改为增量语义；部署、重启、升级之后由谁、凭什么授权
   以新 grant 重新打开转发。需求草案见[重新授权与增量续期](2026-09-28-incus-forwarding-regrant-renewal-draft.md)。

## 8. 补充核对：现有 Refresh 与续期 job 的冲突面

同日追加，逐项对照 `internal/incusprovision/forwarding_permissions*.go`、`internal/incusingresshost/forwarding_*.go`、
`internal/jobexecutor/workspace_ingress_change.go` 与 `internal/hostaction/registry.go`。

### 8.1 现有 Refresh 做什么

Refresh 现在只在 enable 的 apply 里调用一次，没有独立的续期动作。一次调用依次：

1. 取宿主状态锁（apply 为排他，plan 为共享），打开租约会话：取工作区 `.anas/state/lock` 共享锁，读活动部署、
   manifest、Provider/消费者 `.env` 与连接 bundle，经固定 mTLS 读 Incus 版本和租约网络，从内核读 bridge 身份，
   逐个目的地读路由；
2. 逐实例读 Incus 事实（UUID、运行代际、IPv4、MAC）与本机 veth（名称、MAC、ifindex、对端 ifindex），
   再读一次比对，最后检查身份唯一；
3. 实例集合有变化或许可已关闭时，先 Close：清空带期限集合与 ipset，并精确清理当前全部已知实例的连接；
4. 刷新 ipset，再以一个 nft 事务 flush 并重新加入元素（30 秒 timeout），读回核验后保存回执的
   `Instances`、`LastRefresh`。每个外部效果之前都先持久化 pending。

元素过期后的实际效果：`flows` 与 ipset 过期后，新建和既有 TCP 都落到 `deny-original`/Docker DROP；
`sources` 等过期后，已知 guest 的 MAC/IP 仍在静态 `macs`/`guests` 拒绝集合里，它在租约 bridge 上的 IP/ARP
帧会被整体丢弃，连网关的 DNS/DHCP 也不通。conntrack 条目不会因过期消失，所以撤销仍要显式 Close。

### 8.2 直接拿来做续期的两个缺口

- **整体替换语义**：one-job 实例启动或结束都会改变实例集合，Refresh 随之 Close 并撤销当前全部实例的连接，
  同一租约里正在运行的其他作业会被断连。续期需要增量语义：不变的实例只重置 TTL，新实例追加，离开的实例
  单独撤销。
- **grant 绑定部署与宿主身份**：grant 含 `WorkspaceDigest`、`Epoch`（工作区摘要、部署、激活时间与
  manifest 摘要的哈希）、bundle 摘要、Incus 版本、bridge 身份（含 boot ID 与 ifindex）和路由 ifindex。
  已有 scope 的 enable 要求新 grant 与记录完全相同，否则 `ErrDrift`；`failed` 或 `released` 的 scope 不能
  再 enable。任何一次新部署激活、宿主重启、Incus 升级、租约证书轮换或路由变化之后，续期都会 drift，
  而现在没有「同一 scope 换新 grant」的路径。

另外，anasd 在领取任何工作区写任务（部署、凭据轮换、Module 变更、快照维护）之前，会先排空中介，再以
`incus.forwarding.withdraw`（不需确认、只关不开）关闭该工作区的全部转发许可。部署之后由谁、凭什么授权
重新打开，是所有候选都要回答的问题，与 owner 选型正交；C2 同样需要。

### 8.3 B1 下续期 job 的冲突面

| 冲突对象 | 现有机制 | 需要定的规则 |
| --- | --- | --- |
| 同一 scope 的上一次续期 | `incus.forwarding.permission` 的并发策略为 `reject` | 调度器单飞，或续期动作改用 `coalesce` |
| 其他租约的续期 | apply 持宿主状态排他锁，全部租约串行，单次上限 60 秒 | 一次激活续全部租约；慢续期会把其他租约推近 30 秒 TTL |
| 人工 enable/disable/retire | 同一把排他锁，按到达顺序串行 | 续期带 generation 与状态栅栏：只续 `enabled` 且 generation 未变的 scope，disable 之后排队的续期不得重新打开 |
| Incus 供给与维护（configure/enroll/uninstall/prune/观察配置） | 都持宿主状态排他锁 | 执行期间无法续期、许可会失效：先显式撤回，或接受中断 |
| 工作区写任务 | 领取前先撤回；续期持工作区共享锁，与写任务的排他锁互斥 | 续期最长 60 秒会推迟写任务；写完 epoch 变化后续期 drift，需走 §8.2 的重新授权 |
| M11 观察 `observe_http` | 持宿主状态共享锁，续期要排他锁 | 两类高频 job 互相等待，周期和批量要一起设计 |
| 队列容量 | 默认排队 1024、同时运行 32，hostd `MaxConnections=8`，与用户动作共用 | 给续期设上限或独立配额，避免挤占用户动作 |

## 9. 补充：残留规则的实际影响与事件驱动方案（2026-09-28）

### 9.1 实例退出后不删规则，会怎样

对照 `internal/incusingresshost/forwarding_nft.go`：

- bridge 层的 `sources` 按（宿主 veth ifindex、MAC、IP）放行。实例退出后它的 veth 消失，残留元素不会再被
  任何帧命中。ifindex 按递增方式分配、用尽才回绕，这一点是推断；规则同时钉住接口名和编号，就是为了防复用。
- 其他 guest 冒用残留的 IP 或 MAC，会被静态 `guests`/`macs` 拒绝集合丢弃，Provider profile 的 MAC/IPv4
  过滤也会拦下。
- 新实例复用了旧 IP：它的帧被静态拒绝集合整体丢弃，连网关都不通，直到 owner 为它加上许可。后果是新实例
  不可用，不是越权。
- 其余影响：残留元素占用集合容量（每个集合上限 8192；每个实例约 5 + 2×目的地数个 nft 元素，外加 ipset
  成员）；回执与实际不一致时会挡住 retire 和宿主卸载；conntrack 条目要等内核超时。只要对账在运行，残留
  就不会累积。

结论：实例退出后留下的规则在安全上影响很小；IP 长期不复用时，只剩容量和状态整洁问题。宿主供给设计
§5.1.7 本来也把「受管分配保留」（许可清理之前 IP 不复用）列为短期到期之外的另一种做法。

### 9.2 期限真正兜底的情况

真正需要兜底的，是**实例还在运行、授权却已撤销**：租约撤销（`INCUS-R-111` 会保留实例）、目的地被移除、
操作者 disable。这时残留许可仍能被这个合法实例使用。三种来源的覆盖情况：

- anasd 执行的工作区写任务，在领取前已经撤回；
- CLI `anas apply` 不经过撤回：runner 里没有转发调用，CLI 也不能直接调用 hostd。这是缺口；
- 在 Incus 上手工撤证书或删 project，完全不经过 ANAS。

### 9.3 候选 D：事件驱动 + 启动对账 + 失败重试（许可不设期限）

形态：逐实例许可不设期限，anasd 作为 owner：

1. ANAS 自己发起的变化同步撤销：anasd 写任务在领取前撤回（已有）；CLI apply 要移除仍有转发记录的租约时，
   要么经 anasd 撤回，要么直接拒绝执行。
2. 监听 Incus 生命周期事件（实例启动、停止、删除，证书删除等）：实例启动时核验并加许可，停止或删除时撤销。
3. anasd 启动时、事件连接重连后先做一次全量对账；另外以较低频率（例如几分钟一次）周期对账，兜住漏掉的
   事件和在 Incus 上的手工改动。
4. hostd 失败：记进回执（现在已有 pending/failed），按退避重试，每次重新观察；持续失败在控制台告警，交给
   操作者处理。

利：

- 没有高频 job；
- anasd 停机不影响运行中 guest 的出网；
- 新实例一启动就拿到许可；
- 增量处理是自然结果，不需要改造 Refresh 的整体替换语义。

弊：

- 在 anasd 停机期间撤销的授权，要等 anasd 恢复后才生效（失败开放）。最坏情况是：已撤销租约的实例在这段
  时间里仍能访问已批准的目的地。
- 监听事件需要一个能读取各租约 project 事件的 Incus 凭据。现在只有 root 持有的管理证书能做到，而设计上
  刻意没有把它交给 anasd 里的中介（§7.8）。要么让 anasd 持有管理证书，要么另开受限证书，这是一个新的
  权限决定。
- 需求要改：`INCUS-R-104` 中的「许可期限」要改写成不设期限时的残留与退役规则；`INCUS-R-103` 要写明撤销
  时效依赖事件和对账。

D 与 C2 的区别：D 仍然逐实例核验（保留 `INCUS-R-102`），只是不再靠期限让许可失效。

修正后的建议：如果操作者接受「anasd 停机期间，已撤销的授权延后生效」，D 比 B1 成本更低、可用性更好；
不接受则维持 B1。无论选哪个，§8.2 的重新授权规则都需要。

## 10. 补充：租约级静态出站策略与宿主端口（2026-09-28）

操作者提问：如果让实例只经宿主访问网络、不能访问宿主本机端口，能否去掉出站策略？

- **许可是开口，不是额外限制。** 默认 Docker 下 FORWARD 的默认策略是 DROP，许可是在这堵墙上开的口子，不是额外加的限制。
  去掉许可后，guest 除了宿主哪里都到不了。所以总要保留一条放行，要决定的只是放行的粒度。
- **只挡宿主本机端口不够。**
  - Docker 发布到宿主 IP 上的端口，报文先被 DNAT 改写再走转发路径，Docker 会放行来自任意接口的这类转发，
    所以拦截本机端口（INPUT）拦不住它们。
  - 局域网设备、其他租约与 Docker 网络、云主机的元数据地址、公网滥用，都不属于「本机端口」。
  - Forgejo 本身就在宿主上，经 Traefik 对外，需要单独放行。
- **候选 E（C2 的具体化）：出站策略改成租约级的静态规则。**
  - 目的地策略写进每个租约 bridge 已经挂着的 Incus network ACL（M9a 的来源围栏）。它在 input 和 forward 两个
    钩子都生效，Provider 已有 `inspect`/`ensure` 漂移修复。规则：拒绝宿主全部地址（DNS/DHCP 由内建规则放行），
    拒绝私有、链路本地和元数据地址；放行 Forgejo 入口，以及公网或一份明确的目的地清单。
  - Docker 一侧按租约 bridge 加一条静态放行：租约创建时加，退役时删。
  - 租约撤销时，由 Provider 在 `INCUS-R-111` 路径里把 ACL 改为全部拒绝。这一步只用 Incus API，CLI 与 anasd
    两条路径都覆盖到。
  - 结果：不逐实例、不设期限、不需要续期 owner，实例增减也不用处理；常驻 owner 只剩 M11 入站中介。
  - 代价：放弃逐实例核验（`INCUS-R-102`），同一租约的所有 guest 共用一份策略；`INCUS-R-101`—`R-104` 需要改写
    或废弃，Forgejo 的出站白名单要求随策略调整；已经验收的逐实例实现大部分作废。
- **现状发现（按配置推断，未实测）。** 租约 bridge 的 ACL 只按来源子网放行，不限制目的地，ANAS 也没有加
  拦截宿主的规则。因此 guest 现在能访问宿主本机端口；没开转发许可时，还能访问 Docker 发布到宿主 IP 的端口。
  无论最终选哪种出站方案，都应该补上这一条。

2026-09-28 后续：操作者接受按租约放行，并定出四档出站分级（默认可访问互联网）。出站方向改为租约级静态策略，
见[出站分级草案](2026-09-28-compute-egress-tiers-draft.md)；运行 owner 问题只剩 M11，见
[设计简化评审](2026-09-28-incus-design-simplification-review.md)。

2026-09-28 定案：出站采用租约级静态分级（候选 E 的具体化），写入 `INCUS-R-112`—`R-129`，M10a 已重排；出站方向
不再需要运行 owner。M11 入站另行讨论。
