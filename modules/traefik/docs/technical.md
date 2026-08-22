# Traefik reverse proxy 技术实现

本文面向 Module 维护者，记录 `traefik` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `3.7.10-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_traefik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-traefik:3.7.10-r5` | `` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `traefik.base_port` | int | `1..65535` | `9000` | `static` | `TRAEFIK_BASE_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | 对外端口基数 |
| `traefik.domain_prefix` | string | — | `traefik` | `static` | `TRAEFIK_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `traefik.forwarded_headers_trusted_ips` | string | — | `""` | `static` | `TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS` | 否 | 否 | 否 | 是 | `container_recreate` | 可向 Traefik 提供转发客户端 Header 的上游代理 IP/CIDR，逗号分隔 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

Dashboard 不使用目录或 IAM；其他应用的 IAM/ForwardAuth 由各自路由声明。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

Dashboard 使用 `primary` 本地管理员，默认用户名 `admin_traefik`，密码以 bcrypt 形式投影到运行配置，可由 ANAS 查询和事务轮换。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `dashboard` | `TRAEFIK_DASHBOARD_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `primary` | `primary` | `admin_traefik` | `bcrypt` | 是 |

```bash
anas admin local list -w /srv/anas
anas admin local credential traefik primary -w /srv/anas
anas admin local rotate traefik primary -w /srv/anas
anas admin local rotate traefik primary --prompt -w /srv/anas
```

`credential` 会输出明文密码，应避免进入日志；`rotate` 默认生成随机密码，`--prompt` 从终端安全读取，不接受 argv 或普通环境变量传入密码。

### Secret 边界

- `ANAS_LOCAL_ADMIN__TRAEFIK__PRIMARY__PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

—

### 显式消费

- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_DNS_PROVIDER`
- `LEGO_EMAIL`
- `LEGO_KEY_NAME`
- `ANAS_TRAEFIK_ROUTE__*`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP 边界

HTTPS entrypoint 默认不信任请求自带的 `X-Forwarded-*`。只有 `traefik.forwarded_headers_trusted_ips` 中通过 IP/CIDR 校验的上游代理可以提供转发链，且不会启用 `forwardedHeaders.insecure`。Traefik 以 JSON 向标准输出记录访问日志，保留客户端地址证据并丢弃 Header 与查询参数，避免日志收集敏感值。

所有 Docker provider 路由必须声明 `anas.client-ip.mode`：`application` 表示后端自身按精确代理清单还原地址，`edge` 表示该组件不消费客户端 IP、以 Traefik 访问日志为准。仓库测试会拒绝漏标路由、全 RFC1918 信任范围和 insecure forwarded headers。

| 处理方式 | Module / 入口 |
| --- | --- |
| 应用还原 | Authentik、LAM、LLNG、MeshCentral Web、NetBird Management、Nextcloud 主站、OAuth2 Proxy |
| 边缘记录 | Traefik Dashboard、Collabora、DDNS Updater、DDNS Go 动态路由、MariaDB/Postgres Adminer、NetBird Dashboard/Signal/Relay、Nextcloud Push/Talk |
| 非 HTTP / 不适用 | Eturnal、FreeRADIUS、Lego、MariaDB/Postgres 数据库、MeshCentral MPS、Samba DC、Samba FS |

非 HTTP 服务不读取 `X-Forwarded-For`：它们使用各自协议携带的对端信息或直连 socket 地址，不能套用 HTTP 信任代理配置。

## 当前限制

Traefik 本地账号只保护 Dashboard，不代表下游应用管理员。
