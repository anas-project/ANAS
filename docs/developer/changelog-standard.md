---
doc_type: standard
status: current
created: 2026-08-18
updated: 2026-08-28
---

# Changelog 规范

本规范定义 ANAS Core 与 Module 变更记录的位置、写法、写入时机和发布处理。

**目标**：让 Core 与每个 Module 都有面向用户、可追溯到具体发布身份的变更记录，而且这份记录本身
就是文档，不是需要另一套工具才能读懂的中间格式。

**评估后明确不做的**（理解本规范为什么长这样，这几条比正文更关键）：

- 不引入 `.changes/` 之类的 change fragment 中间格式；
- 不要求每个提交都写变更记录——强制时刻是分支合并回 `master`；
- 不依赖强制 PR：`master` 未启用分支保护，约一半变更走本地合并；
- 不为一轮批量 Module 发布的每个 Module 创建 GitHub Release；
- 第一阶段不修改 `anas.module-catalog/v1`；
- 不在 CI 内调用 LLM 直接生成并发布未经人审的正文。

本规范描述的机制**尚未实现**，当前不可执行。完整需求矩阵（`CHLOG-R-<序号>`，规范来源）见
[Changelog 要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/changelog.md)，落地顺序与剩余工作见
[Changelog 实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/changelog.md)；两份都在仓库 `dev-docs/` 下，不在本站发布。

## 1. 事实源

变更记录是文档的一部分，不是另一套中间格式。事实源是按发布版本命名的 Markdown 文件：未发布内容写在固定的 `master.md`，发版时由发布流水线改名为发布版本，再重建一个空的 `master.md`。

**文件名即发布身份，改名即完成版本归属。** 因此不需要基于 tag 区间的归属算法，Core 与 Module 两条节奏不同的发布线在这里不产生任何复杂度——各自改各自的文件。

## 2. 位置：两条发布线不同

```text
docs/changelog/
  master.md      master.en.md      未发布
  v0.1.2.md      v0.1.2.en.md      已发布，只读

modules/<name>/changelog/
  master.md      master.en.md
  34.0.2-r7.md   34.0.2-r7.en.md
```

两个目录被**两条完全不同的取数路径**读取，不能互换：

| | Core | Module |
| --- | --- | --- |
| 位置 | `docs/changelog/` | `modules/<name>/changelog/` |
| 发布分支 | `anas-release` | `image-release` |
| 文档站取数 | 发布模式执行 `git archive <coreTag> -- docs`，整个 `docs/` 随 Core tag 取出 | `materialize-module-docs` 执行 `git show <module-tag>:modules/<name>/changelog/...`，逐 Module tag 读取 |
| 与 Core tag 的关系 | 绑定 | 无关 |

这个差异有一个有用的副作用：Core 侧不需要额外的“发布模式”逻辑。发布模式下 `docs/` 来自 Core tag，而该 tag 的 `master.md` 正是改名后重建的空文件，因此正式站点自然看不到未发布内容；开发与 PR 模式下 `docs/` 来自工作树，`master.md` 里累积的条目正常显示。

Module 文件名必须使用完整发布身份 `version-rrevision`。只写上游版本号会让同一上游版本的多次 ANAS 修订互相覆盖。

## 3. 条目写法

`master.md` 与已发布文件结构一致，只是标题固定为 `Unreleased`：

```markdown
# Unreleased

### Fixed

- 修复域密码策略拒绝时错误信息不明确的问题。（#123）

### Changed

- 模块更新失败时保留结构化错误码。

### Breaking changes and migration

- `identity.password_policy` 改名为 `identity.password.policy`。升级前手工改过该键的
  部署需要同步改名，否则启动时报未知配置项。
```

| 项 | 规则 |
| --- | --- |
| 分节 | `Added`、`Changed`、`Fixed`、`Deprecated`、`Removed`、`Security`、`Breaking changes and migration`；空节不写 |
| 顺序 | 分节内按追加顺序排列，不排序、不合并同类项；新条目追加到所属分节末尾 |
| 措辞 | 面向用户的完整句子，描述行为变化，不写实现过程 |
| 双语 | `master.md` 与 `master.en.md` 必须在同一次提交里一起改 |
| breaking | 必须同时在 `Breaking changes and migration` 节写出升级前后要做什么 |
| 追溯 | 句尾可带 `（#123）` 引用 issue 或 PR |
| 安全 | `Security` 节条目可附 CVE 或 advisory 链接，未公开披露前留空，公开后回填 |

上游应用版本不写进条目正文。Module 发布页头部的“上游应用版本”由生成器从该 tag 的 `modules/<name>/module.yml` 读取。

## 4. 写什么、不写什么

只记录用户、管理员、部署者或 Module 开发者能感知的变化：新能力、行为变化、bug 修复；配置、CLI、API、contract 或数据格式变化；升级兼容性、迁移步骤、弃用与移除；安全修复；Module 的上游应用升级及 ANAS 对该 Module 的集成变化。

