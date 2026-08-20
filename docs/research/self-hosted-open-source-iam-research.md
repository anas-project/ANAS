---
doc_type: research
created: 2026-08-20
updated: 2026-08-20
evidence_as_of: 2026-08-20
---

# 开源自部署 IAM 与 ANAS 适配调研

## 1. 结论摘要

本轮围绕当前 ANAS 的 LemonLDAP::NG（下文简称 LLNG）与 Authentik，重点核验以下五项能力：

1. 绑定 Google、Apple ID、微信等第三方身份；
2. 免密码登录协议与实现；
3. 将用户改密写回 Samba AD；
4. 面向终端用户的应用列表/应用启动器；
5. 用户管理界面，特别是能否管理作为事实来源的 Samba AD 用户，而不是只管理 IAM 本地副本。

结论如下：

- **短期不建议替换 LLNG。** 仓库内 LLNG 已是 `release`，已经实现 OIDC、SAML、按组过滤的应用门户和 Samba 密码写回；缺口主要是上游 Passkey 能力尚未在 ANAS 中启用，以及 LLNG 没有完整的目录用户管理界面，需要继续配合 LAM。
- **优先把 Authentik 从 `developing` 做到可发布。** 在本轮五项能力中，Authentik 上游覆盖最完整：Google、Apple、微信均有官方 Source 文档，支持 WebAuthn 免密码 Flow、LDAP 密码写回、应用 Dashboard 和用户/组管理界面。ANAS 已实现 Samba Source、密码写回和应用注册，但仍需真实部署验证社交账号安全绑定、Passkey、目录撤权和恢复流程。
- **若要引入第三种 IAM，优先 PoC Keycloak。** Keycloak 同时提供 OIDC/SAML、WebAuthn passwordless、LDAP/AD `WRITABLE` 密码更新、Account Console 应用列表和成熟的用户管理控制台。主要缺口是 Apple/微信不是当前官方内置社交 Provider，需要扩展或协议适配；同时必须证明 `anasIdentityAnchor`、嵌套组、禁用账号和 Samba 密码策略语义。
- **WSO2 Identity Server 是第二候选，不是默认选择。** 它的 read-write AD user store、FIDO2、My Account 应用发现和目录管理能力很强，但部署、配置和升级面显著大于现有方案；微信仍需自定义 Connector。
- **Casdoor 不进入 Samba 主身份源路线。** 它对 Google、Apple、微信和中国常用 OAuth Provider 的覆盖最好，也支持 WebAuthn；但官方 LDAP 模型是导入用户并远程校验密码，导入后的用户记录独立，未提供改密写回 LDAP/AD 的明确契约。这与 ANAS 的 Samba 唯一事实来源、永久锚点和密码回写要求冲突。
- **ZITADEL 更适合应用原生 CIAM，不适合当前目录中心架构。** 它支持 Google、Apple、Passkey、OIDC/SAML 和强管理 API，但没有可核验的 Samba 密码写回或终端用户应用启动器，LDAP 是外部 IdP，而不是 ANAS 需要的可写目录源。

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
snapshot_date: 2026-08-20
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

### 4.1 五项需求

