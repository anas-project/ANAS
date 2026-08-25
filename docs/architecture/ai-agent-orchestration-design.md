# AI Agent 编排设计（Forgejo 基线）

> 状态：**提案**。本文描述的 Module、配置项、命令与表结构当前**均不可执行**。
> 基线已从 Vikunja 改为**已集成的 `forgejo` Module**：协作面用 Forgejo 的 issue、label 与
> Projects 看板，代码面用同一实例的仓库。更新：2026-08-23。

上一版以 Vikunja 为看板基线，核验后被推翻，理由见 §2。候选比较的原始证据见
[看板应用接入 AI Agent](/research/kanban-ai-agent-integration-research)（2026-08-15）。执行面的
Provider 工作见 [Incus compute Provider 要求](/requirements/incus-module)与[实施计划](/plans/incus-module)。
Forgejo 侧的既有边界见 [Forgejo Module 设计](/architecture/forgejo-module-design)。

## 1. 结论

**在 Forgejo 里做，明显优于跨应用方案。** 协作对象（issue）、控制信号（label、指派、评论）、
代码、分支、PR、CI 与身份**在同一个应用、同一套权限里**，编排器不再需要跨应用身份映射、仓库
绑定流程和第二套授权模型——上一版设计里最复杂的三块（两级授权、绑定与权限复核、凭据引导）
大部分直接消失。

形态：`ai_agent` Module（独立开源项目 `anas-agent` 打包）订阅 Forgejo 系统 webhook，以每个 Agent
一个 Forgejo **bot 账号**的身份参与 issue 讨论；执行阶段在一次性隔离实例中改代码、跑测试、推
`ai/*` 分支并开 PR。

三条决定性的能力差异（相对 Vikunja 基线）：

| 能力 | Forgejo | Vikunja 2.4.0 |
| --- | --- | --- |
| 无人值守发放身份与凭据 | `POST /admin/users` 建账号、`POST /admin/users/{u}/tokens` 发 token、`POST /admin/users/{u}/keys` 加 SSH key，**全部管理端 API，可脚本化、可轮换** | token 只能用用户会话 JWT 签发；无人值守必须打开全局本地口令登录 |
| token 作用域 | scope（`read:issue`、`write:repository`…）**且可限定到具体仓库**（`repositories`） | 只有路由组，无项目维度 |
| 控制标签 | 标签是**仓库/组织级共享对象**，有写权限的人都能打，且有 `issue_label` 事件 | 标签是用户私有对象，不能假设人人可打 |

代价只有一条：**Forgejo 的 Projects 看板没有 API 也没有 webhook**（§3），因此看板是人类视图，
机器可读的状态必须落在 label、issue 开闭与指派上。

## 2. 为什么推翻 Vikunja 基线

核验 Vikunja `2.4.0` 得到的三个阻塞，在 Forgejo 上全部不存在：

1. **凭据引导**：Vikunja 的 API token 只能由用户会话签发，CLI 没有 token/bot 命令，因此无人值守
   要么打开全局本地口令登录，要么按 IAM Provider 定制登录流程（Core 禁止的 Provider 分支）。
2. **控制面入口**：Vikunja 标签是用户私有对象，标签驱动的授权无法保证项目成员都能使用。
3. **富文本**：Vikunja 存 HTML，Markdown 交换有损，回写需要“只发改过的字段”这类补偿逻辑。

除此之外，跨应用方案还固有地带来：Vikunja↔Forgejo 身份映射（Forgejo 不消费 `anasIdentityAnchor`）、
仓库绑定与权限复核、两套权限模型的对齐、两套事件与对账。这些都是**跨应用产生的成本**，不是
AI Agent 本身需要的复杂度。

`vikunja` Module 保持现状，继续作为通用任务与项目管理应用；它不承担 Agent 编排。

## 3. Forgejo 事实核验

