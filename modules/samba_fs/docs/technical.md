# Samba file server 技术实现

本文面向 Module 维护者，记录 `samba_fs` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `4.23.6-r6` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `samba_dc` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_fs` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-fs:4.23.6-r6` | `default` | 2 |
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

运行时接线保持同一身份边界，但把身份与传输分开。Samba FS 位于 macvlan 子接口，不能
直接访问父接口上的宿主地址；Hook 因此从宿主侧 macvlan bridge 的 `VLAN_BRIDGE_IP` 派生
私有的 `SAMBA_FS_DC_TRANSPORT_IP`（没有 bridge 值时兼容回退到
`SAMBA_DC_DNS_SERVER`）。Compose 的初始 resolver 与容器内 `/etc/resolv.conf` 仍使用
DC 原始监听地址 `SAMBA_DC_DNS_SERVER` 和 `SAMBA_DC_DNS_SEARCH`；初始化脚本为该 DNS
地址安装一条以 transport IP 为下一跳的 `/32` 路由。同时，`/etc/hosts` 将 canonical
`SAMBA_DC_DC_DOMAIN` 映射到 transport IP。Kerberos 和 AD 仍只以 canonical FQDN 标识 DC：
`krb5.conf` 的 Realm、KDC FQDN 与 domain mapping 分别来自 `SAMBA_DC_REALM`、
`SAMBA_DC_DC_DOMAIN` 和 `SAMBA_DC_DOMAIN`，不会把 bridge IP 当成 Kerberos 身份；
`smb.conf` 的 workgroup/realm 来自 `SAMBA_DC_WORKGROUP` 与 `SAMBA_DC_REALM`。

每次启动先执行 `net ads testjoin`。已有 trust 有效时立即复用，不执行 join，更不存在
自动 leave 路径；只有 trust 无效时才用 Samba DC 管理员凭据重试 `net ads join`。join 返回
成功后仍必须再次通过 `net ads testjoin`，否则继续重试，不能把未验证的机器账号当成 ready。
随后用短生命周期 Kerberos cache 取得 DC 管理员票据，以 `samba-tool dns` 幂等删除旧 A
记录、写入当前成员地址，并直接查询 `SAMBA_DC_DNS_SERVER` 验证结果；该查询的数据包通过
前述 `/32` 路由跨过 macvlan 隔离。密码只从标准输入进入
`kinit`，不出现在进程参数或日志中。注册或验证失败会阻断启动，避免 FS FQDN 指向旧地址。
健康检查使用 `wbinfo -t` 验证同一份 `smb.conf` 与成员机 trust，不读取应用域或 TLS 服务别名。
因此即使应用域变化导致容器重建，已有 AD trust 仍只会被检查和复用。

DNS server 与传输下一跳都是数值地址，Docker 安装 resolver 和路由时无需先解析 DC 名称，
不形成 DNS 启动环。
Samba DC 是本 Module 的单向依赖且不依赖 Samba FS；若 DC 尚未 ready，join helper 会
等待并再次执行 `testjoin`。DC 恢复后已有 trust 有效就直接返回，只有可达后 trust 仍无效
才进入 join。

## 身份与授权数据流

SMB 客户端直接使用目录身份。`FS Share RW`/`FS Admins` 等 Group 控制读写权限；用户和 Group 在 Samba AD/LAM 中管理，不在本 Module 内同步副本。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | 不支持/不适用 |
| Group | `FS Share RW`, `FS Admins` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。

- **身份存在哪张表/哪个字段**：没有数据库。持久身份落在**文件系统**上：inode 的 UID/GID，
  以及 `security.NTACL` 扩展属性里的 NT ACL（由 `vfs objects = acl_xattr` 写入，
  `map acl inherit = Yes` 继承）。另有 winbind 的 `idmap` tdb 缓存，但它是可重建的映射缓存，
  不是事实来源。
- **匹配键怎么配出来的**：`smb.conf.envsubst` 的
  `idmap config ${SAMBA_DC_WORKGROUP} : backend = rid` / `range = 10000-999999`。rid backend 是
  **确定性算法**：UID = range 起点 + SID 的 RID。因此同一个 SID 在任何时候、任何成员机上都映射到
  同一个 UID，不需要持久映射表，也不会因改名漂移。默认域 `idmap config * : backend = tdb` /
  `range = 3000-7999` 只服务本地与信任域回退。
- **每次登录刷新什么**：没有可刷新的副本。用户与组由 winbind 经 `nsswitch.conf` 实时解析；
  `winbind enum users/groups = No` 关闭枚举，`winbind expand groups = 2` 限定嵌套展开层数。
- **撤权经哪个接口**：DC 的 Kerberos/NTLM 认证裁决（新会话）与 `valid users`/`write list`/
  POSIX ACL（授权）。**没有作用于已建立 SMB 会话的接口**——`smbcontrol` 是运维手动动作，不是
  自动路径。
- **对账或事件订阅路径**：没有，也不需要保留目录副本，因此不落入
  [目录事件订阅要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
  的适用范围。winbind 缓存的收敛时延由其自身 TTL 决定。
- **没有自动路径的地方，技术阻碍是什么**：**SMB 协议层面没有"按目录事件断开会话"的原语**。
  Samba 提供的是 `smbcontrol`，一个管理员命令，没有可供 ANAS 调用的事件接口。这不是缺不可变
  ID（这里有 SID），是缺撤权接口。

**`DIRKEY-R-002` 符合性**：符合。持久键是 SID/UID，目录改名不改变它，文件归属与 ACL 因此跨改名
稳定。本 Module 不消费 `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`，因为 SMB 与 POSIX 都只认 SID；
用 anchor 反而要引入一层本不存在的映射表。

**`DIRKEY-R-010` 观察点：`[Home]` 把用户名投影进了文件路径。** `path = /userdata/Home/%U` 与
`root preexec = /usr/local/bin/samba_create_user_dir.sh /userdata/Home %U`。被投影的是
`sAMAccountName` 这个**标签**，不是 anchor，因此不违反 `DIRKEY-R-010`（它禁止的是把 anchor 投影
进路径）。但它确实让改名产生一个孤立目录，后果与兜底写在 README。**用 anchor 或 SID 命名家目录
会让路径变成 UUID/S-1-5-… 形态**，那才是 `DIRKEY-R-010` 禁止的形态——因此这里维持用户名命名是
正确选择，代价由第 3 条兜底动作承担。

**`DIRKEY-R-013` 投影结论：不适用。** 本 Module 不是 OIDC/SAML Consumer（`module.yml` 不声明
`iam`，也不消费任何 IAM binding），不消费主体标识符，M2 的切换对它没有任何影响。

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
- [`register_ad_dns.sh`](../samba_fs/root/usr/local/bin/register_ad_dns.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

修改主机名需要重新加入当前 `SAMBA_DC_DOMAIN`；修改共享目录名需要迁移文件，普通 apply
不会搬运数据。已有 AD 不支持原地换域，且已有 workspace 的应用域/内部 DNS zone 迁移器
尚未交付。
