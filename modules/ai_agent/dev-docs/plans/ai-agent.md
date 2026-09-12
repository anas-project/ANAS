---
doc_type: plan
status: implementing
created: 2026-08-27
updated: 2026-09-13
---

# AI Agent 编排实施计划

验收依据是[AI Agent 编排集成要求](../requirements/ai-agent.md)的需求矩阵，设计依据是
[AI Agent 编排设计](../../docs/architecture/orchestration-design.md)。协作面是已集成的
`forgejo` Module，执行面依赖[Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md)的 M0—M2。

**当前状态：M1 的控制面代码已落地，其真实部署验证未做。** `modules/ai_agent` 已存在：manifest、
Compose、Hook、双语文档、编排器（身份引导与轮换、系统 webhook 登记、入站验签与 inbox、周期对账、
outbox 幂等、脱敏）与单元测试全部就位，仓库全部 CI 门禁通过。

编排器**当前放在本仓库** `modules/ai_agent/orchestrator/`：它编译依赖仓库内的 `internal/computeclient`，
和 `forgejo` 的 actions-controller 处境相同，因此按同一个先例以 named build context 取共享代码。
**已决定后续独立为单独的 git 仓库**，届时 ANAS 只引用它发布的 docker image，本 Module 只留 manifest、
Hook 与文档。拆分时机未定（不阻塞任何里程碑），但从现在起按“可搬走”约束写：编排器不依赖 ANAS 内部
约定，只消费环境变量、配置文件与 Secret 文件；`internal/computeclient` 这类共享代码在拆分时要么随之
独立，要么改为按 Contract 调用。

**上游事实已在 `finance.hlong.wang` 上对固定 `forgejo 15.0.7` 复核完毕**（24 项通过、0 项失败），
结论回写在[要求文档 §15](../requirements/ai-agent.md)。复核推翻了实现里的五处假设，全部已修正并加了
回归测试：发 token 的端点、认证方式、`repositories` 的形态、可与仓库限定并存的 scope 集合、
token/SSH key 名称的唯一性，以及 webhook 存在性不能用列表判断。`AGENT-R-006` **走原设计，不启用
退化**；退化实现保留。

控制面已对**真实 Forgejo 15.0.7 与真实 PostgreSQL 17** 跑通端到端：身份引导、轮换、webhook 登记、
入站去重、自触发过滤、重连后状态续接、对账不重复。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module 骨架、身份凭据与事件入站 | AGENT-R-001—R-014 | 已完成；单元、契约与真实依赖 e2e 均通过，容器级重启 e2e 待整套部署 |
| M2：交互契约与产物入库 | AGENT-R-015—R-029 | 已完成；单元、契约与真实 Forgejo e2e 均通过 |
| M3：权限、目录组授权与审计 | AGENT-R-030—R-036 | 已完成；判定引擎、审计与目录侧 `OU=Cap` 均已落地，`--group-team-map` 投影待接 |
| M4：执行面、分支策略与执行 issue | AGENT-R-037—R-044、R-065、R-071 | 未开始；**阻塞于** [Incus compute Provider](../../../../dev-docs/plans/incus-module.md) 的真实宿主验收，按决定等它完成后再开工 |
| M5：排程、执行时机与队列 | AGENT-R-045—R-051 | 已完成；排序、时机与队列面均已落地并对真实 Forgejo 验证 |
| M6：记录、会话视图与可扩展性 | AGENT-R-052—R-061、R-070 | 未开始 |
| M7：真实部署验收 | AGENT-R-062—R-064 | 未开始 |
| M8：补充的交互与安全约束 | AGENT-R-066—R-069 | 已完成；四条均已实现并有单元用例 |
| M9：Agent 发起的写操作与三档授权 | AGENT-R-072—R-078 | 未开始；提议档（`R-072`—`R-074`、`R-076`、`R-078`）不依赖执行面，可与 M6 并行；自主档与 MCP 写路径（`R-075`、`R-077`）排在 M6 之后 |

