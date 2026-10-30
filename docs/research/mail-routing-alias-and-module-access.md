---
doc_type: research
created: 2026-10-23
updated: 2026-10-24
evidence_as_of: 2026-10-24
---

# 邮件托管接入、个人别名与外部 Module 读取研究

本轮针对四项问题：邮件内容统一保存到 `data`；接入 Cloudflare Email Routing、SMTP2GO 等服务；门户自助别名与登录邮箱；其他 Module 读取或订阅邮件。沿用[邮件服务器比较](mail-server-anas-integration-research.md)和[托管服务比较](hosted-email-forwarding-services-research.md)的候选范围，不重新进行全产品排名。

**最新范围（2026-10-24）：用户已选择 Go + PostgreSQL + DMS + Roundcube，并取消 Cloudflare＋Gmail IMAP 和所有正文直投 NAS 的入口。** 一期收件选择 Worker/R2 或自管云持久中转，NAS 每 60 秒主动取信，可选通知触发立即取信；发件独立选择 SMTP2GO、Resend 或兼容 SMTP。25 检测只针对自管云 SMTP 入口，不再按 NAS 25 状态自动切换。用户已同意先做 Worker/R2 隔离验证，再复用接口验证自管云。正式规范与执行状态见[要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/mail-integration.md)和[计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/mail-integration.md)。

第 1—10 节保留 2026-10-23 的研究和原生实验基线，后续 Go PoC 20/20 结果见[管理层研究](mail-management-layer-feasibility.md)；其中早期失败和候选方向不代表当前最终状态。第 11 节核验服务接口，第 12 节记录最新常态中转与存取边界；第 13 节按用户新提出的云中转思路整理接入选项。其他产品没有重新核查。没有部署生产邮件 Module、修改 DNS、创建云端规则或执行真实公网收发。

**后续确认：用户要求自管云中转与 Worker/R2 都加入 Webhook，并选择“只通知有新邮件，NAS 立即拉取，继续每 60 秒补取”。** 第 14 节补开发难度，第 15 节比较 SMTP/IMAP 的衔接；统一确认与去重设计见架构 §12。它替代第 12—13 节中“推送仅为以后候选”的表述；具体账号、部署自动化与重试参数仍待落实。已取消的 Gmail、Forward Email 的历史比较及发信方案复核见[托管服务报告 §4.4—4.6](hosted-email-forwarding-services-research.md)。

## 1. 结论与实施取舍

| 问题 | 结论 | 需要 ANAS 补齐的部分 |
| --- | --- | --- |
| 全部邮件内容放 `data` | 可以，按用户本轮决定执行后续设计 | 完整持久卷清单、元数据与邮件一致备份、恢复验证 |
| Cloudflare 收件 + SMTP2GO 发件 | 可以组合；NAS 直收仍需公网入站 TCP 25 | 投递子域、目标验证、SMTP 凭据、配额提示；自助别名还需云端路由联动 |
| Portal 自助别名写 AD | 可行；优先评估现有多值字段 `proxyAddresses`，不急于新增 schema | 受控写入、唯一性、地址保留、发信权限、事件与失败恢复 |
| 一个账号管理所有别名邮件 | 邮件服务器原生别名即可共箱 | Portal 是管理入口，Webmail 是读写邮件入口；别名本身没有独立账号 |
| 登录 Portal 后打开邮箱 | 可通过 Webmail 的 OIDC/OAuth2 或受控代理认证链路实现 | 正确绑定邮箱；后端可用适当令牌或受控内部凭据，不能仅凭入口已有 Cookie 就认定完成邮箱登录 |
| 外部 Module 读邮件 | 通用首选 IMAP；需要 JSON API 时重点评估 Stalwart JMAP | 单独授权、撤销、断线补读、幂等和最小读取范围 |

在本节 2026-10-23 基线中，已先验证 **DMS + Roundcube** 的登录、别名收发与恢复，当时两项失败见第 9 节；后续 Go 原型已单独复验，见本文开头的最新状态。公网收发与 SMTP relay 仍未实测。原生 JSON 邮件 API、HTTPS 推送和 mailcow 在早期研究中的取舍，不能替代当前已选择的 Go 方案。

## 2. 邮件内容统一保存在 DATA_PATH

后续邮件 Module 的本地持久化至少覆盖：收件箱、已发送、草稿、附件、垃圾箱/隔离区、尚未投递的邮件队列，以及恢复这些内容必需的数据库、邮箱映射和状态。Webmail 若持久保存草稿或附件，也必须纳入邮件数据的保存与恢复清单，不能遗漏在容器可写层或 `USERDATA_PATH`。

具体挂载点依固定镜像核验，不直接把示例 Compose 当成完整备份清单。可重建索引可以单独声明重建方式，但不能在元数据缺失时仍声称“正文在磁盘上，所以恢复成功”。配置和 Secret 继续遵循仓库既有配置/凭据规范；恢复所需材料一并列入备份契约。

邮箱与目录用户通过 `anasIdentityAnchor` 的内部绑定关联；邮箱内部 ID 和目录布局应保持稳定。不能随 `mail` 或用户名变化新建存储目录，也不能未经确认把 anchor 直接用作路径：现有 `DIRKEY-R-010` 的路径例外只适用于 Nextcloud。具体邮件内部 ID 映射仍需 PoC。

这项决定约束 **ANAS 保存的邮件内容**。使用 Cloudflare/SMTP2GO 仍会让邮件经过第三方；选择外部邮箱暂存再拉取，还会产生第三方持久副本。不能把“保存到 data”解释成这些服务完全不接触或保存邮件。

对应设计已记录于[门户与邮件归属方案](../architecture/portal-account-mail-administration.md)。存储规则见[Module 开发规范](../developer/module-development.md)，身份约束见[目录身份键要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-identity-key.md)。

## 3. 托管服务如何接入

### 服务分工与支持范围

下表是协议层接入判断，不代表 ANAS 已有这些供应商的管理适配器。各套餐、价格与完整限制以[托管服务报告](hosted-email-forwarding-services-research.md)为准。

| 服务 | 可以承担什么 | 对本地邮件 Module 的接法与限制 |
| --- | --- | --- |
| Cloudflare Email Routing | 自有域名入站转发 | MX 指向 Cloudflare，再转到可达的 NAS 投递地址；不是 IMAP 邮箱 |
| ImprovMX、Forward Email、ForwardMX | 入站转发；部分套餐还提供发送或邮箱 | 可替换转发入口；逐套餐检查目标验证、规则 API、地址额度；不能假定免费包含 SMTP/存储 |
| SMTP2GO、Resend、Brevo、MailerSend | 外发 SMTP/API | 有 SMTP 权限的套餐可接 DMS relay；若只选 HTTP API，需额外适配，不是 Postfix 的直接 relay 配置 |
| SimpleLogin、addy.io | 隐私别名与回复中转 | 可转到 NAS；回复通常走其反向别名/联系人地址，不等同本地任意别名 SMTP 发信 |
| Purelymail；Forward Email 的邮箱套餐 | 完整外部邮箱，也可配置转发 | 可作为 IMAP 拉取来源；会增加远端邮件存储、凭据和供应商额度 |

### 推荐先验证的组合

```text
收件：外部发件人
        → Cloudflare（alice@example.com、shopping@example.com）
        → 唯一目标 box-42@in.example.com
        → NAS 邮件服务器 → DATA_PATH 中的同一个邮箱

发件：Webmail / 客户端
        → NAS 认证提交与发信身份检查
        → SMTP2GO → 外部收件人

读取：用户 Webmail / 获授权的 Module → NAS IMAP 或 JMAP
```

