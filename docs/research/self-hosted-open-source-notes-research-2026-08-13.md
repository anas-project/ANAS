# 开源自部署笔记应用全景调研（2026-08-13）

本报告按[开源自部署应用研究 Module 规范](./application-research-module-spec.md)研究“笔记”主题，为 ANAS 选择后续 Runtime Module 提供依据。动态数据采集于 2026-08-13；报告是选型快照，不是当前部署说明。

## 1. 结论先行

没有一个项目同时在轻量、全平台原生客户端、Web 编辑、E2EE、多人协作、社区版 SSO、稳定公共 API 和原生 AI 上全部领先。对 ANAS 更合理的是分场景选择：

1. **默认新增 Module 首选 Memos**：一个 Go 应用即可运行，默认 SQLite，也能复用 PostgreSQL/MySQL；完整 Web、通用 OAuth2 SSO、PAT、REST/gRPC、Webhook 和可声明式挂载的设置最贴合 ANAS。它偏时间线式短笔记/轻知识库，不是 OneNote 式复杂文档。
2. **零新增服务首选 Nextcloud Notes**：ANAS 已有 Nextcloud、关系数据库、LDAP/SAML、Web 与移动端。启用 Notes app 即可，身份、备份和客户端生态全部复用；不应再创建独立 Compose Module。
3. **完整个人知识库首选 TriliumNext 或 SiYuan**：两者功能和社区都强，单容器/本地数据也易运维；但原生企业 SSO、多用户隔离和移动端自托管体验都有明显边界，适合个人实例而不是家庭统一账号默认项。
4. **AI 笔记首选 Blinko（experimental）**：原生 RAG、OpenAI-compatible 模型、Web/桌面/Android、通用 OAuth2 和 REST API，且只新增 PostgreSQL 消费；项目不到两年，升级与恢复仍需 PoC。
5. **文件可携带与低资源首选 Note Mark / Many Notes / Jotty / flatnotes / SilverBullet**：其中 Note Mark、Many Notes、Jotty 有较好的 SSO/API 组合；flatnotes 和 SilverBullet 极简但不适合统一多用户 IAM。
6. **全平台 E2EE 客户端首选 Joplin**：桌面和移动端成熟、Joplin Server 可自托管且现已支持 SAML；必须接受“自托管 Server 是同步与管理端，不是完整 Web 笔记编辑器”，其 Data API 也运行在客户端 Clipper/插件侧。
7. **Logseq 必须纳入，但当前只能作为“成熟客户端 + 早期自托管 DB/RTC”候选**：经典文件版是最有代表性的 Obsidian 开源替代之一；2026 DB Web 已可自托管，Sync/Publish 也允许自定义服务器，但 DB 仍 beta、RTC/新移动端仍 alpha，现有社区镜像认证仍依赖 Logseq Cognito Token，不适合立即承诺 ANAS 通用 SSO。
8. **暂不纳入生产：Notesnook self-host**。客户端优秀且 E2EE，但官方同步服务仍标 beta/无支持，拓扑包含 MongoDB replica set、MinIO、Identity、API、SSE 和发布服务；不符合 ANAS 当前的稳定、低维护默认项。
9. **团队协作文档另选 HedgeDoc 或 La Suite Docs**。它们适合会议纪要/多人文档，不应与个人 local-first 笔记用同一把尺子比较。

建议首轮 PoC 顺序：`memos` → Nextcloud `notes` 可选应用 → `blinko` → `note-mark`；Joplin 与 TriliumNext 分别做“全平台同步”和“个人知识库”专项验证。

## 2. 研究范围与发现方法

### 2.1 本轮如何理解“所有”

“当前所有”无法永久穷尽：目录标签有误、个人项目持续出现，项目也会改名或改许可证。本轮从以下入口建立长名单并逐项回到官方资料核验：

