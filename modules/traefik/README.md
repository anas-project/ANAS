Traefik
=====

`Traefik`是本项目的基础，任何通过TCP(HTTP)访问的服务都会通过Traefik反向代理。

## Administrator access / 管理员入口

Dashboard 未接入 IAM，使用一个托管 BasicAuth 账号。Manifest 账号 ID 是 `primary`；
默认实际用户名由全局 `{module}` 模板生成，为 `admin_traefik`。ANAS 只把 bcrypt
校验值写入当前 file-provider 配置，密码不会进入部署 `.env` 或 Compose label。

The Dashboard has no IAM integration and uses one managed BasicAuth account.
Its Manifest account ID is `primary`; the default physical username is
`admin_traefik`. Only a bcrypt verifier enters active Traefik configuration.

```bash
anas admin local credential traefik -w /srv/anas
anas admin local rotate traefik --prompt -w /srv/anas
```

配置
----------------

### 依赖的模块
无
### 需要的环境变量

- `TRAEFIK_BASE_PORT`: 定义了HTTP服务的对外端口
  - 默认为`9000`
- `LEGO_EMAIL`: 获取证书时，提交给Let's Encrypt的邮件，用于获取证书相关提醒
  - 如果为空，使用`core`的`Email`代替
- `LEGO_DNS_PROVIDER`: DNS提供者，LEGO支持市面上大部分DNS供应商的API，[支持列表](https://go-acme.github.io/lego/dns/)
  - 需要同时设置相应DNS供应商需要的环境变量定义，相应环境变量不会被检查，但是在docker-compose 运行时会出错

声明式路由
----------------

Traefik 的 Docker provider 只能发现与它同网络的容器。使用 host 网络的容器、
Docker 之外的进程、以及任何只能按地址访问的服务都不在其中。这类服务通过环境变量
注册路由，由本 module 的 entrypoint 渲染进 file provider 目录：

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE          必填，Traefik 规则表达式
ANAS_TRAEFIK_ROUTE__<NAME>__URL           必填，上游地址
ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   可选，逗号分隔
ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   可选，默认 https
ANAS_TRAEFIK_ROUTE__<NAME>__TLS           可选，默认 true
```

`<NAME>` 只能包含环境变量允许的字符，会被转成小写并以 `-` 连接作为 router 名。
声明方在自己的 `config.exports` 里放行 `ANAS_TRAEFIK_ROUTE__*`。

示例：让 host 网络上的 ddns-go 通过 Traefik 暴露，并且只允许通过 forwardAuth：

```yaml
ANAS_TRAEFIK_ROUTE__DDNS_GO__RULE: "Host(`ddns-go.example.com`)"
ANAS_TRAEFIK_ROUTE__DDNS_GO__URL: "http://172.18.0.1:9876"
ANAS_TRAEFIK_ROUTE__DDNS_GO__MIDDLEWARES: "forward-auth"
```

值会被渲染成带引号的 YAML 标量，反斜杠和双引号会被转义；含有换行的值会被拒绝，
因为换行是唯一能结束标量并注入 YAML 结构的字符。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`3.7.10-r2`（reviewed 2026-08-13）
- Timezone / 时区：`container` — Traefik receives TZ for process and access-log timestamps.
- Language scope / 语言范围：Traefik Proxy built-in Dashboard
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：The built-in Dashboard is English and exposes no supported language selector.
- Supported languages / 支持语言（1）：`en`

Evidence / 证据：

- [v3.7.10 — Dashboard and static configuration expose no localization setting](https://github.com/traefik/traefik/tree/v3.7.10)
<!-- generated:localization:end -->
