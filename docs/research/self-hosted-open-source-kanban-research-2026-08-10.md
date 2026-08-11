# 开源自部署 Kanban 应用全景调研

调研日期：2026-08-10（Asia/Singapore）
适用项目：ANAS（Go cask 编排器、Docker Compose、Traefik、PostgreSQL/MariaDB、Samba/LDAP、Authentik/LLNG、Nextcloud）

## 1. 结论先行

不存在一个适合所有 ANAS 用户的“最佳 Kanban”。按当前项目能力和部署成本，建议分成三条产品路径：

1. **默认独立 cask：Vikunja**。成熟度、资源占用、OIDC、LDAP、REST API/Webhook、迁移能力和 Go 单体部署之间最平衡。它不只是纯看板，适合个人和中小团队的任务、日历、Gantt/表格需求。
2. **现代纯看板优先：Kan**。界面和 Trello 心智最接近，AGPL、PostgreSQL、Docker Compose、通用 OIDC、API Key 和原生 MCP 都很贴合 ANAS；但项目始于 2023 年，成熟度和升级历史弱于 Vikunja/Wekan，建议先标为 `experimental`。
3. **零新增服务：Nextcloud Deck**。ANAS 已有 Nextcloud、SAML/LDAP、移动端和文件体系，启用 Deck 应用即可获得最低运维成本。短板是大型看板性能、工作流深度和 API 稳定性不如专用系统。

备选：

- **Wekan**：最成熟、最完整的 Trello 式 FLOSS 看板之一，认证和 REST API 丰富；代价是引入 MongoDB/FerretDB，Meteor 运行栈较重。
- **4ga Boards**：现代、实时、OIDC、开发非常活跃，适合作为第二个实验性纯看板候选；历史较短，API/生态仍不如 Kan/Vikunja。
- **Kanboard**：极轻、稳定、API/插件成熟；官方已声明 maintenance mode，且现代 OIDC 通常依赖插件，适合保守型或低配设备而不适合作为默认新产品。
- **Leantime**：如果目标是“目标/里程碑/时间管理 + 看板”而非 Trello 克隆，它与 MariaDB、OIDC/LDAP 的匹配很好；需要持续核对免费核心与付费插件边界。

不建议作为首个 cask：Plane、OpenProject、Taiga、Huly。它们是完整项目管理套件，依赖和运维面明显更大；Plane/OpenProject 社区版的企业 SSO 边界也不符合 ANAS 当前“开源版即可接 IAM”的理想契约。

## 2. 范围、口径与局限

### 2.1 纳入范围

本报告把候选分成四类：

- 以 Kanban/Trello 替代为核心的多人 Web 应用；
- 同时提供看板的开源任务/项目管理套件；
- 已在 ANAS 生态内或能显著复用现有基础设施的应用；
- 经常出现在推荐列表、但已停更或已改为非开源许可证的项目，用于排雷。

