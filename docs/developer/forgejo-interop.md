# 与 Forgejo 互操作基线

> 状态：**当前模型**。事实由 `test-env/scripts/forgejo-agent-api-probe.sh` 对固定
> `codeberg.org/forgejo/forgejo:15.0.7-rootless`（`15.0.7+gitea-1.22.0`）实测得出，逐条结论与通过
> 数记录在 [`ai_agent` 要求 §15](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md)。
> 表单相关的几条来自 `TestFormRenderingAgainstLiveForgejo`（同一固定镜像）而不是探针脚本；标 `CLI`
> 的几条来自 2026-09-20 对同一镜像跑 `--help` 的实测，报告在
> `test-env/reports/forgejo-cli-probe-20260920.md`（报告目录不入库）。三者都是实测，但要重跑的是不同
> 的东西。**探针里被 SKIP 的用例不算事实**，仍未有结论的列在 §1 末尾。
> 升级固定版本时必须重跑探针再改这一页，并同时执行
> [Forgejo Module 设计](/architecture/forgejo-module-design) §2.3 的身份复核（`FORGEJO-R-066`）：LDAP 与
> OIDC 能否按不可变 ID 同时接入，只能由探针回答，不能凭 changelog。更新：2026-09-20。

ANAS 里有两块代码跟 Forgejo 说话：[`forgejo` Module](/architecture/forgejo-module-design) 自己，
以及 `ai_agent` 的编排器。这一页是它们**共用的底座**——上游在固定版本上到底怎么表现，以及由此
直接推出的 API 规则。写任何 Forgejo 客户端代码之前先读它。

各自的内部纪律不在这里：编排器的 outbox、评论纪律与授权判定见
[编排器与 Forgejo 互操作的规则](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/forgejo-interop.md)。

## 1. 上游会咬人的事实

**下表里每一条的共同特征是：按直觉写出来的代码能编译、能跑，然后在别的地方错。**

「来源」列说这条结论是**怎么来的**，因为重跑它们要跑的是不同的东西：`探针` = 探针脚本的具名用例
（括号里是用例名），`live` = `TestFormRenderingAgainstLiveForgejo`，`推断` = 还没有实测证据。
**改任何一条之前先看这一列**；升级固定版本时，`探针` 与 `live` 两类都要重跑。

