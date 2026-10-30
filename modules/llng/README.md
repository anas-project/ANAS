# LemonLDAP::NG

提供门户、应用启动器以及 OIDC/SAML 身份提供服务。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `llng` |
| 版本 / revision | `2.23.4-r13` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | 提供 Capability | `oidc, saml` |

## 最简配置

```yaml
modules:
  llng: {}
```

## 身份、用户与 Group

Samba AD 是用户和 Group 来源。Portal 使用目录认证，IAM 向 Consumer 发布 OIDC/SAML 端点和 Group 属性。`Admins` 可进入 Manager。

固定 LLNG `2.23.4` 在 OIDC RP 声明标准 endpoint 时配置 back-channel logout 和 session-required；从 Portal 登出可同步撤销通过 E2E 的应用会话。SAML 从 SP metadata 导入 SLS 并签名 SLO，Redirect/POST 均需要浏览器。每次 apply 会先删除旧 `LogoutUrl`、OIDC RP、SAML SLS/SP 元数据，再按当前协议重建，覆盖声明消失、换域名、协议切换和重复 apply。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更说明

**OIDC 匹配键：`anasIdentityAnchor`。** r12 为每个 RP 设置
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor`；ID Token、UserInfo、刷新后的 ID Token
与 Logout Token 使用同一来源。RP 准入规则要求会话包含非空 anchor，并与应用组规则取交集；
不回退到用户名。密码仍按 `sAMAccountName` 登录；Passkey 按不可变 anchor 查找同一用户，
`whatToTrace=anasIdentityAnchor` 使持久凭据在改名后继续有效。LLNG 不建立用户副本，普通会话的
主键是 session ID，持久会话由 anchor 定位。

Netbird 会把 `sub` 放入普通用户 API URL，尚无 anchor 投影例外；calculate 和 render_env
均拒绝 LLNG + Netbird OIDC 组合（`DIRKEY-R-013`）。Nextcloud 新部署已获内部 UID 和技术路径
使用 anchor 的明确例外，仍用目录属性显示姓名。

| 目录变化 | LLNG 的动作 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改名 | 重新登录读取新标签，OIDC `sub` 不变；支持 anchor 的 Consumer 继续使用原账号。SAML anchor 映射已接入，实际 SP 验收待完成 | `已验证`；见[实机验收](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) |
| `mail` / UPN 改变 | 下次目录认证更新属性；不改变 OIDC `sub` | `推断`；本轮未跑邮箱/UPN 专项 |
| `displayName` 等属性变化 | 下次目录认证重新读取；已建立会话可能保留旧值 | 读取链路 `已验证`；变更专项 `推断` |
| 直接或递归组变化 | 下次目录认证重新计算组；没有目录事件驱动的既有会话撤权 | 历史准入矩阵 `已验证`；实时撤权尚未实现 |
| 账号停用 | 新目录认证被 enabled filter 拒绝；管理员仍需删除已有 SSO 会话 | 历史准入矩阵 `已验证`；本轮未复验 |
| 账号删除 | 不再通过目录认证；已有 SSO 与 Consumer 会话需另行撤销 | `推断`；本轮未跑删除专项 |
| 登录名回收再分配 | 新目录对象取得不同 anchor，OIDC Consumer 不应复用旧账号 | `已验证`；见[实机验收](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) |

**兜底路径**：停用、删除或撤组后，在 LLNG Manager 删除该人的所有 SSO 会话，
按旧/新登录名核对，并按各 Consumer 的文档完成撤权。Portal 浏览器登出会发送已注册的
OIDC back-channel 通知，但这不等于监听 Samba AD 事件。旧 SSO 会话中的目录属性不会自动刷新。

**待实现**：SAML NameID 统一 anchor 与真实应用会话验收；目录事件触发的会话撤销。
SAML 不作为本轮主要支持协议。未完成前，SAML 使用者仍需人工处理改名与标签回收，
不能把 OIDC 的稳定身份结论套用到 SAML。产品尚未发版，本轮不提供旧账号迁移。

## 管理员登录与 IAM 故障恢复

当前没有独立的本地 `break_glass`。Manager 与 Portal 共用目录认证；IAM 或目录故障时需要从主机侧修复，不能依赖不存在的本地密码。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

Portal 的 `Documentation` 分类只向 Samba 管理员组显示。Module 会在首次初始化和每次
容器启动时同时写入该显示规则，因此从旧 revision 升级后，普通用户也不会继续看到
`Local documentation` 或 `Official Website` 入口。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres`, `mariadb` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 为本 Module 创建专属数据库、用户和稳定生成凭据。修改 `db_type`/`db_name` 不会迁移现有数据。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.background_logout_exceptions` | string | — | `""` | `static` | `LLNG_BACKGROUND_LOGOUT_EXCEPTIONS` | 否 | 否 | 否 | 是 | `container_recreate` | 显式接受无法后台强制退出的 OIDC client ID，逗号分隔；应用旧会话仍可能有效 |
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-llng-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用测试入口 |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Manager 域名前缀 |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 测试入口域名前缀 |
| `llng.workers` | int | — | `2` | `static` | `LLNG_WORKERS` | 否 | 否 | 否 | 是 | `container_recreate` | FastCGI worker 数（1–32）；较少 worker 降低内存，较多 worker 提高并发容量 |

### 查询和修改

```bash
anas config list llng -w /srv/anas
anas config explain llng.enable_test
anas config set llng.enable_test false -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list llng -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

