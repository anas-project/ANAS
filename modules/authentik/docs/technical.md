# authentik 技术实现

本文面向 Module 维护者，记录 `authentik` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `2026.5.6-r15` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | 提供 Capability | `oidc, saml` |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_authentik` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r15` | `traefik, authentik, db` | 3 |
| `anas_authentik_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r15` | `authentik, db` | 2 |
| `anas_authentik_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r15` | `` | 3 |
| `anas_authentik_worker` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-authentik:2026.5.6-r15` | `authentik, db` | 3 |
<!-- generated:compose-topology:end -->

首次启动会在主服务内执行完整数据库迁移；主服务健康检查给予 600 秒启动窗口，避免在受支持的
4 vCPU / 3 GiB 基线环境中把仍在推进的冷启动迁移误判为失败。该窗口不改变迁移失败后的
5 次常规健康重试。

客户端与目录 blueprint 在引用默认 flow/stage、OIDC scope 和 LDAP mapping 前，用固定版本原生 `metaapplyblueprint` 应用其内置依赖，避免首次发现任务无序执行。依赖名称经固定镜像核对，不增加独立初始化器或调度器。

worker 健康检查还要求 `/blueprints/anas` 下的每个 blueprint 已存在对应实例、状态为
`successful`，且 Authentik 记录的 `last_applied_hash` 与挂载文件 SHA-512 一致。依赖
Authentik 的 Module 因而不会在 OIDC Provider 尚未可发现时启动。

deployment 整体保持 root-only；`anas_authentik_init` 只把运行必需的生成 blueprint（包含客户端凭据和签名材料，必须保持私有）
复制到 `${DATA_PATH}/authentik/blueprints`，将该私有副本交给 UID/GID 1000 后，server 与 worker
再以只读方式挂载。这样无需放宽 deployment 中其他配置或 Secret 的权限，也不会让非 root worker
因无法遍历 `0700` deployment 目录而永久停在启动状态。

`after_start` Hook 最多等待 10 分钟读取同一 ready marker；Runner 只有在该屏障通过后才会
启动下游 Module，因而 Compose 内健康状态与 Module 间依赖顺序使用同一权威判据。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `authentik.db_name` | string | — | `authentik` | `static` | `AUTHENTIK_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `authentik.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AUTHENTIK_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-authentik-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `authentik.domain_prefix` | string | — | `auth` | `static` | `AUTHENTIK_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀 |
| `authentik.ldap_enabled` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 LDAP Source |
| `authentik.ldap_password_writeback` | bool | — | `true` | `static` | `AUTHENTIK_LDAP_PASSWORD_WRITEBACK` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许目录密码回写 |
| `authentik.log_level` | string | — | `warn` | `static` | `AUTHENTIK_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 身份与授权数据流

Samba AD 是人员与组的事实来源。LDAP Source 通过 LDAPS 同步用户和组；`ldap_password_writeback` 控制是否允许 Authentik 使用受限服务账号回写普通用户密码。应用登录使用按 Consumer 生成的 OIDC 或 SAML 端点。`Admins` 映射为 Authentik superuser，`APP_all`/`APP_authentik` 只授予访问权。

### 应用会话登出

固定 `2026.5.6` 的 OIDC blueprint 把授权回调和登出后回调分别标为 `authorization`/`logout`，并从通用契约选择 `backchannel` 优先的 `logout_uri/logout_method`。浏览器登出、管理员删 session 和账号停用是否向某个 RP 发送有效签名 logout token，由该 Consumer 固定版本 E2E 判定。SAML Redirect/POST 均映射为浏览器参与的 `frontchannel_native` 并签名 LogoutRequest/LogoutResponse；HTTP-POST 不是后台通道。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | ldaps source (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins`, `APP_authentik`, `APP_all` |
| 目录密码回写 | `ldap_password_writeback` / restricted bind |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。本 Module 既是 Consumer（对 Samba AD）也是 Provider
（对各应用），两侧分别说明。