| 事实 | 按直觉写会怎样 | 来源 |
| --- | --- | --- |
| `POST /admin/users/{u}/tokens` 返回 **404**；唯一端点是 `POST /users/{u}/tokens`，且**拒绝 token 认证**，只接受管理员 basic auth 加 `Sudo:` 头 | 控制面只持有 token，引导整条链走不通——它必须持有管理员**口令** | 探针（`admin-token-absent`） |
| token 的 `repositories` 是 `[{"owner":…,"name":…}]` **对象数组**，`"owner/name"` 字符串被拒 | 反序列化错误，且容易被"退化成不限定"绕过 | 探针（`token-repo-scope`） |
| 发 token 前账号**必须已是该仓库 collaborator** | 报 `repository does not exist`，看起来像仓库配错了 | 探针（`token-repo-scope` 的引导顺序） |
| 带仓库限定的 token **只能**携带 `read:issue`、`write:issue`、`read:repository`、`write:repository`，其余组合 400 | 为多加一个 scope 而放弃 `repositories` 限定，代码照跑，最小权限没了 | 探针（`token-repo-scope`） |
| 仓库限定作用于**内容与写操作**（越界仓库 contents 403、开 issue 404），但**元数据仍可读**（`GET /repos/{o}/{r}` 200） | 文档写成"看不见别的仓库"，与实际不符 | 探针（`token-repo-isolation`、`token-metadata-leak`） |
| access token 名称、SSH key 标题**按用户唯一** | "先发后吊销"在发新的那一步 400，轮换保证反而没了 | 探针（`admin-ssh-key` 与轮换实测） |
| **`GET /admin/hooks` 恒返回空数组**，`GET /admin/hooks/{id}` 正常 | 每轮对账都以为 hook 不存在，重复注册 | 探针（`hook-list-empty`） |
| 服务端**展开事件族**：请求 8 个事件，存下 17 个 | 用事件列表比对会永远判定为漂移 | 探针（`hook-event-expansion`） |
| issue 表单 front matter 的 `labels` 是**新建页的预勾选**，由浏览器回传 `label_ids`，不是服务端在提交时应用 | 靠某个标签判断"这个 issue 是谁建的"，取消勾选或非浏览器提交就漏判 | live |
| 表单正文渲染在**服务端**：`### <字段 label>` + 空行 + 值，未填为 `_No response_`，checkboxes 为 `- [x] <label>`，dropdown 多选以 `, ` 连接 | 以为要在浏览器里才能拿到渲染结果，于是去做 UI 自动化 | live |
| Forgejo 15 的表单**没有 `_csrf`**，改用 SameSite cookie 加 Origin/Referer 校验 | 脚本化登录去找 CSRF token，找不到 | live |
| **Projects 看板没有 API**，是纯 UI 功能 | 设计出依赖看板列的自动化，做不出来 | 探针（`projects-absent`，v1/v2 均 404） |
| Projects 看板**也没有 webhook 事件** | 同上，且以为能靠事件补上 | `推断`：探针只测了 API，没有接收端测事件 |
| `admin auth add-oauth` **有** `--group-team-map` 与 `--group-team-map-removal`；组→team 的投影只发生在**登录时刻** | 以为目录里踢掉一个人就会到达 Forgejo，于是不做控制面侧的即时否决 | CLI（`forgejo-cli-probe-20260920`） |
| `admin auth add-ldap` **没有任何组选项**（`--help` 里匹配 `group` 的行数为 0），也没有可配置的不可变 ID 字段 | 设计出"经 LDAP 把组同步进来"的方案，做不出来 | CLI（`forgejo-cli-probe-20260920`） |
| `admin user` **没有 `prohibit_login` 子命令**；停用账号只能走管理端 API | 撤权流程写成一条 CLI 命令，落地时发现不存在 | CLI（`forgejo-cli-probe-20260920`） |

尚未复核，按未知对待：

- webhook 投递语义（超时、重试、能否手动重投）与 `issue_label` / `issue_assign` 的 payload 字段。
  两者都需要一个真实接收端，**在有结论之前按"至多一次"设计**：任何只有在保证重试的前提下才正确的
  逻辑都不能依赖；
- **`prohibit_login` 是否同时关闭 access token 与 Git over SSH**。撤权流程直接依赖这个答案，见
  [Forgejo Module 设计](/architecture/forgejo-module-design) §2.2 与计划的 `FJPROBE-T-001`；
- **OIDC `sub` 是否原样存进 `login_name`**、以及改名后的实际行为（计划的 `FJPROBE-T-003`/`T-004`）。
  按 `sub` 与目录对账的撤权路径依赖第一条；
- 认证源是否有 REST API，以及是否存在 IAM 主动 logout receiver 或按用户撤销会话/token 的管理端接口
  （[设计](/architecture/forgejo-module-design) §2.3 第 3、4 点）。两者都要管理员 token，
  `forgejo-cli-probe-20260920` 只覆盖了 CLI 一侧。

## 2. 由事实直接推出的规则

### 2.1 身份引导顺序是硬的

```text
建账号（POST /admin/users）
  → 授 collaborator（PUT /repos/{o}/{r}/collaborators/{u}）
  → 发 token（POST /users/{u}/tokens + Sudo 头）
```

Forgejo 在**发起账号自己的上下文里**解析 `repositories`，因此该账号还看不见的仓库会被报成
"不存在"而不是"无权限"。顺序不能调换。

### 2.2 轮换要带代次

先发新凭据、确认落库、再吊销旧的——这个顺序意味着新旧必然短暂并存，而名称按用户唯一，所以名字
里必须带代次。复用同一个名字会在第一步就 400，失败的轮换反而只剩一份凭据。