覆盖统计：78 项需求全部有且只有一个里程碑归属。2026-08-30 补入 `R-065`—`R-071`：它们在设计文档中
一直存在，但此前没有进入矩阵，因此不会被任何阶段验收。2026-09-08 补入 `R-072`—`R-078`，对应设计
文档新增的 §3.5「Agent 自主建 issue：三档授权」、§5.8「Agent 侧的写路径」与 §6.5「Agent 发起的
写操作」。

## 2. M1 检查表

- [x] 用 `test-env/scripts/forgejo-agent-api-probe.sh` 对固定 `forgejo 15.0.7` 复核设计文档 §11 的
      上游事实，结论回写在[要求文档 §15](../requirements/ai-agent.md)。脚本本身也修了两处错误
      （token 端点、依赖 payload 形态）。投递语义与表单渲染仍需接收端与 Web 表单，留待 M2。
- [x] ~~建立 `anas-agent` 上游项目骨架~~：改为仓库内 `modules/ai_agent/orchestrator/`，理由见上。
- [x] 新增 `modules/ai_agent`：manifest、Hook、Compose、`relational_database` 消费、双语文档。
- [x] 实现 Agent 账号、token 与 SSH key 的无人值守引导与轮换，含按仓库限定不可用时的退化路径
      （`ScopingPerRepoAccount`）。
- [x] 注册系统 webhook 并实现 ingress 验签、inbox、202 快返、周期对账与自触发过滤。
- [x] 单元测试覆盖凭据不外泄、重复投递幂等、对账不重复副作用。
- [x] 对真实 Forgejo 与 PostgreSQL 的 e2e：实现为 opt-in 的 Go 测试而不是 shell 脚本
      （`TestM1AgainstLiveDependencies`、`TestForgejoAdminAgainstLiveInstance`、`TestPostgresStore`）。
      它们驱动的是控制面自己的代码路径，shell 只能从外部戳 API，戳不到 outbox 幂等与重连续接。
      同一轮真实依赖运行还顺带覆盖了矩阵只要求「单元」的 R-009、R-011—R-014（webhook 登记与密钥轮换、
      10 次重复投递只落一行、对账重复扫描不增长、Agent 自身事件被丢弃）。这些不进 §10 的 e2e 记录表，
      因为那张表只登记矩阵要求 e2e 的条目。
- [ ] 容器级重启 e2e（`server-ai-agent-restart-e2e.sh`）：需要整套 ANAS 部署起来，等 M4 有执行面后
      和隔离、取消等一起做。

## 3. M2 检查表

- [x] 由 Agent 注册表生成 issue 表单模板与 `config.yaml`，注册表变化触发重新生成并走 PR。
      五个模板在真实 `forgejo 15.0.7` 上被识别为 issue form（字段类型、id 与 front matter 标签均正确）。
- [x] 实现表单答案解析与容错、越权选项降级说明、创建后指派。
- [x] 实现状态评论（唯一、原地更新）、对话评论、reaction 确认与回合串行队列。
- [x] 实现标签与命令的等价映射，运行参数不进标签。
- [x] 实现文档由控制面经 contents API 提交、路径白名单、commit 永久链接与执行依据冻结。
- [x] 实现引导 issue：探测既有约定、问卷、提交 `.anas-agent.yml` 并开 PR。
- [x] **表单答案的渲染格式已实测**：渲染在服务端（`issue_template.RenderToMarkdown`），因此
      `TestFormRenderingAgainstLiveForgejo` 用脚本化的表单提交跑通了真函数，无需人工。结论见
      [要求文档 §15](../requirements/ai-agent.md)；顺带发现 front matter 的标签只是页面预勾选，
      Agent issue 的识别因此改为读正文而不是读标签。R-015/R-016 的矩阵验证方式是「单元」，
      因此这轮真实环境验证不进 §10 的 e2e 记录表。

## 4. M3 检查表

- [x] 实现仓库权限推导（none/read/write/admin/owner → 动作集）、逐条覆盖与整体关闭同步。
      五个权限档位与推导表在真实 `15.0.7` 上逐一验证过。
- [x] 实现 `CAP_ai_agent_*` 目录组经 Forgejo team 的投影与定期快照刷新。读法是以该用户身份
      `GET /user/teams`（`Sudo` 头）——一次调用拿到全部 team，而不是逐个 team 查成员。
