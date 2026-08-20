---
doc_type: research
created: 2026-08-10
updated: 2026-08-13
evidence_as_of: 2026-08-13
---

# 开源自部署 Kanban 应用全景调研

本报告按[应用研究文档规范 v1.1](/developer/research-document-standard)研究 Kanban 主题。首轮事实采集于 2026-08-10；2026-08-13 按规范补做商业基准反向发现、目录补漏、社区/付费边界和同日动态指标刷新。报告是选型快照，不是当前部署说明。

```yaml
topic: kanban
title: 开源自部署 Kanban 应用
snapshot_date: 2026-08-13
decision_for: ANAS Runtime Module 候选
must_be:
  - 可获得源代码并允许自行部署
  - 提供多人 Kanban，或在相邻类别明确标注单人/套件属性
  - 上游、许可证和可用部署路径仍可访问
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  architectures: [amd64, arm64]
target_users: 家庭、小团队、研发团队
expected_scale: 5-50 用户，单实例 1k-10k cards
questions:
  - 哪些项目值得进入 Module PoC？
  - 社区自部署版能否接 ANAS IAM？
  - 能否复用关系数据库、Nextcloud、入口与备份能力？
```

## 1. 结论先行

不存在一个适合所有 ANAS 用户的“最佳 Kanban”。按当前项目能力和部署成本，建议分成三条产品路径：

1. **默认独立 module：Vikunja**。成熟度、资源占用、OIDC、LDAP、REST API/Webhook、迁移能力和 Go 单体部署之间最平衡。它不只是纯看板，适合个人和中小团队的任务、日历、Gantt/表格需求。
2. **现代纯看板优先：Kan**。界面和 Trello 心智最接近，AGPL、PostgreSQL、Docker Compose、通用 OIDC、API Key 和原生 MCP 都很贴合 ANAS；但项目始于 2023 年，成熟度和升级历史弱于 Vikunja/Wekan，建议先标为 `experimental`。
3. **零新增服务：Nextcloud Deck**。ANAS 已有 Nextcloud、SAML/LDAP、移动端和文件体系，启用 Deck 应用即可获得最低运维成本。短板是大型看板性能、工作流深度和 API 稳定性不如专用系统。
4. **新增的轻量现代候选：Kaneo**。规范补漏发现它已具备 MIT、PostgreSQL、Docker、通用 OIDC 与 OpenAPI，值得与 Kan 同批做 experimental PoC；但项目不足两年，暂不替代成熟度更高的 Vikunja。

备选：

- **Wekan**：最成熟、最完整的 Trello 式 FLOSS 看板之一，认证和 REST API 丰富；代价是引入 MongoDB/FerretDB，Meteor 运行栈较重。
- **4ga Boards**：现代、实时、OIDC、开发非常活跃，适合作为第二个实验性纯看板候选；历史较短，API/生态仍不如 Kan/Vikunja。
- **Kanboard**：极轻、稳定、API/插件成熟；官方已声明 maintenance mode，且现代 OIDC 通常依赖插件，适合保守型或低配设备而不适合作为默认新产品。
- **Leantime**：如果目标是“目标/里程碑/时间管理 + 看板”而非 Trello 克隆，它与 MariaDB、OIDC/LDAP 的匹配很好；需要持续核对免费核心与付费插件边界。

不建议作为首个 module：Plane、OpenProject、Taiga、Huly。它们是完整项目管理套件，依赖和运维面明显更大；Plane/OpenProject 社区版的企业 SSO 边界也不符合 ANAS 当前“开源版即可接 IAM”的理想契约。

## 2. 范围、发现方法与局限

### 2.1 纳入范围

本报告把候选分成五类：

- 以 Kanban/Trello 替代为核心的多人 Web 应用；
- 同时提供看板的开源任务/项目管理套件；
- 已在 ANAS 生态内或能显著复用现有基础设施的应用；
- 源码可见/开放核心但不满足严格开源定义的应用；
- 经常出现在推荐列表、但已停更或已改为非开源许可证的项目，用于排雷。

“当前所有”无法做数学意义的穷尽：目录标签可能错误，个人仓库每天都可能出现。本次核对 40 余个有名称的项目；仅静态单机板、编辑器插件、纯白板和 SaaS-only 产品进入排除项而不进入严格开源排名。

### 2.2 发现来源与检索边界

本轮使用了相互独立的四类来源，并打开 AlternativeTo 第二页：

