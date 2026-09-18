# 评审与历史快照

本目录保存针对明确日期、版本或提交基线的审查、库存和实施评估。文件名前缀日期表示评审基线，不是文档最后修改时间；后续只能补充解决状态或勘误，新的独立评审应创建新文件。

| 文档 | 基线 | 类型 |
| --- | --- | --- |
| [Incus 固定控制转发接续核对](2026-09-18-incus-control-relay-implementation.md) | 2026-09-18，`f7642c5` 加并行修改的未提交工作树 | 非 root 控制转发、执行租约保护与未验收边界；不是评审批准 |
| [Incus 本地镜像归档接续核对](2026-09-18-incus-artifact-archive-implementation.md) | 2026-09-18，未提交工作树 | 本地镜像归档、不可变 revision、恢复边界与未验收项；不是发布批准 |
| [Incus 发行版 daemon 供给探查](2026-09-11-incus-daemon-probe.md) | 2026-09-11 未提交工作树 / Incus 6.0.5 | 存储配额误报、修复边界与未通过项 |
| [Incus HTTP namespace 实验](2026-09-11-incus-http-netns-validation.md) | 2026-09-11 未提交工作树 / Ubuntu 26.04 | 真实 nft/HTTP 数据面检查与 Incus 验收边界 |
| [Incus 网络与 proxy 权限核验](2026-09-10-incus-network-proxy-validation.md) | 2026-09-10，ANAS `f7642c5` / Incus `v7.3.0` | 固定版本源码调用链、兼容性缺陷与实机验证边界 |
| [凭据轮换机制审查：全面轮换与单独轮换](2026-09-04-credential-rotation-review.md) | 2026-09-04 工作树，HEAD `5306b63` | 轮换覆盖面、作用域语义与 resource 凭据空白 |
| [GPT‑6 / GPT‑5.6 混用配置与项目质量评估](2026-09-05-agent-configuration-and-project-quality-review.md) | 2026-09-05 工作树，HEAD `455770b` | Agent 配置、文档、架构与代码质量审查 |
| [AI Agent 编排设计审查](../../modules/ai_agent/dev-docs/reviews/2026-09-05-orchestration-design-review.md) | 2026-09-05 工作树，HEAD `455770b` | 编排设计、凭据边界与事件状态审查（随组件迁至 `modules/ai_agent/`） |
| [ANAS 综合项目审计与整改状态（2026-09-03）](2026-09-03-comprehensive-project-audit.md) | 2026-09-03 工作树复核 | 综合审计、整改状态与未解决发现 |
| [ANAS 综合项目审计与整改状态（2026-08-23）](2026-08-23-comprehensive-project-audit.md) | 2026-08-23 工作树复核 | 综合审计、整改状态与未解决发现 |
| [Vikunja Module 设计规范符合性审查](2026-08-21-vikunja-module-design-compliance.md) | 2026-08-21 工作树，Vikunja `2.4.0-r1` | Module 规范与发布门禁审查 |
| [Module 分类与访问边界分析](2026-08-19-module-classification.md) | 2026-08-19 工作树 | 分类和实现差距审查 |
| [Module 官方镜像切换与版本升级评估](2026-07-29-module-image-upgrade.md) | 2026-07-29，后续实施更新至 2026-08-02 | 升级评估与实施记录 |
| [ANAS 设计问题审查报告](2026-07-19-design-review.md) | 2026-07-19 代码基线 | 代码与架构审查 |
