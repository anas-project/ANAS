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
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | 否 | 否 | 否 | 是 | `container_recreate` | 私有审计容量；满额关闭写入 |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | 否 | 否 | 否 | 是 | `container_recreate` | 完整委派规则 JSON 数组；默认关闭 |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 门户域名前缀 |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属单成员写绑定 DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属成员写绑定凭据，不交付浏览器 |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 固定 OIDC 接入 |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 默认中英文选择，不支持时英文回退 |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | 否 | 否 | 否 | 是 | `container_recreate` | 门户专属密码/解锁写绑定 DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | 否 | 否 | 是 | 是 | `container_recreate` | 专属密码写绑定凭据，不交付浏览器 |
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

服务端 LDAP 动作是固定 unicodePwd change/reset、pwdLastSet、lockoutTime 与单个 member add/delete。以当前 anchor 找到 GUID 后进行稳定寻址；不接受任意属性、DN、完整 member replace 或命令。审计记录稳定引用和结果，没有密码/token 字段；意图先 fsync，网络未知不重放，结果存储失败关闭后续写入。

## 存储、备份与升级

私有 audit.jsonl 属于 workspace data，必须随 data 备份。会话只在内存，恢复不保留会话。未完成意图读取为 unknown；损坏日志关闭写入而不删除原文件。单实例持有文件锁，不新增数据库、缓存或同步守护进程。

## 验证

`go test ./modules/user_portal/portal ./modules/user_portal/hook` 覆盖 OIDC 时间/身份绑定、策略/AD 拒绝、CSRF、审计失败及不重放。关联 Casdoor Hook 和 appcatalog 一同验证。服务器与浏览器入口、固定制品和逐 ID 验收结论维护在配套门户计划，未执行不算通过。

## 当前限制

真实浏览器主要流程、LDAP TLS/ACL、写入未知与审计故障已通过；逐 ID 结论及剩余项见 Module 私有计划。跨对象事务不在能力范围；其他应用退出不由本门户推断。正式 Dockerfile 已构建，Linux/amd64 制品在 finance 完成 95 项运行检查及 41 项首版判据。下载加速保持 TLS 校验。临时 reset 密码只在成功响应返回一次。外部应用会话状态不由门户猜测。英语技术文档与中文说明等价。

账号锁定或密码状态无法确认时关闭普通访问；只读状态不得把未知显示为正常。UserInfo/get-account 的当前状态字段缺失亦拒绝。审计查询按一次当前人员范围快照过滤。

提交前审查修复了公开改密的身份限流响应差异和无效成员请求的审计身份遗漏。本地回归通过；历史 95/95 与 41/41 属于修复前制品，当前正式制品需重新构建并在 finance 复验。

账户 API 格式由 `ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA=casdoor-account/v1` 声明；门户按绑定契约检查，不按 IAM Module 名分支。它仍依赖现有原生本人 API，不扩展 API 权限。门户以 `no_prior_release` 登记首次发布基线，不宣称存在旧版本升级通过结果。

Go 依赖采用 coreos/go-oidc 验证 OIDC 与签名登出，go-ldap 提交受限目录请求；x/net/publicsuffix 校验门户与 IAM 的同站关系。共享目录的 x/image 用于生成七档 PNG。这些库复用协议实现，不增加运行服务。
