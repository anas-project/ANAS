# ANAS 邮件

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `mail` |
| 版本 / revision | `0.1.0-r1` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

当前候选实现包括 Go 邮件管理、DMS 固定控制进程和 Roundcube 插件；Manifest/Compose 已接入通用 OIDC、Samba 只读目录、独立数据库资源及受管私有运行绑定。**finance 已部署 10000 HTTPS 候选入口，真实账号已无感进入 Roundcube；一期尚未完成验收，云端收信及正式出站尚未启用**。完整范围和阻塞以[根计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/mail-integration.md)为准，原型或隔离子集不代替正式验收。

## 依赖与启用前提

复用现有 PostgreSQL Provider、IAM OIDC、Samba AD、Traefik 和用户门户，不新增数据库或认证服务。Go 使用 `primary_database`，Roundcube 使用 `webmail`，两者数据库与非超级用户凭据独立，删除策略均为 retain。Go 身份读取使用 AD reader，地址维护使用独立受限写身份；门户仅在建号时检查地址冲突。

启用前须先通过 ANAS 验证完整停机快照、停止外部目录写入者，再显式准备 Samba 的邮件资源与角色。`samba_dc.mail_management_policy` 必须完整指定受管域、保护地址、箱数、别名数、每箱容量和发送速率；`mail_initial_source_domain` 指向已收敛的旧域，`mail_initial_snapshot_id` 记录已验证的初始快照。完成后旧源域和角色 SID 固定，不能通过普通重启重新导入或接管替换账号。尚未映射的旧 SMTP 值会阻止导入，不猜测其分箱。

```yaml
modules:
  mail:
    enabled: true
    config:
      domain_prefix: mail
      webmail_domain_prefix: webmail
      iam_protocol: oidc
      db_type: postgres
```

这是待验收候选配置，不会因保存配置就自动成为可用邮箱。云端收件、Resend/兼容 SMTP、Tutorial 与一致恢复仍须完成根计划的实际检查后才能在业务环境启用。

## 人员、邮箱和管理员

人员以 IAM 验证的 `anasIdentityAnchor` 登录，准入须由实时 AD 的 APP_mail/APP_all/管理员组确认；组角色首次绑定后保存 anchor，改名保持身份，同 DN 新组不继承。主箱、附加箱、协议登录、存储路径与 Roundcube 用户键分别具有稳定标识，公开地址不作邮箱身份。

本人可在邮件服务内开通附加箱、管理个人别名；管理员可按受限权限代办。Go 首页只显示本人的邮箱、地址及实际确认状态，POST 跳转到 Roundcube 当前箱，默认地址和别名只属于当前箱。单次交接码不进入 URL；短期协议凭据由 Go 续期，每次新认证/提交重新核验。停用和撤权关闭访问及发送，保留接收与历史。

`mail_domain` 后缀迁移保留原箱和旧地址，sAMAccountName 改名尚未开放。普通删除保留地址占用；管理员建号冲突移交仍按 R-068 待完成，不因 schema 或普通别名 API 存在而宣称支持。公司目录只公开人员主地址与指定联系方式，支持直接/嵌套组和主组；共享来源只保留接口，二期实现。

## 管理员访问

没有本地管理员或独立邮箱密码。Go 与门户使用当前 IAM/AD 准入；IAM 故障不开放静态密码绕过，已建立邮件登录仍可本地退出。管理性地址操作由邮件服务执行，并检查最近认证、目标权限和持久审计。恢复路径是 ANAS 停机恢复及 IAM 的既有恢复入口，不是修改邮件数据库或下发人员密码。

