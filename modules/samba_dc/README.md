# Samba domain controller and DNS

提供 Active Directory、LDAPS、Kerberos 和 BIND9-DLZ DNS。

## 快速信息

| 项目 | 值 |
| --- | --- |
| Module | `samba_dc` |
| 版本 / revision | `4.23.6-r6` |
| 状态 | `release` |
| 类别 | `identity` |
| 运行时 | `compose` |

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## 最简配置

```yaml
modules:
  samba_dc: {}
```

## 身份、用户与 Group

它是人员用户、Group、服务账号和身份锚点的事实来源。使用 LAM 或 Samba 管理工具创建、禁用用户及管理 Group。`svc_ldap` 只读，`svc_password` 仅能在限定范围修改普通用户密码，`svc_anchor` 管理身份锚点。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | 不支持/不适用 |
| Group | Group 事实来源 |
| 目录密码回写 | 目录事实来源；由 ACL 与密码策略控制 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

日常目录管理员由 `admin_name` 定义；内置 RID 500 `Administrator` 只用于初始化和底层恢复。两者密码独立，不属于 `anas admin local` 管理面。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 用户与管理员密码策略

普通用户使用 `user_*` 参数生成域密码策略；内置 `Administrator` 和 `Admins` 组成员使用 `admin_*` 参数生成独立的 `pso_privileged`。两组参数互不覆盖。

| 策略 | 普通用户默认值 | 管理员默认值 |
| --- | --- | --- |
| 复杂度 | 关闭 | 开启 |
| 最小长度 | 8 位 | 8 位 |
| 密码历史 | 2 个 | 2 个 |
| 最短有效期 | 1 天 | 1 天 |
| 最长有效期 | 90 天 | 0（永不过期） |
| 锁定阈值 | 10 次 | 10 次 |
| 锁定时间 | 30 分钟 | 30 分钟 |
| 失败计数重置 | 30 分钟 | 30 分钟 |

复杂度关闭表示 Samba 不检查字符类别，并不强制密码同时包含字母和数字。最短有效期为 1 天表示用户成功修改密码后，一天内不能再次自行修改。

