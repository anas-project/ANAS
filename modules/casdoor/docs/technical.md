# Casdoor 技术实现

本文面向 Module 维护者，记录 `casdoor` 的协议契约、安全边界和验证入口。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `3.143.0-r8` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r8` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r8` | `casdoor` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-casdoor-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀及所有 IAM 端点 |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | 否 | 否 | 否 | 是 | `container_recreate` | LDAP 自动同步周期（分钟） |

## 数据与启动流程

Hook 生成 Casdoor `app.conf` 和初始化数据模板。PostgreSQL DSN 显式包含 `dbname`。容器启动时 Helper 从 `0600` 临时投影读取恢复密码，生成 bcrypt 后的 `/tmp/init_data.json`，再以 UID/GID `1000` 启动上游进程。由于上游先初始化 LDAP 自动同步器、后导入 init data，entrypoint 会短暂启动一次以提交表结构与托管对象，再启动正式进程；entrypoint 删除容器内无意义的 `lsof` old-instance 探测和上次启动遗留的 init 文件，避免 bootstrap 子进程把自己杀死或因 UID 1000 文件权限失败。正式进程 HTTP 可用后再次投影并回读恢复管理员密码，只有成功后才创建 readiness marker；普通健康检查同时要求 marker，直接 Docker 重启因此也是自包含的。初始化数据为 built-in 恢复管理员显式开启特权确认，并给 `anas` 组织创建不可注册的内部目录 Application；PostgreSQL 是唯一受支持的数据接口。

