# MeshCentral

Remote device management with OIDC authentication and LDAP directory
provisioning.

## Authentication / 认证

- ANAS registers MeshCentral as a confidential OIDC client. Its callback is
  `/auth-oidc-callback`; the default scopes are `openid profile email`, with
  directory identity and group claims added by the IAM provider.
- OIDC is the normal SSO path. MeshCentral still uses LDAPS for directory user
  and group synchronization, so authentication and provisioning remain
  separate capabilities.
- `APP_meshcentral`, `APP_all`, or the directory administrator group grants
  application access when application filtering is enabled. Membership in the
  configured administrator group grants MeshCentral site-admin rights and is
  re-evaluated at login.

- ANAS 将 MeshCentral 注册为机密 OIDC client，回调地址为
  `/auth-oidc-callback`。默认 scope 是 `openid profile email`，IAM provider
  另外发布目录身份和组 claims。
- OIDC 是日常 SSO 路径；LDAPS 继续负责目录用户和组同步，因此认证与 provisioning
  是两条独立链路。
- 启用应用过滤时，`APP_meshcentral`、`APP_all` 或目录管理员组可进入应用；目录管理员组
  还会授予 MeshCentral site-admin 权限，并在每次登录时重新核对。

## Administrator access / 管理员入口

Upstream MeshCentral supports native username/password accounts when domain
`auth` is unset. This Module keeps `auth: ldap` for directory synchronization
and adds OIDC as an authentication strategy; it does not create a separate
native recovery administrator and therefore declares no
`management.local_accounts`. Administration comes from the Samba AD admin
group through both the OIDC group claim and `ldapSiteAdminGroups`. Recovery
means restoring the directory/IAM path or explicitly migrating the
authentication topology.

上游 MeshCentral 在 domain 未设置 `auth` 时支持原生账号；本 Module 保留
`auth: ldap` 做目录同步，并增加 OIDC authentication strategy，但不会创建另一套原生恢复
管理员，因此不声明 `management.local_accounts`。管理员权限同时来自 OIDC group claim
和 Samba AD `ldapSiteAdminGroups`。恢复方式是修复目录/IAM 链路，或专门迁移认证拓扑，
而不是轮换本地密码。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`1.2.4-r4`（reviewed 2026-08-13）
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
