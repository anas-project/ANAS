---
doc_type: requirement
status: current
created: 2026-08-21
updated: 2026-08-21
---

# 使用 OIDC/SAML 的 Module 双向登出要求

本文规定消费 IAM capability 的应用 Module 必须如何实现、声明、验证和文档化双向登出。
IAM Provider 自身的注册、通知和安全要求继续由[新 IAM Provider 准入与实施要求](iam-provider.md)
约束；两份文档共同构成 `Provider × 协议 × Module` 的完整验收条件。

关键词“必须”“不得”“应该”具有规范性。

## 1. 目标与适用范围

本要求适用于以下 Module：

- 直接以 OIDC 或 SAML 消费 `iam` capability，并在应用内建立自己的登录会话；
- 通过应用自带 OIDC/SAML 插件建立会话；
- 以 `oauth2-proxy` 等 IAM 网关保护管理面，但仍可能在网关或后端建立会话。

不建立人机会话的服务不适用。经 ForwardAuth 间接接入 IAM 的 Module 不得把网关登录等同于
自身已支持双向登出；网关 Cookie、后端应用 Cookie 和 IAM 中央会话必须分别判断。

### 1.1 按 Module 能力尽量覆盖标准登出机制

“尽量支持所有登出协议”是逐 Module 的适配目标，不是让所有 Module 虚报同一能力集合：

1. Module 必须以固定应用及认证插件版本为单位，盘点其正式支持的 OIDC/SAML 登出机制、
   endpoint、binding、会话标识和浏览器依赖；不得只依据协议登录能力推断登出能力。
2. ANAS 应在 `Provider × 协议 × Module` 的能力交集中实现所有能安全配置和验证的标准机制，
   不得为了形成统一最低配置而主动丢弃某个 Module 已正式支持的 back-channel 或 SLO 能力。
3. 上游只支持部分机制时，可以按真实能力降级，不要求 Module 自建不受上游支持的协议处理器；
   但必须明确标记未支持、未确认或等待上游，不得登记普通 `/logout` 页面冒充标准 endpoint。
4. 同一 Module 支持多种机制时，应用发起方向保留 RP/SP-Initiated Logout；IAM 发起方向优先
   选择无浏览器且能精确定位 session 的机制，同时保留经过验证的浏览器机制作为兼容路径。
5. Module 升级应用或认证插件后必须重新盘点。新增、移除或改变 endpoint/binding、`sid`、
   `SessionIndex` 或签名语义都属于身份契约变更，必须重新执行对应 E2E。
6. 当前通用契约无法表达上游正式能力时，必须先扩展 Provider-neutral contract、Runner 校验和
   Provider adapter，再启用该机制；不得把 Provider 私有字段直接泄漏给 Consumer Module。

目标语义如下：

```text
Module -> IAM
用户从应用退出
  -> 应用会话失效
  -> IAM 中央会话失效
  -> IAM 按协议通知同一 SSO 会话中的其他应用

IAM -> Module
用户从 IAM 退出、管理员撤销 IAM session 或账号停用
  -> IAM 按协议通知应用
  -> 对应应用会话失效
```

只有两个方向都满足，Module 才能声明“支持双向登出”。只清浏览器本地 Cookie、只撤销
access/refresh token、只跳转到退出后页面、缩短会话 TTL 或等待重新登录失败，都不等价于
双向登出同步。

## 2. 术语与能力分级

| 术语 | 本文含义 |
| --- | --- |
| 应用会话 | Module 或其认证插件建立的 Cookie、服务端 session 或等效状态 |
| IAM 中央会话 | OIDC OP 或 SAML IdP 保存的 SSO 会话 |
| RP/SP 发起登出 | 应用发起，先清理应用会话，再请求 IAM 结束中央会话 |
| IAM 发起登出 | IAM 主动通知 RP/SP 清除应用会话 |
| 浏览器参与 | 必须依赖原用户浏览器、Cookie、iframe 或 Redirect 才能完成 |
| 后台撤销 | 无用户浏览器时，管理员或 IAM 服务端仍能撤销应用会话 |

Module 必须按以下分级记录真实能力：

| 等级 | 条件 | 可用表述 |
| --- | --- | --- |
| 本地登出 | 只清应用会话 | “仅本地登出” |
| 单向 RP/SP 登出 | 应用退出能结束 IAM 会话，但 IAM 不能反向通知应用 | “支持应用发起登出” |
| 浏览器双向登出 | 两个方向都成立，但 IAM -> Module 依赖浏览器 | “支持浏览器参与的双向登出” |
| 后台双向登出 | 两个方向都成立，管理员无浏览器撤销也能清理应用会话 | “支持双向登出及后台撤销” |

