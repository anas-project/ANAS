---
doc_type: research
created: 2026-08-21
updated: 2026-08-21
evidence_as_of: 2026-08-21
---

# Mastodon 相关开源自部署服务研究

本报告按[应用研究文档规范](/developer/research-document-standard)研究 Mastodon、兼容的联邦微博服务及相邻 ActivityPub 应用，为 ANAS 后续 Runtime Module 选型提供依据。动态版本、文档和维护状态采集于 2026-08-21；报告是研究快照，不是当前部署说明。

## 1. 结论先行

1. **ANAS 的小型实例默认候选应是 GoToSocial，而不是 Mastodon。** 它面向小型、低资源实例，官方给出的典型内存约为 250–350 MiB，支持 amd64/arm64、SQLite/PostgreSQL、S3-compatible 存储、OIDC 与 Mastodon API 客户端。代价是它采用 backend-first 设计，没有 Mastodon 那样完整的内置 Web 客户端，且部分 API 和跨实现联邦行为仍不完全等价。
2. **Mastodon 是公共社区与兼容性基准。** 当完整 Web UI、最广泛的第三方客户端兼容、成熟审核工具、角色权限、横向扩展与大型社区运维资料是硬需求时，应选择 Mastodon。它至少包含 Web、Streaming、Sidekiq、PostgreSQL、Redis 等组件，搜索与翻译还会增加 Elasticsearch/OpenSearch、LibreTranslate，因此不适合作为普通家庭 NAS 的无条件默认项。
3. **Akkoma 是功能丰富、相对轻量的进阶备选。** 它兼容 Mastodon/ActivityPub，支持更长文本、Markdown/MFM、emoji reaction、本地帖、多个前端、LDAP 和可扩展 OAuth consumer；但官方 Docker 路径需要拉源码、构建镜像、运行 Mix 编译与迁移，不能直接包装成“换几个环境变量即可”的稳定 Module。
4. **Misskey 适合明确喜欢其产品体验的社区，不是 Mastodon 的透明替代。** 它的频道、天线、Drive、角色、插件、AiScript、reaction 和完整 Web UI 很强，但使用自己的 API/MiAuth 生态，官方建议约 4 GB 内存，依赖 PostgreSQL + Redis，升级迁移频繁；OIDC client API 也不等于用外部 OIDC 登录 Misskey。
5. **Pleroma 仍在维护，但新 ANAS Module 不必与 Akkoma 同时首发。** Pleroma 2.10.2 在 2026 年仍有发布和开发活动，不能标成停止维护；二者部署、API 和用户群高度重叠，应先通过一轮真实 PoC 决定只维护一个 Elixir/BEAM 系候选。
6. **glitch-soc、Sharkey 等 fork 只在明确需要差异功能时选。** glitch-soc 紧跟 Mastodon 并持续发布，Sharkey 增强了 Misskey 与 Mastodon API 兼容；但 fork 会增加安全补丁同步、数据库迁移和文档偏差的验证成本。Hometown 的上游同步历史较慢，不作为新部署默认项。
7. **Pixelfed、PeerTube、Lemmy、WriteFreely、Friendica 是相邻服务，不应塞进“通用 Mastodon 替代”一张功能表。** 它们分别偏图片、视频、论坛、博客和 Facebook 式关系网络；可以与 Mastodon 用户互相关注或交互，但产品、存储和 API 契约不同，应作为独立 Module 研究。
8. **联邦身份的核心资产是域名、数据库中的 actor URI 与签名私钥。** `LOCAL_DOMAIN`、账号域名等在开始联邦后通常不能安全修改；若丢失数据库或签名密钥，同域重装也不能让远端把新 actor 当成原账号。正式上线前必须固定永久域名，并完成数据库、密钥、配置和本地媒体的恢复演练。
9. **ActivityPub 不是完整迁移协议。** `Move` 通常只把关注者引导到新账号，不会搬走帖子、收藏、书签、列表、媒体和所有关系；GoToSocial 的帖子导入也明确是重新创建副本。选型不能建立在“以后随时无损换后端”的假设上。
10. **联邦服务是公开互联网工作负载，也是内容治理工作负载。** 即使只有一名本地用户，服务仍会主动访问远端 actor、媒体和链接并接收外部请求。SSRF 防护、可信代理、邮件、限流、远端媒体留存、域名封禁、举报处理、对象存储公开读取边界和安全更新都属于最小上线范围。

