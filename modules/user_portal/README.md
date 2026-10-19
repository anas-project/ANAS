# ANAS 用户门户

独立门户用于有权限的应用、本人账号、LDAP 改密与范围受限的用户管理。资料、系统主邮箱、建号与管理员扩展已实现，验收基线保留在配套计划。

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

默认入口为 `https://portal.<base_domain>`。门户与 IAM 必须使用同站 HTTPS。ANAS 管理员角色默认启用；目录写入还需要单独配置专用绑定和 ACL。显式委派规则用于额外的范围受限角色。

## 身份与用户管理

OIDC Authorization Code + PKCE/S256。服务端验证签名、issuer、audience、nonce、请求绑定和有效期，浏览器只持有受保护 Cookie。匹配键为 `sub` 中的 immutable anchor，AD 名称和邮箱不参与会话绑定。

Casdoor 用户自己的 token 读取 UserInfo/get-account；后端不持有应用通用 API secret。卡片、搜索和图标在当前 IAM/AD 校验及固定正向组策略检查后返回。LDAP 改密使用原密码 delete/new add，不退化为 reset。ANAS 管理员写入使用专用受限绑定及近期认证；系统权限操作还要求本人域权限和逐次验证。额外委派角色继续要求完整规则。

退出先撤销门户会话，再从浏览器调用 discovery 的原生登出端点，不传递 token。IAM 失败时明确显示未确认，允许重试。原生 back-channel receiver 为 `/oidc/backchannel-logout`；真实双向登出和无浏览器撤销结论以配套计划中对应制品的 E2E 记录为准。

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


### 资料与 ANAS 管理员管理

个人资料允许修改名、姓、显示名；邮箱、登录名、锚点只读，未接邮件模块不提供别名写入。资料在写入前逐字复核版本及旧值，门户内串行写入；同一 LDAP Modify 检查旧属性值。外部目录工具的并发不属于全局事务保证。分类开关同时控制用户应用和后台管理的小分类，保留两大组并记住当前浏览器偏好；密码/资料结果显示在表单旁。未支持语言仍回退英文。

`administration_enabled` 使用目录 Provider 注册的管理员组并绑定稳定标识，不以 Casdoor 原生管理员或名称前缀授权。默认管理列表只展示明确注册的 ANAS 组，按应用、能力、文件资源和角色分类；高级视图说明系统权限风险。不能重置本人密码、移出自身管理员路径或移除最后一个可确认的可用管理员。计算机、服务和信任主体不进入人员管理。

启用写入前，部署者在选定 Samba DC 内运行 `tools/provision-directory.py`，以私有标准输入提供确认串、实际 base_dn 和 profile/reset/group/create 四个随机密码；确认串为 `provision-user-portal-scoped-writers`。脚本创建 `svc_portal_profile`、`svc_portal_reset`、`svc_portal_group`、`svc_portal_create`，只增加特定属性/对象 ACL，不给服务账号域角色、不修改已有服务密码。随后将四个 DN/密码填入对应门户配置并重新部署。输入须保留在 0700 目录/0600 文件，不进入命令行、Git 或日志。该脚本是管理员运行的目录配置工具，不是门户 API。

姓名绑定只写三个姓名属性；密码绑定写普通人员的 reset/pwdLastSet/lockoutTime；成员绑定只写已注册且非域级组的 member。初始配置显式拒绝密码绑定管理现有域级人员。以后在门户外新增高权限人员或更改权限边界时，部署者须重新核对 ACL；目录 ACL 不能按 member 值限制目标，也不能代替门户每次动态权限检查。服务账号没有全域权限不代表凭据泄露没有风险。

`domain_administration_enabled` 默认关闭；开启后受保护人员/系统组走独立确认：近期 OIDC、限时一次性参数确认、同一登录人员的本人 LDAPS 绑定、原生 AD ACL。密码只用于一个请求，不跨请求保存；不是 MFA 或进程隔离的保证。推荐的专用受限绑定配置不长期持有全域管理凭据。组关系之外的自定义 ACL 权限仍以 AD 的最终拒绝/允许为准。

## 管理员登录与 IAM 故障恢复

没有本地管理员或可绕过 IAM 的直接管理入口。IAM 故障时使用 Casdoor 自身的恢复流程；门户只允许重试退出，不启用降级授权。

## 管理命令

复用 `anas config list user_portal --json`、`anas config set`、`anas plan` 和 `anas apply`。没有任意 LDAP/DN/命令执行接口。

## 数据库支持

无数据库 Consumer/Provider。用户、组和密码由 Samba AD 管理。

## 所有可用配置参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.administration_enabled` | bool | — | `true` | `static` | `USER_PORTAL_ADMINISTRATION_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 启用受管 ANAS 管理员角色；实际写入仍要求专属 ACL 和绑定 |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | 否 | 否 | 否 | 是 | `container_recreate` | 私有审计容量；满额关闭写入 |
| `user_portal.create_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 在 Provider 用户 OU 创建普通人员的指定授权绑定 DN |
| `user_portal.create_bind_password` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 创建绑定密码；不交付浏览器 |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | 否 | 否 | 否 | 是 | `container_recreate` | 完整委派规则 JSON 数组；默认关闭 |
| `user_portal.domain_administration_enabled` | bool | — | `false` | `static` | `USER_PORTAL_DOMAIN_ADMINISTRATION_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 显式启用本人域权限和逐次凭据验证；默认关闭 |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 门户域名前缀 |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属单成员写绑定 DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属成员写绑定凭据，不交付浏览器 |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 固定 OIDC 接入 |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 默认中英文选择，不支持时英文回退 |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属密码/解锁写绑定 DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属密码写绑定凭据，不交付浏览器 |
| `user_portal.profile_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 仅具 givenName、sn、displayName 写权的专属绑定 DN |
| `user_portal.profile_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 资料专属写绑定密码；不交付浏览器 |
| `user_portal.protected_groups` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_GROUPS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护分组的 anchor JSON 数组 |
| `user_portal.protected_users` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_USERS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护人员的 anchor JSON 数组 |
| `user_portal.recent_window_seconds` | int | `5..300` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | 否 | 否 | 否 | 是 | `container_recreate` | 近期认证窗口；默认 300 秒 |

