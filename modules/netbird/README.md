# NetBird

Incomplete WireGuard overlay network scaffold; excluded from recommended deployments.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.76.1-r2`（reviewed 2026-08-13）
- Timezone / 时区：`partial` — Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- Language scope / 语言范围：NetBird Dashboard v2.90.9
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：English is the only Dashboard language in the fixed source version.
- Supported languages / 支持语言（1）：`en`
- Notes / 说明：NetBird desktop client's i18n package is a different component and must not be used to claim Dashboard languages.

Evidence / 证据：

- [v2.90.9 — source tree contains no locale, i18n, or translation resources](https://github.com/netbirdio/dashboard/tree/v2.90.9)
<!-- generated:localization:end -->