证据来自 Forgejo 官方文档、`forgejo` 分支源码 `modules/webhook/type.go` 与 Codeberg 实例的
OpenAPI（`16.0-dev`），核验日期 2026-08-23。ANAS 固定 `15.0.7`，**实施前必须在固定镜像上复核**（§12）。

| 事实 | 说明 |
| --- | --- |
| Webhook 事件常量 | `create`、`delete`、`fork`、`push`、`issues`、`issue_assign`、`issue_label`、`issue_milestone`、`issue_comment`、`pull_request*`（含 review/sync/assign/label）、`wiki`、`repository`、`release`、`package`、`schedule`、`workflow_dispatch`、`action_run_success/failure/recover` |
| **无 Projects/看板事件与 API** | 事件常量里没有 project/column；REST API 里没有任何 `project`/`board`/`column` 路径。Projects 是纯 UI 功能 |
| 投递头与签名 | `X-Forgejo-Event`、`X-Forgejo-Delivery`、`X-Forgejo-Signature`（body 的 HMAC-SHA256），同时发 GitHub/Gitea/Gogs 兼容头 |
| Webhook 层级 | 仓库级 `/repos/{o}/{r}/hooks`、组织级 `/orgs/{org}/hooks`、**系统级 `/admin/hooks`**（一次注册覆盖全实例）、用户级 `/user/hooks` |
| 无人值守身份 | `POST /admin/users` 建账号、`POST /admin/users/{u}/tokens` 发 token（可列出与吊销）、`POST /admin/users/{u}/keys` 加 SSH key |
| Token 作用域 | `scopes`（如 `read:issue`、`write:repository`、`read:user`）**加** `repositories`：只对指定仓库生效 |
| 标签 | 仓库级 `/repos/{o}/{r}/labels` 与组织级标签，完整 CRUD；issue 标签 `POST/PUT/DELETE .../issues/{i}/labels`；变更触发 `issue_label` |
| 评论 | 创建、**编辑（`PATCH .../issues/comments/{id}`）**、附件（`.../comments/{id}/assets`）、**反应（`.../reactions`）** |
| 指派 | `issue_assign` 事件；issue 可指派给 Agent 账号 |
| Markdown | issue 与评论原生 Markdown，无 HTML 往返转换 |
| Actions | `POST /repos/{o}/{r}/actions/workflows/{file}/dispatches` 手动派发；`action_run_*` 事件回报结果 |
| 权限模型 | 仓库权限 read/write/admin + 组织 team；`GET /repos/{o}/{r}/collaborators/{u}/permission` 可查询 |

## 4. 交互模型

一个 issue 就是一个 topic。人与 Agent 的全部交互发生在 issue 里：

| 人的动作 | 事件 | Agent 行为 |
| --- | --- | --- |
| 新建 issue（`auto` 模式的仓库/组织） | `issues` | 读正文 → 理解摘要 + 待澄清问题 + 验收标准草案（一条评论） |
| 指派 `agent-codex` | `issue_assign` | 接单，同上；取消指派 = 撤回并中断作业 |
| 评论 `@agent-codex …` | `issue_comment` | 续接同一会话回复 |
| 打标签 `ai:plan` | `issue_label` | 冻结 issue 快照与 commit SHA，产出方案文档评论 |
| 打标签 `ai:approved` | `issue_label` | 通过授权判定后入队执行 |
| 打标签 `ai:cancel` / 关闭 issue | `issue_label` / `issues` | 中断作业、销毁实例、回写终态 |
| PR 上评论 `@agent-codex 修改…` | `pull_request_comment` | 在同一分支续做修订 |

Agent 用 **reaction（👀 / 🚀 / ✅）确认收到**，避免每个动作都刷一条评论；实质进展写入它自己维护的
**一条可编辑状态评论**（`PATCH` 更新，不新建）：

```markdown
**Agent 状态** · codex · chat=claude / exec=codex
阶段：执行中（2/5）· 分支 `ai/142-v2` · 预算 0.42 / 2.00 USD · 已用 6 分钟
最近：`go test ./internal/...`
完整日志见附件 run-142-v2.log
```

