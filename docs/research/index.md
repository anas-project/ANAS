# 研究与选型资料

本目录只存放外部事实调研、候选比较和技术选型。需求、架构设计、实施计划、时间点评审、操作手册和开发规范分别进入各自目录。

研究文档使用稳定的主题文件名，不把创建时间或更新时间写入文件名。每篇文档在 frontmatter 中声明 `created`、`updated`，依赖动态上游事实时再声明 `evidence_as_of`；后续更新原文件并同步这些字段。

## 应用与产品选型

比较候选应用、基础设施产品或服务实现，回答“采用什么”。

| 文档 | 范围 | 创建 | 证据截至 |
| --- | --- | --- | --- |
| [Mastodon 相关开源自部署服务](./mastodon-related-self-hosted-services-research.md) | Mastodon、兼容联邦微博与相邻 ActivityPub 服务选型 | 2026-08-21 | 2026-08-21 |
| [开源自部署 IAM 与 ANAS 适配](./self-hosted-open-source-iam-research.md) | 第三方身份、Passkey、密码写回、应用门户和 Provider 选型 | 2026-08-20 | 2026-08-20 |
| [BIND 9 开源 Web 管理工具](./bind9-open-source-web-management-research.md) | 标准 BIND、BIND9-DLZ 与管理/监控选型 | 2026-08-19 | 2026-08-19 |
| [开源自部署邮件服务](./self-hosted-open-source-mail-services-research.md) | 完整邮件服务与外部转发能力 | 2026-08-19 | 2026-08-19 |
| [开源自部署邮件转发服务](./self-hosted-open-source-email-forwarding-research.md) | 透明转发与隐私别名服务 | 2026-08-19 | 2026-08-19 |
| [开源自部署 S3 兼容存储](./self-hosted-open-source-s3-compatible-storage-research.md) | 对象存储和文件网关选型 | 2026-08-15 | 2026-08-15 |
| [开源自部署 Git 服务](./self-hosted-open-source-git-services-research.md) | Git 协作平台与 Module 选型 | 2026-08-15 | 2026-08-15 |
| [Super Productivity 与同类项目](./super-productivity-alternatives-research.md) | 个人任务、时间规划和工时工具 | 2026-08-15 | 2026-08-15 |
| [开源自部署笔记应用](./self-hosted-open-source-notes-research.md) | 笔记和个人知识管理选型 | 2026-08-13 | 2026-08-13 |
| [开源自部署 Kanban 应用](./self-hosted-open-source-kanban-research.md) | Kanban 与相邻项目选型 | 2026-08-10 | 2026-08-13 |

## 功能与集成可行性

围绕特定能力、协议或系统组合验证实现路径与边界，回答“能否实现以及如何衔接”。

| 文档 | 范围 | 创建 | 证据截至 |
| --- | --- | --- | --- |
| [LLNG Passkey/WebAuthn 与 Samba 共享边界](./llng-passkey-webauthn-samba-sharing.md) | LLNG 分阶段启用、凭据存储与多 IAM 共享边界 | 2026-08-21 | 2026-08-21 |
| [IAM 登出与应用会话同步](./iam-logout-application-session-sync.md) | OIDC/SAML 全局登出与应用会话撤销 | 2026-08-20 | 2026-08-20 |
| [Super Productivity 与 Nextcloud 零配置同步](./super-productivity-nextcloud-sso-sync-research.md) | OIDC/SAML、Login Flow v2 与 BFF 方案 | 2026-08-20 | 2026-08-20 |
| [Nextcloud 搜索方案](./nextcloud-search-solution-research.md) | 全文、SQL、语义搜索和 Elasticsearch 实施门槛 | 2026-08-20 | 2026-08-20 |
| [看板应用接入 AI Agent](./kanban-ai-agent-integration-research.md) | 看板事件、编排服务与 Coding Agent 集成 | 2026-08-15 | 2026-08-15 |

## 维护约定

- 新文档先按研究问题分类：候选比较归入“应用与产品选型”，单项能力、协议或跨系统路径归入“功能与集成可行性”。
- 架构方案进入 `docs/architecture/`，时间点审计进入仓库根的 `dev-docs/reviews/`，不在研究索引重复收录。
- 文件名只表达稳定主题；创建、更新和证据截点只写在文档 frontmatter 中。
- `created` 只在首次创建时写入，后续不得改成最后修改日期。
- `updated` 在结论、比较范围或重要事实发生实质变化时更新；只修正排版或链接时可以不变。
- `evidence_as_of` 表示动态外部事实的最后核验日期，不等于 `updated`。
- Star、最新提交和版本不是质量分数，只作为社区规模和活跃度信号。
- 优先引用项目官网、官方文档、许可证和仓库；聚合站只用于发现候选。
- “开源”默认采用 OSI/FSF 通常语义。Fair-code、Business Source License 等源码可见项目单列，不混入严格开源推荐。
- 新选型文档必须说明与 ANAS 的 Module、IAM、数据库、Traefik、备份和升级契约如何衔接。
- 新主题按[应用研究文档规范](/developer/research-document-standard)定义范围、证据和交付物。
