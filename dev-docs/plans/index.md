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
| [Module 专属命令能力](module-command-capability.md) | manifest/deployment 冻结、共享执行服务、CLI/anasd 与 Forgejo/Incus 验收 | 实施中 |
| [VersityGW S3 兼容 Module](versitygw-module.md) | S3 Module、Capability/Resource、独立 bucket/凭据、客户端与恢复验收 | 实施中 |
| [凭据轮换覆盖面](credential-rotation.md) | 表述修正、资源凭据声明位与两侧契约、跨类清单、PostgreSQL 认证基线；验收依据见[要求](../requirements/credential-rotation.md) | 提案 |
| [Incus compute Provider](incus-module.md) | Contract 改形、`incus` Provider Module、Core 支持、共享客户端与 Forgejo 迁移；验收依据见[要求](../requirements/incus-module.md) | 实施中 |
| [Module IAM 双向登出](module-iam-bidirectional-logout.md) | 全部内置 IAM Consumer 的 Provider × 协议 × 登出方向能力与真实会话 E2E | 实施中 |
| [需求 ID 矩阵采用](requirement-id-adoption.md) | 门禁豁免清单、双向登出矩阵与迁移后扫描边界；验收依据见[要求](../requirements/requirement-id-adoption.md) | 实施中 |
| [文档驱动测试自动化](document-driven-test-automation.md) | Agent 生成完整测试、需求/用例/代码溯源、SSH 一键服务器执行与报告 | 实施中 |
| [版本升级 E2E 测试](upgrade-testing.md) | Core、Web 与全部内置 Module 的真实旧版升级、数据往返和发布门禁；验收依据见[要求](../requirements/upgrade-testing.md) | 实施中 |

## 已归档

全部里程碑完成的计划移到 `archived/`，保留为交付记录。它们不再参与需求覆盖的一致性校验：归属表
记录的是已交付的事实，不是待排的工作。稳定结论在移动前已沉淀到需求、架构或运维文档，下表的「结论
去向」给出那份文档。

| 文档 | 结论去向 | 状态 |
| --- | --- | --- |
| [workspace 与备份体系](archived/workspace-backup.md) | [备份与恢复指南](../../docs/guide/backup-and-restore.md)、[backup 契约](../../docs/reference/contracts/backup.md) | 已完成（已归档） |
| [内置 Module 与配置 Inventory](archived/builtin-inventory.md) | [需求矩阵](../requirements/builtin-inventory.md)、[Module 目录](../../docs/reference/modules.md)、[配置参考](../../docs/reference/configuration.md) | 已完成（已归档） |
| [Web API 与管理前端](archived/web-api-admin-console.md) | 管理面首版里程碑与验证记录；验收依据见[要求](../requirements/web-api-admin-console.md) | 已完成（已归档） |
| [共享应用层迁移](application-layer-migration.md) | 依赖测绘、子进程边界注入化、三个服务实现迁移与断开 `anasd` 对 runner 的链接；验收依据见[要求](../requirements/application-layer-migration.md) | 提案 |
| [Samba 身份锚点 OID 与既有目录迁移](archived/samba-identity-anchor.md) | [需求矩阵](../requirements/samba-identity-anchor.md)、[OID 注册表](../../docs/governance/oid-registry.md)、[迁移 Runbook](../../docs/guide/migrate-identity-anchor-oid.md) | 已完成（已归档） |
| [AI Agent 编排](ai-agent.md) | `ai_agent` Module、Forgejo 协作面接入、权限与执行面、排程与记录；验收依据见[要求](../requirements/ai-agent.md) | 实施中 |

## Module 私有计划

单个 Module 私有的实施计划放在该 Module 目录下（[文档写作标准](../../docs/developer/documentation-standard.md) §1 的归属规则），
不进本索引，但同样受 `npm run docs:check-requirements` 覆盖：

- [Forgejo Module](../../modules/forgejo/dev-docs/plans/forgejo-module.md)（实施中）
- [Vikunja Module](../../modules/vikunja/dev-docs/plans/vikunja-module.md)（实施中）
- [Casdoor IAM Provider](../../modules/casdoor/dev-docs/plans/archived/casdoor-iam.md)（已归档）
- [MeshCentral OIDC-only](../../modules/meshcentral/dev-docs/plans/archived/meshcentral-oidc-only.md)（已归档）

计划使用稳定主题文件名。创建日期、更新时间、状态和目标里程碑写在文档内，不因日常更新重命名。
