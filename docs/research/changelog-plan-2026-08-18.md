# ANAS Core 与 Module Changelog 方案

> 日期：2026-08-18
>
> 状态：**提案**。本文描述尚未实现的机制，不能作为操作指南；文中所有
> `go run ./cmd/changelog ...` 与 `go run ./cmd/materialize-changelog-docs ...`
> 命令当前不可执行。
>
> 核对基线：`master` 位于 `72391e4`，Core tag 仅 `v0.1.0`，Module tag 46 个，
> 已注册 Module 18 个（`.github/modules.json`）。

## 1. 结论

不建议只增加一个手工维护的根 `CHANGELOG.md`，也不建议直接把 Git commit 或 GitHub
自动生成的 Release Notes 当成 changelog。

推荐采用以下模型：

1. 每个 PR 提交一份小型、结构化的 change fragment，作为唯一事实源；
2. ANAS Core 和每个 Module 都拥有独立的 changelog 视图；
3. 发布工作流按各自 tag 区间读取 fragment，生成 Markdown Release Notes；
4. 文档站和仓库内的 `CHANGELOG.md` 都从同一批 fragment 与不可变 tag 派生；
5. fragment 在发版前由 agent 按上次发版以来的区间补全，强制检查放在发布工作流。

这套方案适合当前仓库，因为 Core 使用 `vX.Y.Z`，Module 使用
`module/<name>/<version>-r<revision>`，二者的发布分支、节奏和版本含义均不同。

## 2. 当前状态与约束

### 2.1 已有发布身份

- ANAS Core：`vMAJOR.MINOR.PATCH`，从 `anas-release` 分支发布；
- Module：`module/<name>/<version>-r<revision>`，从 `image-release` 分支批量发布；
- Module 的 `version` 表示规范化应用版本，`revision` 表示同一应用版本下的 ANAS 打包修订；
- 一次 Module 发布可能同时为多个 Module 产生独立 tag；
- Core 当前通过 `gh release create --generate-notes` 生成 GitHub Release 文本；
- Module 当前只有 tag、OCI artifact 和 catalog，没有面向用户的 release note。

### 2.2 为什么不能只用 Git 历史

仓库同时包含 Core、共享 contract、打包器、派生镜像和多个 Module。一个提交可能影响一个
Module、全部 Module 或只影响开发流程。merge commit 和 Conventional Commit 的 scope
不能稳定表达以下信息：

- 变化是否面向最终用户；
- 是否需要迁移或人工操作；
- 是否为 breaking/security 变化；
- 共享代码变化具体影响哪些 Module；
- 中英文用户文案；
- 同一提交在 Core 和 Module 两条版本线中分别进入了哪个 release。

因此 Git 历史适合审计和追溯，不适合作为公开 changelog 的唯一输入。

### 2.3 已核对的流水线事实

以下事实来自当前仓库，方案的每个门禁和生成步骤都必须与它们对齐：

- `master` 上的 PR 目前**没有任何 CI 门禁**。仓库只有五个 workflow，其中只有 `docs.yml`
  在 `pull_request`（所有分支）上运行；`container-images.yml` 的 `pull_request` 触发器限定
  `branches: [image-release]`，`anas-release.yml` 只在 `anas-release` 分支 push 时运行。
  `go test ./...` 和 `module-revisions.sh` 都不会在功能 PR 上执行。因此 `changelog-check`
  会是仓库里第一个作用于 master PR 的门禁。
- Module 发布**会**把发布分支快进回 `master`：`container-images.yml` 的 `prepare` 作业在
  `image-release` 上创建 `chore(images): calculate release revisions` 提交，`finalize` 作业
  在全部产物成功后执行 `git push origin "$RELEASE_SHA:refs/heads/master"`。
- Core 发布**不**产生提交，只创建 tag。`anas-release` 当前是 `master` 的祖先，发布提交本来
  就在 `master` 里。两条发布线在“生成文件如何回到 master”上是不对称的。
- Module 是否需要发布由 `scripts/ci/module-revisions.sh` 的变更上下文判定，而
  `.github/modules.json` 的 18 个 Module 中有 17 个把 `go.mod`、`go.sum`、`contracts` 列为
  `shared_contexts`（`freeradius` 只有 `contracts`）。一次 Go 依赖升级会让 17 个 Module 同时
  `revision + 1` 并产生 17 个新 tag。
- CNB 的 Core Release 由 `.cnb.yml` 的 `type: git:release` 阶段创建，正文是硬编码的单行
  `description: Byte-identical mirror of the ANAS GitHub Release assets.`，输入是推送到 CNB 的
  `refs/heads/<tag>` 分支树，读不到 GitHub Release 正文。

## 3. 信息模型

### 3.1 唯一事实源：change fragment

新增目录：

```text
.changes/
  2026-08-18-nextcloud-password-policy.yml
  2026-08-18-runner-resource-no-tty.yml
  schema.json
```

文件名只要求唯一、稳定、便于定位；合并后不删除 fragment。永久保留可以避免 Core 和
Module 发布节奏不同导致的“消费状态”冲突，也不需要在 release 分支反向同步删除操作。

建议 schema：

```yaml
id: nextcloud-password-policy
date: 2026-08-18
issues: [123]
changes:
  - component: module/nextcloud
    type: fixed
    summary: 修复域密码策略拒绝时错误信息不明确的问题。
    summary_en: Clarify errors returned when the domain password policy rejects a password.
    breaking: false
    migration: ""
  - component: core
    type: changed
    summary: 模块更新失败时保留结构化错误码。
    summary_en: Preserve structured error codes when a Module update fails.
    breaking: false
    migration: ""
```

字段约束：

| 字段 | 规则 |
| --- | --- |
| `id` | 仓库内唯一，发布后不可复用 |
| `component` | `core`、`module/<name>` 或 `all-modules` |
| `type` | `added`、`changed`、`fixed`、`deprecated`、`removed`、`security` |
| `summary` / `summary_en` | 面向用户的中英文完整句子，不写实现过程 |
| `breaking` | 布尔值；为 `true` 时必须填写 `migration` |
| `migration` | 升级前后需要执行的操作；无操作时为空 |
| `issues` | 可选的 issue/PR 编号，用于追溯 |

