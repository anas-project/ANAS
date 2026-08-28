---
doc_type: requirement
status: current
created: 2026-08-28
updated: 2026-08-28
---

# 无序 Capability 依赖要求

本文规定 Module 如何声明「我需要这个 Provider 在部署里，但不需要它排在我前面」，以及这种依赖在解析、
Hook、渲染和启动各阶段的边界。实施进度见[无序 Capability 依赖实施计划](../plans/weak-capability-dependency.md)。
条件依赖（`enabled_by`）见[条件 Capability 依赖要求](conditional-capability-dependency.md)，
两者可以叠加。

本文的需求矩阵是验收规范来源，正文是解释。两者冲突以矩阵为准。

## 1. 问题：守门人依赖被守卫者

给 Adminer 挂 ForwardAuth 网关时出现一个无法解开的环：

```text
postgres --forward_auth--> oauth2_proxy --iam--> llng
         <--relational_database-------------------
```

网关需要 IAM，IAM 需要关系数据库，而那个数据库正是要被守卫的控制台所属的 Module。实测：

| 拓扑 | 结果 |
| --- | --- |
| 单数据库，IAM 用它 | `dependency cycle at <db>`，无法解析 |
| 两个数据库引擎，开 IAM 未使用的那个的 Adminer | 解析通过 |

面向家用与小型公司的部署几乎总是第一种。这不是某一次接线接错了，是「A 守卫 B，而 A 自己要靠 B 才能
跑起来」这一类关系的固有形状。同类关系还会出现在监控、日志和证书上——任何**为其他服务提供横切能力、
自己又消费基础设施**的 Provider 都可能撞上它。

现有的两种依赖都解决不了：强依赖产生排序边因而成环；`dependencies.requires[].optional` 不产生边，
但它同时放弃了存在性，Provider 不在部署里也照样通过——那不是「顺序无所谓」，是「有没有都行」。

## 2. 语义：存在性强制，顺序不强制

这是依赖矩阵里缺的第三格：

| | 存在性 | 排序边 |
| --- | --- | --- |
| `dependencies.requires[].optional` | 不强制 | 无 |
| 现有 `requires_capabilities` | 强制 | 有 |
| **本文** | **强制** | **无** |

Provider 缺失、被禁用、或没有兼容 interface 时，照常在 `plan` 阶段失败。绑定照常记录、照常进 lock。
**唯一放弃的是「Provider 必须排在 Consumer 之前」这一条。**

字段拼写必须说清放弃的是什么，不能说成「弱」。`weak: true` 会被读成「这条依赖可有可无」，那正是要
避免的误解。

## 3. 为什么放弃顺序是安全的：Runner 本来就是两趟

`materializeDeployment` 分两个阶段，中间没有交叉：

- **第一趟 `a.calculate()`**（`internal/runner/deployment.go:507`）按顺序跑完**所有**模块的 calculate
  Hook，每个 Hook 的输出合并进共享的 `a.env`；
- **第二趟 `a.renderAll()`**（`:520`）再走一遍做渲染，读的是**已经填满的** `a.env`。
  `a.scopedEnv(name)`（`internal/runner/runner.go:1201`）只按 `consumes` 和前缀做可见性过滤，
  不含任何时序逻辑。

所以**渲染期读 Provider 的值不受顺序影响**：无论 Consumer 排第几，渲染发生时所有 calculate 都已结束。
Adminer 的 `traefik.http.routers.adminer.middlewares=${ANAS_FORWARD_AUTH_MIDDLEWARE}` 正是渲染期
读取，因此不受影响。

**容器启动先后也无所谓**，至少对 ForwardAuth 这个形状：Traefik 解析不到被引用的中间件时**禁用该
router 并返回 404**，不是放行（`modules/oauth2_proxy/hook/main.go:126-130` 记录了这一观察）。启动
窗口期是 fail-closed。

## 4. 唯一真正的约束：calculate 阶段

放弃排序边只放弃一件事——**Consumer 的 calculate Hook 可能先于 Provider 的 calculate Hook 运行**。
因此 Consumer 不得在自己的 calculate Hook 里读取 Provider 的 Hook 输出。

这条不能只靠评审。calculate 是特权阶段，拿到的是完整 `a.env` 而不是 scoped 视图
（`internal/runner/hook.go:223`），所以一个读了不该读的值的 Hook **在某些拓扑下会碰巧成功**——作者
本地测通、换个部署就坏。

修法是让它**确定性地读不到**：无序依赖的 Provider 所发布的键，必须从 Consumer 的 calculate Hook 环境
中排除。把一个竞态变成一个稳定的缺失，错误就会在第一次运行时暴露，而不是在别人的机器上。

## 5. 两处必须拒绝，一处必须显式声明