不得把较低等级写成较高等级。OIDC front-channel 和 SAML Redirect SLO 通常只能达到
“浏览器双向登出”；OIDC back-channel 才是 ANAS 对后台撤销的首选。

### 2.1 标准机制覆盖矩阵

Module 必须逐项记录下表状态；状态只能是“支持并通过 E2E”“仅上游支持、待接入”
“不支持”“不适用”或“未确认”。“OIDC/SAML 登录已实现”不是其中任一登出项的证据。

| 协议机制 | 方向 | 浏览器依赖 | 能否承诺管理员无浏览器撤销 | ANAS 选择规则 |
| --- | --- | --- | --- | --- |
| OIDC RP-Initiated Logout | Module -> IAM | 通常需要 | 否 | 支持 `end_session_endpoint` 时应该接入 |
| OIDC Back-Channel Logout | IAM -> Module | 不需要 | 是 | Module 有标准 endpoint 时优先接入 |
| OIDC Front-Channel Logout | IAM -> Module | 需要 | 否 | 作为 back-channel 补充或唯一上游能力接入 |
| OIDC Session Management | IAM 状态 -> Module | 需要活动页面 | 否 | 只作状态检测补充，不替代 logout notification |
| SAML SP-Initiated SLO | Module -> IAM | 取决于 binding | 不能单独推断 | 固定版本正式支持时按 metadata 接入 |
| SAML IdP-Initiated Redirect SLO | IAM -> Module | 需要 | 否 | 支持浏览器双向登出，不得写成后台撤销 |
| SAML IdP-Initiated HTTP-POST Binding | IAM -> Module | 需要 | 否 | 标准路径是浏览器自动提交表单 |
| Provider 扩展的服务端 POST SLO | IAM -> Module | 不需要 | 可能 | 不能从出现 POST SLS 自动推断，必须单独验证 |
| SAML SOAP SLO | 双向/同步 | 不需要 | 可能 | 双方正式支持且通用契约可表达时优先验证 |
| SAML Artifact SLO | 双向/异步 | 通常需要 | 否 | 只有 metadata、解析服务和 E2E 齐全才接入 |

HTTP 方法或 binding 名称本身不能决定是否为 back-channel。标准 SAML HTTP-POST Binding
由浏览器自动提交表单；部分 Provider 另外把 POST SLS 用于服务端 SLO，这是产品实现能力，
不是看见 `post` binding 就能推导的协议保证。只有明确的双方产品契约和无浏览器 E2E 才能把
后一种情况标记为后台撤销。

每个建立会话的 Module 必须在 Manifest/README/技术文档或其引用的支持矩阵中记录：有效登录
协议、本地会话拥有者、支持的发起方向、通知机制、endpoint/binding、会话定位粒度、浏览器
依赖、降级行为、固定版本和 E2E 证据。经网关接入时必须为 IAM、网关和后端应用分别记录，
不得把 OAuth2 Proxy Cookie 失效投射为后端业务会话已经失效。

## 3. 通用 Module 要求

1. Module 只能使用 Runner 解析出的有效 IAM binding，不得按 `llng`、`authentik`、
   `casdoor` 或其他 Provider 名称分支，也不得读取 Provider 私有配置。
2. 登录 redirect URI、登出后跳转 URI 和应用会话通知 endpoint 必须分开声明。普通首页、
   `/login`、`/logout` 或返回页不得被猜测为 front-/back-channel endpoint。
3. Module 必须先使自己的会话失效，再离开应用执行 RP/SP 发起登出。IAM 不可用时，本地
   登出仍必须成功；界面或日志应明确全局登出未完成，不得为了等待 IAM 而保留本地会话。
4. Module 必须保存协议登出所需的最小关联信息，例如 OIDC `sid`/ID Token 或 SAML
   `NameID`/`SessionIndex`，并将其与应用会话绑定。不得使用 email、用户名或显示名替代
   协议会话标识。
5. 每次通知只撤销目标用户或目标 session。其他用户、其他 client、其他浏览器会话和 API
   token 不得被误删；如果上游协议只提供 `sub` 而没有 `sid`，文档必须说明会撤销该用户
   在本应用中的哪些会话。
