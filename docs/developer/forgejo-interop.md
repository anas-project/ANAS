# 与 Forgejo 互操作的原则

> 状态：**当前模型**。本文归纳的规则来自已落地并对固定 `forgejo 15.0.7` 验证过的代码
> （`modules/ai_agent/orchestrator`、`modules/forgejo`）；少数标注为**提案**的条目尚未实现，
> 不能当作可执行说明。更新：2026-09-08。

写任何要跟 Forgejo 说话的代码之前先读这一页。它不解释设计动机——那些在
[AI Agent 编排设计](/architecture/ai-agent-orchestration-design)与
[Forgejo Module 设计](/architecture/forgejo-module-design)里——它只给出**必须遵守的规则**，
以及**上游那些会安静地咬你一口的事实**。

规则分两类：**边界**（违反会造成越权、审计断链或重复副作用，不可协商）和**约定**
（违反只是不一致，评审时会被要求改）。每条都注明依据；改规则先改依据文档，不要先改代码。

## 0. 速查

| 你要做的事 | 读哪节 | 一句话 |
| --- | --- | --- |
| 建账号、发 token、加 SSH key | §2 | 顺序是硬的：建账号 → 授 collaborator → 发 token |
| 发评论、打标签、改 issue | §3 | 一律经 outbox，带幂等键；永远不要直接调客户端 |
| 提交文件、开 PR | §3.4 | 路径校验在控制面；讨论期工作区没有 git 写凭据 |
| 接 webhook | §4 | 先验签再解析；任何非服务端故障都回 202 |
| 判断"这个人能不能让 Agent 干这个" | §5 | 默认拒绝；三层取交集；只能收窄 |
| 调一个还没用过的 Forgejo API | §6 + §8 | 先在固定镜像上实测，把结论写进事实表 |

## 1. 三条不可协商的边界

### 1.1 凭据分三面，永不混用

| 面 | 凭据 | 谁持有 | 绝不出现在 |
| --- | --- | --- | --- |
| 管理面 | 管理员账号**口令**（不是 token） | 控制面进程 | 执行实例、日志、任何 issue |
| 协作面 | 每个 Agent 自己的、限定到仓库的 token | 控制面 outbox | 执行实例 |
| 执行面 | 作业期短时 token / SSH key | 一次性实例的 tmpfs | 镜像、环境变量、日志 |

代码上这条边界是**两个接口**：`ForgejoAdmin`（身份）与 `ForgejoIssues`（协作）。它们没有合并成
一个，就是为了让"顺手拿错凭据"写不出来。新增方法时先问它属于哪一面，不要因为共用同一个 HTTP
transport 就把它塞进就近的接口。

管理面为什么必须是口令：固定版本上 `POST /admin/users/{u}/tokens` **不存在**，发 token 的唯一
端点 `POST /users/{u}/tokens` **拒绝 token 认证**，只接受管理员 basic auth 加 `Sudo:` 头（§6）。

### 1.2 写操作只走 outbox

任何会在 Forgejo 里留下痕迹的调用都必须包在 `Outbox.Do(ctx, key, runID, kind, target, fn)` 里。
幂等键描述**意图**（`<owner>/<repo>#<issue>:<kind>:<discriminator>`），不是描述**投递**——这正是
webhook 与对账扫描针对同一变化只产生一次写入的原因。

绕过 outbox 直接调客户端会同时丢掉四样东西：幂等、`run_id` 自触发标记、审计记录、预算计账。
四样里任意一样丢了都不会立刻报错，只会在几周后表现为"同一条评论发了两遍"或"这次写入没人能解释"。

### 1.3 入站先验签，再谈内容

签名校验发生在**读取仓库名之前、读取 sender 之前、读取事件类型之前**。在验签之前，请求体里
没有任何字段是可信的，包括看起来无害的 `repository.full_name`。

请求体有 4 MiB 上限，且上限在验签之前生效——否则一个未认证的请求就能让 ingress 先缓冲任意大小
的内存再决定拒绝它。

## 2. 凭据与身份（管理面）

### 2.1 引导顺序是硬的

