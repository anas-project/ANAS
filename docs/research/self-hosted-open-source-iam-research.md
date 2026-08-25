---
doc_type: research
created: 2026-08-20
updated: 2026-08-21
evidence_as_of: 2026-08-21
---

# 开源自部署 IAM 与 ANAS 适配调研

## 1. 结论摘要

本轮围绕当前 ANAS 的 LemonLDAP::NG（下文简称 LLNG）与 Authentik，重点核验以下六项能力：

1. 绑定 Google、Apple ID、微信等第三方身份；
2. 免密码登录协议与实现；
3. 将用户改密写回 Samba AD；
4. 面向终端用户的应用列表/应用启动器；
5. 用户管理界面，特别是能否管理作为事实来源的 Samba AD 用户，而不是只管理 IAM 本地副本；
6. 是否提供 OIDC，以及能否与应用 Module 实现双向登出同步。

结论如下：

- **短期不建议替换 LLNG。** 仓库内 LLNG 已是 `release`，已经实现 OIDC、SAML、按组过滤的应用门户和 Samba 密码写回；缺口主要是上游 Passkey 能力尚未在 ANAS 中启用，以及 LLNG 没有完整的目录用户管理界面，需要继续配合 LAM。
- **优先把 Authentik 从 `developing` 做到可发布。** 在前五项产品功能中，Authentik 上游覆盖最完整：Google、Apple、微信均有官方 Source 文档，支持 WebAuthn 免密码 Flow、LDAP 密码写回、应用 Dashboard 和用户/组管理界面。ANAS 已实现 Samba Source、密码写回和应用注册，但仍需真实部署验证社交账号安全绑定、Passkey、目录撤权和恢复流程。
- **Passkey 支持必须按 2FA、免密码和无用户名三层验收。** LLNG 的 Passkey 第一因素仍标 Beta；Authentik 与 Keycloak 已有 discoverable credential、passwordless 和 conditional UI；WSO2 稳定版支持登记/渐进登记，下一版文档才明确 usernameless 开关；Casdoor 当前官方流程仍先输入用户名；ZITADEL 的 Passkey 绑定本地用户，不满足 Samba 实时总闸门。无论选哪种 IAM，credential 都必须经随机 WebAuthn user handle 绑定到同一 `anasIdentityAnchor`，有效签名不能绕过 Samba 停用、锁定和组撤权。
- **若要引入第三种 IAM，优先 PoC Keycloak。** Keycloak 同时提供 OIDC/SAML、WebAuthn passwordless、LDAP/AD `WRITABLE` 密码更新、Account Console 应用列表和成熟的用户管理控制台。主要缺口是 Apple/微信不是当前官方内置社交 Provider，需要扩展或协议适配；同时必须证明 `anasIdentityAnchor`、嵌套组、禁用账号和 Samba 密码策略语义。
- **WSO2 Identity Server 是第二候选，不是默认选择。** 它的 read-write AD user store、FIDO2、My Account 应用发现和目录管理能力很强，但部署、配置和升级面显著大于现有方案；微信仍需自定义 Connector。
- **Casdoor 仍不进入 Samba 主身份源路线，但“没有 LDAP/AD 密码写回”的原结论有误。** Casdoor 已在 2024 年 12 月合并 LDAP/AD 改密写回：AD 使用 UTF-16LE 编码的 `unicodePwd`，普通 LDAP 使用 Password Modify 或 `userPassword`。不过其目录用户仍是导入后的独立 Casdoor 记录，AD 路径还会把 `userAccountControl` 写成 `512`；在未完成 Samba、最小 ACL、停用位保持和永久锚点 PoC 前，不能视为满足 ANAS 的目录权威契约。
- **ZITADEL 更适合应用原生 CIAM，不适合当前目录中心架构。** 它支持 Google、Apple、Passkey、OIDC/SAML 和强管理 API，但没有可核验的 Samba 密码写回或终端用户应用启动器，LDAP 是外部 IdP，而不是 ANAS 需要的可写目录源。
- **OIDC 不是候选间的区分项，双向登出的交付状态才是。** 六个候选都能作为 OIDC Provider，也都提供 RP-Initiated Logout 与至少一种 OP 通知应用的标准机制。ANAS 当前只有 Nextcloud Module 同时实现“应用退出结束 IAM 会话”和“IAM 退出/管理员撤销使应用会话失效”所需接口；LLNG、Authentik 已完成通用契约翻译并有真实会话 E2E 脚本，但仍需保存隔离容器运行结果。Casdoor 3.143.0 adapter 已登记 back-channel URI，尚未做真实撤销验收；Keycloak、WSO2 IS、ZITADEL 尚无 ANAS Module，不能把上游支持写成 ANAS 已支持。

因此推荐顺序为：

```text
生产基线：LLNG
        ↓ 补齐 Passkey PoC，但保留 LAM 管理目录
功能优先：Authentik
        ↓ 完成现有 developing Module 的生产验收
第三候选：Keycloak
        ↓ 仅在确实需要第三实现时建立 Module PoC
备选：WSO2 IS
        ↓ 只有“完整 AD 管理面”价值足以覆盖运维成本时采用
不进入当前路线：Casdoor、ZITADEL
```

## 2. 范围、口径与证据等级

### 2.1 主题卡

```yaml
topic: self-hosted-open-source-iam
snapshot_date: 2026-08-21
decision_for: ANAS IAM provider 与后续能力建设
must_be:
  - 可开源自部署
  - 同时提供 OIDC Provider 和 SAML IdP
  - 能接 Samba AD，且不能绕过 Samba 的账号状态与授权组
deployment_target:
  runtime: Docker Engine + Docker Compose v2
  ingress: Traefik HTTPS
  identity_source: Samba AD
  architectures: [amd64, arm64]
questions:
  - 是否支持 Google、Apple、微信等外部身份绑定？
  - 是否支持标准化免密码登录？
  - 是否能将密码安全写回 Samba AD？
  - 是否有按权限显示的终端用户应用列表？
  - 是否有用户管理界面，以及它管理的是 AD 还是本地副本？
  - 是否提供 OIDC Provider、RP-Initiated Logout 和 OP-Initiated Logout？
  - 与当前应用 Module 的双向登出同步是否已实现并通过真实会话 E2E？
```

本报告不是“所有 IAM 产品”的市场全景，而是按 ANAS 的双协议准入条件和 Samba 目录契约筛出的可实施候选。Authelia、Dex、Ory、Kanidm、LLDAP 等没有同时满足当前 OIDC Provider + SAML IdP 硬条件的项目，仅在排除表中说明。

### 2.2 符号

| 符号 | 含义 |
| --- | --- |
| ✅ | 官方当前文档明确支持；对 LLNG/Authentik 还会注明是否已在 ANAS 落地 |
| ◐ | 能力存在但需扩展、复杂配置或 Samba 专项 PoC，不能直接视为 ANAS 支持 |
| ❌ | 官方能力缺失，或实现模型与 ANAS 硬约束冲突 |
| 未确认 | 本轮没有找到足以支撑肯定或否定结论的一级证据 |

“支持 LDAP”不等于“支持 Samba 密码回写”，“有 Applications 管理页”不等于“终端用户应用启动器”，“WebAuthn 二次认证”也不自动等于“免密码登录”。本报告按这些严格口径评分。

## 3. ANAS 的不可放宽约束

本轮结论继承[新 IAM Provider 准入与实施要求](../requirements/iam-provider.md)与[IAM 多实现与协议能力设计](../architecture/iam-capability-design.md)：

- Samba AD 是业务用户、组、启停状态、目录属性和密码的唯一事实来源；IAM 本地用户只能是安装或故障恢复账号。
- 跨系统永久身份键是 `mS-DS-ConsistencyGuid` 的文本投影 `anasIdentityAnchor`，不能改用邮箱、用户名、UPN、`objectGUID`、外部 IdP `sub` 或微信 `openid/unionid`。
- 应用访问集合固定为 `APP_<app> OR APP_all OR Admins`，并要求嵌套组与禁用账号在签发 token/assertion 前生效。
- Provider 必须同时提供 OIDC 和 SAML；应用 Module 只消费统一 IAM Contract，不能读取 Provider 私有配置。
- 密码写回只能经 LDAPS 和最小权限账号完成；成功后不得在 IAM 中留下第二份可登录的业务密码哈希。

这会直接改变“第三方登录”和“用户管理 UI”的产品判断：