6. 登出处理必须幂等。重复通知可以返回成功，但不得恢复会话、产生 5xx 风暴或扩大撤销范围。
7. 所有外部 URI 必须使用 HTTPS；只有隔离测试环境可以例外。Secret、Cookie、ID Token、
   Logout Token、SAML assertion 和私钥不得进入日志、plan、错误消息或审计详情。

## 4. OIDC Module 要求

### 4.1 Module -> IAM：RP-Initiated Logout

OIDC Module 必须：

1. 从自己的 `ANAS_IAM_BINDING__<APP>__OIDC_DISCOVERY_URL` 发现并使用
   `end_session_endpoint`，不得拼接某个 Provider 的私有退出路径。
2. 在上游应用支持时发送原会话的 `id_token_hint`；无法保留 ID Token 时至少使用标准
   `client_id` 方式，并通过真实 Provider 验证中央会话确实失效。
3. `post_logout_redirect_uri` 必须来自已登记的
   `ANAS_IAM_CLIENT__<APP>__POST_LOGOUT_REDIRECT_URIS` 精确允许列表；使用随机 `state`
   关联请求与回调并验证返回值。
4. 在跳转到 IAM 前使应用 Cookie/服务端 session 不可继续使用。回调页只负责显示结果或
   开始新登录，不得根据旧 Cookie 自动恢复会话。
5. 用旧应用 Cookie 和原 IAM Cookie 分别验证退出结果；只观察 302 或最终 URL 不算通过。

### 4.2 IAM -> Module：Front-/Back-Channel Logout

支持 IAM 主动登出的 OIDC Module 必须通过通用注册请求发布：

```dotenv
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_URI=https://app.example/oidc/backchannel-logout
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_SESSION_REQUIRED=true
```

规则如下：

- `OIDC_LOGOUT_URI` 与 `OIDC_LOGOUT_METHODS` 必须同时存在；method 只能是
  `backchannel` 或 `frontchannel`。同时支持时必须把 `backchannel` 放入能力集合，由
  Provider adapter 优先选择。
- `OIDC_LOGOUT_SESSION_REQUIRED=true` 只在应用能按 `sid` 精确撤销会话时声明；不能处理
  `sid` 时不得虚报，但仍必须按规范处理合法的 `sub`。
- back-channel endpoint 必须接受 HTTPS `POST` 和
  `application/x-www-form-urlencoded` 的 `logout_token`，不依赖用户 Cookie、CSRF token
  或浏览器交互。
- Logout Token 必须验证签名、允许算法、`iss`、`aud`、`events`、`iat` 与最大可接受时效、
  `jti` 防重放，以及至少一个有效 `sid`/`sub`；存在 `exp` 时必须验证，存在 `nonce` 时必须
  拒绝。不得把 ID Token、access token 或任意 JWT 当作 Logout Token 接受。
- 无效、过期或重放 token 必须拒绝且不得改变合法会话；审计记录只能保存事件 ID、client、
  session 的安全标识、结果和原因分类，不能保存原 token。
- front-channel endpoint 必须验证 `iss` 与 `sid`，并处理 iframe、SameSite、CSP 和第三方
  Cookie 限制。只支持 front-channel 时必须明确“不支持管理员无浏览器后台撤销”。

上游应用没有标准通知 endpoint 时，Module 必须省略全部 `OIDC_LOGOUT_*` 字段，并在 README
标记为“仅本地登出”或“只支持应用发起登出”。不得登记一个看似可用的普通退出页。

### 4.3 OIDC 多机制选择与降级

1. 同时支持 back-channel 和 front-channel 时应该全部登记并测试，Provider adapter 必须优先
   back-channel；front-channel 可以作为兼容补充，但不得在 back-channel 失败后无提示地扩大
   撤销范围或把浏览器送达当作服务端成功。
2. 只支持 front-channel 时可以交付“浏览器参与的双向登出”，但管理员删除 session、用户停用
   和设备离线场景必须明确依赖会话 TTL、token 撤销或重新鉴权收敛，不能标记为同步撤销。
3. 支持 OIDC Session Management 的 Module 可以额外监测 OP 登录状态，但 iframe/
   `postMessage` 检测不是 Logout Token，不得替代 front-/back-channel endpoint 或管理员撤销测试。
4. 同一应用为 front-channel 和 back-channel 提供不同 URI 时，不得把两个 URI 压缩成一个普通
   `/logout`。当前单 URI contract 无法无损表达时，应先扩展为分方法 endpoint，再接入该应用。
