---
doc_type: requirement
status: current
created: 2026-08-28
updated: 2026-08-28
---

# 条件 Capability 依赖要求

本文规定 Module 如何声明「只有某个配置开关打开时才需要某个 Capability」，以及这种依赖在解析、排序、
锁和 `plan` 输出中的行为。实施进度见[条件 Capability 依赖实施计划](/plans/conditional-capability-dependency)。
Capability 的通用规则见[Capability 开发标准](/developer/capability-development)。

本文的需求矩阵是验收规范来源，正文是解释。两者冲突以矩阵为准。

## 1. 问题：可选服务的依赖无法表达

`dependencies.requires_capabilities` 是无条件的。一个 Module 的**可选**服务需要某个 Capability 时，
作者只有两个都不可接受的选择：

- **无条件声明**——`postgres` 声明需要 `forward_auth`，于是**每一个 postgres 部署都被迫拉起
  oauth2_proxy**，哪怕 `adminer_enabled` 是默认的 `false`。为一个默认关闭的调试界面给所有部署加一个
  身份网关，代价和收益完全不成比例。
- **完全不声明**——现状。开启 `postgres.adminer_enabled` 后，Adminer 路由直接暴露在公网上，没有任何
  东西提醒作者或运维它本该被网关挡住。

第一个真实用例是 Adminer：`postgres` 与 `mariadb` 的 Adminer 只在 `adminer_enabled=true` 时存在，
而它一旦存在就必须挂在 ForwardAuth 网关后面（Adminer 的登录表单允许填任意目标主机，公网可达的
Adminer 是进入内部 Docker 网络的跳板）。

## 2. 不能复用 `dependencies.requires[].optional`

仓库里已经有一个叫 `optional` 的依赖修饰符，**它的语义与本文要的相反，不能改写它的含义**：

- `resolveOrder` 在构图时把 `optional` 依赖**整个跳过**（`internal/runner/runner.go:822` 的
  `if !dep.Optional`），因此它**不产生排序边**；
- `validateVersions` 只在该 Module 恰好也被部署时才检查版本（`internal/runner/versions.go:177` 的
  `if dep.Optional && !contains(a.order, dep.Name) { continue }`）。

那是「**碰巧在场就顺带检查**」，不是「**条件成立就必须在场且必须排在前面**」。两者混用会让一条
本应阻塞部署的依赖静默降级成软依赖。条件依赖必须是新的表达，且与既有 `optional` 在同一个 Module
上不得同时作用于同一目标。

## 3. 条件成立后与无条件依赖完全等价

条件只决定这条依赖**是否存在**，不改变它存在之后的任何语义。条件成立时：

- 照常进入拓扑排序，Provider 必须排在 Consumer 之前；
- Provider 缺失、有多个候选而无法自动选择、或 interface 不匹配时，照常在 `plan` 阶段失败；
- 照常写入 lock 的 `bindings`，照常参与后续的锁一致性校验。

**不存在「弱条件依赖」这种中间态。** 一个只在运气好时才被满足的安全网关不是安全网关。

## 4. 条件必须在解析依赖顺序之前可判定

这是本特性最硬的技术约束，来自现有的执行顺序，不是设计偏好：

`resolveOrder` 跑在 `applyModuleDefaults` **之前**——`materializeDeployment` 是
`internal/runner/deployment.go:450` 然后 `:454`，配置变更规划路径是
`internal/runner/config_validate.go:387` 然后 `:392`。而 `applyModuleDefaults` 本身只遍历
`a.order`（`internal/runner/runner.go:1051`），也就是说**它必须先有顺序才能填默认值**。

因此在判定条件的那一刻，`a.env` 里**只有用户显式配置过的值，没有 Module 声明的默认值**。一个读
`a.env["POSTGRES_ADMINER_ENABLED"]` 的条件求值器，会在用户没写这个参数时读到空字符串，而不是读到
`module.yml` 里的 `false`。

由此推出三条：

