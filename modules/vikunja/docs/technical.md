# Vikunja 技术实现

本文面向 Module 维护者，记录 `vikunja` 的实现、安全边界和验证入口。用户操作见
[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2.4.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_vikunja` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-vikunja:2.4.0-r4` | `db, traefik` | 2 |
<!-- generated:compose-topology:end -->

`anas_vikunja` 只在 Traefik network 的 `3456/tcp` 提供 Web/API，没有宿主端口。`db` 是
Runner 解析的 PostgreSQL 或 MariaDB external network。容器使用部署 DNS 访问 IAM 域名，并把
ANAS 内部 CA 只读挂载到 Go system-roots 目录。

镜像从上游 `v2.4.0` commit `907850f` 的源码压缩包构建，并用固定 SHA-256 校验；只应用
`0001-logout-local-session-first.patch`，构建阶段运行补丁自带的两个 Vitest 回归用例。最终仍是
scratch runtime。Docker 首次创建 bind mount 时目录属于 root，因此 Dockerfile 用数值身份
`0:0` 启动静态 `anas-vikunja-entrypoint`：root 阶段只创建并 `lchown`
`/app/vikunja/files` 树，随后设置 groups/gid/uid 为 `1000:1000`、umask 为 `0027`，再 `exec`
补丁后的 Vikunja 二进制。rootfs 为 read-only，只有附件 volume 与 `/tmp` tmpfs 可写。
入口在启动业务进程前通过通用 IAM binding 的 discovery URL 等待 Provider 就绪，最多 60 次、
每次间隔 2 秒；这不包含 LLNG/Authentik 分支，超时后仍失败关闭。

健康检查通过同一入口先降权，先要求主进程的本地 `/api/v1/info` 已返回 `200`，再执行上游
`vikunja healthcheck` 覆盖 API 与数据库连接；这个前置门禁避免健康检查子进程与空库/升级迁移并发。OIDC provider 不可用会在
v2 health status 中表现为 degraded；配置的 `requireavailability=true` 还会让首次初始化失败
关闭并由 Compose restart policy 重试。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `vikunja.db_name` | string | — | `vikunja` | `static` | `VIKUNJA_DB_NAME` | 否 | 否 | 否 | 否：`migrate-vikunja-database` | `data_migrate` | 应用数据库名 |
| `vikunja.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `VIKUNJA_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-vikunja-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `vikunja.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `tasks` | `static` | `VIKUNJA_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `vikunja.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `VIKUNJA_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议；仅支持 OIDC |
| `vikunja.language` | string | — | — | `inherited` | `VIKUNJA_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 新用户默认 UI 语言；已有用户偏好优先 |

Hook 使用 `internal/localization.Match` 把显式值或 `DEFAULT_LANGUAGE` 匹配到固定 2.4.0 的
`SUPPORTED_LOCALES`。无法匹配时返回 `module_localization_fallback` warning 并使用 `en`。
`TZ` 同时投影到 `service.timezone` 和 `defaultsettings.timezone`。

## 身份、授权与会话数据流

calculate 阶段发布 provider-neutral registration：

- client id `vikunja`，confidential client secret；
- redirect URI `<domain>/auth/openid/anas`；
- post-logout URI `<domain>/`；
- scopes `openid,profile,email`；
- claims `name`、`preferred_username`、`email`；
- 可选准入组 `APP_vikunja,APP_all,<administrator group>`。

render 阶段只读取 `ANAS_IAM_BINDING__VIKUNJA__OIDC_ISSUER_URL` 和对应 discovery URL，写入
上游 provider key `anas`。它不读取 IAM 实现名或其他 consumer binding。上游通过 discovery
获得 authorization、token、JWKS 与 `end_session_endpoint`；`requireavailability=true` 在首次
初始化时失败关闭。`auth.local.enabled=false` 和 `service.enableregistration=false` 关闭本地密码
与开放注册。

首次登录按 `(issuer, sub)` JIT 建号。IAM 应提供 email、name、`preferred_username`，应用内
team 与权限继续由 Vikunja 数据库拥有；没有 LDAPS、Group 同步或密码回写。

### 登出边界

2.4.0 在 Vikunja session 中保存 provider key 和原始 ID Token。r4 前端补丁先捕获 bearer token
和 provider key，立即执行 `removeToken()`、清空 `localStorage`、重置 Pinia authenticated/user/
session 状态并写入 `justLoggedOut`；这些操作发生在任何 awaited HTTP 请求之前。随后用捕获的
bearer token 调用 `/api/v1/user/logout`，并将请求 timeout 限制为 5 秒。后端读出 ID Token，构造
含 `id_token_hint`、`client_id`、`post_logout_redirect_uri` 的 RP-Initiated Logout URL，再删除并
提交服务端 session。URL 解析失败只记录分类错误，不阻塞服务端删除；缓存的
`end_session_endpoint` 避免 IAM 故障时重新 discovery。HTTP 失败或超时只会跳过服务端/IAM
注销，不会恢复已经删除的浏览器本地状态。

该版本没有接收 OIDC Logout Token 或 front-channel iframe 的标准 endpoint。Hook 因此省略
`OIDC_LOGOUT_URI/METHODS/SESSION_REQUIRED`；IAM 主动退出和管理员无浏览器撤销不能清除已存在的
Vikunja session。真实浏览器尚需验证 `state`、旧 Cookie、IAM Cookie 与重试边界，当前不声明
双向登出。

## 管理面与 Secret 生命周期

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `VIKUNJA_DOMAIN_FULL` | `iam` |

没有 `management.local_accounts` 或真实 direct-login recovery path。IAM 故障必须恢复 IAM、
目录、DNS 或内部 CA。普通 `/login` 页面在 `auth.local.enabled=false` 时不提供本地密码入口；
上游 CLI 虽能创建用户，但 Module 没有预置、托管或轮换应急用户。

### Secret 边界

- `VIKUNJA_SERVICE_SECRET`：Vikunja JWT/加密操作的 32-byte random hex seed；
- `VIKUNJA_OIDC_CLIENT_SECRET`：通用 OIDC registration 与应用配置共享的 32-byte random hex；
- `VIKUNJA_DB_PASSWORD`：`primary_database` Resource 的稳定生成凭据。

三个值只持久保存在 workspace `.anas/secrets.yml`（`0600`），并只进入已授权的渲染制品：
service/数据库凭据进入 Vikunja，OIDC client secret 还进入所选 IAM Provider 的 client registration。
deployment manifest、README、普通配置输出与 Hook 错误不得包含值。API token 与 webhook Secret
由应用用户拥有，不由 Module 生成或导出。

前两个值分别声明为 `vikunja.service_secret` 和 `vikunja.oidc_client_secret`，使用 32-byte hex
generator 以及显式 `credential_probe/reconcile/verify` phases。deployment 在首次渲染时冻结所有
已经通过 env scope 获权且携带相同高熵值的投影；因此 OIDC secret 的 Vikunja 配置、
`ANAS_IAM_CLIENT__VIKUNJA__CLIENT_SECRET` 和 IAM Provider 渲染制品会在同一 candidate 更新。
普通路径停止 previous 后按 IAM Provider→Vikunja 的原依赖顺序启动；Vikunja Hook 通过只读
Docker inspect 验证 candidate 容器环境，发现 missing/mismatch 时 fail closed，由 Runner 补偿恢复。
Secret Store 只在全部验证后一次提交。

## 数据库与存储

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres`, `mariadb` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 发布 `VIKUNJA_DB_*` 与 `VIKUNJA_NETWORK_DB`。Hook 映射 PostgreSQL 为上游 `postgres`、
MariaDB 为上游 `mysql`，并固定当前 Provider network 内部连接的 TLS/sslmode 为 `false/disable`。
附件独立写入 `${DATA_PATH}/vikunja/files`；其他持久状态在数据库。`db_type`/`db_name` 变更是
`data_migrate`，不会由 render 自动搬迁。

## 环境变量所有权

### 导出

- `ANAS_IAM_CLIENT__VIKUNJA__*`
- `APPS_LIST*`

### 显式消费

- `ANAS_IAM_BINDING__VIKUNJA__*`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_APP_FILTER`
- `TRAEFIK_BASE_PORT`

Runner-owned globals（包括 `TZ`、`DEFAULT_LANGUAGE`、`LOCAL_DNS_SERVER` 和 workspace path）按通用
作用域进入 Module；数据库 Resource keys 由 Runner 归 `vikunja` 所有。Hook 不读取 Provider
管理员密码或其他 consumer 的 binding。

## Hook、变更与回滚

- Hook command：`go run ./hook`
- `calculate`：匹配语言、派生域名、验证 binding 选择、稳定生成两个 Secret、登记 OIDC client
  与 launcher metadata。
- `render_env`：验证自身 OIDC binding 和数据库 Resource，映射上游 database/OIDC/localization
  环境变量并关闭本地认证/注册。
- `credential_probe/reconcile/verify`：验证 candidate 容器实际收到 service/OIDC secret；
  reconcile 不接受旧容器原地改写，candidate 投影不一致时直接失败并触发部署回滚。
- `db_name`/`db_type` 只能经过 `migrate-vikunja-database` 生命周期；普通 `config set` 被拒绝。
- `language` reconcile 只改变新用户默认；现有用户 preference 不回写。
- rollback 必须同时考虑数据库 migration、附件和 Secret Store；`upgrade.data_breaking: []` 不替代
  前一 minor/patch 的真实升级回滚 E2E。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)：数据库映射、OIDC registration/binding、稳定 Secret、
  credential candidate probe、应用组、登出字段省略、语言 fallback 和失败关闭。
- [`entrypoint.go`](../vikunja/entrypoint.go)：附件权限初始化与不可逆降权。
- [`0001-logout-local-session-first.patch`](../vikunja/patches/0001-logout-local-session-first.patch)：
  本地优先登出实现和两个上游前端回归测试。
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)
- [Vikunja Module 集成要求](../../../dev-docs/requirements/vikunja-module.md)

提升到 `release` 前还必须完成 PostgreSQL/MariaDB、amd64/arm64、Authenticator/LLNG 真实浏览器
登录登出、IAM-down、备份恢复、API/webhook 和前一 patch/minor 升级回滚 E2E。

## 当前限制

SMTP、S3、Redis、搜索、Pro、bot user 和 AI/MCP sidecar 不在首期自动配置范围。上游移动应用
仍处于早期阶段。应用没有本地恢复账号，也不接收 IAM 主动会话撤销。
