---
doc_type: requirement
status: current
created: 2026-08-21
updated: 2026-08-21
---

# Vikunja Module 集成要求

本文规定 ANAS 集成 Vikunja 时必须交付的用户结果、边界、安全约束和验收标准。候选比较与
选型证据见[自托管开源看板与项目管理方案调研](../research/self-hosted-open-source-kanban-research.md)；
本文不重复调研结论，也不把实现进度当作需求。

关键词“必须”“不得”“应该”具有规范性。

## 1. 目标

ANAS 必须提供可独立部署的 `vikunja` Module，使个人和中小团队能够通过统一域名与 IAM
使用任务、项目、看板、列表、表格、日历和 Gantt 视图，并能安全保存附件、调用 API 和配置
Webhook。

首期目标是可维护的社区版核心部署，不以 Vikunja Pro、移动端完整功能或 AI/MCP 编排为
前置条件。

## 2. 范围

首期必须包含：

- 固定上游稳定版本的单应用容器和 Traefik HTTPS 路由；
- 通过 `relational_database` Contract 使用独立 PostgreSQL 或 MariaDB 数据库；
- 通过通用 `iam` capability 使用 OIDC Authorization Code Flow，默认关闭本地密码登录和
  开放注册；
- OIDC 首次登录 JIT 建号，以及 `APP_vikunja`、`APP_all` 和管理员组的部署级准入；
- 数据库、附件和生成 Secret 的持久化、备份与恢复边界；
- 时区、默认语言、健康检查、版本化中英文文档和可执行的单元测试；
- REST API、API token、Webhook 和 CalDAV 的上游能力说明及安全边界。

首期不要求：

- LDAP 与 OIDC 双重自动建号或密码回写；
- SMTP、S3、Redis、搜索、Vikunja Pro、bot user 或 AI/MCP sidecar 的自动配置；
- ANAS 代用户生成管理员 API token；
- 把移动客户端描述为与 Web 功能完全等价；
- 在缺少上游标准 endpoint 时自建 OIDC front-/back-channel logout receiver。

## 3. Module 与网络契约

1. Module 名必须为 `vikunja`，类别为 `app`；真实容器验收完成前状态必须为 `developing`。
2. 应用版本、镜像 tag、ANAS image revision 和 `localization.yml` 必须一致；不得使用
   `latest`。
3. Web 服务只能通过 Traefik HTTPS 暴露，不得额外发布未认证的宿主端口。
4. 应用必须使用部署 DNS 解析 IAM 公网域名，并信任 ANAS 内部 CA；不得关闭 OIDC TLS
   验证。
5. 健康检查必须验证应用进程及数据库，而不是只检查容器 PID。
6. 运行镜像必须以非 root 身份执行 Vikunja。若挂载目录需要初始化权限，可以由最小
   entrypoint 在降权前完成，但不得让业务进程保留 root。

## 4. 数据库与持久化

1. Module 必须消费 `relational_database >=1.0.0 <2.0.0`，支持 `postgres` 和 `mariadb`，
   默认选择 `postgres`。
2. Runner 必须为 `primary_database` 创建独立数据库、principal 和稳定生成密码；Module
   不得读取 Provider 管理员凭据，也不得在自身 Compose 中复制数据库容器。
3. Vikunja 的 `mariadb` binding 必须映射为上游的 `mysql` database type；这一映射不得改变
   ANAS 对外的接口名称。
4. 数据库与 principal 的删除策略必须为 `retain`。修改 `db_type` 或 `db_name` 不得被描述为
   自动迁移；必须经过显式迁移流程。
5. 附件目录必须位于 workspace 的受管数据树，容器重建后仍可用。备份必须把数据库、附件、
   Secret Store 和部署元数据保持在同一恢复点。
6. 恢复验收至少验证 project、task、comment、attachment、OIDC 身份关联、API token 和
   webhook 配置。

## 5. IAM、账号与授权

1. Module 必须只消费自己的 `ANAS_IAM_BINDING__VIKUNJA__*`，不得按 Authentik、LLNG、
   Casdoor 或其他 Provider 名称分支。
2. OIDC client 必须是 confidential client，scope 为 `openid profile email`，redirect URI
   必须精确登记为 `<VIKUNJA_DOMAIN_FULL>/auth/openid/anas`。
3. OIDC issuer 必须来自 Runner 解析的 binding。client secret 必须稳定生成并保存在
   `.anas/secrets.yml`，不得写入 README、普通配置、日志或 deployment manifest。