- 条件求值必须自己回落到 `a.reg[<module>].Defaults`，不能依赖 `applyModuleDefaults` 已经跑过；
- 条件只能引用**本 Module 自己的静态配置参数**。Hook 产物、其他 Module 的导出、渲染期变量在这一刻
  都还不存在；
- 条件参数必须是布尔类型。「非空即真」这种判定会让一个拼错的参数名静默变成 false，而拼错的参数名
  在这里意味着一道安全网关静默消失。

## 5. 与既有两个「可选」机制的关系

仓库里已经有两处表达「这个东西只在某个开关打开时才存在」，本特性是第三处。**三处不能各写各的。**

| 机制 | 位置 | 引用什么 | 求值时机 |
| --- | --- | --- | --- |
| `services.optional[].enabled_by` | `module.yml` | 本 Module 的配置参数名（lower snake_case） | 声明式，Hook 用 `disable_services` 执行 |
| launcher `enabled_if` | 应用目录 schema（尚未实现） | 一个变量，布尔或非空判定 | 渲染期，变量可能来自 Hook |
| 条件 Capability 依赖 | `module.yml` | 本文规定 | 解析依赖顺序时，早于一切 Hook |

本特性**复用 `enabled_by` 的拼写和取值含义**：同样指向本 Module 的一个布尔配置参数，同样用
lower snake_case。理由是它与本特性的粒度和求值时机一致，而且 `module_manifest_test.go` 已经在为它
做命名 lint。

launcher 的 `enabled_if` **不是同一件事**：它引用的是变量而不是参数，求值时机在渲染期，可以依赖
Hook 的输出。它不能作为依赖条件，因为依赖顺序必须在 Hook 运行之前就确定。这个区别要写进 Manifest
文档，否则两个字段迟早会被当成一件事的两种写法而互相污染。

## 6. 条件翻转会让锁陈旧，必须说清楚

开启 `postgres.adminer_enabled` 会把 Provider Module 加进部署集合，并新增一条 capability binding。
两者都进 lock（`moduleLock.Modules` 与 `moduleLock.Bindings`，`internal/runner/versions.go:41-50`），
所以翻转这个开关会让既有 lock 陈旧。

这对用户是可见的行为变化：一个原本只需要重建容器的参数，现在还要求更新锁。

**本文初稿要求给 `changes.effect` 新增取值来表达这件事，那是错的，实施时读代码后已修正。**
`effect` 描述的是**用什么动作应用这次变更**，不是这次变更波及多大范围：`effectExecutor`
（`internal/runner/config_cli.go:346`）把 effect 映射成执行器，`container_recreate` 落到
`deployment_apply_fallback`，也就是渲染并激活一个新的不可变部署——对 `adminer_enabled` 而言这个动作
本来就是对的。缺的不是 effect，是**锁**：没有 `--update-lock` 时解析会因为模块集合变化而失败。

所以要求落在失败信息上，不落在 effect 词汇表上：翻转条件后的失败必须说清**这个模块为什么会出现在
部署里**，并给出可执行的下一步。只说「lock stale」时，运维打开的是一个数据库控制台，被告知锁里缺一个
身份网关——那读起来像 bug，而不像一条指示。

## 7. `plan` 必须说明依赖为什么存在

这是本特性面向用户的主要价值。没有它，用户看到的现象是「我打开了一个数据库管理界面，部署里凭空
多了一个身份网关」。`plan` 必须能回答「这条依赖是哪个参数带出来的」，且这个答案要在
`capability_bindings` 已有的结构里表达（`internal/runner/deployment.go:185`），不新造一个平行的
输出通道。

条件不成立时**不得**在输出里留下一条空绑定或占位符——那会让「没有网关」和「有网关但没绑定成功」
在机器可读输出里长得一样。

## 8. Adminer 曾被一个结构性的环挡住（已解除）

**实施 M2 时发现的，不是设计时预见到的。** `postgres`/`mariadb` 按本文声明条件依赖后，
`adminer_enabled=true` 会产生依赖环：

