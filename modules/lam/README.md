# LDAP Account Manager

Web UI for LDAP account administration.

## Administrator access / 管理员入口

LAM 未接入 IAM。主登录页允许 Samba `Admins` 组的已启用用户登录；每位用户输入自己的
`sAMAccountName` 和目录密码。组成员搜索由只读 `svc_ldap` 完成，认证及后续目录操作使用
登录用户自己的 LDAPS 身份。界面中的 `lam` 是默认服务器配置 profile 名，不是用户名。

`Admins` 在这里授予 LAM 的最高应用级访问，即完整管理界面，但不会自动授予域管理员
权限。用户进入 LAM 后能执行的
读写操作仍由其 Samba AD 组成员关系和目录 ACL 决定；需要域级管理的专用账号还必须按
Samba 管理策略获得相应权限。

LAM 自己的 `admin_password` 只保护“LAM configuration”和服务器 profile 编辑；该界面没有
配套用户名。省略时生成并持久化独立的 `LAM_ADMIN_PASSWORD` Secret。当前未声明可事务
轮换的 `management.local_accounts`，因此不支持 `anas admin local rotate lam`。

LAM is not IAM-integrated. Its main login accepts enabled members of Samba's
`Admins` group; each user enters their own `sAMAccountName` and directory password.
The read-only `svc_ldap` identity performs the DN lookup, while authentication and
subsequent directory operations use the login user's own LDAPS identity. `lam` is
the default server-profile name, not a username. Group membership grants LAM's
full application-level administration surface but not domain-administration
rights; Samba AD groups and
directory ACLs remain authoritative. The module-owned `admin_password` protects
LAM configuration/profile editing, which has no username field; omission creates
an independent `LAM_ADMIN_PASSWORD` Secret.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`9.6.0-r5`（reviewed 2026-08-13）
- Timezone / 时区：`application` — ANAS writes the IANA TZ value to the LAM profile timeZone setting.
- Language scope / 语言范围：LDAP Account Manager Web UI
- Selection / 选择方式：`deployment_default`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：POSIX locale ending in .utf8
- Fallback / 回退：CLDR language matching chooses the closest same-script LAM locale, then English.
- Supported languages / 支持语言（15）：`de-DE`, `en-GB`, `en-US`, `es-ES`, `fr-FR`, `el-GR`, `it-IT`, `nl-NL`, `pl-PL`, `pt-BR`, `sk-SK`, `uk-UA`, `ja-JP`, `zh-TW`, `zh-CN`
- Notes / 说明：The ANAS image generates every listed POSIX locale. BCP 47 input is converted by the localization library, not by string replacement.

Evidence / 证据：

- [9.6 — active entries in lam/config/language](https://github.com/LDAPAccountManager/lam/blob/9.6/lam/config/language)
<!-- generated:localization:end -->
