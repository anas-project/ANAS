---
doc_type: requirement
status: current
created: 2026-08-26
updated: 2026-08-27
---

# Casdoor IAM Provider 集成要求

本文规定 ANAS 集成 Casdoor 时必须满足的 Module、目录同步、OIDC/SAML、授权、会话、安全和
发布验收边界。通用 Provider 约束见[新 IAM Provider 准入与实施要求](iam-provider.md)，应用侧
登出契约见[使用 OIDC/SAML 的 Module 双向登出要求](module-iam-bidirectional-logout.md)，实施进度
和执行证据见[Casdoor IAM Provider 实施计划](../plans/archived/casdoor-iam.md)。

本文只规定 Casdoor 的可判定结果，不重复实现进度。关键词“必须”“不得”具有规范性。

## 1. 交付范围与生命周期

Casdoor Module 为 ANAS 提供通用 `iam` Capability 的 OIDC 和 SAML 接口，消费 Samba AD、
Traefik 与独立 PostgreSQL Resource。Samba AD 始终是业务用户、组、账号启停状态和目录属性的
唯一事实来源；Casdoor 的本地影子用户、登录会话和恢复管理员不改变这条边界。

首期必须交付固定上游版本、可重复生成的配置、受信任 LDAPS、OIDC/SAML Consumer 注册、
目录事件低延迟同步、周期同步兜底、受管本地恢复账号和可复现 E2E。未完成本矩阵中的发布验收
前，Module 必须保持 `developing`，不得使用“发布级”“生产支持”或等价措辞。

固定版本不具备或尚未验证的能力必须保持关闭并明确记录。尤其不得把普通退出页面声明为 OIDC
back-channel endpoint，不得发布未经验证的 SAML SLO，也不得把 Casdoor 本地用户 ID 描述为
Samba 永久身份锚点。

## 2. Module、数据与安全边界

Casdoor 只经 Traefik HTTPS 提供公网入口，不发布额外宿主端口。业务进程和目录订阅器必须以
非 root 身份运行；初始化过程可以短暂使用 root 准备 `0600` 文件和挂载点，但必须在启动业务
进程前降权。健康检查必须访问 Casdoor HTTP health endpoint，不能只检查 PID。

业务数据库必须来自 `relational_database` Contract 的独立 PostgreSQL Resource，删除策略为
`retain`。改变数据库类型或名称必须进入显式迁移，不得静默切换到空数据库。数据库、签名密钥、
Consumer Secret、目录订阅游标、本地管理员库存和 deployment metadata 必须纳入一致的备份恢复点。

