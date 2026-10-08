# Immich 技术实现

用户操作见[README](../README.md)。本页描述已落地代码；真实主机未验收的结论保持开放。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `3.2.4-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_immich` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-immich:3.2.4-r1` | `db, traefik, immich` | 3 |
| `anas_immich_machine_learning` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-immich-machine-learning:3.2.4` | `immich` | 1 |
| `anas_immich_valkey` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-immich-valkey:9.1.1` | `immich` | 1 |
<!-- generated:compose-topology:end -->

server 只在 Traefik 网络提供 2283，不发布宿主端口；同时加入 Runner 解析的共享 PG network。
专属 Valkey 与 ML 服务只在模块私有 network，未发布端口。Valkey 启用 AOF/everysec，配置和队列
保存为独立 data 子目录；网络成员与 Docker 管理权限仍是该队列的信任边界。ML 不接收 server 的
数据库/OIDC Secret。关闭 ML 由 services Hook 禁用服务并同步应用 enabled=false。

Valkey 健康探针执行 `valkey-cli -e SET anas:healthcheck 1 EX 30`，验证可写性并把服务端错误转为非零退出。
真实 4MiB 临时文件系统上的 AOF ENOSPC 已证明普通 `valkey-cli ping` 会输出 MISCONF 却退出 0；
新探针退出 1，Docker 状态转为 unhealthy。探针键有短 TTL，不扫描业务队列。

## 固定版本和入口保护

Server 基于上游 `v3.2.4` 多架构摘要 `sha256:d317916b28090c33eb36b308464ea391f8b7df1d850fcfea227a39ec879718c2`。
ML mirror 固定 `v3.2.4` 摘要 `sha256:e16c2f166a8174901959fdf85e2e4c7bd1ebc4b37e0b6655de97c41408a260c4`。
Valkey mirror 固定上游 compose 的摘要 `sha256:70739f85ad2ee01a726a965584a0f94895f01b0c60b3cc8b0aeef11eaa6888cf`，
该摘要实测 binary 为 9.1.1。固定来源和发布镜像由仓库 image/mirror 清单管理，运行时不安装依赖。

上游 v3.2.4 的 passwordLogin.enabled 只保护密码 login。固定镜像的构建脚本
[`enforce-oidc-only.cjs`](../immich/enforce-oidc-only.cjs) 为编译服务增加精确拒绝 guard：
BaseService.createUser 必须具有非空 oauthId 且不携带密码；AuthService.changePassword/link/unlink
和 AuthAdminService.unlinkAll 拒绝；UserService.updateMe 与 UserAdminService.update 拒绝密码字段。
这同时保护管理员 create 和 setup 的公共建号路径。DTO 不接受 oauthId 更新，未改写 callback、内部 id、
资产主外键或目录框架。所有锚点先验证后写入，未知/重复/缺失锚点或重复补丁导致构建失败。
来源：[AuthService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth.service.ts)、
[AuthAdminService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth-admin.service.ts)、
[BaseService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/base.service.ts)。

真实 PG/签名 OIDC fixture 在未补唯一约束的固定镜像中复现：8 个并发首次 callback 使用同一 sub、
不同邮箱，会创建 8 条相同 oauthId。构建补丁因此在原生数据库 migration 锁内、HTTP 启动前增加
`user_oauthId_uq UNIQUE(oauthId)`，同时更新固定 schema 元数据。约束保留软删除行的 anchor；同 sub
软删除后注册失败，不能创建新的绑定。重复启动验证现有约束定义；重复数据或不符定义使启动失败，
不自动合并或改绑身份。约束由应用数据库普通所有者创建，无超级用户权限。
并发首次回调验收要求至少一个成功、只有一个有效同 sub 账号，所有成功响应命中同一内部 id，
失败响应不携带 accessToken；唯一约束竞争可能返回上游 400/500，不能宣称所有并发请求均成功。

真实队列 ENOSPC 上传测试复现上游先排队文件清理、后回滚新增 asset 的异常路径：清理入队同样失败时，
HTTP 返回 500 但遗留 checksum 行，健康重试会被误作重复上传。固定补丁
[`fix-upload-queue-rollback.cjs`](../immich/fix-upload-queue-rollback.cjs) 只把既有新增 asset 删除移到清理入队前。
真实 HTTP 已验证失败不留数据库 asset，队列恢复后重试完成原件与缩略图；已有重复资产不会被删。
故障期间无法排队清理的未引用临时文件仍可能存在，不声明额外的文件回收机制。

## 身份与配置

calculate 稳定生成 `IMMICH_OIDC_CLIENT_SECRET`，发布通用 OIDC registration，显式请求
sub:目录 anchor 与 `anas_role:anasRole`。受信 Admins 映射在 IAM Provider 完成；首普通登录由原生
BaseService 拒绝，首管理员由原生 OAuth roleClaim 建号。要求 email、name，保留内部 user.id。
所有接入 Immich 的目录用户，包括首管理员，都须有有效的 `mail`；上游原生回调会拒绝缺少
email 的资料并保持空库。`global.email` 是服务联系邮箱，不会自动填入目录管理员的 `mail`。
部署前由目录管理员确认并填写账号邮箱，随后由既有目录同步交付；不以本地建号绕过此项。
render 只读取本消费者 binding，生成带 Secret 的私有 config.json，不能从其他 IAM binding 或
Provider 管理凭据取值。文件固定挂载，IMMICH_CONFIG_FILE 使上游配置更新 API 拒绝运行中改写。

原生 OAuthRepository 验证 Authorization Code、state、PKCE/issuer/signature/audience 并拒绝空 sub。
同 sub 优先命中，email 仅在 sub 不存在时用于冲突检查。入口保护保证正常安装不能创建未绑定用户；
数据库唯一约束补丁使并发首次和软删除后重登保持唯一绑定；这些场景已通过真实 PG/签名 fixture，
真实 Linux/Btrfs AD/Authentik 与 AD/Casdoor 的首管理员和普通 JIT、目录锚点、邮箱变更/冲突、并发回调及软删除保护已通过；缺少所需受信角色或目录撤权能力的 Provider 组合拒绝启用。

回调包括 `/auth/login`、原生 `/user-settings` 和 `app.immich:///oauth-callback`，不开启手机重定向替换。
只读固定 config 包含 passwordLogin=false、oauth.autoRegister/autoLaunch=true、roleClaim=anas_role、
storageLabelClaim=""、backup.database.enabled=false 和队列并发。配置变更重建对应容器；数据库名/类型
变更标记 data_migrate，不在普通 apply 静默迁移数据。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `immich.db_name` | string | — | `immich` | `static` | `IMMICH_DB_NAME` | 否 | 否 | 否 | 否：`migrate-immich-database` | `data_migrate` | 应用独立数据库名；修改需匹配媒体迁移 |
| `immich.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `IMMICH_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-immich-database` | `data_migrate` | 仅共享 PostgreSQL |
| `immich.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `photos` | `static` | `IMMICH_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 公开照片服务子域名前缀 |
| `immich.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `IMMICH_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 仅 OIDC；自动选择仍只能解析到 OIDC |
| `immich.job_concurrency` | int | `1..16` | `2` | `static` | `IMMICH_JOB_CONCURRENCY` | 否 | 否 | 否 | 是 | `container_recreate` | 非视频后台队列的并发数 |
| `immich.machine_learning` | bool | — | `true` | `static` | `IMMICH_MACHINE_LEARNING` | 否 | 否 | 否 | 是 | `container_recreate` | 同时开启应用 ML 设置和专属 ML 服务 |
| `immich.video_concurrency` | int | `1..8` | `1` | `static` | `IMMICH_VIDEO_CONCURRENCY` | 否 | 否 | 否 | 是 | `container_recreate` | 视频转换队列并发数 |

ML 关闭后照片/视频上传仍保留，依赖 ML 的搜索/识别功能关闭。未完成整机压力测试，不声明支持 4GB。

## 登出与目录事件

固定补丁为原生 RP logout 同事务取得实际删除的父/子孙 session，并沿原 SessionDelete 路径逐凭据断连，然后使用 ID Token 构造 IAM end_session URL。
原生 `/api/oauth/backchannel-logout` 验证签名、issuer、audience、alg、最大年龄、event 和 nonce，
按 sid 或 sub 删除 OAuth session。原生 sid-less token 重放在实际 HTTP/PG 测试中撤销了重新登录的
合法会话。因此固定补丁在原生验证之后要求非空 jti，将已验证 issuer/client/jti 的唯一消费与会话
删除放入同一数据库事务，重复请求返回 400。小型 `anas_immich_logout_token` 表在原生 migration 锁内
创建，按数据库接收时刻保留 5 分钟，覆盖原生年龄验证最后一秒；事务内用数据库时钟再次检查
相对 iat 的接收年龄在 `[-5,126)` 秒，并按原生 JOSE 容差复核可选 exp，拒绝验签后排队至过期的请求。补丁也拒绝任何 nonce 字段
（包括空值）和非对象 backchannel event。处理新请求时清过期记录；
不增加维护服务或调度器。8 路并发仅首个可撤销，跨重启重放也保留新会话。Hook 复用通用
OIDC_LOGOUT 注册。API key/分享的独立验证不被普通或 backchannel logout 撤销。

目录准入丧失通过现有注册的 `OIDC_CAEP_EVENTS=session-revoked` 协商；Runner 检查 Provider 支持、
backchannel 与 subject-wide 语义，当前固定 Authentik `2026.5.6-r15` 支持，其他组合拒绝。
发送端复用 LDAP 全量/事件触发同步、删除事务和原生持久任务，使用目录 anchor 与共享 PG 检测时间，
不依赖仍存在的 AccessToken。受信 Admins 角色丧失在原生 membership/组更新和删除事务内立即持久入队；准入仍允许也撤销旧授权，下一次原生 callback 更新角色。普通 logout 不含策略成员。接收端验证签名、issuer/audience、sub_id
及 [CAEP session-revoked](https://openid.net/specs/openid-caep-1_0.html) 的 policy/事件时间，保留原生 OIDC
backchannel member；此选定事件扩展不声明完整 SSF 部署。

只按凭据 createdAt 清理会漏过通知投递前的旧会话派生 API key，以及通知后才完成的旧登录回调。
固定补丁 [`guard-credential-races.cjs`](../immich/guard-credential-races.cjs) 保留已验证 ID Token 的签发时间：
session、派生 session、API key/rotate 与分享均从真实来源继承 `anasOAuthIssuedAt`，创建和撤销使用同一
anchor advisory lock 和 user 行锁。`anas_immich_directory_revocation` 只保存最大截止值，覆盖尚未首次
建号的主体；同事务内消费 jti、推进截止并清除旧来源凭据。不删除 user/媒体或替换身份绑定。
恢复准入后的新 ID Token 必须晚于截止；iat 只有秒精度，同一秒的回调保守拒绝，下一秒可重试。
userinfo subject 必须与已验证 ID Token subject 一致，不能用 userinfo.iat 作为来源。原生 callback 更新既有用户 isAdmin 也在同锁内核对 cutoff，防止旧管理员回调先写角色再403。API key 认证时内部保留不可枚举的 hash 版本，锁内核对当前版本，防止旧请求借同ID轮换后的新来源；该值不进DTO/JSON。

[`fix-revoked-sockets.cjs`](../immich/fix-revoked-sockets.cjs) 在服务端按被删 session/API key 房间断连，
包含原生 parentId 级联后代，避免只发通知而依赖客户端退出；连接先加入凭据房间再复核认证，封住加入竞态。
成功 API key 同ID轮换在返回新secret前断开该canonical UUID房间，旧secret的已有连接不能借新row继续存活；新secret可重新连接。不踢整个 user room，保留独立新凭据及其他用户连接。无效 Logout Token 只记录固定错误，不记录原 JOSE
payload 或带原 payload 的 cause。真实 PG/HTTP/WebSocket run `f0961da23d` 已验证接收、来源和断连；
原生 Authentik 签名方法/同步方法验证通过，原生五种目录功能场景已分轮通过；自动事件传播已在真实主机验证，真实移动端仍是发布验收阻塞。

## 数据和 ANAS 生命周期

primary_database 请求 vector 与 earthdistance；cube 等依赖由 PG Provider 管理，应用普通角色。
DB_VECTOR_EXTENSION=pgvector 避免镜像扩展候选改变自动路线。Provider 确保已安装版本与默认版本相符，
否则应用原生启动中的扩展更新会失败，不能以超级用户权限绕过。

`data/immich/media` 是媒体与数据库耦合恢复集合的一部分；Valkey AOF 也进入 data。模型缓存可重新下载，
首版沿整 workspace 备份捕获。仅用 ANAS 停写/快照/复制/恢复，关闭应用数据库调度（固定版本使用 pg_dump，
不是 pg_dumpall）。共享 PG 的数据和媒体必须匹配；恢复会影响其他 Consumer 和恢复点后的上传。
本模块不实现单应用恢复、PG 大版本迁移、通用运行档位或 4GB 支持。只读外部图库源文件仍归源目录所有者，
须单独核对备份与撤权边界；无自动挂载参数。

## 验证入口与限制

- [`main_test.go`](../hook/main_test.go)：配置、资源映射、绑定缺失拒绝、secret 稳定、ML服务/队列设置。
- [`enforce-oidc-only.test.cjs`](../immich/enforce-oidc-only.test.cjs)：入口拒绝、正常 OIDC create、锚点反例。
- [`verify-image.cjs`](../immich/verify-image.cjs)：真实固定镜像编译服务的仓储替身回归；不计真实 IAM/PG e2e。
- [`container-e2e.sh`](../tests/container-e2e.sh)：临时网络/卷的真实 PG、签名 OIDC grant、HTTP 登录、
  入口限制、并发/软删除和媒体验收；服务器入口 `test-env/scripts/server-immich-e2e.sh` 额外限制测试 daemon。
- [私有需求](../dev-docs/requirements/immich-module.md)与[计划/记录](../dev-docs/plans/immich-module.md)。

`valkey-e2e.py` 读取实际 Compose 探针，在有界临时文件系统执行满盘反例。
`test-env/scripts/server-immich-workspace-e2e.sh` 执行真实 ANAS CLI 新装、重复 apply、重启及全 workspace
备份/恢复，使用 Samba AD/Authentik 和第二 PG 消费者；须调用者指定 Linux/Btrfs、独立 daemon/netns。

本机 arm64 镜像构建、编译服务回归、真实 PG 的并发/软删除保护、照片/视频原件 hash 和缩略图任务通过。
真实暂停的 metadataExtraction job 从 Valkey AOF 重启恢复，应用启动后原生 bootstrap 恢复处理并完成缩略图。
队列满盘、写失败回滚和健康重试也通过。fixture 使用测试签名 IdP，不证明真实 IAM、浏览器/移动端、
目录同步链路通过。真实Linux/Btrfs ANAS全workspace恢复、匹配旧/新镜像、维护失败和SIGKILL原冻结候选重试已通过；当前受管HTTP CA修正镜像组合也已完成匹配全workspace恢复核对。developing状态不构成剩余客户端验收放行。


2026-10-03 finance隔离主机的管理员/普通用户浏览器验收通过：固定原生OIDC、照片/视频上传及原件hash/缩略图、UI相册创建改名、RP和IAM主动退出、旧Bearer/Cookie401及再次认证。Playwright1.62.1配套Chromium151.0.7922.34在同主机临时容器运行，私有合成账号/媒体不导出。内部CA浏览器夹具不计ServiceWorker/PWA验收；真实手机上传/后台备份与会话仍待测试。当前受管HTTP CA镜像组合全workspace恢复已通过，核对受控损坏的媒体、两共享消费者、Valkey、配置/Secret原字节和精确images。原生停用/删除/准入丧失/管理员降权/先登出再撤权五种功能场景分轮通过，自动事件路径已通过：不手工同步，71.206秒内旧四类凭据401，达到本次健康固定组合300秒主机验收上限，已有旧/新匹配恢复证据保留。


自动事件验收核对新 `Modify/member` 事件seq99、监听持久cursor99、原生LDAP与签名通知DONE任务、接收端已验签截止以及旧Bearer/Cookie/API key/分享401，保留媒体/绑定和无关用户。300秒是本次健康固定组合的测试上限，不表示4GB、整机容量或故障传播保证；原生任务默认重试耗尽后需经既有任务入口处理。
