# Vikunja

使用 OIDC-only 登录的任务与项目管理服务，提供列表、看板、表格、日历、Gantt、REST API、
Webhook 和 CalDAV。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `vikunja` |
| 版本 / revision | `2.4.0-r4` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## 最简配置

```yaml
modules:
  vikunja: {}
```

本 Module 还要求部署级 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

默认域名为 `https://tasks.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>`，默认数据库为 PostgreSQL。

## 身份、用户与 Group

Vikunja 使用 OIDC Authorization Code Flow。Module 注册 confidential client，固定 provider key
为 `anas`，回调地址为 `<VIKUNJA_DOMAIN_FULL>/auth/openid/anas`，scope 为
`openid profile email`。首次登录会按上游 `(issuer, sub)` JIT 创建用户，并读取 email、name 和
`preferred_username`；Vikunja 不从 LDAP 同步用户或 Group，也不支持目录密码回写。

本地密码登录和开放注册默认关闭。启用 Samba 应用过滤时，只有 `APP_vikunja`、`APP_all`
或管理员组成员可以在 IAM 完成登录；切换 IAM Provider 可能改变 `(issuer, sub)` 并创建新账号，
当前不自动合并。

Vikunja `2.4.0-r4` 会保存登录时的 ID Token。ANAS 的固定源码补丁在用户点击退出后先捕获当前
token/provider，再立即删除浏览器 token、认证状态和本地缓存；随后用已捕获 token 在最多 5 秒内
尽力删除 Vikunja 服务端 session，并从缓存的 discovery metadata 生成携带 `id_token_hint`、
`client_id` 和已登记 post-logout URI 的 RP-Initiated Logout 请求。IAM 或服务端注销不可用不会
恢复本地 session。当前版本没有标准 IAM→Vikunja front-/back-channel receiver，所以 Module
不发布 `OIDC_LOGOUT_*`，不声明双向登出或管理员后台撤销；r4 真实浏览器复验完成前仍标为
“修复已实现、待验收”。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | OIDC；JIT 建号 |
| Group | IAM 准入 `APP_vikunja` / `APP_all` / 管理员组；应用内 team 独立管理 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。Vikunja 用户、team 和 API token 应在应用内
管理；目录账号和密码应在 Samba AD/LAM 或其他目录管理面操作。

### 目录属性变更说明

**匹配键**：OIDC `(issuer, sub)`。Vikunja 自己不接目录，它对"这是谁"的全部认知都来自 ID Token。
**这个键在目录改名后是否稳定，完全取决于部署选了哪个 IAM Provider**——`authentik`（内部用户 UUID）
与 `casdoor`（不可变 User ID）的 `sub` 改名后不变；`llng` 的 `sub` 默认取自登录名，改名后会变，
Vikunja 会按新的 `(issuer, sub)` JIT 建出第二个账号，原有任务、清单与团队成员资格全部留在旧账号
下，且**当前不自动合并**。切换 IAM Provider 也会改变 `issuer`，效果相同。

应用内用户名是另一个值：Vikunja 从 `preferred_username`（即 `sAMAccountName`）取用户名，它出现在
界面与 `/api/v1/users/...` 路径里，**不参与认人**。

