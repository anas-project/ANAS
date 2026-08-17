# Samba file server

加入 Samba AD 的 SMB 文件共享服务。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `samba_fs` |
| 版本 / revision | `4.23.6-r3` |
| 状态 | `release` |
| 类别 | `storage` |
| 运行时 | `compose` |

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

## 管理员登录与 IAM 故障恢复

没有 Web 管理员或本地恢复账号。目录或域加入故障时需恢复 Samba AD 链路。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | `all_read_group_write` | `SHARE_ACCESS_MODE` | 否 | 否 | 是 | `reconcile` | 共享访问模式 |
| `env.SHARE_DIR_NAME` | string | `Share` | `SHARE_DIR_NAME` | 否 | 否 | 否：`migrate-share-directory` | `data_migrate` | 共享目录名 |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | `No` | `SHARE_GUEST_READ_ONLY` | 否 | 否 | 是 | `reconcile` | Guest 是否只读 |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`) | `yes` | `USE_DEFAULT_DOMAIN` | 否 | 否 | 是 | `container_recreate` | 是否使用默认域 |
| `samba_fs.hostname` | string | `SambaFS` | `SAMBA_FS_HOSTNAME` | 否 | 否 | 否：`rejoin-samba-member` | `data_migrate` | 主机名 |
| `samba_fs.log_level` | int | `1` | `SAMBA_FS_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_fs.wsdd_log_level` | int | `0` | `SAMBA_FS_WSDD_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | WSDD 日志级别 |

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

- Module version / 版本：`4.23.6-r3`（reviewed 2026-08-13）
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