1. 第三方账号只能绑定到**已经存在且仍启用的 Samba 用户**。
2. 禁止按外部邮箱、Apple Private Relay 邮箱或微信资料自动创建业务用户。
3. 第三方 `sub/openid/unionid` 只是认证器绑定键，不是 ANAS 身份锚点。
4. 每次外部登录后仍需从 Samba 读取启停状态、`anasIdentityAnchor` 和授权组，再决定是否签发应用凭据。
5. IAM 的 Users 页面若只编辑本地影子用户，不算“目录用户管理界面”；允许修改的属性必须真正写回 Samba，并受 AD ACL 和 ANAS 字段契约约束。

## 4. 总体能力矩阵

### 4.1 原五项产品需求

| IAM | Google / Apple / 微信 | 真正免密码 | Samba 密码写回 | 终端用户应用列表 | 用户管理界面 | ANAS 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| **LLNG 2.23.2-r11（现有）** | Google ✅；Apple ◐；微信未确认 | Passkey/WebAuthn ✅，上游标 Beta；ANAS 未启用 | ✅ ANAS 已实现，E2E 脚本覆盖 | ✅ Portal 按规则显示应用 | ❌ 无完整目录管理；配合 LAM | 保持生产基线，补 Passkey PoC |
| **Authentik 2026.5.6-r10（现有）** | Google ✅；Apple ✅；微信 ✅ | WebAuthn passwordless / Passkey autofill ✅；ANAS 未验收 | ✅ 上游支持；ANAS 已实现且有 E2E 脚本，Module 仍为 `developing` | ✅ Application Dashboard | ✅ 本地/同步对象 UI；◐ 作为 Samba 管理面 | 五项覆盖最佳，优先完成生产验收 |
| **Keycloak** | Google ✅；Apple ◐；微信 ◐ | WebAuthn passwordless ✅ | ✅ LDAP `WRITABLE` 上游支持；◐ Samba 待验 | ✅ Account Console Applications | ✅ Admin Console；◐ LDAP 可写字段需收口 | 最优第三 Provider PoC |
| **WSO2 Identity Server** | Google ✅；Apple ✅；微信 ◐ | FIDO2/Passkey ✅ | ✅ read-write AD user store；◐ Samba 待验 | ✅ My Account discoverable apps | ✅ Console + read-write AD | 能力强但运维复杂，第二候选 |
| **Casdoor** | Google ✅；Apple ✅；微信 ✅ | WebAuthn ✅ | ✅ 上游已实现 LDAP/AD 写回；◐ Samba/ACL/停用位待验 | ◐ 有应用管理，未核实合格的授权启动器 | ✅ 本地用户；❌ 完整 Samba 权威管理 | 功能存在，但目录权威与安全语义仍不合格 |
| **ZITADEL** | Google ✅；Apple ✅；微信 ◐ generic OAuth | Passkey passwordless ✅ | ❌ 未找到 LDAP/AD 写回能力 | ❌ 无一站式终端用户启动器 | ✅ 本地/组织用户；❌ Samba 权威管理 | 适合 CIAM，不进入当前 Provider 路线 |

### 4.2 双协议、许可与集成风险

| IAM | OIDC Provider | SAML IdP | 主要许可证 | 目录模型 | 主要集成风险 |
| --- | --- | --- | --- | --- | --- |
| LLNG | ✅ | ✅ | GPL-2.0+ | 直接认证/检索 Samba | 无完整用户管理；Passkey 尚未在 Module 启用 |
| Authentik | ✅ | ✅ | 核心 MIT，Enterprise 目录另行许可 | LDAP Source 同步 + 认证/回写 | 本地影子对象与 Samba 权威边界要持续验证 |
| Keycloak | ✅ | ✅ | Apache-2.0 | LDAP/AD Federation，可 `WRITABLE` | 社交 Provider 扩展、嵌套组/锚点/撤权需专项适配 |
| WSO2 IS | ✅ | ✅ | Apache-2.0 | read-write AD/LDAP user store | 运行栈重、配置面大、Module 生命周期成本高 |
| Casdoor | ✅ | ✅ | Apache-2.0 | LDAP/AD 导入 + 远程认证/密码写回，Casdoor 保留独立记录 | anchor 映射不符；AD 改密覆盖 `userAccountControl`；默认对象模型偏本地 IAM |
| ZITADEL | ✅ | ✅ | AGPL-3.0-only（部分目录例外） | 外部 LDAP IdP + ZITADEL 用户 | 无 Samba 写回/应用启动器；需自建同步和 UI |

