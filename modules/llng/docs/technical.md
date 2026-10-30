# LemonLDAP::NG 技术实现

本文面向 Module 维护者，记录 `llng` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2.23.4-r13` / `anas.module/v1`.
<!-- generated:module-identity:end -->

2026-10-23 上游升级到 2.23.4。服务器回归发现原启动脚本读取 `ping` 首行后提前关闭管道，
可能在 `pipefail` 下中断客户端注册；现改为读完输出，保留地址解析失败时终止配置的行为。
这是 Module 脚本修复，不修改 LLNG 上游源码。新版本验收结果与历史基线分别记录在
[改密计划](../dev-docs/plans/llng-password-change.md)；完整上线范围见
[上线方案](../../../docs/research/llng-production-readiness.md)。

## 依赖的 Module、Capability 与 Contract

目录事件消费者使用同一 LLNG 镜像中的原生 Perl 会话和 OIDC 签名接口，不新增运行时依赖。
`anas_llng_dirwatch` 以 UID 33 只读挂载 Samba 日志及 LLNG 配置，单独保存 `dirwatch/`
下的游标、待通知列表和健康状态。2 秒防抖、至少 5 秒调用间隔、300 秒全量核对；事件突发
从首条事件计时，不随新事件无限延期。目标为健康依赖下 60 秒内撤权，实机验收尚未完成。
先同步落盘待通知列表，再删除受影响的 LLNG 凭据并调用原生后台登出；只有应用明确返回
200/204 才确认通知成功。失败不提交游标，继续处理其他账户并重试。游标损坏触发全量核对；
待通知文件损坏必须恢复该文件，不能丢弃未完成通知。读日志、配置或目录失败均不确认消费。
仅支持浏览器登出或没有登出接收接口的 Consumer 默认保留失败状态，不能声明后台撤权完成。
`llng.background_logout_exceptions` 可显式列出已接受此限制的 OIDC client ID（逗号分隔，
默认空；例如 `vikunja`）。仅在确实没有后台接收端时，撤销 LLNG 中央授权后将该客户端
记录在持久 `pending.json.limitations` 和健康摘要 `limited_clients`，允许事件游标推进；
应用自己的旧会话仍可能有效。已配置接收端的 HTTP/签名等错误继续重试，不受例外影响。
该选项不提供后台退出能力，也不豁免其他客户端或 SAML；升级后具备接收端即恢复实际通知。
公开 Nginx 请求日志只保留地址、时间、方法、路径、状态和字节数，省略查询参数及 Referer。
Nginx 自带请求错误日志无法脱敏原始 URL，因此公开虚拟主机关闭该日志；保留上述状态日志
和原生 LLNG 服务错误。生产环境应使用默认 `warn`，不启用会打印协议令牌的上游 debug 日志。
本轮仍处于开发验收，结果见[上线计划](../dev-docs/plans/llng-production-readiness.md)。

主容器配置期间对外返回 503，worker 使用只读就绪标记避免消费尚未完成的客户端配置。
LDAPS 明确使用 `/certs/` 中受信任 CA 文件，保持证书验证，两个容器采用同一信任来源。

门户外观使用原生 `portalCustomCss`、`portalCustomJs`、`portalMainLogo` 和 `portalFavicon`，
不复制登录/改密模板。Hook 从应用目录生成明暗配色，优先复用隐藏 user_portal 的冻结图标，
其次使用 LLNG 管理图标；没有目录图标时沿用 ANAS 门户标记。只接受既有主题规范中的颜色值。
门户跳转的 `theme=dark/default` 会保存到认证域的本地偏好；直接访问默认跟随系统，也可切换
浅色、深色及系统模式。不同域的 localStorage 独立，门户每次跳转携带当时的明暗选择。
原生表单、CSRF token、密码策略和协议回跳保持由 LLNG 处理；中英文及窄屏实测结果归上线计划。

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | 提供 Capability | `oidc, saml` |

## Compose 拓扑

镜像将公开 Perl 模块、配置模板和 SQL 模板固定为 0644，主题目录为 0755、资源文件为
0644，保证 `www-data` 从私有源码 bundle 构建后仍可读取。运行时私有配置沿用原有权限。
目录 watcher 显式消费 Lego 发布的 `ANAS_TLS_TRUST_BUNDLE_NAME`，使用对应公开信任包。

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_llng` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.4-r13` | `traefik, db` | 8 |
| `anas_llng_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.4-r13` | `traefik, db` | 5 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.background_logout_exceptions` | string | — | `""` | `static` | `LLNG_BACKGROUND_LOGOUT_EXCEPTIONS` | 否 | 否 | 否 | 是 | `container_recreate` | 显式接受无法后台强制退出的 OIDC client ID，逗号分隔；应用旧会话仍可能有效 |
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-llng-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用测试入口 |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Manager 域名前缀 |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 测试入口域名前缀 |
| `llng.workers` | int | — | `2` | `static` | `LLNG_WORKERS` | 否 | 否 | 否 | 是 | `container_recreate` | FastCGI worker 数（1–32）；较少 worker 降低内存，较多 worker 提高并发容量 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

