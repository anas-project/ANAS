# 用户门户技术实现

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `0.1.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

原生 OIDC binding 负责发行者；Casdoor 原生 API 用于本人事实读取，Samba AD 用于当前状态、组和密码。首版限定 Casdoor，没有 SAML 或数据库。

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_user_portal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-user-portal:0.1.0-r1` | `` | 4 |
<!-- generated:compose-topology:end -->

单个 Go 进程交付嵌入页面和 API，只有 Traefik 路由，无宿主发布端口。不挂 Docker socket。root 启动仅通过固定审计挂载的目录描述符初始化权限，再不可逆切换至 UID/GID 65532。应用 rootfs 只读。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.administration_enabled` | bool | — | `true` | `static` | `USER_PORTAL_ADMINISTRATION_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 启用受管 ANAS 管理员角色；实际写入仍要求专属 ACL 和绑定 |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | 否 | 否 | 否 | 是 | `container_recreate` | 私有审计容量；满额关闭写入 |
| `user_portal.create_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 在 Provider 用户 OU 创建普通人员的指定授权绑定 DN |
| `user_portal.create_bind_password` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 创建绑定密码；不交付浏览器 |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | 否 | 否 | 否 | 是 | `container_recreate` | 完整委派规则 JSON 数组；默认关闭 |
| `user_portal.domain_administration_enabled` | bool | — | `false` | `static` | `USER_PORTAL_DOMAIN_ADMINISTRATION_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 显式启用本人域权限和逐次凭据验证；默认关闭 |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 门户域名前缀 |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属单成员写绑定 DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属成员写绑定凭据，不交付浏览器 |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 固定 OIDC 接入 |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 默认中英文选择，不支持时英文回退 |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属密码/解锁写绑定 DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属密码写绑定凭据，不交付浏览器 |
| `user_portal.profile_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 仅具 givenName、sn、displayName 写权的专属绑定 DN |
| `user_portal.profile_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 资料专属写绑定密码；不交付浏览器 |
| `user_portal.protected_groups` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_GROUPS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护分组的 anchor JSON 数组 |
| `user_portal.protected_users` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_USERS` | 否 | 否 | 否 | 是 | `container_recreate` | 额外保护人员的 anchor JSON 数组 |
| `user_portal.recent_window_seconds` | int | `5..300` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | 否 | 否 | 否 | 是 | `container_recreate` | 近期认证窗口；默认 300 秒 |

参数库存以 module.yml 为准。规则默认关闭，写绑定不自动扩权。Runtime 使用显式 environment，不交付 IAM 注册或通用应用 secret。

源码构建复用仓库的 `DOCKER_HUB_REGISTRY`、`GOPROXY_URL` 与 `CHINESE_BUILD_SPEEDUP`/`APK_MIRROR_URL`。`GO_MODULE_PROXY` 可单独覆盖 Go 代理；默认仍为官方地址。加速参数只影响下载来源，不关闭 TLS 校验。

## Hook 与 IAM

Hook 仅发布自己的 ANAS_IAM_CLIENT__USER_PORTAL__ 注册，PUBLIC_CLIENT 表示不向门户交付应用 API secret 的 PKCE 客户端。REAUTHENTICATION_CLIENT_ID/REAUTHENTICATION_REDIRECT_URI 由 Provider 生成单独原生应用，关闭自动登录与 signin session；近期窗口仍按签名 auth_time 校验，不能只依赖 max_age/prompt。没有新增 Casdoor 上游补丁。

## 数据流与安全

内存会话和一次性 state/nonce/verifier 有容量及 TTL。Cookie 为 Secure、HttpOnly、SameSite=Lax、host-only。账号 POST 必须匹配 Origin 和 CSRF；只有精确 Traefik peer 可到业务 API。LDAP TLS 校验 CA 和服务器名；失败不降级。

应用 API 复用 internal/appcatalog 的语言和 BuildView，固定策略必须启用、Approved、Allow、匹配 model/resource/Read/组集合，并与当前 AD 组交叉确认。enforce 的默认允许和管理员绕过不用于门户。图标读取使用 no-link reader 及内容摘要，授权先于文件读取。

服务端 LDAP 动作限定为三个姓名字段、unicodePwd change/reset、pwdLastSet、lockoutTime 与单个 member add/delete。以当前 anchor 找到 GUID 后进行稳定寻址；不接受任意属性、DN、完整 member replace 或命令。审计记录稳定引用和结果，没有密码/token 字段；意图先 fsync，网络未知不重放，结果存储失败关闭后续写入。


### 资料与 ANAS 管理员管理

个人资料允许修改名、姓、显示名；邮箱、登录名、锚点只读，未接邮件模块不提供别名写入。资料在写入前逐字复核版本及旧值，门户内串行写入；同一 LDAP Modify 检查旧属性值。AD 的姓名匹配忽略大小写，因此不能承诺与外部工具之间的全局事务。分类开关同时控制用户应用和后台管理的小分类，保留两大组并记住当前浏览器偏好；密码/资料结果显示在表单旁，资料保存后立即刷新当前页的姓名摘要。未支持语言仍回退英文。

`administration_enabled` 使用目录 Provider 注册的管理员组并绑定稳定标识，不以 Casdoor 原生管理员或名称前缀授权。默认管理列表只展示明确注册的 ANAS 组，按应用、能力、文件资源和角色分类；高级视图说明系统权限风险。不能重置本人密码、移出自身管理员路径或移除最后一个可确认的可用管理员。计算机、服务和信任主体不进入人员管理。

启用写入前，部署者在选定 Samba DC 内运行 `tools/provision-directory.py`，以私有标准输入提供确认串、实际 base_dn 和 profile/reset/group/create 四个随机密码；确认串为 `provision-user-portal-scoped-writers`。脚本创建 `svc_portal_profile`、`svc_portal_reset`、`svc_portal_group`、`svc_portal_create`，只增加特定属性/对象 ACL，不给服务账号域角色、不修改已有服务密码。随后将四个 DN/密码填入对应门户配置并重新部署。输入须保留在 0700 目录/0600 文件，不进入命令行、Git 或日志。该脚本是管理员运行的目录配置工具，不是门户 API。

姓名绑定只写三个姓名属性；密码绑定写普通人员的 reset/pwdLastSet/lockoutTime；成员绑定只写已注册且非域级组的 member。配置工具要求服务账号是普通用户、没有额外组且主组为 Domain Users，同时检查该主组的继承域权限；任一检查失败均不发放 ACL。初始配置显式拒绝密码绑定管理现有域级人员，判断包含直接、嵌套和 AD 主组权限；主组无法解析时停止配置。以后在门户外新增高权限人员或更改权限边界时，部署者须重新核对 ACL；目录 ACL 不能按 member 值限制目标，也不能代替门户每次动态权限检查。服务账号没有全域权限不代表凭据泄露没有风险。

资料写入成功后，即使后续读取失败，也保留已保存结果及请求编号，并提示重新加载；离开表单后迟到的资料响应不会恢复已退出的界面身份。

`domain_administration_enabled` 默认关闭；开启后受保护人员/系统组走独立确认：近期 OIDC、限时一次性参数确认、同一登录人员的本人 LDAPS 绑定、原生 AD ACL。密码只用于一个请求，不跨请求保存；不是 MFA 或进程隔离的保证。推荐的专用受限绑定配置不长期持有全域管理凭据。组关系之外的自定义 ACL 权限仍以 AD 的最终拒绝/允许为准。

## 界面、语言与账号偏好

前端是无构建步骤的原生 JavaScript：`theme.js` 在样式之前同步执行，先套用本浏览器上次的主题；`i18n.js` 保存 zh-CN、zh-TW、en、ja、ko、fr、de、es 八种界面文字；`app.js` 按 `#/apps`、`#/account/<profile|security|groups|mail|log>`、`#/admin/users[/<anchor>|/new]`、`#/admin/audit` 渲染页面，未知地址回到 `#/apps`。全部文本以 textContent 写入，CSP 仍只允许同源脚本和样式。语言匹配与目录文本回退由 internal/appcatalog 统一完成：繁体地区归 zh-TW 并先回退简体中文，其他新增语言回退英文；目录内容仍只要求中英文完整。

