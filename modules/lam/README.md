# LDAP Account Manager

通过 Web 界面管理 Samba AD 用户、组和计算机对象。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `lam` |
| 版本 / revision | `9.6.0-r7` |
| 状态 | `release` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |

## 最简配置

```yaml
modules:
  lam: {}
```

## 身份、用户与 Group

LAM 直接通过 LDAPS 工作。主登录页使用操作者自己的目录用户名和密码；`Admins` 允许进入完整管理界面，但实际目录写权限仍由 AD ACL 决定。LAM 可用于创建、禁用和管理用户及 Group，也可在操作者 ACL 允许时重置普通用户的目录密码。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps |
| IAM | 不支持/不适用 |
| Group | `APP_lam` / `APP_all` |
| 目录密码回写 | 使用登录操作者的 LDAPS 身份，受 AD ACL 限制 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

`admin_password` 保护 LAM configuration/profile 编辑面，并不是普通目录管理员密码。该凭据尚未建模为 `management.local_accounts`。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lam.admin_password` | string | — | — | `generated` | `LAM_ADMIN_PASSWORD` | 否 | 是 | 是 | 是 | `container_recreate` | 管理界面或服务管理员密码 |
| `lam.domain_prefix` | string | — | `lam` | `static` | `LAM_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `lam.language` | string | — | — | `inherited` | `LAM_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 界面回退语言 |

### 查询和修改

```bash
anas config list lam -w /srv/anas
anas config explain lam.admin_password
anas config set lam.domain_prefix lam -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

### 敏感参数与生成 Secret

- `lam.admin_password` → `LAM_ADMIN_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get LAM_ADMIN_PASSWORD -w /srv/anas
```

`secret get` 只在该值由 Module 生成并保存到 Secret Store 时可用。用户显式写入配置的值不会由安全库存命令回显。对于 `credential_rotate`，不能用 `config set` 或 `env.<KEY>` 代替应用内部轮换；对于仍标为普通重建的敏感参数，当前 CLI 虽接受 `config set`，但值会进入 argv/shell history，建议省略并使用生成 Secret，或在受保护的配置编辑流程中设置。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list lam -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

目录不可用时主登录不可用；当前没有由 `anas admin local` 管理的恢复入口。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`9.6.0-r7`（reviewed 2026-08-13）
- Timezone / 时区：`application` — ANAS writes the IANA TZ value to the LAM profile timeZone setting.
- Language scope / 语言范围：LDAP Account Manager Web UI
- Selection / 选择方式：`deployment_default`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：POSIX locale ending in .utf8
- Fallback / 回退：CLDR language matching chooses the closest same-script LAM locale, then English.
- Supported languages / 支持语言（15）：`de-DE`, `en-GB`, `en-US`, `es-ES`, `fr-FR`, `el-GR`, `it-IT`, `nl-NL`, `pl-PL`, `pt-BR`, `sk-SK`, `uk-UA`, `ja-JP`, `zh-TW`, `zh-CN`
- Notes / 说明：The ANAS image generates every listed POSIX locale. BCP 47 input is converted by the localization library, not by string replacement.

Evidence / 证据：

- [9.6 — active entries in lam/config/language](https://github.com/LDAPAccountManager/lam/blob/9.6/lam/config/language)
<!-- generated:localization:end -->
