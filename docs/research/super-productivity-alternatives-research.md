---
doc_type: research
created: 2026-08-15
updated: 2026-08-15
evidence_as_of: 2026-08-15
---

# Super Productivity 与同类开源自部署项目调研

本报告按[应用研究文档规范](/developer/research-document-standard)研究以 Super Productivity 为代表的“个人任务、时间规划与工时跟踪”应用，为 ANAS 后续 Runtime Module 选型提供依据。动态数据采集于 2026-08-15；报告是研究快照，不是当前部署说明。

## 1. 结论先行

这个品类不存在一个在“个人任务、日历时间块、Pomodoro、工时统计、开发工具集成、离线、多端、自部署、多用户、SSO、稳定 API”上全部等价的严格开源项目。Super Productivity 实际跨了三个通常分开的产品类别：个人 local-first 任务工具、专注/计时器、开发者工作项聚合器。

1. **个人开发者的默认选择仍是 Super Productivity 本身**。它在任务、子任务、重复任务、时间盒、Pomodoro、工时统计以及 Jira/GitHub/GitLab 等开发工具集成上最完整。建议 ANAS 首轮只 PoC“静态 Web 容器 + 现有 Nextcloud WebDAV 同步”；不要默认引入仍为 beta 的 SuperSync。
2. **最接近其个人规划体验的是 dayGLANCE，但只能列为 experimental**。它有 24 小时时间块、任务、项目/目标、习惯、Pomodoro、计划与实际统计、离线 PWA 和 WebDAV/Nextcloud 同步；项目创建于 2026 年 1 月，维护历史太短，且缺少 Super Productivity 的开发工具集成和稳定公共业务 API。
3. **需要独立服务端、原生客户端和实时计时同步时，可试验 FocusFlow**。它提供任务、日历、Pomodoro、统计、REST/OpenAPI、WebSocket 和 PostgreSQL 后端，但项目很年轻、社区很小、尚未确认通用 OIDC/LDAP，也没有已确认的 iOS 客户端。
4. **任务 + 习惯 + 多平台客户端可试验 OpenHabitTracker**。它有可调用的 OpenAPI 和 Docker 服务端，但当前服务端明确是单用户模型；PWA 本地模式不提供在线同步，不能把它包装成 ANAS 家庭多用户服务。
5. **成熟的服务器优先任务管理首选 Vikunja**。它有多用户协作、列表/Kanban/Gantt/日历、OIDC/LDAP、REST/OpenAPI、Token、Webhook 和成熟部署文档，最符合传统 ANAS Module 契约；但当前官方 Pro 边界把任务工时记录/报表放在付费层，且没有原生 Pomodoro，因此不是 Super Productivity 的一比一替代。
6. **团队项目与工时选择 Leantime 或 Worklenz**。Leantime 历史更长、项目/目标/工时能力成熟，但 Pomodoro 是付费 Marketplace 插件；Worklenz 的团队资源、预算、任务工时和分析更完整，但 PostgreSQL、Redis、MinIO 等拓扑更重。二者都不是 local-first 个人效率工具。
7. **只解决工时记录时，选择 solidtime 或 Kimai；不要把它们当任务规划器**。solidtime 更现代、面向自由职业者/代理机构，Kimai 的报表、发票和插件生态更成熟。ActivityWatch 则是本地自动活动记录的补充工具。
8. **Will Be Done 是轻量任务计划器观察项**。它 local/offline-first、单容器 SQLite、有重复任务和 HTTP API，但没有任务计时、Pomodoro 或日内时间块；项目同样始于 2026 年。
9. **TaskTrove 不进入严格开源推荐**。其仓库使用 Sustainable Use License，限制再分发和商业用途；Pro 才包含多用户和 header SSO，并依赖许可证服务。它属于源码可见/开放核心，不应因目录标签而写成 OSI 开源。
10. **没有必要为每种侧重点都新建 ANAS Module**。建议 PoC 顺序为 `super-productivity-web` → `vikunja` → `dayglance`（experimental）；团队工时已有明确需求时再评估 `leantime`/`worklenz`，纯工时需求再评估 `solidtime`/`kimai`。

## 2. 主题卡、范围与局限

```yaml
topic: personal-task-time-management
title: Super Productivity 与同类开源自部署项目
snapshot_date: 2026-08-15
decision_for: ANAS module 候选
must_be:
  - 可获得源代码并允许自行部署
  - 核心用途覆盖个人任务、时间规划、专注计时或工时跟踪中的至少两项
  - 上游仍可访问
core_categories:
  - 个人任务与日程规划
  - Pomodoro、时间盒与任务工时
  - local-first 客户端加自托管 Web 或同步服务
adjacent_categories:
  - 团队项目管理加工时
  - 纯工时或自动活动跟踪
  - Kanban、CalDAV 任务和习惯追踪
excluded:
  - SaaS-only 或专有软件
  - 没有可部署 Web、服务端或同步端的纯客户端
  - 已停止维护且没有明确继任者
target_users:
  - 个人开发者
  - 家庭或小团队
expected_scale: 单用户至 20 用户，低并发
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  architectures: [amd64, arm64]
questions:
  - 哪些项目能替代 Super Productivity 的主要工作流？
  - 哪些项目值得进入 ANAS Module PoC？
  - 社区版能否复用 IAM、数据库、Nextcloud 与备份能力？
search_date: 2026-08-15
```

