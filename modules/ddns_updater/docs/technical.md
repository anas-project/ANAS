# DDNS Updater 技术实现

本文面向 Module 维护者，记录 `ddns_updater` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2.10.0-r3` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `forward_auth` | Capability | `http` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ddns-updater` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ddns-updater:2.10.0` | `` | 0 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ddns_updater.dns_provider` | string | — | — | — | `DDNS_UPDATER_DNS_PROVIDER` | 否 | 是 | 否 | 是 | `container_recreate` | DNS 厂商 |
| `ddns_updater.domain_prefix` | string | — | `ddns` | `static` | `DDNS_UPDATER_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `ddns_updater.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `DDNS_UPDATER_FORWARD_AUTH_INTERFACE` | 否 | 否 | 否 | 是 | `container_recreate` | ForwardAuth 接口选择 |
| `ddns_updater.publicip_dns_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_DNS_PROVIDERS` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 地址探测器列表 |
| `ddns_updater.publicip_fetchers` | string | — | `http` | `static` | `DDNS_UPDATER_PUBLICIP_FETCHERS` | 否 | 否 | 否 | 是 | `container_recreate` | 公网地址发现方法 |
| `ddns_updater.publicip_ipv4_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_IPV4_PROVIDERS` | 否 | 否 | 否 | 是 | `container_recreate` | IPv4 探测服务列表 |
| `ddns_updater.publicip_ipv6_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_IPV6_PROVIDERS` | 否 | 否 | 否 | 是 | `container_recreate` | IPv6 探测服务列表 |
| `ddns_updater.publicip_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_PROVIDERS` | 否 | 否 | 否 | 是 | `container_recreate` | 通用地址探测服务列表 |
| `ddns_updater.ttl` | int | — | `300` | `static` | `DDNS_UPDATER_TTL` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 记录 TTL |
| `ddns_updater.zone_identifier` | string | — | — | — | `DDNS_UPDATER_ZONE_IDENTIFIER` | 否 | 否 | 否 | 是 | `container_recreate` | DNS Zone 标识 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

应用没有用户数据库。Web 请求经 `forward_auth` 交给 OAuth2 Proxy，再通过所选 IAM 的 OIDC 登录；默认只允许管理员组。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | indirect: forward_auth/http → OIDC |
| Group | `Admins` (forward_auth) |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理面与 Secret 生命周期

没有私有管理员或本地恢复账号。IAM/ForwardAuth 故障时必须先恢复该链路。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

Manifest 没有声明跨 Module 密码/Secret 消费或托管本地管理员。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

—

### 显式消费

- `ANAS_FORWARD_AUTH_MIDDLEWARE`

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

不能通过直连绕过 ForwardAuth；暴露未受保护的上游端口会失去全部访问控制。
