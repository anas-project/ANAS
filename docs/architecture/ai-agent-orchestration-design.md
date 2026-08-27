# AI Agent 编排设计（Forgejo 基线）

> 状态：**提案**。本文描述的 Module、配置项、命令与表结构当前**均不可执行**。协作面用已集成
> `forgejo` Module 的 issue、label 与 Projects 看板，代码面用同一实例的仓库。更新：2026-08-26。

执行面的 Provider 工作见 [Incus compute Provider 要求](/requirements/incus-module)与[实施计划](/plans/incus-module)；
Forgejo 侧的既有边界见 [Forgejo Module 设计](/architecture/forgejo-module-design)；候选运行时的原始
调研见[看板应用接入 AI Agent](/research/kanban-ai-agent-integration-research)。

## 1. 结论

协作对象（issue）、控制信号（label、指派、评论）、代码、分支、PR、CI 与身份**在同一个应用、同一套
权限里**，因此编排器不需要跨应用身份映射、仓库绑定流程或第二套授权模型。

形态：`ai_agent` Module（独立开源项目 `anas-agent` 打包）订阅 Forgejo 系统 webhook，以每个 Agent
一个 Forgejo **专用账号**的身份参与 issue 讨论；执行阶段在一次性隔离实例中改代码、跑测试、推
`ai/*` 分支并开 PR。

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