### 2.1 “同类型”和“所有”的口径

本轮把 Super Productivity 的核心工作流拆成五块：任务/项目、日历时间盒、Pomodoro、手工工时与统计、开发工具工作项集成。覆盖其中至少两块的严格开源自部署项目进入核心或重点相邻表；只覆盖一块的项目进入相邻类别。

“当前所有”表示在下列已声明目录、商业产品反向检索和官方仓库搜索范围内，尽可能完整地给每个发现项目一个去向，不表示数学意义上的永久穷尽。个人项目变化快，90 天后必须重新核验许可证、发行、活跃度和商业功能边界。

### 2.2 发现来源

目录仅用于建立长名单，许可证、功能和部署结论均回到上游核验：

- [AlternativeTo：Super Productivity 关于页](https://alternativeto.net/software/super-productivity/about/)及其[自部署 alternatives](https://alternativeto.net/software/super-productivity/?platform=self-hosted)；
- [awesome-selfhosted：Task Management & To-do Lists](https://awesome-selfhosted.net/tags/task-management--to-do-lists.html)与[Time Tracking](https://awesome-selfhosted.net/tags/time-tracking.html)；
- [selfh.st Apps](https://selfh.st/apps/)及其项目周报，例如发现 TaskTrove 的[周报条目](https://selfh.st/weekly/2025-09-19/)；
- GitHub 仓库、release、许可证、Compose 和官方部署/API/认证文档；
- 已有的[开源自部署 Kanban 调研](./self-hosted-open-source-kanban-research.md)，用于避免重复把纯团队看板误判为个人效率工具。

### 2.3 从头部商业产品反向发现

| 商业基准 | 主要卖点 | 反向发现或确认的候选 |
| --- | --- | --- |
| [Todoist alternatives](https://alternativeto.net/software/todoist/?license=opensource&platform=self-hosted) | 快速捕获、重复任务、跨平台 | Super Productivity、Vikunja、Tududi、Jotty、Will Be Done |
| [TickTick alternatives](https://alternativeto.net/software/ticktick/?license=opensource&platform=self-hosted) | 任务、日历、习惯、Pomodoro | Super Productivity、Nextcloud Tasks、Vikunja、Wekan |
| Sunsama / Akiflow | 每日计划、日历时间盒、任务聚合 | dayGLANCE、Super Productivity；严格开源自部署候选明显稀少 |
| [Toggl Track alternatives](https://alternativeto.net/software/toggl/?license=opensource&platform=self-hosted) | 任务工时、报表、团队 | solidtime、Kimai、Cattr、TimeTagger、Traggo |
| [ClickUp alternatives](https://alternativeto.net/software/clickup/?license=opensource&platform=self-hosted) | 团队任务、项目、工时 | Leantime、Worklenz、Vikunja、Taiga |
| Jira / GitHub / GitLab | 开发工作项、提交与任务联动 | Super Productivity 的原生集成；通用任务工具通常只能经 API/Webhook 衔接 |

这一反向检索说明“同类”不能只看 Todo 标签：日历时间盒候选主要来自 daily planner，工时报表候选主要来自 time tracking，团队任务候选则来自 project management。

## 3. 基准产品：Super Productivity

### 3.1 产品能力

[Super Productivity](https://github.com/super-productivity/super-productivity) 是 MIT 许可的 local-first 个人任务与时间管理应用。官方 README 列出的核心能力包括：

- 任务、子任务、项目、标签、重复任务、拖放计划和每日/工作总结；
- 时间盒、任务工时、Pomodoro 与休息提醒、个人指标；
- Jira、Trello、GitHub、GitLab、Gitea、OpenProject、Linear、ClickUp 和 Azure DevOps 等集成；
- Web、Windows、macOS、Linux、Android 和 iOS/PWA 路径；
- 数据优先保存在本地，不要求账号；同步可选 Nextcloud、通用 WebDAV、Dropbox、SuperSync 或桌面本地文件。

它的最大差异不是“有一个计时器”，而是计时记录和当前任务、开发工作项、日计划及复盘连在同一客户端里。

### 3.2 “自部署”实际包含两种架构

#### 静态 Web 应用

官方[Docker 文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/2.13-Run-with-Docker.md)提供 `super-productivity/super-productivity:latest`，镜像覆盖 amd64、arm64 和 arm/v7。容器只提供静态前端；任务数据保存在浏览器 IndexedDB，而不是容器卷。因此：

- 重建容器不会恢复或删除浏览器任务；
- 保护 URL 的 oauth2-proxy 只能控制“谁能打开页面”，不能把浏览器数据变成服务端多用户数据；
- 同一个浏览器配置文件中的隔离边界与普通 local-first Web App 相同；
- 真正的跨设备恢复依赖应用导出或同步 Provider。

官方[Web 与桌面差异文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/3.05-Web-App-vs-Desktop.md)还指出：Web 版没有 Node 插件、本地文件同步和文件系统自动备份；部分 CalDAV/WebDAV 会受 CORS 影响；应用关闭后不能持续计时，空闲检测依赖浏览器扩展；PWA 离线能力也弱于桌面客户端。

#### SuperSync 服务

仓库内的 [SuperSync README](https://github.com/super-productivity/super-productivity/tree/master/packages/super-sync)把它标为新的操作日志式同步服务，使用 PostgreSQL/Prisma，并提供参考 Caddy 拓扑。当前关键边界是：

- 仍为 beta，官方同步选择文档建议生产用户优先考虑 Nextcloud/WebDAV/Dropbox/本地文件；
- 没有独立 release tag，镜像只有 `latest` 和从 master 生成的 `master-<sha>`，固定部署应锁 SHA；
- 数据库迁移应使用上游部署脚本，不能只照抄普通 `docker compose up`；
- 参考 Compose 预留约 2.5 GB 容器内存，构建峰值还会更高；
- 登录以 passkey/magic link 为主，未确认 OIDC、LDAP 或 SAML；JWT 有效期长、撤销粒度有限，部分挑战与缓存是进程内状态；
- 多账号表示同步数据隔离，不表示共享项目或实时协作；
- 客户端仍是主要事实来源。服务端备份不能替代客户端导出，E2EE 历史恢复还有官方明确的限制。

因此，SuperSync 目前适合专项实验，不适合作为 ANAS 默认生产依赖。

### 3.3 同步、API 与 AI 边界

官方[同步选择文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/4.20-Syncing-Choose-Sync-Provider.md)说明数据始终先写本地，再同步任务、项目、工时和设置。Nextcloud/WebDAV/Dropbox/本地文件支持客户端加密；通用 WebDAV 因服务端实现差异被标为 experimental，本地文件只适用于桌面，而且不应再套 Syncthing/Resilio 复制同一同步文件。

Super Productivity 有插件 API，但没有已确认的、可从服务端稳定调用的完整任务 REST/OpenAPI；SuperSync API 也只服务于同步协议，不能等同于业务 API。对 AI/自动化的评估为：

- 客户端内插件：`C 可集成`，适合用户主动触发；
- ANAS 服务端代理直接增删任务：`D 困难`，除非上游形成稳定公共 API；
- 第三方 MCP/桥接可用于试验，不能写成官方契约。

## 4. 严格开源核心候选：产品能力

符号：✅ 官方原生；◐ 有限制、插件或早期；❌ 官方明确缺少；— 本轮未确认或不适用。`离线`指完整客户端工作流，不把“网页能缓存少量资源”自动算作 local-first。

| 项目 | 定位 / 许可证 | 任务与计划 | Pomodoro / 工时 | 离线与同步 | 多用户 / 协作 | API、集成与 AI |
| --- | --- | --- | --- | --- | --- | --- |
| **Super Productivity** | 个人任务、时间与开发工作项；MIT | ✅ 项目/标签/重复/时间盒 | ✅ Pomodoro、任务工时、统计 | ✅ local-first；Nextcloud/WebDAV/Dropbox/SuperSync | ◐ 多账号同步隔离；非共享协作 | ◐ 开发工具集成强；插件 API，缺稳定服务端业务 API；AI `C/D` |
| **dayGLANCE** | 每日时间块、任务、目标与习惯；MIT，商标除外 | ✅ 24h 时间块、收件箱、项目/目标/领域、重复 | ✅ 任务 Pomodoro、计划与实际统计 | ✅ local-first/PWA；Nextcloud/WebDAV/Obsidian，可选 AES-256-GCM | ❌ 个人应用 | ◐ iCal/CalDAV 与 BYO OpenAI-compatible/Ollama；未确认稳定公共 API；AI `A` |
| **OpenHabitTracker** | 任务、笔记与习惯；GPL-3.0 | ✅ 分类、优先级、搜索、导入导出 | ◐ 有 timer 与时间/次数习惯；非完整 Pomodoro/时间盒 | ◐ PWA 本地离线但无在线同步；客户端可连自建端 | ❌ Docker 服务当前单用户 | ✅ OpenAPI；AI `C` |
| **FocusFlow** | 任务、日历与专注计时；MIT | ✅ 任务、分类、优先级、排期、周/月历 | ✅ Pomodoro、会话、统计 | ◐ 原生客户端连自建后端；实时 WebSocket | ◐ 账号能力存在，协作/RBAC 未确认 | ✅ REST/OpenAPI；AI `C` |
| **Will Be Done** | local-first 周计划；AGPL-3.0 | ✅ 周计划、项目、分区、重复、清单、Todoist/TickTick 导入 | ❌ 无任务工时、Pomodoro、日内时间块 | ✅ 浏览器本地数据库、PWA、实时同步 | ❌ 上游明确不计划多人空间 | ✅ HTTP API；AI `C` |
| **Vikunja** | 多用户任务与项目管理；AGPL-3.0 | ✅ 列表/Kanban/Gantt/日历、重复、共享 | ◐ 工时在 Pro；无 Pomodoro | ◐ Web/桌面/基础移动；服务器优先 | ✅ 用户、团队、共享 | ✅ REST/OpenAPI、Token、Webhook、CLI/n8n；AI `C` |

### 4.1 平台与客户端覆盖

| 项目 | Web | Windows | macOS | Linux | Android | iOS/iPadOS |
| --- | --- | --- | --- | --- | --- | --- |
| Super Productivity | ✅ 静态 Web/PWA | ✅ | ✅ | ✅ | ✅ | ✅/PWA |
| dayGLANCE | ✅ 静态 Web/PWA | ✅ Electron | ✅ Electron | ✅ Electron | ✅ APK | PWA；签名商店封装有商业边界 |
| OpenHabitTracker | ✅ PWA 或 Blazor Server | ✅ | ✅ | ✅ | ✅ | ✅ |
| FocusFlow | 后端 API；主客户端非完整 Web 产品 | ✅ Tauri | ✅ Tauri | ✅ Tauri | ✅ | — |
| Will Be Done | ✅ Web/PWA | 桌面封装 | 桌面封装 | 桌面封装 | PWA | PWA |
| Vikunja | ✅ | ✅ | ✅ | ✅ | 基础移动/PWA | 基础移动/PWA |

dayGLANCE 的代码为 MIT，但 README 明确排除名称/图标商标，签名商店分发和部分封装可收费；这不改变核心代码的严格开源属性，却会影响 ANAS 是否能沿用品牌和直接转发商店包。

## 5. 重点相邻候选

### 5.1 团队任务与工时

| 项目 | 许可证 / 主定位 | 任务与工时 | 部署与身份 | 为什么不是直接替代 |
| --- | --- | --- | --- | --- |
| **Leantime** | AGPL-3.0；目标导向项目管理 | ✅ 项目、看板、里程碑、时间表/工时 | PHP 应用 + MySQL/MariaDB；认证扩展边界需按目标版本核验 | 服务器优先、团队化；Pomodoro 是付费 Marketplace 插件，非社区核心 |
| **Worklenz** | AGPL-3.0；团队项目、资源与预算 | ✅ 任务、列表/看板/Gantt、任务工时、分析 | Docker/Compose；PostgreSQL、Redis、MinIO、Nginx | 拓扑重、无 local-first/Pomodoro；通用 OIDC/LDAP 未确认 |
| **Tududi** | MIT；任务、项目、笔记 | ◐ 任务/Kanban/CalDAV；未确认完整任务计时 | 单实例容器路径较轻；OIDC 可用 | 任务优先，缺时间盒/Pomodoro/工时闭环 |
| **Nextcloud Tasks** | AGPL；CalDAV 任务 | ◐ 任务、列表、重复、共享；无原生工时 | 作为现有 Nextcloud app，继承其身份与备份 | 最省新增服务，但功能明显少于 Super Productivity |
| **Taiga / Wekan / Kanboard / Kan** | 各自开源许可；团队看板/项目 | ◐ 看板、任务；部分有插件或估时 | 见专项 Kanban 调研 | 团队看板不是个人日历时间盒与专注计时工具 |

### 5.2 工时与活动记录

| 项目 | 许可证 / 主定位 | 强项 | 主要缺口 |
| --- | --- | --- | --- |
| **solidtime** | AGPL-3.0；自由职业者/代理机构工时 | 项目、任务、客户、计费费率、组织与角色、导入；现代 Web 与桌面 beta | 无个人重复任务、日计划、时间盒或 Pomodoro；应用、队列、调度、PostgreSQL 等比单容器重 |
| **Kimai** | AGPL-3.0；成熟多用户工时与发票 | 报表、客户/项目、账单、API、插件和长期升级历史 | 不是个人任务规划器，不提供 local-first/Pomodoro 主工作流 |
| **ActivityWatch** | MPL-2.0；本地自动活动记录 | 自动记录窗口/应用/浏览器活动，数据本地，适合补充“实际时间” | 不管理任务、项目计划或 Pomodoro；更适合作为互补工具 |
| **Cattr / TimeTagger / Traggo** | 开源工时工具 | 轻量工时、标签或团队追踪 | 功能只覆盖 Super Productivity 的计时切片，本轮不做优先 Module |
| **FocusTide** | 开源专注计时器 | Pomodoro/专注会话 | 不足以替代任务、项目、工时报表和开发工具集成 |

## 6. 社区自部署、付费版与限制

“代码可见”“免费自部署”“官方托管免费套餐”和“企业功能免费”不是一回事。以下只记录与选型直接相关的边界；实施前仍要按锁定 release 重查。

| 项目 | 开源自部署社区版 | 官方付费/托管增加什么 | 自部署限制与隐性依赖 |
| --- | --- | --- | --- |
| **Super Productivity** | MIT 客户端/Web 完整核心免费；Nextcloud/WebDAV/Dropbox/本地文件可同步；SuperSync 代码可部署 | 官方无统一企业功能锁；应用商店和捐赠属于分发/支持 | Web 数据在浏览器；SuperSync beta、无 release tag、无通用 SSO，需 PostgreSQL，认证/恢复契约尚早 |
| **dayGLANCE** | MIT 核心、静态 Web、WebDAV/Nextcloud、E2EE、AI Provider 配置可用 | 签名商店包/平台封装可收费 | 商标不在 MIT 授权内；项目非常新，服务端只是静态 UI，数据安全依赖浏览器和同步端 |
| **OpenHabitTracker** | GPL-3.0；PWA、原生客户端、单用户 Docker 服务和 OpenAPI 免费 | 商店分发或托管成本不是社区功能锁 | 服务端当前一实例一用户，无 OIDC/LDAP；Docker 模式需要联网，持久化目录必须单独备份 |
| **FocusFlow** | MIT；客户端、Rust 后端、PostgreSQL、REST/OpenAPI 免费 | 未确认官方功能型商业层 | 很年轻；JWT/CORS/VAPID 等由管理员配置，未确认企业 SSO、iOS 和长期迁移策略 |
| **Will Be Done** | AGPL-3.0；本地数据库、服务端同步、SQLite、HTTP API 免费 | 未确认官方功能型商业层 | 上游明确不计划多人协作；没有计时和日内排程；2026 新项目 |
| **Vikunja** | AGPL CE 的任务/项目、协作、OIDC/LDAP、API 可自部署 | **Pro** 增加任务工时记录/报表、审计和管理 UI；也有官方托管 | 启用 Pro key 后会联系许可证服务；不能把付费工时写成 CE 能力 |
| **Leantime** | AGPL 核心项目管理与工时可自部署 | Marketplace 的 Pomodoro 等插件按许可证/更新通行证收费；托管/支持另计 | SSO、插件和企业能力必须在目标版本逐项核验；MySQL/MariaDB 自管 |
| **Worklenz** | AGPL Community Edition 提供团队任务、工时和分析 | 官方托管/支持按当前方案计费 | 多服务资源与备份成本高；未确认 CE 通用 OIDC/LDAP |
| **solidtime** | AGPL 核心工时、客户/项目/任务和组织能力可部署 | 官方托管、支持与未来商业能力需看当期矩阵 | 队列、调度、邮件/导出等生产依赖需一起运维；不是任务规划器 |
| **Kimai** | AGPL 核心工时、报表、API 可部署 | 商业插件、托管和支持可收费 | 选择插件时需逐个核验许可证；管理员承担数据库、附件和升级 |

## 7. 源码可见、停止与排除项目

| 项目 | 分类 | 结论与理由 |
| --- | --- | --- |
| **TaskTrove** | 源码可见 / 开放核心 | [Sustainable Use License](https://github.com/dohsimpson/TaskTrove/blob/main/LICENSE.md)限制用途和分发，不是 OSI 开源；Pro 才有多用户/header SSO，并联系 Keygen/项目许可证服务校验。不能进入严格开源排名 |
| **AFFiNE** | 开放核心 / 相邻 | Notion 式工作空间优先，仓库存在社区与企业目录/许可边界；不是个人计时工具，严格开源能力需按目录核验 |
| **Fizzy** | 专有 | AlternativeTo 可能把它与自部署候选混列，但不是严格开源项目 |
| **Focalboard** | 停止维护 | 官方仓库已明确不再维护；即使历史上可自部署，也不建议新建 Module |
| **Tasks.org / Planify / WeekToDo / Org mode** | 排除为独立服务 | 可作为优秀客户端或文件工作流，但本身没有独立可部署的 Web/同步服务；Tasks.org 可搭配 CalDAV/Nextcloud，不等于它自身是 Module |
| **AppFlowy** | 相邻工作空间 | 严格开源自部署能力存在，但 Notion 式工作空间、多服务协作/AI 拓扑明显超出本主题；参见笔记应用调研 |
| **Cattr / TimeTagger / Traggo / FocusTide** | 相邻 | 只覆盖工时或专注计时，不覆盖完整任务/日程工作流 |
| **Taiga / Wekan / Kanboard / Kan** | 相邻 | 主要是团队看板/项目管理，已由 Kanban 专项报告覆盖 |

## 8. 部署、社区与成熟度快照

Star、fork、open issue、push 和 release 均取自 2026-08-15 的上游仓库/API 快照，只是社区规模与维护信号，不是产品质量排名。GitHub 的 `open_issues_count` 同时包含 issue 与 PR，不代表未处理缺陷数；本轮没有把自动依赖提交单独当作活跃证据。日期采用 `YYYY-MM-DD`；ActivityWatch 另说明预发行版。

| 项目 / 仓库 | 创建 | Star / fork / open | 最近 push | 最新稳定 release | 许可证 / 状态 | 成熟度判断 |
| --- | --- | ---: | --- | --- | --- | --- |
| [Super Productivity](https://github.com/super-productivity/super-productivity) | 2017-01-06 | 21,357 / 1,946 / 1,444 | 2026-08-15 | v18.19.0 / 2026-08-07 | MIT / 未归档 | 成熟客户端；SuperSync 单独按 beta 评价 |
| [dayGLANCE](https://github.com/krelltunez/dayGLANCE) | 2026-01-24 | 113 / 8 / 5 | 2026-08-15 | v4.3.0 / 2026-08-08 | MIT / 未归档 | 很早期，更新快但历史不足 |
| [OpenHabitTracker](https://github.com/Jinjinov/OpenHabitTracker) | 2023-11-14 | 269 / 21 / 4 | 2026-08-14 | 1.2.4 / 2026-08-15 | GPL-3.0 / 未归档 | 成长中；服务端模型仍受限 |
| [FocusFlow](https://github.com/francesco-gaglione/focus_flow_cloud) | 2025-09-29 | 45 / 3 / 5 | 2026-06-16 | v1.3.0 / 2026-06-16 | MIT（LICENSE/README）/ 未归档 | 早期，小社区 |
| [Will Be Done](https://github.com/will-be-done/will-be-done) | 2026-01-17 | 123 / 5 / 5 | 2026-08-14 | v0.10.1 / 2026-07-20 | AGPL-3.0 / 未归档 | 很早期，0.x |
| [Vikunja](https://github.com/go-vikunja/vikunja) | 2018-11 | 5,065 / 612 / 256 | 2026-08-14 | v2.5.0 / 2026-08-04 | AGPL-3.0 / 未归档 | 成熟、持续发布，升级/备份文档完整 |
| [Worklenz](https://github.com/Worklenz/worklenz) | 2024-05 | 3,144 / 357 / 76 | 2026-08-11 | v2.1.7 / 2026-02-09 | AGPL-3.0 / 未归档 | 成长中，团队产品；有备份/恢复脚本 |
| [Leantime](https://github.com/Leantime/leantime) | 2015-01 | 11,341 / 1,105 / 310 | 2026-08-15 | v3.9.8 / 2026-07-08 | AGPL-3.0 / 未归档 | 很成熟，商业插件边界需锁定 |
| [Tududi](https://github.com/chrisvel/tududi) | 2023-11 | 3,250 / 237 / 15 | 2026-08-13 | v1.3.1 / 2026-08-09 | MIT / 未归档 | 成长中 |
| [solidtime](https://github.com/solidtime-io/solidtime) | 2024-01 | 8,849 / 498 / 37 | 2026-08-10 | v0.19.1 / 2026-08-07 | AGPL-3.0 / 未归档 | 成长中，仍为 0.x |
| [Kimai](https://github.com/kimai/kimai) | 2016-10 | 4,887 / 823 / 332 | 2026-08-14 | 2.65.0 / 2026-08-11 | AGPL-3.0 / 未归档 | 很成熟，升级与插件文档长期维护 |
| [ActivityWatch](https://github.com/ActivityWatch/activitywatch) | 2016-04 | 18,597 / 978 / 191 | 2026-08-06 | v0.13.2 / 2024-10-05 | MPL-2.0 / 未归档 | 成熟；2026-08 有 v0.14 beta，稳定版较旧 |
| [TaskTrove](https://github.com/dohsimpson/TaskTrove) | 2025-07 | 1,096 / 24 / 33 | 2026-01-09 | 未确认 | Sustainable Use / 源码可见 | 非严格开源，且当日最近 push 已超过 7 个月 |

维护者人数、issue/PR 响应时间和安全政策没有稳定的统一 API 口径，本报告不据此制造精确分数。进入 A/B 级 PoC 前，仍应检查目标仓库近 90/365 天的实质提交者、已关闭安全问题、迁移说明和恢复演练；项目年龄不足一年或 0.x 的候选即使近期 push 很活跃，也继续按早期项目处理。

## 9. ANAS 适配评估

### 9.1 总表

| 候选 | 拓扑、持久化与备份面 | IAM / API | 架构与升级 | 等级 |
| --- | --- | --- | --- | --- |
| **Super Productivity Web + Nextcloud** | 一个静态 Web 容器；容器无业务数据。同步数据进入现有 Nextcloud/WebDAV；同时保留应用导出 | 入口可用 oauth2-proxy，但不等于应用账号；无稳定服务端业务 API | 官方镜像支持 amd64/arm64；锁定 release/image digest。WebDAV CORS、冲突、E2EE 恢复要实测 | **A 个人**：最贴近目标，且复用现有 Nextcloud；必须正确表达 local-first 边界 |
| **Vikunja** | 单 Go 应用含前后端；SQLite 或复用 PostgreSQL/MySQL，附件目录一起备份 | 社区版 OIDC/LDAP；REST/OpenAPI、Token、Webhook | 多架构镜像、备份/升级文档成熟；DB 与文件需一致恢复 | **A 服务器**：最符合标准 Module；但 CE 无工时/Pomodoro |
| **dayGLANCE + Nextcloud** | 静态容器；浏览器本地数据 + WebDAV/Nextcloud 同步，容器卷不承载任务 | 入口代理仅保护页面；无通用账号/稳定业务 API | 多平台客户端与静态镜像路径清楚；项目历史极短 | **A experimental**：功能最接近，先做数据/冲突/移动端 PoC |
| **OpenHabitTracker** | 单用户服务端 + 持久化 `.OpenHabitTracker`；每个用户需独立实例或接受共享账号 | 内置用户名/密码/JWT；OpenAPI；无通用 SSO | 客户端多平台；服务端恢复面简单但多实例运维会放大 | **B experimental**：适合个人实例，不适合默认家庭多用户模块 |
| **FocusFlow** | Rust 后端 + PostgreSQL，客户端连接自定义 URL；备份 PG 与服务端 Secret | JWT + REST/OpenAPI/WebSocket；未确认 OIDC/LDAP | 后端与客户端 release 要配套；arm64 镜像需 PoC 核验 | **B experimental**：服务/API 契约好，但历史与社区不足 |
| **Will Be Done** | 单容器 + SQLite `/var/lib/will-be-done`；浏览器本地状态与服务端同步需一起验证恢复 | 无 SSO；HTTP API | 拓扑轻，多架构和破坏性迁移需验证 | **B experimental**：轻量，但缺目标功能且上游不做多人协作 |
| **Leantime** | 应用 + MySQL/MariaDB，备份 DB 与上传目录 | API/MCP 有集成潜力；SSO/插件 entitlement 需锁版本核验 | 成熟镜像与迁移；不能复用只提供 PostgreSQL 的契约 | **B 团队**：成熟但偏重，Pomodoro 另付费 |
| **Worklenz** | 前后端 + PostgreSQL + Redis + MinIO + Nginx；需一致备份 DB 和对象 | API/SSO 社区版边界未完全确认 | 组件多、资源与升级测试成本高 | **C 团队**：有任务工时，但家庭 NAS 默认成本高 |
| **solidtime** | Laravel 应用、队列、调度 + PostgreSQL；导出/文档可能需要额外服务 | API/认证能力按锁定版本核验 | 可复用 PG，运行组件多于简单计时器 | **B 工时**：仅在明确需要现代团队工时时 PoC |
| **Kimai** | Web 应用 + SQL 数据库 + 持久化目录；备份契约成熟 | 官方 API 与插件生态；企业身份能力依插件逐项核验 | 长期稳定、多架构镜像路径需按选定发行核验 | **B 工时**：成熟，但产品面与个人时间盒不同 |

### 9.2 推荐的首个 Super Productivity Module 边界

首轮 Module 应命名为类似 `super-productivity-web`，并明确它是“可自托管静态客户端”，不是“服务端任务数据库”。建议契约如下：

- `requires_capabilities`: `traefik`；不要为了页面容器强制要求 PostgreSQL；
- 可选集成：现有 `nextcloud` 的 HTTPS WebDAV URL，不能把用户凭据写入公共配置；
- `data/`: 只包含 Nginx/静态服务配置，通常无业务恢复价值；
- `userdata/`: 不把浏览器 IndexedDB伪装成服务器卷。文档要求用户启用同步并定期导出；若使用 Nextcloud，备份面在 Nextcloud；
- IAM：可用 oauth2-proxy 保护入口，但 UI 必须注明它不能提供应用级多用户映射，也不替代 WebDAV 认证；
- 版本：锁定确定 release 和镜像 digest，不能在 Module 中使用浮动 `latest`；
- 健康检查：验证静态入口与关键 JS 资源；同步健康必须通过客户端实际写入/第二端拉取验证；
- 外部访问：必须 HTTPS；WebDAV/CalDAV CORS 与反向代理 header 是 PoC 阻断项；
- 回滚：静态前端回滚前先做应用导出，验证新版本写出的本地 schema 是否能被旧版本读取。

SuperSync 若以后独立成熟，应成为可选的第二拓扑，而不是悄悄加进同一默认 Compose。它至少需要 PostgreSQL、迁移前备份、镜像 SHA 锁定、邮件或 passkey 域名、客户端恢复演练与认证风险审查。

### 9.3 为什么 Vikunja 仍值得单独建 Module

Vikunja 与 Super Productivity 不是重复项：前者解决“服务器上的多用户任务与协作”，后者解决“个人设备上的任务、专注和工时”。Vikunja 能原生消费 OIDC/LDAP，并提供真正的服务端 API、Token、Webhook 和共享项目；其 PostgreSQL 与 Traefik 也能直接进入 ANAS 标准能力模型。Module 文案必须把 Pro 工时和 CE 任务功能分开。

## 10. PoC 清单

### 10.1 `super-productivity-web` 第一优先级

- [ ] 锁定 v18.19.0 或后续目标 tag 的多架构镜像 digest，分别验证 amd64/arm64。
- [ ] 经 Traefik HTTPS 打开 Web/PWA，验证 Service Worker、深链接、WebSocket/跨域控制台无错误。
- [ ] 用两个浏览器配置文件证明数据默认互相隔离且不写入容器卷。
- [ ] 连接 ANAS Nextcloud WebDAV，验证新增、修改、删除、重复任务、附件/设置和任务工时双向同步。
- [ ] 制造离线并发修改，验证冲突提示、合并结果和加密同步恢复。
- [ ] 清空浏览器站点数据后，从 WebDAV 和应用导出分别恢复。
- [ ] 验证 oauth2-proxy 登录/退出后本地 IndexedDB 残留的安全提示和共享终端风险。
- [ ] 验证 Chrome/Firefox/Safari 的 CORS、PWA 离线、标签页互斥、后台计时和空闲检测差异。
- [ ] 从 Jira/GitHub/GitLab 至少各选一项，验证 token 保存、权限最小化和同步失败降级。
- [ ] 升级一个 minor release 后再回滚，核验 local schema 和同步文件兼容性。

### 10.2 `vikunja` 第二优先级

- [ ] 使用固定官方镜像，验证 SQLite 与现有 PostgreSQL 两种拓扑，最终只保留一条受支持的默认路径。
- [ ] 配置 OIDC 首次登录、账号绑定、禁用本地注册、组/角色映射和退出；确认移动/桌面客户端兼容。
- [ ] 用 API Token、Webhook 和 OpenAPI 做任务增删改及 ANAS 自动化样例。
- [ ] 备份数据库与附件，执行空机恢复、升级迁移、失败回滚。
- [ ] 在无 Pro key 的全新实例中验证 CE 功能清单，确保 UI/文档不承诺工时、审计或管理 UI。

### 10.3 experimental 候选

- [ ] dayGLANCE：验证 Nextcloud/WebDAV E2EE、冲突合并、浏览器清理恢复、24h 时间块、Pomodoro 和商标/镜像再分发边界。
- [ ] OpenHabitTracker：验证 Docker 单用户限制、客户端连接服务端、OpenAPI 认证、卷恢复和多实例资源成本。
- [ ] FocusFlow：验证 arm64、PostgreSQL migration、JWT 轮换、WebSocket 反代、Android/桌面客户端兼容和服务端 URL 迁移。
- [ ] Will Be Done：验证 SQLite 恢复、浏览器本地数据库与服务端同步一致性、HTTP API 版本化和 0.x 升级。

## 11. 待核验事项

- dayGLANCE、FocusFlow、OpenHabitTracker 和 Will Be Done 的核心维护者数量、安全响应流程及真实生产案例尚不足。
- FocusFlow 官方镜像的 arm64 manifest、iOS 路线和 OIDC/LDAP 支持未确认。
- Worklenz 社区版的通用 OIDC/LDAP、API 稳定性和最小生产资源需要按目标 tag 做源码/Compose PoC。
- Leantime 的 SSO、API/MCP 与 Marketplace entitlement 会随版本和插件变化，Module 实施前需锁定精确矩阵。
- solidtime 的稳定 API、导出/PDF 辅助服务、邮件与对象存储最小依赖需从目标 release 的 Compose 核验。
- SuperSync 的正式 release、OIDC、逐设备 token 撤销、无损 E2EE 恢复和多副本支持是转为生产候选的前置条件。

## 12. 主要上游来源

### Super Productivity

- [主仓库与 README](https://github.com/super-productivity/super-productivity)
- [Docker 运行文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/2.13-Run-with-Docker.md)
- [Web App 与 Desktop 差异](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/3.05-Web-App-vs-Desktop.md)
- [同步 Provider 选择](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/4.20-Syncing-Choose-Sync-Provider.md)
- [SuperSync 源码与部署说明](https://github.com/super-productivity/super-productivity/tree/master/packages/super-sync)

### 核心与重点相邻项目

- [dayGLANCE 仓库与部署说明](https://github.com/krelltunez/dayGLANCE)
- [OpenHabitTracker 仓库](https://github.com/Jinjinov/OpenHabitTracker)与[官网](https://openhabittracker.net/)
- [FocusFlow 仓库](https://github.com/francesco-gaglione/focus_flow_cloud)
- [Will Be Done 仓库](https://github.com/will-be-done/will-be-done)
- [Vikunja 仓库](https://github.com/go-vikunja/vikunja)、[官方文档](https://vikunja.io/docs/)与[Pro 边界](https://vikunja.io/docs/pro/)
- [Leantime 仓库](https://github.com/Leantime/leantime)与[官方 Docker](https://github.com/Leantime/docker-leantime)
- [Leantime Pomodoro Marketplace 插件](https://marketplace.leantime.io/product/leantime-pomodoro/)
- [Worklenz 仓库](https://github.com/Worklenz/worklenz)与[自部署文档](https://docs.worklenz.com/en/start/self-host/)
- [Tududi 仓库](https://github.com/chrisvel/tududi)
- [solidtime 仓库](https://github.com/solidtime-io/solidtime)
- [Kimai 仓库](https://github.com/kimai/kimai)与[Docker 文档](https://www.kimai.org/documentation/docker.html)
- [ActivityWatch 仓库](https://github.com/ActivityWatch/activitywatch)
- [TaskTrove 许可证](https://github.com/dohsimpson/TaskTrove/blob/main/LICENSE.md)与[自部署文档](https://docs.tasktrove.io/)

## 13. 维护日期

- 动态数据与功能边界采集：2026-08-15。
- 最迟重查：2026-11-13；若任一候选改变许可证、推出稳定同步服务、调整 Pro/CE 边界或进入 Module PoC，应提前重查。
