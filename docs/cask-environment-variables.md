# Cask 环境变量契约

本文记录当前各 Cask Hook 产生的环境变量，以及跨 Cask 共享变量的所有权。变量值最终写入各 Cask 自己的 `.env`；敏感值只有在 `cask.yml` 的 `config.consumes` 明确声明后才允许跨边界。

## Runner 产生的身份变量

这些变量不属于某个应用实现，由 Runner 根据启用的 Cask、`identity.interfaces` 和最终 IAM 绑定统一产生。它们采用显式消费作用域：只有在 `config.consumes` 声明相应变量的 Cask 才会在自己的 `.env` 中收到，其他 Cask 不会获得整套身份拓扑。

| 变量 | 含义 |
| --- | --- |
| `ANAS_IDENTITY_CLIENTS` | 使用任一身份协议的 Cask 并集 |
| `ANAS_IDENTITY_APP_CLIENTS` | 声明 `identity.application_group: true`、需要 `APP_<cask>` 组的应用 |
| `ANAS_IDENTITY_LDAPS_CLIENTS` | 直接使用 LDAPS 的 Cask |
| `ANAS_IDENTITY_OIDC_CLIENTS` | 最终绑定 OIDC 的 IAM 消费方 |
| `ANAS_IDENTITY_SAML_CLIENTS` | 最终绑定 SAML 的 IAM 消费方 |
| `ANAS_IDENTITY_CLIENT__<CASK>__INTERFACES` | 单个 Cask 使用的协议列表，例如 `ldaps,saml` |
| `ANAS_IAM_PROVIDER` | 当前部署选择的 IAM Provider |
| `ANAS_IAM_INTERFACES` | Provider 提供的 IAM 协议 |
| `ANAS_IAM_BINDING__<CASK>__INTERFACE` | 某消费方最终选择的 IAM 协议 |

旧的 `USE_LDAP_MODS_NAME`、`ANAS_IAM_CLIENTS`、`ANAS_IAM_OIDC_CLIENTS` 和 `ANAS_IAM_SAML_CLIENTS` 已移除，不提供兼容别名。

## Runner 产生的宿主与全局变量

- 主机与网络：`HOST_IP`、`HOST_SUBNET_MASK`、`HOST_DNS_SERVER`、`DEFAULT_GATEWAY_IP`、`VLAN_GATEWAY_IP`、`INTERFACE`、`LOCAL_DNS_SERVER`。
- macvlan 地址规划：`HOST_SEGMENT`、`VLAN_SEGMENT`、`VLAN_SUBNET_MASK`、`VLAN_BRIDGE_IP`、`VLAN_BRIDGE_INTERFACE`、`VLAN_INTERFACE`。仅当存在 `features.host_lan: required` 的 Cask 时才计算——宿主前缀窄于 /28 时无法划出地址池，而没有消费方时这个池本来也没人要。
- 路径与名称：`SERVER_NAME`。
- `DATA_PATH`（应用状态）与 `USER_DATA_PATH`（用户文件）来自工作区布局；`BASE_DOMAIN`、`EMAIL`、`TZ`、容器/镜像/网络前缀等来自全局配置，不算某个业务 Cask 的私有输出。

## lego

- 私有证书变量：`LEGO_DATA_PATH`、`LEGO_CERTS_PATH`、`LEGO_CERTS_USER1000_PATH`、`LEGO_CERT_NAME`、`LEGO_KEY_NAME`、`LEGO_CA_CERT_NAME`、`LEGO_EMAIL`。
- 对外证书契约：`ANAS_TLS_CERTS_DIR`、`ANAS_TLS_CERT_NAME`、`ANAS_TLS_KEY_NAME`、`ANAS_TLS_ISSUER_NAME`、`ANAS_TLS_INTERNAL_CA_NAME`。

其他 Cask 应读取 `ANAS_TLS_*`，不要读取 `LEGO_*` 来判断证书实现。

## samba_dc

