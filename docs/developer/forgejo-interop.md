# 与 Forgejo 互操作基线

> 状态：**17.0.1 Module 探针、16.0.5 AI Agent 设计与 15.0.7 历史实测分列**。§1.1 是本轮 Module 实测，不能替代 AI Agent 全套验收。
> §1—§2 的历史事实由 `test-env/scripts/forgejo-agent-api-probe.sh` 对固定
> `codeberg.org/forgejo/forgejo:15.0.7-rootless`（`15.0.7+gitea-1.22.0`）实测得出，逐条结论与通过
> 数记录在 [`ai_agent` 要求 §15](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/dev-docs/requirements/ai-agent.md)。
> 表单相关的几条来自 `TestFormRenderingAgainstLiveForgejo`（同一固定镜像）而不是探针脚本；标 `CLI`
> 的几条来自 2026-09-20 对同一镜像跑 `--help` 的实测，报告在
> `test-env/reports/forgejo-cli-probe-20260920.md`（报告目录不入库）。三者都是实测，但要重跑的是不同
> 的东西。**探针里被 SKIP 的用例不算事实**，仍未有结论的列在 §1 末尾。
> 升级固定版本后必须重跑探针，才能补记新版实测结论，并同时执行
> [Forgejo Module 设计](/architecture/forgejo-module-design) §2.3 的身份复核（`FORGEJO-R-066`）：LDAP 与
> OIDC 能否按不可变 ID 同时接入，只能由探针回答，不能凭 changelog。文档更新：2026-10-30；历史实测：2026-09-20。

ANAS 里有两块代码跟 Forgejo 说话：[`forgejo` Module](/architecture/forgejo-module-design) 自己，
以及 `ai_agent` 的编排器。这一页是它们**共用的底座**——上游在固定版本上到底怎么表现，以及由此
直接推出的 API 规则。写任何 Forgejo 客户端代码之前先读它。

各自的内部纪律不在这里：编排器的 outbox、评论纪律与授权判定见
[编排器与 Forgejo 互操作的规则](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/forgejo-interop.md)。

## 0. Forgejo 16.0.5 的新设计基线（2026-10-12）

AI Agent 本节设计/验收基线为 **16.0.5**。Module manifest 当前为 17.0.1；以下
§1—§2 的探针事实与旧客户端规则保留其原版本归属，尤其不能把“管理员 token 端点不存在”继续用于新版。

| 调用面 | 16.0.5 规则与证据边界 |
| --- | --- |
| 管理用户 token | `GET/POST /admin/users/{u}/tokens`、`DELETE .../{token}`；管理员 API token 可调用，不要求 basic auth + Sudo；路由/handler 已核验，真实最小 scope 未验收 |
| 仓库限定 | token payload 保留 repositories 对象数组与 scopes；需要重做真实跨仓库访问和 scope 组合测试 |
| 系统 hook | 固定 handler 提供分页列表，不把旧版“恒空”现象推广为新版事实；已有 hook id 可继续用于精确查证 |
| 文件发布 | contents 新增 force_overwrite_new_branch；本设计不启用，冲突用最新 SHA 和隔离工作区处理，不用强推覆盖 |
| 标签 / 评论 | 保留 color 和 page/limit；语义颜色规范化，重建上下文必须读取全部评论页 |
| 真实验收 | 旧版 token、表单、hook、OIDC 实测不自动覆盖 16.0.5；后续在选定镜像重跑 |

固定来源与摘要见[16.0.5 API 兼容性研究](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/research/forgejo-api-compatibility.md)。
源码检查只证明对应实现/定义存在，未将历史失败假设改成新版实机通过。

## 1. 历史实测事实（15.0.7）

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

### 1.1 17.0.1 Module 实测（2026-10-30）

finance 的独立 Docker/netns、PostgreSQL 和测试 OIDC Provider 上执行
`server-forgejo-postgres-upgrade-e2e.py`、`server-forgejo-oidc-e2e.py`（含 `--session-only`）。
证据、镜像摘要与限制见 [Forgejo 配对计划 §14](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/dev-docs/plans/forgejo-module.md)。

| 检查 | 17.0.1 结果与边界 |
| --- | --- |
| OIDC group→team | 真实登录加入/移除映射 team，管理员降权，保留手工 team；产品 helper 的新增、更新、清空通过。映射移除不是目录实时同步 |
| 身份和改名 | `login_name` 逐字节等于稳定 `sub`；用户名/邮箱声明改变后仍为同一用户 ID、旧用户名、旧邮箱和原仓库。真实 LLNG/Casdoor 目录改名仍未验收 |
| 最小停用请求 | 管理员 `PATCH /admin/users/{u}` 只带 `prohibit_login` 被接受 |
| 停用的实际范围 | 已有 Web 会话显示 HTTP 200 的 **Account is suspended** 页面，私有文件不可读；API token 为 403，Git HTTP/SSH push 均失败。不是 session/凭据删除证明 |
| 管理员凭据 API | `/admin/users/{u}/tokens` 创建和删除已实测，管理员 SSH key 创建/删除通过；15.0.7 的 token 端点不存在结论不能用于新版 |
| LDAP / OIDC CLI | LDAP 仍无组同步或独立不可变 ID 字段；OAuth CLI 仍有 group-team-map/removal，无按自定义 claim 绑定既有 LDAP 用户的入口 |
| 认证源、会话、登出 | 固定实例 Swagger 无对应 admin auth/session/logout 路由，探测四个候选端点均为 404；v17.0.1 Web 路由仍只有本地登出，未发现标准 IAM logout receiver。不能据此宣称 cookie 已撤销 |
| 管理员改名 | 实例 `EditUserOption` 没有 `new_name` 字段；不宣称管理端 API 可自动重命名 |
| PostgreSQL 升级与恢复 | 15.0.7→17.0.1、重建测试数据库并恢复旧数据/配置→15.0.7、再次升级均通过 Git HTTP/SSH、LFS 和包数据核对 |

这些结果不自动覆盖 §1 的全部 AI Agent 探针、真实 IAM Provider、ARM64、完整 ANAS apply、
凭据轮换或 Actions guest。单链路结论保持；永久撤权仍显式删除凭据，不能把停用期阻断当成永久吊销。

后续在正式 Dockerfile 构建的 amd64 镜像上，普通 ANAS apply 和真实 Samba AD + Casdoor/LLNG
浏览器矩阵均通过：包括 group→team 加入/登录移除、管理员降权、独立准入拒绝、手工 team 保留、
IAM 停机拒绝新会话及恢复保持用户 ID。此矩阵没有执行真实目录改名，不能补记上表的改名边界。
实际制品、源码和私有证据摘要见 [Forgejo 配对计划 §16](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/dev-docs/plans/forgejo-module.md)。

## 2. 旧客户端规则（15.0.7）

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