主题有跟随系统、浅色、深色三种，颜色全部是 CSS 变量，跟随系统时由 `prefers-color-scheme` 实时切换。工作台与管理模式分开；只有拥有人员查询动作的用户看到“管理”，管理侧栏列出其有权访问的后台应用。⌘K / Ctrl+K 打开快速搜索：应用来自当前授权目录，页面按权限列出，人员结果只调用现有 `/api/users`。写操作只展示可确认的结果，改密成功提示用新密码登录，保留失败和部分结果，不展示未跟踪的IAM或会话状态；重置密码与解锁位于危险操作区，确认与本人验证不变。

账号偏好（语言、主题、分类显示、最多 24 个常用应用）通过 `POST /api/me/preferences` 保存，`GET /api/me` 的 `preferences` 返回。写入要求当前会话与 CSRF，字段和取值使用白名单，不要求近期认证、不写安全审计。常用列表在返回时按当前授权过滤，保存时拒绝未授权名称。未登录页面只用本浏览器缓存；登录后账号偏好覆盖并更新缓存。

## 存储、备份与升级

私有 audit.jsonl 和 `preferences/preferences.json` 属于 workspace data，必须随 data 备份。偏好目录由启动阶段以 0700、uid 65532 创建；文件以临时文件加原子改名写入，读不出或内容损坏时使用默认偏好并拒绝写入，不覆盖原文件。会话只在内存，恢复不保留会话。未完成意图读取为 unknown；损坏日志关闭写入而不删除原文件。单实例持有文件锁，不新增数据库、缓存或同步守护进程。