这里 `box-42` 只是投递目标示例，不是身份锚点，也不是密码或访问凭据。`example.com` 的 MX 指向 Cloudflare；`in.example.com` 的 MX 指向 NAS 的 DNS-only 主机名，不能再绕回同一 Cloudflare 路由。每个用户可有一个稳定目标，多条公开别名规则指向它。目标需先在 NAS 可接收验证信并完成 Cloudflare 验证。[Cloudflare 接入说明](https://developers.cloudflare.com/email-service/get-started/route-emails/)

NAS 可使用公网 IPv4 或 IPv6，但入站 TCP 25 必须实际可达。发信另经 SMTP2GO 的认证端口，DMS 支持外部 relay；其 LDAP 路径可评估 `DEFAULT_RELAY_HOST`。SMTP2GO Free 当前为 **1,000 封/月、200 封/天**，属于同一服务账号的共享额度，月额耗尽会拒绝新提交。供应商的域验证不替代 NAS 对“这个用户能否用这个别名发送”的检查。[Cloudflare IPv6 说明](https://developers.cloudflare.com/email-service/reference/postmaster/#ipv6-support)、[DMS relay](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-forwarding/relay-hosts/)、[SMTP2GO 免费规则](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)

若入站 25 不可达，可另选“Cloudflare → 外部邮箱 → DMS Fetchmail 主动拉取”。DMS 原生支持 IMAP/POP3 拉取；必须核验远端认证、每用户映射、容量及副本保留。其默认取回可能删除远端邮件，试验应显式配置 `keep`。这是网络受限的替代路径，不应在尚未检测 NAS 网络前默认增加。[DMS Fetchmail](https://docker-mailserver.github.io/docker-mailserver/latest/config/advanced/mail-fetchmail/)

普通 Cloudflare Tunnel 不能直接替代公网 SMTP 入站。Email Worker 可获得原始 MIME 邮件并自行经 HTTPS 传输，但要另外解决 NAS 离线、持久暂存、重试、重复投递和导入确认，复杂度明显更高，暂不建议作为默认方案。[Tunnel 协议要求](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/protocols/)、[Worker 邮件对象](https://developers.cloudflare.com/email-service/api/route-emails/email-handler/)、[官方存储示例](https://developers.cloudflare.com/email-service/examples/email-routing/email-storage/)

### 自助别名必须同时考虑云端入口

当主域 MX 在 Cloudflare 时，**仅把 `shopping@example.com` 写入 AD 不会创建云端路由**。若采用逐地址规则，邮件 Module 还需调用路由 API。当前 Routing 限制为每域 200 条规则、每账号 200 个已验证目标、入站单封 25MiB；是全体用户共享的边界，不能按每人 200 条设计。[路由 API](https://developers.cloudflare.com/api/resources/email_routing/subresources/rules/)、[服务限制](https://developers.cloudflare.com/email-service/platform/limits/)

建议的操作语义是：

1. 新增先完成地址占用和本地收件准备，再建立云端规则；两处都确认后才显示可用。
2. 移除立即撤销本地发信许可及新投递许可，再删除云端路由；保留原邮件和地址归属记录。
3. 超时结果未知时查实际状态，不能直接重放或显示成功。保存操作编号、期望状态和已完成步骤，重启后可对账。

这是待确认的跨系统设计建议，不是已实现流程。若不用云端转发、MX 直接指向 NAS，可以省掉云端规则联动。不要为了省规则，把全部用户的 catch-all 转到一个普通邮箱后只按邮件 `To` 头分发：Bcc 可能没有目标头，头字段也不能证明收件归属。原始信封收件人是否在转发后可靠保留，需要独立测试。

## 4. Portal、AD 与邮件别名的归属

### 可以写入 Samba AD，优先评估标准字段

建议优先验证 `proxyAddresses`，而不是立即新增自定义 schema。它是已有的多值地址属性；本仓库的主地址冲突检查也已读取其中的 `smtp:` 地址。示意：

```text
mail: alice@example.com
proxyAddresses: smtp:shopping@example.com
proxyAddresses: smtp:work@example.com
```

AD `mail` 继续保存系统生成的唯一主地址；别名不塞入 OIDC 单值 `email`。是否在 `proxyAddresses` 同时投影主地址及使用 `SMTP:` 主地址约定，应随选定后端统一，不依赖大小写表达未验证的权限规则。[Microsoft 属性定义](https://learn.microsoft.com/en-us/windows/win32/adschema/a-proxyaddresses)、[DMS v16.0.1 LDAP 示例](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/docs/content/config/account-management/provisioner/ldap.md)

Portal 提供表单，服务端从登录主体确定本人身份，再由邮件 Module 的受控逻辑检查允许域、配额、保留名、冲突和权限后写入。浏览器不直接连接 LDAP，也不持有目录或云服务管理凭据。LDAP 委派只覆盖约定 OU 和属性；用户不能提交任意目标 DN 或替别人增加地址。

但 `proxyAddresses` **只是地址列表，不是完整的地址管理系统**。它本身不提供跨用户原子唯一约束、删除后的地址保留、兼容旧址保护、仅收不发标记或云端操作状态。现有门户先查再写的冲突检查也不等于全局唯一约束。生产实现需选择一个可执行原子占用和保留的权威机制，并让主地址生成、个人别名和后台管理都遵守它。

若新增 `anasMailAliases` 一类字段，还要增加 OID/schema、ACL、迁移、目录事件以及既有冲突检查的适配；仅换成自定义字段不能解决上述问题。该名称仅作示例，不是仓库已有属性。没有 `proxyAddresses` 无法表达的具体要求前，不建议新增。

### 也可以由邮件后端原生管理别名

| 选择 | 数据归属 | 适用性与代价 |
| --- | --- | --- |
| AD 保存有效别名，邮件后端只读消费 | Portal 经邮件 Module 受控写 AD | 适合 DMS LDAP 路径；仍需补齐占用/保留及云端状态，不是零开发 |
| 后端原生别名为权威，Portal 调其管理接口 | 后端承担唯一性和别名生命周期；AD 如需展示，仅做单向投影 | 适合有成熟管理 API 的后端；需证明删除保留、系统主地址及身份绑定满足 ANAS 规则 |

两条路二选一确定写入权威，不允许 Portal 写 AD、用户又在后端独立改一套别名。可把后端界面设为只读或让两者通过同一受控写入路径。首期也不必为了展示而把全部后端状态同步回 AD。

mailcow 原生允许别名收件和授权发信，SOGo 可选别名身份；其普通用户自助的“临时别名”功能有明确生命周期，不能直接当成 ANAS 要求的永久个人别名及移除后保留。DMS 有 LDAP/文件配置路径，但不自带用户 Webmail 和完整自助管理门户。[mailcow 收发模型](https://docs.mailcow.email/models/model-sender_rcv/)、[临时别名](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-spamalias/)

### 一个账号如何使用所有别名

所有个人别名都投到同一个邮箱，不产生新账号、新密码或新收件箱。用户可在 Webmail 切换已授权的发件地址；服务器同时检查 SMTP 信封发件人和可见 `From`，不能只靠界面下拉框限制。

需要分类时，可用 Sieve（服务器端邮件过滤规则）把不同地址的邮件放进不同文件夹；依赖可信的原始投递收件人信息，不能仅看 `To` 头。**别名不是权限边界**：给一个外部 Module 此邮箱的完整凭据，通常意味着它可读全部别名的邮件。需要隔离时应采用独立服务邮箱，或服务器强制的文件夹读取授权；这不改变个人别名共箱的已确认模型。

## 5. 登录 Portal 后直接打开邮箱

用户界面建议保留两个清楚的动作：“管理别名”留在 Portal；“打开邮箱”进入 Webmail。DMS 只提供邮件服务，需要另选 Webmail，例如 Roundcube；这是新增依赖，其数据库、配置和草稿恢复也要纳入方案。

```text
Portal 中点击“打开邮箱”
    → Webmail 发起自己的 OAuth/OIDC 登录
    → 已配置 IAM 复用当前登录会话
    → Webmail 取得面向邮件服务的 access token
    → IMAP/SMTP 校验令牌并打开对应邮箱
```

Roundcube 官方支持通用 OAuth2 配置，邮件端需支持 XOAUTH2；DMS/Dovecot 提供对应认证配置路径。因此可实现不再次输入密码的跳转，但不是给普通 URL 加一个用户名就完成。本次已验证 LLNG 2.23.4、DMS 16.0.1 与 Roundcube 1.7.5 的真实登录和协议令牌匹配；邮箱使用手工测试预配。Casdoor、自动预配、完整退出及禁用仍未验证。[Roundcube OAuth2](https://github.com/roundcube/roundcubemail/wiki/Configuration:-OAuth2)、[DMS OAuth2](https://docker-mailserver.github.io/docker-mailserver/latest/config/account-management/supplementary/oauth2/)

Portal 不把自身 cookie、ID token 或密码拼进链接。Webmail 应注册独立 client，使用受验证的回调地址，并以稳定主体绑定本地账号。还需核验 Webmail 自己的用户偏好、通讯录和发信身份不会因邮箱改址产生另一个本地账号；仅后端邮件未丢失不足以证明完整身份连续性。

mailcow 则已有通用 OIDC 用户界面链路，但其官方做法对 IMAP/SMTP 客户端使用应用密码。这说明“浏览器 OIDC 登录”和“邮件协议登录”可以是不同凭据体系，不能假定用户的浏览器令牌可直接给任意客户端用。[mailcow OIDC](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/)

## 6. 外部 Module 如何读取和订阅

### 协议与候选能力

| 方式 | 能力 | 适用候选与限制 |
| --- | --- | --- |
| IMAP over TLS | 列目录、检索、读取正文/附件；IDLE 可通知变化，定期查询可补读 | DMS、Stalwart、mailcow、Mailu 的通用路径；固定版本探测实际能力和权限 |
| JMAP over HTTPS | JSON 邮件查询、读取、增量变化；推送通知后再拉取内容 | Stalwart 原生支持；本轮未确认其余三者有同等原生接口 |
| Dovecot 通知插件 | 可用插件生成新邮件等事件并接 HTTP 处理程序 | 需要额外配置/代码和可靠性设计，不是 DMS 已接好的统一 webhook |
| Cloudflare Email Worker | 收到新邮件时读取信封信息及原始 MIME | 只覆盖经过该入口的新信；不是 NAS 历史邮件、已发送或草稿查询 API |
| 邮件产品管理 API | 创建账号、别名、域、配额等 | 不应等同读取正文接口；例如 Mailu API 文档描述的是管理配置操作 |

来源：[IMAP 标准 RFC 9051](https://www.rfc-editor.org/rfc/rfc9051.html)、[JMAP Core](https://datatracker.ietf.org/doc/html/rfc8620)、[JMAP Mail](https://datatracker.ietf.org/doc/html/rfc8621)、[Stalwart JMAP](https://stalw.art/docs/http/jmap/)、[Dovecot 通知插件](https://doc.dovecot.org/2.4.3/core/plugins/push_notification_lua.html)、[Mailu 管理 API](https://mailu.io/2024.06/api.html)。协议说明不等于固定 DMS 镜像已经带有某个 Dovecot 版本或插件，仍需镜像内核验。

**首期建议 IMAP + IDLE 唤醒 + 定期补读。** 不要求为所有 Module 新建邮件事件中心。消费端保存邮箱/文件夹、`UIDVALIDITY` 与 `UID`，用 `BODY.PEEK` 等方式避免读取时无意标记已读；连接恢复后按游标检索遗漏。`Message-ID` 不能当成全局唯一键，UID 也不能跨文件夹或 `UIDVALIDITY` 变化直接复用。业务处理与游标落盘应可幂等重试。[IMAP 标准](https://www.rfc-editor.org/rfc/rfc9051.html)

如果业务以 HTTP/JSON 为主，Stalwart 的 JMAP 更直接：用 `Email/query`、`Email/get` 读取，使用 changes/state 跟踪增量；状态过旧、无法计算变化时重新建立快照。它还支持 SSE 长连接通知和 `PushSubscription` HTTPS 回调。推送告知状态变化，不应当成携带完整邮件、永不丢失的任务队列；断线或重试耗尽后仍要补读。[JMAP Mail](https://datatracker.ietf.org/doc/html/rfc8621)、[Stalwart 推送](https://stalw.art/docs/http/jmap/push/)

SMTP2GO 的投递/退信事件是外发状态，不是本地收件箱读取权限。Cloudflare 路由 API 管的是地址规则；需要新邮件正文时用 Worker，但仍不能替代邮箱历史查询。外部 Module 也不应直接挂载并解析邮件服务的数据目录，以免绕过授权、依赖后端内部布局或读到不一致状态。

### OIDC 登录后，读取权限怎样建立

OIDC 回答“是谁登录”，邮件协议授权回答“允许读哪个邮箱、哪些文件夹、能否修改”。`openid/profile/email`、Portal 登录成功和 anasd service token 都不自动授予邮箱读取权限；ID token 不能直接冒充 mail access token。

建议按用途选一种授权方式：

| 用途 | 建议路径 | 必须验证的边界 |
| --- | --- | --- |
| 用户授权某个 Module 读自己的邮件 | Module 注册独立 OAuth client，用户完成授权码流程；允许时使用 PKCE 和可撤销 refresh token | 邮件服务器验证 issuer、有效期、受众/客户端、scope 和主体映射；Provider 签发及服务端执行权限均要验证 |
| 持续处理固定业务邮箱 | 专用服务主体/应用凭据，仅授权该邮箱或文件夹 | 每 Module 独立、可轮换和撤销；不用个人 AD 主密码或全域管理员凭据 |
| 只读个人邮箱里的指定文件夹 | 单独主体加服务器端 ACL，或后端明确支持的等效限制 | 必须从服务器拒绝其他目录和写操作；客户端自愿只读不算授权隔离 |

Dovecot 支持 OAuth SASL 认证与令牌校验；但 scope 名称不是各产品通用的“只读邮箱开关”。即使名为 `mail.read`，也要证明服务端据此拒绝写入；普通邮箱 owner 的 OAuth 登录可能仍有完整权限。Dovecot ACL 可以表达目录可见/读取等权限，技术主体如何接入仍需固定版本测试。`client_credentials` 也不会天然代表任意用户。[Dovecot OAuth2](https://doc.dovecot.org/2.4.4/core/config/auth/databases/oauth2.html)、[Dovecot ACL](https://doc.dovecot.org/2.4.4/core/plugins/acl.html)、[OAuth SASL 标准](https://datatracker.ietf.org/doc/html/rfc7628)

用户禁用、授权撤销和密码事件需要按已有目录事件要求处理：停止新访问、撤销可撤销凭据，并处理既有长连接与缓存。不能只等 access token 自然过期。IMAP IDLE 连接、Webmail 会话、Module refresh token 是不同对象，都要列入验证。

## 7. 对后端选择和现有实现的影响

当前已实现的是目录主邮箱规则与门户展示，尚无已交付的邮件 Module、个人别名 CRUD、邮件 OAuth Consumer 或 Module 读取授权链路。主地址规则读取 `mail` 和 `proxyAddresses` 做冲突检查，但没有因此自动得到全局地址唯一性或保留机制。

2026-10-23 当前工作区的 Samba 默认事件属性已包括 `mail`、锁定和到期等字段，仍未包括 `proxyAddresses`。若选择 AD 别名路径，需补齐相关事件和完整对账；密码撤销也不能只看默认列表。本地检查涉及 `modules/samba_dc/hook/main.go`、`modules/samba_dc/anchor_worker/mail_policy.py`、`modules/user_portal/portal/create.go`。基线为 `058d5c2ce87253897e1619b762f42cef9c3e71ee` 加本轮开始时已有的未提交改动，未把其他任务的改动认定为本轮交付。

| 候选 | 对本轮需求有利的能力 | 先解决的问题 |
| --- | --- | --- |
| DMS v16.0.1 | Samba LDAP 示例、标准 IMAP、外发 relay、Fetchmail；协议组合直接 | 稳定 Webmail 身份、可见 From 授权、地址保留与原子占用、别名 UI、目录撤权；本次仅固定配置下的 Webmail/OAuth 与恢复已通过 |
| Stalwart v0.16.26 | 原生 JMAP 与推送；适合其他 Module 用 HTTP 读邮件 | 固定源码的原生 LDAP 同步以主邮件地址查找内部账户；改址可能另建账号，别名同步路径未删除 LDAP 中已移除的地址；需验证其他原生管理路径能否补齐 |
| mailcow | 成套管理、别名收发、SOGo、通用 OIDC 与应用密码 | AD 稳定身份和主地址维护、永久自助别名规则、撤销应用密码；整套运行与备份成本更高 |
| Mailu | 管理 API 与标准邮件协议 | 原生 AD/稳定身份链路尚未确认；管理 API 不解决邮件内容读取授权 |

前两项固定源码结论沿用 [2026-10-22 实施评审](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-10-22-mail-module-implementation-review.md)，不是对所有未来版本的断言。Stalwart 证据为 [LDAP 映射](https://github.com/stalwartlabs/stalwart/blob/v0.16.26/crates/directory/src/backend/ldap/lookup.rs)及[账户同步](https://github.com/stalwartlabs/stalwart/blob/v0.16.26/crates/common/src/cache/directory.rs)；当前在线产品文档所述能力与此固定源码基线分别看待。

## 8. 建议的验证顺序与复杂度

以下保留完整落地的排序建议；用户随后确认优先进行组合验证，其独立需求/计划与结果见第 9 节。这里的其余步骤尚不是已批准的生产实施计划。后端选定后，邮件 Module 的需求与配对计划再承接验收条件；既有身份、事件、主邮箱要求继续适用。

| 顺序 | 验证内容 | 产出/通过条件 |
| --- | --- | --- |
| 1 | NAS 网络与最小收发 | 实测 IPv4/IPv6 入站 25、SMTP relay 出站；测试子域与单用户邮件落入 DATA_PATH；确定直投还是外部邮箱拉取 |
| 2 | 身份与数据恢复 | 主地址变更仍复用原邮箱；所有恢复必需数据有归属；备份恢复后邮件、队列和用户绑定一致 |
| 3 | 单用户多个别名 | 同箱收件、合法别名发信、伪造发件拒绝、移除停止新收发且保留旧邮件与地址归属；并发申请同址只能成功一次 |
| 4 | Portal 与 Webmail | 自助增删及状态反馈；已有 IAM 登录后打开本人邮箱；改址不生成新的 Webmail 账号，退出/禁用生效 |
| 5 | 外部 Module | 最小读取授权、附件、断线补读、重复事件、撤销与只读拒写；API 路径另验证 JMAP 状态过期和推送遗漏 |
| 6 | 云端别名自动联动 | 创建/删除/超时重启、规则额度耗尽、目标验证失败均能正确恢复；恢复旧备份不会复活已撤销地址 |

复杂度评估：单用户固定地址的 SMTP relay/IMAP 试验为**中等**，涉及网络、TLS、DNS 和持久存储；完整“Portal + AD + 邮件后端 + 云端别名 + OAuth + 外部 Module 授权”为**高**，主要成本是多处状态一致性、稳定身份、撤权和失败恢复。完整方案实施前需要按仓库规则确认；本轮没有把高复杂度候选设计当成已确认架构。

较简单的起点是固定少量测试别名、手工配置云端规则、标准 IMAP 读取，只验证路径与后端适配；这不是以手工配置替代最终已确认的自助别名需求。若省去云端入口、由 NAS 直接收信，可减少一处同步，但仍须承担公网收件可用性。若需要添加通用事件代理、Worker 邮件桥或上游补丁，应先用实验证明原生路径无法满足具体要求，再单独评估。

本报告的协议与供应商能力以官方文档及固定源码为依据。第 9 节另外记录本次真实隔离实验；公网 SMTP、JMAP、云端 API、NAS 入站 25、容量及未测试后端不作验收通过结论。

## 9. DMS + Roundcube 隔离验证后的选型判断

**暂不确定该组合优于成套方案。** 2026-10-23 在用户指定的 finance 上完成原生软件实验：DMS 16.0.1、Roundcube 1.7.5、LLNG 2.23.4、Samba 4.23.6。逐需求结果、镜像/源码摘要、重试和恢复证据由[配套计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/mail-webmail-poc.md)承载，不在本报告重复建立验收台账。

| 验证问题 | 实测结论 | 对实施的影响 |
| --- | --- | --- |
| IAM 登录后打开邮箱 | 通过；Alice/Bob 都仅提交一次 IAM 密码进入实际邮箱，真实授权码 + PKCE 与 IMAP/SMTP 令牌认证成功 | DMS 仍需预先有邮箱；本次没有证明自动 AD 预配 |
| 令牌身份匹配 | 无效令牌、Alice 令牌请求 Bob 邮箱均在 IMAP 和 SMTP 被拒绝 | 能支撑后续协议接入验证；不等于已验证独立 Module 的最小权限、scope 或撤销 |
| 两个个人别名共箱与界面发信 | 通过；Roundcube 创建两个身份后实际发送，Bob 收到的 From 与 Return-Path 正确 | 原生 identities 是发件显示设置；不能把可编辑下拉框当服务器授权来源 |
| 未授权/已移除地址 | 信封地址拒绝通过；合法信封搭配他人或已移除别名的可见 From 仍被接受 | 当前最小配置不满足完整发信授权；需补充并实测服务端策略 |
| 主地址变化 | AD anchor 和真实 OIDC sub 不变，固定邮箱目录及旧邮件仍完整；Roundcube user_id 从 1 变成 3，旧偏好和别名身份不随新用户出现 | 当前原生 email 登录映射不能满足稳定 Webmail 身份；需要验证额外映射/迁移方案 |
| 全部内容放 data 并恢复 | 明确挂载，无匿名持久卷；停止全部服务后从新目录恢复，邮件原文/附件/文件夹/标志和 SQLite 用户设置一致；原 hold 邮件只投递一次 | 同版本停机恢复可行；正式 Module 要把邮件、队列、Webmail 数据库及必要配置/密钥一起纳入契约 |

发信缺口与固定 DMS 的实现相符：`SPOOF_PROTECTION` 设置 Postfix `smtpd_sender_login_maps`，并不单独证明可见 From 已与登录主体关联。本次关闭了 Rspamd 等过滤组件，结论只针对已测配置；不能据此声称 DMS 的所有原生扩展方式都无解，也不能未经反例验证就认为打开 Rspamd 足够。[DMS v16.0.1 spoofing.sh](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/target/scripts/startup/setup.d/security/spoofing.sh)、[Rspamd 配置说明](https://docker-mailserver.github.io/docker-mailserver/latest/config/security/rspamd/)

主地址问题同样限定在本次 `oauth_identity_fields = ['email']` 的原生配置。稳定邮箱目录解决的是正文归属，Roundcube 还维护独立用户 ID、身份、签名和偏好；这两层都要延续。后续可以评估稳定的内部邮箱登录标识或受控用户映射，但不能直接把目录 anchor 暴露为邮件地址/存储路径，也不能在没有失败恢复设计时手工改多份数据库。

### 与成套方案比较

| 维度 | DMS + Roundcube（本轮实测） | mailcow + SOGo（文档及固定源码核验，未部署） | Mailu（2024.06 文档核验，未部署） |
| --- | --- | --- | --- |
| 新增邮件组件 | DMS + Roundcube；小规模实验可用持久 SQLite，共用既有 IAM/代理 | 已组合邮件服务、管理 UI、SOGo、MariaDB、Redis、反垃圾/反病毒等 | 多容器成套服务，提供管理 UI、管理 API 和 Webmail 配置 |
| 统一登录 | 真实 LLNG→Roundcube→DMS 令牌链路通过；仍需账号预配 | Generic OIDC 支持登录后进入 UI/SOGo；其他 IMAP/SMTP 客户端使用应用密码 | 本轮没有证明 ANAS AD/OIDC 稳定身份链路，不能把 Webmail 自带登录当统一登录验收 |
| 别名管理 | 文件/LDAP 后端，Roundcube 发信身份另存一份；Portal 自助与状态联动需开发 | 已有别名与授权发送模型、管理界面/API；仍须核验 ANAS 的地址保留和主地址规则 | 有管理接口及发送权限设置；完整 From 边界和个人别名规则仍须实测 |
| 数据恢复 | 本次同版本停机完整恢复通过；组合清单和一致性由 ANAS 维护 | 原生备份工具覆盖 vmail、crypt、Redis、Rspamd、Postfix、MySQL 等；仍需实际空环境恢复 | 需要按选定拓扑列出邮件、管理数据库、Webmail 与队列的完整保存集合；未实测恢复 |
| 主要维护代价 | 容器数少，但要自己维护身份连续性、发信策略、AD/别名映射及组合升级 | 组件更多，集成管理功能更完整；ANAS 接入和稳定身份仍不能省略 | 管理能力较完整，但尚缺本仓库身份/恢复的实测证据 |

来源：[mailcow Generic OIDC](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/)、[别名与授权发送](https://docs.mailcow.email/models/model-sender_rcv/)、[备份组件](https://docs.mailcow.email/backup_restore/b_n_r-backup/)、[恢复流程](https://docs.mailcow.email/backup_restore/b_n_r-restore/)、[Mailu 配置](https://mailu.io/2024.06/configuration.html)、[Mailu API](https://mailu.io/2024.06/api.html)。这些是产品能力依据，不代表本次已运行这些成套产品或测得其性能。

mailcow 默认配置官方最低资源是 **6 GiB RAM + 1 GiB swap**，其数据库、缓存、Webmail 和过滤组件带来更多运行/恢复对象。finance 总内存约 3.3 GiB，本轮没有在这台承载现有服务的主机上追加默认 mailcow。Mailu 文档给出的最低值随是否启用 ClamAV 变化，也不能据此推断本仓库完整方案容量。[mailcow 系统要求](https://docs.mailcow.email/getstarted/prerequisite-system/)、[Mailu 系统要求](https://mailu.io/2024.06/compose/requirements.html)

**当前建议：保留 DMS + Roundcube 为可行候选，但不直接进入生产 Module 实施。** 若当前优先级是少量组件、标准 IMAP、复用既有 IAM，它的基础链路有实测支撑；若优先级是完善的多用户自助管理和减少自研地址管理，则应先用相同失败用例验证 mailcow + SOGo。两者都必须证明主地址变化和发信权限，不能因成套产品功能较多就假定自动符合 ANAS 身份规则。

下一步：先核验稳定 Webmail 身份及可见 From 授权的最低成本方案，再确定后端。若需要新增持久同步、独立策略服务或上游补丁，先给出具体范围与维护成本并确认；公网中转、自助别名和外部 Module 最小读取授权在后端确定后分别承接。

## 10. mailcow + SOGo 固定源码复核

2026-10-23 继续比较用户关心的两个失败场景，固定 [mailcow 2026-10](https://github.com/mailcow/mailcow-dockerized/releases/tag/2026-10)，提交 `55982ac74bfd35581cc44b122ab5fc659e3ae90a`。本节是源码审阅，不是浏览器、SMTP 或恢复实测；执行基线、证据位置和未执行项仍由[配套计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/mail-webmail-poc.md)记录。

### 统一登录与主地址变化

原生链路是 IAM 授权码登录 mailcow UI，再由其会话和 Nginx 内部认证进入 SOGo。`sogo-auth.php` 输出受控身份及内部 SSO 凭据，Dovecot 的 mailcow 认证入口对来自 SOGo 的请求检查该凭据。这与 DMS 的 IMAP OAuth 令牌链路不同；既有门户会话能否无二次密码进入本人邮箱，仍要真实浏览器验证。[OIDC 登录实现](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/inc/functions.inc.php#L2881)、[SOGo 会话入口](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/sogo-auth.php)、[邮件认证入口](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/conf/dovecot/auth/mailcowauth.php)

**仅更改 AD 主地址，原生 OIDC 不会自动把旧邮箱改名。** `verify-sso` 以 UserInfo 的 `email` 查询 `mailbox.username`，查不到时按配置创建邮箱或拒绝登录；该分支没有按稳定 `sub` 关联旧邮箱。因此即使 AD anchor 和 OIDC sub 不变，也不能靠再次登录证明原有状态延续。

mailcow 已有独立的 `mailbox_rename` 流程，界面通过 `edit/rename-mbox` 调用：更新邮箱及关联表，移动邮件目录，再调用 `sogo-tool rename-user`。它提供了比重新建号更完整的原生迁移入口，但接入 AD 事件仍需 ANAS 维护稳定归属与调用顺序。本版本入口接受一个 `domain` 和旧、新 local part，不能将它描述成已覆盖跨域主地址变更；该内部路由也没有出现在所读 OpenAPI 中。[改名实现](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/inc/functions.mailbox.inc.php#L3537)、[实际 API 路由](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/json_api.php)、[OpenAPI](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/api/openapi.yaml)

源码还显示，SQL 事务先提交，邮件目录移动与 SOGo 改名随后执行；调用方没有检查后两步的返回值。Docker API 会返回命令成功或失败，PHP 包装层也会返回错误文本，但改名分支未据此回退或阻止成功提示。**这是已观察到的错误处理缺口，实际故障后状态尚未复现**；不能把界面“改名成功”当成邮件、偏好、签名和身份全部保留的证明。[Docker API 执行](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/Dockerfiles/dockerapi/modules/DockerApi.py#L345)、[PHP Docker 包装](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/web/inc/functions.docker.inc.php)

### 别名与可见 From

原生 Postfix 通过 `sender_allowed`、别名去向和 `sender_acl` 检查信封地址；SOGo 的 SQL 用户源提供别名，减少了另外维护 Webmail 地址列表的工作。但这仍不是可见 From 与登录用户的授权证明。[Postfix SQL 映射](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/Dockerfiles/postfix/postfix.sh#L318)、[SOGo 用户源](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/Dockerfiles/sogo/bootstrap-sogo.sh)

默认 Rspamd 的 `SPOOFED_UNAUTH` 明确排除 `MAILCOW_AUTH`；后者由存在认证用户名触发。默认还有伪造地址的评分规则，但评分不等于逐用户的硬性发信许可。由这些配置可判断：**不能用默认反冒用规则证明“合法信封 + 他人/已移除别名 From”一定被拒绝**；是否最终接受仍须保留 Rspamd 后实际投递验证，当前没有填写失败或通过结论。SOGo 另走内部 SMTP 588，外部客户端通常走 587，两条链路均需观察。[默认规则](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/conf/rspamd/local.d/composites.conf)、[认证符号实现](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/conf/rspamd/lua/rspamd.local.lua)、[SMTP 入口](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/data/conf/postfix/master.cf)

### data、恢复与外部 Module

该版本 Compose 声明 18 个服务，正文、队列、数据库、加密材料等默认在 Docker 命名卷中；仓库已有的 `data/` 目录主要承载配置和附属文件，**备份它不等于备份全部邮件**。ANAS 仍需将保存集合明确绑定到 `DATA_PATH`。如果改成直接 bind mount，上游备份脚本按命名卷查找数据的逻辑也不能原样套用；实验应完整停机后归档配置及全部状态，并在空目录恢复验证。[固定 Compose](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/docker-compose.yml)、[备份脚本](https://github.com/mailcow/mailcow-dockerized/blob/55982ac74bfd35581cc44b122ab5fc659e3ae90a/helper-scripts/backup_and_restore.sh)

外部 Module 可按官方应用密码路径使用 IMAP，结合 IDLE 通知及定期补读。浏览器 OIDC 会话、内部 SOGo SSO 凭据、管理 API Key 和外部 Module 的授权是不同边界；内部共享凭据不能下发给 Module，管理 API 也不能当成用户邮件内容 API。应用密码是否能满足本系统的只读、文件夹及撤权粒度仍未验证。[外部客户端认证](https://docs.mailcow.email/manual-guides/mailcow-UI/u_e-mailcow_ui-generic-oidc/#authentication-for-external-mail-clients-imap-sieve-pop3-smtp)

**目前没有足够依据认为更换 mailcow 就能消除 DMS 的两项适配成本。** mailcow 的优势是原生别名管理、SOGo 和改名入口；额外成本包括整套服务、AD 与邮箱状态协调、改名失败处理及 data/备份适配。下一步仍是两个失败场景的实测，随后才决定是否值得采用整套后端。默认完整实验需资源足够的 Linux 主机；不把关闭过滤组件后的结果当成默认 mailcow 的发信授权结论。

## 11. 2026-10-24 外部服务纳入一期

本节原始直投数据流保留作历史研究；最新产品入口见第 12—13 节，不能再按本节的 NAS 直投示例部署。

本轮新增的是正式需求和实施计划中的接入功能，不是已部署服务。接入沿用 Go 统一授权、AD 地址归属、PostgreSQL 操作状态、DMS 队列和 Roundcube 界面；云服务不会成为人员登录或邮箱身份的权威。

### 官方接口与免费套餐核验

| 服务 | 已核验的接法 | 本次观察到的边界 |
| --- | --- | --- |
| Cloudflare Email Routing | 路由 REST API 可维护公开地址到已验证目标的规则；管理页配置目标和状态 | 每域 200 条规则、每账号 200 个目标，单封入站 25 MiB；目标尚未验证时规则不启用 |
| SMTP2GO Free | `mail.smtp2go.com`，SMTP 用户/密码；本方案选要求 TLS 的 587 或 2525，也可按配置使用隐式 TLS | 1,000 封/月、200 封/天；超过日额可由供应商排队，月额耗尽后的新提交被拒绝，不由其保存等待下月；账号最多 5 个发送域或单独发件地址 |
| Resend | `smtp.resend.com`，用户名 `resend`，API key 作密码；587 为 STARTTLS，465 为隐式 TLS | Free 事务邮件 3,000/月、100/天，发送和接收共用额度，多收件人分别计数；当前 3 个已验证域；需先验证发送域 |

依据：[Cloudflare 规则与目标](https://developers.cloudflare.com/email-service/configuration/email-routing-addresses/)、[Cloudflare 限制](https://developers.cloudflare.com/email-service/platform/limits/)、[规则 API](https://developers.cloudflare.com/api/resources/email_routing/subresources/rules/methods/create/)、[SMTP2GO SMTP 参数](https://www.smtp2go.com/setup/)、[SMTP2GO Free](https://support.smtp2go.com/hc/en-gb/articles/223087947-Free-Plan)、[Resend SMTP](https://resend.com/docs/send-with-smtp)、[Resend 配额](https://resend.com/docs/knowledge-base/account-quotas-and-limits)。数字是本次核验快照；实现显示来源和核验时间，以实际账号为准，不承诺免费额度永久不变或每个本地用户各享一份。

DMS v16.0 文档已有默认 relay、按发件域选择 relay 和凭据映射；因此 SMTP2GO、Resend 与其他认证 SMTP 服务可以共用一条出站配置路径，不需要 Go 转发邮件正文或新增 HTTP 发信服务。固定制品仍须验收这些配置与当前 Go 授权适配的组合。[DMS relay 文档](https://docker-mailserver.github.io/docker-mailserver/v16.0/config/advanced/mail-forwarding/relay-hosts/)

### 首版数据流与地址撤销

```text
收件：外部发件人 → Cloudflare 公开地址规则
                    → 每地址独立 route-id@in.example.com
                    → DMS 收件映射 → 固定 mailbox_id 的邮件数据

发件：Roundcube → DMS + Go 发件授权 → SMTP2GO 或 Resend → 外部收件人
管理：Go Web → PostgreSQL 操作记录 → AD / DMS / Cloudflare 受管规则
```

每个公开地址使用独立投递目标是本次方案选择，不是 Cloudflare 的强制规则。它修正第 3 节“同箱全部地址共用一个投递目标”的简化示例：别名移除后，Go 可先撤销它的本地投递目标，再重试失败的云端规则删除；剩余有效地址不受影响。若共用目标，Bcc 或缺少可信原始信封信息的邮件无法可靠判断原来使用了哪个别名，不能靠 To 头补救。新增目标会消耗验证及账号额度，应在申请地址前提示，不以 catch-all 隐藏这一限制。该设计满足既有地址撤销要求，仍待实际 Bcc/云端残留规则反例验证。

本地邮箱与公网入口分别记录就绪状态。先配置本地目标收件，再由授权人员在已就绪的本地箱完成验证信操作，最后启用云端公开规则；避免把“尚未验证所以不能打开任何邮箱”变成验证死循环。目标只能指向受管投递域，不能由普通用户任意改为外部邮箱。规则只管理本 Module 登记的对象，状态未知时先回读，未经纳管的现有规则保留原样。

直投仍要求投递域的公网 TCP 25 可达。Cloudflare 支持 IPv6 投递，但没有公网 IPv4 本身不能证明 IPv6 可用。入站转发会改变信封发件人，并保留可见 From；转发认证与失败通知有额外边界，不能把 Routing 当成可无限保存离线邮件的云邮箱。[Cloudflare Postmaster](https://developers.cloudflare.com/email-service/reference/postmaster/)

### DNS、发送结果与恢复

Cloudflare 收件主域 MX 与 SMTP2GO/Resend 的发送域验证分开维护。Resend 的发送退信子域可能需要 MX/TXT，也可能由新域配置生成 CNAME，应使用该账号页面给出的实际记录；不要套用旧模板覆盖主域 MX 或让同名 CNAME 与现有记录冲突。Resend 入站是另一项功能，在后续降级研究中作为候选核验，未获选或启用。[Resend DNS 冲突说明](https://resend.com/docs/knowledge-base/how-do-i-avoid-conflicting-with-my-mx-records)

Go 的授权在 DMS 提交侧执行，云端整域验证不能允许用户冒用同域其他人地址。外部收件应核对正文、附件、公开 From、回复回箱及认证结果；供应商可能改写 Return-Path，不能据此重建邮箱身份。管理面区分本地排队、供应商接收、已知退信及结果未知；Sent 不代表对方已经收到，SMTP 接收确认丢失也不能承诺恰好一次。

免费账号耗尽、SMTP 认证错误和云端 API 限流按实际结果处理，不自动升级套餐或轮询其他免费账号。临时拒绝可由本地队列重试，永久拒绝明确反馈；不能将供应商已拒绝的邮件写为“云端排队”。切换代发服务需要明确处理待投递队列，不能对未知接收结果立即换服务重发。

配置、Secret 引用、投递映射、云端规则归属和未完成操作纳入现有恢复集合。恢复后先核对云端当前状态，不用旧快照重建已经撤销或被其他管理员改变的路由。Cloudflare API token 采用所需范围，规则写操作可使用 `Email Routing Rules Write`，不因维护规则就默认索取 Global API Key 或 DNS 写权限。[Cloudflare 规则权限](https://developers.cloudflare.com/api/resources/email_routing/subresources/rules/methods/create/)

本节初版只覆盖直投；用户随后明确追加每分钟拉取，以下第 12 节替代“25 不通就只记录阻塞”的旧结论。Worker/R2 等具体新增依赖仍需确认；Resend 入站 Webhook、营销发送及自动费用切换不纳入。两组真实云端互通尚无账号/域名基线，当前只核验官方接口，未连接生产账号、未发送测试邮件。

## 12. 常态云中转与每分钟取信

### 最新取舍

用户明确取消 Cloudflare＋Gmail IMAP、Cloudflare 直接投递 NAS、其他端口直投以及正文 Webhook 直推 NAS。NAS 不保证在线，因此常态入口必须先独立持久接收；不再等 NAS 25 不通后才切到存信源。旧 R-051/R-053/R-055 按用户决定废弃，60 秒拉取、恢复、可信分箱及通知确认保留。

```text
方案一：Cloudflare Routing → Email Worker → 私有 R2
                                              ↑ NAS HTTPS 取信 / 最终回执
方案二：Cloudflare Routing → 云 Postfix → 云 Go 持久暂存
                                              ↑ NAS HTTPS 取信 / 最终回执
本地：NAS Go → DMS → Roundcube
外发：Roundcube → DMS（Go 授权）→ SMTP2GO / Resend / 兼容 SMTP
```

两种来源都可在持久保存后发“有新邮件”的通知，NAS 立即取信；通知无入口/失败时继续每 60 秒补取。直接推送正文已取消，可选通知不构成 NAS 在线依赖。配置引导详见[架构 Tutorial](../architecture/portal-account-mail-administration.md#_13-邮件服务配置-tutorial)。

### Cloudflare 能力与仍需验证的边界

Routing 自身不提供 IMAP、POP3 或历史原文下载。Worker 提供真实信封 message.to 与 message.raw，可把元数据和原文一并保存到私有 R2；NAS 调用受认证接口枚举、下载及确认。普通邮件头不能替代信封目标，移除别名后按本地当前映射拒绝/隔离新投递。[Worker 接口](https://developers.cloudflare.com/email-service/api/route-emails/email-handler/)、[R2 API](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/)

以一个 R2 对象同时发布原文和信封元数据，可减少“只有索引没有正文”的中断状态；通知回执与最终交付回执分开条件写入，避免迟到通知覆盖完成状态。这是待验证实现选择：本地模拟器可验证接口及重启持久性，不能证明真实 SMTP 在 Worker/R2 写入失败时如何响应或重投，后者必须云端实测。setReject 是永久拒绝，不能用它假装存储故障的临时重试。[拒收语义](https://developers.cloudflare.com/email-service/api/route-emails/email-handler/#reject-emails)

R2 需开通订阅并受存储/请求计费约束，Worker 有运行时限制。收益是 NAS 只需出站 HTTPS，代价是 Worker、云存储、两端进度、确认和恢复；完整集成复杂度仍高，但省去 NAS 直投与自动来回切换。[R2 开通](https://developers.cloudflare.com/r2/get-started/)、[价格](https://developers.cloudflare.com/r2/pricing/)、[Workers 限制](https://developers.cloudflare.com/workers/platform/limits/)

### 云入口检测、显式切换与恢复

自管云必须从独立外网检查投递域 MX/A/AAAA、TCP 25 和 SMTP；无探测点显示未知。Worker/R2 不执行 NAS 25 探测；Worker 默认限制向外 TCP 25，不能拿它充当云 SMTP 探测器。[TCP 限制](https://developers.cloudflare.com/workers/runtime-apis/tcp-sockets/)

只有管理员显式切换来源，Go 才修改受管路由并逐地址回读，失败可恢复。旧源继续补取，不能因 NAS 上线或某端口恢复停止。新源未就绪不切换；已过期、丢失或从未被来源接收的邮件不能报告已恢复。旧备份需核对保留窗口内的云端完成清单，不仅枚举未确认项。

### 其他研究候选

Resend 可独立保存收件并提供列表和原文下载，即使没有 Webhook；Free 收发共享额度及 30 天标准留存限制仍适用。但 received_for 来自 Received 头，未证明可信分箱与别名撤销前不开放 ANAS 入站选项。SMTP2GO 无入站邮箱。已有邮箱 IMAP、私网 SMTP 正文推送不纳入当前 Tutorial；之前的比较不再构成推荐。[Resend 接收](https://resend.com/docs/dashboard/receiving/introduction)、[原文字段](https://resend.com/docs/api-reference/emails/retrieve-received-email)、[限制](https://resend.com/docs/knowledge-base/account-quotas-and-limits)

## 13. 接入方式选择与自管云中转可行性

2026-10-24 用户提出增加可一键部署的在线云中转，Cloudflare 先投递到云端，再由 NAS 取回或云端推送。本节整理可选方案，**不表示全部纳入一期实现，也不表示已确认购买云主机**。选择界面、部署边界与职责建议见[架构方案 §11](../architecture/portal-account-mail-administration.md#_11-邮件接入选择与云中转提案)，待决事项见正式计划 D7/D8。

### 13.1 面向用户的接入选项

所有选项最终都把邮件交给 NAS 的 DMS，Go 管理人员、地址与访问权限，Roundcube 提供读写界面。下表的复杂度是**新增接入部分**的比较；包含 AD、IAM、地址协调和恢复的完整邮件系统仍属高复杂度。

| 接入选项 | 收件路径 | NAS 网络要求 | NAS 离线后的保管能力 | 费用和主要取舍 |
| --- | --- | --- | --- | --- |
| A. Cloudflare 直接投递 | Cloudflare → NAS DMS | 公网入站 25 实际可达 | 不应依赖 Routing 作为离线邮箱；失败后的重试取决于上游 SMTP 处理 | Routing 免费额度内无新增中转主机；新增复杂度低，适合稳定在线 NAS |
| B. 自管云中转 | Cloudflare → 云主机持久接收 → NAS | 选 B2 HTTPS 拉取；SMTP/IMAP 仅历史比较 | 云端已接收邮件可留待 NAS 恢复；受队列期限、磁盘、备份及云主机自身可用性限制 | VPS、存储、备份和运维成本；新增复杂度中至高，离线策略由自己控制 |
| C. 托管接收 API | Resend → Go 每 60 秒取原文 → DMS；也可 Cloudflare → Resend 接收子域 | 只需主动 HTTPS 出站 | Resend 明确保存已接收邮件，即使没有 Webhook 或 NAS 离线；有留存和配额限制 | 有免费档；新增复杂度中，省云主机维护，需验证可信分箱与服务使用范围 |
| D. 已有外部邮箱（不进入当前选项；Gmail 已取消） | Cloudflare → 外部邮箱 → Go 每 60 秒 IMAP 取信 → DMS | 可主动连接来源的 IMAP TLS 端口 | 由外部邮箱容量、保留策略和账号状态决定 | 已有/免费邮箱可复用；新增复杂度中，多箱映射和远端认证不一定简单 |
| E. Cloudflare 云存储 | Email Worker → 私有 R2 → Go 每 60 秒取信 → DMS | 只需主动 HTTPS 出站 | 已持久写入 R2 的原文按配置留存；写入失败和清理策略仍需验证 | 有免费额度，超额和开通条件另核；新增复杂度高，需维护 Worker、存储和恢复 |
| F. 其他端口或正文 HTTPS 直投 NAS（已取消） | Forward Email 自定义 SMTP 端口；或 Forward Email/Mailgun Webhook → NAS | 其他公网 TCP 端口或 HTTPS 入口 | 转发/有限重试不等于可轮询的离线邮箱 | 免费资格和认证方式各有约束；新增复杂度中，适合端口受限但 NAS 稳定在线 |

**最新可启用范围为 B2 与 E；C 仅研究，A/D/F 不进入 Tutorial。** A/F 的网络绕行不能独立满足“NAS 长期离线仍可靠保管”的目标。B/C/D/E 都有远端持久副本，但“云端已接收”和“已进入本地邮箱”应分别显示；任何选项都不能保证云端故障、超额或超期后无限收件。免费服务具体限制及来源见[托管服务比较 §4.3](hosted-email-forwarding-services-research.md)。

Resend 可以独立承担收件和 SMTP 发件；DNS 仍可托管在 Cloudflare，收件主域 MX 改为 Resend 后不必再经过 Routing。若保留 Cloudflare 首跳，则把目标放在另一个已在 Resend 验证接收的域/子域，不能把两家的 MX 混放在同一域充当按用户分流。Free 当前收发合计 100 封/天、3,000 封/月，标准邮件数据留存 30 天；发送还受用途和信誉限制，不承诺适合所有普通邮箱通信场景。额度耗尽时入站具体响应、Bcc 可信目标识别仍待实测。[接收域](https://resend.com/docs/dashboard/receiving/custom-domains)、[离线存信](https://resend.com/blog/inbound-emails)、[额度与留存](https://resend.com/docs/knowledge-base/account-quotas-and-limits)、[使用政策](https://resend.com/legal/acceptable-use)

### 13.2 云中转的三种交付方式

| 子选项 | 云端组件和交付机制 | 优势 | 代价及与现有要求的关系 |
| --- | --- | --- | --- |
| B1. 私网 SMTP 推送 | 云端 Postfix 持久队列；NAS 主动建立私网连接，Postfix 经该连接投递 DMS | 复用标准 SMTP、排队和重试，不需开发原文下载协议；NAS 无需公网入站 25 | 新增 VPN/隧道维护；SMTP 重试不是每 60 秒拉取，当前不纳入产品；历史自动降级要求已废弃，持久补取仍适用 R-052/061 |
| B2. HTTPS 拉取，可加推送提示 | Postfix 接收后交给受限 Go 接收适配器，持久保存原文、信封和状态；NAS Go 每 60 秒取回并确认 | 只需 NAS 出站 443，精确保留信封目标；符合已要求的轮询方向，可统一管理离线保管 | 新增云端 Go 程序及暂存格式、确认/去重/恢复；新增复杂度高，没有现成的 Postfix HTTP 取信 API |
| B3. IMAP 拉取 | 云端 Postfix + Dovecot 暂存邮箱；NAS Go 每 60 秒经 IMAP 取回 | 复用标准邮箱存取协议和 UID 进度，不自建原文下载接口 | 新增邮箱服务、远端凭据和 UIDVALIDITY 变化处理；按目标隔离或接收时保存可信信封，不能只看 To 头分箱 |

DMS 官方站点提供社区贡献的“公网 Postfix + 内网 DMS”参考，支持 B1 的协议可行性；它不是 ANAS 的现成一键部署功能，也不是本项目离线保管验收。NAS 可主动建立 WireGuard 连接，云端再在私网内发起 SMTP；NAT 场景可配置保活，但需实际允许相应出站协议。仅允许 HTTPS 的网络不能直接假定 WireGuard 可用。常规 Cloudflare Tunnel 公共 TCP 入口要求客户端配合 cloudflared，不能直接替代 Cloudflare Routing 的 SMTP 目标。[DMS 参考](https://docker-mailserver.github.io/docker-mailserver/v16.0/examples/use-cases/external-relay-only-mailserver/)、[WireGuard](https://www.wireguard.com/quickstart/)、[Tunnel 协议](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/protocols/)

B2 是基于 SMTP 接收与 HTTPS 存取能力提出的待开发组合。Postfix 到云 Go 的交接只有在原文及信封状态持久保存后才确认；NAS 的拉取完成以 DMS 持久接收和本地完成记录为准。持久交接、重复投递和确认丢失均需故障验证，不直接读写 Postfix 活跃队列文件。已确认首版使用通知加拉取；NAS 从同一持久来源取信，不实现正文推送。

B3 可利用标准 IMAP，但通用 DMS Fetchmail 不会自动提供既有 Go 权限、可信分箱及恢复语义。应由 Go 统一管理来源和进度，只选择一个执行器；远端原文删除晚于本地确认。云端无需复制个人 AD 密码，专用取信凭据与用户登录凭据分开。[DMS Fetchmail](https://docker-mailserver.github.io/docker-mailserver/v16.0/config/advanced/mail-fetchmail/)

### 13.3 离线保管与端口的真实边界

云中转的价值在于**先持久接收，再向 NAS 投递**。单纯 TCP 代理或等待 NAS 成功才确认的同步转发，不能独立承担 NAS 离线期间的保管。Cloudflare 会将目标 SMTP 错误在当前会话返回上游；云端应在本地成功接收后承担后续交付，存储不可用时不能先确认成功。[Cloudflare SMTP 错误](https://developers.cloudflare.com/email-service/reference/postmaster/#smtp-errors)

普通 Postfix 延迟队列有到期时间：上游默认 maximal_queue_lifetime 为 5 天，到期仍投递失败会按不可投递处理；实际安装值需回读。它不适合直接宣称“NAS 停机数周也不会丢件”。若业务需要更长离线期，应明确保管期限、容量及临近到期处理，或选择 B2/B3 的持久暂存，并独立验证备份恢复。Cloudflare 还明确不向原发件人转递 NDR，因此不能只依赖队列到期后的退信通知；须在管理端显示失败，并事先约定超期保留/处置方式。[Postfix 手册镜像](https://manpages.debian.org/trixie/postfix/postconf.5.en.html#maximal_queue_lifetime_(default:_5d))、[Cloudflare NDR 限制](https://developers.cloudflare.com/email-service/reference/postmaster/#non-delivery-reports-ndrs)

云主机仍需可达的**公网入站 25**；改变的是公网 SMTP 的落点。安装成功不证明云商已放通端口，需要从独立外网探测，分别检查云平台规则和主机监听。NAS 使用私网端口、HTTPS 或 IMAP 出站。外发仍可由 NAS DMS 经 SMTP2GO/Resend 的认证 SMTP 完成，不要求把普通外发迁到 VPS；接收后的失败通知出口也应另行验证，不能因云端只收信就遗漏退信处理。

Cloudflare → 云端时，主域 MX 保持 Cloudflare，目标投递子域 MX 指向云服务器的 DNS-only 主机名；目标地址先验证。每个公开地址仍有独立 route-id，云端保存实际 SMTP 信封收件人。若选择“公网 MX 直接指向云端”，可减少 Cloudflare 规则/目标限制，但会新增自管公网收信过滤及运维职责，属于 B 的另一入口选项，不能自动切换。无论哪种入口，Bcc 和伪造 To/Received 都不能改变邮箱归属。

### 13.4 最新选择与验证范围

最新推荐：希望减少云主机维护时选 E（Worker/R2）；已有专用 VPS 且希望自己管理离线保管时选 B2（云 Go 暂存及 HTTPS 拉取）。A/D/F 已从当前可选项移除，B1/B3 只保留协议比较，C 暂不开放启用。两种选项均可配 SMTP2GO 或 Resend 外发。

云端始终先持久接收，NAS 离线不依赖本地 Go 临时改路由。只有切换两个云来源时才由管理员显式发起操作，并继续旧源补取。旧自动降级/回切条款已依用户要求废弃，不能再作为实施任务；保留的离线、地址、原文和恢复检查仍必须完成。

用户同意先验证 Worker/R2；本地原型、真实云环境、Go/DMS 消费者和自管云按顺序推进。真实 SMTP 写入失败响应、额度、长期离线/容量、Bcc/多目标、地址撤销和恢复仍须实测，不用研究推导或模拟器结果取代。Postfix 参数的既有依据来自 Debian 发布手册，DMS 示例仅为协议参考。

## 14. Worker/R2 的开发难度与最小实现

**只打通原文存取是中等难度；达到已确认的地址撤销、无感登录、故障恢复和云端确认要求，集成难度高。** 收件到对象存储并不是难点，主要工作是确保进程中断、重试和响应丢失不会误删邮件或错误分箱。相对自管云 B2，它省掉公网 Postfix、操作系统、磁盘和证书的日常维护，但增加 Worker 运行约束及 R2 接口适配，不应仅按示例代码长度估算交付。

| 部分 | 工作量判断 | 具体工作 |
| --- | --- | --- |
| Email Worker 保存邮件 | 中 | 流式保存原始 MIME，绑定真实信封目标，等待持久完成；失败时的上游 SMTP 行为要实测 |
| HTTPS 列表、下载、完成确认 | 中 | 服务认证、分页、按目标隔离、原文完整性和幂等确认；与自管云提供相同的少量操作 |
| Webhook 与 60 秒补取 | 中 | 先存信再通知，短超时和有限重试；通知失败不阻止补取，重复通知不重复导入 |
| DMS 投递结果核对及恢复 | 高 | DMS 已接收但确认丢失、旧备份恢复、已撤销别名、同信多目标、云副本清理 |
| 部署与运行维护 | 中 | Worker/R2 凭据、受管路由、额度、告警、状态回读与升级；不是写完 Worker 就完成 Module |

官方 Email Worker 提供 message.raw 原文流、message.to 信封 RCPT TO；R2 支持对象自定义元数据、条件写入和分页列举。候选实现用一个小型 TypeScript Worker 承担接收和存取端点，原文保存在私有 R2；管理、身份和 NAS 取信继续使用 Go/PostgreSQL。无需把 Go 的 AD/IAM 逻辑移到 Worker，也不默认添加 D1、KV、Durable Objects 或 Queues。[Email Worker](https://developers.cloudflare.com/email-service/api/route-emails/email-handler/)、[R2 API](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/)

可用“每个目标一份不可变邮件对象及信封元数据＋独立完成凭据”的最小格式，避免正文与目标发布到一半就可被读取。通知尝试记录与最终完成凭据分开；条件写入或不可变记录确保迟到的通知结果不会覆盖完成状态。R2 的强一致性不等于跨多个对象的事务，列表还必须按 truncated/cursor 完整分页；不应把一次返回不足 1,000 项当成已经读完。正式格式及恢复顺序仍需故障验证。[R2 一致性](https://developers.cloudflare.com/r2/reference/consistency/)、[分页与条件操作](https://developers.cloudflare.com/r2/api/workers/workers-api-reference/)

**免费能否覆盖要看实际负载。** R2 Standard 当前免费含 10GB-month、每月 100 万次 A 类操作和 1,000 万次 B 类操作，互联网流出免费；Workers Free 有每天 10 万请求及 CPU/内存限制，入站邮件也受 Worker 限制。Cloudflare 入站单封上限 25MiB。薄接收器应流式保存，避免在 Worker 中全面解析 MIME、重编码附件或扫描全部历史对象；免费运行失败不能自动变成付费订阅。[R2 价格](https://developers.cloudflare.com/r2/pricing/)、[Workers 限制](https://developers.cloudflare.com/workers/platform/limits/)、[邮件限制](https://developers.cloudflare.com/email-service/platform/limits/)

量级示例：一个 NAS、空闲时每分钟一次列表，30 天约 43,200 次；每封来信还会增加写入、下载、状态/确认和清理请求，分页积压及通知重试另外计费。不能把空闲列表请求数当成整个系统月操作数，也不能仅凭免费存储容量承诺零账单。

通知失败后的延迟重试，可由同一个 Worker 的定时触发读取持久待通知记录；NAS 轮询承担最终补取，不依赖一次调用中的长时间 sleep 或后台任务存活。若额外要求持久消息队列保障通知时延，才另评估 Queues；它不是本次方案的默认依赖。自管云则复用云 Go 的持久任务循环。共同协议与候选重试参数见[架构 §12](../architecture/portal-account-mail-administration.md#_12-webhook-通知、立即取信与统一交付状态)。

## 15. 与 SMTP 推送、IMAP 通知及外发的关系

| 机制 | 实际传输内容 | 与当前接收流程结合 |
| --- | --- | --- |
| 已确认的 Webhook 通知 | 消息/事件 ID 与来源提示，不传正文 | NAS 立即触发同一个取信任务；60 秒轮询继续兜底，完成确认独立回云端 |
| 云端 SMTP 投递 NAS | 完整 MIME 和 SMTP 信封 | 可作为云中转的可选交付通道；简单地直投 DMS 后再并行拉取同一封信，会产生两套无法共享结果的路径 |
| IMAP IDLE | 在 NAS 主动建立的 IMAP 连接中通知邮箱变化 | NAS 收到变化后执行 UID FETCH；断线重连后补读，保留 60 秒核对，不需要 NAS 公网入站端口 |
| IMAP APPEND | 客户端向指定远端邮箱写入邮件 | 属于上传动作；不能当作跨服务器入站 SMTP 的等价替换，会涉及额外凭据和绕过正常收件处理，本轮不选 |

通常所说的“IMAP 推送”指 IDLE，通知本身不携带完整邮件，NAS 仍主动 FETCH；服务器无 IDLE 能力时退回轮询。需按能力协商、处理连接和 OAuth 到期，并保留重连补读。它可用于 Gmail 或自管 Dovecot 来源，**R2 没有 IMAP 服务**；为 R2 新增 IMAP 服务器会抵消轻量部署优势。[IMAP IDLE 标准](https://www.rfc-editor.org/rfc/rfc2177.html)、[Gmail 连接与会话](https://developers.google.com/workspace/gmail/imap/imap-smtp)

如果将来增加 SMTP 与拉取双通道，应把云端稳定交付 ID、目标和来源认证带入受限 NAS 入站适配器，两条路径都先登记到同一 PostgreSQL 接收记录。只有这个受信通道的标识可用于去重，外部发件人自带的 X-* 头不得冒充。SMTP 250 只表示约定接收层已接管，实际 DMS 投递继续通过同一任务与完成确认；普通 SMTP 没有读取 NAS 导入状态的标准接口。因此首版不把“SMTP 已超时”直接解释为可以经 HTTPS 再投一次。裸 SMTP 直投与纯拉取可分别配置，混合交付另行确认，不能承诺端到端恰好一次。

这与用户对外发信独立：Roundcube 始终经本地 DMS 与 Go 的 From 授权，再走 SMTP2GO/Resend/通用 SMTP；云收件暂存不自动成为出站服务器。Forward Email 的完整正文 Webhook、SMTP2GO 的发件结果 Webhook、自管云/R2 的新邮件通知是三类不同事件，不能共用一个“成功”字段。[最新收发组合](hosted-email-forwarding-services-research.md#_4-6-重新整理收信转发与发信代发)

本次确认的是两种云来源的通知加拉取功能；SMTP 混合交付和 IMAP IDLE 仍是兼容性研究，不新增传输实施承诺。Gmail 真实认证与可信分箱、Worker 存储失败后的上游响应、Forward Email 的免费来源认证以及两种云来源的故障恢复尚未实测。

## 16. 接入引导与验证顺序

用户已确认邮件服务 Web 内提供 Tutorial；方案选择、账号/域名/凭据准备、费用与限制、配置草稿、分项核验及启用规则见[架构 §13](../architecture/portal-account-mail-administration.md#_13-邮件服务配置-tutorial)。要求编号 R-060—066 承接常态云入口和引导，不把教程草稿写成已有生产界面。

先使用官方 Miniflare 固定版本验证 Worker/R2 的持久对象、通知失败、分离回执、分页及重启，再连接隔离 Cloudflare 资源核对真实 SMTP/额度/错误行为，随后复用接口验证自管云。原始日志不入 Git，结果与缺口统一记入正式配套计划；模拟器不代替真实 DMS 或云端收发。


### 2026-10-27 子域规则补充核查

Cloudflare 当前官方说明：Email Routing 子域可按完整收件地址创建 literal 规则，catch-all 只用于 zone 根域。因此 finance 的 `mail-poc.bengbu.cc.cd` 不能仅靠一条子域 catch-all，把所有地址交给 Worker。正式接入须为主地址、别名及独立箱同步受管逐地址规则；Worker 内地址许可及 NAS 的可信分箱仍需独立核验。该限制属于既有 R-040/R-041 实现范围，不新增供应商或服务。共享规则上限和实际账号状态必须在 Tutorial 中核查，不能由本地每人限额外推云端无限规则。原管理 Token 继续只用于本机获授权隔离测试，尚未向 NAS 交付正式规则管理凭据。

依据：[Cloudflare 子域配置](https://developers.cloudflare.com/email-service/configuration/subdomains/)、[路由规则 API](https://developers.cloudflare.com/api/resources/email_routing/subresources/rules/)、[服务限制](https://developers.cloudflare.com/email-service/platform/limits/)。本次为官方接口核查，不是逐地址自动同步或 finance 正式公网收发的通过结果。


## 17. eusend 投递 Outlook 垃圾邮件的核查（2026-10-27）

本次针对用户已确认在垃圾箱收到的 `ANAS eusend SMTP isolated verification 20261026`，不是新一轮发信。eusend 控制台记录 2026-10-26 15:28:23 Delivered，仅能证明微软收件服务器接受，不能证明进入收件箱。初查时尚未取得收件端邮件头；随后用户提供原始邮件头，核查结论见本节末尾。以下保留初查事实与限制。原始截图、DNS JSON 和执行过程放私有报告目录，验收状态仍归配对计划。

| 项目 | 已观察事实 | 能得出的结论与限制 |
| --- | --- | --- |
| 域签名 | 控制台 mail-poc.hlong.wang 为 verified、DKIM Required Live；公开 eusend._domainkey.mail-poc.hlong.wang TXT 存在 | 签名公钥已发布；是否在微软侧验证通过仍看那封邮件的实际邮件头 |
| DMARC | _dmarc.mail-poc.hlong.wang 为 `v=DMARC1; p=none;` | p=none 不指示收件方隔离；不能以此解释垃圾箱，也不能靠改成 reject 保证收件箱 |
| Return-Path 对齐 | eusend 页面为 Not set up，建议 send.mail-poc.hlong.wang 的 SPF TXT + feedback.eusend.dev MX 配对 | 未启用自定义 Return-Path，依赖已对齐 DKIM 实现 DMARC 是服务支持的默认方式；不能直接断言 SPF/DMARC 失败 |
| 与 Resend 共用发送域 | send.mail-poc.hlong.wang 已是 CNAME → send.forge.rmta.net，其目标公开 SPF 当前列出 Forge IP | 不能在同名 CNAME 旁追加 eusend TXT/MX，不能为诊断覆盖既有 Resend 配置；父域加 SPF 也不自动修复实际 Return-Path 的对齐 |
| 提供商出口 | eusend 官方说明免费及未建立信任的流量使用独立出口，而非已预热的共享池 | 新账号/发信子域与出口信誉是应查的候选原因；尚无该封邮件的实际出口 IP 或微软判分，不能当作已经确认的黑名单/信誉故障 |
| 邮件内容 | 原邮件为单段纯文本隔离 SMTP 测试，无附件；实际收件头 Message-ID 使用 mail-poc.hlong.wang | 无证据支持“附件导致垃圾”或“Message-ID 域不同必然认证失败”；内容评分仍需实际收件判据 |

初查所需收件端判据包括 Authentication-Results 中的 SPF `smtp.mailfrom`、DKIM `header.d`、DMARC/compauth，以及 SCL、BCL、CAT/SFV、实际接收 IP。若认证失败，按实际发送路径修正；若认证均通过，则优先检查域/出口信誉及收件方规则，再用受控收件地址验证正常往来邮件。允许收件人将已确认正常邮件标记为非垃圾，但这是个人收件行为，不等于修好全体收件人的投递。没有向第三方邮件头分析网站上传用户数据，也未改 DNS、套餐或发送新邮件。

官方依据：[eusend 投递机制](https://eusend.dev/deliverability)、[eusend DNS 配置](https://eusend.dev/docs/domains/dns-records)、[Microsoft 邮件头判据](https://learn.microsoft.com/en-us/defender-office-365/message-headers-eop-mdo)。发送端声明或 DNS 当前状态均不代替收件方实际结果。


### 17.1 收件端邮件头核实

用户随后提供同一主题、2026-10-26 07:28:19 UTC 发信/07:28:26 UTC 到箱的完整邮件头。以下仅摘录诊断字段，不保存用户完整正文、私有收件信息或微软加密诊断串到 Git：

| 字段 | 实测结果 | 判断 |
| --- | --- | --- |
| SPF | `pass`，smtp.mailfrom=eusend.dev，Return-Path 为服务自身域 | eusend 默认信封域 SPF 通过，不与 From 子域对齐，但不存在本封 SPF 失败 |
| DKIM | mail-poc.hlong.wang 和 eusend.dev 两份均 `pass` | 自有发信子域的 DKIM 已对齐 From；正常签名且传输后验证成功 |
| DMARC / compauth | `dmarc=pass action=none`；`compauth=pass reason=100` | 本封已通过 DMARC 与微软综合认证；p=none、自定义 Return-Path 未设置均不能解释为认证故障 |
| 入站来源 | 88.198.193.148，HELO mail2.eusend.dev | 确定本封实际 eusend 出口；尚无该时刻的信誉/封禁结果 |
| 分类与落箱 | `SCL:5`、`BCL:0`、`dest:J`、`OFR:SpamFilterAuthJ`、`RF:JunkEmail` | 认证通过后实际进入微软垃圾箱；BCL 没有显示高批量投诉评分。SCL 不是精确根因，也不单独决定云端落箱 |

据此，本封问题定位到**收件方反垃圾分类/信誉判断**，不是 DNS 认证不通过。微软 ARA 数字规则和 Message-Info 未公开可逐项解释的评分依据；没有 CAT/SFV 等明确分类时，不声称已查到某条触发规则、IP 黑名单或内容关键词。新建发信子域/账号、该免费出口信誉及测试内容仍是候选因素，不能由单封判分证明其中某项。

不为此覆盖 Resend 的 send CNAME，也不把 DMARC 改为 reject 当作入箱修复。先由收件人对这封确认正常的邮件标记“非垃圾”，可改善个人收件体验；同时把出口 IP、时间和认证全过/SCL5 的脱敏摘要交给 eusend 支持，核查微软出口信誉及是否应由服务方处理。对外联系仍须用户明确授权，当前未发送工单或邮件。后续受控比较应使用正常业务样式、小量且收件人知情的邮件，不重复轰炸测试或自动预热；通过认证不保证收件箱。

依据：[微软邮件头解释](https://learn.microsoft.com/en-us/defender-office-365/message-headers-eop-mdo)、[微软 SCL 的适用边界](https://learn.microsoft.com/en-us/defender-office-365/anti-spam-spam-confidence-level-scl-about)、[eusend 免费出口隔离与信誉说明](https://eusend.dev/deliverability)。

2026-10-27 公共 DNS 只读补查：Google HTTPS DNS 回读 `mail2.eusend.dev A = 88.198.193.148`，该 IP 的 PTR 回到 `mail2.eusend.dev`。没有发现当前正反向解析不一致；这不是历史信誉或微软白名单证明。本机普通 DNS 受代理 fake-IP 影响，不将其 198.18/15 返回值用于邮件 DNS 结论。