`all-modules` 只用于确实影响全部已发布 Module 的共享打包格式、hook ABI 或运行时 contract
变化。普通共享代码改动应明确展开为受影响的 `module/<name>`，避免把无关 Module 的
changelog 污染掉。

补充规则：

- **唯一键只用文件名。** `id` 和文件名两处同时要求唯一容易漂移。规定 `id` 等于去掉日期
  前缀的文件名主干，`validate` 强制两者一致，生成器只以 `id` 作为跨 tag 的身份。这条
  规则是 5.3 节归属算法的前提。
- **`component` 的校验来源是 `.github/modules.json`，不是 `modules/` 目录。**
  `module-revisions.sh` 已经强制两者一一对应，changelog 复用同一份注册表，避免出现第三份
  Module 名单。
- **上游应用版本不进 fragment。** 发布段落里的“上游应用版本”从该 Module tag 的
  `modules/<name>/module.yml` 读取。`cmd/materialize-module-docs` 的 `loadReleases` 已经在做
  `git show <tag>:modules/<name>/module.yml` 并校验 tag 与清单身份一致，共享 package 直接
  复用该逻辑，不要让作者在 fragment 里重复抄版本号。
- **`security` 条目增加可选 `advisory` 字段**（CVE 编号或 GitHub Security Advisory 链接）。
  公开披露前留空，公开后回填。
- **`.changes/` 按年分目录**（`.changes/2026/...`），避免单目录随发布批次线性膨胀。归属算法
  必须对文件移动免疫，见 5.3 节。

### 3.2 分类规则

公开 changelog 只描述用户、管理员、部署者或 Module 开发者能感知的变化：

- 新能力、行为变化、bug 修复；
- 配置、CLI、API、contract 或数据格式变化；
- 升级兼容性、迁移步骤、弃用与移除；
- 安全修复及必要的升级建议；
- Module 的上游应用升级，以及 ANAS 对该 Module 的集成变化。

默认不记录纯重构、测试补充、格式化、CI 调整和无行为变化的文档修正。若这些变化本身会
影响发布、安装或兼容性，则应记录。

### 3.3 维护责任：谁写、谁审、谁兜底

方案原文默认“作者会写 fragment”，但从没说作者是谁。这在本仓库不是细节，因为仓库的协作
形态和常见开源项目完全不同：

```text
提交作者    291 个提交全部来自同一维护者（两个 Git 身份）+ 5 个 github-actions[bot] 提交
实际执行    分支命名显示变更由 AI agent 完成：codex/* 16 个、claude/* 4 个、
            worktree-agent-* 4 个
外部贡献者  无
```

由此确定三层责任，缺一层方案就会退化成“事后由人补写的 CHANGELOG.md”，也就是 1 节明确
否定的做法：

| 层 | 责任人 | 时机 | 职责 |
| --- | --- | --- | --- |
| 写（默认） | 发版的 agent | 推送发布分支之前 | 补全上次发版到本次发版之间的全部条目，见 3.4 节 |
| 写（可选前置） | 变更作者或 agent | 实现变更时 | 提前写下 diff 读不出来的信息：breaking、migration、安全影响 |
| 审 | 维护者 | 发版前 | 审分类、breaking 标记、是否面向用户；不代写 |
| 兜底 | 生成器与不可变 tag | 发版时 | 版本归属、排序、页面、Release 正文全部推导，人不参与 |

三条推论直接影响实现：

- **规则必须写进 agent 读得到的地方，而不是只写进 CI。** 本仓库的变更主要由 agent 产出，
  但 `AGENTS.md` 目前只有 4 行且只讲 research 报告，`CONTRIBUTING.md` 只有 14 行。
  `docs/developer/documentation-standard.md` 已经有现成的先例句式：“AI Agent、生成脚本和
  其他自动化也必须遵守同一规则……不能先生成单语页面并把翻译留给后续任务。” changelog 的
  发版补全步骤应当照抄这个模式写进 `AGENTS.md` 与 `docs/developer/release.md`。
- **单人仓库的 PR 门禁本质是自己拦自己。** 维护者既是唯一作者也是唯一 reviewer，任何
  “声明豁免”的通道也由同一个人使用。门禁在多人仓库用来防松懈，挂在 PR 上时在这里只能起
  提醒作用。这正是 6.1 节把强制检查移到发布工作流的原因：那里拦的是不可逆动作。同时门禁的
  设计目标不是“让违规最难”，而是**让合规路径最省事**——这与 5.5 节的自动分档是同一件事：
  17 个 Module 因共享上下文重新打包时自动归类，人只需要为真正面向用户的变更写一句话。
- **fragment 的成本必须低到能顺手写完。** 一条 fragment 是两句话加四个字段。如果写的人需要
  查上游版本号、算受影响 Module 集合或判断进入哪个 release，就一定会漏写。这些都由生成器
  从 tag 和 `module.yml` 推导，见 3.1 节和 5 节。

### 3.4 写入时机：发版时由 agent 补全

fragment 在**推送发布分支之前**由 agent 一次性补全，范围是上一次发版到本次发版之间的变更。
不要求每个 PR 都随手写 fragment。

这与 2.2 节“不能只用 Git 历史”并不矛盾。2.2 节反对的是**自动从 commit message 推导**
changelog；这里是 agent 读区间内的 diff 和 commit message 后**撰写**结构化条目，产物仍然是
人审过、可冻结、可复算的 fragment。生成器侧的确定性不受影响：fragment 在打 tag 之前就已经
提交，5.3 节的集合差算法照常成立。

本仓库的实测数据支持这个时机：

```text
Module 批次区间     4 ~ 19 个提交（6 个批次），中位数约 9
每批次 Module tag   通常 1 ~ 4 个；共享上下文变更时一次 18 个
commit message      规范 Conventional Commits，最近 100 条中
                    fix 23 / feat 19 / docs 18 / test 7 / chore 5 / refactor 1 / ci 1
Core 区间           v0.1.0 至今 59 个提交（尚无第二次 Core 发布）
```