## 验证

`go test ./modules/user_portal/portal ./modules/user_portal/hook` 覆盖 OIDC 时间/身份绑定、策略/AD 拒绝、CSRF、审计失败及不重放。关联 Casdoor Hook 和 appcatalog 一同验证。界面用例 `node test-env/playwright/user-portal-ui.test.mjs` 与 `node test-env/playwright/user-portal-experience.test.mjs` 使用生产静态资源和模拟 API。服务器与浏览器入口、固定制品和逐 ID 验收结论维护在配套门户计划，未执行不算通过。

## 当前限制

历史首版基线的真实浏览器主要流程、LDAP TLS/ACL、写入未知与审计故障已通过；本轮扩展的验证状态以配套计划为准。逐 ID 结论及剩余项见 Module 私有计划。跨对象事务不在能力范围；其他应用退出不由本门户推断。正式 Dockerfile 已构建，Linux/amd64 制品在 finance 完成 95 项运行检查及 41 项首版判据。下载加速保持 TLS 校验。临时 reset 密码只在成功响应返回一次。外部应用会话状态不由门户猜测。英语技术文档与中文说明等价。

账号锁定或密码状态无法确认时关闭普通访问；只读状态不得把未知显示为正常。UserInfo/get-account 的当前状态字段缺失亦拒绝。审计查询按一次当前人员范围快照过滤。

提交前审查修复了公开改密的身份限流响应差异和无效成员请求的审计身份遗漏。本地回归通过；历史 95/95 与 41/41 属于修复前制品，当前正式制品需重新构建并在 finance 复验。

账户 API 格式由 `ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA=casdoor-account/v1` 声明；门户按绑定契约检查，不按 IAM Module 名分支。它仍依赖现有原生本人 API，不扩展 API 权限。门户以 `no_prior_release` 登记首次发布基线，不宣称存在旧版本升级通过结果。

Go 依赖采用 coreos/go-oidc 验证 OIDC 与签名登出，go-ldap 提交受限目录请求；x/net/publicsuffix 校验门户与 IAM 的同站关系。共享目录的 x/image 用于生成七档 PNG。这些库复用协议实现，不增加运行服务。

### 系统主邮箱与建号

`samba_dc.mail_domain` 缺省继承 AD DNS 域。例如 finance.hlong.wang 的 admin 获得 `admin@finance.hlong.wang`；设置为 hlong.wang 后，指定人员 OU 的主地址按 `用户名@hlong.wang` 维护。它不改变 AD 域、UPN、登录名或锚点。`mail` 完全由系统管理，页面不接受手工主地址；地址存在不代表邮件服务已经开通。

人员枚举、详情、姓名、密码、分组及域级操作均以 Provider 的用户 OU 及子 OU 为边界。ANAS 管理员可用“新建用户”创建普通人员；后端使用 `create_bind_dn/password` 指定的授权绑定，而不是使用前台操作者的域权限。推荐独立 `svc_portal_create`，仅授予用户 OU 的 user 创建权限。配置账号也可已有相应 AD 授权；门户角色和后端目录授权分别检查。若指定域管理员作为创建绑定，门户进程会长期持有该账号既有权限，逐次域级验证并不能隔离这份凭据；默认专用受限绑定可避免这一点。

