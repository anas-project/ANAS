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
| `anas_user_portal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-user-portal:0.1.0-r1` | `` | 3 |
<!-- generated:compose-topology:end -->

单个 Go 进程交付嵌入页面和 API，只有 Traefik 路由，无宿主发布端口。不挂 Docker socket。root 启动仅通过固定审计挂载的目录描述符初始化权限，再不可逆切换至 UID/GID 65532。应用 rootfs 只读。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.administration_enabled` | bool | — | `true` | `static` | `USER_PORTAL_ADMINISTRATION_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 启用受管 ANAS 管理员角色；实际写入仍要求专属 ACL 和绑定 |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | 否 | 否 | 否 | 是 | `container_recreate` | 私有审计容量；满额关闭写入 |
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
| `user_portal.recent_window_seconds` | int | `5..900` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | 否 | 否 | 否 | 是 | `container_recreate` | 近期认证窗口；默认 300 秒 |

参数库存以 module.yml 为准。规则默认关闭，写绑定不自动扩权。Runtime 使用显式 environment，不交付 IAM 注册或通用应用 secret。

源码构建复用仓库的 `DOCKER_HUB_REGISTRY`、`GOPROXY_URL` 与 `CHINESE_BUILD_SPEEDUP`/`APK_MIRROR_URL`。`GO_MODULE_PROXY` 可单独覆盖 Go 代理；默认仍为官方地址。加速参数只影响下载来源，不关闭 TLS 校验。

## Hook 与 IAM

Hook 仅发布自己的 ANAS_IAM_CLIENT__USER_PORTAL__ 注册，PUBLIC_CLIENT 表示不向门户交付应用 API secret 的 PKCE 客户端。REAUTHENTICATION_CLIENT_ID/REAUTHENTICATION_REDIRECT_URI 由 Provider 生成单独原生应用，关闭自动登录与 signin session；近期窗口仍按签名 auth_time 校验，不能只依赖 max_age/prompt。没有新增 Casdoor 上游补丁。

## 数据流与安全

内存会话和一次性 state/nonce/verifier 有容量及 TTL。Cookie 为 Secure、HttpOnly、SameSite=Lax、host-only。账号 POST 必须匹配 Origin 和 CSRF；只有精确 Traefik peer 可到业务 API。LDAP TLS 校验 CA 和服务器名；失败不降级。

应用 API 复用 internal/appcatalog 的语言和 BuildView，固定策略必须启用、Approved、Allow、匹配 model/resource/Read/组集合，并与当前 AD 组交叉确认。enforce 的默认允许和管理员绕过不用于门户。图标读取使用 no-link reader 及内容摘要，授权先于文件读取。

服务端 LDAP 动作限定为三个姓名字段、unicodePwd change/reset、pwdLastSet、lockoutTime 与单个 member add/delete。以当前 anchor 找到 GUID 后进行稳定寻址；不接受任意属性、DN、完整 member replace 或命令。审计记录稳定引用和结果，没有密码/token 字段；意图先 fsync，网络未知不重放，结果存储失败关闭后续写入。


### 资料与 ANAS 管理员管理

个人资料允许修改名、姓、显示名；邮箱、登录名、锚点只读，未接邮件模块不提供别名写入。资料在写入前逐字复核版本及旧值，门户内串行写入；同一 LDAP Modify 检查旧属性值。AD 的姓名匹配忽略大小写，因此不能承诺与外部工具之间的全局事务。用户应用分类开关记住当前浏览器偏好；密码/资料结果显示在表单旁，资料保存后立即刷新当前页的姓名摘要。未支持语言仍回退英文。

`administration_enabled` 使用目录 Provider 注册的管理员组并绑定稳定标识，不以 Casdoor 原生管理员或名称前缀授权。默认管理列表只展示明确注册的 ANAS 组，按应用、能力、文件资源和角色分类；高级视图说明系统权限风险。不能重置本人密码、移出自身管理员路径或移除最后一个可确认的可用管理员。计算机、服务和信任主体不进入人员管理。

