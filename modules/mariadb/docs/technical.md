# MariaDB 技术实现

本文面向 Module 维护者，记录 `mariadb` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `12.3.2-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | 提供 Contract | `1.0.0` / `mariadb` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_mariadb` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-mariadb:12.3.2` | `mariadb` | 2 |
| `anas_mariadb_adminer` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-adminer:5.5.0` | `mariadb, traefik` | 0 |
| `anas_mariadb_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-mariadb:12.3.2` | `mariadb` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mariadb.adminer_enabled` | bool | — | `false` | `static` | `MARIADB_ADMINER_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Adminer |
| `mariadb.root_password` | string | — | — | `generated` | `MARIADB_ROOT_PASSWORD` | 否 | 是 | 是 | 否：`rotate-mariadb-root-password` | `credential_rotate` | 数据库 root 密码 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

数据库服务不使用目录或 IAM。每个 Consumer 由 Provider operation 创建独立数据库和专属账号。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

root 密码是 Provider 管理凭据，不是 Module 本地管理员。Adminer 启用后使用数据库账号登录。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `MARIADB_ROOT_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 提供 `relational_database/mariadb` Contract，版本 `1.0.0`。

## 环境变量所有权

### 导出

- `MYSQL_*`

### 显式消费

—

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

普通 `config set` 不能完成 root 密码轮换；必须使用声明的凭据生命周期流程。
