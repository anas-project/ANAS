# FreeRADIUS

FreeRADIUS 服务骨架，仅用于开发和集成验证。

> [!WARNING]
> 当前生命周期为 `developing`，仅用于开发和验证，不属于推荐生产部署。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `freeradius` |
| 版本 / revision | `3.2.10-r4` |
| 状态 | `developing` |
| 类别 | `network` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## 最简配置

```yaml
modules:
  freeradius: {}
```

## 身份、用户与 Group

当前 Manifest 没有声明目录、IAM、用户同步或 Group 映射。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

没有受支持的管理员登录或本地恢复账号。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

当前没有公开可配置参数。

### 查询和修改

```bash
anas config list freeradius -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list freeradius -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

状态为 `developing`，不能作为生产认证服务使用。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`3.2.10-r4`（reviewed 2026-08-13）
- Timezone / 时区：`container` — The RADIUS service receives TZ for process and log timestamps.
- Language scope / 语言范围：RADIUS protocol service
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing language exists.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [3.2.10 — protocol service without a localized UI](https://github.com/FreeRADIUS/freeradius-server/tree/release_3_2_10)
<!-- generated:localization:end -->
