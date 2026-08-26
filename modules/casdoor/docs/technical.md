# Casdoor 技术实现

本文面向 Module 维护者，记录 `casdoor` 的协议契约、安全边界和验证入口。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `3.143.0-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r5` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r5` | `casdoor` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-casdoor-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀及所有 IAM 端点 |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | 否 | 否 | 否 | 是 | `container_recreate` | LDAP 自动同步周期（分钟） |

## 数据与启动流程

Hook 生成 Casdoor `app.conf` 和初始化数据模板。PostgreSQL DSN 显式包含 `dbname`。容器启动时 Helper 从 `0600` 临时投影读取恢复密码，生成 bcrypt 后的 `/tmp/init_data.json`，再以 UID/GID `1000` 启动上游进程。由于上游先初始化 LDAP 自动同步器、后导入 init data，entrypoint 会短暂启动一次以提交表结构与托管对象，再启动正式进程；镜像移除容器内无意义的 `lsof` old-instance 探测，避免 bootstrap 子进程把自己杀死。初始化数据为 built-in 恢复管理员显式开启特权确认，并给 `anas` 组织创建不可注册的内部目录 Application；PostgreSQL 是唯一受支持的数据接口。

## LDAP、目录事件与权威边界

LDAP 连接固定使用受信任 LDAPS，过滤禁用账号并要求 Samba 永久锚点属性已存在。`anas_casdoor_dirwatch` 以只读方式跟随 `ANAS_DIRECTORY_EVENTS_DIR`，按独立游标恢复、过滤并防抖事件，然后使用 Module 自己的受管 Application 凭据调用本地 Casdoor API。每批同步先读取目录和 Casdoor 影子用户，以永久锚点关联改名用户；随后执行上游 LDAP 导入，并收敛 `externalId/name/ldap/properties/groups/isForbidden/isDeleted`。`externalId` 保存 Samba 永久锚点，Casdoor 的不可变 `id` 不被修改；目录属性只合并到 `properties`，不删除人工属性；`displayName/email` 仍只为本批相关用户刷新，密码和人工权限不被覆盖。

由于上游会保留既有 Group，订阅器使用同一受限 Bind 经受信任 LDAPS 查询 `ALLOW_GROUPS`，以 AD matching rule `1.2.840.113556.1.4.1941` 计算直接和递归成员，再权威覆盖受管用户 Group。缺失组、重复/缺失锚点或任何 Casdoor 补丁失败都会使整批失败并保留游标重试。默认 5 分钟的上游自动同步不关闭，因此订阅器仍是低延迟加速器。

当前仅导入和远程认证，不启用密码写回。删除事件把影子记录标为禁止和删除，停用事件标为禁止，两者都清空 Group；重新启用或同锚点改名会复用并恢复原记录。该收敛逻辑已有单元证据，但仍必须通过真实同步 E2E 后才能提升生命周期。

## IAM 契约

- OIDC：固定 `3.143.0` 发布部署级 issuer/discovery，按 Consumer 注册 client、redirect URI 与显式 back-channel URI；声明消失或切换 SAML 时用空值清理旧 URI，真实通知仍为受限/待验收。
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
- [`server-casdoor-directory-authority-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-authority-e2e.sh)（已编写，待指定服务器执行）
- [`module.yml`](../module.yml)

## 当前限制

状态为 `developing`。目录订阅 E2E 已验证新增、既有用户属性刷新、突发防抖与游标重启恢复；账号删除/停用传播仍未验收。还必须完成真实浏览器/HTTP OIDC 与 SAML 登录、Group 门禁、永久锚点、OIDC 会话撤销和恢复登录 E2E，才能评估生产支持。

规范性要求、稳定需求 ID、里程碑归属和逐项执行记录见
[Casdoor IAM Provider 集成要求](../../../docs/requirements/casdoor-iam.md)与
[Casdoor IAM Provider 实施计划](../../../docs/plans/casdoor-iam.md)。本节只保留面向 Module 维护者的
限制摘要，不作为完成状态的来源。
