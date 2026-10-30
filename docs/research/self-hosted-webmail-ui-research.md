---
doc_type: research
created: 2026-10-23
updated: 2026-10-23
evidence_as_of: 2026-10-23
---

# Webmail 界面项目与 DMS、ANAS 适配研究

本文面向 ANAS 邮件选型：比较类似 Roundcube 的浏览器邮件客户端，回答界面是否现代、项目是否持续维护、能否接 docker-mailserver（下称 DMS），以及统一登录、个人别名、稳定身份和数据恢复需要多少适配。

**建议保留 Roundcube 为实测基线，优先比较 SOGo 与 Nextcloud Mail；把 MailFlow 作为现代界面的试验候选。没有发现已证明能原样满足 ANAS 全部要求的产品。** SnappyMail 的界面与协议适配值得保留，但正式发布停留在 2024 年，且通用 OIDC 链路未确认，不宜只凭最近提交就排在首位。

这是研究，不是部署或正式选型决定。本轮核验上游资料、固定版本源码、仓库活动和公开界面；未安装新邮件服务、改生产配置或执行新的收发/恢复验收。既有 DMS + Roundcube 实机结果来自原配套计划，不能移植为其他候选的通过记录。

## 1. 结论与短名单

适配等级表示进入比较验证的优先级：A 优先验证，B 有条件备选，C 专项场景，D 当前不建议；不是验收分数。

| 候选 | UI 判断 | DMS 配合度 | ANAS 的主要收益与成本 | 建议 |
| --- | --- | --- | --- | --- |
| **Roundcube 1.7.5 / Elastic** | 中等现代，清楚实用；三栏、响应式、明暗主题，仍偏传统邮件工具 | 高；IMAP/SMTP、Sieve 插件；已有真实 OAuth 链路证据 | 已测登录和恢复，新增组件少；改址身份连续性和完整 From 授权仍失败 | **A：继续作为基线** |
| **SOGo 5.12.12** | 中等；Material 风格、邮件/日历/联系人完整，视觉代际较早 | 高；原生使用 Dovecot/Postfix，现版支持 OIDC + XOAUTH2 | LDAP 多地址和独立 IMAP 登录字段很合适；Objective-C/GNUstep、SQL、缓存和镜像维护更重，身份投影仍须验证 | **B：最值得比较的独立替换方案** |
| **Nextcloud Mail 5.12.5** | 较现代；与 Nextcloud 联系人、文件和日历一致 | 高（邮件协议）；邮箱认证接线尚未解决 | 最能复用现有 Nextcloud 和数据库；该版使用 Vue 2.7，不能直接复用 ANAS Vue 3 组件；邮箱凭据仍需解决 | **B：已有 Nextcloud 时优先比较** |
| **MailFlow 4.0.4** | 较现代；明暗主题、会话列表、统一收件箱和布局定制较完整 | 中高（IMAP/SMTP）；不是 DMS 专用管理层 | 原生通用 OIDC、内部身份绑定较好；React/Node、PostgreSQL、Redis、邮箱凭据和邮件缓存增加维护面，项目仅约半年 | **B：重视界面时做隔离试验** |
| **SnappyMail 2.38.2** | 较现代但非明显代际领先；轻量、暗色、响应式 | 高（邮件协议）；通用 IAM 链路未确认 | LDAP identities 插件能读 AD 地址；发布长期中断、email 相关用户状态、SSO 插件边界需要复核 | **B：协议备选，先过发布与认证门槛** |
| **Cypht 2.13.0** | 中等；当前界面有明暗主题，重点是多账号聚合 | 高（IMAP/SMTP） | 活跃且有 profiles 发信身份；多账号聚合不是当前一人一箱的核心，通用 OIDC 未确认 | **C：多个外部邮箱成为需求时再提高优先级** |
| **Afterlogic WebMail Lite 9.8.7** | 桌面简洁；手机优化需区分付费 Pro | 高（IMAP/SMTP） | 有 identities、Sieve 和集成 API；原生 ANAS OIDC、版本制品与完整免费手机体验存在缺口 | **C：当前无明显综合优势** |

