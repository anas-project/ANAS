# Casdoor 技术实现

本文面向 Module 维护者，记录 `casdoor` 的协议契约、安全边界和验证入口。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `3.143.0-r10` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r10` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r10` | `casdoor` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-casdoor-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀及所有 IAM 端点 |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | 否 | 否 | 否 | 是 | `container_recreate` | LDAP 自动同步周期（分钟） |

## 数据与启动流程

r9 将 `0400`、root 所有的冻结 `app.conf` 只读挂载到 `/opt/anas/conf/app.conf`。root 启动入口以 `umask 077` 复制到容器可写层的 `/conf/app.conf`，设置所有者 `1000:1000` 和权限 `0600`，之后才运行 bootstrap 和正式进程。复制或权限设置失败即退出；源制品与历史 deployment 的内容和权限保持不变。配置不是业务持久数据，每次容器启动均重新生成。

Hook 生成 Casdoor `app.conf` 和初始化数据模板。PostgreSQL DSN 显式包含 `dbname`。容器启动时 Helper 从 `0600` 临时投影读取恢复密码，生成 bcrypt 后的 `/tmp/init_data.json`，再以 UID/GID `1000` 启动上游进程。由于上游先初始化 LDAP 自动同步器、后导入 init data，entrypoint 会短暂启动一次以提交表结构与托管对象，再启动正式进程；entrypoint 删除容器内无意义的 `lsof` old-instance 探测和上次启动遗留的 init 文件，避免 bootstrap 子进程把自己杀死或因 UID 1000 文件权限失败。正式进程 HTTP 可用后再次投影并回读恢复管理员密码，只有成功后才创建 readiness marker；普通健康检查同时要求 marker，直接 Docker 重启因此也是自包含的。初始化数据为 built-in 恢复管理员显式开启特权确认，并给 `anas` 组织创建不可注册的内部目录 Application；PostgreSQL 是唯一受支持的数据接口。

r10 以 Casdoor `3.143.0` 对应提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 为源码输入，构建前校验归档 SHA-256 `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`，再顺序应用仓库内六个受控补丁：SAML 模板读取 `displayName/externalId`；OIDC 授权签发独立 `sid` 并记录父 Beego session，用户退出和管理员删 session 发出两分钟 Logout Token；delivery 失败记录不含凭据的原因；token 查询使用 XORM 字段条件，避免 PostgreSQL 把未引用的 `user` 解析为当前数据库用户。镜像仍以固定官方 `3.143.0` 运行时为基础；构建代理不改变提交与校验和。Go 构建 stage 固定在 `BUILDPLATFORM` 并用 BuildKit 的 `TARGETOS/TARGETARCH` 交叉编译，最终目标 stage 不执行 `RUN`；历史 r8 的 amd64 部署和 arm64 固定源码构建/非特权探针通过；r10 实机与完整双架构构建分别验收。

## 受管凭据生命周期

`CASDOOR_SIGNING_MATERIAL` 是含当前 RSA 私钥、证书和限时旧证书的 Secret JSON bundle。部署清单只冻结
Secret 投影位置和公开证书投影位置，不记录值；候选部署同步更新 `CASDOOR_SIGNING_CERT`。Helper 以证书
SHA-256 指纹命名当前 Casdoor Cert，并把 Application 引用原子切换到新名称。轮换后一小时内保留旧
JWKS key；从 r7 升级时还保留旧 `anas-signing` alias，直到其证书退出信任窗口。

`CASDOOR_PORTAL_CLIENT_SECRET` 通过同一 credential transaction 更新 built-in Portal Application。
probe/reconcile/verify 都从 stdin 读取候选，错误不包含值；候选启动、应用回读和健康验证全部通过后才
提交 Secret Store。失败时恢复上一 deployment、数据库值和 Store generation。

## LDAP、目录事件与权威边界

LDAP 连接固定使用受信任 LDAPS，过滤禁用账号并要求 Samba 永久锚点属性已存在。`anas_casdoor_dirwatch` 以只读方式跟随 `ANAS_DIRECTORY_EVENTS_DIR`，按独立游标恢复、过滤并防抖事件，然后使用 Module 自己的受管 Application 凭据调用本地 Casdoor API。每批同步先读取目录和 Casdoor 影子用户，以永久锚点关联改名用户；随后执行上游 LDAP 导入，并收敛 `externalId/name/ldap/properties/groups/isForbidden/isDeleted`。`externalId` 保存 Samba 永久锚点，Casdoor 的不可变 `id` 不被修改；目录属性只合并到 `properties`，不删除人工属性；`displayName/email` 仍只为同步用户刷新，密码和人工权限不被覆盖。

