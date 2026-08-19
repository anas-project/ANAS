# 开源自部署邮件转发与别名服务调研（2026-08-19）

本报告按[开源自部署应用研究 Module 规范](./application-research-module-spec.md)研究“把自有域名收到的邮件转发到既有邮箱”的服务，重点回答哪些方案能替代 **Cloudflare Email Routing**，以及哪些其实是隐私别名平台、完整邮箱套件或只依赖 Cloudflare 的管理层。动态版本、许可证和维护状态采集于 2026-08-19；报告是研究快照，不是已验证的生产部署说明。

配套阅读：[开源自部署完整邮件服务与转发能力调研](./self-hosted-open-source-mail-services-research-2026-08-19.md)。那份报告回答“完整邮箱套件是否也能转发”，本报告则把 forward-only 与 privacy-alias 产品作为主对象。

## 1. 结论先行

1. **最接近 Cloudflare Email Routing 透明转发语义的严格开源首选是 docker-mailserver 的 forward-only 形态。**官方已有“无本地邮箱、alias 转发至外部地址”的用例，`SMTP_ONLY=1` 可只运行 Postfix，`ENABLE_SRS=1` 明确用于转发场景；精确 alias、正则 alias 和 catch-all 都能配置。SRS 默认关闭，不能遗漏这个显式开关。它的缺口不是 SMTP，而是没有面向普通用户的管理 UI/REST API，也没有自动 reverse alias。适合先做配置即代码的 PoC。
2. **如果目标是“一站一别名、隐藏真实邮箱、直接回复仍不泄露邮箱”，首选 addy.io，SimpleLogin 是成熟备选。**两者都是 AGPL-3.0、可自部署，支持自有域名、catch-all/自动创建、Web 管理和 API，也能经 reverse alias 回复或主动发信。它们会把收件箱里可见的 `From` 改为可回复的特殊地址，再以平台控制的域和 DKIM 重新投递；这不是 Cloudflare 保留原始可见 `From` 的透明 SRS 转发。
3. **SRS 只解决 envelope sender 和退信回送，不是 DMARC 万灵药。**透明转发将 SMTP `MAIL FROM` 改到转发器控制的 SRS 域，使该 envelope sender 能通过 SPF，并让退信可反解回原发件人；它不会让 SRS 域与原始可见 `From:` 对齐。原始 `From` 的 DMARC 仍依赖原始对齐 DKIM 在转发中不被破坏，或最终收件方愿意信任 ARC。改主题、正文、部分已签名头都可能破坏原 DKIM。
4. **Cloudflare Email Routing 本身不提供“从路由地址自然发送/回复”。**Cloudflare 当前 Postmaster 文档明确写明：转发时保留可见 `From`，但回复会从 Gmail、Outlook 等目标邮箱真实地址发出。Cloudflare Email Service 现在另有独立的 Email Sending Beta，这不改变 Routing 规则地址本身的回复限制。docker-mailserver/原生 Postfix 透明转发也有同一边界；要隐藏回复来源，需另建受认证的 SMTP send-as/地址权限，或改用 addy.io/SimpleLogin 的 reverse-alias 模型。
5. **Mailu 能转发且有 UI、REST API、SRS 和完整反滥用栈，但它是完整邮箱套件，不是轻量路由器。**若已经选择 Mailu 承载邮箱，直接使用其 alias、domain alias、custom routing 和 SRS 比再叠一个转发平台合理；若只要转发，引入 IMAP、Webmail、Dovecot 等会扩大状态面和升级面。
6. **Postfix + PostfixAdmin + PostSRSd 是最可控的传统组合，但需要自己集成。**Postfix 提供可靠磁盘队列和虚拟 alias，PostfixAdmin 提供域/alias Web UI及可选 XML-RPC API，PostSRSd 提供 SRS；反垃圾、ARC、DKIM/DMARC、速率限制、备份和升级契约仍由运维者组合。它适合有邮件系统经验且必须用 SQL 管理路由的团队，不是首个低风险 PoC。
7. **Forward Email 技术上很完整，但当前不能列为“严格开源”主推荐。**仓库 `LICENSE.md` 只把指定 IMAP/POP/存储文件放在 MPL-2.0，其余文件为 BUSL-1.1，并限制提供与 Forward Email 竞争的托管/嵌入式服务；自部署页面仍称整个代码库为 MIT。应以仓库许可证为准，把它列为源码可见，并把这处文档漂移纳入风险。
8. **同一个域不能把 MX 同时交给完整邮箱服务和独立转发平台，并期待按 local-part 自动分流。**MX 是域级入口，多个优先级通常是同一域的替代接收主机，不是 `sales@` 去 A、`alias@` 去 B 的规则。最稳妥的配套部署是：主域继续给现有邮箱，另用 `alias.example.com` 或第二域给转发平台；否则必须让一个受控网关独占 MX，再由它明确路由所有地址。
9. **自部署的真正门槛是公网邮件运维，而不是 Compose 能否启动。**至少需要稳定公网 IP、可接收入站 TCP/25；出站则需 TCP/25 直投或受信 smarthost，并配置正确 A/AAAA 与 PTR、TLS、队列持久化、IP 信誉、退信和投诉处理、反开放中继、反滥用限速、监控和恢复演练。家庭宽带或无法接收入站 TCP/25、又无法使用合适邮件网关的主机不适合作为权威入站 MTA；IP 信誉差或无法配置 PTR 的环境不适合直接投递。
10. **建议分两条 PoC 路线，避免把产品模型混在一起。**需要 Cloudflare 式域级透明路由时先测 docker-mailserver forward-only；需要隐私别名与匿名回复时先测 addy.io。SimpleLogin 只有在其客户端生态更重要时进入第二轮，因为当前应用 release 很活跃，但官方自部署 README 仍以 Ubuntu 18.04、PostgreSQL 12.1 和 `simplelogin/app:3.4.0` 为例，与当前 `v4.81.7` 明显脱节。

## 2. 范围与研究方法

### 2.1 主题卡

```yaml
topic: self-hosted-open-source-email-forwarding
title: 开源自部署邮件转发与别名服务
snapshot_date: 2026-08-19
search_date: 2026-08-19
decision_for: ANAS Runtime Module 或外部邮件资源候选
must_be:
  - 能在自有 Linux 主机接收自有域名的 SMTP 邮件
  - 能按地址、alias、catch-all 或规则转发至既有外部邮箱
  - 核心服务端许可证与自部署路径可核验
core_categories:
  - 透明域级邮件转发器
  - 隐私别名与 reverse-alias 平台
adjacent_categories:
  - 可配置转发的完整邮箱套件
  - Postfix 组件化参考栈
source_available:
  - 可自部署但关键代码受非开源/竞争限制许可证约束
excluded:
  - 只发送邮件的 SMTP relay 或营销邮件平台
  - 临时收件箱、一次性 Web inbox、纯客户端
  - 仍把 Cloudflare Email Routing 作为实际 MX/转发引擎的管理 UI
deployment_target:
  os: Linux
  runtime: Docker Engine + Docker Compose v2 或受控系统包
  web_ingress: Traefik HTTPS
  smtp_ingress: 公网 TCP/25，通常直接暴露而非普通 HTTP 反向代理
target_scale:
  domains: 1-20
  aliases: 10-10000
  users: 1-20
questions:
  - 哪个方案真正等价于 Cloudflare 的域级透明转发？
  - 哪个方案能在回复时继续隐藏真实邮箱？
  - SRS、DKIM、DMARC、SPF 和 ARC 的边界分别是什么？
  - 如何与已有完整邮箱服务安全配套？
```

### 2.2 证据口径与局限

Cloudflare Email Routing 是本报告的商业/托管基准。候选发现后，许可证、当前 release、自部署拓扑、SMTP 流程、UI/API 和安全能力均回到官方仓库、官方版本文档、项目维护者讨论或协议 RFC 核验。关键冲突遵循“仓库 `LICENSE` 高于营销/安装页面”的规则；未找到当前上游证据的能力写“未确认”，不根据产品类别猜测支持。

本轮深入核验 docker-mailserver、addy.io、SimpleLogin、Mailu、Postfix/PostfixAdmin/PostSRSd、Forward Email、NixOS Mailserver 和新出现的 mail-forwarding-core。MailPal、Vuzon 一类 Cloudflare Worker 管理层只作排除对照；Postal、Hyvor Relay 等发送型 relay 不进入入站转发排名。完整邮箱套件的整体选型由另一份邮件服务报告覆盖，本报告只保留与转发模型直接相关的 Mailu 作为代表。

