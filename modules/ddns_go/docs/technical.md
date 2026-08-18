# DDNS-GO 技术实现

本文面向 Module 维护者，记录 `ddns_go` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `6.17.4-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ddns-go` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-ddns-go:6.17.4-r5` | `` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ddns_go.dns_provider` | string | — | — | — | `DDNS_GO_DNS_PROVIDER` | 否 | 是 | 否 | 是 | `container_recreate` | DNS 厂商 |
| `ddns_go.domain_prefix` | string | — | `ddns-go` | `static` | `DDNS_GO_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `ddns_go.interval` | int | — | `300` | `static` | `DDNS_GO_INTERVAL` | 否 | 否 | 否 | 是 | `container_recreate` | 执行间隔（秒） |
| `ddns_go.ipv4_gettype` | enum (`url`, `netInterface`) | — | `url` | `static` | `DDNS_GO_IPV4_GETTYPE` | 否 | 否 | 否 | 是 | `container_recreate` | IPv4 地址发现方式 |
| `ddns_go.ipv4_interface` | string | — | `""` | `static` | `DDNS_GO_IPV4_INTERFACE` | 否 | 否 | 否 | 是 | `container_recreate` | IPv4 本地网卡 |
| `ddns_go.ipv4_urls` | string | — | `""` | `static` | `DDNS_GO_IPV4_URLS` | 否 | 否 | 否 | 是 | `container_recreate` | IPv4 外部探测地址 |
| `ddns_go.ipv6_gettype` | enum (`url`, `netInterface`) | — | `url` | `static` | `DDNS_GO_IPV6_GETTYPE` | 否 | 否 | 否 | 是 | `container_recreate` | IPv6 地址发现方式 |
| `ddns_go.ipv6_interface` | string | — | `""` | `static` | `DDNS_GO_IPV6_INTERFACE` | 否 | 否 | 否 | 是 | `container_recreate` | IPv6 本地网卡 |
| `ddns_go.ipv6_urls` | string | — | `""` | `static` | `DDNS_GO_IPV6_URLS` | 否 | 否 | 否 | 是 | `container_recreate` | IPv6 外部探测地址 |
| `ddns_go.web_enabled` | bool | — | `true` | `static` | `DDNS_GO_WEB_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Web 界面 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

不接入目录或 IAM，没有用户同步和 Group 授权。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

Web 界面使用 `primary` 本地管理员，默认用户名为 `admin_ddns_go`。该账号由 ANAS 生成、查询和事务轮换。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `DDNS_GO_DOMAIN_FULL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `primary` | `primary` | `admin_ddns_go` | `bcrypt` | 是 |

```bash
anas admin local list -w /srv/anas
anas admin local credential ddns_go primary -w /srv/anas
anas admin local rotate ddns_go primary -w /srv/anas
anas admin local rotate ddns_go primary --prompt -w /srv/anas
```

`credential` 会输出明文密码，应避免进入日志；`rotate` 默认生成随机密码，`--prompt` 从终端安全读取，不接受 argv 或普通环境变量传入密码。

### Secret 边界

- `ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `ANAS_TRAEFIK_ROUTE__*`

### 显式消费

—

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

## 当前限制

本地账号是域名入口和 host 端口直连入口的实际安全边界。