5. Back-Channel Logout 结束应用 Cookie/session 后，还必须按应用的 OAuth 使用方式处理 refresh
   token、access token 和离线授权；“应用会话已退出”不得自动写成“所有 API token 立即失效”。

## 5. SAML Module 要求

### 5.1 Module -> IAM：SP-Initiated SLO

SAML Module 必须：

1. 从自己的 IAM binding/metadata 读取 IdP SLO URL 和兼容 binding，不得写死 Provider URL。
2. 保存登录 assertion 对应的 `NameID`、NameID format 和 `SessionIndex`，用它们构造
   `LogoutRequest`；不得以当前用户名重新猜测。
3. 在应用会话失效后向 IdP 发送 SLO 请求。上游支持签名时必须使用 Module 自己持有的 SP
   私钥签名；私钥不得经 IAM contract 交给 Provider。
4. 验证 `LogoutResponse` 的签名、Issuer、Destination、`InResponseTo`、状态和时效；
   `RelayState` 只用于安全导航，不能代替会话关联或接受任意外部跳转。

### 5.2 IAM -> Module：IdP-Initiated SLO

支持 IAM 主动登出的 SAML Module 必须发布：

```dotenv
ANAS_IAM_CLIENT__<APP>__SAML_SLS_URL=https://app.example/saml/sls
ANAS_IAM_CLIENT__<APP>__SAML_SLS_BINDINGS=redirect,post
```

规则如下：

- SLS URL 与 bindings 必须同时存在；binding 只能是 `redirect` 或 `post`，且只能声明固定
  上游版本真实支持并经过测试的值。
- SLS 必须验证 `LogoutRequest` 的签名、Issuer、Destination、NameID、SessionIndex、时效
  和重放；成功后只撤销匹配的应用会话，并返回带正确 `InResponseTo` 的签名
  `LogoutResponse`。
- HTTP-Redirect SLO 依赖用户浏览器，只能承诺浏览器参与的双向登出。不得声称管理员后台
  删除 IdP session 会立即清理应用会话。
- 只有 SP 的固定版本正式支持可由 Provider 服务端调用的 POST SLS，并通过无浏览器 E2E
  后，才可以声明 SAML 后台撤销能力。不能把 Redirect SLS 配成 Provider 的 back-channel
  模式来绕过限制。
- Provider 没有发布 `SAML_SLO_URL` 时，Module 必须清除旧的 SLO 配置并退化为本地登出；
  不得继续调用上一次部署留下的 endpoint。

### 5.3 SAML 多 binding 选择与契约扩展

1. Module 应从 SP metadata 发布固定版本真实具备的全部 SingleLogoutService，而不是只留下第一个
   endpoint；Provider 从双方交集中选择其能正确签名、关联和验证的 binding。
2. Redirect、浏览器表单 POST 和 Artifact 属于浏览器路径时，可以作为兼容机制全部保留，但
   只能声明浏览器参与的 SLO。浏览器关闭、跨站 Cookie/CSP 限制和中途跳转失败必须进入限制说明。
3. Module 与 Provider 同时正式支持 SOAP，或明确支持可由 Provider 服务端调用的 POST SLS 时，
   应优先增加无浏览器 E2E；通过前仍按浏览器能力分级，不能仅凭 metadata 中出现 `post`/`soap`
   就升级为后台双向登出。
4. 当前 `SAML_SLS_URL + SAML_SLS_BINDINGS=redirect,post` 只表达共用 URL 的 Redirect/POST
   子集。Module 若支持 SOAP、Artifact、每种 binding 不同 Location 或独立 ResponseLocation，
   必须先把通用注册升级为可重复 SLS endpoint 结构，并同步扩展 Runner、全部 Provider adapter、
   协议切换清理和契约测试。
5. 任何新增 binding 都必须继续满足签名、Issuer、Destination、时效、`InResponseTo`、NameID、
   SessionIndex 和重放验证要求；扩大 binding 覆盖不得降低验证强度。

## 6. 通用注册契约

消费 IAM 的 Module 在 `calculate` 阶段拥有并发布自己的
`ANAS_IAM_CLIENT__<APP>__*` 注册请求。登出字段如下：