## 2. 范围与方法

```yaml
topic: mastodon-related-self-hosted-services
title: Mastodon 相关开源自部署服务
snapshot_date: 2026-08-21
decision_for: ANAS module 候选
must_be:
  - 源代码可获得且允许自行部署
  - 使用 ActivityPub 与 Mastodon 或更广泛 Fediverse 联邦
core_categories:
  - 通用联邦微博/社交后端
  - Mastodon 或 Misskey 的活跃 fork
adjacent_categories:
  - 图片、视频、论坛、博客和关系网络服务
excluded:
  - SaaS-only 服务
  - 只有客户端而不提供联邦服务端的项目
  - 只有实验代码、无可维护部署路径的 ActivityPub 实现
target_users:
  - 个人、家庭和小型可信社区
expected_scale: 1 至 20 本地用户，低至中等并发
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  architectures: [amd64, arm64]
questions:
  - 哪个项目适合作为 ANAS 默认联邦社交 Module？
  - Mastodon 兼容究竟覆盖联邦、客户端 API 还是完整产品体验？
  - IAM、备份、域名、对象存储和审核边界是什么？
search_date: 2026-08-21
```

本报告优先使用项目官网、官方文档、上游仓库、release 和安全公告。版本仅表示证据日可见信号；正式 Module 必须固定镜像 digest，并在每次升级前读取所有跨越版本的迁移说明。

“Mastodon 相关”分三层理解：

- **协议互通**：实现 ActivityPub，可与 Mastodon actor 互相关注或交换活动；不保证每种帖子、投票、reaction、quote、可见性和删除行为一致。
- **客户端 API 兼容**：实现部分或大部分 Mastodon REST/Streaming API，可使用 Tusky 等客户端；不保证所有客户端和新 API 无差异工作。
- **产品等价**：拥有类似的 Web UI、审核、搜索、注册、导入导出和管理体验。只有协议或 API 兼容不等于产品等价。

## 3. 动态维护快照

