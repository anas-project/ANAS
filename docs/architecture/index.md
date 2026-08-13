# ANAS 功能设计

本目录同时包含当前架构说明、演进方案和历史决策记录。实现状态以代码、Module manifest
和 `docs/reference/contracts/` 为准；带日期的调研、评审快照放在 `docs/research/`。

| 文档 | 类型 | 范围 |
| --- | --- | --- |
| [Module、Contract 与 Resource](module-contract-resource-design.md) | 当前模型 | 独立发布单元、跨模块协议、持久资源及 Provider operation |
| [管理员账号系统](admin-account-system.md) | 当前模型与路线图 | 目录管理员、应用角色、本地账号、Secret 与 CLI 生命周期 |
| [IAM 能力](iam-capability-design.md) | 当前模型 | IAM provider、OIDC/SAML 协议选择与绑定 |
| [应用目录](app-catalog-design.md) | 设计 | 门户条目、可见性与执行点授权映射 |
| [动态 DNS 能力](dynamic-dns-capability-design.md) | 当前模型 | DDNS 实现选择、凭据和 Web 认证 |
| [运行时与发布状态](runtime-release-state-design.md) | 已实施设计记录 | deployment 制品、锁和持久状态 |
| [配置状态生命周期](config-state-lifecycle.md) | 审计与路线图 | desired、applied、observed state 的边界 |
| [CLI 配置生命周期](config-cli-lifecycle.md) | 当前模型与提案 | 当前变更门禁和未来专用执行器 |
| [AI Design Guide](ai-design.md) | 当前开发入口 | 仓库结构与 Module 开发规则 |

设计文档出现“建议”“未来”或“提案”时，不应当成可执行命令。用户操作以使用指南和参考契约为准。