**Consumer 侧（Authentik ← Samba AD）**

- **身份存在哪张表/哪个字段**：`authentik_core.UserSourceConnection.identifier` 保存 anchor 值，
  并规范化进用户的 `attributes.ldap_uniq`。`User.username` 是 `sAMAccountName`，`User.name` 是
  `displayName`，两者都只是标签。
- **匹配键怎么配出来的**：`hook/main.go` 渲染
  `AUTHENTIK_LDAP_OBJECT_UNIQUENESS_FIELD = SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`，
  `hook/directory.go` 的 blueprint 把它填进 LDAP source 的 `object_uniqueness_field`；同一 Hook 把
  `AUTHENTIK_LDAP_USER_OBJECT_FILTER` 构造成
  `(&(objectClass=user)(!(objectClass=computer))(anasIdentityAnchor=*))`，缺 anchor 的对象不会进来。
- **每次同步刷新什么**：`user_property_mappings` 列出的 `givenName`、`sAMAccountName`、`sn`、
  `userPrincipalName`、`mail` 与显示名映射；`group_property_mappings` 刷新组名与
  `is_superuser`。`identifier` 按定义不刷新。
- **撤权经哪个接口**：`delete_not_found_objects: true` 让同步删除消失的对象；
  `anas_authentik_dirwatch`（`authentik/directory_watch.py`）按独立游标跟随持久目录事件日志并触发
  增量同步，周期全量同步兜底。
- **技术阻碍**：无。Consumer 侧满足 `DIRKEY-R-002` 与 `DIRKEY-R-007`。

**Provider 侧（Authentik → 各应用）**

- **主体标识符**：`hook/iam.go` 对每个 OIDC Provider 写死 `sub_mode: user_uuid`，注释明确记录了
  原因——LDAP source 按 printable anchor 匹配，因此 Authentik 用户 UUID 跨森林重建稳定，而
  **用户名是登录名，绝不能成为 OIDC subject**。
- **anchor 作为 claim**：`oidcClaimExpression`/`samlAttributeExpression` 把等于
  `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` 的来源翻译成 `request.user.attributes.get("ldap_uniq")`，
  OIDC 走 scope mapping、SAML 走 property mapping；固定版本补丁进一步将显式 OIDC sub
  写回原生 `IDToken.sub`，使签发、持久化和后续登出通知的表示一致。
- **SAML NameID**：blueprint **刻意不设置** `name_id_mapping`——Authentik 的该字段是指向 property
  mapping 的外键，不是 NameID format URN，也没有承载 format 本身的字段；它遵循 SP 在 AuthnRequest
  里发来的 NameIDPolicy。因此 NameID 的实际取值由 SP 决定，**未经复核**。
- **`DIRKEY-R-008` 验证边界**：`sub_mode` 保留原生 `user_uuid` 默认；显式请求 sub:anchor 的
  Consumer 经 scope mapping 和固定源码补丁使用 `ldap_uniq`。实际镜像内已验证原生签发与
  通知方法，但目录重建和联合 Consumer E2E 仍需真实固定组合验收。这是
  [目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)
  M2 第一项阻塞。在它解开之前，本 Provider 按 `DIRKEY-R-012` 声明：该部署下 Consumer 拿到的主体
  标识符是稳定的内部 id，不是 anchor。

**`DIRKEY-R-013` 投影结论：不适用（Provider 不是 Consumer）。** 本 Module 不消费别人的主体标识符，
因此没有"把 `sub` 投影成用户名/URL/文件路径"的问题。它在 `R-013` 里的角色是**被验证的那一侧**：
M2 切换后由各 Consumer 断言自己没有投影。需要注意的是，切换会改变 Authentik 发出的 `sub` 值，
按 `(issuer, sub)` 建号的 Consumer（`vikunja`）与按 `sub` 建号的 Consumer（`forgejo`、`netbird`）
都会把老用户认成新人——产品尚未上线、没有历史账号需要兼容，因此可以直接切。

