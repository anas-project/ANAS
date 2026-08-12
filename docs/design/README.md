# ANAS 功能设计

本目录存放仍对当前实现具有约束力的架构和功能设计。带日期的调研、评审快照仍放在
`docs/research/`；命令和文件格式的稳定外部约定放在 `docs/contracts/`。

| 文档 | 范围 |
| --- | --- |
| [管理员账号系统](admin-account-system.md) | 目录管理员、应用角色、本地账号、Secret 与 CLI 生命周期 |
| [IAM 能力](iam-capability-design.md) | IAM provider、OIDC/SAML 协议选择与绑定 |
| [应用目录](app-catalog-design.md) | 门户条目、可见性与执行点授权映射 |
| [动态 DNS 能力](dynamic-dns-capability-design.md) | DDNS 实现选择、凭据和 Web 认证 |
| [运行时与发布状态](runtime-release-state-design.md) | deployment 制品、锁和持久状态 |
| [AI Design Guide](ai-design.md) | 仓库结构与 Cask 开发入口 |

