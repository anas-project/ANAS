# authentik

提供 OIDC 与 SAML 的身份提供方，并从 Samba AD 同步用户和组。

> [!WARNING]
> 当前生命周期为 `developing`，仅用于开发和验证，不属于推荐生产部署。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `authentik` |
| 版本 / revision | `2026.5.6-r4` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |

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

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | `authentik` | `AUTHENTIK_DB_NAME` | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `authentik.db_type` | string | `auto` | `AUTHENTIK_DB_TYPE` | 否 | 否 | 否：`migrate-authentik-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `authentik.domain_prefix` | string | `auth` | `AUTHENTIK_DOMAIN_PREFIX` | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `authentik.ldap_enabled` | string | `true` | `AUTHENTIK_LDAP_ENABLED` | 否 | 否 | 是 | `container_recreate` | 是否启用 LDAP Source |
| `authentik.ldap_password_writeback` | string | `true` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | 否 | 否 | 是 | `container_recreate` | 是否允许目录密码回写 |
| `authentik.log_level` | string | `warn` | `AUTHENTIK_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | 日志级别 |

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