**Projects 看板的处置**：没有 API 也没有事件，Agent 无法读或移动卡片。因此：

- 机器可读状态一律落在 **label + issue 开闭 + 指派**；
- Projects 看板作为人类视图，由人拖动，或用 Forgejo 自身“关闭即入 Done 列”的行为；
- 不设计任何依赖看板列的自动化。若上游后续提供 Projects API，可作为展示增强接入，不改状态机。

### 4.1 标签词表

标签由 ANAS 在启用的仓库/组织中创建（组织级标签可一次覆盖全部仓库）：

| 标签 | 含义 | 谁能打 |
| --- | --- | --- |
| `ai:auto` / `ai:manual` | 本 issue 的参与模式覆盖 | write |
| `ai:silent` | 让 Agent 退出本 issue | write |
| `ai:plan` | 冻结快照并产出方案 | write（plan 权限） |
| `ai:approved` | 批准执行（授权点） | maintain/admin（execute 权限） |
| `ai:cancel` | 中断当前作业 | write |
| `ai:chat/<agent>`、`ai:exec/<agent>` | 指定聊天/执行 Agent | write |
| `ai:branch/<name>`、`ai:target/<branch>` | 工作分支 / 目标分支 | write |
| `ai:pr` / `ai:direct` | 完成后开 PR / 直接提交到目标分支 | `ai:direct` 需仓库策略显式允许 |
| `ai:running`、`ai:needs-review`、`ai:failed` | Agent 维护的状态标签 | 仅 Agent 账号 |

评论命令（`@agent-codex /plan`、`/approve`、`/stop`、`/chat claude`、`/exec codex`、`/branch`、
`/budget`）与标签等价，供不想翻标签列表的人使用；两者都要过同一套授权判定。

## 5. 身份与凭据

每个 Agent 一个 Forgejo 账号（`agent-codex`、`agent-claude`、`agent-pi`），由 ANAS 在
`ai_agent` 的 reconcile 阶段通过管理端 API **无人值守**创建与维护：

1. `POST /admin/users` 建账号（口令为随机占位值，账号不用于交互登录）；
2. `POST /admin/users/{agent}/tokens` 发 token：`scopes` 取 `write:issue`、`read:repository`
   （执行阶段另发 `write:repository`），`repositories` 限定到**已启用的仓库**；
3. 需要 git 推送时 `POST /admin/users/{agent}/keys` 下发独立 SSH key；
4. 把账号按需加为仓库 collaborator（`write`）或加入组织 team；
5. 轮换 = 发新 token → 写 Secret Store → `DELETE` 旧 token，全流程无人工。

这解决了上一版最难的一块：**凭据引导与轮换完全自动**，不需要打开任何降级的认证开关。

管理端 API 需要一个 Forgejo **管理凭据**，由 `ai_agent` Module 持有并只用于上述动作；它不进入
执行实例，也不用于日常 issue 读写（日常用各 Agent 自己的受限 token）。该凭据权限高，必须单独
审计、单独轮换，并在文档中写明。

## 6. 架构

### 6.1 组件

```text
┌──────────────────────────── ANAS deployment ─────────────────────────────┐
│ traefik ──▶ anas_forgejo   issue / label / comment / PR / Projects        │
│               │      ▲                                                    │
│    系统 webhook│      │ REST（各 Agent 的受限 token）                      │
│               ▼      │                                                    │
│ traefik ──▶ anas_ai_agent                                                 │
│      ├── ingress   验签 → inbox → 202                                     │
│      ├── policy    §7 授权判定（deny by default）                          │
│      ├── orchestr. 状态机 / 计划 / 预算 / 租约 / 记录                      │
│      ├── chat runtime  常驻容器 ×N（每 Agent 一个，只读）                   │
│      └── outbox    评论 / 状态评论 / 标签 / 反应 / 附件                    │
│      ├── PostgreSQL（inbox/policy/plan/job/run_event/usage）               │
│      ├── llm_gateway（可选：虚拟 key、预算、审计）                          │
│      └── compute Contract ──▶ incus Provider ──▶ Incus 宿主                │
└───────────────────────────────┬──────────────────────────────────────────┘
                                ▼
        一次性执行实例（默认 LXC，可选 VM）：worktree + Agent CLI
        exec_stdin 注入短时 token/SSH key → tmpfs，作业后销毁
```