| 字段 | 协议 | 语义 | 是否必需 |
| --- | --- | --- | --- |
| `POST_LOGOUT_REDIRECT_URIS` | OIDC | IAM 完成登出后允许返回的 URI | 支持 RP-Initiated Logout 时必需 |
| `OIDC_LOGOUT_URI` | OIDC | IAM 通知应用会话失效的标准 endpoint | 支持 IAM -> Module 时必需 |
| `OIDC_LOGOUT_METHODS` | OIDC | `backchannel`/`frontchannel` 能力集合 | 与 URI 成对 |
| `OIDC_LOGOUT_SESSION_REQUIRED` | OIDC | 是否要求 Logout Token 带可处理的 `sid` | 可选；声明后必须为布尔值 |
| `SAML_SLS_URL` | SAML | 应用的 Single Logout Service | 支持任一方向 SLO 时必需 |
| `SAML_SLS_BINDINGS` | SAML | 应用支持的 `redirect`/`post` 集合 | 与 SLS URL 成对 |

Module 只能为当前 effective binding 的协议发布字段。OIDC 绑定不得遗留 SAML SLS，SAML
绑定不得遗留 OIDC logout 字段；协议切换、域名变化和重复 apply 后必须幂等收敛，并清除旧
endpoint。Runner 负责通用成对关系和枚举校验，Module 负责上游版本是否真实支持、endpoint
路径、会话语义和安全处理。

## 7. 失败、降级与运维要求

- IAM 在 RP/SP 发起登出时不可用：应用本地会话必须失效；用户应得到“本地退出完成、全局
  退出未确认”的安全提示，且可重新尝试 IAM 登出。
- 应用 back-channel endpoint 暂时不可用：Provider 可以按自身策略有限重试；Module 必须以
  会话 TTL 作为通知永久失败的风险上限，但不得把 TTL 写成同步登出。
- 域名或协议切换：旧通知 endpoint 和旧登出回调必须从 Provider 与应用配置中删除；旧地址
  不得继续接受跨 client 的登出消息。
- 备份恢复：恢复后原 client、签名信任和未过期 session 的处理必须有明确策略。不能验证旧
  会话时应统一失效，而不是在缺少关联信息时按用户名模糊撤销。
- 监控与审计：至少能区分用户本地登出、RP/SP 发起 SLO、IAM 用户登出、管理员撤销、账号
  停用、通知失败和 token/message 校验失败。

## 8. 强制验收矩阵

验收必须使用真实 Provider、真实应用容器和真实会话。保存登出前 Cookie，在操作后继续访问
需要认证的应用接口或页面并断言未认证；静态配置、Hook 单元测试、HTTP 302、退出页文字或
token 过期时间都不能替代。

验收按 Module 实际声明的机制选择适用行，不要求不支持某协议的 Module 伪造通过结果；但 §2.1
的所有机制都必须有明确状态。声称同时支持多个 method/binding 时，每个机制至少单独执行一次
成功路径和关键安全失败路径，不能用 back-channel 的结果替代 front-channel，或用 Redirect 的
结果替代 POST/SOAP。

### 8.1 OIDC

| 场景 | 必须结果 |
| --- | --- |
| 从 Module 点击退出 | 原应用 Cookie 失效，IAM 中央会话失效，回到应用不能静默恢复 |
| 从 IAM Portal 退出 | IAM 发送标准通知，原应用 Cookie 失效 |
| 管理员无浏览器删除 IAM session | 声明 back-channel 的 Module 在限定时间内使目标 Cookie 失效 |
| 账号停用/会话撤销 | 新 token 被拒绝，已有目标应用会话按 Provider 通知失效 |
| 错误签名/issuer/audience/events/时间 | 请求被拒绝，合法会话保持有效 |
| 重放同一 `jti` | 不扩大撤销范围，不产生 5xx；记录重放审计 |
| 两个用户、同用户两个 session、两个 client | 只撤销 token 指定的目标范围 |
| IAM 在应用发起退出时不可用 | 本地 Cookie 仍失效，并明确全局退出未确认 |
| 声明 front-channel | 使用真实浏览器完成通知；Cookie、iframe、SameSite 和 CSP 行为符合限制说明 |
| 声明 Session Management | OP 状态变化可被检测，但测试结果不得标记为后台撤销 |
| 同时声明 front-/back-channel | 两条路径分别通过，默认选择 back-channel；任一路径失败可观测 |

### 8.2 SAML