一个批次十几个提交、scope 明确（`fix(iam)`、`feat(samba_dc)`、`fix(runtime)`），agent 在发版
时读 diff 重建用户可见影响是可靠的。

#### 提交位置：一律在 master 上写完再推发布分支

这是本模型唯一容易出错的地方，两条发布线的同步方向不同（见 2.3 节）：

```text
Module   master --push--> image-release --CI 打 tag--> finalize 快进回 master
         写在 image-release 上也能回到 master，但没有必要绕这一圈

Core     master --push--> anas-release --CI 打 tag--> 不回流
         写在 anas-release 上的 fragment 永远进不了 master，
         下次发版会被当成缺失重新补写
```

因此统一规则：**fragment 一律在 `master` 上写完并提交，再把 `master` 推到发布分支。**
这条规则对两条线都成立，不需要记两套流程。

#### agent 的补全步骤

```bash
# 1. 确定区间起点：最近一个且是 HEAD 祖先的发布 tag
#    Module 用 image-release/*，Core 用 v*
# 2. 列出区间内的实质提交，跳过 merge 提交
git log --no-merges <base>..HEAD

# 3. Module 发版时，用 CI 同一份脚本取得受影响集合与原因，不要自己判断路径
bash scripts/ci/module-revisions.sh --base <base> --print

# 4. 读已存在的 .changes/，只补缺失的条目
# 5. 按 component 写 fragment，提交到 master
```

三条约束：

- **只写本次发布组件的条目。** Module 发版只写 `module/<name>` 与 `all-modules`，Core 发版
  只写 `core`。同一个提交同时影响两侧时，两次发版各写各的，互不覆盖。
- **必须先读 `.changes/` 去重。** Module 和 Core 的发布区间会重叠，agent 要跳过已有 `id`，
  不能重复描述同一变更。
- **`revision` 号不写进 fragment。** 发版时 `prepare` 才计算 revision，agent 推送前拿到的是
  预测值。fragment 只写 `component: module/<name>`，由生成器绑定到最终 tag。

#### 这个模型放弃了什么

diff 里读不出来的信息会丢：某修复对应的真实用户场景、某配置项改名对手工改过配置的用户是
breaking、某变更的安全影响范围。agent 能从 diff 稳定识别出契约变更和配置键改名，但识别不出
“为什么”。因此 3.3 节保留了“可选前置”一层：作者在实现时如果知道这类信息，就当场写下
fragment，发版 agent 读到已有 `id` 后跳过。强制的是发版补全，前置写入是省事的优化。

## 4. 用户看到的产物

建议生成以下产物，但不重复手工维护：

```text
CHANGELOG.md                         # ANAS Core 历史
modules/<name>/CHANGELOG.md          # 单个 Module 历史
dist/release-notes/anas-vX.Y.Z.md    # Core 单次发布说明
dist/release-notes/<name>-X-rN.md    # Module 单次发布说明
dist/release-notes/module-batch.md   # 本次批量发布摘要
```

每个发布段落统一使用以下结构：

```markdown
## 34.0.2-r7 - 2026-08-18

上游应用版本：34.0.2

### Added
### Changed
### Fixed
### Deprecated
### Removed
### Security
### Breaking changes and migration
```

空分类不输出。Module 标题必须使用完整 release 身份 `version-rrevision`，不能只写
`app_version`，否则同一上游版本的多次 ANAS 修订无法区分。

仓库内 `CHANGELOG.md` 是生成后的便捷视图，fragment 与不可变 tag 才是事实源。生成器必须
保证相同 commit/tag 输入得到完全相同的输出。

## 5. 版本归属算法

### 5.1 Core

对目标 Core tag `vX.Y.Z`：

1. 找到此前最新的稳定 Core tag；
2. 按 5.3 节的集合差算法取出该区间新增的 fragment；
3. 只保留 `component: core` 的条目；
4. 按 breaking/security/type 和稳定排序规则渲染；
5. 用生成文件替代当前 GitHub `--generate-notes` 的正文；
6. 在末尾追加完整 commit/PR 对比链接，保留工程审计入口。

第一次启用时使用配置中的 baseline tag，不自动把整个仓库历史解释成用户变更。

### 5.2 单个 Module

对 `module/<name>/<version>-r<revision>`：

1. 找到该 Module 的上一个 `module/<name>/*` tag；
2. 按 5.3 节的集合差算法取出该区间新增的 fragment；
3. 选择 `module/<name>` 和适用的 `all-modules` 条目；
4. 输出该 Module 的单次 notes，并合入该 Module 的完整历史视图；
5. 批量发布摘要列出本轮所有发布 Module、旧版本、新版本及各自 notes 链接。

Module revision 脚本判定某 Module 需要发布，但没有匹配 fragment 时，发布 PR/正式发布应
失败；只有明确标记为内部重新打包、制品恢复等无用户可见变化时，才允许生成标准说明：
“重新发布制品，运行行为无变化”。该豁免必须有原因，不能静默跳过。这条规则在本仓库需要
按 5.5 节分档，否则会在最常见的一类变更上产生批量噪声。

### 5.3 归属算法：集合差，不是 `--diff-filter=A`

“区间内首次加入的 fragment”有两种实现，二者在本仓库并不等价：

- `git log --diff-filter=A <from>..<to> -- .changes`：对重命名、按年归档、cherry-pick 和
  squash 合并敏感，一次目录整理就会让历史区间重新“新增”一批旧条目；
- 集合差：分别读取两端的 fragment 全集，按 `id` 求差。

采用集合差：

```text
new(from, to) = ids(to) \ ids(from)
ids(ref)      = git ls-tree -r --name-only <ref> -- .changes 中每个 YAML 的 id
```

它对文件移动、重命名和目录重组免疫，与提交顺序无关，也天然给出 `Unreleased`
（工作树全集减去参考 tag 全集）。代价是要解析两端的全部 fragment，在当前规模（每批次十几条、
年增数百条）完全可接受。

同时必须定义正文来源，方案原文没有回答这一点：

- **文档站**从生成时所在的 checkout 读取 fragment 正文，因此后续修正措辞或补翻译会回溯更新
  历史段落；
- **GitHub 与 CNB Release 正文**在发布时刻冻结，之后不再改写。