凭据边界不变：**协作凭据只在控制面，执行凭据只在执行面**。Agent 进程看不到管理凭据，也看不到
其他仓库的 token；所有 issue 写操作经控制面 outbox。

### 6.2 执行面 Provider

沿用既有决定：`compute` Contract 已定义一次性实例生命周期，把 Forgejo Actions controller 内嵌的
Incus 客户端提取为独立 `incus` Provider Module，Forgejo Actions 与 `ai_agent` 都作为消费者，各自
绑定独立 restricted project 与证书。范围与验收见[要求](/requirements/incus-module)与[计划](/plans/incus-module)。

### 6.3 隔离档

| 档 | 边界 | 资源 | 适用 |
| --- | --- | --- | --- |
| `container`（常驻） | 命名空间隔离 | 按需，亚秒启动 | 聊天、计划（不执行仓库代码） |
| `lxc`（**默认执行档**） | 共享宿主内核 + user namespace + seccomp/AppArmor | 无内存预留，秒级启动，I/O 接近原生 | 改代码、装依赖、跑测试 |
| `vm` | 独立 guest kernel + 硬件虚拟化 | 需预留内存，数秒启动 | 需要硬边界：外部贡献者仓库、不受信依赖 |

本质差别只有共享内核这一条；其余隔离手段两档都有，Incus 对二者使用同一套 project/quota/exec 接口。
系统容器 interface 已登记为 [Incus 计划](/plans/incus-module) M5。

### 6.4 执行后端：自建实例 vs Forgejo Actions

| 后端 | 做法 | 取舍 |
| --- | --- | --- |
| `compute`（默认） | 控制面直接创建一次性实例，注入短时凭据并运行 Agent CLI | 队列、预算、租约、取消、夜间窗口都由控制面掌控；日志进 ANAS 记录体系 |
| `actions`（可选） | 用 `workflow_dispatch` 派发仓库内固定 workflow，复用既有 Runner | 复用已有隔离与日志 UI，零新增执行组件；但 **workflow 文件在仓库里，有写权限的人都能改它运行什么**，且 Actions secrets 会暴露给该 workflow；调度、预算与取消语义弱 |

首期只做 `compute`。要开 `actions` 后端，必须同时满足：只允许派发**默认分支**上的 workflow、
默认分支受保护、模型凭据不以 Actions secret 形式存在（改为控制面签发的短时虚拟 key）。

### 6.5 Agent 运行时

聊天与计划用**常驻容器**（每 Agent 一个，空闲 30 分钟回收，下次自动拉起），执行才用一次性实例；
常驻必须防状态串味：独立工作目录与缓存目录、轮次结束清凭据、不共享临时文件、容器重启不影响
正确性（会话是缓存不是事实）。可选预热池消除执行冷启动。

Agent 注册表字段（镜像 fingerprint、CLI 版本、认证方式、chat/exec 模型、并发与预算、能力集、
隔离档、状态）、preflight、熔断、凭据过期监控、版本升级即配置变更（禁止运行时自动升级 CLI）与
订阅席位信号量等规则，与基线变化无关，沿用上一版结论。

## 7. 权限模型

Forgejo 的仓库权限就是访问权，**不再需要第二套访问模型**。策略层只回答“谁能让哪个 Agent 做哪一类
动作、花多少钱”。

### 7.1 三层

