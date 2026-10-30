# 不依赖 Cloudflare 的邮件接收方案研究

核查日期：2026-10-28。本文是候选研究，不代表已实现或已在 finance 启用。现有 Worker/R2、Cloudflare 转发到 VPS 与 NAS 直接收件仍按既有方案保留；以下方案不经过 Cloudflare 邮件接收或转发。

## 1. Worker 能否自己监听 25 端口

Worker 是平台提供的运行环境，不等于拥有公网 IP 的虚拟机。是否能直接接收公网 TCP 取决于平台；不能仅因为代码能使用 TCP 就假定能监听公网 SMTP。Cloudflare 官方目前说明入站 TCP 仅可经其他 Worker 的 service binding 转入，其他来源的入站支持尚未提供；它的 Email Worker 由 Cloudflare 邮件平台接收后触发，不能作为脱离 Cloudflare 的收信入口。[Cloudflare TCP 文档](https://developers.cloudflare.com/workers/runtime-apis/tcp-sockets/)。Google Cloud Run 公开服务的协议是 HTTP/1 或 HTTP/2，不把普通 SMTP 25 直接交给容器。[Cloud Run 容器规范](https://docs.cloud.google.com/run/docs/container-contract)、[端口协议](https://docs.cloud.google.com/run/docs/reference/rest/v1/Container)。

## 2. VPS 直接接收、NAS 拉取

`发件方 → 域名 MX → 自有 VPS 的 SMTP 25 → 持久暂存 → NAS 通过认证 HTTPS 拉取 → DMS → Roundcube`。VPS 收到邮件并持久保存后才能在 SMTP 事务中确认接收；NAS 不在线不影响 VPS 暂存，容量不足、磁盘故障、未知收件人等必须安全拒收或临时失败，不能确认成功后丢失。采用 SMTP 信封目标，不能用 To/Cc 推断 Bcc 或归属。通知仅提示 NAS 拉取，拉取账本和逐目标去重仍需保留，通知失败不表示邮件失败。VPS 不得成为开放中继。

准备：自有域名及 DNS 管理权限；有公网 IP、入站 TCP 25 可用的 VPS；专用持久磁盘、TLS 证书、备份及容量监控；部署权限和独立受限 NAS 取信凭据。服务商可能封禁 25、收取 VPS/存储/流量费用，需按实际账号核验。配置顺序：预检资源与权限 → 安装隔离服务并回读 → 验证 TLS 和地址许可 → 授权后将 MX 指向 VPS → 从受控外部发件人验证实际进入 VPS → NAS 断网后恢复取信 → 检查每个目标的 DMS 最终交付与重复投递。切换 MX 后也必须持续消费旧源积压；不能仅凭 DNS 或端口连通宣称成功。计划中的自动安装脚本尚未完成。

## 3. 非 Cloudflare 云函数候选：Amazon SES + S3 + Lambda

`发件方 → SES 公网 SMTP → S3 原文 + 收件事件 → Lambda/队列协调 → NAS HTTPS 拉取 → DMS`。SES 是实际 SMTP 25 入口，Lambda 本身不直接监听公网 25。SES 官方支持按收件规则将原文写入 S3，并向 SNS 提供接收事件；事件包含匹配规则的 SMTP 信封收件地址。[SES 接收动作](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-action.html)、[S3 动作](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-action-s3.html)、[事件字段](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-notifications-contents.html)。独立核验接收地区、域名所有权和 MX；SES 不提供 IMAP 邮箱。[接收地区](https://docs.aws.amazon.com/ses/latest/dg/receiving-email.html)、[MX 配置](https://docs.aws.amazon.com/ses/latest/dg/receiving-email-mx-record.html)。

此方案复杂度**高**：需要 SES、S3、事件通知、持久队列/重试、函数、IAM 和现有 NAS 源协议；需要安全持久保存原文与可信信封目标、绑定接受时的人员/邮箱和地址版本、处理多收件人/Bcc、写入/通知顺序、重复事件、NAS 离线、地址移交、清理和故障恢复。尤其仅扫描 S3 正文不能可靠恢复 Bcc 和地址在接收时的归属；事件丢失、延迟或地址已转让时不能猜测归属，必须保留原文并告警。此关键一致性及提供商失败行为尚未证实，不能凭 API 列表宣称可投入业务。一个更简单的替代方案是直接 VPS 入口，或继续使用已研究的 Cloudflare Worker/R2。

材料：AWS 账号和相应地区、域名验证及 MX、S3 私有桶、限定权限的 SES/S3/SNS/队列/Lambda 配置、受限取信凭据及容量/账单设置。AWS 官方按量收费，当前公开价入站为每千封 0.10 美元，另有大邮件分块以及 S3、SNS、Lambda、网络等费用；新账号试用额度是否适用和实际费用需要单独核实，不能称为永久免费。[SES 价格](https://aws.amazon.com/ses/pricing/)。公开文档对单封大小存在不同页面口径，实际启用前须以目标地区和账户当前设置复核，不能将 40 MB 视作已验收上限。

建议将来先建立不含业务邮件的隔离端到端实验：真实多目标/Bcc、原文和事件乱序或缺失、重复投递、地址移交期间延迟、存储故障、NAS 停机恢复、限额和费用，再决定是否开发正式适配。2026-10-28 用户明确 SES 方案暂不纳入一期；不创建 AWS 资源、付款或修改 DNS。

## 4. 配置引导检查点

收件和发件分开选择。每一步应显示材料、如何获得、费用和权限范围、待检查/通过/失败/未知、最近检查时间及修复建议。检查应逐步覆盖：域名所有权与 DNS → 入站设备/服务及 25 可达性 → 持久暂存与凭据 → 地址许可和信封目标 → 实际外部邮件及 NAS 补取 → DMS 交付；发件覆盖发件域资格、SPF/DKIM/DMARC/PTR（按方案适用）、上游授权或直发网络 → 测试邮件 → 对方服务器接受与用户确认。只查到 MX 不能证明路由正确、端口开放或最终投递；供应商接收与收件箱到达也分别显示。尚无实现或无法核验的检查必须保持未知，不能由保存草稿替代。外部测试邮件和业务 DNS 变更需要已有明确授权。

## 5. 其他可能的收信来源

Resend 等托管收信 API 可以接收并暂存，NAS 通过 API 轮询；[Resend 官方说明](https://resend.com/blog/inbound-emails)通知不可用时仍保存邮件，[安全说明](https://resend.com/security)称 Free 等套餐邮件和日志保留 30 天，具体接收数据、大小及账户实际额度仍需分别核实。这是除四种现有方案之外的候选，不是已确认的一期方案；本项目曾实测 Resend 拒绝 `mail-poc.bengbu.cc.cd`，必须先解决发收域资格，并验证原始邮件、Bcc 信封、地址变更、过期和离线恢复。Mailgun 的 [`store()` 官方说明](https://documentation.mailgun.com/docs/mailgun/user-manual/receive-forward-store/route-actions)为最多 3 天，不能保证超过该时长的 NAS 离线。

另可使用支持自有域的托管邮箱收件后通过 IMAP 拉取，但是否免费、每箱凭据、长时间离线和目标归属取决于供应商；先前用户已取消 Cloudflare＋Gmail IMAP，不能默认恢复。Forward Email 免费转发虽可向自定义端口或 Webhook 转发，但其[官方说明](https://forwardemail.net/en/faq)的转发与付费邮箱存储是不同功能；不能把转发重试当作独立持久邮箱，因此若 NAS 不稳定仍需 VPS/其他持久暂存。托管邮箱直接作为用户邮箱则属于另一架构，需另行确定数据、身份与备份边界。
