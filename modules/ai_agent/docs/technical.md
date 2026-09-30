# AI Agent 编排技术实现

## 共享源码与构建传输设置

orchestrator 通过命名 `shared` 上下文编译当前仓库的 compute 类型与校验，不能静默从网络
取另一份 ANAS 共享实现。staging 必须用 `ANAS_SHARED_BUILD_CONTEXT` 指向匹配版本的绝对
源码根；`check-shared-build --json` 只核对输入字节/权限与声明，不读运行凭据、不执行 Docker。

Go 与 Alpine 运行层均接入既有 `DOCKER_HUB_REGISTRY`，显式 `GO_BUILDER_REGISTRY` 只覆盖
Go 层。pgx 等已声明第三方模块通过 `GO_MODULE_PROXY` 获取，缺省回退到 `GOPROXY_URL` 与
官方源，保留 `go.sum` 和 checksum database。代理与镜像源都是构建设置，不进入服务的
runtime environment，不携带代理凭据，也不改变租约和 guest 的权限。

独立 VM 构建门禁的范围见仓库 `test-env/fixtures/incus-shared-build/README.md`：源码和
build-only staging 分别实构并比较最终二进制。镜像可编译不代表 agent runtime、外部模型
调用、compute 作业或完整 Core `build/apply` 已验收，不据此开放尚未完成的运行路径。