默认不记录：纯重构、测试补充、格式化、CI 调整、无行为变化的文档修正，以及只因共享上下文而重新打包的 Module（见 §8）。若这些变化本身影响发布、安装或兼容性，则应记录。

## 5. 写入时机：合并回 master 时总结分支

不要求每个提交都写。分支开发期间随时可以写，但**唯一强制的时刻是分支合并回 `master`**：合并的人或 agent 通读该分支引入的用户可见变更，总结成条目写进 `master.md` 与 `master.en.md`，随合并提交进入 `master`。

选这个时刻有两个理由：

- **信息最新。** 合并者刚做完这个分支，知道为什么改、对谁 breaking、要不要迁移。这些是 Git 历史表达不了、发版时从 diff 反推最容易丢的部分。
- **不产生冲突。** 分支自身从不修改 `master.md`，只有合并提交修改它。第二个分支合入时该文件已被第一个分支的合并改过，但因为第二个分支对它零改动，Git 不报冲突，追加即可。若改为每个提交都写，按仓库每周 9 ~ 21 个分支合入 `master` 的速度，冲突会是每周十几次的无谓劳动。

### 5.1 落点

| 变更影响 | 落点 |
| --- | --- |
| ANAS Core 自身行为 | `docs/changelog/master.md` + `.en.md` |
| 单个 Module | `modules/<name>/changelog/master.md` + `.en.md` |
| 多个 Module | 逐个 Module 各写一条，措辞可以相同 |
| 同时影响 Core 与某 Module | 两边各写一条，各自描述该侧的影响 |

不设跨组件的聚合落点，否则读者会在 Module 页面上看到不属于该 Module 的记录。真正影响多个 Module 的共享 contract 变更，按 `scripts/ci/module-revisions.sh --print` 的输出展开为受影响的 Module 集合。

### 5.2 发版前的整理

改名之前通读本轮累积的条目：合并重复描述、统一措辞、删掉中途反复修改又回退的条目。**这次整理是规范的一部分，不是可选步骤**——各分支独立总结出来的条目不保证彼此协调，尤其是同一个功能分几条分支做完时。

## 6. 发版：改名在 CI 内完成

```text
Core     docs/changelog/master.md      -> docs/changelog/v0.1.2.md
         docs/changelog/master.en.md   -> docs/changelog/v0.1.2.en.md
         新建只含 # Unreleased 的空文件

Module   modules/<n>/changelog/master.md -> modules/<n>/changelog/34.0.2-r7.md
         只对本批实际获得新 tag 的 Module 执行
         新建只含 # Unreleased 的空文件
```

改名**必须由发布流水线执行**，因为目标文件名在推送发布分支之前还不存在：

```text
Module   revision 由 prepare 的 module-revisions.sh --write 计算
         推之前不知道这次是 r7 还是 r8
Core     版本号由 anas-release-version.sh 按 bump 规则计算
         推之前不知道这次是 v0.1.2 还是 v0.2.0
```

本地改名等于预测版本号，猜错就要回滚一次已发布的不可变 tag。所以流程是：本地只写 `master.md`，CI 算出发布身份后改名、提交，再推回 `master`。改名提交必须包含在 tag 指向的提交里，这样 `git show <tag>:<changelog-path>` 永远能取到该版本正文。

已改名的版本文件此后只读。两个例外：安全披露公开后回填 CVE 或 advisory 链接；明确的勘误（措辞错误、失效链接）可以修但要在文件内注明，不得改变已发布行为的描述。

## 7. master 回推是承重结构

改名是破坏性操作：一旦 `master.md` 改名而这次改名没有到达 `master`，`master` 上的 `master.md` 仍留着已发布的条目，下一次发版会重复发布同一批内容。

而现有回推链路**确实会失败**。`container-images.yml` 的 `finalize` 同步是 fast-forward-only：

```text
release_head != RELEASE_SHA        跳过，交给排队中的下一次发布同步
master 不是 RELEASE_SHA 的祖先      报错退出
   "master advanced independently; merge master into image-release before
    synchronizing it back"
```

按每周 9 ~ 21 个分支合入 `master` 的速度，构建 22 个 Module 多架构镜像的窗口期内 `master` 前进是常态。`image-release` 分支上已经存在 `Merge branch 'master' into image-release`，是这条恢复路径被实际用过的证据。对 revision 重算而言同步失败可以容忍——下次会重新计算；对改名则不能。

因此：

- **Core 侧也要推回 `master`。** `anas-release.yml` 现在完全不推，需照抄 `container-images.yml` `sync_master` 的两级祖先校验。
- **发布运行开始时做幂等自愈。** 检查 `master` 上的 `master.md` 是否含有已出现在某个已发布版本文件里的条目；若有，说明上次同步丢失，先把改名补到 `master` 再继续本次发布。这比调换 `finalize` 的 tag/sync 顺序安全——先推 `master` 再打 tag 会产生“版本文件存在但没有对应 tag”的反向不一致。
- **重复发布检测兜底。** 待改名的 `master.md` 与任何已发布版本文件存在重复条目时中止发布。