| IAM | Google / Apple / 微信 | 真正免密码 | Samba 密码写回 | 终端用户应用列表 | 用户管理界面 | ANAS 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| **LLNG 2.23.2-r11（现有）** | Google ✅；Apple ◐；微信未确认 | Passkey/WebAuthn ✅，上游标 Beta；ANAS 未启用 | ✅ ANAS 已实现，E2E 脚本覆盖 | ✅ Portal 按规则显示应用 | ❌ 无完整目录管理；配合 LAM | 保持生产基线，补 Passkey PoC |
| **Authentik 2026.5.6-r10（现有）** | Google ✅；Apple ✅；微信 ✅ | WebAuthn passwordless / Passkey autofill ✅；ANAS 未验收 | ✅ 上游支持；ANAS 已实现且有 E2E 脚本，Module 仍为 `developing` | ✅ Application Dashboard | ✅ 本地/同步对象 UI；◐ 作为 Samba 管理面 | 五项覆盖最佳，优先完成生产验收 |
| **Keycloak** | Google ✅；Apple ◐；微信 ◐ | WebAuthn passwordless ✅ | ✅ LDAP `WRITABLE` 上游支持；◐ Samba 待验 | ✅ Account Console Applications | ✅ Admin Console；◐ LDAP 可写字段需收口 | 最优第三 Provider PoC |
| **WSO2 Identity Server** | Google ✅；Apple ✅；微信 ◐ | FIDO2/Passkey ✅ | ✅ read-write AD user store；◐ Samba 待验 | ✅ My Account discoverable apps | ✅ Console + read-write AD | 能力强但运维复杂，第二候选 |
| **Casdoor** | Google ✅；Apple ✅；微信 ✅ | WebAuthn ✅ | ❌ 官方 LDAP 模型未提供写回契约 | ◐ 有应用管理，未核实合格的授权启动器 | ✅ 本地用户；❌ Samba 权威管理 | 中国社交登录强，但不适合当前身份架构 |
| **ZITADEL** | Google ✅；Apple ✅；微信 ◐ generic OAuth | Passkey passwordless ✅ | ❌ 未找到 LDAP/AD 写回能力 | ❌ 无一站式终端用户启动器 | ✅ 本地/组织用户；❌ Samba 权威管理 | 适合 CIAM，不进入当前 Provider 路线 |

### 4.2 双协议、许可与集成风险

| IAM | OIDC Provider | SAML IdP | 主要许可证 | 目录模型 | 主要集成风险 |
| --- | --- | --- | --- | --- | --- |
| LLNG | ✅ | ✅ | GPL-2.0+ | 直接认证/检索 Samba | 无完整用户管理；Passkey 尚未在 Module 启用 |
| Authentik | ✅ | ✅ | 核心 MIT，Enterprise 目录另行许可 | LDAP Source 同步 + 认证/回写 | 本地影子对象与 Samba 权威边界要持续验证 |
| Keycloak | ✅ | ✅ | Apache-2.0 | LDAP/AD Federation，可 `WRITABLE` | 社交 Provider 扩展、嵌套组/锚点/撤权需专项适配 |
| WSO2 IS | ✅ | ✅ | Apache-2.0 | read-write AD/LDAP user store | 运行栈重、配置面大、Module 生命周期成本高 |
| Casdoor | ✅ | ✅ | Apache-2.0 | LDAP/AD 导入后形成独立记录 | 无可靠写回；默认对象模型偏本地 IAM |
| ZITADEL | ✅ | ✅ | AGPL-3.0-only（部分目录例外） | 外部 LDAP IdP + ZITADEL 用户 | 无 Samba 写回/应用启动器；需自建同步和 UI |

