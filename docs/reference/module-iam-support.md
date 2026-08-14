# Module IAM / OIDC 支持清单

OIDC 是 ANAS 当前默认 IAM 接入协议，但只对声明消费 `iam` capability 且支持 OIDC 的 Module 生效。不支持 OIDC 的 Module 不会被强制改用 OIDC；它按 Manifest 支持范围回退或继续使用自己的认证方式。

| Module | 是否可用 OIDC 登录 | 当前认证路径 | 结论 |
| --- | --- | --- | --- |
| `netbird` | 是 | 直接消费 IAM/OIDC | 已实现 |
| `oauth2_proxy` | 是 | 直接消费 IAM/OIDC，并为 ForwardAuth consumer 提供门禁 | 已实现 |
| `ddns_updater` | 间接 | 经 `oauth2_proxy` + Traefik ForwardAuth | 已实现 |
| `nextcloud` | 是 | 默认使用官方 `user_oidc`；LDAPS provision 用户/组；`user_saml` 保留为显式 fallback | 已实现；OIDC 登录复用 LDAP 用户，不自动创建第二套账号 |
| `meshcentral` | 是 | IAM/OIDC 认证；LDAPS 同步用户/组；OIDC group 映射应用访问和 site-admin | 已实现 |
| `lam` | 否 | LDAPS 目录管理登录 | 不属于当前 IAM consumer |
| `authentik` | 不适用 | IAM provider；另有固定 `akadmin` break-glass | 提供 OIDC/SAML，不把自身当普通 consumer |
| `llng` | 不适用 | IAM provider | 提供 OIDC/SAML，不把自身当普通 consumer |
| `ddns_go` | 否 | ANAS 托管 local emergency account | 不支持 OIDC |
| `traefik` | 否 | ANAS 托管本地 BasicAuth emergency account | 不支持 OIDC |
| `collabora` | 间接 | 由 Nextcloud/WOPI 集成，不提供独立用户登录 | 跟随 Nextcloud 会话 |
| `postgres`, `mariadb` | 否 | 数据库凭据/Adminer | 不是 IAM 登录 |
| `samba_dc`, `samba_fs` | 否 | AD/LDAP/Kerberos/SMB | 不是 OIDC Web consumer |
| `eturnal`, `freeradius`, `lego` | 否 | TURN/RADIUS/无交互 UI | 不适用 |

## 默认解析规则

协议优先级为：Module 显式 `iam_protocol` > 部署 `identity.iam.default_protocol`（仅当 Module 支持）> Module Manifest preference。Nextcloud、MeshCentral、NetBird 和 OAuth2 Proxy 当前都默认选择 OIDC；Nextcloud 可显式切换回 SAML，MeshCentral 只声明 OIDC。

## 后续实现门槛

把一个 Module 标为“OIDC 已实现”至少需要：Manifest OIDC interface、provider client registration、redirect URI/scope/claim/group 映射、Secret 传递、应用内验证和真实浏览器/HTTP 登录 E2E。Nextcloud 与 MeshCentral 由 `server-authentik-oidc-login-e2e.sh` 覆盖完整授权码登录、应用 session、目录身份和管理员组映射。仅因为上游软件声称支持 OIDC，不能修改本表的实现状态。
