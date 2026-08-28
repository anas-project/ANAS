# OAuth2 Proxy 技术实现

本文面向 Module 维护者，记录 `oauth2_proxy` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `7.15.3-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `forward_auth` | 提供 Capability | `http` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_oauth2-proxy` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-oauth2-proxy:7.15.3-r4` | `` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `oauth2_proxy.domain_prefix` | string | — | `auth-gate` | `static` | `OAUTH2_PROXY_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `oauth2_proxy.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `OAUTH2_PROXY_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

自身不保存人员用户。它作为 OIDC Consumer 登录所选 IAM，只放行管理员组的成员。

**放行哪些 Group 不是参数。** 这道门后面全是管理界面，所以答案固定为 `platform_admin` 角色，
Hook 把它解析成目录里管理员组的真实名称（`SAMBA_DC_ADMIN_GROUP_NAME`，没有目录 Module 时回落到
契约名 `Admins`）。做成可配置意味着一次修改同时放宽所有受这道门保护的服务——包括 Adminer——而且
没有任何提示。要让某个入口对非管理员开放，应扩展应用目录的 `audience` 词汇，而不是改宽这道门。

### 登出边界

固定 `7.15.3` 的 `/oauth2/sign_out` 只清除 oauth2-proxy 网关 Cookie。IAM Cookie 和后端业务 session 不在其撤销范围。Hook 不发布 `OIDC_LOGOUT_*`，也不设置会在 Cookie 清理前无超时调用 IAM 的 `backend-logout-url`；统一浏览器 E2E 会暂停 IAM，验证本地 Cookie 仍先失效且受保护服务重新要求认证。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | oidc |
| Group | `platform_admin` 角色（派生，不可配置） |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

没有本地管理员或 IAM 故障绕过账号。故障时应恢复 IAM，而不是暴露受保护服务。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `OAUTH2_PROXY_CLIENT_SECRET`
- `OAUTH2_PROXY_COOKIE_SECRET`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `ANAS_FORWARD_AUTH_*`
- `ANAS_IAM_CLIENT__OAUTH2_PROXY__*`

### 显式消费

- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `TRAEFIK_BASE_PORT`
- `ANAS_IAM_BINDING_*`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- 当前没有 Hook 单元测试文件。
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

ANAS 包装镜像在启动时解析 Traefik 地址，为 oauth2-proxy 追加精确的 `--trusted-proxy-ip=/32`，并校验可选上游代理 IP/CIDR。它不再信任三个完整 RFC1918 范围；解析或校验失败时门禁保持关闭，防止伪造转发 Header 影响回跳地址和认证上下文。

## 当前限制

它只负责入口门禁，不负责被保护应用内部的角色授权。
