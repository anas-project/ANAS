# PostgreSQL 技术实现

本文面向 Module 维护者，记录 `postgres` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `18.4.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | 提供 Contract | `1.0.0` / `postgres` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_postgres` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-postgres:18.4.0-r4` | `postgres` | 1 |
| `anas_postgres_adminer` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-adminer:5.5.0` | `postgres, traefik` | 0 |
| `anas_postgres_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-postgres:18.4.0-r4` | `postgres` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | bool | — | `false` | `static` | `POSTGRES_ADMINER_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Adminer |
| `postgres.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `POSTGRES_FORWARD_AUTH_INTERFACE` | 否 | 否 | 否 | 是 | `container_recreate` | Adminer 所用认证网关的接口，`auto` 由 Runner 选择 |
| `postgres.password` | string | — | — | `generated` | `POSTGRES_PASSWORD` | 否 | 是 | 是 | 否：`rotate-postgres-password` | `credential_rotate` | 管理员或服务密码 |
| `postgres.username` | string | — | `postgres` | `static` | `POSTGRES_USERNAME` | 否 | 否 | 否 | 否：`migrate-postgres-owner` | `data_migrate` | 数据库管理员用户名 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

数据库服务不使用目录或 IAM。每个 Consumer 获得独立数据库、角色和生成凭据。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