Samba AD 是用户和 Group 来源。Portal 使用目录认证，IAM 向 Consumer 发布 OIDC/SAML 端点和 Group 属性。`Admins` 可进入 Manager。
OIDC issuer 保留固定 `2.23.4` discovery 返回的尾斜杠，Consumer 必须按该精确值校验；discovery URL 仍为 Portal 根路径下的 `/.well-known/openid-configuration`。

### 应用会话登出

固定 `2.23.4` 中，通用 OIDC 登出方法在 `render_env` 映射为 `oidcRPMetaDataOptionsLogoutUrl`、`LogoutType=back` 和 `LogoutSessionRequired=1`；`PostLogoutRedirectUris` 与免确认设置只服务 RP 发起登出，不能替代 back-channel。SAML 从 SP metadata 读取 SLS并保持 `SignSLOMessage=1`；Redirect/POST 都只保证浏览器 SLO。启动配置脚本先删除整个 `oidcRPMetaDataOptions`/`oidcRPMetaDataExportedVars` 与 SAML SP registry，再从当前 Client 契约重建，确保旧 endpoint 和相反协议配置不残留。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| 目录密码回写 | restricted password-bind identity |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 公开 OIDC 客户端与 SAML 身份

公开 OIDC 客户端必须显式声明 `PUBLIC_CLIENT=true` 且不得传 secret，映射为原生 Public 和
RequirePKCE。近期认证使用独立公开 RP，复制相同属性和准入组；两类 RP 都保留 RS256、精确
redirect URI 与不可变 subject。`AnasOIDC` 使用上游插件 hook 对公开客户端强制 S256，
匿名授权请求完成 PKCE 检查后交回原生认证流程，不在缺少会话时提前读取目录；
`prompt=none` 因而可以由原生发行方返回 `login_required`。已有会话仍必须重新核对目录。
近期认证 RP 还必须完成一次新的密码或 Passkey 认证。原因是固定 2.23.4 会忽略 `max_age=0`，
且 `prompt=login` 默认容忍近期 Cookie；仅设置这两个请求参数不足以证明重新认证。
这项适配的原生浏览器验证及 user_portal 整体接入仍按上线计划执行。

`AnasDirectory` 在 OIDC 授权和 SAML AuthnRequest 上重新读取 AD 账户状态与嵌套组，目录
查询失败即拒绝，不让旧 SSO 属性授予新权限。`llng-account/v1` 只接受绑定门户 RP 的本人
Bearer token；经原生 token/会话校验后返回当前目录身份、组及实际准入规则的结果。只读的
`account-policies.json` 关联应用目录与原生 RP/菜单规则；运行配置偏离时不返回该应用权限。
不向门户提供管理员令牌、其他用户查询、设备私密数据或目录写权限。

SAML 每个 SP 显式设置请求的 NameID format、ForceNameIDFormat 和
`NameIDSessionKey=anasIdentityAnchor`，缺省使用 persistent；拒绝 transient/entity 等无法保持
用户稳定身份的格式。新 SSO 的 GrantSession 规则拒绝缺失 anchor。真实 SP 矩阵待验收。

### 原生首次登录与到期改密

`lmConf.json` 为新安装设置 `ldapChangePasswordAsUser=1` 和
`ldapAllowResetExpiredPassword=1`；`llng-config.sh` 每次启动通过 CLI 重写两项，覆盖历史
持久配置。未增加 Module 参数、独立服务或 LLNG 源码补丁。

原生 `Auth/AD.pm` 将 `pwdLastSet=0` 映射为 PE25，真正到期映射为 PE22；错误旧密码为 PE5。
`Password/AD.pm` 经 `Lib/Net/LDAP.pm` 提交带旧密码的 `unicodePwd` delete/add，Samba 裁决，
无需让到期用户先成功 LDAP Bind，也无需赋予查询账号管理员重置权限。
`Auth/LDAP.pm` 在成功后返回 PE35 和登录表单，不创建会话；用户需用新密码继续原认证请求。

