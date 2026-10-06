# LemonLDAP::NG 技术实现

本文面向 Module 维护者，记录 `llng` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2.23.2-r12` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | 提供 Capability | `oidc, saml` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_llng` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.2-r12` | `traefik, db` | 2 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-llng-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用测试入口 |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Manager 域名前缀 |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 测试入口域名前缀 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

Samba AD 是用户和 Group 来源。Portal 使用目录认证，IAM 向 Consumer 发布 OIDC/SAML 端点和 Group 属性。`Admins` 可进入 Manager。
OIDC issuer 保留固定 `2.23.2` discovery 返回的尾斜杠，Consumer 必须按该精确值校验；discovery URL 仍为 Portal 根路径下的 `/.well-known/openid-configuration`。

### 应用会话登出

固定 `2.23.2` 中，通用 OIDC 登出方法在 `render_env` 映射为 `oidcRPMetaDataOptionsLogoutUrl`、`LogoutType=back` 和 `LogoutSessionRequired=1`；`PostLogoutRedirectUris` 与免确认设置只服务 RP 发起登出，不能替代 back-channel。SAML 从 SP metadata 读取 SLS并保持 `SignSLOMessage=1`；Redirect/POST 都只保证浏览器 SLO。启动配置脚本先删除整个 `oidcRPMetaDataOptions`/`oidcRPMetaDataExportedVars` 与 SAML SP registry，再从当前 Client 契约重建，确保旧 endpoint 和相反协议配置不残留。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 配置缓存刷新

启动入口在恢复 Nginx runtime 后写入仅监听 `127.0.0.1:8089` 的 `/reload` 虚拟主机，
转交现有 FastCGI server 的 `LLTYPE=reload`；不发布端口，也不添加外部路由。
`lmConf.json` 和每次启动的持久配置重建都将 reload URL 指向该地址。最终 CLI cache update
与 HTTP reload 成功后才写 `/run/llng-configured`，避免旧 RP 配置继续服务而健康检查提前通过。
这一入口采用[上游配置刷新方式](https://www.lemonldap-ng.org/documentation/latest/configlocation.html)，
用 loopback 监听限制访问，而不是开放 Manager API。

### 目录属性变更的实现侧

LLNG 直接认证 Samba AD，不保存用户副本。`sessions`、`psessions`、`samlsessions`、
`oidcsessions`、`cassessions` 的主键是 session ID；`_whatToTrace` 等字段只有查询索引。
`lmConf.json` 的 `whatToTrace=_whatToTrace` 在 AD 认证下等于小写登录名，继续用于日志和
Manager 查询。目录属性由 `ldapExportedVars` 在新认证时读取，已有会话可能保留旧属性。

r12 的 `llng-config.sh` 在重建 RP 时明确设置
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor`。固定 `2.23.2-1` 的
`Lib/OpenIDConnect.pm::getUserIDForRP` 只在配置项为空时回退 `whatToTrace`；指定了 anchor 后
直接读取该会话变量。`Issuer/OpenIDConnect.pm` 的授权码签发、`_generateIDToken`、注销及
`Lib/OpenIDConnect.pm::buildUserInfoResponse` 都调用它；在线 refresh session 另存
`_oidc_logout_sub`，供其会话终止时发送同一主体的通知。Refresh token / access token 可为
不透明的会话 ID，不能因为不是 JWT 就要求它们包含 `sub`。

每个 RP 的 Rule 都包含 `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""`，
再与原应用组规则取交集；缺 anchor 的 SSO 会话不能取得授权码。`ldapExportedVars` 始终加载
anchor，不依赖 Consumer 是否显式请求同名 claim。通用属性契约仍用于人类可读的用户名、
姓名、邮箱与数组类型的组。Netbird 的 `sub` URL 投影在 `hook/iam.go` 的 calculate/render_env
边界被拒绝；Nextcloud 的 anchor UID 遵守已确认的 `DIRKEY-R-010` 例外。

验收入口为 `test-env/scripts/server-llng-nextcloud-identity-e2e.py`，使用测试主机已有
`cryptography` 验证 RS256 签名，不引入产品运行时依赖。它验证真实授权码、UserInfo、refresh、
签名 Logout Token，以及 Nextcloud 原 Cookie、LDAP 映射、改名文件归属和标签回收隔离。
协议夹具专用 RP 的 refresh 开关不代表所有生产 RP 都启用了 refresh。

SAML NameID 默认格式映射尚未切成 anchor，真实 SAML 应用验收列为待实现，不作为本轮主要协议。
本模块仍无目录事件 watcher；目录停用/撤组不自动删除已建立会话，需要 Manager 删除与 Consumer
撤权。不能把普通 Portal 登出记成目录事件实时撤权。

## 管理面与 Secret 生命周期

当前没有独立的本地 `break_glass`。Manager 与 Portal 共用目录认证；IAM 或目录故障时需要从主机侧修复，不能依赖不存在的本地密码。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Portal Documentation 菜单

`lmConf.json` 为两个 Documentation 应用写入
`inGroup("{{SAMBA_DC_ADMIN_GROUP_NAME}}")`，覆盖全新安装；`llng-config.sh` 在每次启动时
对当前持久配置再次写入同一规则，覆盖升级前遗留的 `display=on`。两个子应用都不可见时，
普通用户不会看到空的 Documentation 分类，管理员仍可使用本地文档和官方站点入口。

### Secret 边界

- `LLNG_OIDC_SERVICE_KEY_ID`
- `LLNG_SERVICE_PRIVATE_KEY`
- `LLNG_SERVICE_PUBLIC_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

### 签名密钥泄露与轮换

OIDC/SAML 签名私钥一旦进入终端输出、日志、测试报告、任务记录或其他非 Secret 边界，即使
没有确认外传，也按可能泄露处理。诊断命令必须按精确键白名单读取配置，禁止递归输出整份
LLNG 配置；报告只能记录 key ID、指纹和公钥，不得记录私钥正文。

当前不自动轮换运行中的 LLNG 密钥。轮换必须作为单独获批的运维事务：先盘点 RP/SP 是
动态读取 JWKS/metadata 还是固定证书，备份 workspace Secret 与 LLNG 配置，生成新密钥并
发布公钥，刷新固定信任方，验证 OIDC/SAML 登录和登出，再在旧 token/assertion 的有效期
及回滚窗口结束后撤销旧密钥。不支持双密钥过渡的部署必须安排维护窗口。发生疑似泄露但
尚未获批轮换时，应记录事件、限制任务/日志访问并保留轮换待办，不能静默覆盖 Secret。

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

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `APPS_LIST*`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`iam_test.go`](../hook/iam_test.go)
- [`lmConf.json`](../llng/root/root/lmConf.json)
- [`llng-config.sh`](../llng/root/root/llng-config.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

容器启动时在 LLNG Nginx 中启用 `real_ip_header X-Forwarded-For` 和递归解析，只信任 `TRAEFIK_HOSTNAME` 以及 Traefik 已校验的显式上游代理 IP/CIDR。这样登录历史、会话审计和基于 IP 的规则得到最左侧未受信任客户端地址，而不是 Docker bridge 地址；非法代理值会导致启动失败。

## 当前限制

不要配置已删除的 `LLNG_PASSWORD`；它不会创建上游管理员。
