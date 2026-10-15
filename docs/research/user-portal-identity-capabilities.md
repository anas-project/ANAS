---
doc_type: research
status: current
created: 2026-10-11
updated: 2026-10-12
evidence_as_of: 2026-10-12
---

# 用户门户的身份与目录能力

固定版本原生能力足以支撑原密码改密、到期恢复、受限重置/解锁和单成员修改。
Casdoor 后续原生实验确认：用户 OIDC token 可读取当前本人账号/权限，
独立再认证客户端的原生配置配合签名 auth_time 校验可完成近期认证，
discovery 的 OIDC 登出端点可退出当前 IAM 会话；本轮没有新增 Casdoor 源码补丁。
普通应用凭据仍不能用作最小只读凭据，原生 max_age/prompt 仍未强制重新输入凭据。
Samba 不支持 LDAP assertion control，
应采用已验证的 GUID/SID 稳定寻址和单成员增删，而不是依赖该控制。

本文研究已有协议和固定实现的边界，不是门户验收报告。逐项结果、源码/制品摘要、
失败重试和共享证据仅维护在
[门户计划 §6—7](https://github.com/anas-project/ANAS/blob/master/modules/user_portal/dev-docs/plans/archived/user-portal.md)。
接线与授权方案见 [独立用户门户设计](/architecture/user-portal-design)，门户候选已实现，产品验收见配套计划。

## 研究范围

对象是 Casdoor 3.143.0（上游提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 加 ANAS r12 补丁）
及 Samba 4.23.6。验证采用隔离域、最小普通人员和专属写绑定，不访问业务目录。
不重新比较 IAM 产品，也不测试已移除的 Authentik。
规范说明与固定版本实验相互核对；未实验的门户授权、审计、限流和跨系统竞态仍是未验证项。
实验镜像是既有基础镜像覆盖当前源码/制品的候选，不证明完整发布镜像或多架构兼容。

## Casdoor：登录与受限读取须分开

固定源码的 `authz.IsAllowed` 对 application 身份提前放行；原生应用客户端凭据并非天然只读。
隔离 API 实验验证了创建应用、修改测试用户，以及普通 consumer 读取另一组织用户的能力。
即使门户只调用读取 API，交付这个 secret 仍会把更广的权限交给门户进程。
固定源码依据为
[authz/authz.go](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/authz/authz.go)
及 `routers/base.go`；本文结论使用本地固定归档，不根据滚动主分支推断。

同一固定版本支持使用 S256 verifier、不携带 client_secret 交换 authorization code。
错误 verifier 和重复使用 code 被拒绝，ID Token 的 RS256 签名、issuer、audience、nonce 与 auth_time
可验证。源码见
[token_oauth.go](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/token_oauth.go)。
据此普通门户登录继续使用 PKCE。后续实验验证用户自己的 Bearer token 能调用原生
`/api/userinfo`、`/api/get-account`，取得当前本人 anchor、组、状态与完整权限对象；
同一个旧 token 在 IAM 已撤组后读取到新事实，不用新增专用接口。
`/api/get-user` 受 AccountItems 的字段显示规则影响，隐藏的 isForbidden 会被置零，
不能用它替代真实状态读取。`/api/get-account` 保留该状态，门户须显式核验，
UserInfo 本身返回成功不等于账号有效。

按首版限定的正向组 OR 协议，实验读取本人权限并检查明确 binding、启用/Approved 状态、
Allow、固定 model/resource/action 与相同组集合，再结合当前组过滤；
组 OR 两侧、platform_admin 同源 Admins 组、管理员无显式组、撤组、隐藏/禁用条目及账号停用反例通过。
这验证原生读取路径，门户实现、当前 AD 状态、未知组发布校验和目录/图标旁路仍须产品验收。
不把上述固定模板判定扩展成任意 Casbin 模型解释器。

[原生 Casbin API](https://casdoor.ai/docs/permission/exposed-casbin-apis/) 的 enforce/batch-enforce
在固定版本可用，但单独 enforce 在权限 isEnabled=false 或 State=Pending 时仍返回 true，
不能直接作完整目录判定。普通用户 token 默认不能调用 enforce，无需为固定组规则增加此调用权。
另验证了 User 类型 API key 绑定非管理员账号、原生 API permission 仅允许 GET get-permission：
可读该组织策略，其他组织、全量用户列表和应用/权限写入拒绝，key 停用后拒绝。
这是备用的原生只读方案；首选本人 token 路径，无需额外 key。

## Casdoor：真实 auth_time 尚不等于强制再认证

[OIDC Core 的认证请求规则](https://openid.net/specs/openid-connect-core-1_0.html#AuthRequest)
规定 max_age 的重新认证及 auth_time 语义。ANAS r12 补丁已记录真实认证时间：
旧会话和 refresh 保留原时间，重新输入密码认证更新时间，这些行为已实验确认。
但是原生浏览器在 max_age=0 和 prompt=login 下会自动返回旧会话的认证结果，未要求输入密码。

后续真实 Chrome 实验采用独立再认证客户端，关闭 enableAutoSignin/enableSigninSession；
普通登录客户端保留 SSO，再认证客户端即使收到旧 IAM Cookie 仍显示密码表单。
重新输入密码得到同一 anchor 的新认证时间；输入另一人员凭据产生不同 sub，近期检查拒绝。
直接复用旧会话仍可取得 code，但签名 auth_time 保留原时间，实验的 5 秒窗口拒绝该结果。
因此采用“原生表单配置 + 门户校验 auth_time/同一身份”的路径，不依赖 max_age/prompt 被正确执行，
当前无需新增强制再认证补丁。此配置没有禁用原生“继续使用当前账号”的能力，
门户仍须拒绝旧认证，并清楚提示重新输入凭据；不能把新 token 的 iat 当成新认证。
门户回调是否拒绝换身份、伪造/缺失时间和请求串用，须随门户实现另验。

## Casdoor：退出门户同时退出当前 IAM 会话

按 [OIDC RP-Initiated Logout](https://openid.net/specs/openid-connect-rpinitiated-1_0.html)，
使用 discovery 的 end_session_endpoint。固定客户端 discovery 指向 `/api/logout`；
真实 Chrome 携带 id_token_hint、登记的 post_logout_redirect_uri 和 state 完成回跳，
当前 IAM Cookie 随后不能自动登录，门户 token 与同一 IAM 会话的观察 token 被拒绝，
另一浏览器上下文的会话仍可 SSO。未登记返回地址被拒绝。
上述是 ANAS 固定组合的协议能力证据，hint 不能用于门户接线，因为门户 token 不得进入浏览器。
同一 server 制品的 SQLite/内置组织补测确认：不传 hint、使用 IAM Cookie 的原生退出可清除当前会话，
不能再取得 SSO code；两应用使用不同回调列表时已登记返回仍可用。
但 IAM 已退出时该端点返回成功 JSON 而不回跳，首版采用固定门户结果页调用原生
`POST /api/logout`、`credentials: include`，不发送 token 或退出返回参数。
同站 HTTPS 下，原生 redirectUris 登记门户 origin 后 CORS 放行；未登记同站 origin 被原生 403 拒绝，
已退出时重复调用成功，另一浏览器会话保持有效。
内置组织旧 token 仍有效，不能将 Cookie 退出解释为全部 token 撤销；
既有 ANAS 专用撤销补丁的适用范围未被该无 LDAP 补测替代。
门户须先撤销本地会话、丢弃后端 token，再执行 IAM 退出。
门户实际 ANAS 接线、CSRF/Origin、固定结果页及 IAM 不可达/Cookie/CORS 受限提示仍需产品实现，
不宣称所有设备或外部应用的本地会话均已撤销。此路径不新增 Casdoor 源码补丁。

## Samba：原密码 change 与管理员 reset

[Microsoft AD 的 unicodePwd 规则](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/6e803168-f140-4d23-b2d3-c3a8ab5917d2)
区分同一 Modify 中 delete 旧值/add 新值的 change 与单个 replace 的 reset。
change 由目录核验当前原密码，reset 依赖 Reset Password 权限；两者不能互相降级。
固定 Samba 的普通 change、策略拒绝、首次强改和真实密码到期均已有原生实验。

首次强改/到期时，普通本人 bind 会失败。实验确认已有只读 svc_ldap 可以通过 LDAPS
提交原密码 delete/new add，由目录验证原密码；错误原密码被拒绝，新密码随后正常登录。
因此受限改密入口不必先取得正常 SSO 或成功的本人 bind，也不必持有 reset 凭据。
仍须在入口实现唯一人员定位、状态/保护检查、统一错误、限流和不生成普通会话的约束。

改密并不保证旧密码立即失去所有登录能力。Samba 的
[old password allowed period 参数](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html)
默认允许旧密码在 60 分钟内用于特定认证；官方
[4.15.7 发布说明](https://rzip.samba.org/samba/history/samba-4.15.7.html)
也记录了 LDAP simple bind 遵循该参数的修复。
本轮实际观察到旧密码仍能 bind，但不能再以旧密码更改当前密码。
门户应独立注销本人门户会话，不能宣称所有 IAM/外部应用会话或所有密码登录已立即撤销。

## Samba：最小 ACL 不会约束 member 值的人员范围

专属 reset 服务可限制到指定 OU 的 Reset Password、pwdLastSet、lockoutTime；
专属成员服务可限制到指定组的 member 属性。跨 OU、属性和组拒绝已有固定版本反例。
解锁可仅清零 lockoutTime，保持密码、userAccountControl 与 pwdLastSet 不变。
现有共享 svc_password 没有组写权，不应为门户扩大它或只读凭据的权限。

member 属性写权仍可把范围外人员加入允许组，ACL 不会自动理解门户的目标人员名单。
门户必须按实时事实验证同一完整规则中的操作者、动作、人员与组，以及直接/间接保护关系。
不能用目录 ACL 通过来推断完整业务授权通过。

## Samba：稳定引用和单值操作可用，assertion 不可用

[RFC 4528](https://datatracker.ietf.org/doc/html/rfc4528) 描述 LDAP assertion control；
[RFC 4511](https://datatracker.ietf.org/doc/html/rfc4511) 描述 critical control 的协议行为。
固定 Samba 未声明支持 assertion。正确编码的 critical 请求返回 12（unavailableCriticalExtension），
不执行写入。非 critical 或错误 Boolean 编码被忽略，不能据此判断支持。
本轮以独立原始 TLS/BER 探针验证；具体编码重试与响应类型见配套计划。

组 <GUID=…> 寻址、人员 <GUID=…>/<SID=…> 成员值、单值 add/delete 则实际可用。
两个连接并发添加不同成员不会覆盖其他成员；同 DN 删除重建后，旧 GUID 失败而新组保持不变。
重复 add/缺失 delete 的实际返回码为 68/53，需要结合具体操作和当前状态判断，
不能把任意 53 错误都处理成无害冲突。

据此建议门户由当前业务 anchor 解析当前对象的 GUID/SID，核验后以稳定引用单值修改；
引用不存在时失败，不回退旧 DN，不 replace 全部 member。
GUID/SID 是当前 LDAP 操作的定位方式，业务身份仍使用 immutable anchor。
该方法不提供跨对象事务；门户外管理员同时改变保护或授权关系的反例仍待验证，
不能用单进程锁或上述并发添加结果宣称竞态已全部解决。
