---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-13
---

# 统一动作 ABI 实施计划

验收依据是[统一动作 ABI 要求](../requirements/action-abi.md)；设计见
[同名架构文档](../../docs/architecture/action-abi.md)。

**当前状态：全部未开始。** 它是[宿主特权动作通道](host-action-channel.md)的前置——通道复用本 ABI
的线格式与 job 语义。同时它取代 [Module 专属命令能力](module-command-capability.md)已实现的
executor 协议与取消语义，因此落地时要一并处理那部分既有实现。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：job 模型、事件日志与重放 | R-001、R-002 | 未开始 |
| M1：取消语义与并发合流 | R-003、R-007、R-009 | 未开始 |
| M2：大块数据边界 | R-004—R-006 | 未开始 |
| M3：job 可见性 | R-008 | 未开始 |

覆盖统计：9 项需求全部有且只有一个里程碑归属。

## 2. 与既有实现的关系

`anas.module-command/v1` 的 M1/M2 已实现（约 2100 行）。本 ABI 取代其 executor 协议与取消语义，
但**不取代**其 manifest 声明模型、descriptor 冻结、锁冲突表与发现路径。迁移前必须先在
[Module 专属命令能力要求](../requirements/module-command-capability.md)中标出哪几条被取代，
避免两份文档各自宣称有效。

## 3. CI 门禁

全部里程碑未开始，没有实施提交，`go test ./...` 等代码门禁无从记录；从 M0 的首个实施提交起逐项登记。

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
| [统一动作 ABI 设计](../../docs/architecture/action-abi.md)与[架构索引](../../docs/architecture/index.md) | 状态「设计，未实现」随里程碑落地改写 | 未开始 |
| [英文架构索引](../../docs/en/architecture/index.md) | 按文档标准 §2 至少补英文摘要并链接中文原文；目前没有本设计的条目 | 未开始 |
| [Module 专属命令参考](../../docs/reference/module-commands.md)与英文镜像 | 目前描述 `anas.module-command/v1` 的现行实现；M0/M1 落地后改写为 job、事件重放与取消语义 | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | `module_command_abi` 取值与 `module_command_*` 错误语义随 ABI 更新 | 未开始 |
| [日志与可观测性](../../docs/architecture/observability-and-logs.md) | 「job 事件日志」一行标注未实现，M0 落地后更新；下文待决的持久化位置与保留期在该文档定案 | 未开始 |

## 6. 待决

- 事件日志的持久化位置与保留期，见[日志与可观测性](../../docs/architecture/observability-and-logs.md)。