ANAS revision 8 必须从 Casdoor `3.143.0` 对应提交
`1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 构建并校验源码归档 SHA-256
`365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`。仓库补丁集只允许扩展
SAML 模板对已同步 `displayName`/`externalId` 的读取，以及完成 OIDC `sid`、用户/管理员
back-channel、两分钟 Logout Token、失败可观测性和 PostgreSQL 保留列安全查询；固定提交、校验和、
每个补丁和最终服务端替换都必须有静态测试，构建代理不得改变源码身份。

生成 Secret 必须使用加密随机源并在重复 calculate/render 时保持稳定。LDAP Bind 密码、数据库
密码、签名私钥、Consumer Secret、订阅器 API Secret 和恢复密码不得进入普通日志、计划、
deployment manifest 或进程 argv，也不得投影给无关 Module。

## 3. Samba 目录与事件同步

Casdoor 必须通过受信任 LDAPS 使用受限 Bind，过滤非用户对象、禁用账号和缺少
`anasIdentityAnchor` 的账号。不得启用 LDAP/AD 密码写回，也不得允许 Casdoor 修改 Samba 的人员、
组或账号状态。

目录订阅器只读 Samba 发布的持久 JSONL 事件日志，使用自己的持久游标。它必须过滤相关
`Add`、`Modify`、`Delete` 事件，对关注属性做防抖和最小调用间隔控制；日志轮换、进程重启和
不完整尾记录不能造成事件丢失或重复同步风暴。只有 LDAP 同步和本批 profile 刷新全部成功后
才能原子提交游标，失败必须保留事件并重试。

目录事件路径是周期 LDAP 同步的低延迟加速器，不是第二事实来源。同步既有用户时，只允许按本批
事件刷新 `displayName` 和 `email`；不得覆盖密码、权限或其他人工字段。账号删除、停用、组撤权和
改名必须通过真实目录与 Casdoor E2E 证明最终收敛，不能以“触发过同步 API”代替结果断言。

## 4. OIDC、SAML、授权与身份锚点

OIDC 必须按 Consumer 发布 issuer/discovery，并注册精确 redirect URI、post-logout redirect URI、
confidential client 和通用 claim。SAML 必须按 Consumer 发布 metadata、entity ID、SSO URL 和
签名证书，注册准确的 SP entity ID、ACS URL 与显式属性映射。协议切换或声明删除后必须清理旧
协议字段和旧会话通知 endpoint。

Casdoor 必须在签发 token 或 assertion 前执行通用 `ALLOW_GROUPS`：直接 `APP_<app>`、`APP_all`、
`Admins` 和递归组成员允许，无允许组和禁用账号拒绝。`Admins` 必须映射到各应用最高应用级管理员，
但不得获得域、主机或存储管理权；移出 `Admins` 后应用内管理员权限必须撤销。

OIDC 与 SAML 必须使用同一个 Samba 永久锚点语义。用户改名而锚点不变时，既有应用账号必须继续
使用且不能产生第二身份。未知属性映射不得退化为 Casdoor 本地 ID 后仍声称满足永久锚点要求。

真实登录 E2E 必须保存并检查最终用户名、显示名、邮件、锚点、组类型和应用内权限。只验证
discovery、metadata、HTTP 302、门户可见性或 Casdoor 本地建号均不算通过。

## 5. 登出与会话撤销

Casdoor 只能为 Consumer 明确声明且固定版本真实支持的机制登记通知 endpoint。OIDC Consumer
声明 back-channel 时，Casdoor 必须登记准确 URI；声明消失或切换 SAML 时必须清空旧 URI。
真实验收必须分别覆盖用户正常退出和管理员无浏览器删除 IAM session，并继续使用原应用 Cookie
断言目标会话失效且不影响其他用户、client 或 session。

OIDC Logout Token 必须具有可验证的签名、`iss`、`aud`、logout event、时效、`sid`/`sub` 和防重放
语义。SAML SLO 只有在 metadata、签名验证、NameID、SessionIndex、Destination、RelayState 与应用
会话失效 E2E 全部成立后才能发布；浏览器 Redirect/POST 流程不得描述为管理员后台撤销。

## 6. 恢复、升级与发布

本地恢复账号使用账号 inventory 的默认 `admin_{module}` 模板，即 `admin_casdoor`。它是独立生成、
可事务轮换的 break-glass 凭据，不要求保留 Casdoor 上游内置用户名。密码只通过 Secret 文件或
stdin 进入 Helper；更新后必须回读 bcrypt 验证，失败必须恢复旧密码。

发布验收必须在明确指定且隔离的服务器上覆盖真实恢复登录、密码轮换、备份恢复、多架构构建、
固定版本升级与安全回滚。恢复后原 issuer、签名验证、Consumer 注册、目录游标、永久锚点映射和
恢复账号必须继续有效。运维文档必须记录密钥轮换、备份恢复、IAM 故障恢复、Provider 切换与
弃用迁移边界。

只有本矩阵全部强制项具有可复现证据、支持清单与 Module 文档同步后，才可以把生命周期改为
`release`。无法由固定版本实现的项目必须先调整产品支持范围及需求，而不能直接跳过验收。

## 7. 需求矩阵

本矩阵是规范来源，正文是解释。两者冲突以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `CASDOOR-R-001` | Module 必须固定 Casdoor `3.143.0` 提交、源码 SHA-256、受控 SAML/OIDC/PostgreSQL 兼容补丁集与 ANAS revision `8`，不得使用 `latest`；发布验收未完成时状态必须为 `developing` | CI |
| `CASDOOR-R-002` | Module 必须提供通用 `iam` Capability 的 `oidc`、`saml` 接口，并显式依赖 Traefik、Samba DC 和 `relational_database` Contract | CI |
| `CASDOOR-R-003` | Casdoor 必须使用独立 PostgreSQL Resource 和生成凭据，删除策略为 `retain`；改变数据库类型或名称必须进入显式迁移 | 单元 |
| `CASDOOR-R-004` | Casdoor 只经 Traefik HTTPS 暴露，业务进程与目录订阅器以非 root 运行，健康检查必须访问应用 HTTP health endpoint | 单元 |
| `CASDOOR-R-005` | Hook 生成的签名材料、Portal/订阅器凭据必须使用加密随机源、重复计算保持稳定，并保持 Secret 敏感性和 Module 隔离 | 单元 |
| `CASDOOR-R-006` | Samba AD 必须是业务用户、组、账号状态和目录属性的唯一事实来源；Casdoor 不得启用目录密码写回 | 审阅 |
| `CASDOOR-R-007` | LDAP Source 必须使用受信任 LDAPS 和受限 Bind，并过滤非用户、禁用及缺少 Samba 永久锚点的账号 | 单元 |
| `CASDOOR-R-008` | 目录订阅器必须只读 Samba 持久事件日志并使用独立持久游标，不得把 Casdoor API Secret 暴露给 Samba 生产者 | 单元 |
| `CASDOOR-R-009` | 目录订阅器必须过滤相关 `Add`、`Modify`、`Delete` 和关注属性，并执行防抖与最小同步间隔 | 单元 |
| `CASDOOR-R-010` | 目录日志轮换、进程重启和不完整尾记录不得丢失事件或重放已提交序号；游标必须以 `0600` 原子写入 | 单元 |
| `CASDOOR-R-011` | 只有 LDAP 同步与本批 profile 刷新全部成功后才能提交游标；失败必须保留待处理事件并重试 | 单元 |
| `CASDOOR-R-012` | 既有用户 profile 刷新只允许修改 `displayName` 和 `email`，不得覆盖密码、权限或其他人工字段 | 单元 |
| `CASDOOR-R-013` | 周期 LDAP 自动同步必须保留为兜底，目录事件订阅只能作为低延迟加速器 | 单元 |
| `CASDOOR-R-014` | 真实目录事件 E2E 必须验证新增导入、既有用户 profile 刷新、突发事件合并和重启游标恢复 | e2e |
| `CASDOOR-R-015` | 真实目录 E2E 必须验证账号删除和停用在限定时间内传播到 Casdoor，且旧账号不能继续认证或签发新 token/assertion | e2e |
| `CASDOOR-R-016` | 真实目录 E2E 必须验证组加入、移除和递归组变化传播，并在撤权后停止签发对应应用凭据 | e2e |
| `CASDOOR-R-017` | OIDC 必须发布部署级 issuer/discovery，并为每个 Consumer 注册 confidential client、精确 redirect URI 和 post-logout redirect URI | 单元 |
| `CASDOOR-R-018` | OIDC Application 配置必须使用 RS256、非零且受管的 token 有效期，并把用户名、显示名、邮件、组和扩展属性列入 token 字段 | 单元 |
| `CASDOOR-R-019` | 真实 OIDC Authorization Code 登录必须验证允许与拒绝账号、claim、应用建号和最终应用权限，不能以 HTTP 302 代替 | e2e |
| `CASDOOR-R-020` | SAML 必须发布按 Consumer 区分的 metadata/entity ID、SSO URL 和签名证书，并注册精确 SP entity ID、ACS URL、POST binding 与 assertion 签名 | 单元 |
| `CASDOOR-R-021` | SAML 属性映射必须显式处理用户名、邮件和组；未知来源不得冒充已验证的 Samba 永久锚点 | 单元 |
| `CASDOOR-R-022` | 真实 SAML 登录必须验证 assertion 签名、Destination、Issuer、Audience、NameID、属性和最终应用权限 | e2e |
| `CASDOOR-R-023` | OIDC 与 SAML 必须对直接 `APP_<app>`、`APP_all`、`Admins`、递归组、无允许组和禁用账号执行通用 `ALLOW_GROUPS` 准入语义 | e2e |
| `CASDOOR-R-024` | 用户改名且 Samba 永久锚点不变时，OIDC 与 SAML Consumer 必须继续使用原应用身份且不得创建重复账号 | e2e |
| `CASDOOR-R-025` | `Admins` 必须映射为最高应用级管理员且不附带基础设施权限；移出 `Admins` 后各应用管理员权限必须撤销 | e2e |
| `CASDOOR-R-026` | OIDC back-channel URI 只能在 Consumer 同时声明 URI 和 `backchannel` 方法时登记；声明删除或协议切换后必须清空旧 URI | 单元 |
| `CASDOOR-R-027` | 用户从 Casdoor 正常登出后，所有声明 OIDC back-channel 的目标应用必须拒绝登出前保存的 Cookie | e2e |
| `CASDOOR-R-028` | 管理员无用户浏览器参与删除 Casdoor session 后，声明 OIDC back-channel 的目标应用必须拒绝原 Cookie | e2e |
| `CASDOOR-R-029` | Casdoor 发出的 OIDC Logout Token 必须具有可验证的签名、`iss`、`aud`、logout event、时效、`sid`/`sub` 和防重放语义，且只影响目标会话 | e2e |
| `CASDOOR-R-030` | 未通过真实 SAML SLO 验收时不得发布 SLO endpoint、binding 或后台撤销声明 | 单元 |
| `CASDOOR-R-031` | 声明 SAML SLO 前必须通过带原应用 Cookie 的签名 `LogoutRequest`/`LogoutResponse` 浏览器流程，并断言应用会话失效 | e2e |
| `CASDOOR-R-032` | break-glass 账号必须由本地账号 inventory 以默认模板生成 `admin_casdoor`，密码独立生成且不得声明不必要的 `fixed_username` | 单元 |
| `CASDOOR-R-033` | break-glass apply/rotate 必须通过 Secret 文件或 stdin 传递候选密码，写入 bcrypt 后回读验证，失败时恢复旧密码且密码不进入 argv | 单元 |
| `CASDOOR-R-034` | 真实恢复 E2E 必须验证 `admin_casdoor` 可登录、成功轮换后新密码生效旧密码失效、失败轮换恢复旧密码 | e2e |
| `CASDOOR-R-035` | 真实备份恢复必须在空 workspace 恢复数据库、签名材料、Consumer Secret、目录游标、本地管理员库存和 deployment metadata，并保持原身份锚点映射 | e2e |
| `CASDOOR-R-036` | amd64 与 arm64 必须完成固定源码构建；真实部署必须验证冷启动、重启、固定版本升级和安全回滚不破坏用户、client、签名与目录状态 | e2e |
| `CASDOOR-R-037` | 签名密钥与受管 client credential 的轮换必须具有明确事务、信任重叠和失败恢复语义，并通过真实登录与旧凭据失效 E2E | e2e |
| `CASDOOR-R-038` | 运维文档必须记录密钥轮换、备份恢复、IAM 故障恢复、Provider 切换、弃用迁移和所有未支持能力 | 审阅 |
| `CASDOOR-R-039` | 只有所有强制发布验收具有可复现证据后，Module 生命周期才能从 `developing` 改为 `release` | CI |
| `CASDOOR-R-040` | Module README、技术文档、IAM 支持清单和需求计划必须使用一致的版本、生命周期与能力限制描述 | 审阅 |