| 层 | 来源 | 说明 |
| --- | --- | --- |
| L0 访问权 | Forgejo 仓库权限与组织 team | Agent 账号被加进哪些仓库、token `repositories` 限定到哪些仓库；两者取交集 |
| L1 触发权 | 默认由仓库权限推导，可覆盖 | 谁能让 Agent 做什么 |
| L2 执行权 | 仓库策略 | 允许的分支模式、是否允许直接提交、是否必须开 PR、预算与并发 |

默认推导（`sync: mirror`，可逐条覆盖，也可 `sync: off` 全手动）：

| Forgejo 仓库权限 | 默认动作集 |
| --- | --- |
| read | `reply` |
| write | `reply`、`plan` |
| maintain / admin | `reply`、`plan`、`execute` |

于是三种常见情形自然成立：仓库管理员有 Agent 使用权 → 在自己仓库里完整配置；管理员无权而某个
成员有权 → 该成员只能用聊天能力，`ai:approved` 不被接受；都无权 → Agent 不响应。

部署级仍保留一层 `agent_grant`：某个用户是否被允许使用 Agent、上限动作等级是什么。仓库侧的推导
结果与它取交集——仓库权限不能凭空造出 Agent 使用权。

### 7.2 判定顺序

```text
webhook 事件
  → 解析 sender、repo、issue、目标 agent、请求动作
  → 仓库是否已启用 Agent？（否 → 丢弃）
  → 部署级 agent_grant 覆盖 sender 与该 agent？（否 → deny）
  → 仓库权限推导 ⊕ 覆盖条目 → 动作集，与部署级上限取交集
  → issue 正文/评论中的配置只能收窄
  → 动作 ∈ 集合？（否 → deny，回一条可解释评论）
  → execute：检查分支模式、目标分支保护状态、预算与并发
  → 建 job，判定依据写入 decision 表；作业开始前再判定一次
```

### 7.3 执行权细则

| 项 | 默认 | 可选 |
| --- | --- | --- |
| 工作分支 | 新建 `ai/<issue>-<plan-version>` | `ai:branch/<name>` 指定 |
| 目标分支 | 仓库默认分支 | `ai:target/<branch>` |
| 直接提交目标分支 | 禁止 | `ai:direct`，需仓库策略开启且目标分支未受保护 |
| 完成动作 | 开 PR，issue 打 `ai:needs-review` | `/no-pr` 直接合并（同上受限） |
| 合并 | 人工 | Agent 自审留评论；低风险类别可按策略自动合并 |
| commit 身份 | Agent 账号（author/committer），trailer 记录发起人与 issue | 可配置展示名 |

**不使用发起人的凭据代跑**：Forgejo 的推送与活动流按凭据所有者归属，冒用会污染审计，且该凭据
携带此人全部仓库权限；改 commit 身份只是展示层，补偿不了凭据层。Agent 一律用自己受限到该仓库的
token/SSH key。仓库权限变化由 Forgejo 自己收敛——这正是同应用方案的红利：**不存在跨应用绑定漂移**。

### 7.4 数据形态

```sql
agent_grant(id, forgejo_user_id, username, agent NULL, max_action, granted_by, created_at)

repo_settings(repo_id PRIMARY KEY, enabled, sync_mode, trigger_mode,
              chat_agent, exec_agent, allow_direct_commit, branch_pattern,
              daily_budget_usd, max_concurrent, updated_by, updated_at)

policy_override(id, repo_id, issue_number NULL, agent NULL, user_id NULL,
                actions TEXT[], created_by, created_at)
```

不再需要 `repo_binding` 与身份锚点映射表：issue 天然属于仓库，sender 天然是 Forgejo 用户。

## 8. 事件入站

- **一次系统 webhook 覆盖全实例**（`POST /admin/hooks`），订阅 `issues`、`issue_assign`、
  `issue_label`、`issue_comment`、`pull_request`、`pull_request_comment`、`action_run_*`；
  也可退化为按组织或按仓库注册，由 `ai_agent` 的 reconcile 维护。
