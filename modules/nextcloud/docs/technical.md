# Nextcloud 技术实现

本文面向 Module 维护者，记录 `nextcloud` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `34.0.2-r9` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc, saml` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_imaginary` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-imaginary:2026.07.30-d5e7ffac6e1a` | `nextcloud` | 0 |
| `anas_nextcloud` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-nextcloud:34.0.2-r9` | `nextcloud, db, traefik` | 3 |
| `anas_nextcloud-cron` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-nextcloud:34.0.2-r9` | `nextcloud, db` | 2 |
| `anas_nextcloud-push` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-notify-push:2026.07.30-7c156254927e` | `nextcloud, db, traefik` | 1 |
| `anas_nextcloud-redis` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-redis:8.10.0-alpine` | `nextcloud` | 1 |
| `anas_talk` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-nextcloud-talk:2026.07.30-2b9a7d12d3e6` | `nextcloud, traefik` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `nextcloud.db_name` | string | — | `nextcloud` | `static` | `NEXTCLOUD_DB_NAME` | 否 | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 应用数据库名 |
| `nextcloud.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `NEXTCLOUD_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-nextcloud-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `nextcloud.domain_prefix` | string | — | `nc` | `static` | `NEXTCLOUD_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `nextcloud.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NEXTCLOUD_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |
| `nextcloud.language` | string | — | — | `inherited` | `NEXTCLOUD_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 界面回退语言 |
| `nextcloud.locale` | string | — | — | `inherited` | `NEXTCLOUD_LOCALE` | 否 | 是 | 否 | 是 | `reconcile` | 区域格式回退值 |
| `nextcloud.log_level` | string | — | `2` | `static` | `NEXTCLOUD_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `nextcloud.memories_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_MEMORIES_ENABLED` | 否 | 否 | 否 | 是 | `reconcile` | 是否启用 Memories |
| `nextcloud.memory_limit` | string | — | `1G` | `static` | `NEXTCLOUD_MEMORY_LIMIT` | 否 | 否 | 否 | 是 | `container_recreate` | 内存限制 |
| `nextcloud.phone_region` | string | — | `CN` | `static` | `NEXTCLOUD_PHONE_REGION` | 否 | 否 | 否 | 是 | `container_recreate` | 默认电话区域 |
| `nextcloud.rm_skeleton_files` | bool | — | `false` | `static` | `NEXTCLOUD_RM_SKELETON_FILES` | 否 | 否 | 否 | 是 | `container_recreate` | 是否删除默认骨架文件 |
| `nextcloud.talk_enabled` | bool | — | `true` | `static` | `NEXTCLOUD_TALK_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Talk |
| `nextcloud.upload_max_size` | string | — | `16G` | `static` | `NEXTCLOUD_UPLOAD_MAX_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | 上传大小上限 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

LDAPS provisioning 管理用户和 Group；OIDC 是默认登录协议，SAML 仍受支持。两条链路通过一致的目录用户名和 `anasIdentityAnchor` 关联。Samba `Admins` 动态映射 Nextcloud 管理员权限。普通目录密码修改通过受限 password bind 服务账号回写，而不是数据库管理员账号。

Web 与 cron 容器都会在 ANAS 内部 CA 存在时安装它。`user_ldap` 会在 cron 后台任务中周期更新目录属性，因此 cron 不仅共享 Nextcloud 数据，也必须共享 `/certs` 信任材料；CA 安装或 trust store 更新失败会阻断 cron 启动，公有 CA 则直接使用系统 trust store。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc, saml |
| Group | `APP_nextcloud` / `APP_all`；同步 groups |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

`task.sh` 会把 Nextcloud 账号上下文的 `minLength` 同步为 `SAMBA_DC_USER_MIN_PASS_LENGTH`，并将 Nextcloud 独有的常见密码、HIBP、字符类别、历史、过期和登录失败锁定校验关闭。AD 的复杂度是“字符类别满足其一组组合”的目录规则，不能由 Nextcloud 的“逐项强制”开关等价表达，因此复杂度、历史、有效期和锁定始终只由 Samba 执行。首次迁移前会把原账号策略复制到 Nextcloud 34 的 `sharing` 上下文，使共享链接密码策略与目录账号策略解耦。

## 管理面与 Secret 生命周期

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

### Secret 边界

- `ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD`
- `NEXTCLOUD_IMAGINARY_SECRET`
- `NEXTCLOUD_OIDC_CLIENT_SECRET`
- `NEXTCLOUD_SAML_SP_CERT`
- `NEXTCLOUD_SAML_SP_PRIVATE_KEY`
- `NEXTCLOUD_TALK_INTERNAL_SECRET`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`
- `TALK_SIGNALING_SECRET`
- `TURN_SECRET`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

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

## 环境变量所有权

### 导出

- `ANAS_IAM_CLIENT__NEXTCLOUD__*`
- `APPS_LIST*`
- `TALK_*`

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_APP_ALL_DN`
- `SAMBA_DC_APP_FILTER`
- `SAMBA_DC_BASE_APP_DN`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_GROUP_CLASS_NAME`
- `SAMBA_DC_GROUP_DISPLAY_NAME`
- `SAMBA_DC_GROUP_MEMBER_ATTR`
- `SAMBA_DC_HOST`
- `SAMBA_DC_HOST_IP`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_CLASS_NAME`
- `SAMBA_DC_USER_COMPLEX_PASS`
- `SAMBA_DC_USER_DISPLAY_NAME`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_LOGIN_ATTRS`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_HOSTNAME`
- `TURN_DOMAIN`
- `TURN_DOMAIN_PORT`
- `TURN_PORT`
- `ANAS_IAM_BINDING__NEXTCLOUD__*`
- `ANAS_IAM_PORTAL_URL`
- `COLLABORA_DOMAIN_FULL`
- `COLLABORA_HOSTNAME`
- `TURN_SECRET`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`iam_test.go`](../hook/iam_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

切换 OIDC/SAML 需要重建并协调 IAM 注册；切换数据库不会迁移现有数据。
