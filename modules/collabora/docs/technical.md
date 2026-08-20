# Collabora Online 技术实现

本文面向 Module 维护者，记录 `collabora` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `26.4.2-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `nextcloud` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_collabora` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-collabora:26.04.2.4.1` | `` | 0 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `collabora.admin_password` | string | — | — | `generated` | `COLLABORA_ADMIN_PASSWORD` | 否 | 是 | 是 | 是 | `container_recreate` | 管理界面或服务管理员密码 |
| `collabora.admin_username` | string | — | `admin_collabora` | `static` | `COLLABORA_ADMIN_USERNAME` | 否 | 否 | 否 | 是 | `container_recreate` | 管理界面用户名 |
| `collabora.auto_save` | int | — | `60` | `static` | `COLLABORA_AUTO_SAVE` | 否 | 否 | 否 | 是 | `container_recreate` | 自动保存间隔 |
| `collabora.domain_prefix` | string | — | `collabora` | `static` | `COLLABORA_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `collabora.log_level` | string | — | `warning` | `static` | `COLLABORA_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

不直接同步目录，也不是 IAM Consumer。最终用户身份和文档权限由 Nextcloud/WOPI 会话决定。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

管理控制台使用 Module 自己的 `admin_username`/`admin_password`。该账号尚未声明为托管本地账号，因此不能使用 `anas admin local credential/rotate collabora`。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `COLLABORA_ADMIN_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `COLLABORA_DOMAIN_FULL`
- `COLLABORA_HOSTNAME`

### 显式消费

- `NEXTCLOUD_DOMAIN_FULL`
- `TRAEFIK_BASE_PORT`

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

管理员密码变更需要按配置项声明的重建流程处理；不要把它当作已验证的在线轮换。