- ingress 校验 `X-Forgejo-Signature`（HMAC-SHA256）与仓库白名单，落 `inbox` 后立即 `202`。
- **投递语义待核实**（超时、重试次数、可否手动重投）；结论出来前一律按“至多一次”设计，保留按
  `updated_at` 游标的周期对账。
- 过滤自身回写：忽略 `sender` 为 Agent 账号的事件，并用 outbox 的 `run_id` 二次去重。

## 9. 记录、审计与用量

沿用上一版设计：`run_event`（append-only 事件）、`turn`（每轮模型交互与 token/花费）、`job`、
`decision`（每次授权判定含拒绝）、`artifact`、`usage_rollup`（每日 × 用户 × Agent × 仓库）。
大产物（日志、diff、测试输出）写 workspace 数据树或 `object_storage`，库里只存 URI 与 sha256；
写入前脱敏；分层保留（事件/轮次 90 天，job/decision/rollup 长期，产物 30 天）；`usage-export`
导出对账。走 `llm_gateway` 时以网关计费为准。

Forgejo 特有的一点：issue 评论、PR 与 Actions run 本身就是**人类可读的记录面**，因此库里的记录
只服务于审计、成本与恢复，不需要把过程复述到 issue 上。

## 10. 与既有 ANAS 资产的关系

| 资产 | 关系 |
| --- | --- |
| `forgejo` Module | 协作面与代码面；`ai_agent` 只通过 API 使用它。需要新增的只有：Agent 账号与 token 的管理端调用、系统 webhook 注册 |
| Forgejo Actions + Runner | 可选执行后端（§6.4）；默认不使用，但与 `ai_agent` 共用 `incus` Provider |
| `incus` Provider | 两个消费者（Actions Runner、`ai_agent`），各自独立 project 与证书 |
| `vikunja` Module | 保持通用任务应用定位，不承担 Agent 编排 |
| 控制台 | [Web API 与管理前端](/plans/web-api-admin-console) M1 未实现，因此 Agent 状态与配置首期走 Module 命令 |
| `llm_gateway`（提案） | 统一模型 key、预算与审计；需要独立选型文档后再立项 |

## 11. 安全边界

| 风险 | 控制 |
| --- | --- |
| issue/评论提示注入 | issue 正文与评论是不可信输入；其中的配置只能收窄；授权只看判定结果；禁止从评论拼接 shell |
| 管理凭据滥用 | Forgejo 管理凭据只用于建账号/发 token/加 key/注册 webhook，单独审计与轮换，不进执行实例 |
| token 过宽 | 每个 Agent 的 token 按 scope + `repositories` 双重限定；执行阶段才追加写权限 |
| 生成代码破坏宿主 | 一次性实例、无宿主挂载、无 socket、资源上限与 TTL；需要硬边界时切 `vm` |
| 直接提交绕过评审 | `ai:direct` 默认关闭，需仓库策略开启且目标分支未受保护；保护分支永不直推 |
| `actions` 后端的 workflow 篡改 | 只派发默认分支上的 workflow，要求分支保护，模型凭据不做 Actions secret |
| 常驻运行时状态串味 | 独立工作/缓存目录、轮次后清凭据 |
| 重复执行 | inbox 去重 + job 租约 + outbox idempotency key + 计划绑定 commit SHA |
| 事件丢失 | 快速落库 + 周期对账（投递语义确认前按至多一次设计） |
| 记录泄密 | 写入前脱敏、分层保留、导出需权限 |
| 任意出网 | 默认 deny + allowlist（模型 API、Forgejo、DNS/NTP、批准的依赖镜像源） |

信任模型不变：单租户、成员可信，安全结构完整实现但 P1 不以对抗内部恶意用户为门槛；沙箱隔离
不放宽，因为模型生成的代码与其依赖始终是不可信执行。

## 12. 落地前必须在 `forgejo 15.0.7` 上验证