- 域与主机：`SAMBA_DC_DOMAIN`、`SAMBA_DC_REALM`、`SAMBA_DC_WORKGROUP`、`SAMBA_DC_NETBIOS_NAME`、`SAMBA_DC_DC_NAME`、`SAMBA_DC_DC_DOMAIN`、`SAMBA_DC_HOST`、`SAMBA_DC_HOST_IP`、`SAMBA_DC_INTERFACES`。
- DNS/LDAPS：`SAMBA_DC_DNS_SERVER`、`SAMBA_DC_DNS_SEARCH`、`SAMBA_DC_DNS_FORWARDERS`、`SAMBA_DC_DNS_ALLOWED_NETWORKS`、`SAMBA_DC_DNS_CACHE_SIZE`、`SAMBA_DC_LDAPS_SERVER_URL`、`SAMBA_DC_LDAPS_SERVER_URL_PORT`、`SAMBA_DC_LDAPS_PORT`。
- 目录根：`SAMBA_DC_BASE_DN`、`SAMBA_DC_BASE_USERS_DN_PREFIX`、`SAMBA_DC_BASE_USERS_DN`、`SAMBA_DC_BASE_GROUPS_DN_PREFIX`、`SAMBA_DC_BASE_GROUPS_DN`、`SAMBA_DC_BASE_GROUPS_ROLE_DN`、`SAMBA_DC_BASE_APP_DN`、`SAMBA_DC_BASE_ADMINS_DN`、`SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN`、`SAMBA_DC_BASE_COMPUTERS_DN`。
- 管理员：`SAMBA_DC_ADMIN_NAME`、`SAMBA_DC_ADMIN_DN`、`SAMBA_DC_ADMIN_PASSWORD`、`SAMBA_DC_ADMIN_GROUP_NAME`、`SAMBA_DC_ADMIN_GROUP_DN`、`SAMBA_DC_ADMINISTRATOR_NAME`、`SAMBA_DC_ADMINISTRATOR_DN`、`SAMBA_DC_ADMINISTRATOR_PASSWORD`。
- 服务绑定：`SAMBA_DC_LDAP_BIND_DN`、`SAMBA_DC_LDAP_BIND_PASSWORD`、`SAMBA_DC_PASSWORD_BIND_DN`、`SAMBA_DC_PASSWORD_BIND_PASSWORD`、`SAMBA_DC_ANCHOR_BIND_DN`、`SAMBA_DC_ANCHOR_BIND_PASSWORD`。
- 身份锚点：`SAMBA_DC_IDENTITY_ANCHOR_BINARY_ATTRIBUTE` 固定为二进制 `mS-DS-ConsistencyGuid`，`SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` 固定为应用使用的文本 `anasIdentityAnchor`；`SAMBA_DC_ANCHOR_USER_BASES`、`SAMBA_DC_ANCHOR_GROUP_BASES` 控制正向扫描范围，`SAMBA_DC_ANCHOR_SCAN_INTERVAL` 控制补漏周期。
- 用户/组属性：`SAMBA_DC_USER_CLASS_NAME`、`SAMBA_DC_USER_CLASS_FILTER`、`SAMBA_DC_USER_ENABLED_FILTER`、`SAMBA_DC_USER_LOGIN_ATTRS`、`SAMBA_DC_USER_NAME`、`SAMBA_DC_USER_DISPLAY_NAME`、`SAMBA_DC_USER_EMAIL`、`SAMBA_DC_GROUP_CLASS_NAME`、`SAMBA_DC_GROUP_CLASS_FILTER`、`SAMBA_DC_GROUP_DISPLAY_NAME`、`SAMBA_DC_GROUP_MEMBER_ATTR`。
- 授权组：`SAMBA_DC_APP_ALL_NAME`、`SAMBA_DC_APP_ALL_DN`、`SAMBA_DC_FS_ADMIN_GROUP_NAME`、`SAMBA_DC_FS_SHARE_RW_GROUP_NAME`。
- Kerberos：`KRB5RCACHETYPE`。

## samba_fs

产生 `SAMBA_FS_NETBIOS_NAME`、`SAMBA_FS_DNS_SERVER`、`SAMBA_FS_ADMIN_USERS`、`SAMBA_FS_SHARE_VALID_USERS`、`SAMBA_FS_SHARE_WRITE_LIST`、`SAMBA_FS_SHARE_DOMAIN_USERS_ACL`、`SAMBA_FS_USE_DEFAULT_DOMAIN` 和 `SAMBA_FS_USERDATA_PATH`。它消费 Samba DC 的域、DNS、管理员和组变量。