4. 默认必须设置 `auth.local.enabled=false` 和 `service.enableregistration=false`。Vikunja
   没有由 `anas admin local` 托管的本地账号；IAM 故障时的恢复动作是恢复 IAM/目录链路，
   不能虚构应用内 break-glass 登录。
5. 首次 OIDC 登录可以 JIT 创建用户；email 和 `preferred_username` 必须由通用 claim contract
   提供。切换 IAM Provider 可能改变 `(issuer, sub)` 并产生新账号，首期不得宣称自动合并。
6. 开启应用过滤时，IAM 必须在完成登录前限制到 `APP_vikunja`、`APP_all` 或管理员组；关闭
   应用过滤时按部署的普通 IAM 策略准入。
7. API token 必须由用户在 Vikunja 内按最小权限创建。ANAS 不得默认生成管理员 token，
   webhook Secret 也不得跨 Module 暴露。

## 6. 登出与会话

1. 固定版本若能从 discovery 读取 `end_session_endpoint`、保留原 ID Token，并在跳转 IAM
   前删除本地 session，Module 应登记精确的 post-logout redirect URI 并接入 RP-Initiated
   Logout。
2. IAM 不可用或 logout URL 构造失败时，本地 session 仍必须先失效；不得为了等待 IAM 而
   保留可用的 Vikunja session。
3. 上游没有标准 OIDC front-/back-channel logout receiver 时，Module 必须省略
   `OIDC_LOGOUT_URI`、`OIDC_LOGOUT_METHODS` 和 `OIDC_LOGOUT_SESSION_REQUIRED`，并明确记录
   “不支持 IAM 主动撤销应用 session”。
4. 在真实浏览器 E2E 同时验证本地 session、IAM 中央 session、`id_token_hint`、
   `post_logout_redirect_uri` 和 IAM 故障降级前，只能声明“上游支持、待验收”，不得宣称
   双向登出或后台撤销。

## 7. 本地化与运维

1. `service.timezone` 和新用户默认时区必须继承 ANAS `global.timezone`。
2. 新用户默认语言应该继承 `global.default_language`，通过固定版本的支持清单匹配；不支持值
   必须告警并回退英语，不得阻断部署。已登录用户选择继续优先。
3. `localization.yml` 必须记录固定版本的 Web UI 语言清单、选择机制、fallback 和源码证据。
4. README 必须说明最简配置、IAM 故障恢复、数据库迁移边界、附件路径、备份恢复、API token、
   webhook、CalDAV、移动客户端限制和所有参数。

## 8. 验收标准

### 8.1 静态与单元验收

- `module.yml`、Compose、Hook、镜像入口和中英文文档通过仓库全部 manifest/documentation gate；
- Hook 测试覆盖 PostgreSQL/MariaDB 映射、稳定 Secret、OIDC registration/binding、应用组、
  service/OIDC credential candidate probe、不发布伪 logout receiver、语言匹配与 fallback；
- Compose 配置可解析，镜像 tag 固定，附件 volume、DB/Traefik network、内部 CA 和健康检查
  均存在；
- 生成的 Module catalog、Contract consumer 清单、环境变量参考和本地化矩阵保持同步。

### 8.2 真实部署验收

- amd64 与 arm64 镜像均可构建、启动并通过健康检查；
- PostgreSQL 和 MariaDB 各完成一次空库安装、重启和小版本升级；
- Authentik 与 LLNG 至少各完成一次 OIDC 登录、JIT 建号、应用组拒绝/允许和本地注册关闭验证；
- 浏览器验证应用发起登出及 IAM 不可用时本地 session 仍失效；
- 创建并恢复包含 project、task、comment 和 attachment 的备份；
- 用最小权限 API token 完成读写 smoke test，并验证 webhook 签名/Secret 不进入日志；
- `vikunja.service_secret`、`vikunja.oidc_client_secret` 分别完成单目标轮换，并完成一次
  `--module vikunja` 与 deployment `--all` candidate 事务；验证 IAM Provider/Vikunja 两端同步、
  Secret Store 单次提交、service-secret 会话失效影响及失败恢复 previous deployment；
- 记录 idle/load CPU、内存、首屏和 1k/10k task 样本，确认适合目标 NAS 规格。

完成 §8.1 只代表实现进入 `developing`。只有 §8.2 全部有可复现证据、升级/回滚边界已记录，
才可以把 Module 提升为 `release`。
