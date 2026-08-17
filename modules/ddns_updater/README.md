# DDNS Updater

更新基础域名与通配符记录的动态 DNS 服务。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `ddns_updater` |
| 版本 / revision | `2.10.0-r1` |
| 状态 | `release` |
| 类别 | `network` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `forward_auth` | Capability | `http` |

## 最简配置

```yaml
modules:
  ddns_updater: {}
```

## 身份、用户与 Group

应用没有用户数据库。Web 请求经 `forward_auth` 交给 OAuth2 Proxy，再通过所选 IAM 的 OIDC 登录；默认只允许管理员组。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | indirect: forward_auth/http → OIDC |
| Group | `Admins` (forward_auth) |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有私有管理员或本地恢复账号。IAM/ForwardAuth 故障时必须先恢复该链路。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ddns_updater.dns_provider` | string | `—` | `DDNS_UPDATER_DNS_PROVIDER` | 是 | 否 | 是 | `container_recreate` | DNS 厂商 |
| `ddns_updater.domain_prefix` | string | `ddns` | `DDNS_UPDATER_DOMAIN_PREFIX` | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `ddns_updater.forward_auth_interface` | string | `auto` | `DDNS_UPDATER_FORWARD_AUTH_INTERFACE` | 否 | 否 | 是 | `container_recreate` | ForwardAuth 接口选择 |
| `ddns_updater.publicip_dns_providers` | string | `all` | `DDNS_UPDATER_PUBLICIP_DNS_PROVIDERS` | 否 | 否 | 是 | `container_recreate` | DNS 地址探测器列表 |
| `ddns_updater.publicip_fetchers` | string | `http` | `DDNS_UPDATER_PUBLICIP_FETCHERS` | 否 | 否 | 是 | `container_recreate` | 公网地址发现方法 |
| `ddns_updater.publicip_ipv4_providers` | string | `all` | `DDNS_UPDATER_PUBLICIP_IPV4_PROVIDERS` | 否 | 否 | 是 | `container_recreate` | IPv4 探测服务列表 |
| `ddns_updater.publicip_ipv6_providers` | string | `all` | `DDNS_UPDATER_PUBLICIP_IPV6_PROVIDERS` | 否 | 否 | 是 | `container_recreate` | IPv6 探测服务列表 |
| `ddns_updater.publicip_providers` | string | `all` | `DDNS_UPDATER_PUBLICIP_PROVIDERS` | 否 | 否 | 是 | `container_recreate` | 通用地址探测服务列表 |
| `ddns_updater.ttl` | string | `300` | `DDNS_UPDATER_TTL` | 否 | 否 | 是 | `container_recreate` | DNS 记录 TTL |
| `ddns_updater.zone_identifier` | string | `—` | `DDNS_UPDATER_ZONE_IDENTIFIER` | 否 | 否 | 是 | `container_recreate` | DNS Zone 标识 |

### 查询和修改

```bash
anas config list ddns_updater -w /srv/anas
anas config explain ddns_updater.dns_provider
anas config set ddns_updater.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 地址发现与 IPv6

本 Module 没有读取宿主网卡的模式：`http` 向外部 echo 服务查询，`dns` 向解析器查询，`all` 轮换两者。provider 列表默认 `all`，缩小列表只应用于某个服务不可达或过慢的情况；只配置一个 provider 会失去轮换带来的限流保护。

```yaml
dynamic_dns:
  provider: ddns_updater
  dns_provider: cloudflare

modules:
  ddns_updater:
    config:
      publicip_fetchers: http
      publicip_ipv4_providers: "url:https://api.ipify.org,ipify"
```

它运行在 bridge 网络中，因此看到的是容器出口地址。IPv4 通常与宿主一致；IPv6 只有在 Docker 网络具备 IPv6 出站时才有效。需要直接读取宿主 IPv6 时选择 `ddns_go`。宿主没有全局 IPv6 时自动降级为只更新 A，并发布 `DDNS_UPDATER_IPV6_AVAILABLE`。

支持的 DNS 厂商以 `hook/dns_registry_gen.go` 为准。`dnspod` 不在此实现中；旧 DNSPod token 与腾讯云 API 密钥不能互换。Web 路由必须始终挂载 ForwardAuth，不能通过暴露上游端口绕过。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list ddns_updater -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

不能通过直连绕过 ForwardAuth；暴露未受保护的上游端口会失去全部访问控制。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.10.0-r1`（reviewed 2026-08-13）
- Timezone / 时区：`application` — Upstream officially accepts the IANA TZ environment variable for Web UI and log timestamps.
- Language scope / 语言范围：DDNS Updater Web UI
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：English is the only UI language in the fixed source version.
- Supported languages / 支持语言（1）：`en`

Evidence / 证据：

- [v2.10.0 — source tree contains no locale or translation resources](https://github.com/qdm12/ddns-updater/tree/v2.10.0)
<!-- generated:localization:end -->
