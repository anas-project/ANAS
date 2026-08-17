# LemonLDAP::NG

提供门户、应用启动器以及 OIDC/SAML 身份提供服务。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `llng` |
| 版本 / revision | `2.23.2-r5` |
| 状态 | `release` |
| 类别 | `identity` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | 提供 Capability | `oidc, saml` |

## 最简配置

```yaml
modules:
  llng: {}
```

## 身份、用户与 Group

Samba AD 是用户和 Group 来源。Portal 使用目录认证，IAM 向 Consumer 发布 OIDC/SAML 端点和 Group 属性。`Admins` 可进入 Manager。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

当前没有独立的本地 `break_glass`。Manager 与 Portal 共用目录认证；IAM 或目录故障时需要从主机侧修复，不能依赖不存在的本地密码。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres`, `mariadb` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 为本 Module 创建专属数据库、用户和稳定生成凭据。修改 `db_type`/`db_name` 不会迁移现有数据。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.adminer_enabled` | string | `false` | `LLNG_ADMINER_ENABLED` | 否 | 否 | 是 | `container_recreate` | 是否启用 Adminer |
| `llng.db_name` | string | `lemonldap_ng` | `LLNG_DB_NAME` | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `llng.db_type` | string | `auto` | `LLNG_DB_TYPE` | 否 | 否 | 否：`migrate-llng-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `llng.domain_prefix` | string | `auth` | `LLNG_DOMAIN_PREFIX` | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `llng.enable_test` | string | `true` | `LLNG_ENABLE_TEST` | 否 | 否 | 是 | `container_recreate` | 是否启用测试入口 |
| `llng.log_level` | string | `warn` | `LLNG_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `llng.manager_domain_prefix` | string | `auth-manager` | `LLNG_MANAGER_DOMAIN_PREFIX` | 否 | 否 | 是 | `container_recreate` | Manager 域名前缀 |
| `llng.test_domain_prefix` | string | `auth-test` | `LLNG_TEST_DOMAIN_PREFIX` | 否 | 否 | 是 | `container_recreate` | 测试入口域名前缀 |

### 查询和修改

```bash
anas config list llng -w /srv/anas
anas config explain llng.adminer_enabled
anas config set llng.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list llng -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

不要配置已删除的 `LLNG_PASSWORD`；它不会创建上游管理员。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.23.2-r5`（reviewed 2026-08-17）
- Timezone / 时区：`container` — LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- Language scope / 语言范围：LemonLDAP::NG Portal and language selector
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：LLNG JSON language code
- Fallback / 回退：Portal language selector and Accept-Language are used; unmatched requests fall back to English.
- Supported languages / 支持语言（17）：`ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- Notes / 说明：Manager and mail templates have separate translation resources; the list records the user-facing Portal inventory.

Evidence / 证据：

- [v2.23.2 — Portal JSON language directory](https://gitlab.ow2.org/lemonldap-ng/lemonldap-ng/-/tree/v2.23.2/lemonldap-ng-portal/site/htdocs/static/languages)
<!-- generated:localization:end -->
