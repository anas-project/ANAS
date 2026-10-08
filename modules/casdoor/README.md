# Casdoor

提供 OIDC 与 SAML 的 IAM Provider，并通过 LDAPS 从 Samba AD 导入目录用户。

> [!NOTE]
> 当前生命周期为 `release`。r10 的正式双架构构建、OIDC 目录事件撤权、真实 Nextcloud
> anchor UID/改名/文件归属、备份恢复、凭据轮换和生命周期验收已通过，见
> [2026-10-06 发布验收](dev-docs/plans/archived/casdoor-iam.md)。
> OIDC 为主要支持协议；SAML 应用会话注销列为待实现，不作为当前 OIDC 发布阻塞项。
> Netbird 因将 `sub` 投影到普通用户 API URL 而被拒绝注册。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `casdoor` |
| 版本 / revision | `3.143.0-r11` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | 提供 Capability | `oidc, saml` |

## 最简配置

```yaml
identity:
  iam:
    provider: casdoor
modules:
  nextcloud: {}
```

只有存在 IAM Consumer 时 Runner 才自动加入所选 Provider；也可在 `modules` 中显式加入 `casdoor` 做独立验证。

## 身份与协议行为

Samba AD 仍是人员和目录账号的事实来源。Casdoor 使用受限只读 Bind 经 LDAPS 导入用户并远程校验密码。独立 `casdoor_dirwatch` 订阅 Samba 的持久目录事件日志，经过防抖后立即触发一次 LDAP 导入；它以 `anasIdentityAnchor` 关联影子用户，确定性收敛改名、停用、删除和 Group 撤权，并对同步用户刷新 `displayName` 和邮件。订阅器还经受信任 LDAPS 直接计算声明的递归组成员，避免上游同步保留旧 Group。默认每 5 分钟的周期同步继续保留为兜底。本实现不启用 Casdoor 的 LDAP/AD 密码写回，也不把 Casdoor 本地用户记录当作目录权威。

固定 Casdoor `3.143.0` 按通用 `ANAS_IAM_CLIENT__<APP>__*` 注册 OIDC/SAML Consumer。r10 从固定上游提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 和校验和为 `365d61c…f9460` 的源码包构建；六个受控补丁扩展 SAML `displayName/externalId` 模板、OIDC exact `sid` 用户/管理员 back-channel、两分钟 Logout Token、失败可观测性和 PostgreSQL 保留列安全查询，均位于 `casdoor/patches/`。OIDC access token 固定为 1 小时，refresh token 固定为 30 天。OIDC back-channel URI 仅在 Consumer 明确声明时登记，声明消失或协议切换会显式写空旧 URI；历史 Consumer E2E 已通过用户退出、管理员精确删 session、原 Cookie 撤销、多会话隔离、签名字段与重放拒绝。固定版本没有 SAML LogoutRequest/LogoutResponse 消费路径，因此不发布 SLO endpoint/binding，SAML Consumer 只能本地登出。

通用 `ALLOW_GROUPS` 被渲染为同名 Casdoor Group/Role 和按 Consumer 区分的 Application Permission；无命中组、禁用或已删除用户会在签发前被拒绝。订阅器把 Samba `anasIdentityAnchor` 写入 Casdoor `ExternalId`，OIDC 自定义 claim 与 SAML 显式锚点属性使用该值，Group claim 来自同名 Role；OIDC token、UserInfo、Logout Token 的 `sub` 与 SAML `NameID` 均取同一 `ExternalId`，同锚点改名会复用原记录。未知 SAML 来源会被省略，不会冒充永久锚点。真实 Consumer E2E 已覆盖签名、属性、Group 门禁、改名复用、应用建号、管理员映射和 OIDC 会话撤销；恢复、升级/回滚与凭据轮换证据见实施计划。

### 目录属性变更说明

**匹配键**：`anasIdentityAnchor`，保存在 Casdoor 用户的 `externalId` 字段。`casdoor_dirwatch` 每批
同步都先按 anchor 关联目录对象与 Casdoor 影子用户，再执行 LDAP 导入，因此改名、停用、删除、组
撤权都确定性地落在**同一条记录**上；Casdoor 自己不可变的 `id` 不被修改。LDAP 过滤器
`(anasIdentityAnchor=*)` 保证没有 anchor 的对象不会被导入。

**r10 发给 Consumer 的主体标识符就是 anchor。** 受控补丁 `0005-directory-subject.patch`
统一 JWT 的四种格式、UserInfo、Logout Token 与 SAML 1.1/2.0；SAML 2.0 NameID 使用 persistent
格式。`anas` 组织的用户缺锚点或锚点带空白时拒绝签发，`built-in` 恢复管理员的 OIDC 保留内部 ID。
`0006-directory-revocation.patch` 在刷新时重新检查身份和应用准入，并保留原 `sid`。
产品尚未发版，无需旧账号迁移。源码测试不代替真实协议或 Consumer 账号归属验收。

