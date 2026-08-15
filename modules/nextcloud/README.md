# Nextcloud

File sync, sharing, office integration, memories, and Talk.

## 管理员访问 / Administrator access

- 日常登录默认通过 IAM/OIDC；也可把 `nextcloud.iam_protocol` 显式设为 `saml`。
  两种协议都只负责认证，用户和组仍由 LDAP provisioning 管理，本地账号不参与日常 SSO。
- Samba AD `Admins` 通过 Nextcloud LDAP administrative group 动态映射为 `admin` 权限；
  移出该目录组后权限随映射撤销，不维护单独的本地管理员成员关系。
- OIDC 使用官方 `user_oidc` 应用。OIDC `preferred_username` 对齐 LDAP 的
  `sAMAccountName`/Internal Username，因而登录复用现有 LDAP 用户；目录
  `anasIdentityAnchor` 仍作为 IAM claim 发布，用于跨系统身份核验。
- 本地恢复账号的 Manifest ID 是 `break_glass`，purpose 也是 `break_glass`。`ACCOUNT` 指这个
  ID，不是用户名。
- 用户名由 ANAS 固定默认模板确定；`admin_{module}` 在本 Module 解析为
  `admin_nextcloud`。用户名不可配置，首次物化后锁定，也不提供 rename 命令。
- 密码没有 YAML 定义。Runner 生成逻辑 Secret
  `ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD`，持久化在 workspace 的
  `.anas/secrets.yml`。首次安装只通过 0600 文件
  `.anas/runtime-secrets/local-admins/nextcloud/break_glass.password` 交给官方 entrypoint；
  明文不进入 deployment `.env`、lock 或 manifest。
- IAM 故障恢复入口是 `https://<nextcloud>/login?direct=1`，可由 credential 命令一起查询。
- apply/rotate handler 使用 `occ user:resetpassword --password-from-env` 更新应用内部账号，
  再通过 Nextcloud 用户管理器验证；轮换失败恢复旧密码。

```bash
anas admin local credential nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass --prompt -w /srv/anas
```

Daily login uses IAM/OIDC by default; setting `nextcloud.iam_protocol` to
`saml` keeps the existing SAML path available. Both protocols authenticate
LDAP-provisioned users rather than creating a second user backend. The OIDC
`preferred_username` claim matches the LDAP `sAMAccountName` internal username,
while `anasIdentityAnchor` remains available for cross-system identity checks.
Samba AD `Admins` is promoted as Nextcloud's dynamic LDAP administrative group;
membership removal revokes application administration without a sticky local
group assignment.
The native account is only for recovery. Its logical Manifest ID is
`break_glass`; the default physical username is `admin_nextcloud`, while its
password is an independent generated Secret rather than YAML configuration.
The direct `/login?direct=1` route bypasses IAM. Apply and rotation update the
real account through `occ`, verify it, and restore the previous password on
failure.

```bash
anas config set nextcloud.iam_protocol oidc -w /srv/anas
# Optional fallback:
anas config set nextcloud.iam_protocol saml -w /srv/anas
```

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`34.0.2-r4`（reviewed 2026-08-13）
- Timezone / 时区：`partial` — Main, cron, push, Imaginary, and Talk services receive TZ; Redis has no localization behavior.
- Language scope / 语言范围：Nextcloud Web UI
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=fallback`; `default_locale=fallback`
- Upstream format / 上游格式：Nextcloud language code with underscore region
- Fallback / 回退：User preference, then browser language, then ANAS default_language, then English.
- Supported languages / 支持语言（58）：`en`, `ar`, `ast`, `be`, `bg`, `ca`, `cs`, `da`, `de`, `de-DE`, `el`, `en-GB`, `eo`, `es`, `es-EC`, `es-MX`, `et-EE`, `eu`, `fa`, `fi`, `fr`, `ga`, `gl`, `hr`, `hu`, `id`, `is`, `it`, `ja`, `ka`, `ko`, `lo`, `lt-LT`, `lv`, `mk`, `mn`, `nb`, `nl`, `pl`, `pt-BR`, `pt-PT`, `ro`, `ru`, `sc`, `sk`, `sl`, `sr`, `sv`, `sw`, `th`, `tr`, `ug`, `uk`, `uz`, `vi`, `zh-CN`, `zh-HK`, `zh-TW`
- Notes / 说明：ANAS writes default_language and default_locale only. It never writes force_language or force_locale.

Evidence / 证据：

- [v34.0.2 — core/l10n JSON files plus English source language](https://github.com/nextcloud/server/tree/v34.0.2/core/l10n)
- [34 — default_language and default_locale precedence](https://docs.nextcloud.com/server/stable/admin_manual/configuration_server/language_configuration.html)
<!-- generated:localization:end -->