```text
建账号（POST /admin/users）
  → 授 collaborator（PUT /repos/{o}/{r}/collaborators/{u}）
  → 发 token（POST /users/{u}/tokens + Sudo 头）
```

不能调换。Forgejo 在**发起账号自己的上下文里**解析 token 的 `repositories` 字段，因此一个该账号
还看不见的仓库会被报成 `repository does not exist`——读起来像仓库不存在，其实是权限顺序错了。

Agent 账号建出来就是拿来持有凭据的，不用于交互登录：`must_change_password: false`、
`restricted: false`、`visibility: limited`，不给管理员权限。

### 2.2 轮换：先发新的，再吊销旧的，名字必须带代次

access token 名称**按用户唯一**，SSH key 标题同理。而轮换的保证恰恰是"新旧短暂并存"，所以复用
同一个名字会在发新凭据那一步就 400。代次写进名字（`anas-agent-<n>`）不是美观问题，是可行性问题。

凭据按**年龄**轮换（当前 30 天），由 reconcile 驱动，不依赖任何人记得做。手工建的同名账号的
token 不带模块前缀，因此不会被轮换逻辑碰到。

### 2.3 scope 集合被上游钉死，不要随手加

带仓库限定的 token **只能**携带 `read:issue`、`write:issue`、`read:repository`、`write:repository`
四个之一或组合，其他组合 400。讨论期实际用三个：`read:repository`、`read:issue`、`write:issue`；
`write:repository` 只在执行作业期间追加。

这条的陷阱在于失败方式：为了加一个 `read:user` 而放弃 `repositories` 限定，**代码照样能跑**，
只是最小权限没了。所以允许集合在代码里是断言，不是注释。

### 2.4 仓库限定不是隐藏手段

限定在**内容与写操作**上生效（越界仓库的 contents 403、开 issue 404），但**仓库元数据仍可读**
（`GET /repos/{o}/{r}` 200）。写文档时要照实说：它是最小权限手段，不是"看不见别的仓库"。

## 3. 写路径（协作面）

### 3.1 自触发要闭两次环

1. **按账号**：`sender` 是本部署的任一 Agent 账号 → 丢弃；
2. **按 run marker**：正文里有本编排器签发的 `<!-- anas-agent-run:<id> -->` 且该 `run_id` 在
   `outbox_write` 里 → 丢弃。

只做第一条会在"以某个人的身份代 Agent 写入"时立刻破功。两条都要有。

### 3.2 评论三分法

| 要说的 | 怎么写 |
| --- | --- |
| "这个 issue 现在什么情况" | **唯一一条**状态评论，`PATCH` 原地更新，永不重发 |
| 修改意见、提问、结论 | **新建**评论 |
| 收到了但暂时没内容 | **reaction**（👀 / 🚀 / ✅） |

状态评论靠正文里的 HTML 注释标记找回，因此重启后不依赖数据库里的 id 也能定位。状态渲染是
**确定性**的：状态没变就产生逐字节相同的正文，调用方据此跳过无意义的编辑——节流才诚实。

**永远不要发"正在思考"占位评论。** 占位评论事后要么删要么改，两种都弄脏时间线和通知。

### 3.3 标签分两类，别混

- **控制标签**（`ai:plan`、`ai:approved`、`ai:chat/<agent>`…）：人打的，是输入；
- **状态标签**（`ai:running`、`ai:needs-review`、`ai:failed`、`ai:job`…）：模块打的，是输出。

把状态标签当输入读，等于让任何有写权限的人伪造一个"正在运行的作业"。

模型与思考强度**不进标签**：它们在对话里反复调整，每次调整都会产生一个 `issue_label` 事件，
把 issue 时间线刷成参数流水。它们是状态，存库并显示在状态评论里。

标签词表由 Agent 注册表生成，**代码与文档里都不出现硬编码的运行时名**。

### 3.4 产物进 Git，不进评论

- 讨论与计划阶段的工作区**没有 git 写凭据**；文档由控制面用 contents API 提交；
- 路径校验在控制面：既要在仓库允许的文档目录内，也要符合"这一类文档放哪"的约定——把调研报告
  写进计划目录能过 allowlist，但违反仓库自己的约定，一样拒绝；
