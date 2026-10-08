# Nextcloud

回收站默认禁止用户手动永久删除和清空：模块配置 `files_trash_delete: false` 对应 Nextcloud 官方 `files.trash.delete=false`。自动清理默认开启，`trashbin_retention_obligation: "60,365"` 表示至少保留 60 天、365 天到期；设为 `disabled` 可关闭自动清理。两项均由启动任务通过 `occ config:system:set` 写入，修改后重建容器应用。

文件同步、分享、在线文档、Memories 和 Talk 平台。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `nextcloud` |
| 版本 / revision | `34.0.2-r11` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

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
    provider: casdoor
```

## 回收站配置

默认配置如下，保留期可设为 `disabled` 关闭自动清理：

```yaml
modules:
  nextcloud:
    config:
      files_trash_delete: false
      trashbin_retention_obligation: "60,365"
```

启动任务在容器内以 `www-data` 身份执行：

```bash
php /var/www/html/occ config:system:set files.trash.delete --type=boolean --value=false
php /var/www/html/occ config:system:set trashbin_retention_obligation --type=string --value="60,365"
```

## Office 启动

启用 Collabora 后，Nextcloud 先完成自身初始化，Office 连接再等待 Collabora 就绪并自动激活。首次部署、重建和临时目录切换可能需要等待，后台最多重试900秒；超时在容器日志明确报告。Nextcloud 的健康状态通过后，仍需等待 Office 激活完成才能编辑。不会关闭证书验证或放宽 WOPI 访问范围。

## 身份、用户与 Group

LDAPS provisioning 管理用户和 Group；OIDC 是默认登录协议，SAML 仍受支持。两条链路通过 `anasIdentityAnchor` 关联既有 LDAP 账号。Samba `Admins` 动态映射 Nextcloud 管理员权限。普通目录密码修改通过受限 password bind 服务账号回写，而不是数据库管理员账号。

固定版本 `user_oidc 8.11.0` 声明 RP-Initiated Logout 与 session-required back-channel endpoint，并按 `sid` 撤销匹配的 Nextcloud 会话；只有 Provider 的管理员删 session/账号停用真实 E2E 产生通知时，才把对应 Provider 标为后台双向登出。`user_saml 8.2.0` 声明 HTTP-Redirect SLS：Authentik/LLNG 仅按浏览器 SLO 验收，Casdoor 不发布 SLO 时只执行本地登出。协议或域名切换会清掉相反协议和旧 endpoint。

r11 使用原样官方 OIDC 插件，启动时检查应用完整性；不再修改插件控制器。模块仍处于
`developing`，其他 Provider、客户端体验与完整镜像生命周期仍需验收。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`；同步 groups |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

Nextcloud 的账号密码策略不另建一套规则：界面预检的最小长度来自 `samba_dc.user_min_pass_length`，复杂度、历史、最短/最长有效期和锁定策略由 Samba AD 最终执行。Nextcloud 自带的常见密码、HIBP 和字符类别账号校验会关闭，避免拒绝 Samba 本可接受的密码。共享链接密码使用独立的 Nextcloud 策略，不随目录账号策略变化。

### 目录属性变更说明

**匹配键与内部 UID**：`anasIdentityAnchor`。LDAP 的 `ldapExpertUUIDUserAttr`、
`ldapExpertUUIDGroupAttr` 和 `ldapExpertUsernameAttr` 均取 anchor 属性，OIDC 使用
`--unique-uid=0 --mapping-uid=sub`，Provider 必须使 `sub` 等于同一 anchor。
LDAP 搜索属性和登录过滤器同时允许 anchor 匹配，确保新用户首次登录能够导入。
Casdoor 与 LLNG r12 的 anchor `sub` 组合均已实机验证；其他 Provider 的历史验收不覆盖本次配置。

此配置面向无需迁移的全新部署；既有 LDAP 映射不会因修改属性配置而重写 UID。
登录名仍使用 `sAMAccountName`，显示名称仍使用 `displayName`；内部 UID、WebDAV/API 路径和
内部数据目录会包含 anchor。SAML 仍按显式 anchor 属性匹配 LDAP 账号，应用会话回归待完成。

