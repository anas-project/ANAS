---
doc_type: requirement
status: current
created: 2026-09-08
updated: 2026-09-08
---

# 项目审查整改要求

来源为[9 月 5 日审查](../reviews/2026-09-05-agent-configuration-and-project-quality-review.md)。
本主题承载跨项目维护规则与新增门禁；Compose 端点/错误协议沿用 COMPOSE-R，应用层迁移沿用 ALM-R，
共享镜像构建沿用 INCUS-R-038，不复制其验收矩阵。实施见[计划](../plans/archived/project-quality-hardening.md)。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `QUALITY-R-001` | 两代模型共用项目规则，明确执行/提问/评审边界、提交范围、审查文档归属和验证停止条件 | 审阅 |
| `QUALITY-R-002` | 计划索引校验章节、实际链接路径和归档状态，拒绝活跃计划误入归档区及错误路径 | 单元 |
| `QUALITY-R-003` | daemon 恢复失败或事务不可读时不得确认补偿完成；保留阻塞标记并记录不含原始错误文本的结果事件 | 单元 |
| `QUALITY-R-004` | PR/master CI 执行前端类型检查、测试、构建和不修改源文件的 OpenAPI 生成一致性检查 | CI |
| `QUALITY-R-005` | 文档概述与里程碑一致，历史审查通过追加状态更新；只在全部验收完成时归档计划 | 审阅 |
| `QUALITY-R-006` | lego Hook 与 deploymentaudit 的关键边界有直接行为测试；本地构建产物不得误入 Git | 单元 |

验证不以真实宿主条件不足推导为通过，也不把这份审查整改扩展为 AI Agent 产品提案的全量实施。