| 场景 | 必须结果 |
| --- | --- |
| 从 Module 点击退出 | 完成 `LogoutRequest -> IdP -> LogoutResponse`，原应用与 IAM 会话失效 |
| 从 IAM Portal 退出 | 浏览器完成 IdP-Initiated SLO，原应用 Cookie 失效 |
| 篡改签名/Destination/Issuer/NameID/SessionIndex | 请求被拒绝，合法会话保持有效 |
| 错误或重放 `InResponseTo`/RelayState | 不接受伪造响应，不产生开放重定向 |
| Redirect SLS 下管理员后台删 session | 测试和文档明确“不覆盖无浏览器撤销”，不得伪报通过 |
| 声明 POST/back-channel | 必须另有真实无浏览器撤销 E2E 证明固定版本确实支持 |
| 声明浏览器表单 POST | 浏览器完成 POST SLO；测试和文档不得把它归为后台撤销 |
| 声明 SOAP | 无用户浏览器完成请求/响应和目标会话撤销，并验证签名、关联、超时和重放 |
| 声明 Artifact | 浏览器与 Artifact Resolution 链路完整通过，解析失败不得误删会话 |

### 8.3 交付证据

每个 `Provider × 协议 × Module` 组合必须记录：

| 字段 | 内容 |
| --- | --- |
| 版本 | Provider、应用、OIDC/SAML 插件和 ANAS Module revision |
| 环境 | 独立 fixture、入口域名、协议与选定 logout method/binding |
| 自动化 | 脚本、测试名和 CI artifact/log 位置 |
| 会话断言 | 使用的受保护资源及操作前后 HTTP/应用状态 |
| 限制 | 是否依赖浏览器、是否覆盖管理员撤销、按 `sid` 还是 `sub` 撤销 |
| 结果 | 通过、失败或受限；不得用“配置成功”代替会话结果 |

## 9. Module 发布与文档门禁

使用 OIDC/SAML 的内置 Module 在进入 `release` 前必须：

1. 按 §2.1 盘点固定上游版本的全部标准 logout 机制，声明真实能力；不支持的方向明确留空，
   上游支持但尚未接入的机制必须记录原因、风险和后续接入条件；
2. 通过 Hook/Runner 测试证明通用字段、协议切换、缺项和非法值处理正确；
3. 对每个声称支持的方向完成真实会话 E2E；OIDC 声称后台撤销时必须覆盖管理员无浏览器删
   session，SAML Redirect 必须明确不覆盖该场景；
4. README 中英文版记录登出入口、两个方向、method/binding、后台撤销能力和限制；
5. 技术文档中英文版记录会话标识、数据流、签名验证、重放保护、失败降级、实现文件和测试；
6. 在 [Module IAM / OIDC 支持清单](../reference/module-iam-support.md)中更新当前状态。

新增 IAM Provider 时，必须反向使用至少一个已满足本要求的基准 Module 执行同一矩阵。Provider
支持协议而 Module 不支持通知 endpoint，或 Module 提供 endpoint 而 Provider adapter 未登记，
都不得把组合标为“双向登出已支持”。

## 10. 明确不接受的替代方案

- 把 `post_logout_redirect_uri`、首页或普通 `/logout` 当作 IAM 通知 endpoint；
- 只删除前端 Cookie，不删除服务端应用 session；
- 只撤销 access/refresh token，不验证既有应用 Cookie；
- 依赖一分钟级 TTL 后自然失效并称为“同步”；
- OIDC back-channel endpoint 接受未验证的 JWT、用户名或 email；
- SAML Redirect SLS 声称支持管理员无浏览器撤销；
- 为适配某个 Provider，在应用 Module 中读取 Provider 私有环境变量或按 Provider 名称分支；
- 只用 mock、render 字符串或 302 跳转作为发布验收证据；
- 上游和通用契约已支持更强标准机制，却无记录地只接入最弱登出路径；
- 把 OIDC Session Management、SAML binding 名称或 Provider 私有“back-channel”开关本身当作
  无浏览器撤销证据。

## 11. 规范依据

- [OpenID Connect RP-Initiated Logout 1.0](https://openid.net/specs/openid-connect-rpinitiated-1_0-final.html)
- [OpenID Connect Back-Channel Logout 1.0](https://openid.net/specs/openid-connect-backchannel-1_0-final.html)
- [OpenID Connect Front-Channel Logout 1.0](https://openid.net/specs/openid-connect-frontchannel-1_0-final.html)
- [OpenID Connect Session Management 1.0](https://openid.net/specs/openid-connect-session-1_0-final.html)
- [OASIS SAML 2.0 Profiles：Single Logout Profile](https://docs.oasis-open.org/security/saml/v2.0/saml-profiles-2.0-os.pdf)
- [OASIS SAML 2.0 Bindings](https://docs.oasis-open.org/security/saml/v2.0/saml-bindings-2.0-os.pdf)
