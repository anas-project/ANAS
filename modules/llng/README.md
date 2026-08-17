# LemonLDAP::NG

SSO portal, SAML/OIDC identity provider, and app launcher.

## Administrator access / 管理员入口

LLNG Manager 当前使用 LLNG Portal 的 AD 认证和 Samba `Admins` 组授权。Manager 本身是
LLNG 的最高应用管理面，因此 `Admins` 成员进入后拥有完整应用管理能力，移组后下一次
Portal 授权即撤销。旧的
`LLNG_PASSWORD` 变量没有任何上游消费者，已经删除；给它随机值不会创建本地管理员。
上游虽支持 Choice/Combination 多认证后端，也支持另行配置 Web Server BasicAuth，但两者
都会改变当前认证拓扑，并需要独立用户存储、直达入口及验证/回滚实现。因此本 Module
目前不声明 `break_glass`。目录故障恢复必须在主机侧修复目录，或显式修改 Manager
protection，不能依赖不存在的本地密码。

The Manager currently uses AD authentication through the LLNG Portal and grants
its full application-administration surface to the Samba `Admins` group; the next
Portal authorization revokes access after membership removal. The removed `LLNG_PASSWORD` variable had no
upstream consumer and did not create a local administrator. Upstream Choice or
web-server BasicAuth would require a different authentication topology, user
store, direct route, and verified rollback path, so this module does not claim a
`break_glass` account.

## Samba 密码同步行为 / Samba password behavior

LLNG 的用户改密直接写回 Samba AD。ANAS 将 Samba 的最小长度、复杂度开关和密码历史
次数写入 LLNG 配置或中文规则提示；修改这些 Samba 设置后，需要重新执行 ANAS
apply/reconcile 才会刷新 LLNG。当前 LLNG 集成**没有**消费最小改密间隔，因此只会在
Samba 拒绝后通过通用说明提示用户稍后再试。

LLNG 能在提交前精确检查最小长度和两次输入是否一致。复杂度、用户名/姓名以及历史
密码仍由 Samba 最终裁决。Samba 的 LDAP 返回码通常只把策略拒绝归为 19 或 53，LLNG
不能可靠区分具体违反的是复杂度、姓名、历史还是最小间隔；中文页面因此显示包含全部
相关规则的可操作说明，而不会伪造一个精确原因。原始目录诊断保留给管理员日志。

LLNG password changes are written directly to Samba AD. ANAS synchronizes the
minimum length, complexity flag, and history count into LLNG configuration or its
Chinese guidance; run ANAS apply/reconcile after changing these Samba settings.
The current LLNG integration does not consume the minimum password age, so a
Samba rejection can only produce guidance to retry later.

LLNG can preflight the minimum length and matching confirmation exactly. Samba
remains authoritative for complexity, username/display-name content, history,
and minimum age. LDAP result codes normally collapse policy rejection into 19 or
53, so LLNG cannot safely name one exact failed rule; it displays comprehensive
actionable guidance and keeps raw directory diagnostics in administrator logs.
See [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#samba-目录密码接入规范)
for the provider contract.

服务器 E2E `test-env/scripts/server-llng-password-policy-e2e.sh` 使用临时 Samba 用户覆盖
长度、确认、复杂度、成功写回、历史拒绝、旧/新凭据和首次登录强制改密，并在退出时
恢复临时修改的域策略。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.23.2-r5`（reviewed 2026-08-17）
- Timezone / 时区：`container` — LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- Language scope / 语言范围：LemonLDAP::NG Portal and language selector
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：LLNG JSON language code
- Fallback / 回退：Portal language selector and Accept-Language are used; unmatched requests fall back to English.
- Supported languages / 支持语言（17）：`ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- Notes / 说明：Manager and mail templates have separate translation resources; the list records the user-facing Portal inventory.

Evidence / 证据：

- [v2.23.2 — Portal JSON language directory](https://gitlab.ow2.org/lemonldap-ng/lemonldap-ng/-/tree/v2.23.2/lemonldap-ng-portal/site/htdocs/static/languages)
<!-- generated:localization:end -->
