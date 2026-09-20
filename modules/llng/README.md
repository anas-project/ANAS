# LemonLDAP::NG

提供门户、应用启动器以及 OIDC/SAML 身份提供服务。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `llng` |
| 版本 / revision | `2.23.2-r11` |
| 状态 | `release` |
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

固定 LLNG `2.23.2` 在 OIDC RP 声明标准 endpoint 时配置 back-channel logout 和 session-required；从 Portal 登出可同步撤销通过 E2E 的应用会话。SAML 从 SP metadata 导入 SLS 并签名 SLO，Redirect/POST 均需要浏览器。每次 apply 会先删除旧 `LogoutUrl`、OIDC RP、SAML SLS/SP 元数据，再按当前协议重建，覆盖声明消失、换域名、协议切换和重复 apply。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更说明

**匹配键：`sAMAccountName`（小写化），不是 anchor —— 这是一个已声明的缺口。**

LLNG 不保存目录副本：Portal 每次登录都按
`AuthLDAPFilter = (&<user class><enabled>(sAMAccountName=$user))` 直接向 Samba AD 认证，会话是
唯一的持久状态。会话的主键是 `whatToTrace`，本 Module 的 `lmConf.json` 把它定义为宏
`_whatToTrace`，在 AD 认证下求值为 `lc($_user)`，即用户输入的登录名小写化。

**由此产生两条不符合 `DIRKEY-R-001` 的后果**，按 `DIRKEY-R-004` 在此显式声明，不用"由 IAM 负责"
打发：

1. **OIDC `sub` 就是这个登录名。** 本 Module 没有为任何 RP 设置
   `oidcRPMetaDataOptionsUserIDAttr`，固定 `2.23.2` 在该字段为空时回退到 `whatToTrace`。于是目录
   改名之后，同一个人对所有 OIDC Consumer 呈现为**新的主体标识符**：`forgejo` 会建出第二个账号、
   `vikunja` 会按新的 `(issuer, sub)` JIT 建号、`netbird` 会产生第二个用户，原有资产全部留在旧
   账号下。
2. **SAML `NameID` 同样取自登录名。** `llng-config.sh` 里的 `samlNameIDFormatMap*` 全部是注释，
   因此 NameID 由 LLNG 的默认格式映射决定，仍然落到 `$uid`（= `sAMAccountName`）。

**anchor 是可送达的，只是没有被用作主体标识符。** `ldapExportedVars` 已包含
`anasIdentityAnchor`，明确请求它的 Consumer 会在 claim/attribute 里拿到正确的值——`nextcloud` 与
`meshcentral` 正是这样绕开上面两条的，两者在 LLNG 部署下的身份断言都已通过 E2E。

**技术阻碍**：`whatToTrace` 是会话主键与日志键，改动它会同时改变会话存储、审计日志与所有按它
索引的表；而 per-RP 的 `oidcRPMetaDataOptionsUserIDAttr` 能否取到 `anasIdentityAnchor` 这个
exported var、SAML 一侧能否独立配置 NameID 来源，**都必须在固定 `2.23.2` 上跑探针确认**。整改与
探针归[目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)
M2；按 `DIRKEY-R-005`，每次变更固定版本时复核结论。

| 目录侧变更 | LLNG 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | **等同于换了一个人**：Portal 用新名认证成功，但 `whatToTrace` 变了，OIDC `sub` 与 SAML NameID 随之改变，下游 Consumer 把他当作新人。请求 anchor claim 的 Consumer（`nextcloud`、`meshcentral`）不受影响 | `sub`/NameID 取自 `whatToTrace`：`推断`（依据本 Module 的 `lmConf.json`、`llng-config.sh` 未设置对应覆盖项，以及固定版本的回退行为，**未跑探针**）；anchor claim 可正确送达：`已验证`，入口 `test-env/scripts/server-llng-oidc-login-e2e.sh`（断言 MeshCentral 账号 id 等于 `user//~oidc:<anchor>`、Nextcloud 的 `oc_ldap_user_mapping.directory_uuid` 等于 anchor） |
| `mail` 改变 | 每次登录从 `ldapExportedVars.mail` 重新读取；不参与身份匹配，也没有唯一性约束 | `推断` |
| `displayName` 与其他 profile 属性 | 每次登录从 `ldapExportedVars` 重新读取。LLNG 不缓存目录副本，因此**不存在"陈旧属性"问题** | `推断` |
| 直接或递归组成员变更 | 每次登录经 `ldapGroupRecursive: 1` 重新计算，授权在**下次登录**生效；已建立的 LLNG 会话不会因目录组变更而重算 | 递归组生效：`已验证`，入口 `server-llng-login-matrix-e2e.sh`（准入与拒绝矩阵）；已有会话不重算：`推断` |
| 账号停用 | `AuthLDAPFilter` 含 `(!(userAccountControl:...=2))`，停用后**无法再登录**。**已建立的 LLNG 会话不会自动失效**，必须由管理员在 Manager 里删除对应 session | 登录被拒：`已验证`，入口 `server-llng-login-matrix-e2e.sh`；已有会话的存活：`推断` |
| 账号删除 | 同上。LLNG 没有应用内资产；但下游 Consumer 的账号与资产完全不受影响，必须逐个处理 | `推断` |
| 标识符回收再分配 | **fail-open，且是本部署最危险的一条**：新人拿到回收的 `sAMAccountName` 后，`whatToTrace` 与旧人完全相同，因此 OIDC `sub` 与 SAML NameID 也相同，所有按主体标识符认人的 Consumer 会**直接把新人认成旧人**，继承其全部账号与资产 | `推断`（依据上一条同样未经探针的 `sub` 取值链路） |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 停用或删除目录账号后，必须在 LLNG Manager 里**删除该用户的 session**（按 `whatToTrace` 检索，
   即其登录名的小写形式）；仅目录停用不会结束已建立的会话；
