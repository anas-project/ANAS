---
doc_type: research
created: 2026-08-20
updated: 2026-08-20
evidence_as_of: 2026-08-20
---

# Super Productivity 在 ANAS 中通过 Nextcloud 与 OIDC/SAML 零配置同步的可行性研究

本报告是对[既有 Super Productivity 选型报告](./super-productivity-alternatives-research.md)的专项补充，回答一个更窄的问题：在 ANAS 环境中，能否把 Super Productivity 直接预配置为 Nextcloud 同步，并让用户只经过 OIDC/SAML 身份验证、不再填写 Nextcloud 地址、用户名或应用密码。动态上游证据核验于 2026-08-20；ANAS 结论基于同日工作树。

## 1. 结论先行

**现有 Super Productivity、ANAS Nextcloud 和 OAuth2 Proxy 不能仅靠配置实现目标。** 如果“无需用户操作”指用户仍会登录 IAM、但不再手工设置同步，那么结论如下：

1. **原样部署不可行。** Super Productivity 当前的 Nextcloud Provider 明确要求 `Server URL`、Nextcloud 用户 ID 和 `App Password`；它没有 Nextcloud OIDC/SAML 登录按钮、授权码流程、刷新令牌生命周期或 SAML 客户端实现。[官方同步配置文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/2.09-Configure-Sync-Backend.md#nextcloud)也把应用密码列为必填项。
2. **入口 SSO 不等于 WebDAV SSO。** Super Productivity Web 是静态 local-first 客户端，没有自己的账号或服务端用户会话。用 ANAS `oauth2_proxy` 保护页面只能决定谁能加载应用，不能让应用自动获得该用户的 Nextcloud WebDAV 权限。上游维护者也明确说明 Docker 镜像只是静态客户端，不会变成有账号和服务端存储的 Web 服务。[上游说明](https://github.com/super-productivity/super-productivity/discussions/5564#discussioncomment-14986925)
3. **SAML 不能直接作为 WebDAV 请求凭据。** ANAS 的 SAML 配置可让 Nextcloud 登录页和客户端登录流程使用 SSO，但 Super Productivity 的 DAV 请求仍需要 Basic/App Password、有效 Nextcloud 会话 Cookie，或 Nextcloud 明确启用的 OIDC Bearer Token。SAML Assertion 不是 Super Productivity 或 Nextcloud DAV 接口当前接受的请求头凭据。
4. **OIDC Bearer 在协议上可行，在当前产品链路中不可用。** Nextcloud 34 可以让 `user_oidc` 校验外部 IdP 的 ID/Access Token 并用于 API/WebDAV；但 ANAS 当前固定的 `user_oidc` 8.10.1 新建 Provider 时默认关闭 `check-bearer`，ANAS 初始化脚本也没有显式打开它。Super Productivity 虽在 WebDAV 底层模型中留有可发送 `Bearer` 的 `accessToken` 字段，却没有 Nextcloud Provider 可用的取令牌、刷新、连接测试和配置流程；连接测试仍固定使用 Basic Auth。
5. **上游 Docker 预配置只能减少非敏感输入，不能完成用户绑定。** 当前入口脚本只把 `WEBDAV_BASE_URL`、`WEBDAV_USERNAME`、同步目录和间隔写入公开静态 JSON，并选择通用 `WebDAV` Provider；它不支持 Nextcloud Provider、密码或按当前登录用户生成配置。[入口脚本](https://github.com/super-productivity/super-productivity/blob/master/docker-entrypoint.sh)把同一 JSON 发给所有访问者，因此也不能安全放入某个用户的应用密码。
6. **最小、安全、协议中立的产品改造是接入 Nextcloud Login Flow v2。** 用户首次连接时在默认浏览器完成一次 OIDC 或 SAML 登录/授权，客户端轮询得到 `server`、`loginName` 和独立 `appPassword`，以后自动同步。它消除地址、用户名和复制密码，但按设计仍保留一次用户授权，不能称为完全零交互。[Nextcloud Login Flow v2](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/LoginFlow/index.html#login-flow-v2)
7. **若严格要求“登录 IAM 后零手工”，必须新增受信后端/BFF 和客户端适配。** 推荐由 BFF 根据已验证用户为其创建、保管并轮换专属 Nextcloud App Password，再代理同源 DAV 请求；不要把管理员生成的共享凭据或某个用户的密码写进静态前端。此方案以 OIDC/SAML 验证人、以 App Password 授权 DAV，并不是“OIDC/SAML 直接访问 WebDAV”。
8. **不建议把直接 OIDC Bearer 作为首版。** 它只能覆盖 OIDC，不能覆盖 SAML；还需解决 public-client/PKCE、Token audience、刷新与撤销、跨 Provider 一致性、CORS 和 Super Productivity 上游分叉维护。可做后续实验，不应成为 `super-productivity-web` 的默认契约。

因此，本题最准确的回答是：

> **“直接配置 + OIDC/SAML 直接验证 + 零用户操作”目前不成立。** 一次 SSO 授权可通过实现 Nextcloud Login Flow v2 达成；严格零手工可以通过 ANAS 自研 BFF 达成，但需要代码改造，并且 DAV 下游仍应使用每用户应用密码。只有 OIDC 可进一步试验直接 Bearer，SAML 不具备该路径。

## 2. 判定口径与假设

本报告把“无需用户操作”解释为：用户仍需在没有有效 IAM Session 时完成正常登录，但登录后不需要进入 Super Productivity 设置、不需要打开 Nextcloud 安全设置、不需要创建或复制应用密码，也不需要填写服务器 URL/用户名。

如果要求连 IAM 登录本身也完全无交互，则只有已经存在可复用的浏览器 SSO Session、设备身份或其他上游认证状态时才可能；新设备上的首次身份验证不可能凭空省略。

“直接验证”还必须区分两个动作：

| 层次 | 当前目标 | 可用协议/凭据 | 当前 ANAS 状态 |
| --- | --- | --- | --- |
| 页面入口 | 谁可以加载 Super Productivity 静态前端 | OIDC → OAuth2 Proxy Cookie | 已有能力，但还没有 Super Productivity Module |
| 应用本地状态 | 哪个浏览器本地库属于谁 | 浏览器 Origin/Profile；上游无账号 Session | 没有应用级用户映射 |
| Nextcloud WebDAV | 谁可以读写该用户的同步文件 | App Password/Basic、Nextcloud Session Cookie、可选 OIDC Bearer | App Password 可用；OIDC Bearer 当前未启用；SAML Assertion 不适用 |

只有把第三层授权也打通，才算 Nextcloud 同步 SSO。仅完成第一层会产生“页面已经 SSO，所以同步也已 SSO”的错误安全假设。

## 3. Super Productivity 当前实现边界

### 3.1 Nextcloud Provider 是应用密码模型

上游[配置文档](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/2.09-Configure-Sync-Backend.md#nextcloud)要求：

- Server URL；
- Nextcloud user ID；
- 可选、仅用于登录名与 user ID 不同时使用的 login name/email；
- 必填 App Password；
- 相对 DAV files root 的同步目录。

底层 [`webdav-base-provider.ts`](https://github.com/super-productivity/super-productivity/blob/master/packages/sync-providers/src/file-based/webdav/webdav-base-provider.ts)的就绪检查要求 `userName`、`baseUrl`、`syncFolderPath` 和 `password` 均存在，并明确把 WebDAV/Nextcloud 凭据当成用户输入的、不可自动刷新的密码。实际 [`webdav-api.ts`](https://github.com/super-productivity/super-productivity/blob/master/packages/sync-providers/src/file-based/webdav/webdav-api.ts#L500-L529)在普通请求中有 `accessToken` 时会发 Bearer，否则发 Basic；但这不足以构成 OIDC 支持：

- Nextcloud Provider 没有 Authorization Code + PKCE 流程；
- 没有 OIDC discovery、state/nonce、redirect callback；
- 没有 refresh token、过期重试、撤销或统一退出；
- `isReady()` 仍要求 password；
- `testConnection()` 固定构造 Basic header，而不是使用 `accessToken`；
- UI 和官方文档都不提供 Nextcloud OAuth/OIDC Token 输入方式。

所以 `accessToken` 是底层传输能力，不是可用、完整或受支持的 Nextcloud OIDC 产品契约。

### 3.2 Docker 默认覆盖不是动态用户配置

上游 [`docker-entrypoint.sh`](https://github.com/super-productivity/super-productivity/blob/master/docker-entrypoint.sh)会从环境变量生成 `assets/sync-config-default-override.json`，但只覆盖通用 WebDAV 的 URL、用户名、目录、同步间隔、压缩与加密开关。它不写密码，也不把 Provider 设为 `Nextcloud`。

这份文件由 Nginx 作为静态资源返回：

- 所有用户读取到相同内容；
- 它只能提供默认值，不能从 OIDC/SAML Session 动态得到 `preferred_username`；
- 把应用密码加入这份文件会让任何获准加载页面的用户获得同一凭据；
- 即使每人单独部署一个容器，秘密仍会以浏览器可读的静态响应下发，轮换和撤销也没有上游生命周期。

因此可以安全预填 Nextcloud/同源代理地址和同步目录，但不能靠现有环境变量完成每用户无感认证。

### 3.3 静态前端没有服务端用户隔离

Super Productivity Web 的任务数据首先落在浏览器 IndexedDB。不同浏览器 Profile/Origin 是实际本地隔离边界；退出 OAuth2 Proxy 不会清除 IndexedDB。共享终端上，下一位登录者可能看到上一位使用者仍留在同一浏览器 Profile 中的本地任务，即使 DAV 凭据已经切换。

零配置方案因此还必须定义：

- IAM 用户变化时是否切换/清除本地数据库；
- 退出是否只撤销网络访问，还是清除本地任务；
- 一个用户多设备如何得到各自可撤销的 Token；
- 浏览器站点数据被清除后如何恢复；
- 首次本地与远端都有数据时由谁确认覆盖方向。

这些问题不是配置服务器地址能够解决的。

## 4. ANAS 当前能力与缺口

### 4.1 已具备的部分

当前 [`nextcloud` Module](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/README.md)已经：

- 用 LDAPS 供应用户和 Group；
- 默认选择 OIDC，SAML 仍受支持；
- 将 `preferred_username` 映射到 Samba AD `sAMAccountName`，保持 OIDC 与 LDAP 内部用户名一致；
- 将 `/login?direct=1` 留给受管 break-glass 本地账号；
- 使用固定的 `user_oidc` 8.10.1 或 `user_saml` 8.2.0；
- 可以经 [`oauth2_proxy` Module](https://github.com/anas-project/ANAS/blob/master/modules/oauth2_proxy/README.md)为无登录能力的 Web 服务提供 OIDC ForwardAuth 门禁。

这些能力足以为 Super Productivity 页面增加 OIDC 门禁，也足以让 Nextcloud Login Flow v2 的浏览器页面继续复用 Nextcloud 当前 OIDC/SAML 登录。

### 4.2 OIDC Bearer 当前没有启用

ANAS 的 [`task.sh`](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/nextcloud/root/usr/local/bin/task.sh)创建 `user_oidc` Provider 时没有传 `--check-bearer=1`。固定版本 [`user_oidc` 8.10.1 的命令实现](https://github.com/nextcloud/user_oidc/blob/v8.10.1/lib/Command/UpsertProvider.php#L29-L35)说明，新 Provider 的 Bearer API/WebDAV 检查默认关闭；因此 fresh ANAS 部署不会把外部 OIDC Access Token 当作 DAV 身份。

Nextcloud 本身具备这项能力：[Nextcloud 34 管理文档](https://docs.nextcloud.com/server/stable/admin_manual/configuration_user/user_auth_oidc.html#bearer-token-validation)说明 `user_oidc` 可以接受 OIDC ID Token 或 Access Token 用于 API 请求；`user_oidc` 还会对 Bearer Token 做 audience 校验，[上游 README](https://github.com/nextcloud/user_oidc#disable-audience-and-azp-checks)明确只有手工关闭才会跳过。启用它并不是唯一缺失步骤，客户端仍需要取得 audience 正确的 Token。

### 4.3 现有 OAuth2 Proxy Session 不能直接复用

当前 [`oauth2_proxy` Compose](https://github.com/anas-project/ANAS/blob/master/modules/oauth2_proxy/docker-compose.yml)使用 `--set-xauthrequest=true`，但 Traefik 只接收并转发：

- `X-Auth-Request-User`；
- `X-Auth-Request-Email`；
- `X-Auth-Request-Preferred-Username`。

它没有把 Access Token 暴露给上游。即使增加 `--pass-access-token`/Authorization header，也还存在两个阻断项：

1. OAuth2 Proxy Token 是为 `oauth2-proxy` Client 取得的，Nextcloud Provider Client ID 是 `nextcloud`；默认 audience 校验不会把它们当作同一受众。
2. 把长期 Access Token交给静态前端会扩大 XSS、浏览器扩展和日志泄漏风险，还必须实现刷新与撤销。

因此不能把“复用 OAuth2 Proxy Cookie/Token”列为零改造方案。正确做法要么由 BFF 保管下游凭据，要么让 Super Productivity 成为真正的 OIDC Public Client，并由 IAM 明确签发 Nextcloud audience。

### 4.4 当前没有 WebDAV CORS 契约

ANAS Nextcloud 当前没有为独立的 Super Productivity Origin 配置专用 CORS header。上游文档也提示 Web 版 WebDAV 可能需要 CORS。可选解决办法是：

- 在严格 allowlist 下为 `https://<super-productivity-domain>` 放行 DAV 方法与 `Authorization`、条件请求 header；或
- 用 Super Productivity 同源 BFF 代理 DAV，浏览器不直接跨域访问 Nextcloud。

后一种方案更适合严格零手工架构，因为它同时能隐藏 App Password。

## 5. 可行方案比较

| 方案 | 用户操作 | OIDC | SAML | 上游分叉 | 安全/运维判断 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| 原样 Super Productivity + 手填 App Password | 每设备创建/粘贴一次 | 仅页面/Nextcloud 登录 | 仅页面/Nextcloud 登录 | 无 | 最简单、每设备可撤销；不满足零手工 | 可作基础 PoC |
| Docker 环境变量全局预填密码 | 无 | 无真实用户绑定 | 无真实用户绑定 | 小 | 同一静态秘密泄给所有用户，无法正确隔离 | **拒绝** |
| Nextcloud Login Flow v2 | 首次一次浏览器登录/授权 | 支持 | 支持 | 需要客户端功能贡献/分叉 | Nextcloud 官方客户端模式，独立 App Password，可撤销 | **首选产品路线** |
| ANAS BFF + 每用户 App Password | IAM 登录后无设置操作 | 支持 | 可由入口 SSO 间接支持 | 需要 SP 请求适配和新增服务 | Token 留在服务端；需窄化 `occ` 权限、轮换和本地库切换 | **严格零手工首选** |
| Super Productivity 直接 OIDC Bearer | 首次 OIDC 登录；有 Session 时可尝试 silent auth | 支持 | **不支持** | 较大 | audience、PKCE、刷新、撤销、CORS、Provider 差异复杂 | 后续实验 |
| OAuth2 Proxy Access Token 直传 Nextcloud | 表面无额外操作 | 理论上 | 不支持 | ANAS 改造 | 当前不传 Token，且默认 audience 不匹配；混淆入口与资源授权 | **不采用** |

### 5.1 方案 A：Nextcloud Login Flow v2

这是改动最少且与 OIDC/SAML 解耦的方向：

1. ANAS 预置公开的 Nextcloud URL、同步目录和合理同步间隔；
2. 用户首次打开 Super Productivity 时选择“连接 ANAS Nextcloud”；
3. 客户端 `POST /index.php/login/v2`，打开返回的 `login` URL；
4. Nextcloud 按当前 `iam_protocol` 走 OIDC 或 SAML；
5. 用户完成登录/授权后，客户端轮询得到 `server`、`loginName`、`appPassword`；
6. 客户端把凭据写入本地 Secret/Credential Store，后续用 Basic/App Password 同步。

[Nextcloud 官方说明](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/LoginFlow/index.html#login-flow-v2)指出结果只返回一次，每个客户端获得独立凭据，用户可单独撤销。它解决手填问题，但授权页面是安全边界的一部分，不应通过自动点击或共享管理员密码绕过。

Electron/移动原生客户端可以直接实现这个流程；Super Productivity Web 跨 Origin 调用 Nextcloud 的启动和轮询 Endpoint 时仍可能受 CORS 限制。Web 版应经同源、无凭据的 Login Flow Broker 转发这两个 Endpoint，或为精确 Origin 增加经过验证的 CORS 规则，不能假设原生客户端流程在浏览器中自动可用。

适合把它做成 Super Productivity 上游贡献：其 Nextcloud Provider 已有 `serverUrl/loginName/password` 数据模型，新增的主要是登录流 UI、轮询、凭据落库与取消/错误处理，不需要改变 DAV 同步协议。

### 5.2 方案 B：ANAS BFF 与受管 App Password

若产品要求是“用户登录 ANAS 后立即使用，连一次授权确认也没有”，建议架构为：

```text
Browser Super Productivity
        │ same-origin DAV, no downstream secret
        ▼
ANAS Super Productivity BFF ── verified IAM identity/session
        │ per-user App Password, server-side only
        ▼
Nextcloud /remote.php/dav/files/<uid>/super-productivity
```

BFF 首次见到已验证用户时，通过一个**窄权限的内部凭据 Broker**为该 Nextcloud UID 创建专用 App Password。Nextcloud 34/35 提供 `user:auth-tokens:add/list/delete`；[官方 OCC 文档](https://docs.nextcloud.com/server/stable/admin_manual/occ_users.html#user-auth-tokens-add)说明管理员可为指定用户生成应用密码，并可按 Token ID 删除。BFF 保存加密后的凭据、Token ID、用户锚点和最后使用时间，浏览器只持有 HttpOnly BFF Session。无登录密码创建的 Token 会受限，PoC 必须在 ANAS 的 LDAP 用户上证明其 `filesystem` 能力足够完成全部 DAV 方法，不能只根据命令成功就判定可用。

当前 ANAS 的 ForwardAuth Provider 本身只消费 OIDC。若部署只提供 SAML 而不提供 OIDC，BFF 还需要真正的 SAML SP/前门或其他可信身份转换；不能把现有 `oauth2_proxy` 自动写成 SAML 支持。

必须满足以下安全条件：

- 身份映射使用 ANAS 已有 `preferred_username`/`sAMAccountName` 和不可变 `anasIdentityAnchor`，不能信任浏览器自报用户名；
- Broker 只能为允许组内的当前用户创建/删除名为 `ANAS Super Productivity <device/session>` 的 Token，不能暴露任意 `occ`；
- 每用户至少一枚独立 Token，禁止全局共享 Nextcloud 账号；
- App Password 加密静态存储，不能出现在 Compose environment、日志、URL、静态 JSON 或前端错误消息中；
- IAM 登出、用户禁用、Group 移除和 Token 轮换要有明确回收策略；
- BFF 路径固定到当前用户的 `/remote.php/dav/files/<uid>/super-productivity`，防止路径穿越和跨用户代理；
- BFF 原样维护 ETag、`If-Match`/`If-None-Match`、PROPFIND、MKCOL、PUT、GET、DELETE 等同步语义；
- 本地 IndexedDB 按 IAM subject 分区或在用户切换时阻断并明确清除/切换，避免共享终端串号。

该方案能达到产品意义上的零手工，但增加了数据库/Secret、用户生命周期和代理兼容性，是一个新服务边界，不是简单静态 Module。

### 5.3 方案 C：直接 OIDC Bearer

若必须让 DAV 请求本身使用 OIDC Bearer，需要同时完成：

1. 将 Nextcloud `user_oidc` Provider 显式设置 `--check-bearer=1`，并保留 LDAP 用户映射与 Group 限制；
2. 为 Super Productivity 建立 Public Client + Authorization Code with PKCE；不得把 Client Secret 放入 SPA；
3. 让 IAM 为该 Client 签发 `aud=nextcloud` 的 Access Token，或实现标准 Token Exchange/Resource Indicator；不能简单关闭 Nextcloud audience 校验；
4. 在 Super Productivity 实现 discovery、state、nonce、PKCE、回调、refresh、401 单次刷新、撤销和 logout；
5. 修改 Nextcloud Provider 的 readiness、test connection 和 credential store，使 Bearer-only 配置成为正式路径；
6. 处理 Web、Electron、Android、iOS/PWA 各自的 redirect URI 和安全存储；
7. 配置严格 CORS 或同源代理；
8. 对 ANAS 支持的每种 IAM Provider测试 claims、audience、refresh token 与登出行为。

Nextcloud 的外部 OIDC Bearer 能力已存在，但不提供细粒度文件 scope；获得 Token 的客户端实际拥有该用户相当广泛的 Nextcloud API/文件权限。若只需要一个同步目录，BFF 固定路径比把完整 Bearer 暴露给浏览器更容易做到最小权限。

SAML 没有相应的刷新型 API Bearer Token，因此该方案必须将 Module 依赖固定为 `iam/oidc`，不能再声称同时支持 `saml`。

## 6. 推荐决策与实施顺序

### 6.1 当前决策

1. `super-productivity-web` 首轮 PoC 仍使用 Nextcloud App Password，明确记录一次用户设置；不要把 OAuth2 Proxy 门禁描述成同步 SSO。
2. 同时向上游实现/提交 Nextcloud Login Flow v2。这是最小、标准、可长期维护的用户体验改进：用户只需一次 SSO 授权，不再复制秘密。
3. 只有业务明确把“一次授权页面也不可接受”列为硬需求时，才实施 ANAS BFF + 每用户受管 App Password。
4. 直接 OIDC Bearer 放到独立实验，不与首个 Module 交付绑定；SAML 不进入这个实验的兼容承诺。

### 6.2 建议验收门槛

无论选择 Login Flow v2 或 BFF，都至少验证：

- OIDC 与 SAML 两种 Nextcloud 登录模式下的首次连接、续期、登出和 IAM 故障恢复；
- 两名用户共用一个浏览器 Profile 时不会看到或同步到对方数据；
- 同一用户两设备并发修改、离线恢复、ETag 冲突和同步文件损坏恢复；
- Token 单设备撤销、用户禁用、退出 APP Group 后访问立即或在声明窗口内失效；
- App Password/Access Token 不出现在 HTML、静态 JSON、浏览器日志、Traefik access log、容器环境导出和备份索引；
- 清空浏览器站点数据后能从 Nextcloud 恢复，同时不会自动覆盖较新的远端数据；
- Web/Electron/Android/iOS 的凭据保存和回调行为分别通过；
- Nextcloud 与 Super Productivity 升级/回滚前后的同步文件兼容性。

## 7. 对现有研究结论的修正

既有报告把“静态 Web + 现有 Nextcloud WebDAV”列为第一 PoC方向仍然成立，但需要补充两项更严格的结论：

- 上游 Docker 环境变量可以预填通用 WebDAV 非敏感默认值，**不能**按用户直接完成 Nextcloud 同步配置；
- ANAS 当前 Nextcloud OIDC 登录能力并未自动转化为 WebDAV Bearer 能力，入口 ForwardAuth 也未提供同步身份传播。

因此，PoC 应先验证 App Password 路径和 CORS，再决定是否投入 Login Flow v2 或 BFF；不能把“已有 OIDC/SAML”当成无需研发的充分条件。

## 8. 主要证据

### Super Productivity

- [Nextcloud Provider 配置字段](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/2.09-Configure-Sync-Backend.md#nextcloud)
- [同步快速配置：Nextcloud 需要用户名和应用密码](https://github.com/super-productivity/super-productivity/blob/master/docs/wiki/1.02-Configure-Data-Synchronization.md)
- [Docker 入口的静态同步默认值覆盖](https://github.com/super-productivity/super-productivity/blob/master/docker-entrypoint.sh)
- [WebDAV/Nextcloud Provider readiness 与凭据模型](https://github.com/super-productivity/super-productivity/blob/master/packages/sync-providers/src/file-based/webdav/webdav-base-provider.ts)
- [WebDAV 请求 Basic/Bearer 分支与 Basic-only 连接测试](https://github.com/super-productivity/super-productivity/blob/master/packages/sync-providers/src/file-based/webdav/webdav-api.ts)
- [维护者对静态 Web、无账号和无服务端存储的说明](https://github.com/super-productivity/super-productivity/discussions/5564#discussioncomment-14986925)

### Nextcloud

- [Login Flow v2](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/LoginFlow/index.html#login-flow-v2)
- [WebDAV 基础认证与外部认证场景的 App Password](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/WebDAV/basic.html)
- [OIDC Bearer Token验证](https://docs.nextcloud.com/server/stable/admin_manual/configuration_user/user_auth_oidc.html#bearer-token-validation)
- [`user_oidc` Bearer 与 audience 配置](https://github.com/nextcloud/user_oidc#bearer-token-validation)
- [`user_oidc` 8.10.1 `check-bearer` 创建默认值](https://github.com/nextcloud/user_oidc/blob/v8.10.1/lib/Command/UpsertProvider.php#L22-L29)
- [管理员创建、列出和删除用户 App Token](https://docs.nextcloud.com/server/stable/admin_manual/occ_users.html#user-auth-tokens-add)

### ANAS 当前实现

- [`nextcloud` 身份、OIDC/SAML 与 LDAP 边界](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/README.md)
- [`nextcloud` OIDC/SAML 初始化](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/nextcloud/root/usr/local/bin/task.sh)
- [`nextcloud` 通用 IAM Client/Binding 映射](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/hook/iam.go)
- [`oauth2_proxy` OIDC ForwardAuth 边界](https://github.com/anas-project/ANAS/blob/master/modules/oauth2_proxy/README.md)
- [`oauth2_proxy` 返回的身份 Header 与 Traefik middleware](https://github.com/anas-project/ANAS/blob/master/modules/oauth2_proxy/docker-compose.yml)