```markdown
**Agent 状态** · 讨论 claude(主持)+codex · 执行 codex(high)
阶段：执行中（2/5）· 分支 `ai/142-v2` · 预算 0.42 / 2.00 USD · 截止 09-02
最近：`go test ./internal/...`（12s 前）
方案文档 docs/requirements/foo.md@a1b2c3d · 运行视图 → anas agent job show 318
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
| `research.yaml` | 调研 | `reply`+`plan` | 只读；强制禁止写代码，产出调研文档 |

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
| 模型 | dropdown ×N | 每个可选 Agent 一个字段，选项是该运行时的允许模型 |
| 思考强度 | dropdown ×N | 同上，取值用各运行时**自己的原生命名**（§5.5） |
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
3. **同步到标签与指派**：写 `ai:chat/*`、`ai:host/*`、`ai:exec/*`、`ai:model/*`、`ai:effort/*`、
   `ai:branch/*`、`ai:exec-when/*`，并把讨论 Agent 指派到 issue（模板做不到指派）；
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
  `docs/requirements/`、实施计划进 `docs/plans/`，遵循[文档写作标准](/developer/documentation-standard)
  的分类；**这些位置以后可能调整**，届时只改仓库配置，历史产物不迁移——评论里的链接指向 commit，
  永远有效；
- 每次产物更新 = 一次 commit（消息含 `issue #<n>` 与阶段），随即 push 到工作分支；
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

### 3.7 Projects 看板的处置

当前没有 API 也没有事件，Agent 无法读或移动卡片。因此：

- 机器可读状态一律落在 **label + issue 开闭 + 指派**；
- Projects 看板作为人类视图，由人拖动，或用 Forgejo 自身"关闭即入 Done 列"的行为；
- 不设计任何依赖看板列的自动化。

上游正在补这块能力（PR 13700 是重构基础，真正的 Project API 在后续 PR）。跟踪方式：列入 §11 复核项，
`board` 适配器预留 `sync_board()`（首期空实现）。API 可用后接入的效果是"Agent 把卡片移到对应列"，
属于展示增强，**不改状态机**。

### 3.8 标签词表

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
| `ai:model/<agent>=<model>` | 模型覆盖 | write |
| `ai:branch/<name>`、`ai:target/<branch>` | 工作分支 / 目标分支 | write |
| `ai:pr` / `ai:direct` | 完成后开 PR / 直接提交到目标分支 | `ai:direct` 需仓库策略允许 |
| `ai:exec-when/now`、`ai:exec-when/at=<时间>`、`ai:exec-when/on=<事件>` | 执行时机（§3.6） | execute 权限 |
| `ai:effort/<agent>=<原生取值>` | 思考强度用该运行时自己的命名（§5.5） | write |
| `ai:at-risk` | 预估无法在截止前完成 | 仅 Agent |
| `ai:running`、`ai:needs-review`、`ai:failed` | Agent 维护的状态标签 | 仅 Agent |

评论命令（`/plan`、`/approve`、`/stop`、`/chat`、`/exec`、`/model`、`/effort`、`/branch`、`/budget`、
`/due`、`/execute now|at|on|hold`、`/summarize`、`/split`，含可配置的中文别名如 `/立即执行`）与标签
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
project 与证书。范围与验收见[要求](/requirements/incus-module)与[计划](/plans/incus-module)。

### 5.3 隔离档

| 档 | 边界 | 资源 | 适用 |
| --- | --- | --- | --- |
| `container`（常驻） | 命名空间隔离 | 按需，亚秒启动 | 讨论、计划（不执行仓库代码） |
| `lxc`（**默认执行档**） | 共享宿主内核 + user namespace + seccomp/AppArmor | 无内存预留，秒级启动，I/O 接近原生 | 改代码、装依赖、跑测试 |
| `vm` | 独立 guest kernel + 硬件虚拟化 | 需预留内存，数秒启动 | 需要硬边界：外部贡献者仓库、不受信依赖 |

本质差别只有共享内核这一条；Incus 对二者使用同一套 project/quota/exec 接口。系统容器 interface 已
登记为 [Incus 计划](/plans/incus-module) M5。

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

配套规则：preflight 失败保持 disabled 且不影响其他运行时；连续失败自动熔断；版本升级即配置变更
（禁止运行时自动升级 CLI，保留 N-1 回滚）；凭据过期监控；订阅席位作为受限资源用信号量调度。

### 5.6 多 issue 之间的隔离与工作区

**先澄清运行时现状**：Codex、Claude Code、Pi 的 CLI 都是"在给定工作目录里干活"，它们自己**不管理
git worktree**——没有内建的"每个任务一个隔离检出"。准备工作区是宿主（本方案）的责任，这也是必须
自己设计隔离的原因。

两个阶段用两种工作区：

| 阶段 | 工作区 | 生命周期 | 在哪运行 |
| --- | --- | --- | --- |
| 讨论 / 计划（只读） | 从仓库的共享 bare mirror 派生的 `git worktree`，每 issue 一个，只读挂载 | 与 issue 同寿，`ai:silent` 或关闭后回收 | **常驻容器**内，同容器多 issue 并发 |
| 执行（读写） | 一次性实例内的 **fresh clone**，检出到冻结的 commit SHA，建工作分支 | 单个作业，结束即随实例销毁 | **每作业一个一次性 Incus 实例**（默认 LXC 系统容器） |

"全新实例"指的是**一次性 Incus 实例**，不是"给每个 issue 常驻一个容器"。讨论期不为每个 issue 起
容器：每个 Agent 一个常驻容器，容器内按 issue 分工作目录与会话目录，多个 issue 的会话在同一容器里
并发（进程级并发，受注册表 `limits.concurrency` 约束）。这样长期占用的只有"每个 Agent 一个容器"，
而不是"每个 issue 一个容器"。

并发与隔离的四条硬边界：

| 维度 | 规则 |
| --- | --- |
| 会话 | `discussion(repo, issue, agent) → session_id`，会话目录 `agent-sessions/<repo>/<issue>/<agent>/`，互不可见 |
| 工作区 | 讨论期每 issue 一个只读 worktree；执行期每作业 fresh clone，**永不复用上一个作业的工作区** |
| 分支 | 每 issue（每计划版本）一个分支 `ai/<issue>-<version>`；同一分支同时只允许一个作业，按 `(repo, branch)` 加锁 |
| 并发与预算 | 每仓库写作业默认并发 1（可配），跨仓库可并行；预算按仓库与主体分别计账，一个 issue 跑超不影响其他 issue |

因此两个 issue 改同一个文件也看不到对方的中间状态：各自从冻结 SHA 出发、各自分支、各自实例，
冲突只在 PR 合并时出现，由人或后续作业解决。共享 bare mirror 只做只读加速（`git fetch` 一次、多个
worktree 复用对象库），执行期不使用它，避免作业写入污染共享对象。

### 5.7 会话如何在容器销毁后存活

常驻容器是**无状态**的，会话状态全部落在持久卷上：

- 会话文件写 `agent-sessions/<repo>/<issue>/<agent>/`，`session_id` 与运行时版本存库；容器空闲回收
  或升级重建后，挂同一路径 `resume` 即可继续；
- 每次收敛点（讨论结束、计划提交、作业结束）把会话目录打包成 artifact 备份，保留期与 issue 关联，
  issue 关闭后按保留策略清理；
- **续接失败不是错误路径**：跨运行时版本、跨主机、文件损坏都可能续不上，降级是从**仓库文档 +
  issue 时间线 + 状态评论**重建上下文——这三者才是持久事实，且人与 Agent 共享同一份；
- 因此长会话按轮次或 token 阈值主动压缩：生成纪要、丢弃过程细节、重新锚定到冻结的方案文档。

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
备份筛选与访问控制，也符合[命名规范](/architecture/samba-ad-user-planning) §5.5"一个组只表达一种
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

**撤销延迟与实时同步**：组声明只在用户**登录时**随 claim 到达 Forgejo，`--group-team-map-removal`
也在登录时才移除 team，因此目录里踢掉一个人可能到下次登录才生效。ANAS 已经有解决这类问题的机制：
[目录事件日志](/architecture/directory-event-journal)（Samba dsdb 审计 → `events.jsonl` → 各订阅者
带自己的游标，authentik 与 Casdoor 的 dirwatch 已实现）。方案是**给 Forgejo 增加同类订阅者**：
监听 `OU=Cap` 与 `OU=Apps` 下相关组的 `member` 变更，立即调用 Forgejo API 增删对应 team 成员，把
延迟压到秒级；`ai_agent` 也可直接订阅同一日志刷新 `agent_grant` 快照。这属于 `forgejo` Module 与
目录事件日志的扩展，登记在 §14，不需要新写一份机制文档。

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

### 6.5 数据形态

```sql
agent_grant(id, forgejo_user_id, username, agent NULL, max_action, source, granted_by, created_at)
agent_grant_deny(id, forgejo_user_id, agent NULL, reason, created_by, created_at)

repo_settings(repo_id PRIMARY KEY, enabled, sync_mode, trigger_mode,
              chat_agents TEXT[], host_agent, exec_agent, models JSONB, efforts JSONB,
              allow_direct_commit, branch_pattern, daily_budget_usd, max_concurrent,
              updated_by, updated_at)

policy_override(id, repo_id, issue_number NULL, agent NULL, user_id NULL,
                actions TEXT[], created_by, created_at)

discussion(id, repo_id, issue_number, agent, session_id, runtime_version,
           host_agent BOOL, state, last_event_id, updated_at)

comment_provenance(comment_id PRIMARY KEY, repo_id, issue_number, agent,
                   session_id, turn_id, kind, created_at)   -- kind: reply|status|minutes|milestone

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
[管理前端](/plans/web-api-admin-console)可用后升级为页面；状态评论里给出这两个视图的链接。

#### 8.1.0 上下文怎么拼：不要把 Agent 自己说过的话喂回去

每条 Agent 评论都记录来源 `(agent, session_id, turn_id)`（`comment_provenance` 表）。给某个 Agent
组装增量上下文时按来源过滤：

| 评论来源 | 会话仍在 | 会话丢失需重建 |
| --- | --- | --- |
| 人类 | **带上**（增量部分） | 带上（全量） |
| **该 Agent 自己** | **剔除**——它的会话里已经有了，重复喂等于花钱强化自己的观点，还会让模型误以为被追问 | 带上（全量），并标注是它此前的发言 |
| 其他 Agent | **带上**，并标注发言身份（如 `[claude]:`） | 带上，同样标注身份 |
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

完整日志作为附件挂在结束评论上，链接指向运行视图与 commit。

## 9. 与既有 ANAS 资产的关系

| 资产 | 关系 |
| --- | --- |
| `forgejo` Module | 协作面与代码面；需要新增：Agent 账号与 token 的管理端调用、系统 webhook 注册、OIDC 增加 `--group-team-map`、目录事件订阅者 |
| Forgejo Actions + Runner | 可选执行后端（§5.4）；默认不使用，但与 `ai_agent` 共用 `incus` Provider |
| `incus` Provider | 两个消费者（Actions Runner、`ai_agent`），各自独立 project 与证书 |
| [目录事件日志](/architecture/directory-event-journal) | 新增 Forgejo 订阅者以消除组变更的登录延迟（§6.2） |
| 控制台 | [Web API 与管理前端](/plans/web-api-admin-console) 未实现，Agent 状态与配置首期走 Module 命令 |
| `llm_gateway`（提案） | 统一模型 key、预算与审计；需要独立选型文档后再立项 |

## 10. 安全边界

| 风险 | 控制 |
| --- | --- |
| issue/评论提示注入 | issue 正文、模板答案与评论都是不可信输入，只能收窄；授权只看判定结果；禁止从评论拼接 shell |
| 管理凭据滥用 | 只用于建账号/发 token/加 key/注册 webhook，单独审计与轮换，不进执行实例 |
| token 过宽 | 每个 Agent 的 token 按 scope + `repositories` 双重限定；执行阶段才追加写权限 |
| 生成代码破坏宿主 | 一次性实例、无宿主挂载、无 socket、资源上限与 TTL；需要硬边界时切 `vm` |
| 跨 issue 串味 | §5.6 的会话/工作目录/分支/预算四条边界 |
| 直接提交绕过评审 | `ai:direct` 默认关闭，需仓库策略开启且目标分支未受保护 |
| 终端附着被滥用 | 管理员组默认可用、非管理员需 `CAP_ai_agent_terminal`；默认只读，全程录制归属 |
| 多 Agent 讨论失控 | 主持 Agent 唯一发起权 + 轮数/预算/时长硬上限，截断时明示 |
| `actions` 后端 workflow 篡改 | 只派发默认分支上的 workflow，要求分支保护，凭据不做 Actions secret |
| 重复执行 | inbox 去重 + job 租约 + outbox idempotency key + 计划绑定 commit SHA |
| 事件丢失 | 快速落库 + 周期对账 |
| 记录泄密 | 写入前脱敏、分层保留、导出需权限 |
| 任意出网 | 默认 deny + allowlist（模型 API、Forgejo、DNS/NTP、批准的依赖镜像源） |

信任模型：单租户、成员可信，安全结构完整实现但 P1 不以对抗内部恶意用户为门槛；沙箱隔离不放宽，
因为模型生成的代码与其依赖始终是不可信执行。

## 11. 落地前必须在 `forgejo 15.0.7` 上验证

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

## 12. 演进路线

| 阶段 | 交付 | 退出判据 |
| --- | --- | --- |
| P0 事实验证 | §11 的验证脚本与结论 | 全部有结论，降级路径确定 |
| P1 讨论闭环 | Module 与账号自动引导、系统 webhook + inbox + 对账、issue 模板生成与解析、单 Agent 讨论、状态评论与反应、产物进 Git、引导 issue、记录与用量 | 指派/@/标签/模板能触发；重复投递 10 次只产生一次评论；未授权 sender 被拒；重启后可恢复；每轮交互有用量记录 |
| P2 执行面 | `incus` Provider 落地后接入一次性实例、分支与 PR 策略、预估与截止校验、预算与取消 | 一个真实 issue 从讨论到 PR 全链路可复现；取消能终止实例；保护分支不可直推 |
| P3 组队与治理 | 多 Agent 讨论与主持、`/split` 与 `/summarize`、夜间与空闲调度、目录事件订阅、终端附着、控制台视图 | 多 Agent 讨论能在硬上限内收敛并产出文档；组变更秒级生效 |
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
- **把终端附着作为主入口**：自由输入绕过判定与幂等；只作管理员的只读旁观与授权后临时介入。
- **内嵌各家 Agent 的 Web 界面**：绑定其云端账号，自托管无法嵌入或共享登录态。
- **借发起人凭据代跑、只改 commit 身份**：污染审计、泄露面过大。
- **在 `forgejo` Module 内实现编排**：编排器要消费模型 API、compute 与预算治理，塞进应用 Module 会
  越权且与上游版本节奏绑死。
- **在 ANAS 核心宿主跑特权容器执行生成代码**：与 Forgejo 既有结论一致，否决。

## 14. 后续文档

方案通过评审后产出 `docs/requirements/ai-agent.md` 与 `docs/plans/ai-agent.md`（需求矩阵、里程碑、
需求归属与 e2e 记录）。

需要同步决策或修改的既有文档：

| 文档 | 变更 | 状态 |
| --- | --- | --- |
| [Samba AD 用户与权限规划](/architecture/samba-ad-user-planning) | 登记 `CAP_<module-id>_<capability>` 类别与 `OU=Cap,OU=Groups`；这是所有 Module 的通用规则，不只服务 AI | 待决策，未提交 |
| [Forgejo Module 要求](/requirements/forgejo-module) | Agent 账号与 token 的管理端引导、系统 webhook 归属、OIDC 增加 `--group-team-map` | 待登记 |
| [目录事件日志](/architecture/directory-event-journal) | 增加 Forgejo 订阅者，消除组变更的登录延迟（§6.2） | 待登记 |
| `llm_gateway` 选型 | 统一模型 key、预算与审计的候选比较 | 未开始 |