### 2.3 webhook 存在性用 id 判断，漂移只比对 URL 与密钥

列表端点不可信（恒空），事件列表也不可信（服务端会展开）。因此：存在性走
`GET /admin/hooks/{id}`，漂移判断只比对 URL 与密钥指纹。

### 2.4 签名先于一切

`X-Forgejo-Signature` 是 body 的 HMAC-SHA256，必须在解析 payload 的**任何**字段之前校验，并且
请求体大小上限也要在验签之前生效。投递头是 `X-Forgejo-Event` 与 `X-Forgejo-Delivery`。

### 2.5 机器可读状态不要放看板

Projects 既无 API 也无事件。状态一律落在 label、issue 开闭与指派上。上游在做 Project API，可用后
属于展示增强，**不改任何状态机**。

## 3. `forgejo` Module 的边界

这些是部署侧的约束，写代码时会撞上，完整设计见
[Forgejo Module 设计](/architecture/forgejo-module-design) §3、§5：

- **Actions 只有一个 ANAS 开关** `forgejo.actions_enabled`（默认 `false`）。开启的前置校验分两处：
  Hook 在渲染时校验获批 scope、控制面账号口令与固定 image fingerprint，一次性 preflight service 在
  Forgejo 启动前连接 Incus 验证 project/quota/profile。Incus endpoint 与证书不是 Forgejo 的配置项，
  由 compute contract 供给。任一处失败都不启动 Forgejo，因此不存在"只开了服务端"的半功能状态；
- **执行实例有两档隔离**，`forgejo.actions_isolation` 选择，`auto` 解析为 `incus_container`：非特权
  系统容器**与宿主共享内核**；`incus_vm` 是 QEMU/KVM，有独立 guest kernel 但要求宿主具备 KVM。
  默认档是容器档，因为目标硬件不保证有 KVM。**跨信任域或执行不受信输入的 scope 必须选 `incus_vm`**；
- 开关之外还有两层**授权**，它们不是开关：仓库 Units 里是否启用 Actions，以及 ANAS 实例管理员是否
  批准了该 `{owner}/{repo}` 或 `{owner}` 的 Runner scope。**不部署 global runner**；
- **能改 `.forgejo/workflows` 的人等价于能在对应 Runner 上执行代码**，所以一个 scope 只能覆盖写入者
  属于同一信任域的仓库，且每个执行实例只执行一个作业；容器档下这条是前提而不是建议；
- 两个高风险开关默认关闭且变更触发 `container_recreate`：`forgejo.custom_git_hooks_enabled`
  （等于允许服务器端任意代码执行）、`forgejo.local_path_import_enabled`。

## 4. 调一个没用过的端点之前

1. 先跑 `test-env/scripts/forgejo-agent-api-probe.sh`，不要凭上游文档下结论——这一页里过半的条目都
   与文档描述不符；
2. 把结论补进本页 §1，**连同「来源」列**（哪个探针用例或哪个测试给出的结论；没有实测就写 `推断`），
   并同步到发起改动那一侧的要求文档（`ai_agent` 在
   [要求 §15](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md)，
   `forgejo` 在[它自己的要求](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/dev-docs/requirements/forgejo-module.md)）；
3. 创建类接口 `201` 与 `200` 都要接受；
4. 想清楚失败路径：会不会把一个可恢复的冲突当成服务端故障，或者反过来。

**下一个人会依赖你的结论，不会重跑你的探针。**

## 5. 谁在用这份基线

| 使用方 | 它自己的规则 |
| --- | --- |
| `ai_agent` 编排器 | [编排器与 Forgejo 互操作的规则](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/forgejo-interop.md)、[编排设计](/architecture/ai-agent-orchestration-design) |
| `forgejo` Module | [Forgejo Module 设计](/architecture/forgejo-module-design) |

固定版本升级时，这一页是先改的那一份：两边的规则都建立在它上面。