这条差异要写进 changelog 页面的说明，避免读者把两处措辞不同当成 bug。

### 5.4 baseline 必须是 commit，不是 `v0.1.0`

仓库当前只有一个 Core tag `v0.1.0`，它是 `master` 的祖先，`master` 领先 59 个提交。如果按
8 节把 baseline 记为“最新 `v*`”，下一次 Core 发布的区间就是 `v0.1.0..release-commit`，覆盖
这 59 个没有 fragment 的提交，直接触发 6.2 节的“无 Core fragment 则失败”。

配置中必须保存启用 changelog 的那个提交：

```yaml
baseline:
  core_commit: <启用 changelog 的 master commit>
  modules:
    <name>: <该 Module 启用时的最新 module tag>
```

生成器对早于 baseline 的区间只输出一行说明和 compare 链接，不做分类。

### 5.5 共享上下文导致的重新打包必须自动判定

按 2.3 节，一次 `go.mod` 升级会让 17 个 Module 同时 bump。如果每个都要求 fragment 或人工
豁免，门禁会在最常见的一类变更上产生 17 条噪声，维护者只会习惯性地批量豁免，门禁随即失效。

规则改为按 revision 提升的**来源**分档：

| revision 提升来源 | changelog 要求 |
| --- | --- |
| `modules/<name>/` 自身运行文件变化 | 必须有 `module/<name>` 条目，否则失败 |
| 只有 `shared_contexts`、打包器或 catalog 条目变化 | 自动归类为重新打包，生成固定文案，不要求 fragment |
| 有 fragment 明确声明该 Module | 以 fragment 为准，覆盖自动分类 |
| `version` 变化（上游升级） | 必须有 `module/<name>` 条目，否则失败 |

这要求 `scripts/ci/module-revisions.sh` 输出更细的原因。它现在只打印 `unchanged`、
`new-module`、`version-changed`、`context-changed` 四种 reason，需要把 `context-changed` 拆成
`module-context` 与 `shared-context`：`module_context_changed` 内部已经分别检查这两条路径，
只是把结果合并成了一个布尔值。这是唯一需要改动既有脚本的地方，也保证 changelog 与 revision
使用同一份判定，而不是另写一套近似规则。

### 5.6 `all-modules` 展开为本批实际发布的集合

`all-modules` 不能展开成 `.github/modules.json` 的全集。`shared_contexts` 并不统一
（`freeradius` 没有 `go.mod` / `go.sum`），一次共享变更未必让所有 Module 都 bump，而没有新
tag 的 Module 不应该出现新的 changelog 段落。展开集合取本批 `package_matrix` 中实际获得新
tag 的 Module。

## 6. 工具与 CI

建议新增一个小型 Go 工具 `cmd/changelog`，复用仓库现有的 Go 工具链，提供：

```text
go run ./cmd/changelog validate
go run ./cmd/changelog check --base <sha> --head <sha>
go run ./cmd/changelog render-core --from <tag> --to <sha> --version <version>
go run ./cmd/changelog render-module --module <name> --from <tag> --to <sha>
go run ./cmd/changelog render-all
go run ./cmd/changelog render-all --check
```

### 6.1 门禁位置：发布工作流，不是 PR

按 3.4 节，fragment 在发版时补全，因此**强制检查放在发布工作流，而不是 master PR**。这同时
解决了 3.3 节指出的“单人仓库自己拦自己”问题：PR 门禁拦不住唯一的作者兼 reviewer，而发布
门禁拦的是一个真正不可逆的动作——打不可变 tag、推 OCI 制品、建 GitHub Release。

两级职责：

| 位置 | 触发 | 职责 | 失败后果 |
| --- | --- | --- | --- |
| master PR（可选，轻量） | `on: pull_request` | 只校验 `.changes/` 的 schema、`id` 唯一性、Module 名合法、双语字段齐全 | 提示，不阻塞功能开发 |
| 发布工作流（强制） | 推送 `image-release` / `anas-release` | 本次发布的每个组件都有条目或合法的自动分类 | 中止发布，不打 tag |

PR 侧那一层只做 `go run ./cmd/changelog validate`，不判断“这个 PR 该不该有 fragment”——在
本模型里答案总是“不必须”。它的价值只是让写错格式的 fragment 早点暴露，而不是逼作者写。
如果连这一层也不想要，可以完全省掉：`docs.yml` 已经在每个 PR 上运行，把 `validate` 挂进它
的 “Validate documentation sources” 步骤即可，不必新建 workflow。

发布侧的强制检查是真正的门禁，判定逻辑：

- 校验 YAML schema、唯一 `id`、Module 名称、中英文摘要和 migration 规则；
- Core 发布：区间内无 `core` 条目且不是显式无变化重发时，中止；
- Module 发布：本批每个获得新 tag 的 Module 都必须有条目，或被 5.5 节自动归类为重新打包；
- 受影响集合直接取 `scripts/ci/module-revisions.sh --base <上一个 image-release tag> --print`
  的输出，不另写一份近似的路径判定规则，保证“需要提升 revision”和“需要 changelog”永远
  是同一个集合；
- 豁免必须落在 fragment 里（显式的重新打包条目），而不是 PR label。发布分支上没有 PR，
  label 这条通道在本模型里不存在。

### 6.2 Core 发布工作流

修改 `.github/workflows/anas-release.yml`：

- 版本计算完成后生成 `anas-vX.Y.Z.md`；
- 无 Core fragment 且不是显式无变化重发时失败；
- `gh release create` 改用 `--notes-file`；
- 将 notes 与二进制、校验和一起作为发布构建产物保留；
- CNB Release 正文当前是 `.cnb.yml` 里硬编码的单行 `description`，由 CNB 侧
  `type: git:release` 阶段用推送上去的 `refs/heads/<tag>` 分支树创建，读不到 GitHub Release
  的正文。要让两边一致，必须把生成的 notes 提交进该 tag 的树，再让 `.cnb.yml` 从文件读取，
  并确认 CNB 的 `description` 字段能承载多行 Markdown；