“当前所有”无法做数学意义的穷尽：个人仓库每天都可能出现。本次从用户给出的 [AlternativeTo Kanba 列表](https://alternativeto.net/software/kanba/?license=opensource&platform=self-hosted)、[Opensource.com Trello 替代文章](https://opensource.com/alternatives/trello)、GitHub 主题/搜索、项目官网和官方文档交叉扩展，共核对 30 余个代表性项目。未把仅有静态单机板、编辑器插件、纯白板、SaaS-only 项目列入主表。

### 2.2 指标口径

- Star、最后推送时间、许可证和归档标志来自 GitHub 官方仓库 API，快照时间为 2026-08-10；Star 取整显示，完整值见 §8。
- “活跃度”综合最近推送、发行节奏和项目自述。仓库有自动化提交不等于健康社区，故只分为高/中/低/停止。
- “成熟度”综合项目年龄、升级历史、文档、API、部署方式和维护状态，不等同于功能多少。
- “社区版 SSO”只认免费自部署版本可用的 LDAP、OIDC 或 SAML；社交登录不自动等于通用企业 SSO。
- “AI”分为原生 AI、官方 MCP/Agent、仅能通过 API/Webhook 外接三档。
- 移动平台区分原生 App、桌面封装和响应式 Web/PWA，避免把“手机浏览器可打开”写成“有 App”。

## 3. 主流严格开源候选总表

符号：✅ 原生/官方支持；◐ 有限制、插件或仍在早期；— 未确认或没有。Star 为 2026-08-10 快照。

| 项目 | 定位 / 许可证 | Web 与客户端 | 社区版 SSO | API / 自动化 | AI 接入 | Star | 活跃度 / 成熟度 | ANAS 适配 |
| --- | --- | --- | --- | --- | --- | ---: | --- | --- |
| **Kan** | 现代 Trello 替代；AGPL-3.0 | ✅ Web/响应式；无官方原生 App | ✅ 通用 OIDC，另有 Google/GitHub/Discord | ✅ API Key；MCP 提供 46 个工具 | ✅ 官方 MCP，可由 Codex/Claude/Copilot 控制 | 5,327 | 高 / 成长中 | **A**：PostgreSQL、Compose、OIDC、MCP 非常匹配；先 experimental |
| **Vikunja** | 任务管理 + Kanban/List/Gantt/Table；AGPL-3.0 | ✅ Web；官方桌面；Android/iOS 功能较基础 | ✅ OIDC；✅ LDAP | ✅ REST/OpenAPI、Webhook、OAuth2 server、n8n | ◐ 无内置生成式 AI；API、Webhook、bot user、agent CLI 易外接 | 5,029 | 高 / 成熟 | **A**：单容器/单二进制、Go、PG/MariaDB、IAM 都匹配 |
| **Nextcloud Deck** | Nextcloud 内的团队/个人看板；AGPL-3.0 | ✅ Web；✅ Android、iOS | ✅ 继承 Nextcloud LDAP/SAML/OIDC 体系 | ✅ OCS REST API；Nextcloud 活动/日历/文件集成 | ◐ 可经 REST/Nextcloud Assistant 外接，非 Deck 原生 | 1,410 | 高 / 成熟 | **A**：现有 Nextcloud 直接启用；规模性能要压测 |
| **Wekan** | 功能完整的 Trello 克隆；MIT | ✅ Web/PWA；Snap/多架构包；无可靠官方原生移动 App | ✅ LDAP、OAuth2/OIDC，另有多种认证配置 | ✅ REST API、Webhook、导入导出 | ◐ REST/Webhook 外接，无原生 AI | 21,027 | 很高 / 很成熟 | **B+**：功能强；MongoDB/FerretDB 与 Meteor 增加新栈 |
| **4ga Boards** | 轻量实时纯看板；MIT | ✅ 响应式 Web；无原生 App | ✅ 通用 OIDC | ◐ 项目接口和实时能力可扩展，公开 API 生态尚小 | ◐ 无原生 AI | 687 | 高 / 成长中 | **B+**：Compose/OIDC 好；较年轻，需补 API 和升级验证 |
| **Kanboard** | 极简传统 Kanban；MIT | ✅ 响应式 Web；无官方原生 App，存在第三方客户端 | ✅ LDAP；◐ OAuth/OIDC/SAML 主要靠插件或前置认证 | ✅ 完整 JSON-RPC API、Webhook、插件/事件系统 | ◐ API/插件外接 | 9,785 | 中 / 很成熟；官方 maintenance mode | **B**：轻量可靠；不宜期待大功能演进 |
| **Leantime** | 目标导向 PM + 看板；AGPL-3.0 | ✅ Web/PWA；无主流官方原生 App | ✅ OIDC、LDAP（部分高级配置可能需认证插件） | ✅ JSON-RPC API；插件；暂无内置 Webhook | ◐ AI/MCP 以 Pro/Marketplace 插件为主 | 11,292 | 高 / 成熟 | **B**：MariaDB、IAM 很匹配；需固定 OSS/插件功能边界 |
| **Taiga** | Scrum + Kanban 套件；MPL-2.0 | ✅ Web；无当前官方原生 PM App | ◐ 核心没有通用企业 SSO；LDAP/OIDC/SAML 多依赖社区插件 | ✅ 完整 REST API、Webhook、GitHub/GitLab 集成 | ◐ API 外接，无核心原生 AI | 844（后端） | 中高 / 成熟 | **B-**：成熟但多容器、PG/RabbitMQ/Redis，IAM 需自维护插件 |
| **Plane CE** | Linear/Jira 式 PM + 看板/周期/页面；AGPL-3.0 | ✅ Web/PWA；官方移动端对自部署 CE 支持仍需逐版核对 | **— CE 无通用 OIDC/SAML/LDAP**；付费层提供 | ✅ 180+ REST endpoints、Webhook、OAuth App | ✅ Plane AI、MCP/Agents；具体自托管版本/额度需核对 | 55,778 | 很高 / 成长为成熟 | **C+**：能力强，但 ≥4 GB、多服务，CE SSO 是硬缺口 |
| **OpenProject CE** | 企业级 PM/Portfolio + 基础看板；GPL-3.0 | ✅ Web；✅ Android/iOS Beta（需 v17+、可信 HTTPS） | ◐ LDAP 登录；**OIDC/SAML 为 Enterprise add-on** | ✅ HATEOAS REST API v3、OpenAPI | ◐ 官方 MCP 为 Enterprise；CE 可用 REST 外接 | 15,817 | 很高 / 很成熟 | **C+**：专业但重；SSO/MCP 社区版缺失 |
| **Huly** | Jira/Linear/Slack/Notion 一体化；EPL-2.0 | ✅ Web/桌面；移动能力非主要卖点 | ◐ 自托管认证与企业 SSO需逐版核对 | ✅ 丰富内部服务接口；外部 API 文档成熟度一般 | ✅ 平台含 AI/协作能力，但自托管配置复杂 | 27,288 | 高 / 成长中 | **C**：系统面和资源面过大，不适合作为轻量 cask |
| **Tududi** | 个人/小团队任务、项目、笔记 + Kanban；MIT | ✅ Web；借 CalDAV 接 Tasks.org、Apple Reminders 等 | ✅ OIDC（新版本）；CalDAV 另有 Basic Auth | ◐ CalDAV 强，通用业务 API 生态较小 | ✅ 可选 OpenAI Key 的 Daily Brief/Insights | 3,230 | 很高 / 年轻 | **B（观察）**：轻、OIDC、AI；多人权限和升级历史需验证 |
| **Scrumboy** | 可分享实时看板/Issue tracker；AGPL-3.0 | ✅ Web/PWA | ✅ OIDC/SSO | ✅ API；✅ MCP client/server 能力 | ✅ MCP、语音/AI 接入导向 | 427 | 很高 / 很年轻 | **B（观察）**：方向匹配但生产样本和历史不足 |
| **Kanba** | 面向 maker/小团队的轻量 Trello 替代；MIT | ✅ Web；无官方原生 App | ◐ Supabase Auth/社交登录；未确认通用 OIDC/SAML/LDAP | ◐ 使用 Supabase/Next.js API，尚无成熟公开集成 API 契约 | — 未见原生 AI | 638 | 高 / 很年轻（2025） | **C+（观察）**：正是参考链接对象，但 Supabase/Stripe 架构与 ANAS 复用度低 |

### 3.1 与 Git/ERP 平台捆绑的可用替代

这些系统有看板，但不应只为看板而部署：

| 项目 | 看板特点 | SSO/API/AI | Star / 活跃度 | ANAS 判断 |
| --- | --- | --- | --- | --- |
| **Gitea**（MIT） | Issues/Projects 看板适合研发团队 | LDAP/OAuth2/OIDC；完整 REST API；可接外部 AI | 57,299 / 很高 | 若未来已有 Git cask，顺带使用最合理；不是通用业务看板首选 |
| **Forgejo**（GPL-3.0+，主要在 Codeberg） | Gitea 系谱的项目看板 | LDAP/OAuth2/OIDC；API | 活跃成熟 | 比 Gitea 更社区治理；同样仅推荐给代码研发场景 |
| **ERPNext**（GPL-3.0） | DocType/任务的 Kanban 视图 | OAuth/LDAP、REST、Frappe 扩展 | 37,913 / 很高 | 只有同时需要 ERP 时才合理；部署和数据模型远超看板需求 |
| **Odoo Community**（LGPL 核心） | Project/CRM 等模型的 Kanban 视图 | XML-RPC/JSON-RPC；认证和 AI 功能存在版本/企业版边界 | 53,627 / 很高 | 适合已有 Odoo 的组织，不建议作为独立看板 cask |
| **Super Productivity**（MIT） | 个人 Kanban、时间盒、时间跟踪；GitHub/GitLab/Jira/OpenProject 集成 | 本地优先/文件同步，非标准多人服务 API/SSO | 21,244 / 很高 | 更像跨平台个人客户端，不符合 ANAS 多用户服务 cask 主目标 |
| **GitLab Self-Managed Free/CE** | Issue Boards 与代码、MR、Milestone 结合；Free 层有项目看板 | ✅ REST Board API；✅ Self-Managed Free 的实例级 SAML/LDAP/OmniAuth；AI 多为付费能力 | GitLab 主仓库在 GitLab.com，不用 GitHub Star横比 | 已有 GitLab 时直接用；只为看板部署过重 |
| **Tuleap**（GPL-2.0-or-later） | 完整 ALM，Tracker、Scrum、Kanban、Git、测试和文档 | REST API；官方称不区分 Enterprise/Community 功能，认证仍需按发行版核对 | 官方 Git 仓库不以 GitHub API 计数 | 软件研发和强追踪场景成熟，但明显不是轻量看板 |
| **禅道 ZenTao 开源版** | 中国团队常用 Scrum/Kanban/需求/缺陷/测试套件 | API 与企业集成因版本/版本线而异；SSO/AI 需核对开源版边界 | 1,666 / 高 | 本地化强；ZPL/产品线和免费功能边界需法务及 PoC 单独核实 |
| **Phorge**（Apache-2.0） | Phabricator 的社区延续，Workboards 面向研发任务 | Conduit API；认证扩展丰富；无现代原生 AI | 195 / 活跃镜像 | 适合既有 Phabricator 用户，不建议新建通用看板实例 |
| **Redmine**（GPL-2.0） | 核心是 Issue/Gantt；Kanban 通常依赖 Agile/看板插件 | REST API、LDAP；OIDC/SAML 和 Kanban 都依赖插件选择 | 6,015（GitHub 镜像）/ 成熟 | 不能把插件能力写成 Redmine 社区核心；仅在已有 Redmine 时考虑 |

## 4. 源码可见但不再属于严格开源

| 项目 | 当前许可 / 状态 | 功能和风险 | 结论 |
| --- | --- | --- | --- |
| **PLANKA** | Fair Use License + Pro/Enterprise License；12,326 Star；活跃 | 现代 Trello 式 UI、实时协作、OIDC、Swagger/Postman API、Docker/Helm；文档明确称 source available/self-hostable | 可作为商业/源码可见候选，但不能写进“严格开源默认 cask” |
| **Taskosaur** | Business Source License；542 Star；活跃、年轻 | 内置对话式 AI，可接 OpenAI/OpenRouter/Anthropic/Ollama，Swagger API，Jira/Trello 同步；许多能力仍标注 planned/working toward | 技术观察，不进入 FLOSS 选型 |

特别提醒：网上较旧的 PLANKA 对比文章仍把它标为 MIT/开源，当前官方文档已改变口径，必须以当前 LICENSE 和官方许可指南为准。

## 5. 停更、维护模式或不建议新部署的项目

| 项目 | Star | 最后有效开发信号 | 状态判断 | 替代建议 |
| --- | ---: | --- | --- | --- |
| **Focalboard standalone** | 26,386 | README 明示 “currently not maintained”；最后正式版 8.0.0（2024） | 不作为新生产部署；桌面端/Server/API 曾经完整 | Kan、Vikunja、Wekan |
| **Taskcafe** | 5,208 | 最后代码推送 2023-07 | 事实停更，虽未归档 | Plane/Kan/Vikunja |
| **Restyaboard** | 2,085 | 最后代码推送 2023-10，最后发行版 2022 | README 称 active 与仓库活动矛盾；旧 PHP/ElasticSearch 部署面大 | Wekan/Kanboard |
| **Lavagna** | 641 | 2024-08，仓库已 archived | 停止 | Kanboard/Vikunja |
| **TaskBoard** | 1,401 | 提交信号稀疏，架构和 UI 较旧 | 低维护，不建议新部署 | Kanboard/4ga Boards |
| **Kanboard** | 9,785 | 2026 仍发布修复 | 不是停更，但官方明确 maintenance mode | 低资源保守场景仍可选 |

## 6. 重点候选详评

### 6.1 Kan：最匹配 ANAS 的现代纯看板

优势：

- AGPL-3.0，官方 Docker Compose；应用 + migration + PostgreSQL 的职责清楚。
- 通用 OIDC 只需 discovery URL、client ID/secret，能直接对接 ANAS Authentik；若 LLNG OIDC consumer contract 完成，也可接 LLNG。
- 可关闭本地注册和本地凭据，适合受控家庭/团队实例。
- 支持 S3 兼容附件，未来可映射到 ANAS 自托管对象存储；当前也可先禁用或使用本地策略。
- 原生 API Key + 官方 `@kan/mcp`，46 个 MCP 工具覆盖 workspace/board/list/card/comment/checklist/label/member，AI 集成不需要屏幕自动化。
- 依赖 PostgreSQL，ANAS 已有 capability provider，可复用现有数据库 cask，而不是再部署内置数据库。

风险：

- 年轻项目，数据库迁移、跨版本回滚、导入大看板和高并发样本不如 Wekan/Vikunja。
- `latest` 示例不能进入 ANAS；需固定 release/digest，验证 amd64/arm64 镜像和 migration 的幂等性。
- 目前无官方原生移动 App，应按响应式 Web/PWA 描述。
- MCP 运行在客户端侧 Node.js 进程；ANAS 若要把它作为长期服务，应单独设计 sidecar 或只发布配置说明。

建议 cask：`kan`，`status: experimental`，`requires: traefik`，`requires_capabilities: relational_database/postgres`，`iam.interfaces: oidc`。第一期不引入 S3，先验证本地附件持久化和备份。

### 6.2 Vikunja：默认推荐的稳健方案

优势：

- 后端为 Go，前后端已打包为单二进制/单容器；可接 PostgreSQL、MySQL/MariaDB，拓扑最适合 cask。
- OIDC 官方文档直接给出 Authentik、Keycloak 等配置；当前文档也列出 LDAP。
- REST/OpenAPI、Webhook、bot users、n8n、CalDAV、OAuth 2.0 authorization server，自动化边界最完整。
- Web 之外有桌面包和移动 App；官方明确移动端目前只覆盖基础能力，应保留这个限制说明。
- 任务、重复任务、日历、Gantt、表格比纯 Trello 克隆更适合家庭和个人生产力。

风险：

- 不是“只做看板”，用户若只要极简 Trello UI，Kan/4ga/Wekan 更直观。
- OIDC 移动端使用 WebView，有 cookie/state 已知限制；需要用 ANAS 实际 IdP 做 iOS/Android E2E。
- 2026 已出现 Vikunja Pro 可选功能，需对每个版本锁定社区版功能矩阵，避免把付费能力写入 cask 契约。

建议 cask：`vikunja`，可直接作为 stable 候选。优先 PostgreSQL，提供 `db_type: auto`；OIDC 为第一期身份协议，LDAP 作为备选而不是双重自动建号。

### 6.3 Nextcloud Deck：最低总拥有成本

优势：

- ANAS 已部署 Nextcloud 34、LDAP、SAML、Traefik、PostgreSQL/MariaDB；Deck 只是 Nextcloud app 生命周期，不需要新域名、数据库或账号体系。
- 官方 Android/iOS App 均能连接自托管 Nextcloud；继承 Nextcloud 文件、评论、活动流、日历和 Circles。
- REST API 能读写 board/stack/card/label/ACL/attachment，足以做基础 AI 与自动化。

风险：

- 官方 README 明示高 board/card/attachment 数量会产生大量数据库查询，不适合未经压测的大团队。
- API 文档和实际 endpoint 曾出现不同步问题；集成必须用固定 Deck/Nextcloud 版本做契约测试。
- 工作流、报表、自动化和 WIP 能力弱于专业 PM 套件。

实施建议：不要新增独立 `deck` Compose cask。把它作为 `nextcloud` cask 的可选 app（例如 `deck_enabled`），在现有 app reconcile 和兼容版本锁中安装，并增加 REST smoke test、Android/iOS 登录说明和大板基准。

### 6.4 Wekan：成熟 Trello 替代

优势是 2014 年以来的深厚功能、21k Star、非常高的发布频率、MIT、REST API、Webhook、WIP/swimlane/custom fields、导入导出和丰富认证。其 2026-08-10 仍有提交和近期发行，不属于“老而停更”。

主要代价是 Meteor + MongoDB。ANAS 当前没有文档型数据库 capability；为一个应用增加 MongoDB 会扩大备份、升级和资源矩阵。Wekan 正在探索 FerretDB/其他后端，但不能在未做上游兼容性验证前把 PostgreSQL 等价替换写进生产设计。

建议：只有当用户明确优先“最完整 Trello 克隆”时提供 `wekan`，同时新建 document database capability 或把 MongoDB 明确设为 Wekan 私有依赖。

### 6.5 4ga Boards：值得跟踪的轻量新秀

官方文档提供 Docker 变量和 OIDC client 配置，实时协作与现代界面强，2026 年仍高频发行。相对 Kan，API/MCP/第三方客户端生态较弱；相对 Wekan，历史和生产规模证据较少。适合 experimental cask 与 Kan 做实际 UX/资源 A/B 测试。

### 6.6 Kanboard：低资源和长期稳定场景

单 PHP 应用、SQLite/MySQL/PostgreSQL、完整 JSON-RPC API、Webhook、自动动作和成熟插件体系使它非常适合低配 NAS。LDAP 是核心能力，OIDC/SAML 通常依赖插件或前置代理。官方明确只做小修和接受社区贡献，因此选择它等于接受 UI/功能基本冻结，换取可预测性。

### 6.7 Plane、OpenProject、Taiga、Huly：为什么暂缓

- **Plane**：官方最低 2 vCPU/4 GB，包含 Web/API/worker/live/PG/Redis/MinIO 等服务。CE 核心很强、API/MCP 优秀，但通用 SAML/OIDC/LDAP 属商业层或存在版本差异，和 ANAS IAM 自动注册目标冲突。
- **OpenProject**：企业 PM、Gantt、时间/成本、移动 Beta 和 API 都成熟；社区版只提供基础看板，OIDC/SAML 与 MCP Server 是 Enterprise add-on。适合有正式项目组合管理需求的组织，而非家庭 NAS 默认应用。
- **Taiga**：Scrum 语义最完整，REST API 成熟；部署涉及前后端、async events、PostgreSQL、RabbitMQ/Redis 等，企业 SSO依赖社区插件，升级测试矩阵大。
- **Huly**：把 issue、文档、聊天、邮箱/日历等合并，能力远超当前需求；部署和运维面最大，外部 API/身份契约需要专项研究。

### 6.8 Kanba：参考项目本身的判断

Kanba 是 MIT、源码开放、自托管的年轻项目，强调简洁、速度、无限项目和小团队协作。当前官方部署说明以 Next.js + Supabase（数据库、Auth、存储/Edge 能力）为中心，并保留托管业务所需的 Stripe 配置。作者说明自托管可不使用 Stripe，但这仍不是 ANAS 现有 PostgreSQL capability 可直接复用的传统单体拓扑。

它目前没有 Kan 那样清晰的通用 OIDC、稳定公开业务 API 和 MCP 契约，也没有原生移动 App。可以继续观察 UI 和产品方向，但如果现在落 cask，需要额外承担 Supabase 组件、认证映射与迁移维护；综合优先级低于名称相近但技术契约更完整的 **Kan**。

## 7. ANAS 落地方案

### 7.1 推荐顺序

| 阶段 | 动作 | 完成标准 |
| --- | --- | --- |
| 1 | 在现有 Nextcloud cask 增加 Deck 可选 app | 新装/升级/禁用可 reconcile；REST smoke；SAML/LDAP 与移动端验证 |
| 2 | 新增 `vikunja` experimental cask | PG/MariaDB 二选一、OIDC、备份恢复、升级回滚、API/Webhook 全通过 |
| 3 | 新增 `kan` experimental cask | PostgreSQL、OIDC、Trello import、附件、API Key/MCP、迁移幂等通过 |
| 4 | 对 Kan/Vikunja/Deck 做真实用户与资源对比 | 记录 idle/load RAM/CPU、首屏、1k/10k card、移动体验、恢复时间 |
| 5 | 按需求决定 Wekan/4ga/Kanboard | 只有明确用户场景再扩充，避免一次维护多个同质 cask |

### 7.2 cask 契约要求

所有候选必须满足：

- 固定上游版本和镜像 digest；验证 amd64 与 arm64。
- 不直接复制上游一体化 Compose 的数据库，优先声明 ANAS `relational_database` capability；确实需要 MongoDB 时单独建能力边界。
- `requires: traefik`，域名/HTTPS/上传限制走 cask 参数；禁止把数据库、OIDC、SMTP secret 暴露给无关容器。
- 身份声明必须与真实协议一致：Kan/Vikunja 先声明 OIDC；Deck 继承 Nextcloud；不得用 oauth2-proxy 冒充应用内账号生命周期已经集成。
- 测试首次登录、JIT 建号、禁用本地注册、用户名/邮箱冲突、登出和 IdP 不可用降级。
- 为 API 创建最小权限服务账号或 token；AI 不得默认获得管理员 token。
- MCP/AI 设为显式 opt-in，单独 secret，记录 outbound endpoint；支持本地模型时优先允许用户配置 OpenAI-compatible base URL。
- 备份至少覆盖数据库、附件和必要配置；恢复后验证 board/card/comment/attachment、OIDC subject 映射和 API token。
- 升级探针覆盖数据库 migration、前一 patch、至少一个前一 minor；数据破坏版本写入 `upgrade.data_breaking`。

### 7.3 建议的实测评分权重

| 维度 | 权重 | 原因 |
| --- | ---: | --- |
| 社区版 IAM（OIDC/LDAP/SAML） | 20% | ANAS 已有统一身份能力，不能退回重复账号 |
| 部署、升级、备份复杂度 | 20% | NAS 产品的长期运维成本高于一次性安装体验 |
| API/Webhook/MCP | 15% | 决定自动化和 AI 能否使用稳定机器接口 |
| 看板与协作 UX | 15% | 核心用户价值 |
| 活跃度、安全响应、成熟度 | 15% | 降低弃坑和升级风险 |
| 移动与多平台 | 10% | 家庭/团队任务经常在手机录入 |
| 资源占用 | 5% | NAS 需要与文件、IAM、数据库等服务共存 |

按文档证据的预评分：Vikunja 88、Kan 85、Deck 83、Wekan 78、4ga 76、Kanboard 73、Leantime 72、Plane CE 66。分数只用于安排 PoC 顺序，不替代真实部署测试。

## 8. GitHub 指标快照

数据来自各仓库 `https://api.github.com/repos/{owner}/{repo}`，采集于 2026-08-10。`pushed_at` 只表示仓库最近推送，不自动代表发布质量。

| 仓库 | Star | Fork | Open issues | 最近推送（UTC） | GitHub 许可证标识 |
| --- | ---: | ---: | ---: | --- | --- |
| `makeplane/plane` | 55,778 | 5,286 | 1,004 | 2026-08-10 | AGPL-3.0 |
| `hcengineering/platform` | 27,288 | 2,095 | 844 | 2026-08-07 | EPL-2.0 |
| `mattermost-community/focalboard` | 26,386 | 2,577 | 784 | 2026-05-18 | 未识别；README 明示未维护 |
| `wekan/wekan` | 21,027 | 2,997 | 347 | 2026-08-10 | MIT |
| `opf/openproject` | 15,817 | 3,419 | 199 | 2026-08-10 | GPL-3.0 |
| `plankanban/planka` | 12,326 | 1,338 | 445 | 2026-08-10 | 未识别；官方为 Fair Use/商业许可 |
| `Leantime/leantime` | 11,292 | 1,099 | 321 | 2026-08-10 | AGPL-3.0 |
| `kanboard/kanboard` | 9,785 | 1,986 | 163 | 2026-08-01 | MIT |
| `kanbn/kan` | 5,327 | 439 | 114 | 2026-08-08 | AGPL-3.0 |
| `JordanKnott/taskcafe` | 5,208 | 470 | 22 | 2023-07-23 | MIT |
| `go-vikunja/vikunja` | 5,029 | 600 | 238 | 2026-08-10 | AGPL-3.0 |
| `chrisvel/tududi` | 3,230 | 233 | 16 | 2026-08-10 | MIT |
| `RestyaPlatform/board` | 2,085 | 385 | 189 | 2023-10-26 | OSL-3.0 |
| `kiswa/TaskBoard` | 1,401 | 296 | 55 | 2025-05-23 | MIT |
| `nextcloud/deck` | 1,410 | 346 | 838 | 2026-08-10 | AGPL-3.0 |
| `taigaio/taiga-back` | 844 | 300 | 94 | 2026-08-04 | MPL-2.0 |
| `RARgames/4gaBoards` | 687 | 119 | 157 | 2026-08-09 | MIT |
| `Kanba-co/kanba` | 638 | 102 | 11 | 2026-08-08 | MIT |
| `digitalfondue/lavagna` | 641 | 113 | 28 | 2024-08-06 | GPL-3.0；已归档 |
| `Taskosaur/Taskosaur` | 542 | 106 | 10 | 2026-07-21 | GitHub 未识别；实际 BSL |
| `markrai/scrumboy` | 427 | 35 | 5 | 2026-08-10 | AGPL-3.0 |
| `easysoft/zentaopms` | 1,666 | 398 | 0 | 2026-07-30 | GitHub 未识别；ZPL/版本线需单独核实 |
| `redmine/redmine`（官方 SVN 镜像） | 6,015 | 2,455 | 3 | 2026-08-03 | GitHub 未识别；GPL-2.0 |
| `phorgeit/phorge`（只读镜像） | 195 | 44 | 0 | 2026-08-09 | Apache-2.0 |

旁系大型平台：`go-gitea/gitea` 57,299 Star、`odoo/odoo` 53,627、`frappe/erpnext` 37,913、`super-productivity/super-productivity` 21,244；这些 Star 不能归因于看板模块本身。

## 9. 主要来源

### 发现入口

- [AlternativeTo：Kanba 的开源自托管替代](https://alternativeto.net/software/kanba/?license=opensource&platform=self-hosted)
- [Opensource.com：Trello alternatives](https://opensource.com/alternatives/trello)

### 官方仓库和功能文档

- Kan：[GitHub](https://github.com/kanbn/kan)（README 含 Compose、OIDC、API key、MCP）
- Vikunja：[安装](https://vikunja.io/docs/installing/)、[OIDC](https://vikunja.io/docs/openid/)、[文档/API/集成目录](https://vikunja.io/docs/)
- Wekan：[GitHub](https://github.com/wekan/wekan)、[官方文档](https://wekan.github.io/docs/)、[REST API](https://wekan.github.io/api/)
- Kanboard：[GitHub](https://github.com/kanboard/kanboard)、[官方文档、API、LDAP、插件](https://docs.kanboard.org/)
- Nextcloud Deck：[GitHub 与移动端说明](https://github.com/nextcloud/deck)、[REST API](https://github.com/nextcloud/deck/blob/master/docs/API.md)
- 4ga Boards：[GitHub](https://github.com/RARgames/4gaBoards)、[Docker/OIDC 变量](https://docs.4gaboards.com/docs/dev/install/docker-vars/)
- Plane：[CE/Commercial 版本说明](https://developers.plane.so/self-hosting/editions-and-versions)、[开发者文档/API/MCP](https://developers.plane.so/)、[开源 CE 功能](https://plane.so/open-source)、[定价和 SSO 边界](https://plane.so/pricing?mode=self-hosted)
- Taiga：[后端仓库](https://github.com/taigaio/taiga-back)、[Docker](https://github.com/taigaio/taiga-docker)、[REST API](https://docs.taiga.io/api.html)
- OpenProject：[Community Edition](https://www.openproject.org/community-edition/)、[API](https://www.openproject.org/docs/api/)、[认证 FAQ](https://www.openproject.org/docs/system-admin-guide/authentication/authentication-faq/)、[Enterprise add-on 矩阵](https://www.openproject.org/docs/enterprise-guide/)、[移动 Beta](https://www.openproject.org/docs/mobile-app-guide/)
- Leantime：[认证配置](https://docs.leantime.io/installation/authentication-configuration)、[FAQ/API/MCP](https://docs.leantime.io/installation/frequently-asked-questions)、[OSS 与插件功能矩阵](https://leantime.io/pricing/)
- PLANKA：[官方文档、当前许可声明](https://docs.planka.cloud/docs/about-planka/)、[部署/API 目录](https://docs.planka.cloud/docs/welcome/)
- Focalboard：[官方仓库和未维护声明](https://github.com/mattermost-community/focalboard)
- Tududi：[官方仓库](https://github.com/chrisvel/tududi)
- Scrumboy：[官方仓库](https://github.com/markrai/scrumboy)
- Kanba：[官方仓库和部署说明](https://github.com/Kanba-co/kanba)
- Taskosaur：[官方仓库](https://github.com/Taskosaur/Taskosaur)
- GitLab：[Self-Managed Issue Boards API](https://docs.gitlab.com/api/boards/)、[Self-Managed Free SAML SSO](https://docs.gitlab.com/integration/saml/)
- Tuleap：[官方用户文档与开源版本说明](https://docs.tuleap.com/user-guide/intro.html)、[公开实例 Kanban](https://tuleap.net/projects/tuleap/kanban)
- 禅道：[官方仓库](https://github.com/easysoft/zentaopms)
- Phorge：[官方站点](https://we.phorge.it/)、[GitHub 只读镜像](https://github.com/phorgeit/phorge)
- Redmine：[官网](https://www.redmine.org/)、[GitHub SVN 镜像](https://github.com/redmine/redmine)

## 10. 待 PoC 核实问题

文档研究无法替代以下实测：

1. Kan、Vikunja、4ga 在 Authentik 与 LLNG 下的 JIT 建号、账号冲突、组/角色映射和移动端 OIDC。
2. 三者在 1,000/10,000 cards、50 并发和大附件下的 CPU、RAM、数据库查询及 WebSocket 行为。
3. 固定前一 patch/minor 的升级与回滚；migration 失败后能否安全重试。
4. API token 的权限粒度、撤销、审计和备份恢复后的有效性。
5. Kan MCP 是否能限制 destructive tool、workspace 与 board 范围；提示注入内容是否会导致越权操作。
6. Deck 1.17.x 与 Nextcloud 34 的明确兼容范围、REST reorder 契约和移动端功能差异。
7. Leantime 当前 release 中 OIDC/LDAP/API/MCP 各自究竟属于核心、免费插件还是付费 bundle。

完成这些 PoC 后再把推荐从“文档预选”升级为 ANAS 稳定 cask 决策。