详细基线、真实浏览器 OIDC 结果及限制见[改密计划](../dev-docs/plans/llng-password-change.md)。
原生机制和旧密码宽限期见[研究](../../../docs/research/llng-samba-password-change.md)。
### 原生 Passkey

原生 OIDC `oidcServiceMetaDataAmrRules` 按实际首因素发布签名的 `amr`：AD 为 `pwd`，WebAuthn 为 `webauthn`。不再用 authenticationLevel 推断密码登录，避免同等级 Passkey 被误判。用户门户仅信任已验证 ID Token 中的该声明。

`authentication.jq` 在新安装模板和持久配置重建时统一启用原生 Choice：AD 密码与 WebAuthn
首因素。镜像新增 Debian `libauthen-webauthn-perl`，这是原生 WebAuthn 校验器的依赖，未修改
上游认证代码。凭据要求 discoverable/resident key 和用户验证（设备 PIN 或生物识别），
可在原生设备管理页登记多个认证器及删除丢失设备。

持久凭据使用 `anasIdentityAnchor` 作为 `whatToTrace`；Passkey 分支按该不可变标识查询 AD，
密码分支仍按登录名查询，页面显示 `displayName`。持久表为 WebAuthn user handle 增加索引。
以前未启用 Passkey 的版本按用户名保存的历史个人偏好不会自动搬迁；旧数据库和配置须随升级备份。

原生 GrantSession 规则拒绝缺失标识、禁用、锁定及账户到期；密码自然到期和 `pwdLastSet=0`
不阻断已有 Passkey。两种首因素并列，登记 Passkey 不会自动强制密码登录再做第二因素。
原生设备管理需要激活规则非空，因此使用恒假的 `0 == 1` 保留删除入口。关闭进程内和本地
会话缓存，使双 worker 的连续设备登记不会用旧列表覆盖新设备，也让删除会话立即生效；
额外数据库读取计入资源测量。默认两个 worker，可通过 `workers` 调整。
丢失全部认证器时可用有效 AD 密码登录并撤销旧设备、重新登记；密码也不可用时先通过既有
AD 管理流程恢复密码，不提供独立本地恢复管理员。生产验收进度见[上线计划](../dev-docs/plans/llng-production-readiness.md)。

### 配置缓存刷新