文件共享参数 `SHARE_DIR_NAME`、`SHARE_ACCESS_MODE`、`SHARE_GUEST_READ_ONLY`、`USE_DEFAULT_DOMAIN` 归 samba_fs 所有，但以裸名声明：它们是用户在文件管理器里看到的东西，在配置的顶层 `env:` 块里设置。共享树固定挂在容器内的 `/userdata`：这个名字同时是 smb.conf 共享路径和 guest ACL 状态文件的前缀，不可配置。宿主路径 `SAMBA_FS_USERDATA_PATH` 由 `${USER_DATA_PATH}/samba_fs` 推导，**不是** `DATA_PATH`——用户文件属于 `<workspace>/userdata`，而 `<workspace>/data` 会被 restore 整体替换。要把这些文件放到别的盘，把那块盘挂到 `<workspace>/userdata`，一个挂载点解决全部 cask 的用户内容；没有 per-cask 的路径覆盖，因为那会让某个 cask 的文件跑到快照和备份都不知道的地方，同时长得像个普通设置。

## postgres

产生 `POSTGRES_HOST`、`POSTGRES_PORT`、`POSTGRES_HOST_PORT`、`POSTGRES_NETWORK_NAME`、`POSTGRES_USER`、`POSTGRES_USERNAME`、`POSTGRES_PASSWORD`、`POSTGRES_ADMINER_DOMAIN_PREFIX`、`POSTGRES_ADMINER_DOMAIN`。

`POSTGRES_PASSWORD` 是生成 Secret；数据库消费者必须在自己的 `config.consumes` 中显式声明。

## mariadb

产生 `MARIADB_HOST`、`MARIADB_PORT`、`MARIADB_HOST_PORT`、`MARIADB_NETWORK_NAME`、`MARIADB_USERNAME`、`MARIADB_PASSWORD`、`MARIADB_ROOT_PASSWORD`、`MARIADB_ADMINER_DOMAIN_PREFIX`、`MARIADB_ADMINER_DOMAIN`，并发布兼容运行时别名 `MYSQL_HOST`、`MYSQL_PORT`、`MYSQL_USERNAME`、`MYSQL_PASSWORD`。

## traefik

不产生新的跨 Cask 环境变量。它消费全局域名、端口、网络前缀、BasicAuth 和 `ANAS_TLS_*` 证书契约。

## authentik

- 服务端点：`AUTHENTIK_DOMAIN`、`AUTHENTIK_DOMAIN_PORT`、`AUTHENTIK_DOMAIN_FULL`、`ANAS_IAM_PORTAL_URL`。
- PostgreSQL：`AUTHENTIK_NETWORK_DB`、`AUTHENTIK_POSTGRESQL__HOST`、`AUTHENTIK_POSTGRESQL__PORT`、`AUTHENTIK_POSTGRESQL__USER`、`AUTHENTIK_POSTGRESQL__PASSWORD`、`AUTHENTIK_POSTGRESQL__NAME`。
- Samba AD Source：`AUTHENTIK_LDAP_SERVER_URI`、`AUTHENTIK_LDAP_BIND_DN`、`AUTHENTIK_LDAP_BIND_PASSWORD`、`AUTHENTIK_LDAP_BASE_DN`、`AUTHENTIK_LDAP_ADDITIONAL_USER_DN`、`AUTHENTIK_LDAP_ADDITIONAL_GROUP_DN`、`AUTHENTIK_LDAP_USER_OBJECT_FILTER`、`AUTHENTIK_LDAP_GROUP_OBJECT_FILTER`、`AUTHENTIK_LDAP_GROUP_MEMBERSHIP_FIELD`、`AUTHENTIK_LDAP_USER_MEMBERSHIP_ATTRIBUTE`。
- 本地运行 Secret：`AUTHENTIK_SECRET_KEY`、`AUTHENTIK_SIGNING_KEY`、`AUTHENTIK_SIGNING_CERT`、`AUTHENTIK_BREAK_GLASS_PASSWORD`。
- 启动变量：`AUTHENTIK_BOOTSTRAP_PASSWORD`、`AUTHENTIK_BOOTSTRAP_EMAIL`。
- 每应用 IAM 端点：`ANAS_IAM_BINDING__<CASK>__OIDC_*` 或 `ANAS_IAM_BINDING__<CASK>__SAML_*`。