- [AlternativeTo：Trello 的 Open Source + Self-Hosted alternatives](https://alternativeto.net/software/trello/?license=opensource&platform=self-hosted)及[第二页](https://alternativeto.net/software/trello/?license=opensource&p=2&platform=self-hosted)；
- 用户给出的 [AlternativeTo：Kanba alternatives](https://alternativeto.net/software/kanba/?license=opensource&platform=self-hosted)；
- [awesome-selfhosted](https://awesome-selfhosted.net/) 与 [selfh.st Apps](https://selfh.st/apps/) 的 task/project management 分类；
- [Opensource.com 的 Trello 替代文章](https://opensource.com/alternatives/trello)、GitHub/GitLab 搜索、候选官方 alternatives/竞品页。

目录只用于发现。许可证、部署、认证、API、商业边界和维护状态均回到上游文档、仓库或配置核验。第二页补入 **Kaneo、Operately、Fira、Koge Kanban**；进一步搜索补入 **Tasks.md**。

### 2.3 头部商业基准反向发现

| 商业基准 | 核心比较场景 | 反向发现或确认的开源候选 |
| --- | --- | --- |
| Trello | 最低学习成本的 board/list/card、Power-Ups、移动端 | Wekan、Kan、PLANKA、4ga Boards、Kaneo、Kanboard |
| Jira | Issue、workflow、Scrum、报表、研发集成 | Plane、OpenProject、Taiga、Tuleap、禅道、GitLab |
| Asana | List/Board/Timeline、任务依赖、团队协作 | Vikunja、Leantime、Plane、OpenProject |
| Monday.com | 多视图、自定义字段、自动化和 dashboard | Plane、OpenProject、Huly、ERPNext/Odoo |
| ClickUp | 任务、文档、目标、时间与 AI 一体化 | Plane、Huly、Leantime、Operately |
| Linear | 现代研发 UX、cycles、快捷键、API | Plane、Kaneo、Kan、Huly |

这些闭源产品只建立用户场景与发现入口，不参与严格开源排名。

### 2.4 指标口径

- Star、创建日、最后推送时间、许可证和归档标志来自 GitHub 官方仓库 API，动态快照刷新到 2026-08-13；未在同日成功获取的值明确留空。
- “活跃度”综合最近推送、发行节奏和项目自述。仓库有自动化提交不等于健康社区，故只分为高/中/低/停止。
- “成熟度”综合项目年龄、升级历史、文档、API、部署方式和维护状态，不等同于功能多少。
- “社区版 SSO”只认免费自部署版本可用的 LDAP、OIDC 或 SAML；社交登录不自动等于通用企业 SSO。
- AI 统一按规范分为 A 原生可自选模型、B 原生但受限、C 可通过稳定 API/Webhook/MCP 集成、D 只能靠不稳定接口或 UI 自动化。
- 移动平台区分原生 App、桌面封装和响应式 Web/PWA，避免把“手机浏览器可打开”写成“有 App”。

### 2.5 候选台账摘要

| 分层 | 项目 | 发现来源与去向 |
| --- | --- | --- |
| 核心 | Kan、Vikunja、Deck、Wekan、4ga、Kanboard、Kaneo、Leantime、Taiga、Plane CE、OpenProject CE、Tududi、Scrumboy、Kanba | 严格开源、可自部署且看板是核心能力；进入产品/部署表 |
| 相邻 | Huly、Operately、Tasks.md、GitLab、Gitea、Forgejo、Tuleap、禅道、Redmine、Phorge、ERPNext、Odoo、Super Productivity | 套件、研发平台、文件型单人板或客户端；单列，不与纯看板同分 |
| 源码可见 | PLANKA、Taskosaur | 当前许可证不属于严格开源；单列商业/许可风险 |
| 停止/不成熟 | Focalboard、Taskcafe、Restyaboard、Lavagna、TaskBoard、Fira、Koge Kanban | 明确停更、长期低活动或生产证据不足 |
| 排除 | Trello、Jira、Asana、Monday、ClickUp、Linear、Notion board、GitHub Projects | SaaS/闭源或不可独立自部署；仅作商业基准 |

## 3. 严格开源核心候选

符号：✅ 官方原生；◐ 有限制、插件或早期；❌ 官方明确不支持；— 未确认或不适用。平台项不把移动网页写成原生 App；Star 为 2026-08-13 同日快照。

### 3.1 产品能力与平台

| 项目 | 定位 / 许可证 | Web | 桌面 | Android | iOS | 社区版 SSO | API / 自动化 | AI 等级 | Star | 活跃 / 成熟 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | ---: | --- |
| **Vikunja** | 任务 + 多视图；AGPL-3.0 | ✅ 完整 | ✅ 官方封装 | ◐ 官方、基础功能 | ◐ 官方、基础功能 | ✅ OIDC、LDAP | ✅ REST/OpenAPI、Webhook、OAuth2、n8n | C | 5,050 | 高 / 成熟 |
| **Kan** | 现代 Trello 替代；AGPL-3.0 | ✅ 响应式 | — | — | — | ✅ 通用 OIDC | ✅ API Key；官方 MCP 46 tools | C（MCP） | 5,343 | 高 / 成长 |
| **Kaneo** | 轻量现代 PM/Kanban；MIT | ✅ 响应式 | — | — | — | ✅ 通用 OAuth2/OIDC | ✅ OpenAPI API | C | 8,206 | 高 / 早期 |
| **Nextcloud Deck** | Nextcloud 看板 app；AGPL-3.0 | ✅ 完整 | — | ✅ 官方 | ✅ 官方 | ✅ 继承 Nextcloud LDAP/SAML/OIDC | ✅ OCS REST；活动/日历/文件 | C | 1,412 | 高 / 成熟 |
| **Wekan** | 完整 Trello 克隆；MIT | ✅ PWA | ◐ Snap/桌面式包 | — | — | ✅ LDAP、OAuth2/OIDC 等 | ✅ REST、Webhook、导入导出 | C | 21,026 | 高 / 成熟 |
| **4ga Boards** | 轻量实时纯看板；MIT | ✅ 响应式 | — | — | — | ✅ 通用 OIDC | ◐ 公开 API 契约仍弱 | D/C 边界 | 689 | 高 / 成长 |
| **Kanboard** | 极简传统 Kanban；MIT | ✅ 响应式 | — | ◐ 第三方 | ◐ 第三方 | ✅ LDAP；◐ OIDC/SAML 插件 | ✅ JSON-RPC、Webhook、插件/事件 | C | 9,791 | 中 / 成熟；maintenance mode |
| **Leantime** | 目标导向 PM + 看板；AGPL-3.0 | ✅ PWA | — | — | — | ◐ OIDC、LDAP；插件边界需锁版 | ✅ JSON-RPC；插件；无核心 Webhook | B/C（插件） | 11,322 | 高 / 成熟 |
| **Taiga** | Scrum + Kanban；MPL-2.0 | ✅ 完整 | — | — | — | ◐ 通用 SSO 多依赖社区插件 | ✅ REST、Webhook、GitHub/GitLab | C | 844（后端） | 中高 / 成熟 |
| **Plane CE** | Linear/Jira 式 PM；AGPL-3.0 | ✅ PWA | ◐ 官方设备文档 | ◐ 自部署 CE 逐版核对 | ◐ 自部署 CE 逐版核对 | ❌ CE 无通用 OIDC/SAML/LDAP | ✅ 180+ REST、Webhook、OAuth App | B/C；AI entitlement 需锁版 | 55,901 | 高 / 成长为成熟 |
| **OpenProject CE** | 企业 PM + 基础 board；GPL-3.0 | ✅ 完整 | — | ◐ 官方 Beta | ◐ 官方 Beta | ◐ LDAP；❌ OIDC/SAML 为 Enterprise | ✅ REST v3/OpenAPI | B/C；MCP 为 Enterprise | 15,836 | 高 / 成熟 |
| **Tududi** | 任务/项目/笔记 + Kanban；MIT | ✅ 响应式 | — | ◐ 经 CalDAV 客户端 | ◐ 经 CalDAV 客户端 | ✅ OIDC | ◐ CalDAV 强；公共业务 API 生态小 | A/B：OpenAI Key，模型范围需核对 | 3,242 | 高 / 早期 |
| **Scrumboy** | 实时看板/Issue tracker；AGPL-3.0 | ✅ PWA | — | — | — | ✅ OIDC | ✅ API、MCP | C（MCP） | 428 | 高 / 很早期 |
| **Kanba** | maker/小团队轻看板；MIT | ✅ 响应式 | — | — | — | — Supabase Auth；通用 OIDC 未确认 | ◐ 无稳定公开业务契约 | D | 640 | 高 / 很早期 |

### 3.2 部署、数据与 ANAS 适配

| 项目 | 官方自部署拓扑 | 必须依赖 | 可选/联网依赖 | 数据与备份面 | 镜像/架构证据 | ANAS 适配 |
| --- | --- | --- | --- | --- | --- | --- |
| **Vikunja** | 单容器/单二进制 | SQLite 或 PostgreSQL/MySQL | SMTP、S3/搜索等可选 | DB + attachments/config | 官方 Docker/二进制；amd64/arm64 | **A**：最小拓扑、Go、关系数据库、OIDC 都匹配 |
| **Kan** | web + migration | PostgreSQL | Redis、SMTP、S3、MCP client 可选 | PG + 对象/附件 + secret | 官方 GHCR/Compose；双架构需锁版确认 | **A**：PG/OIDC/API/MCP 契约清楚；先 experimental |
| **Kaneo** | web/API + PostgreSQL | PostgreSQL | OAuth providers、SMTP | PG + 上传/配置 secret | 官方 Docker/Compose；双架构需 PoC | **A**：PG、通用 OIDC、OpenAPI 高度匹配；不足两年，先 experimental |
| **Deck** | 现有 Nextcloud app | Nextcloud 及其 DB/文件 | App Store 或离线 app 包 | 复用 Nextcloud DB/files/config | 跟随 ANAS Nextcloud 镜像 | **A**：零新增服务；大板性能和 app 兼容要压测 |
| **Wekan** | Meteor app + MongoDB | MongoDB（或上游验证过的兼容后端） | SMTP、Webhook | MongoDB + attachments/config | 官方多架构镜像/Snap | **B**：成熟，但新增 document DB 与 Meteor 运维面 |
| **4ga** | 官方 Compose 多服务 | 以锁定 Compose 为准 | OIDC/SMTP | DB + attachments/config | 官方 Docker；架构需 PoC | **B**：OIDC 好、年轻；API 与恢复契约待验证 |
| **Kanboard** | PHP app；可单容器 | SQLite/MySQL/PostgreSQL | SMTP、插件 | DB + data/plugins/config | 官方镜像；架构需锁版确认 | **B**：极轻且 API 强；功能处于维护模式 |
| **Leantime** | PHP app + DB | MySQL/MariaDB | OIDC/LDAP/AI/MCP 插件、SMTP | DB + userdata + plugins | 官方 Docker；架构/插件 entitlement 需核对 | **B**：MariaDB 复用好；免费插件边界是风险 |
| **Taiga** | front/back/events/async + 基础服务 | PostgreSQL、RabbitMQ/Redis 等 | SMTP、第三方 SSO 插件 | PG + media + queue/config | 官方 taiga-docker；多架构需 PoC | **B**：成熟可用，但拓扑和 IAM 维护成本高 |
| **Plane CE** | web/api/worker/live + PG/Redis/MinIO | PostgreSQL、Redis、对象存储 | AI/集成外联 | PG + object storage + secrets | 官方 Docker/K8s；最低约 2 CPU/4 GB | **C**：API 强但重，CE SSO 是硬缺口 |
| **OpenProject CE** | 官方包/容器 + PostgreSQL | PostgreSQL | 缓存、SMTP、企业 add-ons | PG + attachments/config | 官方 Docker/包/K8s | **C**：专业且重；社区版 OIDC/SAML/MCP 缺失 |
| **Tududi** | 应用容器 | 内置数据存储（锁版确认） | CalDAV、Telegram、OpenAI | DB + config/secrets | 官方 Docker；架构需 PoC | **B**：轻、OIDC、AI；多人/恢复历史仍短 |
| **Scrumboy** | 官方容器 | 以锁定 release Compose 为准 | OIDC、MCP/语音 | DB/files/config | GHCR；双架构未确认 | **B（观察）**：契约方向好，生产历史不足 |
| **Kanba** | Next.js + Supabase 拓扑 | Supabase DB/Auth/Storage | Stripe 仅托管业务；自托管可不配 | Supabase 数据/对象/secret | 自建路径可用；官方镜像/架构证据弱 | **C**：比传统 PG module 多一层 Supabase 运维 |

### 3.3 相邻候选与套件型替代

这些系统有看板，但不应只为看板而部署：

| 项目 | 看板特点 | SSO/API/AI | Star / 活跃度 | ANAS 判断 |
| --- | --- | --- | --- | --- |
| **Gitea**（MIT） | Issues/Projects 看板适合研发团队 | LDAP/OAuth2/OIDC；完整 REST API；可接外部 AI | 57,351 / 很高 | 若未来已有 Git module，顺带使用最合理；不是通用业务看板首选 |
| **Forgejo**（GPL-3.0+，主要在 Codeberg） | Gitea 系谱的项目看板 | LDAP/OAuth2/OIDC；API | 活跃成熟 | 比 Gitea 更社区治理；同样仅推荐给代码研发场景 |
| **ERPNext**（GPL-3.0） | DocType/任务的 Kanban 视图 | OAuth/LDAP、REST、Frappe 扩展 | 38,016 / 很高 | 只有同时需要 ERP 时才合理；部署和数据模型远超看板需求 |
| **Odoo Community**（LGPL 核心） | Project/CRM 等模型的 Kanban 视图 | XML-RPC/JSON-RPC；认证和 AI 功能存在版本/企业版边界 | 53,691 / 很高 | 适合已有 Odoo 的组织，不建议作为独立看板 module |
| **Super Productivity**（MIT） | 个人 Kanban、时间盒、时间跟踪；GitHub/GitLab/Jira/OpenProject 集成 | 本地优先/文件同步，非标准多人服务 API/SSO | 21,319 / 很高 | 更像跨平台个人客户端，不符合 ANAS 多用户服务 module 主目标 |
| **Huly**（EPL-2.0） | Issue、文档、聊天、日历等一体化 | 自托管认证/API/AI 边界需专项核验 | 同日 API 限流，未列 Star / 活跃 | 系统和资源面远超轻量看板 |
| **Operately**（许可证需按 LICENSE 复核） | 公司 operating system：Goals、Projects、check-ins | API token/CLI 可供 AI agent；自部署 SSO 未确认 | 539 / 高、成长 | 适合 OKR/公司治理，不是 Trello 式纯看板 |
| **Tasks.md**（MIT） | 单实例 Markdown 文件型看板 | 无标准多人 IAM；直接以文件/容器卷自动化 | 2,180 / 中 | 低资源、Git/AI 友好，但不满足统一多用户默认项 |
| **GitLab Self-Managed Free/CE** | Issue Boards 与代码、MR、Milestone 结合；Free 层有项目看板 | ✅ REST Board API；✅ Self-Managed Free 的实例级 SAML/LDAP/OmniAuth；AI 多为付费能力 | GitLab 主仓库在 GitLab.com，不用 GitHub Star横比 | 已有 GitLab 时直接用；只为看板部署过重 |
| **Tuleap**（GPL-2.0-or-later） | 完整 ALM，Tracker、Scrum、Kanban、Git、测试和文档 | REST API；官方称不区分 Enterprise/Community 功能，认证仍需按发行版核对 | 官方 Git 仓库不以 GitHub API 计数 | 软件研发和强追踪场景成熟，但明显不是轻量看板 |
| **禅道 ZenTao 开源版** | 中国团队常用 Scrum/Kanban/需求/缺陷/测试套件 | API 与企业集成因版本/版本线而异；SSO/AI 需核对开源版边界 | 1,667 / 高 | 本地化强；ZPL/产品线和免费功能边界需法务及 PoC 单独核实 |
| **Phorge**（Apache-2.0） | Phabricator 的社区延续，Workboards 面向研发任务 | Conduit API；认证扩展丰富；无现代原生 AI | 196 / 活跃镜像 | 适合既有 Phabricator 用户，不建议新建通用看板实例 |
| **Redmine**（GPL-2.0） | 核心是 Issue/Gantt；Kanban 通常依赖 Agile/看板插件 | REST API、LDAP；OIDC/SAML 和 Kanban 都依赖插件选择 | 6,016（GitHub 镜像）/ 成熟 | 不能把插件能力写成 Redmine 社区核心；仅在已有 Redmine 时考虑 |

### 3.4 社区自部署、官方托管与付费边界

| 项目/组 | 开源社区自部署 | 官方免费托管 | 付费/企业差异 | 自部署限制或隐性外部依赖 |
| --- | --- | --- | --- | --- |
| Kan | AGPL，核心看板、OIDC、API/MCP 可自部署；无 license key | 有，额度以官网当期为准 | 托管容量/服务；未发现社区核心 SSO 被锁 | S3/SMTP/社交登录可外联；MCP client 需 Node/npm |
| Vikunja | AGPL，自部署核心完整；不要求 license key | 有试用/托管产品 | Vikunja Pro 是可选自部署功能集，具体 entitlement 按锁定版本核验 | Pro 启用时会联系 license server；社区核心可离线 |
| Kaneo | MIT，自部署核心、通用 OIDC、API | 官方托管状态/额度未确认 | 未确认是否存在私有企业功能 | 社交 OAuth/SMTP 可外联；本地账号可独立运行 |
| Nextcloud Deck | AGPL app，依附任一社区 Nextcloud | 各托管商而非 Deck 单独套餐 | Nextcloud 企业支持不改变 Deck 开源许可 | App Store 联网可用离线包替代；继承 Nextcloud 的限制 |
| Wekan / Kanboard / 4ga | MIT 社区自部署，无已知 license key | Wekan 有商业托管/支持；其余不统一 | 主要为托管/支持；Kanboard 功能依插件生态 | 第三方插件必须单独核验许可、维护和 API 兼容 |
| Leantime | AGPL 核心可自部署 | 有云服务 | Advanced Auth、AI/MCP、自定义字段等存在插件/Pro 边界 | Marketplace 插件可能闭源或需订阅；必须锁版验 entitlement |
| Plane CE | AGPL CE，无社区用户上限 | Cloud Free 有独立额度 | Commercial/Pro/Business/Enterprise 增加 SSO、工作流、LDAP、审计、AI 配额等 | CE、Commercial 是不同代码/发行线；不要把 Cloud Free 功能写成 CE |
| OpenProject CE | GPL CE，可免费自部署 | Cloud 试用/套餐 | OIDC/SAML、MCP、组同步和多项治理能力为 Enterprise add-on | Enterprise on-prem 需 license；CE API 可独立运行 |
| Taiga | MPL 前后端可自部署 | 官方 SaaS 有免费/付费计划 | 托管容量与支持；企业 SSO主要靠社区插件而非核心付费层 | 插件不是官方稳定契约；部署仍需多项基础服务 |
| Tududi / Scrumboy / Kanba | 社区源码可部署 | 有无托管及额度变化快 | 未确认统一企业矩阵 | AI、Telegram、Supabase、OAuth 等可产生外部依赖；逐 release 核验 |

报告未确认的官方 SaaS 用户数、存储和 AI credits 不外推为社区自部署上限；反过来，仓库存在也不证明同品牌的商业插件属于开源版。

## 4. 源码可见但不再属于严格开源

| 项目 | 当前许可 / 状态 | 功能和风险 | 结论 |
| --- | --- | --- | --- |
| **PLANKA** | Fair Use License + Pro/Enterprise License；12,326 Star；活跃 | 现代 Trello 式 UI、实时协作、OIDC、Swagger/Postman API、Docker/Helm；文档明确称 source available/self-hostable | 可作为商业/源码可见候选，但不能写进“严格开源默认 module” |
| **Taskosaur** | Business Source License；542 Star；活跃、年轻 | 内置对话式 AI，可接 OpenAI/OpenRouter/Anthropic/Ollama，Swagger API，Jira/Trello 同步；许多能力仍标注 planned/working toward | 技术观察，不进入 FLOSS 选型 |

特别提醒：网上较旧的 PLANKA 对比文章仍把它标为 MIT/开源，当前官方文档已改变口径，必须以当前 LICENSE 和官方许可指南为准。

## 5. 停更、维护模式或不建议新部署的项目

| 项目 | Star | 最后有效开发信号 | 状态判断 | 替代建议 |
| --- | ---: | --- | --- | --- |
| **Focalboard standalone** | 26,396 | README 明示 “currently not maintained”；最后正式版 8.0.0（2024） | 不作为新生产部署；桌面端/Server/API 曾经完整 | Kan、Vikunja、Wekan |
| **Taskcafe** | 5,208 | 最后代码推送 2023-07 | 事实停更，虽未归档 | Plane/Kan/Vikunja |
| **Restyaboard** | 2,085 | 最后代码推送 2023-10，最后发行版 2022 | README 称 active 与仓库活动矛盾；旧 PHP/ElasticSearch 部署面大 | Wekan/Kanboard |
| **Lavagna** | 641 | 2024-08，仓库已 archived | 停止 | Kanboard/Vikunja |
| **TaskBoard** | 1,401 | 提交信号稀疏，架构和 UI 较旧 | 低维护，不建议新部署 | Kanboard/4ga Boards |
| **Kanboard** | 9,791 | 2026 仍发布修复 | 不是停更，但官方明确 maintenance mode | 低资源保守场景仍可选 |
| **Fira** | 83 | 最后 push 2025-11；项目创建不足一年 | Markdown/Git 方向有趣，但无持续 release/多人 IAM 生产证据 | Tasks.md 或 Kan |
| **Koge Kanban** | 7 | 2026-04 后无 push；README 自述 AI/vibe-coding 生成 | 极早期，Gemini AI 外联且无成熟运维契约 | Kan/Scrumboy |

### 5.1 明确排除清单

| 项目/类型 | 排除原因 | 是否保留观察 |
| --- | --- | --- |
| Trello、Jira、Asana、Monday.com、ClickUp、Linear | 闭源/SaaS 商业基准，不能自行部署开源服务 | 是，只用于反向发现和 UX 基准 |
| GitHub Projects | SaaS-only，不存在可独立自部署服务 | 否；自部署研发平台看 GitLab/Gitea/Forgejo |
| Notion database board、Obsidian Kanban 插件 | 主题相邻或客户端插件，不是独立多人 Kanban 服务 | 否；分别进入文档/笔记研究 |
| 纯静态 HTML board、编辑器内 Kanban/TUI | 没有多账号服务端、持久化与协作契约 | 仅在“Git/文件型个人任务”专项观察 |
| TaskView Community | source-available，自述 SAML/OIDC/API 但许可证和完整免费拓扑不满足严格开源口径 | 是，放入未来源码可见补录 |

## 6. 重点候选详评

### 6.1 Kan：最匹配 ANAS 的现代纯看板

优势：

- AGPL-3.0，官方 Docker Compose；应用 + migration + PostgreSQL 的职责清楚。
- 通用 OIDC 只需 discovery URL、client ID/secret，能直接对接 ANAS Authentik；若 LLNG OIDC consumer contract 完成，也可接 LLNG。
- 可关闭本地注册和本地凭据，适合受控家庭/团队实例。
- 支持 S3 兼容附件，未来可映射到 ANAS 自托管对象存储；当前也可先禁用或使用本地策略。
- 原生 API Key + 官方 `@kan/mcp`，46 个 MCP 工具覆盖 workspace/board/list/card/comment/checklist/label/member，AI 集成不需要屏幕自动化。
- 依赖 PostgreSQL，ANAS 已有 capability provider，可复用现有数据库 module，而不是再部署内置数据库。

风险：

- 年轻项目，数据库迁移、跨版本回滚、导入大看板和高并发样本不如 Wekan/Vikunja。
- `latest` 示例不能进入 ANAS；需固定 release/digest，验证 amd64/arm64 镜像和 migration 的幂等性。
- 目前无官方原生移动 App，应按响应式 Web/PWA 描述。
- MCP 运行在客户端侧 Node.js 进程；ANAS 若要把它作为长期服务，应单独设计 sidecar 或只发布配置说明。

建议 module：`kan`，`status: experimental`，`requires: traefik`，`requires_capabilities: relational_database/postgres`，`iam.interfaces: oidc`。第一期不引入 S3，先验证本地附件持久化和备份。

### 6.2 Vikunja：默认推荐的稳健方案

优势：

- 后端为 Go，前后端已打包为单二进制/单容器；可接 PostgreSQL、MySQL/MariaDB，拓扑最适合 module。
- OIDC 官方文档直接给出 Authentik、Keycloak 等配置；当前文档也列出 LDAP。
- REST/OpenAPI、Webhook、bot users、n8n、CalDAV、OAuth 2.0 authorization server，自动化边界最完整。
- Web 之外有桌面包和移动 App；官方明确移动端目前只覆盖基础能力，应保留这个限制说明。
- 任务、重复任务、日历、Gantt、表格比纯 Trello 克隆更适合家庭和个人生产力。

风险：

- 不是“只做看板”，用户若只要极简 Trello UI，Kan/4ga/Wekan 更直观。
- OIDC 移动端使用 WebView，有 cookie/state 已知限制；需要用 ANAS 实际 IdP 做 iOS/Android E2E。
- 2026 已出现 Vikunja Pro 可选功能，需对每个版本锁定社区版功能矩阵，避免把付费能力写入 module 契约。

建议 module：`vikunja`，可直接作为 stable 候选。优先 PostgreSQL，提供 `db_type: auto`；OIDC 为第一期身份协议，LDAP 作为备选而不是双重自动建号。

### 6.3 Nextcloud Deck：最低总拥有成本

优势：

- ANAS 已部署 Nextcloud 34、LDAP、SAML、Traefik、PostgreSQL/MariaDB；Deck 只是 Nextcloud app 生命周期，不需要新域名、数据库或账号体系。
- 官方 Android/iOS App 均能连接自托管 Nextcloud；继承 Nextcloud 文件、评论、活动流、日历和 Circles。
- REST API 能读写 board/stack/card/label/ACL/attachment，足以做基础 AI 与自动化。

风险：

- 官方 README 明示高 board/card/attachment 数量会产生大量数据库查询，不适合未经压测的大团队。
- API 文档和实际 endpoint 曾出现不同步问题；集成必须用固定 Deck/Nextcloud 版本做契约测试。
- 工作流、报表、自动化和 WIP 能力弱于专业 PM 套件。

实施建议：不要新增独立 `deck` Compose module。把它作为 `nextcloud` module 的可选 app（例如 `deck_enabled`），在现有 app reconcile 和兼容版本锁中安装，并增加 REST smoke test、Android/iOS 登录说明和大板基准。

### 6.4 Wekan：成熟 Trello 替代

优势是 2014 年以来的深厚功能、21k Star、非常高的发布频率、MIT、REST API、Webhook、WIP/swimlane/custom fields、导入导出和丰富认证。其 2026-08-10 仍有提交和近期发行，不属于“老而停更”。

主要代价是 Meteor + MongoDB。ANAS 当前没有文档型数据库 capability；为一个应用增加 MongoDB 会扩大备份、升级和资源矩阵。Wekan 正在探索 FerretDB/其他后端，但不能在未做上游兼容性验证前把 PostgreSQL 等价替换写进生产设计。

建议：只有当用户明确优先“最完整 Trello 克隆”时提供 `wekan`，同时新建 document database capability 或把 MongoDB 明确设为 Wekan 私有依赖。

### 6.5 4ga Boards：值得跟踪的轻量新秀

官方文档提供 Docker 变量和 OIDC client 配置，实时协作与现代界面强，2026 年仍高频发行。相对 Kan，API/MCP/第三方客户端生态较弱；相对 Wekan，历史和生产规模证据较少。适合 experimental module 与 Kan 做实际 UX/资源 A/B 测试。

### 6.6 Kanboard：低资源和长期稳定场景

单 PHP 应用、SQLite/MySQL/PostgreSQL、完整 JSON-RPC API、Webhook、自动动作和成熟插件体系使它非常适合低配 NAS。LDAP 是核心能力，OIDC/SAML 通常依赖插件或前置代理。官方明确只做小修和接受社区贡献，因此选择它等于接受 UI/功能基本冻结，换取可预测性。

### 6.7 Plane、OpenProject、Taiga、Huly：为什么暂缓

- **Plane**：官方最低 2 vCPU/4 GB，包含 Web/API/worker/live/PG/Redis/MinIO 等服务。CE 核心很强、API/MCP 优秀，但通用 SAML/OIDC/LDAP 属商业层或存在版本差异，和 ANAS IAM 自动注册目标冲突。
- **OpenProject**：企业 PM、Gantt、时间/成本、移动 Beta 和 API 都成熟；社区版只提供基础看板，OIDC/SAML 与 MCP Server 是 Enterprise add-on。适合有正式项目组合管理需求的组织，而非家庭 NAS 默认应用。
- **Taiga**：Scrum 语义最完整，REST API 成熟；部署涉及前后端、async events、PostgreSQL、RabbitMQ/Redis 等，企业 SSO依赖社区插件，升级测试矩阵大。
- **Huly**：把 issue、文档、聊天、邮箱/日历等合并，能力远超当前需求；部署和运维面最大，外部 API/身份契约需要专项研究。

### 6.8 Kanba：参考项目本身的判断

Kanba 是 MIT、源码开放、自托管的年轻项目，强调简洁、速度、无限项目和小团队协作。当前官方部署说明以 Next.js + Supabase（数据库、Auth、存储/Edge 能力）为中心，并保留托管业务所需的 Stripe 配置。作者说明自托管可不使用 Stripe，但这仍不是 ANAS 现有 PostgreSQL capability 可直接复用的传统单体拓扑。

它目前没有 Kan 那样清晰的通用 OIDC、稳定公开业务 API 和 MCP 契约，也没有原生移动 App。可以继续观察 UI 和产品方向，但如果现在落 module，需要额外承担 Supabase 组件、认证映射与迁移维护；综合优先级低于名称相近但技术契约更完整的 **Kan**。

### 6.9 Kaneo：规范补漏后进入 PoC 的候选

Kaneo 是 2024-12 创建的 MIT 项目，定位介于 Kan 的纯看板和 Plane 的完整项目管理之间。官方文档明确提供 Docker、自托管 PostgreSQL、GitHub/Google/Discord 和通用 OAuth2/OIDC；API 文档由 OpenAPI 自动生成。这使它能直接消费 ANAS PostgreSQL 与 IAM，而不需要商业 SSO license。

风险来自年龄而不是基本契约：项目不足两年，8k Star 增长很快，但 Star 不能替代升级、备份、安全响应和多人生产历史。建议与 Kan 同批建 `kaneo` experimental PoC，验证镜像双架构、数据库 migration 幂等、附件落点、API token 权限、禁止开放注册、OIDC JIT/登出、前一版本升级和空机恢复。通过后再决定 Kan 与 Kaneo 是否只保留一个同质 Module。

## 7. ANAS 落地方案

### 7.1 推荐顺序

| 阶段 | 动作 | 完成标准 |
| --- | --- | --- |
| 1 | 在现有 Nextcloud module 增加 Deck 可选 app | 新装/升级/禁用可 reconcile；REST smoke；SAML/LDAP 与移动端验证 |
| 2 | 新增 `vikunja` experimental module | PG/MariaDB 二选一、OIDC、备份恢复、升级回滚、API/Webhook 全通过 |
| 3 | 新增 `kan` experimental module | PostgreSQL、OIDC、Trello import、附件、API Key/MCP、迁移幂等通过 |
| 3 | 同批验证 `kaneo` experimental module | PostgreSQL、OIDC、OpenAPI、附件和升级恢复通过；与 Kan 二选一 |
| 4 | 对 Kan/Kaneo/Vikunja/Deck 做真实用户与资源对比 | 记录 idle/load RAM/CPU、首屏、1k/10k card、移动体验、恢复时间 |
| 5 | 按需求决定 Wekan/4ga/Kanboard | 只有明确用户场景再扩充，避免一次维护多个同质 module |

### 7.2 module 契约要求

所有候选必须满足：

- 固定上游版本和镜像 digest；验证 amd64 与 arm64。
- 不直接复制上游一体化 Compose 的数据库，优先声明 ANAS `relational_database` capability；确实需要 MongoDB 时单独建能力边界。
- `requires: traefik`，域名/HTTPS/上传限制走 module 参数；禁止把数据库、OIDC、SMTP secret 暴露给无关容器。
- 身份声明必须与真实协议一致：Kan/Vikunja 先声明 OIDC；Deck 继承 Nextcloud；不得用 oauth2-proxy 冒充应用内账号生命周期已经集成。
- 测试首次登录、JIT 建号、禁用本地注册、用户名/邮箱冲突、登出和 IdP 不可用降级。
- 为 API 创建最小权限服务账号或 token；AI 不得默认获得管理员 token。
- MCP/AI 设为显式 opt-in，单独 secret，记录 outbound endpoint；支持本地模型时优先允许用户配置 OpenAI-compatible base URL。
- 备份至少覆盖数据库、附件和必要配置；恢复后验证 board/card/comment/attachment、OIDC subject 映射和 API token。
- 升级探针覆盖数据库 migration、前一 patch、至少一个前一 minor；数据破坏版本写入 `upgrade.data_breaking`。

### 7.3 可复算的文档预评分

| 维度 | 权重 | 原因 |
| --- | ---: | --- |
| 社区版 IAM（OIDC/LDAP/SAML） | 20% | ANAS 已有统一身份能力，不能退回重复账号 |
| 部署、升级、备份复杂度 | 20% | NAS 产品的长期运维成本高于一次性安装体验 |
| API/Webhook/MCP | 15% | 决定自动化和 AI 能否使用稳定机器接口 |
| 看板与协作 UX | 15% | 核心用户价值 |
| 活跃度、安全响应、成熟度 | 15% | 降低弃坑和升级风险 |
| 移动与多平台 | 10% | 家庭/团队任务经常在手机录入 |
| 资源占用 | 5% | NAS 需要与文件、IAM、数据库等服务共存 |

每项按 `0-5` 打分：`0` 明确不支持，`1` 严重缺口，`2` 依赖商业层/高成本插件，`3` 可用但有限制，`4` 完整且有文档，`5` 与 ANAS 契约高度匹配且证据成熟。总分公式为 `Σ(分项 / 5 × 权重)`。这只是文档证据分，不把 Star 直接换算成分数。

| 项目 | IAM 20 | 运维 20 | API 15 | UX 15 | 活跃成熟 15 | 平台 10 | 资源 5 | 总分 /100 | 主要扣分依据 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| Deck | 5 | 5 | 3.5 | 3.5 | 4.5 | 5 | 5 | 89.5 | API/大型看板性能与专业工作流有限 |
| Vikunja | 4 | 4.5 | 5 | 4 | 4.5 | 3.5 | 4.5 | 86 | 移动端仅基础功能；OIDC WebView 需实测 |
| Kan | 5 | 4 | 5 | 4.5 | 3.5 | 2 | 4 | 83 | 年轻、无原生移动端、升级历史较短 |
| Kaneo | 5 | 4 | 4 | 4 | 3 | 2 | 4 | 77 | 不足两年，运维与生产案例仍少 |
| Wekan | 4 | 2.5 | 4.5 | 4.5 | 5 | 2.5 | 2.5 | 75.5 | MongoDB/Meteor 新栈、无可靠原生移动 App |
| 4ga | 5 | 4 | 2.5 | 4 | 3.5 | 2 | 4 | 74 | 公共 API/自动化契约和生产历史较弱 |
| Kanboard | 2.5 | 4.5 | 5 | 3 | 3 | 2 | 5 | 70 | OIDC/SAML 依赖插件；maintenance mode |
| Leantime | 3.5 | 3.5 | 3 | 4 | 4 | 2.5 | 3.5 | 69.5 | Auth/API/AI 插件与付费边界需锁版 |
| Plane CE | 1 | 2 | 5 | 5 | 5 | 3 | 1.5 | 64.5 | CE 无通用 SSO；多服务、资源高 |

Deck 的高分表示“对现有 ANAS 的增量成本最低”，不是说它在纯看板功能上胜过 Kan/Wekan。PoC 后用实测数据替换运维、平台和资源分；若需求是独立服务，比较时应排除 Deck 再排序。

## 8. GitHub 动态指标快照

数据来自各仓库 `https://api.github.com/repos/{owner}/{repo}`，统一刷新于 2026-08-13。`pushed_at` 只表示仓库事件，不代表稳定 release；open issues 也可能包含 PR。Huly 请求遇到未认证 API 限流，因此同日值留空而不混用 8 月 10 日旧值。

| 仓库 | Star | 创建 | 最近 push | 归档 | GitHub API 许可证标识 |
| --- | ---: | --- | --- | --- | --- |
| `makeplane/plane` | 55,901 | 2022-11 | 2026-08-13 | 否 | AGPL-3.0 |
| `hcengineering/platform` | — | — | — | — | 同日请求限流；项目声明 EPL-2.0 |
| `mattermost-community/focalboard` | 26,396 | 2020-10 | 2026-05-18 | 否 | 未识别；README 明示未维护 |
| `wekan/wekan` | 21,026 | 2014-01 | 2026-08-13 | 否 | MIT |
| `opf/openproject` | 15,836 | 2012-11 | 2026-08-13 | 否 | GPL-3.0 |
| `plankanban/planka` | 12,343 | 2019-08 | 2026-08-10 | 否 | 未识别；官方为 Fair Use/商业许可 |
| `Leantime/leantime` | 11,322 | 2015-01 | 2026-08-11 | 否 | AGPL-3.0 |
| `kanboard/kanboard` | 9,791 | 2014-01 | 2026-08-11 | 否 | MIT |
| `usekaneo/kaneo` | 8,206 | 2024-12 | 2026-08-12 | 否 | MIT |
| `kanbn/kan` | 5,343 | 2023-10 | 2026-08-12 | 否 | AGPL-3.0 |
| `JordanKnott/taskcafe` | 5,208 | 2020-06 | 2023-07-23 | 否 | MIT |
| `go-vikunja/vikunja` | 5,050 | 2018-11 | 2026-08-13 | 否 | AGPL-3.0 |
| `chrisvel/tududi` | 3,242 | 2023-11 | 2026-08-11 | 否 | MIT |
| `BaldissaraMatheus/Tasks.md` | 2,180 | 2023-02 | 2026-03-08 | 否 | MIT |
| `RestyaPlatform/board` | 2,086 | 2015-06 | 2023-10-26 | 否 | OSL-3.0 |
| `nextcloud/deck` | 1,412 | 2017-01 | 2026-08-13 | 否 | AGPL-3.0 |
| `kiswa/TaskBoard` | 1,401 | 2014-10 | 2025-05-23 | 否 | MIT |
| `easysoft/zentaopms` | 1,667 | 2011-01 | 2026-07-30 | 否 | 未识别；ZPL/版本线需单独核实 |
| `taigaio/taiga-back` | 844 | 2021-04 | 2026-08-04 | 否 | MPL-2.0 |
| `RARgames/4gaBoards` | 689 | 2023-01 | 2026-08-13 | 否 | MIT |
| `digitalfondue/lavagna` | 641 | 2014-10 | 2024-08-06 | 是 | GPL-3.0 |
| `Kanba-co/kanba` | 640 | 2025-06 | 2026-08-08 | 否 | MIT |
| `Taskosaur/Taskosaur` | 544 | 2025-07 | 2026-07-21 | 否 | 未识别；实际 BSL |
| `operately/operately` | 539 | 2023-02 | 2026-08-12 | 否 | 未识别；按 LICENSE 复核 |
| `markrai/scrumboy` | 428 | 2026-03 | 2026-08-13 | 否 | AGPL-3.0 |
| `phorgeit/phorge`（只读镜像） | 196 | 2022-09 | 2026-08-12 | 否 | Apache-2.0 |
| `Onix-Systems/Fira` | 83 | 2025-09 | 2025-11-13 | 否 | Apache-2.0 |
| `dezuhan/Koge-Kanban` | 7 | 2025-12 | 2026-04-01 | 否 | MIT |
| `redmine/redmine`（官方 SVN 镜像） | 6,016 | 2012-11 | 2026-08-03 | 否 | 未识别；项目 GPL-2.0 |

旁系大型平台同日快照：`go-gitea/gitea` 57,351 Star、`odoo/odoo` 53,691、`frappe/erpnext` 38,016、`super-productivity/super-productivity` 21,319；这些 Star 不能归因于看板模块本身。GitLab/Tuleap/Forgejo 的权威仓库不在 GitHub，未用非官方镜像 Star 横比。

## 9. 主要来源

### 发现入口

- [AlternativeTo：Kanba 的开源自托管替代](https://alternativeto.net/software/kanba/?license=opensource&platform=self-hosted)
- [AlternativeTo：Trello 的开源自托管替代](https://alternativeto.net/software/trello/?license=opensource&platform=self-hosted)及[第二页](https://alternativeto.net/software/trello/?license=opensource&p=2&platform=self-hosted)
- [selfh.st Apps](https://selfh.st/apps/)（目录每日更新，活动色只作发现信号）
- [awesome-selfhosted](https://awesome-selfhosted.net/)
- [Opensource.com：Trello alternatives](https://opensource.com/alternatives/trello)

### 官方仓库和功能文档

- Kan：[GitHub](https://github.com/kanbn/kan)（README 含 Compose、OIDC、API key、MCP）
- Kaneo：[GitHub](https://github.com/usekaneo/kaneo)、[通用 OAuth/OIDC](https://kaneo.app/docs/core/social-providers/custom-oauth)、[OpenAPI API](https://kaneo.mintlify.app/api-reference/introduction)
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
- Tasks.md：[官方仓库](https://github.com/BaldissaraMatheus/Tasks.md)
- Operately：[官方仓库](https://github.com/operately/operately)、[自托管 API token/AI agent](https://operately.com/help/use-with-openclaw/)
- Fira：[官方仓库](https://github.com/Onix-Systems/Fira)、[产品说明](https://fira.onix.team/)
- Koge Kanban：[官方仓库](https://github.com/dezuhan/Koge-Kanban)
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
2. Kaneo 在 Authentik/LLNG 下的通用 OIDC、禁止注册、API token、附件与 migration 行为，并与 Kan 做同数据集 A/B。
3. Kan/Kaneo/Vikunja/4ga 在 1,000/10,000 cards、50 并发和大附件下的 CPU、RAM、数据库查询及 WebSocket 行为。
4. 固定前一 patch/minor 的升级与回滚；migration 失败后能否安全重试；amd64/arm64 镜像是否同 digest release 发布。
5. API token 的权限粒度、撤销、审计和备份恢复后的有效性。
6. Kan MCP 是否能限制 destructive tool、workspace 与 board 范围；提示注入内容是否会导致越权操作。
7. Deck 1.17.x 与 Nextcloud 34 的明确兼容范围、REST reorder 契约和移动端功能差异。
8. Leantime、Vikunja Pro、Plane CE/Commercial 当前 release 的社区/付费 entitlement 与是否联网校验。

完成这些 PoC 后再把推荐从“文档预选”升级为 ANAS 稳定 module 决策。

## 11. 更新周期

在 2026-11-13 前，或在实施任一候选 Module 前，重新采集许可证、Star、创建/最近 push、稳定 release、镜像架构、SSO/API/AI entitlement 和社区/商业边界。优先关注 Kaneo 的升级与生产历史、Kan MCP 权限收敛、Vikunja Pro 对社区能力的影响、PLANKA 许可/SSO 变化、Plane CE 与 Commercial 的发行差异、OpenProject CE 的 board/SSO/MCP 边界，以及 Fira/Koge 是否恢复持续维护。
