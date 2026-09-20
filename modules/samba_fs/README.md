# Samba file server

加入 Samba AD 的 SMB 文件共享服务。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `samba_fs` |
| 版本 / revision | `4.23.6-r6` |
| 状态 | `release` |
| 类别 | `storage` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `samba_dc` | Module | — |

## 最简配置

```yaml
modules:
  samba_fs: {}
```

## 身份、用户与 Group

SMB 客户端直接使用目录身份。`FS Share RW`/`FS Admins` 等 Group 控制读写权限；用户和 Group 在 Samba AD/LAM 中管理，不在本 Module 内同步副本。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | 不支持/不适用 |
| Group | `FS Share RW`, `FS Admins` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更说明

**匹配键：对象 SID（经 `idmap backend = rid` 投影成 POSIX UID/GID）。** 本 Module 以
`security = ADS` / `server role = MEMBER SERVER` 加入域，不保存用户副本；文件的归属与 NT ACL 落在
文件系统上，键是 SID：`idmap config <WORKGROUP> : backend = rid` 把 SID 的 RID 部分确定性地映射成
UID/GID，`vfs objects = acl_xattr` 把 NT ACL 以 SID 形式写进 `security.NTACL` 扩展属性。

SID 与 `anasIdentityAnchor` 都是不可变键，改名不改变它们，因此**文件归属与 ACL 跨改名稳定**，
满足 `DIRKEY-R-002` 的判据。（两者的差别在 anchor 能跨森林重建存活而 SID 不能；这与本 Module
无关，SMB 协议本来就只认 SID。）

**但有一个标签被投影进了文件路径。** `[Home]` 共享的 `path = /userdata/Home/%U`，`%U` 是会话
用户名（`sAMAccountName`）。这不是身份键——归属仍按 UID——但它决定**目录叫什么名字**，改名后
会出现下表第一行的后果。

| 目录侧变更 | samba_fs 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | 文件归属与 ACL 不变（键是 SID/UID）。但 `[Home]` 的路径 `/userdata/Home/%U` 跟着新名走：**该用户再登录会拿到一个新建的空家目录**，旧目录留在 `/userdata/Home/<旧名>`，仍归他的 UID 所有但从共享里看不见。共享目录不受影响 | 路径按 `%U` 展开、登录时按需建目录：`已验证`（`smb.conf.envsubst` 的 `[Home]` 与 `samba_create_user_dir.sh`）；改名后的实际表现：`推断`（无改名 E2E） |
| `mail` 改变 | 完全不参与；SMB 不使用邮箱 | `已验证`（`smb.conf.envsubst` 与 Hook 均不消费 `mail`） |
| `displayName` 与其他 profile 属性 | 不参与，也不缓存 | `已验证`（同上） |
| 直接或递归组成员变更 | 由 winbind 解析，`winbind expand groups = 2` 展开两层嵌套。`valid users`/`write list`/`admin users` 与 POSIX ACL 都按组名与组 GID 判定。收敛受 winbind 的缓存 TTL 与用户已有 SMB 会话影响，**不是实时** | 组驱动授权：`已验证`（`smb.conf.envsubst` 的 `valid users`/`write list` 与 `fix_perm.sh` 的 `setfacl`）；收敛时延：`推断` |
| 账号停用 | 新的 SMB 认证失败（Kerberos/NTLM 由 DC 裁决）。**已建立的 SMB 会话不会被踢掉**，winbind 也不会主动断开；`winbind refresh tickets = Yes` 只在票据有效期内续期 | `推断` |
| 账号删除 | 同上。**文件全部保留**，归属仍是那个已不存在的 UID，在 `ls -l` 里显示为裸数字；家目录与其在共享里留下的文件都不会自动转交 | `推断` |
| 标识符回收再分配 | **fail-open 风险在这里是真实的**：新人拿到回收的 `sAMAccountName` 后，若 AD 也把同一个 RID 重新分配（AD 正常不回收 RID，但域重建或 SID 历史迁移会），新人的 UID 会与旧人相同，从而**继承旧人全部文件的所有权**。即使 RID 不同，新人登录也会拿到 `/userdata/Home/<回收的名字>` 这个路径——如果旧目录还在，他会看到一个不属于自己 UID 的目录（无权读，但存在） | `推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 停用或删除目录账号后，**必须主动断开其已建立的 SMB 会话**：在容器内执行
   `smbcontrol smbd close-share <share>`，或重启 `samba_fs` 容器；仅在 AD 停用不会踢掉在线会话；
2. 删除目录账号前，先把 `/userdata/Home/<用户名>` 与其在共享里的文件转交给接手人
   （`chown -R` 到新属主），再删除账号；否则文件会挂在一个不再解析的 UID 上；
3. **改名后要人工迁移家目录**：把 `/userdata/Home/<旧名>` 重命名为 `/userdata/Home/<新名>`，
   否则用户会看到一个空的新家目录。这一步没有自动路径；
4. **目录侧流程约束**：`sAMAccountName` 不得回收再分配，且不得在域重建后重用 RID。

## 管理员登录与 IAM 故障恢复

没有 Web 管理员或本地恢复账号。目录或域加入故障时需恢复 Samba AD 链路。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | — | `all_read_group_write` | `static` | `SHARE_ACCESS_MODE` | 否 | 否 | 否 | 是 | `reconcile` | 共享访问模式 |
| `env.SHARE_DIR_NAME` | string | — | `Share` | `static` | `SHARE_DIR_NAME` | 否 | 否 | 否 | 否：`migrate-share-directory` | `data_migrate` | 共享目录名 |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | — | `No` | `static` | `SHARE_GUEST_READ_ONLY` | 否 | 否 | 否 | 是 | `reconcile` | Guest 是否只读 |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`, `true`, `false`) | — | `yes` | `static` | `USE_DEFAULT_DOMAIN` | 否 | 否 | 否 | 是 | `container_recreate` | 是否使用默认域 |
| `samba_fs.hostname` | string | — | `SambaFS` | `static` | `SAMBA_FS_HOSTNAME` | 否 | 否 | 否 | 否：`rejoin-samba-member` | `data_migrate` | 主机名 |
| `samba_fs.log_level` | int | — | `1` | `static` | `SAMBA_FS_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_fs.wsdd_log_level` | int | — | `0` | `static` | `SAMBA_FS_WSDD_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | WSDD 日志级别 |

### 查询和修改

```bash
anas config list samba_fs -w /srv/anas
anas config explain samba_fs.share_access_mode
anas config set samba_fs.share_access_mode all_rw -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list samba_fs -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

修改主机名需要重新加入域；修改共享目录名需要迁移文件，普通 apply 不会搬运数据。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`4.23.6-r6`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The file server receives TZ and includes tzdata; client-visible timestamps are also affected by SMB client behavior.
- Language scope / 语言范围：SMB protocol service
- Selection / 选择方式：`client`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：File-manager language belongs to each SMB client, not the server Module.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [4.23.6 — server protocol configuration; no Module Web UI](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html)
<!-- generated:localization:end -->
