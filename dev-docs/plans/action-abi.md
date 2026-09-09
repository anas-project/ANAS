---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-04
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

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | 待记录 |
| `npm run docs:check-requirements` | 待记录 |

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | 断连后任务继续、重连可见 | — | 待执行 |
| R-006 | 待新增 `test-env/scripts/server-backup-send-e2e.sh` | 取消后不完整目的文件被清理 | — | 待执行 |
| R-008 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | CLI 发起的 job 在控制台可见 | — | 待执行 |

## 5. 待决

- 事件日志的持久化位置与保留期，见[日志与可观测性](../../docs/architecture/observability-and-logs.md)。
