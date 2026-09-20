# Casdoor

提供 OIDC 与 SAML 的 IAM Provider，并通过 LDAPS 从 Samba AD 导入目录用户。

> [!NOTE]
> 当前生命周期为 `release`。目录永久锚点、按应用 Group 授权、账号停用传播、OIDC 会话撤销、
> 恢复管理员、空 workspace 备份恢复、多架构生命周期和受管凭据轮换均已完成真实 E2E。
> 固定版本不支持 SAML SLO，因此不发布对应 endpoint/binding。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `casdoor` |
| 版本 / revision | `3.143.0-r8` |
| 状态 | `release` |
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

Samba AD 仍是人员和目录账号的事实来源。Casdoor 使用受限只读 Bind 经 LDAPS 导入用户并远程校验密码。独立 `casdoor_dirwatch` 订阅 Samba 的持久目录事件日志，经过防抖后立即触发一次 LDAP 导入；它以 `anasIdentityAnchor` 关联影子用户，确定性收敛改名、停用、删除和 Group 撤权，并只对本批事件涉及的用户刷新 `displayName` 和邮件。订阅器还经受信任 LDAPS 直接计算声明的递归组成员，避免上游同步保留旧 Group。默认每 5 分钟的周期同步继续保留为兜底。本实现不启用 Casdoor 的 LDAP/AD 密码写回，也不把 Casdoor 本地用户记录当作目录权威。

固定 Casdoor `3.143.0` 按通用 `ANAS_IAM_CLIENT__<APP>__*` 注册 OIDC/SAML Consumer。r8 从固定上游提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 和校验和为 `365d61c…f9460` 的源码包构建；四个受控补丁扩展 SAML `displayName/externalId` 模板、OIDC exact `sid` 用户/管理员 back-channel、两分钟 Logout Token、失败可观测性和 PostgreSQL 保留列安全查询，均位于 `casdoor/patches/`。OIDC access token 固定为 1 小时，refresh token 固定为 30 天。OIDC back-channel URI 仅在 Consumer 明确声明时登记，声明消失或协议切换会显式写空旧 URI；真实 Consumer E2E 已通过用户退出、管理员精确删 session、原 Cookie 撤销、多会话隔离、签名字段与重放拒绝。固定版本没有 SAML LogoutRequest/LogoutResponse 消费路径，因此不发布 SLO endpoint/binding，SAML Consumer 只能本地登出。

通用 `ALLOW_GROUPS` 被渲染为同名 Casdoor Group/Role 和按 Consumer 区分的 Application Permission；无命中组、禁用或已删除用户会在签发前被拒绝。订阅器把 Samba `anasIdentityAnchor` 写入 Casdoor `ExternalId`，OIDC 自定义 claim 与 SAML 显式锚点属性使用该值，Group claim 来自同名 Role；Casdoor 不可变 User ID 继续作为稳定 `sub`，同锚点改名会复用原记录。未知 SAML 来源会被省略，不会冒充永久锚点。真实 Consumer E2E 已覆盖签名、属性、Group 门禁、改名复用、应用建号、管理员映射和 OIDC 会话撤销；恢复、升级/回滚与凭据轮换证据见实施计划。

### 目录属性变更说明

**匹配键**：`anasIdentityAnchor`，保存在 Casdoor 用户的 `externalId` 字段。`casdoor_dirwatch` 每批
同步都先按 anchor 关联目录对象与 Casdoor 影子用户，再执行 LDAP 导入，因此改名、停用、删除、组
撤权都确定性地落在**同一条记录**上；Casdoor 自己不可变的 `id` 不被修改。LDAP 过滤器
`(anasIdentityAnchor=*)` 保证没有 anchor 的对象不会被导入。

**它发给 Consumer 的主体标识符还不是 anchor，且两种协议表现不同（当前缺口）：**

- **OIDC `sub` = Casdoor 不可变 User ID**。跨改名稳定，但不是可以直接与目录对账的值；
- **SAML `NameID` = 用户名**。**改名后 NameID 会变**——这是一个标签被当作主体标识符使用，直接
  违反 `DIRKEY-R-001`。SAML Consumer 的稳定关联**必须**使用显式的锚点属性（映射到
  `$user.externalId`），不能使用 NameID。`nextcloud` 的 SAML 模式正是这样配置的。