## 8. 门禁

`master` 未启用分支保护，约一半变更走本地合并而不是 PR，git hook 也不随仓库分发。因此“合并时要写变更记录”无法在合并那一刻机械阻断，只能靠 `AGENTS.md` 与 `CONTRIBUTING.md` 约束写入者，靠 CI 在能拦的地方兜底。

| 级别 | 触发 | 检查 | 失败后果 |
| --- | --- | --- | --- |
| 覆盖检查 | PR 与发布工作流 | 区间内每个引入发布相关改动的 merge，是否也改了对应的 `master.md` | PR 上提示；发布上中止 |
| 完整性检查 | 发布工作流 | 本批每个将获得新 tag 的组件条目非空或已归类为重新打包；改名已发生；新 `master.md` 为空 | 中止发布，不打 tag |

覆盖检查按 **merge 提交**判定，因此对本地合并与 PR 合并一视同仁：取该 merge 的 `git diff <first-parent>...<second-parent>` 得到分支引入的路径，再判定是否需要条目。

四条豁免，判定依据与 revision 计算共用同一份上下文：

- 分支只命中 `shared_contexts`、打包器或 catalog 条目——自动归类为重新打包；
- 分支只改测试、格式、CI、生成产物或无行为变化的文档——沿用 `runtime_path()` 的忽略规则；
- 同步性 merge（第二父已是 `master` 祖先）不引入新工作，跳过；
- dependabot 分支按第一条自动落入重新打包，不需要额外配置。

受影响集合直接取 `scripts/ci/module-revisions.sh --print`，不另写一套近似的路径规则，保证“需要提升 revision”与“需要写变更记录”永远是同一个集合。PR 侧不需要新建 workflow：`docs.yml` 已经在每个 PR 上运行，把覆盖检查挂进它现有的 “Validate documentation sources” 步骤即可。

## 9. 前置排除

`modules/<name>/changelog/` 位于 Module 目录内，会落进两条既有判定，必须先排除，否则模型自锁：

| 位置 | 不改的后果 |
| --- | --- |
| `scripts/ci/module-revisions.sh` 的 `runtime_path()` | 写变更记录本身触发 `revision + 1` → 新 tag → 又要写变更记录，无限循环 |
| `internal/modulepackage` 的 `excludedRuntimeDirectory()` | 变更记录进入 OCI 制品并计入 digest，每次写条目都改变制品身份 |

两处都改为同时排除 `docs` 与 `changelog` 两个目录名。

## 10. 文档站呈现

生成页面写入临时 VitePress 源树，不修改仓库内的真实页面：

```text
/reference/changelog                        Core，中文
/en/reference/changelog                     Core，英文
/reference/modules/<name>/changelog         Module，中文
/en/reference/modules/<name>/changelog      Module，英文
```

三条约束：

- **每个注册 Module 都必须生成页面**，无条目时写明确占位。`materialize-module-docs` 先运行、变更记录物化命令后运行，前者输出的“变更记录”链接依赖后者是否生成页面；缺页面即死链，而当前版站点的 `ignoreDeadLinks` 为 `false`，构建会直接失败。
- **链接指向 Module 根**（`/reference/modules/<name>/changelog`），不能拼在 `renderVersionNavigation` 的 `pageBase` 后面——快照页的 `pageBase` 带 release 段，拼出来是死链。
- **同一上游版本的不同 revision 各自保留段落。** 现有 Module 文档会按正文 fingerprint 合并相同页面，变更记录不能复用这个去重结果。

生成器必须转义条目正文中的 Vue 插值。VitePress 把 Markdown 编译成 Vue 模板，围栏代码块默认带 `v-pre`，但行内代码和正文里的双大括号会被当成表达式求值。两种失败方式不同：

```text
${{ github.event.pull_request.base.sha }}
  JavaScript 语法合法 -> SSR 渲染期抛 TypeError
  但 vitepress build 仍输出 build complete，npm run docs:build 退出码为 0

{{ '{{' }}
  语法非法 -> vite:vue 编译期报 Error parsing JavaScript expression，退出码非零
```

第一种是危险的：CI 会静默放过一个渲染失败的页面。因此文档 CI 必须检查构建日志，不能只看退出码。

## 11. 相关文档

- [Changelog 要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/changelog.md)：需求矩阵（规范来源），仓库 `dev-docs/`
- [Changelog 实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/changelog.md)：里程碑与剩余工作，仓库 `dev-docs/`
- [ANAS、Module 与容器发布](/developer/release)：发布流程中的变更记录步骤
- [文档写作标准](/developer/documentation-standard)：双语与目录分类规则