| 目录侧变更 | Nextcloud 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | OIDC 按 `sub` 直接匹配 anchor UID；改名保持内部 UID 和文件归属 | Casdoor + Nextcloud 真实登录与文件：`已验证`，入口 `server-casdoor-nextcloud-identity-e2e.py`；其他 Provider 的 r11 回归待运行 |
| `mail` 改变 | 下次同步或登录时从 `ldapEmailAttribute` 刷新。它不参与账号绑定；Nextcloud 的邮箱没有全局唯一约束，不会因此建号失败 | `推断` |
| `displayName` 与其他 profile 属性 | 从 `ldapUserDisplayName` 刷新，时机是 LDAP 同步与登录；不是实时 | Casdoor + 官方 8.11.0：HTML 与 OCS 显示姓名而非 anchor UID，`已验证`，入口同上；刷新时机：`推断` |
| 直接或递归组成员变更 | `ldapNestedGroups=1`，授权在 LDAP 同步与登录时生效；`Admins` 经 `ldap:promote-group` 动态映射为应用管理员。订阅目录事件后可缩短到事件传播时间，但不是实时 | 组映射与管理员映射：`已验证`，入口同上；收敛时刻：`推断` |
| 账号停用 | 登录被拒：`NEXTCLOUD_USER_LOGIN_FILTER` 含 `(!(userAccountControl:...=2))`。但**用户过滤器不含该条件**，账号在 Nextcloud 里继续存在；**已有 session、App 密码与 WebDAV/CalDAV 客户端凭据不经过登录过滤器，是否随之失效未经复核** | 登录过滤器含停用条件：`已验证`（Hook 渲染，见技术文档）；App 密码与已有 session 的结果：`推断` |
| 账号删除 | 用户掉出用户过滤器，Nextcloud 按 `user_ldap` 的删除检测把它标为已删除并保留映射记录；**文件不会自动转交**，必须由管理员显式转移 | `推断` |
| 标识符回收再分配 | 新人复用用户名时 anchor 不同，内部 UID 也不同，按设计不会继承原账号文件；原样官方插件的用户名复用用例仍待实机验收 | `推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 停用目录账号后，必须在 Nextcloud 执行 `occ user:disable <uid>`，并在"设置 → 安全"里**删除该
   用户的全部 App 密码与设备 token**；只靠目录停用不足以断开已配对的桌面/移动客户端；
2. 删除目录账号前，先用 `occ files:transfer-ownership <uid> <接手人>` 转移文件，再删除账号；
3. OIDC 会话由 Provider 的 back-channel 通知终止；没有通知路径或使用设备凭据时，组撤权要立即生效，还要结束该用户的 session（删除其 App 密码与
   session token）；
4. **目录侧流程约束**：`sAMAccountName` 不得回收再分配。anchor 能保证"改名还是同一个人"，但
   保证不了"回收的用户名不会撞上冻结在旧值的 `uid`"。

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

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `nextcloud.db_name` | string | — | `nextcloud` | `static` | `NEXTCLOUD_DB_NAME` | 否 | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 应用数据库名 |
| `nextcloud.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `NEXTCLOUD_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `nextcloud.domain_prefix` | string | — | `nc` | `static` | `NEXTCLOUD_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `nextcloud.files_trash_delete` | bool | — | `false` | `static` | `NEXTCLOUD_FILES_TRASH_DELETE` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许手动永久删除和清空回收站，对应官方 files.trash.delete |
| `nextcloud.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NEXTCLOUD_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |
| `nextcloud.language` | string | — | — | `inherited` | `NEXTCLOUD_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 界面回退语言 |
| `nextcloud.locale` | string | — | — | `inherited` | `NEXTCLOUD_LOCALE` | 否 | 是 | 否 | 是 | `reconcile` | 区域格式回退值 |
| `nextcloud.log_level` | string | — | `2` | `static` | `NEXTCLOUD_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `nextcloud.memories_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_MEMORIES_ENABLED` | 否 | 否 | 否 | 是 | `reconcile` | 是否启用 Memories |
| `nextcloud.memory_limit` | string | — | `1G` | `static` | `NEXTCLOUD_MEMORY_LIMIT` | 否 | 否 | 否 | 是 | `container_recreate` | 内存限制 |
| `nextcloud.phone_region` | string | — | `CN` | `static` | `NEXTCLOUD_PHONE_REGION` | 否 | 否 | 否 | 是 | `container_recreate` | 默认电话区域 |
| `nextcloud.rm_skeleton_files` | bool | — | `false` | `static` | `NEXTCLOUD_RM_SKELETON_FILES` | 否 | 否 | 否 | 是 | `container_recreate` | 是否删除默认骨架文件 |
| `nextcloud.talk_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_TALK_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Talk |
| `nextcloud.trashbin_retention_obligation` | string | — | `60,365` | `static` | `NEXTCLOUD_TRASHBIN_RETENTION_OBLIGATION` | 否 | 否 | 否 | 是 | `container_recreate` | 自动清理至少保留 60 天、365 天到期；`disabled` 关闭自动清理 |
| `nextcloud.upload_max_size` | string | — | `16G` | `static` | `NEXTCLOUD_UPLOAD_MAX_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | 上传大小上限 |

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

`TURN_SECRET` 通过 `credentials.consumes` 显式绑定到 `eturnal.secret`。Runner 只有在 Eturnal 的
credential ready barrier 验证成功后才启动 Nextcloud；本 Module 不拥有或自行轮换该值。

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

- Module version / 版本：`34.0.2-r11`（reviewed 2026-08-21）
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
