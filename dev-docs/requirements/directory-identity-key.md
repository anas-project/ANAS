---
doc_type: requirement
status: current
created: 2026-09-20
updated: 2026-10-05
---

# 目录身份键要求

本文规定 ANAS 里的每一个 Module 用什么值来认定"这是同一个人"。适用于所有用户来自 Samba AD 的
Module——直接读 LDAP/LDAPS 的,以及经 IAM 的 OIDC/SAML 接入的,义务相同。只有本地账号、用户
不来自目录的 Module 不在范围内。

锚点属性本身的 OID、schema 与目录迁移由[Samba 身份锚点要求](samba-identity-anchor.md)规定,
本文不重复;本文只规定**消费侧**怎么用它。事件订阅与撤权时效由
[目录事件订阅要求](directory-event-subscription.md)规定。

关键词"必须""不得""应该"具有规范性。ID 一经分配即固定,废弃的需求保留行并标 `已废弃`。

## 1. 只有一个身份键

`anasIdentityAnchor` 是本部署唯一的永久身份键。它由 Anchor Worker 从 `mS-DS-ConsistencyGuid`
(以 `objectGUID` 原始 16 字节初始化)按 AD GUID 字节序投影成 UUID 文本,对象改名、移动 OU、
改邮箱都不会改变它。

`sAMAccountName`、UPN、`mail`、`displayName` 和 DN 都是**登录、搜索与显示用的标签,不是身份**。
它们可变、可回收、可重新分配。任何把标签当身份键的地方,都会在改名时把一个人认成两个人,在
标识符回收时把两个人认成一个人。这不是洁癖问题:前者让人丢掉自己的资产,后者让新人继承别人
的权限。

## 2. 一个键,四跳,不派生

```text
AD: mS-DS-ConsistencyGuid ──► anasIdentityAnchor
      └─► IAM LDAP source 的对象唯一性字段 = anchor
            └─► OIDC sub / SAML NameID = anchor
                  └─► 应用的持久绑定字段 = anchor
```

**IAM 发出的主体标识符必须就是 anchor 本身**,不是"另一个同样稳定的值"。差别不在措辞:取
Provider 内部 id 也能做到跨改名稳定,但那样 Consumer 手里的键就无法直接和目录对账,必须再建一张
`sub ↔ anchor` 映射表、一个中立契约扩展,以及一条每次撤权都要在线的查询路径。让主体标识符直接
取 anchor,这三样零件全部不需要——Consumer 落进绑定字段的值**已经**是可以拿去和 LDAP 取差集
的值。

LDAP 直连的 Module 把 anchor 作为 LDAP 唯一性属性,走的是同一个键。两条链路因此落在同一个值上,
这也是"LDAP 预配 + OIDC 登录"双链路唯一可能安全成立的前提。

代价只有一处,必须逐个 Consumer 检查:有些应用把主体标识符直接当成应用内用户 id,而那个 id 可能
出现在 URL 或文件路径里。`DIRKEY-R-010` 默认禁止这种投影,`DIRKEY-R-013` 要求在切换前逐个验证。
2026-10-05 用户确认 Nextcloud 全新部署的内部 UID 使用 anchor：允许它出现在该应用的
WebDAV/API 技术路径和内部存储目录，但登录名与显示名继续使用目录的人类可读属性。
此例外不扩大到其他 Consumer，也不允许未经验证地更换既有账号 UID。

## 3. Module 的义务

Module 在自己的持久状态里记录一个目录用户或组时,键必须是 anchor。判据是"目录里改个名,这个键
变不变",不是"它看起来够不够唯一"。

上游不提供可配置身份字段的 Module,**不得用"按用户名或邮箱回退匹配"冒充满足本要求**。这类
Module 必须显式声明缺口、写明后果,并在每次固定版本升级时复核上游是否已经补上。

只减权、不授权的对账或撤权逻辑是唯一的例外:在拿不到 anchor 时它可以按标签匹配,因为匹配错误
的后果是误撤权(吵闹、可恢复)而不是误授权。但它必须只走撤权方向,恢复走人工。

## 4. 需求矩阵

本矩阵是规范来源,正文是解释。两者冲突以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `DIRKEY-R-001` | `anasIdentityAnchor` 是唯一的永久身份键;`sAMAccountName`、UPN、`mail`、`displayName` 与 DN 只用于登录、搜索和显示,不得作为任何持久身份键 | 审阅 + e2e |
| `DIRKEY-R-002` | Module 持久化目录用户或组时,键必须是 anchor;判据是"目录改名后该键是否不变" | 静态 + 审阅 |
| `DIRKEY-R-003` | 目录账号改名后 Module 必须复用原有应用身份,不得重复建号或产生孤儿账号 | e2e |
| `DIRKEY-R-004` | 上游不提供可配置身份字段的 Module 不得以用户名/邮箱回退匹配冒充满足 `DIRKEY-R-002`,必须在 README 与技术文档声明缺口、技术阻碍与由此产生的改名、回收、撤权后果 | 审阅 |
| `DIRKEY-R-005` | 声明了缺口的 Module 必须在每次变更上游固定版本时复核是否已出现可配置的不可变身份字段,并把结论写回文档 | 审阅 |
| `DIRKEY-R-006` | 只减权、不授权的对账或撤权逻辑可以在缺少 anchor 时按标签匹配,但必须只执行撤权方向、声明误判风险,且不得据此自动恢复访问 | 单元 + 审阅 |
| `DIRKEY-R-007` | IAM Provider 的 LDAP source 必须以 anchor 作为对象唯一性字段,不得使用 `sAMAccountName`、DN 或 `mail` | 单元 + e2e |
| `DIRKEY-R-008` | IAM Provider 签发的 OIDC `sub` 与 SAML `NameID` 必须就是 anchor 值本身,不得是 Provider 内部 id、用户名或邮箱;不得改用"内部 id 加 `sub ↔ anchor` 映射"替代 | 单元 + e2e |
| `DIRKEY-R-009` | Provider 必须能把 anchor 作为 claim/attribute 发给明确请求它的 Consumer;请求了而 Provider 无法提供时必须 fail closed,不得静默降级为标签 | 单元 |
| `DIRKEY-R-010` | anchor 不得被投影为登录名、显示名、URL 中的标识符或文件路径;它只出现在应用的内部绑定字段与管理视图。Nextcloud 全新部署例外：允许内部 UID、WebDAV/API 技术路径及内部存储目录使用 anchor，登录名和显示名仍取人类可读属性 | 审阅 |
| `DIRKEY-R-011` | 每个在范围内的 Module 必须在 README 的《目录属性变更说明》记录自己实际使用的匹配键及其证据等级(已验证/推断),不得留空或写"由 IAM 负责" | 审阅 |
| `DIRKEY-R-012` | provider-neutral 的 IAM 契约必须声明主体标识符取自 anchor,使 Consumer 无需了解 Provider 私有配置即可依赖这一点;Provider 无法配置主体标识符来源时必须在其 Module 文档声明,该部署下所有 Consumer 按 `DIRKEY-R-004` 的缺口路径处理 | 契约 + 单元 |
| `DIRKEY-R-013` | 把主体标识符切换为 anchor 之前,必须逐个 OIDC/SAML Consumer 验证应用内用户名、URL 标识符或文件路径的投影;存在未获 R-010 例外允许的投影时必须先改掉该 Consumer，不得以"多数 Consumer 没问题"放行。Nextcloud 必须验证 UID 等于 anchor、改名保持账号和文件归属，并区分技术路径与登录名/显示名 | 审阅 + e2e |
