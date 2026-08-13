DDNS Updater
=====

把这个部署的 A/AAAA 记录保持最新。相对 `ddns_go` 的取舍：**上游支持的厂商总数多出
一倍左右**；代价是没有本地网卡模式、中国厂商覆盖较弱、发布节奏较慢。

它并不过时：`qdm12/ddns-updater` 2.10.0 就是当前最新版。

能力设计与两个实现的对比见
[动态 DNS 能力设计](../../../docs/design/dynamic-dns-capability-design.md)。

配置
----------------

### 依赖的模块

- `traefik`
- `forward_auth` 能力（当前由 `oauth2_proxy` 提供）——**硬依赖**。ddns-updater
  **完全没有自己的用户系统**，这个中间件是界面与互联网之间唯一的东西。

### 最简用法

不需要在 `modules` 里列出本 module，声明能力即可：

```yaml
dynamic_dns:
  provider: ddns_updater      # 或 auto
  dns_provider: cloudflare

secrets:
  cloudflare_dns_api_token: ...
```

### 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `dns_provider` | 无（必填） | DNS 厂商。优先于 `dynamic_dns.dns_provider` |
| `domain_prefix` | `ddns` | Web 界面的域名前缀 |
| `publicip_fetchers` | `http` | `http`、`dns` 或 `all`，可逗号分隔 |
| `publicip_providers` | `all` | 不分地址族的 echo 服务列表 |
| `publicip_ipv4_providers` | `all` | 仅 IPv4 |
| `publicip_ipv6_providers` | `all` | 仅 IPv6 |
| `publicip_dns_providers` | `all` | `dns` fetcher 用的解析器 |

支持的厂商见 `modules/ddns_updater/hook/dns_registry_gen.go`。注意 **`dnspod`
不在其中**：旧版 DNSPod token 无法与 lego 共享凭据，而 ddns_go 已覆盖仍持有旧
token 的场景，在这里再开一条通往同一厂商的路只会多一个不该被选的选项。腾讯云账号
请用 `ddns_go` + `tencentcloud`。

### 地址发现

**没有读本地网卡的模式**，这是和 ddns_go 的结构性差异：

| | ddns_go | ddns_updater |
|---|---|---|
| 问外部 HTTP 服务 | `gettype: url` | `http` fetcher |
| 问 DNS 解析器 | 无 | `dns` fetcher |
| 读本地网卡 | `gettype: netInterface` | **不支持** |

即使宿主的公网地址就挂在自己网卡上，也只能从外部问回来。

provider 列表默认 `all`，即上游自己的默认：**每次请求轮换所有 echo 服务**。
上游 README 明说这是为了避免请求过多被某一家限流，所以缩小这个列表要有具体理由
（某个服务从这台宿主访问不通或很慢），而且**只填一个等于放弃这层保护**。

条目可以是 provider 名，也可以是显式端点：

```yaml
services:
  ddns_updater:
    env:
      publicip_ipv4_providers: "url:https://myip.ipip.net,ipify"
```

可用的 provider 名见
[上游文档](https://github.com/qdm12/ddns-updater#public-ip)：`ipify`、`ifconfig`、
`ipinfo`、`spdyn`、`ipleak`、`icanhazip`、`ident`、`nnev`、`wtfismyip`、`seeip`、
`changeip`。这份名单**不在 hook 里校验**——把它抄进来就多了一处要跟上游同步的
地方，而名字写错 ddns-updater 自己会报。只有 `publicip_fetchers` 这个小的封闭集合
会在 `plan` 阶段校验。

`publicip_fetchers` 默认 `http` 而不是上游的 `all`：`dns` fetcher 要走 UDP 53 出站，
而这个部署里有 Samba DC 自己的 DNS，混在一起容易出意外。这是有理由的收窄。

### IPv6

`core` 导出的 `HOST_HAS_IPV6` 决定是否启用 AAAA。宿主没有全局 IPv6 时自动降级为
只更新 A，结果记在 `DDNS_UPDATER_IPV6_AVAILABLE` 里。

在 bridge 网络上运行，所以它拿到的是**容器出口**看到的公网地址。对 IPv4 而言这和
宿主一致；IPv6 需要 Docker 网络本身启用 IPv6 才有意义——需要宿主 IPv6 时用
`ddns_go`。

### Web 界面

通过 Docker label 挂上 forward_auth 中间件，只放行 `Admins` 组。

在此之前这个 module 的 router 上**一个中间件都没有**，`SERVER_ENABLED=yes` 的界面
是完全裸奔的——任何人拿到域名就能改这个部署的全部 DNS 记录。

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
