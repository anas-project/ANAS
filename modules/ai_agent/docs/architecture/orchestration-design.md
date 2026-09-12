# AI Agent 编排设计（Forgejo 基线）

> 状态：**部分实施**。控制面（身份与凭据引导、事件入站、交互契约与产物入库、权限判定、排程与队列）
> 已落地在 `modules/ai_agent` 并对固定 `forgejo 15.0.7` 验证；**执行面（§5.2—§5.6）与记录面（§8）
> 仍是提案**，前者阻塞于 Incus Provider 的真实宿主验收。逐条状态以
> [实施计划](../../dev-docs/plans/ai-agent.md)
> 为准。协作面用已集成 `forgejo` Module 的 issue、label 与 Projects 看板，代码面用同一实例的仓库。
> 更新：2026-09-13。

执行面的 Provider 工作见 [Incus compute Provider 要求](../../../../dev-docs/requirements/incus-module.md)与[实施计划](../../../../dev-docs/plans/incus-module.md)；
Forgejo 侧的既有边界见 [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md)；候选运行时的原始
调研见[看板应用接入 AI Agent](../research/kanban-integration.md)。

## 1. 结论

协作对象（issue）、控制信号（label、指派、评论）、代码、分支、PR、CI 与身份**在同一个应用、同一套
权限里**，因此编排器不需要跨应用身份映射、仓库绑定流程或第二套授权模型。

形态：`ai_agent` Module 订阅 Forgejo 系统 webhook，以每个 Agent
一个 Forgejo **专用账号**的身份参与 issue 讨论；执行阶段在一次性隔离实例中改代码、跑测试、推
`ai/*` 分支并开 PR。

**编排器的代码归属**：现阶段放在本仓库 `modules/ai_agent/orchestrator/`——它编译依赖仓库内的
`internal/computeclient`，与 `forgejo` 的 actions-controller 处境相同，按同一先例以 named build
context 取共享代码。**后续独立为单独的 git 仓库**，届时 ANAS 只引用它发布的 docker image，Module
侧只保留 manifest、Hook 与文档。因此从现在起就按"可搬走"约束写：编排器不得依赖 ANAS 的内部约定，
只消费环境变量、配置文件与 Secret 文件。

三条能力让这条路可行：

- **无人值守发放身份与凭据**：`POST /admin/users` 建账号、`POST /admin/users/{u}/tokens` 发 token、
  `POST /admin/users/{u}/keys` 加 SSH key，全是管理端 API，可脚本化、可轮换；
- **token 可限定到具体仓库**：`scopes` 之外还有 `repositories`，最小权限落得下去；
- **标签是仓库/组织级共享对象**，有写权限的人都能打，且有 `issue_label` 事件，可以作为授权点。

代价只有一条：**Forgejo 的 Projects 看板当前没有 API 也没有 webhook**（§2），因此看板是人类视图，
机器可读的状态必须落在 label、issue 开闭与指派上。

## 2. Forgejo 事实核验

证据来自 Forgejo 官方文档、`forgejo` 分支源码 `modules/webhook/type.go` 与 Codeberg 实例的
OpenAPI（`16.0-dev`），核验日期 2026-08-26。ANAS 固定 `15.0.7`，**实施前必须在固定镜像上复核**（§11）。

