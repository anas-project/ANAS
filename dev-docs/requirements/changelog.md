---
doc_type: requirement
status: current
created: 2026-08-28
updated: 2026-08-28
---

# Changelog 要求

本文是 ANAS Core 与 Module 变更记录的目标、范围、硬约束与验收标准，回答“什么算做对了”，不随实现顺序变化。

**[§7 需求矩阵](#7-需求矩阵)是规范来源，其余章节是解释。** 逐条要求带稳定 ID（`CHLOG-R-<序号>`），测试、检查表与提交都引用 ID 而不是章节号。

该特性**不单独建架构文档**：机制、被否决的替代方案与写作规则写在[Changelog 规范](../../docs/developer/changelog-standard.md)里；落地顺序与剩余工作见[Changelog 实施计划](../plans/changelog.md)。

「必须／不得」是约束，「应当」是有正当理由可偏离的默认。除计划文档标注为已实施的部分外，本文描述的能力**当前不可执行**，不是操作指南。

## 1. 范围与总体决策

### 1.1 目标

让 ANAS Core 与每个 Module 都拥有面向用户的、可追溯到具体发布身份的变更记录，并且这份记录本身就是文档，而不是需要另一套工具才能读懂的中间格式。

事实源是按发布版本命名的 Markdown 文件。未发布内容写在固定的 `master.md`，发版时由发布流水线改名为发布版本，再重建一个空的 `master.md`。**文件名即发布身份，改名即完成版本归属**，因此不需要任何基于 tag 区间的归属算法。

### 1.2 非目标

以下是评估后明确不做的，理由见 §6：

- **不**引入 `.changes/` 之类的 change fragment 中间格式。
- **不**要求每个提交都写变更记录；强制时刻是分支合并回 `master`。
- **不**依赖强制 PR：`master` 未启用分支保护，约一半变更走本地合并。
- **不**为一轮批量 Module 发布的每个 Module 创建 GitHub Release。
- **不**在第一阶段修改 `anas.module-catalog/v1`。
- **不**在 CI 内调用 LLM 直接生成并发布未经人审的正文。

### 1.3 两条发布线的位置差异

Core 与 Module 的变更记录**放在不同的位置，被两条完全不同的取数路径读取**，这是本特性最容易出错的地方：

```text
Core     docs/changelog/<version>.md
         发布模式下文档构建执行 git archive <coreTag> -- docs 抽取整个 docs/ 目录，
         因此 Core 变更记录随 Core tag 一起被取出，天然与该 tag 一致

Module   modules/<name>/changelog/<version>-r<revision>.md
         materialize-module-docs 执行 git show <module-tag>:modules/<name>/...
         逐个 Module tag 读取，与 Core tag 无关
```

两者都正确，但不能互换：把 Module 变更记录放进 `docs/` 会让它被 Core tag 的时间点冻结，把 Core 变更记录放进 `modules/` 则无处安放。

## 2. 事实源与文件布局

每个组件一个变更记录目录，中英文各一份文件：

```text
docs/changelog/
  master.md      master.en.md      未发布
  v0.1.2.md      v0.1.2.en.md      已发布，改名而来，此后只读

modules/<name>/changelog/
  master.md      master.en.md
  34.0.2-r7.md   34.0.2-r7.en.md
```

`master.md` 与已发布文件结构一致，只是标题固定为 `Unreleased`。分节沿用 `Added`、`Changed`、`Fixed`、`Deprecated`、`Removed`、`Security`、`Breaking changes and migration`，空节不写。

Module 的目标文件名必须使用完整发布身份 `version-rrevision`。只写上游版本号会让同一上游版本的多次 ANAS 修订互相覆盖。

## 3. 写入时机与冲突

强制时刻是**分支合并回 `master`**：合并的人或 agent 通读该分支引入的用户可见变更，总结成条目写进 `master.md` 与 `master.en.md`，随合并提交进入 `master`。

选择这个时刻而不是"每个提交"，是因为它同时满足两件事：

- **信息仍然最新。** 合并者刚做完这个分支，知道为什么改、对谁 breaking、要不要迁移。这些正是 Git 历史表达不了、发版时从 diff 反推最容易丢的部分。
- **不产生冲突。** 分支自身从不修改 `master.md`，只有合并提交修改它。第二个分支合入时该文件已被第一个分支的合并改过，但因为第二个分支对它零改动，Git 不报冲突。若改为每个提交都写，按仓库当前每周 9 ~ 21 个分支合入 `master` 的速度，冲突会是每周十几次的无谓劳动。

## 4. 发版：改名必须在 CI 内完成并回到 master

改名**必须由发布流水线执行**，因为目标文件名在推送发布分支之前还不存在：Module 的 `revision` 由 `prepare` 阶段的 `module-revisions.sh --write` 计算，Core 的版本号由 `anas-release-version.sh` 按 bump 规则计算。本地改名等于预测版本号，猜错就要回滚一次已发布的不可变 tag。

改名是**破坏性操作**，因此回推 `master` 从"顺带"变成"承重"：一旦 `master.md` 改名而这次改名没有到达 `master`，`master` 上的 `master.md` 仍留着已发布的条目，下一次发版会重复发布同一批内容。

现有回推链路确实会失败。`container-images.yml` 的同步是 fast-forward-only，`master` 不是发布提交的祖先时报错退出；`image-release` 分支上已经存在 `Merge branch 'master' into image-release`，是这条恢复路径被实际用过的证据。对 revision 重算而言失败可以容忍，对改名不行。

## 5. 前置改造

`modules/<name>/changelog/` 位于 Module 目录内，会落进两条既有判定，必须先排除，否则模型自锁：

| 位置 | 现状 | 不改的后果 |
| --- | --- | --- |
| `scripts/ci/module-revisions.sh` 的 `runtime_path()` | 只按 basename 排除 `README*.md`、`localization.yml` 等，不排除目录 | 写变更记录本身触发 `revision + 1` → 新 tag → 又要写变更记录，无限循环 |
| `internal/modulepackage` 的 `excludedRuntimeDirectory()` | 只排除 `modules/*/docs/` | 变更记录进入 OCI 制品并计入 digest，每次写条目都改变制品身份 |

## 6. 决策记录

- **不用 change fragment。** fragment 方案要用 tag 区间反推归属，在 Core 与 Module 两条节奏不同的发布线上还要处理 baseline、集合差和发布内容边界。改名方案把这些全部消掉，代价只是 §5 的两处排除。
- **不强制每个提交写。** 见 §3：冲突成本高于收益，且逐提交在本仓库同样无法机械强制。
- **不依赖强制 PR。** `master` 未启用分支保护，PR 合并 17 次、本地直接合并 18 次，且 dependabot 是 `go.mod` 变更的主要来源却永远不会写条目。按提交范围取数才覆盖得全。
- **不在 CI 内调 LLM 直接发布。** 发版是不可逆动作——打不可变 tag、推 OCI 制品、建 Release。未经人审的生成文本直接进入这三样，风险与收益不成比例。
- **不设跨组件聚合落点。** 聚合落点会让读者在 Module 页面上看到不属于该 Module 的记录。

## 7. 需求矩阵

本矩阵是规范来源，正文是解释。两者冲突以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `CHLOG-R-001` | 变更记录的事实源必须是按发布版本命名的 Markdown 文件，不得引入需要额外工具才能阅读的中间格式 | 检查 |
| `CHLOG-R-002` | Core 变更记录必须位于 `docs/changelog/`，Module 变更记录必须位于 `modules/<name>/changelog/` | 单元 |
| `CHLOG-R-003` | 每个组件的未发布内容必须写在该目录下的 `master.md`，标题为 `Unreleased` | 单元 |
| `CHLOG-R-004` | 每个变更记录文件必须有中英文两份（`<name>.md` 与 `<name>.en.md`），且必须在同一次提交里一起修改 | 单元 |
| `CHLOG-R-005` | Module 已发布文件名必须使用完整发布身份 `version-rrevision`，不得只用上游版本号 | 单元 |
| `CHLOG-R-006` | 条目必须使用面向用户的完整句子，描述行为变化而不是实现过程 | 检查 |
| `CHLOG-R-007` | 标记为 breaking 的条目必须在 `Breaking changes and migration` 节写出升级前后要执行的操作 | 检查 |
| `CHLOG-R-008` | 分支合并回 `master` 时，若该分支引入了用户可见变更，合并提交必须同时写入对应的 `master.md` 与 `master.en.md` | 契约 |
| `CHLOG-R-009` | 不得要求每个提交都修改变更记录 | 检查 |
| `CHLOG-R-010` | 只命中 `shared_contexts`、打包器或 catalog 条目的变更，必须自动归类为重新打包，不得要求人工撰写条目或人工豁免 | 单元 |
| `CHLOG-R-011` | 同步性 merge（第二父已是 `master` 祖先）不得被要求写入条目 | 单元 |
| `CHLOG-R-012` | 覆盖检查必须按 merge 提交判定，对本地合并与 PR 合并一视同仁 | 单元 |
| `CHLOG-R-013` | 覆盖检查的受影响集合必须复用 `scripts/ci/module-revisions.sh` 的变更上下文，不得另写一套近似的路径规则 | 单元 |
| `CHLOG-R-014` | `master.md` 到版本文件的改名必须由发布流水线执行，不得要求提交者预测版本号或 revision | 契约 |
| `CHLOG-R-015` | 改名后必须在同一次发布运行内重建仅含 `# Unreleased` 标题的空 `master.md` 与 `master.en.md` | 单元 + e2e |
| `CHLOG-R-016` | 改名提交必须包含在该发布 tag 指向的提交中，使 `git show <tag>:<changelog-path>` 始终可取到该版本正文 | e2e |
| `CHLOG-R-017` | Core 发布流水线必须把改名结果推回 `master`，并使用与 `container-images.yml` 同一套祖先校验 | e2e |
| `CHLOG-R-018` | 发布运行开始时必须检测 `master` 上的 `master.md` 是否含有已出现在已发布版本文件中的条目；若有，必须先把上次丢失的改名补到 `master` 再继续 | 单元 + e2e |
| `CHLOG-R-019` | 待改名的 `master.md` 与任何已发布版本文件存在重复条目时，发布必须中止 | 单元 |
| `CHLOG-R-020` | 本批每个将获得新 tag 的组件，其 `master.md` 必须非空或已被归类为重新打包，否则发布必须中止且不得打 tag | 单元 + e2e |
| `CHLOG-R-021` | `scripts/ci/module-revisions.sh` 的 `runtime_path()` 必须排除 `modules/*/changelog/`，使写变更记录本身不触发 revision 提升 | 单元 |
| `CHLOG-R-022` | `internal/modulepackage` 必须把 `modules/*/changelog/` 排除出 Module 制品与其 digest | 单元 |
| `CHLOG-R-023` | 已改名的版本文件此后只读；仅允许安全披露回填与注明的勘误，不得改变已发布行为的描述 | 检查 |
| `CHLOG-R-024` | Core 变更记录页面必须由发布模式下 `git archive <coreTag> -- docs` 抽取的 `docs/` 提供，不得读取工作树中尚未发布的内容 | 单元 |
| `CHLOG-R-025` | Module 变更记录页面必须通过 `git show <module-tag>:modules/<name>/changelog/...` 按各自 Module tag 读取，与 Core tag 无关 | 单元 |
| `CHLOG-R-026` | Module 同一上游版本的不同 revision 必须各自保留变更记录段落，不得因文档去重而合并 | 单元 |
| `CHLOG-R-027` | 每个在 `.github/modules.json` 中注册的 Module 都必须生成变更记录页面，无条目时写入明确占位，避免版本导航产生死链 | 单元 |
| `CHLOG-R-028` | 生成页面必须转义条目正文中的 Vue 插值，且文档 CI 不得仅依赖退出码判定构建成功 | 单元 |
| `CHLOG-R-029` | Core GitHub Release 正文必须来自改名后的版本文件，不得继续使用 `--generate-notes` | e2e |
| `CHLOG-R-030` | CNB 的 Core Release 正文必须来自同一份版本文件，不得保留硬编码的单行说明 | e2e |
| `CHLOG-R-031` | GitHub Release 正文使用英文文件，CNB Release 正文使用中文文件 | 检查 |
| `CHLOG-R-032` | 发布流水线不得调用 LLM 生成并直接发布未经人审的变更记录正文 | 检查 |
| `CHLOG-R-033` | 变更记录的写入时机、落点与合并时的责任必须写入 `AGENTS.md`、`CONTRIBUTING.md` 与 `docs/developer/release.md` | 检查 |

## 8. 相关文档

- [Changelog 规范](../../docs/developer/changelog-standard.md)：写作规则、机制细节与被否决方案
- [Changelog 实施计划](../plans/changelog.md)：里程碑、需求归属与剩余工作
- [ANAS、Module 与容器发布](../../docs/developer/release.md)：发布流程中的变更记录步骤