两者都属于 `DIRKEY-R-008` 尚未满足的部分，整改归
[目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)
M2；主体标识符是否可配置必须在真实固定版本上跑探针确认。

| 目录侧变更 | Casdoor 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | 同一条记录：按 anchor 关联，`id` 与 `externalId` 都不变，`name`（用户名）刷新为新值，旧名不再解析。**OIDC `sub` 不变；SAML `NameID` 变成新用户名** | 改名复用不可变 `id` 与 anchor、旧名不保留：`已验证`，入口 `test-env/scripts/server-casdoor-directory-authority-e2e.sh`（"rename reuses the permanent identity"）；一个 anchor 只对应一个不可变 `sub`：`已验证`，入口 `server-casdoor-oidc-e2e.sh`；NameID 随改名变化：`已验证`，入口 `server-casdoor-saml-e2e.sh` |
| `mail` 改变 | 只为本批事件涉及的用户刷新 `email`；不参与身份匹配 | `已验证`（改名/停用/删除矩阵中 email 随目录刷新），入口 `server-casdoor-saml-e2e.sh` 与 `server-casdoor-oidc-e2e.sh` 的属性断言 |
| `displayName` 与其他 profile 属性 | `displayName` 同上只为相关用户刷新；其余目录属性合并进 `properties`，不删除人工属性 | `已验证`，入口同上（断言 `name`/`displayName` 与目录一致） |
| 直接或递归组成员变更 | `casdoor_dirwatch` 订阅持久目录事件日志，防抖后立即触发一次同步，并经受信任 LDAPS 直接计算递归成员；默认每 5 分钟周期同步兜底。直接与递归撤权都权威生效 | `已验证`，入口 `server-casdoor-directory-authority-e2e.sh`（"group removals are authoritative"，直接与递归各一次）与 `server-casdoor-directory-events-e2e.sh` |
| 账号停用 | 影子用户置 `isForbidden = true` 且**清空全部 Group**，签发前被拒；重新启用后恢复同一 `id`、同一 anchor 与原有 Group。**已签发的 access token（1 小时）与 refresh token（30 天）不会因此立即失效** | 停用清空 Group 与重新启用复用同一身份：`已验证`，入口同上（"disable and re-enable converge without replacing identity"）；已签发 token 的存活：`推断` |
| 账号删除 | 置 `isForbidden = true` 且 `isDeleted = true`，清空 Group，影子身份不可用。这是软删除，记录保留。**Consumer 侧的应用账号与资产不受影响** | `已验证`，入口同上（"delete forbids, soft-deletes, and clears access"） |
| 标识符回收再分配 | 新人的 anchor 不同，dirwatch 不会关联到旧影子用户，得到新的 Casdoor `id`（fail-closed）。但旧记录是软删除、仍占用用户名，用户名唯一约束下新记录的 `name` 会冲突——表现为同步失败而不是接错人 | anchor 关联而非按名关联：`已验证`，入口同上；回收用户名的冲突表现：`推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 停用或删除目录账号后，**Consumer 侧不会跟着收敛**。必须按各 Consumer 自己的
   《目录属性变更说明》执行撤权动作；Casdoor 只保证该用户拿不到新 token、且现有 OIDC session
   可被精确撤销；
2. 需要立即结束某人的会话时，在 Casdoor 管理界面删除其 session——对声明了 back-channel URI 的
   Consumer 会按精确 `sid` 传播；**SAML Consumer 没有 SLO 消费路径，只能本地登出**；
3. 已签发的 access/refresh token 在 TTL 内仍然有效，紧急撤权必须同时在 Consumer 侧动作；
4. **Consumer 侧约束**：接入 Casdoor 的 SAML Consumer **不得**用 `NameID` 作为持久身份键，必须
   请求并使用锚点属性。

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

- Module version / 版本：`3.143.0-r8`（reviewed 2026-08-27）
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
