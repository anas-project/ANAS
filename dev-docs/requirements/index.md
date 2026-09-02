# 需求与验收标准

本目录记录系统必须满足的目标、范围、约束和验收标准，只回答“必须做到什么”，不混入候选调研、具体实现步骤或阶段进度。

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [新 IAM Provider 准入与实施要求](iam-provider.md) | 目录、OIDC/SAML、身份锚点、安全和 E2E 验收 | 无矩阵（未采用 ID） |
| [Changelog 要求](changelog.md) | Core 与 Module 变更记录的位置、写入时机、发布改名与门禁 | 0/33 已完成 |
| [Compose 执行边界要求](compose-execution-boundary.md) | Docker endpoint 绑定、Compose 子进程失败归因与补偿输出边界 | 2/17 已完成 |
| [内置 Module 与配置 Inventory 要求](builtin-inventory.md) | Module 集合、配置参数、派生统计、文档目录与发布门禁的单一逻辑事实边界 | 18/18 已完成 |
| [条件 Capability 依赖要求](conditional-capability-dependency.md) | 可选服务的 Capability 依赖声明、求值时机、锁与 `plan` 输出边界 | 19/19 已完成 |
| [无序 Capability 依赖要求](weak-capability-dependency.md) | 存在性强制但不参与排序的依赖、calculate 与渲染两阶段边界、守门人成环的解法 | 15/15 已完成 |
| [Samba 目录事件订阅与实时同步要求](directory-event-subscription.md) | IAM Provider 与所有 LDAP/LDAPS Module 的事件订阅、可靠消费、全量兜底和实时同步验收 | 0/12 已完成 |
| [Samba 身份锚点 OID 与既有目录迁移要求](samba-identity-anchor.md) | PEN/OID 注册、正式 schema、新旧目录迁移、ACL、Consumer 连续性与恢复验收 | 12/12 已完成 |
| [Casdoor IAM Provider 集成要求](casdoor-iam.md) | Casdoor Module、Samba 同步、OIDC/SAML、授权、会话和发布验收 | 40/40 已完成 |
| [使用 OIDC/SAML 的 Module 双向登出要求](module-iam-bidirectional-logout.md) | RP/SP 发起登出、IAM 发起登出、通用注册、安全和真实会话 E2E | 20/42 已完成 |
| [Web API 与管理前端要求](web-api-admin-console.md) | 管理控制台的范围、访问与证书、认证与角色、安全和验收 | 141/149 已完成 |
| [Module 专属命令能力要求](module-command-capability.md) | Module 命令声明、发现、类型化执行、CLI/anasd 共享服务与权限边界 | 23/34 已完成 |
| [Forgejo Module 集成要求](forgejo-module.md) | 代码托管、OIDC、Actions 单开关、Incus one-job VM Runner 与安全验收 | 11/36 已完成 |
| [Vikunja Module 集成要求](vikunja-module.md) | 独立任务/看板服务、数据库、OIDC、持久化、登出和验收 | 28/28 已完成 |
| [MeshCentral OIDC-only 验收要求](meshcentral-oidc-only.md) | 服务端密码拒绝、双 IAM OIDC、revision 与恢复账号边界 | 7/7 已完成 |
| [Incus compute Provider Module 集成要求](incus-module.md) | `compute/incus_vm` Provider、多消费者隔离、Secret 注入边界与 Forgejo 迁移 | 0/28 已完成 |
| [VersityGW S3 兼容 Module 集成要求](versitygw-module.md) | `object_storage/s3` Capability、per-Resource bucket/凭据、POSIX backend、安全和验收 | 30/32 已完成 |
| [需求 ID 矩阵采用范围与门禁要求](requirement-id-adoption.md) | 门禁可见性、豁免清单、双向登出矩阵范围与迁移后的扫描边界 | 8/14 已完成 |
| [文档驱动测试生成与远程执行要求](document-driven-test-automation.md) | 需求到用例/完整测试代码、SSH 专用服务器执行、隔离与报告证据 | 14/32 已完成 |

> [!IMPORTANT]
> **「状态」列是生成的，不要手改。** 它由需求矩阵与配套计划的里程碑状态算出：
> `npm run docs:requirement-status`。文档 CI 跑 `--check`，手改会被拦下。
> 「文档」和「范围」两列仍然人写——新增需求文档时要手工补一行，生成器只填状态，
> 但它会在缺行时报错，不会让一份文档在索引里消失。

新增或修改需求文档前先读[需求编写规范](../../docs/developer/requirement-authoring.md)：它规定需求矩阵怎么产出、
一条需求的判据、验证方式如何选择，以及需求变化时 ID 怎么处理。

需求发生变化时原地更新稳定文件名，并同步 `updated`；实现方案进入 `docs/architecture/`，落地顺序进入 `dev-docs/plans/`。