不要配置已删除的 `LLNG_PASSWORD`；它不会创建上游管理员。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

## Samba 密码同步与强制改密

LLNG 通过校验证书的 LDAPS 将用户改密直接写回 Samba AD。Module 在初始配置及每次启动时
启用 `ldapChangePasswordAsUser=1`、`ldapAllowResetExpiredPassword=1`，因此已有持久配置
也会收敛到原生用户改密流程，不需要 LLNG 源码补丁。

用户输入正确原密码后，`pwdLastSet=0` 显示强制改密表单，真正到期的密码显示到期改密表单。
提交成功会显示成功提示，**随后仍需用新密码登录一次**；原生流程不会在改密提交时创建 SSO。
实际 OIDC 应用回跳的基线与结果见[配套计划](dev-docs/plans/llng-password-change.md)。

写回是同一次 LDAP Modify 删除旧 `unicodePwd`、添加新值；服务查询连接不意味着管理员
重置语义。Samba 验证旧密码并裁决域密码策略。LLNG 可精确预检最小长度和两次输入一致性；
复杂度、姓名、历史和最小间隔不能仅凭 LDAP 19/53 返回码区分。中文提示列出相关规则，
原始目录诊断保留在管理员日志。历史和最小间隔的完整策略矩阵未在本轮逐项验收。

ANAS 将 Samba 最小长度、复杂度和历史次数写入配置或提示；更改域策略后应重新执行
apply/reconcile。Samba 的旧密码宽限期可能使原密码短时间内仍可认证，本 Module 不改变该策略。
回归入口为 `server-llng-password-policy-e2e.sh`、`server-llng-password-config-e2e.sh`
和 `test-env/playwright/llng-password-oidc.spec.mjs`。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.23.4-r13`（reviewed 2026-10-23）
- Timezone / 时区：`container` — LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- Language scope / 语言范围：LemonLDAP::NG Portal and language selector
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：LLNG JSON language code
- Fallback / 回退：Portal language selector and Accept-Language are used; unmatched requests fall back to English.
- Supported languages / 支持语言（17）：`ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- Notes / 说明：Manager and mail templates have separate translation resources; the list records the user-facing Portal inventory.

Evidence / 证据：

- [v2.23.4 — Portal JSON language directory](https://gitlab.ow2.org/lemonldap-ng/lemonldap-ng/-/tree/v2.23.4/lemonldap-ng-portal/site/htdocs/static/languages)
<!-- generated:localization:end -->
