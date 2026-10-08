# Collabora Online 技术实现

本文面向 Module 维护者，记录 `collabora` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `26.4.2-r6` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `nextcloud` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_collabora` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-collabora:26.04.2.4.1` | `` | 1 |
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

冻结的 `.hook.bin --container-start` 同时作为容器入口。它把 `/opt/cool/systemplate` 与 `/opt/collaboraoffice` 复制到单个受管挂载 `/var/lib/anas-collabora` 下的 `systemplate` 与 `office`。复制保留权限、所有权、符号链接和硬链接，遇到特殊文件拒绝启动；两份模板完成后才写固定镜像版本标记。入口只清理本实例可丢弃的 `child-roots` 和 `cache`，清空补充组并降为 GID/UID `1001:1001`，然后 `exec coolwsd`，使 Docker 正常停止信号直接到达应用。

原有 `--use-env-vars`、文件服务、日志与配置变更停止参数保持有效。模板、jail 和缓存都指向受管树，`--lo-template-path` 选择复制后的 office 模板；`mount_jail_tree=false` 避免增加挂载特权。Compose 以 `0:0` 完成初始化，只读挂载冻结 Hook，并提供 180 秒正常停止等待。该实现不依赖镜像内 shell、`cp` 或 `setpriv`，不新增服务，也不重建上游镜像。Runner 生成带 `create_host_path: false` 的临时 bind，并核验实际挂载与持久租约一致。

Compose 健康检查执行同一冻结 Go Hook 的 `/usr/local/bin/anas-collabora-start --container-probe` 入口；该入口拒绝额外参数，只 `exec /usr/bin/coolwsd --probe` 并保留原生退出码。Compose 的初始化身份仍为 `0:0`，健康入口先清空补充组，再固定降为 GID/UID `1001:1001`；任何降权失败均拒绝执行 probe，不能退回 root。该入口不初始化模板、不清空 `child-roots` 或 `cache`，也不启动 `coolwsd` 服务。原生 probe 的结果继续决定健康状态，健康检查不会用 discovery 或初始化标记替代它。

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`container_start_test.go`](../hook/container_start_test.go)：模板完整性、权限、链接及特殊文件拒绝反例
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

管理员密码变更需要按配置项声明的重建流程处理；不要把它当作已验证的在线轮换。