2. 撤权后还必须按各 Consumer 自己的《目录属性变更说明》逐个执行动作；LLNG 只能保证该人无法再次
   通过 Portal 登录；
3. **目录侧流程约束（强制）**：使用 LLNG 作为 Provider 时，**`sAMAccountName` 绝对不得回收再
   分配**，且**改名必须走人工流程**：先在每个 Consumer 里转移或清理该人的资产，再改名。在
   `DIRKEY-R-008` 落地之前，这两条是纪律而非技术保障；
4. 需要跨改名稳定的新 Consumer，**必须显式请求 `anasIdentityAnchor` claim 并用它作为持久键**，
   不得使用 `sub` 或 `NameID`。

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
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-llng-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用测试入口 |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Manager 域名前缀 |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 测试入口域名前缀 |

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

## Samba 密码同步行为 / Samba password behavior

LLNG 的用户改密直接写回 Samba AD。ANAS 将 Samba 的最小长度、复杂度开关和密码历史
次数写入 LLNG 配置或中文规则提示；修改这些 Samba 设置后，需要重新执行 ANAS
apply/reconcile 才会刷新 LLNG。当前 LDAP 写回使用委派服务账号重置语义；Samba 会执行
长度、复杂度和姓名规则，但不会可靠执行用户改密的历史与最小间隔。因此这两项目前仅作
提示，不能声称已强制执行。

LLNG 能在提交前精确检查最小长度和两次输入是否一致。复杂度与用户名/姓名规则仍由
Samba 最终裁决。Samba 的 LDAP 返回码通常只把策略拒绝归为 19 或 53，LLNG
不能可靠区分具体违反的是复杂度、姓名、历史还是最小间隔；中文页面因此显示包含全部
相关规则的可操作说明，而不会伪造一个精确原因。原始目录诊断保留给管理员日志。

LLNG password changes are written directly to Samba AD. ANAS synchronizes the
minimum length, complexity flag, and history count into LLNG configuration or its
Chinese guidance; run ANAS apply/reconcile after changing these Samba settings.
The delegated LDAP reset reliably enforces Samba length, complexity, and name
rules, but not user-change history or minimum age; those two are guidance-only.

LLNG can preflight the minimum length and matching confirmation exactly. Samba
remains authoritative for complexity and username/display-name content. LDAP
result codes normally collapse policy rejection into 19 or 53, so LLNG cannot
safely name one exact failed rule; it displays comprehensive actionable guidance
and keeps raw directory diagnostics in administrator logs.
See [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#samba-目录密码接入规范)
for the provider contract.

服务器 E2E `test-env/scripts/server-llng-password-policy-e2e.sh` 使用临时 Samba 用户覆盖
长度、确认、复杂度和成功写回（`pwdLastSet` 变化且新密码可认证）；历史次数与最小间隔
只验证提示状态，并在退出时恢复临时修改的域策略。当前 LLNG 未把 Samba 的
`pwdLastSet=0`/LDAP 773 状态路由到强制改密流程，因此不声明首次登录强制改密支持。
测试不要求旧密码立即失效，因为 Samba 可以按配置在旧密码宽限期内继续接受它。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.23.2-r11`（reviewed 2026-08-21）
- Timezone / 时区：`container` — LLNG receives TZ through the module .env; no deployment-wide application timezone is forced.
- Language scope / 语言范围：LemonLDAP::NG Portal and language selector
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：LLNG JSON language code
- Fallback / 回退：Portal language selector and Accept-Language are used; unmatched requests fall back to English.
- Supported languages / 支持语言（17）：`ar`, `en`, `es`, `fi`, `fr`, `he`, `it`, `mfe`, `pl`, `pt-BR`, `pt`, `ru`, `sk`, `tr`, `vi`, `zh-TW`, `zh`
- Notes / 说明：Manager and mail templates have separate translation resources; the list records the user-facing Portal inventory.

Evidence / 证据：

- [v2.23.2 — Portal JSON language directory](https://gitlab.ow2.org/lemonldap-ng/lemonldap-ng/-/tree/v2.23.2/lemonldap-ng-portal/site/htdocs/static/languages)
<!-- generated:localization:end -->
