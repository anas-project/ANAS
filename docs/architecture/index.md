# ANAS 功能设计

本目录包含当前架构说明、设计决策和明确标注的演进方案。实现状态以代码、Module manifest
和 `docs/reference/contracts/` 为准；外部调研、需求、实施计划和时间点评审分别放在
`docs/research/`、`docs/requirements/`、`docs/plans/` 和 `docs/reviews/`。

| 文档 | 类型 | 范围 |
| --- | --- | --- |
| [Core 实现标准](core-implementation-standard.md) | 强制架构标准 | Core/Module 参数所有权、禁止特判和通用扩展路径 |
| [Module、Contract 与 Resource](module-contract-resource-design.md) | 当前模型 | 独立发布单元、跨模块协议、持久资源及 Provider operation |
| [管理员账号系统](admin-account-system.md) | 当前模型与路线图 | 目录管理员、应用角色、本地账号、Secret 与 CLI 生命周期 |
| [Samba AD 用户、组命名与权限规划](samba-ad-user-planning.md) | 当前目录与权限规范 | 目录结构、部门/角色/应用/资源组命名、账号分类与权限矩阵 |
| [IAM 能力](iam-capability-design.md) | 当前模型 | IAM provider、OIDC/SAML 协议选择、绑定与双向登出注册 |
| [应用目录](app-catalog-design.md) | 设计 | 门户条目、可见性与执行点授权映射 |
| [动态 DNS 能力](dynamic-dns-capability-design.md) | 当前模型 | DDNS 实现选择、凭据和 Web 认证 |
| [凭据轮换](credential-rotation.md) | 目标方案与实施基线 | deployment 驱动的凭据协调、轮换和回滚 |
| [运行时与发布状态](runtime-release-state-design.md) | 已实施设计记录 | deployment 制品、锁和持久状态 |
| [配置状态生命周期](config-state-lifecycle.md) | 审计与路线图 | desired、applied、observed state 的边界 |
| [CLI 配置生命周期](config-cli-lifecycle.md) | 当前模型与提案 | 当前变更门禁和未来专用执行器 |
| [AI Design Guide](ai-design.md) | 当前开发入口 | 仓库结构与 Module 开发规则 |

设计文档出现“建议”“未来”或“提案”时，不应当成可执行命令。用户操作以使用指南和参考契约为准。

跨分类入口：[需求与验收](/requirements/) · [实施计划](/plans/) · [研究与选型](/research/) · [评审与历史快照](/reviews/)