`POST /api/users` 只接收 `username`、三项 `profile`、`force_change`。登录名限 20 位 ASCII 字母、数字、下划线、点、连字符，并须能组成系统邮箱。邮箱、UPN 后缀、目标 DN、初始随机密码和锚点由服务器决定，一次原生 LDAP Add 写入；不授予管理组。该动作需要近期认证、CSRF、当前管理员权限和持久审计；冲突或结果未知不得自动重试。临时密码只在成功响应显示一次，默认要求下次修改。

建号授权边界：finance Samba 4.23.6 的专用创建者同时成为新用户对象所有者，能修改该对象的 DACL（访问控制表）；临时原生 Owner Rights 规则未移除该能力。因此仅配置子对象创建 ACL 不能证明与后续高权限账号严格隔离。门户在加入域风险组前执行下一节的所有者移交并核对结果；默认创建绑定不应授予额外域权限。没有向正式 OU 添加全局 Owner Rights 规则，也没有新增 Casdoor 补丁。


### 域风险组的对象所有权

向域风险组加人时，本人域绑定先将用户对象 owner 移交到同域 Domain Admins，以原生 critical SD Flags control 仅写 owner 并回读，再添加 member。所有权不确定时不加组；成员操作失败保留新 owner。API 和审计分别提供 ownership、membership，页面确认说明两步及部分成功。没有跨对象事务，外部工具并发修改仍须核实。指定创建绑定若本身已有广泛域权限，移交 owner 不会撤销它的其他 ACL/组授权。


## 分组共享清单与页内确认

分组信息由各 Module 的 `identity.groups` 声明。Core 在全部 calculate 完成后生成本部署唯一的 `shared/identity-groups/catalog.json`，Samba 与门户通过 `identity.group_catalog: true` 只读挂载到 `/run/anas/identity-groups`。应用入口组沿用既有 `identity.application_group` 和应用元数据；能力组从声明模块发布的列表按所属前缀选取。Samba 的通用组和 AD 内置组介绍在其 manifest 维护。旧 CSV 为清单投影，门户不再猜测 APP_/CAP_ 前缀类别。

默认只列出绑定到实际目录对象的受管组，按应用、能力、文件资源和角色展示名称及介绍。系统和其他分组置于页面底部的“高级”区域，默认折叠；未知组使用 AD 的现有介绍，没有则明确标注。中文/英文随页面语言，未支持语言回退英文。介绍是展示信息，域风险仍根据当前 SID、嵌套关系和 AD 权限计算。

页面宽度不超过 600 像素时，每组名称和介绍在上方，成员状态与操作在下一行；状态和按钮文字保持单行，较长组名在可用宽度内换行。

普通受管组（不含 ANAS 管理员角色组）添加直接执行；移除成员及建号、管理员资料修改、重置/解锁在页内对话框核对动作、目标和参数。系统组添加必须页内确认；受管域风险组同样保留本人域凭据及一次性确认。页面及写请求不提供操作理由或备注；写API拒绝已移除的 `reason` 字段。审计仍记录动作、对象、授权和结果，历史记录保留。取消/关闭不执行写入，不使用原生浏览器 alert/confirm/prompt。

ANAS 管理员角色组（含嵌套继承该角色的组）的加入和移出需要 5 分钟内真实认证、页内确认和限时单次参数确认，无需重复输入密码；该角色可管理全部人员和应用，在 anasd 启用 `trusted_proxy` 时还映射为控制台 owner。确认后仍由门户受限分组绑定写成员，不要求开启域级管理，审计规则为 `platform_admin_confirmed`。

域确认仅准备一次，绑定当前身份、完整参数和目录GUID的一次性 `proof`；目录对象被替换、改参、过期或重放均由服务端拒绝。

语言和分类下拉框使用原生select，统一文本内边距和箭头位置，支持键盘与窄屏；不增加自定义弹出菜单。

普通用户操作和 ANAS 管理员角色变更可复用 5 分钟内真实认证；角色变更仍须页内一次性确认，无需重复密码。系统/操作系统权限组及受保护账号操作保留每次本人域密码和原生 AD 权限验证。本人改密仍需原密码。退出结果页提供“返回主页”，IAM 失败保留重试且不报告完整成功。跨应用退出的实测范围及限制见配套计划 §19。


### 当前浏览器的退出清理