| 事实 | 说明 |
| --- | --- |
| Webhook 事件常量 | `create`、`delete`、`fork`、`push`、`issues`、`issue_assign`、`issue_label`、`issue_milestone`、`issue_comment`、`pull_request*`（含 review/sync/assign/label）、`wiki`、`repository`、`release`、`package`、`schedule`、`workflow_dispatch`、`action_run_success/failure/recover` |
| **无 Projects/看板事件与 API** | 事件常量里没有 project/column；REST API 里没有任何 `project`/`board`/`column` 路径。Projects 是纯 UI 功能 |
| Projects API 进展 | 上游正在做：Codeberg PR [forgejo#13700](https://codeberg.org/forgejo/forgejo/pulls/13700)「Project API Refactorings」（2026-08-25 仍 open、未合并）只是重构基础，真正的 Project API 由后续 PR 提供，提案见 `forgejo/discussions#466` |
| 投递头与签名 | `X-Forgejo-Event`、`X-Forgejo-Delivery`、`X-Forgejo-Signature`（body 的 HMAC-SHA256） |
| Webhook 层级 | 仓库级 `/repos/{o}/{r}/hooks`、组织级 `/orgs/{org}/hooks`、**系统级 `/admin/hooks`**、用户级 `/user/hooks` |
| 无人值守身份 | `POST /admin/users`、`POST /admin/users/{u}/tokens`、`POST /admin/users/{u}/keys`；CLI 等价物 `forgejo admin user create` / `generate-access-token` |
| Token 作用域 | `scopes`（`read:issue`、`write:repository`…）**加** `repositories`：只对指定仓库生效 |
| 标签 | 仓库级与组织级标签 CRUD；issue 标签 `POST/PUT/DELETE .../issues/{i}/labels`；变更触发 `issue_label` |
| 评论 | 创建、**编辑（`PATCH .../issues/comments/{id}`）**、附件（`/assets`）、**反应（`/reactions`）** |
| Issue 表单模板 | 仓库内 `.forgejo/issue_template/*.yaml` 支持 dropdown / input / textarea / checkboxes 与 front matter 的 `labels`、`assignees`；`GET .../issue_templates` 与 `GET .../issue_config` 可读取与校验 |
| Issue 依赖 | `GET/POST/DELETE .../issues/{i}/dependencies` 可建立"阻塞/被阻塞"关系 |
| 文件写入 | `POST/PUT/DELETE .../contents/{filepath}` 可直接提交单文件；`POST .../contents` 批量改文件；`POST .../pulls` 开 PR——引导流程不需要克隆仓库 |
| 指派 | `issue_assign` 事件；issue 可指派给 Agent 账号 |
| Markdown | issue 与评论原生 Markdown，无 HTML 往返转换 |
| Issue 时间字段 | **只有 `due_date`**（截止日，忽略时分）；没有开始时间与预估时长字段；milestone 只有 `due_on` |
| 工时 | 原生 tracked time：`POST .../issues/{i}/times`、`GET .../times`、stopwatch 起停 |
| OIDC 组映射 | `--group-claim-name`（ANAS 已固定为 `groups`）、`--admin-group`、**`--group-team-map`** 与 `--group-team-map-removal`（登录时同步） |
| Actions | `POST .../actions/workflows/{file}/dispatches` 手动派发；`action_run_*` 事件回报结果 |
| 权限模型 | 仓库权限 read/write/admin + 组织 team；`GET /repos/{o}/{r}/collaborators/{u}/permission` 可查询 |

## 3. 交互模型

一个 issue 就是一个 topic。人与 Agent 的全部交互发生在 issue 里：

| 人的动作 | 事件 | Agent 行为 |
| --- | --- | --- |
| 用 Agent 模板新建 issue | `issues` | 解析模板答案 → 同步标签 → 发状态评论 → 按模式开始讨论 |
| 指派某个 Agent 账号 | `issue_assign` | 接单；取消指派 = 撤回并中断作业 |
| 评论 `@agent-codex …` | `issue_comment` | 续接同一会话回复 |
| 打标签 `ai:plan` | `issue_label` | 冻结 issue 快照与 commit SHA，产出文档并提交推送，评论给出 commit 永久链接 |
| 打标签 `ai:approved` | `issue_label` | 通过授权判定后入队执行 |
| 打标签 `ai:cancel` / 关闭 issue | `issue_label` / `issues` | 中断作业、销毁实例、回写终态 |
| PR 上评论 `@agent-codex 修改…` | `pull_request_comment` | 在同一分支续做修订 |

### 3.1 回复三分法

1. **状态评论**：Agent 一旦介入某个 issue，**立即发一条状态评论**（因此总在最前几条），之后只用
   `PATCH` 原地更新，永不重发。它承载：当前阶段、生效配置（讨论/执行 Agent、模型、思考强度、
   分支、预算、截止）、产物链接与用量。它是"这个 issue 现在什么情况"的唯一入口。
2. **对话回复**：修改意见、澄清提问、方案讨论、执行结论——**一律新建评论**，保持时间线可读、
   保留通知、可逐条引用。不塞进状态评论。
3. **轻量确认**：收到指令但暂无内容要说时用 **reaction（👀 收到 / 🚀 已入队 / ✅ 完成）**，不发评论。

**"正在思考"不发占位评论**：生成需要时间，但占位评论要么事后删除、要么被编辑成正文，两种都会
弄脏时间线与通知。做法是收到触发立刻 👀，同时把状态评论的阶段行改成"思考中（自 12:03:11，
codex/high）"，人一眼就知道它在干活。

**同一 issue 的回合严格串行**，避免新评论打乱生成顺序：

| 新评论到达时 | 处理 |
| --- | --- |
| 上一轮尚未开始生成 | 合并进同一轮，一次回复覆盖两条 |
| 正在生成中 | 默认排队，当前轮结束后按顺序处理；状态评论显示"待处理 2 条" |
| 以 `/stop` 或紧急前缀开头 | 立即中断当前生成（运行时支持 interrupt/steer 时用原语，否则终止本轮） |

排队期间新评论照样 👀 确认，人知道没被吞。跨 issue 之间没有顺序约束，各自串行。

```markdown
**Agent 状态** · 讨论 claude(主持)+codex · 执行 codex(high)
阶段：执行中（2/5）· 分支 `ai/142-v2` · 预算 0.42 / 2.00 USD · 截止 09-02
最近：`go test ./internal/...`（12s 前）
方案文档 dev-docs/requirements/foo.md@a1b2c3d · 运行视图 → anas agent job show 318
```

### 3.2 Agent 模板与组队

新建 issue 时通过 **Forgejo issue 表单模板**选择 Agent 组合与工作方式。上游的能力与限制先摆清楚：

| 能力 | 结论 |
| --- | --- |
| 多模板 | 支持：`.forgejo/issue_template/` 下任意多个 `.yaml`，用户在"新建 issue"页选择 |
| 字段类型 | `markdown` / `input` / `textarea` / `dropdown`（支持 `multiple: true` 多选）/ `checkboxes`；校验有 `required`、`is_number`、`regex` |
| `visible` | 每个块可声明只在表单里显示、只写进 issue 正文，或两者都要 |
| front matter | 只有 `name`、`about`、`title`、`ref`、`labels`——**可以自动打标签，但不能自动指派** |
| 条件字段 | **不支持**：无法做到"选了多个讨论 Agent 才显示主持 Agent" |
| 强制用模板 | `config.yaml` 的 `blank_issues_enabled: false` |

两条限制直接决定设计：**指派由编排器在 issue 创建后用 API 补**（模板只写标签），**"按场景拆成多个
模板"而不是"一个模板靠条件字段自适应"**。

#### 模板集

模板由 `ai_agent` 按 Agent 注册表**生成并提交**到默认分支（注册表变化即重新生成，走 PR）。首期五个：

| 文件 | 场景 | 默认动作集 | 特点 |
| --- | --- | --- | --- |
| `discuss.yaml` | 需求讨论 / 方案设计（**默认模板**） | `reply`+`plan` | 单 Agent 讨论；产出需求或设计文档 |
| `roundtable.yaml` | 多 Agent 圆桌讨论 | `reply`+`plan` | 必填主持 Agent 与自主终止条件 |
| `task.yaml` | 明确的小改动 | `reply`+`plan`+`execute` | 字段最少，直奔执行 |
| `bug.yaml` | 缺陷 | `reply`+`plan`+`execute` | 多复现步骤、期望/实际、日志字段 |
| `research.yaml` | 调研 | `reply`+`plan` | 不改代码，但**产出调研文档并入库**，遵循该仓库的研究文档规范（本仓库为 `docs/research/`，含 `created`/`updated`/`evidence_as_of` 等 frontmatter 约定） |

`config.yaml` 保留空白 issue（`blank_issues_enabled: true`），空白 issue 默认不触发 Agent，要靠
指派或 `@` 唤起——这样人类之间的普通 issue 不会被 Agent 打扰。

#### 字段清单

跨模板共享的字段（各模板按需裁剪）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| 目标描述 | textarea, required | 要做什么、为什么 |
| 验收标准 | textarea | 人先写的判据；留空则由 Agent 起草后回填文档 |
| 涉及范围 | input | 模块、子目录或包名；monorepo 里限制 Agent 的读写面 |
| 禁改路径 | input | 覆盖仓库配置的额外禁区 |
| 参考资料 | textarea | 相关 issue / PR / 文档 / 链接 |
| 讨论 Agent | dropdown multiple | 选项来自注册表；`roundtable` 里至少选两个 |
| 主持 Agent | dropdown | 仅 `roundtable` 有，必填，必须是所选讨论 Agent 之一 |
| 讨论终止条件 | textarea | 仅 `roundtable` 有，见下 |
| 执行 Agent | dropdown | **只能一个**；留空 = 只讨论不执行 |
| 模型 | dropdown ×N | 每个可选 Agent 一个字段，选项是该运行时的允许模型；只作初值 |
| 思考强度 | dropdown ×N | 同上，取值用各运行时**自己的原生命名**（§5.5）；只作初值 |
| 目标分支 | input | 留空 = 自动（`ai/<issue>-<version>`） |
| 完成方式 | dropdown | 开 PR（默认）/ 直接提交（受仓库策略约束） |
| 预算档 | dropdown | 受目录组上限约束 |
| 执行时机 | dropdown | 讨论后人工批准（默认）/ 批准即执行 / 夜间窗口 / 等待事件（§3.6） |
| 风险声明 | checkboxes | 涉及数据库迁移、密钥、删除、外部系统——勾中则强制人工批准且不允许直接提交 |
| 回复语言 | dropdown | 默认取仓库配置 |

截止时间**不在模板里**：front matter 没有 `due_date`，由用户在 issue 界面设置，或用 `/due` 命令让
Agent 代设（`POST .../issues/{i}/deadline`）。

#### 提交之后

编排器按顺序做四件事，全部可在状态评论里看到：

1. **解析**表单答案（Forgejo 把答案渲染成 `### 字段名` + 值）；
2. **校验并降级**：越权的 Agent、模型、强度、预算档、完成方式一律降到允许集合，并在状态评论里
   逐条说明"你选了 X，因为 Y 降级为 Z"；
3. **同步到标签与指派**：写 `ai:chat/*`、`ai:host/*`、`ai:exec/*`、`ai:branch/*`、`ai:exec-when/*`，
   并把讨论 Agent 指派到 issue（模板做不到指派）。**模型与思考强度不进标签**——它们在对话过程中
   会被反复调整（`/model`、`/effort`），每改一次都会产生 `issue_label` 事件和一条时间线记录，把
   issue 的历史刷成参数流水。标签只承载**授权与流程控制**（谁参与、什么阶段、执行时机、分支），
   **运行参数是状态**：存库、在状态评论里实时显示、用命令修改；
4. **发状态评论**并开始工作。

标签此后是人类可见、可直接修改的控制面；标签被改动同样触发重新解析。完整配置以状态评论与库中
记录为准。

#### 多 Agent 圆桌

- 主持 Agent 拥有**唯一发起权**：按需要 `@` 其他讨论 Agent 提问或要求评审；其他 Agent 只回应被
  `@` 的部分，不主动互相追问，避免自增长的对话；
- 每轮由主持汇总分歧与已达成结论，写进它维护的讨论纪要评论；
- **讨论终止条件只在圆桌模式下存在**，它是给 Agent 自己判断"讨论何时可以结束"的判据，不是给人的
  开关。默认 prompt：需求边界清晰、无未决分歧、验收标准可判真假、风险已列出、没有需要人类决策的
  开放问题。用户可以整段替换或追加（例如"必须给出两套备选方案并说明取舍"）；
- 无论 prompt 怎么写都有**硬上限**：最大轮数、最大预算、最长墙钟、人类无回应超时。触顶即强制收敛，
  并在文档与评论里注明"由上限截断，未自然收敛"；
- 收敛后由主持产出最终文档并提交（§3.3），其他讨论 Agent 的意见以纪要形式保留在文档中。

单 Agent 讨论没有主持角色与终止条件字段，其余流程相同。

### 3.3 产物一律进 Git

Agent 生成的需求文档、方案、代码、脚本**不留在评论里**，而是提交并推送到工作分支：

- 文档落在**该仓库自己的工程化约定**里，不是某个 Agent 专用目录。ANAS 本仓库当前的约定是需求进
  `dev-docs/requirements/`、实施计划进 `dev-docs/plans/`，遵循[文档写作标准](../../../../docs/developer/documentation-standard.md)
  的分类；**这些位置以后可能调整**，届时只改仓库配置，历史产物不迁移——评论里的链接指向 commit，
  永远有效；
- **文档由控制面提交，不由 Agent 推送**：Agent 只产出内容，控制面校验路径是否在该仓库允许的文档
  目录内，然后用 contents API（`POST/PUT .../contents/{path}`）直接提交到工作分支。因此讨论与计划
  阶段的 Agent 工作区**不需要任何 git 写凭据**，也不需要可写检出；越权写路径在控制面就被拒绝；
- 代码变更走执行阶段的正常 git 推送（§5.6），凭据是作业期短时注入的；
- 每次产物更新 = 一次 commit（消息含 `issue #<n>` 与阶段）；
- 评论里的**永久链接一律指向具体 commit**：`<forgejo>/{owner}/{repo}/src/commit/<sha>/<path>`
  （原始内容用 `/raw/commit/<sha>/...`），并带上短 SHA；
- 评论正文只放摘要与要点，完整内容看 Git：有版本、可 diff、可 review、可回滚。

**执行的输入是文档，不是聊天记录。** 批准时冻结 `(文档路径, commit SHA)`：执行阶段只读该 commit 上
的文件；讨论记录仅作背景。文档或基线 commit 变化都会使既有 `ai:approved` 失效并要求重新批准。

#### 仓库工程化约定从哪来

四级解析，取第一个命中的：

1. **仓库内的 Agent 配置文件**（`.anas-agent.yml`，随仓库版本化）——权威来源；
2. **引导 issue 的结论**；
3. **从仓库既有约定推断**：`AGENTS.md` / `CLAUDE.md` / `CONTRIBUTING.md`、既有 `docs/` 结构、测试
   命令、提交信息风格、分支命名；
4. **内置预设**：按仓库类型（应用、库、文档站、monorepo、基础设施）给出保守默认。

**引导 issue** 在仓库首次启用 Agent 时自动创建，用带默认值的问卷问清工程化方式：

| 问题 | 默认来源 |
| --- | --- |
| 需求文档目录 / 实施计划目录 / 设计文档目录 | 探测既有 `docs/` 结构 |
| 文档语言，是否需要英文镜像 | 探测既有文档 |
| 提交信息规范、分支命名、是否强制 PR、目标分支 | 探测 git 历史与分支保护 |
| 测试命令、lint 命令、构建命令 | 探测 `Makefile`/`package.json`/`go.mod`/CI 配置 |
| 禁止改动的路径 | 空 |
| 默认讨论/执行 Agent 与预算档 | 部署默认 |
| 是否允许 Agent 自主建 issue、每作业/每日配额（§3.5） | 部署默认（默认只开提议档） |

人在 issue 里逐项确认或修改后，Agent 用 contents API 提交 `.anas-agent.yml` 并开 PR，合并后以文件
为准；改约定就是改这个文件，走正常评审。整个流程只用 REST API，不需要克隆仓库。

### 3.4 时间与排程

| 输入 | 来源 |
| --- | --- |
| 截止时间 | issue 原生 `due_date`（硬约束） |
| 预估时长 | **Agent 在准备执行前自己给出**，不由用户填写 |
| 优先级 | 优先级标签 + 入队时间 |
| 时间窗 | 仓库/部署的夜间或空闲策略 |

**不设"最早开始时间"**：上游没有该字段，需要延后就用"先不批准"表达。

1. 批准后、入队前，Agent 基于冻结的方案文档给出预估（工作量、预计墙钟、预计花费），写进状态评论
   与 `job`；
2. 入队时校验 `预计完成 ≤ due_date`，不满足立即打 `ai:at-risk` 并写明差多少，**不静默跑超时**；
3. 队列按**最早截止优先**（EDF），同截止再看优先级与入队时间；
4. 单作业墙钟上限 `min(预估 × k, job_wallclock_minutes)`，`k` 默认 2；
5. 结束把**实际耗时写回 Forgejo 原生 tracked time**，既让工时报表看得见 AI 的投入，也用 EWMA 校准
   下次预估。

### 3.5 拆分与总结

两个评论命令，都受 §6 判定约束：

- `/summarize`：把当前讨论压缩成结构化文档并提交（§3.3），常用于讨论过长、上下文将被压缩之前；
- `/split <标题>`：把当前上下文中已经收敛的一块**总结成新 issue**——新 issue 带自己的 Agent 配置
  （默认继承父 issue，可在命令里覆盖），并用 issue **依赖 API** 建立"父被新 issue 阻塞"关系，双方
  各留一条交叉引用评论。

这个功能是必要的：讨论天然会长出子问题，没有拆分机制就只有两条坏路——要么在一个 issue 里堆到
上下文爆掉，要么人肉复制粘贴、丢掉与文档和分支的关联。拆分后每个 issue 有独立会话、独立分支、
独立预算，父 issue 的状态评论列出子 issue 与其状态。

#### Agent 自主建 issue：三档授权

> 状态：**提案**。需求条目 `AGENT-R-072`—`R-078`，里程碑 M9。

`/split` 是**人触发**的：命令带 sender，§6 拿它去判定。Agent 在讨论里自己发现一个子问题时没有
sender，因此不能沿用同一条路径。按"授权链是否清楚"分三档，默认是中间那档：

| 档 | 谁在建 | 授权来源 | 例子 |
| --- | --- | --- | --- |
| **派生** | 控制面状态机 | 不需要额外授权——它是既有动作的产物，不是 Agent 的主张 | 执行 issue（§3.7）、队列 issue（§3.6）、引导 issue（§3.3） |
| **提议**（默认） | Agent 提议，人确认后由控制面建 | 归属**点确认的那个人**，受其动作集约束 | 讨论中发现的 bug、应当拆出去的子任务 |
| **自主** | Agent 直接建，控制面校验后落地 | 归属**发起本轮的那个人**，且仓库须显式开启 | "扫一遍 TODO 建 issue"这类人已明确下单的批量任务 |

**提议档的形态**：Agent 不调用任何写接口，而是在本轮输出里给出结构化提议（与它产出文档同构，
§3.3）。控制面把提议渲染成一条评论——标题、正文摘要、拟打标签、与父 issue 的依赖关系——人用
👍 或 `/accept <n>` 确认即建，👎 或 `/reject <n>` 即作废，超时未处理自动过期。确认动作本身走 §6.3
判定，因此"谁批准了这个 issue"在 `decision` 里有据可查。

**自主档的护栏**（缺一不可，任一超限即降级回提议档并在状态评论说明）：

| 护栏 | 规则 |
| --- | --- |
| 配额 | 每作业与每仓库每日两级上限，`repo_settings` 配置，默认作业 5 条、每日 20 条 |
| 去重 | 建之前用规范化标题指纹比对该仓库**未关闭**的 issue，命中则改为在既有 issue 上追加一条评论 |
| 溯源 | 必带 `ai:proposed` 标签、父 issue 交叉引用与创建它的 `job_id`；溯源块由控制面追加到正文尾部 |
| 不自触发 | Agent 建的 issue **不带** `ai:auto`，要人指派或打标签才进入讨论 |
| 跨仓库 | 一律不允许（见下） |

「不自触发」这一条是**防自喂**。§7 已经过滤 `sender` 为 Agent 账号的事件，因此自触发目前天然不成立；这里把它
写成显式约束，是为了避免以后放宽那条过滤时悄悄丢掉这个保证。一旦 Agent 建的 issue 能自动触发下
一轮，就形成"自己写输入、自己读输入"的闭环，既烧预算，也会让模型把自己的猜测当成事实。同理，
Agent 创建的 issue 正文在组装上下文时按 §8.1.0 的来源规则处理，不当作人类输入。

**跨仓库建 issue 不在本节范围内**：§5.6 已经确定仓库就是信任边界，Agent 的 token 也按 §4 限定到
已启用仓库。跨仓库写需要按"仓库对"显式授权，登记在 §14，首期不做。

### 3.6 执行时机：立即、定时与事件

批准与"什么时候跑"是两件事。默认是"批准后按调度策略跑"，另外提供三种显式时机，命令与模板字段
等价，命令执行后写回 `ai:exec-when/*` 标签保持可见：

| 时机 | 命令 | 含义 |
| --- | --- | --- |
| 立即 | `/execute now`（别名 `/立即执行`） | 跳过夜间窗口，入队即优先租约；仍需 `execute` 权限与已批准的方案 |
| 定时 | `/execute at 2026-09-01T02:00` | 到点入队；仍受夜间窗口与资源门槛约束 |
| 事件 | `/execute on <事件>` | 事件到达才入队 |
| 暂停 | `/execute hold` | 取消已设置的时机，回到人工批准 |

可用事件都来自控制面**已经订阅**的 webhook，不需要新增机制：

| 事件 | 触发源 | 典型用途 |
| --- | --- | --- |
| `on:merge #<pr>` | `pull_request` merged | 依赖的 PR 合并后再动手 |
| `on:closed #<issue>` | `issues` closed | 与 issue 依赖（§3.5）配合，前置任务完成后自动接力 |
| `on:ci-green <branch>` | `action_run_success` | CI 绿了再改，避免在坏基线上工作 |
| `on:ci-red <branch>` | `action_run_failure` | 构建失败时自动开始排查 |
| `on:push <branch>` | `push` | 基线更新后重新执行 |

命令解析支持别名表（含中文别名），别名由配置提供，不写死在代码里。事件等待有超时上限，超时后
回到人工批准并在状态评论说明。

#### 并行还是顺行

| 命令 | 作用域 | 含义 |
| --- | --- | --- |
| `/queue serial` | 仓库 | 同一仓库同一时刻只跑一个执行作业（默认） |
| `/queue parallel <n>` | 仓库 | 允许 `n` 个作业并行，上限受部署与主机资源门槛约束 |
| `/execute after #<issue>` | 单个执行 issue | 显式排在某个 issue 之后——写成 Forgejo 原生 **issue 依赖**，Forgejo 自己就会显示"被 #12 阻塞" |
| `/queue move <位置>` | 单个执行 issue | 调整队列位置（仅影响同优先级内的顺序） |

顺序的**语义来源**是三样东西的组合：issue 依赖（硬顺序）→ 截止时间 EDF（§3.4）→ 优先级与入队时间。
调度器不发明顺序，只按这三样解释。

#### 顺序展示在哪

Forgejo 没有可写的看板 API（§3.8），但有**可排序的置顶 issue**：`POST .../issues/{i}/pin`、
`PATCH .../issues/{i}/pin/{position}`、`GET .../issues/pinned`，另有 `GET .../new_pin_allowed` 说明
是否还能再置顶。因此：

- 每个启用仓库有一个由 Agent 维护的 **「Agent 队列」issue 并置顶**，正文是一张有序表（位置、执行
  issue、状态、依赖、预计开始、预算、负责 Agent），随队列变化原地 `PATCH` 更新；
- 置顶数量有限，队列 issue 只占一个，且 `new_pin_allowed` 为假时降级为不置顶并在状态评论说明；
- 硬顺序同时写成 issue 依赖，这样即使不看队列 issue，在每个执行 issue 上也能看到"被谁阻塞"；
- 命令行入口 `anas module ai_agent agent queue list`，管理前端可用后再做页面；
- 里程碑（milestone）可选作为"批次"表达，不参与调度判定。

### 3.7 执行拆成独立 issue

讨论与计划结束后**默认另开一个执行 issue**，父 issue 保持人类可读的需求历史：

| | 讨论 issue（topic） | 执行 issue（job） |
| --- | --- | --- |
| 承载 | 需求、方案分歧、决策、最终文档链接 | 一次作业：配置、进度、日志、失败重试、PR 链接 |
| 生命 | 需求线的全程 | 一个作业，完成或失败即关闭 |
| 数量 | 1 | 一个方案可派生多个（分阶段、分模块、可并行） |
| 关系 | 被执行 issue **阻塞**（`POST .../issues/{i}/dependencies`），双向交叉引用 | 标题 `[exec] <原标题> · plan v<n>`，正文含冻结文档链接、commit SHA、生效配置、验收标准 |

理由：作业会产生大量过程信息（进度、日志、重试、中断），塞进讨论 issue 会把需求讨论淹没；拆开后
父 issue 的时间线只剩"需求怎么定下来的"，执行 issue 的时间线只剩"这次作业发生了什么"，两条线各自
可读、可搜索、可关闭。执行 issue 也天然可指派、可加依赖、可进看板列。

**默认全部拆**，包括 `task` 与 `bug`：规则一致比省一个 issue 更重要，否则"这次为什么没拆"会变成
需要解释的例外。确实嫌重的仓库可以用 `repo_settings.exec_issue = false` 整仓关闭，或对单个 issue 用
`/exec-issue off`；关闭时三档回写落在原 issue，节流阈值更保守。

执行 issue 内的回写遵循 §8.2：一条**作业状态评论**（定时原地更新）、里程碑另发评论、结束时一条含
日志附件的总结评论；完成后在父 issue 回一条摘要并解除阻塞。人在执行 issue 里可以 `/stop`、追加
指示或 `@` 其他 Agent 参与排查——所有打断与讨论都走评论，不需要另一套界面。

### 3.8 Projects 看板的处置

当前没有 API 也没有事件，Agent 无法读或移动卡片。因此：

- 机器可读状态一律落在 **label + issue 开闭 + 指派**；
- Projects 看板作为人类视图，由人拖动，或用 Forgejo 自身"关闭即入 Done 列"的行为；
- 不设计任何依赖看板列的自动化。

上游正在补这块能力（PR 13700 是重构基础，真正的 Project API 在后续 PR）。跟踪方式：列入 §11 复核项，
`board` 适配器预留 `sync_board()`（首期空实现）。API 可用后接入的效果是"Agent 把卡片移到对应列"，
属于展示增强，**不改状态机**。

### 3.9 标签词表

标签由 ANAS 在启用的仓库/组织中创建（组织级标签可一次覆盖全部仓库），具体的 Agent 名与模型名
来自注册表，不硬编码：

| 标签 | 含义 | 谁能打 |
| --- | --- | --- |
| `ai:auto` / `ai:manual` | 本 issue 的参与模式覆盖 | write |
| `ai:silent` | 让 Agent 退出本 issue | write |
| `ai:plan` | 冻结快照并产出方案文档 | write（plan 权限） |
| `ai:approved` | 批准执行（授权点） | maintain/admin（execute 权限） |
| `ai:cancel` | 中断当前作业 | write |
| `ai:chat/<agent>`、`ai:host/<agent>`、`ai:exec/<agent>` | 讨论 / 主持 / 执行 Agent | write |
| `ai:branch/<name>`、`ai:target/<branch>` | 工作分支 / 目标分支 | write |
| `ai:pr` / `ai:direct` | 完成后开 PR / 直接提交到目标分支 | `ai:direct` 需仓库策略允许 |
| `ai:exec-when/now`、`ai:exec-when/at=<时间>`、`ai:exec-when/on=<事件>` | 执行时机（§3.6） | execute 权限 |
| `ai:at-risk` | 预估无法在截止前完成 | 仅 Agent |
| `ai:running`、`ai:needs-review`、`ai:failed` | Agent 维护的状态标签 | 仅 Agent |
| `ai:proposed` | 标记这是 Agent 提议或自主创建的 issue（§3.5） | 仅 Agent |
| `ai:job` | 标记这是执行 issue（§3.7） | 仅 Agent |
| `ai:queue` | 标记置顶的队列总览 issue（§3.6） | 仅 Agent |

评论命令（`/plan`、`/approve`、`/stop`、`/chat`、`/exec`、`/model`、`/effort`、`/branch`、`/budget`、
`/due`、`/execute now|at|on|hold`、`/summarize`、`/split`、`/accept`、`/reject`，含可配置的中文别名如
`/立即执行`）与标签
等价；两者都过同一套授权判定，且命令执行后写回对应标签，使状态始终
可见。

## 4. 身份与凭据

每个 Agent 一个 Forgejo 账号（`agent-<id>`），由 ANAS 在 reconcile 阶段通过管理端 API **无人值守**
创建与维护：

1. `POST /admin/users` 建账号（口令为随机占位值，账号不用于交互登录）；
2. `POST /admin/users/{agent}/tokens` 发 token：`scopes` 取 `write:issue`、`read:repository`
   （执行阶段另发 `write:repository`），`repositories` 限定到**已启用的仓库**；
3. 需要 git 推送时 `POST /admin/users/{agent}/keys` 下发独立 SSH key；
4. 按需加为仓库 collaborator（`write`）或加入组织 team；
5. 轮换 = 发新 token → 写 Secret Store → `DELETE` 旧 token，全程无人工。

容器内 `forgejo admin user create` / `generate-access-token` 是等价回退路径。管理端凭据由 `ai_agent`
持有，只用于上述动作与系统 webhook 注册，不进入执行实例，单独审计与轮换。

## 5. 架构

### 5.1 组件

```text
┌──────────────────────────── ANAS deployment ─────────────────────────────┐
│ traefik ──▶ anas_forgejo   issue / label / comment / PR / Projects        │
│               │      ▲                                                    │
│    系统 webhook│      │ REST（各 Agent 的受限 token）                      │
│               ▼      │                                                    │
│ traefik ──▶ anas_ai_agent                                                 │
│      ├── ingress   验签 → inbox → 202                                     │
│      ├── policy    §6 授权判定（deny by default）                          │
│      ├── orchestr. 状态机 / 组队 / 计划 / 预算 / 租约 / 记录                │
│      ├── chat runtime  常驻容器 ×N（每 Agent 一个，按 issue 分工作目录）     │
│      └── outbox    评论 / 状态评论 / 标签 / 反应 / 附件                    │
│      ├── PostgreSQL（inbox/policy/plan/job/run_event/usage）               │
│      ├── 持久卷 agent-sessions/<repo>/<issue>/<agent>/                     │
│      └── compute Contract ──▶ incus Provider ──▶ Incus 宿主                │
└───────────────────────────────┬──────────────────────────────────────────┘
                                ▼
        一次性执行实例（默认 LXC，可选 VM）：fresh clone + worktree + Agent CLI
        exec_stdin 注入短时 token/SSH key → tmpfs，作业后销毁
```

凭据边界：**协作凭据只在控制面，执行凭据只在执行面**。Agent 进程看不到管理凭据，也看不到其他
仓库的 token；所有 issue 写操作经控制面 outbox。

### 5.2 执行面 Provider

`compute` Contract 已定义一次性实例生命周期，把 Forgejo Actions controller 内嵌的 Incus 客户端提取
为独立 `incus` Provider Module，Forgejo Actions 与 `ai_agent` 都作为消费者，各自绑定独立 restricted
project 与证书。范围与验收见[要求](../../../../dev-docs/requirements/incus-module.md)与[计划](../../../../dev-docs/plans/incus-module.md)。

### 5.3 隔离档

| 档 | 边界 | 资源 | 适用 |
| --- | --- | --- | --- |
| `container`（常驻） | 命名空间隔离 | 按需，亚秒启动 | 讨论、计划（不执行仓库代码） |
| `lxc`（**默认执行档**） | 共享宿主内核 + user namespace + seccomp/AppArmor | 无内存预留，秒级启动，I/O 接近原生 | 改代码、装依赖、跑测试 |
| `vm` | 独立 guest kernel + 硬件虚拟化 | 需预留内存，数秒启动 | 需要硬边界：外部贡献者仓库、不受信依赖 |

本质差别只有共享内核这一条；Incus 对二者使用同一套 project/quota/exec 接口。系统容器 interface 已
登记为 [Incus 计划](../../../../dev-docs/plans/incus-module.md) M5。

### 5.4 执行后端：自建实例 vs Forgejo Actions

| 后端 | 做法 | 取舍 |
| --- | --- | --- |
| `compute`（默认） | 控制面直接创建一次性实例，注入短时凭据并运行 Agent CLI | 队列、预算、租约、取消、夜间窗口由控制面掌控；日志进 ANAS 记录体系 |
| `actions`（可选） | `workflow_dispatch` 派发仓库内固定 workflow，复用既有 Runner | 复用已有隔离与日志 UI；但 workflow 文件在仓库里，有写权限者可改它运行什么，Actions secrets 暴露面大，调度与取消语义弱 |

首期只做 `compute`。要开 `actions` 必须同时满足：只派发默认分支上的 workflow、默认分支受保护、
模型凭据不以 Actions secret 形式存在。

### 5.5 Agent 运行时与可扩展接入

当前接入 Codex、Claude Code、Pi，**但架构不为这三个特设**。新增一个运行时只需三件事：实现
`AgentAdapter`、在注册表加一条、提供固定 fingerprint 的运行时镜像；标签、issue 模板、权限组
（`CAP_ai_agent_<id>`）与命令补全都由注册表生成，代码与文档里不出现硬编码的厂商分支。

注册表条目（能力描述符）：

| 字段 | 说明 |
| --- | --- |
| `id` / `account` | `codex` ↔ Forgejo 账号 `agent-codex` |
| `adapter` | 实现名；决定进程协议（CLI/JSONL/SDK） |
| `runtime_image` / `cli_version` | 固定 fingerprint 与版本；启用时 preflight 校验 |
| `auth_mode` / `credential_id` | `api_key` / `session_file` / `oauth`，值在 Secret Store |
| `models` / `default_model` | 允许的模型列表；issue 模板下拉项由此生成 |
| `effort_levels` | 思考强度取值，**用该运行时自己的原生命名**；不支持该维度就留空，模板不显示该字段 |
| `effort_tiers` | 可选：把抽象档（低/中/高）映射到本运行时的原生取值，仅用于部署默认与跨 Agent 策略 |
| `capabilities` | 支持 `reply` / `plan` / `execute` / `host`（能否当主持）中的哪些 |
| `session` | 是否支持续接与 fork，会话状态目录 |
| `limits` | 并发、单作业墙钟、单作业与每日预算 |
| `isolation_profile` / `sandbox_project` | 执行隔离档与 Incus project |
| `status` | `enabled` / `disabled` / `circuit_open` |

**思考强度不做统一术语**：各运行时的分级名称与档数不同，强行映射成统一词表会在某个运行时上产生
不存在的档位，也会让人以为跨 Agent 的"high"是同一件事。因此模板下拉、标签与记录一律使用原生取值
（`ai:effort/<agent>=<原生值>`）；只有"部署默认"这类需要跨 Agent 表达的地方才用可选的 `effort_tiers`
抽象档，且展示与审计仍落回原生值。适配器负责校验取值合法，非法值拒绝并说明可选项。

配套规则：preflight 失败保持 disabled 且不影响其他运行时；版本升级即配置变更（禁止运行时自动升级
CLI，保留 N-1 回滚）。

#### 5.5.1 凭据形态与管理渠道

各运行时的认证方式不统一，ANAS 侧收敛成一个导入面，不为每家做各自的登录 UI：

| 形态 | 来源 | ANAS 侧做法 |
| --- | --- | --- |
| `api_key` | 厂商控制台 | `agent-credential set --agent <id>` 从 stdin 读入，不回显、不入日志、不入 issue |
| `session_file` | 管理员在自己机器上用官方 CLI 登录后导出的凭据文件 | `agent-credential import --agent <id> --file -`，整文件存 Secret Store，作业时经 `exec_stdin` 注入 |
| `oauth` | 需要浏览器交互的授权 | 交互在管理员机器上完成，ANAS 只接收结果凭据；**服务端不弹浏览器，也不代管账号口令** |

配套命令：`agent-credential status`（只显示存在性、指纹与剩余有效期，**永不显示值**）、
`agent-credential rotate`、`agent-preflight`、`agent-disable`。

运行规则：

- **过期监控**：记录到期时间，临近到期告警；到期或认证失败使该运行时进入 `circuit_open` 并在相关
  issue 的状态评论里留一句说明，而不是静默重试；
- **熔断隔离**：连续失败达阈值自动熔断，只影响该运行时，其他 Agent 照常工作；
- **席位调度**：订阅型凭据往往有并发席位限制，按信号量调度，超出即排队而不是并发失败；
- **多用户部署优先 `api_key`**：个人订阅的登录态属于该自然人，不应作为部署内其他用户的共享额度，
  具体以各厂商服务条款为准。

### 5.6 工作实例与隔离

**先澄清运行时现状**：Codex、Claude Code、Pi 的 CLI 都是"在给定工作目录里干活"，它们自己**不管理
git worktree**，也不提供任务间的隔离。准备工作区、划分边界是宿主（本方案）的责任。

#### 隔离边界跟着信任边界走

关键判断：**仓库就是信任边界**。能写这个仓库的人本来就能改它的任何分支，因此同一仓库的不同 issue
之间不需要强隔离；**跨仓库必须隔离**，因为写 A 仓库的人未必有 B 仓库的权限。据此定两档：

| 档 | 粒度 | 含义 | 何时用 |
| --- | --- | --- | --- |
| `repo`（**默认**） | 每个仓库一个工作实例（LXC 系统容器），空闲回收 | 实例内每 issue 一个 `git worktree`；Agent 进程、编辑、构建、测试都在这个实例里；仓库间绝不共用实例 | 常规内部仓库 |
| `job`（可选） | 每个作业一个一次性实例 | 作业结束即销毁，冷启动换更强边界 | 外部贡献者分支、不受信依赖、高危变更、需要干净基线复现 |

这直接回答"为什么不干脆常驻一个 Agent 实例开多个 worktree"：**可以，但边界必须是仓库而不是
Agent**。Agent 的工具循环本身就在执行任意命令（装依赖、跑测试、跑脚本），把多个仓库的检出放进同
一个容器，等于让 A 仓库的一条命令能读写 B 仓库的代码与凭据。按仓库切分既保留了你要的"常驻 + 多
worktree + 不用每次冷启动"，又不让隔离失效。

#### 各阶段在哪跑

| 阶段 | 工作区 | 写权限 |
| --- | --- | --- |
| 讨论 / 计划 | 工作实例内该 issue 的 worktree，代码**只读** | 无 git 凭据；产出的文档由控制面用 contents API 提交（§3.3） |
| 执行 | 同一实例内该 issue 的 worktree，切到 `ai/<issue>-<version>` | 作业期注入短时 token/SSH key 到 tmpfs，作业结束吊销 |
| 测试与构建 | 同一实例内 | 出网受 allowlist 限制 |

`job` 档下三个阶段都在一次性实例里，其余规则不变。需要更强的测试环境隔离（例如要求干净内核或
特权操作）时，可以在实例之外再申请一个一次性 `vm`，只跑测试，不承载 Agent 会话。

#### 四条硬边界

| 维度 | 规则 |
| --- | --- |
| 实例 | 每仓库一个（`repo` 档）或每作业一个（`job` 档）；跨仓库绝不共用 |
| 会话 | `discussion(repo, issue, agent) → session_id`，会话目录 `agent-sessions/<repo>/<issue>/<agent>/` |
| 工作区 | 每 issue 一个 worktree，从实例内的共享对象库派生；`job` 档为 fresh clone |
| 分支 | 每 issue（每计划版本）一个分支 `ai/<issue>-<version>` |

#### 并发控制：用租约，不用分支锁

同一仓库的不同 issue 走不同分支，不会互撞，因此**默认不需要分支锁**。真正要防的是三件事，各用
对应机制：

| 要防的 | 机制 |
| --- | --- |
| 同一作业被重复触发（重复投递、重试、连点两次批准） | `job` 租约 + `(issue, plan_version)` 唯一性；这是幂等问题，不是并发问题 |
| 同一 issue 的新作业与仍在跑的旧作业重叠 | 新作业入队前先取消或等待旧作业，状态评论说明 |
| `ai:direct` 直接提交到**共享目标分支** | 仅此场景加 `(repo, target_branch)` 锁，因为多个 issue 会写同一个分支 |

资源层面的并发上限由 `/queue serial|parallel`（§3.6）与主机资源门槛控制，与正确性无关。

### 5.7 会话如何在容器销毁后存活

常驻容器是**无状态**的，会话状态全部落在持久卷上：

- 会话文件写 `agent-sessions/<repo>/<issue>/<agent>/`，`session_id` 与运行时版本存库；容器空闲回收
  或升级重建后，挂同一路径 `resume` 即可继续；
- 每次收敛点（讨论结束、计划提交、作业结束）把会话目录打包成 artifact 备份，保留期与 issue 关联，
  issue 关闭后按保留策略清理；
- **续接失败不是错误路径**：跨运行时版本、跨主机、文件损坏都可能续不上，降级是从**仓库文档 +
  issue 时间线 + 状态评论**重建上下文——这三者才是持久事实，且人与 Agent 共享同一份；
- 因此长会话按轮次或 token 阈值主动压缩：生成纪要、丢弃过程细节、重新锚定到冻结的方案文档。

### 5.8 Agent 侧的写路径：控制面自有工具，不是仓库内 skill

> 状态：**提案**。需求条目 `AGENT-R-077`，里程碑 M9。

§3.5 说的是"能不能建"，这一节说"怎么建"。Agent 要发起任何 Forgejo 写操作，路径只有一条：
**控制面拥有的工具面**——与 outbox 是同一套判定、幂等与记账，只是换了一张脸。两种形态按阶段推进：

| 形态 | 做法 | 何时用 |
| --- | --- | --- |
| **结构化提议**（P1） | Agent 在本轮输出里给出提议，控制面在回合结束时统一裁决 | 提议档；实现最省，不必往工作实例开任何入口 |
| **控制面 MCP**（P3） | `anas-agent-mcp` 跑在控制面，工作实例经作业期凭据走 loopback/unix socket 访问，只暴露白名单操作 | 自主档；工具在 Agent 循环内，建完能拿到 issue 号继续引用 |

MCP server 暴露的操作就是白名单本身：`create_issue`、`comment`、`set_labels`、`link_issue`、
`open_pr`。每个调用进 outbox，带 idempotency key，写 `decision` 与 `comment_provenance`，计入预算。
它跑在控制面而不是沙箱里，因此 **Forgejo token 始终不进执行实例**（§5.1 的凭据边界不变）；工作
实例拿到的只是一个作业期、限定到本 issue 的短时凭据，作业结束即吊销。选 MCP 而不是各家 CLI 各自
的插件机制，是因为三家运行时都支持它，符合 §5.5"架构不为这三个特设"。

**为什么不是"往每个仓库装一个控制 Forgejo 的 skill"**：

| 反对 | 依据 |
| --- | --- |
| 仓库内文件谁有写权限谁就能改"Agent 能做什么" | §5.4 已用同一理由限制 `actions` 后端 |
| skill 要能干活就得在沙箱里放可写 Forgejo 的凭据 | 破 §5.1 凭据边界与 §3.3"文档由控制面提交" |
| 绕过判定：无 sender、无 `decision`、无幂等、无 provenance、不计预算 | §6.3、§6.5、§10 |
| issue 与评论是不可信输入，直接写工具等于注入即可建 issue、改标签 | §10 |
| skill 是某一家 CLI 的机制，其余运行时各有各的 | §5.5 |

仓库内确实需要一份"这个仓库怎么干活"的说明，但那是 `.anas-agent.yml`（§3.3）已经承担的职责：
它描述**约定**（文档目录、测试命令、禁改路径）与**开关**，不描述**权限**。两者不合并——一个走
PR 评审即可改，另一个必须走 §6。运行时若支持 skill 或规则文件，由控制面在准备工作区时**从
`.anas-agent.yml` 渲染生成**，不接受仓库里手写的同名文件覆盖工具面。

## 6. 权限模型

Forgejo 的仓库权限就是访问权，策略层只回答"谁能让哪个 Agent 做哪一类动作、花多少钱"。

### 6.1 三层

| 层 | 来源 | 说明 |
| --- | --- | --- |
| L0 访问权 | Forgejo 仓库权限与组织 team | Agent 账号进了哪些仓库、token `repositories` 限定到哪些仓库，取交集 |
| L1 触发权 | 默认由仓库权限推导，可覆盖 | 谁能让 Agent 做什么 |
| L2 执行权 | 仓库策略 | 允许的分支模式、是否允许直接提交、是否必须开 PR、预算与并发 |

默认推导（`sync: mirror`，可逐条覆盖，也可 `sync: off` 全手动）：

| Forgejo 仓库权限 | 默认动作集 |
| --- | --- |
| read | `reply` |
| write | `reply`、`plan` |
| maintain / admin | `reply`、`plan`、`execute` |

### 6.2 用目录组授予 Agent 使用权

部署级授权来自 **Samba AD 组**，走已有链路：

```text
Samba AD 组（CAP_ai_agent_*）
  → IAM 的 groups claim（Forgejo OIDC 已固定 --group-claim-name groups）
  → Forgejo 组织 team（--group-team-map / --group-team-map-removal）
  → ai_agent 通过 Forgejo API 读 team 成员 → agent_grant
```

**组类别**：Agent 使用权既不是"人员职责"（`ROLE_*` 由管理员按组织结构建），也不是"能否登录应用"
（`APP_<module>`），而是**应用内的功能授权且由 ANAS 按启用的 Module 生成**。因此新增一类，并给它
自己的 OU，避免与人建组、应用登录组混在一起：

| 类别 | 命名 | 创建主体 | 位置 | 含义 |
| --- | --- | --- | --- | --- |
| 应用登录（现有） | `APP_<module-id>` | ANAS 生成 | `OU=Apps,OU=Groups` | 能不能进这个应用 |
| **应用能力（新增提案）** | `CAP_<module-id>_<capability>` | ANAS 生成 | **`OU=Cap,OU=Groups`** | 进去之后能用哪个功能 |

这是通用机制，不为 AI 特设：任何 Module 要把应用内角色投影到目录都用同一规则（如
`CAP_nextcloud_admin`）。独立 OU 让"ANAS 生成、随 Module 启停增删"的组有明确边界，便于批量审计、
备份筛选与访问控制，也符合[命名规范](../../../../docs/architecture/samba-ad-user-planning.md) §5.5"一个组只表达一种
含义"的原则。

`ai_agent` 生成的组：

| 组 | 含义 |
| --- | --- |
| `CAP_ai_agent_reply` | 可以让 Agent 参与讨论与回复 |
| `CAP_ai_agent_plan` | 可以让 Agent 读代码并产出需求/方案文档 |
| `CAP_ai_agent_execute` | 可以批准执行（`ai:approved` 生效） |
| `CAP_ai_agent_<runtime>` | 可以使用该运行时（由注册表生成，不硬编码） |
| `CAP_ai_agent_budget_high` | 使用较高的每日预算档 |
| `CAP_ai_agent_terminal` | **非管理员**附着运行中的 CLI 终端（管理员组成员无需此组，§8.1.2） |

最终动作集 = **目录组上限** ∩ **仓库权限推导** ∩ **覆盖条目**；两边都不能单方面放大对方。

**撤销延迟与实时同步**：若只有 OIDC 一条链路，组声明只在用户**登录时**随 claim 到达 Forgejo，
`--group-team-map-removal` 也在登录时才移除 team，因此目录里踢掉一个人可能到下次登录才生效。

**2026-09-13 决定（Forgejo 侧文档已改写）：Forgejo 改为 LDAP 同步 + OIDC 登录的双源形态**——
用户与组由只读 LDAP source 同步，登录由 OIDC 完成，`ACCOUNT_LINKING=auto` 把 OIDC 登录绑到既有
账号。设计见 [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §2.2，
验收条目 `FORGEJO-R-063`—`R-065`（原 `FORGEJO-R-006` 已废弃），实现未开始。对本方案的影响：

1. Forgejo 自此**保存目录副本**，落入[目录事件订阅要求](../../../../dev-docs/requirements/directory-event-subscription.md)
   的适用范围（该文档 §1 覆盖"所有直接通过 LDAP/LDAPS 读取 Samba 用户、组、账号状态或目录属性的
   Module"），必须订阅[目录事件日志](../../../../docs/architecture/directory-event-journal.md)并在
   声明的最大传播时间内完成增量刷新——**组撤权不再等下次登录**，这正是本节需要的能力。机制已经
   存在（Samba dsdb 审计 → `events.jsonl` → 各订阅者带自己的游标，authentik 与 Casdoor 的 dirwatch
   已实现），Forgejo 只是再加一个同类订阅者，不需要新写一份机制文档。
2. 能力组的投影路径随之变化：双源落地后 `CAP_ai_agent_*` 可以经 **LDAP source 的组→team 映射**
   到达 Forgejo，随同步与事件刷新；OIDC 的 `--group-team-map`（`FORGEJO-R-060`）是双源落地前的过渡
   路径。两条路径产出的都是 Forgejo team，本方案的读法（以用户身份 `GET /user/teams`）不变。
3. `auto` 的安全性依赖三个前提：IAM 禁止自助修改 `mail`/`sAMAccountName`（已写进
   [IAM Provider 要求](../../../../dev-docs/requirements/iam-provider.md) §1.6）、部署只有一个
   OIDC/OAuth source、邮箱别名与用户名不回收再分配。任一不成立则降级为 `ACCOUNT_LINKING=login`。

在该订阅落地前（以及作为兜底），控制面保留**即时否决表** `agent_grant_deny`：优先于一切推导，
`agent-grant deny <user>` 立即生效，用于离职、误授权与紧急止血。

### 6.3 判定顺序

```text
webhook 事件
  → 解析 sender、repo、issue、目标 agent、请求动作
  → 仓库是否已启用 Agent？（否 → 丢弃）
  → agent_grant_deny 命中？（是 → deny）
  → 部署级 agent_grant 覆盖 sender 与该 agent？（否 → deny）
  → 仓库权限推导 ⊕ 覆盖条目 → 动作集，与部署级上限取交集
  → issue 模板答案、标签与命令只能收窄
  → 动作 ∈ 集合？（否 → deny，回一条可解释评论）
  → execute：检查分支模式、目标分支保护状态、预算与并发
  → 建 job，判定依据写入 decision 表；作业开始前再判定一次
```

Agent 自己发起的写操作（§3.5 的提议档与自主档）不是 webhook 事件，但走**同一条链**：sender 取本轮
的发起人或点确认的人，请求动作取白名单里的具体操作，判定结果同样落 `decision`。细则见 §6.5。

### 6.4 执行权细则

| 项 | 默认 | 可选 |
| --- | --- | --- |
| 工作分支 | 新建 `ai/<issue>-<plan-version>` | 模板字段或 `ai:branch/<name>` 指定 |
| 目标分支 | 仓库默认分支 | `ai:target/<branch>` |
| 直接提交目标分支 | 禁止 | `ai:direct`，需仓库策略开启且目标分支未受保护 |
| 完成动作 | 开 PR，issue 打 `ai:needs-review` | `/no-pr` 直接合并（同上受限） |
| 合并 | 人工 | Agent 自审留评论；低风险类别可按策略自动合并 |
| commit 身份 | Agent 账号（author/committer），trailer 记录发起人与 issue | 可配置展示名 |
| 执行依据 | 批准时冻结的 `(文档路径, commit SHA)` | —— |

**不使用发起人的凭据代跑**：Forgejo 按凭据所有者归属推送与活动流，冒用会污染审计，且该凭据携带
此人全部仓库权限；改 commit 身份只是展示层。Agent 一律用自己受限到该仓库的 token/SSH key。

### 6.5 Agent 发起的写操作

> 状态：**提案**。需求条目 `AGENT-R-072`—`R-075`、`R-077`，里程碑 M9。

L1 触发权回答"谁能让 Agent 做什么"，这一节回答"Agent 自己提出来的动作算谁的"。原则一句话：
**Agent 不是独立的权限主体，它的每个写操作都必须能归属到一个人**。

| 档（§3.5） | 归属的人 | 判定时机 | 动作集上限 |
| --- | --- | --- | --- |
| 派生 | 触发上游动作的人（批准执行、打 `ai:plan` 等） | 上游动作判定时一并授出 | 不超过上游动作本身 |
| 提议 | 点 👍 / `/accept` 的人 | 确认到达时判定 | 该人的动作集 |
| 自主 | 发起本轮的人 | 入队前判定一次，落地前再判定一次 | 该人的动作集 ∩ 仓库 `agent_write_ops` |

配套约束：

- 自主档需要仓库显式开启（`repo_settings.agent_issue_tier = autonomous`），部署默认 `proposed`；
  与 §6.3 一致，仓库侧只能收窄，不能放大目录组给出的上限；
- 白名单 `agent_write_ops` 逐项开关（`create_issue`、`comment`、`set_labels`、`link_issue`、
  `open_pr`），未列出的操作一律 deny；**新增操作默认关闭**，不随版本升级自动获得；
- 归属的人被 `agent_grant_deny` 命中或动作集缩小时，**在途提议立即失效**，不按提出时的权限结算；
- 每个落地的写操作在 `decision` 里记 `(tier, 归属人, 操作, 配额余量)`，拒绝同样记录。

### 6.6 数据形态

```sql
agent_grant(id, forgejo_user_id, username, agent NULL, max_action, source, granted_by, created_at)
agent_grant_deny(id, forgejo_user_id, agent NULL, reason, created_by, created_at)

repo_settings(repo_id PRIMARY KEY, enabled, sync_mode, trigger_mode,
              chat_agents TEXT[], host_agent, exec_agent, models JSONB, efforts JSONB,
              allow_direct_commit, branch_pattern, daily_budget_usd, max_concurrent,
              agent_write_ops TEXT[], agent_issue_tier,
              issue_quota_per_job, issue_quota_per_day,
              updated_by, updated_at)

policy_override(id, repo_id, issue_number NULL, agent NULL, user_id NULL,
                actions TEXT[], created_by, created_at)

discussion(id, repo_id, issue_number, agent, session_id, runtime_version,
           host_agent BOOL, state, last_event_id, updated_at)

comment_provenance(comment_id PRIMARY KEY, repo_id, issue_number, agent,
                   session_id, turn_id, kind, created_at)   -- kind: reply|status|minutes|milestone

issue_provenance(repo_id, issue_number, agent, job_id, turn_id, tier,
                 created_by_user_id, created_at, PRIMARY KEY (repo_id, issue_number))
                 -- tier: derived|proposed|autonomous

issue_proposal(id, repo_id, parent_issue_number, agent, job_id, turn_id,
               title, body, labels TEXT[], dedup_fingerprint, tier,
               state, decided_by, decided_at, created_issue_number, created_at)
               -- state: pending|accepted|rejected|expired|deduped

job(id, repo_id, issue_number, agent, action, plan_path, plan_commit_sha,
    branch, due_at, estimate_seconds, priority,
    lease_owner, lease_until, attempts, state, created_at)
```

## 7. 事件入站

- **一次系统 webhook 覆盖全实例**（`POST /admin/hooks`），订阅 `issues`、`issue_assign`、
  `issue_label`、`issue_comment`、`pull_request`、`pull_request_comment`、`action_run_*`；也可退化
  为按组织或按仓库注册，由 `ai_agent` 的 reconcile 维护。
- ingress 校验 `X-Forgejo-Signature`（HMAC-SHA256）与仓库白名单，落 `inbox` 后立即 `202`。
- **投递语义待核实**（超时、重试、可否手动重投）；结论出来前按"至多一次"设计，保留按 `updated_at`
  游标的周期对账。
- 过滤自身回写：忽略 `sender` 为 Agent 账号的事件，并用 outbox 的 `run_id` 二次去重。

## 8. 记录、审计与用量

`run_event`（append-only 事件）、`turn`（每轮模型交互与 token/花费）、`job`、`decision`（每次授权
判定含拒绝）、`artifact`、`usage_rollup`（每日 × 用户 × Agent × 仓库）。大产物写 workspace 数据树或
`object_storage`，库里只存 URI 与 sha256；写入前脱敏；分层保留（事件/轮次 90 天，job/decision/rollup
长期，产物 30 天）；`usage-export` 导出对账。

issue 评论、PR 与 Actions run 本身就是**人类可读的记录面**，因此库里的记录只服务审计、成本与恢复，
不需要把过程复述到 issue 上。

### 8.1 会话视图：不内嵌厂商界面，自己渲染

各家 Agent 的 Web 界面属于其云端产品、绑定各自账号，自托管部署既嵌不进来也无法共享登录态。改为
自己渲染，数据来自本来就要记录的 `turn`/`run_event`：

| 层级 | 对应关系 | 呈现 |
| --- | --- | --- |
| issue | 一个长期会话（`discussion`） | 会话视图：按轮次展开的完整问答、每轮模型、思考强度与用量 |
| 执行作业 | 一个独立运行（`job` + `run_event`） | 运行视图：工具调用、文件变更、测试输出、diff、耗时与花费，可实时跟随 |

首期入口是 Module 命令（`agent job show --follow`、`agent session show`），
[管理前端](../../../../dev-docs/plans/archived/web-api-admin-console.md)已可用；状态评论里给出这两个视图的链接。

#### 8.1.0 上下文怎么拼：不要把 Agent 自己说过的话喂回去

每条 Agent 评论都记录来源 `(agent, session_id, turn_id)`（`comment_provenance` 表）。给某个 Agent
组装增量上下文时按来源过滤：

| 评论来源 | 会话仍在 | 会话丢失需重建 |
| --- | --- | --- |
| 人类 | **带上**（增量部分） | 带上（全量） |
| **该 Agent 自己** | **剔除**——它的会话里已经有了，重复喂等于花钱强化自己的观点，还会让模型误以为被追问 | 带上（全量），并标注是它此前的发言 |
| 其他 Agent | **带上**，并标注发言身份（如 `[claude]:`） | 带上，同样标注身份 |
| **Agent 建的 issue 正文与提议块**（§3.5） | 按 `issue_provenance` 归属的 Agent 套用上面两行：自己建的剔除，别的 Agent 建的带上并标注 | 同左 |
| 状态评论、进度更新 | 剔除 | 剔除（它是渲染产物，不是事实） |

这条规则在圆桌讨论里尤其重要：主持 Agent 需要看到别人的发言，但不需要回放自己的；反过来，重建
上下文时必须把它自己的历史发言补回去，否则它会与自己此前的结论矛盾。判断依据是 provenance 记录，
不是"评论作者是不是 bot"这种粗粒度判断。

#### 8.1.1 issue 就是 Agent 里的一个主题

`discussion(repo, issue, agent) → session_id` 是 1:1 持久映射：每条新评论 `resume` 同一会话，执行
作业从该会话 `fork` 出运行会话，使执行不污染讨论上下文。各家 CLI 都提供会话续接原语，具体行为按
固定版本复核。会话是**缓存不是事实来源**，持久化与降级见 §5.7。

#### 8.1.2 终端附着（可选）

常驻容器里以 PTY 运行 Agent CLI，经 WebSocket 接到管理前端的终端组件，管理员看到的就是 CLI 原界面。
定位是**调试与旁观**：

- **Samba 管理员组**（`SAMBA_DC_ADMIN_GROUP_NAME`，默认 `Admins`；该组已映射为 Forgejo 站点管理员）
  成员**直接可用**，不需要额外授权；非管理员必须显式加入 `CAP_ai_agent_terminal` 才能附着——该组
  存在的意义就是"把终端开放给不是管理员的人"，默认为空；仓库权限再高也不构成终端权限；
- 默认**只读附着**；输入需要 `execute` 级授权，且每次输入记为归属到该人的 `turn`，与评论触发的
  交互同等审计；
- 附着会话有 TTL 与并发上限，断开即结束，不改变作业状态机；
- 不作为主入口：终端里的自由输入绕过 §6 判定与 outbox 幂等。

### 8.2 中间步骤不进评论

Agent 的工具调用、文件读写、命令执行是高频事件，**不发评论**：每条评论都会触发订阅者通知，几十条
过程记录会淹没 issue 的对话价值，也让编辑历史失去意义。回写分三档：

| 粒度 | 去向 | 频率 |
| --- | --- | --- |
| 每个工具调用、每次文件变更、每行输出 | `run_event` + 运行视图（实时流） | 全量 |
| 当前进度（"最近步骤"一行、百分比、预算消耗） | **状态评论**，`PATCH` 原地更新 | 节流：阶段变化或 ≥30 秒一次 |
| 里程碑（进入执行、方案已提交、PR 已开、失败需人介入、被上限截断） | **新评论**（或 reaction） | 事件驱动，通常一个作业 2–4 条 |

拆出执行 issue 后（§3.7），上面三档全部落在**执行 issue** 里：作业状态评论定时原地更新、里程碑另发
评论、结束一条含日志附件的总结评论；父 issue 只在作业结束时收到一条摘要并解除阻塞。轻量模式不拆
issue 时，三档落在原 issue，节流阈值更保守（阶段变化才更新，不做秒级刷新）。

完整日志作为附件挂在结束评论上，链接指向运行视图与 commit。

## 9. 与既有 ANAS 资产的关系

| 资产 | 关系 |
| --- | --- |
| `forgejo` Module | 协作面与代码面；需要新增：Agent 账号与 token 的管理端调用、系统 webhook 注册、OIDC 增加 `--group-team-map`、目录事件订阅者 |
| Forgejo Actions + Runner | 可选执行后端（§5.4）；默认不使用，但与 `ai_agent` 共用 `incus` Provider |
| `incus` Provider | 两个消费者（Actions Runner、`ai_agent`），各自独立 project 与证书 |
| [目录事件日志](../../../../docs/architecture/directory-event-journal.md) | 新增 Forgejo 订阅者以消除组变更的登录延迟（§6.2） |
| 控制台 | [Web API 与管理前端](../../../../dev-docs/plans/archived/web-api-admin-console.md) 已实现；Agent 状态与配置仍可首期走 Module 命令 |
| `llm_gateway`（提案） | 统一模型 key、预算与审计；需要独立选型文档后再立项 |

## 10. 安全边界

| 风险 | 控制 |
| --- | --- |
| issue/评论提示注入 | issue 正文、模板答案与评论都是不可信输入，只能收窄；授权只看判定结果；禁止从评论拼接 shell |
| 管理凭据滥用 | 只用于建账号/发 token/加 key/注册 webhook，单独审计与轮换，不进执行实例 |
| token 过宽 | 每个 Agent 的 token 按 scope + `repositories` 双重限定；执行阶段才追加写权限 |
| 生成代码破坏宿主 | 一次性实例、无宿主挂载、无 socket、资源上限与 TTL；需要硬边界时切 `vm` |
| 跨仓库越界 | 工作实例按仓库切分，跨仓库绝不共用（§5.6）；不受信来源升级为 `job` 档一次性实例 |
| 跨 issue 串味 | 每 issue 独立 worktree、会话与分支；预算分别计账 |
| 直接提交绕过评审 | `ai:direct` 默认关闭，需仓库策略开启且目标分支未受保护 |
| 终端附着被滥用 | 管理员组默认可用、非管理员需 `CAP_ai_agent_terminal`；默认只读，全程录制归属 |
| 多 Agent 讨论失控 | 主持 Agent 唯一发起权 + 轮数/预算/时长硬上限，截断时明示 |
| `actions` 后端 workflow 篡改 | 只派发默认分支上的 workflow，要求分支保护，凭据不做 Actions secret |
| 重复执行 | inbox 去重 + job 租约 + outbox idempotency key + 计划绑定 commit SHA |
| 事件丢失 | 快速落库 + 周期对账 |
| 记录泄密 | 写入前脱敏、分层保留、导出需权限 |
| Agent 刷 issue / 自喂闭环 | 三档授权（§3.5）默认需人确认；自主档有每作业与每日配额、标题指纹去重、`ai:proposed` 溯源；建出来的 issue 不带 `ai:auto`，不触发下一轮 |
| 仓库内文件篡改 Agent 工具面 | 写路径只在控制面（§5.8）；`.anas-agent.yml` 只描述约定与开关，不描述权限；运行时规则文件由控制面渲染，不接受仓库内同名文件覆盖 |
| Agent 跨仓库写 | token 按 scope + `repositories` 双重限定（§4）；跨仓库写需按"仓库对"显式授权，首期不开（§14） |
| 任意出网 | 默认 deny + allowlist（模型 API、Forgejo、DNS/NTP、批准的依赖镜像源） |

信任模型：单租户、成员可信，安全结构完整实现但 P1 不以对抗内部恶意用户为门槛；沙箱隔离不放宽，
因为模型生成的代码与其依赖始终是不可信执行。

## 11. 落地前必须在 `forgejo 15.0.7` 上验证

> **已复核（2026-09-06）**：下表 24 项在固定镜像上实测完成、0 项失败，结论与由此产生的实现约束记录在
> [要求文档 §15](../../dev-docs/requirements/ai-agent.md)。
> 仍未复核的只剩两项：webhook 的投递语义（超时、重试、可否重投）与 `issue_label` / `issue_assign`
> 的 payload 字段，两者都需要一个真实接收端。下表保留为复核清单，不再是开工前提。

| 待验证 | 影响 |
| --- | --- |
| token 的 `repositories` 限定是否存在（`16.0-dev` 已确认） | 最小权限模型；不具备则退化为每 Agent 每仓库一个账号 |
| 系统 webhook（`/admin/hooks`）的事件集合与 payload | 一次注册覆盖全实例是否可行 |
| webhook 投递语义：超时、重试、可否手动重投 | 对账策略强度 |
| issue 表单模板的字段类型、答案在 body 中的渲染格式、front matter 自动打标签 | §3.2 的模板方案能否成立 |
| `GET .../issue_templates` 与 `issue_config` 的返回 | 模板生成后的校验方式 |
| contents API 单文件提交与 `POST /pulls` 的组合 | 引导 issue 无需克隆即可落配置 |
| issue dependencies 的语义与事件 | `/split` 的父子关系表达 |
| `issue_label` / `issue_assign` payload 的字段与差异表达 | 授权判定与状态机 |
| 评论 `PATCH`、reaction、附件 API 行为 | 状态评论、轻量确认与日志附件 |
| 管理端建用户/发 token/加 SSH key 的字段与限制 | 无人值守引导 |
| `--group-team-map` / `--group-team-map-removal` 的行为与同步时机 | §6.2 链路与撤销延迟 |
| tracked time 能否以 Agent 账号身份记账、`due_date` 时区语义 | 排程与耗时回写 |
| 各 Agent CLI 会话在容器重建、版本升级后的续接行为 | §5.7 降级策略强度 |
| Incus 系统容器的 `exec`+stdin、非特权配置与配额 | `lxc` 默认档 |
| Projects API 后续 PR 的合并进度与端点形态 | `sync_board()` 何时可实现 |
| 置顶 issue 的数量上限、排序语义与 `new_pin_allowed` 行为 | §3.6 队列展示是否可行 |
| 建 issue API 能否在创建时一并打标签与建依赖，创建者归属与速率限制 | §3.5 提议档与自主档的落地形态 |
| 依赖 API 在 issue 关闭/重开时的行为与事件 | `/execute after` 与执行 issue 解除阻塞 |
| contents API 提交文档时的作者归属、并发冲突（SHA 不匹配）处理 | §3.3 文档由控制面提交的可靠性 |

## 12. 演进路线

| 阶段 | 交付 | 退出判据 |
| --- | --- | --- |
| P0 事实验证 | §11 的验证脚本与结论 | 全部有结论，降级路径确定 |
| P1 讨论闭环 | Module 与账号自动引导、系统 webhook + inbox + 对账、issue 模板生成与解析、单 Agent 讨论、状态评论与反应、产物进 Git、引导 issue、Agent 提议建 issue（提议档）、记录与用量 | 指派/@/标签/模板能触发；重复投递 10 次只产生一次评论；未授权 sender 被拒；重启后可恢复；每轮交互有用量记录；未经人确认的提议不会创建 issue |
| P2 执行面 | `incus` Provider 落地后接入一次性实例、分支与 PR 策略、预估与截止校验、预算与取消 | 一个真实 issue 从讨论到 PR 全链路可复现；取消能终止实例；保护分支不可直推 |
| P3 组队与治理 | 多 Agent 讨论与主持、`/split` 与 `/summarize`、执行 issue 与队列总览、并行/顺行、夜间与空闲调度、目录事件订阅、终端附着、控制台视图、`anas-agent-mcp` 写路径与自主档配额 | 多 Agent 讨论能在硬上限内收敛并产出文档；队列顺序在置顶 issue 与依赖上一致可见；组变更秒级生效；自主档超配额或命中去重时自动降级回提议档 |
| P4 扩展 | 更多运行时接入、`llm_gateway` 与虚拟 key、`actions` 后端、外部仓库 | 新增运行时只改注册表与 adapter，不改状态机与表结构 |

## 13. 备选与否决

- **依赖 Projects 看板做状态机**：当前无 API 无事件，做不到；看板只作人类视图。
- **把文档、方案与代码留在评论里**：无版本、无 diff、不能 review、编辑即丢历史。
- **用聊天记录作为执行依据**：执行只认批准时冻结的文档与 commit SHA。
- **中间执行步骤逐条发评论**：通知噪音淹没对话价值；改为状态评论节流 + 运行视图实时流（§8.2）。
- **用 `ROLE_*` 表达 Agent 使用权**：那是管理员按组织结构维护的职责组；应用能力组改用
  `CAP_<module-id>_<capability>` 并放进独立 `OU=Cap`。
- **给 issue 增加"最早开始时间"字段**：上游没有该字段；延后用"暂不批准"表达。
- **让用户填写预估时长**：预估由 Agent 读过冻结方案后给出，并用历史实际耗时校准。
- **让 Agent 常驻一个实例、按 Agent（而不是按仓库）切分工作区**：Agent 的工具循环会执行任意命令，
  多仓库共处一个容器等于让 A 仓库的命令能读写 B 仓库；按仓库切分才与信任边界一致（§5.6）。
- **把模型与思考强度写进标签**：它们在对话中反复调整，每次都会产生 `issue_label` 事件，把 issue
  时间线刷成参数流水；标签只承载授权与流程控制。
- **用"思考中"占位评论**：占位评论事后要删或要改，弄脏时间线与通知；改用 reaction + 状态评论阶段行。
- **给每个分支加锁**：不同 issue 走不同分支本就不撞；真正要防的是重复触发（租约与幂等）与共享目标
  分支的直接提交（仅此场景加锁）。
- **把终端附着作为主入口**：自由输入绕过判定与幂等；只作管理员的只读旁观与授权后临时介入。
- **内嵌各家 Agent 的 Web 界面**：绑定其云端账号，自托管无法嵌入或共享登录态。
- **借发起人凭据代跑、只改 commit 身份**：污染审计、泄露面过大。
- **在 `forgejo` Module 内实现编排**：编排器要消费模型 API、compute 与预算治理，塞进应用 Module 会
  越权且与上游版本节奏绑死。
- **在仓库里放一个"控制 Forgejo"的 skill 或工具定义，并把可写 token 发进沙箱**：仓库内文件谁有
  写权限谁就能改，与 §5.4 限制 `actions` 后端是同一个敞口；这条路径还绕过 §6.3 判定、outbox 幂等
  与预算记账，并把 issue 正文这类不可信输入直接接到写接口上。写路径留在控制面（§5.8）。
- **给每个仓库统一"安装"一份 Agent 能力文件**：能力是部署级的，随注册表与 §6 变化；往每个仓库
  复制一份只会漂移，且改它不需要经过 §6。仓库层只保留 `.anas-agent.yml` 里的约定与开关，且只能
  收窄（§6.3）。
- **让 Agent 自主建的 issue 自动触发下一轮 Agent**：自己写输入、自己读输入的闭环，烧预算且会把
  模型的猜测固化成"事实"；建出来的 issue 不带 `ai:auto`，要人接手才进入讨论。
- **让 Agent 成为独立的权限主体**：无法审计、无法撤销。每个写操作都归属到一个人（§6.5）。
- **在 ANAS 核心宿主跑特权容器执行生成代码**：与 Forgejo 既有结论一致，否决。

## 14. 后续文档

配套的[要求](../../dev-docs/requirements/ai-agent.md)（需求矩阵）与[实施计划](../../dev-docs/plans/ai-agent.md)
（里程碑、需求归属与 e2e 记录）已经存在，逐条状态以计划为准，本节不复述。

需要同步决策或修改的既有文档：

| 文档 | 变更 | 状态 |
| --- | --- | --- |
| [Samba AD 用户与权限规划](../../../../docs/architecture/samba-ad-user-planning.md) | 登记 `CAP_<module-id>_<capability>` 类别与 `OU=Cap,OU=Groups`；这是所有 Module 的通用规则，不只服务 AI | 已采纳并实现（§5.4.1）：`samba_dc` 按 `create_structure` 创建 `OU=Cap`，Module 经 `ANAS_IDENTITY_CAPABILITY_GROUPS` 声明能力码 |
| [Forgejo Module 要求](../../../../modules/forgejo/dev-docs/requirements/forgejo-module.md) | Agent 账号与 token 的管理端引导、系统 webhook 归属、OIDC 增加 `--group-team-map` | 已登记为 `FORGEJO-R-060`—`R-062`（M6）；`--group-team-map` 的实现待做 |
| [目录事件日志](../../../../docs/architecture/directory-event-journal.md) | 增加 Forgejo 订阅者，消除组变更的登录延迟（§6.2） | **已决定并登记**（2026-09-13）：Forgejo 设计 §2.2 已改写为双源形态，`FORGEJO-R-063`—`R-065` 与其计划 M7 已建立；实现未开始 |
| [LLM Gateway 要求（待讨论）](../../../../dev-docs/requirements/llm-gateway.md) | 统一模型 key、虚拟 key、预算执行点、用量归因与审计 | 问题域已锁定，选型调研与需求矩阵待做 |
| `anas-agent-mcp` 工具面契约 | §5.8 白名单操作的入参出参、幂等键、错误语义与版本策略，需独立一份接口文档 | 未开始 |
| 跨仓库写授权（"仓库对"模型） | Agent 在 A 仓库的讨论里给 B 仓库建 issue 的授权形态；§5.6 的信任边界要求显式配对，不能靠放宽 token 顺手实现 | 未开始 |
