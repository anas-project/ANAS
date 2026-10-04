---
doc_type: plan
status: implementing
created: 2026-09-29
updated: 2026-10-03
---

# 运行问题记录实施计划

验收依据是[运行问题记录要求](../requirements/runtime-issues.md)的需求矩阵。本文只回答先做什么。

**当前状态：实施中。** 2026-10-03 随 Incus 端口绑定（[Incus 计划](incus-module.md) M11c）交付记录包
`internal/runtimeissues`、三个写入方（apply、anasd、hostd 与开机恢复）和 CLI `anas issues`；控制台查看与 e2e
待办，见[实施记录](../reviews/2026-10-03-incus-lease-network-implementation.md)。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：记录格式、去重与写入 | R-001—R-005、R-008、R-009 | 实施中；格式、按键去重、解决与保留、敏感值拦截、文件锁原子写入、开启/解决各写一条日志与读取失败报错均已实现并有单元测试；已解决记录的保留期暂定 30 天，待 §2 定案 |
| M1：CLI 与控制台查看 | R-006、R-007 | 实施中；`anas issues [--json]` 已实现（直接读两份记录、未解决错误或读取失败时退出码 1），契约见命令 JSON 契约；e2e 与控制台视图待办，控制台合并展示方式待 §2 定案 |

覆盖统计：9 项需求全部有且只有一个里程碑归属。

## 2. 待定

以下三项留给单独的对话决定（2026-09-30 操作者要求）：

- 已解决记录的保留期，和[日志与可观测性](../../docs/architecture/observability-and-logs.md)的保留期讨论一起定；
- CLI 命令名已定为 `anas issues`（2026-09-30）；JSON 契约写进[命令 JSON 契约](../../docs/reference/contracts/commands.md)。理由：与
  `anas deployments` 一样用复数名词表示列表；`anas doctor` 暗示主动检查，`anas errors` 容纳不了警告；
- 工作区与宿主两处记录在控制台里怎么合并展示。

## 3. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-006 | 待新增 | anasd 停止时 CLI 查看、`--json` 与退出码 | — | 已实现，单元测试通过（`internal/runner/issues_cli_test.go`）；e2e 未运行 |

## 4. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [日志与可观测性](../../docs/architecture/observability-and-logs.md) | 在「几种日志」旁列出运行问题，说明它是当前状态而不是日志 | 已完成（2026-09-29） |
| [命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | 查看命令的输出契约 | 已完成（2026-10-03） |
