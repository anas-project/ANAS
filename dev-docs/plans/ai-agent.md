---
doc_type: plan
status: implementing
created: 2026-08-27
updated: 2026-09-06
---

# AI Agent 编排实施计划

验收依据是[AI Agent 编排集成要求](../requirements/ai-agent.md)的需求矩阵，设计依据是
[AI Agent 编排设计](../../docs/architecture/ai-agent-orchestration-design.md)。协作面是已集成的
`forgejo` Module，执行面依赖[Incus compute Provider 实施计划](incus-module.md)的 M0—M2。

**当前状态：M1 的控制面代码已落地，其真实部署验证未做。** `modules/ai_agent` 已存在：manifest、
Compose、Hook、双语文档、编排器（身份引导与轮换、系统 webhook 登记、入站验签与 inbox、周期对账、
outbox 幂等、脱敏）与单元测试全部就位，仓库全部 CI 门禁通过。

编排器**没有**按原计划拆成独立上游项目 `anas-agent`：它编译依赖仓库内的 `internal/computeclient`，
和 `forgejo` 的 actions-controller 处境相同，因此按同一个先例放在 `modules/ai_agent/orchestrator/`，
以 named build context 取共享代码。等它需要独立发布节奏时再拆，届时只是搬目录加发布流程。

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
| M3：权限、目录组授权与审计 | AGENT-R-030—R-036 | 未开始 |
| M4：执行面、分支策略与执行 issue | AGENT-R-037—R-044 | 未开始 |
| M5：排程、执行时机与队列 | AGENT-R-045—R-051 | 未开始 |
| M6：记录、会话视图与可扩展性 | AGENT-R-052—R-061 | 未开始 |
| M7：真实部署验收 | AGENT-R-062—R-064 | 未开始 |

覆盖统计：64 项需求全部有且只有一个里程碑归属。

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

- [ ] 实现仓库权限推导、逐条覆盖与整体关闭同步。
- [ ] 实现 `CAP_ai_agent_*` 目录组经 IAM 与 Forgejo team 的投影与定期快照刷新。
- [ ] 实现即时否决表与 `agent-grant deny` 命令。
- [ ] 实现判定审计（含拒绝）与作业开始前的二次判定。
- [ ] 验证 issue 正文、模板与评论中的配置只能收窄。

## 5. M4 检查表

- [ ] 实现按仓库切分的工作实例与每作业一次性实例两档，跨仓库不共用。
- [ ] 实现每 issue 独立 worktree、会话目录与工作分支。
- [ ] 实现作业期凭据注入与结束吊销、出网 allowlist。
- [ ] 实现默认建分支开 PR、保护分支拒绝直推、直接提交的双条件校验。
- [ ] 实现执行 issue 的创建、依赖与交叉引用、结束回写与关闭拆分开关。
- [ ] 实现取消（中断 Agent + 销毁实例 + 状态收敛）与外部写操作幂等键。

## 6. M5 检查表

- [ ] 实现执行前预估、`due_date` 校验与风险标记。
- [ ] 实现队列排序（依赖 → 截止 → 优先级/入队时间）与串行/并行配置。
- [ ] 实现 `now`/`at`/`on`/`hold` 四种时机与事件触发、等待超时回落。
- [ ] 实现实际耗时写回 Forgejo 工时记录。
- [ ] 实现置顶队列 issue 的维护与置顶不可用时的降级。

## 7. M6 检查表

- [ ] 实现事件、轮次、作业、判定、产物与每日汇总记录，含 token 与花费。
- [ ] 实现上下文组装的 provenance 过滤与会话重建降级。
- [ ] 实现会话持久卷、容器重建后续接与备份保留期。
- [ ] 实现进度节流、脱敏、分层保留与导出。
- [ ] 实现终端附着的授权（管理员默认可用、非管理员需能力组）、只读默认与输入归属记录。
- [ ] 实现预算与墙钟硬上限截断。
- [ ] 验证注册表驱动：新增一个运行时不改控制面分支逻辑，思考强度使用原生取值。

## 8. M7 检查表

- [ ] 在固定 `forgejo 15.0.7` 与真实 Incus 宿主完成端到端链路并记录证据。
- [ ] 记录控制面常驻资源、单作业资源、典型作业墙钟与花费。
- [ ] 同步 Module 双语文档、配置参考与 `dev-docs` 状态。

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
| R-032 | 待新增 `test-env/scripts/server-ai-agent-grant-e2e.sh` | Samba 组 → IAM → team 投影 | — | 待实现 |
| R-037 | 待新增 `test-env/scripts/server-ai-agent-isolation-e2e.sh` | 两个仓库并行作业 | — | 待实现 |
| R-039 | `server-ai-agent-isolation-e2e.sh credentials` | 作业期凭据注入与吊销 | — | 待实现 |
| R-040 | 待新增 `test-env/scripts/server-ai-agent-branch-pr-e2e.sh` | 保护分支 + 直接提交策略 | — | 待实现 |
| R-041 | `server-ai-agent-branch-pr-e2e.sh exec-issue` | 执行 issue 依赖与解除阻塞 | — | 待实现 |
| R-042 | `server-ai-agent-isolation-e2e.sh egress` | 出网 allowlist | — | 待实现 |
| R-043 | 待新增 `test-env/scripts/server-ai-agent-cancel-e2e.sh` | 运行中作业取消 | — | 待实现 |
| R-044 | `server-ai-agent-cancel-e2e.sh idempotency` | 重复触发与重试 | — | 待实现 |
| R-048 | 待新增 `test-env/scripts/server-ai-agent-schedule-e2e.sh` | 立即/定时/事件触发 | — | 待实现 |
| R-050 | `server-ai-agent-schedule-e2e.sh tracked-time` | 工时回写 | — | 待实现 |
| R-054 | 待新增 `test-env/scripts/server-ai-agent-session-e2e.sh` | 容器重建后续接与降级 | — | 待实现 |
| R-057 | 待新增 `test-env/scripts/server-ai-agent-terminal-e2e.sh` | 管理员与非管理员附着 | — | 待实现 |
| R-058 | `server-ai-agent-schedule-e2e.sh budget` | 预算与墙钟截断 | — | 待实现 |
| R-062 | 待新增 `test-env/scripts/server-ai-agent-full-e2e.sh` | 讨论 → 文档 → 批准 → 执行 issue → PR | — | 待实现 |
| R-063 | `server-ai-agent-full-e2e.sh roundtable` | 多 Agent 圆桌收敛 | — | 待实现 |
| R-064 | `server-ai-agent-full-e2e.sh baseline` | 资源与成本基线 | — | 待实现 |

## 11. 当前阻塞

- webhook 的**投递语义**（超时、重试、可否手动重投）仍未复核：需要一个真实的接收端，属于 M2。
  当前实现按“至多一次”设计，对账是补偿手段，这条不阻塞 M1。
- 执行面依赖 [Incus compute Provider](incus-module.md) 的 M6（真实宿主验收），该里程碑本身
  阻塞于独立 KVM/Incus 宿主。M4 在此之前只能实现控制面侧逻辑。
- `CAP_<module-id>_<capability>` 组类别与 `OU=Cap,OU=Groups` 尚未在
  [Samba AD 用户与权限规划](../../docs/architecture/samba-ad-user-planning.md)登记，M3 依赖该决策。
  注册表已按 `CAP_ai_agent_<id>` 生成组名，只等目录侧登记。
- `modules/ai_agent` 的镜像尚未推到 registry；`.github/images.json` 已登记构建条目。
