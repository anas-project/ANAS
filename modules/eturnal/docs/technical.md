# Eturnal TURN 技术实现

本文面向 Module 维护者，记录 `eturnal` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `1.12.2-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_eturnal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-eturnal:1.12.2-r5` | `` | 0 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `eturnal.domain_prefix` | string | — | `turn` | `static` | `TURN_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `eturnal.port` | int | `1..65535` | `3478` | `static` | `TURN_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | 服务端口 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

协议服务没有人员用户、目录同步、OIDC、SAML 或 Group 管理。Consumer 使用生成的 TURN shared secret。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

没有 Web 管理员入口或本地管理员账号。

本 Module 没有声明由 `anas admin local` 管理的人员账号。`eturnal.secret` 由 deployment credential
ready barrier 管理，并通过 `anas credential list/rotate` 暴露无明文库存、dry-run 与主动轮换。

### Secret 边界

- `TURN_SECRET`

`module.yml` 将 `eturnal.secret` 声明为 `shared_secret/reconcile` provider，使用 16-byte hex
生成策略和 `TURN_SECRET` 投影；Nextcloud 与 NetBird 是显式 consumer。Materialization 根据 Secret
Store provenance 冻结 `anas`/`external` authority 和 generation，不把值、hash 或 verifier 写入
`deployment.yml`。

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

—

### 显式消费

—

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- 精确 phases：`calculate`、`render_env`、`services`、`after_start`、`credential_probe`、
  `credential_reconcile`、`credential_verify`。
- Probe 通过 `docker exec -i` 从 stdin 读取期望值，比较运行容器使用的 `eturnal.yml`；容器或配置
  路径不可用返回 `unavailable`，不得误判为 mismatch。ANAS authority 的 mismatch/missing 才会重启
  容器并重新 probe；external authority 只允许 probe/verify。
- Candidate 与 previous 各自携带 `TURN_SECRET` 投影。Candidate 失败时先停止 candidate，再启动
  previous，由同一幂等屏障把无状态 Eturnal 配置恢复为 previous 的期望值；Store 不在验证前提交。
- 主动轮换使用无明文 journal 记录 ID、代次和 phase。Store 提交前的中断恢复 previous；Store
  已提交但 promotion 未完成时，排他运行时操作依据 `rotation_id/generation` 完成 candidate promotion。
- 成功轮换后，普通 rollback 不能只切回旧制品：previous generation 与 Store 不同会返回
  `credential_store_mismatch`，当前需恢复匹配 snapshot。
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

TURN Secret 是机器凭据，不应作为人员密码查询或共享。当前 verify 校验实际运行配置与容器可用性，
尚未执行完整 TURN 鉴权握手；resource credential、Samba 和本地管理员也尚未迁入这一统一执行器。