界面证据入口：[Roundcube 截图](https://roundcube.net/screens/)、[SOGo 公开演示](https://demo.sogo.nu/SOGo/)、[Nextcloud Mail 截图与版本](https://apps.nextcloud.com/apps/mail)、[MailFlow 图库](https://mailflow.sh/#screenshots)、[SnappyMail 官方版本截图](https://github.com/the-djmaze/snappymail/blob/v2.38.2/integrations/nextcloud/screenshots/inbox.jpg)、[Cypht 界面](https://www.cypht.org/)、[Afterlogic Lite](https://afterlogic.org/webmail-lite)。功能、版本与集成依据见后文；表中“高”仅表示协议契合，不能替代 ANAS 验收。

如果目标只是界面更好看，**先并排看 Nextcloud Mail、MailFlow 和当前 Elastic**。SOGo 的价值主要在群件、LDAP 多地址和原生认证能力，不能把它当作最现代视觉方案。用 React/Vue、是否 PHP、Star 大小都不能单独决定体验或安全。

## 2. 范围与 ANAS 当前要求

### 主题卡

| 项目 | 本轮口径 |
| --- | --- |
| 核心类别 | 可自部署、能连接既有 IMAP/SMTP 邮箱的 Webmail；保留开源免费版与商业版差异 |
| 相邻类别 | JMAP 客户端、群件、完整邮件套件、早期现代界面项目 |
| 排除主排名 | SaaS-only、桌面客户端、测试邮件捕获器、无法确认许可证、已归档且无独立维护的旧项目 |
| 目标 | ANAS 家庭/小团队、多用户；一人一主邮箱，多个个人别名共用一箱；规模与并发数未确定 |
| 运行环境 | Linux、Docker/Compose、Traefik HTTPS；amd64/arm64 为目标，具体镜像架构仍需固定 tag 核验 |
| 日期 | 外部事实采集日 2026-10-23；文中活动日期按新加坡时间，API 检索窗口使用明确 UTC 边界 |
| 未执行 | 新候选的本机部署、DMS 互通、中文手机操作、大邮箱性能、资源测量、升级与恢复 |

本轮读取了需求/计划索引、邮件 PoC 矩阵与计划、目录身份要求、门户邮件模型，以及实际 `go.mod`、`web/package.json` 和 Nextcloud Module。当前 checkout 没有正式 `modules/mail/`，DMS 是选型实验基线。

| 既有要求 | 选 Webmail 时必须辨别什么 |
| --- | --- |
| 一主地址、多个人别名、同一箱/配额/历史 | 多邮箱聚合功能不是必要条件；多个 From 身份不能代替服务器路由 |
| Portal 自助增删本人别名 | 受管域、全局唯一性、保留名称/数量、地址保留、审计和失败可见；不开放任意地址 |
| 发信权限 | SMTP 信封 `MAIL FROM` 与用户可见 `From:` 都要由服务器检查；界面下拉框不能作为权限边界 |
| MAILPOC-R-002/003 | 已有 IAM 会话进入本人实际邮箱，无效令牌和跨邮箱请求被拒绝 |
| MAILPOC-R-004、DIRKEY-R-001/003/008/010/013 | IAM `sub` 为 anchor；地址变化后邮件与 Webmail 状态连续；不能把 anchor 直接变成登录名、URL 或路径，Nextcloud 有已确认的特定例外 |
| MAILPOC-R-005/006 | 合法别名收发；删除后立即撤销发信并保留旧邮件；他人/已移除地址均拒绝 |
| MAILPOC-R-007/008/009 | 全部邮件内容在明确的 DATA_PATH 保存集合，停机后从空目录恢复正文、附件、文件夹/标志、发信身份、偏好和待投递队列 |
| 其他 Module 读取、托管收发 | 邮件端授权和 IMAP 等协议负责；换 Webmail 不会自动得到每别名隔离、Cloudflare 路由或 SMTP relay 配置 |

规范来源：[门户邮件模型](/architecture/portal-account-mail-administration)、[目录身份矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-identity-key.md)、[邮件 PoC 矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/mail-webmail-poc.md)。已有实验通过 8/10 项，**MAILPOC-R-004 与 MAILPOC-R-006 失败仍未关闭**；准确环境、制品摘要和逐项记录在[配套计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/mail-webmail-poc.md)，摘要见[既有研究](/research/mail-routing-alias-and-module-access)。

## 3. 别名需求：哪些能做，哪些仍需 ANAS/DMS

### 四个不同动作

1. **收件别名**：`shopping@example.com` 投递到 Alice 的同一邮箱。这是 DMS 的收件人映射。
2. **发信许可**：Alice 有权用该地址作为信封和可见 From。应在 DMS 的认证提交链路强制执行。
3. **Webmail 发信身份**：显示地址、名字、签名、Reply-To，并在写信/回复时选择。各候选大多具备。
4. **个人别名生命周期**：Portal 创建、移除、保留地址，协调服务器许可和界面身份；若 MX 在云端，还要协调云端路由。候选的身份编辑页通常不负责这些。

| 候选 | 展示/选择多个发信身份 | 从目录读取地址 | 原生管理 DMS 收件别名与发信许可 | 已证明完整满足当前别名要求 |
| --- | --- | --- | --- | --- |
| Roundcube | ✅ multiple identities | ◐ 通过选定插件/适配，核心 LDAP 地址簿不等于本人身份同步 | — 未确认现成受支持的完整管理链路 | 否；合法收发通过，完整 From 授权失败 |
| SOGo | ✅ | ✅ LDAP `MailFieldNames`；`proxyAddresses` 有去前缀规则 | — 客户端消费地址；不因此自动创建 Postfix 路由 | 否；待固定 DMS 组合验证 |
| Nextcloud Mail | ✅ aliases | ◐ 原生 LDAP aliases provisioning，要求实际用户后端是 LDAP | — 预配的是 Mail app 本地账号/别名 | 否；AD 格式与邮箱认证待验证 |
| SnappyMail | ✅ identities | ◐ 上游 `ldap-identities` 插件，可配置 `smtp:` 前缀 | — LDAP identities 是读取，不是别名生命周期服务 | 否；SSO、改址和撤销待验证 |
| MailFlow | ✅ account aliases | — 未确认通用 AD 自动导入 | — `account_aliases` 写本地 SQL，没有自动写 DMS | 否；本地 alias CRUD 不能代替邮件服务器配置 |
| Cypht | ✅ profiles，绑定 IMAP/SMTP、地址与签名 | — 本轮未确认本人别名自动预配 | — profiles 是客户端配置 | 否；需额外授权/同步接线 |
| Afterlogic Lite | ✅ identities | — 本轮未确认 ANAS 所需目录投影 | — identities 不等于服务器路由 | 否 |

源码依据：[SOGo 5.12.12 安装指南](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/Documentation/SOGoInstallationGuide.asciidoc)、[Nextcloud Mail 5.12.5 预配 Manager](https://github.com/nextcloud/mail/blob/v5.12.5/lib/Service/Provisioning/Manager.php)、[SnappyMail LDAP identities](https://github.com/the-djmaze/snappymail/blob/v2.38.2/plugins/ldap-identities/LdapIdentities.php)、[MailFlow 4.0.4 accounts 路由](https://github.com/maathimself/mailflow/blob/v4.0.4/backend/src/routes/accounts.js)、[Cypht profiles](https://github.com/cypht-org/cypht/blob/v2.13.0/modules/profiles/README.md)、[Afterlogic identities](https://afterlogic.com/docs/webmail-lite/configuring-webmail/how-to-use-or-disable-identities)。✅/◐描述上游能力，不代表已在 ANAS 验收。

尤其要防止把 MailFlow 的“新增 alias”误认为完成需求：固定源码的 POST 路由验证当前用户拥有该连接后，将地址插入 `account_aliases` 并触发本地身份变更 hook；这条核心路径没有创建 DMS 收件路由、全局地址占用或服务器发信授权。该功能适合登记**已经由邮件服务器授权**的身份。

换界面也不会自动修好 MAILPOC-R-006：合法信封搭配未授权 From 必须从独立 SMTP 客户端测试。既有 DMS 最小实验关闭了 Rspamd，不能把该失败扩大为“DMS 所有配置无解”；也不能认为打开一个反垃圾组件就必然通过。[DMS 固定版本 spoofing 配置](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/target/scripts/startup/setup.d/security/spoofing.sh)、[DMS OAuth2](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/supplementary/oauth2/)

`+tag`、catch-all、隐私别名反向中转与这里的登记个人别名不同。一个共箱用户的 IMAP 凭据通常可以读所有别名邮件；Webmail 不提供“只给另一个 Module 读 shopping 别名”这样的天然安全隔离。对应边界沿用[邮件读取研究](/research/mail-routing-alias-and-module-access)。

## 4. 重点候选的认证和身份连续性

### Roundcube：已有证据最多，继续修复最容易形成可比基线

1.7.5 原生配置包含通用 OAuth provider、发现地址、PKCE、refresh/logout 相关能力。既有 LLNG→Roundcube→DMS 实测证明无二次邮箱密码和跨邮箱拒绝可行，但只覆盖已记录的 Provider 与手工预配夹具，不代表 Casdoor 或正式自动预配已通过。[1.7.5 OAuth 配置](https://github.com/roundcube/roundcubemail/blob/1.7.5/config/defaults.inc.php)、[OAuth 说明](https://github.com/roundcube/roundcubemail/wiki/Configuration%3A-OAuth2)

`rcube_user::query()` 按 `mail_host + username` 查用户；已测 `oauth_identity_fields=['email']` 使改址后创建新 user_id。该实现同时存在 `user_aliases` 身份邮箱回查路径，值得验证受控迁移是否可用，但不能把它当作 anchor 绑定已经解决，也不能无条件开启后放行改址。只改成 `sub` 又可能把 anchor 投影为登录名，并与 Dovecot 的令牌用户名校验不相容。[用户查询源码](https://github.com/roundcube/roundcubemail/blob/1.7.5/program/lib/Roundcube/rcube_user.php)

`identities_level` 能限制用户编辑哪些字段，但不维护全局别名归属，也不能拦住绕过浏览器的 SMTP。优点是现成插件接口、SQLite/PostgreSQL/MariaDB 选择和官方容器生态，现有停机恢复也已有证据。[配置说明](https://github.com/roundcube/roundcubemail/wiki/Configuration)、[官方容器源码](https://github.com/roundcube/roundcubemail-docker)

### SOGo：协议和多地址很合适，原生 OIDC 值得认真验证

固定 **SOGo-5.12.12** 指南明确支持 `SOGoAuthenticationType=openid`，配置发现地址、client、scope、refresh 和 logout；通过 XOAUTH2 将 access token 用于 IMAP/SMTP。它可以独立接 DMS，不必为使用 SOGo 就部署整套 mailcow。[固定版本认证章节](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/Documentation/SOGoInstallationGuide.asciidoc#authenticating-using-openid)

它可分别配置 `UIDFieldName`、`IMAPLoginFieldName` 和 `MailFieldNames`；`proxyAddresses` 的协议前缀会被剥离，较贴近 AD 多地址。仍不能直接宣布满足稳定身份：默认 OIDC 地址字段是 email，而 UID 会用于应用对象与 URL；本轮公开 demo 的路径也含用户标识。直接用 anchor 替换 UID 可能违反 DIRKEY-R-010，必须检查内部绑定、技术路径和改址后的偏好归属。[目录字段定义](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/Documentation/SOGoInstallationGuide.asciidoc)、[公开 demo](https://demo.sogo.nu/SOGo/)

该版原生 OIDC 指南还要求关闭 `SOGoXSRFValidationEnabled`。这是具体部署障碍：本轮没有核验此要求的原因、补偿机制和所有写接口保护，不能把指南配置直接作为 ANAS 可接受的上线方案。安装还需 SQL、SOPE/GNUstep 和缓存等组件；固定前端仍使用 AngularJS 1.8，维护技能复用弱于 Nextcloud Mail。[安装指南](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/Documentation/SOGoInstallationGuide.asciidoc)、[前端依赖](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/UI/WebServerResources/package.json)

### Nextcloud Mail：与 ANAS 已有组件最接近，认证是关键缺口

ANAS 当前 Nextcloud Module 为 34.0.2，已有 LDAP、OIDC/SAML、Traefik、数据库 Contract 与 Redis 接线；Mail app 商店列出的 **5.12.5** 支持 Nextcloud 34。读取现有启动脚本没有发现 Mail app 自动安装/配置，不能说它已经接入。固定 Mail 版使用 PHP、Vue 2.7 和 webpack，能复用 Nextcloud 的应用环境与协作界面，但不能直接共享 ANAS Vue 3/Vite 的组件与构建配置。[Mail app 商店](https://apps.nextcloud.com/apps/mail)、[固定前端依赖](https://github.com/nextcloud/mail/blob/v5.12.5/package.json)

固定版本 `findProvisionedAccount()` 使用 Nextcloud UID 查已有预配邮箱，随后更新邮箱地址，而不是只能按 email 新建。这为已绑定稳定 UID 的用户提供较好的基础；仍需真实改址与恢复验证，且内部 API 不构成稳定的外部邮件管理契约。[账号 Mapper](https://github.com/nextcloud/mail/blob/v5.12.5/lib/Db/MailAccountMapper.php)、[预配 Manager](https://github.com/nextcloud/mail/blob/v5.12.5/lib/Service/Provisioning/Manager.php)

两项接线限制尤其明确：

- LDAP aliases provisioning 仅在 `getBackendClassName() === 'LDAP'` 等条件下执行；OIDC 账号是否复用了该 LDAP 用户必须核验。它直接读取多值属性，示例是裸地址 `mailAlias`；不能未经转换验证就把带 `smtp:` 前缀的 AD `proxyAddresses` 当成已兼容输入。
- `updatePassword()` 使用登录时可取得的密码，或配置的 master password。OIDC/Passkey 登录不保证取得 AD 密码；Nextcloud 的应用密码也不自动成为 DMS 凭据。通用外部 IAM 令牌到 DMS 的刷新链路本轮未确认，不能拿 Gmail/Outlook 的 OAuth 支持替代。

上述均来自[固定版本 Manager](https://github.com/nextcloud/mail/blob/v5.12.5/lib/Service/Provisioning/Manager.php)及[LDAP 别名示例](https://github.com/nextcloud/mail/blob/v5.12.5/doc/developer.md)。共享 master password 是可研究的原生机制，但会引入可访问多邮箱的凭据与撤销边界，不能作为已经确认的默认设计。

Mail 自身的缓存、临时附件以及稍后发送状态也进入保存集合；复用 Nextcloud 不代表可以只备份 DMS。Nextcloud Files 中由用户主动保存的附件副本属于另一项文件操作，不能把这种副本混同于邮件 Module 的原件存储。

### MailFlow：现代 UI 与稳定 OIDC 绑定较好，邮箱连接仍是另一层

固定 **v4.0.4** 的 OIDC 路由以 `issuer + subject` 查 `user_identities`，已绑定用户无需以新 email 重建 MailFlow 用户；这比只按邮件地址匹配更符合 ANAS 的方向。首次绑定仍有 email/claim 匹配策略和 `email_verified` 要求，需要与系统主邮箱尚未验证的实际语义一致，不能为方便登录就伪造已验证状态。[OIDC 路由](https://github.com/maathimself/mailflow/blob/v4.0.4/backend/src/routes/oidc.js)

但 OIDC 登录完成后是 `imapManager.connectAllForUser()` 去连接已有账号。`email_accounts` 另存加密邮箱密码或 OAuth 凭据，核心刷新代码针对 Google/Microsoft；本轮没有找到“ANAS OIDC 登录后直接将同一令牌交给 DMS 并自动预配”的已验证链路。[账号连接路由](https://github.com/maathimself/mailflow/blob/v4.0.4/backend/src/routes/accounts.js)、[IMAP 管理器](https://github.com/maathimself/mailflow/blob/v4.0.4/backend/src/services/imapManager.js)

官方 Compose 默认包括前端 nginx、Node 后端、PostgreSQL、Redis，可选 Caddy；ANAS 可研究复用数据库与 Traefik。邮件同步缓存、附件/图片、会话、加密密钥与原 DMS 邮箱构成更大的保存面。它不是只有一层静态漂亮界面；原生恢复还可能重新触发收件规则，必须与 DMS 一起验证。[固定 Compose](https://github.com/maathimself/mailflow/blob/v4.0.4/docker-compose.ghcr.yml)、[备份限制](https://github.com/maathimself/mailflow/blob/v4.0.4/README.md#backup--restore)

### SnappyMail：LDAP identities 有价值，发布和 SSO 不能忽略

上游 `ldap-identities` 能从 LDAP 读取本人地址，并支持为 AD `proxyAddresses` 去除 `smtp:` 前缀，减少手工维护发信身份的工作。它仍按账号 email 搜索目录，不是已经完成的稳定主体绑定；不得无筛选导入团队地址作为个人发信许可。[插件配置](https://github.com/the-djmaze/snappymail/blob/v2.38.2/plugins/ldap-identities/index.php)、[读取实现](https://github.com/the-djmaze/snappymail/blob/v2.38.2/plugins/ldap-identities/LdapIdentities.php)

当前源码的 `login-oauth2` 插件仍包含 Google 固定端点；`login-external-sso` 要求 Email/Password/SsoKey；`proxy-auth` 使用可信 HTTP 用户头和 Dovecot master user。因此“有 OAuth/SSO 插件”不能直接标成通用 LLNG/Casdoor OIDC 已具备。[OAuth 插件](https://github.com/the-djmaze/snappymail/blob/e1e94c815d84a7630b03bd435f7d60499ad070bd/plugins/login-oauth2/index.php)、[外部 SSO 插件](https://github.com/the-djmaze/snappymail/blob/e1e94c815d84a7630b03bd435f7d60499ad070bd/plugins/login-external-sso/index.php)、[代理认证插件](https://github.com/the-djmaze/snappymail/blob/e1e94c815d84a7630b03bd435f7d60499ad070bd/plugins/proxy-auth/index.php)

开发分支仍合并真实修复，但本轮 API 找到的最新正式 release 是 **v2.38.2，2024-10-10（新加坡）**。不能靠浮动 master 绕过固定制品、升级和安全更新要求。Nextcloud 商店里的 SnappyMail 集成最高声明 Nextcloud 30，而 ANAS 是 34；不能把独立 SnappyMail 可运行等同于该集成可安装。[正式发行](https://github.com/the-djmaze/snappymail/releases)、[Nextcloud 集成版本](https://apps.nextcloud.com/apps/snappymail)

### Cypht 与 Afterlogic：有能力，但没有解决当前主要难题

Cypht 持续多人维护，profiles 可组合地址、签名与 IMAP/SMTP；当前更适合聚合多个外部账号。固定 OAuth 配置主要是 Gmail/Outlook/Office365，IMAP/LDAP/DB 认证不等于复用 ANAS 浏览器会话；本轮未确认通用 OIDC 的完整实现。官方 Compose 示例还落后于当前 release，选型应固定真实制品。[profiles](https://github.com/cypht-org/cypht/blob/v2.13.0/modules/profiles/README.md)、[OAuth 配置](https://github.com/cypht-org/cypht/blob/v2.13.0/config/oauth2.php)、[Compose 示例](https://github.com/cypht-org/cypht/blob/v2.13.0/docker/docker-compose.yaml)

Afterlogic 当前官网是 Lite 9.8.7，官方仍将源码链接到名为 `webmail-lite-8` 的仓库；该仓库 GitHub Releases 仍是旧版本，不能用它误判整个产品停更，也不能把主分支直接当成已经核验的 9.8.7 制品。Lite 有多发信身份，手机优化、多 IMAP 账号等需区分 Pro；本轮未完成 9.8.7 下载包与仓库源码的对应验证。[Lite 官网](https://afterlogic.org/webmail-lite)、[源码入口](https://github.com/afterlogic/webmail-lite-8)、[Lite/Pro 区别](https://afterlogic.com/docs/webmail-lite/installation/upgrading-to-webmail-pro)

## 5. 技术栈、部署与恢复成本

ANAS 核心是 Go，管理前端是 Vue 3/TypeScript/Vite；应用由 Compose Module、Traefik、IAM capability、数据库 Contract 和生命周期 Hook 接入。**外部应用不是必须与核心同语言**，但长期 fork、认证补丁、数据库迁移和新增状态越多，维护成本越高。本轮不建议为统一技术栈重写一个 Webmail。

| 候选 | 应用栈与主要依赖 | 镜像/架构证据 | 必须关注的保存与升级面 |
| --- | --- | --- | --- |
| Roundcube | PHP/JS；SQLite 或 PostgreSQL/MariaDB | 有官方 Docker 项目；具体 1.7.5 变体的双架构 manifest 待验 | Webmail DB、身份/通讯录/签名/偏好、临时附件及配置；与 DMS 邮件/队列成组恢复 |
| SOGo | Objective-C、GNUstep/SOPE、AngularJS；SQL、缓存 | 官方主要交付源码/发行版包；未确认适合 ANAS 的独立官方双架构镜像 | 用户 profile、日历/通讯录、会话/OIDC 表、配置密钥与 DMS；包升级和镜像构建需维护 |
| Nextcloud Mail | PHP/Vue 2.7/webpack；既有 Nextcloud、SQL、Redis、后台任务 | 复用现有 ANAS Nextcloud 镜像；Mail app 与 Nextcloud 版本配对 | Mail 表、连接凭据、缓存/附件/待发送任务和 Nextcloud 加密所需配置；升级/恢复与 Nextcloud 耦合 |
| MailFlow | React/Vite + Node/Express、PostgreSQL、Redis | 官方发布流程声明 amd64/arm64，运行 manifest 未拉取 | DMS 之外还有缓存消息、上传文件、账号凭据、ENCRYPTION_KEY 和 session；数据库迁移限制降级 |
| SnappyMail | PHP/JS；文件状态，联系人可用 SQLite/PostgreSQL/MySQL | 上游 Docker workflow 声明 amd64/arm64；固定镜像与发布年代需核对 | 用户/域配置、联系人 DB、identities、临时文件和插件；不能只留邮件服务器卷 |
| Cypht | PHP/JS，配置/会话可用 SQL 等后端 | 上游 workflow 声明 amd64/arm64/armv7/386；当前 tag manifest 待验 | 配置、profiles、会话加密材料和用户设置；不能只按容器个数估恢复成本 |
| Afterlogic Lite | PHP/JS、多模块 Composer、SQL | 本轮未确认合适的官方多架构固定镜像 | SQL 用户/身份/联系人、data 配置、加密材料、临时附件；9.x 与组件版本对应待核验 |

部署证据：[Roundcube Docker](https://github.com/roundcube/roundcubemail-docker)、[SOGo 安装/依赖](https://github.com/Alinto/sogo/blob/SOGo-5.12.12/Documentation/SOGoInstallationGuide.asciidoc)、[MailFlow 构建架构](https://github.com/maathimself/mailflow/blob/v4.0.4/.github/workflows/publish.yml)、[SnappyMail Docker](https://github.com/the-djmaze/snappymail/blob/v2.38.2/.github/workflows/docker.yml)、[Cypht 镜像构建](https://github.com/cypht-org/cypht/blob/v2.13.0/.github/workflows/Release-Image.yml)、[Afterlogic 依赖](https://github.com/afterlogic/webmail-lite-8/blob/master/composer.json)。这些是源码声明，不是本轮镜像拉取或容量实测。

每个新候选还须把 client 注册、TLS 信任、Secret 生成/轮换、健康检查、依赖就绪、固定版本与升级恢复纳入 Module；不能仅添加一个 Traefik 链接就宣布集成完成。官方首次安装管理账号或管理向导也要转换为受控初始化。没有测量依据，本轮不提供各项目 RAM、吞吐或启动秒数排名。

对自动化接口也应保守：Roundcube/SnappyMail/Cypht 有插件或 hook，SOGo 有 DAV 和 `sogo-tool`，Nextcloud 有 `occ` 与内部 Mail controllers，MailFlow 有内部 REST/hook，Afterlogic 有集成 API；**本轮未证明其中任何一套是稳定的 DMS 地址管理 API**。其他 Module 读写邮件优先研究邮件后端授权和 IMAP，不以浏览器私有接口作为基础契约。

AI 不是本轮硬要求：MailFlow 声明支持自选 OpenAI-compatible 模型，Nextcloud Mail 有可选文本处理集成；其余可通过后端 IMAP/插件另外研究。本轮未启用 AI、未发送邮件给模型，也未把 AI 功能计入推荐分数。[MailFlow 功能说明](https://github.com/maathimself/mailflow/blob/v4.0.4/README.md)、[Nextcloud Mail 管理说明](https://docs.nextcloud.com/server/latest/admin_manual/groupware/mail.html)

## 6. 开源、付费和外部依赖边界

| 候选 | 免费自部署与许可证 | 付费/使用限制及外部依赖 |
| --- | --- | --- |
| Roundcube | GPL-3.0-or-later，另有皮肤/插件例外；核心无产品许可 Key | 托管商收费不等于核心有付费功能锁；自选插件另核许可证与更新来源 |
| SOGo | 官方说明 GPL/LGPL v2 及以后；免费开源群件 | 商业支持和部分稳定二进制包交付有订阅边界，不能把源码可编译等同免费获取所有官方包；不是功能自动解锁问题 |
| Nextcloud Mail | AGPL-3.0-only；依附自部署 Nextcloud，核心邮件/别名不因企业支持解锁 | 企业支持与托管是不同服务；可选 AI、远程图片和外部账号连接另有联网面 |
| SnappyMail | AGPL-3.0；免费客户端与上游插件 | 未发现核心多身份/LDAP 插件收费解锁；插件市场、第三方镜像与 Nextcloud 集成各自维护 |
| MailFlow | 固定 LICENSE 为标准 AGPL-3.0；README 说明双许可，不能履行 AGPL 义务时另有商业许可 | 官网页面将 commercial deployments 写为需付费，README 的口径更窄，存在表述差异；组织使用前需澄清，不擅自认定“所有商业使用必付费”或“许可证无边界”。AI/云邮箱可选，不是接本地 DMS 的必需依赖 |
| Cypht | LGPL-2.1；免费自部署 | 未发现此次核心需求被企业版锁定；外部邮箱的 OAuth 注册、SMTP/IMAP 可达性另算 |
| Afterlogic Lite | AGPLv3 / 商业双许可 | 手机优化界面、多 IMAP 账号、群件等与 Pro 区分；SaaS 或 Pro 试用不等于 Lite 免费功能 |
| Bulwark | 当前 LICENSE 是 AGPLv3-only 加尚未最终完成的应用商店额外许可；GitHub 自动检测为 NOASSERTION | 必须读实际 LICENSE，不能因此写成“不开放源码”；默认托管 push relay 是可替换的联网依赖，DMS 还需另外的协议代理 |

来源：[Roundcube 固定版本许可声明](https://github.com/roundcube/roundcubemail/blob/1.7.5/README.md#license)、[SOGo 产品说明](https://www.sogo.nu/)与[交付入口](https://www.sogo.nu/download.html)、[Nextcloud Mail 许可声明](https://github.com/nextcloud/mail/blob/v5.12.5/README.md)、[SnappyMail 许可](https://github.com/the-djmaze/snappymail/blob/v2.38.2/LICENSE)、[MailFlow LICENSE](https://github.com/maathimself/mailflow/blob/v4.0.4/LICENSE)与[双许可说明](https://github.com/maathimself/mailflow/blob/v4.0.4/README.md#licensing)、[Cypht LICENSE](https://github.com/cypht-org/cypht/blob/v2.13.0/LICENSE)、[Afterlogic Lite](https://afterlogic.org/webmail-lite)、[Bulwark LICENSE](https://github.com/bulwarkmail/webmail/blob/1.17.1/LICENSE)。官方托管免费配额不属于本地 DMS 方案，本轮不比较这些套餐。

## 7. 活跃度与成熟度快照

数据直接来自 2026-10-23 的 GitHub REST：仓库元数据、默认分支最近一年提交（最多 100 条）、最近 100 个 release，以及最近更新的 5 个已关闭 PR。提交统计使用 2025-10-23 至采集日，近 90 日自 2026-07-25 起；不是全部分支的贡献数。

| 主仓库 | 创建日 | 最近 push（新加坡） | Star / Fork | 当前稳定发行 | 活跃度 / 成熟度判断 |
| --- | --- | --- | --- | --- | --- |
| [roundcube/roundcubemail](https://github.com/roundcube/roundcubemail) | 2012-05-04 | 2026-10-23 00:24 | 7,241 / 1,801 | 1.7.5；2026-10-19 | 高 / 成熟；多年多贡献者与持续修复 |
| [Alinto/sogo](https://github.com/Alinto/sogo) | 2012-09-18 | 2026-10-22 13:00 | 2,176 / 326 | 5.12.12；2026-10-13 | 高 / 成熟；组织维护，仍需评估旧前端依赖 |
| [nextcloud/mail](https://github.com/nextcloud/mail) | 2016-08-28 | 2026-10-23 09:48 | 1,039 / 369 | 5.12.5；2026-10-20，发行在单独仓库 | 高 / 成熟；不能只数 bot 更新，也有人工性能/功能修复 |
| [the-djmaze/snappymail](https://github.com/the-djmaze/snappymail) | 2020-03-09 | 2026-10-23 05:02 | 1,715 / 232 | 2.38.2；2024-10-10 | 开发活动恢复、正式发布弱 / 成熟代码但交付节奏存疑 |
| [cypht-org/cypht](https://github.com/cypht-org/cypht) | 2014-02-15 | 2026-10-23 14:55 | 1,769 / 255 | 2.13.0；2026-10-16 | 高 / 成熟；持续多人修复与发行 |
| [maathimself/mailflow](https://github.com/maathimself/mailflow) | 2026-04-20 | 2026-10-23 06:03 | 576 / 66 | 4.0.4；2026-10-22 | 高 / 早期；约半年、演进快、核心维护者集中 |
| [afterlogic/webmail-lite-8](https://github.com/afterlogic/webmail-lite-8) | 2017-03-21 | 2026-10-19 21:11 | 363 / 77 | 官网 9.8.7；2026-10-19，制品对应待核验 | 中 / 成熟产品；仓库/release 命名容易误读 |
| [bulwarkmail/webmail](https://github.com/bulwarkmail/webmail) | 2026-03-13 | 2026-10-23 05:49 | 1,326 / 235 | 1.17.1；2026-10-23 | 高 / 早期；主分支密集变化不等于长期运维成熟 |

表中创建日是当前主仓库的创建时间，不是产品最初诞生时间；本轮以上仓库均未归档。许可证以第 6 节实际文件为准，Star 不参与适配评分。

| 仓库 | 近 90 / 365 日提交 | 近 90 / 365 日非预发行 release | 最近提交样本的作者数量 | 人工维护活动样本 |
| --- | --- | --- | --- | --- |
| Roundcube | ≥100 / ≥100 | 6 / 19 | 11 | [PR 10359](https://github.com/roundcube/roundcubemail/pull/10359)，10-22 合并 |
| SOGo | 38 / ≥100 | 3 / 8 | 8 | [PR 412](https://github.com/Alinto/sogo/pull/412)，10-07 合并 |
| Nextcloud Mail | ≥100 / ≥100 | 19 / ≥77，单独发行仓库样本 | 14 | [PR 13359](https://github.com/nextcloud/mail/pull/13359)，10-22 合并性能修复 |
| SnappyMail | 62 / 76 | 0 / 0 | 15 | [PR 1922](https://github.com/the-djmaze/snappymail/pull/1922)，10-22 合并；未形成新正式 release |
| Cypht | ≥100 / ≥100 | 6 / 18 | 11 | [PR 2119](https://github.com/cypht-org/cypht/pull/2119)，10-23 合并 MIME 修复 |
| MailFlow | ≥100 / ≥100 | 43 / 69 | 7 | [PR 660](https://github.com/maathimself/mailflow/pull/660)，10-22 合并 |
| Afterlogic 源码入口 | 22 / 32 | GitHub 当前仓库 0 / 0；不能代替官网发行数 | 2 | 本轮未量化 issue 首次响应或下载包的贡献者分布 |
| Bulwark | ≥100 / ≥100 | 18 / 55 | 4 | [PR 1300](https://github.com/bulwarkmail/webmail/pull/1300)，10-21 合并 |

`≥100` 是 API 单页截断的下界；其他数字来自该窗口内未满页样本。作者数仅为最近最多 100 条提交中的去重名称，剔除明显 bot/自动账号，**不是维护者人数**，也不能据此排名团队大小。release 按非 draft、非 prerelease 及发布时间统计，不同维护分支的安全版本分别计数；Nextcloud 发行样本可能截断，故一年写下界。PR 样本只证明人工修复被处理，不能推断平均响应时间或 SLA。

特别更正两个常见误读：Nextcloud Mail 的最新发行来自 [nextcloud-releases/mail](https://github.com/nextcloud-releases/mail/releases)，不是主源码仓库停留在 2022 年的 Releases；SnappyMail 确有 2026 年人工提交，但本轮查询仍没有 2025/2026 年正式 release。

安全维护方面，Roundcube 近期公告包含安全更新，Cypht 有固定版本安全报告政策，SnappyMail 的安全文件存在旧支持版本文字，不能把文件存在当作当前发行覆盖证明。Nextcloud 的安全体系应结合平台版本复核。SOGo/MailFlow 等的安全响应完整性本轮未审计；没有 `SECURITY.md` 不足以单独认定无安全流程。[Roundcube 公告](https://roundcube.net/)、[Cypht 安全政策](https://github.com/cypht-org/cypht/blob/v2.13.0/SECURITY.md)、[SnappyMail 安全政策](https://github.com/the-djmaze/snappymail/blob/v2.38.2/SECURITY.md)

可复核的 API 模式为 `https://api.github.com/repos/{owner}/{repo}`、其下的 `commits?since=...&until=...&per_page=100`、`releases?per_page=100` 和 `pulls?state=closed&sort=updated&per_page=5`。原始 JSON 本轮保存在 Git 外的临时证据目录，不是共享持久档案；报告中的固定版本链接与关键结论用于后续复查。

## 8. 相邻候选、发现过程与排除理由

### 现代 JMAP 界面不能直接当 DMS 界面

JMAP 是通过 HTTP/JSON 操作邮箱的协议；DMS 当前选型基线提供 IMAP/SMTP，没有本轮已确认的原生 JMAP 服务。因此纯 JMAP 前端不能只改 DMS 地址就运行。

| 项目 | 已核实的边界 | 本轮去向 |
| --- | --- | --- |
| [Bulwark Webmail](https://github.com/bulwarkmail/webmail) | React/Next.js/TypeScript，JMAP，现代三栏/明暗主题；主目标 Stalwart | 若保持 DMS，需额外代理，**C**；若未来另选 JMAP 后端，可提高优先级 |
| [Bulwark legacy-proxy](https://github.com/bulwarkmail/legacy-proxy) | 上游确有 IMAP/SMTP/ManageSieve/CardDAV→JMAP 代理；含 SQLite、凭据 vault、同步状态与额外密钥；本轮 releases 列表为空 | 不能写成“Bulwark 永远不兼容 DMS”；但这条路明显增加组件与恢复面，不作为当前默认方案 |
| [Linagora Twake Mail / tmail-flutter](https://github.com/linagora/tmail-flutter) | AGPL-3.0、多平台 Flutter，使用 JMAP | 同样需要 JMAP 后端或已验证的桥接；**C**，不是直接的 DMS Webmail 替换 |
| [oxi](https://github.com/c0h1b4/oxi) | React/Rust，IMAP/SMTP、SQLite/Tantivy，创建于 2026-02-24，30 Star，未见正式 release；README/官网称 MIT，但仓库树没有 LICENSE，读取返回 404 | 界面方向可关注；许可证未确认，退出严格开源主排名，SSO/完整别名也未验 |
| [Mail-0/Zero](https://github.com/Mail-0/Zero) | MIT、现代界面和云邮箱方向；本轮未建立通用 DMS IMAP/SMTP、ANAS IAM 与恢复的证据链 | 相邻观察，不用其 10,860 Star 代替集成依据 |
| [RainLoop](https://github.com/RainLoop/rainloop-webmail) | 主仓库已归档，最后 push 为 2024-08-21（新加坡）；SnappyMail 是独立维护分支 | 不新增部署旧主线 |
| [Mailpile](https://github.com/mailpile/Mailpile) | 当前仓库最后 push 为 2023-11-02（新加坡），未归档 | 活跃度不足，不作为新多用户平台候选 |
| [SquirrelMail](https://squirrelmail.org/)、[Horde IMP](https://www.horde.org/apps/imp) | 属传统 Webmail/群件路线；本轮未完成当前版本与认证深查 | 无现代 UI 优势的专项证据，列入补漏范围，不断言全部停更 |
| Mailcow、Mailu、Modoboa、mox | 完整邮件服务或套件，不能当作只替换 DMS 的前端 | 沿用[邮件后端研究](/research/mail-server-anas-integration-research)，不重复排名 |
| Mailpit、MailHog、Thunderbird 容器桌面 | 测试邮件捕获器或远程桌面客户端 | 不属于面向 ANAS 人员邮箱的原生 Webmail，排除 |

Bulwark 代理有价值，但它把选型变成 DMS + JMAP 代理 + Webmail，还要维护凭据、身份和增量同步状态。若决定进入该路线，应先形成具体设计并按仓库复杂设计规则确认；本次只是记录可能性，没有实施。[Bulwark 配置](https://bulwarkmail.org/docs/getting-started/configuration)、[代理实现说明](https://github.com/bulwarkmail/legacy-proxy)

### 发现来源与检索局限

- [awesome-selfhosted Webmail 分类](https://awesome-selfhosted.net/tags/communication---email---webmail-clients.html)：发现/确认 Roundcube、SnappyMail、Cypht、SquirrelMail。
- AlternativeTo 反向发现：以 Gmail 的会话/搜索、Outlook 的群件、Fastmail 的自有域/多地址为商业基准，尝试各自的 Open Source + Self-Hosted 页面，并做 Roundcube 第二跳。仅 [Outlook 过滤页](https://alternativeto.net/software/microsoft-office-outlook/?license=opensource&platform=self-hosted)成功提取，补出 Nextcloud Mail、Mailpile；Gmail、Fastmail、Roundcube 页面抓取失败，不能声称完成全部分页候选核验。
- GitHub 官方仓库/主题与搜索：补出独立 MailFlow、Bulwark、oxi、Twake Mail、Zero，并核验当前仓库身份；不把其他项目中名为 mailflow 的工作流功能混为一谈。
- [selfh.st Apps](https://selfh.st/apps/?tag=Email) 入口已检查，但目录需要 JavaScript，检索工具未提取完整清单；没有把空结果理解成没有候选。

目录用于发现，能力结论以官方文档和代码为准。本轮不是穷尽所有新项目；后端完整套件与桌面客户端的筛选范围有意较窄。

### UI 观察方法与限制

本轮看过原 DMS PoC 的 Elastic 截图、SnappyMail 固定版本官方截图、Nextcloud 商店截图、MailFlow 官网完整截图，以及 SOGo 官方公开账号的真实收件箱。Cypht 仅检查了官网及其界面预览，Afterlogic 以官方截图/功能边界为依据；没有测试这些候选的中文输入、手机断点、无障碍、长会话和大附件。因此 UI 判断是可复核的视觉/功能筛选，不是完整可用性或性能测评，也不把官网宣传的“fast”当实测。

## 9. 建议的下一轮比较

**最小后续顺序：先把 Roundcube 的两项失败形成可复查的修复候选，同时优先验证 SOGo；若更看重现有界面和组件复用，再验证 Nextcloud Mail；若视觉优先，再加入 MailFlow。** SnappyMail 先明确可固定的维护发行和通用 IAM 路线，避免一开始就维护 master 加认证改造。

| 比较问题 | 复用的现有判据 | 必须独立观察 |
| --- | --- | --- |
| 真正无二次密码进入本人邮箱 | MAILPOC-R-002/003 | 已登录 IAM 后打开候选；新用户预配、无效令牌和 Bob→Alice 拒绝，不仅看 Webmail 首页 |
| 主域后缀变化后归属不变 | MAILPOC-R-004、DIRKEY-R-003/010/013 | 固定 anchor/sub，改变系统主地址；核对原邮件、联系人、签名、偏好、发信身份与 URL/登录名投影 |
| 别名共箱与撤销 | MAILPOC-R-005/006、门户模型 | 两个别名真实界面收发；删除后立即拒绝旧地址，独立 SMTP 构造合法信封配非法 From，保留历史 |
| data 与恢复 | MAILPOC-R-007/008/009 | 停机备份、空目录恢复；包含客户端状态、临时/待发送数据与 Postfix 真队列，独立核对原文/附件哈希 |
| 维护成本 | MAILPOC-R-010 | 固定镜像/依赖、需要新增的状态/密钥、升级迁移和实际资源；没有运行就保留未执行 |

这些是候选比较建议，不改写原来明确以 Roundcube 为对象的 PoC 矩阵，也不新增已确认范围外的验收项。正式替换、共享 master user、跨库身份同步或新增 JMAP 代理等设计未获本报告的研究行为自动授权。

当前最大工作量在**稳定身份绑定、DMS 发信授权、Portal 别名生命周期与完整恢复**。选好一个 UI 可以减少界面开发，却省不掉这些规则；适配复杂度整体为中高，若引入多系统同步、代理和故障补偿则为高。应先验证原生组合能否解决具体失败，再决定是否值得承担自研适配。
