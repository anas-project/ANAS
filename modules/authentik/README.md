# authentik

Identity provider serving OIDC and SAML with per-application endpoints.

## Administrator access / 管理员入口

Samba AD `Admins` 由 LDAP Source 显式映射为 Authentik superuser；移组后下一次 Source
同步会撤销 superuser。`APP_all` 和 `APP_authentik` 只表示访问权，不会提权。

Samba AD `Admins` is explicitly mapped to Authentik superusers by the LDAP Source;
the next source sync revokes superuser after membership removal. `APP_all` and
`APP_authentik` grant access only.

Authentik keeps its upstream built-in `akadmin` user as an IAM-outage recovery
account. The Manifest account ID is `break_glass` (not the username), while the
fixed physical username is `akadmin`. ANAS supplies first-boot state through a
`0600` runtime file and applies later rotations through `ak shell` with verification.

Authentik 保留上游内置的 `akadmin` 作为 IAM 故障恢复账号。Manifest 账号 ID 是
`break_glass`（不是用户名），实际固定用户名是 `akadmin`。首次启动使用 `0600`
运行时文件，后续轮换通过 `ak shell` 更新并验证。

```bash
anas admin local credential authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass --prompt -w /srv/anas
```

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2026.5.6-r4`（reviewed 2026-08-13）
- Timezone / 时区：`container` — All long-running authentik services receive the module .env and TZ; no separate application timezone is forced.
- Language scope / 语言范围：authentik Web UI
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：authentik locale code
- Fallback / 回退：Browser negotiation first; authentik falls back to English when no packaged locale matches.
- Supported languages / 支持语言（17）：`cs-CZ`, `de-DE`, `en`, `en-XA`, `es-ES`, `fi-FI`, `fr-FR`, `it-IT`, `ja-JP`, `ko-KR`, `nl-NL`, `pl-PL`, `pt-BR`, `ru-RU`, `tr-TR`, `zh-Hans`, `zh-Hant`
- Notes / 说明：The list describes packaged frontend locales. ANAS does not force a locale and preserves authentik's browser locale selector.

Evidence / 证据：

- [2026.5.6 — web/lit-localize.json targetLocales](https://github.com/goauthentik/authentik/blob/version/2026.5.6/web/lit-localize.json)
<!-- generated:localization:end -->