## 所有可用配置参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mail.db_type` | enum (`postgres`) | — | `postgres` | `static` | `MAIL_DB_TYPE` | 否 | 否 | 否 | 是 | `container_recreate` | 固定使用 PostgreSQL 数据库接口。 |
| `mail.domain_prefix` | string | — | `mail` | `static` | `MAIL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Go 邮件管理页的 HTTPS 子域前缀 |
| `mail.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `MAIL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 使用通用 OIDC 绑定，支持的两个 Provider 仍须分别实测 |
| `mail.ingress_capacity_bytes` | int | `0..9007199254740991` | `0` | `static` | `MAIL_INGRESS_CAPACITY_BYTES` | 否 | 否 | 否 | 是 | `container_recreate` | 全部 active/draining 来源的显式云暂存总预算；0 只允许未绑定来源，不自动购额或删除邮件。 |
| `mail.ingress_credentials` | string | — | `""` | `static` | `MAIL_INGRESS_CREDENTIALS` | 否 | 否 | 是 | 是 | `container_recreate` | 按来源 UUID 索引的 Reader/Control/通知/路由与代理秘密，仅进入 Go 私有绑定。 |
| `mail.ingress_sources` | string | — | `""` | `static` | `MAIL_INGRESS_SOURCES` | 否 | 否 | 否 | 是 | `container_recreate` | 公开来源 JSON：UUID/代次、域、容量、留存及可选 Cloudflare 规则绑定；不含密钥。 |
| `mail.language` | string | — | — | `inherited` | `MAIL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 邮件界面八语言默认值，继承全局设置后映射至 Go 与 Roundcube |
| `mail.outbound_passwords` | string | — | `""` | `static` | `MAIL_OUTBOUND_PASSWORDS` | 否 | 否 | 是 | 是 | `container_recreate` | 按服务 ID 关联的 SMTP 密码 JSON，仅交付 DMS 私有运行绑定。 |
| `mail.outbound_routing` | string | — | `""` | `static` | `MAIL_OUTBOUND_ROUTING` | 否 | 否 | 否 | 是 | `container_recreate` | 默认 SMTP 服务及按发件域覆盖的 JSON；密码单独输入。 |
| `mail.service_api_credentials_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_CREDENTIALS_DIR` | 否 | 否 | 否 | 是 | `container_recreate` | anasd 交付令牌的专用只读挂载目录。 |
| `mail.service_api_socket_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_SOCKET_DIR` | 否 | 否 | 否 | 是 | `container_recreate` | 可选的 anasd 邮件安装套接字专用目录，只读挂载。 |
| `mail.service_api_url` | string | — | `""` | `static` | `MAIL_SERVICE_API_URL` | 否 | 否 | 否 | 是 | `container_recreate` | 邮件安装器访问 anasd 的可选直连 HTTPS 地址。 |
| `mail.service_api_workspace` | string | — | `""` | `static` | `MAIL_SERVICE_API_WORKSPACE` | 否 | 否 | 否 | 是 | `container_recreate` | 邮件安装受限授权对应的工作区 ID。 |
| `mail.webmail_domain_prefix` | string | — | `webmail` | `static` | `MAIL_WEBMAIL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Roundcube Webmail 的独立 HTTPS 子域前缀，与管理页共用站点但隔离浏览器会话 |

### 出站 SMTP 候选配置

`outbound_routing` 保存服务、端口、TLS、账号名和按 SMTP 信封发件域的覆盖；`outbound_passwords` 是按服务 ID 对应的秘密 JSON，建议通过受管配置的 `secrets.MAIL_OUTBOUND_PASSWORDS` 输入。Hook 只向 DMS 私有绑定交付密码；Go 与 Roundcube 不取得上游密码。

例如路由为 `{"default":"resend","profiles":[{"id":"resend","provider":"resend","host":"smtp.resend.com","port":587,"tls":"starttls","username":"resend"}],"domains":{}}`；秘密输入另填该服务 ID 对应的 API Key，不在路由中填密码。兼容 SMTP 可使用 `provider: smtp` 及 `tls: tls` 的隐式 TLS，例如 eusend 的 465。发送域必须另外通过服务商验证，保存配置不能证明公网就绪。

也可明确选择直发，例如 `{"default":"direct","profiles":[{"id":"direct","provider":"direct"}],"domains":{}}`，无需上游凭据。直发优先 TLS，不支持时允许明文，且不强制验证对方证书；检查出站 25、固定 IP、PTR、SPF、DKIM、DMARC 和真实投递后才能判断公网可用。未配置时不会自动直发，切换路径仍须处理旧队列。当前管理页面只保存草稿，不能直接启用。

改变服务或域路由前先完成旧队列；存在待投递邮件时，候选准备器会拒绝变更，不自动换服务重发。两种原生 TLS 的隔离子集已通过；管理员 Tutorial 与完整云端验收仍在实施中。

## 保存、恢复和限制

管理员可从邮件首页打开 `/setup` 配置向导候选，选择 Worker/R2 或自管云、查看准备材料与套餐限制，并保存非秘密草稿。密钥在现有 ANAS Secret 管理中保存，这里只接受引用。草稿会跨浏览器保存在邮件 PostgreSQL 中；它不会部署云资源、修改现有路由或启用服务。实际云核验和启用流程仍在实施。

DMS 内容、队列/控制状态及 Webmail 临时附件采用 DATA_PATH/mail 下的专用绑定，人员/箱/地址/会话及 Roundcube 用户/偏好在各自 PostgreSQL 资源。恢复集合还必须包含 AD、IAM 与必要 Secret；停机、一致性核对及旧凭据失效仍待完整实测。当前 /readyz 保持 503，不把三个进程启动当一期完成。

测试入口：go test ./modules/mail/...；真实检查须显式配置隔离源，缺失时跳过不算实机通过。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.1.0-r1`（reviewed 2026-10-26）
- Timezone / 时区：`configured` — The foundation inherits TZ; database timestamps are stored as timestamptz and diagnostics use RFC 3339 UTC.
- Language scope / 语言范围：Optional candidate Go mail home and handoff/logout pages; formal module UI deployment remains pending.
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：BCP 47
- Fallback / 回退：Unconfigured language uses English; all eight Go UI dictionaries are complete. Roundcube locale mapping is wired but native eight-language acceptance remains pending.
- Supported languages / 支持语言（8）：`en`, `zh-CN`, `zh-TW`, `de`, `es`, `fr`, `ja`, `ko`
- Notes / 说明：Candidate UI tests are separate from actual Roundcube and formal module acceptance; no shared theme integration is claimed yet.

Evidence / 证据：

- [0.1.0 — modules/mail/service/ui/mail.js](https://github.com/anas-project/ANAS/blob/master/modules/mail/service/ui/mail.js)
<!-- generated:localization:end -->