由于上游会保留既有 Group，订阅器使用同一受限 Bind 经受信任 LDAPS 查询 `ALLOW_GROUPS`，以 AD matching rule `1.2.840.113556.1.4.1941` 计算直接和递归成员，再权威覆盖受管用户 Group。缺失组、重复/缺失锚点或任何 Casdoor 补丁失败都会使整批失败并保留游标重试。默认 5 分钟的上游自动同步不关闭，因此订阅器仍是低延迟加速器。

当前仅导入和远程认证，不启用密码写回。删除事件把影子记录标为禁止和删除，停用事件标为禁止，两者都清空 Group；重新启用或同锚点改名会复用并恢复原记录。r10 正式双架构构建、OIDC 主体与撤权、真实 Nextcloud、备份恢复、密钥轮换及生命周期通过，见 [2026-10-06 发布验收](../../../dev-docs/reviews/2026-10-06-casdoor-release-acceptance.md)。ARM64 验证为 QEMU 下的目标 helper 执行；SAML 应用会话与 SLO 仍待实现。

## IAM 契约

- OIDC：固定 `3.143.0` 发布部署级 issuer/discovery，按 Consumer 注册 client、redirect URI 与显式 back-channel URI；access token 有效期为 1 小时，refresh token 为 30 天；ID Token 与 Logout Token 使用同一 `sid`，Logout Token 为 RS256 且带 `iss/aud/sub/iat/exp/jti/events`。声明消失或切换 SAML 时用空值清理旧 URI。
- SAML：发布 metadata、SSO 和签名证书；不发布未经证实的 SLO。
- 授权：把每个 `ALLOW_GROUPS` 建成 `anas` 组织的同名 Group/Role，并为 Consumer 建立 Approved Application Permission；Casdoor 在登录签发前检查这些组。
- 属性：OIDC 使用 `JWT-Custom`/RS256，注册的永久锚点 claim 取自 `ExternalId`，Group 由 Role 名称发出；OIDC sub、UserInfo 和 Logout Token 同取 ExternalId。SAML 的注册锚点映射到 `$user.externalId`、Group 映射到 `$user.roles`。未知 SAML 来源被省略；SAML NameID 同取 ExternalId，显式锚点属性继续提供。

### 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。本 Module 既是 Consumer（对 Samba AD）也是 Provider
（对各应用）。

**Consumer 侧（Casdoor ← Samba AD）**

- **身份存在哪张表/哪个字段**：Casdoor `user` 表。`externalId` 列保存 Samba 永久锚点（匹配键），
  Casdoor 自己不可变的 `id` 列不被 dirwatch 修改；`name` 列是用户名，`displayName`/`email` 是标签，
  其余目录属性合并进 `properties`。
- **匹配键怎么配出来的**：`hook/main.go` 把 `CASDOOR_LDAP_FILTER` 构造成
  `(&<user class><enabled>(anasIdentityAnchor=*))`，并渲染
  `CASDOOR_DIRWATCH_IDENTITY_ANCHOR_ATTRIBUTE`；`hook/iam.go` 的 `ldapCustomAttributes` 把锚点属性
  登记为 LDAP 自定义属性。`anas_casdoor_dirwatch` 每批先读目录与 Casdoor 影子用户、**以永久锚点
  关联改名用户**，再执行上游 LDAP 导入，最后收敛
  `externalId/name/ldap/properties/groups/isForbidden/isDeleted`。
- **每批同步刷新什么**：为每个当前同步用户刷新 `displayName` 与 `email`；`properties` 只合并
  不删除人工属性；密码与人工权限不被覆盖。`id` 保持不变，`externalId` 按永久锚点收敛。
- **撤权经哪个接口**：dirwatch 使用 Module 自己的受管 Application 凭据调用**本地 Casdoor API**，
  置 `isForbidden`/`isDeleted` 并清空 Group。它以只读方式跟随 `ANAS_DIRECTORY_EVENTS_DIR`，
  按独立游标恢复、过滤并防抖；默认每 5 分钟的周期 LDAP 全量同步是兜底。
- **技术阻碍**：无。Consumer 侧满足 `DIRKEY-R-002` 与 `DIRKEY-R-007`。本实现不启用 Casdoor 的
  LDAP/AD 密码写回，也不把 Casdoor 本地用户记录当作目录权威。

**Provider 侧（Casdoor → 各应用）**

- **OIDC**：四种 JWT 格式、UserInfo 和 Logout Token 的 `sub` 都取 `ExternalId`，与目录 anchor
  逐字节一致；自定义 `sub` 配置不能覆盖该校验。恢复管理员 OIDC 保留内部 ID。
