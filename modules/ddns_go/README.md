DDNS-GO
=====

把这个部署的 A/AAAA 记录保持最新。相对 `ddns_updater` 的取舍：**中国厂商覆盖更好、
能读本地网卡拿宿主 IPv6、有 Web 界面**；代价是上游支持的厂商总数少一些。

能力设计与两个实现的对比见
[动态 DNS 能力设计](../../../docs/design/dynamic-dns-capability-design.md)。

配置
----------------

### 依赖的模块

- `traefik`

ddns-go 不依赖 IAM 或 `forward_auth`。Web 界面始终使用它自己的本地账号登录。

### 最简用法

不需要在 `modules` 里列出本 module，声明能力即可：

```yaml
dynamic_dns:
  provider: auto          # 或直接写 ddns_go
  dns_provider: tencentcloud

secrets:
  tencentcloud_secret_id: ...
  tencentcloud_secret_key: ...
```

### 参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `dns_provider` | 无（必填） | DNS 厂商。优先于 `dynamic_dns.dns_provider` |
| `domain_prefix` | `ddns-go` | Web 界面的域名前缀 |
| `interval` | `300` | 检查间隔（秒） |
| `web_enabled` | `true` | 关掉则以 `-noweb` 运行，不发布路由 |
| `web_port` | `9876` | host 网络上的监听端口 |
| `ipv4_gettype` / `ipv6_gettype` | `url` | `url` 或 `netInterface` |
| `ipv4_urls` / `ipv6_urls` | 见下 | 逗号分隔的探测地址 |
| `ipv4_interface` / `ipv6_interface` | 见下 | `netInterface` 时读哪块网卡 |

支持的厂商见 `modules/ddns_go/hook/dns_registry_gen.go`，配置错误时报错信息里
也会列出。

### 地址发现

默认 `url`，即问外部服务"你看到的我是哪个地址"。理由是一块网卡上常有同族的多个
地址——IPv6 临时隐私地址与稳定地址并存、ULA 与全局地址并存——只有外部观察者能说清
哪个真正可达。

留空时的默认探测地址：

```text
ipv4_urls  https://myip.ipip.net, https://ddns.oray.com/checkip, https://ip.3322.net
ipv6_urls  https://v6.ident.me, https://api6.ipify.org, https://v6.ip.zxinc.org/getip
```

宿主的公网地址就挂在自己网卡上、或者没有到探测服务的出站路径时，改用
`netInterface`：

```yaml
services:
  ddns_go:
    env:
      ipv6_gettype: netInterface
      ipv6_interface: enp1s0     # 留空则取 core 探测到的网卡
```

两处校验会在 `plan` 阶段就报错：

- **未知的 gettype**（比如大小写写成 `netinterface`）。ddns-go 遇到不认识的值只会
  打印 `get IP method is unknown` 然后返回空串，更新器会一直跑但永远读不到地址。
- **`netInterface` 没有网卡名**。不给名字时 ddns-go 会扫描所有网卡取第一个全局
  地址，在一台挂满 Docker 网桥的宿主上，那不一定是对外提供服务的那个。

### IPv6

`network_mode: host`。这是为了让容器看得见宿主的 IPv6 地址——bridge 容器默认没有
IPv6 出站，既到不了 IPv6 探测服务，也看不到宿主自己的全局地址。

`core` 导出的 `HOST_HAS_IPV6` 决定是否真的启用 AAAA。宿主没有全局 IPv6 时会自动
降级为只更新 A，并把结果记在 `DDNS_GO_IPV6_AVAILABLE` 里，避免静默。

### Web 界面

host 网络的容器 Traefik 的 Docker provider 看不见，所以路由用
[声明式路由契约](../traefik/README.md)注册。该路由不挂 IAM 或 ForwardAuth 中间件；
通过域名和通过宿主端口直连，都由 ddns-go 自带登录保护。

登录使用 ANAS 管理的 Module 私有账号。默认用户名是 `admin_ddns_go`，密码随机生成且
不与任何其他服务共享；用 `anas admin local credential ddns_go` 查询。ddns-go 的
登录无法关闭（详见能力设计文档 §6.1），所以不能把账号留空，否则首个访问者会在
初始化窗口内选定账号。由于 host 网络监听端口也可被直接访问，这个本地账号是实际
安全边界，不是备用凭据。

### 配置合并

`.ddns_go_config.yaml` 有两个作者：ANAS 和 Web 界面。容器启动时由
`anas-ddns-go-reconcile` 合并，ANAS 拥有的条目名为 `anas-managed:<id>`：

- 同名条目整条替换
- 记录目标完全相同的手工条目被**接管**，不会重复添加
- 记录目标部分重叠时**报错**，不猜谁该拥有
- 其他条目、webhook、语言、以及本程序不认识的字段**原样保留**

所以可以放心在界面上加自己的记录，重新部署不会被删。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`6.17.4-r1`（reviewed 2026-08-13）
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
