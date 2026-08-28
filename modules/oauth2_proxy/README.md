# OAuth2 Proxy

为没有登录能力的服务提供 OIDC ForwardAuth 门禁。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `oauth2_proxy` |
| 版本 / revision | `7.15.3-r4` |
| 状态 | `release` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `forward_auth` | 提供 Capability | `http` |

## 最简配置

```yaml
modules:
  oauth2_proxy: {}
```

本 Module 还要求在部署级选择 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

## 身份、用户与 Group

自身不保存人员用户。它作为 OIDC Consumer 登录所选 IAM，只放行管理员组的成员。

**放行哪些 Group 不是参数。** 这道门后面全是管理界面，所以答案固定为 `platform_admin` 角色，
Hook 把它解析成目录里管理员组的真实名称（`SAMBA_DC_ADMIN_GROUP_NAME`，没有目录 Module 时回落到
契约名 `Admins`）。做成可配置意味着一次修改同时放宽所有受这道门保护的服务——包括 Adminer——而且
没有任何提示。要让某个入口对非管理员开放，应扩展应用目录的 `audience` 词汇，而不是改宽这道门。

固定 `7.15.3` 的 `/oauth2/sign_out` 只保证清除网关 Cookie；IAM Cookie 与受保护后端自身 session 是不同状态。Module 不发布 `OIDC_LOGOUT_*`，也不配置会在清 Cookie 前用无超时请求 IAM 的 `backend-logout-url`。因此 IAM 停止时本地登出仍应成功，后续访问重新要求认证，但不宣称结束 IAM 或后端业务 session。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | oidc |
| Group | `platform_admin` 角色（派生，不可配置） |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有本地管理员或 IAM 故障绕过账号。故障时应恢复 IAM，而不是暴露受保护服务。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `oauth2_proxy.domain_prefix` | string | — | `auth-gate` | `static` | `OAUTH2_PROXY_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `oauth2_proxy.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `OAUTH2_PROXY_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |

### 查询和修改

```bash
anas config list oauth2_proxy -w /srv/anas
anas config explain oauth2_proxy.domain_prefix
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list oauth2_proxy -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

它只负责入口门禁，不负责被保护应用内部的角色授权。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`7.15.3-r4`（reviewed 2026-08-13）
- Timezone / 时区：`container` — oauth2-proxy receives TZ for process and log timestamps.
- Language scope / 语言范围：oauth2-proxy built-in error and sign-in pages
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：Built-in pages are English; protected applications manage their own language.
- Supported languages / 支持语言（1）：`en`

Evidence / 证据：

- [v7.15.3 — built-in page templates without locale resources](https://github.com/oauth2-proxy/oauth2-proxy/tree/v7.15.3)
<!-- generated:localization:end -->
