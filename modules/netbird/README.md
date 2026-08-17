# NetBird

尚未完成的 WireGuard overlay 网络 Module。

> [!WARNING]
> 当前生命周期为 `developing`，仅用于开发和验证，不属于推荐生产部署。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `netbird` |
| 版本 / revision | `0.76.1-r2` |
| 状态 | `developing` |
| 类别 | `network` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## 最简配置

```yaml
modules:
  netbird: {}
```

本 Module 还要求在部署级选择 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

## 身份、用户与 Group

声明为 OIDC Consumer，并需要应用 Group，但管理员角色映射仍是发布阻塞项。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有受支持的私有恢复管理员。IAM 故障时没有文档化的绕过入口。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.adminer_enabled` | string | `false` | `NETBIRD_ADMINER_ENABLED` | 否 | 否 | 是 | `container_recreate` | 是否启用 Adminer |
| `netbird.domain_prefix` | string | `netbird` | `NETBIRD_DOMAIN_PREFIX` | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `netbird.iam_protocol` | string | `auto` | `NETBIRD_IAM_PROTOCOL` | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |

### 查询和修改

```bash
anas config list netbird -w /srv/anas
anas config explain netbird.adminer_enabled
anas config set netbird.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list netbird -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

状态为 `developing`，不属于推荐部署。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.76.1-r3`（reviewed 2026-08-13）
- Timezone / 时区：`partial` — Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- Language scope / 语言范围：NetBird Dashboard v2.90.9
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：English is the only Dashboard language in the fixed source version.
- Supported languages / 支持语言（1）：`en`
- Notes / 说明：NetBird desktop client's i18n package is a different component and must not be used to claim Dashboard languages.

Evidence / 证据：

- [v2.90.9 — source tree contains no locale, i18n, or translation resources](https://github.com/netbirdio/dashboard/tree/v2.90.9)
<!-- generated:localization:end -->