| 待验证 | 影响 |
| --- | --- |
| `15.0.7` 是否具备 token 的 `repositories` 限定（该字段在 `16.0-dev` swagger 中确认） | 最小权限模型是否成立；不具备则退化为“每 Agent 每仓库一个账号”或只依赖 collaborator 边界 |
| 系统 webhook（`/admin/hooks`）在固定版本的事件集合与 payload | 一次注册覆盖全实例是否可行 |
| webhook 投递语义：超时、重试、可否手动重投 | 对账策略强度 |
| `issue_label` / `issue_assign` payload 是否含足够的 sender 与前后差异 | 授权判定与状态机 |
| 评论 `PATCH` 与 reaction API 行为 | 状态评论与轻量确认 |
| 管理端建用户/发 token/加 SSH key 的实际字段与限制 | 无人值守引导 |
| 组织级标签能否覆盖下属仓库 | 标签词表的部署方式 |
| Projects 在 `15.0.7` 是否同样无 API（预期是） | 看板只作人类视图的结论 |
| `workflow_dispatch` 与 `action_run_*` 行为 | `actions` 后端可行性 |
| Incus 系统容器的 `exec`+stdin、非特权配置与配额 | `lxc` 默认档 |

降级路径：无 `repositories` 限定 → 每 Agent 每仓库独立账号；系统 webhook 事件不全 → 退回组织级
注册；投递会自动重试 → 放宽对账频率但保留幂等。

## 13. 演进路线

| 阶段 | 交付 | 退出判据 |
| --- | --- | --- |
| P0 事实验证 | §12 的验证脚本与结论 | 全部有结论，降级路径确定 |
| P1 讨论闭环 | `anas-agent` 骨架、`ai_agent` Module、Agent 账号与 token 自动引导、系统 webhook + inbox + 对账、标签与命令、常驻聊天运行时、状态评论与反应、记录与用量 | 指派/@/标签能触发；重复投递 10 次只产生一次评论；未授权 sender 被拒并有可解释回复；重启后可恢复；每轮交互都有 token/花费记录 |
| P2 执行面 | `incus` Provider 落地后接入一次性实例、分支与 PR 策略、预算与取消 | 一个真实 issue 从讨论到 PR 全链路可复现；取消能终止实例；保护分支不可直推 |
| P3 治理 | 夜间/空闲调度、`llm_gateway` 与虚拟 key、控制台状态页、导出与保留期 | 夜间窗口自动完成任务且不影响备份窗口；执行实例不持有长期厂商 key |
| P4 扩展 | Claude Code / Pi 适配器与 golden task 对比、`actions` 执行后端、外部仓库 | 换 Agent 不迁移 `job`/`policy` 表 |

## 14. 备选与否决

- **Vikunja 作为协作面**：见 §2，推翻。
- **依赖 Projects 看板做状态机**：无 API 无事件，做不到；看板只作人类视图。
- **`actions` 作为首期唯一执行后端**：workflow 可被任何写权限者修改，secrets 暴露面大；作为 P4
  可选项并附加约束。
- **借发起人凭据代跑、只改 commit 身份**：污染审计、泄露面过大。否决。
- **在 `forgejo` Module 内实现编排**：编排器要消费模型 API、compute 与预算治理，塞进应用 Module
  会越权且与上游版本节奏绑死；仍然是独立 `ai_agent` Module。
- **在 ANAS 核心宿主跑特权容器执行生成代码**：与 Forgejo 既有结论一致，否决。

## 15. 后续文档

方案通过评审后产出 `docs/requirements/ai-agent.md` 与 `docs/plans/ai-agent.md`（需求矩阵、里程碑、
需求归属与 e2e 记录）。`forgejo` Module 侧需要评估两处改动并在其要求文档中登记：Agent 账号与 token
的管理端引导、系统 webhook 的注册与轮换归属。若采纳 `llm_gateway`，先出 `docs/research/` 选型文档。