**Contract 依赖与 `resources.requires` 不得声明无序。** Resource 是 Provider 的 Hook 真实创建出来的
持久对象（数据库、bucket、凭据），Consumer 的 Hook 要读它的连接信息——这正是 §4 禁止的那种读取。
对它们来说顺序不是可以放弃的优化，是语义的一部分。

Capability 之所以可以，是因为它默认只建立依赖、选择 interface 并记录绑定
（[Capability 开发标准](../../docs/developer/capability-development.md) §1 硬边界第 5 条），不执行任何持久对象的
生命周期。

**注册了 output ABI 的 Capability 也不得声明无序**（实施 M1 时发现的）。`publishCapabilityOutputs`
在 Provider 的 calculate Hook 之后才把这些键投影到 Consumer 的绑定命名空间，而**投影出来的键归
Consumer 所有**——所以 §4 那条按 owner 过滤的规则看不见它们，无序之后它们会变成时有时无。与其造一套
推测性的机制，不如在加载期拒绝这个组合。`forward_auth` 没有注册 Outputs，不受影响。

**无序 Consumer 必须显式声明 `consumes`**（同样是 M1 发现的）。去掉排序边的副作用之一是 Provider 不再
属于 Consumer 的依赖闭包，于是 `envScopeFor` 的闭包与前缀可见性都不再覆盖它，`consumes` 是唯一剩下的
通路。这不算缺陷——显式优于隐式——但必须写进文档，否则作者会遇到一个渲染期为空、原因却在依赖声明里的
变量。

## 6. 被否决的方案

- **Runner 自动断开成环的边。** 会让「这个部署为什么能起来」取决于图的形状而不是任何人的声明，
  而且断哪条边是任意的。成环必须报错，除非作者显式说明这条边不需要顺序。
- **只放宽容器启动顺序，保留 calculate 顺序。** 环在解析图上，而解析图同时驱动两者；要拆开就得维护
  两张图。收益不足以支撑那个复杂度，而 §3 已经说明渲染期本来就不受影响。
- **给 Consumer 加「重试直到 Provider 就绪」。** 把静态的拓扑问题变成运行期的时序问题，更难诊断。
- **复用 `dependencies.requires[].optional`。** 它放弃的是存在性，不是顺序，见 §2。

## 7. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `WEAKDEP-R-001` | `requires_capabilities` 的条目必须能声明「不需要排序」，且该字段的拼写必须表达放弃的是顺序而非存在性 | 审阅 |
| `WEAKDEP-R-002` | 声明无序后，Provider 缺失、被禁用或无兼容 interface 时仍必须在 `plan` 阶段失败 | 单元 |
| `WEAKDEP-R-003` | 声明无序后，该依赖不得向拓扑排序贡献边，因而不得参与成环判定 | 单元 |
| `WEAKDEP-R-004` | 声明无序后，capability binding 必须与有序依赖一样记录并写入 lock | 单元 |
| `WEAKDEP-R-005` | 字段缺省即有序；既有声明的行为不得改变 | 单元 |
| `WEAKDEP-R-006` | 无序依赖的 Provider 所发布的键必须从 Consumer 的 calculate Hook 环境中排除 | 单元 |
| `WEAKDEP-R-007` | 该排除不得影响 Consumer 的渲染环境，渲染期读取 Provider 的值必须照常可用 | 单元 |
| `WEAKDEP-R-008` | Contract 依赖与 `resources.requires` 不得声明无序；声明时 Manifest 加载必须失败 | 单元 |
| `WEAKDEP-R-009` | 无序依赖必须可以与条件依赖（`enabled_by`）叠加，两者语义互不影响 | 单元 |
| `WEAKDEP-R-010` | Manifest 文档必须写明无序依赖与 `dependencies.requires[].optional` 的区别，并给出各自放弃了什么 | 审阅 |
| `WEAKDEP-R-011` | `postgres` 与 `mariadb` 以无序条件依赖声明 `forward_auth` 后，单数据库且 IAM 使用该数据库的拓扑必须解析成功 | 单元 |
| `WEAKDEP-R-012` | 该拓扑下 `adminer_enabled=true` 渲染出的 Adminer 路由必须带非空的 ForwardAuth 中间件标签 | e2e |
| `WEAKDEP-R-013` | 该拓扑下 `adminer_enabled=false` 的部署不得引入 `oauth2_proxy` | 单元 |
| `WEAKDEP-R-014` | 注册了 output ABI 的 Capability 不得声明无序；声明时 Manifest 加载必须失败并说明原因 | 单元 |
| `WEAKDEP-R-015` | 无序 Consumer 必须显式声明 `consumes` 才能读取 Provider 的键；Manifest 文档必须写明闭包与前缀可见性不再覆盖该 Provider | 审阅 |
