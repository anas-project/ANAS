# Module IAM / OIDC 支持清单

Authentik 自 2026-10-05 标记为 `deprecated`（过时），不再推荐新部署。下文相关结果为历史能力记录，Authentik 整改不作为 Casdoor 发布前置条件。

OIDC 是 ANAS 当前默认 IAM 接入协议，但只对声明消费 `iam` capability 且支持 OIDC 的 Module 生效。不支持 OIDC 的 Module 不会被强制改用 OIDC；它按 Manifest 支持范围回退或继续使用自己的认证方式。

## Samba 目录事件订阅规范

所有 IAM Provider，以及所有直接使用 LDAP/LDAPS 读取 Samba 用户、组、账号状态或目录属性的
Module，都必须订阅 Samba 发布的持久目录事件，不能只等待定时同步或下一次登录。消费者按自身
关注属性处理 `Add`、`Modify`、`Delete`，通过增量刷新、缓存失效或受控 Source Sync 在声明的最大
传播时间内收敛；保存目录副本的消费者还必须保留周期 LDAP 全量同步兜底。完整可靠性、安全和 E2E
要求见[Samba 目录事件订阅与实时同步要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)。

收敛不止于“下一次登录会用新状态”。事件影响用户准入时——账号停用或删除、改名、身份锚点变化、
直接或递归组变更导致不再满足 `ALLOW_GROUPS` 等登录条件——消费者必须在同一最大传播时间内使该
用户**已经建立的应用会话**失效，不能等待会话 TTL 或下一次登录失败。IAM Provider 把这类事件转成
对应 Consumer 的标准登出通知，自持会话的 LDAP Module 自行终止本地会话，撤销范围只限受影响用户。

上游同时支持 LDAP 用户同步和 OIDC/SAML 登录的应用必须**双接入**：LDAP/LDAPS 负责目录 provision
与属性，OIDC/SAML 负责登录与会话。两侧义务同时成立，“已接 OIDC”不免除事件订阅，“已接 LDAP”
也不免除会话撤销。下表中 `nextcloud` 与 `meshcentral` 已是双接入；`forgejo` 只接 OIDC——固定
`15.0.7` 虽有 LDAP 用户同步，却没有按不可变 ID 把 OIDC 身份绑定到 LDAP 账号的接口，双接入无法
安全成立，缺失方向与兜底见 [Forgejo Module 设计](/architecture/forgejo-module-design) §2.2，
升级复核触发点见同文 §2.3；`vikunja` 只接 OIDC，M1 盘点确认固定 `2.4.0` 没有 LDAP 用户同步，
也没有可配置的身份 claim。Casdoor r10 已接入目录事件驱动的 OIDC 撤权与持久重试，源码/helper
测试通过，两 client 隔离与真实 Nextcloud 改名/组撤权 Cookie 验收已通过；最终故障矩阵结果见
[2026-10-04 实机记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-10-04-casdoor-directory-identity-acceptance.md)。
Nextcloud r11 改用 anchor 内部 UID、OIDC `sub` 和原样官方 `user_oidc 8.11.0`；不再修改控制器。
其他 Provider 回归、客户端体验与完整镜像生命周期仍需验收，不计作发布完成。
下文历史 Authentik/LLNG 矩阵属于旧插件/配置基线。2026-10-05 的 LLNG r12 独立验收
已将 OIDC `sub` 切为 anchor，并验证官方 8.11.0 的改名账号/文件与 Portal 登出隔离；
其他 Provider、客户端与完整发布矩阵仍待完成。

**「匹配键」列**记录每个 Module 持久化目录用户时**实际使用的键**，由
[目录身份键要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-identity-key.md)
`DIRKEY-R-011` 要求。它是摘要，不是结论来源：逐行证据等级（`已验证` / `推断`）、改名与回收的
后果，以及每条缺口的兜底动作，都写在各 Module README 的《目录属性变更说明》里。IAM Provider
分「消费侧」（它自己怎么认目录里的人）与「签发侧」（它发给 Consumer 的主体标识符是什么）两项，
Casdoor r10 已统一取 anchor，其他 Provider 的签发侧仍待整改与验证；验收归
[目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) M2。