本文记录 `ai_agent` Module 的控制面结构与安全边界。配置与操作见[中文 README](../README.md)，
验收依据见[需求矩阵](../dev-docs/requirements/ai-agent.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `0.1.0-r2` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose 拓扑

编排服务经 `work_instances` Resource 的控制网络投影连接宿主供给的 external bridge，不获取
Docker socket 或宿主服务权限。数据库连接仍独立，`traefik` 网络以 `gw_priority: 1` 保持默认
业务出口；要求 Compose 2.33.1+。控制桥连通、证书隔离与真实作业生命周期仍需实机验收。

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ai_agent` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-ai-agent:0.1.0-r2` | `db, traefik, compute-control` | 2 |
<!-- generated:compose-topology:end -->

只有一个常驻服务。它 `read_only`、`cap_drop: ALL`、`no-new-privileges`、以 `65532` 运行，
没有宿主 Docker socket、没有 ANAS 数据树以外的挂载。唯一的可写路径是会话持久卷，用来让容器重建后
还能续接会话。Forgejo 走它自己发布的 HTTPS 地址，用挂进来的内部 CA 校验——两个容器之间没有私网旁路。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ai_agent.agent_runtime_images` | string | `format: json_object` | `""` | `static` | `AI_AGENT_AGENT_RUNTIME_IMAGES` | 否 | 否 | 否 | 是 | `container_recreate` | runtime id → 结构化镜像引用；Core 冻结映射 |
| `ai_agent.agent_runtimes` | string | `pattern: ^(?:[a-z][a-z0-9_-]{0,31}(?:,[a-z][a-z0-9_-]{0,31})*)?$` | `""` | `static` | `AI_AGENT_AGENT_RUNTIMES` | 否 | 否 | 否 | 是 | `container_recreate` | 启用哪些 Agent 运行时；标签、issue 模板与能力组都由注册表据此生成 |
| `ai_agent.daily_budget_usd` | int | `0..100000` | `20` | `static` | `AI_AGENT_DAILY_BUDGET_USD` | 否 | 否 | 否 | 是 | `reconcile` | 部署级每日花费上限；超限中断作业并回写说明 |
| `ai_agent.db_name` | string | — | `ai_agent` | `static` | `AI_AGENT_DB_NAME` | 否 | 否 | 否 | 否：`migrate-ai-agent-database` | `data_migrate` | 编排状态所在的数据库名 |
| `ai_agent.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AI_AGENT_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-ai-agent-database` | `data_migrate` | 编排状态的数据库接口，只支持 postgres |
| `ai_agent.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `agent` | `static` | `AI_AGENT_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | webhook 入站地址的主机名前缀；改它需要重新登记 webhook |
| `ai_agent.egress_allowlist` | string | `pattern: ^(?:[a-z0-9.*-]{1,253}(?::[0-9]{1,5})?(?:,[a-z0-9.*-]{1,253}(?::[0-9]{1,5})?)*)?$` | `""` | `static` | `AI_AGENT_EGRESS_ALLOWLIST` | 否 | 否 | 否 | 是 | `reconcile` | 工作实例唯一可以访问的出网目的地，其余一律拒绝 |
| `ai_agent.enabled` | bool | — | `false` | `static` | `AI_AGENT_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 唯一功能开关；打开需要管理凭据、仓库白名单、固定镜像与 compute 绑定同时就位 |
| `ai_agent.execution_isolation` | enum (`auto`, `incus_container`, `incus_vm`) | — | `auto` | `static` | `AI_AGENT_EXECUTION_ISOLATION` | 否 | 否 | 否 | 是 | `container_recreate` | 向 compute Provider 申请的隔离档：系统容器共享宿主内核，VM 有独立 guest kernel |
| `ai_agent.job_wallclock_minutes` | int | `1..1440` | `60` | `static` | `AI_AGENT_JOB_WALLCLOCK_MINUTES` | 否 | 否 | 否 | 是 | `reconcile` | 单个作业的墙钟硬上限；超限中断 |
| `ai_agent.language` | string | — | — | `inherited` | `AI_AGENT_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 编排器写入 issue 的状态评论与说明所用的语言 |
| `ai_agent.reconcile_interval_seconds` | int | `30..86400` | `300` | `static` | `AI_AGENT_RECONCILE_INTERVAL_SECONDS` | 否 | 否 | 否 | 是 | `reconcile` | 对账扫描的间隔，用来补回 webhook 漏掉的事件 |
| `ai_agent.repository_allowlist` | string | `pattern: ^(?:[A-Za-z0-9._-]{1,64}/[A-Za-z0-9._-]{1,100}(?:,[A-Za-z0-9._-]{1,64}/[A-Za-z0-9._-]{1,100})*)?$` | `""` | `static` | `AI_AGENT_REPOSITORY_ALLOWLIST` | 否 | 否 | 否 | 是 | `container_recreate` | 参与编排的仓库；不在名单上的事件在入站处直接丢弃 |
| `ai_agent.workspace_scope` | enum (`repo`, `job`) | — | `repo` | `static` | `AI_AGENT_WORKSPACE_SCOPE` | 否 | 否 | 否 | 是 | `container_recreate` | 每仓库一个常驻工作实例，或每作业一个一次性实例 |

十四项全部经 `.env` 进入控制面容器。`enabled` 是唯一功能开关，Hook 在 apply 早期就把它的四个前置
条件判掉；其余参数要么是入站与执行的边界（白名单、镜像指纹、隔离档），要么是硬上限（预算、墙钟）。

## 控制面分层

| 文件 | 职责 |
| --- | --- |
| `config.go` | 读环境、解析仓库与镜像指纹、失败即拒；`enabled` 的全部前置条件都在这里判 |
| `registry.go` | Agent 运行时注册表：模型、原生思考强度、能力、镜像、能力组名 |
| `forgejo.go` | 管理端 API：建账号、发 token、加 SSH key、注册系统 webhook、对账读取 |
| `ingress.go` | 验签、白名单、自触发过滤、落 inbox、返回 202 |
| `reconcile.go` | 周期对账与 outbox 幂等键 |
| `bootstrap.go` | 身份发放与轮换、webhook 登记 |
| `postgres.go` / `store.go` | 权威状态：inbox、outbox、身份、webhook、游标、审计 |
| `redact.go` | 出站脱敏 |

## 为什么控制面代码不认识运行时的名字

`registry.go` 是唯一写着 `codex`、`claude_code`、`pi` 的文件，其他文件一律通过 `Runtime` 结构体
取值。新增一个运行时只需要三件事：加一条注册表条目、实现适配器、提供固定 fingerprint 的镜像；
标签、issue 模板、能力组名（`CAP_ai_agent_<id>`）都由注册表生成。

思考强度**不做统一词表**。各运行时的档位名称与档数不同，强行映射会造出某个运行时上不存在的档位，
也会让人以为跨 Agent 的 "high" 是同一件事。因此 `EffortLevels` 存的是运行时自己的原生取值，非法
取值被拒绝时会把可选项列出来。不分级的运行时（`pi`）把这个字段留空，模板就不显示该项。

单元测试 `TestControlPlaneDoesNotBranchOnRuntimeName` 直接扫描包内源码，任何在 `registry.go` 之外
出现的运行时 id 都会让测试失败。

## 从固定版本上学到的、写进代码的五条

这些不是设计推导出来的，是对 `forgejo 15.0.7` 实测出来的（结论见
[要求文档 §15](../dev-docs/requirements/ai-agent.md)）。每一条都推翻了实现里的一个假设：

| 上游事实 | 代码里的落点 |
| --- | --- |
| 没有 `/admin/users/{u}/tokens`，发 token 只在 `/users/{u}/tokens`，且拒绝 token 认证 | `tokenPath` + `sudo()`；管理凭据因此必须是口令 |
| `repositories` 是 `[{owner,name}]` 对象数组，字符串会 unmarshal 失败 | `ForgejoTarget` |
| 带仓库限定的 token 只能配 issue/repository 两族 scope | `discussionScopes` 与 `repositoryScopedScopes` 的一致性测试 |
| token 名与 key 标题按用户唯一 | `tokenNameFor` / `keyTitleFor` 带代次 |
| `GET /admin/hooks` 返回空数组，但按 id 取得到；服务端还会展开事件族 | `SystemHook(id)` 判存在，只按 URL 与指纹判漂移 |

单元测试里的 `fakeAdmin` 复现了后三条（名称唯一、空列表、事件展开），所以这些不是"跑真实环境才发现
得了"的问题——现在本地就会红。

## 幂等：两把锁管两件不同的事

一次外部副作用只发生一次，靠的是两个不同的键，因为要防的是两件不同的事：

| 要防的 | 键 | 在哪 |
| --- | --- | --- |
| 同一条投递被送了多次 | `X-Forgejo-Delivery` → `inbox_event` 主键 | `Ingress.Accept` |
| 同一件事经 webhook 与对账各来一次 | `<仓库>#<issue>:<种类>:<判别式>` → `outbox_write` 主键 | `Outbox.Do` |

只有前者会漏掉后者：对账重建出来的事件带的是自己的确定性投递 ID，和 webhook 的投递 ID 不同，
必然写进 inbox；真正把外部写操作收敛成一次的是 outbox 的幂等键。反过来只有后者也不行——那样每次
重复投递都会白跑一遍处理逻辑。两个都要。

对账的投递 ID 由 `仓库 + issue 号 + updated_at` 推出，因此对同一个没变化的 issue 扫两遍产生的是
同一个 ID，会被 inbox 去重；用随机 ID 会让每次扫描都变成一场事件风暴。

## 轮换为什么是"先发后吊销"

顺序是：发新凭据 → 确认落库 → 吊销旧凭据。任何一步失败都会把刚发的那个吊销掉。

触发它的是 `Bootstrapper.Reconcile` 里的年龄判断，不是外部调度：`AGENT-R-007` 要求轮换无人值守，
而"有个入口可以调用"不等于"会被调用"。

这个顺序的取舍是明确的：最后一步（吊销旧的）失败会留下旧凭据仍然可用，下一轮对账能看见并收敛；
而反过来先吊销再发新，中间任何失败都会留下一个**没有任何可用凭据**的账号，需要人工介入。宁可多
一个待收敛的旧凭据，不要一个断掉的身份。要绝对避免的是双活，所以失败路径一定回收新凭据。

## 脱敏是值级的，不是调用点级的

`Redactor` 持有已知的敏感值，任何要出去的字符串——日志、错误、审计 reason——都过一遍替换。这样做
而不是"在每个调用点小心一点"，是因为后者不是一种强制：HTTP 传输错误会回显请求 URL，数据库驱动会
打印连接串，两者都不在人会想到的调用点上。短于 8 字节的值不进脱敏表：那种长度的值本来就猜得出来，
把它加进去只会把无关文本一起抹掉。

## 目录属性变更的实现侧

与 README 的《目录属性变更说明》一一对应。只讲**人**；Agent 机器账号不来自目录。

- **身份存在哪张表/哪个字段**：`orchestrator/postgres.go` 的四处，键都是 Forgejo 用户名字符串：
  `agent_grant.username`（PRIMARY KEY）、`agent_grant_deny.username`（与 `agent` 组成唯一索引
  `agent_grant_deny_subject`）、`policy_override.username`、`audit_record.subject`。
- **匹配键是怎么来的**：`orchestrator/ingress.go` 的 webhook envelope 只解出 `sender.login`。
  编排器不解析 ID Token，也不接 IAM binding——它对人的全部认知经由 Forgejo 转手。
- **什么时候重新派生**：`PolicyEngine.grant` 保存内存快照，超过 TTL 后调用
  `Permissions.UserTeams(ctx, user)` 重读 Forgejo team 名，交给 `GrantFromTeams` 重新计算上限并
  `SaveGrant` 回写。`agent_grant` 因此是**派生快照，不是事实来源**——`Grant.Source` 字面写着
  `"directory groups projected into Forgejo teams"`。
- **撤权经哪个接口**：`PolicyEngine.Veto` 写 `agent_grant_deny` 并丢弃与之矛盾的缓存上限，
  对应 `agent-grant deny` 命令。没有任何目录侧接口能直接触发它。
- **对账或事件订阅路径**：编排器**需要**目录事件即时性（组撤权秒级生效，见
  `docs/architecture/orchestration-design.md` §6.2），但 `forgejo` 是 OIDC-only、不持有目录副本，
  组只能在登录时经 claim 到达 team。即时否决表就是为填这个洞而存在的**人工快路径**。
- **技术阻碍**：**缺一个能到达编排器的不可变人员标识符**。Forgejo 的 OAuth2 source 把 OIDC
  `sub` 存进 `login_name`，但 webhook payload 里没有这个字段，管理端 API 才回显它；因此要让
  编排器按 anchor 认人，需要在收到 webhook 后额外调一次管理端用户查询把 `login` 换成
  `login_name`。这条路在 `DIRKEY-R-008` 落地后才有意义——在那之前 `login_name` 里装的是
  Provider 内部 id，同样不能直接与目录对账。

**`DIRKEY-R-002` 符合性：不符合，缺口已按 `DIRKEY-R-004` 声明。** 持久授权键是 Forgejo 用户名，
一个目录标签。本 Module **没有**用"按用户名回退匹配"冒充满足要求，而是显式记录了后果与兜底
（见 README）。整改归[目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)
M2 之后：`sub` 变成 anchor、`login_name` 里装的就是 anchor 之后，编排器才可能把授权键换掉。
按 `DIRKEY-R-005`，每次变更 `forgejo` 固定版本时复核 webhook 是否开始携带稳定标识符。

**`DIRKEY-R-006` 例外的适用范围**：`agent_grant_deny` 与 `policy_override` 只走撤权方向
（`Override` 的注释写明"final action set is an intersection"，`AGENT-R-031`/`R-036`），因此允许
按标签匹配；误判后果是误撤权，恢复由管理员人工重建条目，**代码里没有自动恢复路径**。
`agent_grant` 是授权方向，不适用该例外，它靠 TTL 过期后从当前 team 重新派生来限制暴露窗口。

**`DIRKEY-R-013` 投影结论：不适用（本 Module 不是 OIDC/SAML Consumer）。** `module.yml` 不声明
`iam`，编排器不消费任何主体标识符，M2 切换不会让 UUID 出现在它的任何界面或路径里。它受 M2 影响
的方式是间接的：切换会改变 `forgejo` 的 `login_name` 内容，从而**打开**上面那条目前走不通的
"按 anchor 认人"路径。

## 与需求矩阵的对应

| 需求 | 落点 |
| --- | --- |
| `AGENT-R-001` | `module.yml` 固定 `0.1.0-r1` 镜像 tag，`status: developing` |
| `AGENT-R-002` | `docker-compose.yml`：无 socket、无宿主挂载、`cap_drop: ALL`、非 root |
| `AGENT-R-003` | `postgres.go` 的 schema 与 `store.go` 的 `Store` 接口；进程内不留权威状态 |
| `AGENT-R-004` | `hook/main.go` 的 `validateEnabled` 与 `config.go` 的 `requireEnabledPreconditions` |
| `AGENT-R-005` | `bootstrap.go` `EnsureUser` |
| `AGENT-R-006` | `discussionScopes` + `CreateToken(repositories)` + `EnsureCollaborator`；退化路径 `ScopingPerRepoAccount` 保留未启用 |
| `AGENT-R-007` | `bootstrap.go` `Rotate` 与 `abandon` |
| `AGENT-R-008` | Hook 的 `adminAccount`，只在控制面容器内使用 |
| `AGENT-R-009` | `EnsureWebhook`：按 id 判存在、按 URL 与密钥指纹判漂移 |
| `AGENT-R-010` | `redact.go`，以及 Hook 不回显 `local-admin` 输出 |
| `AGENT-R-011` | `ingress.go` `serveWebhook` |
| `AGENT-R-012` | `inbox_event` 主键 + `RecordDelivery` |
| `AGENT-R-013` | `reconcile.go` `Sweep` |
| `AGENT-R-014` | `ByAccount` 过滤 + `RunIDFromPayload` 二次去重 |

## compute 镜像配置冻结

镜像配置改为结构化对象：Forgejo 为单对象，AI Agent 为 runtime→对象映射。Core 通过显式
`spec_from` 投影，在 Hook calculate 前解析。运行时容器只接收冻结摘要：Forgejo 读取租约
allowlist，AI Agent 读取 JSON 镜像绑定。Agent Hook 使用与 manifest 参数一致的
`AI_AGENT_AGENT_RUNTIMES`，Compose 向 orchestrator 映射为 `AI_AGENT_RUNTIMES`。本轮未引入新依赖。

Incus 的 `image_architecture` 必须显式描述目标 daemon。受信 bundle 目录当前为空；ensure 在
登记信任前检查租约 project 中现有镜像的 fingerprint、架构与类型，缺失直接失败，不查询 alias
或重建。自动导入/烘焙仍待实现。快照及回滚语义见 [compute 契约](../../../contracts/compute/docs/technical.md)。

## 租约命名密钥生命周期

Core 已接入独立的 32 字节 compute `LEASE_SECRET`，与客户端证书分开生成和复用。Deployment 与
resource state 只保存引用；消费者接收敏感 base64 投影，备份恢复保留同一密钥，不参与凭据轮换。
详见 [compute 生命周期契约](../../../contracts/compute/docs/technical.md#独立租约命名密钥)。
专属轮换命令和生产 HTTP 发布仍待实现。
