# Traefik reverse proxy

为所有 Web 服务提供 HTTPS 反向代理、路由和 Dashboard。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `traefik` |
| 版本 / revision | `3.7.10-r2` |
| 状态 | `release` |
| 类别 | `network` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## 最简配置

```yaml
modules:
  traefik: {}
```

## 身份、用户与 Group

Dashboard 不使用目录或 IAM；其他应用的 IAM/ForwardAuth 由各自路由声明。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

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

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `traefik.base_port` | int | `9000` | `TRAEFIK_BASE_PORT` | 否 | 否 | 是 | `container_recreate` | 对外端口基数 |
| `traefik.domain_prefix` | string | `traefik` | `TRAEFIK_DOMAIN_PREFIX` | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |

### 查询和修改

```bash
anas config list traefik -w /srv/anas
anas config explain traefik.base_port
anas config set traefik.base_port 9000 -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 声明式路由

Docker provider 只能发现与 Traefik 同网络的容器。host 网络容器、Docker 外进程和只能按地址访问的服务通过以下环境契约注册路由：

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE          必填
ANAS_TRAEFIK_ROUTE__<NAME>__URL           必填
ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   可选，逗号分隔
ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   可选，默认 https
ANAS_TRAEFIK_ROUTE__<NAME>__TLS           可选，默认 true
```

声明方必须在自己的 `config.exports` 中发布 `ANAS_TRAEFIK_ROUTE__*`。`<NAME>` 会规范化为 router 名；值按带引号 YAML scalar 渲染，反斜杠和双引号会转义，换行会被拒绝以阻止 YAML 结构注入。

```yaml
env:
  ANAS_TRAEFIK_ROUTE__DDNS_GO__RULE: "Host(`ddns-go.example.com`)"
  ANAS_TRAEFIK_ROUTE__DDNS_GO__URL: "http://172.18.0.1:9876"
```

是否附加 ForwardAuth 由声明路由的 Module 决定。Traefik Dashboard 的本地管理员只保护 Dashboard，不会自动保护这些上游路由。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list traefik -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

Traefik 本地账号只保护 Dashboard，不代表下游应用管理员。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

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
