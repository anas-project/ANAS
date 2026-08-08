DDNS-GO
=====

把这个部署的 A/AAAA 记录保持最新。相对 `ddns_updater` 的取舍：**中国厂商覆盖更好、
能读本地网卡拿宿主 IPv6、有 Web 界面**；代价是上游支持的厂商总数少一些。

能力设计与两个实现的对比见
[动态 DNS 能力设计](../../../docs/dynamic-dns-capability-design.md)。

配置
----------------

### 依赖的模块

- `traefik`
- `forward_auth` 能力（当前由 `oauth2_proxy` 提供）——**硬依赖**。Web 界面能改写这个
  部署拥有的全部 DNS 记录，而 ddns-go 自己的登录关不掉也不足以单独承担认证。

### 最简用法

不需要在 `modules` 里列出本 cask，声明能力即可：

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
| `web_port` | `9876` | 监听端口，只绑共享网络网关地址 |
| `ipv4_gettype` / `ipv6_gettype` | `url` | `url` 或 `netInterface` |
| `ipv4_urls` / `ipv6_urls` | 见下 | 逗号分隔的探测地址 |
| `ipv4_interface` / `ipv6_interface` | 见下 | `netInterface` 时读哪块网卡 |

支持的厂商见 `casks/mods/ddns_go/hook/dns_registry_gen.go`，配置错误时报错信息里
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
[声明式路由契约](../traefik/README.md)注册。界面绑定的是**共享网络的网关地址**
（例如 `172.18.0.1`）而不是 `0.0.0.0`：后者在 host 网络下会让界面对整个 LAN 可达，
绕过唯一在认证它的网关。

登录用的是管理员密码（`global.default_service_root_password`），改管理员密码会在
下次 render 时同步过去。这一层不是主要认证手段——前面的 forward_auth 网关才是，
它只放行 `Admins` 组。ddns-go 的登录无法关闭（详见能力设计文档 §6.1），所以由 ANAS
托管而不是留空，留空反而会让首个访问者选定账号。

### 配置合并

`.ddns_go_config.yaml` 有两个作者：ANAS 和 Web 界面。容器启动时由
`anas-ddns-go-reconcile` 合并，ANAS 拥有的条目名为 `anas-managed:<id>`：

- 同名条目整条替换
- 记录目标完全相同的手工条目被**接管**，不会重复添加
- 记录目标部分重叠时**报错**，不猜谁该拥有
- 其他条目、webhook、语言、以及本程序不认识的字段**原样保留**

所以可以放心在界面上加自己的记录，重新部署不会被删。