```yaml
modules:
  samba_dc:
    config:
      user_complex_pass: false
      user_min_pass_length: 8
      user_password_history: 2
      admin_complex_pass: true
      admin_min_pass_length: 8
      admin_password_history: 2
      admin_max_pass_age: 0
```

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 默认值 | 环境变量 | 必填 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | string | `true` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | 否 | 否 | 是 | `hot_reload` | 管理员密码复杂度策略 |
| `samba_dc.admin_lockout_duration` | string | `30` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | 否 | 否 | 是 | `hot_reload` | 管理员锁定时长 |
| `samba_dc.admin_lockout_reset_after` | string | `30` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | 否 | 否 | 是 | `hot_reload` | 管理员失败计数重置时长 |
| `samba_dc.admin_lockout_threshold` | string | `10` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | 否 | 否 | 是 | `hot_reload` | 管理员锁定阈值 |
| `samba_dc.admin_max_pass_age` | string | `0` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | 否 | 否 | 是 | `hot_reload` | 管理员密码最长有效期，0 表示永不过期 |
| `samba_dc.admin_min_pass_age` | string | `1` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | 否 | 否 | 是 | `hot_reload` | 管理员密码最短使用期 |
| `samba_dc.admin_min_pass_length` | string | `8` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | 否 | 否 | 是 | `hot_reload` | 管理员密码最短长度 |
| `samba_dc.admin_password_history` | string | `2` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | 否 | 否 | 是 | `hot_reload` | 管理员密码历史数量 |
| `samba_dc.admin_name` | string | `admin` | `SAMBA_DC_ADMIN_NAME` | 否 | 否 | 是 | `container_recreate` | 日常目录管理员用户名 |
| `samba_dc.admin_password` | string | `—` | `SAMBA_DC_ADMIN_PASSWORD` | 否 | 是 | 否：`rotate-samba-admin-password` | `credential_rotate` | 管理界面或服务管理员密码 |
| `samba_dc.administrator_password` | string | `—` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | 否 | 是 | 否：`rotate-samba-administrator-password` | `credential_rotate` | 内置 Administrator 密码 |
| `samba_dc.anchor_bind_name` | string | `svc_anchor` | `SAMBA_DC_ANCHOR_BIND_NAME` | 否 | 否 | 是 | `container_recreate` | 身份锚点服务账号名 |
| `samba_dc.anchor_bind_password` | string | `—` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | 否 | 是 | 否：`rotate-anchor-bind-password` | `credential_rotate` | 身份锚点服务账号密码 |
| `samba_dc.anchor_scan_interval` | string | `300` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | 否 | 否 | 是 | `container_recreate` | 身份锚点补漏扫描间隔 |
| `samba_dc.app_filter` | string | `true` | `SAMBA_DC_APP_FILTER` | 否 | 否 | 是 | `container_recreate` | 是否按 APP_* Group 过滤应用用户 |
| `samba_dc.create_structure` | string | `true` | `SAMBA_DC_CREATE_STRUCTURE` | 否 | 否 | 是 | `container_recreate` | 是否创建目录 OU/Group 结构 |
| `samba_dc.dns_allowed_networks` | string | `—` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | 否 | 否 | 是 | `container_recreate` | 允许递归查询的网络 |
| `samba_dc.dns_cache_size` | string | `128M` | `SAMBA_DC_DNS_CACHE_SIZE` | 否 | 否 | 是 | `container_recreate` | DNS 缓存上限 |
| `samba_dc.dns_debug` | string | `false` | `SAMBA_DC_DNS_DEBUG` | 否 | 否 | 是 | `container_recreate` | DNS 调试开关 |
| `samba_dc.dns_forwarders` | string | `—` | `SAMBA_DC_DNS_FORWARDERS` | 否 | 否 | 是 | `container_recreate` | 上游 DNS 转发器 |
| `samba_dc.ldap_bind_name` | string | `svc_ldap` | `SAMBA_DC_LDAP_BIND_NAME` | 否 | 否 | 是 | `container_recreate` | 只读 LDAP 服务账号名 |
| `samba_dc.ldap_bind_password` | string | `—` | `SAMBA_DC_LDAP_BIND_PASSWORD` | 否 | 是 | 否：`rotate-ldap-bind-password` | `credential_rotate` | 只读 LDAP 服务账号密码 |
| `samba_dc.log_level` | string | `1` | `SAMBA_DC_LOG_LEVEL` | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_dc.max_log_size` | string | `2048` | `SAMBA_DC_MAX_LOG_SIZE` | 否 | 否 | 是 | `container_recreate` | 单个日志文件上限 |
| `samba_dc.netbios_name` | string | `—` | `SAMBA_DC_NETBIOS_NAME` | 否 | 否 | 否：`replace-domain-controller` | `immutable` | 域控制器 NetBIOS 名称 |
| `samba_dc.password_bind_name` | string | `svc_password` | `SAMBA_DC_PASSWORD_BIND_NAME` | 否 | 否 | 是 | `container_recreate` | 密码回写服务账号名 |
| `samba_dc.password_bind_password` | string | `—` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | 否 | 是 | 否：`rotate-password-bind-password` | `credential_rotate` | 密码回写服务账号密码 |
| `samba_dc.realm` | string | `—` | `SAMBA_DC_REALM` | 否 | 否 | 否：`migrate-domain` | `immutable` | AD Realm |
| `samba_dc.template_homedir` | string | `/home/%D/%U` | `SAMBA_DC_TEMPLATE_HOMEDIR` | 否 | 否 | 是 | `container_recreate` | 目录用户 home 模板 |
| `samba_dc.template_shell` | string | `/bin/false` | `SAMBA_DC_TEMPLATE_SHELL` | 否 | 否 | 是 | `container_recreate` | 目录用户 shell 模板 |
| `samba_dc.user_complex_pass` | string | `false` | `SAMBA_DC_USER_COMPLEX_PASS` | 否 | 否 | 是 | `hot_reload` | 密码复杂度策略 |
| `samba_dc.user_lockout_duration` | string | `30` | `SAMBA_DC_USER_LOCKOUT_DURATION` | 否 | 否 | 是 | `hot_reload` | 锁定时长 |
| `samba_dc.user_lockout_reset_after` | string | `30` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | 否 | 否 | 是 | `hot_reload` | 失败计数重置时长 |
| `samba_dc.user_lockout_threshold` | string | `10` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | 否 | 否 | 是 | `hot_reload` | 锁定阈值 |
| `samba_dc.user_max_pass_age` | string | `90` | `SAMBA_DC_USER_MAX_PASS_AGE` | 否 | 否 | 是 | `hot_reload` | 密码最长有效期 |
| `samba_dc.user_min_pass_age` | string | `1` | `SAMBA_DC_USER_MIN_PASS_AGE` | 否 | 否 | 是 | `hot_reload` | 密码最短使用期 |
| `samba_dc.user_min_pass_length` | string | `8` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | 否 | 否 | 是 | `hot_reload` | 密码最短长度 |
| `samba_dc.user_password_history` | string | `2` | `SAMBA_DC_USER_PASSWORD_HISTORY` | 否 | 否 | 是 | `hot_reload` | 密码历史数量 |

### 查询和修改

```bash
anas config list samba_dc -w /srv/anas
anas config explain samba_dc.admin_complex_pass
anas config set samba_dc.admin_min_pass_length 12 -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

### 敏感参数与生成 Secret

- `samba_dc.admin_password` → `SAMBA_DC_ADMIN_PASSWORD`
- `samba_dc.administrator_password` → `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `samba_dc.anchor_bind_password` → `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `samba_dc.ldap_bind_password` → `SAMBA_DC_LDAP_BIND_PASSWORD`
- `samba_dc.password_bind_password` → `SAMBA_DC_PASSWORD_BIND_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get SAMBA_DC_ADMIN_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_ADMINISTRATOR_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_ANCHOR_BIND_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_LDAP_BIND_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_PASSWORD_BIND_PASSWORD -w /srv/anas
```

`secret get` 只在该值由 Module 生成并保存到 Secret Store 时可用。用户显式写入配置的值不会由安全库存命令回显。对于 `credential_rotate`，不能用 `config set` 或 `env.<KEY>` 代替应用内部轮换；对于仍标为普通重建的敏感参数，当前 CLI 虽接受 `config set`，但值会进入 argv/shell history，建议省略并使用生成 Secret，或在受保护的配置编辑流程中设置。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list samba_dc -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

`realm` 和 `netbios_name` 是不可变身份；密码参数需要专用凭据轮换，不能靠重建容器修改。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`4.23.6-r6`（reviewed 2026-08-13）
- Timezone / 时区：`system` — Startup validates TZ against /usr/share/zoneinfo and installs /etc/localtime and /etc/timezone.
- Language scope / 语言范围：directory, Kerberos, and DNS protocol services
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing Web UI exists; automation should keep LC_ALL=C where stable machine-readable output is required.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [4.23.6 — protocol and command-line services without a Module UI](https://www.samba.org/samba/docs/current/man-html/)
<!-- generated:localization:end -->
