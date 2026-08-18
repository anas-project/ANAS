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
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

修改主机名需要重新加入域；修改共享目录名需要迁移文件，普通 apply 不会搬运数据。