启用写入前，部署者在选定 Samba DC 内运行 `tools/provision-directory.py`，以私有标准输入提供确认串、实际 base_dn 和 profile/reset/group 三个随机密码；确认串为 `provision-user-portal-scoped-writers`。脚本创建 `svc_portal_profile`、`svc_portal_reset`、`svc_portal_group`，只增加特定属性/对象 ACL，不给服务账号域角色、不修改已有服务密码。随后将三个 DN/密码填入对应门户配置并重新部署。输入须保留在 0700 目录/0600 文件，不进入命令行、Git 或日志。该脚本是管理员运行的目录配置工具，不是门户 API。

姓名绑定只写三个姓名属性；密码绑定写普通人员的 reset/pwdLastSet/lockoutTime；成员绑定只写已注册且非域级组的 member。配置工具要求服务账号是普通用户、没有额外组且主组为 Domain Users，同时检查该主组的继承域权限；任一检查失败均不发放 ACL。初始配置显式拒绝密码绑定管理现有域级人员，判断包含直接、嵌套和 AD 主组权限；主组无法解析时停止配置。以后在门户外新增高权限人员或更改权限边界时，部署者须重新核对 ACL；目录 ACL 不能按 member 值限制目标，也不能代替门户每次动态权限检查。服务账号没有全域权限不代表凭据泄露没有风险。

资料写入成功后，即使后续读取失败，也保留已保存结果及请求编号，并提示重新加载；离开表单后迟到的资料响应不会恢复已退出的界面身份。

`domain_administration_enabled` 默认关闭；开启后受保护人员/系统组走独立确认：近期 OIDC、限时一次性参数确认、同一登录人员的本人 LDAPS 绑定、原生 AD ACL。密码只用于一个请求，不跨请求保存；不是 MFA 或进程隔离的保证。普通门户不长期持有全域管理凭据。组关系之外的自定义 ACL 权限仍以 AD 的最终拒绝/允许为准。

## 存储、备份与升级

私有 audit.jsonl 属于 workspace data，必须随 data 备份。会话只在内存，恢复不保留会话。未完成意图读取为 unknown；损坏日志关闭写入而不删除原文件。单实例持有文件锁，不新增数据库、缓存或同步守护进程。

## 验证

`go test ./modules/user_portal/portal ./modules/user_portal/hook` 覆盖 OIDC 时间/身份绑定、策略/AD 拒绝、CSRF、审计失败及不重放。关联 Casdoor Hook 和 appcatalog 一同验证。服务器与浏览器入口、固定制品和逐 ID 验收结论维护在配套门户计划，未执行不算通过。

## 当前限制

历史首版基线的真实浏览器主要流程、LDAP TLS/ACL、写入未知与审计故障已通过；本轮扩展的验证状态以配套计划为准。逐 ID 结论及剩余项见 Module 私有计划。跨对象事务不在能力范围；其他应用退出不由本门户推断。正式 Dockerfile 已构建，Linux/amd64 制品在 finance 完成 95 项运行检查及 41 项首版判据。下载加速保持 TLS 校验。临时 reset 密码只在成功响应返回一次。外部应用会话状态不由门户猜测。英语技术文档与中文说明等价。

账号锁定或密码状态无法确认时关闭普通访问；只读状态不得把未知显示为正常。UserInfo/get-account 的当前状态字段缺失亦拒绝。审计查询按一次当前人员范围快照过滤。

提交前审查修复了公开改密的身份限流响应差异和无效成员请求的审计身份遗漏。本地回归通过；历史 95/95 与 41/41 属于修复前制品，当前正式制品需重新构建并在 finance 复验。

账户 API 格式由 `ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA=casdoor-account/v1` 声明；门户按绑定契约检查，不按 IAM Module 名分支。它仍依赖现有原生本人 API，不扩展 API 权限。门户以 `no_prior_release` 登记首次发布基线，不宣称存在旧版本升级通过结果。

Go 依赖采用 coreos/go-oidc 验证 OIDC 与签名登出，go-ldap 提交受限目录请求；x/net/publicsuffix 校验门户与 IAM 的同站关系。共享目录的 x/image 用于生成七档 PNG。这些库复用协议实现，不增加运行服务。
