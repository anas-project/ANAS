---
doc_type: research
created: 2026-08-20
updated: 2026-08-21
evidence_as_of: 2026-08-21
---

# IAM 登出同步到应用的 OIDC / SAML 方案

## 实施状态（2026-08-21）

- 已扩展通用 client contract，并由 Runner 在 Provider `render_env` 前校验 endpoint、method、binding 和布尔字段的成对关系与枚举值。
- Nextcloud 已发布官方 `user_oidc` back-channel endpoint 和 `user_saml` Redirect SLS。
- Authentik blueprint 已登记 authorization/logout 两类 redirect URI、OIDC back-channel 和 SAML `frontchannel_native` + 签名 SLO。
- LLNG 已把通用 OIDC 声明翻译为 `LogoutUrl`、`LogoutType=back`、`LogoutSessionRequired=1`；原 SAML metadata + 签名路径保留。
- OIDC 两套真实登录 matrix 已增加保留 Nextcloud Cookie 的登出失效断言；Authentik 同时增加管理员删除 session 用例。Authentik SAML fallback E2E 已增加 Redirect SLO 断言。
- 静态/模块测试已覆盖契约缺项、非法枚举和 Provider 配置翻译。真实容器 matrix 仍需在对应隔离 Docker fixture 中执行，运行结果不能由静态测试代替。

## 结论

当前问题不是“退出按钮没有跳转”，而是系统只基本完成了 **RP/SP 发起登出**，没有完整配置 **IAM 发起登出通知应用**：

- `POST_LOGOUT_REDIRECT_URIS` 只规定 IAM 完成登出后允许跳回哪里，不能撤销应用自己的会话。
- OIDC 必须另外配置 front-channel 或 back-channel logout URI；优先 back-channel。
- SAML 必须让 IdP 保存 SP 的 Single Logout Service（SLS）地址、绑定方式和会话的 `SessionIndex/NameID`，然后由 IdP 发出 `LogoutRequest`。
- 本仓库中同时支持 OIDC 和 SAML 的应用是 Nextcloud，因此第一阶段应先把 Nextcloud 做成基准实现，再扩展到其他 OIDC 应用。

建议目标语义是：

1. 用户在 IAM 当前浏览器会话中登出后，对应的 Nextcloud 会话立即失效。
2. 管理员撤销 IAM 会话或禁用用户时，OIDC/Nextcloud 也立即失效。
3. SAML 先保证浏览器参与的 IdP-initiated SLO；管理员无浏览器地强制撤销属于另一档能力，不能假装已支持。

## 当前实现检查

### 公共问题

Nextcloud 目前只发布 OIDC 登录回调和 `POST_LOGOUT_REDIRECT_URIS`，没有发布它已经支持的 back-channel logout endpoint。见 `modules/nextcloud/hook/iam.go`。

Nextcloud 的 `user_oidc` 已设置 `--postlogouturi` 和 `--send-id-token-hint=1`，所以“从 Nextcloud 点击退出并通知 IAM”已有基础；这仍然不能覆盖“从 IAM 点击退出并反向通知 Nextcloud”。见 `modules/nextcloud/nextcloud/root/usr/local/bin/task.sh`。

Nextcloud 当前固定安装 `user_oidc 8.10.1`。上游明确提供 back-channel endpoint：

```text
https://<nextcloud>/index.php/apps/user_oidc/backchannel-logout/<provider-id>
```

当前 provider id 是 `anas`，因此应登记：

```text
https://<nextcloud>/index.php/apps/user_oidc/backchannel-logout/anas
```