| 目录侧变更 | Casdoor 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | 同 anchor 复用内部 id，刷新标签；OIDC sub 与 SAML NameID 保持 anchor，捕获旧会话撤权 | r10 OIDC 与签名 SAML 协议通过；真实 Nextcloud 原 uid/文件和旧 Cookie 撤权通过 |
| `mail` 改变 | 刷新 email，不参与身份匹配，不触发注销 | 既有目录属性 E2E；r10 行为由 helper 单元验证 |
| `displayName` 等 profile 改变 | 刷新显示属性，合并 properties，不覆盖人工属性 | helper 单元通过 |
| 直接/递归组改变 | 重新计算成员；准入丧失只撤对应应用，refresh 重查准入 | r10 Cookie/refresh 和两 client 隔离通过；Nextcloud 直接组撤权通过 |
| 账号停用 | 禁止、清组、捕获已有会话并撤权；重新启用复用身份 | r10 标准 Consumer 原 Cookie/refresh 撤权通过 |
| 账号删除 | 软删除、禁止、清组、撤销捕获会话；Consumer 账号与资产保留 | r10 标准 Consumer 原 Cookie 撤权通过；不替代应用资产转移验收 |
| 标识符回收 | 不同 anchor 的同名对象隔离，不覆盖旧绑定或自动恢复准入；健康显示冲突 | r10 同名新 anchor 拒绝登录、旧身份保留与健康冲突诊断通过 |

**撤权与兜底路径**：账号停用、删除、改名或锚点冲突撤销该用户的已捕获会话；组准入丧失只撤
对应应用。OIDC 通过标准 back-channel 通知终止 Consumer 会话，通知失败独立持久化重试，不阻塞
目录游标；缺失接收端、通知超时或 SAML 会话无法注销会在健康状态中显示。
启动时和每 300 秒全量对账修复日志缺口，并清除不在有效 LDAP 结果中的用户准入。
用户名回收且锚点不一致时，冲突对象不导入、旧身份禁止访问，其他用户仍可同步；恢复须人工处理。
离线验证的 access token 仍需 Consumer 自行撤销。SAML Consumer 暂需执行其本地会话撤销路径。

## 管理员登录与 IAM 故障恢复

`break_glass` 本地恢复账号遵循 ANAS 的不可配置默认模板 `admin_{module}`，实际用户名为 `admin_casdoor`；密码独立生成并可事务轮换。Casdoor 没有要求保留的上游内置用户名，因此不声明 `fixed_username`。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `CASDOOR_DOMAIN_FULL` | `iam` |
| `local_recovery` | `CASDOOR_LOCAL_RECOVERY_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `admin_casdoor` | `plaintext_on_bootstrap` | 是 |

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

## 凭据轮换

Casdoor 声明两个 ANAS 受管凭据：`casdoor.signing_key` 使用一小时 X.509 信任重叠，
`casdoor.portal_client_secret` 在新值验证成功后立即废止旧值。先查看 dry-run，再在维护窗口执行：

```bash
anas credential rotate casdoor.signing_key -w /srv/anas --dry-run --json
anas credential rotate casdoor.signing_key -w /srv/anas -y --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas -y --json
```

完整的验证、失败恢复和备份步骤见
[Casdoor IAM 运维 Runbook](../../docs/operations/casdoor-iam.md)。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-casdoor-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀及所有 IAM 端点 |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | 否 | 否 | 否 | 是 | `container_recreate` | LDAP 自动同步周期（分钟） |

### 查询和修改

```bash
anas config list casdoor -w /srv/anas
anas config explain casdoor.ldap_auto_sync_minutes
anas config plan -w /srv/anas
```

## 存储、备份与验证

r9 将 deployment 中仅 root 可读的配置只读挂载到 `/opt/anas/conf/app.conf`。启动入口在容器内建立 UID/GID `1000`、权限 `0600` 的 `/conf/app.conf`，再启动 Casdoor；不会放宽不可变制品权限。每次启动重新复制配置，初始化失败会阻止服务就绪。

Casdoor 持久状态位于绑定的 PostgreSQL Resource；目录订阅游标位于 `${DATA_PATH}/casdoor/dirwatch`。备份必须让数据库、订阅游标、workspace Secret Store 和本地管理员库存保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas status -w /srv/anas
```

## 技术文档

实现、安全边界与测试入口见[技术文档](docs/technical.md)。
发布要求、未完成项和 E2E 证据分别见
[Casdoor IAM Provider 集成要求](dev-docs/requirements/casdoor-iam.md)与
[Casdoor IAM Provider 实施计划](dev-docs/plans/archived/casdoor-iam.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`3.143.0-r11`（reviewed 2026-08-27）
- Timezone / 时区：`container` — Casdoor receives TZ through the module environment; no separate application timezone is forced.
- Language scope / 语言范围：Casdoor Web UI default
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：Casdoor language code
- Fallback / 回退：ANAS maps zh-prefixed defaults to zh and all other values to en; users may change the UI language in Casdoor.
- Supported languages / 支持语言（2）：`en`, `zh`
- Notes / 说明：This inventory records the two ANAS-selected defaults, not every translation shipped by upstream.

Evidence / 证据：

- [v3.143.0 — web/src/locales English and Chinese resources](https://github.com/casdoor/casdoor/tree/v3.143.0/web/src/locales)
<!-- generated:localization:end -->
