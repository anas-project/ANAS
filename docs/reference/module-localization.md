# Module 时区与语言支持矩阵

本页记录按各 Module 当前上游版本核实的行为，由 `modules/*/localization.yml` 生成。修改清单后运行 `go run ./cmd/gen-module-docs`，不要直接编辑本页。

未填写时，时区和语言继承宿主机；locale 依次采用带明确地区的显式语言、宿主 locale、CLDR 推断，最后回退 `en-US`。`TZ` 是应用广泛消费的运行时约定；`DEFAULT_LANGUAGE` 和 `DEFAULT_LOCALE` 只有在表中标为 `applied` 或 `fallback` 时才真正影响应用。标记为浏览器选择的 Module 仍优先采用用户或浏览器语言。

| Module | Version | Timezone | Language | Selection | Global language | Global locale | Count |
| --- | --- | --- | --- | --- | --- | --- | ---: |
| [authentik](#authentik) | 2026.5.6-r14 | container | supported | browser | not_consumed | not_consumed | 17 |
| [casdoor](#casdoor) | 3.143.0-r6 | container | supported | application | applied | not_consumed | 2 |
| [collabora](#collabora) | 26.4.2-r5 | container | supported | integration | not_consumed | not_consumed | 43 |
| [ddns_go](#ddns_go) | 6.17.4-r6 | container | supported | application | not_consumed | not_consumed | 2 |
| [ddns_updater](#ddns_updater) | 2.10.0-r4 | application | fixed | fixed | not_consumed | not_consumed | 1 |
| [eturnal](#eturnal) | 1.12.2-r6 | container | not_applicable | none | not_applicable | not_applicable | 0 |
| [forgejo](#forgejo) | 15.0.7-r1 | configured | supported | application | fallback | not_consumed | 31 |
| [freeradius](#freeradius) | 3.2.10-r4 | container | not_applicable | none | not_applicable | not_applicable | 0 |
| [lam](#lam) | 9.6.0-r8 | application | supported | deployment_default | applied | not_consumed | 15 |
| [lego](#lego) | 5.3.1-r5 | container | not_applicable | none | not_applicable | not_applicable | 0 |
| [llng](#llng) | 2.23.2-r11 | container | supported | browser | not_consumed | not_consumed | 17 |
| [mariadb](#mariadb) | 12.3.2-r4 | container | supported | browser | not_consumed | not_consumed | 47 |
| [meshcentral](#meshcentral) | 1.2.4-r8 | container | supported | browser | not_consumed | not_consumed | 30 |
| [netbird](#netbird) | 0.76.1-r5 | partial | fixed | fixed | not_consumed | not_consumed | 1 |
| [nextcloud](#nextcloud) | 34.0.2-r9 | partial | supported | browser | fallback | fallback | 58 |
| [oauth2_proxy](#oauth2_proxy) | 7.15.3-r4 | container | fixed | fixed | not_consumed | not_consumed | 1 |
| [postgres](#postgres) | 18.4.0-r4 | container | supported | browser | not_consumed | not_consumed | 47 |
| [samba_dc](#samba_dc) | 4.23.6-r10 | system | not_applicable | none | not_applicable | not_applicable | 0 |
| [samba_fs](#samba_fs) | 4.23.6-r6 | container | not_applicable | client | not_applicable | not_applicable | 0 |
| [traefik](#traefik) | 3.7.10-r5 | container | fixed | fixed | not_consumed | not_consumed | 1 |
| [versitygw](#versitygw) | 1.7.0-r3 | container | not_applicable | none | not_consumed | not_consumed | 0 |
| [vikunja](#vikunja) | 2.4.0-r4 | configured | supported | application | fallback | not_consumed | 32 |

## authentik

- **Version / 版本：** `2026.5.6-r14`; reviewed 2026-08-21
- **Timezone / 时区：** `container` — All long-running authentik services receive the module .env and TZ; no separate application timezone is forced.
- **Language / 语言：** `supported`, `browser` — authentik Web UI
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Browser negotiation first; authentik falls back to English when no packaged locale matches.
- **Supported / 支持语言：** `cs-CZ`, `de-DE`, `en`, `en-XA`, `es-ES`, `fi-FI`, `fr-FR`, `it-IT`, `ja-JP`, `ko-KR`, `nl-NL`, `pl-PL`, `pt-BR`, `ru-RU`, `tr-TR`, `zh-Hans`, `zh-Hant`
- **Notes / 说明：** The list describes packaged frontend locales. ANAS does not force a locale and preserves authentik's browser locale selector.
- **Evidence / 证据：** [2026.5.6 — web/lit-localize.json targetLocales](https://github.com/goauthentik/authentik/blob/version/2026.5.6/web/lit-localize.json)

## casdoor

- **Version / 版本：** `3.143.0-r6`; reviewed 2026-08-27
- **Timezone / 时区：** `container` — Casdoor receives TZ through the module environment; no separate application timezone is forced.
- **Language / 语言：** `supported`, `application` — Casdoor Web UI default
- **ANAS globals / 全局默认：** `default_language=applied`; `default_locale=not_consumed`
- **Fallback / 回退：** ANAS maps zh-prefixed defaults to zh and all other values to en; users may change the UI language in Casdoor.
- **Supported / 支持语言：** `en`, `zh`
- **Notes / 说明：** This inventory records the two ANAS-selected defaults, not every translation shipped by upstream.
- **Evidence / 证据：** [v3.143.0 — web/src/locales English and Chinese resources](https://github.com/casdoor/casdoor/tree/v3.143.0/web/src/locales)

## collabora

- **Version / 版本：** `26.4.2-r5`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — The Collabora service receives TZ through the module .env.
- **Language / 语言：** `supported`, `integration` — Collabora Online editor UI and document locale
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Nextcloud/WOPI passes the user or browser locale; Collabora defaults to en-US when the integration supplies none.
- **Supported / 支持语言：** `sq`, `ar`, `hy`, `eu`, `bg`, `ca`, `zh-Hans`, `zh-Hant`, `hr`, `cs`, `da`, `nl`, `en-GB`, `en-US`, `eo`, `fi`, `fr`, `gl`, `de`, `el`, `he`, `hu`, `is`, `id`, `ga`, `it`, `ja`, `kk`, `nb`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `es`, `sv`, `ta`, `tr`, `uk`, `vi`, `cy`
- **Notes / 说明：** Do not set container LANG to select the editor UI. Language is a per-session WOPI value.
- **Evidence / 证据：** [26.04.2.4.1 — WOPI lang parameter and published UI language inventory](https://sdk.collaboraonline.com/CO-SDK-manual.pdf)

## ddns_go

- **Version / 版本：** `6.17.4-r6`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — The service receives TZ through the module .env; upstream does not expose a separate timezone setting.
- **Language / 语言：** `supported`, `application` — ddns-go Web UI and logs
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** The persisted application setting defaults to English; users can switch language in the Web UI.
- **Supported / 支持语言：** `en`, `zh-CN`
- **Notes / 说明：** ddns-go uses zh-cn internally; ANAS documentation exposes the canonical BCP 47 tag zh-CN.
- **Evidence / 证据：** [v6.17.4 — static/i18n.js I18N_MAP](https://github.com/jeessy2/ddns-go/blob/v6.17.4/static/i18n.js); [v6.17.4 — persisted language selector](https://github.com/jeessy2/ddns-go/blob/v6.17.4/web/set_lang.go)

## ddns_updater

- **Version / 版本：** `2.10.0-r4`; reviewed 2026-08-13
- **Timezone / 时区：** `application` — Upstream officially accepts the IANA TZ environment variable for Web UI and log timestamps.
- **Language / 语言：** `fixed`, `fixed` — DDNS Updater Web UI
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** English is the only UI language in the fixed source version.
- **Supported / 支持语言：** `en`
- **Evidence / 证据：** [v2.10.0 — source tree contains no locale or translation resources](https://github.com/qdm12/ddns-updater/tree/v2.10.0)

## eturnal

- **Version / 版本：** `1.12.2-r6`; reviewed 2026-08-17
- **Timezone / 时区：** `container` — The TURN service receives TZ for process and log timestamps.
- **Language / 语言：** `not_applicable`, `none` — TURN protocol service
- **ANAS globals / 全局默认：** `default_language=not_applicable`; `default_locale=not_applicable`
- **Fallback / 回退：** No user-facing language exists.
- **Supported / 支持语言：** not applicable / 不适用
- **Evidence / 证据：** [1.12.2 — protocol service without a localized UI](https://github.com/processone/eturnal/tree/1.12.2)

## forgejo

- **Version / 版本：** `15.0.7-r1`; reviewed 2026-08-22
- **Timezone / 时区：** `configured` — Forgejo time.DEFAULT_UI_LOCATION inherits ANAS TZ; signed-in users may retain their own UI preference.
- **Language / 语言：** `supported`, `application` — Forgejo Web UI
- **ANAS globals / 全局默认：** `default_language=fallback`; `default_locale=not_consumed`
- **Fallback / 回退：** The matched ANAS language is moved to the first position; unmatched values warn and fall back to en-US, while browser and saved user choices take precedence.
- **Supported / 支持语言：** `en-US`, `zh-CN`, `zh-HK`, `zh-TW`, `da`, `de-DE`, `nds`, `fr-FR`, `nl-NL`, `lv-LV`, `ru-RU`, `uk-UA`, `ja-JP`, `es-ES`, `pt-BR`, `pt-PT`, `pl-PL`, `bg`, `it-IT`, `fi-FI`, `fil`, `eo`, `tr-TR`, `cs-CZ`, `sl`, `sv-SE`, `ko-KR`, `el-GR`, `fa-IR`, `hu-HU`, `id-ID`
- **Notes / 说明：** Forgejo uses the first configured locale only when the browser does not match a listed locale. The hook keeps the complete pinned list and reorders the selected default instead of removing choices.
- **Evidence / 证据：** [v15.0 — i18n LANGS and NAMES](https://forgejo.org/docs/v15.0/admin/config-cheat-sheet/)

## freeradius

- **Version / 版本：** `3.2.10-r4`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — The RADIUS service receives TZ for process and log timestamps.
- **Language / 语言：** `not_applicable`, `none` — RADIUS protocol service
- **ANAS globals / 全局默认：** `default_language=not_applicable`; `default_locale=not_applicable`
- **Fallback / 回退：** No user-facing language exists.
- **Supported / 支持语言：** not applicable / 不适用
- **Evidence / 证据：** [3.2.10 — protocol service without a localized UI](https://github.com/FreeRADIUS/freeradius-server/tree/release_3_2_10)

## lam

- **Version / 版本：** `9.6.0-r8`; reviewed 2026-08-13
- **Timezone / 时区：** `application` — ANAS writes the IANA TZ value to the LAM profile timeZone setting.
- **Language / 语言：** `supported`, `deployment_default` — LDAP Account Manager Web UI
- **ANAS globals / 全局默认：** `default_language=applied`; `default_locale=not_consumed`
- **Fallback / 回退：** CLDR language matching chooses the closest same-script LAM locale, then English.
- **Supported / 支持语言：** `de-DE`, `en-GB`, `en-US`, `es-ES`, `fr-FR`, `el-GR`, `it-IT`, `nl-NL`, `pl-PL`, `pt-BR`, `sk-SK`, `uk-UA`, `ja-JP`, `zh-TW`, `zh-CN`
- **Notes / 说明：** The ANAS image generates every listed POSIX locale. BCP 47 input is converted by the localization library, not by string replacement.
- **Evidence / 证据：** [9.6 — active entries in lam/config/language](https://github.com/LDAPAccountManager/lam/blob/9.6/lam/config/language)

## lego

- **Version / 版本：** `5.3.1-r5`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — The certificate worker receives TZ for process and log timestamps.
- **Language / 语言：** `not_applicable`, `none` — certificate automation CLI
- **ANAS globals / 全局默认：** `default_language=not_applicable`; `default_locale=not_applicable`
- **Fallback / 回退：** No user-facing language exists.
- **Supported / 支持语言：** not applicable / 不适用
- **Evidence / 证据：** [v5.3.1 — CLI without localized UI resources](https://github.com/go-acme/lego/tree/v5.3.1)

## llng

- **Version / 版本：** `2.23.2-r11`; reviewed 2026-08-21
- **Timezone / 时区：** `container` — LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- **Language / 语言：** `supported`, `browser` — LemonLDAP::NG Portal and language selector
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Portal language selector and Accept-Language are used; unmatched requests fall back to English.
- **Supported / 支持语言：** `ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- **Notes / 说明：** Manager and mail templates have separate translation resources; the list records the user-facing Portal inventory.
- **Evidence / 证据：** [v2.23.2 — Portal JSON language directory](https://gitlab.ow2.org/lemonldap-ng/lemonldap-ng/-/tree/v2.23.2/lemonldap-ng-portal/site/htdocs/static/languages)

## mariadb

- **Version / 版本：** `12.3.2-r4`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — MariaDB and optional Adminer receive TZ; this does not populate MariaDB timezone tables or change SQL session time_zone.
- **Language / 语言：** `supported`, `browser` — optional Adminer 5.5.0 Web UI; MariaDB itself has no UI language
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Adminer negotiates browser language and falls back to English.
- **Supported / 支持语言：** `ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fa`, `fi`, `fr`, `gl`, `he`, `hi`, `hr`, `hu`, `id`, `it`, `ja`, `ka`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt-BR`, `pt`, `ro`, `ru`, `sk`, `sl`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `uz`, `vi`, `zh-TW`, `zh`
- **Evidence / 证据：** [v5.5.0 — Adminer language files; xx test locale excluded](https://github.com/vrana/adminer/tree/v5.5.0/adminer/lang)

## meshcentral

- **Version / 版本：** `1.2.4-r8`; reviewed 2026-08-21
- **Timezone / 时区：** `container` — MeshCentral receives TZ through the module .env for process and log timestamps.
- **Language / 语言：** `supported`, `browser` — MeshCentral Web UI
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** User localization preference and browser language are used; unmatched languages fall back to English.
- **Supported / 支持语言：** `ar`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `fi`, `fr`, `he`, `hi`, `hr`, `hu`, `it`, `ja`, `ko`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sr`, `sv`, `tr`, `uk`, `zh-Hans`, `zh-Hant`
- **Notes / 说明：** Upstream zh-chs and zh-cht are documented as canonical zh-Hans and zh-Hant.
- **Evidence / 证据：** [1.2.4 — unique language keys in translate.json](https://github.com/Ylianst/MeshCentral/blob/1.2.4/translate/translate.json)

## netbird

- **Version / 版本：** `0.76.1-r5`; reviewed 2026-08-13
- **Timezone / 时区：** `partial` — Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- **Language / 语言：** `fixed`, `fixed` — NetBird Dashboard v2.90.9
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** English is the only Dashboard language in the fixed source version.
- **Supported / 支持语言：** `en`
- **Notes / 说明：** NetBird desktop client's i18n package is a different component and must not be used to claim Dashboard languages.
- **Evidence / 证据：** [v2.90.9 — source tree contains no locale, i18n, or translation resources](https://github.com/netbirdio/dashboard/tree/v2.90.9)

## nextcloud

- **Version / 版本：** `34.0.2-r9`; reviewed 2026-08-21
- **Timezone / 时区：** `partial` — Main, cron, push, Imaginary, and Talk services receive TZ; Redis has no localization behavior.
- **Language / 语言：** `supported`, `browser` — Nextcloud Web UI
- **ANAS globals / 全局默认：** `default_language=fallback`; `default_locale=fallback`
- **Fallback / 回退：** User preference, then browser language, then ANAS default_language, then English.
- **Supported / 支持语言：** `en`, `ar`, `ast`, `be`, `bg`, `ca`, `cs`, `da`, `de`, `de-DE`, `el`, `en-GB`, `eo`, `es`, `es-EC`, `es-MX`, `et-EE`, `eu`, `fa`, `fi`, `fr`, `ga`, `gl`, `hr`, `hu`, `id`, `is`, `it`, `ja`, `ka`, `ko`, `lo`, `lt-LT`, `lv`, `mk`, `mn`, `nb`, `nl`, `pl`, `pt-BR`, `pt-PT`, `ro`, `ru`, `sc`, `sk`, `sl`, `sr`, `sv`, `sw`, `th`, `tr`, `ug`, `uk`, `uz`, `vi`, `zh-CN`, `zh-HK`, `zh-TW`
- **Notes / 说明：** ANAS writes default_language and default_locale only. It never writes force_language or force_locale.
- **Evidence / 证据：** [v34.0.2 — core/l10n JSON files plus English source language](https://github.com/nextcloud/server/tree/v34.0.2/core/l10n); [34 — default_language and default_locale precedence](https://docs.nextcloud.com/server/stable/admin_manual/configuration_server/language_configuration.html)

## oauth2_proxy

- **Version / 版本：** `7.15.3-r4`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — oauth2-proxy receives TZ for process and log timestamps.
- **Language / 语言：** `fixed`, `fixed` — oauth2-proxy built-in error and sign-in pages
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Built-in pages are English; protected applications manage their own language.
- **Supported / 支持语言：** `en`
- **Evidence / 证据：** [v7.15.3 — built-in page templates without locale resources](https://github.com/oauth2-proxy/oauth2-proxy/tree/v7.15.3)

## postgres

- **Version / 版本：** `18.4.0-r4`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — PostgreSQL and optional Adminer receive TZ; database timezone remains an independent SQL setting.
- **Language / 语言：** `supported`, `browser` — optional Adminer 5.5.0 Web UI; PostgreSQL itself has no UI language
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** Adminer negotiates browser language and falls back to English.
- **Supported / 支持语言：** `ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fa`, `fi`, `fr`, `gl`, `he`, `hi`, `hr`, `hu`, `id`, `it`, `ja`, `ka`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt-BR`, `pt`, `ro`, `ru`, `sk`, `sl`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `uz`, `vi`, `zh-TW`, `zh`
- **Evidence / 证据：** [v5.5.0 — Adminer language files; xx test locale excluded](https://github.com/vrana/adminer/tree/v5.5.0/adminer/lang)

## samba_dc

- **Version / 版本：** `4.23.6-r10`; reviewed 2026-08-13
- **Timezone / 时区：** `system` — Startup validates TZ against /usr/share/zoneinfo and installs /etc/localtime and /etc/timezone.
- **Language / 语言：** `not_applicable`, `none` — directory, Kerberos, and DNS protocol services
- **ANAS globals / 全局默认：** `default_language=not_applicable`; `default_locale=not_applicable`
- **Fallback / 回退：** No user-facing Web UI exists; automation should keep LC_ALL=C where stable machine-readable output is required.
- **Supported / 支持语言：** not applicable / 不适用
- **Evidence / 证据：** [4.23.6 — protocol and command-line services without a Module UI](https://www.samba.org/samba/docs/current/man-html/)

## samba_fs

- **Version / 版本：** `4.23.6-r6`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — The file server receives TZ and includes tzdata; client-visible timestamps are also affected by SMB client behavior.
- **Language / 语言：** `not_applicable`, `client` — SMB protocol service
- **ANAS globals / 全局默认：** `default_language=not_applicable`; `default_locale=not_applicable`
- **Fallback / 回退：** File-manager language belongs to each SMB client, not the server Module.
- **Supported / 支持语言：** not applicable / 不适用
- **Evidence / 证据：** [4.23.6 — server protocol configuration; no Module Web UI](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html)

## traefik

- **Version / 版本：** `3.7.10-r5`; reviewed 2026-08-13
- **Timezone / 时区：** `container` — Traefik receives TZ for process and access-log timestamps.
- **Language / 语言：** `fixed`, `fixed` — Traefik Proxy built-in Dashboard
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** The built-in Dashboard is English and exposes no supported language selector.
- **Supported / 支持语言：** `en`
- **Evidence / 证据：** [v3.7.10 — Dashboard and static configuration expose no localization setting](https://github.com/traefik/traefik/tree/v3.7.10)

## versitygw

- **Version / 版本：** `1.7.0-r3`; reviewed 2026-08-22
- **Timezone / 时区：** `container` — The gateway receives TZ through the Module .env for process and access-log timestamps.
- **Language / 语言：** `not_applicable`, `none` — S3 HTTP protocol service
- **ANAS globals / 全局默认：** `default_language=not_consumed`; `default_locale=not_consumed`
- **Fallback / 回退：** S3 responses are protocol-defined and the Module exposes no WebUI.
- **Supported / 支持语言：** not applicable / 不适用
- **Notes / 说明：** Client tools choose their own display language; it is not a gateway setting.
- **Evidence / 证据：** [1.7.0 — cmd/versitygw CLI and S3 API server; WebUI is not enabled by this Module](https://github.com/versity/versitygw/tree/v1.7.0)

## vikunja

- **Version / 版本：** `2.4.0-r4`; reviewed 2026-08-21
- **Timezone / 时区：** `configured` — Vikunja service.timezone and the default timezone for new users inherit ANAS TZ; signed-in users may override their own timezone.
- **Language / 语言：** `supported`, `application` — Vikunja Web UI and localized notifications
- **ANAS globals / 全局默认：** `default_language=fallback`; `default_locale=not_consumed`
- **Fallback / 回退：** New users inherit the matched ANAS default language; unsupported values warn and fall back to English, while saved user preferences take precedence.
- **Supported / 支持语言：** `en`, `de-DE`, `de-CH`, `ru-RU`, `fr-FR`, `vi-VN`, `it-IT`, `cs-CZ`, `pl-PL`, `nl-NL`, `pt-PT`, `zh-CN`, `zh-TW`, `no-NO`, `es-ES`, `da-DK`, `ja-JP`, `hu-HU`, `ar-SA`, `fa-IR`, `sl-SI`, `pt-BR`, `hr-HR`, `uk-UA`, `lt-LT`, `bg-BG`, `ko-KR`, `tr-TR`, `fi-FI`, `he-IL`, `sv-SE`, `el-GR`
- **Notes / 说明：** Browser language is used when the account has no saved language; the ANAS value is a new-user default and does not override an existing preference. Canonical de-CH maps to Vikunja's upstream de-swiss key.
- **Evidence / 证据：** [v2.4.0 — SUPPORTED_LOCALES and DEFAULT_LANGUAGE](https://github.com/go-vikunja/vikunja/blob/v2.4.0/frontend/src/i18n/index.ts)
