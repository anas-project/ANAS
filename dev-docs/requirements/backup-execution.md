---
doc_type: requirement
status: current
created: 2026-09-28
updated: 2026-10-04
---

# 备份与恢复统一执行要求

本文规定：备份、恢复与校验在 CLI 和控制台之间只有一个执行入口、一份记录和一个存放位置，以及这些操作之间的
排他关系。[§5 需求矩阵](#5-需求矩阵规范来源)是规范来源，其余章节是解释。

配套计划见[备份与恢复统一执行实施计划](../plans/backup-execution.md)。

## 1. 缘由

2026-09-28 对照代码核实的现状：

| 方面 | 现状 |
| --- | --- |
| 执行位置 | `backup create`、`backup restore`、`backup verify` 只在 CLI 进程内执行。控制台对它们只生成终端动作说明并记审计（`internal/runner/maintenance_application.go`），看不到执行过程和结果；CLI 执行也不进入 job 存储 |
| 存放位置 | CLI 以 `--to`/`--from` 接受任意路径；控制台只认 `/etc/anas/anasd.yml` 中由 root 登记的 `backup_targets`。写到未登记路径的备份，控制台列不出来 |
| 同一工作区 | create 与 restore 全程持有工作区运行时锁 `.anas/state/lock`。CLI 与 anasd 共用这把锁，所以已经与部署、快照维护和另一次备份互斥 |
| 重复发起 | 第二个请求会等锁释放后再执行一遍，而不会得知已经有同样的操作在跑 |
| 同一目的地 | 不加锁，而备份契约说明目的地常被多台主机共用。每次 create 开始时，`cleanStaleBackupTemp` 会删除目的地上全部 `.tmp-*` 目录，不检查是否有另一次备份正在写入，因此会让并发的备份失败（读代码发现；2026-10-04 已按 `BACKUP-R-011` 改为归属记录与证明式清理，见[备份契约](../../docs/reference/contracts/backup.md)） |

已有的相关结论：

- [统一动作 ABI](../../docs/architecture/action-abi.md) §12：job 存储只有一份。管理员在终端发起的备份，
  控制台必须看得到，否则会被重复触发。
- [宿主特权动作通道](../../docs/architecture/host-action-channel.md) §3.1 与
  [特权操作草案](../../docs/architecture/privilege-helper-draft.md)：btrfs 和备份的特权操作归 `anas-hostd`。
- [workspace 与备份体系（已归档）](../plans/archived/workspace-backup.md)：守护进程不在时，CLI 仍然必须能
  独立工作。
- [备份契约](../../docs/reference/contracts/backup.md)：目的地不设索引文件，每份备份自己的 `backup.yml` 是
  唯一权威。

## 2. 决定

1. **一个入口、一份记录。** 备份、恢复、校验都做成 job，进入统一的 job 存储。anasd 运行时，CLI 把请求提交到
   同一个队列，两端看到的是同一个 job、同一份进度和结果。重复发起的请求合流到正在执行的那个，不排队再跑一遍。
2. **执行者是 hostd。** btrfs send/receive、删除子卷、恢复文件属主都需要 root，作为 `anas-hostd` 的编译动作
   执行；恢复走 plan 加一次性确认。
3. **保留离线路径。** 恢复往往发生在 anasd 不存在或起不来的时候，比如新主机、workspace 丢失。所以「单一来源」
   指的是磁盘上的锁和记录，而不是「必须经过 anasd」：anasd 不在时，CLI 直接执行，但要持有同一把执行租约，
   并把 job 记进同一份存储；anasd 起来后看到的是真实终态。
4. **三层排他。**
   - 工作区：沿用现有的运行时锁。
   - 目的地：用归属记录判断临时目录是否已经废弃，这是跨主机唯一能协调的层面。
   - 宿主：同一台宿主上的写操作一次只执行一个。
5. **一个位置。** CLI 默认按登记 ID 选择目的地，与控制台使用同一份登记；任意路径只作为显式选择的临时或灾备
   选项。
6. **与写任务同等对待。** 经 anasd 执行的备份和恢复，按工作区写任务处理，领取前与部署一样先做协调。

## 3. 否决的方案

| 方案 | 否决理由 |
| --- | --- |
| 恢复也必须经过 anasd | 恢复常发生在 anasd 不存在或起不来的时候 |
| 只靠工作区锁 | 不能跨工作区、跨主机协调同一个目的地；重复请求不合流；另一端也看不到 |
| 在目的地放共享锁文件或索引 | 网络文件系统上 flock 不可靠；索引会成为第二个没人能保证时效的真相源，备份契约已经否决过 |
| 同一宿主上的备份与恢复并发执行 | 磁盘 I/O 互相争用，行为也更难推理；NAS 规模下串行的代价很小 |

## 4. 范围

- **覆盖**：backup create、restore、verify 的入口、记录、执行者、排他关系与目的地选择。
- **不覆盖**：
  - snapshot restore/delete：它们同样只能在终端执行，之后按本文的模型接入；
  - 经浏览器下载备份：统一动作 ABI §13 明确不提供；
  - 定时备份的调度。

## 5. 需求矩阵（规范来源）

| ID | 要求 | 验证 |
| --- | --- | --- |
| `BACKUP-R-001` | 无论从 CLI 还是控制台发起，每次 backup create、restore、verify 都必须在统一 job 存储中留下一条 job 记录；两端用同一个 job ID 查到相同的状态、进度与结果 | 契约 + e2e |
| `BACKUP-R-002` | anasd 运行时，CLI 发起的 backup create、restore、verify 必须提交到 anasd 的同一队列执行，不得在 CLI 进程内另行执行 | 单元 + e2e |
| `BACKUP-R-003` | 同一工作区已有备份或恢复 job 在排队或执行时，参数相同的新请求必须合流到该 job；参数不同的新请求必须被拒绝，并返回正在执行的 job ID | 单元 |
| `BACKUP-R-004` | anasd 未运行时，CLI 必须仍能完成 backup create、restore 与 verify | e2e |
| `BACKUP-R-005` | CLI 离线执行的操作必须记入同一 job 存储，anasd 启动后能查到该 job 的真实终态 | 单元 |
| `BACKUP-R-006` | CLI 离线执行期间 anasd 启动时，不得把该 job 标为中断、重复执行或接管它的执行 | 单元 |
| `BACKUP-R-007` | anasd 运行时，btrfs send/receive、子卷删除与恢复属主这些需要 root 的步骤必须作为 `anas-hostd` 的编译动作执行，不得在 anasd 进程内执行 | 审阅 + 单元 |
| `BACKUP-R-008` | 经 anasd 执行的 backup restore 必须先 plan，再凭绑定该计划摘要的一次性确认执行；计划之后所选备份或目标工作区发生变化时，必须拒绝执行 | 单元 + e2e |
| `BACKUP-R-009` | backup create 与 restore 必须在整个操作期间独占目标工作区的运行时锁 | 单元 |
| `BACKUP-R-010` | 同一宿主上同一时刻最多执行一个 backup create 或 restore，其余按提交顺序排队；backup verify 与备份列表可以与之并发 | 单元 |
| `BACKUP-R-011` | 任何主机、任何工作区的备份、恢复或清理，都不得删除或改动另一次仍在进行的备份在目的地的临时目录；清理只能删除由归属记录证明已经废弃的临时目录 | 单元 + e2e |
| `BACKUP-R-012` | 目的地位于不支持可靠 flock 的共享或网络文件系统时，`BACKUP-R-011` 仍必须成立 | e2e |
| `BACKUP-R-013` | CLI 必须能以登记 ID 选择目的地，同一个 ID 在 CLI 与控制台解析出的路径必须相同 | 单元 |
| `BACKUP-R-014` | CLI 使用未登记路径时必须显式选择这种方式，不得作为默认；job 记录必须标明该目的地未登记 | 契约 |
| `BACKUP-R-015` | 控制台列出某个登记目的地的备份时，必须包含经 CLI 写入该目的地的备份 | e2e |
| `BACKUP-R-016` | 经 anasd 执行的 backup create 与 restore 必须按工作区写任务处理，领取前经过与部署相同的协调 | 单元 |