超级用户密码是 Provider 凭据，不是本地管理员。Adminer 启用后使用数据库账号登录。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `POSTGRES_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 提供 `relational_database/postgres` Contract，版本 `1.0.0`。

当前扩展组合为 PG `18.4` / Alpine + `vector 0.8.2`、`cube 1.5`、`earthdistance 1.2`，需要的
`shared_preload_libraries` 为精确空值。pgvector 源码归档及 SHA-256 固定在 `postgres/Dockerfile`，
构建时采用通用 CPU 参数；运行时不下载、不编译。服务与 provision 使用同一 `anas-postgres` 镜像。

Resource 投影 `ANAS_RESOURCE_POSTGRES_EXTENSIONS` 为经 Runner 校验的逗号列表。Provider 写入前检查
全列表 allowlist、镜像默认版本、实例 HBA/preload 及已有扩展版本。`earthdistance` 隐式补 `cube`，并
在任何请求顺序下先创建 `cube`；未知、重复或非法输入均失败。普通 ensure 只创建缺失扩展，不运行 ALTER。

就绪先由 Provider 权限检查固定 PG 版本、SCRAM HBA 与 preload，再使用实际应用口令经 TCP 连接，
检查角色无 superuser/createdb/createrole/replication/bypassrls 权限及获授角色成员关系、扩展精确版本，并执行向量距离和
earthdistance 函数。PG 限制普通角色读取 preload 参数，因此该实例级检查由 Provider 完成，不授予
`pg_read_all_settings`。inspect 只读并输出 Contract 的 `exists/ready`；任一实际检查失败退出非零。

`postgres/entrypoint.sh` 复用固定官方入口函数。新装使用 SCRAM initdb；既有目录通过不监听 TCP、
私有 Unix socket 的临时 PG 修改管理员 SCRAM 并原子替换受管 HBA，然后才启动正式 TCP 监听。
普通 Resource 口令由随后的 ensure 重新投影为 SCRAM，管理员凭据从不投影到 Consumer。

## 环境变量所有权

### 导出

—

### 显式消费

—

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

PG `after_start` 是消费者启动前的 Provider barrier：等待有上限的真实 TCP 管理员登录后，默认执行
`anas-postgres-extensions inspect`。仅 Runner 在 workspace 停写及 ANAS 恢复点成功后向本次请求传入
`ANAS_POSTGRES_EXTENSION_MAINTENANCE=true` 才运行固定升级；此值不得持久化为普通部署配置。
备份暂停后的补偿仍使用 `compose start` 恢复既有容器，但会在 PostgreSQL Provider 启动后执行同一只读
`after_start` 资格检查，再启动后续消费者。Provider 启动或资格检查失败时立即停止补偿并保留事务供重试；
即使该 Provider 已没有当前 Resource 请求，该屏障仍生效。普通补偿不会取得扩展升级授权。
维护只枚举带 `ANAS relational_database resource` 数据库注释且普通角色拥有的受管库，先检查所有来源，
仅接受 `vector 0.8.1 → 0.8.2` 与已处于目标的库；未知来源在任何升级写入前失败。
0.8.2 的上游升级 SQL 不改函数定义，本路径无需 Consumer 专用索引重建；真实旧 HNSW 索引的普通角色
距离查询已在容器验证。后续版本或其他路线必须重新核对索引维护，不扩大该固定脚本的适用范围。

成功后按实际版本复查；部分跨库操作完成后可重试尚未完成的已知路径。失败时保留服务停止和 ANAS 恢复点，
由 Runner 的既有作业错误阻止不安全自动回退。使用 `anas apply --deployment <原候选ID> -w <workspace> -y`
重试时仍验证原完整恢复点及匹配镜像；其他失败候选不能跨越该保护。移除请求不会 DROP 扩展，卸载仍 retain；
即使所有消费者已移除，保留的 PostgreSQL Provider 制品变化仍须维护保护。PG 大版本迁移不支持。

若 Provider 本身已从当前部署移除，再加入 PG Provider 时不能把 retain 数据视为新装空库。
Runner 在任何容器启动或 Resource ensure 前检查既有 Resource 记录及 `PreviousDeployments` 制品；
发现先前 PG、保留 PG 资源，或无法核对的缺失历史时，会要求先恢复包含该 Provider、匹配镜像及完整数据的
ANAS 恢复点。`--allow-risky`、`--no-snapshot` 和更换 Provider 名称不能绕过；没有 PG 历史或资源记录的
首次安装仍可正常进行。这是保守的容器启动保护，不证明任意手工替换数据或其他恢复范围已经过验收。

上游证据：[pgvector 0.8.2 变更](https://github.com/pgvector/pgvector/blob/v0.8.2/CHANGELOG.md)、
[固定升级 SQL](https://github.com/pgvector/pgvector/blob/v0.8.2/sql/vector--0.8.1--0.8.2.sql)。

普通 `start`/`restart` 在 Compose 探测及 restart 停止操作前核对持久保护，保留
`postgres_recovery_required` 或 `data_restore_incomplete` 前置错误，不包装成通用启动失败。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)
- [`container-e2e.sh`](../tests/container-e2e.sh)：隔离网络/卷的真实 PG 新装、重复 ensure、重启、依赖、
  错误/空口令、冒用管理员、跨库、inspect 只读、缺扩展、retain、旧 trust 及 0.8.1 升级/部分重试。
- [`pgvector-0.8.1.fixture`](../tests/pgvector-0.8.1.fixture)：固定源码的旧二进制测试镜像，仅用于测试。

```bash
docker build -t anas-postgres:test modules/postgres/postgres
docker build -t anas-postgres:old-vector -f modules/postgres/tests/pgvector-0.8.1.fixture modules/postgres/tests
bash modules/postgres/tests/container-e2e.sh --local-task-fixture anas-postgres:test anas-postgres:old-vector
```

## 当前限制

普通 `config set` 不能安全轮换数据库超级用户密码。

2026-10-03 的隔离 Docker 验证覆盖原生 Linux arm64。相同基础镜像 digest 的 amd64 镜像已在本机模拟构建成功，并通过新装、重启、认证、扩展及preload漂移基础场景；
原生 amd64 及真实测试服务器的 apply/一致恢复仍需验收。
该结果不证明 Immich、IAM 或任意其他消费者已完成真实主机业务验收。

目标构建前，Runner 为当前 PG workspace 的实际运行镜像建立 workspace/服务专属本地保留引用。Docker 的 containerd 镜像存储可能在构建替换标签后无法按旧 ID 导出；这些固定服务引用随下一次构建更新，不推送、不改动其他部署标签。恢复点仍按不可变 ID 导出无标签 archive，服务停写后才捕获数据。