版本号来自 2026-08-19 可见的 GitHub release 页面。Star、部署规模和营销性能数字不参与排序；尚未实际投递到 Gmail、Outlook、Proton Mail 等目标服务，因此所有“可投递”结论仍需 PoC 验证。

### 2.3 候选发现来源与商业基准

候选长名单用四类相互独立的入口补漏，再回到上游核验；目录条目本身不作为许可证或功能证据：

- [awesome-selfhosted 的邮件条目](https://github.com/awesome-selfhosted/awesome-selfhosted#communication---email---complete-solutions)：发现完整邮件栈和少量转发/别名项目；
- [selfh.st Apps](https://selfh.st/apps/)：按 email、alias、mail server 等关键词补充 Docker 友好项目；
- [AlternativeTo](https://alternativeto.net/)：从 Cloudflare Email Routing、SimpleLogin、Firefox Relay、Microsoft Exchange 等熟悉产品反查 `Open Source + Self-Hosted` alternatives；
- GitHub/GitLab 的 `email-forwarding`、`email-alias`、`mail-server` topic、release 与代码搜索：解析现行仓库、fork、许可证文件和新项目；mail-forwarding-core 即由此进入早期观察集合。

商业/托管产品只建立场景基线，不进入严格开源排名：

| 基准 | 代表能力 | 对开源候选提出的问题 |
| --- | --- | --- |
| [Cloudflare Email Routing](https://developers.cloudflare.com/email-service/get-started/route-emails/) | 域级透明入站路由、验证目标、catch-all、Worker | 能否保留原始 `From`，用 SRS 处理 envelope sender，并提供可靠队列与反滥用？ |
| [ImprovMX](https://improvmx.com/) | 托管域转发，并以付费发送/日志等能力扩展 | 自部署方案是否同时覆盖入站路由、send-as、日志和目标验证，还是只完成其中一项？ |
| [Apple Hide My Email](https://support.apple.com/en-us/105078) | 与账号生态绑定的随机隐私地址 | 是否能按服务创建/停用 alias，并在回复时隐藏真实邮箱？ |
| [Firefox Relay](https://relay.firefox.com/) | 面向消费者的邮箱掩码和跟踪防护 | 自部署隐私 alias 是否有相近的用户生命周期、扩展/移动端与内容隐私控制？ |

这些反向检索同时解释了为什么报告保留两条排名：Cloudflare/ImprovMX 对应透明域转发，Hide My Email/Firefox Relay 对应隐私 alias；二者不能只按“是否会 forward”合并成一个总冠军。

发现台账如下；`verified` 表示许可证和自部署入口已回到上游核验，`partial` 表示只适合作参考或仍有关键证据缺口：

| 项目 | 首要发现入口 | 分类 | 证据 | 纳入/去向理由 |
| --- | --- | --- | --- | --- |
| docker-mailserver | awesome-selfhosted、selfh.st、GitHub | core / transparent | verified | forward-only、外部 alias 与可选 SRS 均有官方文档 |
| addy.io | AlternativeTo、selfh.st、GitHub | core / privacy alias | verified | AGPL 服务端、官方 Docker、reverse reply/send |
| SimpleLogin | AlternativeTo、awesome-selfhosted、GitHub | core / privacy alias | verified | AGPL、自有域和 reverse alias 完整；部署文档漂移 |
| Mailu | awesome-selfhosted、selfh.st | adjacent / full suite | verified | 完整邮箱套件兼具 alias、API 与内建 SRS |
| Postfix + PostfixAdmin + PostSRSd | 官方组件文档、GitHub | adjacent / reference stack | verified | 协议链可解释，但不是单一可升级产品 |
| NixOS Mailserver | awesome-selfhosted、GitLab | adjacent / NixOS | verified | 声明式外部 forward/SRS，只适合 NixOS 路线 |
| mail-forwarding-core | GitHub topic/code search | early / reference | partial | 方向贴题但无稳定 release、社区与文档仍早期 |
| Forward Email | AlternativeTo、GitHub | source-available | verified conflict | 可自部署且技术完整，但 BUSL/MPL 与 MIT 文案冲突 |
| MailPal、Vuzon | GitHub topic/search | excluded | verified | 实际 MX/转发仍依赖 Cloudflare Email Routing |
| Postal、Hyvor Relay | awesome-selfhosted、产品反向搜索 | excluded | verified | 核心是出站 SMTP/API，不是入站 alias 路由 |

AI/模型能力不参与本主题选型：邮件路由的核心契约是 SMTP、认证、队列、反滥用和恢复；任何外部 AI 分类器都属于可选内容处理器，还会引入正文外发与 DKIM 破坏风险。

## 3. 先分清两种转发模型

### 3.1 Cloudflare Email Routing 基线

[Cloudflare 路由规则文档](https://developers.cloudflare.com/email-service/configuration/email-routing-addresses/)把一个自定义地址或 catch-all 映射到已验证目标、Email Worker 或 drop；[REST API](https://developers.cloudflare.com/api/resources/email_routing/)可管理 rule、catch-all 和 destination。其[当前 Postmaster 文档](https://developers.cloudflare.com/email-service/reference/postmaster/)给出了本报告的协议基线：

- 入站邮件必须至少通过 SPF 或 DKIM，并按原发件域的 DMARC 策略拒绝失败邮件；另有 RBL 信誉检查；
- 转发时使用 SRS 改写 SMTP `MAIL FROM`，但不改可见 `From:`；
- 支持 ARC，并为转发邮件增加 Cloudflare/客户路由域 DKIM 签名；
- 下游 SMTP 错误可在当前 SMTP 会话中返回上游，但事后生成的 NDR 不会再转给原始发件人；
- catch-all 只处理未命中精确规则的地址；
- Email Routing 不支持从路由地址自然发送或回复，回复会暴露目标邮箱地址；同一产品线另有独立 [Email Sending Beta](https://developers.cloudflare.com/email-service/get-started/send-emails/)，不能把它误计为 Routing rule 的 reverse alias。

截至快照日，[Cloudflare 平台限制](https://developers.cloudflare.com/email-service/platform/limits/)还包括每域最多 200 条 routing rules、每账号最多 200 个 destination addresses、单封入站邮件最大 25 MiB。普通规则映射到一个已验证目标或 Worker；多目标 fan-out 需由 Worker 执行。这些数字是托管服务配额，不是自部署候选必须照抄的上限，但自建 API/schema 必须显式定义相应配额，不能留下无限 fan-out。

这组语义决定了“像 Cloudflare”不能只看有没有 alias UI：真正相近的方案应在自有 MX 上接收 SMTP、持久排队、保留原始可见发件人、用 SRS 处理 envelope sender，并对原认证结果有清晰策略。

### 3.2 透明 SRS 转发与隐私别名重投递

| 维度 | 透明 SRS 转发 | 隐私别名重投递/reverse alias |
| --- | --- | --- |
| 代表 | Cloudflare Email Routing、docker-mailserver、Postfix + PostSRSd | addy.io、SimpleLogin |
| 收件箱可见 `From:` | 保留原始发件人 | 改成平台生成的可回复 reverse alias/编码地址 |
| SMTP `MAIL FROM` | 改成转发器的 SRS 地址 | 使用平台控制的退信/重投递地址 |
| SPF | 对改写后的 SRS/平台 envelope 域验证 | 对平台 envelope 域验证 |
| 原始 `From` 的 DMARC | 依赖原始对齐 DKIM 未被破坏，或下游信任 ARC | 不以原始 `From` 直接重放；平台对改写后的可见发件身份重新认证 |
| 普通回复 | 从目标邮箱直接回信，会暴露真实邮箱 | 回复发到 reverse alias，再从 alias 发给原联系人 |
| 主动从 alias 发信 | 需另配 SMTP submission/send-as | 产品内建编码地址/联系人的发送流程 |
| 原始认证可见性 | 可保留原 DKIM；裸 [`Authentication-Results`](https://www.rfc-editor.org/rfc/rfc8601) 只在同一 ADMD 信任边界内采信，跨边界需重新验证或依赖有效 ARC 链 | 服务先做入站鉴权，再形成平台自己的投递认证链 |
| 适用场景 | 部门地址、角色地址、迁移期路由、域级接收 | 注册隔离、反跟踪、一次一别名、匿名回复 |

[SimpleLogin reverse alias 文档](https://simplelogin.io/docs/getting-started/reverse-alias/)明确写明，收到 alias 邮件后会从特殊 reverse alias 转发；每个“发件人 + alias”组合有唯一地址，回复经该地址再以 alias 发出。[addy.io 当前回复文档](https://addy.io/help/replying-to-email-using-an-alias/)同样将转发邮件的 `From` 设为包含联系人和 alias 的编码地址，回复后由 addy.io 以 alias 重新发送。两者都能隐藏目标邮箱，但邮件客户端看到的发件人语义与 Cloudflare 不同。

### 3.3 SRS、SPF、DKIM、DMARC、ARC 的严格边界

[PostSRSd 官方说明](https://github.com/roehling/postsrsd)展示了 SRS 的两个动作：把原 envelope sender 签名、加时间戳并编码到转发器域；收到退信时验证并反解。它同时明确解释：SRS 按设计会破坏 envelope sender 与原始 `From:` 的 SPF alignment；透明转发要通过原始 `From` 的 DMARC，仍需原始域的有效对齐 DKIM。可把认证关系简化为：

| 检查 | SRS 后的结果 | 能否单独证明原始 `From` 合法 |
| --- | --- | --- |
| SPF authentication | 可对 SRS 域通过 | 不能；验证的是转发器有权发 SRS 域邮件 |
| SPF alignment | 与原始 `From` 通常不对齐 | 不能 |
| 原始 DKIM | 若正文/已签头未改可继续通过 | 可以，前提是 `d=` 与原始 `From` 对齐 |
| 转发器新增 DKIM | 证明转发器域处理过邮件 | 通常不与原始 `From` 对齐，不能替代原始 DKIM |
| ARC | 记录转发器在入口看到的认证结果和后续处理链 | 下游可选择信任，但 ARC 不是强制接受凭证 |

因此，透明转发路径不应给主题加 `[SPAM]`、插入正文 banner 或随意重写已签名头；若使用 Rspamd，应优先仅评分、拒绝或增加不破坏原签名的头，并实测 DKIM。ARC 必须在入口完成 SPF/DKIM/DMARC 评价后再 seal，且要接受不同目标提供商信任策略不同这一事实。

## 4. 候选总览

符号：✅ 官方当前资料明确支持；◐ 支持但有限制、需组合或需版本 PoC；❌ 官方明确不支持；— 未找到足够当前证据或不适用。

### 4.1 严格开源候选的定位、许可证与成熟度

| 项目 | 研究版本/状态 | 许可证 | 产品模型 | 结论 |
| --- | --- | --- | --- | --- |
| [docker-mailserver](https://github.com/docker-mailserver/docker-mailserver) | [`v15.1.0`](https://github.com/docker-mailserver/docker-mailserver/releases)；稳定发行 | MIT | 完整邮件容器可裁成 forward-only | **透明转发首选 PoC** |
| [addy.io / anonaddy](https://github.com/anonaddy/anonaddy) | [`v1.7.1`](https://github.com/anonaddy/anonaddy/releases)；2026-07-30 | AGPL-3.0 | 隐私 alias + reverse reply/send | **隐私别名首选 PoC** |
| [SimpleLogin](https://github.com/simple-login/app) | [`v4.81.7`](https://github.com/simple-login/app/releases)；2026-08-11 | AGPL-3.0 | 隐私 alias + reverse alias | 功能成熟；自部署文档漂移 |
| [Mailu](https://github.com/Mailu/Mailu) | [`2024.06.58`](https://github.com/Mailu/Mailu/releases)；2026-08-12 | MIT（Mailu 配置/代码；组件均为 FOSS） | 完整邮箱套件兼具转发 | 已用 Mailu 时优先复用 |
| Postfix + PostfixAdmin + PostSRSd | Postfix/PFA/PostSRSd 独立维护 | Postfix Secure Mailer License；PFA GPL-2.0；PostSRSd 主体 GPL-3.0-only | 传统组件化透明转发 | 高控制、高集成成本 |
| [NixOS Mailserver](https://nixos-mailserver.readthedocs.io/en/nixos-26.05/) | `nixos-26.05` 文档线 | [GPL-3.0-only](https://gitlab.com/simple-nixos-mailserver/nixos-mailserver/-/blob/master/LICENSE) | Nix 配置式完整邮件栈 | NixOS 专项参考 |
| [mail-forwarding-core](https://github.com/haltman-io/mail-forwarding-core) | 无 release；核心仓库仅十余次提交 | Unlicense | Postfix/MariaDB/PostSRSd 参考实现 | **早期，不进生产首选** |

源码可见候选单列，不计入上表的严格开源集合：

| 项目 | 状态 | 许可证边界 | 去向 |
| --- | --- | --- | --- |
| [Forward Email](https://github.com/forwardemail/forwardemail.net) | 持续维护的完整平台 | 指定文件 MPL-2.0；其余 BUSL-1.1；自部署页却仍称 MIT | 技术对照与内部用途法律复核，不列严格开源推荐 |

### 4.2 转发与用户能力

| 项目 | 自有域/MX | 精确 alias | catch-all/自动创建 | 多目标 | UI | API/CLI | reverse reply | 主动 alias 发信 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| docker-mailserver | ✅ | ✅ 外部目标 | ◐ 官方支持；通配优先级会影响精确地址 | ◐ Postfix 可做，DMS 文档不承诺所有组合 | ❌ | setup CLI/文件；无 REST | ❌ | ◐ 另配 SASL/LDAP 与 sender ACL |
| addy.io | ✅ | ✅ | ✅ 首次来信自动建 alias；可用 regex | ✅，单 alias 最多目标数按版本/配置 | ✅ | ✅ REST、扩展和移动端 | ✅ 编码回复地址 | ✅ 编码 send-from 流程 |
| SimpleLogin | ✅ | ✅ | ✅ 首次来信自动建 alias | ✅ mailbox 选择/多 mailbox | ✅ | ✅ REST、扩展和移动端 | ✅ 每联系人 reverse alias | ✅ 可先创建 contact/reverse alias |
| Mailu | ✅ | ✅ | ✅ SQL-LIKE/wildcard alias | ✅ alias 多 destination | ✅ 管理 UI | ✅ REST + Swagger、CLI | ❌ 自动 reverse alias | ✅ 完整 SMTP submission，但需地址授权 |
| Postfix 组合 | ✅ | ✅ | ✅ virtual alias/catch-all/regexp | ✅ | ✅ PostfixAdmin | ◐ XML-RPC + SQL/CLI | ❌ | ◐ 另配 submission 与 sender login map |
| mail-forwarding-core | ✅ | ✅ alias/handle | ◐ handle 类通配；需按当前 schema 验证 | — | 分离的早期 UI | 分离 REST API | ❌ | ◐ Dovecot SASL + sender ACL |
| Forward Email（源码可见） | ✅ | ✅ | ✅ catch-all/regex | ✅ 邮箱、域、IP、Webhook 等目标 | ✅ | ✅ REST | ◐ 完整 SMTP/邮箱能力，不等同 reverse alias | ✅ SMTP |

### 4.3 SMTP、认证和反滥用能力

| 项目 | 入站/可靠队列 | SRS | DKIM/DMARC/SPF/ARC | 反垃圾/反滥用 | 主要缺口 |
| --- | --- | --- | --- | --- | --- |
| docker-mailserver | Postfix 磁盘队列；状态卷可持久化 | ✅ `ENABLE_SRS=1`，默认关闭 | Rspamd 或 OpenDKIM/OpenDMARC/policyd-spf；ARC 组件可用，但公网转发 seal 需另配/实测 | Rspamd、RBL、greylist、postscreen、Fail2Ban、ClamAV | 无用户 UI/API、无 reverse alias |
| addy.io | Postfix 队列 + Redis 应用队列 | — 未见当前官方 SRS 主机制 | Rspamd 做 SPF/DKIM/DMARC；手工指南有 DKIM/ARC signing 配置，当前 Docker 默认及端到端 ARC 行为未确认 | 发送/转发限速、规则、blocklist、greylist/Rspamd | 依赖多；默认 Docker 中 Rspamd 需显式启用 |
| SimpleLogin | Postfix 队列 + email handler/job runner | — 未见当前官方 SRS 主机制 | 入站 DMARC/可选 SPF enforcement；发出邮件 DKIM 签名；ARC 未确认 | Postfix HELO/sender 限制、可选 RBL/SpamAssassin、alias 速率限制、quarantine | 官方安装手册和镜像 tag 严重滞后 |
| Mailu | Postfix 队列 | ✅ release notes 明确支持；目标版本需实测 | Rspamd、DKIM、DMARC、SPF、anti-spoofing | Rspamd、greylist、AV、认证限速 | 完整套件较重；SRS 当前配置文档不集中 |
| Postfix 组合 | Postfix incoming/active/deferred/hold 队列 | ✅ PostSRSd | 需自行加 Rspamd/OpenDKIM/OpenDMARC/ARC | 需自行组合 postscreen/RBL/限速/AV | 组装、升级和安全基线由自己负责 |
| mail-forwarding-core | Postfix 仍有传输队列；不存邮箱 | ✅ PostSRSd | OpenDKIM 可选；未形成完整 ARC/Rspamd 基线 | recipient allowlist、sender ACL、速率限制、SRS 入站拒绝 | 无稳定 release；文档自称“不存消息”不能理解为没有队列 |
| Forward Email（源码可见） | 自有 MX/SMTP/任务队列与邮箱状态 | ✅ 官方技术资料 | SPF/DKIM/DMARC/ARC | 反垃圾、限速和日志面较完整 | BUSL 限制、平台过重、许可证文档冲突 |

### 4.4 社区自部署、官方托管与付费边界

| 项目 | 开源自部署社区版 | 官方托管/付费边界 | 选型影响 |
| --- | --- | --- | --- |
| docker-mailserver | MIT；无许可证 Key，文档所列 MTA/IMAP/SRS/反垃圾能力均在社区发行 | 没有同名官方 SaaS 或功能锁定企业版 | 成本主要是自运维，不是 entitlement |
| addy.io | 主应用 AGPL-3.0；官方 Docker 打包仓库另为 MIT；自托管不应把 SaaS 套餐上限当成本地上限 | [官方托管](https://addy.io/#pricing)有 Free/Lite/Pro 功能和配额差异 | 锁定 tag 后仍需验证自部署 feature flag；不要把 Docker 仓库的 MIT 误写成主应用许可证 |
| SimpleLogin | 服务端 AGPL-3.0；官方 README 允许自托管管理员把账号设为 lifetime 来使用完整功能，不需厂商许可证 Key | [官方托管](https://simplelogin.io/pricing/)有 Free/Premium 套餐；客户端生态与 Proton 体系相连 | SaaS 配额不等于社区自部署限制，但部署文档漂移需自行承担 |
| Mailu | Mailu 自有代码/配置为 MIT，组成系统的组件均为 FOSS；无功能许可证 Key | 未发现上游以商业版锁定 alias、API、Webmail 或 SRS | 若已有完整邮箱需求可直接复用，不必为解锁转发能力付费 |
| Postfix 组合 | 三个独立 OSS 组件，没有统一产品 entitlement | 可分别购买顾问、托管或支持，不形成统一升级/兼容 SLA | 自由度高，但集成责任全部留在 ANAS/运维团队 |
| Forward Email | 仅指定文件为 MPL-2.0，其余主要代码为 BUSL-1.1；不是严格开源社区版 | 官方托管另有付费计划；BUSL Additional Use Grant 对竞争性托管/嵌入有限制 | 内部使用也应先核验具体版本和用途，不能作为严格开源 Module 基础 |

表中的“无功能锁定企业版”只表示本轮没有发现产品级 entitlement，不等于上游承诺免费商业支持。付费支持、托管配额与自部署代码能力是三个不同边界。

### 4.5 动态仓库快照

下表由 [GitHub REST repository API](https://docs.github.com/en/rest/repos/repos#get-a-repository)于 2026-08-19 采集。Star 仅表示可见社区规模，不参与评分；“90/365 天活动”依据默认分支 `pushed_at` 是否落入相应窗口，只是活动信号，不代表提交质量或维护者数量。

| 仓库 | 创建日 | 最后 push（UTC） | Star / Fork | 90/365 天活动 | 治理与发布信号 |
| --- | --- | --- | --- | --- | --- |
| docker-mailserver/docker-mailserver | 2015-03-28 | 2026-08-03 | 18,752 / 2,042 | ✅ / ✅ | 组织维护、多贡献者 release；有版本化文档、升级与安全说明 |
| anonaddy/anonaddy | 2019-06-21 | 2026-07-30 | 4,807 / 266 | ✅ / ✅ | 主项目与 Docker 打包分仓；有 release、自托管与更新说明 |
| simple-login/app | 2019-12-18 | 2026-08-17 | 6,929 / 646 | ✅ / ✅ | 活跃 release、客户端生态与安全页；self-host 安装样例滞后 |
| Mailu/Mailu | 2016-01-10 | 2026-08-17 | 7,457 / 994 | ✅ / ✅ | 组织维护、多贡献者 release；stable 分支、升级与安全政策明确 |
| postfixadmin/postfixadmin | 2014-05-02 | 2026-08-16 | 1,253 / 321 | ✅ / ✅ | 多人维护、稳定 release/升级文档；仍只是组合栈的一部分 |
| haltman-io/mail-forwarding-core | 2026-01-29 | 2026-04-14 | 2 / 1 | ❌ / ✅ | 新项目、无 release、极小社区；不据提交数量推断生产成熟度 |
| forwardemail/forwardemail.net | 2019-12-17 | 2026-08-16 | 1,660 / 200 | ✅ / ✅ | 公司主导且持续更新；许可冲突优先于活跃度信号 |

Postfix 本体、NixOS Mailserver 和各项目的移动客户端没有并入同一 Star 口径；需要采用它们时应按锁定 release 重新采集对应上游数据。开放 issue 数没有进入表，因为不同项目把讨论、支持和漏洞放在不同系统，横向比较会造成虚假精确。

## 5. 核心候选详评

### 5.1 docker-mailserver：Cloudflare 式透明转发首选

docker-mailserver（DMS）把 Postfix、Dovecot、Rspamd/SpamAssassin、ClamAV、OpenDKIM/OpenDMARC、Fail2Ban 等打进一个成熟容器，但并不强制启用全部组件。官方[forward-only 用例](https://docker-mailserver.github.io/docker-mailserver/latest/examples/use-cases/forward-only-mailserver-with-ldap-authentication/)明确使用 `SMTP_ONLY=1` 关闭 Dovecot，不创建本地邮箱，并用：

```text
./setup.sh alias add <alias-address> <external-email-account>
```

将自有域地址直接转到外部邮箱。当前[环境变量文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/environment/#enable_srs)明确写明 `ENABLE_SRS=1` 是 DMS 充当 forwarder 时需要的 SRS 开关；其默认值为 `0`，部署者必须显式启用。[alias 文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/provisioner/file/)支持外部目标、正则和 `@example.com` catch-all；同时警告 wildcard alias 的匹配优先级高于真实地址，精确例外必须放在通配规则之前。

运维方面，[state volume 文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/optional-config/#state-volume)把 Postfix 待投递队列、PostSRSd 状态、Rspamd Redis 等列为需要保留的运行时数据。[Rspamd 集成](https://docker-mailserver.github.io/docker-mailserver/latest/config/security/rspamd/)可做 DKIM/SPF/DMARC 评价、RBL、greylisting 和 ClamAV。Rspamd 本身具备 ARC 模块，但 DMS 示例的 ARC 签名范围面向 local/authenticated 流量，并不会自动为所有公网入站后转发的邮件 seal；forwarding 场景必须另行限定和实测，不能把“启用 Rspamd”直接记为 ARC 已完成。生产 forward-only 也不应机械照搬示例里“关闭 SpamAssassin/ClamAV”的最小配置，应按风险选择 Rspamd，并验证其改头行为不会破坏原始 DKIM。

主要边界：

- DMS 管理面是 setup CLI 和配置文件，没有内建普通用户 Web UI/REST API；
- catch-all 对字典攻击和垃圾邮件放大明显，默认应优先精确 alias；
- 透明转发不会生成 reverse alias，回复从外部目标邮箱发出；
- 若开放 587 做 send-as，必须增加 SASL、sender login map/ACL、强制 TLS 和速率限制，不能把入站 alias 自动当作任意发件权限；
- `SMTP_ONLY=1` 仍是邮件服务器，仍需公网 25、PTR、队列监控和信誉运维。

**结论：**锁定 `v15.1.0` 做第一顺位域级转发 PoC。首版只做单域、精确 alias、单外部目标和 SRS；catch-all、ARC、587 submission 和多节点留到基础投递验证通过后。

### 5.2 addy.io：隐私 alias 与匿名回复首选

[addy.io 主仓库](https://github.com/anonaddy/anonaddy)明确是用于自部署 addy.io 的 AGPL-3.0 源码。原生安装需要 Postfix、PHP、MariaDB/MySQL、Redis、Nginx、Rspamd、开放 25、PTR、MX/SPF/DKIM/DMARC 和 TLS；官方维护的[Docker 仓库](https://github.com/anonaddy/docker)则提供 amd64/arm64 镜像与 Compose 路径，把应用、Postfix 和 Rspamd 放入镜像，数据库与 Redis 作为外部服务。

[alias 文档](https://addy.io/help/creating-new-email-aliases/)支持在 Dashboard、扩展、移动端或 REST API 预创建地址；自有域开启 catch-all 后，任意 local-part 首次收到邮件会自动建 alias。alias 可绑定已验证 recipient，规则可根据 sender、subject、alias 等条件阻止、改收件人或调整加密行为。完整[API 文档](https://app.addy.io/docs/)覆盖 aliases、domains、recipients、rules、failed deliveries 等资源。

回复/发送是它相对 Cloudflare 的核心差异。[发送文档](https://addy.io/help/sending-email-from-an-alias/)要求从已验证 recipient 写信到编码目标，例如 `shop+hello=store.com@johndoe.addy.io`；addy.io 收到后以 `shop@johndoe.addy.io` 对外发出。转发入站时，收件箱里的可见 `From` 也会变成可回复的编码地址，原发件人另存于专用头。这能隐藏真实邮箱，但不是透明保留原 `From`。

安全和运维边界：

- Redis 同时用于节流和应用队列，Postfix 仍负责 SMTP 磁盘队列；两者都要监控和备份相应状态；
- 官方自部署指南给出了 Rspamd 的 SPF/DMARC/DKIM 检查、RBL 及手工 DKIM/ARC signing 配置；Docker 镜像的 `RSPAMD_ENABLE` 默认是 `false`，当前镜像默认和跨目标服务的端到端 ARC 行为仍需实测，不能因组件已打包或指南出现 `arc.conf` 就标成默认支持；
- 默认转发/回复小时限额可通过环境变量控制，应按用户、recipient、源 IP 和总实例维度限速；
- reverse reply/send 必须只接受已验证 recipient，并校验其域 DMARC，避免把系统变成匿名发送器；
- 官方托管帮助页的 Lite/Pro entitlement 不能直接当成自部署限制；锁定 release 后需在自部署实例验证 feature flag 和管理员限额。

**结论：**若需求是个人/小团队隐私别名，addy.io 比 DMS 更符合用户体验；若需求是角色地址透明路由，不应因它“也会转发”就把两种模型混为同一产品。

### 5.3 SimpleLogin：生态成熟，但自部署手册需重做

[SimpleLogin app](https://github.com/simple-login/app)是 AGPL-3.0 后端和 Web 应用，当前 release `v4.81.7` 仍持续更新。它支持自有域、多个 mailbox、catch-all 首次收信自动建 alias，以及 Web、浏览器扩展、Android/iOS 客户端。[API 文档](https://github.com/simple-login/app/blob/master/docs/api.md)覆盖 alias、mailbox、custom domain、contact/reverse alias、通知和统计等操作。

邮件流程由系统 Postfix、PostgreSQL、webapp、email handler 和 job runner 组成。官方[自部署 README](https://github.com/simple-login/app/blob/master/README.md)要求端口 25/80/443、至少 2 GB RAM，配置 MX/A、SPF、DKIM、DMARC，Postfix 通过 PostgreSQL map 把自有域邮件交给 handler；可关闭注册、在数据库把自部署账号设为 lifetime 以使用全部功能。[example.env](https://github.com/simple-login/app/blob/master/example.env)还提供 `ENFORCE_SPF`、SpamAssassin、alias 创建限速、VERP/bounce 和自动禁用等选项。

其[reverse alias 文档](https://simplelogin.io/docs/getting-started/reverse-alias/)证明回复/主动联系能力与 addy.io 类似；[安全页](https://simplelogin.io/security/)说明所有从 SimpleLogin 服务发出的邮件，包括转发和从 mailbox 发出的邮件，都会 DKIM 签名。官方[反钓鱼说明](https://simplelogin.io/docs/getting-started/anti-phishing/)表明服务会在入口依据 DMARC/SPF/DKIM 决定 quarantine、警告或转发。当前官方资料没有把 SRS 列为主转发机制；公开流程是把可见 `From` 转成 reverse alias 后重新投递，因此不能把它标成 Cloudflare 式透明 SRS。

最大风险是安装资料漂移：README 仍以 Ubuntu 18.04、PostgreSQL 12.1、1024-bit DKIM 示例和 `simplelogin/app:3.4.0` 为主，而当前 release 已到 `v4.81.7`。这不表示代码不成熟，但表示生产安装不能直接复制 README；必须从当前 tag 重建 Compose、依赖版本、迁移、升级和回滚 runbook，并验证镜像是否与 tag 一致。

**结论：**需要 SimpleLogin 官方客户端/扩展生态时进入 PoC；单纯追求自部署可维护性时，当前 addy.io Docker 路径更直接。两者都要测试 quarantine、临时退信、reply loop 和邮件内容删除策略。

### 5.4 Mailu：已有完整邮箱需求时复用

[Mailu 2024.06 文档](https://mailu.io/2024.06/)将自身定义为完整 Docker 邮件服务器，包含 IMAP/SMTP/Submission、Webmail、alias、domain alias、custom routing、用户 auto-forward、Rspamd、DKIM、DMARC、SPF 和 anti-spoofing。[管理界面](https://mailu.io/2024.06/webadministration.html)可管理域、用户、aliases、relayed domains 和 Rspamd；[REST API](https://mailu.io/2024.06/api.html)承诺 Web 管理界面可配置的内容也可经 API 修改，并提供 Swagger/OpenAPI 入口。官方 release notes 从 1.8/1.9 起明确列出 SRS 支持。

这使 Mailu 在“UI + API + 完整邮件安全栈”上强于 DMS forward-only，但也引入前置代理、admin、Postfix、Dovecot、Rspamd、数据库、可选 Webmail/AV 等多个容器和邮箱数据。把它只当转发器会承担不必要的状态与攻击面；反过来，如果主域已经由 Mailu 接收，直接在同一控制面配置 alias 是正确做法，无需把同域 MX 再分给另一个平台。

SRS 的当前配置项在 2024.06 文档中不如 alias/API 集中，不能只凭历史 release note 宣称所有外部转发路径默认启用。PoC 应检查目标 tag 生成的 Postfix 配置、SRS secret/域、退信反解和 auto-forward 与 domain alias 两类路径是否都经过 SRS。

**结论：**作为“完整邮箱 + 配套转发”的核心对照；只要轻量域转发时不作为首选。

## 6. 参考组合与非主推荐

### 6.1 Postfix + PostfixAdmin + PostSRSd

[Postfix 地址改写文档](https://www.postfix.org/ADDRESS_REWRITING_README.html)的 virtual alias maps 能在入队前把虚拟地址展开到一个或多个外部目标；[qmgr](https://www.postfix.org/qmgr.8.html)管理 incoming、active、deferred、hold 等磁盘队列和重试。[PostfixAdmin](https://github.com/postfixadmin/postfixadmin)是 GPL-2.0 的 Web 管理面，支持 SQLite/MySQL/PostgreSQL、域/alias/邮箱、DKIM key 存储和可选 XML-RPC API。[PostSRSd](https://github.com/roehling/postsrsd)则负责 signed/timestamped envelope sender 改写与退信反解，当前主体以 GPL-3.0-only 标注。

这是最经典、最可解释的 Cloudflare 替代架构，但不是一个可直接升级的产品。至少还要决定：

- Rspamd 或 postscreen/RBL/greylisting 的入口策略；
- SPF、DKIM、DMARC 评价与 ARC sealing；
- SQL route schema、目标邮箱验证、loop 检测和 audit；
- PostfixAdmin 与 API 的鉴权、权限分层和备份；
- queue、数据库、SRS secret、DKIM key、TLS key 的一致恢复；
- 软件包升级与 Postfix map/config 兼容性。

如果 ANAS 后续确实需要 SQL 驱动的多租户转发服务，可在 DMS PoC 验证协议后再选这条路线，不应先从零拼生产栈。

### 6.2 NixOS Mailserver

NixOS Mailserver 的[forward 选项](https://nixos-mailserver.readthedocs.io/en/nixos-26.05/options.html)支持外部转发、alias 与 catch-all，[SRS 指南](https://nixos-mailserver.readthedocs.io/en/nixos-26.05/srs.html)清楚展示专用 SRS 子域、MX、SPF、DKIM 与 DMARC 配置，并明确指出 SRS 后 SPF authentication 可通过但与原始 `From` 的严格 alignment 会断开。它适合已经采用 NixOS、希望以声明式配置管理完整邮件栈的环境；没有 UI/API，且对普通 Docker Module 不构成直接候选。

### 6.3 mail-forwarding-core：值得观察的早期参考实现

[Haltman mail-forwarding-core](https://github.com/haltman-io/mail-forwarding-core)把 Postfix、MariaDB、PostSRSd、可选 OpenDKIM 和用于 submission 的 Dovecot SASL 组合成无本地邮箱的转发栈；配置强调 recipient allowlist、外部伪造本地域拒绝、sender ACL、速率限制和 SRS 入站伪造拒绝。分离的 [REST API](https://github.com/haltman-io/mail-forwarding-api)与[管理 UI](https://github.com/haltman-io/mail-forwarding-ui)提供 alias/domain/handle/ban/token 管理。

设计方向贴题，但截至快照核心仓库没有 release、只有十余次提交，API README 还主动警告仓库文档可能落后于线上逻辑。核心 README 的“无消息存储”应理解为“不提供 mailbox/不记录内容”，不能理解为 Postfix 没有可靠传输队列；其“用 SRS 保持 SPF alignment”表述也不够严格，SRS 保持的是改写后 envelope 域的 SPF authentication，不保持原始 `From` alignment。适合作为 sender ACL/数据库 schema/deny-by-default 配置参考，不进入生产首选。

### 6.4 Forward Email：技术完整但许可证降级

[Forward Email 自部署页](https://forwardemail.net/en/self-hosted)展示的已不只是转发器：包含入站 MX、出站 SMTP、IMAP/POP3、Web 管理、API、MongoDB、Redis 和加密 SQLite 邮箱存储；官方[技术白皮书](https://forwardemail.net/technical-whitepaper.pdf)也记录 SRS、ARC、SPF/DKIM/DMARC 等能力。它在 domain/alias/catch-all、API、队列、send/receive 和日志方面技术面最宽，但这也意味着部署与备份远重于 Cloudflare Routing。

许可证必须单独处理：[当前 `LICENSE.md`](https://github.com/forwardemail/forwardemail.net/blob/master/LICENSE.md)仅把列出的 IMAP/POP/存储文件置于 MPL-2.0，其他文件使用 BUSL-1.1；BUSL 的 Additional Use Grant 允许生产使用，但禁止以与 Forward Email 产品竞争的托管或嵌入方式向第三方提供，且每个版本四年后才转为 MPL-2.0。与此同时，自部署页面仍写“整个代码库 MIT”。

**结论：**个人/内部自用可能落在 Additional Use Grant 内，但需自行做法律核验；不进入严格开源名单，不用于构建对外别名托管服务，且文档/许可证漂移降低了自动化采信度。

## 7. 与完整邮箱服务配套的域名边界

### 7.1 为什么多个 MX 不能按地址分流

[SMTP RFC 5321 第 5.1 节](https://datatracker.ietf.org/doc/html/rfc5321#section-5.1)规定发件 MTA 根据同一域的 MX preference 选择并尝试接收主机。它不知道 `alice@` 是 mailbox、`shop-123@` 是 privacy alias；多个 MX 是可替代投递路径，不是 local-part 路由表。如果低优先级“备份 MX”与主 MX 的有效收件人/反滥用策略不同，它还会成为垃圾邮件绕路或 backscatter 来源。

以下拓扑不可取：

```text
example.com MX 10 现有邮箱服务
example.com MX 20 自建转发平台

错误期待：user@example.com 去邮箱服务，alias@example.com 去转发平台
实际语义：发件方把两台视为同一域的首选/备选接收路径
```

### 7.2 推荐三种配套拓扑

| 拓扑 | 示例 | 优点 | 代价/边界 |
| --- | --- | --- | --- |
| **主域 + alias 子域** | `user@example.com` 在现有邮箱；`shop@alias.example.com` 在转发平台 | DNS 和故障边界最清楚；首选 | 地址多一个子域；部分用户不习惯 |
| **主邮箱域 + 第二 alias 域** | `example.com` 做正式邮箱；`exm.pl` 做隐私别名 | alias 更短且完全隔离；迁移简单 | 多持有一个域；品牌/续费管理 |
| **单一网关独占主域 MX** | 所有 `@example.com` 先到自建 Postfix，再按 route 转外部邮箱或 alias 目标 | 可真正按 local-part 分流 | 网关成为所有邮件单点/合规边界；需维护完整 route DB、HA 和回退 |

若完整邮箱服务本身已经提供 alias/forward（例如 Mailu），优先让它单独拥有 MX 并在内部配置；只有其功能不足且愿意让网关成为权威入口时，才使用第三种方案。外部目标不要再指回由同一转发器负责的地址，否则会形成 loop。

### 7.3 收发 DNS 分离

只收件的 alias 域仍应发布 SPF 和 DMARC，防止他人伪造该域；一旦提供 reverse reply/send-as，还必须为实际出站 MTA配置 SPF、DKIM、DMARC、PTR 和 TLS。透明 SRS 推荐使用专用 `srs.example.net`：

- `srs.example.net` 的 MX 指回转发器，以接收并反解 DSN/bounce；
- SPF 授权实际转发出站主机；
- SRS secret 必须持久化、备份和可轮换，否则旧退信无法验证；
- 纯 SRS envelope 域的核心记录是指回转发器的 MX（及相应 A/AAAA）与授权转发出站主机的 SPF；只有当它还被用作 DKIM `d=` 或可见 `From` 域时，才分别需要 DKIM、DMARC。无论如何，都不应宣称这些记录修复了原发件人的 DMARC。

## 8. 可靠性、安全与反滥用基线

### 8.1 队列和错误语义

“不存邮箱”不等于“邮件完全不落盘”。生产转发器应先将消息可靠写入 Postfix 等 MTA 队列，再向上游返回 SMTP `250`；下游 `4xx` 进入 deferred queue 并重试，下游永久 `5xx` 应在可行时同步拒绝或生成受控 DSN。必须监控：

- incoming/active/deferred/hold 队列深度与最老消息年龄；
- 按目标域的延迟、4xx/5xx、TLS 和认证失败；
- SRS 反解失败、loop、消息大小和速率限制；
- 磁盘占用与 inode，防止垃圾流量耗尽队列盘；
- 服务重启、容器重建和主机掉电后队列是否仍在。

Cloudflare 能把部分下游错误在入站会话中返回原发件 MTA；自建多跳/异步架构未必能完全复制。报告中的“支持队列”只表示有可靠 MTA/应用队列，不表示具备 Cloudflare 边缘网络的同会话下游探测和多 MX 可用性。

### 8.2 防开放中继和 backscatter

- 端口 25 只接受本机托管域且存在的 recipient；未命中 alias 时在 `RCPT TO` 阶段 `550`，不要先接收后向伪造 sender 发 bounce；
- 端口 587 只允许强制 TLS + SASL 的账号，并以 sender login map/ACL 限定能用哪些 alias 作为 `MAIL FROM`/`From`；
- 验证外部 destination 所有权，限制一个 alias 的目标数、链路深度和总 fan-out；
- 防止目标指向本机同域、SRS 域、退信地址或彼此循环；
- 对源 IP、账号、alias、recipient、目标域和全实例设置连接/消息/收件人数限额；
- 入站 SRS 格式地址只能在签名/时间戳验证成功后反解，不能把任意 `SRS0=` 当可信 relay token。

### 8.3 catch-all 的风险

catch-all 便于临时造地址，也把整个 local-part 空间暴露给字典攻击、随机垃圾和注册轰炸。企业角色地址场景默认应关 catch-all，显式创建 alias；隐私 alias 场景可开启“首次来信建 alias”，但至少要有限速、正则允许范围、禁用/忘记流程、全局 emergency off 和流量告警。DMS 的 wildcard 优先级还可能覆盖真实邮箱，PoC 必须测试精确例外。

### 8.4 内容隐私与日志

SMTP TLS 只保护每一跳，转发器通常可看到未端到端加密的主题、头和正文。数据库/日志应只保留路由和必要元数据，默认不记录正文；quarantine、failed delivery 和 debug 日志是例外，需要加密、限权和自动过期。若开启 SimpleLogin/addy.io 的 PGP 重加密，应额外测试 DKIM/ARC、附件大小、密钥轮换和无法扫描加密正文的反垃圾边界。

### 8.5 高可用不是再放一个“宽松 MX”

第二 MX 必须与第一 MX 使用同一份有效域/recipient/alias 数据、相同反滥用策略和可靠队列，并能安全地把消息转到相同目标。一个接受所有地址、稍后再转主机的传统 backup MX 往往绕过主机入口过滤并产生 backscatter。对 1–20 人规模，先把单节点队列持久化、监控、备份和恢复做对，再评估两个真正等价的 MX 节点。

## 9. ANAS 适配与实施建议

### 9.1 适配评级

| 候选 | 评级 | 适配结论 |
| --- | --- | --- |
| docker-mailserver forward-only | A（有公网邮件前提） | 单容器、MIT、配置即代码、队列/SRS/安全组件清楚；适合首个 Runtime Module PoC |
| addy.io | B | UI/API/reverse reply 完整，Docker 多架构；需要 MariaDB、Redis、Postfix/Rspamd 和更多 Secret/备份面 |
| SimpleLogin | B- | 客户端/API生态强；当前部署说明严重滞后，需维护自己的锁版 Compose 与升级路径 |
| Mailu | B（完整邮箱）/C（只转发） | 已承载邮箱时高度适配；仅为 forwarding 引入过多组件 |
| Postfix + PFA + PostSRSd | C | 可塑性高但不是单一可升级产品，Module 需自行承担大量集成契约 |
| NixOS Mailserver | C | NixOS 环境合适；与默认 Docker Module 模型不一致 |
| mail-forwarding-core | D/观察 | 设计可参考，尚无稳定 release 和整合发行 |
| Forward Email | D（严格开源口径） | 技术可用但 BUSL 为主且许可文档冲突，不作为开源 Module 基础 |

这里评的是“只为转发而部署一个 ANAS Module”的集成成本，不是单独的 SMTP 协议能力分。Mailu 在配套[完整邮件服务报告](./self-hosted-open-source-mail-services-research-2026-08-19.md)中获得公网转发能力 `A-`，但若仅为 forwarding 部署整个 IMAP/Webmail 栈，本报告按 Module 成本降为 `C`，两者口径并不冲突。iRedMail、mailcow、Stalwart 和 Modoboa 的完整转发表也在该报告维护，本报告未重复列入 core 不表示它们完全不能 forward。

### 9.2 Runtime Module/Resource 边界

邮件转发器需要公网 DNS、PTR、25 端口和 IP 信誉，这些通常不是普通 NAS 应用容器能自行声明的能力。ANAS 若实现 Module，建议把以下内容建模为外部前置检查或 Resource，而非假装自动完成：

- `public_smtp_ingress`: 静态公网 IPv4/IPv6、TCP/25 可达；
- `smtp_egress`: TCP/25 直投或受信 smart host；
- `dns_control`: MX、A/AAAA、PTR、SPF、DKIM、DMARC、可选 MTA-STS/TLS-RPT；
- `mail_reputation`: PTR/HELO一致、黑名单和投诉处理责任人；
- `relational_database`/`redis`: addy.io、SimpleLogin、SQL 组合的私有或共享依赖；
- `secret_backup`: SRS secret、DKIM/TLS private keys、API token、数据库凭据；
- `durable_queue`: Postfix spool 不能放临时容器层，也不能被两个实例共享挂载。

Web 管理面可放 Traefik HTTPS 后；SMTP 25/465/587 是独立 TCP 服务，应直接绑定或使用明确支持 STARTTLS/passthrough 的 L4 配置，不能当成普通 HTTP route。

### 9.3 管理身份、初始化与数据归属

| 候选 | 管理/IAM 边界 | API 与初始化 | ANAS 持久化/恢复面 |
| --- | --- | --- | --- |
| docker-mailserver | 没有 Web 管理用户；LDAP/OAuth2 是邮件认证能力，不等于管理面 OIDC | setup CLI + 配置文件，无稳定 REST；ANAS 应自己拥有 schema、审计和 reload | config、mail-state/Postfix queue、日志、SRS secrets、DKIM/TLS keys；全部属于应用 `data/`，邮件正文若不设本地邮箱不进入 `userdata/` |
| addy.io | 应用本地账号/2FA；本轮未确认通用 OIDC/LDAP，不能仅靠 Traefik 登录替代应用授权 | Bearer API token；官方 CLI/artisan 创建首个用户，生产应关闭公开注册 | MariaDB、Redis 必要状态、`/data`、Postfix queue、failed delivery/quarantine、GPG/DKIM/TLS keys |
| SimpleLogin | 应用本地账号/2FA；本轮未确认社区自部署通用 OIDC/LDAP | REST token；首个账号及 lifetime 权限当前需按官方数据库流程初始化 | PostgreSQL、Postfix queue/maps、DKIM key、应用 Secret、quarantine/作业状态；需从目标 tag 重建迁移清单 |
| Mailu | Web 可用 header authentication 对接外部 IdP，但 IMAP/SMTP 客户端仍需独立 token/password 契约 | 管理 REST token、CLI、配置导入/导出 | Maildir、管理 DB、Webmail DB、Sieve、alias、queue、DKIM/SRS/TLS secrets；邮箱正文属于 `userdata/` |

因此首个 DMS PoC 不应同时发明多租户管理 API；先把静态 alias 配置、投递和恢复做稳。若用户自助管理是硬需求，选择 addy.io 比在 DMS 前临时拼一个无审计 CRUD 页面更合理。

### 9.4 建议路线

**路线 A：Cloudflare 语义替代**

1. 用专用子域 `alias.example.com`；
2. 部署锁版 DMS，启用 `SMTP_ONLY=1`、`ENABLE_SRS=1` 和持久 state/config/log volume；
3. 先只建 3 个精确 alias 到一个已验证目标邮箱；
4. 入口启用 Rspamd 鉴权/评分但不改主题正文，按目标服务测试原 DKIM、DMARC 和 ARC；
5. 队列、退信、loop、备份恢复通过后再考虑 catch-all、REST 管理层和 HA；
6. 普通回复若必须隐藏邮箱，停止扩展透明模型，转路线 B，而不是临时伪造 `From`。

**路线 B：隐私 alias 平台**

1. 用第二域或 alias 子域部署 addy.io；
2. 关闭公开注册，只创建受控用户和已验证 recipients；
3. 显式启用并加固 Rspamd、Redis 密码、DKIM/ARC、实例级与用户级限速；
4. 测试 Web/API 创建、首次收信自动创建、禁用 alias、reply 和 send-from；
5. 验证收件箱可见 `From` 已被改成编码地址，并让用户明确接受这一 UX；
6. 只有需要 SimpleLogin 客户端生态时，再对当前 tag 重建其部署清单做对照。

## 10. PoC 与上线验收清单

### 10.1 DNS、网络与身份

- [ ] MX 只指向本轮唯一权威入口；A/AAAA、PTR、HELO/EHLO 完全一致。
- [ ] 从至少两个外部网络验证入站 TCP/25 与 STARTTLS；若直接投递，再确认云厂商允许出站 TCP/25；若走 smarthost，则验证其 submission 端口、TLS、认证和 sender 授权。
- [ ] alias/可见出站域按实际用途发布 SPF、DKIM、DMARC；纯 SRS envelope 域至少有正确 MX（及 A/AAAA）和 SPF，只有用作 DKIM `d=` 或可见 `From` 时才增加相应 DKIM/DMARC；各域均不存在多个 SPF TXT。
- [ ] 目标邮箱、用户 mailbox/recipient 在转发前完成验证；移除后立即停止投递。
- [ ] 用开放中继测试证明任意外域到外域、未授权 587 sender 都被拒绝。

### 10.2 路由和认证

- [ ] 精确 alias、禁用 alias、未知 recipient、catch-all、正则和精确例外行为符合预期。
- [ ] Gmail、Outlook、Proton Mail及一个自有 `p=reject` 发件域分别投递，记录 SPF、DKIM、DMARC、ARC。
- [ ] 透明路径中可见 `From` 不变，SRS envelope 可见，原始对齐 DKIM 仍 pass；若原发件邮件无 DKIM，记录 DMARC 失败而不是把 SRS 误报成修复。
- [ ] 主题、纯文本/HTML、附件、UTF-8、plus addressing、邮件列表和 `Reply-To` 均回归。
- [ ] addy/SimpleLogin 路径确认可见 `From` 为 reverse alias、真实 mailbox 不出现在对外头，回复和主动发送都不泄露。

### 10.3 队列、退信和故障

- [ ] 模拟目标 `451`/超时，消息进入 deferred queue；容器和主机重启后仍在，恢复后只投递一次。
- [ ] 模拟永久 `550`、超大消息、磁盘接近满、DNS 失败、TLS 失败，告警和退信符合策略。
- [ ] 给 SRS 地址发送合法退信，能反解到原 sender；伪造、过期或 secret 不匹配的 SRS 地址被拒绝。
- [ ] 人为构造同域、跨 alias 和两个转发器互指，loop 被 hop/route 策略及时截断。
- [ ] queue depth、oldest age、按目标域 defer rate、bounce rate、Rspamd 拒绝和磁盘容量有指标与告警。

### 10.4 安全、备份和恢复

- [ ] catch-all 遭随机 local-part 攻击时有限速、磁盘保护和一键关闭机制。
- [ ] Web/API 启用强认证、最小权限 token、CSRF/CORS/可信代理边界；管理端不直接暴露 Rspamd 无密码 UI。
- [ ] 备份 route DB/config、Postfix queue、SRS secret、DKIM/TLS key、Redis 必要持久状态和版本清单。
- [ ] 在空主机恢复后，旧 alias、旧 SRS 退信、排队邮件、DKIM DNS 匹配和用户登录均通过。
- [ ] 完成一次锁版升级与回滚演练；数据库迁移失败不会破坏现有路由或清空队列。
- [ ] 日志/failed delivery/quarantine 不长期保留正文，debug 模式有自动关闭和数据清理期限。

## 11. 排除项与候选去向

| 项目/类型 | 去向 | 理由 |
| --- | --- | --- |
| [MailPal](https://github.com/betahuhn/mailpal) | 排除：Cloudflare 管理层 | Worker 和 Dashboard 可管理 alias/catch-all，但前提仍是域已启用 Cloudflare Email Routing，实际 MX/转发不是自建 |
| Vuzon 等 Cloudflare Email Worker UI | 排除：Cloudflare 管理层 | 同上；“部署在自己 Cloudflare 账户”不等于自部署 SMTP/MX |
| Postal、Hyvor Relay、常见 SMTP relay | 排除：发送型平台 | 重点是应用出站/营销/API 发送，不接管自有域入站并按 alias 转发 |
| 临时邮箱/一次性 Web inbox | 排除：产品模型不符 | 提供短期收件箱或存储，不是把稳定自有域路由到既有邮箱 |
| 仅 PostSRSd | 组件，不是产品 | 只解决 envelope sender 改写；没有 SMTP 接收、route DB、队列管理 UI 或反滥用全栈 |
| mail-forwarding-core | 早期/参考 | 方向正确但无 release、组件整合和文档契约仍在形成 |
| Forward Email | 源码可见 | BUSL-1.1 为主，不能按严格开源推荐 |

## 12. 官方来源索引

### Cloudflare 与协议

- [Cloudflare Email Routing rules and addresses](https://developers.cloudflare.com/email-service/configuration/email-routing-addresses/)
- [Cloudflare Email Routing REST API](https://developers.cloudflare.com/api/resources/email_routing/)
- [Cloudflare Email Service Postmaster：SRS、ARC、DKIM、DMARC、RBL、回复限制](https://developers.cloudflare.com/email-service/reference/postmaster/)
- [Cloudflare Email Sending Beta getting started](https://developers.cloudflare.com/email-service/get-started/send-emails/)
- [RFC 5321：SMTP MX 选择](https://datatracker.ietf.org/doc/html/rfc5321#section-5.1)
- [RFC 7208：SPF](https://datatracker.ietf.org/doc/html/rfc7208)
- [RFC 8601：Authentication-Results 与 ADMD 信任边界](https://www.rfc-editor.org/rfc/rfc8601)
- [RFC 9989：DMARC](https://www.rfc-editor.org/rfc/rfc9989)
- [RFC 8617：ARC](https://datatracker.ietf.org/doc/html/rfc8617)

### docker-mailserver 与 Postfix 组件

- [docker-mailserver repository/license/releases](https://github.com/docker-mailserver/docker-mailserver)
- [DMS forward-only mail server use case](https://docker-mailserver.github.io/docker-mailserver/latest/examples/use-cases/forward-only-mailserver-with-ldap-authentication/)
- [DMS environment variables：SMTP_ONLY、ENABLE_SRS](https://docker-mailserver.github.io/docker-mailserver/latest/config/environment/)
- [DMS alias/catch-all configuration](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/provisioner/file/)
- [DMS state volume and Postfix queue persistence](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/optional-config/#state-volume)
- [DMS Rspamd、DKIM、ARC、RBL](https://docker-mailserver.github.io/docker-mailserver/latest/config/security/rspamd/)
- [Postfix virtual alias/address rewriting](https://www.postfix.org/ADDRESS_REWRITING_README.html)
- [Postfix qmgr](https://www.postfix.org/qmgr.8.html)
- [PostfixAdmin repository](https://github.com/postfixadmin/postfixadmin)
- [PostSRSd repository/DMARC FAQ](https://github.com/roehling/postsrsd)

### addy.io 与 SimpleLogin

- [addy.io application repository](https://github.com/anonaddy/anonaddy)
- [addy.io Docker repository](https://github.com/anonaddy/docker)
- [addy.io self-hosting guide](https://addy.io/self-hosting/)
- [addy.io alias and catch-all behavior](https://addy.io/help/creating-new-email-aliases/)
- [addy.io reply and send-from behavior](https://addy.io/help/replying-to-email-using-an-alias/)
- [addy.io API](https://app.addy.io/docs/)
- [SimpleLogin application repository/self-hosting README](https://github.com/simple-login/app)
- [SimpleLogin example.env](https://github.com/simple-login/app/blob/master/example.env)
- [SimpleLogin API](https://github.com/simple-login/app/blob/master/docs/api.md)
- [SimpleLogin custom-domain catch-all](https://simplelogin.io/docs/custom-domain/manage-domain/)
- [SimpleLogin reverse alias](https://simplelogin.io/docs/getting-started/reverse-alias/)
- [SimpleLogin security/authentication](https://simplelogin.io/security/)

### Mailu、参考实现与许可证边界

- [Mailu 2024.06 documentation](https://mailu.io/2024.06/)
- [Mailu web administration](https://mailu.io/2024.06/webadministration.html)
- [Mailu REST API](https://mailu.io/2024.06/api.html)
- [Mailu release notes including SRS](https://github.com/Mailu/Mailu/blob/master/docs/releases.rst)
- [NixOS Mailserver forwards/options](https://nixos-mailserver.readthedocs.io/en/nixos-26.05/options.html)
- [NixOS Mailserver SRS](https://nixos-mailserver.readthedocs.io/en/nixos-26.05/srs.html)
- [mail-forwarding-core](https://github.com/haltman-io/mail-forwarding-core)
- [mail-forwarding-api](https://github.com/haltman-io/mail-forwarding-api)
- [Forward Email repository and current license](https://github.com/forwardemail/forwardemail.net/blob/master/LICENSE.md)
- [Forward Email self-host page with conflicting MIT statement](https://forwardemail.net/en/self-hosted)
- [Forward Email technical whitepaper](https://forwardemail.net/technical-whitepaper.pdf)