启动入口在恢复 Nginx runtime 后写入仅监听 `127.0.0.1:8089` 的 `/reload` 虚拟主机，
转交现有 FastCGI server 的 `LLTYPE=reload`；不发布端口，也不添加外部路由。
`lmConf.json` 和每次启动的持久配置重建都将 reload URL 指向该地址。最终 CLI cache update
与 HTTP reload 成功后才写 `/run/llng-configured`，避免旧 RP 配置继续服务而健康检查提前通过。
初始化同样通过独立的 loopback reload 端点等待 worker；所有公开 Portal/Manager 请求在就绪前
返回 503，包括来自 loopback 反向代理的请求。
这一入口采用[上游配置刷新方式](https://www.lemonldap-ng.org/documentation/latest/configlocation.html)，
用 loopback 监听限制访问，而不是开放 Manager API。

### 目录属性变更的实现侧

LLNG 直接认证 Samba AD，不保存用户副本。`sessions`、`psessions`、`samlsessions`、
`oidcsessions`、`cassessions` 的主键是 session ID；`_whatToTrace` 等字段只有查询索引。
`authentication.jq` 把最终 `whatToTrace` 统一设为 `anasIdentityAnchor`，用于持久凭据和
Manager 查询；页面标签仍取目录 `displayName`。目录属性由 `ldapExportedVars` 在新认证时读取，已有会话可能保留旧属性。

r12 的 `llng-config.sh` 在重建 RP 时明确设置
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor`。固定 `2.23.4-1` 的
`Lib/OpenIDConnect.pm::getUserIDForRP` 只在配置项为空时回退 `whatToTrace`；指定了 anchor 后
直接读取该会话变量。`Issuer/OpenIDConnect.pm` 的授权码签发、`_generateIDToken`、注销及
`Lib/OpenIDConnect.pm::buildUserInfoResponse` 都调用它；在线 refresh session 另存
`_oidc_logout_sub`，供其会话终止时发送同一主体的通知。Refresh token / access token 可为
不透明的会话 ID，不能因为不是 JWT 就要求它们包含 `sub`。

每个 RP 的 Rule 都包含 `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""`，
再与原应用组规则取交集；缺 anchor 的 SSO 会话不能取得授权码。`ldapExportedVars` 始终加载
anchor，不依赖 Consumer 是否显式请求同名 claim。通用属性契约仍用于人类可读的用户名、
姓名、邮箱与数组类型的组。Adapter 将通用 `email` 来源映射到 Samba AD 的 `mail` 会话变量，
OIDC/SAML 输出字段名保持 Consumer 的声明；不能把 `email` 当作 AD 属性读取，否则真实
邮箱会缺失，要求邮箱的 OIDC Consumer 会拒绝登录。其他明确属性来源保持原值。
Netbird 的 `sub` URL 投影在 `hook/iam.go` 的 calculate/render_env
边界被拒绝；Nextcloud 的 anchor UID 遵守已确认的 `DIRKEY-R-010` 例外。

验收入口为 `test-env/scripts/server-llng-nextcloud-identity-e2e.py`，使用测试主机已有
`cryptography` 验证 RS256 签名，不引入产品运行时依赖。它验证真实授权码、UserInfo、refresh、
签名 Logout Token，以及 Nextcloud 原 Cookie、LDAP 映射、改名文件归属和标签回收隔离。
协议夹具专用 RP 的 refresh 开关不代表所有生产 RP 都启用了 refresh。

SAML NameID 映射已显式切成 anchor；真实 SP 的签名、身份及退出矩阵仍待本轮验收。
本模块仍无目录事件 watcher；目录停用/撤组不自动删除已建立会话，需要 Manager 删除与 Consumer
撤权。不能把普通 Portal 登出记成目录事件实时撤权。

## 管理面与 Secret 生命周期

当前没有独立的本地 `break_glass`。Manager 与 Portal 共用目录认证；IAM 或目录故障时需要从主机侧修复，不能依赖不存在的本地密码。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Portal Documentation 菜单

`lmConf.json` 为两个 Documentation 应用写入
`inGroup("{{SAMBA_DC_ADMIN_GROUP_NAME}}")`，覆盖全新安装；`llng-config.sh` 在每次启动时
对当前持久配置再次写入同一规则，覆盖升级前遗留的 `display=on`。两个子应用都不可见时，
普通用户不会看到空的 Documentation 分类，管理员仍可使用本地文档和官方站点入口。

### Secret 边界

- `LLNG_OIDC_SERVICE_KEY_ID`
- `LLNG_SERVICE_PRIVATE_KEY`
- `LLNG_SERVICE_PUBLIC_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

### 签名密钥泄露与轮换

OIDC/SAML 签名私钥一旦进入终端输出、日志、测试报告、任务记录或其他非 Secret 边界，即使
没有确认外传，也按可能泄露处理。诊断命令必须按精确键白名单读取配置，禁止递归输出整份
LLNG 配置；报告只能记录 key ID、指纹和公钥，不得记录私钥正文。

当前不自动轮换运行中的 LLNG 密钥。轮换必须作为单独获批的运维事务：先盘点 RP/SP 是
动态读取 JWKS/metadata 还是固定证书，备份 workspace Secret 与 LLNG 配置，生成新密钥并
发布公钥，刷新固定信任方，验证 OIDC/SAML 登录和登出，再在旧 token/assertion 的有效期
及回滚窗口结束后撤销旧密钥。不支持双密钥过渡的部署必须安排维护窗口。发生疑似泄露但
尚未获批轮换时，应记录事件、限制任务/日志访问并保留轮换待办，不能静默覆盖 Secret。

原生 2.23.4 可用 `keys` 保存多把密钥，`oidcServiceSignatureKey` 逗号列表的第一把用于
签名，所有有效条目均发布到 JWKS。顺序为旧签名＋新公钥预发布、Consumer 更新缓存、
新签名＋保留旧公钥，最后等待有效期和回滚窗口后撤旧。仅在验收夹具中修改原生配置不会
更新 workspace Secret，不能代替正式凭据交付；重建前须确认两者一致。

已核实 Nextcloud `user_oidc 8.11.0` 缓存 JWKS 3600 秒，未知 `kid` 会在刷新重试之前报错。
不能发布公钥后立即切换签名。应等待缓存刷新并验证，或使用原生 `occ config:app:delete
user_oidc provider-<实际提供方数字ID>-jwksCacheTimestamp` 清除该提供方的缓存时间，完成一次
真实 OIDC 登录以取得含新旧公钥的 JWKS，再切换签名。只清除时间字段，不改变客户端凭据、
映射或其他提供方配置。提前撤旧会使仍有效的旧 token 无法通过只持有当前 JWKS 的验证方；
测试中的提前撤旧是失败边界验证，不是生产轮换顺序。实际测试结果归配套计划。

Nextcloud 8.11.0 客户端秘密可通过 `occ user_oidc:provider` 的 `--clientsecret-file` 传递，
使用应用进程可读的 0600 临时文件并及时清理。同一命令须同时传入原有 `--clientid`：
该版本的更新选项判定漏掉 secret-file，单独使用时会只查询却返回 0。两端更新后必须用
真实登录验证，并确认旧秘密不再通过客户端认证；单个命令退出码不足以证明轮换成功。

LDAP 绑定密码与数据库密码轮换必须分别更新服务端和 LLNG 的凭据投影，保留可用回退配置。
数据库密码改变不会终止已经建立的连接；应使用新连接验证旧密码拒绝、新密码接受，再验证
真实应用登录和新会话写入。PostgreSQL 的测试连接必须命中密码认证规则（例如 SCRAM），
`trust` 下的登录成功无法证明密码正确。隔离验收使用专用绑定账户、克隆库和专用角色，
避免影响共享账号；这不代表已经提供 ANAS 统一服务凭据轮换命令。实际运维还须同步 workspace
Secret 与运行配置，并验证重建后仍使用新凭据。

Samba AD 的 `old password allowed period` 默认允许旧密码继续认证 60 分钟，不能把改密成功
等同于旧凭据立即退役。隔离 4.23.6 实测 LDAPS 简单绑定也接受宽限期内旧密码；验收中缩短
测试域窗口来观察真实到期；该基线仅重载配置未生效，重启测试 DC 后生效。生产轮换应等待
并验证实际窗口结束，或单独批准调整目录策略，
不得由 LLNG 静默修改全域设置。参见 [Samba 参数文档](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#OLDPASSWORDALLOWEDPERIOD)。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres`, `mariadb` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

Runner 为本 Module 创建专属数据库、用户和稳定生成凭据。修改 `db_type`/`db_name` 不会迁移现有数据。

## 环境变量所有权

### 导出

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### 显式消费

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`iam_test.go`](../hook/iam_test.go)
- [`lmConf.json`](../llng/root/root/lmConf.json)
- [`llng-config.sh`](../llng/root/root/llng-config.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

容器启动时在 LLNG Nginx 中启用 `real_ip_header X-Forwarded-For` 和递归解析，只信任 `TRAEFIK_HOSTNAME` 以及 Traefik 已校验的显式上游代理 IP/CIDR。这样登录历史、会话审计和基于 IP 的规则得到最左侧未受信任客户端地址，而不是 Docker bridge 地址；非法代理值会导致启动失败。

## 当前限制

不要配置已删除的 `LLNG_PASSWORD`；它不会创建上游管理员。

## 受信 OIDC 应用角色来源

通用 OIDC ATTRIBUTES 的保留来源 `anasRole` 使用 `inGroup` 宏从受信管理员组计算 admin/user，
并删除同名 LDAP exported variable，防止用户属性冒充角色。其他普通目录 claims 仍按原逻辑导出。

## 应用目录

人员入口在 module.yml 的 applications 声明，包含中英文文案、稳定 URL 来源、分类与权限事实。
Runner 生成七档 PNG 和 deployment 目录制品，只有显式消费目录的 IAM Module 获得共享文件。
默认 Portal 模板只渲染一层 category；原生组名采用“用户应用 / 主分类”“后台管理 / 主分类”，
应用直接放在该分类下，避免嵌套数据中的卡片被页面遗漏。完整两级布局属于第二阶段门户。
启动时移除上游默认的示例应用、管理和文档分组，避免未部署的示例与旧地址混入列表；
ANAS 的管理与文档入口仍按管理员组规则保留。
本地认证入口的目录可见规则不保护目标 URL。原生显示限制与真实会话验收见
[目录设计](/architecture/app-catalog-design)。

### PostgreSQL 会话驱动

PostgreSQL 会话后端显式依赖 `libdbd-pg-perl`。入口在启动 FastCGI 前通过标准 `PERL5OPT=-MDBD::Pg` 加载该驱动，并保留原有 Perl 启动选项；MariaDB 分支不增加此选项。这样驱动加载不进入认证请求的超时窗口，避免加载中断后常驻工作进程持续报重载失败。驱动不可用时启动失败，不回退文件会话、放宽认证或延长请求超时。此补正源于邮件集成中的实际冷启动失败；完整冷启动与浏览器重验见根邮件计划。

### 应用图标来源

应用入口的512px PNG由 `applications.icons.source` 声明。`assets/icon-source.json` 保存官方出处、原件摘要、是否重绘及生成输入；官方原件一同保留。`node scripts/application-themes.mjs --check` 校验来源与原始/简约两套包；修改矢量后用 `--render` 重新生成，不在运行时加载SVG或下载外部图片。全站主题沿用共享应用目录，个人主题只影响门户。