| 项目 | 2026-08-21 可见信号 | 许可证 | 判断 |
| --- | --- | --- | --- |
| Mastodon | [4.7 于 2026-08-20 宣布](https://blog.joinmastodon.org/2026/08/mastodon-4.7/)；证据日上游生产 Compose 仍引用 4.6.5 | AGPL-3.0 | 活跃、成熟；4.7 刚发布，先核验制品并做升级演练 |
| GoToSocial | 官方文档已发布 `v0.22.0` 路径，`latest` 文档持续更新 | AGPL-3.0 | 活跃；小实例优先，但需验证 API/联邦兼容 |
| Akkoma | [`stable 2026.08`](https://meta.akkoma.dev/t/akkoma-stable-2026-08-searching-for-rubies/951)，2026-08-08 | AGPL-3.0 | 活跃；Docker 交付路径偏源码构建 |
| Pleroma | [`2.10.2`](https://git.pleroma.social/pleroma/pleroma/)，2026-05；8 月仍有开发活动 | AGPL-3.0 | 活跃；与 Akkoma 重叠，二选一验证 |
| Misskey | [`2026.7.0`](https://github.com/misskey-dev/misskey/releases/tag/2026.7.0)，2026-07-31 | AGPL-3.0 | 活跃、功能密集、升级节奏快 |
| glitch-soc | 2026 年持续构建 Mastodon 4.6/4.7 对应镜像 | AGPL-3.0 | 活跃 fork；仅差异功能驱动时选 |
| Pixelfed | [`0.12.7`](https://github.com/pixelfed/pixelfed/releases/tag/v0.12.7)，2026-02-17 | AGPL-3.0 | 活跃；图片社区，不是通用微博默认项 |
| Friendica | [`2026.05-1`](https://github.com/friendica/friendica/releases/tag/2026.05-1)，2026-05-21 | AGPL-3.0 | 活跃；关系网络与跨协议场景 |
| PeerTube | [`8.2.4`](https://builds.joinpeertube.org/release/)，2026-08-04 | AGPL-3.0 | 活跃；视频工作负载，应单独选型 |
| Lemmy | 2026 年仍有 0.19 系发布；官方 Docker/Ansible 路径持续维护 | AGPL-3.0 | 活跃；社区论坛/链接聚合 |
| WriteFreely | [`0.17.0`](https://github.com/writefreely/writefreely/releases/tag/v0.17.0)，2026-07-17 | AGPL-3.0 | 活跃；极简联邦博客 |
| Bonfire Social | 1.0 系；官方声明 Social flavour 可用于生产 | AGPL-3.0 | 新兴、模块化；生态与恢复路径需 PoC |
| Takahē | stable 文档持续更新 | BSD-3-Clause | 多域名差异化明显；官方仍提示功能不完整 |

Mastodon 4.7 在证据日前一天宣布，官方文章强调包含长时间数据库迁移，并支持从 4.6 到 4.7 的零停机迁移；这不是“可以立刻让所有小实例自动升级”的同义词。ANAS PoC 应保留 4.6.x 恢复点和 4.7 升级路径，核验上游 release tag、容器镜像和 Compose 引用已经一致后再固定版本。

## 4. 核心候选比较

符号：✅ 官方原生；◐ 部分兼容、插件/策略或需额外组件；❌ 没有；— 本轮未确认。

| 项目 | 完整 Web 客户端 | Mastodon 客户端 API | 数据栈 | 外部身份 | S3 | 相对重量 | 最适合 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **GoToSocial** | ◐ 设置/管理/公开页；日常客户端外置 | ◐ 广泛但非完整 | SQLite 或 PostgreSQL | ✅ OIDC，支持 allowed/admin groups | ✅ | 很轻 | 1–20 人、个人/家庭、ARM NAS |
| **Mastodon** | ✅ | ✅ 参考实现 | PostgreSQL + Redis；可选搜索/翻译 | ✅ OIDC、LDAP、CAS、PAM | ✅ | 中到重 | 公共社区、最大兼容性、可扩展审核 |
| **Akkoma** | ✅ 多前端 | ◐ Mastodon + 自有扩展 | PostgreSQL + BEAM | ◐ LDAP；OAuth consumer 依赖策略 | ◐ 可配置远端 uploader | 轻到中 | 长文、reaction、本地帖、可定制社区 |
| **Pleroma** | ✅ 多前端 | ◐ Mastodon + 自有扩展 | PostgreSQL + BEAM | ◐ LDAP/OAuth consumer | ◐ | 轻到中 | 既有 Pleroma 经验或实例 |
| **Misskey** | ✅ | ❌ 主要是 Misskey API/MiAuth | PostgreSQL + Redis | — 未发现官方通用外部 OIDC/LDAP 登录 | ✅ | 中 | 频道、天线、Drive、reaction、插件体验 |
| **glitch-soc** | ✅ | ✅，继承 Mastodon | 与 Mastodon 相同 | 与 Mastodon 相同 | ✅ | 中到重 | 明确需要其 UI/发帖差异且愿维护 fork |

### 4.1 GoToSocial：ANAS 小实例默认候选

[GoToSocial 官方介绍](https://docs.gotosocial.org/en/latest/)给出的典型内存约为 250–350 MiB，运行时除数据库外不要求 Redis、Sidekiq、Node 或单独媒体处理服务；[官方容器安装](https://docs.gotosocial.org/en/stable/getting_started/installation/container/)可以单容器 + SQLite 起步。amd64 与 arm64 是完整支持平台，适合 ANAS 的多架构目标。

它不是“用 Go 重写的完整 Mastodon UI”。公开 profile/post 页面、用户设置和管理员界面由服务端提供，但日常时间线和发帖主要依赖 Mastodon API 客户端。官方说明大多数 Mastodon app 可工作，并列出测试客户端；正式 PoC 仍应以 ANAS 选定的 Web、Android、iOS 客户端逐项验证登录、媒体、通知、投票、quote、reaction、列表、推送和账户迁移。

[OIDC 配置](https://docs.gotosocial.org/en/latest/configuration/oidc/)可用 `sub` 关联账号，支持 allowed groups 与 admin groups，和 ANAS 的 authentik 契约匹配度高。必须注意：

- OIDC 启用后基本替代本地密码登录，IdP 可用性进入登录关键路径；
- 首次创建仍需要唯一 email，用户名在 ActivityPub 中不可随 IdP `preferred_username` 一起变化；
- 只应在迁移窗口临时启用按 email 关联既有账号，官方明确提示存在账号接管风险。

本地媒体和 S3 可相互迁移，[存储文档](https://docs.gotosocial.org/en/latest/configuration/storage/)提供同步步骤。SQLite 适合个人/家庭起步；公开注册、多用户或需要数据库统一治理时优先 PostgreSQL。SQLite 的清理不会自动缩小文件，长期实例需要按[数据库维护文档](https://docs.gotosocial.org/en/v0.22.0/admin/database_maintenance/)安排 `VACUUM`/`ANALYZE` 和空闲空间。

主要限制：

- API 兼容和跨软件联邦不能视为完整 Mastodon 等价；
- backend-first 模式需要明确推荐客户端，不能交付后只留下一个“能看 profile 的网页”；
- 它强调许多小实例，不适合默认承诺超大关注者账号或大型公共实例；
- 官方文档不同页面可能随功能演进存在更新时差，Module 不能从单个页面自动推导能力。

### 4.2 Mastodon：成熟公共社区候选

[Mastodon 上游](https://github.com/mastodon/mastodon)提供官方容器和面向生产服务器的 Compose 示例。最小应用面仍包含：

```text
Traefik
   ├── Mastodon web / Puma
   ├── Mastodon streaming
   └── Sidekiq worker
          ├── PostgreSQL
          ├── Redis
          ├── local media or S3
          ├── SMTP
          ├── optional Elasticsearch/OpenSearch
          └── optional translation service
```

它的优势是产品、协议和客户端生态的共同参考实现：完整 Web UI、REST/Streaming API、注册审批、角色、举报与域名审核、数据导入导出、推送、管理 CLI、横向拆分与健康检查都已有官方资料。[扩展文档](https://docs.joinmastodon.org/admin/scaling/)覆盖多 Web/Streaming/Sidekiq、PgBouncer、Redis 拆分/Sentinel、PostgreSQL read replica 和负载均衡。

成本来自拓扑和升级，而不只是容器数量。4.7 引入长时间数据库迁移和本地 actor 私钥加密；升级时必须阅读所有跳过版本的说明。官方 Compose 示例只是可工作的起点，ANAS 仍需替换其中的数据库信任方式、绑定持久卷、生成并保护 Rails/VAPID/Active Record encryption secrets、接入 Traefik/SMTP/备份，而不能原样发布。

身份方面，[环境配置](https://docs.joinmastodon.org/admin/config/)和上游初始化代码支持 OIDC、LDAP、CAS、PAM 等。OIDC `uid_field` 应固定为稳定 `sub`；不要开启基于相同 email 的不安全 provider 重新绑定。SSO 只解决本地登录，不会让 Fediverse 账号跟随 IAM username 改名，也不等于 IdP 登出会撤销 Mastodon API token 或全部客户端会话。

### 4.3 Akkoma 与 Pleroma：可定制的 BEAM 系候选

[Akkoma](https://docs.akkoma.dev/stable/)由 backend、akkoma-fe 和可选 Mastodon frontend 组成，支持 Mastodon API 与自己的扩展。其差异化功能包括 Markdown/MFM、更灵活的 reaction、本地帖、多个前端、消息重写与较强的实例配置自由度。

问题主要在可重复部署。官方[Docker 安装](https://docs.akkoma.dev/stable/installation/docker_en/)中的容器只是依赖包装：需要 clone `stable`、本地 build、`mix deps.get`、compile、生成配置、数据库迁移并另外安装前端。ANAS 若选择它，应自行产出固定源码提交的可复现镜像和前端制品，而不是在用户 NAS 上每次更新时拉源码编译。

身份接入也比 GoToSocial 更需要工程验证。[配置表](https://docs.akkoma.dev/stable/configuration/cheatsheet/)支持 LDAP，并通过 Ueberauth strategy 接入 Keycloak 等 OAuth provider；策略是单独依赖，不能把它描述成开箱即用的通用 OIDC。需要验证 authentik 的 claims、账号创建、email 变化、禁用用户和管理员角色映射。

Pleroma 与 Akkoma 共享大量概念，但不是停止维护项目：上游 2026 年仍发布 2.10.2 并合并 grouped notification 等改动。ANAS 不宜为了目录完整同时承担两套近似 Module；更实际的门禁是同一硬件上对比资源、中文搜索、前端、OIDC/LDAP、升级和恢复后选一套。

### 4.4 Misskey：产品体验优先候选

Misskey 的价值不是“更轻的 Mastodon”，而是完全不同的社交产品：reaction、频道、天线、Drive、角色与 policy、聊天、插件/Play/AiScript 等都很突出。[官方 Docker Compose 指南](https://misskey-hub.net/en/docs/for-admin/install/guides/docker/)和 Docker Hub 镜像可部署，核心仍需 PostgreSQL、Redis 和持久媒体；官方详细指南建议约 [4 GB 内存](https://misskey-hub.net/en/docs/for-admin/install/guides/ubuntu-manual/)。

兼容边界必须写清：

- Misskey 与 Mastodon 通过 ActivityPub 联邦，但客户端使用 Misskey API/MiAuth；
- Misskey 的 OAuth 2.0 是给第三方 app 获取 token，[官方明确说明不支持 OAuth via OpenID Connect](https://misskey-hub.net/en/docs/for-developers/api/token/oauth/)；这不能计为 authentik SSO；
- reaction、MFM、quote、频道和 Drive 等扩展到 Mastodon 端可能降级或不可见；
- 官方警告实例启用后不要修改 domain/hostname 或 ID 生成方式；
- 2026.7.0 改变 Node/CPU 等运行要求，老 x86_64 机器还需检查 SSE4.2。

如果用户明确要 Misskey 体验，可进入独立 PoC；若目标只是“自建一个能用 Mastodon 客户端的账号”，它不是最短路径。

### 4.5 Fork：glitch-soc、Sharkey、Hometown

| Fork | 上游 | 价值 | 风险与去向 |
| --- | --- | --- | --- |
| glitch-soc | Mastodon | 更灵活的发帖/UI/本地帖等长期差异 | 仍活跃且同步及时，但升级需同时读 Mastodon 与 fork 说明；B 级按需候选 |
| Sharkey | Misskey | 增强审核、UI 和 Mastodon API 兼容 | 官方资料与运维生态小于 Misskey；先做 API、镜像来源和迁移 PoC |
| Hometown | Mastodon | 本地帖、长文、exclusive list | 历史上游同步曾明显滞后；不作为新部署默认项 |

fork 的判断标准不是功能数量，而是安全补丁同步时延、数据库迁移可逆性、容器签名/来源和回到上游的真实路径。不能把“数据库 schema 看起来相似”当作可随时替换镜像。

### 4.6 新兴通用候选：Bonfire 与 Takahē

[Bonfire Social](https://bonfirenetworks.org/app/social/)已经声明 1.0 可用，差异点是模块化 extension、circle/boundary、细粒度互动权限、GraphQL、OIDC 和面向社区治理的工具；[生产指南](https://github.com/bonfire-networks/bonfire-app/blob/main/docs/DEPLOY.md)首推 Co-op Cloud。它值得作为社区型候选跟踪，但其部署栈、Mastodon 客户端兼容、备份恢复、升级与 ANAS Compose 契约尚未达到可直接替代 GoToSocial/Mastodon 的证据强度。

[Takahē](https://docs.jointakahe.org/en/stable/)的主要差异是同一安装可托管多个账号域名，适合机构或多品牌托管；但官方 feature 文档仍明确提示它尚未具备完整 ActivityPub server 的全部功能。两者都进入 `C/experimental`，只有差异能力是硬需求时才做 PoC。

## 5. 相邻 ActivityPub 服务

| 服务 | 产品类型 | 与 Mastodon 的关系 | ANAS 去向 |
| --- | --- | --- | --- |
| **Pixelfed** | 图片/短视频社区 | ActivityPub 联邦，提供部分 Mastodon 风格 API；媒体处理和存储是核心 | 单独图片社区 Module 研究；不作为微博默认替代 |
| **PeerTube** | 视频托管、直播、频道 | 频道/视频可被联邦账号关注与互动 | 单独视频 Module；转码、带宽、对象存储容量远高于微博 |
| **Lemmy** | 论坛/链接聚合 | 社区、帖子和评论通过 ActivityPub 联邦 | 单独论坛 Module；约 150 MB 应用 RAM 信号不能代表媒体/DB 总成本 |
| **WriteFreely** | 极简 Markdown 博客 | 博客可被 Mastodon 等账号关注、转发 | 适合轻量写作发布；SQLite/MySQL，官方称 256 MB 可运行 |
| **Friendica** | Facebook 式联系人/关系网络 | 支持 ActivityPub 及多种外部网络，提供部分 Mastodon API | 需要关系圈、跨协议和 addon 时考虑；MariaDB/MySQL 栈 |
| **Hubzilla** | 身份/频道/权限型社交与内容 | 支持联邦协议，产品模型更复杂 | 只有明确需要 nomadic identity/频道权限时另行研究 |

这些服务可以出现在同一 Fediverse，但不能共享一套数据库、账号表或备份恢复流程。ANAS 最多共享 Traefik、SMTP、IAM、PostgreSQL/S3 等资源契约，不应试图把它们做成一个可互换的 `fediverse` 镜像参数。

## 6. 域名、身份与迁移边界

### 6.1 域名是数据的一部分

[Mastodon 配置文档](https://docs.joinmastodon.org/admin/config/)明确说明 `LOCAL_DOMAIN` 和 `WEB_DOMAIN` 在联邦后不能安全修改。原因不是本机 Nginx 配置，而是远端已经缓存了 actor URI、账号 handle、inbox/outbox 和签名公钥。Misskey 与 GoToSocial 也有相同约束。

上线前应完成以下决策：

1. 永久账号域名，例如 `example.com`；
2. 实际 Web/API 域名是否分离，例如 `social.example.com`；
3. 根域 `/.well-known/webfinger` 重定向与 CORS；
4. 域名、DNS、TLS 和对象媒体域名的长期所有权；
5. 灾难恢复时能否在新机器上恢复相同域名、私钥与 actor URI。

不要把临时动态 DNS 名、设备厂商域名或可能转移所有者的试验域名用于正式 actor。

### 6.2 账号移动不是实例数据迁移

[GoToSocial 的 Move 文档](https://docs.gotosocial.org/en/latest/user_guide/migration/)说明，账号移动主要把 followers 引导到目标账号；following、媒体、收藏、书签、屏蔽等不会全部随 Move 转移。其[帖子导入](https://docs.gotosocial.org/en/latest/user_guide/importing_posts/)会创建副本，旧帖的原始 URI 和远端互动仍属于旧实例。

因此退出策略分两类：

- **整实例原样迁机**：恢复数据库、私钥、配置、媒体并继续使用原域名，可保留联邦身份；
- **用户迁往新账号**：用 Move + CSV 导入尽量保留关注关系，但接受历史内容与互动不完整。

跨后端原地替换（例如保留同域名从 Mastodon 换成 Misskey）只有在有官方迁移工具能保留 actor URI、主键语义和签名密钥时才安全；本轮未发现可作为 ANAS 通用承诺的工具。

### 6.3 IAM 只管理登录，不管理联邦身份生命周期

推荐为 authentik 建立每应用独立 OIDC client，使用稳定、pairwise 或应用固定的 `sub`，并把 email 视作可变属性。还需定义：

- IdP 用户禁用后，应用账号是禁止登录、冻结还是删除；
- IAM group 到管理员权限是否自动映射，失去 group 后能否自动降权；
- 本地 break-glass 管理员及恢复流程；
- IdP 不可用时是否允许本地登录；
- OIDC logout、应用 session、移动端 OAuth token 和 ActivityPub actor 是否分别撤销。

## 7. 存储、备份与恢复

| 项目 | 必备备份 | 可重建/可省略项 | 关键恢复约束 |
| --- | --- | --- | --- |
| GoToSocial | SQLite/PG、配置、local media；S3 需版本化/复制 | remote media 可重新抓取 | DB 含 actor 签名私钥，备份必须加密；SQLite 需一致快照 |
| Mastodon | PostgreSQL、`.env.production`/secrets、本地媒体或 S3、Redis 队列 | home/list feeds、搜索索引可重建 | 丢 DB 即丢账号/帖/关注；丢 secrets 会破坏会话、2FA、Web Push 等 |
| Akkoma | PG dump、`config.exs`/`prod.secret.exs`、uploads、static、自定义项 | 前端可按固定制品重装 | 官方备份要求停服务；DB/配置/上传必须成套恢复 |
| Misskey | PostgreSQL、配置、Redis 持久状态、Drive/local media 或 S3 | 搜索索引按后端可重建 | domain、ID 设置不可改变；逐版执行 migration |

[Mastodon 备份文档](https://docs.joinmastodon.org/admin/backups/)把优先级列为 PostgreSQL、application secrets、用户上传和 Redis。[迁机文档](https://docs.joinmastodon.org/admin/migrating/)还要求在切换期间保留一致数据点并重建 feeds/search。对象存储提供商的耐久性不等于防误删备份；ANAS 应为 bucket 开启版本控制或跨目标复制，并把删除传播策略写入恢复说明。

[GoToSocial 备份文档](https://docs.gotosocial.org/en/v0.21.2/admin/backup_and_restore/)特别强调数据库包含所有本地 actor 的签名密钥；若丢失这些密钥，将无法以原域名正常继续联邦。其 CLI 最小 export 会丢失 statuses/faves 等，不可代替灾难恢复备份。

[Akkoma 官方流程](https://docs.akkoma.dev/stable/administration/backup/)要求停止服务后备份 PG dump、配置、uploads 与 static。恢复验收不应只检查首页，而应验证：

- 原账号 actor URI 与签名公钥未变化；
- 能向既有远端 follower 投递新帖；
- 远端回复、删除、关注与举报能进站；
- 本地和远端媒体显示正常；
- OIDC subject、管理员权限、2FA 和移动端 token 行为符合预期；
- 域名 block/allow list、审核记录、custom emoji 和实例配置仍在。

## 8. 安全与治理

### 8.1 网络边界

联邦服务必须能从公网通过 HTTPS 访问，也会主动请求远端 URL。最低要求：

- 只信任 Traefik 所在网络的 `X-Forwarded-*`，不接受任意来源认证头；
- 出站访问默认阻断 loopback、RFC1918、link-local、metadata endpoint 和 ANAS 管理网段；
- DNS rebinding、IPv4-compatible IPv6、重定向和媒体代理都要进入 SSRF 测试；
- 不把 PostgreSQL、Redis、Sidekiq dashboard、管理 API 或对象存储写接口暴露公网；
- 对上传做类型、大小、像素、时长和转码限制，持续更新 FFmpeg/libvips/ImageMagick；
- 分离主站与非可信远端媒体域名，设置合适 CSP、CORS 与缓存头。

Mastodon 2026-07 的安全公告包含 IPv4-compatible IPv6 SSRF 绕过与权限/DoS 修复，说明此类服务必须建立快速安全更新通道，而不是只做季度功能升级。GoToSocial 默认 HTTP client 会限制多类特殊地址；部署时不要为了抓取内网资源而全局放开。

### 8.2 内容治理

公开注册意味着管理员需要处理邮件验证、spam、骚扰、违法内容、copyright/隐私投诉、域名封禁与数据保留。实例规模小不会消除责任，因为一名被入侵的本地账号也能向整个 Fediverse 发送内容。

ANAS 默认应：

1. 关闭公开注册或使用审批/邀请码；
2. 明确管理员联系邮箱、服务器规则和隐私说明；
3. 提供 remote media retention 与孤儿媒体清理默认值；
4. 对注册、登录、搜索、媒体上传和 federation inbox 限流；
5. 导入共享 blocklist 时保留人工审核、来源和回滚能力，不盲目自动封禁；
6. 把管理员操作、举报和域名权限纳入备份与审计验收。

## 9. ANAS 推荐等级与 Module 设计

| 等级 | 候选 | 决策 |
| --- | --- | --- |
| **A** | GoToSocial | 默认小实例 PoC；先验证客户端矩阵、OIDC、S3 与恢复 |
| **A-** | Mastodon | 完整社区/兼容性候选；不作为低配设备默认安装 |
| **B** | Akkoma | 长文、reaction、本地帖和多前端需求；需先解决可复现镜像 |
| **B-** | Misskey | 明确 Misskey 产品需求时独立 PoC |
| **C+** | Pleroma | 既有经验/迁移场景；与 Akkoma 二选一维护 |
| **C（按需）** | glitch-soc、Sharkey | 只有差异功能构成硬需求时进入验证 |
| **C（实验）** | Bonfire Social、Takahē | 模块化社区或多域名是硬需求时跟踪/PoC |
| **独立主题** | Pixelfed、PeerTube、Lemmy、WriteFreely、Friendica | 作为不同产品 Module 研究，不纳入通用替换开关 |

### 9.1 GoToSocial Module 建议拓扑

```text
Internet
   │ HTTPS / ActivityPub / Mastodon API
Traefik
   │
GoToSocial ─────── PostgreSQL resource
   │               （个人试验可选择 SQLite critical volume）
   ├────────────── S3 resource or critical local-media volume
   ├────────────── authentik OIDC
   └────────────── SMTP resource
```

首版契约建议：

- 固定 `host`、可选 account domain 和 WebFinger 路由，创建后禁止普通配置更新修改；
- `database_mode=postgres` 为默认，`sqlite` 仅个人/试验 profile；
- S3/local media 二选一，切换必须运行显式迁移任务，不可直接改配置；
- OIDC 默认使用 `sub`，group 到注册准入/管理员映射由配置声明；
- 预置 amd64/arm64 镜像 digest，不使用浮动 `latest`；
- 提供创建首个用户/管理员、健康检查、备份、恢复、媒体清理和数据库维护 hook；
- UI 明确推荐并链接受支持的客户端，不把设置页描述成完整 Web 社交客户端。

### 9.2 Mastodon Module 的额外门禁

若后续实现 Mastodon Module，还需满足：

- Web、Streaming、Sidekiq 使用同一版本镜像和同一组加密 secrets；
- PostgreSQL 与 Redis 均有健康检查和持久化，Redis 不能误标为完全无状态；
- 可选搜索/翻译是独立 profile，默认不启用；
- 升级 hook 支持 pre-deployment/post-deployment migration 与后台迁移观察；
- 4.6→4.7 及后续 required stop 版本有明确门禁；
- 备份恢复验证 signing keys、Active Record encryption keys、VAPID 与媒体；
- 安全补丁可独立于普通功能版本快速发布。

## 10. PoC 验收清单

建议先用同一域名规划下的两个临时子域完成 GoToSocial 与 Mastodon 对照 PoC，但不要让试验 actor 进入长期使用。

1. amd64、arm64 各启动一次并记录应用/数据库/缓存的空闲与峰值资源；
2. authentik OIDC 首次注册、重复登录、group 准入、管理员降权、IdP 禁用与 break-glass；
3. 选定 Web/Android/iOS 客户端完成发帖、图片/视频、编辑、删除、投票、通知、列表和推送；
4. 与 Mastodon、GoToSocial、Akkoma、Misskey、Pixelfed 各做关注、回复、boost/reaction、quote 与删除互测；
5. 测试 blocked domain、用户举报、远端媒体清理和注册滥用；
6. 本地媒体→S3→本地（若支持）的显式迁移与 URL 稳定性；
7. 完整备份后在新宿主恢复同域实例，检查 actor URI、公钥和既有远端投递；
8. 模拟丢失 Redis、搜索索引、remote media，确认哪些可重建；
9. 升级一个稳定小版本及一个含数据库迁移的版本，再执行回滚/恢复；
10. 从实例导出用户数据并迁往另一后端，记录 Move/CSV/帖子导入实际保留与丢失项。

## 11. 最终建议

首轮只实现一个 `gotosocial` 实验 Module，并保留 `mastodon` 的对照部署文档。GoToSocial 通过以下门禁后可升为稳定候选：

- ANAS 目标客户端的必要功能全部通过；
- authentik OIDC 的 `sub`、group 和禁用语义通过；
- PostgreSQL/S3 与 SQLite/local 两种 profile 至少选择一套形成闭环；
- 同域灾难恢复后与既有远端继续联邦；
- 安全更新、远端媒体保留和治理默认值可自动化。

若客户端或审核能力缺口不可接受，再转向 Mastodon，而不是在 GoToSocial 上自研大量兼容层。Akkoma 与 Misskey 只在用户明确需要其差异化交互时进入后续 PoC；Pixelfed、PeerTube、Lemmy 和 WriteFreely 按独立应用主题处理。
