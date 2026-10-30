---
doc_type: research
created: 2026-10-23
updated: 2026-10-23
evidence_as_of: 2026-10-23
---

# LLNG 与现有 Casdoor 集成、界面及免密码能力对比

## 结论

LLNG 适合以 Samba AD 为用户源的 Web SSO，原生强制改密比当前 Casdoor 接入更直接。
在用户确认完整方案后，候选版已补入 **user_portal 兼容、目录事件撤权、Passkey 和统一主题**，
并完成下述隔离验收。全部消费者、SAML 和剩余运维验收尚未完成，继续保持 developing；
本轮不切换生产 IAM。详细结果和未完成项以[配套计划](https://github.com/anas-project/ANAS/blob/master/modules/llng/dev-docs/plans/llng-production-readiness.md)为准。

2026-10-23 用户进一步明确：LLNG 不需要独立恢复管理员；AD 要求改密或密码到期时允许
已有 Passkey 继续登录。这两项作为后续[上线方案](./llng-production-readiness.md)的明确边界。
下表更新到当前候选；历史失败、测试基线与新增能力的验收均保留在配套计划。

初始比较为 LLNG 2.23.2、Casdoor 3.143.0；LLNG 当前候选为 2.23.4。初始源码基线为
`058d5c2ce87253897e1619b762f42cef9c3e71ee` 加 2026-10-23 工作区变更。
下文区分仓库已接入行为、上游可配置能力和待验证能力。Casdoor 工作区第九个同步串行化补丁
的验收独立于本轮；工作区代码不能直接视为生产通过基线。

## 对照已实现的 Casdoor 功能

| 能力 | 当前 Casdoor 接入 | 当前 LLNG 接入与需要补充的部分 |
| --- | --- | --- |
| Samba 首次/到期改密 | 登录页链接到 user_portal 恢复入口；Casdoor 自身不启用目录密码写回 | 两个原生开关已接入；改密成功后用新密码重新登录。实际回跳结果见[配套计划](https://github.com/anas-project/ANAS/blob/master/modules/llng/dev-docs/plans/llng-password-change.md) |
| OIDC 稳定身份 | 补丁使 token、UserInfo、Logout Token 的 `sub` 使用目录 anchor | 已用 `anasIdentityAnchor`，缺 anchor 拒绝发码；不需要重写这一部分 |
| SAML 稳定身份 | 补丁使 NameID 使用目录 anchor | 已生成永久 anchor NameID 和缺失拒绝配置；真实 SP/SAML 矩阵后置，尚未通过 |
| 组准入及角色 | 应用组准入、受信目录组映射角色；角色变更关联撤权 | 已有 RP/SP 规则及事件消费者；门户直接/嵌套组和 Admins 撤销旧 Cookie 实测通过，其他应用逐项待验 |
| 目录停用、删除、撤组 | dirwatch 订阅目录事件，持久游标、周期兜底，撤销 IAM/下游会话 | 已实现持久事件消费、回调失败重试和周期核对；门户禁用/锁定/撤组旧 Cookie 失效通过，删除后旧 Passkey 与中央会话拒绝 |
| OIDC 双向登出 | 原生加补丁、应用回调及失败记录/重试 | 门户双向退出及 IAM 故障本地退出通过；Nextcloud 原生退出缺 state/本地优先保障，扩展方案待确认；Vikunja 无后台接收端限制已获接受 |
| user_portal 自助及委派管理 | 当前门户依赖 `casdoor-account/v1`、用户 token 账户读取及新鲜认证 | 已接入 `llng-account/v1`、公开 S256 PKCE、签名 auth_time/AMR、真实近期认证；本人资料/改密/委派写入和普通用户边界实测通过 |
| 应用目录 | 已接入冻结目录、图标及门户入口 | 原生 applicationList/catalog 与独立门户均已适配；应用准入使用 IAM 现值、目录与冻结清单交集 |
| 主题与登录品牌 | 已接入主题导入、组织默认、个人明暗模式、应用 logo 和改密链接 | 原生配置与 CSS/JS 接入品牌、明暗偏好及主题资产；中英文桌面/390 像素窄屏实测通过 |
| 本地恢复管理员 | 有独立 `admin_casdoor`、受管凭据和恢复路径 | Manager 仍依赖 AD；用户明确无需独立恢复管理员，不作为 LLNG 上线缺口 |
| 运维生命周期 | 已有凭据轮换、空工作区恢复及原生管理操作记录 | 空库恢复后既有 Passkey/身份、公钥轮换、实际 Nextcloud 客户端秘密轮换与回滚通过；旧版升级及其他适用凭据/故障验收继续 |

本表依据 `modules/casdoor/hook/iam.go`、`hook/themes.go`、`hook/catalog.go`、
`casdoor/patches/`、两 Provider 的 `docs/technical.md`、LLNG 的 `llng-config.sh`，以及
`modules/user_portal/hook/main.go` / `portal/` 的双 schema 适配和配套计划中的实际测试记录。

特别要分清：现有门户的用户/组/邮箱/委派管理不少能力由门户直接访问 Samba 实现，
它们并非 Casdoor 独占；主要迁移障碍是门户对身份验证、账户状态和再次认证的接口依赖。
Casdoor 当前 SAML SLO 也不是已完成的全部能力，不能把它列为 LLNG 单方缺失。

## LLNG UI 能否定制

**可以，基础外观和错误提示不需要打源码补丁。** 官方支持 logo、背景、favicon、CSS/JS、
登录按钮、语言文案，以及自定义 skin。错误提示可按消息编号覆盖，例如强制改密 `PE25`、
到期 `PE22`、成功 `PE35`。修改文案不会自动补齐缺失的认证流程。

建议先使用配置、独立 CSS 和 `customLoginHeader.tpl` / `customLoginFooter.tpl` 等扩展点。
整体更换模板时，应保留官方表单字段、一次性 token 和回跳参数，并在升级时检查模板兼容性。
直接修改容器中的默认 skin 会被重建/升级覆盖；Module 需要管理自有资产和配置。
依据：[LLNG Portal customization](https://lemonldap-ng.org/documentation/latest/portalcustom.html)。

Casdoor 将登录页、组织主题、注册项、用户资料和 Provider 配置放在产品管理界面中；
LLNG 更偏向 SSO 配置、规则、会话和认证因子管理。LLNG 可以做同样风格的登录页，
但不会因此自动获得完整的业务账号中心。当前候选已接入 ANAS 主题资产和明暗偏好。
[Casdoor UI 定制](https://casdoor.ai/docs/application/ui-customization/)说明其管理界面里的
CSS、背景与布局能力；该在线文档会随新版变化，不据此断言固定 3.143.0 已具备所有新增选项。

产品层面的其他差距是 Casdoor 的组织/用户生命周期管理、Casbin 权限管理、现成的
短信/社交/扫码 Provider 管理体验。LLNG 也能以 OIDC/SAML 联邦或扩展后端接入外部身份，
但不是 Casdoor 用户管理 API、组织模型或权限 API 的兼容实现。对于本项目，Samba/LAM/门户
可以承担其中部分职责，是否迁移应按实际使用功能决定，不能只比较功能数量。
依据：[Casdoor 核心概念](https://casdoor.ai/docs/basic/core-concepts/)、
[LLNG 后端与协议清单](https://lemonldap-ng.org/documentation/latest/start.html)。

## 免密码登录支持什么、缺什么

这里的“免密码”指登录时不输入 AD 密码。已有 SSO cookie 自动登录、密码后再输入 OTP、
忘记密码找回，是不同流程。当前 **LLNG 候选提供密码和 Passkey 两个独立首因素**；
Casdoor Hook 的 `signinMethods` 仍只生成 Password，不能把上游能力当作已上线功能。

| 方式 | LLNG 原生能力 | 当前 ANAS 缺口 |
| --- | --- | --- |
| Passkey / FIDO2，含设备指纹、面容、Windows Hello、安全密钥 | 2.20 起可作为第一因素；2.23.2 已满足版本条件。官方页面仍带 beta 标识 | 2.23.4 候选已启用、使用原生登记/删除和双设备；虚拟认证器验证首因素、备用设备、撤销/重登记、目录边界及空库恢复；未宣称各品牌实体设备均已测试 |
| Kerberos / SPNEGO 域内自动登录 | 根据桌面域会话验证 Kerberos ticket | 需服务 SPN/keytab、安全保存/轮换、浏览器信任和失败回退；不是改两个开关即可 |
| 客户端证书 / 智能卡 | 原生 mTLS 后端，由 Web 层验证证书并传入身份 | 需证书签发/撤销、可信代理传递和多登录方式配置 |
| 外部身份免密码 | 可作为 OIDC/SAML 客户端，将认证交给上游 IdP | 需身份锚点关联、AD 用户状态/组规则和注销验证 |
| 邮件验证码、TOTP、SMS OTP | 官方现成组件主要是第二因素；SMS 可由 external/REST 扩展连接 | 尚无与 Casdoor 相同的默认首因素产品流程；若组合为唯一认证方式，需要单独验证，不能把 2FA 直接标成密码免输入 |
| 邮件 Magic Link、手机扫码确认 | 在所核查 LLNG 2.x 文档中未确认等效的一键原生首因素流程 | 可评估联邦或插件；这是待补能力，不宣称上游绝对无法实现 |

[Passkey 官方说明](https://lemonldap-ng.org/documentation/latest/authwebauthn.html)要求先启用
WebAuthn 第二因素，并使用可发现凭据（discoverable credential，认证器可直接选账号）。
用户先通过 AD 登录登记，之后以 WebAuthn + AD userDB 登录；无需升级或另建凭据服务。
服务端凭据保存在原生 `psessions`；当前配置与边界见[技术文档](https://github.com/anas-project/ANAS/blob/master/modules/llng/docs/technical.md)。

[Kerberos](https://lemonldap-ng.org/documentation/latest/authkerberos.html)、
[mTLS](https://lemonldap-ng.org/documentation/latest/authssl.html)、
[邮件第二因素](https://lemonldap-ng.org/documentation/latest/mail2f.html)分别说明上述能力和配置边界。
Casdoor 在线文档列出验证码、Magic Link、扫码与 WebAuthn，但这是上游现有产品能力，
没有在本轮对固定 3.143.0 逐项运行验证：
[登录方式](https://casdoor.ai/docs/application/signin-methods/)、
[WebAuthn](https://casdoor.ai/docs/how-to-connect/webauthn/)。

### Passkey 上线前尤其要解决的边界

- **密码被要求重置时能否继续用 Passkey**：用户明确允许已有 Passkey 继续登录。
  候选实测两者均允许；目录禁用、删除、锁定及账号到期均拒绝。
- **停用、删除、撤组**：新认证必须读取并过滤 AD 状态；已有 SSO/应用会话还依赖前述撤权闭环。
- **丢失设备与恢复**：备用认证器、用户撤销、密码重新登记及空库恢复后的既有凭据已实测；
  独立恢复管理员按用户决定排除，AD/LLNG 故障依赖恢复其配置与数据库。
- **域名与备份**：在固定 RP ID、HTTPS 下恢复配置/会话库后，原凭据有效性已验证；
  不能把换域名或从 Casdoor 迁移 Passkey 当作普通用户数据同步。

## 建议顺序与复杂度

1. 本轮完成原生改密与真实 OIDC 回跳，复杂度低。
2. 已按用户确认的高复杂度方案补入门户、再次认证和目录撤权；独立恢复管理员排除。
   继续完成全部适用消费者和运维验收，尚不切换生产 IAM。
3. 界面先接 logo、CSS、中文提示和已有主题资产，复杂度低到中。
4. 免密码优先采用 LLNG 自有 Passkey + 现有 AD userDB/psessions，复杂度中；
   已通过注册、免密码登录、撤销/重登记、空库恢复及实际 OIDC 回跳。

不建议为了免密码认证再叠加一个独立 IAM；当前版本已有原生基础能力。


## 同主机资源结果

2026-10-23 已计入必需目录消费者后，空闲工作内存中位值之和：Casdoor 92.08 MiB、
LLNG 2 worker 269.14 MiB。双方 60 次 OIDC 流程全部成功；共享主机压力和 swap 不同，
不能用本轮尾延迟推断通用性能优劣。完整方法、CPU/延迟、限制和证据见
[上线调研中的资源实测](./llng-production-readiness.md#2026-10-23-同主机资源实测)。