- 语言分流需要明确：CNB 面向中文用户用 `summary`，GitHub 用 `summary_en`（或双语并列）。
  fragment 强制双语的价值正体现在这里，不能只说“复用同一份正文”。

`anas-release` 只创建 tag、不产生提交，当前它是 `master` 的祖先，发布提交本来就在 `master`
上；但发布过程中生成的任何文件都无法沿这条路径回到 `master`。因此根 `CHANGELOG.md` 有两个
可选来源，需要二选一并写进决策表：

- 由 master 的文档构建动态生成，仓库里不保留该文件（与 `materialize-module-docs` 的现有模型
  一致，最省事）；
- 发布成功后由 bot 创建一个只更新生成文件的 master PR，不直接写回或强推。

无论选哪种，都不要在 `anas-release` 上删除 fragment。

### 6.3 Module 发布工作流

修改 `.github/workflows/container-images.yml`：

- `prepare` 阶段在 revision 计算后为每个候选 Module 生成 notes；
- PR dry-run 将批量摘要和单 Module notes 上传为 Actions artifact；
- 正式发布在创建 Module tag 前验证 notes 不为空或已有合法豁免；
- 给每个 Module tag 的 annotated tag message 写入精简摘要；
- 发布一个批次级 Markdown/JSON notes artifact，供文档站和管理界面读取；
- catalog 后续可增加可选 `changelog_url` 或 `notes_digest`，但不要把完整历史塞进发现 catalog。

Module 侧比 Core 侧多一个便利：`prepare` 作业已经在 `image-release` 上创建
`chore(images): calculate release revisions` 提交，`finalize` 作业成功后会执行
`git push origin "$RELEASE_SHA:refs/heads/master"`。生成的 `modules/<name>/CHANGELOG.md`
只要在 `prepare` 里一起 `git add`（`gen-module-docs` 的产物已经这样处理），就会随发布提交
自动进入 `master`，不需要 bot PR。这正是 6.2 节 Core 侧做不到的事。

还需要覆盖手动发布路径：`workflow_dispatch` 且 `module=all` 时，`packages` 步骤会选中全部
18 个 Module，与 revision 是否变化无关。这条路径下未变化的 Module 不应生成新的 changelog
段落，也不应因为“没有 fragment”让整批发布失败。

不建议为一轮批次发布的每个 Module 都创建 GitHub Release：一次发布可能产生十几个 release，
会淹没 Core Release。GitHub 上保留 Core Release，Module 历史通过独立 tag、文档站模块页和
OCI notes artifact 展示更清晰。

## 7. 文档站和未来管理界面

`cmd/materialize-module-docs` 已经在构建时生成单 Module 页面和有界版本历史，适合直接接入
changelog 数据：

- Core 文档增加 `/reference/changelog`；
- 每个 Module 页面增加“版本变更”区块；
- 中文页读取 `summary`，英文页读取 `summary_en`；
- 最新版本展示完整 notes，旧版本默认折叠；
- breaking、migration、security 使用明确的视觉标识；
- 未来 Web 管理端执行升级前，可以通过 catalog 的 notes 引用展示“当前版本 → 目标版本”
  的累计变化和迁移要求。

第一阶段不应修改 `anas.module-catalog/v1`。先把 Git 内记录、发布 notes 和文档展示做通，待
管理端确实需要远程查询 notes 时再设计 catalog 的兼容扩展或独立 OCI artifact。

### 7.1 接入现有 VitePress 构建链路

当前文档构建不是直接在仓库的 `docs/` 中生成 Module 页面，而是使用临时文档源树：

```text
npm run docs:build
  -> scripts/ci/build-versioned-docs.mjs
  -> scripts/ci/docs-source.mjs: prepareDocumentationSource()
     开发模式：复制仓库 docs/ 到临时目录
     发布模式：git archive <coreTag> -- docs 抽取该 Core tag 的 docs/，
               再用当前 checkout 的 docs/.vitepress 覆盖回去
  -> cmd/materialize-module-docs 生成 Module 页面
  -> vitepress build
  -> docs/.vitepress/dist/
```

这个区别决定了两件事：

- `docs/.vitepress/config/sidebar.ts` 的新增条目在发布模式下**立即生效**，因为 `.vitepress`
  始终取自当前 checkout，不受 Core tag 约束；
- 任何写进仓库 `docs/` 的真实页面在发布模式下会被 Core tag 的版本覆盖。changelog 页面只能写
  进 `--docs-root`，这不是风格偏好，而是唯一能在发布模式下生效的做法。

Changelog 应复用这个模型。生成器只向 `--docs-root` 指定的临时目录写 Markdown，不直接修改
或提交真实 `docs/` 页面。VitePress 会像处理普通 Markdown 一样把生成页面编译成 HTML，
不需要自定义 VitePress plugin。

另外，`build-versioned-docs.mjs` 现在用 `releaseModules: Boolean(releaseCoreTag)` 把 Module 的
发布模式绑定在 Core tag 是否存在上。changelog 生成器不要照抄这个耦合，应接收独立的
`--core-ref` 与 `--module-release-mode`，否则会出现“Core 尚未发布 → Module changelog 意外
显示未发布内容”的组合。

建议把 fragment 解析、tag 区间和 Markdown 渲染放入共享 Go package，并增加单独的物化
命令：

```text
internal/changelog/                 fragment、tag 区间和渲染逻辑
cmd/materialize-changelog-docs/     向临时 VitePress 源树写页面
```

命令接口：

```bash
go run ./cmd/materialize-changelog-docs \
  --root /path/to/repository \
  --docs-root /tmp/anas-docs/docs \
  --core-ref v0.1.0 \
  --release-mode
```

`scripts/ci/docs-source.mjs` 应在 `materialize-module-docs` 完成后调用这个命令：

```js
run('go', [
  'run',
  './cmd/materialize-changelog-docs',
  '--root', repositoryRoot,
  '--docs-root', destinationDocs,
  ...(options.coreTag ? ['--core-ref', options.coreTag] : []),
  ...(options.releaseModules ? ['--release-mode'] : [])
], {
  cwd: repositoryRoot,
  env: {
    ...process.env,
    GOCACHE: path.join(tmpdir(), 'anas-docs-go-build-cache')
  }
})
```