- **SAML**：1.1/2.0 NameID 同取 `ExternalId`，2.0 使用 persistent 格式；显式锚点属性继续提供。
- **构建**：Dockerfile 应用六个固定源码补丁。`test-casdoor-identity-source.sh` 先复现 r8/r9
  配置边界，再应用生产主体与撤权补丁验证签名、缺锚点拒绝和重放范围。实机证据单独记录。

**目录事件撤权（r10）**：沿用现有 helper，没有新增服务、数据库或消息总线。
Hook 从 `ALLOW_GROUPS` 生成 `CASDOOR_DIRWATCH_APPLICATIONS`；事件同步先按旧内部用户 ID 通过
`/api/anas-directory-revocation` 的 `prepare` 保存目标，再更新影子状态、再次捕获并落盘更新期间签发的授权，最后以 `revoke` 删除已捕获
token/会话记录。该接口只接受 `admin/app-built-in` 的 Basic Auth 服务凭据，限定 `anas` 组织，
不接受调用者提供的接收 URL 或签名密钥。OIDC sid 每次授权独立生成，`Token.SessionId` 仅记录
父 Beego 会话，`Token.UserId` 关联不可变内部用户 ID，改名和同名新对象不会改变授权归属。
单个 Casdoor 进程内，目录用户更新、授权码签发与兑换、刷新和撤权用同一互斥锁串行；当前 Module
只运行一个 Casdoor 实例，此同步不提供多实例一致性。普通/管理员中央登出使对应父会话的刷新授权失效。
未兑换的授权码和已由中央登出作废的授权仍删除，但不生成不存在的应用会话通知；两次捕获的相同应用/sid/主体只保留一份通知。
刷新保留原 sid；按应用撤权不销毁共享父会话，重放不会删除新的独立授权。

`pending-logouts.json` 保存在现有 `/data/anas-dirwatch`，权限 `0600`，原子替换并 fsync 文件与
父目录；仅记录身份和会话标识符，不存 bearer token/Secret。`deliver` 使用已登记接收端、
新签名两分钟 Logout Token，5 秒 HTTP 超时并拒绝重定向；只有 2xx 才确认，2/5/10/30/60 秒
退避持续重试。通知失败不阻塞目录游标。健康文件提供 pending 数量、最早时间与诊断，超过 60 秒
或缺能力时不就绪；没有静默丢弃。SAML SLO 尚未实现，受影响的 SAML 会话保留显式未完成诊断。
启动与每 300 秒全量 reconcile 修复日志缺口；身份标签冲突隔离、准入禁止，不按标签恢复权限。

2026-10-04 隔离实机已通过 r10 的标准 Consumer OIDC 完整矩阵：保存 Cookie 与真实 refresh、
直接/递归组撤权、停用、删除、改名、同名新 anchor 隔离，以及接收端 503、持久待办、watcher
重启和重新授权后的旧通知重试。两 client 单独验收确认同用户另一 client 与其他用户会话不受影响。
真实 Nextcloud r11 另行验证原 uid、LDAP anchor 映射、原 DAV 文件及改名/直接组撤权的旧 Cookie。
签名 SAML 2.0 SP 协议通过，不包含 Nextcloud SAML 会话或 SLO。完整候选摘要、测试入口与
发布缺口见[实机记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-10-04-casdoor-directory-identity-acceptance.md)。
默认事件防抖 5 秒、最小同步间隔 60 秒；本轮脚本的影子状态等待上限 420 秒、其后会话撤销等待
上限 120 秒，是测试超时边界，不是故障情况下的服务保证。


**`DIRKEY-R-013` 投影结论：不适用（Provider 不是 Consumer）。** 本 Module 不消费别人的主体标识符。
它在 `R-013` 里的角色是被验证的那一侧。需要注意的是，把 SAML NameID 切成 anchor 会让 anchor 进入
断言的 NameID 字段，按 NameID 建号的 SAML Consumer 会因此在应用内产生 UUID 形态的用户 id——当前
唯一的 SAML Consumer 是 `nextcloud`，它的 `uid_mapping` 显式取锚点属性并经
`user_id_ldap_mapping` 解析回 LDAP 账号，不读 NameID，因此不受影响（见该 Module 技术文档的
同名小节）。

## 管理面与 Secret 生命周期

`admin_casdoor` 由本地账号 inventory 按默认 `admin_{module}` 模板管理；Casdoor 不需要 `fixed_username`。Apply/rotate Handler 通过 stdin 把候选密码送入容器 Helper，直接更新 bcrypt 值并回读验证；密码不进入 argv。轮换失败时恢复旧密码。

## 空工作区恢复

