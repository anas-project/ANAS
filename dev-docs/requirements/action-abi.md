---
doc_type: requirement
status: current
created: 2026-09-04
updated: 2026-09-04
---

# 统一动作 ABI 要求

本文规定 `anas.action/v1` 必须交付的结果。设计见[统一动作 ABI](../../docs/architecture/action-abi.md)。
同一套协议服务两类执行者：部署内的 **Module Command**（无特权）与宿主上的
**[特权动作](host-action-channel.md)**（root）；协议共用，注册表与授权不共用。

它取代 [Module 专属命令能力](module-command-capability.md)中 executor 协议与取消语义那一部分。
那套 ABI 尚未发布，因此不做兼容层。

这些要求最初写在 [Incus Module 要求](incus-module.md)里（`INCUS-R-075` 等），但它们与 Incus 无关，
因此迁到本文并重新编号。

关键词“必须”“不得”“应该”具有规范性。ID 一经分配即固定，废弃的需求保留行并标 `已废弃`。

## 1. 为什么重新设计

原 ABI 是「一次调用 = 一次前台执行」：调用方连着、进程跑着、断了就没了。三条需求让这个模型
不成立——Web 端发起的任务必须留在服务端跑完、`btrfs send` 会输出与 JSONL 互斥的大块数据、
破坏性动作需要中间隔着用户确认的两段。三条都在说同一件事：**执行的生命周期不该绑在连接的
生命周期上。**

## 2. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `ACTABI-R-001` | Web 端发起的任务必须留在服务端执行至结束：调用方断连不得中止执行，只有显式 `cancel` 才停止；重新打开控制台必须能看到进行中的 job 及其状态 | 契约 + e2e |
| `ACTABI-R-002` | 每次调用都必须创建 job 并写入带序号的追加事件日志，支持从任意位置重放；日志超限必须写入显式 `truncated` 事件而不是静默丢弃 | 单元 |
| `ACTABI-R-003` | 取消为协作式；杀进程组后 job 必须标记 `outcome: unknown` 而不是 `cancelled`——动作未确认收敛时不得把未知报成已取消 | 单元 |
| `ACTABI-R-004` | 大块数据不得复用 JSONL 控制流：目的地由动作自己校验并打开；需要经浏览器取回时走一次性 token 的独立下载端点，不得内联 base64 | 契约 + 审阅 |
| `ACTABI-R-005` | `btrfs send` 的已写字节必须精确；总量估算只在能廉价获得时进行（增量用 `find-new`、已开 quota 用 referenced），不得为估算阻塞传输；估算值放独立字段，实际超出时不得钳制 | 单元 |
| `ACTABI-R-006` | `btrfs send` 取消必须清理不完整目的文件，并在确认时明确告知不支持断点续传 | 单元 + e2e |
| `ACTABI-R-007` | 重复触发必须按「是否已有参数相同的同名动作在跑」判定，动作声明 `coalesce`/`reject`/`queue`；不得使用时间窗防抖 | 单元 |
| `ACTABI-R-008` | job 存储必须单一、不按入口分区：控制台能看到 CLI 发起的 job，反之亦然；`list` 只按查看者权限过滤，不按创建者过滤 | 契约 + e2e |
| `ACTABI-R-009` | `idempotency_key` 在 job 到达终态后保留 1 小时，之后同一 key 视为新请求 | 单元 |