`delegation_rules` 是 JSON 数组：每条包含 id、operator_groups、actions、target_users、target_user_groups、editable_groups，引用为稳定 anchor。动作固定为 users.read、password.reset、account.unlock、groups.member.add、groups.member.remove、audit.read。空目标不授权，规则不能拼接。操作者组、范围组与其保护关系不可修改。

密码/成员绑定必须是门户专用服务账号，并由目录管理员授予仅所需 OU、对象和属性的权限。不要提供 Samba Administrator 或共享只读/改密账号的扩权凭据。当前不自动创建或轮换这些手工绑定。

## 存储、备份、验证与限制

只有 `${DATA_PATH}/user_portal/audit` 持久化，私有 0700/0600 JSONL 意图与结果逐次 fsync。随 workspace data 备份，恢复清空内存登录会话。写意图失败不写目录；结果保存失败报告真实结果并停止后续写入。损坏、残缺或容量已满不自动清理、修复或重放。

审计默认容量 64 MiB，可用 audit_capacity_mb 调整。满额时先停止门户、备份并人工核对未完成记录，再按受管维护流程导出；不要直接删除未完成记录。当前没有自动轮转/清理接口。

单实例；重启要求重新登录。每次 LDAP 写入前重新核验，GUID 单成员修改不提供跨对象事务保证。Traefik 地址改变后需要重启门户更新精确可信 peer。测试入口见技术文档，完整验收见[门户计划](dev-docs/plans/archived/user-portal.md)。

## 时区与语言

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.1.0-r1`（reviewed 2026-10-16）
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

### 系统主邮箱与建号

`samba_dc.mail_domain` 缺省继承 AD DNS 域。例如 finance.hlong.wang 的 admin 获得 `admin@finance.hlong.wang`；设置为 hlong.wang 后，指定人员 OU 的主地址按 `用户名@hlong.wang` 维护。它不改变 AD 域、UPN、登录名或锚点。`mail` 完全由系统管理，页面不接受手工主地址；地址存在不代表邮件服务已经开通。

人员枚举、详情、姓名、密码、分组及域级操作均以 Provider 的用户 OU 及子 OU 为边界。ANAS 管理员可用“新建用户”创建普通人员；后端使用 `create_bind_dn/password` 指定的授权绑定，而不是使用前台操作者的域权限。推荐独立 `svc_portal_create`，仅授予用户 OU 的 user 创建权限。配置账号也可已有相应 AD 授权；门户角色和后端目录授权分别检查。若指定域管理员作为创建绑定，门户进程会长期持有该账号既有权限，逐次域级验证并不能隔离这份凭据；默认专用受限绑定可避免这一点。

`POST /api/users` 只接收 `username`、三项 `profile`、`force_change`。登录名限 20 位 ASCII 字母、数字、下划线、点、连字符，并须能组成系统邮箱。邮箱、UPN 后缀、目标 DN、初始随机密码和锚点由服务器决定，一次原生 LDAP Add 写入；不授予管理组。该动作需要近期认证、CSRF、当前管理员权限和持久审计；冲突或结果未知不得自动重试。临时密码只在成功响应显示一次，默认要求下次修改。

建号授权边界：finance Samba 4.23.6 的专用创建者同时成为新用户对象所有者，能修改该对象的 DACL（访问控制表）；临时原生 Owner Rights 规则未移除该能力。因此仅配置子对象创建 ACL 不能证明与后续高权限账号严格隔离。门户在添加域风险组成员前，使用操作者本人域绑定将用户对象所有者移交给同域 Domain Admins，回读确认后再添加成员；成员操作失败保留新所有者，分别报告结果。移交不会撤销自定义创建绑定已有的其他 ACL 或组权限。没有向正式 OU 添加全局 Owner Rights 规则，也没有新增 Casdoor 补丁，详情见[技术文档](docs/technical.md#域风险组的对象所有权)。

普通用户操作和 ANAS 管理员角色变更可复用 5 分钟内真实认证；角色变更仍须页内一次性确认，无需重复密码。系统/操作系统权限组及受保护账号操作保留每次本人域密码和原生 AD 权限验证。本人改密仍需原密码。退出结果页提供“返回主页”，IAM 失败保留重试且不报告完整成功。跨应用退出的实测范围及限制见配套计划 §19。

退出结果会分别显示 IAM 及已接入原生退出桥的应用；超时或仍有有效会话时显示未确认，并提供重试和返回主页。当前退出桥支持 Nextcloud、Immich，清理当前浏览器的旧会话，不撤销其他设备。应用原生发起来源及 finance 实机结果见配套计划，不据此宣称全 Module 矩阵已经完成。
