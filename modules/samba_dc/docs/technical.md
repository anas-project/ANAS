# Samba domain controller and DNS 技术实现

本文面向 Module 维护者，记录 `samba_dc` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `4.23.6-r7` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r7` | `` | 3 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r7` | `` | 3 |
| `anas_samba_dc_events_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ubuntu:resolute-678c6550cc43` | `` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | bool | — | `true` | `static` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码复杂度策略 |
| `samba_dc.admin_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员锁定时长 |
| `samba_dc.admin_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员失败计数重置时长 |
| `samba_dc.admin_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员锁定阈值 |
| `samba_dc.admin_max_pass_age` | int | — | `0` | `static` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最长有效期，0 表示永不过期 |
| `samba_dc.admin_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最短使用期 |
| `samba_dc.admin_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最短长度 |
| `samba_dc.admin_name` | string | — | `admin` | `static` | `SAMBA_DC_ADMIN_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 日常目录管理员用户名 |
| `samba_dc.admin_password` | string | — | — | `generated` | `SAMBA_DC_ADMIN_PASSWORD` | 否 | 是 | 是 | 否：`rotate-samba-admin-password` | `credential_rotate` | 管理界面或服务管理员密码 |
| `samba_dc.admin_password_history` | int | — | `2` | `static` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码历史数量 |
| `samba_dc.administrator_password` | string | — | — | `generated` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | 否 | 是 | 是 | 否：`rotate-samba-administrator-password` | `credential_rotate` | 内置 Administrator 密码 |
| `samba_dc.anchor_bind_name` | string | — | `svc_anchor` | `static` | `SAMBA_DC_ANCHOR_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 身份锚点服务账号名 |
| `samba_dc.anchor_bind_password` | string | — | — | `generated` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-anchor-bind-password` | `credential_rotate` | 身份锚点服务账号密码 |
| `samba_dc.anchor_scan_interval` | int | — | `300` | `static` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | 否 | 否 | 否 | 是 | `container_recreate` | 身份锚点补漏扫描间隔 |
| `samba_dc.app_filter` | bool | — | `true` | `static` | `SAMBA_DC_APP_FILTER` | 否 | 否 | 否 | 是 | `container_recreate` | 是否按 APP_* Group 过滤应用用户 |
| `samba_dc.create_structure` | bool | — | `true` | `static` | `SAMBA_DC_CREATE_STRUCTURE` | 否 | 否 | 否 | 是 | `container_recreate` | 是否创建目录 OU/Group 结构 |
| `samba_dc.dns_allowed_networks` | string | — | `""` | `static` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | 否 | 否 | 否 | 是 | `container_recreate` | 允许递归查询的网络 |
| `samba_dc.dns_cache_size` | string | — | `128M` | `static` | `SAMBA_DC_DNS_CACHE_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 缓存上限 |
| `samba_dc.dns_debug` | bool | — | `false` | `static` | `SAMBA_DC_DNS_DEBUG` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 调试开关 |
| `samba_dc.dns_forwarders` | string | — | `""` | `static` | `SAMBA_DC_DNS_FORWARDERS` | 否 | 否 | 否 | 是 | `container_recreate` | 上游 DNS 转发器 |
| `samba_dc.ldap_bind_name` | string | — | `svc_ldap` | `static` | `SAMBA_DC_LDAP_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 只读 LDAP 服务账号名 |
| `samba_dc.ldap_bind_password` | string | — | — | `generated` | `SAMBA_DC_LDAP_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-ldap-bind-password` | `credential_rotate` | 只读 LDAP 服务账号密码 |
| `samba_dc.log_level` | string | — | `1` | `static` | `SAMBA_DC_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_dc.max_log_size` | int | `>= 1` | `2048` | `static` | `SAMBA_DC_MAX_LOG_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | 单个日志文件上限 |
| `samba_dc.netbios_name` | string | — | — | `runtime` | `SAMBA_DC_NETBIOS_NAME` | 否 | 是 | 否 | 否：`replace-domain-controller` | `immutable` | 域控制器 NetBIOS 名称 |
| `samba_dc.password_bind_name` | string | — | `svc_password` | `static` | `SAMBA_DC_PASSWORD_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 密码回写服务账号名 |
| `samba_dc.password_bind_password` | string | — | — | `generated` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-password-bind-password` | `credential_rotate` | 密码回写服务账号密码 |
| `samba_dc.realm` | string | — | — | `inherited` | `SAMBA_DC_REALM` | 否 | 是 | 否 | 否：`migrate-domain` | `immutable` | AD Realm |
| `samba_dc.template_homedir` | string | — | `/home/%D/%U` | `static` | `SAMBA_DC_TEMPLATE_HOMEDIR` | 否 | 否 | 否 | 是 | `container_recreate` | 目录用户 home 模板 |
| `samba_dc.template_shell` | string | — | `/bin/false` | `static` | `SAMBA_DC_TEMPLATE_SHELL` | 否 | 否 | 否 | 是 | `container_recreate` | 目录用户 shell 模板 |
| `samba_dc.user_complex_pass` | bool | — | `false` | `static` | `SAMBA_DC_USER_COMPLEX_PASS` | 否 | 否 | 否 | 是 | `hot_reload` | 密码复杂度策略 |
| `samba_dc.user_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_DURATION` | 否 | 否 | 否 | 是 | `hot_reload` | 锁定时长 |
| `samba_dc.user_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | 否 | 否 | 否 | 是 | `hot_reload` | 失败计数重置时长 |
| `samba_dc.user_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | 否 | 否 | 否 | 是 | `hot_reload` | 锁定阈值 |
| `samba_dc.user_max_pass_age` | int | — | `90` | `static` | `SAMBA_DC_USER_MAX_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最长有效期 |
| `samba_dc.user_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_USER_MIN_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最短使用期 |
| `samba_dc.user_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最短长度 |
| `samba_dc.user_password_history` | int | — | `2` | `static` | `SAMBA_DC_USER_PASSWORD_HISTORY` | 否 | 否 | 否 | 是 | `hot_reload` | 密码历史数量 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 密码策略实现