r8 以 Casdoor `3.143.0` 对应提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` 为源码输入，构建前校验归档 SHA-256 `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`，再顺序应用仓库内四个受控补丁：SAML 模板读取 `displayName/externalId`；OIDC ID Token 绑定 Beego session `sid`，用户退出和管理员删 session 发出两分钟 Logout Token；delivery 失败记录不含凭据的原因；token 查询使用 XORM 字段条件，避免 PostgreSQL 把未引用的 `user` 解析为当前数据库用户。镜像仍以固定官方 `3.143.0` 运行时为基础；构建代理不改变提交与校验和。Go 构建 stage 固定在 `BUILDPLATFORM` 并用 BuildKit 的 `TARGETOS/TARGETARCH` 交叉编译，最终目标 stage 不执行 `RUN`；amd64 真实部署和 arm64 固定源码构建/非特权目标运行探针均已通过。

## 受管凭据生命周期

`CASDOOR_SIGNING_MATERIAL` 是含当前 RSA 私钥、证书和限时旧证书的 Secret JSON bundle。部署清单只冻结
Secret 投影位置和公开证书投影位置，不记录值；候选部署同步更新 `CASDOOR_SIGNING_CERT`。Helper 以证书
SHA-256 指纹命名当前 Casdoor Cert，并把 Application 引用原子切换到新名称。轮换后一小时内保留旧
JWKS key；从 r7 升级时还保留旧 `anas-signing` alias，直到其证书退出信任窗口。

`CASDOOR_PORTAL_CLIENT_SECRET` 通过同一 credential transaction 更新 built-in Portal Application。
probe/reconcile/verify 都从 stdin 读取候选，错误不包含值；候选启动、应用回读和健康验证全部通过后才
提交 Secret Store。失败时恢复上一 deployment、数据库值和 Store generation。

## LDAP、目录事件与权威边界

LDAP 连接固定使用受信任 LDAPS，过滤禁用账号并要求 Samba 永久锚点属性已存在。`anas_casdoor_dirwatch` 以只读方式跟随 `ANAS_DIRECTORY_EVENTS_DIR`，按独立游标恢复、过滤并防抖事件，然后使用 Module 自己的受管 Application 凭据调用本地 Casdoor API。每批同步先读取目录和 Casdoor 影子用户，以永久锚点关联改名用户；随后执行上游 LDAP 导入，并收敛 `externalId/name/ldap/properties/groups/isForbidden/isDeleted`。`externalId` 保存 Samba 永久锚点，Casdoor 的不可变 `id` 不被修改；目录属性只合并到 `properties`，不删除人工属性；`displayName/email` 仍只为本批相关用户刷新，密码和人工权限不被覆盖。

由于上游会保留既有 Group，订阅器使用同一受限 Bind 经受信任 LDAPS 查询 `ALLOW_GROUPS`，以 AD matching rule `1.2.840.113556.1.4.1941` 计算直接和递归成员，再权威覆盖受管用户 Group。缺失组、重复/缺失锚点或任何 Casdoor 补丁失败都会使整批失败并保留游标重试。默认 5 分钟的上游自动同步不关闭，因此订阅器仍是低延迟加速器。

当前仅导入和远程认证，不启用密码写回。删除事件把影子记录标为禁止和删除，停用事件标为禁止，两者都清空 Group；重新启用或同锚点改名会复用并恢复原记录。该收敛逻辑已通过真实目录与 OIDC/SAML E2E。

## IAM 契约

- OIDC：固定 `3.143.0` 发布部署级 issuer/discovery，按 Consumer 注册 client、redirect URI 与显式 back-channel URI；access token 有效期为 1 小时，refresh token 为 30 天；ID Token 与 Logout Token 使用同一 `sid`，Logout Token 为 RS256 且带 `iss/aud/sub/iat/exp/jti/events`。声明消失或切换 SAML 时用空值清理旧 URI。
- SAML：发布 metadata、SSO 和签名证书；不发布未经证实的 SLO。
- 授权：把每个 `ALLOW_GROUPS` 建成 `anas` 组织的同名 Group/Role，并为 Consumer 建立 Approved Application Permission；Casdoor 在登录签发前检查这些组。
- 属性：OIDC 使用 `JWT-Custom`/RS256，注册的永久锚点 claim 取自 `ExternalId`，Group 由 Role 名称发出；不可变 Casdoor User ID 继续作为稳定 `sub`。SAML 的注册锚点映射到 `$user.externalId`、Group 映射到 `$user.roles`。未知 SAML 来源被省略；SAML NameID 仍是用户名，Consumer 的稳定关联必须使用显式锚点属性。

## 管理面与 Secret 生命周期

`admin_casdoor` 由本地账号 inventory 按默认 `admin_{module}` 模板管理；Casdoor 不需要 `fixed_username`。Apply/rotate Handler 通过 stdin 把候选密码送入容器 Helper，直接更新 bcrypt 值并回读验证；密码不进入 argv。轮换失败时恢复旧密码。

## 环境变量所有权

导出 `ANAS_IAM_BINDING_*` 和 `ANAS_IAM_PORTAL_URL`；显式消费 TLS、Samba LDAPS、`ANAS_DIRECTORY_EVENTS_*`、IAM Consumer 注册和应用清单。镜像构建把全局 `GOPROXY_URL` 传给目录事件 Helper 的 Go builder，不能固定依赖 `proxy.golang.org`。敏感 Bind 密码和订阅器使用的 Casdoor Application Secret 只进入本 Module，Samba 生产者不持有 Casdoor 凭据。

## 测试与实现位置

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

状态为 `release`。固定版本没有 SAML LogoutRequest/LogoutResponse 消费路径，因此 SLO endpoint/binding 保持不发布；不启用目录密码写回，不支持静默切换数据库，也不把 Casdoor 本地 User ID 当作 Samba 永久锚点。其余声明能力和发布验收范围见需求矩阵与实施计划。

规范性要求、稳定需求 ID、里程碑归属和逐项执行记录见
[Casdoor IAM Provider 集成要求](../../../dev-docs/requirements/casdoor-iam.md)与
[Casdoor IAM Provider 实施计划](../../../dev-docs/plans/archived/casdoor-iam.md)。本节只保留面向 Module 维护者的
限制摘要，不作为完成状态的来源。
