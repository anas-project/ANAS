# Nextcloud

File sync, sharing, office integration, memories, and Talk.

## 管理员访问 / Administrator access

- 日常登录通过 IAM/SAML，并由 LDAP provisioning 关联目录用户；本地账号不参与日常 SSO。
- 本地恢复账号的 Manifest ID 是 `break_glass`，purpose 也是 `break_glass`。`ACCOUNT` 指这个
  ID，不是用户名。
- 用户名来自全局 `administration.local_accounts.username_template`；默认模板
  `admin_{module}` 在本 Module 解析为 `admin_nextcloud`。可在
  `modules.nextcloud.administration.local_accounts.break_glass.username` 覆盖。物理用户名首次
  物化后锁定。
- 密码没有 YAML 定义。Runner 生成逻辑 Secret
  `ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD`，持久化在 workspace 的
  `.anas/secrets.generated.yml`。首次安装只通过 0600 文件
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

Daily login uses IAM/SAML and LDAP-provisioned directory users. The native
account is only for recovery. Its logical Manifest ID is `break_glass`; the
default physical username is `admin_nextcloud`, while its password is an
independent generated Secret rather than YAML configuration. The direct
`/login?direct=1` route bypasses IAM. Apply and rotation update the real account
through `occ`, verify it, and restore the previous password on failure.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`34.0.2-r3`（reviewed 2026-08-13）
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