门户退出在删除本地会话后，先调用原生 IAM 退出，再通过已部署应用域名的固定 `/.well-known/anas/browser-logout` 路径清理 Nextcloud、Immich 的旧浏览器会话。该路径由同一个门户进程服务，限定原生模块来源、HTTPS 同站点及端口，使用短期签名请求和严格的 origin/窗口/nonce 消息校验；不接受外部条目提供任意退出脚本。

应用目录仍严格按权限过滤。用户确认的独立协议例外仅含域名、固定路径和仅可退出的随机凭证，覆盖已隐藏或不再授权应用的旧会话。Iframe 在应用 origin 内使用原生 Cookie 和退出 API，只返回退出状态；5xx、超时和仍有效身份都为未确认。部分失败可在刷新匿名 CSRF 会话后重试，始终提供返回主页；其他设备不按用户名撤销。实机覆盖范围和原生应用发起来源接线进度以配套计划为准。


用户已确认固定原生返回页 `/signed-out?native=1` 可在匿名状态读取同一份最小退出协议；业务目录与图标 API 不变。匿名普通首页不发布应用域名，固定退出页可在重载后刷新匿名 CSRF 并重试。跨站点导航或没有 Fetch Metadata 时需点击页内继续，同站点原生跳转自动继续并使用匿名 CSRF 会话。

重载退出结果页时，若 bootstrap 已确认新的门户登录，页面清除旧退出结果并返回应用页；匿名会话仍保留部分失败结果和重试入口。缓存不能覆盖当前认证状态。

原生 IAM 后台退出通知若在 bootstrap 与退出请求之间撤销门户会话，前端仅在退出 API 明确返回 403 `csrf_rejected`（尚未执行退出）时，重新获取 CSRF 并重试一次。重复拒绝、5xx 或网络未知不自动重放；该恢复不适用于资料、密码、建号或分组写入。

### 应用图标主题

`theme` 继续表示系统/浅色/深色；账号偏好的 `icon_theme` 独立选择图标主题，空值跟随全站默认。内置 `original` 保留原图，`soft` 是简约几何风格。个人设置不修改 Casdoor。全站默认从 `application_catalog.icon_theme` 冻结到现有共享目录，Casdoor 继续使用原生 Application.logo。

当前 ANAS 管理员可在 `#/admin/themes` 导入和导出数据主题包，导入要求 CSRF 与近期认证；导入后全站可选，不自动改变任何偏好或执行 apply。协议为 `anas.application-theme/v1`，仅接受名称、允许的十六进制配色、PNG 字节及出处；拒绝任意 CSS/脚本/SVG/外链图片和未知字段。每图512–2048px正方形、最多2MiB；每包最多32MiB、128图，私有库最多8个导入主题且总计32MiB。正文与背景等文字配对要求至少4.5:1对比度。

`GET /api/themes` 只返回主题显示信息，不含应用全集。`GET /api/assets/{name}/{size}?theme=ID` 先验证所属应用当前权限再读取主题图；缺图保留原图，失效选择回退全站默认并在设置页提示。`GET /api/themes/{id}/export` 只允许管理员读取完整包；配置冻结而不在可写库中的全站主题不提供下载。`themes/*.json` 与偏好一样保存在私有数据卷，0700目录/0600文件，原子写入，重启读取；同名不同内容拒绝覆盖，损坏文件保留但不列为可用。

图标来源、共享制品及管理员设置方式见[主题指南](/guide/application-themes)。新增验证入口为 `node scripts/application-themes.mjs --check`、`go test ./internal/apptheme ./internal/appcatalog ./modules/user_portal/portal ./modules/casdoor/hook` 和 `node --test test-env/playwright/application-themes.test.mjs`；真实主机结论仍以跨Module配套计划为准。

### 原生 IAM 登录外观与改密入口

从门户点击登录会将当时的明暗模式传为 Casdoor 原生 `theme=dark/default`；系统模式在点击时解析，已打开的 IAM 标签页不会实时跟随。门户以 `hidden: true` 的专属品牌声明给 IAM 登录及再次验证页提供512px圆角logo，不增加可见自链或准入组。
Casdoor 的 ANAS 目录登录页提供“首次／到期改密 · Set new password”，通过原生 `forgetUrl` 跳到门户 `/#/recover`。该页验证旧/临时密码后写入 AD；本地恢复管理员保留 Casdoor 原生找回页面。Samba强制改密会先拒绝LDAP登录，首期不承诺IAM自动弹出改密、不新增Casdoor补丁或目录授权。