### 7.2 生成页面和 URL

生成器应创建以下临时 Markdown 页面：

```text
docs/reference/changelog.md
docs/en/reference/changelog.md
docs/reference/modules/<name>/changelog.md
docs/en/reference/modules/<name>/changelog.md
```

对应公开 URL：

```text
/reference/changelog
/en/reference/changelog
/reference/modules/<name>/changelog
/en/reference/modules/<name>/changelog
```

所有生成页面应关闭编辑链接和文件时间，避免把用户指向不存在的源文件或把本次构建时间误当
成变更时间：

```yaml
---
title: ANAS 变更记录
editLink: false
lastUpdated: false
---
```

Core 页面按 `vX.Y.Z` 分段；Module 页面必须按完整的 `version-rrevision` 分段，例如同时保留
`34.0.2-r5` 与 `34.0.2-r6`。

**生成器必须转义 fragment 正文里的 Vue 插值。** VitePress 把 Markdown 编译成 Vue 模板，
围栏代码块默认带 `v-pre`，但**行内代码和正文里的双大括号会被当成 Vue 表达式求值**。摘要里
只要出现一次，就会破坏整页。本方案撰写时两种情况都实测过，失败方式不同：

```text
${{ github.event.pull_request.base.sha }}
  表达式在 JavaScript 语法上合法 -> SSR 渲染期抛
  TypeError: Cannot read properties of undefined
  但 vitepress build 仍输出 build complete，npm run docs:build 退出码为 0

{{ '{{' }}
  表达式语法非法 -> vite:vue 编译期报
  Error parsing JavaScript expression，构建以非零退出码失败
```

第一种是真正危险的：CI 会静默放过一个渲染失败的页面。一条描述 GitHub Actions 表达式或模板
语法的 Module 摘要就足以触发它。因此：

- 渲染时把 `summary`、`summary_en`、`migration` 里的双大括号转义（用 `<span v-pre>` 包裹，
  或改写成 Vue 的字面量写法），fragment 作者不应该需要了解 VitePress；
- 或者更简单，`validate` 直接拒绝含裸双大括号的摘要，代价是无法描述模板类变更；二选一即可；
- 文档 CI 必须额外检查构建日志，不能只看退出码，见 7.8 节。

本文件自身也踩过这两个坑，7.2 与 12 节的示例因此全部放在围栏代码块里。

### 7.3 Core 发布内容边界

正式文档构建时，`build-versioned-docs.mjs` 已经解析最新稳定 Core tag，并通过
`DOCS_PUBLISH_RELEASES=true` 选择 release source。该 tag 必须继续向 changelog 生成器传递为
`--core-ref`：

- PR 和本地开发模式可以显示一个明确标注的 `Unreleased` 区块；
- 正式发布模式只渲染 `--core-ref` 已包含的 fragment；
- master 中尚未进入 Core tag 的 fragment 不得提前出现在正式网站；
- Core 历史段落由相邻稳定 Core tag 区间计算。

第一次接入时，生成器读取配置中的 Core baseline tag；baseline 以前只提供 tag/compare 链接，
不尝试自动解释全部历史提交。

### 7.4 Module 发布内容边界

正式文档构建已经向 `materialize-module-docs` 传递 `--release-mode`，以最新完整 Module release
作为当前页面。Changelog 生成器应采用相同规则：

- 只展示已经存在不可变 `module/<name>/<version>-r<revision>` tag 的发布；
- 每一段内容由该 Module 上一个 tag 到当前 tag 之间新增的匹配 fragment 生成；
- 本地开发模式可以显示候选 Module 变化，但必须标记为 `Unreleased`；
- 发布模式不得根据工作树中的 `module.yml` revision 猜测尚未成功发布的版本。

现有 Module 文档为了减少重复页面，会对同一上游 version 只保留最终 revision，并按文档正文
fingerprint 合并相同页面。Changelog 不能复用这个去重结果：即使 `r5` 和 `r6` 的 README
完全相同，两次 revision 的变更记录也必须分别保留。可以让多个版本化文档页面继续 alias，
但它们的 changelog anchor 不能被合并。

### 7.5 页面导航

在 `docs/.vitepress/config/sidebar.ts` 的中英文参考区分别加入：

```ts
{ text: 'ANAS 变更记录', link: '/reference/changelog' }
{ text: 'ANAS changelog', link: '/en/reference/changelog' }
```

`cmd/materialize-module-docs` 的版本导航应从：

```text
用户文档 · 技术文档
```

扩展为：

```text
用户文档 · 技术文档 · 变更记录
```

其中“变更记录”分别链接到：

```text
/reference/modules/<name>/changelog
/en/reference/modules/<name>/changelog
```

两个必须注意的实现约束：

- `renderVersionNavigation` 里的 `pageBase` 在固定快照页是
  `/reference/modules/<name>/<version>-r<revision>/`。“变更记录”必须链接到 Module 根
  （`prefix + module + "/changelog"`），不能拼在 `pageBase` 后面，否则快照页会指向
  `/reference/modules/<name>/<release>/changelog` 这样的死链。当前版本站点的
  `ignoreDeadLinks` 取 `docsVersion.archive`（当前版为 `false`），死链会让构建直接失败。
- `materialize-module-docs` 先运行、changelog 物化命令后运行，前者输出的链接依赖后者是否
  生成了页面。为避免这层反向依赖，**为 `.github/modules.json` 中每个 Module 无条件生成
  changelog 页**，没有条目时写入明确占位（例如“该 Module 自 baseline 起尚无变更记录”）。

侧边栏还需要一次取舍：Module 列表来自 `moduleSidebar()` 读取
`docs/.vitepress/generated/module-docs.json`，该文件由 `materialize-module-docs` 生成，目前
每个 Module 只有 `link_zh` / `link_en` 两个字段。要么接受 Module changelog 只能从页内导航
进入，要么扩展这个索引结构并同步改 `docs/.vitepress/config/module-docs.ts`。建议第一阶段接受
前者，等页面稳定后再决定是否进侧边栏。

Module catalog 页面也可以在版本列或 Module 详情入口旁提供 changelog 链接，但不应把全部
变更正文复制进 catalog 表格。