| Module | 是否可用 OIDC 登录 | 当前认证路径 | 结论 | 匹配键 |
| --- | --- | --- | --- | --- |
| `netbird` | 是 | 直接消费 IAM/OIDC | 已实现 OIDC；Casdoor/LLNG anchor-sub 组合拒绝当前投影 | OIDC `sub`（`AuthUserIDClaim` 可配置）；随 Provider 而稳定 |
| `oauth2_proxy` | 是 | 直接消费 IAM/OIDC，并为 ForwardAuth consumer 提供门禁 | 已实现 | 无持久键；透传 `sub`，控制台取 `sha256(issuer‖sub)` |
| `ddns_updater` | 间接 | 经 `oauth2_proxy` + Traefik ForwardAuth | 已实现 | 无持久键；跟随网关 |
| `nextcloud` | 是 | 官方 `user_oidc 8.11.0`；LDAPS provision 用户/组；`user_saml` 保留为显式 fallback | developing；Casdoor 真实改名、原文件与 OIDC Cookie 撤权通过，其他 Provider、客户端和完整发布生命周期待验收 | 身份锚点同时作为 LDAP UUID 和新账号 UID；OIDC 按 `sub` 直接匹配 |
| `meshcentral` | 是 | IAM/OIDC 认证；LDAPS 同步用户/组；OIDC group 映射应用访问和 site-admin | 已实现 | 身份锚点，**直接作为用户 id**（OIDC `uuid` claim / `ldapUserKey`） |
| `forgejo` | 是 | IAM/OIDC JIT 建号；`APP_forgejo`/`APP_all` 门禁；管理员组映射 site-admin；保留托管 break-glass | developing；Manifest、Provider 注册、Hook 和应用配置已实现，真实浏览器/数据库 E2E 待验收 | OIDC `sub`（存入 `login_name`）；随 Provider 而稳定 |
| `immich` | 是 | 共享 PostgreSQL；OIDC JIT；受信管理员 roleClaim | developing；固定镜像、注册、入口限制与 Authentik 目录撤权已实现并本机验证；真实 IAM/移动端/目录链待验收 | anchor → OIDC `sub` → `user.oauthId`；内部 `user.id` 保留上游生成值 |
| `vikunja` | 是 | IAM/OIDC JIT 建号；`APP_vikunja`/`APP_all` 门禁；本地认证和注册关闭 | developing；Manifest、Provider 注册、Secret、Hook 和应用配置已实现，真实浏览器/数据库 E2E 待验收 | OIDC `(issuer, sub)`；不可配置，随 Provider 而稳定 |
| `lam` | 否 | LDAPS 目录管理登录 | 不属于当前 IAM consumer | 无持久键；每次登录按 `sAMAccountName` 检索后 bind DN |
| `authentik` | 不适用 | IAM provider；另有固定 `akadmin` break-glass | 提供 OIDC/SAML，不把自身当普通 consumer | 消费侧：身份锚点（`object_uniqueness_field`）。签发侧：内部用户 UUID，**非 anchor** |
| `casdoor` | 不适用 | release IAM provider；使用默认模板 `admin_casdoor` break-glass | OIDC/SAML 登录、Samba 目录收敛、永久 anchor、`ALLOW_GROUPS` 门禁、OIDC exact-`sid` 会话撤销、空 workspace 恢复、多架构生命周期及受管凭据轮换已有真实 E2E；固定版本不发布 SAML SLO | 消费侧：身份锚点（`externalId`）。r10 签发侧：OIDC `sub`、UserInfo、Logout Token 与 SAML `NameID` 统一取 anchor；签名协议、OIDC 故障矩阵和真实 Nextcloud 归属通过；2026-10-06 正式发布验收通过，已标 release |
| `llng` | 不适用 | IAM provider | r12 developing；提供 OIDC/SAML，不把自身当普通 consumer | 消费侧：无用户副本，session 保存 anchor；签发侧：r12 OIDC `sub` = anchor；SAML NameID 待实现 |
| `ddns_go` | 否 | ANAS 托管 local emergency account | 不支持 OIDC | 不适用（本地账号） |
| `traefik` | 否 | ANAS 托管本地 BasicAuth emergency account | 不支持 OIDC | 不适用（本地账号） |
| `collabora` | 间接 | 由 Nextcloud/WOPI 集成，不提供独立用户登录 | 跟随 Nextcloud 会话 | 不适用（无独立用户） |
| `postgres`, `mariadb` | 否 | 数据库凭据/Adminer | 不是 IAM 登录 | 不适用（非目录用户） |
| `samba_dc`, `samba_fs` | 否 | AD/LDAP/Kerberos/SMB | 不是 OIDC Web consumer | `samba_dc` 是目录本身；`samba_fs` 用对象 SID（`idmap backend = rid`） |
| `eturnal`, `freeradius`, `lego` | 否 | TURN/RADIUS/无交互 UI | 不适用 | 不适用（无目录用户） |

## 固定版本登出矩阵