LDAP 变量全部从 `SAMBA_DC_*` 计算，Blueprint 只通过 `!Env` 读取 `AUTHENTIK_LDAP_*`，不保存部署 DN 或密码。
Authentik worker 从 `ANAS_TLS_CERTS_DIR/ANAS_TLS_INTERNAL_CA_NAME` 导入 `anas-samba-ad-ca`，LDAP Source 通过 `peer_certificate` 和 SNI 验证 Samba DC 的 LDAPS 证书；不能依赖空证书字段，因为 Authentik 在该状态下会跳过验证。

## llng

- 数据库别名：`DB_HOST`、`DB_POST`、`DB_USER`、`DB_PASSWORD`。
- Portal：`ANAS_IAM_PORTAL_URL`。
- 动态客户端清单：`OIDC_RP_APPS`、`SAML_SP_APPS`。
- 每应用私有配置：`OIDC_RP__<CASK>__*`、`SAML_SP__<CASK>__*`。
- 每应用通用端点：`ANAS_IAM_BINDING__<CASK>__OIDC_*`、`ANAS_IAM_BINDING__<CASK>__SAML_*`。

LLNG 从 `ANAS_IDENTITY_OIDC_CLIENTS` 和 `ANAS_IDENTITY_SAML_CLIENTS` 读取最终协议名单。

## nextcloud

- 地址/容器：`NEXTCLOUD_DOMAIN`、`NEXTCLOUD_DOMAIN_PORT`、`NEXTCLOUD_DOMAIN_FULL`、`NEXTCLOUD_HOSTNAME`、`NEXTCLOUD_PUSH_HOSTNAME`、`NEXTCLOUD_BASE_PATH`、`NEXTCLOUD_DATA_DIR`、`NEXTCLOUD_NETWORK_DB`。
- 管理员：`NEXTCLOUD_ADMIN_USERNAME`、`NEXTCLOUD_ADMIN_USER`、`NEXTCLOUD_ADMIN_PASSWORD`。
- LDAP：`NEXTCLOUD_USER_FILTER`、`NEXTCLOUD_USER_LOGIN_FILTER`、`NEXTCLOUD_USER_COMPLEX_PASS`。
- SAML：`NEXTCLOUD_SAML_IDP_*`、`NEXTCLOUD_SAML_SP_PRIVATE_KEY`、`NEXTCLOUD_SAML_SP_CERT`、`NEXTCLOUD_IAM_HOST`。
- Redis/Imaginary/Talk：`NEXTCLOUD_REDIS_*`、`NEXTCLOUD_IMAGINARY_*`、`NEXTCLOUD_TALK_*`、`TALK_SIGNALING_SECRET`。
- 镜像运行别名：`MYSQL_*`、`POSTGRES_*`、`REDIS_HOST`、`PHP_*`、`APACHE_BODY_LIMIT`、`OVERWRITE*`。
- 应用注册：`ANAS_IAM_CLIENT__NEXTCLOUD__*`、`APPS_LIST__NEXTCLOUD__*`。

## meshcentral

产生 `MESHCENTRAL_DOMAIN`、`MESHCENTRAL_TITLE`、`MESHCENTRAL_SUBTITLE`、`MESHCENTRAL_USER_FILTER`、`MESHCENTRAL_USER_LOGIN_FILTER`。它消费 MariaDB 和 Samba DC 的 LDAPS 地址、`svc_ldap` 凭据、用户/组 DN 与属性。

## netbird

- 地址：`NETBIRD_DOMAIN`、`NETBIRD_DOMAIN_PORT`、`NETBIRD_DOMAIN_FULL`、`NETBIRD_DASHBOARD_ENDPOINT`、`NETBIRD_MGMT_API_ENDPOINT`、`NETBIRD_MGMT_GRPC_API_ENDPOINT`、`NETBIRD_SIGNAL_ENDPOINT`、`NETBIRD_RELAY_ENDPOINT`。
- OIDC：`AUTH_AUDIENCE`、`AUTH_AUTHORITY`、`AUTH_CLIENT_ID`、`AUTH_CLIENT_SECRET`、`AUTH_SUPPORTED_SCOPES`、`NETBIRD_AUTH_AUTHORITY`、`NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT`、`NETBIRD_AUTH_USER_ID_CLAIM`。
- Secret：`NETBIRD_DATASTORE_ENC_KEY`、`NETBIRD_RELAY_AUTH_SECRET`。
- IAM 注册：`ANAS_IAM_CLIENT__NETBIRD__*`。
- Launcher：`APPS_LIST__NETBIRD__*`。