`structure.sh` 使用 `SAMBA_DC_USER_*` 调用 `samba-tool domain passwordsettings set`，生成普通用户的域策略。管理员策略由 `SAMBA_DC_ADMIN_*` 生成或更新 `pso_privileged`，并应用于内置 `Administrator` 和 `Admins` 组。管理员 PSO 的复杂度、长度、历史、最短/最长有效期以及三项锁定规则均来自 Module 参数，脚本中不保留策略常量。

所有 16 个策略参数声明为 `hot_reload`/`samba-password-policy`。当前执行器仍使用 deployment apply fallback；重新运行容器初始化时会幂等更新域策略与现有 PSO。

## 身份与授权数据流

它是人员用户、Group、服务账号和身份锚点的事实来源。使用 LAM 或 Samba 管理工具创建、禁用用户及管理 Group。`svc_ldap` 只读，`svc_password` 仅能在限定范围修改普通用户密码，`svc_anchor` 管理身份锚点。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | 不支持/不适用 |
| Group | Group 事实来源 |
| 目录密码回写 | 目录事实来源；由 ACL 与密码策略控制 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

日常目录管理员由 `admin_name` 定义；内置 RID 500 `Administrator` 只用于初始化和底层恢复。两者密码独立，不属于 `anas admin local` 管理面。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `SAMBA_DC_ADMIN_PASSWORD`
- `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`

### 显式消费

- `DOMAINS`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `LEGO_CA_CERT_NAME`
- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_KEY_NAME`
- `ANAS_IDENTITY_APP_CLIENTS`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

`realm` 和 `netbios_name` 是不可变身份；密码参数需要专用凭据轮换，不能靠重建容器修改。