```text
postgres --forward_auth（adminer_enabled）--> oauth2_proxy --iam--> llng
         <--relational_database---------------------------------------
```

`oauth2_proxy` 需要 IAM，IAM（`llng`）需要一个关系数据库，而那个数据库正是要被守卫的
Adminer 所属的 Module。实测结果：

| 拓扑 | 结果 |
| --- | --- |
| 单数据库（部署里只有 postgres 或只有 mariadb），IAM 用它 | **`dependency cycle at <db>`，无法解析** |
| 两个数据库引擎，IAM 用其中一个，开另一个的 Adminer | 解析通过，顺序为 `… mariadb llng oauth2_proxy postgres` |

**面向家用与小型公司的部署几乎总是第一种。** 也就是说，用 IAM 网关守卫数据库控制台在实际拓扑下
基本不可行——不是实现缺陷，是「守门人依赖被守卫者」这件事本身的循环。

这个环不是条件依赖机制引入的：无条件声明会让**每个**部署都撞上它，条件依赖只是把它推迟到开启
Adminer 的那一刻。条件机制本身（M0、M1）不受影响，它正确地表达并暴露了这个环。

当时在三条路之间做过决定，**最终走了 C**，环已解除，R-018 与 R-019 正常验收。三条路的记录保留在此，
因为被否决的理由和被选中的理由一样值得留存：

**A. 去掉 Adminer 的公网路由。** 调试工具改由 `docker compose exec` 或 SSH 端口转发访问。没有环，
也不需要网关。代价是运维少一个浏览器入口。

**B. 为基础设施控制台使用一道不依赖 IAM 的门**（例如 Traefik basicauth）。环消失，代价是第二套凭据，
与单一身份源的姿态冲突。

**C（已采纳）。引入无序依赖：要求 Provider 在部署里，但不要求它排在前面。** 这会直接消掉环——去掉这条边之后
图变成 `llng -> postgres`、`oauth2_proxy -> llng`，拓扑序是 `postgres llng oauth2_proxy`。它与既有的
`dependencies.requires[].optional` 仍然不同：**存在性依旧是强制的**，缺 Provider 照样在 plan 阶段失败，
去掉的只有排序边。

C 在运行期和渲染期都是安全的，**因为 Runner 已经是两趟**：

- **第一趟 `a.calculate()`**（`internal/runner/deployment.go:507`）按顺序跑完**所有**模块的 calculate
  Hook，每个 Hook 的输出合并进共享的 `a.env`；
- **第二趟 `a.renderAll()`**（`:520`）再走一遍做渲染，读的是**已经填满的** `a.env`
  （`internal/runner/runner.go:1201` 的 `a.scopedEnv(name)` 只按 `consumes` 和前缀做可见性过滤，
  不含任何时序逻辑）。

所以 postgres 即使排在 oauth2_proxy 前面，渲染时 `ANAS_FORWARD_AUTH_MIDDLEWARE` 也已经有值——那时
provider 的 calculate 早就跑完了。**本文早前版本推演过的「空变量导致 `middlewares=` 裸奔」不成立**，
它把 calculate 的顺序误当成了渲染的顺序。

容器启动先后同样无关：Traefik 解析不到被引用的中间件时**禁用该 router 并返回 404**，不是放行
（`modules/oauth2_proxy/hook/main.go:126-130` 记录了这一观察）。

因此 C 唯一真正放弃的是 **calculate 阶段的先后**，约束也就只有一条：**弱依赖的 Consumer 不得在自己的
calculate Hook 里读取 Provider 的 Hook 输出；渲染期读取不受限制。** Adminer 完全落在允许范围内——
postgres 的 Hook 不碰这个变量，只有 Compose label 在渲染期用它。

这也意味着 C 不需要把中间件名升格成 Capability 契约级的静态事实（本文早前版本认为需要）。详细约束
另立文档。

## 9. 被否决的方案

- **给 `requires_capabilities` 加 request schema，让 Consumer 声明它需要的准入策略。** 这是把
  Capability 改造成 Contract。[Capability 开发标准](/developer/capability-development) §1 的 ABI 硬
  边界第 4 条已经排除了这条路，而准入策略应当由角色绑定派生，不由每个 Consumer 各自声明。
