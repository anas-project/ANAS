---
doc_type: research
status: current
created: 2026-10-22
updated: 2026-10-26
evidence_as_of: 2026-10-26
---

# Cloudflare Email Routing 与同类托管服务比较

本报告比较把自有域名邮件转发到既有邮箱的托管服务，重点是价格、免费版边界、数量与大小限制，以及能否用原地址回复。适合个人、家庭和少量域名的维护者选型；套餐比较核验于 **2026-10-22**，**2026-10-23** 补充 NAS 本地邮箱组合可行性，**2026-10-24** 在 §4.3 复核无需 NAS 入站 25 的免费路径。**2026-10-26** 在 §9 补充用户提供的 Reddit 候选、Plunk 及邮箱/手机注册条件，检查公开注册页，没有创建账户或发送验证码。未进行真实收发测试，旧表未涉及的产品不视为全部重新核价。

2026-10-24 新增的自管云中转候选与完整接入选择集中在[接入研究 §13](mail-routing-alias-and-module-access.md#_13-接入方式选择与自管云中转可行性)：对比 Cloudflare 直投、云中转、托管 API、外部邮箱、Worker/R2 及其他端口/Webhook。VPS 中转有主机与存储成本，不归入本报告的免费托管服务；部署选择见[架构方案 §11](../architecture/portal-account-mail-administration.md#_11-邮件接入选择与云中转提案)，未选择或部署云资源。

**2026-10-24 最新产品取舍：Cloudflare＋Gmail IMAP 和正文直接投递 NAS 已取消。** 当前一期只选 Worker/R2 或自管云持久中转，通知只触发 NAS 拉取；发送继续独立选择 SMTP2GO/Resend/兼容 SMTP。下文 Gmail、Forward Email 直投与其他服务的能力比较保留研究历史，不代表可启用路径。接入步骤及限制见[邮件 Tutorial 设计](../architecture/portal-account-mail-administration.md#_13-邮件服务配置-tutorial)。

## 1. 结论

- **只要免费收件转发，且愿意使用 Cloudflare DNS：优先 Cloudflare Email Routing。**入站邮件数量不计费，但规则数、目标地址数和单封大小仍有限制。
- **不想迁移 DNS，只有一个域名：优先比较 ImprovMX Free。**界面/API 管理直接，免费 25 个别名、500 封/天；触顶后会退信，不能把它当作无限收件服务。
- **多个域名需要免费转发：Forward Email 值得比较。**免费域名与别名数量宽松，也可加密 DNS 配置；代价是以 DNS 管理为主，且免费资格受域名状态检查影响。
- **本轮隐私别名候选中，给一个自有域名增加隐私回复的最低年费是 addy.io Lite 的 $12。**但 100MB/月很容易被附件耗尽；经常通信可比较 $36/年的 addy.io Pro 和 SimpleLogin Premium。
- **免费发信也有选择，可与免费收件转发组合。**SMTP2GO 免费 1,000 封/月且 200 封/天；Resend 免费 3,000 封/月且 100 封/天；Brevo 免费 300 封/天；MailerSend 免费 500 封/月。具体资格和边界见 §4.1。不能因为转发平台把自身 SMTP 放在付费层，就得出自有域名发信必须付费的结论。
- **2026-10-26 补充：Plunk 可加入 SMTP/API 发送候选，注册首屏只需邮箱和密码，也提供 Google/GitHub。**免费 1,000 封/月并带品牌标识；SMTP2GO 当前公开表单则含手机号和短信验证步骤。各服务的手机号国家白名单及 `+86` 实际验证码送达未获确认，不能以“没有看到电话输入框”承诺整个开通流程免手机。完整比较见 §9。
- **希望邮件保存到 NAS：可评估 Cloudflare Routing + docker-mailserver + SMTP2GO。**公网 IPv4 或 IPv6 的入站 TCP 25 可达时，Cloudflare 可直接转发到 NAS；不可达时，可先转到外部邮箱，再由 NAS 主动拉取。当前 NAS 网络条件未知，按 §4.2 的条件表选路，不视为已具备部署条件。
- **没有入站 25 也有免费方案，先区分是否有其他公网入口。**仅 25 被封时，优先验证 Forward Email 免费自定义 SMTP 端口；有 HTTPS 入口时可评估 Forward Email 或 Mailgun 免费入站 Webhook；完全只能主动出站时比较 Resend API 与 Cloudflare → Gmail → IMAP。具体免费边界及 ANAS 分箱限制见 §4.3，不必先引入 Worker/R2。
- **希望由同一家同时提供转发和 SMTP：比较 ForwardMX Personal（$30/年）、Forward Email（$3/月）和 ImprovMX Light（$50/年）。**若还要保存邮件、IMAP/Webmail，则低用量的 Purelymail（$10/年，有软上限）也值得纳入。

以上是根据下列官方功能与价格作出的场景建议，不是投递质量排名。

## 2. 范围与比较口径

本轮包含四个域级转发产品：Cloudflare、ImprovMX、Forward Email、ForwardMX；两个隐私别名产品：SimpleLogin、addy.io；另以完整邮箱 Purelymail 作低价参照。补充 SMTP2GO、Resend、Brevo、MailerSend 四个独立发送服务，比较免费收件与免费发送的组合。核心使用情境是 1–20 个自有域名，把多个地址收进现有邮箱，并按需要回复或发送应用通知。

先区分三项功能：

| 功能 | 具体含义 | 边界 |
| --- | --- | --- |
| 收件转发 | `hello@example.com` 收到邮件后转给现有邮箱 | 不必然包含回复或发信 |
| 隐私回复 | 用服务生成的中转地址回复，收件人看到别名，真实邮箱被隐藏 | 不必然提供客户端可连接的 SMTP 账号 |
| SMTP 发信/完整邮箱 | SMTP 供客户端或应用发送；IMAP/Webmail 用于保存、阅读邮件 | SMTP 发送也不必然包含邮箱存储 |

**Catch-all（全域收件）**指把未单独配置的地址也接收下来，例如 `anything@example.com`。它可以接住很多地址，但不等于能分别管理无限条路由。**Reverse alias（回复中转地址）**代表某个外部联系人；向它发信，由别名服务代转并隐藏真实邮箱。

价格统一为美元，域名注册/续费、税费、已有目标邮箱和可能的额外资源费用不计入。年付折合月价与可按月付款明确区分；“无限”按厂商套餐表述记录，不代表免除反滥用、速率或公平使用限制。免费表比较普通新账号的持续免费层，不把试用、历史保留权益计入。

本报告研究托管商业服务，不沿用自部署研究的开源资格筛选和容器评分。候选发现以现有邮件研究、官方产品/竞品目录及检索补充，关键事实回到各自官方价格页、FAQ 和技术文档。自部署候选另见[开源自部署邮件转发研究](./self-hosted-open-source-email-forwarding-research.md)，不重复展开。

## 3. 免费版差异

| 服务 | 免费自有域名/地址 | 收件量与单封限制 | 回复/主动发信 | 免费版最关键的边界 |
| --- | --- | --- | --- | --- |
| **Cloudflare Email Routing** | 支持自有域名；200 条路由/域；200 个已验证目标/账号 | 入站数量无限；单封 **25MiB** | Routing 本身不提供邮箱客户端的别名发信；向账号内已验证目标发送免费 | 支持 catch-all；必须使用 Cloudflare DNS；普通规则一个目标，多目标需 Worker；每个 zone（DNS 区域）的 Routing/Sending 域合计上限 30（包括主域） |
| **ImprovMX Free** | **1 域，25 别名** | **500 封/天**；单封 **50MB** | 不含 SMTP | 7 天日志、API、catch-all；免费高级 Rules 为 0，这不表示不能配置普通别名转发 |
| **Forward Email Free** | **无限域名、无限别名**，受免费资格检查约束 | 官网列无限入站；单封 **50MB**，包含头部、正文和附件 | 不含其 SMTP 和邮箱存储 | DNS TXT 管理；catch-all、多目标、正则和 webhook；目标默认最多 10 个/别名；TXT 可免费加密 |
| **SimpleLogin Free** | **不能新增自有域名**；10 个平台域名别名；1 个目标邮箱 | 无带宽上限；单封 **25MB** | **可以回复**；主动联系新收件人为 Premium 功能 | 免费层适合少量隐私地址，不能作为免费自有域名路由替代 |
| **addy.io Free** | **不含自有域名**；用户名子域下无限标准别名，另有 10 个随机别名；1 个目标邮箱 | **10MB/月**；服务单封上限 25MB，但免费月额度更小 | **不能使用别名回复或主动发信** | “无限别名”不能消除月流量限制 |

免费表来源：[Cloudflare 价格](https://developers.cloudflare.com/email-service/platform/pricing/)、[限制](https://developers.cloudflare.com/email-service/platform/limits/)与[域配置前提](https://developers.cloudflare.com/email-service/get-started/route-emails/)；[ImprovMX 价格](https://improvmx.com/pricing/)、[附件限制](https://improvmx.com/guides/what-are-the-allowed-attachments/)与[catch-all](https://improvmx.com/guides/catchall-wildcard-aliases/)；[Forward Email 价格](https://forwardemail.net/en/private-business-email)、[FAQ](https://forwardemail.net/en/faq)与[TXT 加密 API](https://forwardemail.net/en/email-api#encrypt-txt-record)；[SimpleLogin 价格](https://simplelogin.io/pricing/)与[FAQ](https://simplelogin.io/faq/)；[addy.io 价格](https://addy.io/#pricing)与[FAQ](https://addy.io/faq/)。

**ForwardMX 与 Purelymail 没有在当前价格页提供持续免费套餐。**退款保证、受限试用不能等同免费版。参见 [ForwardMX 价格](https://forwardmx.net/pricing/)、[Purelymail 价格](https://purelymail.com/pricing)与[试用限制](https://purelymail.com/docs/faq)。

## 4. 付费价格与增加的功能

| 服务/套餐 | 公开价格 | 域名与地址 | 付费增加的主要功能/额度 |
| --- | --- | --- | --- |
| **Cloudflare Email Sending Beta** | Workers Paid **$5/月起**；含 3,000 封/月，超出 **$0.35/千封** | 沿用 Email Service 域配置 | 向任意收件人发送；支持 API、Workers binding 和 SMTP；一般出站整封上限 **5MiB** |
| **ImprovMX Light** | **$50/年**，约 $4.17/月 | 5 域；25 别名/域 | 150,000 封转发/月；750 封 SMTP/月；5 条高级规则/域 |
| **ImprovMX Premium / Pro** | **$9/月 / $24/月**；官网另列年付优惠 | 30/100 域；100/200 别名每域 | 转发 150,000/450,000 封每月；SMTP 12,000/60,000 封每月；更长日志、webhook 等 |
| **Forward Email Enhanced / Team** | **$3/月 / $9/月** | 无限域和别名；Team 增加团队管理 | SMTP、IMAP/POP3、10GB 共享存储、管理 API；发信按信誉调整，不能理解为无日限速 |
| **ForwardMX Personal** | **$30/年**，折 $2.50/月 | 1 域；100 个 forwarder | 500 封转发/天，catch-all 和 SMTP；当前价格页未给出明确 SMTP 封数配额 |
| **ForwardMX Starter / Pro / Business** | 年付 **$72 / $216 / $360**，折 $6 / $18 / $30 每月 | 30/125/250 域；100/500/1,000 个 forwarder 每域 | 转发 **2,000/4,000/10,000 封/天/域**；SMTP 与团队成员；邮箱存储另购 |
| **SimpleLogin Premium** | **$36/年**，或 **$4/月** | 无限自有域、别名、目标邮箱 | catch-all、主动发信中转、PGP；包含 Proton Pass premium 功能 |
| **addy.io Lite / Pro** | Lite **$12/年**；Pro **$36/年**，或 **$4/月** | 1/20 自有域；5/30 目标邮箱 | 两者均可隐私回复/主动发信；Lite **100MB/月**，Pro 无月带宽上限 |
| **Purelymail Simple** | **$10/年**，约 $0.83/月 | 用户、域名和存储无硬性套餐上限 | 完整邮箱与路由；消耗显著超过套餐资源价值时可能需改按量计费 |

付费表来源：[Cloudflare Email 计价](https://developers.cloudflare.com/email-service/platform/pricing/)、[Workers 计价](https://developers.cloudflare.com/workers/platform/pricing/)、[Email Service 功能](https://developers.cloudflare.com/email-service/)与[限制](https://developers.cloudflare.com/email-service/platform/limits/)；[ImprovMX 当前价格](https://improvmx.com/pricing/)及[收发配额](https://improvmx.com/guides/sending-receiving-limits/)；[Forward Email 套餐](https://forwardemail.net/en/private-business-email)；[ForwardMX 套餐](https://forwardmx.net/pricing/)；[SimpleLogin 套餐](https://simplelogin.io/pricing/)；[addy.io 套餐](https://addy.io/#pricing)及[带宽说明](https://addy.io/faq/)；[Purelymail 计价](https://purelymail.com/pricing)。

注意计量单位不同：ImprovMX 付费收发配额按账号/月；ForwardMX 上表多域套餐的入站额度按域/天。不能只比较“150,000”与“2,000”而忽略周期和范围。Purelymail 的低价适用于低资源消耗情境，不是固定价格承诺无限占用存储或大量转发。

### 4.1 独立免费发信：不必购买转发商的 SMTP 套餐

以下是持续免费层，不是仅用于测试的收件人白名单沙箱或限时试用。均可在完成所需账号开通、发送域验证后，用自有域名向外部收件人发信；是否支持特定业务用途仍按供应商要求判断。

| 服务 | 免费发送额度 | 接口 | 主要限制与适用方向 |
| --- | --- | --- | --- |
| **SMTP2GO** | **1,000 封/月，同时最多 200 封/天** | SMTP、API | 最多 5 个发送域或单独验证的发件地址；未验证发送域时另限 25 封/小时；5 天日志。适合客户端正常通信、NAS/网站通知 |
| **Resend** | **3,000 封/月，同时最多 100 封/天** | SMTP、API | 当前价格页列 3 个自有域；发送前验证域名。主要面向验证码、订单、应用通知等事务邮件 |
| **Brevo** | **300 封/天**，未用额度不累计 | SMTP、API | 免费层包含事务邮件；发送平台需要单独激活；官方免费 FAQ 列品牌标识限制，选型时确认实际发送路径的表现 |
| **MailerSend** | **500 封/月** | SMTP、API | 1 个发送域；账号需审批，订阅免费套餐仍要求填写组织地址和信用卡；免费 API 请求限 100 次/天，不能把请求次数直接当作所有接口的邮件封数 |

来源：[SMTP2GO 免费限制](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)与[SMTP/API 接入](https://www.smtp2go.com/setup/)；[Resend 价格](https://resend.com/pricing)与[SMTP 前提](https://resend.com/docs/send-with-smtp)；[Brevo 免费限制](https://help.brevo.com/hc/en-us/articles/208580669-FAQs-What-are-the-limits-of-the-Free-plan)、[收发接口](https://help.brevo.com/hc/en-us/articles/7924148470546-How-can-I-send-transactional-emails-with-Brevo)及[激活要求](https://help.brevo.com/hc/en-us/articles/115000188150-Troubleshooting-Issues-with-Brevo-SMTP)；[MailerSend 价格及免费订阅 FAQ](https://www.mailersend.com/pricing)。

**对日常自有域名邮箱，可以先比较 Cloudflare Routing + SMTP2GO。**邮件进入 Cloudflare 后转到既有 Gmail 等邮箱；在客户端或 Gmail 的“Send mail as”里配置 SMTP2GO，发送时使用 `hello@example.com`。SMTP2GO 官方有该配置路径，并说明无需改动原有收件 MX。服务费可为 $0，域名费用仍需承担。这是基于官方能力的组合建议，尚未实测。[官方 Gmail 配置说明](https://www.smtp2go.com/blog/avoid-gmails-behalf-message-use-different-smtp-server/)

SMTP2GO 注册要求自有域或工作邮箱，不接受普通 `@gmail.com` 等地址直接注册；因此可先让域名转发正常收件，再用该域名地址注册发送服务。**2026-10-26 公开注册表单另有联系电话、5 位 SMS 验证码及邮箱激活步骤**，仅列工作邮箱不足以说明全部条件；是否对特定账号触发及 `+86` 能否完成验证仍待真实开通核验。按发送商要求添加认证 DNS 记录，保留原收件 MX；配置后检查发件身份和回复落点。[注册前提](https://support.smtp2go.com/hc/en-gb/articles/12747932085145-Quick-Start-Guide)、[当前注册表单](https://www.smtp2go.com/pricing/)

已有域名并使用免费目标邮箱时，这个组合在额度内可做到额外邮件服务费为零。SMTP2GO Free 没有使用期限，不需要信用卡；域名续费、目标邮箱超出免费容量后的费用仍另计。这是当前套餐条件，不是永久价格保证。[SMTP2GO 定价](https://www.smtp2go.com/pricing/)、[免费套餐](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)

对于应用通知，Resend 的免费月额度较高，Brevo 的每日额度较高；Brevo 在 30 天里每天用满才相当于 9,000 封，不能将其视为可一次发送的月配额。SMTP2GO 超过日限额会排队，但达到月额度后继续提交会被拒绝；免费不表示没有需要处理的发送失败。[SMTP2GO 超额行为](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)

### 4.2 NAS 保存邮件：Cloudflare Routing + docker-mailserver + SMTP2GO

**有条件可行。关键条件是 Cloudflare 能通过公网 SMTP 25 端口投递到 NAS。**普通 Routing 的目标是已验证邮箱地址；它不会直接把邮件写入 NAS，也不提供普通规则中的内网 IP 或自定义 SMTP 端口配置。以下是依据官方能力推导的组合方案，尚未部署或验收。[Cloudflare 目标地址与规则](https://developers.cloudflare.com/email-service/get-started/route-emails/)、[DMS 端口说明](https://docker-mailserver.github.io/docker-mailserver/latest/config/security/understanding-the-ports/)

#### 4.2.1 按网络条件选择收件方式

当前尚未确认 NAS 的公网地址及端口可达性，先保留直投与主动拉取两条路径。表中的“可行”是能力判断，实际仍需从 NAS 所在网络以外验证连接。

| NAS 网络条件 | Cloudflare 普通转发能否直达 NAS | 适用路径与前提 |
| --- | --- | --- |
| 公网 IPv4，入站 TCP 25 可用 | 可以 | 路由器按需映射 TCP 25 到 DMS，并放行主机防火墙 |
| 只有公网 IPv6，入站 TCP 25 可用 | 可以 | NAS 主机名配置可达的 AAAA，放行 IPv6 防火墙并确认 DMS 的 IPv6 监听 |
| 有公网地址，但入站 TCP 25 被封 | 不可以 | 先转发到外部邮箱，再由 NAS 经 IMAP/POP3 主动拉取 |
| 只有内网地址，且无可达的公网 IPv6 | 不可以 | 先转发到外部邮箱，再主动拉取；普通 Cloudflare Tunnel 不替代公网 SMTP 入口 |
| 仅出站 TCP 25 被封，入站正常 | 可以 | 收件照常直投；发信通过 SMTP2GO 的 587/2525，并确认所选出站端口可用 |

Cloudflare 官方确认可向 MX 主机的 IPv6 地址投递。公网 IPv4 不是唯一条件；入站与出站端口限制必须分别判断。[IPv6 投递说明](https://developers.cloudflare.com/email-service/reference/postmaster/#ipv6-support)、[DMS 端口说明](https://docker-mailserver.github.io/docker-mailserver/latest/config/security/understanding-the-ports/)

#### 4.2.2 路径一：Cloudflare 直接投递到 NAS

以对外地址 `alice@example.com`、投递地址 `alice@in.example.com` 为例：

```text
收信：外部发件人 → Cloudflare（alice@example.com）
                  → NAS:25（alice@in.example.com）→ 本地邮箱
发信：邮件客户端 → NAS 的认证提交端口 → SMTP2GO:587 → 外部收件人
阅读：邮件客户端 → NAS 的 IMAP 服务（通常使用 TLS 993）
```

| 配置项 | 设计示例 |
| --- | --- |
| 对外收件域 | `example.com` 的 MX 保持指向 Cloudflare |
| NAS 投递域 | `in.example.com` 的 MX 指向 `mx-nas.example.com`，不为此子域启用 Cloudflare 收件路由 |
| NAS 主机地址 | `mx-nas.example.com` 的 A/AAAA 指向 NAS 可达的公网地址，设为 DNS only（灰云） |
| Cloudflare 规则 | `alice@example.com` 转到先行验证的 `alice@in.example.com` |
| DMS 本地投递 | 建立 `alice@example.com` 邮箱，并将 `alice@in.example.com` 配为投递到该邮箱的本地别名 |
| 对外发件身份 | 使用 `alice@example.com`；在 SMTP2GO 验证 `example.com`，按其要求添加认证 DNS 记录 |

这样主域 MX 与本地投递子域 MX 分工明确。不能将目标再次设为同一个走 Cloudflare 的收件地址，否则可能形成转发循环。DNS only 是因为普通 Cloudflare 代理处理 HTTP/HTTPS，不能代替 SMTP 入口。[DNS 代理限制](https://developers.cloudflare.com/dns/proxy-status/limitations/)

网络与端口需要分别判断：

- **入站必须可达 NAS 的 TCP 25。**公网 IPv4 配端口映射，或公网 IPv6 配防火墙及容器监听均可评估。Cloudflare 官方确认出站投递支持 IPv6；没有公网 IPv4 不等于无法使用，但必须实测 IPv6 25 连通性。动态地址需及时更新 DNS。[IPv6 投递说明](https://developers.cloudflare.com/email-service/reference/postmaster/#ipv6-support)
- **出站只需能连接 SMTP2GO。**DMS 原生支持外部 relay host（代发服务器），可设 `DEFAULT_RELAY_HOST=[mail.smtp2go.com]:587`，另配 SMTP 凭据与 TLS；也可使用 SMTP2GO 支持的 2525 端口。无需依靠 NAS 直接连接其他收件服务器的 25 端口。[DMS 代发配置](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-forwarding/relay-hosts/)、[SMTP2GO 接入参数](https://www.smtp2go.com/setup/)
- **普通 Cloudflare Tunnel 不能直接替代公网 SMTP 25。**其通用 TCP 公共主机名连接需要客户端运行 `cloudflared`；不能据此推断 Email Routing 会经该隧道投递到 NAS。[Tunnel 协议要求](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/protocols/)

#### 4.2.3 路径二：外部邮箱收信，NAS 主动拉取

若入站 25 不可用，可评估下列路径；本地邮箱和 SMTP2GO 发信方式保持一致：

```text
收信：外部发件人 → Cloudflare → 支持 IMAP/POP3 的外部邮箱
                                      ↑ NAS 主动连接并取回邮件
                                   DMS 本地邮箱
发信：邮件客户端 → DMS → SMTP2GO → 外部收件人
```

DMS 官方支持 Fetchmail 经 IMAP/POP3 获取邮件再投递到本地账户。这条路径不需要开放公网收信端口；在外部邮箱正常收件、容量足够且保留策略允许的前提下，NAS 离线期间的邮件先保存在外部邮箱，恢复后再取回。它增加了外部邮箱、拉取延迟和凭据维护，需要确认供应商允许的认证方式、收件人映射与远端副本保留策略。Fetchmail 默认可能删除取回的远端邮件，试验阶段应明确配置保留副本。[DMS Fetchmail 文档](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-fetchmail/)

选择免费外部邮箱时，还要满足其存储容量与 IMAP/POP3 开通条件；不能仅因网页邮箱免费就假定所有拉取接口免费可用。如果目标是邮件不落地第三方邮箱，应优先验证直投路径；直投仍经过 Cloudflare 转发，不能理解为邮件完全不经过第三方。

#### 4.2.4 其他路径与成本边界

另一个可研究方向是 Email Worker 接收原始邮件后，经 HTTPS 接口或云端存储交给 NAS。Cloudflare 有 Worker 存储邮件的官方示例，但 DMS 需要额外接入程序；离线持久化、重试与重复投递处理都需自行设计，不能视为现成的免费 Routing 开关。本报告不将该复杂方案作为已确认的实施设计。[官方存储示例](https://developers.cloudflare.com/email-service/examples/email-routing/email-storage/)

费用方面，在已有 NAS、域名和可用网络的条件下，普通 Cloudflare Routing 与 SMTP2GO Free 的供应商服务费可以是 $0；SMTP2GO 仍限 **1,000 封/月、200 封/天**，同一免费账号共用额度，月额耗尽后的新提交会被拒绝。电费、磁盘备份、域名续费和可能增加的公网服务费用不包含在“免费”里。[SMTP2GO 免费规则](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)

直投方案复杂度评估为**中等**：没有自研邮件接入层，但需维护收件子域、目标验证、TLS、邮箱账户、转发认证兼容、NAS 可用性和备份。Cloudflare 是转发入口，不能把它当作 NAS 长期离线时的无限邮箱存储。实际部署前至少需验证目标地址验证信、外部入站、回复身份、附件大小、NAS 断网恢复及发送额度耗尽时的表现；这些尚未测试。

### 4.3 2026-10-24 补充：无需 NAS 入站 25 的免费收信

**有可行候选，且不必都使用 Cloudflare Worker/R2。**“25 不通”可能只是这个端口被限制，也可能是 NAS 完全没有公网入口；两者适用的方案不同。以下是官方功能核验与接入推导，不是已经通过 ANAS 多箱、撤权和恢复验收。

| 服务/组合 | 无 25 的接法 | 免费边界 | 取舍 |
| --- | --- | --- | --- |
| **Forward Email → DMS 自定义端口** | SMTP 转到例如公网 2525 | 免费支持域级自定义端口，TXT 配置；沿用免费转发资格 | 仍需该 TCP 端口公网可达；传输继续由 DMS 处理，组件最少 |
| **Forward Email → Go Webhook** | HTTPS 推送完整邮件到 Go | 免费含 Webhook；免费不含存信/IMAP，签名密钥属于付费功能 | 需可达 HTTPS 及可靠来源认证；没有可每分钟拉取的免费邮箱 |
| **Resend Receiving → Go** | HTTPS API 列表/原文拉取，可每分钟执行 | Free 收发共用 100/天、3,000/月；标准留存 30 天 | 不需 NAS 公网入站；低量可用，额度和收件归属需核验 |
| **Mailgun Free → Go Webhook** | 入站路由推送 HTTPS，支持原始 MIME | 当前 $0/月含 1 条 inbound route；价格页列 100 emails/day，未据此推断入站有无限额度 | 需 HTTPS；免费未列长期消息留存，不能当纯轮询邮箱 |
| **Cloudflare → 免费 Gmail → Go IMAP** | 云端邮箱保存，Go 主动取信 | Google 账号含最多 15GB 免费共享空间；个人 Gmail 开放 IMAP | 可保留 Cloudflare；多箱映射、Google 认证和空间管理增加维护 |

Forward Email 官方明确给出免费 `forward-email-port` 配置，端口按域生效；Webhook 免费方案通过 DNS TXT 配置，消息包含原文及收件元数据，失败会通过 SMTP 临时错误让上游重试，不代表保存了一份可拉取的邮箱。免费层验证来源需要额外审视，不能假定已获得付费签名密钥。[自定义端口与 Webhook FAQ](https://forwardemail.net/en/faq)、[免费套餐](https://forwardemail.net/en/private-business-email)

Resend 接收列表和原文接口已在[接入研究 §12](mail-routing-alias-and-module-access.md)核验；可以直接让专用收信域 MX 指向 Resend，也可由 Cloudflare 转发到其验证目标。两种模式都只需 Go 向外访问 HTTPS，但其 `received_for` 来源于邮件头，不能未经验证用于任意分箱。[接收邮件](https://resend.com/docs/dashboard/receiving/introduction)、[接收 API](https://resend.com/docs/api-reference/emails/retrieve-received-email)、[共享额度](https://resend.com/docs/knowledge-base/account-quotas-and-limits)、[留存价格表](https://resend.com/pricing)

Mailgun 的长期 Free 与 Foundation 的一个月试用分列，前者目前包含一条入站路由。HTTP 收件接口可提供 SMTP 收件人和原始 MIME；普通失败最长重试 8 小时，不能把消息留存付费功能外推到 Free。可以评估一条通配路由交给 Go 再按有效地址映射，但是否满足本系统容量及注册条件仍需账号核验。[价格](https://www.mailgun.com/pricing/)、[HTTP 收件格式与重试](https://documentation.mailgun.com/docs/mailgun/user-manual/receive-forward-store/receive-http)

Google 官方确认个人 Gmail 的 IMAP 持续开放；Go 应接入其支持的认证方式，不能保存 Google 主密码。免费空间与 Drive/Photos 共用，不等于每个本地邮箱各有 15GB。Gmail 只是备用存信源，AD/IAM 仍是人员身份权威。[IMAP 与认证](https://support.google.com/mail/answer/7126229?hl=en)、[免费空间](https://support.google.com/googleone/answer/9004014?hl=en)

#### 对 ANAS 的建议顺序

1. **仅 25 被封、其他 TCP 端口可达：先验证 Forward Email → DMS:2525。**这是最少新增处理逻辑的路线。2525 应映射到受限的公网入站 SMTP 接收端，不可误接需要用户认证的 587 submission；Go 仍管理地址和配置，保留本地目标撤销规则。DNS 可继续托管在 Cloudflare，收信 MX 可以换给 Forward Email；若要保留 Cloudflare 第一跳，也可评估转发到 Forward Email 负责的独立投递子域，但会多一跳和目标验证，不能混放同域两家的 MX。
2. **只方便提供 HTTPS：比较 Forward Email 与 Mailgun 的 Webhook。**Go 收到后先持久保存原文和接收状态，再返回成功，随后交 DMS；需要来源验证、重放处理和可信 SMTP 目标。Forward Email 免费签名受限，若采用 IP 核验必须由可信入口保留真实来源并维护官方地址，不能信任任意 X-Forwarded-For。Mailgun 的时间戳/token 签名本身不覆盖邮件正文，仍须保持 TLS 和重放保护。正常运行不必轮询，但 NAS 长时间离线的保管能力弱于真正的邮箱。[Mailgun 认证格式](https://documentation.mailgun.com/docs/mailgun/user-manual/webhooks/securing-webhooks)
3. **NAS 完全无公网入口、只允许向外连接：比较 Resend API 与免费 Gmail IMAP。**前者适配接口直接但日额较小，后者有容量存储但账户认证与可靠分箱较复杂。共享一个远端邮箱不能再按 To 头猜本地箱；别名撤销、Bcc、同人多箱仍必须通过既有 R-041。Cloudflare 普通转发仍须先送到这种实际存信源，不能直接轮询 Cloudflare Routing。

SMTP2GO 是出站代发候选，不能用发送活动日志取回入站邮件。ImprovMX 普通转发到 NAS 仍要目标 SMTP 可达；其免费转发到 Gmail 等邮箱后拉取属于组合方案。Brevo 文档确认存在入站解析 Webhook，但本轮没有找到足够清晰的免费入站额度及完整原文保证，因此不列为已确认满足本项目的免费收信候选；300/天是发送额度。[Brevo 入站](https://developers.brevo.com/docs/inbound-parse-webhooks)、[Free 发送边界](https://help.brevo.com/hc/en-us/articles/208580669-FAQs-What-are-the-limits-of-the-Free-plan)

上述是无 25 的协议比较。最新用户已取消 NAS 直投、Gmail IMAP 和自动降级/回切，当前只支持常态云持久中转，60 秒补取保留；不能将 Forward Email 的同步转发/421 上游重试当作符合离线保管的新选项。

### 4.4 个人 Gmail IMAP（方案已取消，保留研究）

**协议可行，优先用于小规模验证；尚不能宣称满足全部多邮箱/别名验收。** 路径为 Cloudflare 公开地址 → 已验证的专用 Gmail 收件账号 → NAS Go 取信 → 本地 DMS。Gmail 使用 imap.gmail.com:993/TLS，个人账号开放 IMAP，标准账号最多有 15GB 免费空间，与 Drive/Photos 共用。这里不需要购买自定义域名 Gmail 托管，公开地址仍由 Cloudflare 接收。[Google IMAP](https://developers.google.com/workspace/gmail/imap/imap-smtp)、[个人账号设置](https://support.google.com/mail/answer/7126229?hl=en)、[免费空间](https://support.google.com/googleone/answer/9004014?hl=en)

可执行的接入步骤建议：

1. 用户提供自己的专用 Gmail 账号，先在 Cloudflare 完成目标地址验证。NAS 实际能访问 Google 的 IMAP/认证服务是启用条件，本轮没有检测实际网络。
2. 优先 OAuth；账号允许时，管理员可选择开通两步验证后的应用专用密码，用作这一远端收信源的 Secret。不保存 Google 主密码，不把它变为 AD 用户密码。应用密码可能因安全密钥专用设置、组织策略或高级保护而不可用，Google 改主密码会撤销应用密码。[应用密码边界](https://support.google.com/accounts/answer/185833?hl=en)
3. Go 每 60 秒增量取原文，进度包含账号、邮箱目录、UIDVALIDITY 和 UID；Gmail 的 X-GM-MSGID 可识别同一封邮件在不同标签/目录中的副本，作为来源消息键。不能用邮件自带 Message-ID 或已读标记代替完成记录。[Gmail IMAP 扩展](https://developers.google.com/workspace/gmail/imap/imap-extensions)
4. 本地持久接收后再确认处理，先保留远端副本。只读 Inbox 会漏掉被规则归档或分到其他位置的邮件，需明确抓取目录、垃圾邮件隔离与清理策略。空间耗尽会阻止收信并退回发件人，不是继续排队；也不能把 Google 的全部 15GB 当成邮件专用容量。[容量用尽行为](https://support.google.com/googleone/answer/9004014?hl=en)

OAuth 也有部署成本：外部应用处于 Testing 时，包含 Gmail 权限的 refresh token 通常 7 天到期；公开分发的 IMAP OAuth 应用涉及受限 scope 核验，个人自用可能适用例外，不能假定所有部署都无需配置或审查。这影响“免费服务是否省维护”，不影响 IMAP 协议本身可行。[Token 生命周期](https://developers.google.com/identity/protocols/oauth2)、[核验例外](https://developers.google.com/identity/protocols/oauth2/production-readiness/restricted-scope-verification)

**分箱是主要缺口。** 一个固定来源账号映射一个固定本地目标，技术上容易成立；把多人的主地址和别名全部汇入一个 Gmail 再按 To/Received 猜归属不成立。Gmail 的加号地址和标签可以用于组织邮件，但本轮未证明它们在 Bcc、伪造头和 Cloudflare 转发下构成可信原始信封。即使每个本地箱独用一个 Gmail，仍需识别同箱中已经撤销的别名。严格隔离到每个公开地址的专用来源可以避免混淆，却会迅速增加账号和凭据管理，不建议为此批量注册免费账号。

因此 Gmail 是明确可用的免费存信来源，适合单目标、小规模试验；全功能 ANAS 启用前必须先通过 R-041 的真实目标识别。缺少该证明时保留阻塞，不降低已确认的别名撤销要求。收信源凭据属于外部集成，不等于已经开放 DMS 应用密码或外部客户端登录。

### 4.5 Forward Email 免费版：能改投递方式，不能作为离线邮箱

截至 2026-10-24，Free 是转发套餐：不限域名、可配不限别名；不含邮箱存储、IMAP/POP3 或出站 SMTP。Enhanced Protection 从 $3/月起，才包含 10GB 共享存储和相关邮箱能力，不是免费试用后长期保留存信。[套餐边界](https://forwardemail.net/en/private-business-email)

免费可用的两条路径是域级自定义 SMTP 端口和 DNS TXT 配置的 HTTP(S) Webhook；端口不是逐别名配置。每别名默认最多 10 个目标，整封邮件默认上限 50MB；若前面保留 Cloudflare，仍受其更小的入站限制。Webhook 提供原文和接收上下文，但正文签名密钥属于付费层。域名免费资格受新注册/到期及反滥用规则限制，不能只看“无限别名”。[Forward Email FAQ](https://forwardemail.net/en/faq)、[Cloudflare 入站限制](https://developers.cloudflare.com/email-service/platform/limits/)

Webhook 每次 SMTP 连接中最多重试 3 次、单次 POST 最长等 60 秒；失败后以 SMTP 421 要求上游稍后重试。后续重发仍取决于发件服务器，**不是 Forward Email 接收成功后在磁盘保管几天**。其隐私政策明确转发邮件不落盘；错误日志和付费邮箱存储是不同用途，不提供免费历史邮件拉取。[重试说明](https://forwardemail.net/en/faq#do-you-support-webhooks)、[转发数据处理](https://forwardemail.net/en/privacy)

历史协议结论（直投 NAS 已取消）：NAS 有公网 2525 时可用 SMTP 转入 DMS 的受限入站端口；只有公网 HTTPS 时可用完整原文 Webhook，Go 必须先持久保存再回成功。它与本次自管云/R2 的“仅通知后拉取”协议不同：免费 Forward Email 没有让 NAS 稍后取回正文的存储源，不能只记录通知就返回 200。免费来源认证还需验证可信入口及官方来源地址，不能把任意 X-Forwarded-For 当证据。若需要离线暂存，可以在 Webhook 后接自己的持久云接收端，但那会增加自管存储，不再是免费 Forward Email 原生功能。

### 4.6 重新整理收信转发与发信代发

“收信转发”把外部来信送到自己的邮箱；“发信代发”把本地用户提交的邮件送给外部收件人。两者分开配置，回复与主动写信采用同一出站链路：Roundcube → DMS（Go 校验当前邮箱与 From）→ 选定 SMTP 服务。Gmail 暂存、R2 下载或云中转取信不自动决定回复的发件身份。

| 出站选择 | 当前成本/限制 | 对 ANAS 的定位 |
| --- | --- | --- |
| SMTP2GO Free | 1,000/月、200/天；最多 5 个发件域或单地址验证对象 | 普通低量代发优先候选；日速率超出可由其排队，月额耗尽的新提交被拒绝 |
| Resend Free | 收发合计 3,000/月、100/天 | 可同时收发，也可只代发；收件消耗同一额度，使用范围仍受发送政策约束 |
| Forward Email Free | 无出站 SMTP | 只选它转发时仍需另配发送商；付费邮箱套餐可另行比较 |
| Cloudflare Email Sending | 向任意外部联系人发送需 Workers Paid；当前包含 3,000/月，超出另计费 | 不等于免费 Routing 自带普通邮箱外发；Beta 与大小限制另核，不自动新增为首版供应商 |
| 通用 SMTP / 自有云 Postfix 外发 | 取决于账号，或承担云主机、出口与信誉维护 | 通用配置可复用；直接公网投递要另外验证出站 25、反向 DNS、签名和退信，不因云端能收信就认定能稳定外发 |

来源：[SMTP2GO Free](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)、[Resend 限制](https://resend.com/docs/knowledge-base/account-quotas-and-limits)及[发送政策](https://resend.com/legal/acceptable-use)、[Forward Email 套餐](https://forwardemail.net/en/private-business-email)、[Cloudflare 价格](https://developers.cloudflare.com/email-service/platform/pricing/)。

当前推荐组合：Worker/R2 + 通知/拉取，外发选择 SMTP2GO 或 Resend；已有 VPS 时可用自管云持久暂存 + 相同通知/拉取协议。Cloudflare＋Gmail IMAP 与 Forward Email 直接投递 NAS 已取消。Resend 同时收发保留研究，额度、可信目标和恢复验收通过前不在正式 Tutorial 中开放接收启用。

切换发信商必须明确处置未完成队列，不自动叠加免费账号或把接收结果未知的信重新经另一家发送。云端持久接收、NAS 入箱、SMTP 服务接收和外部最终投递分别记录；Webhook 通知成功不等于其中任意一次正文交付成功。

## 5. 容易影响选择的细节

### 5.1 Cloudflare 已有发送服务，但免费 Routing 仍不等于完整邮箱

当前 Email Sending 仍标 Beta，可以通过 SMTP/API 发信，不能再笼统说“Cloudflare 完全不能发邮件”。不过，向任意外部联系人发送需要 Workers Paid 和发送域配置；仅向账号内已验证目标发送则免费，也不计入月发送配额。已有 Workers Paid 的账号可复用套餐，是否新增 $5 基础费取决于现有订阅。[官方价格](https://developers.cloudflare.com/email-service/platform/pricing/)、[功能概览](https://developers.cloudflare.com/email-service/)

Routing 中配置一个 `hello@example.com`，并不会自动替 Gmail 等目标邮箱配置发件身份。人工回复要再选择 SMTP/send-as 路径，或者采用隐私别名服务的回复中转。Email Worker 的程序化回复也不是邮箱客户端的自动发件身份配置。[官方回复限制](https://developers.cloudflare.com/email-service/reference/postmaster/#sending-or-replying-to-an-email-from-your-cloudflare-domain)

单个地址转给多人时，Cloudflare 要用 Worker 分别 `forward()`，且各目标先验证；同一地址创建多条普通规则不会实现群发。Worker 处理受运行资源限制，复杂邮件解析可能超过免费 CPU 限额。[路由规则](https://developers.cloudflare.com/email-service/configuration/email-routing-addresses/)、[运行限制](https://developers.cloudflare.com/email-service/platform/limits/)

收件也有认证边界：Cloudflare 要求入站邮件至少通过 SPF 或 DKIM，并执行发件域的 DMARC 策略；这些是验证邮件来源的机制。官方还列明 Routing 不向原发件人转发未投递报告（NDR）。用于重要收件时，应测试失败通知和目标邮箱拒收情形，不能仅以“数量无限”判断适用性。[Postmaster 限制](https://developers.cloudflare.com/email-service/reference/postmaster/)

### 5.2 Forward Email：免费可隐藏 DNS 目标，但存在域名资格限制

如果直接写明文 `forward-email=...` TXT，目标邮箱可被公开查询；当前官方也提供**免费 TXT 加密接口**，因此“只有付费才能避免 DNS 明文暴露”已经不准确。加密 DNS 配置不等于邮件内容端到端加密。[加密 API](https://forwardemail.net/en/email-api#encrypt-txt-record)

其 FAQ 对免费域名资格有额外条件：近期过期或处于指定注册商状态的域名需要付费；90 天内新域名并非一律收费，但重新注册的旧域名、部分注册商持有的域名、新 `.cc`/`.xyz` 域及被其指定过滤器拦截的域有附加要求。判断还依赖 WHOIS/RDAP 和历史使用检查。新买域名时应先核对资格。[官方域名规则](https://forwardemail.net/en/faq#why-do-newly-registered-or-recently-expired-domains-require-a-paid-plan)

付费发信的“无限”也有边界：起始日阈值通常为 Enhanced 300、Team 900，之后按信誉提升，域名另有增长规则。适合普通通信的额度不能直接当成营销群发额度。[发送限制](https://forwardemail.net/en/faq)

### 5.3 配额触顶会不会丢失本次投递机会

| 服务 | 官方说明 | 对选型的影响 |
| --- | --- | --- |
| ImprovMX | 达到收件额度后暂停相关域处理，后来邮件退信；免费在 UTC 00:00 重置，付费在下个账期重置 | catch-all 收到大量垃圾邮件时也要注意用量；不能假定超额邮件会自动排队等恢复 |
| addy.io | 免费超过月流量后返回永久超额错误；Lite 每日历年提供一次免费用量重置，之后可付费重置或升级；Pro 无月流量上限 | Lite 的 100MB 是每月总邮件流量，不是单封附件额度 |
| ForwardMX | 超出套餐或显著高于同档客户用量时，可能要求升级或转 Enterprise | 公平使用条款仍适用；不能从 SMTP“包含”推断无发送限制 |
| Purelymail | Simple 资源消耗显著超出 $10/年的水平可能要求改按量计费；对外发信另有约 3,000 封/天、突发约 300 封的速率限制 | 无用户/域名硬上限不等于无资源成本或无反滥用限制 |

来源：[ImprovMX 超额处理](https://improvmx.com/guides/sending-receiving-limits/)、[addy.io 带宽与超额处理](https://addy.io/faq/)、[ForwardMX 公平使用说明](https://forwardmx.net/pricing/)、[Purelymail 价格](https://purelymail.com/pricing)与[FAQ](https://purelymail.com/docs/faq)。

例如，假设编码后的每封邮件是 5MB，addy.io Lite 的 100MB/月大约只能承载 20 封这样的邮件，回复也消耗流量。这是按额度作的示例计算，不是服务商对可收封数的保证。addy.io Pro 仍有每用户 200 封/小时的处理限制；它也明确不提供别名 SMTP 登录凭据，发送通过中转地址完成。[addy.io FAQ](https://addy.io/faq/)

### 5.4 “支持大附件”还要看整条投递路径

服务商允许的整封邮件大小不等于裸附件大小，编码、头部和正文都会占空间；最终目标邮箱也有自己的限制。ForwardMX 知识库写的是经其发送邮件上限 500MB，但这既不能证明所有目标邮箱接收 500MB，也不应未经实测就推定所有入站转发路径相同。[ForwardMX 大小说明](https://forwardmx.net/billing/knowledgebase/23/Message-Size-Limits-and-Attachment-Size-Limits.html)

因此，普通转发可先按 25MB 级别规划兼容测试；需要大附件时，用文件链接通常更省额度。Cloudflare 对一般出站的 5MiB 限制比其入站 25MiB 更紧，要分开评估。

### 5.5 DNS、MX 与可迁移性

Cloudflare 明确要求使用其 DNS；ImprovMX 等通过配置 MX/TXT 接入的服务可以保留现有 DNS 提供商。迁移 DNS 服务不等于迁移域名注册商。[Cloudflare 前提](https://developers.cloudflare.com/email-service/get-started/route-emails/)、[ImprovMX DNS 配置](https://improvmx.com/guides/generic-dns-configuration/)、[ForwardMX 兼容说明](https://forwardmx.net/pricing/)

同一域名不能靠同时添加两个供应商的 MX 来实现 `alice@` 去 A、`bob@` 去 B 的地址分流。需要一个明确的入站入口处理规则，或者用不同子域分别接入；混放 MX 可能造成间歇性退信。[ImprovMX 多 MX 说明](https://improvmx.com/guides/can-i-use-mx-records-from-multiple-forwarders/)

自有域名的地址通常可随 MX 和路由配置迁移；平台提供的别名域名不归用户所有。对银行、长期账号等难以改邮箱的用途，自有域名更可控。这是迁移性建议，不表示需要自部署邮件服务器。[addy.io 对迁移边界的说明](https://addy.io/#pricing)

## 6. 按需求缩小候选

| 实际需求 | 优先比较 | 原因 |
| --- | --- | --- |
| 多个自有域、只收件、零服务费 | Cloudflare；不迁 DNS 时比较 Forward Email | 前者管理直接，后者域名数量宽松但需检查免费资格与 DNS 配置方式 |
| 一个域、少于 25 个独立别名、无需发信 | ImprovMX Free / Cloudflare | 根据是否迁 DNS、是否可能达到 500 封/天决定 |
| 自有域名正常收发、希望服务费为零 | Cloudflare Routing + SMTP2GO Free | 收件与发送分工；发送须满足 1,000 封/月、200 封/天及账号条件 |
| 邮件保存到 NAS，公网入站 25 可达 | Cloudflare Routing + DMS + SMTP2GO Free | 用独立子域投递到 NAS；可用公网 IPv4 或 IPv6，需维护本地邮箱、在线状态和备份 |
| 邮件保存到 NAS，公网入站 25 不可达 | Cloudflare Routing + 外部邮箱 + DMS 主动拉取 + SMTP2GO Free | 不依赖公网收信端口；增加外部存储、拉取延迟和认证维护，免费取决于所有环节的额度 |
| 应用要免费发送验证码或通知 | Resend / Brevo / SMTP2GO | 分别比较月额度、每日额度、激活要求和现有接口；不把月额度当成单日峰值 |
| 一个域、少量隐私回复、附件少 | addy.io Lite | $12/年，但必须能接受 100MB/月 |
| 大量隐私别名、正常附件与双向通信 | SimpleLogin Premium / addy.io Pro | 都可按 $36/年订阅；前者无限自有域，后者最多 20 域 |
| 一个域、用现有客户端 SMTP 发信 | ForwardMX Personal / Forward Email | 分别 $30/年、$3/月；进一步比较实际发信额度和是否需要存储 |
| 少量域、需要完整收发邮箱且用量小 | Purelymail / Forward Email | 分别从低用量年费、含 10GB 的月费套餐入手 |
| 30 个域、需要后台/API和 SMTP | ForwardMX Starter / ImprovMX Premium | 年付 $72 对比月付 $9；还需比较按域日额度与按账号月额度，不能仅按价格决定 |
| 已有 Workers Paid，应用要发事务邮件 | Cloudflare Email Sending Beta | 可复用套餐内额度，但需要接受 Beta 状态及出站大小限制 |

本表为前述事实的选型推论；最终应按实际域名数、每月流量、峰值日收件量和回复方式计算，而不是给所有产品排一个总名次。

## 7. 与 ANAS 的关系

这些服务可作为外部邮件入口或发送供应商，不要求为了使用它们增加 ANAS Runtime Module。本轮没有实施任何接入。

- **Module/IAM**：供应商域名与别名管理账号不自动继承 ANAS IAM 用户和权限。需要自助别名时，应另行确认产品需求与管理 API 权限，本研究不承诺账号同步。
- **数据库/Traefik**：普通托管转发不增加本地数据库；MX 的 SMTP 投递不经过普通 Traefik HTTP 路由。只有明确需要 webhook 时才评估 HTTPS 接口。
- **备份/升级**：保留域名控制权、DNS 和别名配置的可恢复副本；邮件正文的备份由最终邮箱或选用的邮箱托管产品负责。供应商升级和配额调整需要定期复核，不能依赖本地镜像锁定版本。
- **应用通知**：只给少数已验证管理员邮箱发送通知时，Cloudflare 的免费目标地址发送值得单独测试；发给任意注册用户则不能套用这项免费例外，可比较 §4.1 的独立免费 SMTP/API 服务。

Purelymail 的路由可以转到外部邮箱或多个目标，也可用 Sieve（邮件过滤规则语言）实现保留副本再转发，因此是“完整邮箱兼做转发”的参照，而不是与 Cloudflare 完全相同的产品。[Purelymail 路由文档](https://purelymail.com/docs/routing)

## 8. 核验范围与剩余未知

本轮完成官方公开价格、功能与主要限制的桌面研究，没有注册账号、下单、修改 DNS、调用发送接口或测试 Gmail/Outlook/QQ/163 的真实投递。公开承诺的可用率不等于实测到达率，本报告不据此做可靠性排名。

NAS 的公网 IPv4/IPv6、入站 TCP 25 和到 SMTP2GO 的出站端口尚未检测。§4.2 保留两条收件路径，未确定最终部署方式，也未验证 Cloudflare 目标地址验证信、DMS 本地别名、拉取认证或断网恢复。部署前先确定网络条件，再用测试子域检查整条收发链路。

仍需在采购或试用时确认：ForwardMX 具体 SMTP 日/月额度及多方向大小边界；所选账号的当前发送信誉额度；促销、税费和年付结账金额；日志可见性是否足够排障；实际目标邮箱的反垃圾表现。addy.io 的独立 `/pricing/` 链接本轮返回 404，价格依据其当前首页，带宽依据 FAQ。

旧评测中的 ImprovMX Premium 6,000 封 SMTP/月、SimpleLogin $30/年，以及“Forward Email 免费配置只能明文公开”“Cloudflare 没有任何发送功能”等说法不作为本轮依据。价格页和详细限制发生冲突时保留差异，不从历史文章补出未经证实的额度。

下一步：托管邮箱按 SMTP、DNS 与流量条件缩小候选；NAS 本地邮箱先检测公网地址和入站 TCP 25，确定直投或主动拉取。随后用测试子域验证收件、回复身份、附件、退信和配额提示；本报告不代表已完成生产接入验收。

## 9. Reddit 候选、Plunk 与注册条件（2026-10-26）

用户指定的 [Reddit 讨论](https://www.reddit.com/r/email/comments/1l9nb5o/looking_for_free_alternatives_to_resend/)关注多个域名的免费事务邮件。以下覆盖本次能读取的正文及评论中实际出现的服务，并加入用户指定的 [Plunk 对比页](https://www.useplunk.com/vs/resend)。不把 Reddit 页面下方的推荐帖子算成评论推荐；已删除评论和无法展开的回复不能恢复，不能保证其中没有其他服务。

评论只能用于发现候选。额度、注册及开通条件尽量以服务商当前价格、文档和公开注册页为准；Plunk 对 Resend 的比较也以 Resend 自己的页面交叉核验。这里没有新增接入实现或改变既定的云持久中转方案。

### 9.1 可继续比较的免费发送服务

| 服务 | 当前免费边界 | 免费 SMTP / API | 对本项目的意义及来源 |
| --- | --- | --- | --- |
| **Resend** | 3,000 封/月、100 封/天，**3 个域**；收发共享事务邮件额度 | 两者均有 | 原帖“免费只有 1 域”已经过时；官方于 2026-08-25 增至 3 域。先确认 3 域是否已满足需求。[价格](https://resend.com/pricing)、[变更公告](https://resend.com/changelog/three-domains-on-the-free-tier) |
| **Plunk Cloud** | **1,000 封/月**，无限联系人，含自有域；免费邮件带 Plunk 品牌标识；所读价格页未明确列出域数上限 | 两者均有 | 托管平台有持续免费层，付费 $0.001/封。不能把无限联系人写成无限域名。[价格](https://www.useplunk.com/pricing)、[SMTP](https://www.useplunk.com/features/smtp) |
| **Maileroo** | **3,000 封出站/月，2 域**；另列 1,000 封入站/月；免费无需信用卡 | 两者均有 | 新账号先处于测试模式，不能将套餐总额当作注册后立即可用的发送量。[价格](https://maileroo.com/pricing)、[测试模式](https://maileroo.com/help/what-is-test-mode-how-can-i-send-emails-without-any-limitations) |
| **SMTPfast** | **3,000 封/月，1 域**；1,000 联系人；官网说明无需信用卡 | 两者均有 | 免费域数少于当前 Resend；正式域名为 `smtpfa.st`，不是同名猜测域。注册页写明邮箱验证后还要选择激活方式，公开资料另有邀请/申请激活历史，完整开通条件未实测。[价格](https://smtpfa.st/pricing)、[注册](https://smtpfa.st/register)、[入门](https://smtpfa.st/blog/getting-started)、[变更记录](https://smtpfa.st/changelog) |
| **eusend** | **3,000 封/月，100 封/天，1 域**；1 API Key，免费无需信用卡 | 两者均有 | 自有域验证后才可向任意真实收件人发送；共享 sandbox 只投递到账户邮箱。[价格](https://eusend.dev/pricing)、[入门](https://eusend.dev/docs/getting-started/quick-start) |
| **Posthaste** | **750 封/月，1 域**；起始日上限 50，后续按信誉调整；免费无需信用卡 | 两者均有 | 仅事务邮件；Free 不含收信地址或入站邮件额度，不能拿来替代持久收信中转。[价格](https://posthastemail.dev/pricing)、[主页](https://posthastemail.dev/) |
| **Mailumi** | **1,500 封/月，50 封/天，2 域**；免费含收发与 API，免费无需信用卡 | **API 有，SMTP 不含** | SMTP 只在 Pro/Scale，Pro 从 $20/月起。API 免费不等于能直接作为 DMS 的免费 SMTP relay。[价格](https://mailumi.com/#pricing)、[SMTP 认证与套餐限制](https://mailumi.com/docs) |

以上是官网声明和公开页面核验，没有发送、压测或到达率排名。较新的服务不因为价格低或宣称“自有基础设施”就视为已证明长期可靠。

### 9.2 其他评论提及项

| 提及项 | 当前核验 | 处理 |
| --- | --- | --- |
| **SendGrid** | 官方 2025-05-28 宣布取消 Free Email API / Free Marketing Campaigns，旧用户有 60 天过渡期 | 不列入普通新用户的持续免费替代；试用和付费另议。[官方公告](https://www.twilio.com/en-us/changelog/changes-coming-to-sendgrid-s-free-plans) |
| **SelfMailKit** | 评论作者称已被收购并停止新注册；本次网页抓取 502、浏览器连接重置 | 标记不可核验，不据此认定永久停运；当前不作为可注册候选。[评论来源](https://www.reddit.com/r/email/comments/1l9nb5o/looking_for_free_alternatives_to_resend/)、[原站](https://selfmailkit.com/) |
| **SendexAPI** | 评论中有注册故障、暂停及是否关站的反馈；本次网页和浏览器均未能访问 | 当前额度、账户条件和持续运营未确认，不采用旧评论的承诺。[原站](https://sendexapi.com/) |
| **Migma** | 当前定位是 AI 邮件制作、营销和开发者工具；所读价格比较表主要列 Premium/Business/Enterprise，没有核实到明确的持续免费 SMTP 配额 | 属于相邻工具。公开注册首屏是邮箱或 Google；后续免费额度、API/SMTP 使用权及电话条件未确认。[价格](https://migma.ai/pricing)、[注册](https://migma.ai/register) |
| **CampaignLark** | Maileroo 同集团的营销产品；Free 列 1,000 封/月、2 域；注册页为邮箱和至少 8 位密码，无需信用卡 | 有免费营销层，所读资料未证明提供可替换 DMS 出站的通用免费 SMTP relay；不与 Maileroo SMTP 混为同一套餐。[价格](https://campaignlark.com/pricing)、[注册](https://my.campaignlark.com/account/sign-up)、[两产品关系](https://maileroo.com/blog/introducing-campaignlark) |
| **indes-email-comps** | 评论是“邮件组件 + 自己的 ESP（如 SendGrid）”建议；未提供可确认的独立发送服务链接 | 记录为组件建议，不当作另一个有免费额度和注册条件的托管服务 |

### 9.3 账户邮箱、手机及国家条件

**这里区分三个阶段：注册首屏、账户激活/审核、发送域验证。** 能注册不等于已获生产发送权限。下表中的“未见手机号”只描述已查看的公开界面/资料，不承诺后续风控永不追加。没有使用个人资料、OAuth 登录或提交任何注册表单，没有请求短信；因此没有任何一项可写成“`+86` 注册实测成功”。

| 服务 | 邮箱及其他账户条件 | 手机号条件 | 注册手机号国家范围 / `+86` |
| --- | --- | --- | --- |
| **Plunk** | 邮箱+密码，或 Google/GitHub；条款列至少 16 岁，密码至少 6 位；邮箱验证可按部署配置要求，OAuth 邮箱视为已验证；发信须验证自己的域名 | 公开注册首屏无手机字段，所读开通资料未列 SMS 步骤 | 该公开路径没有手机号国家选择；**不需要先证明持有海外号码才能尝试注册**，但未验证后续风控。[注册](https://next-app.useplunk.com/auth/signup)、[账户条件](https://www.useplunk.com/terms)、[域验证](https://docs.useplunk.com/guides/verifying-domains) |
| **Resend** | 邮箱+密码，或 Google/GitHub；当前公开页列至少 12 位密码及字符组合；正式发送需要验证自有域 | 公开注册首屏无手机字段 | 未找到其账户注册专用的手机号国家白名单；当前首屏不存在 `+86` 选择问题，不能写成“短信支持中国”。[注册](https://resend.com/signup) |
| **Maileroo** | 姓名、邮箱、至少 8 位密码；生产开通需添加域名、提交账户验证表及网站/用途资料；初始最多 **100 封总量、7 个不同收件人**，官方称审核通常最多 12 个工作小时 | 注册首屏无手机；当前隐私政策将电话标为 **optional** | 电话并非公开账户资料中的必填；但官方承认服务有国家/地区可用性限制，未公开完整允许名单，**中国大陆可开户仍未确认**。[注册](https://app.maileroo.com/account/sign-up)、[隐私政策](https://maileroo.com/legal/privacy-policy)、[审核](https://maileroo.com/help/what-is-test-mode-how-can-i-send-emails-without-any-limitations)、[地区限制](https://maileroo.com/help/why-is-maileroo-not-accessible-my-country) |
| **SMTPfast** | 姓名、邮箱、至少 8 位密码；邮箱验证后选择激活；条款列至少 18 岁；发信先验证域 | 公开注册首屏无手机；完整激活分支未执行 | 未找到注册手机号国家名单，`+86` 后续是否需要/可用均未确认。[注册](https://smtpfa.st/register)、[条款](https://smtpfa.st/terms) |
| **eusend** | 姓名、邮箱、密码，或 Google/GitHub；正式发信先验证域 | 公开注册首屏无手机 | 未公开本轮可核验的注册手机号名单；后续风控与 `+86` 未实测。[注册](https://eusend.dev/signup) |
| **Posthaste** | 页面标作 Work email、密码、可选公司名，或 Google；创建发送 Key 前必须确认账户邮箱 | 页面及公开 signup API 未列电话必填 | 未列注册手机号白名单；Work email 标签本身不足以证明 Gmail 等公共邮箱必被拒绝。[注册](https://app.posthastemail.dev/signup)、[邮箱确认门禁](https://posthastemail.dev/docs/authentication)、[signup 字段](https://posthastemail.dev/docs/api) |
| **Mailumi** | 邮箱、密码、workspace 名称，勾选条款；文档要求确认邮箱、验证域后创建 Key | 公开注册首屏无手机 | 未列可核验的注册手机号白名单；`+86` 未实测。[注册](https://mailumi.com/register)、[入门](https://mailumi.com/docs) |
| **CampaignLark / Migma** | 前者邮箱+密码，后者首屏邮箱或 Google | 本次查看的首屏均无手机 | 后续激活与注册国家限制未确认；营销 SMS 的国家覆盖不能当作开户手机号支持名单 |
| **SMTP2GO（已有候选复核）** | **工作/自有域邮箱**，公共 Gmail/Yahoo 等被当前表单明确排除；姓名、公司、密码，邮件激活 | 公开表单包含 Contact Phone Number、**5 位 SMS** 验证步骤；是否依账号/地区条件触发尚未执行 | 未在公开资料找到可核验的开户手机国家白名单；**`+86` 未确认**。不能仅凭其 SMS 产品覆盖国家推断注册验证可用。[注册表单](https://www.smtp2go.com/pricing/)、[入门条件](https://support.smtp2go.com/hc/en-gb/articles/12747932085145-Quick-Start-Guide) |
| **SendGrid** | 官方建号说明要求邮箱验证码、手机验证、恢复码及公司/网站资料；2FA 手机与后续联系号码可以不同 | 官方新建号流程列 SMS 或语音验证；2FA 支持说明排除 VoIP/座机 | 未找到本轮可核验的完整开户手机国家白名单，**不能确认 `+86`**；且已不满足持续免费前提。[建号说明](https://help.twilio.com/articles/47492808476827)、[2FA 故障说明](https://help.twilio.com/articles/47688529376539-Unable-to-login-to-Twilio-SendGrid-Direct-Account-due-to-2FA) |
| **Mailgun（既有候选参照）** | 当前首屏为 Work Email、姓名、公司；默认落在带卡的 Foundation 30 天试用，可选项不等于长期 Free 条件 | 较早的官方入门文章明确要求邮箱激活后用可收短信的手机号验证；当前激活分支未执行 | 公开资料不足以确认现行名单或 **`+86`**；旧流程与第三方失败个例都不当作当前白名单。[当前注册](https://signup.mailgun.com/)、[旧官方激活说明（2019）](https://www.mailgun.com/blog/product/getting-started-with-mailgun-introduction-to-platform-training/)、[Free 与试用分列](https://www.mailgun.com/pricing/) |

对“包含哪些国家”的当前可复核答案是：多数候选的公开注册路径没有手机步骤；确实涉及手机的 SMTP2GO、SendGrid，以及 Mailgun 激活路径，本轮未获得完整、现行、注册专用的国家允许列表，不能编造一份国家表。**国家/地区可开户、电话格式可输入、能够收到验证短信是三个不同结论**。账单 Country 下拉、E.164 格式、Twilio 的短信/语音产品覆盖、向客户发送 OTP 的 API，以及第三方接码网站均不能替代开户验证证据；`+852`、`+853`、`+886` 也不能代表中国大陆 `+86`。

### 9.4 Plunk 是否适合当前邮件 Module

Plunk Cloud 支持 `smtp.useplunk.com`、587 STARTTLS / 465 TLS，以项目 Secret Key 作密码，从已验证域发送；官网明确支持既有邮件客户端。因此它可以加入 DMS 出站代发的后续验证候选，免费品牌标识对普通往来邮件的影响尚需实际发送核验。[SMTP 能力](https://www.useplunk.com/features/smtp)

**Plunk 自部署不是免费发送基础设施。**软件开源，官方自部署要求 Docker、域名和 **AWS SES 账户**；还要承担 SES、服务器及运维成本。AWS 注册/SES 生产授权的邮箱、手机和支付条件属于另一个账户流程，本轮没有将其视为已完成或免手机。若目标是减少 ANAS 组件和维护步骤，先比较托管 Cloud，暂不因“开源”新增一套 Plunk。[自部署前提](https://docs.useplunk.com/self-hosting/introduction)

当前建议：保留 Resend；将 Plunk Cloud 作为优先补充候选，Maileroo 在地区可用性与审核明确后再比较。SMTPfast/eusend 的免费域数只有 1，不能解决比当前 Resend 更多域的问题；Mailumi Free 没有 SMTP；Posthaste 偏低量事务邮件。SelfMailKit/SendexAPI 未确认可注册，SendGrid 不再按长期免费推荐。这里是选型结论，不代表已选定或接入新的供应商。

下一步：实际采用前，用拟使用的账户邮箱完成正常开通，若触发手机号验证，再核验该账号当时的国家列表和本人 `+86` 验证码。以上公开页面研究没有完成账号注册、手机号验证或生产审核；后续用户提供的已登录账号实测单独见 §9.5 和实施计划。


### 9.5 Resend 实际域名资格与 eusend SMTP 补查（2026-10-26）

用户已完成 Resend 与 eusend 的人员登录，并要求先测试 Resend、再测试 eusend。Resend 实际添加 `mail-poc.bengbu.cc.cd` 时拒绝，提示不允许免费公共域名；改用用户确认的 `mail-poc.hlong.wang` 后接受域对象，域名/DNS及真实发信进度记于仓库 `dev-docs/plans/mail-integration.md` §4.28。这个账号观察不能外推为所有公共后缀或其他服务一律拒绝。finance 原定邮件域未随隔离发送测试改变。

| 项目 | eusend 当前官方说明 | 验证边界 |
| --- | --- | --- |
| SMTP | `smtp.eusend.dev:465`，隐式 TLS；用户名 `eusend`、API Key 作密码；不支持 587 STARTTLS/25 | 可复用兼容 SMTP 出站类型，不能沿用 Resend 的 587 预设 |
| Free | 3,000 封/月、100 封/日、1 个域、1 个 Key、1 个 Webhook；日志保留 30 天 | 同一账号共享，多个收件人各计数；不是每个 NAS 人员各有额度 |
| 大小/认证 | SMTP 整信 15 MB；最多 20 附件、解码合计 10 MB；发送 Key 可限定 sending_access；test Key 不实际投递 | 真实验收必须使用 live Key；535 也可能表示发件域未验证，不能一律归因为密码错误 |

来源：[官方 SMTP](https://eusend.dev/docs/smtp)、[套餐](https://eusend.dev/pricing)、[Key 权限](https://eusend.dev/docs/getting-started/authentication)。以上为当日文档快照，未将价格/额度写成永久保证。

finance 的不发信网络探针已实际通过：Resend 587 STARTTLS 和 eusend 465 隐式 TLS 均完成证书校验、TLS 1.3 与 EHLO 250，分别通告 SIZE 41,943,040/15,728,640 字节及 PLAIN/LOGIN。未执行 AUTH/DATA，不能据此标记发送成功、可用额度、域验证或接收端 SPF/DKIM/DMARC 通过。

DMARC 按发件域生效，多地址与多个供应商可兼容；它不定义 mailbox_id、别名或收件箱授权。测试只涉及 `_dmarc.mail-poc.hlong.wang`，不自动改变根域策略。`p=none` 不要求接收方因 DMARC 失败而隔离/拒收；希望收到汇总报告还需有效 `rua` 地址。先验证各实际发送源，再决定更严格策略。[Resend DMARC 指南](https://resend.com/docs/dashboard/domains/dmarc)、[RFC 7489](https://www.rfc-editor.org/rfc/rfc7489.html)


2026-10-26 实测进展：Resend 的 hlong 测试域已 verified，587/TLS/AUTH/提交通过，但直达 Outlook 返回 550 mailbox unavailable；经原有 Cloudflare 转发的受控地址返回 521 Upstream error。eusend 独立 DKIM 已 verified，465 隐式 TLS/AUTH/提交通过，服务端 delivered 后用户确认实际到达 Outlook 垃圾邮件；接收端 SPF/DKIM/DMARC 原始结果仍待核验。不能把两家的 SMTP 接受都写成收件成功。eusend 建议的 send TXT/MX 与 Resend 同名 CNAME 冲突，实际未覆盖该 CNAME，当前使用 DKIM 对齐。

用户随后明确取消 SMTP2GO；其历史研究保留，但不再是一期预设或账号验收阻塞。当前产品发送范围为 Resend 与兼容 SMTP，本轮后者使用用户指定的 eusend。Cloudflare 对照测试的 521 表示上游错误，官方说明转发时会在 SMTP 会话中返回目标服务错误；具体上游根因仍不能只据该码确定。[Cloudflare Postmaster](https://developers.cloudflare.com/email-service/reference/postmaster/)