恢复到空工作区后，先核验备份中的数据库、Secret Store、管理员库存、目录游标和 deployment metadata。
导入的 deployment 仍绑定原工作区，不能直接 `start`；停止原栈后，在恢复工作区执行
`anas apply -w <恢复工作区> --module-root <模块目录> --no-snapshot -y`，生成本地绑定的部署，
再验证原用户登录、永久锚点和签名。保留原备份 metadata 作为恢复证据。

## 环境变量所有权

导出 `ANAS_IAM_BINDING_*` 和 `ANAS_IAM_PORTAL_URL`；显式消费 TLS、Samba LDAPS、`ANAS_DIRECTORY_EVENTS_*`、IAM Consumer 注册和应用清单。镜像构建把全局 `GOPROXY_URL` 传给目录事件 Helper 的 Go builder，不能固定依赖 `proxy.golang.org`。敏感 Bind 密码和订阅器使用的 Casdoor Application Secret 只进入本 Module，Samba 生产者不持有 Casdoor 凭据。

## 测试与实现位置

`test-env/upgrades/configs/modules-casdoor.yml` 声明 r8→r9 的升级套件。复用隔离升级 Runner、数据库与目录持久标记，并核验 Casdoor readiness、UID 1000、配置 `0600` 和真实 OIDC issuer。套件登记不表示新 revision 的升级验收已通过；finance 的保留部署切换和 Workspace 临时存储验收分别记录。

- [`iam_test.go`](../hook/iam_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`helper/main_test.go`](../casdoor/helper/main_test.go)
- [`helper/directory_watch_test.go`](../casdoor/helper/directory_watch_test.go)
- [`server-casdoor-directory-events-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-events-e2e.sh)（2026-08-26 在显式指定服务器的隔离 Docker daemon 通过）
- [`server-casdoor-directory-authority-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-authority-e2e.sh)（2026-08-26 在同一隔离环境通过）
- [`server-casdoor-oidc-e2e.sh`](../../../test-env/scripts/server-casdoor-oidc-e2e.sh)（2026-08-27 在同一隔离环境通过）
- [`server-casdoor-saml-e2e.sh`](../../../test-env/scripts/server-casdoor-saml-e2e.sh)（2026-08-27 在同一隔离环境通过）
- [`server-casdoor-oidc-logout-e2e.sh`](../../../test-env/scripts/server-casdoor-oidc-logout-e2e.sh)（2026-08-27 在同一隔离环境通过真实 Consumer、多 session、管理员 API、签名、重放与配置恢复矩阵）
- [`server-casdoor-local-admin-e2e.sh`](../../../test-env/scripts/server-casdoor-local-admin-e2e.sh)（2026-08-27 在最新 r8 通过恢复登录、成功轮换和失败回滚）
- [`server-casdoor-restore-e2e.sh`](../../../test-env/scripts/server-casdoor-restore-e2e.sh)（2026-08-27 通过 Btrfs snapshot 到空 workspace 恢复与原身份登录）
- [`server-casdoor-lifecycle-e2e.sh`](../../../test-env/scripts/server-casdoor-lifecycle-e2e.sh)（2026-08-27 通过 amd64 冷启动/重启/升级/回滚及 arm64 构建/运行）
- [`server-casdoor-key-rotation-e2e.sh`](../../../test-env/scripts/server-casdoor-key-rotation-e2e.sh)（2026-08-27 通过签名与 Portal Secret 轮换、重叠信任和失败恢复）
- [`module.yml`](../module.yml)

## 当前限制

**待实现：SAML SLO 与目录事件驱动的 SAML 应用会话终止。** OIDC 为当前主要支持协议；此项
不作为当前 OIDC 发布阻塞项。SAML 保留可选 SSO 和已验证的签名协议能力，退出暂由应用本地执行。
未来实现仍需签名、NameID/SessionIndex、原 Cookie 失效和会话隔离验收；不提前发布 SLO 声明。

状态为 `developing`，r10 发布验收尚未完成。固定版本没有 SAML LogoutRequest/LogoutResponse 消费路径，因此 SLO endpoint/binding 保持不发布；不启用目录密码写回，不支持静默切换数据库，也不把 Casdoor 本地 User ID 当作 Samba 永久锚点。其余声明能力和发布验收范围见需求矩阵与实施计划。

规范性要求、稳定需求 ID、里程碑归属和逐项执行记录见
[Casdoor IAM Provider 集成要求](../dev-docs/requirements/casdoor-iam.md)与
[Casdoor IAM Provider 实施计划](../dev-docs/plans/archived/casdoor-iam.md)。本节只保留面向 Module 维护者的
限制摘要，不作为完成状态的来源。
