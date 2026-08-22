# 开发者指南

开发 ANAS 时先确认修改属于哪一层：

- Runner：Go CLI、解析、规划、状态和生命周期；
- Module：独立发布的服务单元及其 Compose、`module.yml` 和 Hook；
- Contract：跨 Module 的稳定协议；
- Resource：由 Provider 管理的持久资源。

建议阅读顺序：

1. [仓库结构](repository-layout.md)
2. [Module 开发](module-development.md)
3. [Capability 开发标准](capability-development.md)
4. [Module 设计与发布检查表](module-design-checklist.md)
5. [Module 上游升级 SOP](module-upgrade-sop.md)
6. [Module 升级检查表](module-upgrade-checklist.md)
7. [测试](testing.md)
8. [容器镜像发布](release.md)
9. [文档写作标准](documentation-standard.md)
10. [需求编写规范](requirement-authoring.md)
11. [应用研究文档规范](research-document-standard.md)
12. [中国大陆构建、镜像与 CNB 发行](china-mainland-build-and-distribution.md)
13. [Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)
14. [CLI JSON 契约](/reference/contracts/)

旧的 [AI Design Guide](/architecture/ai-design)仍包含大量实现入口，但新增设计应进入具体架构文档，而不是继续扩充一份总览。