“已通过”只表示表中列出的固定 Provider/Consumer 与场景；“受限”不会被汇总成双向登出。统一浏览器入口是 `test-env/scripts/server-iam-logout-matrix-e2e.sh`，脱敏 JSON 写入 `test-env/reports/iam-logout-*.json`。

| Consumer 固定版本 | endpoint / binding | Module→IAM | IAM→Module | session 粒度 | 浏览器 | 故障/降级结果 | 验收状态 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Nextcloud `user_oidc 8.11.0` | RP logout + `/index.php/apps/user_oidc/backchannel-logout/anas` | RP-Initiated Logout | `sid` back-channel | 单个 OIDC session；同用户多 session/client 属安全矩阵必测项 | RP logout 需要；back-channel 不需要 | IAM 不可用时本地 session 必须先失效；Provider 不通知时标受限 | Authentik 浏览器/管理员删 session 已有 E2E；LLNG 浏览器已有 E2E；Casdoor Provider 已通过真实标准 Consumer 的用户/管理员 exact-`sid`、多会话隔离和重放矩阵，r11 真实 Nextcloud 改名/组撤权旧 Cookie 通过；完整统一浏览器矩阵仍待复验 |
| Nextcloud `user_saml 8.2.0` | `/index.php/apps/user_saml/saml/sls`, Redirect | SP-Initiated SLO | IdP-Initiated SLO | NameID + SessionIndex | 必须 | 无 SLO 时只本地登出 | Authentik Redirect 已有 E2E；LLNG Redirect 入口已实现待隔离 fixture；Casdoor 明确无 SLO |
| MeshCentral `1.2.4` | discovery/provider RP logout + post-logout URI | 上游支持 | 无标准 receiver | 应用 Cookie/session | 必须 | 本地 session 先失效；IAM 不可用不得卡住本地退出 | 统一 Playwright 矩阵已实现；`state`、中央 session 结果未在当前主机 fixture 验收，故为“上游支持、待接入” |
| Forgejo `15.0.7` | `/user/logout` | 仅清应用 session | 无标准 receiver | Forgejo database session | 否 | 本地 session 清除；不声明 IAM session 同步失效 | Hook/容器 helper 单元测试与固定版本文档审查已完成；真实浏览器 E2E 待验收 |
| Immich `3.2.4` | discovery RP logout + `/api/oauth/backchannel-logout` | 原生路径与服务端断连，本机通过 | Authentik r15；标准 logout + 协商的 CAEP policy session-revoked | 普通退出为 session；策略事件撤旧 grant 的 session/key/share/WS | RP logout 需要 | 原事件时间重试保留新凭据；旧回调与凭据竞态拒绝 | developing；本机通过，真实 IAM/目录链/移动端待验收 |
| Vikunja `2.4.0` | discovery `end_session_endpoint` + `id_token_hint` + post-logout URI | 上游支持 | 无标准 receiver | Vikunja server-side session | 必须 | 上游先删除本地 session；组装 Provider 登出 URL 失败不阻断本地退出 | Hook/容器入口单元测试与上游固定版本源码审查已完成；真实浏览器 E2E 待验收 |
| NetBird Dashboard `2.90.9` | discovery `end_session_endpoint` + post-logout URI | 上游支持 | 无标准 receiver | Dashboard 本地认证状态 | 必须 | 本地状态先失效；无通知不声明 IAM→Module | 统一 Playwright 矩阵已实现；`state`、中央 session 结果未在当前主机 fixture 验收，故为“上游支持、待接入” |
| oauth2-proxy `7.15.3` | `/oauth2/sign_out` | 仅清网关 Cookie | 无 | oauth2-proxy Cookie；不含 IAM/后端 session | 否 | IAM 停止仍清 Cookie，受保护服务重新认证 | 不发布 `OIDC_LOGOUT_*`，不配置 `backend-logout-url`；IAM 不可用 Playwright case 已实现待隔离 fixture |

Provider 固定为 Authentik `2026.5.6`、LLNG `2.23.2`、Casdoor `3.143.0`。SAML HTTP-POST 与 Redirect 都是浏览器 binding，不能由 `post` 字面值推断为后台撤销。Casdoor 只登记显式 OIDC back-channel URI，并在声明消失/协议切换时清理旧值；用户/管理员 exact-`sid` 通知已通过真实标准 Consumer E2E。固定版本没有 SAML SLO 消费路径，因此不发布 SLO。

新增 Authentik r15 的 canonical-sub/策略事件补丁尚未重验表中其他历史 Consumer 组合；它们的既有验收记录不等同 r15 放行。

## 默认解析规则

