---
doc_type: research
created: 2026-10-16
updated: 2026-10-16
evidence_as_of: 2026-10-16
---

# 邮件服务器对个人多地址邮箱及 ANAS 的适配研究

本报告研究已确认的“一个主邮箱、多个个人别名、共用收件箱”，以及用户自助增删别名如何接入 ANAS。事实来自官方文档、固定版本源码和同日仓库 API；本轮没有启动邮件服务器、测量资源或在 finance 修改服务。产品设计见[门户用户管理、邮箱归属与域级管理方案](../architecture/portal-account-mail-administration.md)。

## 1. 结论

**多地址共用收件箱已有成熟的原生表达方式，ANAS 不需要自己实现 SMTP、邮箱存储或转发引擎。** 真正决定适配成本的是：AD 原生认证、邮箱与永久用户身份的绑定、别名发信权限、可自动管理的接口，以及改主地址后能否保留原邮箱。

建议先对 **docker-mailserver（DMS）和 Stalwart** 做对照 PoC：前者有明确 Samba AD 配置示例、标准 Postfix/Dovecot 和文件管理路径；后者有原生 LDAP 后端，单服务及现代管理面值得验证。两者都还没有在 ANAS 上证明“改址不换箱、稳定锚点、禁用撤销和自助别名写入”这一整条链路。它们的 A 级表示值得优先实验，不表示可以直接上线。[DMS 固定版本 LDAP 文档](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/docs/content/config/account-management/provisioner/ldap.md)、[Stalwart LDAP 说明](https://stalw.art/docs/auth/backend/ldap/)。

**mailcow 是管理 UI/Webmail/群件优先的 B 级备选**，原生别名收发模型和通用 OIDC 比较完整，代价是较大的整套运行依赖、应用密码撤销和邮箱身份映射的额外核验。**Mailu 是管理 API 和邮件功能较均衡的 B 级备选**，但本轮没有核到固定社区版原生 AD/LDAP 的完整认证链路，不把代理保护 Web 入口算成 AD 邮件客户端认证。[mailcow 收发模型](https://docs.mailcow.email/models/model-sender_rcv/)、[通用 OIDC](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/)、[Mailu API](https://mailu.io/2024.06/api.html)。

这与[2026-08 的完整邮件/转发研究](self-hosted-open-source-mail-services-research.md)不矛盾：旧报告按公网外部转发和 SRS 排序，本报告优先 AD 身份与邮箱生命周期。不能把“转发优先 Mailu”直接变成所有 ANAS 邮件场景的默认选择。

## 2. 主题、范围与证据边界

```yaml
topic: mail-server-anas-integration
snapshot_date: 2026-10-16
decision_for: ANAS 邮件 Module 的首轮验证对象
target_users: 家庭及小团队；研究假设 5–50 人，不作为已确认容量要求
core: 可接收 SMTP、保存个人邮箱、提供 IMAP 或 JMAP 的开源服务/套件
adjacent: 群件、组件组合、隐私别名和外部转发
excluded: 仅客户端、仅发信/测试收信、SaaS-only
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2
  web_ingress: Traefik HTTPS
  architectures: [amd64, arm64]
priorities:
  - AD 是人员/密码权威，Casdoor 是主力 IAM
  - 单主地址、多别名、同一邮箱；首期支持个人别名自助维护
  - 不复制 AD 密码，不默认增加 Casdoor 源码补丁
  - 原生接口、稳定身份、可恢复生命周期优先
```

判定符号：✅ 官方原生能力有依据；◐ 有限制、需映射/组合或尚待固定版本证明；— 未确认，不等于不支持。收件别名、发信身份、登录名、群组列表、共享邮箱和 `+tag` 子地址分别判断。

**局限**：这轮完成文档/源码研究，不是互通验收；没有公网投递、MFA、Casdoor→邮件客户端、LDAP schema、改址存储路径、镜像拉取或备份恢复的实测。滚动文档与固定 release 可能不同，版本漂移单独列出。CPU/RAM/镜像体积、并发数和全量 90/365 天贡献统计未测，不用 Star、最近 push 或单条 issue 推导性能和生产成熟度。

## 3. 候选如何发现

本轮使用三个独立目录：[awesome-selfhosted 邮件完整方案](https://awesome-selfhosted.net/tags/communication---email---complete-solutions.html)、[selfh.st Apps](https://selfh.st/apps/?tag=Email)、[AlternativeTo](https://alternativeto.net/software/microsoft-exchange-server/?platform=self-hosted)。前两者发现轻量服务和完整套件；selfh.st 页面部分内容依赖脚本，本轮没有完整导出其标签结果。聚合目录只用于发现，技术判断回到上游。

商业基准反向检索与第二跳：

| 基准/入口 | 用来比较的场景 | 本轮访问结果 |
| --- | --- | --- |
| Microsoft Exchange / Microsoft 365 | 目录、别名发信、管理员和群件 | 未过滤 alternatives 页面有结果；`license=opensource&platform=self-hosted` 及第二页在工具中不可访问，未声称读完分页 |
| Google Workspace / Gmail | 用户主地址、别名及日历/文档边界 | [Open Source + Self-Hosted 页面](https://alternativeto.net/software/g-suite/?license=opensource&platform=self-hosted)可读，显示 5 个结果，发现 Kolab/WikiSuite；CryptPad/Nextcloud 不因此变成完整邮件后端 |
| Fastmail | 个人多地址、单邮箱、客户端体验 | filtered alternatives 访问失败；以官方产品候选和其他两个独立目录补漏，不采用聚合站的功能结论 |
| mailcow 第二跳 | 近似自部署套件 | 两个名称路径及 filtered alternatives 均不可访问；记录检索缺口，不宣称第二跳完整 |

本报告不声称穷尽所有开源邮件项目。以下台账给已发现项目明确去向：

| 类别 | 项目 | 本轮处理 |
| --- | --- | --- |
| 核心详评 | DMS、Stalwart、mailcow、Mailu | 优先核对身份、原生管理、Compose/数据边界 |
| 核心次选 | Modoboa、iRedMail、Mox、WildDuck、Maddy、Mail-in-a-Box | 核对原生地址模型及明显集成成本；未确认项保留 |
| 相邻群件/组合 | Kolab、grommunio、Zimbra、Open-Xchange、WikiSuite、DebOps、Simple NixOS Mailserver | 需要进一步拆服务端、发行包、许可与管理组件；本轮不作为证据完整的 A/B 候选，不据目录名称认定整个发行均开源或可用 |
| 相邻组件 | Postfix、Dovecot、SOGo、Roundcube、SnappyMail | 可构成完整系统，但单个组件不能同时替代邮箱后端与人员生命周期；DMS 已提供部分成熟组合 |
| 相邻转发/隐私别名 | SimpleLogin、addy.io、ImprovMX 类产品、邮箱转发器 | 个人隐私转发有价值，但不是本轮同一后端里的个人收件箱，见既有转发研究 |
| 非严格开源或许可未充分核验 | Poste.io 的完整发行及付费功能、部分群件企业发行 | 不进入严格开源主表；未核验不等于认定整个项目闭源 |
| 排除 | Postal、Mailpit、MailHog、Inboxen、仅客户端/仅办公文档方案 | 发信平台、测试收信或其他用途不满足普通人员长期收件箱；不据“email”标签纳入 |
| 未深入 | b1gMail 等目录余项 | 未完成当前许可、固定发行及 ANAS 身份核验，不能给适配推荐 |

## 4. 个人多地址能力与身份接入

| 产品 | 同一邮箱多个地址 | 别名发信/地址管理 | AD 与 OIDC 边界 |
| --- | --- | --- | --- |
| DMS | ✅ LDAP alias 或文件 alias 可投递原邮箱 | ✅ 原生配置/CLI；发送方归属过滤需显式配置 | ✅ 官方 Samba AD/LDAPS 示例；◐ OAuth2 SASL 有原生机制，Casdoor/具体客户端未实测 |
| Stalwart | ✅ LDAP 主地址/别名属性，或内部目录多地址 | ◐ 内部目录管理与外部目录写入不同；外部 LDAP 由外部系统维护 | ✅ LDAP 用户 bind；◐ 原生 OIDC、SCIM 与许可/版本边界分别核验 |
| mailcow | ✅ 原生 mailbox + alias，可共用收件箱 | ✅ 原生别名发信、sender ACL、管理 API；主地址改名保箱仍待测 | ✅ 原生 LDAP 和通用 OIDC；OIDC 模式的 IMAP/SMTP 等客户端走应用密码 |
| Mailu | ✅ alias 指向已有 mailbox | ✅ API/CLI；不能用全域 wildcard sender 代替个人发信许可 | ◐ 固定版源码确认本地凭据模型；原生 AD/LDAP 邮件认证完整链路未确认 |
| Modoboa | ✅ 原生邮箱/alias 管理模型 | ✅ 版本化 API 有账户和 alias viewset；AD 模式的端到端 sender 权限待测 | ◐ 官方有 LDAP 认证/自动建号配置；邮件协议端及稳定锚点需另核 |
| iRedMail | ✅ Postfix/Dovecot 和别名配置 | ◐ REST 管理 API 所核文档属于 iRedAdmin-Pro，不能算免费社区 API | ◐ 官方 AD 接入指南；iRedAdmin 的账号管理模型不能自动视为 AD 生命周期支持 |
| Mox | ✅ 一个 account 下增加多个 address | ✅ 原生 CLI；其 `alias` 术语通常指列表，不应错误建立第二账户 | — 未核到可直接作为 ANAS 主路径的原生 LDAP/通用 OIDC 认证 |
| WildDuck | ✅ 地址归属于内部 user ID | ✅ 官方地址 CRUD API、主地址字段；发信限制还需连同完整投递栈验证 | — 本轮未核到原生 AD/OIDC 完整接线；不能用调用认证 API 替代 LDAP 联邦 |
| Maddy | ◐ 原生映射表/重写与 LDAP 可组合 | ◐ 需自行组织地址、账户和 sender 检查配置；管理 REST 未确认 | ✅ 官方 LDAP bind 和 AD 过滤示例；内置 IMAP 存储仍明确标 beta |
| Mail-in-a-Box | ✅ 套件原生用户/aliases | ✅ 控制面板及对应 API；改址保箱与 AD 绑定待核 | — 原生 ANAS LDAP/OIDC 链路未确认；目标是接管整台主机的邮件 appliance |

重点证据：[DMS LDAP](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/docs/content/config/account-management/provisioner/ldap.md)、[Stalwart 目录选择](https://www.stalw.art/docs/install/directory/)、[mailcow 收发模型](https://docs.mailcow.email/models/model-sender_rcv/)、[Mailu CLI](https://mailu.io/2024.06/cli.html)、[Modoboa LDAP 配置](https://modoboa.readthedocs.io/en/latest/configuration.html)、[iRedMail AD](https://docs.iredmail.org/active.directory.html)、[Mox 命令](https://www.xmox.nl/commands/)、[WildDuck 地址 API](https://docs.wildduck.email/docs/wildduck-api/get-addresses/)、[Maddy LDAP](https://maddy.email/reference/auth/ldap/)、[Mail-in-a-Box v77](https://github.com/mail-in-a-box/mailinabox/tree/v77)。

### 通用边界：网页登录成功不代表邮件客户端 SSO

SMTP/IMAP 客户端通常使用密码或 SASL `XOAUTH2` / `OAUTHBEARER`。通用 OIDC Web 登录、ForwardAuth、可收别名和邮件客户端识别 Casdoor 是不同能力。门户退出 IAM 不会自动终止所有 IMAP 长连接，也不必然撤销已发的应用密码。

DMS 的固定版说明明确要求预先配置可收件账号，并指出通用 OAuth2 客户端兼容和文档仍有局限。本轮不沿用其 Authentik 示例；后续只验证 Casdoor 的 token、用户映射、audience、失效和撤销，不能拿一段通用 userinfo URL 当作已经安全互通。[v16.0.1 OAuth2 文档](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/docs/content/config/account-management/supplementary/oauth2.md)。

mailcow 文档明确将 OIDC Web 登录后的应用密码用于 IMAP、SIEVE、POP3、SMTP。这是可用的产品路径，但必须让禁用、重置密码、退出和应用密码撤销各有真实语义，不能承诺一个登出请求注销所有邮件客户端。[mailcow 通用 OIDC](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/)。

## 5. 部署拓扑、存储与 ANAS 适配等级

等级采用研究规范：A 优先 PoC，B 可选但有明确成本，C 仅专项需求，D 不建议。以下判断是本报告的工程推论，依据是上游原生能力及 ANAS 既有 AD/Compose/Traefik 模型，不是上游认证。

| 产品 | 运行及持久化边界 | 架构证据和资源 | 等级与主要原因 |
| --- | --- | --- | --- |
| DMS | 原生 Docker；Postfix/Dovecot 等在一容器，无 SQL；配置、邮件、mail-state、日志；Webmail 另配 | 本轮未查完固定 tag 的 amd64/arm64 manifest；资源取决于反垃圾/杀毒，不报虚构最低值 | **A**：Samba 配方最明确；配置管理适合 Module，需补受控别名管理与邮箱稳定路径 |
| Stalwart | 原生容器/单主服务；可用内嵌或外部存储；配置和数据、签名密钥需一起恢复 | 官方称 multi-architecture，固定 tag 的双架构 digest 待核；未做资源实测 | **A**：原生 LDAP、少组件；外部目录只读、别名属性和稳定邮箱映射是 PoC 门槛 |
| mailcow | 官方 Compose 多服务；MariaDB、Redis、SOGo、Postfix、Dovecot、Rspamd 等；配置/数据库/邮件/加密材料 | 官方列 x86_64、ARM64；默认最低 6 GiB RAM + 1 GiB swap、20 GiB 未含邮件；不是本轮实测 | **B**：管理/群件完整，但私有整栈和权限、网络接管成本较高 |
| Mailu | 官方 Compose 多服务；管理库、Redis、SMTP/IMAP、可选 Webmail/杀毒等；配置、mail、DKIM、数据库 | 官方说明 amd64/arm64v8/armv7，固定 .64 全组件 manifest 待核；无本轮基准 | **B**：原生管理 API 完整；先解决不复制 AD 密码的认证路径 |
| Modoboa | Django 管理面 + 数据库 + Postfix/Dovecot 等；官方主机 installer；配置/数据库/邮件 | 固定官方 Compose 多架构交付未确认；installer 的升级/备份/恢复仍标 experimental | **C**：目录/UI 可用，但改变 ANAS 部署边界，运维契约要额外维护 |
| iRedMail | 主机级 installer；可选 SQL/OpenLDAP 后端及完整邮件栈 | 官方 1.8.8 支持发行列表；SOGo 的 ARM 包存在官方限制 | **C**：AD 指南可参考，主机栈、手动升级与 Pro API 不符合最简首选 |
| Mox | 单 Go 服务，内置 Webmail/管理；配置及账户数据；原生命令备份/校验 | 固定官方双架构镜像链路未核；无资源基准 | **C**：同账户多地址很贴合，但 AD 原生身份接入未确认 |
| WildDuck | Node 服务、MongoDB 存邮件、Redis；完整发收还需配套 SMTP/队列组件 | 固定完整栈双架构镜像未核；MongoDB 不等于可复用 ANAS PostgreSQL | **C**：API 适合自动化，但新增数据栈和身份适配成本明显 |
| Maddy | 单 Go 邮件服务，可搭 Dovecot；配置、索引、邮件需恢复 | 原生 LDAP 有依据；固定 README 称 IMAP storage beta | **C**：认证接近需求，但不宜默认采用 beta 存储；另配 Dovecot 后组合维护增加 |
| Mail-in-a-Box | 在全新 Ubuntu 上接管邮件、DNS、Web、证书、防火墙及备份 | v77 README 指定 Ubuntu 22.04 64-bit；独立 Compose/双架构未确认 | **C**：独立邮件专机合适，直接嵌入 ANAS 与现有 Traefik/DNS 冲突较多 |

证据：[DMS 固定 README](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/README.md)、[Stalwart 容器说明](https://stalw.art/docs/install/platform/docker/)、[mailcow 固定 Compose](https://github.com/mailcow/mailcow-dockerized/blob/2026-09a/docker-compose.yml)、[系统要求](https://docs.mailcow.email/getstarted/prerequisite-system/)、[Mailu 部署](https://mailu.io/master/setup.html)、[Modoboa installer](https://github.com/modoboa/modoboa-installer)、[iRedMail 发行平台](https://www.iredmail.org/download.html)、[Mox](https://github.com/mjl-/mox/tree/v0.0.17)、[WildDuck](https://github.com/zone-eu/wildduck/tree/v1.52.0)、[Maddy 固定 README](https://github.com/foxcpp/maddy/blob/v0.9.6/README.md)、[Mail-in-a-Box](https://github.com/mail-in-a-box/mailinabox/blob/v77/README.md)。

### 共同接入约束

- **入口**：Traefik 可管理 Web HTTPS；邮件收信 25、submission 465/587、IMAPS 993 等需独立 TCP 发布和邮件 TLS。只发布 HTTP 路由不能让邮件客户端工作；默认不开放不需要的 POP3/明文端口。
- **身份**：不拿 `mail`、UPN 或当前登录名当永久邮箱 ID。账号、主地址和磁盘目录必须分开；改主地址后旧 `sub`/anchor、内部邮箱 ID、邮件历史和配额保持一致。
- **权限**：登录/收件资格分别检查账号禁用和邮件服务准入，不能仅因 AD 存在用户就给所有人员自动开箱。别名 API 仅可由受控服务调用，不能把全局管理员 token 放到浏览器。
- **依赖**：DMS 的无 SQL、Stalwart 的内嵌存储可避免新增共享数据库；不能为了“复用”强迫 mailcow 的固定 MariaDB 模型迁到 PostgreSQL。WildDuck 的 MongoDB 是新增私有依赖。
- **数据**：邮箱正文、索引、投递队列、账户/地址映射、DKIM 私钥、TLS/应用密码、数据库各有恢复含义。最终按 Module 的 `data/`、`userdata/`、Secret 和备份契约声明，不在研究里把所有宿主目录直接打包成一个通用恢复脚本。
- **外部联网**：公网邮件依赖 DNS、对端 SMTP、可能的 DNSBL/反垃圾数据和杀毒签名更新；若另用 smarthost，明确外发内容和凭据归属。是否有版本检查/许可证校验应按固定版本配置实查，不宣称所有候选完全离线。
- **AI**：本轮不要求内置 AI，也不授予 AI 邮箱管理或读信权限。原生管理 API 不等于已经提供安全的 AI/MCP 接口；各候选 AI 功能未做选型核验。

## 6. 重点候选详评

### 6.1 DMS：最直接的 Samba AD 验证起点

v16.0.1 文档有 Samba4 AD 专节，要求 LDAPS/StartTLS，提供 `sAMAccountName`、Dovecot 用户属性与 LDAP 收件/别名查询示例。通过用户 bind 验密码，可避免复制 AD 散列。DMS 不需要自建 SQL 账号库，ANAS 可以生成版本化配置。[固定 LDAP 文档](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/docs/content/config/account-management/provisioner/ldap.md)。

但示例不能原样部署：其中发送方查询包含 Domain Admins 例外，ANAS 应去掉这种任意发信捷径；示例 `proxyAddresses`/`mailAlias` 属性也不证明 ANAS Samba schema 当前可直接使用。Dovecot home 路径若来自用户名，改名和改址可能分出新目录，须明确稳定映射。LDAP alias 查询与本地 alias 文件如何共同生效要在固定版本验证，不能默认混用两套 provisioner。

管理入口主要是配置/CLI，不是现成租户隔离 REST 平台。首期自助别名可以由受控 Module 操作生成后端配置，但不能让门户获得 Docker socket、宿主 shell 或任意 `setup.sh` 调用权。若 native LDAP 属性能完整表达别名，则由邮件 Module 作为唯一业务写入者、DMS 只读消费；若需新增 schema 或生成地址映射文件，在 PoC 后选择一种，不同时维护两套别名权威。

备份应覆盖配置、邮件和 mail-state；官方 FAQ 有备份/恢复路径，但在线 `tar` 示例不等于多目录事务一致快照。ANAS 仍应停写或使用明确的一致性窗口，并测试恢复后地址、IMAP 和队列。[DMS FAQ](https://docker-mailserver.github.io/docker-mailserver/latest/faq/)。

### 6.2 Stalwart：服务结构简洁，外部目录的写入位置需先解决

官方 LDAP 文档支持服务账号查询和用户 bind，主地址与 alias 属性可映射。默认 RFC 类对象/属性需改成 AD 模型，并加入人员类型、禁用状态和服务准入判断。使用外部 LDAP 时，上游明确要求外部系统维护用户；本地管理 UI/API 不能自动获得修改 AD 的能力。[LDAP](https://stalw.art/docs/auth/backend/ldap/)、[外部目录边界](https://www.stalw.art/docs/install/directory/)。

因此 ANAS 的姓/名、密码写 AD；邮件 Module 的别名若由 LDAP 供给，也要有一个经过验证的 AD 属性写入路径。若改用内部目录加 OIDC，则必须另外解决账号/地址预配置和停用，不能在“LDAP 无副本”和“内部账号由 SCIM 推送”之间来回混合而不明确权威。

固定 v0.16.26 README 同时列出 LDAP/OIDC/SCIM，并不意味着各功能都在社区授权下可用。当前官方比较页将 SCIM provisioning 列入 Enterprise-only；本轮不给“免费社区版 SCIM 已满足”结论。滚动文档出现新对象/API 表达时，还须检查与固定 release 的一致性。[固定 README 与许可证说明](https://github.com/stalwartlabs/stalwart/blob/v0.16.26/README.md)、[官方版本比较](https://www.stalw.art/compare/)。

存储可先选择官方支持的单机后端，降低组件数。备份不应只导出管理配置而漏掉邮件/blob/索引/密钥；外部数据库或对象存储要按各后端一致性处理，不能直接复制运行中的 RocksDB/SQLite 后声称可恢复。[官方备份 FAQ](https://stalw.art/docs/faq/)。

### 6.3 mailcow：功能完备，但整体边界较重

mailcow 的原生收发模型明确支持 mailbox aliases 和 sender delegation；固定 2026-09a OpenAPI 有邮箱、别名、应用密码及 ACL 操作。它适合不希望另外组合 Webmail、日历和管理界面的部署。[收发模型](https://docs.mailcow.email/models/model-sender_rcv/)、[固定 OpenAPI](https://github.com/mailcow/mailcow-dockerized/blob/2026-09a/data/web/api/openapi.yaml)。

原生 LDAP 和 Generic-OIDC 可用，但账号绑定、模板自动建号、地址与稳定身份需针对 Casdoor/ANAS 验证。文档还说明切到外部认证后旧 SQL 密码保留、切回本地认证可能再次生效，迁移和恢复不能意外打开旧密码登录。[LDAP](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-ldap/)、[OIDC](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/)。

官方完整 Compose 含私有 MariaDB、Redis、网络过滤等配套；不能只替换 Web 容器来完成 Module。备份需要邮件、数据库、crypt 和其他组件；官方脚本与上游 update 流程还要包装到 ANAS 的停写、备份和恢复约束中。[Compose](https://github.com/mailcow/mailcow-dockerized/blob/2026-09a/docker-compose.yml)、[备份文档](https://docs.mailcow.email/backup_restore/b_n_r-backup/)。

官方系统要求还排除 LXC/OpenVZ 及 Synology/QNAP 等成品 NAS 环境。ANAS 的普通 Linux Docker 宿主与成品 NAS 不是同一支持声明；若要放在 Incus 内，不能因 Docker 可启动就认定嵌套容器受支持，应选上游允许的完整虚拟机环境再验证。[平台限制](https://docs.mailcow.email/getstarted/prerequisite-system/)。

### 6.4 Mailu：别名和管理接口贴合，身份是首个问题

2024.06.64 的 User/Alias 模型及 CLI 支持本地 mailbox、指向目标的 alias、域别名和管理操作，API 有专门认证配置。适合把收发和 UI 交给成熟套件，再由 ANAS 控制地址入口。[固定模型](https://github.com/Mailu/Mailu/blob/2024.06.64/core/admin/mailu/models.py)、[CLI](https://mailu.io/2024.06/cli.html)、[API](https://mailu.io/2024.06/api.html)。

但所核 User 模型主要是本地密码验证；本轮未确认无需新补丁就能让 Web 与 SMTP/IMAP 完整使用 ANAS AD 身份。反向代理认证头只解决相应 Web 请求，不能当成邮件客户端认证证明。`WILDCARD_SENDERS` 允许广泛冒用发件地址，不符合个人别名的最小许可，不采用它简化集成。[固定认证入口](https://github.com/Mailu/Mailu/blob/2024.06.64/core/admin/mailu/internal/views/auth.py)、[配置说明](https://mailu.io/2024.06/configuration.html)。

升级涉及 Compose、环境配置和数据迁移；原生 user-delete 的默认停用与实际清除用户/邮件不是一回事。ANAS 必须区分停用、地址保留和数据销毁。[运维](https://mailu.io/2024.06/maintain.html)、[删除命令语义](https://mailu.io/2024.06/cli.html)。

### 6.5 次选方案的重要区别

- **Mox**：官方命令可给 account 增加 address；个人多地址应采用这个模型，其 alias 则用于列表。模型贴合，但未证实原生 AD/Casdoor 认证，不能因为简单就忽略另建凭据/联邦的成本。[命令](https://www.xmox.nl/commands/)。
- **WildDuck**：固定 OpenAPI 用 user ID 关联地址，支持新增、删除、更新与域改名，适合外部自动化；完整 SMTP/队列和 MongoDB/Redis 备份面更大。认证 API 本身不证明已支持 LDAP/OIDC 身份联邦。[v1.52.0 OpenAPI](https://github.com/zone-eu/wildduck/blob/v1.52.0/docs/api/openapidocs.json)、[README](https://github.com/zone-eu/wildduck/blob/v1.52.0/README.md)。
- **Maddy**：原生 LDAP bind 甚至给出带禁用过滤的 AD 示例，值得借鉴；但 v0.9.6 明确说内置 IMAP storage beta，不能只看到发行非 prerelease 就认定所有存储成熟。[LDAP](https://maddy.email/reference/auth/ldap/)、[固定 README](https://github.com/foxcpp/maddy/blob/v0.9.6/README.md)。
- **Modoboa**：原生 LDAP 配置和 v2 API 可参考；官方 installer 仍标 beta，upgrade/backup/restore 标 experimental，ANAS 不应在缺少恢复实测时认定为可运维 Module。[配置](https://modoboa.readthedocs.io/en/latest/configuration.html)、[固定 API](https://github.com/modoboa/modoboa/blob/2.11.1/modoboa/admin/api/v2/viewsets.py)、[installer](https://github.com/modoboa/modoboa-installer)。
- **iRedMail / Mail-in-a-Box**：前者有 AD 指南，但免费社区与 Pro 管理 API 分界重要；后者有面板/API，却设计为接管一台新主机。二者更适合独立邮件专机研究，不能未经适配就在现有 ANAS 主机执行安装器。[iRedAdmin-Pro API](https://docs.iredmail.org/iredadmin-pro.restful.api-20190327.html)、[Mail-in-a-Box v77](https://github.com/mail-in-a-box/mailinabox/tree/v77)。

## 7. 开源、社区自部署与商业边界

下表针对所列服务端/编排仓库。套件包含的 Postfix、Dovecot、Webmail、数据库等仍各按其许可证；顶层 MIT/CC0 不会把所有打包组件变成同一许可。公开 API 的 license 字段为空或 NOASSERTION 也不等于闭源，应读 LICENSE。

| 产品 | 仓库许可证与社区边界 | 商业/企业注意事项 |
| --- | --- | --- |
| DMS | MIT 编排；原生 LDAP/别名/OAuth2 文档公开 | 未核到这些核心能力需商业 entitlement；不以此证明全部配套服务免费或无限制 |
| Stalwart | AGPL-3.0 与 SELv2 按文件许可；企业功能需区分 | 当前比较页将 SCIM provisioning 放在企业区；不使用企业宣传补齐社区能力 |
| mailcow | GPL-3.0；公开 Compose 和 API | 商业支持与托管另计；本轮没发现 LDAP/OIDC/alias 必须付费的依据，未审计所有组件 entitlement |
| Mailu | 固定 LICENSE.md 为 MIT | 不能因社区 LDAP 讨论/第三方脚本存在就当原生稳定支持；支持套餐不在本轮报价范围 |
| Modoboa | ISC；API 源码公开 | 托管/支持另计；免费自部署功能上限、全部扩展许可未逐项审计 |
| iRedMail | GPL-3.0 installer；社区组件各自许可 | 所核 REST 管理 API 文档属 iRedAdmin-Pro；Enterprise 单独发行，不当作同一免费产品 |
| Mox | MIT | 无本轮证据表明个人多地址要商业授权；官方托管套餐/支持未核 |
| WildDuck | EUPL-1.2 或更新；配套投递组件另核 | Zone Mail Suite 商业服务不自动改变所列仓库许可，完整发行和支持另核 |
| Maddy | GPL-3.0 | 维护状态与内置存储 beta 分开；不因免费就忽略组件成熟度 |
| Mail-in-a-Box | CC0-1.0 编排；打包组件分别许可 | 原生面板/API 不等于有商用 SLA；专机运维成本仍存在 |

许可证入口可从各仓库的 LICENSE/README 复核；特别核对了 [Mailu 固定 LICENSE](https://github.com/Mailu/Mailu/blob/2024.06.64/LICENSE.md)、[Stalwart 固定许可说明](https://github.com/stalwartlabs/stalwart/blob/v0.16.26/README.md#license)、[WildDuck 固定许可说明](https://github.com/zone-eu/wildduck/blob/v1.52.0/README.md#license)。本轮没有把任何厂商免费 SaaS 配额写成社区自部署限制，也没有为未核实的托管套餐编造价格/容量。

## 8. 同日版本与维护信号

采集日 2026-10-16；以下 push/release 日期按 GitHub UTC。Star 只是仓库快照，均为服务端或编排仓库，非客户端仓库。十个仓库均 `archived=false`。GitHub `releases/latest` 返回的九个版本均 `prerelease=false`，这不等于产品所有组件都已稳定。

| 仓库 | 创建日 | Stars | 最后 push | 最新 release / 日期 |
| --- | --- | ---: | --- | --- |
| [docker-mailserver/docker-mailserver](https://github.com/docker-mailserver/docker-mailserver) | 2015-03-28 | 19,581 | 2026-10-13 | v16.0.1 / 2026-09-04 |
| [stalwartlabs/stalwart](https://github.com/stalwartlabs/stalwart) | 2023-03-06 | 15,118 | 2026-10-15 | v0.16.26 / 2026-10-11 |
| [mailcow/mailcow-dockerized](https://github.com/mailcow/mailcow-dockerized) | 2016-12-09 | 13,617 | 2026-10-15 | 2026-09a / 2026-10-06 |
| [Mailu/Mailu](https://github.com/Mailu/Mailu) | 2016-01-10 | 7,554 | 2026-10-15 | 2024.06.64 / 2026-10-15 |
| [modoboa/modoboa](https://github.com/modoboa/modoboa) | 2013-04-16 | 3,542 | 2026-10-15 | 2.11.1 / 2026-10-09 |
| [iredmail/iRedMail](https://github.com/iredmail/iRedMail) | 2019-12-09 | 1,848 | 2026-10-14 | GitHub latest 接口失败；官网 1.8.8 / 2026-09-08 |
| [mjl-/mox](https://github.com/mjl-/mox) | 2023-01-30 | 5,899 | 2026-10-09 | v0.0.17 / 2026-08-19 |
| [zone-eu/wildduck](https://github.com/zone-eu/wildduck) | 2017-03-05 | 2,110 | 2026-10-15 | v1.52.0 / 2026-10-07 |
| [foxcpp/maddy](https://github.com/foxcpp/maddy) | 2016-11-07 | 6,110 | 2026-10-14 | v0.9.6 / 2026-10-03 |
| [mail-in-a-box/mailinabox](https://github.com/mail-in-a-box/mailinabox) | 2013-08-21 | 15,439 | 2026-09-26 | v77 / 2026-09-26 |

数据接口口径：`GET /repos/{owner}/{repo}`、`/releases/latest`、`/commits?per_page=12`。iRedMail 另查[官方下载页](https://www.iredmail.org/download.html)，不能由 GitHub 接口失败推导项目未发布。WildDuck 旧 `nodemailer/wildduck` 会跳转到 `zone-eu/wildduck`，去重为一个项目。

最近 12 条提交的小样本里，DMS 有 georglauterbach 等对 provisioner/IMAP 的修改，mailcow 有 FreddleSpl0it 等提交，Stalwart 有 mdecimus 等协议修复，Mailu 有 nextgens 的管理员界面/测试修改；其他样本也有非依赖机器人提交。没有把 dependabot、GitHub Actions、bors 或 Copilot 当成人类维护者。这只能证明近期有人维护，不能据此给出 90 天完整贡献人数、issue 响应 SLA 或多年维护承诺。

维护结论：核心四项具有持续发布和现成运维文档，适合实验；Stalwart 接口/商业边界仍需固定版本核验。Modoboa installer 和 Maddy storage 有上游明确的实验/beta 限制。Mox 的低版本号、iRedMail 仓库迁移日不能单独用于判定产品年龄或质量。近 90/365 天完整提交/发布次数、issue/PR 响应、全部安全政策覆盖仍未系统统计，是投入生产维护前的补充项。

## 9. 建议的固定版本 PoC

先各跑 DMS v16.0.1 与 Stalwart v0.16.26 的最小独立环境，使用测试域/测试 AD 人员，不替换 finance 现有服务。镜像需锁定实际可拉取 tag/digest及架构；本报告没有做拉取成功承诺。若某候选的原生路径失败，再进入 mailcow 或 Mailu，不能为保留排名默认增加源码补丁。

| 验证主题 | 必须观察的行为 |
| --- | --- |
| AD 人员与准入 | 用户 bind；账号禁用、非人员、无邮件准入不能登录；别名收件资格与认证分开 |
| 稳定邮箱 | 固定用户锚点与邮箱 ID，修改姓名/主地址/未来登录名后同一收件箱、历史、配额仍在；重启/恢复后仍一致 |
| 主地址和别名 | 同域及跨受管域多个地址入同一箱；主地址唯一，别名不建立第二用户；列表/共享邮箱不混入 |
| 自助别名 | 用户只能维护本人；保留地址、他人地址、并发抢占、超配额、重放均拒绝；不可删除主地址或受保护旧址 |
| 发信约束 | 自己授权别名可发；伪造其他用户、管理员、未受管域和移除的别名不可发；同时测 envelope sender 与可见 From |
| 原生 OIDC/API | Web 与邮件协议分别验证，检查 issuer/audience/身份映射/重认证；不用管理员 API token 冒充用户授权 |
| 生命周期撤销 | AD 禁用、密码重置、IAM 登出、应用密码撤销及既有长连接逐项测，记录实际生效延迟和限制 |
| 部分失败 | 后端成功 AD 失败、反向失败、HTTP 超时未知、重复调用和进程中断；无重复箱、无地址误转让、可恢复 |
| 运维 | amd64/arm64 拉取与启动；TLS/端口冲突；完整备份恢复；原 tag 重启、升级失败后恢复；审计和 Secret 不泄露 |
| 资源与公网 | 启动/空闲/小团队负载 RAM、CPU、磁盘、镜像体积；先实验室收发，再独立验证 PTR、25 端口和投递信誉 |

这些是后续实验主题，未分配正式验收编号，也没有“通过”结果。选定候选、确认实施范围后写入 Module 的配对需求矩阵/计划，记录源版本、镜像摘要、逐项结果与阻塞。邮件域、别名数量/频率、配额、地址保留及禁用后收信策略在此时确认，不在研究中冒充已生效配置。

## 10. 证据索引与复核记录

固定文件的公开链接与要点已随结论列出。为便于识别本轮读取的制品，保留以下 SHA-256 摘要；它们是源码/文档文件摘要，不是容器 digest，也不是服务验收：

| 文件 | SHA-256 |
| --- | --- |
| DMS v16.0.1 `docs/content/config/account-management/provisioner/ldap.md` | `f4d8d3030b243357891a41f8f69dca7b4d9362f5b0f734a362b4dd24d72ea11b` |
| Mailu 2024.06.64 `core/admin/mailu/models.py` | `fe66bba4d704e1bbc92832cbecae1d0e02410edd2b4914472c4ae01601daca9c` |
| mailcow 2026-09a `data/web/api/openapi.yaml` | `ae01d1d7886a5bcf62a90946a7651fc6aed608d64bf21325e63328ed03b33493` |
| Stalwart v0.16.26 `README.md` | `69b580d7dd97f1147810c794ce5ef162922163a300a087e5d330797129cf4a15` |

临时抓取、JSON 与失败重试在 `/tmp/anas-mail-research-20261016`，未提交 Git；这不是共享持久档案。可复核事实、公开版本链接、时间和必要摘要已经写入本报告。无服务端验收日志或截图可供本轮声明。

未完成/失败的查询包括：部分 AlternativeTo filtered/分页/第二跳；递归 Git tree 下载中断；猜测的个别 API/CI 文档路径无效。已用固定文件、官网文档及仓库元数据补充可核事实；固定镜像全组件多架构 manifest、社区 SCIM entitlement、邮箱稳定标识和真实端到端能力仍保留为待验证，不以“未找到”写成“不支持”。
