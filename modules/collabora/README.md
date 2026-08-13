# Collabora Online

Online document editing backend for Nextcloud.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`26.4.2-r1`（reviewed 2026-08-13）
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
