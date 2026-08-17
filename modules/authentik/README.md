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

## Samba 密码同步行为 / Samba password behavior

`ldap_password_writeback` 默认开启，目录用户在 Authentik 修改密码时会写回 Samba AD，
不会建立第二套业务用户密码。ANAS 把 Samba 的最小长度同步到 Authentik 默认改密策略，
并在默认改密页面显示复杂度开关、历史次数和最小改密间隔。为避免 Authentik 拒绝 Samba
本会接受的密码，这条 Samba 支持的默认流程关闭 zxcvbn，并把独立字符类别计数设为零。
修改 Samba 策略后需要重新执行 ANAS apply/reconcile。

提交前，最小长度由 ANAS 策略精确检查；Authentik 的 AD 校验器从目录读取
`pwdProperties`，按三类/五类规则检查复杂度，并检查 `sAMAccountName` 和显示名称。ANAS
的固定版本派生镜像修复了上游用户名包含判断方向错误。历史密码和最小改密间隔无法从
LDAP 预读，只在页面提示并由 Samba 最终裁决；Authentik Enterprise 的 Password
Uniqueness 策略维护的是 Authentik 自己的历史，不能代替 Samba 历史。

写回失败时，LDAP 19/53 映射为包含长度、复杂度、姓名、历史和最小间隔的“域策略拒绝”
说明，50 映射为服务账号权限不足，32 映射为目录用户不存在，其余错误使用目录暂不可用
的安全回退。原始 LDAP result、message 和 description 仍进入 Authentik 事件，绝不直接
暴露给终端用户。19/53 无法稳定指出某一条具体规则，因此界面不会做虚假的精细归因。

This behavior applies to Authentik's default password-change prompt and LDAP
writeback path. Minimum length is checked locally; Authentik's AD validator reads
`pwdProperties` and applies the three-of-five complexity and account/display-name
checks. Samba alone enforces password history and minimum age. LDAP results 19/53
receive broad policy guidance, while 50 and 32 map to insufficient access and a
missing directory user; raw diagnostics remain in Authentik events.

写回成功后，ANAS 会立即把同步目录用户的 Authentik 本地密码恢复为 unusable，避免 Samba
密码之外再保留一份可登录的本地哈希；固定的 `akadmin` 恢复账号不受影响。服务器 E2E
`test-env/scripts/server-authentik-password-policy-e2e.sh` 会同时验证此不变量、旧/新 Samba
凭据、历史拒绝和管理员事件。

Forced first-login password change is a separate capability and is not claimed by
this implementation merely because ordinary writeback works. Guidance language
comes from deployment `DEFAULT_LANGUAGE`; the rest of the Authentik UI continues
to use browser locale negotiation. This module remains `developing` and is not
automatically selected over the active IAM provider. See
[Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#samba-目录密码接入规范).

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