- commit 消息带 `issue #<n>` 与阶段，让 `git log` 单独就能解释一个文件为什么变；
- 评论里的链接**一律指向具体 commit**（`/src/commit/<sha>/<path>`）。指向分支的链接会在读者脚下
  移动；约定以后调整时，历史链接还得继续有效。

### 3.5 契约细节

| 动作 | 注意 |
| --- | --- |
| 提交文件 | 替换已有 blob 用 `PUT` 并带上它的 SHA，新建用 `POST`；带 SHA 才能把并发编辑变成冲突而不是静默覆盖 |
| 路径转义 | **逐段**转义，整体转义会把分隔符变成 `%2F`，指向一个名字里带斜杠的文件 |
| 建依赖 | 请求体是 `IssueMeta`，只发 index 会被回成 `repository does not exist` |
| 重复建依赖 | 上游回 **500** 而不是冲突；按消息匹配放行这一种，**不要**笼统忽略 500 |
| 指派 | issue 表单的 front matter 只有 `labels` 没有 `assignees`，指派必须在 issue 创建后用 API 补 |
| 工时 | 写进 Forgejo 原生 tracked time，让 AI 的投入出现在所有人共用的报表里 |

## 4. 读路径与入站

### 4.1 一切非服务端故障都回 202

| 结果 | 含义 |
| --- | --- |
| `accepted` | 新事件，已入 inbox |
| `duplicate` | 投递 id 见过了——这是**成功**，副作用只发生一次正是目的 |
| `dropped` | 仓库没启用 |
| `self` | 本部署自己的写引起的 |
| `ignored` | payload 没指明仓库 |

回 4xx 会让 Forgejo 重投一个本部署已经决定不管的事件。丢弃不是发送方能修的错误。

### 4.2 按"至多一次"设计，对账走同一条路

固定版本的投递语义（超时、重试、能否重投）**仍未复核**，因此任何"只有在保证重试的前提下才正确"
的逻辑都不能依赖。对账扫描重建的事件走**与 webhook 完全相同**的 `Accept`：同一份 allowlist、
同一个自触发过滤、同一张去重表。不要为对账另写一条稍有不同的路径。

### 4.3 只解析你需要的字段

入站信封只解出仓库与 sender，其余原样落库，由认领该事件的处理器去解析。**ingress 看不懂的字段
永远不构成丢弃一次投递的理由。**

## 5. 授权

默认拒绝。判定链见[编排设计 §6.3](/architecture/ai-agent-orchestration-design)，写代码时记住三点：

1. **三层取交集**：目录组上限 ∩ 仓库权限推导 ∩ 覆盖条目。任何一边都不能单方面放大另一边；
2. **issue 正文、模板答案、标签、命令只能收窄**，永远不能放宽；
3. **每次判定都写 `decision`，包括拒绝**。只记通过的判定，等于在最需要解释的时候没有记录。

作业真正开始前必须**再判定一次**——批准与开跑之间可能隔着一整夜。

Agent 不是独立的权限主体：它发起的每个写操作都要能归属到一个人（**提案**，见编排设计 §6.5）。

## 6. 上游会咬人的事实（`forgejo 15.0.7` 实测）

全部由 `test-env/scripts/forgejo-agent-api-probe.sh` 对固定镜像验证。**下表里每一条的共同特征是：
按直觉写出来的代码能编译、能跑，然后在别的地方错。**

