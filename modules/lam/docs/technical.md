# LDAP Account Manager 技术实现

本文面向 Module 维护者，记录 `lam` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `9.6.0-r8` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_lam` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-lam:9.6.0-r8` | `traefik` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lam.admin_password` | string | — | — | `generated` | `LAM_ADMIN_PASSWORD` | 否 | 是 | 是 | 是 | `container_recreate` | 管理界面或服务管理员密码 |
| `lam.domain_prefix` | string | — | `lam` | `static` | `LAM_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `lam.language` | string | — | — | `inherited` | `LAM_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 界面回退语言 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

LAM 直接通过 LDAPS 工作。主登录页使用操作者自己的目录用户名和密码；`Admins` 允许进入完整管理界面，但实际目录写权限仍由 AD ACL 决定。LAM 可用于创建、禁用和管理用户及 Group，也可在操作者 ACL 允许时重置普通用户的目录密码。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps |
| IAM | 不支持/不适用 |
| Group | `APP_lam` / `APP_all` |
| 目录密码回写 | 使用登录操作者的 LDAPS 身份，受 AD ACL 限制 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。

- **身份存在哪张表/哪个字段**：不存在。LAM 没有数据库，也不写用户记录；登录态只在 PHP 会话
  存储里保存已 bind 的 DN，容器重启即全部失效。
- **匹配键**：无持久匹配键。每次登录由 `lam/configure.php` 写出的 `loginMethod = 'search'` 驱动：
  用 `loginSearchDN`/`loginSearchPassword`（受限服务账号）按 `loginSearchFilter` 检索，
  过滤器是
  `(&(objectCategory=person)(objectClass=user)(!(userAccountControl:1.2.840.113556.1.4.803:=2))(sAMAccountName=%USER%)(memberOf:1.2.840.113556.1.4.1941:=<SAMBA_DC_ADMIN_GROUP_DN>))`，
  命中后以该 DN 和操作者自己的密码重新 bind。
- **每次登录刷新什么**：全部——没有缓存可刷新。被管理对象的属性在每次页面请求时从 LDAPS 读取。
- **撤权经哪个接口**：登录过滤器本身，三条件缺一不可（账号启用、`sAMAccountName` 匹配、递归
  `Admins` 成员）。目录写权限另由操作者自己的 AD ACL 在服务端裁决，LAM 不代为判权。
- **对账或事件订阅路径**：不需要。LAM 不保有目录副本，因此不落入
  [目录事件订阅要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
  的适用范围。
- **技术阻碍**：无。本 Module 不需要不可变身份键，因为它没有要绑定的持久状态。

**`DIRKEY-R-002` 符合性**：符合（以"无持久身份键"的方式）。判据"目录里改个名，这个键变不变"
在这里退化为"没有这个键"，因此不存在把标签当身份的风险。`sAMAccountName` 只出现在登录检索里，
属于 `DIRKEY-R-001` 明确允许的"登录、搜索用途"。

**`DIRKEY-R-013` 投影结论：不适用。** 本 Module 不是 OIDC/SAML Consumer（`module.yml` 不声明
`iam`），不消费任何主体标识符，M2 的切换对它没有任何影响。

## 管理面与 Secret 生命周期

`admin_password` 保护 LAM configuration/profile 编辑面，并不是普通目录管理员密码。该凭据尚未建模为 `management.local_accounts`。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `LAM_ADMIN_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

—

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_DN`
- `SAMBA_DC_BASE_COMPUTERS_DN`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_DOMAIN`
- `SAMBA_DC_LDAP_BIND_DN`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_LDAPS_SERVER_URL`

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

自定义镜像启用 Apache `mod_remoteip`。启动配置把 `X-Forwarded-For` 映射为 Apache 客户端地址，但只信任 Traefik 主机和显式上游代理，因此 LAM 的 Web 日志和依赖 `REMOTE_ADDR` 的行为不会记录 Docker bridge 地址。

## 当前限制

目录不可用时主登录不可用；当前没有由 `anas admin local` 管理的恢复入口。