许可证证据：[Authentik LICENSE](https://github.com/goauthentik/authentik/blob/main/LICENSE)、[Keycloak LICENSE](https://github.com/keycloak/keycloak/blob/main/LICENSE.txt)、[Casdoor LICENSE](https://github.com/casdoor/casdoor/blob/master/LICENSE)、[WSO2 IS LICENSE](https://github.com/wso2/product-is/blob/master/LICENSE)、[ZITADEL licensing policy](https://github.com/zitadel/zitadel/blob/main/LICENSING.md)。

### 4.3 OIDC 与 Module 双向登出

本报告把“双向登出”严格拆成两个方向：

```text
Module -> IAM：RP-Initiated Logout
  应用先结束本地会话，再把浏览器送到 discovery 的 end_session_endpoint；
  IAM 中央会话必须结束，不能返回应用后立即静默登录。

IAM -> Module：OP-Initiated Logout
  用户从 IAM 登出、管理员删除 IAM session 或停用账号时，IAM 通知应用；
  优先使用不依赖浏览器的 OIDC Back-Channel Logout，应用验证 logout token 后清除目标会话。
```

`post_logout_redirect_uri` 只控制导航，不是应用会话通知 endpoint；撤销 access/refresh token 也不自动清除已经建立的应用 Cookie。只有两个方向都通过真实会话测试，才能称为“双向登出同步”。

#### Provider 上游能力与 ANAS 落地状态

| IAM | OIDC Provider | Module -> IAM | IAM -> Module | ANAS 当前状态 | 判定 |
| --- | --- | --- | --- | --- | --- |
| **LLNG 2.23.2** | ✅ | ✅ 标准 RP-Initiated Logout，发布 `end_session_endpoint` | ✅ Front-Channel + Back-Channel；2.17 起支持 back-channel | adapter 已把通用声明翻译为 `LogoutUrl`、`LogoutType=back`、`SessionRequired=1`；Nextcloud 浏览器登出 E2E 脚本已存在，管理员无浏览器撤销尚无覆盖 | ◐ 已实现主路径，保留运行记录后验收 |
| **Authentik 2026.5.6** | ✅ | ✅ `end_session`；ANAS 额外把 User Logout stage 绑定到 provider invalidation flow，使 RP 登出同时结束中央会话 | ✅ Front-/Back-Channel，back-channel 覆盖用户登出、管理员删 session、停用和撤销 | adapter 已优先选择 back-channel；Nextcloud 浏览器和管理员撤销脚本均已存在，仍需保存隔离容器运行结果 | ◐ 当前最完整，待运行证据闭环 |
| **Keycloak** | ✅ | ✅ RP-Initiated Logout，官方 conformance 标为 certified | ✅ Front-/Back-Channel，官方 conformance 标为 certified | 尚无 ANAS Keycloak Module、adapter 或 Provider × Nextcloud E2E | ❌ ANAS 未实现；上游能力适合 PoC |
| **WSO2 IS** | ✅ | ✅ discovery 发布 `end_session_endpoint`，支持 `id_token_hint` 与登出后回调 | ✅ 标准 Back-Channel Logout，可在会话 API 撤销时通知 RP | 尚无 ANAS Module、adapter 或 E2E | ❌ ANAS 未实现 |
| **Casdoor 3.143.0-r1** | ✅ | ✅ discovery 发布 `/api/logout`，接受 `id_token_hint` 和登出后跳转 | ✅ 3.137.0 起 UI/应用对象可配置 back-channel URI；当前上游源码会发送标准 logout token | developing adapter 已写入 `backchannelLogoutUri`，但固定 3.143.0 镜像仍未验证中央会话、`sid`、管理员撤销、重放拒绝和 Nextcloud Cookie 失效 | ◐ 仅配置层接入，不得标为已验收 |
| **ZITADEL** | ✅ | ✅ `/oidc/v1/end_session` | ✅ 当前版本默认启用 Back-Channel Logout，发送带 `sid` 的签名 logout token；不依赖浏览器 | 尚无 ANAS Module、adapter 或 E2E | ❌ ANAS 未实现 |

上表的 ✅ 表示**上游产品能力**，不等于 ANAS 已交付。Keycloak 对 RP-Initiated、Front-Channel 和 Back-Channel Logout 均列为 certified；LLNG 明确列出三种机制；Authentik 的当前文档说明 back-channel 能覆盖管理员删 session；WSO2 给出完整 `end_session -> logout token` 流程；Casdoor 的当前固定版本晚于引入 back-channel 应用字段的 3.137.0；ZITADEL 的旧文档曾写“尚未实现”，但当前官方 back-channel 文档和 feature API 已明确说明默认启用，应以当前证据为准。

#### 当前应用 Module 的实际交集

| Module | OIDC 登录 | Module -> IAM | IAM -> Module | 双向同步结论 |
| --- | --- | --- | --- | --- |
| **Nextcloud** | ✅ `user_oidc` | ✅ 配置 `postlogouturi` 与 `send-id-token-hint=1`，可走 RP-Initiated Logout | ✅ 发布标准 `/backchannel-logout/anas`、`backchannel` 和 session-required | **当前唯一具备完整双向接口的 Module**；Authentik/LLNG E2E 脚本已覆盖反向 Cookie 失效，仍需归档真实运行结果 |
| **MeshCentral** | ✅ | ◐ 配置 `post_logout_redirect_uri`，但未见 ANAS 对中央会话失效的真实 E2E | ❌ 未发布 `OIDC_LOGOUT_URI/METHODS` | 不能宣称双向同步 |
| **NetBird** | ✅ | ◐ 只登记登出后跳转地址，未完成 RP-Initiated Logout 验收 | ❌ 未发布标准应用会话通知 endpoint | 不能宣称双向同步 |
| **oauth2-proxy** | ✅ | ❌ 当前 ANAS 只配置本地 sign-out 所需客户端信息和跳转地址，未验收中央 SSO logout | ❌ 未发布标准应用会话通知 endpoint | 不能宣称双向同步；经 ForwardAuth 的 Module 跟随此限制 |

因此选择 IAM 时必须按 `Provider × Module × 方向` 判断。即使 Keycloak、WSO2 或 ZITADEL 的上游协议实现完整，只要应用 Module 没有 back-channel/front-channel endpoint，IAM 也无法主动清除应用 Cookie；反过来，Nextcloud 暴露了 endpoint，但 Provider adapter 未登记或未发 token，同样不能完成同步。ANAS 的详细契约和测试口径见 [IAM 登出同步到应用的 OIDC / SAML 方案](iam-logout-application-session-sync.md)、[使用 OIDC/SAML 的 Module 双向登出要求](../requirements/module-iam-bidirectional-logout.md)与[新 IAM Provider 准入与实施要求](../requirements/iam-provider.md#8-iam-登出会话同步验收清单)。

## 5. 分项研究

### 5.1 第三方服务绑定

#### 安全模型先于 Provider 数量

外部身份绑定在 ANAS 中应采用以下模型：

```text
Google / Apple / WeChat
          │ 仅证明外部账号控制权
          ▼
IAM 中的认证器绑定记录
          │ 必须指向既有 anasIdentityAnchor
          ▼
Samba AD 用户状态 + 嵌套授权组实时复核
          │
          ▼
OIDC token / SAML assertion
```

禁止以下流程：

```text
外部 email/openid/sub -> 自动创建 IAM 本地用户 -> 直接进入应用
```

原因包括：Apple 可能只给 Private Relay 邮箱，微信资料通常不能提供符合 ANAS `mail` 契约的唯一可投递邮箱，Google 邮箱也不是永久目录锚点。安全实现应要求用户先以 Samba 密码或已登记 Passkey 登录，再主动绑定外部账号；管理员也应能撤销绑定。首次外部登录不得 JIT 创建普通业务账号。

#### 产品差异

- **Authentik**：官方分别提供 [Google](https://docs.goauthentik.io/users-sources/sources/social-logins/google/cloud/)、[Apple](https://docs.goauthentik.io/users-sources/sources/social-logins/apple/) 和 [WeChat](https://docs.goauthentik.io/users-sources/sources/social-logins/wechat/) Source；OAuth Source 支持 Provider-specific 实现和通用 OIDC。它是候选中最直接覆盖三者的方案。需要注意，把 Source Stage 嵌入其他 Flow 的能力被上游标为 Enterprise；ANAS 社区版方案只能依赖核心 Source authentication/enrollment、link policy 和自建编排，并需用 PoC 证明“只绑定既有 Samba 用户”。[Sources](https://docs.goauthentik.io/users-sources/sources/)、[OAuth Source](https://docs.goauthentik.io/users-sources/sources/protocols/oauth/)
- **LLNG**：官方提供 [Google OIDC 配置](https://lemonldap-ng.org/documentation/latest/authopenidconnect_google.html)，并能接任意标准 [OpenID Connect Provider](https://www.lemonldap-ng.org/documentation/2.0/authopenidconnect.html)。Apple 需要处理其 client secret/JWT 和 claim 特性，本轮没有找到 LLNG 官方 Apple 配方；微信也没有官方 Provider 文档，因此不能写成开箱即用。
- **Keycloak**：官方内置社交 Provider 列表含 Google、GitHub、Microsoft 等，但当前列表没有 Apple 或微信；它能代理标准 OIDC/SAML，Apple/微信通常需要第三方 SPI、定制 Provider 或中间桥接。[Identity brokering and social providers](https://www.keycloak.org/docs/latest/server_admin/#_identity_broker)
- **WSO2 IS**：官方有 [Google](https://is.docs.wso2.com/en/latest/guides/authentication/social-login/add-google-login/) 和 [Apple](https://is.docs.wso2.com/en/7.0.0/guides/authentication/social-login/add-apple-login/) 指南；微信不在当前内置 Social Login 列表，可通过 [Custom Connector](https://is.docs.wso2.com/en/latest/guides/authentication/configure-custom-connector/) 和自定义 federated authenticator 扩展。
- **Casdoor**：官方 OAuth 目录直接列出 Google、Apple、WeChat、WeCom、QQ、钉钉、飞书等，是中国生态覆盖最广的候选。[OAuth Provider catalog](https://casdoor.org/docs/category/oauth)
- **ZITADEL**：官方内置 Google、Apple，并提供 generic OAuth/OIDC IdP 模板；微信理论上可走 generic OAuth，但需要验证 profile endpoint、账号唯一键和 claim 映射，故只标 ◐。[Identity brokering](https://zitadel.com/docs/concepts/features/identity-brokering)、[Management API providers](https://zitadel.com/docs/reference/api/management)

### 5.2 免密码登录协议

#### 协议口径：WebAuthn、Passkey、免密码和无用户名不是同一件事

本报告优先认可 FIDO2/WebAuthn Passkey。WebAuthn 注册时由认证器创建密钥对，IAM/Relying Party（RP）保存 credential ID、公钥和校验元数据，私钥保留在平台认证器、安全密钥或用户选择的 Passkey 同步系统中；登录时认证器对服务端随机 challenge 签名。指纹、Face ID、设备 PIN 只在本地解锁私钥，不会把生物特征交给 IAM。[W3C WebAuthn Level 3](https://www.w3.org/TR/webauthn-3/)

必须区分以下能力：

| 能力 | 用户交互 | 凭据要求 | 本报告口径 |
| --- | --- | --- | --- |
| WebAuthn 二次认证 | 用户名 + 密码后再触碰安全密钥/生物识别 | discoverable 或 non-discoverable credential 均可 | 不是免密码 |
| WebAuthn 免密码 | 先输入用户名，再用 WebAuthn 代替密码 | 服务端可按用户名给出 credential ID；也可使用 discoverable credential | 是免密码，但不是无用户名 |
| Passkey 无用户名登录 | 浏览器 autofill/conditional UI、Passkey 按钮或系统选择器直接选择账号 | 必须是 discoverable credential，认证响应返回不含个人信息的 user handle | 完整 Passkey 体验 |
| Magic Link / Email/SMS OTP | 通过邮箱或短信完成登录 | 依赖通信通道 | 可免输密码，但不具备 Passkey 的抗钓鱼和 RP 域绑定属性 |

“Passkey”通常指可发现、可由设备或凭据管理器保存的 WebAuthn credential；因此不能看到产品写“WebAuthn”就推断它支持无用户名登录。OIDC、SAML、OAuth 是应用与 IAM/外部 IdP 的联邦协议，本身不是免密码协议，PKCE 也不是用户认证器。

#### ANAS 的 Passkey 绑定关系

Passkey 不应写入 Samba 的 `unicodePwd`，也不应把 Samba 密码复制进 IAM。它是绑定在 IAM 用户对象上的独立认证器，而 IAM 用户对象必须继续映射到唯一的 `anasIdentityAnchor`：

```text
Samba AD user
  └─ anasIdentityAnchor（永久身份键）
       └─ IAM 用户/影子对象
            └─ WebAuthn user handle（随机、不含用户名/邮箱）
                 ├─ credential A：平台 Passkey
                 └─ credential B：硬件安全密钥

认证器/凭据管理器：保存私钥，可能按用户选择在其设备间同步
IAM：保存 credential ID、公钥、计数器/备份状态、AAGUID、transports 和用户自定义名称
Samba：仍只保存目录密码、账号状态、anchor、组和业务属性
```

W3C 要求 WebAuthn `user.id` 是最多 64 字节的不透明 user handle，不应包含用户名、邮箱或可猜测的未加盐哈希。因此 ANAS 不应直接把 `sAMAccountName`、`mail` 或 `anasIdentityAnchor` 文本暴露为 user handle；应生成随机稳定值，并在 IAM 数据库内维护 `user handle -> IAM user -> anasIdentityAnchor` 映射。用户名改名只更新显示属性，不改变这条绑定链。

#### 推荐绑定/登记流程

ANAS 应统一使用以下流程，不允许匿名用户只凭一个可控邮箱登记到现有目录账号：

1. 用户先以 Samba 密码、已有 Passkey 或管理员签发的受限恢复流程登录；进入“安全/认证器”页面时要求 fresh authentication 或 step-up，旧的长期 SSO Cookie 不能直接登记新凭据。
2. IAM 实时确认 Samba 用户存在、未停用/锁定、`anasIdentityAnchor` 唯一且存在；若账号是本地 break-glass 用户，则凭据只能绑定本地管理员，不能伪装成业务用户。
3. 用户选择“添加 Passkey”，填写可辨识名称，例如“MacBook Touch ID”或“YubiKey 5”。
4. IAM 生成一次性高熵 challenge 和 `PublicKeyCredentialCreationOptions`：
   - RP ID 固定为正式认证域，不使用容器名、临时域名或动态 `domain_prefix`；
   - `user.id` 使用上述随机稳定 handle；
   - passwordless 至少要求 `userVerification=required`；
   - 要支持无用户名/autofill，`residentKey` 应为 `required`，兼容过渡期可先用 `preferred` 并显式测试实际返回的 discoverable credential；
   - 默认允许平台和 cross-platform authenticator；只有明确的合规场景才基于 attestation/AAGUID 限制设备。
5. 浏览器调用 `navigator.credentials.create()`；认证器经用户手势创建密钥。IAM 校验 challenge、origin、RP ID、attestation 格式、credential 唯一性和 user verification 后，只保存公钥记录并绑定到当前 IAM 用户。
6. IAM 写入审计事件，至少包含 anchor、credential ID 的不可逆摘要、AAGUID/设备类型、操作者、时间和结果；不得记录 challenge 响应原文、私钥或生物特征。
7. 登记完成后立即做一次 assertion 验证，避免保存实际不可用的凭据；建议每个用户登记至少两个独立 Passkey，并保留 Samba 密码作为初期回退方式。

#### 使用/登录流程

```text
浏览器打开 IAM 登录页
  ├─ 无用户名：conditional UI / “使用 Passkey” -> 浏览器列出 discoverable credentials
  └─ 用户名优先：输入用户名 -> IAM 返回该用户允许的 credential IDs
             ↓
认证器本地验证用户并对一次性 challenge 签名
             ↓
IAM 校验 challenge + origin + RP ID hash + signature + user verification
             ↓
credential -> IAM user -> anasIdentityAnchor
             ↓
实时读取 Samba：用户存在、未停用/锁定、anchor 唯一、嵌套授权组仍满足
             ↓
通过才创建 IAM 会话并签发 OIDC token / SAML assertion
```

Passkey 只完成“证明持有已绑定私钥”，不能代替 Samba 授权判断。即使签名有效，只要目录用户已删除、停用、锁定、anchor 缺失/重复，或已移出 `APP_<app> OR APP_all OR Admins`，IAM 都必须拒绝新会话或应用凭据。若产品只能离线验证本地影子用户、无法在可接受时限内收敛 Samba 状态，就不满足 ANAS 准入条件。

#### 撤销、设备丢失和恢复流程

- 用户在已认证的安全设置页查看多个凭据的名称、创建时间和最近使用时间，可逐个删除；管理员也能按 anchor 撤销全部 Passkey 并写审计。
- 删除 credential 后，其公钥记录立即失效，不影响 Samba 密码和其他 Passkey；设备端残留私钥也不能再登录。
- 设备丢失时，用户用第二枚 Passkey 或 Samba 密码登录并删除丢失凭据。普通用户不应依赖平台管理员 break-glass 账号恢复。
- 同步型 Passkey 的跨设备恢复由用户选择的 Apple/Google/密码管理器生态负责，IAM 不接触其私钥；ANAS 只恢复服务端公钥绑定记录。
- 备份/恢复必须包含 IAM 数据库中的 user handle 和 credential records，并验证恢复后仍映射同一 `anasIdentityAnchor`。从其他 IAM 迁移时通常不能复制 Passkey 私钥，应规划旧域并行期和重新登记。
- Samba 用户停用或删除应立即成为总闸门；不能要求管理员再去每个 IAM 中手工删除 Passkey 才阻止登录。

#### 六种 IAM 的登记、使用和绑定差异

| IAM | 支持的模式 | 用户如何登记/绑定 | 登录体验 | 绑定落点与 Samba 风险 | ANAS 状态 |
| --- | --- | --- | --- | --- | --- |
| **LLNG 2.23.2-r11** | WebAuthn 2FA；2.20 起 Passkey 第一因素，官方仍标 **Beta** | 先使用常规 LDAP/AD backend 登录，在 Portal 的 Second factor manager 注册；管理员可开启自助登记/删除，也可通过 `_2fDevices` 预配 | Passkey 模式要求 WebAuthn 2FA 已启用，`discoverable credential=preferred/required`；non-discoverable credential 只能作二次认证 | WebAuthn 设备记录关联 LLNG 用户会话/持久化记录，用户 backend 仍可用 LDAP/AD；但 Passkey 登录时对 Samba disabled/locked、anchor 和嵌套组的实际读取时序仍需 E2E | 上游可用，ANAS 未启用；**当前首选 PoC** |
| **Authentik 2026.5.6-r10** | WebAuthn 2FA；独立 passwordless Flow；discoverable credential 的 Passkey autofill/conditional UI | 在 WebAuthn Authenticator Setup Stage 登记；可把该 Stage 放入受认证的用户设置 Flow，配置 user verification、resident key、平台/跨平台类型及防重复 | Authenticator Validation Stage 验证 `webauthn` device；Identification Stage 引用它即可在用户名框提供 Passkey autofill，无需先输入用户名 | 设备作为已确认的 `WebAuthnDevice` 绑定 Authentik 用户；LDAP Source 用户是本地同步对象，Passkey 验证本身不会验证 LDAP 密码，因此必须证明同步/策略能及时拦截 Samba 停用、删除和移组 | 上游功能完整，ANAS 未验收；**与 LLNG 并行 PoC** |
| **Keycloak** | WebAuthn 2FA；passwordless 第一因素；LoginLess；Passkey conditional UI/autofill 和 modal UI | 管理员启用 `Webauthn Register Passwordless` required action，用户首次登录或 Account Console/AIA 完成登记；passwordless 使用独立 WebAuthn Passwordless Policy | 传统 passwordless Flow 可先问用户名；LoginLess/Passkeys 可直接选择 discoverable credential，不输入密码；仍可配置密码/OTP 回退 | credential 绑定 Keycloak 用户。官方明确 `user.id` 使用内部数据库 ID；外部 User Federation 的 storage ID 可能超过 WebAuthn 64 字节限制，Samba federation ID 长度、改名稳定性和 Import Users 策略必须 PoC | 最成熟第三候选，但 ANAS 尚无 Module |
| **WSO2 IS** | WebAuthn/FIDO2 passwordless；安全密钥和平台 Passkey；稳定版支持预登记/渐进登记；下一版文档提供 usernameless 开关 | 用户在 My Account 的 `Security > Additional Authentication > Passkey` 添加并命名；或管理员在应用 Login Flow 加 Passkey 并启用 progressive enrollment，首次用密码确认后现场登记 | 稳定版支持 Passwordless Login Flow；当前 `next` 文档把 usernameless 作为组织级配置，关闭时先输入用户名 | Passkey 必须关联已存在/已预配的 WSO2 用户；外部联邦用户未预配会登记失败。read-write AD user store 是否在每次 assertion 后重新检查 Samba 状态仍需验证 | 能力强，但固定版本与部署成本待 PoC；不能把 `next` 功能当已交付 |
| **Casdoor 3.143.0-r1** | WebAuthn 可替代密码，也可与密码并用；官方未证明 usernameless/conditional UI | 管理员先在 Application 开启 `Enable WebAuthn signin`；用户登录 My Account，选择 `Add WebAuthn Credential`，可从列表删除 | 登录页选择 WebAuthn，**先输入用户名**，再完成指纹/Windows Hello/安全密钥验证 | credential 绑定 Casdoor 本地用户记录。LDAP/AD 用户是导入后的独立记录，因此有效 WebAuthn 可能绕过实时 LDAP 认证；必须证明 Syncer 的 `isForbidden` 与目录停用/组撤权可及时阻断 | 上游支持免密码，但目录权威风险高，不进入当前路线 |
| **ZITADEL** | FIDO2/WebAuthn Passkey passwordless；平台或 cross-platform；Self Service 管理 | 新用户 API 可请求 passwordless registration link；既有用户可通过 registration link、Login V2 或 Self Service 添加、命名、查看和删除 | Hosted Login/Login V2 可选择 Passkey；当前默认策略文档描述为先输入 login name 再验证，定制 Login UI 可直接调用 Passkey API | Passkey 绑定 ZITADEL 本地 human user；LDAP 只是外部 IdP，Passkey 登录不会天然重新认证 Samba。这与“每次签发前由 Samba 做总闸门”冲突 | 适合 CIAM，不进入 Samba 主身份源路线 |

产品证据：[LLNG Passkeys](https://lemonldap-ng.org/documentation/latest/authwebauthn.html) 与 [WebAuthn 参数](https://lemonldap-ng.org/documentation/2.0/parameterlist.html)、Authentik [WebAuthn Setup Stage](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/authenticator_webauthn/)、[Authenticator Validation Stage](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/authenticator_validate/) 与 [Identification Stage](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/identification/)、[Keycloak Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/)、WSO2 [Register passkeys](https://is.docs.wso2.com/en/latest/guides/user-self-service/register-passkey/) 与 [Passkey Login Flow](https://is.docs.wso2.com/en/next/guides/authentication/passwordless-login/add-passwordless-login-with-passkey/)、[Casdoor WebAuthn](https://casdoor.org/docs/how-to-connect/webauthn/)、ZITADEL [Passkeys](https://zitadel.com/docs/concepts/features/passkeys) 与 [registration API flow](https://zitadel.com/docs/guides/integrate/login-ui/passkey)。

#### RP ID、域名和迁移约束

WebAuthn credential 受 RP ID 和 origin 约束。生产环境必须使用 HTTPS（`localhost` 测试例外）；例如登录 origin 为 `https://auth.example.com` 时，RP ID 可以是 `auth.example.com` 或合规的父域 `example.com`，但不能随意改成另一个可注册域。由此产生以下 ANAS 约束：

1. 安装阶段必须先确定正式 IAM 外部域名和 RP ID，再开放登记；不能使用容器 hostname、IP、Traefik 临时域名或每次重建会变化的前缀。
2. `domain_prefix` 或 base domain 变化不能作为普通无感 reconcile。若新域不在原 RP ID 范围内，旧凭据无法使用，升级器必须阻止或明确要求并行域、密码回退和全员重新登记。
3. 若选择父域 RP ID 以跨子域使用，等于扩大 credential 可被请求的 origin 范围，必须严格维护允许 origin 列表并控制子域接管风险；默认更稳妥的是固定专用认证主机。
4. 备份恢复到不同域名时，恢复公钥记录并不足以恢复登录；ZITADEL 官方迁移文档也明确要求新系统保持相同域，不能直接迁移其他系统的 Passkey。

因此，ANAS 的 Passkey 能力不只是增加一个登录按钮，还必须把 RP ID 作为 Provider 持久化契约，并纳入安装校验、域名变更预检、备份恢复和灾难恢复演练。

### 5.3 Samba 密码回写

#### 当前已实现状态

- **LLNG**：ANAS 的改密表单通过受限 LDAPS 服务账号写回 Samba；E2E 验证 `pwdLastSet` 变化和新密码可认证。委派 reset 语义不能可靠强制 Samba history/minimum age，因此这两项仅显示提示，不虚报强制能力。
- **Authentik**：上游 LDAP Source 有 `User password writeback` 开关；ANAS 已实现受限服务账号、最小长度/复杂度预检查、LDAP 错误安全映射，并在成功后把同步用户的 Authentik 本地密码恢复为 unusable，避免第二份业务密码。[Authentik LDAP Source](https://docs.goauthentik.io/users-sources/sources/protocols/ldap)

共同契约和当前差异见 [Module IAM / OIDC 支持清单的 Samba 目录密码接入规范](../reference/module-iam-support.md#samba-目录密码接入规范)；各 Provider 的实现说明位于仓库 `modules/authentik/README.md` 与 `modules/llng/README.md`。

#### 新候选

- **Keycloak**：LDAP Federation 的 `Edit Mode=WRITABLE` 明确允许用户和管理员修改密码并自动同步到 LDAP；`READ_ONLY` 不允许密码更新，`UNSYNCED` 会把密码存入 Keycloak 本地库，后者不符合 ANAS。[Keycloak LDAP Edit Mode](https://www.keycloak.org/docs/latest/server_admin/#_ldap)
- **WSO2 IS**：官方提供 read-write Active Directory user store，`UniqueIDActiveDirectoryUserStoreManager` 只用于读写操作；通过 My Account/Console/SCIM 的密码更新应落到所选 user store。[Primary user store types](https://is.docs.wso2.com/en/latest/guides/users/user-stores/primary-user-store/)、[Read-write AD configuration](https://is.docs.wso2.com/en/7.1.0/guides/users/user-stores/primary-user-store/configure-a-read-write-active-directory-user-store/)
- **Casdoor**：已支持目录密码写回。2024 年 12 月合并的 [PR #3395](https://github.com/casdoor/casdoor/pull/3395) 将 LDAP 用户的 `SetPassword` 流程接到 `ResetLdapPassword`，不再把该次新密码持久化为 Casdoor 本地密码；AD 分支以带引号的 UTF-16LE 值替换 `unicodePwd`，普通 LDAP 按场景使用 Password Modify 或替换 `userPassword`。维护者在 2026 年的 [issue #5634](https://github.com/casdoor/casdoor/issues/5634) 中再次确认该能力并以“已实现”关闭。该能力仍只能记为“上游存在、ANAS 待验”：AD 写密码要求 LDAPS，当前实现同时把 `userAccountControl` 替换为 `512`，可能覆盖 Samba 中的禁用状态或其他账号标志；还需验证最小权限 ACL、密码策略错误、失败回滚和本地哈希残留。[LDAP overview](https://casdoor.org/docs/ldap/overview/)、[AD Syncer](https://casdoor.org/uk/docs/syncer/ActiveDirectory/)
- **ZITADEL**：官方把 LDAP 作为外部身份 Provider，未找到 LDAP/AD password modify 或 writeback 能力；因此不满足本项。

Keycloak、WSO2 与 Casdoor 的“上游支持读写 AD/密码”仍不能直接等同于“通过 ANAS Samba 验收”。PoC 必须验证 LDAPS、最小权限 ACL、Samba 返回码、密码策略、旧密码宽限、`pwdLastSet=0`、账号状态位、本地哈希残留和故障恢复。

### 5.4 应用列表/启动器

合格的应用列表必须是终端用户登录后的 launchpad，并按 `APP_<app> OR APP_all OR Admins` 过滤；管理员看到的 Clients/Applications 配置页不算。

- **LLNG**：Portal Menu 原生提供 Application list、分类、URI、Logo 和按 access rule 自动显示；ANAS 已按应用目录生成配置。[Portal menu](https://lemonldap-ng.org/documentation/2.0/portalmenu.html)
- **Authentik**：User Interface 的首页就是 Application Dashboard，可按 Application binding 控制可见性和访问权；ANAS 已为 Consumer 创建 Application 对象。[User interface](https://docs.goauthentik.io/users-sources/user/user-interface/)、[Manage applications](https://docs.goauthentik.io/add-secure-apps/applications/manage_apps/)
- **Keycloak**：Account Console 的 Applications 菜单显示用户可访问的应用，client 也有 `Always Display in Console` 配置。它能满足基础列表，但 ANAS PoC 要验证链接 URL 与组撤权后可见性收敛。[Account Console](https://www.keycloak.org/docs/latest/server_admin/#_account-service)
- **WSO2 IS**：My Account 的 Applications 区可发现和打开组织内应用；业务应用标记 `Discoverable application` 并配置 access URL 后显示。[Discoverable applications](https://is.docs.wso2.com/en/7.0.0/guides/applications/)、[My Account configuration](https://is.docs.wso2.com/en/next/guides/user-self-service/configure-self-service-portal/)
- **Casdoor**：有完整 Applications 管理页与 API，但本轮未找到官方文档证明存在按终端用户授权过滤并可直接启动的独立 launchpad，故标 ◐，不能用管理员 Dashboard 代替验收。
- **ZITADEL**：Applications 位于 Project/Management Console，官方还建议不让普通终端用户进入 Console；没有等价的一站式用户应用启动器，需要 ANAS 另建 Portal。[Applications](https://zitadel.com/docs/guides/manage/console/applications-overview)、[Restrict Console](https://zitadel.com/docs/guides/solution-scenarios/restrict-console)

### 5.5 用户管理界面

需要区分两层：

| 层次 | 问题 | ANAS 是否需要 |
| --- | --- | --- |
| IAM 对象管理 | 能否查看/编辑 IAM 本地用户、会话、认证器、角色？ | 需要，但不能替代目录管理 |
| Samba 权威管理 | 能否创建/停用 AD 用户、改目录属性/组/密码，并受 AD ACL 约束？ | 需要；当前由 LAM/目录工具承担 |

- **LLNG** 有 Manager 管理 SSO 配置，但不是用户目录管理器；当前应继续通过 LAM 或 `samba-tool` 管理用户和组。
- **Authentik** Admin Interface 能管理用户、组、密码、会话和权限，但 LDAP Source 同步用户是镜像对象。ANAS 应把普通用户创建、停用、目录组与权威属性编辑留给 Samba/LAM；Authentik UI 主要管理认证器、会话、Source 和 IAM 策略。[About users](https://docs.goauthentik.io/users-sources/user)
- **Keycloak** Admin Console 能管理用户、组、角色、会话和 credential；LDAP `WRITABLE` 可把映射字段写回目录。对 ANAS 必须限制可写 mapper，避免 IAM 修改不属于它的 AD 属性或创建本地业务用户。[Managing users](https://www.keycloak.org/docs/latest/server_admin/#assembly-managing-users_server_administration_guide)
- **WSO2 IS** Console 能管理用户，read-write AD user store 能让编辑落到 AD，功能最接近统一目录管理面；但仍需验证 Samba schema、ACL、嵌套组和 ANAS anchor 映射。[Manage users](https://is.docs.wso2.com/en/7.0.0/guides/users/manage-users/)
- **Casdoor** 和 **ZITADEL** 都有成熟本地用户/组织管理 UI，但不能证明一般用户、组和目录属性操作以 Samba 为权威落点，故不能替代 LAM。Casdoor 的目录密码写回是一个明确例外，不等于完整的 Samba 用户生命周期管理。

## 6. 候选逐项结论

### 6.1 LLNG：继续作为生产基线

优势：

- ANAS 当前唯一 `release` IAM；
- 直接使用 Samba 认证/检索，身份权威边界简单；
- OIDC、SAML、应用 Portal 和组规则已进入统一 IAM Contract；
- OIDC RP-Initiated Logout 与应用 back-channel 通知均已进入 adapter，Nextcloud 反向会话失效脚本已建立；
- 密码写回及错误提示已有服务器 E2E；
- 上游已有 Passkey first-factor，可在不更换 Provider 的情况下补免密码能力。

不足：

- Apple/微信没有开箱即用官方集成；
- 无完整用户管理 UI；
- Passkey 页面仍标 Beta，ANAS 当前镜像也未安装、启用并验证所需组件；
- 没有独立本地 break-glass，目录或认证链故障时只能从主机侧恢复。

建议：维持生产默认，不因新功能调研立即迁移；建立 Passkey 独立 PoC，并保留 LAM 作为目录管理面。

### 6.2 Authentik：最符合原五项功能目标

优势：

- Google、Apple、微信均有官方社交 Source；
- 核心 Source 和 policy 提供安全绑定所需基础，但“先目录认证，再绑定外部身份”的社区版完整流程仍需 PoC，不能依赖 Enterprise-only Source Stage；
- 原生 WebAuthn passwordless 与 conditional UI；
- Application Dashboard 和 Admin Interface 完整；
- ANAS 已实现 Samba LDAPS Source、`anasIdentityAnchor` 唯一性、OIDC/SAML per-app endpoint、组策略和密码写回。
- ANAS 已为 OIDC 配置完整 SLO：应用发起退出会结束 Authentik 中央会话，IAM/管理员撤销则优先经 back-channel 清理 Nextcloud 会话。

不足：

- 当前 Module 仍是 `developing`；
- 同步用户在 Authentik 仍有影子对象，必须持续验证不会保留可登录本地密码；
- 社交登录与 Passkey 尚未进入 ANAS 配置契约和 E2E；
- 双向登出的 E2E 脚本虽已存在，但报告中尚无隔离容器执行记录，不能只凭 blueprint 单元测试把该项关闭；
- Authentik UI 不是完整 AD 管理器，Samba 用户生命周期仍应在 LAM/目录层管理。

建议：优先完善现有 Module，而不是先增加第三 Provider。

### 6.3 Keycloak：最值得新增的第三 Provider

优势：

- 双协议、身份代理、LDAP/AD Federation、Passwordless WebAuthn、Account Console 和 Admin Console 都是成熟上游能力；
- OIDC RP-Initiated、Front-Channel 和 Back-Channel Logout 均通过官方 conformance，协议风险低；
- `WRITABLE` 与 `UNSYNCED` 的语义区分清晰，可以明确禁止本地密码副本路线；
- Admin API/CLI 适合实现 ANAS 幂等 adapter；
- Apache-2.0，社区规模与生态成熟。

不足：

- Apple/微信需要 SPI、第三方扩展或桥接，增加供应链和升级风险；
- 上游 LDAP 模型默认会导入/缓存用户对象，需要证明账号改名、删除、禁用和组撤权能正确收敛；
- `anasIdentityAnchor`、固定 claim 类型、递归 `memberOf` 和应用组规则都需要自建 mapper/adapter；
- 尚无 ANAS client registration/logout adapter；必须用 Nextcloud 同时验证应用发起退出与管理员无浏览器删 session；
- 必须确保本地自注册、JIT 社交用户和 `UNSYNCED` 密码全部关闭。

建议：只有在 Authentik 生产验收后仍有明确缺口时再实施，PoC 不应直接进入 `release`。

### 6.4 WSO2 Identity Server：能力全面但成本最高

优势：

- read-write AD 是正式 user store 类型，而非附加同步器；
- Google、Apple、FIDO2、可发现应用和完整用户管理 UI 均有官方文档；
- OIDC `end_session_endpoint` 和标准 back-channel logout 流程完整，可覆盖 RP 发起和会话 API 撤销；
- 丰富的 Console、My Account、SCIM/API 和委派管理能力。

不足：

- 微信需要自定义 Connector；
- Java/Carbon 系运行、配置、数据库、升级和故障排查面明显更重；
- 广泛可写 AD 能力与 ANAS 最小权限原则存在张力，必须进行字段/ACL 收口；
- 对家庭/小团队自部署场景可能过度设计。
- 尚无 ANAS Module；上游登出能力不能抵消 adapter、最小配置和真实应用会话 E2E 的实施成本。

建议：作为“需要统一 AD 管理面和复杂企业工作流”时的备选，不作为默认第三 Provider。

### 6.5 Casdoor：社交生态强，已有密码写回，但目录模型仍不合格

优势：

- Google、Apple、微信、企业微信、QQ、钉钉、飞书等内置 Provider 丰富；
- OIDC、SAML IdP 和 WebAuthn 齐全；
- 固定的 3.143.0 版本已具备 OIDC RP-Initiated Logout 和 back-channel 通知字段，当前 adapter 也会登记 Consumer 的 `backchannelLogoutUri`；
- 上游已实现 LDAP/AD 用户自助改密和管理员重置写回，LDAP 用户不会在该流程中持久化第二份本地密码；
- UI-first，用户、组织、角色和应用管理直观；
- Apache-2.0，Go 服务便于容器化。

阻断问题：

- LDAP/AD 是同步/登录校验来源，用户记录导入后独立；
- AD Syncer 当前将 `objectGUID` 映射为 Casdoor ID，与 ANAS 指定 `anasIdentityAnchor` 的永久锚点契约冲突；
- AD 改密实现除 `unicodePwd` 外还把 `userAccountControl` 替换为 `512`，必须证明不会重新启用已停用账号或清除 Samba 账号标志；
- 密码写回尚未通过 ANAS 的 LDAPS、最小权限 ACL、Samba 密码策略、失败语义和本地哈希残留 E2E；
- 双向登出只完成配置生成：还没有证明 Casdoor logout token 可被 Nextcloud 接受、`sid` 精确匹配、管理员撤销有效或重放被拒绝；
- 本地 Password 字段和丰富的本地注册模型容易形成第二身份源；
- 未确认终端用户授权应用启动器。

建议：不进入当前 Provider 路线。若未来目标改为 CIAM 或中国社交账号优先、Samba 不再是唯一身份源，可重新评估。

### 6.6 ZITADEL：现代 CIAM，但与 Samba 中心模型错位

优势：

- OIDC、SAML、Passkey、组织/项目/角色、审计和管理 API 完整；
- 当前上游同时支持 RP-Initiated Logout 和默认启用的 OIDC Back-Channel Logout；
- Google、Apple 与 generic OAuth/OIDC 身份代理；
- 自部署活跃，Login V2 适合应用原生体验。

阻断问题：

- LDAP 是外部 IdP，不是可写的权威 user store；
- 没有 Samba 密码回写证据；
- 没有终端用户应用启动器，官方甚至提供阻止普通用户访问 Management Console 的指南；
- 需要本地 ZITADEL 用户/组织对象和自建同步逻辑；
- ANAS 尚无 ZITADEL Module 和应用注册 adapter，因此双向登出仍是上游能力而非可用集成；
- v3+ 主仓库为 AGPL-3.0-only，需要单独做许可证合规评估。

建议：不进入现有 IAM Provider 路线；如果 ANAS 将来为外部应用提供独立 CIAM，可作为新主题研究。

## 7. 明确排除或降级的常见项目

| 项目 | 原因 |
| --- | --- |
| Authelia | 当前 ANAS 准入要求同时提供 OIDC 与 SAML IdP；Authelia 不满足双协议硬条件 |
| Dex | 主要是 OIDC Provider/身份代理，不是完整 SAML IdP + 用户管理平台 |
| Ory Kratos/Hydra | 组件化身份栈，SAML、应用门户和目录管理需额外产品/自建，不适合当前单 Provider Contract |
| LLDAP | 是轻量 LDAP 目录/管理 UI，不是同时提供 OIDC/SAML 的 IAM Provider |
| Kanidm | 目录与 OAuth2/OIDC 能力强，但不满足当前 SAML IdP 准入条件 |
| FreeIPA | 是完整目录/Kerberos/DNS/CA 平台，不是面向现有 Samba AD 的替代 IAM adapter；并会引入第二目录权威 |
| Logto | 更偏 CIAM；SAML/企业能力和目录权威模式需另做社区版边界核验，不优于本轮前三候选 |

若未来放宽“双协议必须由同一 Provider 提供”的硬约束，应重新单独评估这些项目，不能直接复用本轮排名。

## 8. 推荐实施路线

### 阶段 1：完善现有两种 Provider

1. 为 Authentik 增加第三方 Source 的 ANAS 配置 PoC，但默认关闭：
   - `google`、`apple`、`wechat` 分开声明；
   - Secret 进入 Provider 专属 Secret；
   - enrollment 必须禁用，只允许已登录 Samba 用户主动 link；若社区版无法在不使用 Enterprise Source Stage 的前提下满足这一点，则停止该方案；
   - 禁止按 email 自动匹配，绑定记录必须指向 `anasIdentityAnchor`。
2. 为 Authentik 建立 WebAuthn passwordless Flow PoC：
   - 创建 WebAuthn Authenticator Setup Stage、只允许 `webauthn` 的 Authenticator Validation Stage，并由 Identification Stage 引用后启用 Passkey autofill；
   - `userVerification=required`、`residentKey=required`，登记入口要求刚完成的 Samba 认证或已有强认证器 step-up；
   - 先用 Samba 登录登记 Passkey，之后分别验证 username-first、Passkey 按钮和 conditional UI；
   - WebAuthn user handle 必须是随机不透明值，并稳定映射到同一 `anasIdentityAnchor`；
   - 每次登录仍在签发凭据前校验 Samba enabled/locked、anchor 和嵌套组；删除 Samba 用户、移组或停用后 Passkey 不得继续授权。
3. 为 LLNG 建立同样的 Passkey PoC：
   - 启用 WebAuthn second factor、自助登记/删除和 discoverable credential，再以 Backend choice 增加 `WebAuthn authentication + LDAP/AD user backend`；
   - 验证 non-discoverable credential 只能作为二次认证，不能误标为 passwordless；
   - 记录上游 Beta 风险、所需 Perl 包、`_2fDevices`/会话持久化、备份恢复和 Samba 状态读取时序。
4. 保持 LAM 为 Samba 用户/组管理入口；Authentik Users 页面不宣传为 AD 全功能管理器。
5. 在隔离 Docker fixture 中执行并归档 Authentik/LLNG × Nextcloud 的双向登出结果；不能用 render、blueprint 或 HTTP 302 代替原应用 Cookie 失效断言。
6. 逐个盘点 MeshCentral、NetBird、oauth2-proxy 是否存在标准 back-channel/front-channel endpoint；没有 endpoint 时明确标为“不支持 IAM 主动即时登出”，不登记普通 `/logout` 页面冒充协议端点。
7. 把正式 RP ID 作为 Provider 持久化配置：安装后变更 IAM 域名时先阻止无感 reconcile，给出旧域并行、密码回退、重新登记和回滚计划。

### 阶段 2：把 Authentik 从 `developing` 提升到候选发布质量

完成现有 Provider 强制 E2E：

- 直接组、`APP_all`、`Admins`、嵌套组与循环组；
- 禁用账号；
- 改名但 anchor 不变；
- `Admins` 移除后的 superuser 撤销；
- OIDC/SAML claim、数组类型和 per-app endpoint；
- OIDC 双向登出：Nextcloud 发起退出结束中央 SSO、IAM 浏览器登出清除 Nextcloud Cookie、管理员无浏览器删 session 同样清除 Cookie；
- 非法、过期、错误 `iss/aud/events` 和重放 logout token 被拒绝且不影响合法会话；
- Samba 密码写回、失败映射、本地哈希 unusable；
- break-glass 轮换和恢复；
- 备份/恢复后 anchor 与外部账号/Passkey 绑定不变。

### 阶段 3：有明确需求时再做 Keycloak PoC

Keycloak Module 的最小 PoC 范围：

1. PostgreSQL、Traefik、备份、bootstrap/break-glass；
2. LDAP Federation：`WRITABLE`、Import Users 策略、LDAPS truststore、最小权限 bind；
3. `anasIdentityAnchor` 唯一 mapper、`sAMAccountName`、`mail`、`displayName`、账号状态；
4. 嵌套组与固定应用组策略；
5. OIDC/SAML per-consumer adapter 与应用列表；
6. WebAuthn passwordless；
7. Google 原生 Provider；Apple/微信扩展必须锁版本、做供应链审计并证明升级兼容；
8. 运行与 LLNG/Authentik 完全相同的 E2E 矩阵；
9. OIDC logout adapter 必须登记 Nextcloud back-channel URI，并验证 RP-Initiated Logout、IAM 浏览器登出和管理员后台撤销三个方向/触发源。

在这些步骤完成前，生命周期只能是 `developing`。

## 9. 新增验收矩阵

除既有 IAM Provider E2E 外，本轮六项能力至少增加以下测试：

| 场景 | 期望结果 |
| --- | --- |
| 未登录用户首次用 Google/Apple/微信 | 不自动创建普通业务用户；提示先用目录身份绑定或拒绝 |
| 已登录 Samba 用户绑定外部账号 | 绑定落到同一 `anasIdentityAnchor`，应用侧不产生第二用户 |
| 两个 Samba 用户尝试绑定同一外部 `sub/openid` | 第二次绑定被拒绝并审计 |
| 外部邮箱变化、Apple relay 变化 | 不改变 anchor，不新建用户 |
| Samba 用户停用但外部账号仍有效 | IAM 拒绝签发 token/assertion |
| Samba 用户移出应用组 | 外部登录/Passkey 成功证明身份后仍被应用授权拒绝 |
| Samba 用户改名、anchor 不变 | 外部绑定与 Passkey 仍关联原应用身份 |
| 目录用户登记 Passkey 后免密码登录 | 不询问 Samba 密码，但仍校验目录状态与组 |
| 用长期旧 SSO Cookie 登记新 Passkey | 要求 fresh authentication/step-up，不能直接新增凭据 |
| 登记 discoverable credential | username-first、Passkey 按钮和 conditional UI 均按 Provider 声明工作；浏览器不支持 conditional UI 时可安全回退 |
| 登记 non-discoverable credential | 只允许进入已声明的 WebAuthn 2FA 流程，不得宣传或误用于 usernameless 登录 |
| 第二个 Samba 用户登记同一 credential ID | 登记被拒绝并审计，原绑定不变 |
| 使用过期/重放 challenge、错误 origin 或错误 RP ID | 注册和 assertion 均拒绝，不创建凭据/会话，不泄露账户是否存在 |
| 删除/撤销 Passkey | 该 credential 立即不可使用，不影响 Samba 密码 |
| 同一用户有两枚 Passkey，删除其中一枚 | 被删凭据立即失效，另一枚和 Samba 密码继续可用 |
| Samba 用户停用/锁定/删除后使用有效 Passkey | WebAuthn 签名即使有效也不得创建 IAM 会话或签发应用凭据 |
| IAM 备份恢复到相同域 | user handle、credential records 与 anchor 绑定保持不变，凭据仍可验证 |
| IAM 域名/RP ID 发生不兼容变化 | 变更前明确阻止或进入受控重登记流程；不得宣称旧 Passkey 可无感迁移 |
| IAM 改密成功 | Samba `pwdLastSet` 改变，新密码可 LDAP 认证，IAM 无第二本地业务密码 |
| IAM 改密被 Samba 策略拒绝 | 用户得到安全且可操作的提示，原始 LDAP 诊断只进审计日志 |
| 应用目录新增/删除/改组 | Portal/Dashboard 幂等收敛，可见性与实际授权一致 |
| IAM 用户管理 UI 修改受管字段 | 只允许契约内字段，写入 Samba；不可写字段被拒绝而非写入本地副本 |
| 用户从 Nextcloud 点击退出 | Nextcloud 本地 Cookie 失效，IAM 中央会话结束；再次访问应用不能依靠旧 SSO 会话静默恢复 |
| 用户从 IAM Portal 点击退出 | IAM 向 Nextcloud back-channel endpoint 发送签名 logout token，登出前保存的 Nextcloud Cookie 被拒绝 |
| 管理员无浏览器删除 IAM session | 对应 Nextcloud Cookie 在限定时间内失效；其他用户和其他 session 不受影响 |
| 伪造、过期或重放 logout token | 应用拒绝请求并审计；不得误删合法 session，不得返回敏感诊断 |
| MeshCentral/NetBird/oauth2-proxy 未声明通知 endpoint | Provider 不猜测普通 `/logout` URL；文档明确只能本地退出或等待 token/session 自然过期 |

## 10. 最终决策表

| 决策 | 结果 |
| --- | --- |
| 当前生产 IAM | 继续 LLNG |
| 当前重点投入 | 完成 Authentik 生产验收，随后增加安全的社交绑定与 Passkey |
| 是否立即引入第三 IAM | 否 |
| 若必须引入第三 IAM | Keycloak 第一，WSO2 IS 第二 |
| 是否因微信支持选择 Casdoor | 否；Casdoor 已有密码写回，但仍未满足 anchor、账号状态保持、完整目录权威和应用启动器要求 |
| 是否把 IAM Users 页面当 Samba 管理面 | 否；只有写入 Samba 且通过 ACL/anchor/E2E 的操作才算目录管理 |
| 外部账号能否自动创建业务用户 | 否 |
| 推荐免密码协议 | FIDO2/WebAuthn Passkey；邮件/SMS OTP 只作恢复或补充，不等价替代 |
| 候选是否支持 OIDC Provider | 六个候选均支持；OIDC 本身不是淘汰条件 |
| 当前是否已有 OIDC 双向登出 | 仅 Nextcloud 具备完整 Module 接口；LLNG/Authentik adapter 与 E2E 脚本已实现，仍需归档真实运行结果 |
| 新 Provider 的登出准入 | 必须同时支持 RP-Initiated Logout 与 back-channel logout，并通过 Provider × Nextcloud 真实 Cookie E2E |

## 11. 主要官方证据索引

- LLNG：[功能总览](https://lemonldap-ng.org/documentation/latest/)、[OIDC Provider 与三种登出机制](https://www.lemonldap-ng.org/documentation/latest/idpopenidconnect.html)、[Portal application list](https://lemonldap-ng.org/documentation/2.0/portalmenu.html)、[Passkeys](https://lemonldap-ng.org/documentation/latest/authwebauthn.html)、[Google OIDC](https://lemonldap-ng.org/documentation/latest/authopenidconnect_google.html)
- Authentik：[Sources](https://docs.goauthentik.io/users-sources/sources/)、[Google](https://docs.goauthentik.io/users-sources/sources/social-logins/google/cloud/)、[Apple](https://docs.goauthentik.io/users-sources/sources/social-logins/apple/)、[WeChat](https://docs.goauthentik.io/users-sources/sources/social-logins/wechat/)、[LDAP writeback](https://docs.goauthentik.io/users-sources/sources/protocols/ldap)、[WebAuthn Setup Stage](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/authenticator_webauthn/)、[Authenticator Validation Stage](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/authenticator_validate/)、[Identification/Passkey autofill](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/identification/)、[Application Dashboard](https://docs.goauthentik.io/users-sources/user/user-interface/)、[OIDC Front-/Back-Channel Logout](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/frontchannel_and_backchannel_logout/)、[RP-Initiated Full SLO](https://docs.goauthentik.io/add-secure-apps/providers/single-logout/)
- Keycloak：[Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/)，其中包含 Social Identity Providers、LDAP Edit Mode、Passwordless WebAuthn、Account Console Applications 与 Admin Console 用户管理；[实现规范与 OIDC logout conformance](https://www.keycloak.org/securing-apps/specifications)
- WSO2 IS：[Social Login](https://is.docs.wso2.com/en/latest/guides/authentication/social-login/)、[My Account 登记 Passkey](https://is.docs.wso2.com/en/latest/guides/user-self-service/register-passkey/)、[Passwordless/usernameless Passkey Flow](https://is.docs.wso2.com/en/next/guides/authentication/passwordless-login/add-passwordless-login-with-passkey/)、[read-write AD](https://is.docs.wso2.com/en/7.1.0/guides/users/user-stores/primary-user-store/configure-a-read-write-active-directory-user-store/)、[discoverable applications](https://is.docs.wso2.com/en/7.0.0/guides/applications/)、[OIDC Back-Channel Logout](https://is.docs.wso2.com/en/next/guides/authentication/oidc/add-back-channel-logout/)
- Casdoor：[OAuth Providers](https://casdoor.org/docs/category/oauth)、[LDAP model](https://casdoor.org/docs/ldap/overview/)、[AD Syncer](https://casdoor.org/uk/docs/syncer/ActiveDirectory/)、[LDAP SetPassword PR #3395](https://github.com/casdoor/casdoor/pull/3395)、[AD password writeback issue #5634](https://github.com/casdoor/casdoor/issues/5634)、[WebAuthn](https://casdoor.org/docs/how-to-connect/webauthn/)、[SAML IdP](https://casdoor.org/vi/docs/how-to-connect/saml/overview/)、[3.137.0 back-channel application field release](https://github.com/casdoor/casdoor/releases/tag/v3.137.0)、[当前 RP logout 与 back-channel 源码](https://github.com/casdoor/casdoor/blob/master/controllers/account.go)
- ZITADEL：[Identity brokering](https://zitadel.com/docs/concepts/features/identity-brokering)、[Passkey 能力](https://zitadel.com/docs/concepts/features/passkeys)、[Passkey registration API flow](https://zitadel.com/docs/guides/integrate/login-ui/passkey)、[Applications](https://zitadel.com/docs/guides/manage/console/applications-overview)、[RP-Initiated Logout](https://zitadel.com/docs/guides/integrate/login/oidc/logout)、[OIDC Back-Channel Logout](https://zitadel.com/docs/guides/integrate/back-channel-logout)、[Console boundary](https://zitadel.com/docs/guides/solution-scenarios/restrict-console)、[Licensing](https://github.com/zitadel/zitadel/blob/main/LICENSING.md)

本报告中的“未支持/未确认”均以 2026-08-21 可访问的当前官方文档、上游代码和 issue/PR 为界。上游新增 Provider、协议或社区扩展后可重评，但在 ANAS E2E 通过前不能把上游能力写成 Module 已支持。