- [x] 目录侧登记：`OU=Cap` 与 `CAP_<module-id>_<capability>` 已写进
      [Samba AD 用户与权限规划](../../../../docs/architecture/samba-ad-user-planning.md) §5.4.1 并由 `samba_dc`
      实现；Module 经 `ANAS_IDENTITY_CAPABILITY_GROUPS` 声明能力码，与应用追加 `APPS_LIST` 是同一模式。
- [ ] 把 `--group-team-map` 接进 `forgejo` Module 的 OIDC 配置，补上"目录组 → Forgejo team"这一段。
- [x] 实现即时否决表与 `Veto` / `LiftVeto`（`agent-grant deny` 的后端）。否决在每次判定时实时读取，
      不走快照缓存，因此撤权下一次请求即生效。
- [x] 实现判定审计（含拒绝）与作业开始前的二次判定（`Recheck` 主动丢弃快照）。
      审计写入失败即判定失败：记不下来的批准和没发生过的批准无法区分。
- [x] 验证 issue 正文、模板与评论中的配置只能收窄（覆盖条目取交集，不做并集）。
- [ ] `agent-grant` 的 Module 命令外壳（把 `Veto`/`LiftVeto` 接到 `module-command` capability 上）
      留到 M6 与其余命令一起做。

## 5. M4 检查表

> **当前不开工**：本里程碑整体等 [Incus compute Provider](../../../../dev-docs/plans/incus-module.md)
> 完成真实宿主验收。在那之前不提前实现控制面侧的半截执行逻辑，避免对着未验证的 Provider 行为写
> 适配代码。

- [ ] 实现按仓库切分的工作实例与每作业一次性实例两档，跨仓库不共用。
- [ ] 实现每 issue 独立 worktree、会话目录与工作分支。
- [ ] 实现作业期凭据注入与结束吊销、出网 allowlist。
- [ ] 实现默认建分支开 PR、保护分支拒绝直推、直接提交的双条件校验。
- [ ] 实现执行 issue 的创建、依赖与交叉引用、结束回写与关闭拆分开关。
- [ ] 实现取消（中断 Agent + 销毁实例 + 状态收敛）与外部写操作幂等键。
- [ ] 实现“涉及范围/禁改路径”的写入约束与被拒路径回写（`R-065`）。
- [ ] 实现开 PR 前运行仓库声明的测试与 lint、结果写进 PR 与状态评论、未声明如实记录（`R-071`）。

## 6. M5 检查表

- [x] 实现执行前预估、`due_date` 校验与风险标记。预估由 Agent 给出（`Estimate.Valid` 要求有产出者
      和时长），没有预估即视为有风险而不是默认能赶上；墙钟上限取 `min(预估×2, 部署上限)`，预估抬不高
      部署上限。
- [x] 实现队列排序（依赖 → 截止 → 优先级/入队时间）与串行/并行配置。依赖是拓扑序，压过截止时间；
      有依赖环时不丢作业，把环显出来。并发上限受主机门槛压制。
- [x] 实现 `now`/`at`/`on`/`hold` 四种时机与事件触发、等待超时回落。可等待的事件集合是封闭的——
      只包含控制面已订阅的 webhook，否则那是一个永远等不到的等待；每个等待都有超时。
- [x] 实现实际耗时写回 Forgejo 原生工时记录，归属到执行的 Agent 账号。
- [x] 实现置顶队列 issue 的维护与置顶不可用时的降级：置顶失败不是错误，队列照常维护，正文如实说明
      没有置顶。

## 7. M6 检查表

- [ ] 实现事件、轮次、作业、判定、产物与每日汇总记录，含 token 与花费。
- [ ] 实现上下文组装的 provenance 过滤与会话重建降级。
- [ ] 实现会话持久卷、容器重建后续接与备份保留期。
- [ ] 实现进度节流、脱敏、分层保留与导出。
- [ ] 实现终端附着的授权（管理员默认可用、非管理员需能力组）、只读默认与输入归属记录。
- [ ] 实现预算与墙钟硬上限截断。
- [ ] 验证注册表驱动：新增一个运行时不改控制面分支逻辑，思考强度使用原生取值。
- [ ] 实现模型凭据的三形态、不回显导入、有效期查询与轮换、临近过期告警、认证失败熔断与席位调度
      （`R-070`）。

