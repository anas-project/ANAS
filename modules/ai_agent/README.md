# AI Agent 编排

让团队在 Forgejo 的 issue 里和 AI Agent 讨论需求、把结论写进仓库，并在获得显式批准后，由 Agent
在隔离实例中改代码、跑测试、开 PR。全过程可授权、可审计、可中断、可核算。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `ai_agent` |
| 版本 / revision | `0.1.0-r1` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 组件文档

这个组件将来要拆成独立项目 `anas-agent`，因此它的全部文档都放在本目录下，拆分时整体带走：

| 文档 | 内容 |
| --- | --- |
| [编排设计](docs/architecture/orchestration-design.md) | 交互模型、身份与凭据、架构、权限、安全边界、演进路线 |
| [技术实现](docs/technical.md) | 控制面结构与安全边界 |
| [与 Forgejo 互操作的规则](docs/forgejo-interop.md) | 改 `orchestrator` 代码前要遵守的边界与约定 |
| [要求](dev-docs/requirements/ai-agent.md) | 需求矩阵与固定 `forgejo 15.0.7` 的上游事实复核 |
| [实施计划](dev-docs/plans/ai-agent.md) | 里程碑、检查表与 e2e 记录 |
| [看板应用接入 AI Agent](docs/research/kanban-integration.md) | 候选运行时与看板集成的原始调研 |
| [设计评审](dev-docs/reviews/2026-09-05-orchestration-design-review.md) | 2026-09-05 基线的评审快照 |

## 当前实现到哪一步

本 Module 按[实施计划](dev-docs/plans/ai-agent.md)分里程碑落地，**M1、M2、M3、M5 已完成**：

- M1 Module 骨架、Compose 拓扑、Hook 与配置契约；Agent 的 Forgejo 账号、token 与 SSH key 的无人值守
  发放与轮换；系统 webhook 注册、入站验签、inbox 落库、202 快返、周期对账与自触发过滤；
- M2 issue 表单模板的生成与解析、状态评论与命令、reaction 确认、回合串行、文档经 contents API 入库
  与执行依据冻结、引导 issue；
- M3 仓库权限推导、`CAP_ai_agent_*` 目录组投影、即时否决表、判定审计与作业前二次判定；
- M5 执行前预估与 `due_date` 校验、队列排序、`now`/`at`/`on`/`hold` 四种时机、工时回写、置顶队列 issue。

**尚未实现**：M4 执行面（工作实例、分支与 PR、执行 issue、取消与幂等）、M6 记录与会话视图、
M7 真实部署验收、M8 补充的交互与安全约束。M4 阻塞于 `compute` Provider 的真实宿主验收，因此本
Module 目前会讨论、出文档、判权限、排队，但**不会执行任何代码作业**。状态保持 `developing`。

## 边界

它**不**做这些事：

- 不获得宿主 Docker socket、宿主目录挂载或任何特权；
- 不在 ANAS 核心宿主上执行模型生成的代码——那只发生在经 `compute` Contract 租用的一次性实例里；
- 不把管理凭据、Agent token 或 SSH 私钥写进 issue、评论、日志、镜像或 deployment manifest；
- 不自动合并 PR，也不自动批准高风险变更。

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `forgejo` | Module | 协作面：issue、标签、评论、仓库与身份 |
| `traefik` | Module | 发布 webhook 入站地址 |
| `relational_database` | 消费的 Contract | `>=1.0.0 <2.0.0` / `postgres` |
| `compute` | 消费的 Contract（仅在 `enabled` 时） | `>=1.0.0 <2.0.0` / `incus_container`、`incus_vm` |

只消费 PostgreSQL：编排状态用到数组列、JSONB 与作业租约，为一个没有 MariaDB 部署要服务的控制面
维护第二套方言不划算。

## 开启前要准备什么

`enabled` 是唯一的功能开关，但把它打开需要四样东西同时就位，缺一 Hook 就拒绝 apply：

1. **Forgejo 管理凭据**——由 ANAS 生成并托管，Hook 在 `after_start` 通过 Forgejo 自己的托管账号入口
   创建 `anas_ai_agent` 管理员，无需人工；
2. **仓库白名单**（`repository_allowlist`）——没在名单上的仓库，事件在入站处直接丢弃；
3. **每个启用运行时的固定镜像指纹**（`agent_runtime_images`）——只接受 SHA-256，不接受 tag；
4. **`compute` 绑定**——批准后的作业得有地方跑。

```yaml
modules:
  ai_agent:
    enabled: true
    repository_allowlist: "anas-project/ANAS,anas-project/anas-agent"
    agent_runtimes: "codex,claude_code"
    agent_runtime_images: "codex=<64位指纹>,claude_code=<64位指纹>"
    execution_isolation: incus_container
```