| 事实 | 按直觉写会怎样 |
| --- | --- |
| `POST /admin/users/{u}/tokens` 返回 **404**；唯一端点是 `POST /users/{u}/tokens`，且拒绝 token 认证 | 控制面只持有 token，引导整条链走不通 |
| `repositories` 是 `[{"owner":…,"name":…}]` **对象数组**，`"owner/name"` 字符串被拒 | 反序列化错误，且容易被"退化成不限定"绕过 |
| 发 token 前账号**必须已是 collaborator** | 报 `repository does not exist`，看起来像仓库配错了 |
| access token 名称、SSH key 标题**按用户唯一** | "先发后吊销"在发新的那一步 400，轮换保证反而没了 |
| **`GET /admin/hooks` 恒返回空数组**，`GET /admin/hooks/{id}` 正常 | 每轮对账都以为 hook 不存在，重复注册 |
| 服务端**展开事件族**：请求 8 个，存下 17 个 | 用事件列表比对会永远判定为漂移；只比对 URL 与密钥指纹 |
| 带仓库限定的 token 只允许四个 scope，其余组合 400 | 加一个 scope 就静默换掉了仓库限定 |
| 仓库限定不挡**元数据读取**（`GET /repos/{o}/{r}` 200） | 文档写成"看不见别的仓库"，与实际不符 |
| issue 表单 front matter 的 `labels` 是**新建页的预勾选**，由浏览器回传 `label_ids`，不是服务端在提交时应用 | 靠 `ai:auto` 判断"是不是 Agent issue"，取消勾选或非浏览器提交就漏判——改为从正文识别，标签由编排器补 |
| Forgejo 15 的表单**没有 `_csrf`**，改用 SameSite cookie 加 Origin/Referer 校验 | 脚本化登录去找 CSRF token，找不到 |

尚未复核：webhook 投递语义、`issue_label` / `issue_assign` 的 payload 字段。两者都需要真实接收端。

Projects 看板**仍然没有 API 也没有事件**。机器可读的状态一律落在 label、issue 开闭与指派上；
不要设计任何依赖看板列的自动化。

## 7. 反模式

- ❌ 直接调 `ForgejoIssues` 的方法写 Forgejo——绕过 outbox（§1.2）；
- ❌ 用管理凭据做协作面的活，或反过来（§1.1）；
- ❌ 把 Forgejo 写凭据交给执行实例或 Agent 进程（§1.1、§3.4）；
- ❌ 从 issue 正文或评论拼 shell 命令——它们是不可信输入；
- ❌ 发"正在处理中"占位评论（§3.2）；
- ❌ 把模型、思考强度这类频繁变化的参数写进标签（§3.3）；
- ❌ 读状态标签当作输入（§3.3）；
- ❌ 评论里给分支链接而不是 commit 链接（§3.4）；
- ❌ 用 `GET /admin/hooks` 判断 webhook 是否存在（§6）；
- ❌ 笼统 `if status == 500 { ignore }`（§3.5）；
- ❌ 因为"文档上说有"就调一个没在固定镜像上实测过的端点（§8）。

## 8. 动手前的检查清单

- [ ] 这个调用属于管理面、协作面还是执行面？用对应的接口和凭据。
- [ ] 它会在 Forgejo 里留下痕迹吗？包进 outbox，想清楚幂等键的 discriminator 是什么。
- [ ] 这个端点在固定版本上实测过吗？没有就先跑一次探针，把结论补进
      [ai_agent 需求 §15](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md)
      的事实表——**下一个人会依赖你的结论，不会重跑你的探针**。
- [ ] 新增的行为需要一条需求 ID 吗？需求与计划的写法见[需求编写规范](requirement-authoring.md)。
- [ ] 上游返回的成功状态码是什么？创建类接口 `201` 与 `200` 都要接受。
- [ ] 失败路径：会不会把一个可恢复的冲突当成服务端故障，或者反过来？

## 9. 这些规则从哪来

| 规则 | 依据 |
| --- | --- |
| 凭据三面、写路径、授权判定、标签词表、事件入站 | [AI Agent 编排设计](/architecture/ai-agent-orchestration-design) §3—§10 |
| Actions 授权、Runner 隔离、高风险开关 | [Forgejo Module 设计](/architecture/forgejo-module-design) §3、§5 |
| 需求 ID 与验收 | [ai_agent 要求](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md)、[forgejo 要求](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/dev-docs/requirements/forgejo-module.md) |
| 固定版本实测结论 | 同上要求文档 §15；探针 `test-env/scripts/forgejo-agent-api-probe.sh` |
| 实现 | `modules/ai_agent/orchestrator/`（`forgejo.go` 管理面、`forgejo_issues.go` 协作面、`ingress.go` 入站、`reconcile.go` outbox 与对账） |

规则与代码不一致时**以代码和实测为准**，并把这一页改过来：这份文档的价值全在于"读了就不用再去
翻三份设计文档和一遍源码"，一旦它开始说谎，就不如没有。