- **让条件支持任意表达式（比较、与或、跨 Module 引用）。** 条件在依赖解析这一刻求值，那时几乎没有
  数据可用（见 §4）。表达式能力只会制造出在这一刻求不出值的写法。
- **条件不成立时把依赖降级为软依赖而不是移除。** 见 §3。
- **复用 `dependencies.requires[].optional`。** 见 §2。

## 10. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `CONDDEP-R-001` | `requires_capabilities` 的条目必须支持声明一个条件，条件不成立时该条目不产生任何依赖、绑定或校验 | 单元 |
| `CONDDEP-R-002` | 条件成立时，该 Capability 依赖必须与无条件声明完全等价：进入拓扑排序、Provider 排在 Consumer 之前 | 单元 |
| `CONDDEP-R-003` | 条件成立而 Provider 缺失、无法自动选择或 interface 不匹配时，必须在 `plan` 阶段失败，不得降级为软依赖 | 单元 |
| `CONDDEP-R-004` | 条件必须引用本 Module 自己的一个布尔配置参数；引用其他 Module 的参数、变量或非布尔参数时，Manifest 加载必须失败 | 单元 |
| `CONDDEP-R-005` | 条件引用的参数名在本 Module 的 `config.types` 中不存在时，Manifest 加载必须失败 | 单元 |
| `CONDDEP-R-006` | 用户未显式配置该参数时，条件必须按 Module 声明的默认值求值，不得因 `applyModuleDefaults` 尚未执行而读到空值 | 单元 |
| `CONDDEP-R-007` | 条件求值不得读取 Hook 产物、其他 Module 的导出或渲染期变量 | 审阅 |
| `CONDDEP-R-008` | 条件字段必须复用 `services.optional[].enabled_by` 的拼写与取值含义（本 Module 的 lower snake_case 布尔参数名） | 单元 |
| `CONDDEP-R-009` | 同一条 `requires_capabilities` 条目不得同时声明条件与 `dependencies.requires[].optional` 语义；两者的区别必须写入 Manifest 文档 | 审阅 |
| `CONDDEP-R-010` | Manifest 文档必须写明条件 Capability 依赖与 launcher `enabled_if` 是两件事，并给出各自的求值时机 | 审阅 |
| `CONDDEP-R-011` | 条件成立时产生的 capability binding 必须与无条件依赖一样写入 lock 的 `bindings` | 单元 |
| `CONDDEP-R-012` | 条件不成立时不得在 `plan` 输出或 lock 中留下该 Capability 的空绑定或占位符 | 契约 |
| `CONDDEP-R-013` | 翻转条件参数导致部署集合变化时，锁校验失败必须指出该模块由哪个参数带入，并给出可执行的下一步，不得只报锁陈旧 | 契约 |
| `CONDDEP-R-014` | 条件参数不得为此新增 `changes.effect` 取值；effect 描述应用动作而非波及范围，锁的变化由失败信息与 `--update-lock` 承担 | 审阅 |
| `CONDDEP-R-015` | `plan` 的 `capability_bindings` 必须标明该绑定由哪个参数带出，不得新增平行的输出通道 | 契约 |
| `CONDDEP-R-016` | 条件 Capability 依赖必须在 `registryOnlyResolution` 路径（配置变更规划）中与部署路径行为一致 | 单元 |
| `CONDDEP-R-017` | 现有的无条件 `requires_capabilities` 声明必须继续按无条件解析，条件字段缺省即无条件 | 单元 |
| `CONDDEP-R-018` | `postgres` 与 `mariadb` 必须声明「`adminer_enabled=true` 时需要 `forward_auth`」，且 `adminer_enabled=false` 的部署不得因此引入 Provider | e2e |
| `CONDDEP-R-019` | `adminer_enabled=true` 渲染出的 Adminer 路由必须带 ForwardAuth 中间件标签 | e2e |
