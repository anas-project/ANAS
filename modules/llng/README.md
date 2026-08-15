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

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.23.2-r4`（reviewed 2026-08-15）
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
