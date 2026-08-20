# Module IAM / OIDC 支持清单

OIDC 是 ANAS 当前默认 IAM 接入协议，但只对声明消费 `iam` capability 且支持 OIDC 的 Module 生效。不支持 OIDC 的 Module 不会被强制改用 OIDC；它按 Manifest 支持范围回退或继续使用自己的认证方式。

| Module | 是否可用 OIDC 登录 | 当前认证路径 | 结论 |
| --- | --- | --- | --- |
| `netbird` | 是 | 直接消费 IAM/OIDC | 已实现 |
| `oauth2_proxy` | 是 | 直接消费 IAM/OIDC，并为 ForwardAuth consumer 提供门禁 | 已实现 |
| `ddns_updater` | 间接 | 经 `oauth2_proxy` + Traefik ForwardAuth | 已实现 |
| `nextcloud` | 是 | 默认使用官方 `user_oidc`；LDAPS provision 用户/组；`user_saml` 保留为显式 fallback | 已实现；OIDC back-channel 同步 IAM 登出和后台撤销，SAML Redirect SLO 仅覆盖浏览器参与登出 |
| `meshcentral` | 是 | IAM/OIDC 认证；LDAPS 同步用户/组；OIDC group 映射应用访问和 site-admin | 已实现 |
| `lam` | 否 | LDAPS 目录管理登录 | 不属于当前 IAM consumer |
| `authentik` | 不适用 | IAM provider；另有固定 `akadmin` break-glass | 提供 OIDC/SAML，不把自身当普通 consumer |
| `casdoor` | 不适用 | developing IAM provider；使用默认模板 `admin_casdoor` break-glass | 已接入 OIDC/SAML 注册与 Samba 目录事件触发同步；SAML SLO、永久 anchor 与 `ALLOW_GROUPS` 门禁尚未通过生产验收 |
| `llng` | 不适用 | IAM provider | 提供 OIDC/SAML，不把自身当普通 consumer |
| `ddns_go` | 否 | ANAS 托管 local emergency account | 不支持 OIDC |
| `traefik` | 否 | ANAS 托管本地 BasicAuth emergency account | 不支持 OIDC |
| `collabora` | 间接 | 由 Nextcloud/WOPI 集成，不提供独立用户登录 | 跟随 Nextcloud 会话 |
| `postgres`, `mariadb` | 否 | 数据库凭据/Adminer | 不是 IAM 登录 |
| `samba_dc`, `samba_fs` | 否 | AD/LDAP/Kerberos/SMB | 不是 OIDC Web consumer |
| `eturnal`, `freeradius`, `lego` | 否 | TURN/RADIUS/无交互 UI | 不适用 |

## 默认解析规则

协议优先级为：Module 显式 `iam_protocol` > 部署 `identity.iam.default_protocol`（仅当 Module 支持）> Module Manifest preference。Nextcloud、MeshCentral、NetBird 和 OAuth2 Proxy 当前都默认选择 OIDC；Nextcloud 可显式切换回 SAML，MeshCentral 只声明 OIDC。

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

把一个 Module 标为“OIDC 已实现”至少需要：Manifest OIDC interface、provider client registration、redirect URI/scope/claim/group 映射、Secret 传递、应用内验证和真实浏览器/HTTP 登录 E2E。Nextcloud 与 MeshCentral 由 `server-authentik-oidc-login-e2e.sh` 覆盖完整授权码登录、应用 session、目录身份和管理员组映射。Samba 密码接入分别由 `server-authentik-password-policy-e2e.sh` 和 `server-llng-password-policy-e2e.sh` 覆盖 Provider 页面预检、目录最终裁决、写回、错误映射和凭据切换。仅因为上游软件声称支持 OIDC 或改密，不能修改本表的实现状态。

Nextcloud 的 IAM 发起登出由两套 OIDC matrix 保留原 Cookie 验证：Authentik 覆盖浏览器
登出和管理员删除 session，LLNG 覆盖浏览器登出；SAML fallback 的 Authentik E2E 覆盖
Redirect SLO。SAML 后台撤销在 Nextcloud 正式声明 POST SLS 前不属于支持范围。
Provider 没有发布可选 `SAML_SLO_URL`（例如当前 Casdoor 集成）时，Nextcloud 只配置
SSO 与签名证书，并执行应用本地登出，不得把普通退出页猜成标准 SLO endpoint。
