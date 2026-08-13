# Collabora Online

Online document editing backend for Nextcloud.

## Administrator access / 管理员入口

Collabora 未直接接入 IAM。管理控制台用户名遵守 `admin_<module>` 规则，默认为
`admin_collabora`，可通过 Module 自己的 `admin_username` 设置。`admin_password` 未设置时
生成并持久化独立的 `COLLABORA_ADMIN_PASSWORD` Secret，不再复用 Samba 或任何全局密码。
当前没有热更新和验证 handler，因此不声明托管本地账号，也不支持
`anas admin local rotate collabora`。

Collabora is not directly IAM-integrated. Its admin-console username follows the
`admin_<module>` convention (`admin_collabora` by default) and is controlled by
`admin_username`. An independent `COLLABORA_ADMIN_PASSWORD` Secret is generated
when `admin_password` is omitted. No verified hot-rotation handler is declared.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`26.4.2-r2`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The Collabora service receives TZ through the module .env.
- Language scope / 语言范围：Collabora Online editor UI and document locale
- Selection / 选择方式：`integration`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：BCP 47
- Fallback / 回退：Nextcloud/WOPI passes the user or browser locale; Collabora defaults to en-US when the integration supplies none.
- Supported languages / 支持语言（43）：`sq`, `ar`, `hy`, `eu`, `bg`, `ca`, `zh-Hans`, `zh-Hant`, `hr`, `cs`, `da`, `nl`, `en-GB`, `en-US`, `eo`, `fi`, `fr`, `gl`, `de`, `el`, `he`, `hu`, `is`, `id`, `ga`, `it`, `ja`, `kk`, `nb`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `es`, `sv`, `ta`, `tr`, `uk`, `vi`, `cy`
- Notes / 说明：Do not set container LANG to select the editor UI. Language is a per-session WOPI value.

Evidence / 证据：

- [26.04.2.4.1 — WOPI lang parameter and published UI language inventory](https://sdk.collaboraonline.com/CO-SDK-manual.pdf)
<!-- generated:localization:end -->