## 8. M7 检查表

- [ ] 在固定 `forgejo 15.0.7` 与真实 Incus 宿主完成端到端链路并记录证据。
- [ ] 记录控制面常驻资源、单作业资源、典型作业墙钟与花费。
- [ ] 同步 Module 双语文档、配置参考与 `dev-docs` 状态。

## 8.1 M8 检查表

这四条来自设计文档但此前漏进矩阵。2026-09-10 对着 M2/M3 的实现逐条核对，结果如下：

- [x] **`R-066` 风险声明**：`resolveCompletion` 早已把声明了风险的 issue 从"直接提交"降级为 PR 并
      写明理由；核对时发现缺的是另一半——"强制人工批准"没有守卫，因为现在还没有自动批准通道，
      所以是"碰巧成立"。已补 `AutoApprovalAllowed`：声明风险即禁止任何策略性自动批准，
      并带上原因。趁着没有自动批准路径先立闸，比等引入时再想起来便宜。
- [x] **`R-067` `/split` 与 `/summarize`**：核对时只有命令壳——命令表、授权动作与
      `ForgejoIssues.AddDependency` 都在，但非测试代码从未调用。已补 `split.go`：`Split` 建子 issue
      （正文用 `RenderIssueForm` 写回表单形状，因此继承是字面的，由同一个解析器读，不产生第二份
      配置表示）、把父 issue 标成被它阻塞、两侧各留一条交叉引用；`Summarize` 把讨论提交为文档并回
      一条指向 commit 的评论。两者都走 outbox，重复命令不会开第二个 issue。依赖被实例拒绝时不吞掉
      整次拆分——子 issue 与交叉引用仍然成立，只把失败写进结果。
- [x] **`R-068` 非模板 issue 不触发**：`LooksLikeAgentIssue` 此前只被测试调用，没有接线。已补
      `ShouldEngage`（`engagement.go`）：模板 issue 直接参与，普通 issue 只有指派 Agent 账号或
      `@` 到账号才唤起，其余一律不介入且给出原因。识别读正文不读标签——front matter 的标签只是
      新建页预勾选，取消勾选或走 API 提交都不会带上。近似匹配（prose 里提到运行时名、
      `@account-staging`、邮箱里的 `@`）都不算召唤，有对应用例。
- [x] **`R-069` 四级解析**：已实现。`ParseConventions` / `InferConventions` / `BuiltinConventions`
      三个产出者由 `onboarding.go` 按 `.anas-agent.yml` → 引导 issue → 既有约定推断 → 内置预设的
      顺序短路返回，`RepoConventions.Source` 记录命中层级；内置预设保守（仅文档目录、强制 PR、
      禁止直接提交）。本轮只做核对，未改代码。

## 8.2 M9 检查表

提议档先做，它不需要往工作实例开任何入口；自主档与 MCP 写路径等执行面就位后再开工。

- [ ] 建 issue 的三档模型与 `repo_settings.agent_issue_tier`、`agent_write_ops`、两级配额字段（`R-072`）。
- [ ] 提议评论的渲染、`/accept` 与 `/reject`、reaction 确认、过期作废（`R-073`）。
- [ ] `issue_proposal` 与 `issue_provenance` 两张表，以及归属人失效即作废在途提议（`R-074`）。
- [ ] 自主档的每作业/每日配额与标题指纹去重，超限降级回提议档（`R-075`）。
- [ ] `ai:proposed` 标签、交叉引用、`job_id` 溯源块，且不打 `ai:auto`（`R-076`）。
- [ ] `anas-agent-mcp` 白名单写路径：幂等键、`decision` 记录、预算计入、拒绝未列出的操作（`R-077`）。
- [ ] 上下文组装按 `issue_provenance` 过滤 Agent 自建 issue 的正文（`R-078`）。

## 9. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | M1 落地时全绿（未提交） |
| `go run ./cmd/gen-module-docs --check` | M1 落地时全绿（未提交） |
| `npm run docs:check-requirements` | M1 落地时全绿（未提交） |
| `npm run docs:check-requirement-status` | M1 落地时全绿（未提交） |
| 渲染产物 `docker compose config --quiet` | M1 落地时全绿（未提交） |