许可证证据：[Authentik LICENSE](https://github.com/goauthentik/authentik/blob/main/LICENSE)、[Keycloak LICENSE](https://github.com/keycloak/keycloak/blob/main/LICENSE.txt)、[Casdoor LICENSE](https://github.com/casdoor/casdoor/blob/master/LICENSE)、[WSO2 IS LICENSE](https://github.com/wso2/product-is/blob/master/LICENSE)、[ZITADEL licensing policy](https://github.com/zitadel/zitadel/blob/main/LICENSING.md)。

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

#### 口径

本报告优先认可 FIDO2/WebAuthn Passkey：私钥留在用户设备或安全密钥中，服务端保存公钥。OIDC、SAML、OAuth 只是应用与 IAM/上游 IdP 的联邦协议，本身不是免密码协议；PKCE 也不是用户认证器。邮件 Magic Link、Email/SMS OTP 可减少密码输入，但依赖邮箱/短信通道，不与抗钓鱼 Passkey 等价。

#### 产品差异

- **LLNG**：自 2.20 起提供 [Passkeys / WebAuthn 第一因素](https://lemonldap-ng.org/documentation/latest/authwebauthn.html)，可只用 FIDO2 credential 登录；当前上游页面仍标 Beta，且要求同时启用 WebAuthn second factor、使用 discoverable credential，并先通过既有认证注册凭据。ANAS 当前 2.23.2 版本满足版本门槛，但 Module 尚未配置和测试。
- **Authentik**：官方 Password Stage 文档明确支持独立 WebAuthn passwordless Flow，并支持 Passkey autofill/conditional UI。[Passwordless patterns](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/password/)
- **Keycloak**：官方文档将 WebAuthn 同时作为二次认证与 passwordless 第一因素，Account Console 可登记 Passwordless Passkey，配置正确后登录不再输入密码。[Passwordless WebAuthn](https://www.keycloak.org/docs/latest/server_admin/#_webauthn_passwordless)
- **WSO2 IS**：官方 [FIDO2 passwordless](https://is.docs.wso2.com/en/6.0.0/guides/passwordless/fido/) 支持安全密钥和平台生物识别；新版本 Login Flow 也将 Passkey 作为独立 authenticator。
- **Casdoor**：官方 [WebAuthn](https://casdoor.org/zh/docs/how-to-connect/webauthn/) 支持指纹、Face/Windows Hello 和安全密钥替代密码，但文档流程仍先输入用户名，属于免密码而非完全 usernameless。
- **ZITADEL**：Login V2 与 Self Service 明确支持 Passkey passwordless、登记和管理。[Login App](https://zitadel.com/docs/guides/integrate/login-ui/login-app)、[Self Service](https://zitadel.com/docs/concepts/features/selfservice)

对 ANAS 而言，Passkey 不写入 Samba 密码字段是正常行为，但每次认证仍必须校验 Samba 用户未停用、永久锚点存在、授权组仍满足条件。移除或停用 Samba 用户后，已有 Passkey 不能继续获得新 token。

### 5.3 Samba 密码回写

#### 当前已实现状态

- **LLNG**：ANAS 的改密表单通过受限 LDAPS 服务账号写回 Samba；E2E 验证 `pwdLastSet` 变化和新密码可认证。委派 reset 语义不能可靠强制 Samba history/minimum age，因此这两项仅显示提示，不虚报强制能力。
- **Authentik**：上游 LDAP Source 有 `User password writeback` 开关；ANAS 已实现受限服务账号、最小长度/复杂度预检查、LDAP 错误安全映射，并在成功后把同步用户的 Authentik 本地密码恢复为 unusable，避免第二份业务密码。[Authentik LDAP Source](https://docs.goauthentik.io/users-sources/sources/protocols/ldap)

共同契约和当前差异见 [Module IAM / OIDC 支持清单的 Samba 目录密码接入规范](../reference/module-iam-support.md#samba-目录密码接入规范)；各 Provider 的实现说明位于仓库 `modules/authentik/README.md` 与 `modules/llng/README.md`。

#### 新候选

- **Keycloak**：LDAP Federation 的 `Edit Mode=WRITABLE` 明确允许用户和管理员修改密码并自动同步到 LDAP；`READ_ONLY` 不允许密码更新，`UNSYNCED` 会把密码存入 Keycloak 本地库，后者不符合 ANAS。[Keycloak LDAP Edit Mode](https://www.keycloak.org/docs/latest/server_admin/#_ldap)
- **WSO2 IS**：官方提供 read-write Active Directory user store，`UniqueIDActiveDirectoryUserStoreManager` 只用于读写操作；通过 My Account/Console/SCIM 的密码更新应落到所选 user store。[Primary user store types](https://is.docs.wso2.com/en/latest/guides/users/user-stores/primary-user-store/)、[Read-write AD configuration](https://is.docs.wso2.com/en/7.1.0/guides/users/user-stores/primary-user-store/configure-a-read-write-active-directory-user-store/)
- **Casdoor**：官方 LDAP 说明明确写出同步用户会形成独立 Casdoor 记录，资料变更不会回到 LDAP，密码不被同步而是在登录时交给 LDAP 校验；未提供用户在 Casdoor 改密后写回 LDAP/AD 的契约。[LDAP overview](https://casdoor.org/docs/ldap/overview/)、[AD Syncer](https://casdoor.org/uk/docs/syncer/ActiveDirectory/)
- **ZITADEL**：官方把 LDAP 作为外部身份 Provider，未找到 LDAP/AD password modify 或 writeback 能力；因此不满足本项。

Keycloak 与 WSO2 的“上游支持读写 AD”仍不能直接等同于“通过 ANAS Samba 验收”。PoC 必须验证 LDAPS、最小权限 ACL、Samba 返回码、密码策略、旧密码宽限、`pwdLastSet=0`、本地哈希残留和故障恢复。

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
- **Casdoor** 和 **ZITADEL** 都有成熟本地用户/组织管理 UI，但不能证明这些操作以 Samba 为权威落点，故不能替代 LAM。

## 6. 候选逐项结论

### 6.1 LLNG：继续作为生产基线

优势：

- ANAS 当前唯一 `release` IAM；
- 直接使用 Samba 认证/检索，身份权威边界简单；
- OIDC、SAML、应用 Portal 和组规则已进入统一 IAM Contract；
- 密码写回及错误提示已有服务器 E2E；
- 上游已有 Passkey first-factor，可在不更换 Provider 的情况下补免密码能力。

不足：

- Apple/微信没有开箱即用官方集成；
- 无完整用户管理 UI；
- Passkey 页面仍标 Beta，ANAS 当前镜像也未安装、启用并验证所需组件；
- 没有独立本地 break-glass，目录或认证链故障时只能从主机侧恢复。

建议：维持生产默认，不因新功能调研立即迁移；建立 Passkey 独立 PoC，并保留 LAM 作为目录管理面。

### 6.2 Authentik：最符合五项功能目标

优势：

- Google、Apple、微信均有官方社交 Source；
- 核心 Source 和 policy 提供安全绑定所需基础，但“先目录认证，再绑定外部身份”的社区版完整流程仍需 PoC，不能依赖 Enterprise-only Source Stage；
- 原生 WebAuthn passwordless 与 conditional UI；
- Application Dashboard 和 Admin Interface 完整；
- ANAS 已实现 Samba LDAPS Source、`anasIdentityAnchor` 唯一性、OIDC/SAML per-app endpoint、组策略和密码写回。

不足：

- 当前 Module 仍是 `developing`；
- 同步用户在 Authentik 仍有影子对象，必须持续验证不会保留可登录本地密码；
- 社交登录与 Passkey 尚未进入 ANAS 配置契约和 E2E；
- Authentik UI 不是完整 AD 管理器，Samba 用户生命周期仍应在 LAM/目录层管理。

建议：优先完善现有 Module，而不是先增加第三 Provider。

### 6.3 Keycloak：最值得新增的第三 Provider

优势：

- 双协议、身份代理、LDAP/AD Federation、Passwordless WebAuthn、Account Console 和 Admin Console 都是成熟上游能力；
- `WRITABLE` 与 `UNSYNCED` 的语义区分清晰，可以明确禁止本地密码副本路线；
- Admin API/CLI 适合实现 ANAS 幂等 adapter；
- Apache-2.0，社区规模与生态成熟。

不足：

- Apple/微信需要 SPI、第三方扩展或桥接，增加供应链和升级风险；
- 上游 LDAP 模型默认会导入/缓存用户对象，需要证明账号改名、删除、禁用和组撤权能正确收敛；
- `anasIdentityAnchor`、固定 claim 类型、递归 `memberOf` 和应用组规则都需要自建 mapper/adapter；
- 必须确保本地自注册、JIT 社交用户和 `UNSYNCED` 密码全部关闭。

建议：只有在 Authentik 生产验收后仍有明确缺口时再实施，PoC 不应直接进入 `release`。

### 6.4 WSO2 Identity Server：能力全面但成本最高

优势：

- read-write AD 是正式 user store 类型，而非附加同步器；
- Google、Apple、FIDO2、可发现应用和完整用户管理 UI 均有官方文档；
- 丰富的 Console、My Account、SCIM/API 和委派管理能力。

不足：

- 微信需要自定义 Connector；
- Java/Carbon 系运行、配置、数据库、升级和故障排查面明显更重；
- 广泛可写 AD 能力与 ANAS 最小权限原则存在张力，必须进行字段/ACL 收口；
- 对家庭/小团队自部署场景可能过度设计。

建议：作为“需要统一 AD 管理面和复杂企业工作流”时的备选，不作为默认第三 Provider。

### 6.5 Casdoor：社交生态强，但目录模型不合格

优势：

- Google、Apple、微信、企业微信、QQ、钉钉、飞书等内置 Provider 丰富；
- OIDC、SAML IdP 和 WebAuthn 齐全；
- UI-first，用户、组织、角色和应用管理直观；
- Apache-2.0，Go 服务便于容器化。

阻断问题：

- LDAP/AD 是同步/登录校验来源，用户记录导入后独立；
- AD Syncer 当前将 `objectGUID` 映射为 Casdoor ID，与 ANAS 指定 `anasIdentityAnchor` 的永久锚点契约冲突；
- 未找到密码 writeback；
- 本地 Password 字段和丰富的本地注册模型容易形成第二身份源；
- 未确认终端用户授权应用启动器。

建议：不进入当前 Provider 路线。若未来目标改为 CIAM 或中国社交账号优先、Samba 不再是唯一身份源，可重新评估。

### 6.6 ZITADEL：现代 CIAM，但与 Samba 中心模型错位

优势：

- OIDC、SAML、Passkey、组织/项目/角色、审计和管理 API 完整；
- Google、Apple 与 generic OAuth/OIDC 身份代理；
- 自部署活跃，Login V2 适合应用原生体验。

阻断问题：

- LDAP 是外部 IdP，不是可写的权威 user store；
- 没有 Samba 密码回写证据；
- 没有终端用户应用启动器，官方甚至提供阻止普通用户访问 Management Console 的指南；
- 需要本地 ZITADEL 用户/组织对象和自建同步逻辑；
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
   - 先用 Samba 登录登记 Passkey；
   - 之后可用 Passkey 登录；
   - 每次登录仍实时校验 Samba enabled/group；
   - 删除 Samba 用户、移组或停用后 Passkey 不得继续授权。
3. 为 LLNG 建立同样的 Passkey PoC，记录上游 Beta 风险和所需 Perl 包/持久化数据。
4. 保持 LAM 为 Samba 用户/组管理入口；Authentik Users 页面不宣传为 AD 全功能管理器。

### 阶段 2：把 Authentik 从 `developing` 提升到候选发布质量

完成现有 Provider 强制 E2E：

- 直接组、`APP_all`、`Admins`、嵌套组与循环组；
- 禁用账号；
- 改名但 anchor 不变；
- `Admins` 移除后的 superuser 撤销；
- OIDC/SAML claim、数组类型和 per-app endpoint；
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
8. 运行与 LLNG/Authentik 完全相同的 E2E 矩阵。

在这些步骤完成前，生命周期只能是 `developing`。

## 9. 新增验收矩阵

除既有 IAM Provider E2E 外，本轮五项能力至少增加以下测试：

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
| 删除/撤销 Passkey | 该 credential 立即不可使用，不影响 Samba 密码 |
| IAM 改密成功 | Samba `pwdLastSet` 改变，新密码可 LDAP 认证，IAM 无第二本地业务密码 |
| IAM 改密被 Samba 策略拒绝 | 用户得到安全且可操作的提示，原始 LDAP 诊断只进审计日志 |
| 应用目录新增/删除/改组 | Portal/Dashboard 幂等收敛，可见性与实际授权一致 |
| IAM 用户管理 UI 修改受管字段 | 只允许契约内字段，写入 Samba；不可写字段被拒绝而非写入本地副本 |

## 10. 最终决策表

| 决策 | 结果 |
| --- | --- |
| 当前生产 IAM | 继续 LLNG |
| 当前重点投入 | 完成 Authentik 生产验收，随后增加安全的社交绑定与 Passkey |
| 是否立即引入第三 IAM | 否 |
| 若必须引入第三 IAM | Keycloak 第一，WSO2 IS 第二 |
| 是否因微信支持选择 Casdoor | 否；除非放弃 Samba 唯一事实来源与密码回写硬要求 |
| 是否把 IAM Users 页面当 Samba 管理面 | 否；只有写入 Samba 且通过 ACL/anchor/E2E 的操作才算目录管理 |
| 外部账号能否自动创建业务用户 | 否 |
| 推荐免密码协议 | FIDO2/WebAuthn Passkey；邮件/SMS OTP 只作恢复或补充，不等价替代 |

## 11. 主要官方证据索引

- LLNG：[功能总览](https://lemonldap-ng.org/documentation/latest/)、[Portal application list](https://lemonldap-ng.org/documentation/2.0/portalmenu.html)、[Passkeys](https://lemonldap-ng.org/documentation/latest/authwebauthn.html)、[Google OIDC](https://lemonldap-ng.org/documentation/latest/authopenidconnect_google.html)
- Authentik：[Sources](https://docs.goauthentik.io/users-sources/sources/)、[Google](https://docs.goauthentik.io/users-sources/sources/social-logins/google/cloud/)、[Apple](https://docs.goauthentik.io/users-sources/sources/social-logins/apple/)、[WeChat](https://docs.goauthentik.io/users-sources/sources/social-logins/wechat/)、[LDAP writeback](https://docs.goauthentik.io/users-sources/sources/protocols/ldap)、[Passwordless Flow](https://docs.goauthentik.io/add-secure-apps/flows-stages/stages/password/)、[Application Dashboard](https://docs.goauthentik.io/users-sources/user/user-interface/)
- Keycloak：[Server Administration Guide](https://www.keycloak.org/docs/latest/server_admin/)，其中包含 Social Identity Providers、LDAP Edit Mode、Passwordless WebAuthn、Account Console Applications 与 Admin Console 用户管理
- WSO2 IS：[Social Login](https://is.docs.wso2.com/en/latest/guides/authentication/social-login/)、[FIDO2](https://is.docs.wso2.com/en/6.0.0/guides/passwordless/fido/)、[read-write AD](https://is.docs.wso2.com/en/7.1.0/guides/users/user-stores/primary-user-store/configure-a-read-write-active-directory-user-store/)、[discoverable applications](https://is.docs.wso2.com/en/7.0.0/guides/applications/)
- Casdoor：[OAuth Providers](https://casdoor.org/docs/category/oauth)、[LDAP model](https://casdoor.org/docs/ldap/overview/)、[AD Syncer](https://casdoor.org/uk/docs/syncer/ActiveDirectory/)、[WebAuthn](https://casdoor.org/zh/docs/how-to-connect/webauthn/)、[SAML IdP](https://casdoor.org/vi/docs/how-to-connect/saml/overview/)
- ZITADEL：[Identity brokering](https://zitadel.com/docs/concepts/features/identity-brokering)、[Passkeys](https://zitadel.com/docs/concepts/features/selfservice)、[Applications](https://zitadel.com/docs/guides/manage/console/applications-overview)、[Console boundary](https://zitadel.com/docs/guides/solution-scenarios/restrict-console)、[Licensing](https://github.com/zitadel/zitadel/blob/main/LICENSING.md)

本报告中的“未支持/未确认”均以 2026-08-20 可访问的当前官方文档为界。上游新增 Provider、协议或社区扩展后可重评，但在 ANAS E2E 通过前不能把上游能力写成 Module 已支持。
