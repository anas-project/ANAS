# Collabora Online

为 Nextcloud 提供在线文档编辑后端。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `collabora` |
| 版本 / revision | `26.4.2-r5` |
| 状态 | `release` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `nextcloud` | Module | — |

## 最简配置

```yaml
modules:
  collabora: {}
```

## 身份、用户与 Group

不直接同步目录，也不是 IAM Consumer。最终用户身份和文档权限由 Nextcloud/WOPI 会话决定。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

管理控制台使用 Module 自己的 `admin_username`/`admin_password`。该账号尚未声明为托管本地账号，因此不能使用 `anas admin local credential/rotate collabora`。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `collabora.admin_password` | string | — | — | `generated` | `COLLABORA_ADMIN_PASSWORD` | 否 | 是 | 是 | 是 | `container_recreate` | 管理界面或服务管理员密码 |
| `collabora.admin_username` | string | — | `admin_collabora` | `static` | `COLLABORA_ADMIN_USERNAME` | 否 | 否 | 否 | 是 | `container_recreate` | 管理界面用户名 |
| `collabora.auto_save` | int | — | `60` | `static` | `COLLABORA_AUTO_SAVE` | 否 | 否 | 否 | 是 | `container_recreate` | 自动保存间隔 |
| `collabora.domain_prefix` | string | — | `collabora` | `static` | `COLLABORA_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `collabora.log_level` | string | — | `warning` | `static` | `COLLABORA_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |

### 查询和修改

```bash
anas config list collabora -w /srv/anas
anas config explain collabora.admin_password
anas config set collabora.admin_username admin_collabora -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

### 敏感参数与生成 Secret

- `collabora.admin_password` → `COLLABORA_ADMIN_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get COLLABORA_ADMIN_PASSWORD -w /srv/anas
```

`secret get` 只在该值由 Module 生成并保存到 Secret Store 时可用。用户显式写入配置的值不会由安全库存命令回显。对于 `credential_rotate`，不能用 `config set` 或 `env.<KEY>` 代替应用内部轮换；对于仍标为普通重建的敏感参数，当前 CLI 虽接受 `config set`，但值会进入 argv/shell history，建议省略并使用生成 Secret，或在受保护的配置编辑流程中设置。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list collabora -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

管理员密码变更需要按配置项声明的重建流程处理；不要把它当作已验证的在线轮换。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`26.4.2-r5`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The Collabora service receives TZ through the module .env.
- Language scope / 语言范围：Collabora Online editor UI and document locale
- Selection / 选择方式：`integration`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：BCP 47
- Fallback / 回退：Nextcloud/WOPI passes the user or browser locale; Collabora defaults to en-US when the integration supplies none.
- Supported languages / 支持语言（43）：`sq`, `ar`, `hy`, `eu`, `bg`, `ca`, `zh-Hans`, `zh-Hant`, `hr`, `cs`, `da`, `nl`, `en-GB`, `en-US`, `eo`, `fi`, `fr`, `gl`, `de`, `el`, `he`, `hu`, `is`, `id`, `ga`, `it`, `ja`, `kk`, `nb`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sk`, `sl`, `es`, `sv`, `ta`, `tr`, `uk`, `vi`, `cy`
- Notes / 说明：Do not set container LANG to select the editor UI. Language is a per-session WOPI value.

Evidence / 证据：

- [26.04.2.4.1 — WOPI lang parameter and published UI language inventory](https://sdk.collaboraonline.com/CO-SDK-manual.pdf)
<!-- generated:localization:end -->