真实依赖测试的跑法（默认 skip，不进 CI）：

```sh
AI_AGENT_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' \
AI_AGENT_TEST_FORGEJO_URL=http://127.0.0.1:13000 \
AI_AGENT_TEST_FORGEJO_USER=<管理员> AI_AGENT_TEST_FORGEJO_PASSWORD=<口令> \
AI_AGENT_TEST_FORGEJO_ORG=<组织> AI_AGENT_TEST_FORGEJO_REPO_IN=<仓库A> AI_AGENT_TEST_FORGEJO_REPO_OUT=<仓库B> \
  go test ./modules/ai_agent/orchestrator -run 'Live|TestPostgresStore'
```

## 10. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | `TestM1AgainstLiveDependencies` | PostgreSQL 17 + 重连续接 | 2026-09-06 | 通过（容器级重启待 M4） |
| R-005 | `TestM1AgainstLiveDependencies` | Forgejo 15.0.7 管理端引导 | 2026-09-06 | 通过 |
| R-006 | `TestForgejoAdminAgainstLiveInstance` | token scope 与仓库限定（含越界读写） | 2026-09-06 | 通过 |
| R-007 | `TestM1AgainstLiveDependencies` | token/SSH key 轮换，轮换后仅一份存活 | 2026-09-06 | 通过 |
| R-027 | `TestM2AgainstLiveForgejo` | 空仓库首次启用：探测约定、开问卷、提交 `.anas-agent.yml` 并开 PR | 2026-09-06 | 通过 |
| R-032 | `TestPolicyAgainstLiveForgejo` | Forgejo team → 能力上限投影（Samba → IAM 段待目录侧登记后补） | 2026-09-06 | 部分通过 |
| R-037 | 待新增 `test-env/scripts/server-ai-agent-isolation-e2e.sh` | 两个仓库并行作业 | — | 待实现 |
| R-039 | `server-ai-agent-isolation-e2e.sh credentials` | 作业期凭据注入与吊销 | — | 待实现 |
| R-040 | 待新增 `test-env/scripts/server-ai-agent-branch-pr-e2e.sh` | 保护分支 + 直接提交策略 | — | 待实现 |
| R-041 | `server-ai-agent-branch-pr-e2e.sh exec-issue` | 执行 issue 依赖与解除阻塞 | — | 待实现 |
| R-042 | `server-ai-agent-isolation-e2e.sh egress` | 出网 allowlist | — | 待实现 |
| R-043 | 待新增 `test-env/scripts/server-ai-agent-cancel-e2e.sh` | 运行中作业取消 | — | 待实现 |
| R-044 | `server-ai-agent-cancel-e2e.sh idempotency` | 重复触发与重试 | — | 待实现 |
| R-048 | `TestTimingsParseAndWriteBackTheirLabel` 等单元用例 | 立即/定时/事件触发与等待超时 | 2026-09-06 | 通过（事件到达的真实投递待 M6 接收端） |
| R-050 | `TestSchedulingAgainstLiveForgejo` | 真实 Forgejo 的工时回写 | 2026-09-06 | 通过 |
| R-054 | 待新增 `test-env/scripts/server-ai-agent-session-e2e.sh` | 容器重建后续接与降级 | — | 待实现 |
| R-057 | 待新增 `test-env/scripts/server-ai-agent-terminal-e2e.sh` | 管理员与非管理员附着 | — | 待实现 |
| R-058 | `server-ai-agent-schedule-e2e.sh budget` | 预算与墙钟截断 | — | 待实现 |
| R-065 | `server-ai-agent-branch-pr-e2e.sh scope` | 越界路径与禁改路径写入被拒 | — | 待实现 |
| R-071 | `server-ai-agent-branch-pr-e2e.sh tests` | 仓库声明的测试/lint 执行与结果回写 | — | 待实现 |
| R-062 | 待新增 `test-env/scripts/server-ai-agent-full-e2e.sh` | 讨论 → 文档 → 批准 → 执行 issue → PR | — | 待实现 |
| R-063 | `server-ai-agent-full-e2e.sh roundtable` | 多 Agent 圆桌收敛 | — | 待实现 |
| R-064 | `server-ai-agent-full-e2e.sh baseline` | 资源与成本基线 | — | 待实现 |
| R-073 | 待新增 `test-env/scripts/server-ai-agent-proposal-e2e.sh` | 提议渲染 → 人确认 → 建 issue；未确认不创建 | — | 待实现 |
| R-075 | `server-ai-agent-proposal-e2e.sh quota` | 配额超限与标题指纹去重后降级回提议档 | — | 待实现 |

