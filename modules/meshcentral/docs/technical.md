# MeshCentral 技术实现

本文面向 Module 维护者，记录 `meshcentral` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `1.2.4-r7` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_meshcentral` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-meshcentral:1.2.4-r7` | `db, traefik` | 5 |
<!-- generated:compose-topology:end -->

### OIDC 启动就绪

Entrypoint 在生成运行配置后、启动 MeshCentral 前请求
`MESHCENTRAL_OIDC_DISCOVERY_URL`。只有 metadata 返回 200、issuer 与绑定一致，并包含
authorization、token 和 JWKS 端点时才继续启动。默认最多等待 300 次、每次间隔 2 秒；
超时后进程失败，由 Compose restart policy 重试。这避免 IAM Provider 尚未就绪时触发
MeshCentral 自身的三次 discovery 失败保护，并在该进程生命周期内永久禁用 OIDC。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `meshcentral.db_name` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DB_NAME` | 否 | 否 | 否 | 否：`migrate-meshcentral-database` | `data_migrate` | 应用数据库名 |
| `meshcentral.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `MESHCENTRAL_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-meshcentral-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `meshcentral.domain_prefix` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `meshcentral.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `MESHCENTRAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |
| `meshcentral.mps_port` | int | `1..65535` | `4433` | `static` | `MESHCENTRAL_MPS_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | MPS 端口 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

OIDC 是日常登录链路；LDAPS 单独负责 users/groups provisioning。启用应用过滤时，`APP_meshcentral`、`APP_all` 或管理员组可访问，管理员组同时映射 site-admin。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc |
| Group | `APP_meshcentral` / `APP_all`；同步 groups |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

没有单独的原生恢复管理员，也没有 `management.local_accounts`。IAM 故障时需要恢复 IAM/目录链路。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `MESHCENTRAL_DOMAIN_FULL` | `iam` |

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `MESHCENTRAL_OIDC_CLIENT_SECRET`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

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

- `ANAS_IAM_CLIENT__MESHCENTRAL__*`
- `APPS_LIST*`

### 显式消费

- `ANAS_IAM_BINDING__MESHCENTRAL__*`
- `ANAS_IAM_PORTAL_URL`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_APP_ALL_DN`
- `SAMBA_DC_APP_FILTER`
- `SAMBA_DC_BASE_APP_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `SAMBA_DC_LDAPS_SERVER_URL_PORT`
- `SAMBA_DC_LDAP_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_DISPLAY_NAME`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_LOGIN_ATTRS`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_DOMAIN`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

启动脚本解析 Traefik 当前地址并写入 MeshCentral `settings.tlsOffload`。MeshCentral 只接受来自该精确代理邻居的卸载与转发信息，因此 Web 审计使用真实访问地址；MPS 是独立直连 TCP 入口，不经过 HTTP 转发 Header。

## 当前限制

不要把 LDAPS provisioning 误写成浏览器 LDAPS 登录。
