# Samba file server 技术实现

本文面向 Module 维护者，记录 `samba_fs` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `4.23.6-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `samba_dc` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_fs` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-fs:4.23.6-r5` | `default` | 2 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | — | `all_read_group_write` | `static` | `SHARE_ACCESS_MODE` | 否 | 否 | 否 | 是 | `reconcile` | 共享访问模式 |
| `env.SHARE_DIR_NAME` | string | — | `Share` | `static` | `SHARE_DIR_NAME` | 否 | 否 | 否 | 否：`migrate-share-directory` | `data_migrate` | 共享目录名 |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | — | `No` | `static` | `SHARE_GUEST_READ_ONLY` | 否 | 否 | 否 | 是 | `reconcile` | Guest 是否只读 |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`, `true`, `false`) | — | `yes` | `static` | `USE_DEFAULT_DOMAIN` | 否 | 否 | 否 | 是 | `container_recreate` | 是否使用默认域 |
| `samba_fs.hostname` | string | — | `SambaFS` | `static` | `SAMBA_FS_HOSTNAME` | 否 | 否 | 否 | 否：`rejoin-samba-member` | `data_migrate` | 主机名 |
| `samba_fs.log_level` | int | — | `1` | `static` | `SAMBA_FS_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_fs.wsdd_log_level` | int | — | `0` | `static` | `SAMBA_FS_WSDD_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | WSDD 日志级别 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## AD 域边界与成员机信任

Samba FS 的成员机身份只消费 Samba DC 导出的目录值：`SAMBA_DC_DOMAIN`、
`SAMBA_DC_REALM`、`SAMBA_DC_DNS_SEARCH`、`SAMBA_DC_DC_DOMAIN`、workgroup 和 DNS server。
它不会用 `BASE_DOMAIN` 计算 join 参数。`global.base_domain` 只控制应用/Web 命名空间；
`modules.samba_dc.config.domain` 才控制 AD DNS 域、Kerberos Realm 和机器信任。旧配置省略
`samba_dc.config.domain` 时，Samba DC 会为兼容把它回退到 `BASE_DOMAIN`。

Samba DC 的 `application_dns_mode` 只决定应用完整 FQDN 放进 AD zone（`ad_zone`）还是独立
应用 zone（`separate_zone`）；它不改变 FS 使用的 AD 域或 canonical DC FQDN。ANAS LDAP
消费者使用的 `SAMBA_DC_HOST=BASE_DOMAIN` 是指向 `SAMBA_DC_HOST_IP` 的 TLS 服务别名，
Samba FS 不把该别名当作 Kerberos/成员机 canonical name。

因此只改变应用域不得触发 Samba FS leave/join，也不得改写已有机器账号。当前已有
workspace 的服务域和应用 DNS zone 迁移器尚未交付；不要绕过门禁测试这一场景。已
provision 的 `SAMBA_DC_DOMAIN` 不支持原地换域：新 AD 域要求新建目录并重新加入 Samba FS。

运行时接线保持同一边界：Compose 的初始 resolver 与容器内 `/etc/resolv.conf` 都只把
`SAMBA_DC_DNS_SERVER` 设为 nameserver，并用 `SAMBA_DC_DNS_SEARCH` 作为 search domain；
不会回退到 `LOCAL_DNS_SERVER`、宿主 resolver 或 VLAN gateway。`krb5.conf` 的 Realm、KDC
canonical FQDN 与 domain mapping 分别来自 `SAMBA_DC_REALM`、`SAMBA_DC_DC_DOMAIN` 和
`SAMBA_DC_DOMAIN`；`smb.conf` 的 workgroup/realm 来自 `SAMBA_DC_WORKGROUP` 与
`SAMBA_DC_REALM`。

每次启动先执行 `net ads testjoin`。已有 trust 有效时立即复用，不执行 join，更不存在
自动 leave 路径；只有 trust 无效时才用 Samba DC 管理员凭据重试 `net ads join`。join 返回
成功后仍必须再次通过 `net ads testjoin`，否则继续重试，不能把未验证的机器账号当成 ready。
随后以机器账号执行 `net ads dns register -P`；注册失败会阻断启动，避免 FS FQDN 指向旧
地址。健康检查使用 `wbinfo -t` 验证同一份 `smb.conf` 与成员机 trust，不读取应用域或 TLS 服务别名。
因此即使应用域变化导致容器重建，已有 AD trust 仍只会被检查和复用。

`SAMBA_DC_DNS_SERVER` 是 Samba DC Hook 从 `SAMBA_DC_HOST_IP` 导出的数值地址，Docker
安装 resolver 时无需先解析 DC 名称，不形成 DNS 启动环。Samba DC 是本 Module 的单向
依赖且不依赖 Samba FS；若 DC 尚未 ready，join helper 会等待并再次执行 `testjoin`。DC
恢复后已有 trust 有效就直接返回，只有可达后 trust 仍无效才进入 join。

## 身份与授权数据流

SMB 客户端直接使用目录身份。`FS Share RW`/`FS Admins` 等 Group 控制读写权限；用户和 Group 在 Samba AD/LAM 中管理，不在本 Module 内同步副本。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | 不支持/不适用 |
| Group | `FS Share RW`, `FS Admins` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

没有 Web 管理员或本地恢复账号。目录或域加入故障时需恢复 Samba AD 链路。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `SAMBA_DC_ADMIN_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `SHARE_DIR_NAME`
- `SHARE_ACCESS_MODE`
- `SHARE_GUEST_READ_ONLY`
- `USE_DEFAULT_DOMAIN`

### 显式消费

- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_DC_DOMAIN`
- `SAMBA_DC_DNS_SEARCH`
- `SAMBA_DC_DNS_SERVER`
- `SAMBA_DC_DOMAIN`
- `SAMBA_DC_FS_ADMIN_GROUP_NAME`
- `SAMBA_DC_FS_SHARE_RW_GROUP_NAME`
- `SAMBA_DC_REALM`
- `SAMBA_DC_WORKGROUP`
- `SAMBA_DC_ADMIN_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`domain_wiring_test.go`](../hook/domain_wiring_test.go)
- [`join_ad.sh`](../samba_fs/root/usr/local/bin/join_ad.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

修改主机名需要重新加入当前 `SAMBA_DC_DOMAIN`；修改共享目录名需要迁移文件，普通 apply
不会搬运数据。已有 AD 不支持原地换域，且已有 workspace 的应用域/内部 DNS zone 迁移器
尚未交付。