## 11. 当前阻塞

- `AGENT-R-048` 的事件触发只验证了解析、匹配与超时三段；**"真实投递到达后作业入队"**这一段要等
  接收端把 `pull_request` / `action_run_*` 分发到等待中的作业，属于 M6 的记录与分发面。
- webhook 的**投递语义**（超时、重试、可否手动重投）仍未复核：需要一个真实的接收端，属于 M2。
  当前实现按“至多一次”设计，对账是补偿手段，这条不阻塞 M1。
- 执行面依赖 [Incus compute Provider](../../../../dev-docs/plans/incus-module.md) 的 M6（真实宿主验收），该里程碑本身
  阻塞于独立 KVM/Incus 宿主。M4 在此之前只能实现控制面侧逻辑。
- `AGENT-R-032` 的链路还差中间一段（2026-09-13 更新：Forgejo 已决定改为 LDAP 同步 + OIDC 登录的
  双源形态，落地后能力组可经 LDAP source 的组→team 映射到达 Forgejo，并随目录事件订阅秒级刷新；
  `--group-team-map` 是双源落地前的过渡路径，见 `FORGEJO-R-063`—`R-065`）。目录侧已经通了：`CAP_<module-id>_<capability>` 已在
  [Samba AD 用户与权限规划](../../../../docs/architecture/samba-ad-user-planning.md) §5.4.1 登记并由
  `samba_dc` 实现，`ai_agent` 经 `ANAS_IDENTITY_CAPABILITY_GROUPS` 声明能力码；消费侧也已完成
  （从 Forgejo team 读出能力上限）。缺的是 **IAM 用 `--group-team-map` 把组投影成 Forgejo team** ——
  该 CLI 参数已在 `15.0.7` 上确认存在，但 `forgejo` Module 的 OIDC 配置还没有把它接上。
- `modules/ai_agent` 的镜像尚未推到 registry；`.github/images.json` 已登记构建条目。

## 12. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [编排设计](../../docs/architecture/orchestration-design.md) | 从 `docs/architecture/` 迁入组件目录，站内留一页指针；§14 删除“评审后才产出需求与计划”的过期表述 | 已完成 |
| [与 Forgejo 互操作的规则](../../docs/forgejo-interop.md) | 编排器侧规则迁入组件目录；上游固定版本事实留在 [`docs/developer/forgejo-interop.md`](../../../../docs/developer/forgejo-interop.md) 基线，两份不重复 | 已完成 |
| [看板接入调研](../../docs/research/kanban-integration.md) | 调研正文迁入组件目录，站内研究索引留指针 | 已完成 |
| [设计评审](../reviews/2026-09-05-orchestration-design-review.md) | 随组件迁入，`dev-docs/reviews/index.md` 改指向新位置 | 已完成 |
| `modules/ai_agent/README.md`、`README.en.md` | 增加“组件文档一览”，指向本目录下全部文档 | 已完成 |
| [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §2.2 | 改写为 LDAP 同步 + OIDC 登录的双源形态，支撑 §6.2 的秒级撤权；验收见 `FORGEJO-R-063`—`R-065` | 已完成（实现未开始） |
| [IAM Provider 要求](../../../../dev-docs/requirements/iam-provider.md) §1.6 | 登记“禁止自助修改 `mail`/`sAMAccountName`”，它是 `ACCOUNT_LINKING=auto` 的前提 | 已完成 |
| `test-env/scripts/forgejo-agent-api-probe.sh` | 注释与报告中的设计文档路径改为组件目录 | 已完成 |
