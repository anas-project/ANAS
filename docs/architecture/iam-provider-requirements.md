# 新 IAM Provider 准入与实施要求

本文是 ANAS 引入第三种 IAM 实现时的验收契约。Provider 名称、管理 API 和内部对象
模型可以不同，但不得要求应用 Module 感知具体实现。

## 1. 身份与目录硬约束

1. Samba AD 是业务用户、业务组、账号启停状态和目录属性的唯一事实来源。IAM 本地
   账号只允许作为安装或应急账号，不得成为普通应用身份。
2. `mS-DS-ConsistencyGuid` 是 AD 二进制永久锚点，`anasIdentityAnchor` 是应用消费的
   UUID 文本投影。IAM 必须保留该值，不得用 `objectGUID`、SID、用户名、mail 或 `sub`
   替代跨系统身份键。
3. 用户名来自 `sAMAccountName`，显示名来自 `displayName`，邮件来自 `mail`。账号改名
   不得创建新应用身份。
4. `userPrincipalName` 必须遵循 `<sAMAccountName>@<AD DNS realm>`，但它是目录登录属性，
   不是应用用户名、邮件地址或永久身份键。Provider 不得因为 UPN 与 `mail` 当前恰好相同
   而合并两个字段，也不得把 UPN 作为 OIDC `preferred_username` 的默认来源。
5. `mail` 只表示已存在、唯一且可投递的邮箱。没有邮件 Module 或邮箱尚未创建时允许为空；
   邮件 Module 在邮箱创建、改址、迁移和停用后负责同步目录属性。Provider 不得从 UPN、
   `sAMAccountName` 或基础域名臆造 email claim。
6. 停用、锁定或不满足目录认证条件的账号必须在授权策略之前被拒绝。
7. 嵌套组属于正式契约。`用户 -> ROLE_x -> APP_y` 与直接加入 `APP_y` 等价；递归实现
   必须能处理循环，不能只检查用户的直接 `memberOf`。

## 2. 协议和应用注册能力

新 IAM 必须同时提供 OIDC 和 SAML IdP 能力，并实现通用 IAM capability：

- 按应用接收客户端 ID/Secret、redirect URI、logout URI、scope、claim 和允许组声明；
- 按消费方发布 issuer/discovery 或 SAML metadata/entity/SSO/SLO/证书，不假定所有应用
  共用一个 endpoint；
- 支持签名密钥持久化、轮换和可验证的公钥发布；
- 支持 authorization code flow，OIDC claim 同时可进入 ID token 和 UserInfo；
- 多值 claim 必须保持稳定 JSON 类型，`groups` 即使只有一个值也必须是数组；
- 不得要求 Nextcloud、MeshCentral、NetBird 等应用读取 Provider 私有环境变量。

每个应用的允许集合固定为：

```text
APP_<应用名> OR APP_all OR Admins
```

这里是固定 `any` 语义。Provider 不得把它解释成 AND，也不得隐式追加其他组。
`Admins` 是全局应用管理员角色。每个接入目录或 IAM 的 Module 都必须显式把它映射为该
应用的最高应用级管理员角色；成功登录本身不是权限依据。移出 `Admins` 后，权限必须在
下一次目录同步、token 刷新或登录时撤销，不能留下粘滞的本地管理员成员关系。

`Admins` 只表达应用管理权，不等同于 `Domain Admins`、宿主机 root、数据库超级用户或
`FS Admins`。例如 LAM 对 `Admins` 开放完整管理界面，但具体目录修改仍由登录用户的 AD
ACL 决定。`APP_all` 和 `APP_<应用名>` 始终只授予访问权，绝不提升应用管理员权限。

### 2.1 Module 管理员映射契约

| Module/入口 | `Admins` 的应用内含义 | 撤销方式 | 当前状态 |
| --- | --- | --- | --- |
| Authentik | `is_superuser=true` | LDAP Source 同步后收敛 | 已实现 |
| LLNG Manager | 可进入并管理全部 LLNG 配置 | 每次 Portal 授权重新判断目录组 | 已实现 |
| Nextcloud | LDAP administrative group，对应 Nextcloud `admin` 权限 | LDAP 组动态映射，不写本地粘滞成员 | 已实现 |
| MeshCentral | `siteadmin=0xffffffff` | OIDC 组同步并启用 `revokeAdmin` | 已实现 |
| LAM | 可进入完整 LAM 应用；目录操作仍受 AD ACL 约束 | 每次登录重新判断目录组 | 已实现 |
| oauth2-proxy 保护的管理面 | 进入受保护管理面即为应用管理权限 | 每次 IAM 授权重新判断目录组 | 按具体 Module 验证 |
| NetBird | NetBird 最高租户/账号管理员角色 | 组同步或登录时收敛 | `developing`，发布前必须实现并补 E2E |

