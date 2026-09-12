# AI Agent 编排设计

> 状态：**已迁移**。正文不再维护在本页。`anas-agent` 将来要拆成独立项目，因此它的设计、调研、
> 需求、计划与评审全部随组件放在 `modules/ai_agent/` 下，拆分时整体带走。更新：2026-09-10。

设计正文见
[AI Agent 编排设计（Forgejo 基线）](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/architecture/orchestration-design.md)。

组件文档一览：

| 文档 | 内容 |
| --- | --- |
| [编排设计](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/architecture/orchestration-design.md) | 交互模型、身份与凭据、架构、权限、安全边界、演进路线 |
| [编排器与 Forgejo 互操作的规则](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/forgejo-interop.md) | 改 orchestrator 代码前要遵守的边界与约定 |
| [看板应用接入 AI Agent](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/research/kanban-integration.md) | 候选运行时与看板集成的原始调研 |
| [要求](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md) | 需求矩阵与固定 `forgejo 15.0.7` 的上游事实复核 |
| [实施计划](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/plans/ai-agent.md) | 里程碑、检查表与 e2e 记录 |
| [设计评审](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/reviews/2026-09-05-orchestration-design-review.md) | 2026-09-05 基线的评审快照 |

站内相关页面：[Forgejo Module 设计](forgejo-module-design.md)、
[与 Forgejo 互操作基线](/developer/forgejo-interop)、
[Module 用户与技术文档](/reference/modules/ai_agent/)。