## lam

产生 `LAM_DOMAIN`、`LAM_LANGUAGE`、`LAM_ADMIN_PASSWORD`。LAM 的运行配置直接消费 Samba DC 的 LDAPS 地址、Base DN、用户/组/计算机 DN 和 AD 管理员凭据。

## collabora

产生 `COLLABORA_ADMIN_USERNAME`、`COLLABORA_ADMIN_PASSWORD`、`COLLABORA_ALIAS_GROUP`、`COLLABORA_EXTRA_PARAMS`，并消费 Nextcloud 的公开域名。

## eturnal

产生 `ETURNAL_DOMAIN`、`TURN_DOMAIN`、`TURN_DOMAIN_PORT`、`TURN_HOSTNAME` 和生成 Secret `TURN_SECRET`。Nextcloud、NetBird 等 TURN 消费方必须显式声明 `TURN_SECRET`。

## ddns

产生 `DDNS_DOMAIN`、`DDNS_DNS_SERVER`、`DDNS_CONFIG`、`DDNS_IPV6_AVAILABLE` 和运行时 `DNS_PROVIDER`，并消费 DNS Provider 的用户 Secret。

## freeradius

当前 Cask 没有 Hook 产生的环境变量，也没有完成目录用户源或 RADIUS Client 配置；它仍是运行脚手架。

## 维护规则

1. Hook 新增、删除或重命名输出变量时，必须同步更新本文。
2. 跨 Cask 的敏感变量必须在消费方 `config.consumes` 中逐项声明。
3. 实现私有变量使用 Cask 前缀；跨实现契约使用 `ANAS_*`。
4. 身份协议清单只由 Runner 产生，Cask 不得追加或覆盖。
5. 文档只记录变量名和语义，禁止写入实际密码、Token 或私钥。

## 作用域：一个 cask 收到什么

渲染出的 `.env` 只包含这个 cask**声明过**的东西，而不是它依赖闭包里碰巧存在的一切：

```
.env = 全局所有权的键
     + 自己前缀的键（<CASK>_*，或 config.exports 声明的裸名）
     + config.consumes 显式声明的跨 cask 键
     + 用户在 env: / services.<cask>.env 里显式写的
     + runner 注入的（MODULE_NAME 等）
```

依赖闭包只决定启动顺序。依赖 postgres 不等于被交付 postgres 的全部变量——闭包回答的是"谁可能相关"，不是"谁真的需要"。改之前 collabora 的容器拿到 264 个变量，其中它自己只用到 19 个；现在是 49 个。整个部署从 2524 降到 1142。

因为每个 cask 的 compose 都写了 `env_file: .env`，`.env` 里的东西会原样进入容器进程环境——出现在 `docker inspect`、`/proc/<pid>/environ`、崩溃转储里。所以这不只是整洁问题。

漏声明的后果是容器拿到空值、立刻可见地失败，而不是"多给了也看不出来"。`test-env/scripts/test-env-scope.sh` 会渲染两遍（旧规则 / 新规则）并证明没有任何到达应用的值发生变化。

## 命名规则

- **全局参数**：env 键 = 参数名大写。例外只有 `timezone` → `TZ`（容器镜像的既定约定）。
- **cask 参数**：env 键 = `<CASK>_` + 参数名大写，除非在 `config.exports` 里声明为裸名。
- **`ANAS_` 前缀**：标记 runner 推导出来的跨 cask 契约键（`ANAS_IAM_*`、`ANAS_TLS_*`、`ANAS_FORWARD_AUTH_*` 等），不是用户设置。

映射只有一处定义（`internal/config` 的 `globalBindings`），并由测试保证是双射——两个参数不能映射到同一个键。

`anas config list [global|<cask>]` 打印每个参数的路径、env 键、默认值、当前值和变更效果。
