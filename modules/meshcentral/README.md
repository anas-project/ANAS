# MeshCentral

使用 OIDC-only 登录并通过 LDAPS 配置目录用户和 Group 的远程设备管理服务。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `meshcentral` |
| 版本 / revision | `1.2.4-r8` |
| 状态 | `release` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## 最简配置

```yaml
modules:
  meshcentral: {}
```

本 Module 还要求在部署级选择 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

## 身份、用户与 Group

浏览器认证强制使用 OIDC-only：匿名首页跳转 `/auth-oidc`，登录页不显示密码入口，服务端拒绝本地或 LDAP 密码登录。LDAPS 继续负责目录 users/groups provisioning。启用应用过滤时，`APP_meshcentral`、`APP_all` 或管理员组可访问，管理员组同时映射 site-admin。

固定 MeshCentral `1.2.4` 包含上游 RP logout 修复，ANAS 注册 post-logout URI，但上游没有标准 IAM→Module 通知 endpoint。因此只在真实浏览器矩阵同时验证 `state`、应用 session 先失效和 IAM 中央 session 结束后标为“应用发起登出”；当前能力状态是“上游支持、待该矩阵验收”，不声明后台双向登出。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc |
| Group | `APP_meshcentral` / `APP_all`；同步 groups |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更说明

**匹配键**：`anasIdentityAnchor`，而且是**直接作为 MeshCentral 的用户 id**。OIDC 侧
`oidc.custom.claims.uuid` 显式指向 anchor claim（**不是 `sub`**），落库的账号 id 形如
`user//~oidc:<anchor>`；LDAPS 侧 `ldapUserKey` 同样取 anchor。这是本部署里唯一一个两条链路都直接
锚定在永久身份键上的 Module，改名、换邮箱、移动 OU 都不会让它认错人。

代价是 anchor 会出现在 MeshCentral 的账号 id 上——它出现在管理员的用户列表与设备组授权条目里
（`DIRKEY-R-010` 允许的管理视图），不出现在普通用户可见的 URL 路径里。

| 目录侧变更 | MeshCentral 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | 同一账号：id 由 anchor 构成，与用户名无关，不新建也不改 id。界面显示名来自 `name` claim（`displayName`），下次登录刷新 | 账号 id 等于 `user//~oidc:<anchor>`：`已验证`，入口 `test-env/scripts/server-authentik-oidc-login-e2e.sh` 与 `server-llng-oidc-login-e2e.sh`（两个 Provider 各断言一次）；改名后仍命中同一 id：`推断`（两个 E2E 都未改名） |
| `mail` 改变 | 从 `email` claim 刷新，时机是登录；它不参与账号绑定，没有唯一性约束会因此让建号失败 | `推断` |
| `displayName` 与其他 profile 属性 | 从 `name` claim 在**每次登录**刷新 | 显示名与目录一致：`已验证`，入口同上（断言 MeshCentral 账号 `name` 等于目录 `displayName`）；"每次登录都刷新"：`推断` |
| 直接或递归组成员变更 | 授权在**登录时**经 `groups` claim 生效；`oidc.groups.sync = true` 与 `revokeAdmin = true` 使站点管理员在同一时刻收回。不登录不收敛 | 管理员组映射与收回：`已验证`，入口同上（按 `siteadmin == 4294967295` 断言）；组变更的收敛时刻：`推断` |
| 账号停用 | 下次登录被 IAM 拒绝（准入在 Provider 侧判定）。**已有 MeshCentral 会话不会失效**：固定 `1.2.4` 没有标准 front/back-channel logout receiver。设备连接与 agent 不属于用户凭据，不受影响 | 无 IAM→Module receiver：`已验证`（固定版本能力审查，见[Module IAM / OIDC 支持清单](/reference/module-iam-support)的登出矩阵）；已有会话的存活时长：`推断` |
| 账号删除 | 同上，MeshCentral 账号连同其设备组成员资格原样保留；**设备与设备组的归属不会自动转移**，`orphanAgentUser` 只处理无主 agent，不处理已被删除用户名下的授权 | `推断` |
| 标识符回收再分配 | 新人的 anchor 不同，得到一个全新账号，**绝不会接上旧账号**（fail-closed）。这正是 anchor 作为 id 的收益 | `推断`（依据 id 由 anchor 构成这一已验证事实） |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 目录里停用或删除一个人时，必须在 MeshCentral 以 site administrator 身份**删除或禁用该账号**
   （账号 id 就是 `user//~oidc:<该用户的 anchor>`，可用 `samba-tool user show <user>
   --attributes=anasIdentityAnchor` 取到），否则其已有浏览器会话在会话过期前一直可用；
2. 删除账号前，先把它拥有的设备组授权转移给接手人；MeshCentral 不会自动转交；
3. 组撤权要立即生效时，除等待下次登录外还要在 MeshCentral 结束该用户的会话。

## 管理员登录与 IAM 故障恢复

没有单独的原生恢复管理员，也没有 `management.local_accounts`。IAM 故障时需要恢复 IAM/目录链路。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `MESHCENTRAL_DOMAIN_FULL` | `iam` |

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

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
| `meshcentral.db_name` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DB_NAME` | 否 | 否 | 否 | 否：`migrate-meshcentral-database` | `data_migrate` | 应用数据库名 |
| `meshcentral.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `MESHCENTRAL_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-meshcentral-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `meshcentral.domain_prefix` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `meshcentral.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `MESHCENTRAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |
| `meshcentral.mps_port` | int | `1..65535` | `4433` | `static` | `MESHCENTRAL_MPS_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | MPS 端口 |

### 查询和修改

```bash
anas config list meshcentral -w /srv/anas
anas config explain meshcentral.db_name
anas config set meshcentral.domain_prefix meshcentral -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas config list meshcentral -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

LDAPS 只用于后端 provisioning，不能作为浏览器登录或 IAM 故障回退。MeshCentral 上游的 `showPasswordLogin=false` 仅隐藏表单，因此 ANAS 镜像还会在服务端拒绝密码登录 POST；升级上游版本时若登录处理器结构变化，镜像构建会失败并要求审查补丁。

容器启动时会先验证 IAM OIDC Discovery metadata，并等待 issuer、授权、token 和 JWKS
端点可用；Provider 暂未就绪时不会让 MeshCentral 启动后静默禁用 OIDC。持续不可用时
容器退出并由 Compose restart policy 重试。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`1.2.4-r8`（reviewed 2026-08-21）
- Timezone / 时区：`container` — MeshCentral receives TZ through the module .env for process and log timestamps.
- Language scope / 语言范围：MeshCentral Web UI
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：MeshCentral translation key
- Fallback / 回退：User localization preference and browser language are used; unmatched languages fall back to English.
- Supported languages / 支持语言（30）：`ar`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `fi`, `fr`, `he`, `hi`, `hr`, `hu`, `it`, `ja`, `ko`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sr`, `sv`, `tr`, `uk`, `zh-Hans`, `zh-Hant`
- Notes / 说明：Upstream zh-chs and zh-cht are documented as canonical zh-Hans and zh-Hant.

Evidence / 证据：

- [1.2.4 — unique language keys in translate.json](https://github.com/Ylianst/MeshCentral/blob/1.2.4/translate/translate.json)
<!-- generated:localization:end -->