## 管理面与 Secret 生命周期

日常管理员通过目录身份登录。固定用户名 `akadmin` 是 `break_glass` 恢复账号；它使用独立生成密码，不复用 Samba 或数据库管理员凭据。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `AUTHENTIK_DOMAIN_FULL` | `iam` |
| `local_recovery` | `AUTHENTIK_BREAK_GLASS_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `akadmin` | `plaintext_on_bootstrap` | 是 |

```bash
anas admin local list -w /srv/anas
anas admin local credential authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass -w /srv/anas
anas admin local rotate authentik break_glass --prompt -w /srv/anas
```

`credential` 会输出明文密码，应避免进入日志；`rotate` 默认生成随机密码，`--prompt` 从终端安全读取，不接受 argv 或普通环境变量传入密码。

### Secret 边界

- `ANAS_LOCAL_ADMIN__AUTHENTIK__BREAK_GLASS__PASSWORD`
- `AUTHENTIK_SECRET_KEY`
- `AUTHENTIK_SIGNING_CERT`
- `AUTHENTIK_SIGNING_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres` |
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
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `SAMBA_DC_BASE_DN`
- `SAMBA_DC_BASE_GROUPS_DN_PREFIX`
- `SAMBA_DC_BASE_USERS_DN_PREFIX`
- `SAMBA_DC_GROUP_CLASS_FILTER`
- `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`
- `TRAEFIK_BASE_PORT`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `APPS_LIST*`
- `SAMBA_DC_LDAPS_SERVER_URL_PORT`
- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`directory_test.go`](../hook/directory_test.go)
- [`iam_test.go`](../hook/iam_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 真实客户端 IP

Server entrypoint 解析 Traefik 的当前 IPv4 地址，并覆盖 Authentik 默认的宽泛私网代理范围，只保留 loopback、精确的 Traefik `/32` 和显式配置的上游代理。Traefik 无法解析时容器拒绝启动，防止事件与登录审计静默退化为 Docker 地址或接受伪造 Header。

## 当前限制

状态为 `developing`；正式支持前仍需以真实容器验证目录同步、组撤权、密码回写和恢复登录。

## 受信 OIDC 应用角色来源

通用 OIDC ATTRIBUTES 的保留来源 `anasRole` 从受信 `SAMBA_DC_ADMIN_GROUP_NAME` 组成员计算 admin/user；
不读取可自填 LDAP/profile 属性。Consumer 可显式请求 sub:anchor。固定 `2026.5.6` 的
`patch-canonical-oidc-sub.py` 在原生 `IDToken.new` 完成 profile 映射后校验显式 sub 为非空字符串，
将其写回 `IDToken.sub` 并移除 claims 中的重复值；未请求 sub 覆盖的 Consumer 保留原生行为。
只覆盖序列化输出会让 ID Token 使用 anchor、保存的内部 sub 却仍为 Authentik UUID，继而使原生
session 删除通知按错误主体撤销；此补丁同时修复已签发表示与后续通知的主体来源。

构建时 `verify-canonical-oidc-sub.py` 读取固定上游的实际 `IDToken.new`、Provider encode、
AccessToken 序列化、session-delete signal 与 logout-token 方法，使用 ORM/context doubles
并验证真实 PyJWT 签名中的 sub/hashed sid。缺失、重复或已改补丁锚点会中止构建。
该调用链验证不代替真实 LDAP、IAM HTTP、浏览器与 Immich 的联合 E2E；目录撤权仍单独验收。

## 选定的目录撤权事件扩展

OIDC binding 发布 `OIDC_CAEP_EVENTS=session-revoked` 支持值；Consumer 在现有注册中请求同名
列表。Runner 清理继承的该字段，校验支持声明来自所选 Provider calculate、请求来自该 Consumer，
只接受已知事件、OIDC/backchannel 及支持交集。其他 Provider 未声明时不能启用。
这实现选定的 [CAEP session-revoked 事件](https://openid.net/specs/openid-caep-1_0.html#section-3.1)，
不声明完整 SSF/CAEP 部署。

固定 `2026.5.6` 没有 `Application.attributes`，因此 `directory_admission.py` 直接消费既有
`ANAS_IDENTITY_OIDC_CLIENTS`、`ANAS_IAM_CLIENT_*` 和 `ANAS_IAM_BINDING_*`，核对被选择的
Application、实际 Provider、Samba AD 永久 anchor sub 映射及原有应用准入策略。未请求事件的
Consumer 保留原行为。`SAMBA_DC_APP_FILTER=false` 时，声明事件的应用仍建立仅检查 `is_active`
的原有表达式策略，不强迫启用许可组过滤。

LDAP 原生用户/组、成员关系和删除任务完成后，在原有 source lock 内检查停用和准入丧失。
删除在原生事务中先捕获 source connection 的稳定 anchor，再删除影子用户并入队通知。
原生 PostgreSQL Broker 的 Task 与该事务一起持久化；无 OAuth AccessToken 也能发通知。
既有 watcher 和周期 Source Sync 都会进入该路径，不增加服务或调度器。策略执行错误会失败并
重试，不当作准入丧失。已协商事件的源若同步缓存页丢失，抛出失败而不让原生 group.wait 将
记录错误后的空返回值视为完成；未协商源保留原行为。原生 OIDC 授权/换 token 已
`use_cache=False`，sender 直接评估原策略。

请求保留来源 `anasRole` 的应用还接收可信管理员角色丧失通知。判定使用与 claim 相同的
`ak_is_group_member(user, name=SAMBA_DC_ADMIN_GROUP_NAME)` 递归组谓词；`is_superuser` 可由
其他标记组提供，不能替代它。原生成员关系替换、组改名和组删除在同一事务中先捕获受影响
成员的旧角色，完成原更新后只为 true→false 主体持久化同一 CAEP 通知；组删除/改名含后代组。
普通用户和仍持有可信角色的用户不会每轮被撤权。不依赖进程内的整轮同步快照，失败会随
更新事务回滚。已提交的真实角色丧失即使后续同步阶段失败也撤销旧权限；恢复准入后需新的
OIDC callback 获取当前 roleClaim。尚未完成真实目录管理员组丧失与消费者降权的联合验收。

通知沿原生签名、JWKS 和 backchannel URI 发送，含标准 logout event、`sub_id` 的 `iss_sub`
主体及 CAEP `initiating_entity=policy`。检测时用共享 PostgreSQL `clock_timestamp()` 保存
`event_timestamp` 到原生 Task 第五参数；重试保留该值，CAEP 签发 `iat/exp` 也用 PostgreSQL 时钟，
并拒绝检测时间超过 `iat+5`。CAEP 不含 SID，只接受接收端直接返回 200/204；HTTP 失败或跳转
沿既有持久任务重试。固定上游默认最多重试五次，耗尽后保留 REJECTED 任务，需用既有任务
重试入口处理；不声明无限自动重投。普通登出仍使用原生 SID/subject 范围，不附加 CAEP 事件。

Dockerfile 固定上游 OCI 多架构 index digest `ed120caf710ccf82ef0026f0bc74e51615bc95ebff228a7a2d6fc60c441c3868`，
包含 linux/amd64 和 linux/arm64。构建探针执行实际同步、删除、签名与发送方法体，验证失败传递、
检测时间重试不变、当前注册过滤与普通登出分离。ORM/context doubles 及真实 PyJWT 签名测试仍不
代替真实目录停用、删除、递归组撤权、消费者会话/API key/分享失效、整机时钟和恢复验收。

Worker 在导入现有受管公共/内部 CA bundle 供 LDAPS 使用后，同时通过 `REQUESTS_CA_BUNDLE` 将同一只读副本交给原生 Python requests HTTP Task。backchannel/目录通知继续验证 TLS peer，不依赖 LDAPS CertificateKeyPair 自动成为 HTTP 信任，也不关闭证书检查。