| 目录侧变更 | Vikunja 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | Provider 的 `sub` 稳定时是同一账号；Vikunja 里的用户名是否跟着刷新未经复核。Provider 的 `sub` 取自登录名时（`llng`）会建出第二个账号 | 建号时用户名等于目录 `sAMAccountName`：`已验证`，入口 `test-env/scripts/server-vikunja-oidc-e2e.sh`（断言 `select username from users` 命中目录用户名）；改名后的刷新与命中行为：`推断` |
| `mail` 改变 | 从 `email` claim 读取；是否在后续登录刷新未经复核。Vikunja 的邮箱有唯一性约束，旧地址仍挂在原账号上时，用同一地址的新账号会建号失败 | `推断` |
| `displayName` 与其他 profile 属性 | 从 `name` claim 读取；刷新时机未经复核 | `推断` |
| 直接或递归组成员变更 | **只影响能否登录**，在 IAM 侧按 `APP_vikunja`/`APP_all`/管理员组判定，该用户下次登录时生效。**应用内 team 与项目权限完全由 Vikunja 数据库拥有，目录组不投影到 team**，组变更对已授出的项目权限没有任何影响 | 准入门禁生效：`已验证`，入口同上（矩阵覆盖准入与拒绝用例）；team 不受目录组影响：`已验证`（Hook 与上游配置中不存在任何 group→team 映射） |
| 账号停用 | 下次登录被 IAM 拒绝。**已有 Vikunja session 与用户自建的 API token 不会失效**：固定 `2.4.0` 没有标准 IAM→Vikunja front-/back-channel receiver，API token 也不经过登录 | 无 receiver：`已验证`（固定版本能力审查，见[Module IAM / OIDC 支持清单](/reference/module-iam-support)的登出矩阵）；API token 的存活：`推断` |
| 账号删除 | 同上，Vikunja 账号连同其项目、任务、附件与 CalDAV 订阅原样保留；**资产不会自动转交** | `推断` |
| 标识符回收再分配 | 取决于 Provider：`sub` 是内部不可变 id 时（`authentik`、`casdoor`）新人得到新账号（fail-closed）；`sub` 取自被回收的登录名时（`llng`）新人直接接上旧账号的全部项目（**fail-open**）。邮箱唯一性约束在前一种情况下会让建号失败 | `推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 目录里停用或删除一个人时，必须在 Vikunja 以管理员身份**删除该用户并撤销其 API token**；
   只在目录里停用不会结束已有 session，也不会让 API token 失效；
2. 删除账号前，先把该用户拥有的项目转让给接手人（Vikunja 的项目有明确 owner），再删除账号；
3. **部署侧约束**：一旦选定 IAM Provider 就不要更换。更换会改变 `issuer`，所有人都会得到新账号，
   而 Vikunja 没有合并入口；
4. **目录侧流程约束**：使用 `llng` 作为 Provider 时，用户名改名等同于换人——在 `DIRKEY-R-008`
   落地之前，这个组合下的改名必须走"先在 Vikunja 转移资产，再改名"的人工流程。

## 管理员登录与 IAM 故障恢复

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `VIKUNJA_DOMAIN_FULL` | `iam` |

本 Module 没有 `management.local_accounts`，也没有应用内 break-glass 账号；
`anas admin local credential/rotate` 对它不可用。IAM 故障时应恢复 IAM、目录、内部 DNS 或
内部 CA 链路，不能用本地密码绕过。`<VIKUNJA_DOMAIN_FULL>/login` 只是普通登录页；在
`auth.local.enabled=false` 下不会提供可用的本地密码表单，也不存在预置用户名或密码。虽然上游
CLI 能创建本地用户，当前 Module 未声明、托管或轮换这类账号，临时开启本地认证不属于受支持的
应急流程。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres`, `mariadb` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 创建独立数据库、principal 和稳定生成密码。MariaDB binding 在容器内部映射为 Vikunja
的 `mysql` database type。修改 `db_type` 或 `db_name` 不会迁移现有数据。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；
不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `vikunja.db_name` | string | — | `vikunja` | `static` | `VIKUNJA_DB_NAME` | 否 | 否 | 否 | 否：`migrate-vikunja-database` | `data_migrate` | 应用数据库名 |
| `vikunja.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `VIKUNJA_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-vikunja-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `vikunja.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `tasks` | `static` | `VIKUNJA_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `vikunja.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `VIKUNJA_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议；仅支持 OIDC |
| `vikunja.language` | string | — | — | `inherited` | `VIKUNJA_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 新用户默认 UI 语言；已有用户偏好优先 |

### 查询和修改

```bash
anas config list vikunja -w /srv/anas
anas config explain vikunja.db_type
anas config set vikunja.domain_prefix tasks -w /srv/anas
anas config set vikunja.language zh-CN -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成。表中的专用流程名是生命周期声明，不保证
存在同名通用子命令；数据库迁移必须先备份并由操作者显式执行。

## ANAS 管理凭据轮换

`vikunja.service_secret` 与 `vikunja.oidc_client_secret` 已接入 deployment credential 事务：

```bash
anas credential rotate vikunja.service_secret --dry-run -w /srv/anas
anas credential rotate vikunja.oidc_client_secret -y -w /srv/anas
anas credential rotate --module vikunja -y -w /srv/anas
```

