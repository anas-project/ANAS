# Nextcloud

文件同步、分享、在线文档、Memories 和 Talk 平台。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `nextcloud` |
| 版本 / revision | `34.0.2-r4` |
| 状态 | `release` |
| 类别 | `app` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc, saml` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## 最简配置

```yaml
modules:
  nextcloud: {}
```

本 Module 还要求在部署级选择 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

## 身份、用户与 Group

LDAPS provisioning 管理用户和 Group；OIDC 是默认登录协议，SAML 仍受支持。两条链路通过一致的目录用户名和 `anasIdentityAnchor` 关联。Samba `Admins` 动态映射 Nextcloud 管理员权限。普通目录密码修改通过受限 password bind 服务账号回写，而不是数据库管理员账号。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`；同步 groups |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

日常管理员通过 IAM 登录。`break_glass` 本地恢复账号默认用户名为 `admin_nextcloud`，直接入口为 `/login?direct=1`，可由 ANAS 查询和事务轮换。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `NEXTCLOUD_DOMAIN_FULL` | `iam` |
| `local_recovery` | `NEXTCLOUD_BREAK_GLASS_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `admin_nextcloud` | `plaintext_on_bootstrap` | 是 |

```bash
anas admin local list -w /srv/anas
anas admin local credential nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass --prompt -w /srv/anas
```

`credential` 会输出明文密码，应避免进入日志；`rotate` 默认生成随机密码，`--prompt` 从终端安全读取，不接受 argv 或普通环境变量传入密码。

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
| `nextcloud.db_name` | string | `nextcloud` | `NEXTCLOUD_DB_NAME` | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 应用数据库名 |
| `nextcloud.db_type` | string | `auto` | `NEXTCLOUD_DB_TYPE` | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `nextcloud.domain_prefix` | string | `nc` | `NEXTCLOUD_DOMAIN_PREFIX` | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `nextcloud.iam_protocol` | string | `auto` | `NEXTCLOUD_IAM_PROTOCOL` | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |
| `nextcloud.language` | string | `—` | `NEXTCLOUD_LANGUAGE` | 否 | 否 | 是 | `reconcile` | 界面回退语言 |
| `nextcloud.locale` | string | `—` | `NEXTCLOUD_LOCALE` | 否 | 否 | 是 | `reconcile` | 区域格式回退值 |
| `nextcloud.log_level` | string | `2` | `NEXTCLOUD_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `nextcloud.memories_enabled` | string | `true` | `NEXTCLOUD_MEMORIES_ENABLED` | 否 | 否 | 是 | `reconcile` | 是否启用 Memories |
| `nextcloud.memory_limit` | string | `1G` | `NEXTCLOUD_MEMORY_LIMIT` | 否 | 否 | 是 | `container_recreate` | 内存限制 |
| `nextcloud.phone_region` | string | `CN` | `NEXTCLOUD_PHONE_REGION` | 否 | 否 | 是 | `container_recreate` | 默认电话区域 |
| `nextcloud.rm_skeleton_files` | string | `false` | `NEXTCLOUD_RM_SKELETON_FILES` | 否 | 否 | 是 | `container_recreate` | 是否删除默认骨架文件 |
| `nextcloud.talk_enabled` | string | `true` | `NEXTCLOUD_TALK_ENABLED` | 否 | 否 | 是 | `container_recreate` | 是否启用 Talk |
| `nextcloud.upload_max_size` | string | `16G` | `NEXTCLOUD_UPLOAD_MAX_SIZE` | 否 | 否 | 是 | `container_recreate` | 上传大小上限 |

### 查询和修改

```bash
anas config list nextcloud -w /srv/anas
anas config explain nextcloud.db_name
anas config set nextcloud.domain_prefix nc -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list nextcloud -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

切换 OIDC/SAML 需要重建并协调 IAM 注册；切换数据库不会迁移现有数据。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`34.0.2-r4`（reviewed 2026-08-13）
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
