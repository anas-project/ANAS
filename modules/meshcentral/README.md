# MeshCentral

Remote device management service using LDAP authentication.

## Administrator access / 管理员入口

Upstream MeshCentral supports native username/password accounts when domain
`auth` is unset. This Module sets `auth: ldap`; that domain does not also accept
a native local administrator. It therefore declares no `management.local_accounts`.
Administration comes from Samba AD `ldapSiteAdminGroups`; recovery means restoring
the directory or explicitly migrating the authentication topology.

上游 MeshCentral 在 domain 未设置 `auth` 时支持原生账号；本 Module 设置了
`auth: ldap`，该 domain 不会同时接受原生本地管理员。因此不声明托管本地账号。
管理员权限来自 Samba AD `ldapSiteAdminGroups`；恢复方式是恢复目录服务，或专门迁移
认证拓扑，而不是轮换本地密码。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`1.2.4-r3`（reviewed 2026-08-13）
- Timezone / 时区：`container` — MeshCentral receives TZ through the module .env for process and log timestamps.
- Language scope / 语言范围：MeshCentral Web UI
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：MeshCentral translation key
- Fallback / 回退：User localization preference and browser language are used; unmatched languages fall back to English.
- Supported languages / 支持语言（30）：`ar`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `fi`, `fr`, `he`, `hi`, `hr`, `hu`, `it`, `ja`, `ko`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sr`, `sv`, `tr`, `uk`, `zh-Hans`, `zh-Hant`
- Notes / 说明：Upstream zh-chs and zh-cht are documented as canonical zh-Hans and zh-Hant.

Evidence / 证据：

- [1.2.4 — unique language keys in translate.json](https://github.com/Ylianst/MeshCentral/blob/1.2.4/translate/translate.json)
<!-- generated:localization:end -->