Module 批次在一个 candidate 中轮换两项；deployment 批次使用 `anas credential rotate --all`。
OIDC client secret 的冻结投影同时覆盖 Vikunja 与所选 IAM Provider，只有两端 candidate 启动、
Vikunja 容器内投影验证及全体 ready barrier 通过后才提交 Secret Store。任何失败都恢复 previous
deployment。轮换 service secret 会使依赖旧签名材料的 session/token 失效，应预告登录中断；真实
IAM 登录轮换 E2E 仍是 `release` 门禁。

## 存储、备份与恢复

附件保存在 `${DATA_PATH}/vikunja/files`；入口程序只在启动时修正这个挂载树的 UID/GID，随后
永久降权到 `1000:1000`。项目、任务、评论、用户、team、API token 和 webhook 配置在绑定的
关系数据库中。备份/恢复必须把数据库 Resource、附件、`.anas/secrets.yml` 和部署元数据保持在
同一恢复点。

恢复后至少验证 project、task、comment、attachment、OIDC 登录、API token 和 webhook。

```bash
anas plan -c /srv/anas/config.yml
anas config list vikunja -w /srv/anas
anas status -w /srv/anas
```

## API、Webhook 与 CalDAV

Vikunja 原生提供 REST/OpenAPI、用户创建的细粒度 API token、project/user webhook 和 CalDAV。
ANAS 不自动创建管理员 token；自动化应由专用用户创建最小权限 token，并将 token 与 webhook
Secret 保存在调用方自己的 Secret 存储中。Webhook 接收端必须验证签名并自行实现持久 inbox、
幂等与失败对账，不能假设投递会无限重试。

## 当前限制

- Module 状态为 `developing`。PostgreSQL/MariaDB、amd64/arm64、备份恢复、升级回滚和
  Authentik/LLNG 浏览器 E2E 仍是提升到 `release` 的门槛。
- SMTP、S3、Redis、搜索、Vikunja Pro、bot user 和 AI/MCP sidecar 尚未自动配置。
- 上游移动应用仍处于早期阶段，不能按 Web 全功能客户端承诺。
- 没有 IAM 主动清理 Vikunja session 的标准通知 endpoint，也没有本地恢复账号。
- Resource 数据库凭据、本地管理员和外部 API token 不属于 `credential rotate --module/--all`
  的统一 lifecycle 范围；Vikunja 当前没有本地管理员声明，API token 由应用用户管理。

完整验收口径见 [Vikunja Module 集成要求](dev-docs/requirements/vikunja-module.md)。

## 技术文档

镜像入口、Secret、环境作用域、Hook、网络和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`2.4.0-r4`（reviewed 2026-08-21）
- Timezone / 时区：`configured` — Vikunja service.timezone and the default timezone for new users inherit ANAS TZ; signed-in users may override their own timezone.
- Language scope / 语言范围：Vikunja Web UI and localized notifications
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=fallback`; `default_locale=not_consumed`
- Upstream format / 上游格式：BCP 47-like Vikunja locale key
- Fallback / 回退：New users inherit the matched ANAS default language; unsupported values warn and fall back to English, while saved user preferences take precedence.
- Supported languages / 支持语言（32）：`en`, `de-DE`, `de-CH`, `ru-RU`, `fr-FR`, `vi-VN`, `it-IT`, `cs-CZ`, `pl-PL`, `nl-NL`, `pt-PT`, `zh-CN`, `zh-TW`, `no-NO`, `es-ES`, `da-DK`, `ja-JP`, `hu-HU`, `ar-SA`, `fa-IR`, `sl-SI`, `pt-BR`, `hr-HR`, `uk-UA`, `lt-LT`, `bg-BG`, `ko-KR`, `tr-TR`, `fi-FI`, `he-IL`, `sv-SE`, `el-GR`
- Notes / 说明：Browser language is used when the account has no saved language; the ANAS value is a new-user default and does not override an existing preference. Canonical de-CH maps to Vikunja's upstream de-swiss key.

Evidence / 证据：

- [v2.4.0 — SUPPORTED_LOCALES and DEFAULT_LANGUAGE](https://github.com/go-vikunja/vikunja/blob/v2.4.0/frontend/src/i18n/index.ts)
<!-- generated:localization:end -->
