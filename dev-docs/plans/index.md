# 实施计划

本目录记录已明确目标的落地顺序、里程碑、迁移和剩余工作。计划不是当前操作指南；完成后应把稳定结论沉淀到需求、架构、开发或运维文档，并移入 `archived/`。

> [!IMPORTANT]
> 「状态」列由 `npm run docs:plan-status` 从每份计划 frontmatter 的 `status` 字段生成，
> 取值为 `proposed` / `implementing` / `partial` / `done`。文档 CI 跑 `--check`，手改会被拦下。
> 「文档」和「范围」两列仍然人写——新增计划时要手工补一行，生成器只填状态，
> 但它会在缺行或条目指向不存在的文件时报错。

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [应用域与 Samba AD 域分离](domain-separation.md) | 参数契约、DNS 模式、迁移与验收 | 实施中 |
| [Samba 目录事件订阅与实时同步](directory-event-subscription.md) | IAM Provider 与所有 LDAP/LDAPS Module 的订阅接入、可靠消费、全量兜底和 E2E | 实施中 |
| [Changelog](changelog.md) | 变更记录文件布局、合并时写入、发布改名与 master 回推；验收依据见[要求](../requirements/changelog.md) | 提案 |
| [Compose 执行边界](compose-execution-boundary.md) | Docker endpoint 绑定、Compose 首因错误保留与补偿结果分层 | 提案 |
| [条件 Capability 依赖](conditional-capability-dependency.md) | Manifest 条件字段、解析器、锁与输出，Adminer 作为第一个消费者；验收依据见[要求](../requirements/conditional-capability-dependency.md) | 实施中 |
| [无序 Capability 依赖](weak-capability-dependency.md) | `ordering` 字段、calculate 环境隔离、解除 Adminer 成环阻塞；验收依据见[要求](../requirements/weak-capability-dependency.md) | 实施中 |
| [Web API 与管理前端](web-api-admin-console.md) | 管理面里程碑与剩余工作；验收依据见[要求](../requirements/web-api-admin-console.md) | 部分实施 |
| [Module 专属命令能力](module-command-capability.md) | manifest/deployment 冻结、共享执行服务、CLI/anasd 与 Forgejo/Incus 验收 | 实施中 |
| [VersityGW S3 兼容 Module](versitygw-module.md) | S3 Module、Capability/Resource、独立 bucket/凭据、客户端与恢复验收 | 实施中 |
| [Incus compute Provider](incus-module.md) | `incus` Provider Module、多消费者隔离、Forgejo 迁移与真实宿主验收；验收依据见[要求](../requirements/incus-module.md) | 提案 |
| [Forgejo Module](forgejo-module.md) | 安全开关、Incus compute、单作业 Runner 与发布门禁 | 实施中 |
| [Vikunja Module](vikunja-module.md) | 多架构、双数据库、双 IAM、恢复、凭据轮换与负载发布验收 | 实施中 |
| [Module IAM 双向登出](module-iam-bidirectional-logout.md) | 全部内置 IAM Consumer 的 Provider × 协议 × 登出方向能力与真实会话 E2E | 实施中 |
| [需求 ID 矩阵采用](requirement-id-adoption.md) | 门禁豁免清单、双向登出矩阵与迁移后扫描边界；验收依据见[要求](../requirements/requirement-id-adoption.md) | 实施中 |
| [文档驱动测试自动化](document-driven-test-automation.md) | Agent 生成完整测试、需求/用例/代码溯源、SSH 一键服务器执行与报告 | 实施中 |

## 已归档

全部里程碑完成的计划移到 `archived/`，保留为交付记录。它们不再参与需求覆盖的一致性校验：归属表
记录的是已交付的事实，不是待排的工作。稳定结论在移动前已沉淀到需求、架构或运维文档，下表的「结论
去向」给出那份文档。

| 文档 | 结论去向 | 状态 |
| --- | --- | --- |
| [workspace 与备份体系](archived/workspace-backup.md) | [备份与恢复指南](../../docs/guide/backup-and-restore.md)、[backup 契约](../../docs/reference/contracts/backup.md) | 已完成（已归档） |
| [Casdoor IAM Provider](archived/casdoor-iam.md) | [需求矩阵](../requirements/casdoor-iam.md)、[IAM Capability 设计](../../docs/architecture/iam-capability-design.md)、[Casdoor 运维 Runbook](../../docs/operations/casdoor-iam.md) | 已完成（已归档） |
| [MeshCentral OIDC-only](archived/meshcentral-oidc-only.md) | [需求矩阵](../requirements/meshcentral-oidc-only.md)、[Module IAM 支持清单](../../docs/reference/module-iam-support.md) | 已完成（已归档） |

计划使用稳定主题文件名。创建日期、更新时间、状态和目标里程碑写在文档内，不因日常更新重命名。
