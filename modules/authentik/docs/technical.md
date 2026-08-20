# authentik 技术实现

本文面向 Module 维护者，记录 `authentik` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2026.5.6-r9` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | 提供 Capability | `oidc, saml` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_authentik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `traefik, authentik, db` | 3 |
| `anas_authentik_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `authentik, db` | 2 |
| `anas_authentik_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `` | 1 |
| `anas_authentik_worker` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r9` | `authentik, db` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | — | `authentik` | `static` | `AUTHENTIK_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `authentik.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AUTHENTIK_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-authentik-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `authentik.domain_prefix` | string | — | `auth` | `static` | `AUTHENTIK_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `authentik.ldap_enabled` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 LDAP Source |
| `authentik.ldap_password_writeback` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许目录密码回写 |
| `authentik.log_level` | string | — | `warn` | `static` | `AUTHENTIK_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

Samba AD 是人员与组的事实来源。LDAP Source 通过 LDAPS 同步用户和组；`ldap_password_writeback` 控制是否允许 Authentik 使用受限服务账号回写普通用户密码。应用登录使用按 Consumer 生成的 OIDC 或 SAML 端点。`Admins` 映射为 Authentik superuser，`APP_all`/`APP_authentik` 只授予访问权。

### 应用会话登出

OIDC blueprint 把授权回调和登出后回调分别标为 `authorization`/`logout`，并从通用契约选择 `backchannel` 优先的 `logout_uri/logout_method`。因此 Authentik 的浏览器登出、管理员删 session 和账号停用都会向支持的 RP 发送签名 logout token。SAML 将 Redirect SLS 映射为 `frontchannel_native` 并签名 LogoutRequest/LogoutResponse；POST SLS 才可选择无浏览器的 `backchannel`。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| 目录密码回写 | `ldap_password_writeback` / restricted bind |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

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

### Secret 边界

- `ANAS_LOCAL_ADMIN__AUTHENTIK__BREAK_GLASS__PASSWORD`
- `AUTHENTIK_SECRET_KEY`
- `AUTHENTIK_SIGNING_CERT`
- `AUTHENTIK_SIGNING_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

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

## 环境变量所有权

### 导出

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_DN_PREFIX`
- `SAMBA_DC_BASE_USERS_DN_PREFIX`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `TRAEFIK_BASE_PORT`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `APPS_LIST*`
- `SAMBA_DC_LDAPS_SERVER_URL_PORT`
- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`directory_test.go`](../hook/directory_test.go)
- [`iam_test.go`](../hook/iam_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

Server entrypoint 解析 Traefik 的当前 IPv4 地址，并覆盖 Authentik 默认的宽泛私网代理范围，只保留 loopback、精确的 Traefik `/32` 和显式配置的上游代理。Traefik 无法解析时容器拒绝启动，防止事件与登录审计静默退化为 Docker 地址或接受伪造 Header。

## 当前限制

状态为 `developing`；正式支持前仍需以真实容器验证目录同步、组撤权、密码回写和恢复登录。