上游说明见 [Nextcloud user_oidc：Backchannel logout](https://github.com/nextcloud/user_oidc#backchannel-logout)。

### OIDC / Authentik

仓库使用 Authentik `2026.5.6`，该版本支持 OIDC front-channel 和 back-channel logout。

当前 blueprint 只写入授权回调 `redirect_uris`，没有：

- 把 `POST_LOGOUT_REDIRECT_URIS` 写成 `redirect_uri_type: logout`；
- 写入 RP 的 `logout_uri`；
- 选择 `logout_method: backchannel`。

因此 Authentik 结束自身会话时没有 Nextcloud 的通知地址。现有 `default-invalidation-logout` stage 解决的是 RP-initiated logout 时是否同时结束 Authentik 中央会话，并不等同于向所有 RP 传播登出。见 `modules/authentik/hook/iam.go`。

Authentik 官方说明：back-channel 使用带签名的 `logout_token` 直接 POST 给 RP，而且能覆盖管理员删会话和用户停用；front-channel 依赖浏览器，不能覆盖这些场景。见 [Authentik OIDC front-channel/back-channel logout](https://docs.goauthentik.io/add-secure-apps/providers/oauth2/frontchannel_and_backchannel_logout/)。

### OIDC / LemonLDAP::NG

仓库使用 LLNG `2.23.2`。当前已经登记登录回调、登出后跳转地址和 RP-initiated logout 免确认，但没有登记：

- `oidcRPMetaDataOptionsLogoutUrl`；
- `oidcRPMetaDataOptionsLogoutType=back`；
- `oidcRPMetaDataOptionsLogoutSessionRequired=1`。

所以 LLNG 同样不知道应把 logout token 发给 Nextcloud。见 `modules/llng/llng/root/root/llng-config.sh`。LLNG 官方支持 OIDC back-channel logout，并明确使用 `oidcRPMetaDataOptionsLogoutType = back`，见 [LLNG OpenID Connect Provider](https://www.lemonldap-ng.org/documentation/latest/idpopenidconnect.html) 和 [LLNG Global logout](https://lemonldap-ng.org/documentation/latest/globallogout.html)。

### SAML / Nextcloud

Nextcloud 端已经配置 IdP SLO URL、SLO response URL、SP 密钥，并要求登出请求和响应签名。`user_saml` 的 SP metadata 会发布 HTTP-Redirect SLS，典型地址是：

```text
https://<nextcloud>/index.php/apps/user_saml/saml/sls
```

Nextcloud `user_saml` 已支持 IdP-initiated logout，见 [user_saml changelog](https://github.com/nextcloud/user_saml/blob/master/CHANGELOG.md)；其 SLS 实现及元数据生成位于 [SAMLController.php](https://github.com/nextcloud/user_saml/blob/master/lib/Controller/SAMLController.php)。

### SAML / Authentik

当前 Authentik blueprint 只登记 `acs_url`、`audience`、签名证书和登录相关选项，没有登记：

- `sls_url`；
- `sls_binding`；
- `logout_method`；
- `sign_logout_request/sign_logout_response`。

因此当前 Authentik 无法在自身登出时向 Nextcloud 发出可验证的 SAML `LogoutRequest`。Authentik `2026.5.6` 已具备所需字段及 SAML session tracking；官方方案要求 SLS URL 和绑定，见 [Authentik SAML Single Logout](https://docs.goauthentik.io/add-secure-apps/providers/saml/saml_single_logout/)。

### SAML / LemonLDAP::NG

LLNG 会等待并读取 Nextcloud SP metadata，其中包含 SLS URL，并且当前已设置 `samlSPMetaDataOptionsSignSLOMessage=1`。从静态配置看链路基本齐全，但仓库没有覆盖“IAM 发起登出后 Nextcloud 会话失效”的 E2E，因此只能判定为“待运行时验证”，不能判定为已完成。

## 建议设计

### 1. 扩展 Provider-neutral IAM client contract

不要复用 `POST_LOGOUT_REDIRECT_URIS` 表达通知端点。建议新增：

```text
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_URI
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_SESSION_REQUIRED=true

ANAS_IAM_CLIENT__<APP>__SAML_SLS_URL
ANAS_IAM_CLIENT__<APP>__SAML_SLS_BINDINGS=redirect
```

语义是“应用声明它支持什么”，由 IAM provider 从交集中选择最强能力；应用不应直接发布 Authentik 或 LLNG 私有字段。

Nextcloud 应发布：

```text
OIDC_LOGOUT_URI=https://<nextcloud>/index.php/apps/user_oidc/backchannel-logout/anas
OIDC_LOGOUT_METHODS=backchannel
OIDC_LOGOUT_SESSION_REQUIRED=true

SAML_SLS_URL=https://<nextcloud>/index.php/apps/user_saml/saml/sls
SAML_SLS_BINDINGS=redirect
```

同时扩展 runner 的 env scope/contract 校验和静态耦合测试，确保新增字段只流向绑定的 provider，并在声明支持 logout 后拒绝空 endpoint。

### 2. OIDC 落地

#### Authentik

在 OAuth2 provider blueprint 中：

1. 授权回调继续写 `redirect_uris`，明确 `redirect_uri_type: authorization`。
2. 将每个 `POST_LOGOUT_REDIRECT_URIS` 追加为 `redirect_uri_type: logout`。
3. 写入 `logout_uri: <OIDC_LOGOUT_URI>`。
4. 写入 `logout_method: backchannel`。

Back-channel logout 是首选，因为它不受 iframe、第三方 Cookie 和浏览器是否在线影响，并覆盖管理员撤销会话。

#### LemonLDAP::NG

在每个 OIDC RP 下新增：

```text
oidcRPMetaDataOptionsLogoutUrl=<OIDC_LOGOUT_URI>
oidcRPMetaDataOptionsLogoutType=back
oidcRPMetaDataOptionsLogoutSessionRequired=1
```

保留现有 `PostLogoutRedirectUris` 和 `LogoutBypassConfirm`；它们继续服务 RP-initiated logout，不替代 back-channel。

#### Nextcloud

无需自建登出服务。继续使用官方 `user_oidc` endpoint，并保留 `--send-id-token-hint=1`。上线前确认 discovery 中包含 `backchannel_logout_supported`/`backchannel_logout_session_supported`，以及 IAM 发出的 token 带正确 `iss`、`aud`、`events` 和可用的 `sid`。

### 3. SAML 落地

#### Authentik

Nextcloud 的 metadata 当前发布 Redirect SLS，因此建议：

```yaml
sls_url: https://<nextcloud>/index.php/apps/user_saml/saml/sls
sls_binding: redirect
logout_method: frontchannel_native
sign_logout_request: true
sign_logout_response: true
```

选择 `frontchannel_native` 而不是 iframe，减少 CSP、SameSite 和第三方 Cookie 导致 SLS 页面没有真正收到会话 Cookie 的风险。不要配置 `backchannel + redirect`：Authentik 的 SAML back-channel 要求 POST SLS binding，而 Nextcloud metadata 默认只声明 Redirect。

#### LemonLDAP::NG

保留从 SP metadata 导入配置和 `SignSLOMessage=1`。重点补 E2E，验证：

- LLNG 的登录会话保存了 Nextcloud 对应的 SAML session；
- IAM 登出时向 metadata 中的 SLS 发出签名 `LogoutRequest`；
- Nextcloud 返回 `LogoutResponse` 后本地会话失效；
- RelayState 最终落到安全的登录页，不形成循环。

SAML 的边界要明确：浏览器发起 IAM 登出时可以通过 Redirect SLO 同步；管理员在后台删除会话时没有浏览器，Redirect SLO 无法可靠执行。若未来必须实现 SAML 的后台强制撤销，需要确认/扩展 Nextcloud 对 POST SLS 的正式支持，再在 Authentik 选择 `backchannel`。在此之前，管理员强制撤销应以 OIDC back-channel 为推荐路径。

## 验收测试

至少增加四组真实会话 E2E，不能只检查配置字符串：

| IAM | 协议 | 操作 | 通过条件 |
| --- | --- | --- | --- |
| Authentik | OIDC | 登录 Nextcloud 后从 Authentik 登出 | 保留原 Nextcloud Cookie 再访问 OCS/session，返回未认证；日志出现成功的 back-channel POST |
| LLNG | OIDC | 登录 Nextcloud 后从 LLNG 登出 | 同上；重复投递 logout token 不恢复会话、不产生 5xx |
| Authentik | SAML | 登录 Nextcloud 后从 Authentik 登出 | 浏览器完成 `LogoutRequest → SLS → LogoutResponse`，原 Nextcloud 会话失效 |
| LLNG | SAML | 登录 Nextcloud 后从 LLNG 登出 | 同上，且请求/响应签名校验通过 |

OIDC 再增加管理员撤销用例：不使用用户浏览器删除 IAM session，轮询 Nextcloud 原会话，限定时间内必须失效。SAML Redirect 不设置这一通过条件，而应把限制写入测试名称和文档。

安全检查包括：

- back-channel endpoint 仅接受 HTTPS POST，验证 JWT 签名、`iss`、`aud`、`events`、时间和重放；
- SAML SLO 验证签名、Destination、Issuer、NameID、SessionIndex；
- 不把普通应用首页或 `/logout` 当作 back-channel endpoint；
- 不以缩短 Cookie TTL 冒充同步登出；TTL 只能作为通知失败时的风险上限。

## 实施顺序

1. **P0：Nextcloud OIDC**——扩展通用 contract，接入 Authentik 与 LLNG back-channel，并补两套 OIDC E2E。这能同时满足用户主动 IAM 登出和管理员撤销。
2. **P1：Nextcloud SAML**——为 Authentik 补 SLS/native front-channel；对 LLNG 现有 metadata 路径补 E2E，根据真实报文修正签名或 RelayState。
3. **P2：其他 OIDC 应用**——逐个盘点是否有标准 front/back-channel endpoint。无标准支持的应用标为“不支持即时 IAM-initiated logout”，不得仅登记一个普通 `/logout` URL 后宣称完成。
4. 同步更新 IAM capability 设计、provider requirements、Nextcloud/Authentik/LLNG 技术文档和 IAM 支持矩阵。

## 最终建议

默认协议继续选 OIDC，并以 back-channel logout 作为“完整登出”的准入条件。SAML 保留为 Nextcloud fallback，支持浏览器参与的 SLO，但在未具备 POST SLS/back-channel 前，不承诺后台撤销能立即清除 Nextcloud 会话。