协议优先级为：Module 显式 `iam_protocol` > 部署 `identity.iam.default_protocol`（仅当 Module 支持）> Module Manifest preference。Nextcloud、MeshCentral、Forgejo、Vikunja、NetBird 和 OAuth2 Proxy 当前都默认选择 OIDC；Nextcloud 可显式切换回 SAML，MeshCentral、Forgejo 和 Vikunja 只声明 OIDC。

## Samba 目录密码接入规范

当 IAM Provider 将用户改密写回 Samba AD 时，Samba 域策略是唯一权威策略，IAM 页面
的校验和提示只是提前反馈，不能建立一套比 Samba 更严格的独立密码策略。Provider 接入
必须分别记录以下能力，不能只用“支持改密”概括：

1. **策略值同步**：从 `samba_dc` 消费可表达的最小长度、复杂度开关、历史次数和最小改密间隔；不支持的值必须明确标为“仅提示”或“不支持”。
2. **提交前校验**：最小长度可以精确预检。复杂度必须按 AD 的三类/五类算法以及用户名、显示名称限制实现，不能用独立的字符类别计数近似。LDAP 不提供历史密码，历史次数和最小改密间隔通常不能在提交前验证。
3. **LDAP 写回**：业务用户的新密码必须写入 Samba，不得只更新 IAM Provider 的本地密码。必须区分“用户使用旧密码改密”和“服务账号重置密码”：后者可能绕过 Samba 的历史与最小间隔，因此 Provider 必须记录这种差异及补偿控制，不能笼统声称 Samba 已最终裁决全部规则。
4. **错误映射**：面向用户返回安全、可操作的说明；LDAP `constraintViolation`（19）和 `unwillingToPerform`（53）只能稳定归类为“域策略拒绝”，不能据此断言具体违反了历史、复杂度或姓名规则。权限不足和用户不存在可以单独映射；原始 LDAP diagnostic 只进入管理员日志或事件。
5. **首次登录改密**：必须单独声明是否识别 Samba 的强制改密状态以及是否经过同一写回路径；不能由普通改密支持推断。

修改 Samba 密码策略后必须重新执行 ANAS apply/reconcile，使 Provider 的提示和可预检
字段更新。若 LDAP 写回采用服务账号重置、因而不能由 Samba 执行历史规则，可以把同步
相同深度的 Provider 密码历史作为补偿控制，但文档和测试必须明确两者状态及裁决范围不同。

## 后续实现门槛

OIDC/SAML Module 的本地登出、应用发起登出、浏览器双向登出和后台双向登出必须按
[使用 OIDC/SAML 的 Module 双向登出要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/module-iam-bidirectional-logout.md)
分别判定；本表中的“OIDC 已实现”不自动表示双向登出或管理员后台撤销已实现。

把一个 Module 标为“OIDC 已实现”至少需要：Manifest OIDC interface、provider client registration、redirect URI/scope/claim/group 映射、Secret 传递、应用内验证和真实浏览器/HTTP 登录 E2E。Nextcloud 与 MeshCentral 由 `server-authentik-oidc-login-e2e.sh` 覆盖完整授权码登录、应用 session、目录身份和管理员组映射。Samba 密码接入分别由 `server-authentik-password-policy-e2e.sh` 和 `server-llng-password-policy-e2e.sh` 覆盖 Provider 页面预检、目录最终裁决、写回、错误映射和凭据切换。仅因为上游软件声称支持 OIDC 或改密，不能修改本表的实现状态。

Nextcloud 的 IAM 发起登出由两套 OIDC matrix 保留原 Cookie 验证并阻止静默恢复：Authentik 覆盖浏览器
登出和管理员删除 session，LLNG 覆盖浏览器登出；SAML fallback 的 Authentik E2E 覆盖
Redirect SLO。Casdoor 的标准 Consumer fixture 另行覆盖用户退出、管理员删 session、同用户双 session、
其他用户隔离和 Logout Token 重放。2026-10-04 的真实 Nextcloud r11 HTTP 验收补充改名与直接组撤权
后的原 Cookie 失效、其他用户隔离和原文件归属；完整统一浏览器矩阵仍需另行复验。
现有 Redirect/POST SLS 都按浏览器 binding 处理；没有单独通过无浏览器 E2E 的正式服务端撤销能力时，
SAML 后台撤销不属于支持范围。
Provider 没有发布可选 `SAML_SLO_URL`（例如当前 Casdoor 集成）时，Nextcloud 只配置
SSO 与签名证书，并执行应用本地登出，不得把普通退出页猜成标准 SLO endpoint。