### 7.6 本地预览和生产构建

开发者继续使用现有命令：

```bash
npm run docs:dev
```

`dev-docs.mjs` 同样经过 `prepareDocumentationSource()`，因此新增的物化命令会自动参与本地
预览。开发模式允许展示未发布区块，便于在 PR 中审阅文案。

正式构建：

```bash
DOCS_PUBLISH_RELEASES=true npm run docs:build
```

输出仍位于：

```text
docs/.vitepress/dist/
```

`docs.yml` 在 Core 或 Module 发布 workflow 成功后运行 release build，因此页面会在相应 tag
和 artifact 已经成功建立后更新。

### 7.7 历史 Core 文档快照

`build-versioned-docs.mjs` 当前只对最新版调用 `prepareDocumentationSource()`；旧 major 的
归档站点直接从历史 Core tag 提取 `docs/` 后构建。因此第一阶段可只在当前站点提供完整
changelog，旧 major 归档保留指向当前 changelog 的入口。

若要求每个历史 major 都带当时截止的 changelog，第二阶段再修改历史构建循环：在构建每个
历史 tag 前调用 changelog 物化命令，并显式传入该 tag 作为 `--core-ref`。不能让历史归档读取
HEAD 或当前最新 release，否则旧文档会出现未来版本内容。

### 7.8 VitePress 与生成器测试

文档 CI 除现有 `materialize-module-docs` 测试外，应增加：

```bash
go test ./internal/changelog ./cmd/materialize-changelog-docs
go run ./cmd/materialize-changelog-docs \
  --root "$PWD" \
  --docs-root "$TMPDIR/anas-changelog-docs"
npm run docs:build
```

必须覆盖：

- Core 与 Module 条目不会串线；
- 同一 Module 的 `rN` 和 `rN+1` 均被保留；
- 中文和英文页面完整，且不存在缺少翻译的条目；
- 相同 tag/commit 输入得到字节级一致的 Markdown；
- release mode 不出现尚未发布的 fragment；
- baseline、首次发布、无变更重发和 `all-modules` 均有 fixture；
- fragment 被重命名或按年归档后，历史区间的归属不变（5.3 节集合差算法的回归测试）；
- 某个 fragment 进入 tag 之后在工作树被删除时，生成器给出明确错误而不是静默漏条目；
- 每个注册 Module 都有 changelog 页面，包括没有任何条目的 Module；
- 含双大括号插值、`<script>`、未闭合 HTML 标签的摘要不会破坏页面渲染（7.2 节）；
- VitePress 的 dead-link 检查能够验证生成页面、anchor 和中英文导航。

由于 SSR 渲染错误不影响退出码（7.2 节），文档 CI 需要显式判定构建日志：

```bash
npm run docs:build 2>&1 | tee build.log
! grep -qE 'TypeError|\[vitepress\] Error' build.log
```

如果决定在仓库内保留 `CHANGELOG.md` 和 `modules/<name>/CHANGELOG.md`，生成器必须提供
`--check`，并接进 `docs.yml` 现有的 “Validate documentation sources” 步骤，与
`gen-module-docs --check`、`gen-contract-docs --check` 保持同一约定。

最终的数据流保持单向：

```text
.changes/*.yml + immutable tags
  -> internal/changelog
  -> materialize-changelog-docs
  -> 临时 Markdown 页面
  -> VitePress HTML
```

`.changes/` 是事实源，Go 生成器负责版本归属与内容，VitePress 只负责站点编译和导航展示。

## 8. 历史回填策略

不建议依赖 LLM 或 commit message 自动重建全部历史，因为发布归属和用户影响容易判断错误。

建议：

1. 记录启用日各组件的 baseline：Core 为最新 `v*`，每个 Module 为各自最新
   `module/<name>/*`；
2. 在生成的 changelog 中写一条“从此版本开始维护结构化 changelog”；
3. 对最近 1 至 3 个重要版本，可由维护者按 PR/tag 手工补录 `historical: true` fragment；
4. 更早历史只提供 tag/compare 链接，不伪造不可靠的分类说明。

## 9. 分阶段实施

### 阶段 A：最小闭环

预计 1 个小型 PR：

- 定义 `.changes/schema.json`；
- 把 3.4 节的发版补全步骤写进 agent 读得到的文件：`AGENTS.md`（当前 4 行，只讲 research
  报告）与 `docs/developer/release.md` 的发布流程；`CONTRIBUTING.md` 补一句说明 fragment
  何时写、由谁写；
- 实现 `validate`、`check`、Core/Module 单次渲染；
- 为当前 Core 和所有 Module 建 baseline；
- 发布工作流强制校验 change fragment，PR 侧只做 schema `validate`（见 6.1 节）；
- Core GitHub Release 改用生成的 notes；
- Module dry-run 生成单模块 notes 和批次摘要。

验收标准：新增一个 Core 修复和一个 Module 修复时，CI 能正确要求两个 component 条目，并
生成彼此独立的 release notes；同时，一次只改 `go.mod` 的 PR 不会要求 17 条 fragment，而是
让受影响 Module 自动归类为重新打包（5.5 节）。

### 阶段 B：发布与仓库视图

- Module 正式发布校验 notes，并生成批次 artifact；
- 生成根 `CHANGELOG.md` 和 `modules/<name>/CHANGELOG.md`；
- 文档构建加入 Core 和 Module changelog 页面；
- 增加确定性输出、tag 区间、首次发布、同版本 revision 增长和 `all-modules` 测试。

验收标准：从任意历史 tag 重新运行生成器，输出一致；Module `rN` 与 `rN+1` 的内容不会混入
其他 Module。

### 阶段 C：升级体验

- 设计并发布 Module notes OCI artifact 或 catalog 可选引用；
- CLI 增加 `anas module changelog <name> [--from ... --to ...]`；
- Web 管理端在升级确认页展示累计变化和 migration；
- 安全公告可链接 GitHub Security Advisory，同时避免在修复发布前泄露细节。

## 10. 推荐决策

建议现在批准阶段 A 和阶段 B，暂缓 catalog schema 与 CLI 扩展。核心决策如下：