- [AlternativeTo：Self-Hosted + Note-taking](https://alternativeto.net/browse/all/?platform=self-hosted&tag=note-taking) 当日显示 80 项；
- 以 Obsidian、Notion、Evernote、OneNote 和 Google Keep 为头部商业基准，逐个打开 `Open Source + Self-Hosted` filtered alternatives；其中用户指出的 [Obsidian alternatives](https://alternativeto.net/software/obsidian/?license=opensource&platform=self-hosted) 是本轮补漏的关键入口；
- 对 Logseq、Joplin、AppFlowy 等重要开源候选再打开其 alternatives 页面做第二跳；
- [awesome-selfhosted：Note-taking & Editors](https://awesome-selfhosted.net/tags/note-taking--editors.html)；
- [selfh.st Apps](https://selfh.st/apps/) 与 [AwesomeHub Note-taking](https://awesomehub.js.org/list/selfhosted/note-taking-apps)；
- GitHub 主仓库、服务端仓库、官方部署/认证/API 文档和 release。

修订后的主表覆盖 24 个严格开源核心或重要相邻候选（补入 Logseq 后仍将仅静态/客户端同步型项目按更严格口径降级），另列开放核心、停止和排除项。没有把每个 Markdown 桌面编辑器、VS Code 插件、纯静态站点生成器、计算/绘图编辑器和 SaaS-only 产品伪装成传统多用户服务；但 local-first 应用只要存在可复核的自托管 Web、同步或发布路径，就必须进入主表或明确排除。

### 2.2 头部闭源基准带来的候选

| 商业基准 | 主要比较场景 | 反向发现/确认的关键开源候选 |
| --- | --- | --- |
| Obsidian | local-first Markdown、双链、图谱、插件、Publish/Sync | **Logseq**、TriliumNext、SiYuan、SilverBullet、Joplin、Anytype |
| Notion | 页面/数据库、团队空间、实时协作、AI | AppFlowy、AFFiNE、Anytype、Docmost、La Suite Docs |
| Evernote | 全平台捕获、Web Clipper、附件、同步 | Joplin、Notesnook、Standard Notes、Memos |
| OneNote | 富文本、层级笔记、手写/画布、多平台 | Joplin、SiYuan、Butterfly、TriliumNext |
| Google Keep | 快速卡片、标签、清单、移动捕获 | Memos、Blinko、Jotty、Nextcloud Notes |

### 2.3 分类口径

- **个人笔记**：捕获、整理和跨设备使用是核心。
- **个人知识库（PKM）**：双链、块引用、查询、脚本或知识图谱是核心。
- **团队协作文档**：多人实时编辑、空间、权限与评论是核心。
- **客户端 + 自托管同步**：服务端可能不提供 Web 编辑，必须单独标注。
- **Web 构建/文件同步**：浏览器能打开不等于存在多用户服务端。

SSO 只认社区自部署版原生 LDAP、通用 OAuth2/OIDC、SAML 或可信代理认证。符号：✅ 官方原生；◐ 有限制、第三方、内部接口或早期；❌ 官方路径明确没有；— 本轮未确认/不适用。

## 3. 核心候选：产品能力

平台缩写：`W` 完整 Web 编辑；`Win/Mac/Lin` 桌面；`A/i` Android/iOS；`PWA` 可安装网页；“第三方”不算官方 App。

### 3.1 平台与 App 覆盖

这里的 ✅ 表示上游提供对应客户端或完整 Web 编辑器；`PWA/网页` 不冒充应用商店原生 App，`第三方` 表示项目文档推荐但不由核心项目维护。

| 项目 | Web | Windows | macOS | Linux | Android | iOS/iPadOS |
| --- | --- | --- | --- | --- | --- | --- |
| Memos | ✅ | PWA | PWA | PWA | PWA/第三方 | PWA/第三方 |
| Logseq | ✅ DB Web（beta） | ✅ | ✅ | ✅ | ✅ 经典文件版；DB 新版待 alpha | ✅ 经典文件版；DB 新版 alpha |
| Joplin | ❌ Server 仅管理/同步 | ✅ | ✅ | ✅ | ✅ | ✅ |
| TriliumNext | ✅ | ✅ | ✅ | ✅ | 移动网页/第三方 | 移动网页/第三方 |
| Notesnook | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Standard Notes | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| AppFlowy | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| SiYuan | ✅ Docker Web | ✅ | ✅ | ✅ | ✅ | ✅ |
| Blinko | ✅ | ✅ Tauri | ✅ Tauri | ✅ Tauri | ✅ Tauri | — |
| SilverBullet | ✅ PWA | PWA | PWA | PWA | PWA | PWA |
| flatnotes | ✅ 响应式 | PWA | PWA | PWA | PWA | PWA |
| Many Notes | ✅ PWA | PWA | PWA | PWA | PWA | PWA |
| Note Mark | ✅ 响应式 | 网页 | 网页 | 网页 | 网页 | 网页 |
| Jotty | ✅ PWA | PWA | PWA | PWA | PWA | PWA |
| plumio | ✅ | 网页 | 网页 | 网页 | 网页 | 网页 |
| HedgeDoc | ✅ | 网页 | 网页 | 网页 | 网页 | 网页 |
| Nextcloud Notes | ✅ | 网页 | 网页 | 网页 | ✅ | 社区 App |
| La Suite Docs | ✅ | 网页 | 网页 | 网页 | 移动网页 | 移动网页 |
| Anytype | ◐ Web 随版本演进 | ✅ | ✅ | ✅ | ✅ | ✅ |
| Lockbook | ❌ | ✅ | ✅ | ✅ | ✅ | ✅ |
| Butterfly | ✅ | ✅ | ✅ | ✅ | ✅ | ✅ |
| DailyTxT | ✅ | 网页 | 网页 | 网页 | 网页 | 网页 |
| TiddlyWiki | ✅ | 网页/第三方封装 | 网页/第三方封装 | 网页/第三方封装 | 网页 | 网页 |
| CryptPad | ✅ PWA | PWA | PWA | PWA | PWA/移动网页 | PWA/移动网页 |

### 3.2 功能与集成

| 项目 | 定位 / 许可证 | Web 与客户端 | 多用户 / 离线与隐私 | 社区版 SSO | API / 自动化 | AI |
| --- | --- | --- | --- | --- | --- | --- |
| **Memos** | 时间线短笔记、轻知识库；MIT | W、响应式/PWA；移动 App 主要为第三方 | 多用户、可见性/分享；无 E2EE | ✅ 通用 OAuth2，能禁本地登录 | ✅ PAT、REST、gRPC、Webhook、RSS | ◐ 有部署托管的 AI 设置；需按锁定版本验证能力 |
| **Logseq** | Obsidian/Roam 式 local-first 大纲 PKM；AGPL-3.0 | 经典版 Win/Mac/Lin/A/i；DB Web/桌面 beta，新 iOS alpha、Android 待测 | 经典版本地 Markdown/Org；DB graph 为 SQLite，RTC 可 E2EE 实时协作 | ◐ 自托管 Sync 当前校验 Cognito JWT；非通用 OIDC/SAML/LDAP | ✅ Plugin SDK、DB HTTP/CLI、脚本，官方 DB 文档列 MCP Server | ◐ MCP/插件可接 AI，非核心内置 RAG |
| **Joplin + Server** | Evernote/OneNote 式笔记同步；MIT（各包需核验） | Win/Mac/Lin/A/i；**Server 无完整 Web 编辑** | local-first、E2EE、多人分享/发布 | ◐ Server 有 SAML；社区/Business entitlement 需按锁定版本核验 | ◐ Data API 是桌面 Clipper/插件本地 API，不等于 Server 公共 API | ✅ 客户端 AI Chat/插件 API，可用 OpenAI、Ollama 等 |
| **TriliumNext** | 层级式大型 PKM；AGPL-3.0 | W、Win/Mac/Lin；移动 Web，Android/iOS 有第三方客户端 | 以单用户为主、服务端同步；无协作账号模型 | ❌ 无原生 OIDC/SAML/LDAP | ✅ ETAPI REST、脚本/插件 | ◐ 可通过插件/ETAPI 外接，非核心原生能力 |
| **Notesnook** | 跨平台隐私笔记；客户端 GPL-3.0、Server AGPL-3.0 | W、Win/Mac/Lin/A/i | local-first、E2EE、同步/发布 | ❌ 自托管 Identity 为本地账号 | ◐ 同步内部 API；未见面向集成的稳定用户 API | ❌ 产品刻意不内置 AI；可从客户端导出后处理 |
| **Standard Notes** | 极简 E2EE 笔记；客户端 AGPL-3.0、Server GPL-3.0 | W、Win/Mac/Lin/A/i | local-first、E2EE、长期同步 | — 未确认社区自托管通用 SSO | ◐ 官方同步协议/扩展 API，非通用业务 REST | ◐ 插件/外部处理；需核验 Proton 后最新功能边界 |
| **AppFlowy** | Notion 式 local-first 工作空间；AGPL-3.0 | W、Win/Mac/Lin/A/i | 离线、同步、多人协作 | ◐ GoTrue 社交 OAuth；未确认社区版通用 OIDC | ◐ `/api`/WebSocket 主要服务官方客户端，外部契约成熟度一般 | ✅ AppFlowy AI；自托管模型与服务拓扑需一起部署 |
| **SiYuan** | 块引用/双链 PKM；AGPL-3.0 | Win/Mac/Lin/A/i/HarmonyOS；Docker 提供 W | 以单 workspace/个人为主；Docker Web 不能作为原生客户端同步端 | ❌ 只有 access auth code | ✅ HTTP API、插件 API、SQL 查询 | ✅ OpenAI API 写作与问答 |
| **Blinko** | 卡片/闪念 + AI RAG；GPL-3.0 | W；Tauri Win/Mac/Lin/A | 多账号；数据自托管，无 E2EE 声明 | ✅ 自定义 OAuth2/OIDC 与多模板 | ✅ Bearer Token REST、在线 API 文档、Webhook | ✅ 原生 RAG，支持多模型/OpenAI-compatible |
| **SilverBullet** | 可编程 Markdown PKM；MIT | W/PWA；无官方原生移动 App | 单用户/文件优先、离线缓存 | ❌ 内置账号/密码或前置代理 | ✅ Space HTTP API、Space Lua、Plug API | ◐ 插件/脚本外接模型 |
| **flatnotes** | 极简 Markdown 文件笔记；MIT | W、移动响应式 | 单实例/单账号模式；Markdown 直接可取 | ❌ none/read-only/password/TOTP | ✅ REST API | ◐ 可经文件或 API 外接 |
| **Many Notes** | 多用户 Markdown vault；MIT | W/PWA | 多用户、vault 协作；文件与数据库双写 | ✅ Authentik/Keycloak/Zitadel/Authelia 等 OAuth 适配 | — 未见稳定公共业务 API | ◐ 文件层或定制代码外接 |
| **Note Mark** | 极简 Markdown Web；AGPL-3.0 | W、移动友好 | 多用户；本地文件/轻量数据 | ✅ OIDC | ✅ REST API | ◐ API 外接 |
| **Jotty** | 文件式笔记、清单与 Kanban；AGPL-3.0 | W/PWA、mobile-first；离线仅缓存已访问页 | 多用户、分享；Markdown/JSON 可携带 | ✅ OIDC、MFA | ✅ 带认证 REST API | ◐ API 外接 |
| **plumio** | Markdown 文档、组织与加密；AGPL-3.0 | W | 多用户、多组织、文档加密 | — 未确认通用 SSO | ✅ API Key 与文档操作 API | ◐ API 外接 |
| **HedgeDoc** | 多人实时 Markdown 会议/协作笔记；AGPL-3.0 | W | 实时协作、分享、版本历史；非 local-first | ✅ LDAP、SAML、OAuth 等，协议依版本配置 | ◐ 应用接口存在，但 v1 公共 API 契约有限 | ◐ 可通过 Webhook/接口外接 |
| **Nextcloud Notes** | Nextcloud Markdown 笔记应用；AGPL-3.0 | W；官方/社区 Android、iOS 客户端 | 多用户、通过 Nextcloud 同步；非 E2EE 笔记模型 | ✅ 继承 Nextcloud LDAP/SAML/OIDC 配置 | ✅ Notes REST API | ◐ REST/Nextcloud Assistant 外接，非 Notes 原生 |
| **La Suite Docs** | 大规模团队协作文档/笔记；MIT | W | 实时协作、空间/权限、离线能力 | ✅ OIDC（部署栈集成身份服务） | ◐ 服务 API；外部稳定契约需版本核验 | ✅ 官方列出 AI 工具，模型部署需专项核验 |
| **Anytype + any-sync** | 对象化 local-first PKM；客户端许可需逐目录核验，sync 为 MIT | Win/Mac/Lin/A/i；Web 能力随版本演进 | local-first、端到端加密、私有网络同步 | ❌ 网络/账号密钥模型，不是组织 SSO | ◐ Anytype API/本地 daemon；同步协议非普通业务 API | ◐ API 外接，非核心内置 AI |
| **Lockbook** | E2EE 文件/笔记同步；Unlicense | Win/Mac/Lin/A/i；无完整 Web 编辑 | local-first、E2EE、个人同步 | ❌ | ◐ 客户端库/CLI，服务 API 非通用集成面 | D：只能经导出/客户端定制 |
| **Linwood Butterfly** | 自由画布/手写笔记；AGPL-3.0 | W、Win/Mac/Lin/A/i | 文件优先；同步依赖外部存储 | ❌ 不存在统一账号服务 | ◐ 文件格式/客户端能力，无业务 Server API | D：文件处理 |
| **DailyTxT** | 加密日记；MIT | W | 多用户日记、加密上传；非协作 | ❌ 本地认证 | — | D |
| **TiddlyWiki** | 单文件可编程个人 wiki；BSD-3-Clause | W/Node.js server；桌面封装多为第三方 | 文件优先、插件生态；多用户能力有限 | ❌ 核心无组织 SSO | ◐ HTTP/插件/Node API | ◐ 插件外接 |
| **CryptPad** | E2EE 协作办公套件；AGPL-3.0 | W/PWA | 多人实时、浏览器端 E2EE、匿名协作 | ◐ SSO/目录能力需按实例支持方案核验 | ◐ 内部 RPC/集成能力，不是简单公开 Notes REST | ◐ 可扩展，但 E2EE 限制服务端 RAG |

## 4. 核心候选：部署、社区与 ANAS 适配

Star 为主产品仓库；对于 Joplin、Standard Notes、Notesnook 和 AppFlowy，服务端仓库成熟度另行列入判断。`活跃` 表示 2026-08-13 前仍有持续提交，不等于已生产成熟。

| 项目 | 自部署拓扑与必须依赖 | Star / 最近 push | 活跃度 / 成熟度 | ANAS 适配 |
| --- | --- | ---: | --- | --- |
| **Memos** | 单官方容器；SQLite 默认，可用 PostgreSQL/MySQL；本地或 S3 附件 | 62,207 / 08-13 | 很高 / 成熟 | **A**：Traefik + 现有 PG + OAuth2；Go 单体、API/备份清楚 |
| **Logseq** | 最小仅静态 DB Web；完整自托管为 Web + Sync + 可选 Publish。当前社区镜像从上游构建，Sync 持久化目录并要求 Cognito JWT 配置；Publish 用 Wrangler/Miniflare 本地 Durable Object/R2 且限单副本 | 44,425 / 08-13；selfhost guide 86 / 08-08 | 客户端很高/成熟；DB beta、RTC/移动 alpha；自托管早期 | **C 当前 / B 观察**：产品重要且可自托管，但官方未提供成熟 turnkey 拓扑，IAM 仍耦合 Cognito，先跟踪而非承诺生产 |
| **Joplin** | Server 容器；SQLite 测试、生产建议 PostgreSQL；可选 S3/Transcribe | 55,925 / 08-12 | 很高 / 很成熟 | **B+**：PG/SAML 可复用；无 Web 编辑，客户端 API 不能当 Server API |
| **TriliumNext** | 单容器 + SQLite/附件目录 | 37,390 / 08-13 | 很高 / 成熟（社区继任） | **B+ 个人**：极易部署；无 IAM/真正多用户 |
| **Notesnook** | 多服务 + MongoDB replica set + MinIO/S3 + Identity/API/SSE/Monograph；SMTP 建议 | 客户端 14,401；Server 909 / 08-10 | 高 / **Server beta** | **D 当前**：新数据库/对象存储与多域名，官方称无支持/未就绪生产 |
| **Standard Notes** | Sync Server + MySQL/MariaDB；Web App 需另部署，扩展/订阅能力边界复杂 | App 6,582 / 08-11；Server 469 / 04-11 | App 高、Server 中 / 成熟协议 | **C+**：E2EE 强；拓扑与许可/功能核验成本高，SSO 不明确 |
| **AppFlowy** | AppFlowy Cloud、PostgreSQL、Redis、MinIO/S3、GoTrue、Web/Admin 等多容器 | App 75,299 / 08-11；Cloud 1,982 / 08-04 | 很高 / 成长为成熟 | **C+**：功能强但较重；IAM/API 和 AI sidecar 需专项契约 |
| **SiYuan** | 单官方容器 + workspace，无外部 DB | 45,783 / 08-13 | 很高 / 成熟 | **B 个人**：最轻的强 PKM；access code 无法映射 ANAS 用户 |
| **Blinko** | 应用 + PostgreSQL；AI 需模型 API/Embedding，附件可配对象存储 | 10,867 / 08-03 | 高 / 年轻 | **A experimental**：复用 PG/OAuth2，AI 差异化强；先做迁移恢复测试 |
| **SilverBullet** | 单容器/二进制 + Markdown space；服务端现为 Rust | 5,830 / 08-11 | 高 / 成长中 | **B 个人**：极轻、userdata 直观；IAM/多用户弱 |
| **flatnotes** | 单容器 + Markdown 目录/搜索索引，无 DB | 3,178 / 08-02 | 高 / 成熟轻量 | **B 个人**：备份极简；不能消费 IAM，协作弱 |
| **Many Notes** | 单容器；SQLite 默认，文件 vault；内置 Typesense 数据目录，可换数据库 | 1,039 / 08-10 | 很高 / 成长中（2024） | **A- experimental**：OAuth 与文件可携带性好；需验证数据库/文件一致恢复 |
| **Note Mark** | 单容器；轻量持久化目录 | 760 / 08-10 | 高 / 成长中 | **A- experimental**：OIDC + REST + 低资源；社区规模小 |
| **Jotty** | 单容器 + Markdown/JSON 目录，无外部 DB | 1,977 / 08-11 | 很高 / 很年轻（2025） | **A- experimental**：OIDC/API/PWA 都匹配；升级历史短 |
| **plumio** | Node/Docker；应用数据和备份目录，具体生产数据库需 PoC 锁定 | 136 / 08-11 | 很高 / 很年轻（2026） | **C 观察**：功能方向好，历史和社区太短 |
| **HedgeDoc** | 应用 + PostgreSQL/MariaDB；可选 Redis/对象存储/邮件 | 7,364 / 08-11 | 高 / 成熟但 v1→v2 迁移需关注 | **B 团队**：PG/IAM 好；版本路线和 API 需锁定 |
| **Nextcloud Notes** | 现有 Nextcloud app，无新增数据库服务 | 732 / 08-13 | 很高 / 很成熟 | **A+**：作为 `nextcloud` 可选 app，不建新 Compose Module |
| **La Suite Docs** | Kubernetes 优先的多服务栈；Django/Next.js、PostgreSQL、Redis、对象存储等 | 16,722 / 08-12 | 很高 / 成长中（2024） | **C 团队**：能力强但超出家庭 NAS 轻量 module 范围 |
| **Anytype** | any-sync 多节点/协调服务，MongoDB/Redis 等依官方拓扑；客户端连接私有网络 | Client 8,629；compose 926 / 07-24 | 高 / 成长中 | **C**：local-first/E2EE 强；运维和身份模型与 ANAS 差异大 |
| **Lockbook** | 自建同步服务器 + 客户端；部署文档/稳定性需专项核验 | 429 / 08-12 | 高 / 成长中 | **C**：小众且无 Web/IAM/API 优势 |
| **Butterfly** | Web 静态部署或客户端 + 外部同步，不是统一多人 Server | 1,954 / 08-13 | 高 / 成长中 | **D Module**：更适合客户端说明或静态站，不是 ANAS 服务 module |
| **DailyTxT** | 单 Docker Web 应用 + 数据目录 | 494 / 06-07 | 中高 / 小而成熟 | **C**：日记专项；API/SSO 缺口 |
| **TiddlyWiki** | 单 HTML 或 Node.js server + wiki 文件 | 8,623 / 08-12 | 高 / 很成熟 | **B 个人**：非常稳定可携带；现代 IAM/协作/API 不是核心 |
| **CryptPad** | Node.js + 数据目录；生产需反代、独立域名/沙箱域与可选存储配置 | 7,841 / 08-12 | 高 / 很成熟 | **C+ 套件**：E2EE 协作优秀；部署安全拓扑和 AI/API 不适合轻笔记默认项 |

## 5. 开源自部署社区版、付费版与使用限制

本表比较的是产品边界，不是价格表。价格、容量和 entitlement 变化快，实施前必须按锁定版本重查；“无官方付费核心”不表示第三方托管或企业支持免费。

| 项目 | 开源自部署社区版 | 官方托管/付费增加什么 | 社区版使用限制与注意事项 |
| --- | --- | --- | --- |
| **Memos** | MIT，核心服务、OAuth2、API、Webhook、SQLite/PG/MySQL 均无许可证席位限制 | 上游没有统一的企业功能锁定矩阵；第三方托管按资源收费 | 自己承担备份、升级、邮件/对象存储和 AI Provider 成本；不含官方 SLA |
| **Logseq** | AGPL 客户端、DB Web 与上游 Sync/Publish 代码可构建；社区已有三镜像方案 | 官方 DB Sync 与 Publish 文档标为付费、invite-only 服务；自托管可以绕开托管订阅 | **技术限制最重要**：DB beta、RTC/新移动 alpha；社区镜像非官方 turnkey 发行，认证仍依赖 Logseq Cognito JWT，Publish 单副本且有 R2 API gap |
| **Joplin** | 客户端与基础 Server 可免费自托管；基础同步/E2EE 不要求 Cloud 订阅 | Joplin Cloud 提供 Web App、容量、AI 与托管；**Joplin Server Business 是付费自托管产品**，增加团队管理、分享权限、品牌、Email-to-Note 和优先支持 | Server 没有完整 Web 笔记编辑；SAML 已有官方 Server 文档，但必须在目标 tag 核验是否受 Business license/配置约束，不能直接标为社区版无限制 |
| **TriliumNext** | AGPL，完整核心功能免费自托管 | 无官方商业功能层，以赞助/社区维护为主 | 单用户产品模型；第三方移动 App 与自动升级兼容需自行管理 |
| **Notesnook** | 客户端 GPL、Server AGPL，可免费部署完整同步栈 | 官方托管有 Free/Essential/Pro，差异主要是存储、附件、笔记/组织配额和便利支持 | Server 明示 beta、无支持/not ready for production；邮件、MongoDB、S3 和多服务均自管，不能把 SaaS 配额套到自托管实例 |
| **Standard Notes** | Sync Server/Web/客户端代码可自部署 | 官方订阅提供托管、更多编辑器/主题、历史、附件和支持；部分高级能力依 entitlement/offline subscription | 必须对目标版本核验编辑器和扩展授权；“能自托管同步”不等于所有付费编辑器自动解锁 |
| **AppFlowy** | AGPL App 与 Cloud 可自部署；上游曾明确当前自托管能力和协作人数不受托管 Free 的 2 人/5 GB 配额限制，AI 可自带 Key | 官方 Cloud Free/Pro 按成员、存储、来宾与 AI 配额收费；官方计划把部分新增 standalone modules 作为付费 add-on | 多服务资源成本高；未来 add-on、发布、通用 OIDC/企业策略不能仅凭主仓库许可证推断为 CE 全部可用，实施时需重查当前 entitlement |
| **SiYuan** | AGPL 核心编辑、API、插件、Docker Web 与多数功能免费 | 会员主要提供官方云同步、第三方对象存储连接等服务/权益 | Docker Web 不支持桌面/移动客户端连接，导入导出也有限；官方同步权益不能由自托管 Web 替代 |
| **Blinko** | GPL 核心、AI/RAG、OAuth2 和 API 可自部署 | 官方赞助/托管生态可能收费，没有已确认的企业功能锁 | 模型调用、Embedding 和对象存储费用自理；年轻项目的 schema/迁移不受 SLA 保障 |
| **SilverBullet** | MIT，完整文件式核心、脚本/插件/API 免费 | 无官方付费功能层 | 单用户、无企业 SSO/支持；自定义脚本的安全与兼容由管理员负责 |
| **flatnotes** | MIT，完整核心和 REST API 免费 | 无官方付费功能层；PikaPods 等第三方托管收费 | 单账号/简单认证，无组织 RBAC/SSO；管理员承担 Markdown 冲突和备份 |
| **Many Notes** | MIT，多用户、vault、OAuth 与协作免费 | 无已确认的官方付费功能层 | 项目较新；数据库索引和文件双重状态必须一起恢复，无企业 SLA |
| **Note Mark** | AGPL，OIDC、API 和核心笔记免费 | 无已确认的官方付费功能层 | 社区规模小，升级兼容/支持由部署者承担 |
| **Jotty** | AGPL，OIDC、MFA、REST、PWA 和文件存储免费 | 无已确认的官方付费功能层 | 项目始于 2025；PWA 只缓存已访问页，不支持完整离线 CRUD |
| **plumio** | AGPL，多用户/组织、加密和 API 可自部署 | 无已确认的官方付费功能层 | 2026 新项目，生产数据库、迁移、SSO 与支持承诺需要 PoC，不能按活跃提交直接评为成熟 |
| **HedgeDoc** | AGPL，自托管实时协作与多种认证免费 | 官方项目不以企业功能锁为核心；托管商按资源/支持收费 | v1/v2 路线、迁移和 API 稳定性是主要限制；大规模部署自管 DB/缓存/对象存储 |
| **Nextcloud Notes** | AGPL Notes app 免费，继承自托管 Nextcloud 用户和 API | Nextcloud 企业订阅主要提供支持、认证版本和 SLA；托管商按存储/用户收费 | 受 Nextcloud/Notes 兼容矩阵约束；移动 App 来源和功能不完全一致 |
| **La Suite Docs** | MIT，可自部署协作文档核心 | 法国公共数字套件以公共服务/部署支持为主，未确认功能型商业锁 | Kubernetes 优先、多服务且资源较重；AI/身份部署细节需自行集成 |
| **Anytype** | any-sync 为 MIT；客户端和部分组件需目录级许可核验，可建立私有网络 | 官方网络套餐围绕托管存储、共享和便利服务 | 不能把 any-sync 的 MIT 等同于整个产品均同许可；私有网络拓扑、客户端发现和支持由管理员负责 |
| **Lockbook** | 客户端/Server 可自行部署 | 官方托管免费/付费层主要按云存储容量和便利支持区分 | 自托管无 Web/SSO/通用业务 API，且小社区部署文档需专项验证 |
| **Butterfly** | AGPL 客户端与 Web 构建免费 | 无主要企业功能层，以捐赠/商店分发为主 | 没有统一多人服务；Web 自部署不自动获得跨设备同步和组织账号 |
| **DailyTxT** | MIT，日记 Web 核心免费 | 无官方付费功能层 | 功能面、API、SSO 和社区规模有限；适合专项单实例 |
| **TiddlyWiki** | BSD，单文件/Node 核心和插件体系免费 | 无官方付费功能层 | 多用户并发、SSO、审计和支持不是核心产品契约 |
| **CryptPad** | AGPL，可部署 E2EE 协作套件并由管理员设配额 | 官方 cryptpad.fr 付费主要增加托管存储；机构支持/定制另计 | 安全来源隔离、域名和备份配置复杂；E2EE 使服务端搜索/AI 天然受限，SSO/高级实例能力需按支持方案核验 |

## 6. 相邻、开放核心与状态风险

### 6.1 团队知识库相邻候选

| 项目 | 为什么相关 | 社区版关键边界 | 结论 |
| --- | --- | --- | --- |
| **Docmost**（AGPL-3.0，21,344 Star，08-13 push） | 现代团队 wiki、实时编辑、空间/组/权限/评论 | PostgreSQL + Redis；SAML/OIDC/LDAP 等高级 SSO 主要在企业版，公共 API 也不是个人笔记优势 | 若研究主题改为“团队 wiki/文档”，应进入主表；本轮不做默认笔记 Module |
| **BookStack** | 成熟书架/章节/页面式知识库 | Web-only，OIDC/SAML/LDAP 和 REST API 成熟；不是离线个人笔记 | ANAS 团队知识库候选，另开 wiki 主题研究 |
| **Wiki.js / XWiki / DokuWiki** | 成熟 wiki 与权限/扩展 | 产品模型、依赖和客户端逻辑都与个人笔记不同 | 不在本轮横向排名，避免用 Star 扭曲结论 |
| **Outline** | 优秀团队知识库与 OIDC/API | 当前许可证/商业功能边界不属于本轮严格开源默认口径 | 放到 source-available/open-core 专项 |

### 6.2 开放核心或许可需要单独判断

| 项目 | 当前风险 | 处理 |
| --- | --- | --- |
| **AFFiNE**（71,501 Star，08-12 push） | 仓库包含社区与企业许可边界，AlternativeTo 当前甚至标为 proprietary；AI、SSO、移动端和自托管能力也有版本/商业层差异 | 不能只因源码公开写“完全开源”。若进入 ANAS，先对锁定 tag 做目录级许可证与 CE 功能审计 |
| **AppFlowy** | 主代码 AGPL，但托管/AI/企业身份能力可能并非全部属于同一免费自部署契约 | 本报告仅认可可从 AGPL 仓库和官方 self-host Compose 验证的部分 |
| **Standard Notes** | 客户端、Server、Web、编辑器与订阅功能分布在不同仓库和计划 | 部署前做组件清单和许可/功能矩阵，不把官方 SaaS 计划自动算入自托管社区版 |

### 6.3 停止、继任或不建议新生产部署

| 项目 | 状态 | 建议 |
| --- | --- | --- |
| **Turtl** | AlternativeTo 明确标 Discontinued；旧客户端/服务端虽开源但不应新部署 | Notesnook、Joplin、Standard Notes |
| **原 Trilium Notes** | 原维护者已将项目交接给 TriliumNext | 只研究和部署 TriliumNext，不锁旧仓库镜像 |
| **CodiMD** | HedgeDoc 的旧名称/前身 | 新部署选择 HedgeDoc 并核验 v1/v2 路线 |
| **Laverna / Leanote / Notea** | 常见旧清单条目，但维护、release 或服务端安全基线已不足 | 只保留迁移研究，不进入新 Module PoC |
| **Notesnook Sync Server** | 上游 README 称 self-hosting 可用但无支持，release 仍标 beta/not ready for production | 等稳定 release、迁移文档和受支持镜像后重评 |

## 7. 重点候选详评

### 7.1 Memos：当前最佳默认 Module

优势：官方文档覆盖 Docker/Compose/Kubernetes、反向代理、安全、备份恢复、REST/gRPC、Webhook 和 OAuth2；0.30+ 还能从 `/etc/secrets` 挂载权威 SSO、存储、通知和 AI 设置，特别适合 ANAS 的声明式渲染。PAT 只存 SHA-256 hash，自动化不必模拟浏览器会话。应用能从 SQLite 起步，也能复用 ANAS 关系数据库能力。

风险：信息模型偏短内容流，复杂长文档、手写、E2EE 和原生移动离线不是强项。官方移动端生态弱于 Joplin/Notesnook。部署 PoC 应测试 OAuth2 首次建号/禁注册组合、PAT、附件 S3/本地切换、PostgreSQL 备份恢复和跨版本迁移。

建议：`memos`，可先 `status: experimental`；依赖 `traefik`，数据库以 `db_type: auto` 允许 SQLite/PG，IAM 接口用 OAuth2/OIDC，稳定后再升为默认 stable。

### 7.2 Nextcloud Notes：现有栈的最低总成本方案

ANAS 已有 Nextcloud 34、SAML/LDAP、数据库、HTTPS、备份和 app reconcile。Notes 只增加一个兼容版本锁定的 Nextcloud app，并提供 REST API 与 Android/iOS 客户端。账号、域名、证书和运维入口都无需重复。

建议把它做成 `nextcloud.config.notes_enabled`，与 Deck 类似通过 Nextcloud app 生命周期安装。测试覆盖 Markdown CRUD、分类、收藏、REST Token、移动客户端登录和升级兼容；不要新建独立 `notes` Compose Module。

### 7.3 Blinko：最有差异化的 AI 候选

Blinko 把快速卡片、RAG 检索、模型 Provider、附件与公开分享放在同一产品里。自托管只需应用 + PostgreSQL；通用 OAuth2 的 well-known/authorize/token/userinfo 字段能对接 ANAS IdP，Bearer API 也适合自动化。

风险是项目创建于 2024-10，AI schema、Embedding 迁移和 Provider 兼容变化比传统笔记更快。PoC 必须验证：不配置外部 AI 时能否完全离线运行、Ollama/OpenAI-compatible URL、Embedding 维度变更、附件备份、API Token 轮换、amd64/arm64 镜像与回滚。

### 7.4 Joplin：客户端最成熟，但 Server 不是 Web App

Joplin 的强项是经过多年验证的桌面/移动客户端、离线优先、E2EE、Web Clipper、导入导出与插件生态。Server 生产可用 PostgreSQL，官方已有 SAML 文档，与 ANAS LLNG 的 SAML Provider 方向相符。

最大误区是把 Server 管理页写成 Web 笔记客户端。用户仍需安装 Joplin App；官方 Data API 由桌面 Web Clipper 服务或插件暴露，不能直接承诺为服务器端集成 API。若用户的硬要求是“浏览器随时编辑”，Memos/Nextcloud Notes/Blinko 更直接。

### 7.5 Logseq：为什么必须纳入、又为什么暂不做生产 Module

Logseq 是 Obsidian/Roam 路线最重要的开源候选之一：AGPL、local-first、Markdown/Org 大纲、双链、任务、白板、插件，经典客户端覆盖桌面和移动。它从上一版报告消失不是因为不重要，而是研究流程错误地把“非传统服务端”当成了排除条件，且没有留下排除记录。

2026 年情况已经变化。官方 DB 文档明确写明 Sync/RTC 和 Publish 可以配置自托管 URL，DB Web 也能从源码部署；官方主仓库同时警告 DB 版仍为 beta，RTC 与新移动 App 仍为 alpha，并可能丢数据。官方文档引用的 `yshalsager/logseq-selfhost` 提供 Web、Sync、Publish 三个 AGPL 镜像，但它是 2026-04 才建立、86 Star 的社区打包：Sync 需要 `COGNITO_*` JWT 配置；Publish 仍通过 Wrangler/Miniflare 模拟 Cloudflare Durable Objects/R2、只建议单副本，并明确有 raw transit API gap。

因此 Logseq 应作为 `B 观察/C 当前`：持续跟踪并做实验 PoC，但在上游提供稳定 release、独立认证、升级/恢复文档和不依赖补丁的 Compose 前，不承诺生产 Module。PoC 要分开测“经典文件版 + WebDAV/Nextcloud 文件同步”和“DB Web + 自托管 RTC”，不能混成同一种架构。

### 7.6 TriliumNext 与 SiYuan：强 PKM、弱统一身份

TriliumNext 的树、克隆、关系、脚本、ETAPI 和大量笔记性能成熟，社区接续后仍非常活跃；SQLite 单目录也很适合快照。移动主要靠 Web 或第三方 App，且系统本质偏单用户。

SiYuan 的块引用、数据库、PDF 标注、OCR、闪卡、插件、移动端和 OpenAI API AI 功能更全面，单容器部署非常轻。但 Docker Web 使用 access auth code；官方明确 Docker 端不支持桌面/移动 App 连接，也不应把它描述为统一同步服务器。两者都可做“每用户一个实例”，但这会放大域名、备份和资源编排，不适合 ANAS 首个通用笔记模块。

### 7.7 文件优先轻量组

- **Note Mark**：OIDC + REST + 移动友好，综合最像 ANAS 轻量多用户 Module；Star 不高，需做权限和恢复测试。
- **Many Notes**：支持 Authentik/Keycloak/Zitadel 等 OAuth 适配、多人 vault 和 PWA，文件仍落盘；数据库索引与文件恢复的一致性是关键。
- **Jotty**：OIDC、MFA、REST、PWA、Markdown/JSON，无外部 DB；项目年轻但上升很快。
- **flatnotes**：最简单可审计，Markdown 不锁定；认证只有本地/TOTP，适合个人而不是统一家庭账号。
- **SilverBullet**：脚本、查询和插件能力最强，适合技术用户；单用户身份模型和可编程性增加安全审计面。

## 8. ANAS 实施建议

### 8.1 推荐路线

| 优先级 | 工作 | 结果 |
| --- | --- | --- |
| P0 | 为现有 Nextcloud 加 `notes_enabled` 与兼容版本锁 | 最快提供 Web + 移动笔记，不新增服务 |
| P1 | `memos` PoC | 验证默认独立笔记 Module、OAuth2、PG、API、备份 |
| P1 | `blinko` experimental PoC | 验证 AI/RAG、OIDC、PG、模型数据外发边界 |
| P2 | `note-mark` 或 `jotty` 二选一 PoC | 提供文件优先、低资源、OIDC/API 的简单路线 |
| P2 | Joplin Server 专项 | 为全平台 E2EE 用户验证 SAML、客户端同步和 PG 恢复 |
| P3 | TriliumNext 个人实例 | 只在产品明确支持单用户/每人实例时加入 |
| 观察 | Logseq DB self-host 实验 | 跟踪 DB/RTC 从 beta/alpha 转稳定、独立认证与官方 Compose；暂不进入生产 Module 路线 |

### 8.2 初步 Module 依赖

```text
memos        -> traefik + iam(oauth2/oidc) + relational_database(optional)
blinko       -> traefik + iam(oauth2/oidc) + relational_database(postgres)
note-mark    -> traefik + iam(oidc)
jotty        -> traefik + iam(oidc)
joplin       -> traefik + iam(saml) + relational_database(postgres)
trilium      -> traefik; single-user secret, no iam capability claim
logseq-db    -> traefik + external/custom auth + Web/Sync/(optional Publish); experimental only
nextcloud-notes -> existing nextcloud app reconcile only
```

不要为了让无账号映射的应用看起来“支持 SSO”而统一套 oauth2-proxy。入口保护可以作为安全层，但移动客户端、公开分享、REST API、WebSocket 和应用用户身份都可能因此失效。

### 8.3 PoC 验收清单

每个入围项目都应固定 release/tag 与镜像 digest，验证 amd64/arm64、非 root、健康检查、Traefik WebSocket/上传、首次管理员初始化、禁止开放注册、SSO 首次建号与退出、API Token、完整数据备份/空机恢复、上一个稳定版本升级、失败迁移回滚、10k 笔记搜索与大附件。AI 项目另测禁用 AI 的纯离线模式、模型 endpoint、密钥落盘、RAG 重建和数据是否离开实例。

## 9. 动态数据明细

以下数据来自 GitHub 官方仓库 API，采集于 2026-08-13。`最近 push` 是仓库事件信号，不代表稳定 release。

| 仓库 | Star | 创建 | 最近 push | 归档 | API 许可证标识 |
| --- | ---: | --- | --- | --- | --- |
| `usememos/memos` | 62,207 | 2021-12 | 2026-08-13 | 否 | MIT |
| `logseq/logseq` | 44,425 | 2020-05 | 2026-08-13 | 否 | AGPL-3.0 |
| `yshalsager/logseq-selfhost` | 86 | 2026-04 | 2026-08-08 | 否 | AGPL-3.0 |
| `laurent22/joplin` | 55,925 | 2017-01 | 2026-08-12 | 否 | 未识别（需按 LICENSE/包核验） |
| `TriliumNext/Trilium` | 37,390 | 2017-05 | 2026-08-13 | 否 | AGPL-3.0 |
| `streetwriters/notesnook` | 14,401 | 2021-04 | 2026-08-13 | 否 | GPL-3.0 |
| `streetwriters/notesnook-sync-server` | 909 | 2022-12 | 2026-08-10 | 否 | AGPL-3.0 |
| `standardnotes/app` | 6,582 | 2016-12 | 2026-08-11 | 否 | AGPL-3.0 |
| `standardnotes/server` | 469 | 2022-06 | 2026-04-11 | 否 | GPL-3.0 |
| `AppFlowy-IO/AppFlowy` | 75,299 | 2021-06 | 2026-08-11 | 否 | AGPL-3.0 |
| `AppFlowy-IO/AppFlowy-Cloud` | 1,982 | 2021-06 | 2026-08-04 | 否 | AGPL-3.0 |
| `siyuan-note/siyuan` | 45,783 | 2020-08 | 2026-08-13 | 否 | AGPL-3.0 |
| `blinkospace/blinko` | 10,867 | 2024-10 | 2026-08-03 | 否 | GPL-3.0 |
| `silverbulletmd/silverbullet` | 5,830 | 2022-02 | 2026-08-11 | 否 | MIT |
| `dullage/flatnotes` | 3,178 | 2021-08 | 2026-08-02 | 否 | MIT |
| `hedgedoc/hedgedoc` | 7,364 | 2019-03 | 2026-08-11 | 否 | AGPL-3.0 |
| `enchant97/note-mark` | 760 | 2021-02 | 2026-08-10 | 否 | AGPL-3.0 |
| `brufdev/many-notes` | 1,039 | 2024-07 | 2026-08-10 | 否 | MIT |
| `fccview/jotty` | 1,977 | 2025-08 | 2026-08-11 | 否 | AGPL-3.0 |
| `albertasaftei/plumio` | 136 | 2026-01 | 2026-08-11 | 否 | AGPL-3.0 |
| `suitenumerique/docs` | 16,722 | 2024-01 | 2026-08-12 | 否 | MIT |
| `nextcloud/notes` | 732 | 2016-10 | 2026-08-13 | 否 | AGPL-3.0 |
| `anyproto/anytype-ts` | 8,629 | 2023-05 | 2026-08-10 | 否 | 未识别（需目录级核验） |
| `anyproto/any-sync-dockercompose` | 926 | 2023-03 | 2026-07-24 | 否 | MIT |
| `lockbook/lockbook` | 429 | 2020-01 | 2026-08-12 | 否 | Unlicense |
| `LinwoodDev/Butterfly` | 1,954 | 2020-12 | 2026-08-13 | 否 | AGPL-3.0 |
| `PhiTux/DailyTxT` | 494 | 2020-12 | 2026-06-07 | 否 | MIT |
| `TiddlyWiki/TiddlyWiki5` | 8,623 | 2011-11 | 2026-08-12 | 否 | API 未识别（项目声明 BSD-3-Clause） |
| `cryptpad/cryptpad` | 7,841 | 2014-10 | 2026-08-12 | 否 | AGPL-3.0 |
| `docmost/docmost` | 21,344 | 2023-08 | 2026-08-13 | 否 | AGPL-3.0 |
| `toeverything/AFFiNE` | 71,501 | 2022-07 | 2026-08-12 | 否 | 未识别（混合许可需核验） |

## 10. 主要上游依据

发现目录只用于候选枚举。关键事实优先来自下列上游：

- Memos：[官方文档](https://usememos.com/docs)、[认证与 PAT](https://usememos.com/docs/configuration/authentication)、[部署托管配置](https://usememos.com/docs/configuration/deployment-configuration)、[API](https://usememos.com/docs/api/latest)、[仓库](https://github.com/usememos/memos)
- Logseq：[主仓库与 DB beta/RTC alpha 状态](https://github.com/logseq/logseq)、[DB 版功能、付费托管与自托管 URL](https://github.com/logseq/docs/blob/master/db-version.md)、[自托管 Web/Sync/Publish 社区镜像](https://github.com/yshalsager/logseq-selfhost)、[AlternativeTo 产品页](https://alternativeto.net/software/logseq/about/)
- Joplin：[Server 部署](https://github.com/laurent22/joplin/blob/dev/packages/server/README.md)、[SAML](https://joplinapp.org/help/apps/server/saml/)、[Data API](https://joplinapp.org/help/api/references/rest_api/)、[AI Chat API](https://joplinapp.org/help/dev/spec/ai_chat/)
- TriliumNext：[仓库与平台说明](https://github.com/TriliumNext/Trilium)、[ETAPI](https://docs.triliumnotes.org/user-guide/advanced-usage/etapi)、[Server 安装](https://docs.triliumnotes.org/user-guide/setup/server)
- Notesnook：[客户端仓库](https://github.com/streetwriters/notesnook)、[Sync Server README](https://github.com/streetwriters/notesnook-sync-server)、[Compose](https://github.com/streetwriters/notesnook-sync-server/blob/master/docker-compose.yml)、[beta releases](https://github.com/streetwriters/notesnook-sync-server/releases)
- AppFlowy：[主仓库](https://github.com/AppFlowy-IO/AppFlowy)、[Cloud 仓库](https://github.com/AppFlowy-IO/AppFlowy-Cloud)、[部署架构](https://docs.appflowy.io/docs/documentation/appflowy-cloud/deployment)
- SiYuan：[官方仓库、功能与 Docker 限制](https://github.com/siyuan-note/siyuan)
- Blinko：[仓库与 AI 功能](https://github.com/blinkospace/blinko)、[安装](https://docs.blinko.space/zh/install)、[SSO](https://docs.blinko.space/en/settings/sso)、[API Token](https://docs.blinko.space/en/settings/access-token)
- 轻量组：[flatnotes](https://github.com/dullage/flatnotes)、[Many Notes](https://github.com/brufdev/many-notes)、[Note Mark](https://github.com/enchant97/note-mark)、[Jotty](https://github.com/fccview/jotty)、[plumio](https://github.com/albertasaftei/plumio)、[SilverBullet](https://github.com/silverbulletmd/silverbullet)
- 其他：[HedgeDoc 文档](https://docs.hedgedoc.org/)、[Nextcloud Notes](https://github.com/nextcloud/notes)、[La Suite Docs](https://github.com/suitenumerique/docs)、[Anytype self-host](https://tech.anytype.io/how-to/self-hosting)、[CryptPad 管理指南](https://docs.cryptpad.org/en/admin_guide/)
- 商业边界：[Joplin Cloud/Server Business 对比](https://joplinapp.org/plans/)、[Notesnook Plans](https://notesnook.com/pricing)、[Standard Notes Plans](https://standardnotes.com/plans)、[Standard Notes 自托管离线订阅](https://standardnotes.com/help/48/can-i-use-extensions-with-a-self-hosted-server)、[AppFlowy Pricing](https://appflowy.com/pricing)

## 11. 下次更新

在 2026-11-13 前，或在实施任何候选 Module 前，重新采集许可证、Star、release、镜像架构、社区版/付费版功能边界、SSO/AI entitlement 和自托管服务稳定标签。优先关注 Logseq DB/RTC 是否脱离 beta/alpha 并提供独立认证与官方 Compose、Notesnook Server 是否脱离 beta、AFFiNE 许可边界、AppFlowy Cloud 的通用 OIDC、Memos AI 配置契约及 TriliumNext 移动客户端状态。
