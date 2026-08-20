---
doc_type: research
created: 2026-08-19
updated: 2026-08-19
evidence_as_of: 2026-08-19
---

# 开源自部署完整邮件服务与转发能力调研

本报告按[应用研究文档规范](/developer/research-document-standard)研究完整邮件服务/套件，并把“邮件服务能否转发”拆成两个问题：一是软件是否能把邮件送到本地或外部目标，二是转发时是否具备受支持的 SRS、退信路由和认证链路，足以面对 Gmail、Outlook 等公网收件系统。动态文档状态采集于 2026-08-19；报告是研究快照，不是生产部署说明。

配套阅读：[开源自部署邮件转发与别名服务调研](./self-hosted-open-source-email-forwarding-research.md)。那份报告比较 Cloudflare Email Routing 式透明转发和 addy.io/SimpleLogin 式隐私 alias；本报告只把转发作为完整邮件服务的一项能力来评分。

## 1. 结论先行

1. **完整邮箱服务且外部转发是核心需求时，首轮 PoC 选 Mailu。** 它有管理界面、Webmail、外部 alias、wildcard/catch-all、用户自动转发、保留本地副本、ManageSieve；上游 release notes 明确记录了内建 SRS 支持。它是六个核心候选里“完整套件体验”和“转发认证能力”最均衡的方案，但当前 SRS 操作细节文档不够集中，必须以固定版本做跨 Gmail/Outlook/严格 DMARC 域的黑盒验收后才能生产化。
2. **若偏好配置即代码、可审计的文本配置，选 docker-mailserver（DMS）。** 官方直接支持 alias 到外部地址、catch-all、Sieve `redirect`、forward-only 拓扑以及 `ENABLE_SRS=1`；SRS secret 轮换和独立 SRS 域也有明确配置项。`ENABLE_SRS` 默认是 `0`，部署转发器时必须显式启用。代价是没有内置管理 UI/Webmail，账号与规则生命周期要由 Git/IaC、CLI 或外部界面承担。
3. **iRedMail 是成熟的传统主机栈备选。** 外部 alias、catch-all、用户转发和 SRS 都有官方文档；但社区版常需直接维护 SQL/LDAP，友好的 alias/forward/catch-all 图形管理主要在 iRedAdmin-Pro。SRS 自 iRedAPD 3.3 起默认关闭，而且官方列出了重写过宽、日志/统计和 Return-Path Sieve 等副作用，适合能维护 Postfix/Dovecot/SQL 的团队。
4. **mailcow 适合优先追求群件与管理体验，不应被当成默认公网转发器。** 它能创建 alias/catch-all，也有 SOGo/Dovecot Sieve 转发；但截至快照日，要求集成 PostSRSd 的 [SRS issue #2418](https://github.com/mailcow/mailcow-dockerized/issues/2418)仍为 open。能填写外部 `goto` 或通过 Sieve 送出，不等于在严格 SPF/DMARC 环境下稳定到达。
5. **Stalwart 是很有吸引力的现代、轻量完整邮件服务，但当前不适合承担通用公网转发。** 它有 catch-all、mailing list、Sieve `redirect`/`redirect :copy` 和现代管理面；上游明确表示不实现 SRS。更关键的是 2026-08-18 更新的 [ARC 文档](https://stalw.art/docs/mta/authentication/arc/)明确不再 seal，只验证已有 ARC 链，因此不能再用“ARC 会替代 SRS”作为部署假设。
6. **Modoboa 可做功能性转发，但不列为转发优先候选。** 当前源码允许 alias 指向外部域并有每用户 Sieve；未找到官方提供的受支持 SRS 集成。其主机级 installer 仍标为 beta，upgrade、backup、restore 模式又分别标为 experimental，给恢复和升级带来额外门槛。
7. **SRS 只解决 envelope sender，不单独解决 DMARC。** SRS 重写 SMTP `MAIL FROM`，让转发器能用自己的域通过 SPF，并把退信反向还原；它不会让重写后的 envelope domain 与原始可见 `From:` 对齐。转发后的 DMARC 通常仍依赖原始、对齐的 DKIM 签名未被破坏；转发器用自己的域重新 DKIM 签名而不改可见 `From:`，也不会自动形成 DMARC 对齐。
8. **同一域不能把两个独立 MX 当成“按收件人分流器”。** MX preference 表达下一跳优先级/故障转移，不会把 `user@example.com` 和 `alias@example.com` 自动分给不同产品。完整邮箱域应由一个邮件系统接管；隐私 alias/转发服务使用独立子域（如 `alias.example.com`）或第二域，再转到主邮箱，并对别名目标做循环检测。
9. **出站信誉是自部署邮件的硬门槛，不是安装后的优化项。** 必须有可控静态 IP、PTR/正向解析、开放的 25 端口、SPF、DKIM、DMARC、TLS、队列和滥用处置；住宅/被污染网段或无法配置 PTR 的主机，应优先使用可信 SMTP relay/smarthost。转发器还会因“把垃圾邮件继续送到大厂”快速损伤自身 IP/域信誉。

综合建议：ANAS 若只维护一个完整邮件 Module，先做 **Mailu**；若把邮箱管理/UI 交给外部流程并强调 IaC，做 **docker-mailserver**；若需求只是 Cloudflare Email Routing 式 alias，不应先部署整个群件套件，而应参考配套的专用转发服务调研，并使用独立 alias 子域/第二域。

## 2. 范围、方法与判定口径

```yaml
topic: self-hosted-open-source-mail-services
title: 开源自部署完整邮件服务及其转发能力
snapshot_date: 2026-08-19
decision_for: ANAS module 候选与邮件/转发配套边界
must_be:
  - 源代码与许可证可从项目官方来源核验
  - 能自行部署并接收 SMTP 邮件
  - 提供本地邮箱，或明确作为完整邮件栈的一部分
core_candidates:
  - mailcow
  - Mailu
  - docker-mailserver
  - Stalwart
  - Modoboa
  - iRedMail
adjacent_categories:
  - 完整邮件 appliance、声明式 OS module、现代早期单体服务
  - 需要额外组件才能成为完整套件的邮件存储/协议核心
source_available:
  - 可自部署但许可证不是 OSI/FSF 通常意义开源的完整邮件产品
excluded:
  - SaaS-only 邮箱、只发送邮件的 relay、临时邮箱和纯客户端
target_scale: 1 至 20 用户，低至中等邮件量
deployment_target:
  os: Linux
  preferred_runtime: Docker Engine + Docker Compose v2
  ingress: HTTP 可经 Traefik；SMTP/IMAP 使用 TCP 入口
questions:
  - 是否支持 alias、catch-all、Sieve/规则和外部转发？
  - 是否有 SRS 或其他受支持的转发认证链路？
  - 运维、反垃圾、恢复和出站信誉代价是什么？
search_date: 2026-08-19
```

功能和许可证结论优先来自官方文档、官方仓库、仓库内当前源码和官方 issue/discussion。issue/discussion 只用来确认“上游尚未实现、当前推荐路径或已知限制”，不把用户猜测当成产品承诺。本轮没有搭建六套在线服务做端到端投递，因此所有 `A` 级候选仍需 PoC。

AI/模型能力不作为本主题评分维度。邮件系统的硬契约是 SMTP/IMAP、认证、队列、反垃圾、备份和恢复；外部 AI 分类器属于可选内容处理器，会额外引入正文外发、误判和破坏 DKIM 的风险。

### 2.1 两层转发判定

| 层次 | 通过条件 | 不能据此推导的结论 |
| --- | --- | --- |
| **功能性转发** | alias、catch-all、mailing list 或 Sieve 能产生一个外部 SMTP 投递 | 不代表 SPF/DMARC 通过、退信能还原、收件方愿意接收，也不代表无循环 |
| **公网转发就绪候选** | 除功能性转发外，有官方支持的 SRS/退信反向路径，能控制 secret、重写域和适用范围，并可做跨大厂验收 | 仍不保证原始 DMARC 通过；仍受 DKIM 是否保留、目标方本地策略、内容与 IP 信誉影响 |

符号：✅ 官方社区版原生；◐ 存在限制、需手工配置或证据不完整；❌ 官方明确没有/不支持；— 本轮未找到受支持能力，不能当作有。

### 2.2 候选发现来源与台账

长名单至少从四类相互独立的入口补漏，再回到项目官方仓库、许可证和版本文档核验：

- [awesome-selfhosted 的完整邮件方案](https://github.com/awesome-selfhosted/awesome-selfhosted#communication---email---complete-solutions)：发现传统套件、appliance 与轻量栈；
- [selfh.st Apps](https://selfh.st/apps/)：按 mail、email server、groupware 等关键词补充 Docker 友好候选；
- [AlternativeTo](https://alternativeto.net/)：从 Microsoft Exchange、Google Workspace、Fastmail、mailcow、Mailu 等产品页面反向筛选 `Open Source + Self-Hosted`，再做候选自身的第二跳；
- GitHub/GitLab 的 `mail-server`、`smtp-server`、`groupware` topic、release、许可证和代码搜索：解析现行仓库、官方继任者、SRS/ARC 实现与新项目。

目录只用于发现，不能证明仍活跃、严格开源或支持公网转发。候选去向如下：

| 项目 | 首要发现入口 | 分类 | 证据 | 纳入/去向理由 |
| --- | --- | --- | --- | --- |
| Mailu | awesome-selfhosted、selfh.st、GitHub | core | verified | 完整 Docker 套件、管理/API、外部 alias 与内建 SRS |
| docker-mailserver | awesome-selfhosted、selfh.st | core | verified | 配置即代码、完整 MTA/IMAP，可裁 forward-only 并显式启用 SRS |
| iRedMail | awesome-selfhosted、AlternativeTo | core | verified | 成熟 Postfix/Dovecot/SQL/LDAP 栈，外部转发和手工 SRS 有官方文档 |
| mailcow | AlternativeTo、selfh.st、GitHub | core | verified | 群件/UI 强；功能性转发已证实，但内建 SRS 缺口仍在 |
| Stalwart | GitHub topic、selfh.st | core | verified | 现代单体协议栈；无 SRS、ARC verify-only |
| Modoboa | awesome-selfhosted、AlternativeTo | core | partial | 完整管理栈；外部 alias 可由源码证实，SRS/ARC 仍未确认 |
| Mail-in-a-Box | awesome-selfhosted、AlternativeTo | adjacent / appliance | verified | 适合独占整台 VM，不适合嵌入现有 Compose Runtime |
| maddy、mox | GitHub topic、awesome-selfhosted | adjacent / modern | partial | 轻量现代，需下一轮补齐管理、alias 和 SRS 证据 |
| Simple NixOS Mailserver | awesome-selfhosted、GitLab | adjacent / NixOS | verified | 声明式但限定 NixOS，不是默认 Docker 路线 |
| WildDuck | awesome-selfhosted、GitHub | adjacent / component | verified | 协议/存储核心，完整收发需额外组件 |
| Poste.io | AlternativeTo、产品反向搜索 | source-available / excluded | verified | 可自部署但完整源码和许可边界不符合严格开源 must-be |

本轮审阅集合中没有发现“原核心候选已归档/明确停更”的项目；这不表示生态中不存在停更项目，只表示未把明显不相关的历史仓库扩展成长表。

### 2.3 头部商业/托管基准

商业产品不进入严格开源排名，只用于定义用户熟悉的能力和反向发现方向：

| 基准 | 核心卖点 | 本报告借用的比较维度 |
| --- | --- | --- |
| [Google Workspace Gmail](https://workspace.google.com/products/gmail/) | 托管邮箱、协作、统一管理与大厂投递网络 | 低运维、账号生命周期、垃圾过滤、移动/客户端兼容和可用性 |
| [Microsoft Exchange Online](https://www.microsoft.com/en-us/microsoft-365/exchange/exchange-online) | 企业邮箱、组/日历、合规与 Microsoft 生态 | 群件、委派、企业身份、审计、保留和混合部署 |
| [Fastmail for business](https://www.fastmail.com/business/) | 自有域、alias、规则和轻量托管体验 | 小团队管理、alias/规则、迁移与用户体验 |
| [Cloudflare Email Routing](https://developers.cloudflare.com/email-service/get-started/route-emails/) | 无邮箱存储的域级入站转发 | 透明 `From`、SRS、catch-all、已验证目标与无自然 alias 回复的边界 |

这组基准同时说明“完整邮箱”和“只转发”必须拆开：Google/Exchange/Fastmail 与 Mailu/mailcow 同类，Cloudflare Routing 与 DMS forward-only/专用 alias 平台同类。

## 3. 核心候选：转发协议能力对比

| 项目 | 许可证 | 外部 alias / 多目标 | Catch-all | 用户规则 / 保留本地副本 | 官方 SRS 路径 | ARC 对外 seal | 公网转发判断 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **Mailu** | [MIT](https://github.com/Mailu/Mailu/blob/master/LICENSE.md) | ✅ CLI/API/UI；多目标 | ✅ `wildcard` / SQL-like alias | ✅ auto-forward、`forward_keep`、ManageSieve | ✅ [内建；release notes 明确 Support for SRS](https://mailu.io/master/releases.html)；具体配置须 PoC | — | **A-，首选完整套件 PoC** |
| **docker-mailserver** | [MIT](https://github.com/docker-mailserver/docker-mailserver/blob/master/LICENSE) | ✅ 外部目标；多目标/嵌套官方称非正式支持 | ◐ 官方支持，但 wildcard 优先级会遮蔽真实账号 | ✅ Sieve `redirect` / `:copy`、ManageSieve | ✅ 内置但默认 `ENABLE_SRS=0`；设为 `1` 后 secret/域/排除项可配 | ◐ Rspamd ARC 可配置；官方示例只签 local/authenticated，公网入站后转发需另配 `sign_inbound`、域/密钥选择并实测 | **A，IaC/转发优先** |
| **iRedMail** | [GPL-3.0](https://github.com/iredmail/iRedMail/blob/master/LICENSE) | ✅ SQL/LDAP；Pro UI 更方便 | ✅ SQL/LDAP；Pro UI 更方便 | ✅ forwarding + 自身目标保留副本；ManageSieve | ✅ iRedAPD；自 3.3 默认关闭，有已知副作用 | — | **A-/B+，传统栈备选** |
| **mailcow** | [GPL-3.0](https://github.com/mailcow/mailcow-dockerized/blob/master/LICENSE) | ✅ 功能上可转；UI 建议外部目标走 Sieve | ✅ `@domain.tld` | ✅ SOGo/Dovecot Sieve；redirect 上限可配 | ❌ 无受支持内置路径；SRS issue 仍 open | — | **C（公网转发）/ A（群件）** |
| **Stalwart Community** | [AGPL-3.0](https://github.com/stalwartlabs/stalwart#license) | ◐ mailing list/Sieve 可转外部 | ✅ `catchAllAddress` | ✅ Sieve `redirect` / `redirect :copy`、ManageSieve | ❌ 上游明确不会实现 | ❌ 2026 文档明确只 verify、不 seal，verify 默认关闭 | **C（公网转发）/ A-（现代邮箱）** |
| **Modoboa** | [ISC](https://github.com/modoboa/modoboa/blob/master/LICENSE) | ✅ 当前源码允许外部域、多目标/策略 | ◐ 源码保留 catch-all 语义，锁版后再验 UI/API | ✅ 每用户 Sieve | — 未找到官方受支持集成 | — 未确认 | **C，需自维护 Postfix 扩展** |

这里的 `A` 不是“保证到达收件箱”，而是“具备进入投递 PoC 的必要产品能力”。对 Mailu、DMS、iRedMail 都必须验证 SRS 仅作用于需要转发的流量、SRS 域有 SPF/MX、旧 secret 仍可解码在途退信、原始 DKIM 未被页脚/主题重写破坏，以及严格 DMARC 发件域到各目标的结果。

这里评的是转发协议就绪度，不是“只为转发部署整个 ANAS Module”的成本。配套[邮件转发与别名服务报告](./self-hosted-open-source-email-forwarding-research.md)因 Mailu 包含 IMAP/Webmail 等额外状态面，把它的 forward-only Module 适配降为 `C`；这与本表的协议能力 `A-` 使用不同分母。

## 4. 部署与运维门槛

| 项目 | 形态与主要组件 | 官方资源/宿主信号 | 管理体验 | 运维门槛 |
| --- | --- | --- | --- | --- |
| **Mailu** | 多 Docker image；Postfix、Dovecot、Rspamd、管理 UI、可选 Webmail/ClamAV | [默认 SQLite，官方称足够各种 Mailu 部署](https://mailu.io/master/database.html)；生产应固定 stable 文档/镜像 | 完整 UI、CLI、REST、配置导入导出 | 中；Compose 文件与 `.env` 随版本升级，Webmail DB 另有迁移边界 |
| **docker-mailserver** | 单主容器组织 Postfix、Dovecot、Rspamd/Amavis 等可选服务 | [小型建议约 2 GB，关闭 ClamAV 等可更低](https://docker-mailserver.github.io/docker-mailserver/latest/faq/) | CLI/文本配置，无内置 UI/Webmail | 中；少容器但要求理解 Postfix map、Dovecot、Sieve、DNS 和日志 |
| **iRedMail** | 新鲜 OS 上安装 Postfix、Dovecot、SQL/LDAP、Nginx、Roundcube/SOGo、iRedAPD 等 | [低流量反垃圾/病毒场景建议至少约 4 GB](https://docs.iredmail.org/install.iredmail.on.debian.ubuntu.html)；要求 25 端口 | 社区 UI 基础；高级转发管理偏 Pro/手工 DB | 高；占用宿主、发行版升级和组件升级需专门 runbook |
| **mailcow** | 大型 Compose 栈：Postfix、Dovecot、Rspamd、SOGo、MariaDB、Redis、ClamAV 等 | [最低约 6 GiB RAM + 1 GiB swap、20 GiB 磁盘](https://docs.mailcow.email/getstarted/prerequisite-system/)，小团队更宜 8 GiB | 六者中最完整、成熟的群件管理体验之一 | 中高；组件多、资源高、升级前须完整卷/DB/密钥备份 |
| **Stalwart** | 单一现代服务器覆盖 SMTP/IMAP/JMAP/ManageSieve/Web 管理；可外接多种存储 | [空闲约 100 MB，小型 5–10 用户约 1 GB](https://stalw.art/docs/install/requirements/) | 现代 Web 管理、CLI/API | 低至中；轻量但产品演进快，锁定 schema/版本并演练导入导出 |
| **Modoboa** | Django/Vue 管理层 + Postfix/Dovecot/DB/Nginx/Rspamd 或 Amavis/ClamAV/Radicale | [installer 要求至少 2 GB、fresh FQDN，且 `/tmp` 不能 `noexec`](https://github.com/modoboa/modoboa-installer) | 管理 UI、Webmail、日历、地址簿 | 高；原生宿主耦合，installer/upgrade/backup/restore 的成熟度是主要风险 |

### 4.1 Mailu：完整套件与转发的平衡点

[Mailu 项目](https://github.com/Mailu/Mailu)把 aliases、domain aliases、custom routing、auto-forward、Webmail、管理 UI、反垃圾、SPF/DKIM/DMARC 列为主功能。[CLI 文档](https://mailu.io/master/cli.html)给出 alias 到多个完整地址的例子，并公开 `forward_enabled`、`forward_destination`、`forward_keep` 和 alias `wildcard` 字段；这同时覆盖纯 alias、用户自动转发、保留副本与 catch-all。2024.06 又增加外部 ManageSieve 4190 管理路径。

Mailu 的优势是邮箱与转发共用一个可管理对象模型，配置还能从 YAML/JSON 导出导入。风险有两点：

- [setup 向导](https://setup.mailu.io/master/)明确区分 stable 与 master，生产不能跟随 master；升级还要同步生成器产生的 Compose 和环境变量。
- 内建 SRS 支持在官方 release notes 中可确认，但当前主文档没有像 DMS 那样把 SRS secret、适用 sender classes、反向解码和轮换集中说明。因此 PoC 要直接检查实际 MTA/SRS 配置和真实 `Return-Path`，不能只看 UI 成功保存。

### 4.2 docker-mailserver：最清楚的转发配置即代码路径

[DMS alias 文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/overview/)明确允许把本地域地址转发到 Gmail 等外部地址；[file provisioner](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/provisioner/file/)记录了 `@example.com` catch-all、外部目标和匹配顺序限制。catch-all 的 wildcard alias 优先于真实账号，因此必须为真实账号补显式 alias，并把更具体项放在 wildcard 前；多收件人和 alias-to-alias 虽能工作，却被官方标为不正式支持，复杂扇出不宜依赖它。

[环境变量文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/environment/)明确写出“DMS 作为 forwarder 时需要 SRS”，并提供：

- `ENABLE_SRS=1`，默认值为 `0`，必须显式启用；
- `SRS_SENDER_CLASSES`；生产配置应只重写 envelope sender，不建议重写可见 header sender；
- `SRS_EXCLUDE_DOMAINS`、`SRS_DOMAINNAME`；
- base64 `SRS_SECRET` 多 key 轮换，第一把签名、其余验证；集群节点必须共享。

[Sieve 文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-sieve/)还支持 `redirect` 和 `redirect :copy`；官方甚至有[纯转发服务器用例](https://docker-mailserver.github.io/docker-mailserver/latest/examples/use-cases/forward-only-mailserver-with-ldap-authentication/)。其不足主要是产品层而非 MTA：无自带 UI、域/账号/别名 API 边界薄，ANAS 若封装它，需要自己定义配置 schema、secret 轮换、reload 和审计契约。

### 4.3 iRedMail：能力成熟，但社区版管理偏底层

[iRedMail 组件清单](https://docs.iredmail.org/used.components.html)包含 Postfix、Dovecot/ManageSieve、SQL 或 LDAP、Roundcube/SOGo、反垃圾/病毒组件，以及带 SRS 的 iRedAPD。官方分别给出：

- [SQL alias 到多个外部地址](https://docs.iredmail.org/sql.create.mail.alias.html)；
- [per-domain catch-all](https://docs.iredmail.org/sql.create.catch-all.html)；
- [用户转发、多目标与保留本地副本](https://docs.iredmail.org/sql.user.mail.forwarding.html)；
- LDAP 对应的 `mailForwardingAddress` 路径。

这些能力在社区栈可用，但文档多以 SQL/LDAP 命令为入口，iRedAdmin-Pro 才提供一致的图形管理。Sieve 也有套件组合限制：同时安装 Roundcube 与 SOGo 时，上游因二者生成规则语法不兼容而建议只由 Roundcube 管理 Sieve；选择 SOGo-only 才走 SOGo forwarding。

[SRS 文档](https://docs.iredmail.org/srs.html)说明 iRedAPD 2.6+ 支持 7778 正向重写、7779 反向解码，SRS 域必须可解析并宜有 MX；但自 iRedAPD 3.3 起默认关闭。管理员需生成 `srs_secrets`，再手工把 Postfix 的 sender/recipient canonical maps 接到这两个端口。官方还列出重写会发生得过早、外部 sender 即使最终未转发也可能被改写，且会影响 SpamAssassin SPF、基于 `Return-Path` 的 Sieve、日志与 Amavis 统计。因此它是“有官方 SRS”但不是“一键无副作用”。

### 4.4 mailcow：能转发，不等于适合做公网转发器

[mailcow OpenAPI](https://github.com/mailcow/mailcow-dockerized/blob/master/data/web/api/openapi.yaml)的 alias 对象支持逗号分隔 `goto`，并规定 `@domain.tld` 为 catch-all。[当前 UI 文案](https://github.com/mailcow/mailcow-dockerized/blob/master/data/web/lang/lang.en-gb.json)则明确提示外部邮箱应使用 Filters/SOGo Forwarder；底层 Dovecot Sieve 也支持 redirect。这足以证明功能性转发，不能证明认证链完整。

决定性限制是 [SRS issue #2418](https://github.com/mailcow/mailcow-dockerized/issues/2418)：用户报告外部转发 SPF 失败并请求集成 PostSRSd，该 issue 截至本报告仍 open。自行在 Compose 中加 PostSRSd 当然可行，但会改变升级、配置生成、secret、退信反向 map 和支持边界；在上游给出正式集成前，报告不把这种本地补丁算作 mailcow 原生能力。

如果主目标是本地邮箱、SOGo 日历/联系人、移动端与友好的域管理，mailcow 仍是强候选；只是要把外部转发设为非关键功能，或将隐私 alias 子域交给另一套已验证的转发器。

### 4.5 Stalwart：轻量现代，但当前认证链不支持通用转发承诺

[Stalwart Community](https://www.stalw.art/compare/)以 AGPL-3.0 提供 SMTP、IMAP、JMAP、Web 管理、反垃圾和多种存储；[Docker 安装](https://stalw.art/docs/install/platform/docker/)是单镜像路线，资源远低于传统多组件套件。[Domain 文档](https://stalw.art/docs/domains/)提供 `catchAllAddress` 和用于 split delivery 的 `allowRelaying`；mailing list 可包含外部地址，ManageSieve/Sieve 可以 `redirect`，`redirect :copy` 保留本地副本。

但外部投递层面，上游在 [SRS discussion #1002](https://github.com/stalwartlabs/stalwart/discussions/1002)明确表示不会添加 SRS，曾建议改用 ARC。[2026-08-18 ARC 文档](https://stalw.art/docs/mta/authentication/arc/)又明确表示 Stalwart 不 seal，只保留对已有 ARC 链的 inbound verification，而且 `arcVerify` 默认是 `disable`；[edition 对照](https://www.stalw.art/compare/)也写为 “ARC, inbound verification only”。因此：

- generic envelope rewrite 不是带签名/时效/反向解码的 SRS，不能混为一谈；
- 只 verify ARC 对自己转发出去的邮件没有新增可信链；
- Enterprise 的 masked email 是隐私 alias 产品能力，但不能据此推导 Community 或 Enterprise 已解决公网转发认证。

Stalwart 应作为现代完整邮箱单独 PoC，而不是 Cloudflare Email Routing 替代品。未来若上游重新提供明确的 SRS 或受支持的认证链，再提高转发评级。

### 4.6 Modoboa：可管理的传统栈，恢复成熟度需先过关

[Modoboa 主仓库](https://github.com/modoboa/modoboa)提供域/账号管理、Webmail、日历、地址簿、每用户 Sieve 和邮件统计。[当前 alias model](https://github.com/modoboa/modoboa/blob/master/modoboa/admin/models/alias.py)包含 `alias_can_target_any_domain` 策略、允许/阻止域、多目标和 catch-all 相关校验，证明可按策略指向外部域；但本轮没有找到与当前稳定版 UI/API 完全对应的 catch-all 管理文档，所以表中保守标 `◐`。

[官方 installer](https://github.com/modoboa/modoboa-installer)会铺设 DB、Nginx/uWSGI、Postfix、Dovecot、Rspamd 或 Amavis/SpamAssassin/ClamAV、OpenDKIM、Radicale，是典型“占用整台 fresh host”的方案。installer 自身仍为 beta，upgrade、backup、restore 又标为 experimental；在这三项完成故障注入和整机恢复前，不适合纳入 ANAS 的默认邮件 Module。本轮未确认官方 SRS 或 ARC sealing 路径；若要公网转发，只能先把 PostSRSd 等自维护集成当作待验证扩展，并承担和 mailcow 本地补丁相同的升级风险。

## 5. 转发认证：SPF、SRS、DKIM、DMARC 与 ARC

### 5.1 一封转发邮件发生了什么

```text
original.example 发件服务器
  MAIL FROM: bounce@original.example
  From: alice@original.example
  DKIM d=original.example
             │
             ▼
alias.example 转发器（从自己的 IP 再投递）
  无 SRS：MAIL FROM 仍是 original.example，目标按转发器 IP 查原域 SPF，通常失败
  有 SRS：MAIL FROM 变为 SRS0=...@srs.alias.example，目标改查转发器域 SPF
  From: 仍是 alice@original.example
  原始 DKIM：只有正文/已签 header 未被修改时才可能继续通过并与 From 对齐
             │
             ▼
最终收件系统：分别判断 SPF、DKIM，再按 RFC5322.From 做 DMARC alignment 和本地信誉策略
```

依据 [SPF RFC 7208](https://www.rfc-editor.org/rfc/rfc7208)，SPF 评估的是当前 SMTP 客户端 IP 是否被 envelope sender 域授权。普通转发改变了客户端 IP，却保留原 envelope sender，所以常见 SPF fail。像 [PostSRSd](https://github.com/roehling/postsrsd)这样的 SRS 实现把 envelope sender 编码到转发器域，并在退信回到 SRS 地址时还原原地址。

### 5.2 必须避免的三种误写

1. **“开 SRS 后原始邮件 DMARC 就会过”是错误的。** 现行 [DMARC RFC 9989](https://www.rfc-editor.org/rfc/rfc9989)要求通过的 SPF 或 DKIM 身份与可见 `From:` 域对齐。SRS 后 SPF 通过的是 `srs.alias.example`，可见 `From:` 仍是 `original.example`，通常不对齐；原始、对齐 DKIM 保留下来才是常见 DMARC 生路。
2. **“转发器再用自己的域 DKIM 签一次就行”也不完整。** 新签名的 `d=alias.example` 与 `From: original.example` 不对齐，不能自动让原始身份 DMARC pass。把可见 `From:` 改成转发器域虽可改变对齐结果，却损害发件人身份、回复/签名语义和用户体验，不应作为透明转发默认策略。
3. **“有 ARC 就保证接收”不成立。** [ARC RFC 8617](https://www.rfc-editor.org/rfc/rfc8617)传递中间节点观察到的认证结果，最终接收方仍基于对 ARC sealer 的信任和本地策略决定是否覆盖 DMARC。只验证、不 seal 更不能为当前转发新增链路。

### 5.3 SRS 生产验收项

- 使用专用 `srs.alias.example`；发布 A/AAAA 或 MX、SPF，确保退信能回到反向解码服务。
- secret 不进镜像/仓库；第一把用于签名，旧 key 在最大队列与退信窗口内继续验证，再安全退役。
- 正常本地投递和已认证用户出站不应被无差别 SRS 重写；验证 null reverse-path (`MAIL FROM: <>`) 和 DSN。
- 抽查 Gmail、Outlook、Yahoo/Proton 等目标的 `Return-Path`、`Authentication-Results`、DKIM 与 DMARC，而不是只看 SMTP 250。
- 验证正文、主题、MIME、邮件列表页脚和反垃圾器是否破坏原始 DKIM；对严格 `p=reject` 发件域建独立样本。
- 构造转发环、alias 链、多个目标、临时/永久失败和超过 hop/redirect 上限的测试；循环必须在队列膨胀前停止。

## 6. 配套部署边界：邮箱域与转发域怎么组合

### 6.1 推荐拓扑

```text
example.com MX ───────────────► Mailu / DMS / iRedMail 等完整邮箱服务
                                   └─ user@example.com 本地邮箱

alias.example.com MX ─────────► 专用转发服务或已验证 SRS 的邮件服务
                                   └─ random@alias.example.com
                                      → user@example.com
```

也可以用完全独立的第二域。独立 alias 域的收益是：MX 所有权清楚、SRS/DKIM/SPF 可独立发布、故障和滥用面隔离、迁移时不碰主邮箱域；代价是多一组 DNS、证书、监控和备份对象。

### 6.2 不可行的“双 MX 按收件人分流”

[SMTP RFC 5321](https://www.rfc-editor.org/rfc/rfc5321)的 MX preference 用于选择适当下一跳和故障转移，不是收件人路由规则。以下配置不会表达“真实用户去 Mailu、随机 alias 去转发器”：

```dns
example.com. MX 10 mailu.example.net.
example.com. MX 20 forwarder.example.net.
```

正常发件方优先尝试 priority 10；只有它不可达/临时失败时才尝试 20。若第一台对未知收件人返回永久 `550`，发送方也没有义务把第二 MX 当作按地址补查服务。两台又若各自只认识部分收件人，会产生误退信、后备 MX 垃圾接收和循环风险。

同域确需 split delivery 时，必须让**一个前置 MX 接收并拥有完整 recipient directory/routing decision**，再按 transport map、LDAP/SQL 查询或受控 relay 把部分地址交给后端；这是一套系统内的明确路由，不是两个独立产品各自声明 MX。还要验证不存在地址时在 SMTP RCPT 阶段拒绝，避免 accept-then-bounce 形成 backscatter。

### 6.3 循环与生命周期

- 禁止 alias 直接或间接回到自身，包括跨两个域、group、多目标和用户 Sieve 组合后的环。
- 创建/更新规则时做图遍历；运行时另设 `Received`/hop、Sieve redirect 和队列重试上限。
- 删除主邮箱前先查询所有 alias 反向引用；外部目标变更要审计并支持冻结/恢复。
- catch-all 与随机 alias 不等价：catch-all 会接收字典攻击和拼写错误，隐私 alias 应默认显式创建、可停用、可追踪来源。

## 7. 反垃圾、出站信誉与安全风险

### 7.1 自部署邮件的基础门槛

- 公网需直达 TCP 25；submission 587/465 和 IMAP 993 面向用户，不能把 HTTP 反代配置当作 SMTP 入口。
- 主机名正向 A/AAAA 与 PTR 应一致，HELO 合理；PTR 通常只能由 IP 提供商配置。[Mailu DNS 文档](https://mailu.io/master/dns.html)明确提示错误 rDNS 会导致邮件被当作垃圾或拒绝。
- 发布 SPF、DKIM、DMARC，配置 TLS、MTA-STS/TLS-RPT（按产品能力），持续读取 DMARC aggregate report 和退信。
- 禁止 open relay；认证账户要有强密码/2FA 或 app password、发送速率/并发/每日配额、异常告警和快速封禁。
- 队列、磁盘、证书、DNSBL/信誉、SMTP 4xx/5xx、DKIM key 和反垃圾更新都需要监控；“容器在运行”不等于邮件系统健康。

### 7.2 转发器特有风险

转发器替攻击者把邮件从自己的 IP 再投递一次。若先接受所有 catch-all，再无条件送到 Gmail/Outlook，目标会把垃圾率、用户投诉和无效目标都归因到转发器。最低策略应为：

1. 在入站阶段做连接/IP/协议/病毒/内容评分，但避免会破坏 DKIM 的无必要正文改写；
2. 明确拒绝不存在 alias，少用全域 catch-all；
3. 每 alias/目标/租户限速，限制扇出和最大消息大小；
4. 临时失败排队重试，永久失败停止；不要对伪造 sender 生成 backscatter；
5. 记录原始 envelope、SRS 结果、目标响应和 queue id，但对地址与正文日志设最小化/保留期；
6. 建投诉、退订和被攻陷 alias 的一键停用流程。

### 7.3 直接出站还是 smarthost

| 情况 | 建议 |
| --- | --- |
| 有干净静态 IP、PTR 控制、25 端口、愿意长期养信誉 | 可直接出站，但先低量 warm-up，监控主要大厂响应与 DNSBL |
| 家宽/CGNAT、云厂商封 25、PTR 不可控、IP 历史差 | 使用可信 SMTP relay/smarthost；确认服务条款允许普通邮件和转发流量 |
| 只收信、偶尔系统通知 | 入站可自建；出站与通知走独立 relay，隔离用户邮件和系统告警信誉 |
| 高量营销/事务邮件 | 不与个人/团队邮箱共用 IP/域/队列；使用专门投递平台并做退订/投诉治理 |

relay 能绕过本机 IP 信誉冷启动，但不会自动修复 DMARC 对齐、垃圾内容、被盗账户、无效名单或 SRS 退信路径；而且会引入第三方可用性、成本与隐私边界。

## 8. ANAS 选型与 PoC 建议

### 8.1 推荐顺序

| 优先级 | 候选 | 适合场景 | 暂缓/淘汰条件 |
| --- | --- | --- | --- |
| 1 | **Mailu** | 需要完整邮箱、管理 UI/Webmail，同时把外部转发列为一等能力 | 固定 stable 版本的 SRS/退信/DMARC 黑盒不通过；备份恢复不完整 |
| 2 | **docker-mailserver** | 团队接受文本配置/CLI，希望 alias/SRS/secret 全部 IaC | 必须给非技术管理员 UI；配置 schema 与 reload 无法稳定封装 |
| 3 | **iRedMail** | 有传统 Postfix/Dovecot/SQL 运维经验，接受 fresh host/VM | 不接受手工 DB 或 Pro；iRedAPD SRS 副作用无法限定 |
| 4 | **mailcow** | 群件/管理体验优先，外部转发非关键或另有专用转发器 | 要求 mailcow 单独承担严格公网转发且不接受自维护补丁 |
| 5 | **Stalwart** | 优先现代协议、低资源和单体架构，主要做本地邮箱 | 需求包含 Cloudflare Email Routing 式公网转发 |
| 6 | **Modoboa** | 已有 Modoboa/Python/Django 经验或存量迁移 | installer、upgrade、restore 故障演练不过；要求原生 SRS |

### 8.2 两周最小 PoC

第一组只需并行部署 Mailu 与 DMS，使用测试域和专用公网 IP/relay；iRedMail 在前两者出现硬缺口时再进入。验收至少包括：

1. **DNS/协议**：A/AAAA、PTR、MX、SPF、DKIM、DMARC、TLS，25/587/993；IPv4/IPv6 分别验证。
2. **邮箱**：创建/禁用/删除域和用户，IMAP/SMTP submission，配额，Webmail（Mailu），大附件和多语言地址显示。
3. **转发**：本地 alias、外部 alias、多目标、保留副本、catch-all、Sieve、alias 子域；每项都检查 queue/log/最终 header。
4. **认证矩阵**：来自 SPF-only、DKIM-only、严格 DMARC `p=reject`、邮件列表修改正文的样本，分别送到 Gmail、Outlook 和另一个自控严格收件服务器。
5. **SRS**：forward/reverse、null sender、secret 轮换、旧退信、独立 SRS 域 DNS、临时/永久失败、经 smarthost 时的 envelope 是否保留。
6. **滥用**：open-relay 测试、密码爆破/限速、垃圾/病毒、无效 alias、catch-all 字典攻击、超大邮件、循环和扇出限制。
7. **恢复**：备份后删除容器/卷，在新主机恢复域、用户、邮件、Sieve、alias、DKIM/SRS secrets、证书与队列配置；用实际邮件证明旧签名/退信仍工作。
8. **升级**：从固定前一稳定 patch 升到目标 patch，检查 Compose/env diff、数据库迁移、规则与回滚说明；不使用浮动 `latest`。

Go/No-Go 硬门槛：没有 open relay；严格 DMARC 样本结果可解释；SRS 正反向与轮换通过；循环可终止；备份能在空白主机恢复；管理员能从 queue id 追踪一封邮件的接收、重写、投递或失败全过程。

## 9. 邻近、补充与排除项

| 项目 | 许可证/形态 | 去向与理由 |
| --- | --- | --- |
| [Mail-in-a-Box](https://github.com/mail-in-a-box/mailinabox) | CC0-1.0；fresh Ubuntu 22.04 一键 appliance | **邻近 C**。有用户、alias、Webmail、DNS、备份和监控，适合个人整机邮件盒；官方目标明确不是 power-user 可定制系统，宿主占用和 ANAS Compose Module 边界不合，且本轮未深验 SRS/公网转发链路。 |
| [maddy](https://github.com/foxcpp/maddy) | GPL-3.0；可组合的全栈单 daemon | **邻近/实验**。SMTP/MTA/安全协议很有吸引力，但官方仍把 IMAP storage 标为 beta，也没有完整管理/群件面；可在“轻量 MTA/转发器”专项复核，不替代本轮默认套件。 |
| [mox](https://github.com/mjl-/mox) | MIT；现代单体 SMTP/IMAP/Webmail | **下一轮重点复核**。目标是低维护、约 512 MB 可起步，内置 SPF/DKIM/DMARC 与管理面；项目也明确自己年轻。本轮未完成 alias/catch-all/SRS 逐项核验，不在核心评分中贸然下结论。 |
| [Simple NixOS Mailserver](https://gitlab.com/simple-nixos-mailserver/nixos-mailserver) | GPL-3.0-or-later；声明式 NixOS module | **NixOS 专用邻近项**。已有 external forwarding option，但 roadmap 仍列“Improve the Forwarding Experience / ARC signing”；适合全栈 NixOS 团队，不是 Docker/ANAS 默认路径。 |
| [WildDuck](https://github.com/zone-eu/wildduck) | EUPL-1.2；MongoDB-backed IMAP/POP3 mail store | **组件，不是同类整套 appliance**。属于可扩展 Zone Mail Suite 的存储/协议核心，完整收发、过滤、管理需要额外拓扑，超出 1–20 用户默认 Module。 |
| [Poste.io](https://poste.io/) | Free/PRO 镜像与 EULA；官方称 built with open source，但未提供对应完整 OSI 源码仓库 | **排除严格开源集合**。即使产品有原生 forwarding/SRS，许可证与可重建源码边界不符合本报告 must-be；可作商业自部署功能基准。 |

这里没有把“未进入核心表”写成“项目差”。mox 尤其值得在下一轮以同样的 alias/SRS/DMARC 方法复核；Mail-in-a-Box 则更适合把整台 VM 交给它，而不是嵌进已有 NAS 服务编排。

## 10. 已确认、未确认与复核周期

### 10.1 已确认的关键事实

- Mailu 上游文档同时出现 external destinations、用户 forward 字段、wildcard alias、ManageSieve 和内建 “Support for SRS”。
- DMS 官方文档明确 external alias/catch-all、forward-only、Sieve redirect 与 `ENABLE_SRS=1`，并说明默认关闭和 secret 轮换。
- iRedMail 官方文档明确 external alias、catch-all、user forwarding 与 iRedAPD SRS；SRS 默认关闭且有已知副作用。
- mailcow 能功能性转发，但其 PostSRSd 集成 issue 仍 open。
- Stalwart 能功能性转发，但官方拒绝 SRS；2026-08-18 文档明确 ARC 不 seal、只 verify，且 verify 默认关闭。
- Modoboa 源码允许外部 alias target，但 SRS/ARC 未确认；installer/upgrade/backup/restore 成熟度标记限制生产信心。

### 10.2 尚未确认、必须由 PoC 或上游答复的问题

- Mailu 当前 stable patch 中 SRS 的默认启用状态、具体 secret 轮换接口、排除本地/非转发流量的精确规则，以及经 relayhost 后的反向退信行为。
- mailcow 是否会在本报告后正式合并 SRS；在此之前不把社区 override 当官方能力。
- Modoboa 当前锁定 release 的 catch-all UI/API 完整流程，以及团队是否愿意支持 PostSRSd 作为 installer 组件。
- 各候选对原始 DKIM 的实际保留行为会受反垃圾器、Sieve 和管理员自定义规则影响，不能从功能表静态推断。
- Gmail/Outlook 等接收方策略持续变化，任何“今天投递成功”都不是永久 SLA。

建议每 90 天复核核心候选 release notes、许可证、SRS/ARC 状态和 stable 文档；每次升级前重新执行认证矩阵与空白主机恢复测试。

## 11. 社区自部署与付费边界

完整邮件服务没有纳入官方免费托管套餐排名；本表只区分社区自部署代码、商业功能/支持和授权依赖。演示站不视为可承载生产邮件的免费托管。

| 项目 | 开源自部署社区版 | 官方付费/企业边界 | 自部署限制与选型影响 |
| --- | --- | --- | --- |
| Mailu | MIT 自有代码/配置，组成组件均为 FOSS；无许可证 Key | 未发现同名功能锁定企业版；社区捐助/第三方服务不改变代码 entitlement | alias、API、Webmail、SRS 不因付费解锁；支持责任主要由社区和运维承担 |
| docker-mailserver | MIT；无许可证 Key、无同名企业功能层 | 没有官方 SaaS/企业版；可购买第三方顾问但不形成上游 entitlement | 全部能力以配置文件/CLI 暴露，缺 UI 是产品形态而非付费锁定 |
| iRedMail | [GPL-3.0 installer](https://github.com/iredmail/iRedMail)与基础社区组件可自部署 | 官方另有 [Enterprise Edition](https://www.iredmail.org/ee.html)、iRedAdmin-Pro 与付费支持；当前上游还推荐新部署评估 EE | 外部 alias/SRS 可在社区栈手工完成，但一致的高级 Web 管理、升级和支持边界不能全部归入 GPL 社区版 |
| mailcow | GPL-3.0 Compose 栈；核心群件/UI 不需许可证 Key | 官方提供商业支持服务，未发现以闭源企业包锁定 alias/SOGo/核心 API | 付费主要购买支持，不会补上当前内建 SRS 缺口；本地补丁仍由部署方维护 |
| Stalwart | Community 为 AGPL-3.0，可自部署核心邮件/协作协议 | [Enterprise 对照](https://www.stalw.art/compare/)使用 SELv2/商业授权并增加部分规模、运维或产品能力 | masked email/企业功能不能反推 Community 具备；无 SRS、ARC verify-only 是当前产品技术边界，不是简单付费开关 |
| Modoboa | 主项目 ISC；installer 和组成组件可自部署，无许可证 Key | 未找到统一企业 entitlement 矩阵；可获得项目相关商业服务/支持 | installer 的 beta 与 backup/restore experimental 状态不会因“代码免费”自动消失，需自行做恢复验收 |

代码许可证、官方托管配额、企业闭源功能和付费支持是四件不同的事。尤其不能把 iRedAdmin-Pro 的 UI 能力写成社区版原生，也不能把 Stalwart Enterprise 的 masked email 当成 Community 转发能力。

## 12. 动态仓库与发布快照

仓库字段由 [GitHub REST repository API](https://docs.github.com/en/rest/repos/repos#get-a-repository)于 2026-08-19 采集；release 版本回到各项目官方 release 页核验。Star 只作社区规模信号，不参与推荐评分。“90/365 天活动”表示默认分支 `pushed_at` 落在对应窗口，并不是精确提交数或质量评分。

| 项目/仓库 | 创建日 | 最后 push（UTC） | Star / Fork | 快照日可见最新稳定发行 | 90/365 天活动 | 维护、升级与安全信号 |
| --- | --- | --- | --- | --- | --- | --- |
| Mailu/Mailu | 2016-01-10 | 2026-08-17 | 7,457 / 994 | [2024.06.58，2026-08-12](https://github.com/Mailu/Mailu/releases) | ✅ / ✅ | 组织/多贡献者维护；stable 分支、升级文档和 SECURITY policy |
| docker-mailserver/docker-mailserver | 2015-03-28 | 2026-08-03 | 18,752 / 2,042 | [v15.1.0，2025-08-12](https://github.com/docker-mailserver/docker-mailserver/releases) | ✅ / ✅ | 组织/多贡献者维护；版本化文档、升级说明和安全政策 |
| iredmail/iRedMail | 2019-12-09（现仓库） | 2026-08-09 | 1,838 / 258 | [1.8.4，2026-07-22](https://docs.iredmail.org/iredmail.releases.html) | ✅ / ✅ | 厂商主导；支持论坛与逐版本升级文档，不能跳版本 |
| mailcow/mailcow-dockerized | 2016-12-09 | 2026-08-18 | 13,291 / 1,780 | [2026-07，2026-07-13](https://github.com/mailcow/mailcow-dockerized/releases) | ✅ / ✅ | 公司+社区维护、活跃 PR/release；有安全与备份/升级文档 |
| stalwartlabs/stalwart | 2023-03-06 | 2026-08-18 | 14,251 / 888 | [v0.16.15，2026-07-27](https://github.com/stalwartlabs/stalwart/releases) | ✅ / ✅ | 公司主导、高频 release；有安全政策，但 pre-1.0 跨版本迁移风险较高 |
| modoboa/modoboa | 2013-04-16 | 2026-08-14 | 3,530 / 479 | [2.9.2，2026-07-17](https://github.com/modoboa/modoboa/releases) | ✅ / ✅ | 核心团队持续 release；主应用成熟度高于仍为 beta/experimental 的 installer 运维路径 |

开放 issue 数没有用于横向排序：iRedMail 把大量支持放在论坛，mailcow 等项目则在 GitHub 保留较多需求，数字不可直接比较。采用前仍应抽查目标 release 最近 90/365 天的真实人工提交、响应时间和安全公告；自动依赖更新不能单独算“多人活跃”。

## 13. ANAS 集成契约草图

| 候选 | 镜像/架构与入口 | 身份与管理 API | 数据与 Secret 归属 | 健康、升级与回滚边界 |
| --- | --- | --- | --- | --- |
| Mailu | 官方多镜像，amd64/arm64/armv7；HTTP 可接 Traefik，SMTP/IMAP 走独立 TCP | Web 可用 header auth 接外部 IdP；邮件客户端仍需 token/password；管理 REST token/CLI | Maildir 属 `userdata/`；管理/Webmail DB、Sieve、queue 属 `data/`；DKIM/SRS/TLS/API secrets 必须备份 | 按 pinned patch 生成 Compose/env；监控 SMTP/IMAP/queue/Rspamd；数据库和 Webmail migration 后不能假定任意降级 |
| docker-mailserver | 官方单主容器；amd64/arm64 manifest 在目标 tag 再验；无内置 Web | LDAP/OAuth2 可用于邮件 SASL，但没有管理 OIDC/UI；setup CLI/文件由 ANAS 管理 | 邮箱属 `userdata/`；config、mail-state/queue、Sieve、日志属 `data/`；SRS/DKIM/TLS secrets 独立轮换 | 容器健康不等于投递健康；需 queue/header 探针；配置 reload、版本锁与回退文件易 IaC 化 |
| iRedMail | fresh Linux/BSD 主机安装，架构取决于受支持发行版；不适合普通 Compose 子模块 | SQL/LDAP 账号；本轮未确认通用 OIDC；社区高级管理常需 DB/LDAP 或 Pro | 邮件、SQL/LDAP、配置、queue、DKIM/SRS/TLS 均跨宿主目录和服务 | 官方逐版本升级且不可跳版；应把整机 VM/主机当恢复单元，不与 ANAS 其他服务混装 |
| mailcow | 官方 Compose，x86_64/ARM64；明确不支持 Synology/QNAP/LXC | 内置账号/2FA 和 API；本轮不把其登录等同 ANAS 通用 OIDC | vmail、MariaDB、Redis、queue、SOGo、密钥和配置均需一致快照 | 使用官方 backup/upgrade 流程；6+ GiB 与网络/iptables 约束需前置检查；本地 SRS 补丁会破坏支持边界 |
| Stalwart | 官方多架构单容器/二进制；Web 管理与邮件协议端口分离 | 可接外部 OIDC/LDAP/SQL；管理 API/Web UI；Webmail 仍需外部产品 | store/blob/index/config 与证书/密钥属 `data/`；邮件正文按存储布局归 `userdata/` | 资源低但版本演进快；锁 schema、导出/恢复并先演练 0.15→0.16 类迁移，不用浮动 tag |
| Modoboa | fresh host installer，不是官方 Compose-first；架构随发行版 | 应用本地/目录账号与 API；本轮未确认通用 OIDC 对接契约 | Django DB、Dovecot mail、Sieve、Postfix queue、Rspamd/密钥跨多目录 | installer、upgrade、backup、restore 的 beta/experimental 信号是 Go/No-Go；需空主机恢复而非只备份应用 DB |

第一阶段不建议为邮件服务声明“复用现有共享数据库”作为强目标：邮件系统的恢复必须让 route DB、mail store、queue、Sieve 和签名/退信 secrets 保持一致。优先使用应用私有 DB/volume 并定义完整备份集合，等 PoC 证明跨应用共享不会破坏升级和恢复后再抽象 Contract。

## 14. 主要上游来源

### 核心项目

- Mailu：[仓库/许可证](https://github.com/Mailu/Mailu)、[stable release](https://github.com/Mailu/Mailu/releases)、[CLI/alias/forward 字段](https://mailu.io/master/cli.html)、[release notes/SRS](https://mailu.io/master/releases.html)
- docker-mailserver：[仓库/发行](https://github.com/docker-mailserver/docker-mailserver)、[alias/catch-all](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/provisioner/file/)、[环境变量/SRS](https://docker-mailserver.github.io/docker-mailserver/latest/config/environment/)、[Sieve](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-sieve/)
- iRedMail：[仓库/许可证](https://github.com/iredmail/iRedMail)、[release/upgrade](https://docs.iredmail.org/iredmail.releases.html)、[SRS](https://docs.iredmail.org/srs.html)、[SQL forwarding](https://docs.iredmail.org/sql.user.mail.forwarding.html)
- mailcow：[仓库/许可证](https://github.com/mailcow/mailcow-dockerized)、[资源和平台限制](https://docs.mailcow.email/getstarted/prerequisite-system/)、[OpenAPI alias schema](https://github.com/mailcow/mailcow-dockerized/blob/master/data/web/api/openapi.yaml)、[SRS 缺口 issue](https://github.com/mailcow/mailcow-dockerized/issues/2418)
- Stalwart：[仓库/双许可](https://github.com/stalwartlabs/stalwart)、[Community/Enterprise 对照](https://www.stalw.art/compare/)、[Docker](https://stalw.art/docs/install/platform/docker/)、[ARC verify-only](https://stalw.art/docs/mta/authentication/arc/)
- Modoboa：[仓库/ISC](https://github.com/modoboa/modoboa)、[installer](https://github.com/modoboa/modoboa-installer)、[alias model](https://github.com/modoboa/modoboa/blob/master/modoboa/admin/models/alias.py)

### 邮件协议与认证

- [RFC 5321：SMTP 与 MX](https://www.rfc-editor.org/rfc/rfc5321)
- [RFC 7208：SPF](https://www.rfc-editor.org/rfc/rfc7208)
- [RFC 9989：DMARC](https://www.rfc-editor.org/rfc/rfc9989)
- [RFC 8617：ARC](https://www.rfc-editor.org/rfc/rfc8617)
- [PostSRSd：SRS forward/reverse 与 secret](https://github.com/roehling/postsrsd)