| 决策 | 推荐 |
| --- | --- |
| 事实源 | 永久保留的结构化 change fragment |
| Core 与 Module | 独立 changelog，允许一个 fragment 同时包含多个 component |
| 语言 | fragment 强制中英文摘要 |
| 版本归属 | 由组件自己的不可变 tag 区间计算 |
| GitHub Release | Core 使用；Module 不批量创建 GitHub Release |
| Module 展示 | 单 Module tag + 文档页 + 批次 notes artifact |
| 历史 | 从当前 release 建 baseline，重要版本人工回填 |
| CI | 与现有 Module revision 变更上下文共用判定逻辑；强制检查在发布工作流 |
| 维护责任 | 发版 agent 补全；作者可前置写下 diff 读不出的信息；维护者只审不代写 |
| 写入时机 | 推送发布分支之前，覆盖上次发版到本次发版的区间 |
| 写入位置 | 一律提交到 `master`，再推发布分支（`anas-release` 不回流） |
| 门禁位置 | 发布工作流强制；master PR 仅 schema 校验，可挂进已有的 `docs.yml` |
| 归属算法 | 两端 fragment 全集按 `id` 求差，不用 `--diff-filter=A` |
| baseline | 记录启用 changelog 的 commit，而不是 `v0.1.0` |
| 共享上下文重新打包 | 按 revision 提升来源自动分档，不逐个人工豁免 |
| 仓库内 `CHANGELOG.md` | 待定：动态生成不入库，或由 bot PR 更新（见 6.2） |
| Release 正文语言 | GitHub 用英文、CNB 用中文；文档站双语页各取一侧 |

这能先解决“没有 changelog”的实际问题，同时不会把未来的 Web 管理端、catalog schema 或
Module 安装协议提前绑死。

## 11. 实施前需要拍板的开放问题

1. **仓库内是否保留 `CHANGELOG.md`。** 保留就要加 `--check` 和 bot PR；不保留则文档站是唯一
   入口，`git clone` 之后看不到变更历史。倾向不保留，与既有生成页面模型一致。
2. **历史段落是否允许回溯修订。** 见 5.3 节：文档站可修订、Release 正文冻结是推荐组合，但
   需要确认维护者接受两处可能出现措辞差异。
3. **Module changelog 是否进侧边栏。** 见 7.5 节。
4. **PR 侧是否保留 schema 校验。** 见 6.1 节：可以新建 workflow，也可以挂进已有的
   `docs.yml`，或者完全省掉只靠发布门禁。倾向挂进 `docs.yml`，零新增 workflow。
5. **英文摘要是否强制。** 倾向保留强制，理由见 3.3 节：本仓库的 fragment 主要由 agent 在
   实现变更时产出，同时写出中英文摘要几乎零成本，真正昂贵的是事后由人补翻译。参照
   `docs/developer/documentation-standard.md` 已有的约定，要求在同一次任务里写完，不把翻译
   留给后续任务。若实际执行下来仍然拖慢节奏，再退为“中文强制、英文缺失时页面显式标注未
   翻译”，但这必须写进门禁而不是靠自觉。（对比：`docs/reference/module-environment-variables.md`
   至今没有英文镜像，就是靠自觉的结果。）
6. **`workflow_dispatch` 手动全量发布的归属规则**（见 6.3 节）。

## 12. 核对记录

本方案在 2026-08-18 对照 `master` 的 `72391e4` 逐条核对过，结论所依据的事实来源如下。实施时
如果这些文件已经变化，需要重新核对对应结论。

| 结论 | 事实来源 |
| --- | --- |
| master PR 无 CI 门禁 | `.github/workflows/` 下五个 workflow 的 `on:` 触发器 |
| Module 发布快进回 master | `container-images.yml` 的 `prepare` 与 `finalize` 作业 |
| Core 发布只打 tag | `anas-release.yml` 的 `publish` 作业；`anas-release` 是 `master` 的祖先 |
| 17 个 Module 共享 `go.mod` | `.github/modules.json` 的 `shared_contexts` |
| revision 判定与 reason 取值 | `scripts/ci/module-revisions.sh` 的 `module_context_changed` |
| 发布模式从 Core tag 抽取 `docs/` | `scripts/ci/docs-source.mjs` 的 `extractCoreDocumentation` |
| Module 发布模式绑定 Core tag | `scripts/ci/build-versioned-docs.mjs` 的 `releaseModules` |
| 版本导航与快照页 `pageBase` | `cmd/materialize-module-docs/main.go` 的 `renderVersionNavigation` |
| 同版本文档按 fingerprint 合并 | 同上的 `deduplicateReleases` |
| Module 侧边栏来自生成索引 | `docs/.vitepress/config/module-docs.ts` 的 `moduleSidebar()` |
| 当前版站点检查死链 | `docs/.vitepress/config.mts` 的 `ignoreDeadLinks: docsVersion.archive` |
| CNB Release 正文硬编码 | `.cnb.yml` 的 `type: git:release` 阶段 |
| 发版区间规模（支持 3.4 节） | 6 个 image-release 批次实测 4/6/6/12/13/19 个提交；Core `v0.1.0..master` 59 个提交 |
| commit message 可重建性 | 最近 100 条为规范 Conventional Commits：fix 23 / feat 19 / docs 18 / test 7 / chore 5 |
| 单人仓库、变更由 agent 产出 | `git shortlog -sne --all`：291 个提交来自同一维护者 + 5 个 bot；分支前缀 `codex/*`、`claude/*`、`worktree-agent-*` |
| 已有 PR checklist 可挂载 | `.github/pull_request_template.md` 的 Documentation checklist |
| agent 约定的既有句式 | `docs/developer/documentation-standard.md` 第 26 行 |
| Vue 插值的两种失败模式 | 实测：合法表达式仅渲染期报错、退出码 `0`；非法表达式编译期失败、退出码非零 |

本文件自身已按 [文档写作标准](/developer/documentation-standard) 注册进
`docs/research/index.md`、`docs/en/research/index.md` 的英文索引摘要，以及
`docs/.vitepress/config/sidebar.ts` 的 `/research/` 区块。
