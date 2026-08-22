# DDNS-GO

带 Web 界面的动态 DNS 更新器，支持宿主 IPv6 和中国 DNS 厂商。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `ddns_go` |
| 版本 / revision | `6.17.4-r6` |
| 状态 | `release` |
| 类别 | `network` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |

## 最简配置

```yaml
modules:
  ddns_go: {}
```

## 身份、用户与 Group

不接入目录或 IAM，没有用户同步和 Group 授权。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

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

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

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

### 查询和修改

```bash
anas config list ddns_go -w /srv/anas
anas config explain ddns_go.dns_provider
anas config set ddns_go.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 地址发现、IPv6 与配置合并

`ipv4_gettype`/`ipv6_gettype` 默认使用 `url`，向外部服务查询实际可达地址。需要直接读取宿主网卡时使用大小写严格的 `netInterface`，并显式指定网卡：

```yaml
dynamic_dns:
  provider: ddns_go
  dns_provider: tencentcloud

modules:
  ddns_go:
    config:
      ipv6_gettype: netInterface
      ipv6_interface: enp1s0
```

留空 URL 时使用 Module 内置探测器；`plan` 会拒绝未知 gettype，以及无法解析网卡的 `netInterface` 配置。容器使用 host 网络以读取宿主 IPv6；宿主没有全局 IPv6 时自动降级为只更新 A，并发布 `DDNS_GO_IPV6_AVAILABLE` 供诊断。

`.ddns_go_config.yaml` 同时接受 ANAS 和 Web UI 修改。`anas-ddns-go-reconcile` 对 `anas-managed:<id>` 条目执行以下规则：同名条目整体替换；目标完全相同的手工条目由 ANAS 接管；部分重叠时报错；其他条目、webhook 和未知字段原样保留。支持的 DNS 厂商与凭据键以 `hook/dns_registry_gen.go` 为准。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list ddns_go -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

本地账号是域名入口和 host 端口直连入口的实际安全边界。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`6.17.4-r6`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The service receives TZ through the module .env; upstream does not expose a separate timezone setting.
- Language scope / 语言范围：ddns-go Web UI and logs
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：ddns-go language code
- Fallback / 回退：The persisted application setting defaults to English; users can switch language in the Web UI.
- Supported languages / 支持语言（2）：`en`, `zh-CN`
- Notes / 说明：ddns-go uses zh-cn internally; ANAS documentation exposes the canonical BCP 47 tag zh-CN.

Evidence / 证据：

- [v6.17.4 — static/i18n.js I18N_MAP](https://github.com/jeessy2/ddns-go/blob/v6.17.4/static/i18n.js)
- [v6.17.4 — persisted language selector](https://github.com/jeessy2/ddns-go/blob/v6.17.4/web/set_lang.go)
<!-- generated:localization:end -->