仅支持本地账号的 Module 必须把该账号声明为 `primary` 或 `break_glass`；它不是 Samba
`Admins` 的替代品。只要 Module 后续接入 IAM，就必须遵守上表规则。

## 3. 通用目录和 Adapter

Runner 拥有 Provider 无关的应用目录与 IAM 注册事实，包括应用 ID、协议、URL、分类、
标签、图标、claim、允许组和可见性。新增 IAM 需要实现一个 adapter，将这些事实翻译为
自身的 client/RP/SP、策略和门户条目：

```text
应用 Module -> 通用 IAM client 声明 -> Runner 校验 -> IAM adapter -> Provider 对象
Provider endpoint -> 通用 binding -> 应用 Module
```

Adapter 必须幂等：重复执行会收敛到声明状态；修改 redirect URI、claim 或组策略后不能
留下仍可使用的旧配置。删除与弃用行为必须显式定义，不能依赖 Provider UI 中的人工操作。

## 4. Runner 必须校验的内容

- 只能选择一个 IAM Provider，并且必须由用户显式选择；
- Provider 同时声明 OIDC、SAML，应用所选协议存在于双方交集；
- client ID、redirect/logout URI、issuer/metadata 和必需 claim 非空；
- 应用 ID、client ID、目录条目 ID、分类和 endpoint 不发生冲突；
- 所有启用应用的允许组精确为其声明值，`Admins` 不得重复追加；
- `preferred_username`、`displayName`、mail、anchor 和 groups 的来源、必需性及类型正确；
- `preferred_username=sAMAccountName`、`email=mail`；UPN 只在应用明确声明需要目录 UPN 时
  作为单独 claim 发布，不得覆盖前两者或成为应用永久主键；
- 私钥和 client secret 只进入所属 Provider/应用的 Secret 与 render 环境；
- Provider Module 的生命周期不是 `deprecated`；若为 `developing`，计划输出必须明确提示
  其尚未达到发布质量。

## 5. 安全与运维要求

- Samba 连接必须使用 LDAPS、独立最小权限 bind 账号和证书校验；
- IAM 管理入口、应急账号、密钥轮换和恢复步骤必须文档化；
- 普通用户不能通过自注册、社交登录或本地数据库绕过 Samba AD；
- 认证失败、策略拒绝、client 配置变更和管理员操作必须可审计；
- Provider 不可用时不得静默回退到应用本地账号；应急入口必须使用独立、显式 URL；
- 备份必须覆盖 Provider 配置数据库、密钥和 Secret，恢复后原 anchor 映射保持不变。

## 6. 强制 E2E 验收矩阵

Authentik、LLNG 和任何新增 Provider 都必须在相互独立的部署中运行同一套语义测试：

| Samba AD 条件 | 期望 |
| --- | --- |
| 直接属于 `APP_<app>` | 只允许对应应用 |
| 属于 `APP_all` | 允许所有已启用应用 |
| 只属于 `Admins` | 允许所有应用，并在每个应用获得最高应用级管理员权限；不得附带域、主机或存储管理权 |
| 从 `Admins` 移除 | 仍可按 `APP_all`/`APP_*` 登录，但所有应用管理员权限在同步、刷新或再次登录后撤销 |
| `ROLE_x -> APP_<app>` | 与直接成员相同 |
| 无允许组 | IAM 策略拒绝，不创建应用账号 |
| 已停用但属于允许组 | 目录认证拒绝，不签发 token/assertion |
| 用户改名且 anchor 不变 | 原应用账号继续使用，不产生第二身份 |

每个应用还必须验证最终用户名、显示名、mail、anchor、组类型和内部权限；管理员测试必须
同时覆盖 `Admins` 正向授权、普通应用组不提权和移组后的撤销。只验证 HTTP 302
或门户可见性不算通过。E2E 同时检查通用声明、adapter 生成值和应用容器实际环境，防止
“声明正确但翻译或部署错误”。

## 7. 新 Provider 实施顺序

1. Module manifest 声明 IAM capability、协议、依赖和生命周期；
2. 实现通用 endpoint 发布与 client registration adapter；
3. 实现 Samba AD Source、递归组、账号状态和固定 claim mapping；
4. 加入 Runner 静态/渲染/Secret 边界校验；
5. 为每个可登录应用生成注册和门户目录条目；
6. 建立独立部署 fixture，完整运行上述矩阵；
7. 文档化密钥轮换、备份恢复、应急登录和弃用迁移后，方可从 `developing` 改为
   `release`。
