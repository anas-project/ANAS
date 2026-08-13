Lego
=====

通过 ACME DNS-01 挑战申请并续期通配符证书，供 Traefik 和所有需要 TLS 的服务使用。
虚拟域名下改为签发内部 CA 证书。

配置
----------------

### 依赖的模块
无

### DNS 厂商

DNS 厂商**按引擎选择**，不是部署级设置——证书和动态 DNS 经常不在同一家：

```yaml
services:
  lego:
    env:
      dns_provider: tencentcloud

secrets:
  tencentcloud_secret_id: ...
  tencentcloud_secret_key: ...
```

`dns_provider` **不是无条件必填**：它只服务于 ACME DNS-01 挑战，而
`global.virtual_domain: true` 的部署根本不会发起挑战。所以只有在真的要申请证书时
才要求它，错误信息会同时给出两条出路。

支持的厂商与各自需要的凭据见
`modules/lego/hook/dns_registry_gen.go`，由
[`internal/dns/providers.yml`](../../../internal/dns/providers.yml) 生成。

凭据可以按厂商命名（所有用该厂商的引擎共享），也可以加 module 前缀独占：

```yaml
secrets:
  tencentcloud_secret_id: ...        # lego 和 ddns_go 共用
  lego_namecheap_api_key: ...        # 只有 lego 能读到
```

`anas plan` 会报告解析结果和两个引擎能否共用同一份凭据：

```text
dns platforms:
  ddns_go -> tencentcloud
  lego -> tencentcloud
  ddns_go/lego credentials: shared
```

详见[动态 DNS 能力设计](../../../docs/design/dynamic-dns-capability-design.md)。

> **`dnspod` 已不可用于 lego。** lego v5 删除了该 provider，官方替代是
> `tencentcloud`（`TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY`）。旧版
> DNSPod token 和腾讯云 API 密钥是两种不同的凭据对象，不能互相转换，所以 ANAS
> 也不做自动迁移。

### 其他参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `dns_server` | `223.5.5.5` | DNS-01 挑战校验用的解析器 |
| `email` | 取 `core` 的 `email` | 提交给 CA，用于续期提醒 |

### 凭据如何到达容器

凭据在 ANAS 里带 module 前缀存放（`LEGO_TENCENTCLOUD_SECRET_ID`），这样同一厂商下
另一个引擎用不同账号时互不可见。lego 本身读的是无前缀的厂商变量名，翻译发生在
`cert.sh` 里——容器内唯一需要它们的进程，`.env` 中始终只有带前缀的名字。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`5.3.1-r2`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The certificate worker receives TZ for process and log timestamps.
- Language scope / 语言范围：certificate automation CLI
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing language exists.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [v5.3.1 — CLI without localized UI resources](https://github.com/go-acme/lego/tree/v5.3.1)
<!-- generated:localization:end -->
