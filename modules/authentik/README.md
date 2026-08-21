# authentik

提供 OIDC 与 SAML 的身份提供方，并从 Samba AD 同步用户和组。

> [!WARNING]
> 当前生命周期为 `developing`，仅用于开发和验证，不属于推荐生产部署。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `authentik` |
| 版本 / revision | `2026.5.6-r9` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | 提供 Capability | `oidc, saml` |

## 最简配置

```yaml
modules:
  authentik: {}
```

## 身份、用户与 Group

Samba AD 是人员与组的事实来源。LDAP Source 通过 LDAPS 同步用户和组；`ldap_password_writeback` 控制是否允许 Authentik 使用受限服务账号回写普通用户密码。应用登录使用按 Consumer 生成的 OIDC 或 SAML 端点。`Admins` 映射为 Authentik superuser，`APP_all`/`APP_authentik` 只授予访问权。

固定 Authentik `2026.5.6` 对声明标准端点的 OIDC Consumer 优先使用 back-channel logout，并登记独立 logout redirect URI；是否覆盖浏览器登出、管理员删 session 和账号停用按具体 Consumer E2E 分别记录。SAML Redirect 和 POST 都是浏览器 binding，统一映射为 `frontchannel_native`，绝不把普通 POST 解释为后台撤销。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| 目录密码回写 | `ldap_password_writeback` / restricted bind |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

日常管理员通过目录身份登录。固定用户名 `akadmin` 是 `break_glass` 恢复账号；它使用独立生成密码，不复用 Samba 或数据库管理员凭据。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `AUTHENTIK_DOMAIN_FULL` | `iam` |
| `local_recovery` | `AUTHENTIK_BREAK_GLASS_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `akadmin` | `plaintext_on_bootstrap` | 是 |

```bash
anas admin local list -w /srv/anas
anas admin local credential authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass --prompt -w /srv/anas
```

`credential` 会输出明文密码，应避免进入日志；`rotate` 默认生成随机密码，`--prompt` 从终端安全读取，不接受 argv 或普通环境变量传入密码。

## Samba 密码同步行为 / Samba password behavior

`ldap_password_writeback` 默认开启，目录用户在 Authentik 修改密码时会写回 Samba AD，
不会建立第二套业务用户密码。ANAS 把 Samba 的最小长度同步到 Authentik 默认改密策略，
并在默认改密页面显示复杂度开关、历史次数和最小改密间隔。为避免 Authentik 拒绝 Samba
本会接受的密码，这条 Samba 支持的默认流程关闭 zxcvbn，并把独立字符类别计数设为零。
修改 Samba 策略后需要重新执行 ANAS apply/reconcile。

提交前，最小长度由 ANAS 策略精确检查；Authentik 的 AD 校验器从目录读取
`pwdProperties`，按三类/五类规则检查复杂度，并检查 `sAMAccountName` 和显示名称。ANAS
的固定版本派生镜像修复了上游用户名包含判断方向错误。Authentik 使用委派服务账号执行
LDAP 密码重置；Samba 会执行长度、复杂度和姓名规则，但这种重置不会可靠执行用户改密的
历史与最小间隔。Authentik Enterprise 的 Password Uniqueness 是另一份历史状态，且不能
读取改由其他入口设置的 Samba 密码，因此 ANAS 不把它伪装成 Samba 历史。历史次数和最小
改密间隔目前仅显示提示，不能声称已强制执行。

写回失败时，LDAP 19/53 映射为包含长度、复杂度、姓名、历史和最小间隔的“域策略拒绝”
说明，50 映射为服务账号权限不足，32 映射为目录用户不存在，其余错误使用目录暂不可用
的安全回退。原始 LDAP result、message 和 description 仍进入 Authentik 事件，绝不直接
暴露给终端用户。19/53 无法稳定指出某一条具体规则，因此界面不会做虚假的精细归因。

This behavior applies to Authentik's default password-change prompt and LDAP
writeback path. Minimum length is checked locally; Authentik's AD validator reads
`pwdProperties` and applies the three-of-five complexity and account/display-name
checks. Delegated LDAP reset semantics do not reliably enforce Samba history or
minimum age. Authentik Enterprise Password Uniqueness is a separate history
store and cannot see Samba passwords changed through other paths, so ANAS does
not present it as Samba history. History and minimum age are guidance-only.
LDAP results 19/53 receive broad policy guidance, while 50 and 32 map to
insufficient access and a missing directory user; raw diagnostics remain in
Authentik events.

写回成功后，ANAS 会立即把同步目录用户的 Authentik 本地密码恢复为 unusable，避免 Samba
密码之外再保留一份可登录的本地哈希；固定的 `akadmin` 恢复账号不受影响。服务器 E2E
`test-env/scripts/server-authentik-password-policy-e2e.sh` 会同时验证此不变量、Samba
`pwdLastSet` 变化、新密码认证和安全错误映射；历史次数和最小间隔只验证提示状态。测试
不要求旧密码立即失效，因为 Samba 可以按配置在旧密码宽限期内继续接受它。

Forced first-login password change is a separate capability and is not claimed by
this implementation merely because ordinary writeback works. Guidance language
comes from deployment `DEFAULT_LANGUAGE`; the rest of the Authentik UI continues
to use browser locale negotiation. This module remains `developing` and is not
automatically selected over the active IAM provider. See
[Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#samba-目录密码接入规范).

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 为本 Module 创建专属数据库、用户和稳定生成凭据。修改 `db_type`/`db_name` 不会迁移现有数据。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | — | `authentik` | `static` | `AUTHENTIK_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `authentik.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AUTHENTIK_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-authentik-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `authentik.domain_prefix` | string | — | `auth` | `static` | `AUTHENTIK_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `authentik.ldap_enabled` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 LDAP Source |
| `authentik.ldap_password_writeback` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许目录密码回写 |
| `authentik.log_level` | string | — | `warn` | `static` | `AUTHENTIK_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |

### 查询和修改

```bash
anas config list authentik -w /srv/anas
anas config explain authentik.db_name
anas config set authentik.db_name authentik -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list authentik -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

状态为 `developing`；正式支持前仍需以真实容器验证目录同步、组撤权、密码回写和恢复登录。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2026.5.6-r9`（reviewed 2026-08-21）
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