## 身份与凭据

每个 Agent 一个独立 Forgejo 账号（`agent-<id>`），由管理端 API 无人值守创建，不用于交互登录。
token 同时限定 scope 与目标仓库，讨论期只有 `read:repository`、`write:issue`、`read:user`，写权限
只在执行阶段追加。

固定 `forgejo 15.0.7` 已复核**支持按仓库限定**，因此走原设计，不启用退化。复核同时钉死了三条边界：

- 发 token 的端点是 `POST /users/{u}/tokens`（不是 `/admin/users/{u}/tokens`，那个返回 404），
  只接受管理员 **basic auth** 加 `Sudo: <账号>` 头；
- 带仓库限定的 token 只能携带 `read:issue`、`write:issue`、`read:repository`、`write:repository`，
  多加一个 scope 就会 400，因此讨论期的 scope 集合不能再扩；
- 发 token 前 Agent 必须已是该仓库 collaborator（`read`），否则上游报"仓库不存在"。

限定作用于**仓库内容与写操作**：越界仓库的 contents 403、开 issue 404，但仓库**元数据仍可读**
（`GET /repos/{o}/{r}` 返回 200）。它是最小权限手段，不是把仓库藏起来的手段。

退化路径（"每 Agent 每仓库一个独立账号"，`agent-<id>-<仓库摘要>`）保留在 `TokenScoping` 后面，
只在未来版本回退这项能力时启用。

轮换是"先发新的、确认落库、再吊销旧的"。任何一步失败都会把刚发的凭据吊销掉，因此失败的轮换只会
留下一个可用凭据，绝不会留下双活。轮换由**凭据年龄**驱动，不靠人记得：周期对账扫到超过 30 天的
token 与 SSH key 就直接换掉。名称带代次（`anas-ai-agent-g<N>`）——上游要求 token 名与 key 标题按用户
唯一，而"先发后吊销"必然让新旧短暂共存，复用名字会让轮换在第一步就失败。

## 事件入站

Forgejo 的系统 webhook 一次注册覆盖全实例。入站按这个顺序处理，任何一步不过都不进业务逻辑：

1. 校验 `X-Forgejo-Signature`（body 的 HMAC-SHA256，常数时间比较；secret 为空一律拒绝）；
2. 仓库不在白名单 → 丢弃；
3. sender 是本部署的 Agent 账号 → 丢弃；
4. payload 里带着本编排器自己写下的 run 标记 → 丢弃（第二重自触发防护）；
5. 落 `inbox_event`，主键就是投递 ID，重复投递写不进第二行；
6. 返回 `202`。

业务处理不在请求路径内。**重复投递只产生一次外部副作用**有两道保证：投递 ID 的主键管"同一条投递
送了多次"，outbox 的幂等键管"同一件事经 webhook 和对账两条路各来一次"。

对账按仓库游标周期扫描 `updated_at`，把 webhook 漏掉的事件补回来，走的是同一条过滤与去重路径。

## 配置

见[配置参考](../../docs/reference/configuration.md)中 `ai_agent.*` 一节，以及
[技术实现](docs/technical.md)。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；
不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ai_agent.agent_runtime_images` | string | `pattern: ^(?:[a-z][a-z0-9_-]{0,31}=[0-9a-f]{64}(?:,[a-z][a-z0-9_-]{0,31}=[0-9a-f]{64})*)?$` | `""` | `static` | `AI_AGENT_AGENT_RUNTIME_IMAGES` | 否 | 否 | 否 | 是 | `container_recreate` | 每个启用运行时的固定 SHA-256 镜像指纹；不接受 tag |
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

### 查询和修改

```bash
anas config list ai_agent -w /srv/anas
```

```bash
anas config set ai_agent.repository_allowlist "anas-project/ANAS" -w /srv/anas
```

## 语言与时区

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.1.0-r1`（reviewed 2026-09-05）
- Timezone / 时区：`configured` — The orchestrator inherits ANAS TZ and renders every timestamp it writes into a status comment, a schedule or an audit record in that zone; the database stores UTC.
- Language scope / 语言范围：the orchestrator's own writing -- status comments, refusal and downgrade explanations, and generated issue form templates
- Selection / 选择方式：`deployment_default`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：ANAS canonical language tag
- Fallback / 回退：An unsupported value warns and falls back to English. This setting does not constrain the language a model replies in; that follows the issue's own conversation.
- Supported languages / 支持语言（2）：`en`, `zh-CN`
- Notes / 说明：The two values are the languages the orchestrator's own strings exist in. Agent replies are model output and are not translated by this module.

Evidence / 证据：

- [0.1.0 — agentLanguages](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/hook/main.go)
<!-- generated:localization:end -->
