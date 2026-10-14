# ANAS 用户门户

独立门户用于有权限的应用、本人账号、LDAP 改密与范围受限的用户管理。当前为开发中的候选，尚未通过全部首版验收。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `user_portal` |
| 版本 / revision | `0.1.0-r1` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

需要 Traefik、Samba AD 和 Casdoor；只接入 OIDC。首版的当前本人读取使用 Casdoor 原生 API，暂不支持其他 IAM。没有数据库。

## 最简配置

```yaml
modules:
  user_portal: {}
```

默认入口为 `https://portal.<base_domain>`。门户与 IAM 必须使用同站 HTTPS。未配置委派规则时只提供应用与本人功能。

## 身份与用户管理

OIDC Authorization Code + PKCE/S256。服务端验证签名、issuer、audience、nonce、请求绑定和有效期，浏览器只持有受保护 Cookie。匹配键为 `sub` 中的 immutable anchor，AD 名称和邮箱不参与会话绑定。

Casdoor 用户自己的 token 读取 UserInfo/get-account；后端不持有应用通用 API secret。卡片、搜索和图标在当前 IAM/AD 校验及固定正向组策略检查后返回。LDAP 改密使用原密码 delete/new add，不退化为 reset。管理员密码和成员写入需要单独受限绑定、显式完整规则及近期认证。

退出先撤销门户会话，再从浏览器调用 discovery 的原生登出端点，不传递 token。IAM 失败时明确显示未确认，允许重试。原生 back-channel receiver 为 `/oidc/backchannel-logout`；真实双向登出和无浏览器撤销结论以配套计划的 E2E 为准，当前不宣称通过。

| 目录侧变更 | 当前实现及证据等级 |
| --- | --- |
| sAMAccountName | 按 anchor 保持身份，下次账号读取返回当前名称 |
| mail | 返回 AD 实际值，不代造，不参与绑定 |
| displayName/profile | 每次账号读取更新，不保存人员副本 |
| 直接/递归分组 | 每次 API 用当前 AD 关系，权限需同时符合当前 IAM |
| 停用 | 每次 API 拒绝当前停用人员并撤销门户会话 |
| 删除 | 查不到 anchor 即拒绝；没有门户用户资产 |
| 标识符回收 | 不按名称或 DN 替换 anchor；稳定 GUID 用于写入 |

兜底：身份依赖失败关闭访问；修复目录/IAM 后重新登录。变更不宣称撤销其他应用的 token、SSH key 或资产。

## 管理员登录与 IAM 故障恢复

没有本地管理员或可绕过 IAM 的直接管理入口。IAM 故障时使用 Casdoor 自身的恢复流程；门户只允许重试退出，不启用降级授权。

## 管理命令

复用 `anas config list user_portal --json`、`anas config set`、`anas plan` 和 `anas apply`。没有任意 LDAP/DN/命令执行接口。

## 数据库支持

无数据库 Consumer/Provider。用户、组和密码由 Samba AD 管理。

## 所有可用配置参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | 否 | 否 | 否 | 是 | `container_recreate` | 私有审计容量；满额关闭写入 |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | 否 | 否 | 否 | 是 | `container_recreate` | 完整委派规则 JSON 数组；默认关闭 |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 门户域名前缀 |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属单成员写绑定 DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属成员写绑定凭据，不交付浏览器 |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 固定 OIDC 接入 |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 默认中英文选择，不支持时英文回退 |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属密码/解锁写绑定 DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属密码写绑定凭据，不交付浏览器 |
| `user_portal.protected_groups` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_GROUPS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护分组的 anchor JSON 数组 |
| `user_portal.protected_users` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_USERS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护人员的 anchor JSON 数组 |
| `user_portal.recent_window_seconds` | int | `5..900` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | 否 | 否 | 否 | 是 | `container_recreate` | 近期认证窗口；默认 300 秒 |

`delegation_rules` 是 JSON 数组：每条包含 id、operator_groups、actions、target_users、target_user_groups、editable_groups，引用为稳定 anchor。动作固定为 users.read、password.reset、account.unlock、groups.member.add、groups.member.remove、audit.read。空目标不授权，规则不能拼接。操作者组、范围组与其保护关系不可修改。

密码/成员绑定必须是门户专用服务账号，并由目录管理员授予仅所需 OU、对象和属性的权限。不要提供 Samba Administrator 或共享只读/改密账号的扩权凭据。当前不自动创建或轮换这些手工绑定。

## 存储、备份、验证与限制

只有 `${DATA_PATH}/user_portal/audit` 持久化，私有 0700/0600 JSONL 意图与结果逐次 fsync。随 workspace data 备份，恢复清空内存登录会话。写意图失败不写目录；结果保存失败报告真实结果并停止后续写入。损坏、残缺或容量已满不自动清理、修复或重放。

审计默认容量 64 MiB，可用 audit_capacity_mb 调整。满额时先停止门户、备份并人工核对未完成记录，再按受管维护流程导出；不要直接删除未完成记录。当前没有自动轮转/清理接口。

单实例；重启要求重新登录。每次 LDAP 写入前重新核验，GUID 单成员修改不提供跨对象事务保证。Traefik 地址改变后需要重启门户更新精确可信 peer。测试入口见技术文档，完整验收见[门户计划](dev-docs/plans/user-portal.md)。

## 时区与语言

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.1.0-r1`（reviewed 2026-10-14）
- Timezone / 时区：`container` — TZ is supplied to the process; audit timestamps use UTC and the browser displays local time.
- Language scope / 语言范围：All portal pages and catalog presentation
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：BCP 47
- Fallback / 回退：Unsupported selected languages immediately use English. Individual catalog fields fall back to English then simplified Chinese.
- Supported languages / 支持语言（2）：`en`, `zh-CN`
- Notes / 说明：Changing language does not replace the authenticated identity.

Evidence / 证据：

- [v0.1.0 — modules/user_portal/portal/ui/app.js](https://github.com/anas-project/ANAS/blob/master/modules/user_portal/portal/ui/app.js)
<!-- generated:localization:end -->

中英文即时切换，不支持的所选语言使用英文。技术细节见[技术文档](docs/technical.md)。
