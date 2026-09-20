# NetBird 技术实现

本文面向 Module 维护者，记录 `netbird` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `0.76.1-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_dashboard` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-dashboard:2.90.9` | `traefik` | 0 |
| `anas_management` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-netbird-management:0.76.1-r5` | `traefik` | 2 |
| `anas_relay` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-relay:0.76.1` | `traefik` | 0 |
| `anas_signal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-signal:0.76.1` | `traefik` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `netbird.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

声明为 OIDC Consumer，并需要应用 Group，但管理员角色映射仍是发布阻塞项。

### 登出边界

固定 Dashboard `2.90.9` 的 RP logout 由 discovery endpoint 驱动。统一浏览器矩阵必须验证 `state`、Dashboard 本地 session 先失效、IAM 中央 session 结束且不能静默恢复；通过前只记录“上游支持、待接入”。该版本没有标准 front/back-channel receiver，故不声明 IAM→NetBird。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。

- **身份存在哪张表/哪个字段**：NetBird management 自己的数据存储，用户 id 即 ID Token 的 `sub`。
  Module 不持有目录副本，也没有映射表。
- **匹配键怎么配出来的**：`hook/main.go` 渲染 `NETBIRD_AUTH_USER_ID_CLAIM = "sub"`，
  `management.json.envsubst` 把它填进 `AuthUserIDClaim`。**这个字段是可配置的**——上游接受任意
  claim 名，这是 NetBird 与 Forgejo、Vikunja 的关键差别：它没有 `DIRKEY-R-004` 意义上的
  "缺可配置身份字段"缺口。
- **每次登录刷新什么**：未经复核。Module 注册的 claim 是 `name`、`cn`、`sAMAccountName`、`email`，
  上游是否用它们覆盖已有用户行尚未在固定版本上验证。
- **撤权经哪个接口**：只有 Provider 侧的准入判定。固定 Dashboard `2.90.9` 没有 IAM→Module 通知
  endpoint；peer 与 setup key 由 NetBird management API 管理，只能经管理界面或 API 显式撤销。
- **对账或事件订阅路径**：没有。Module 不保有目录副本，不落入目录事件订阅要求的范围。
- **没有自动路径的地方，技术阻碍是什么**：**缺 receiver**，不是缺不可变 ID。上游没有 OIDC
  back-channel logout endpoint，也没有"按目录状态批量禁用 peer"的接口。peer 凭据是 NetBird 自己
  签发的长期凭据，设计上就不经过交互登录，因此任何"下次登录时收敛"的机制对它们都不成立。

**`DIRKEY-R-013` 投影结论：存在投影，需在 M2 切换前复核（`推断`）。** NetBird 把 `sub` 直接当作
用户 id，而 management API 的用户资源路径形如 `/api/users/{userId}`，Dashboard 的用户管理视图也按
这个 id 定位用户。因此主体标识符切成 anchor 之后，**UUID 会出现在管理 API 的 URL 路径与管理员
视图里**。两者是否属于 `DIRKEY-R-010` 禁止的"URL 中的标识符"，取决于该路径是管理视图还是普通
用户可见的界面——管理视图是允许的，普通用户可见的 URL 不是。

这一点未经复核，**必须在 M2 切换前验证**，可行的两条出路都已具备条件：

1. 维持 `AuthUserIDClaim = "sub"`，验证 UUID 只出现在管理 API 与管理员视图；
2. 把 `AuthUserIDClaim` 指向一个与 anchor 无关的稳定 claim，让 anchor 只经普通 claim 到达。

第 1 条是首选：它让 NetBird 的用户 id 直接成为可与目录对账的值，正是 `DIRKEY-R-008` 想要的效果。

## 管理面与 Secret 生命周期

没有受支持的私有恢复管理员。IAM 故障时没有文档化的绕过入口。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET`
- `NETBIRD_DATASTORE_ENC_KEY`
- `NETBIRD_RELAY_AUTH_SECRET`
- `TURN_SECRET`

`credentials.consumes` 将 `TURN_SECRET` 显式绑定到 `eturnal.secret`。该声明冻结到 deployment，
并产生 Eturnal→NetBird 激活 edge；NetBird 只消费 candidate/previous 各自的投影，不拥有该凭据，
也不实现它的 reconcile handler。

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `ANAS_IAM_CLIENT__NETBIRD__*`
- `APPS_LIST*`

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `NETBIRD_SIGNAL_PORT`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_APP_FILTER`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_HOSTNAME`
- `TURN_DOMAIN_PORT`
- `ANAS_IAM_BINDING__NETBIRD__*`
- `TURN_SECRET`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- Eturnal 的 credential ready barrier 在本 Module 启动前完成；owner verify 失败时不会启动 NetBird。
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

Management 容器启动时解析 Traefik 地址，把精确 `/32` 与显式上游代理列表写入 `ReverseProxy.TrustedHTTPProxies`。解析失败会阻止启动，不使用 `TrustedHTTPProxiesCount` 的位置推断，也不信任整个 Docker 私网。Dashboard、Signal 和 Relay 不消费访问 IP，以 Traefik JSON 访问日志为边界记录。

## 当前限制

状态为 `developing`，不属于推荐部署。
