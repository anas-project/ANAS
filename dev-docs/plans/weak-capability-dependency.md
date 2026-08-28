---
doc_type: plan
status: implementing
created: 2026-08-28
updated: 2026-08-28
---

# 无序 Capability 依赖实施计划

验收依据是[无序 Capability 依赖要求](../requirements/weak-capability-dependency.md)的需求矩阵。没有独立
架构文档——它扩展的是既有 Capability 解析路径，不引入新的运行时边界。

M0、M1、M2 全部完成，只剩 R-012 的 e2e 待在真实主机上执行。
[条件 Capability 依赖](conditional-capability-dependency.md)的 M2 阻塞已解除。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Manifest 字段与解析器 | R-001—R-005、R-008—R-009 | 已完成 |
| M1：calculate 环境隔离 | R-006—R-007、R-010、R-014—R-015 | 已完成 |
| M2：解除 Adminer 阻塞 | R-011—R-013 | 已完成 |

## 2. M0 检查表

- [x] 字段定为 `ordering`，取值 `before`（缺省）/ `any`（R-001）。没有用 `weak: true`——那会被读成
      「这条依赖可有可无」，而存在性恰恰仍是强制的。
- [x] `manifestRequiredCapability`（`internal/runner/manifest.go`）与 `RequiredCapability`
      （`internal/runner/modules.go`）增加该字段；`normalizeCapabilityOrdering` 把取值收在闭集里，
      缺省归一为 `before`，未知取值加载失败。
- [x] `resolveOrder` 的 `RequiresCapabilities` 循环在无序时仍然完整解析 Provider、interface 与绑定
      （R-002—R-004），只是不把 provider 追加进 `deps`。
- [x] **presence 必须单独补上**——这一步是实现时才发现的：在这个 resolver 里，模块是**靠被边访问到**
      才进入部署集合的，去掉边会连要求一起去掉（第一版测试直接暴露了 `order = [db]`，网关根本不在）。
      解法是把无序 Provider 收进一个待办集合，在有序遍历**结束之后**再 `visit`：那时命名它的模块都已
      `seen`，回指的路径立刻终止，不会重新进入进行中的访问。循环重跑是因为无序 Provider 自己也可能
      有无序依赖。
- [x] 缺省即有序（R-005）。
- [x] Contract 依赖与 `resources.requires` 出现该字段时加载失败（R-008）。**由 `KnownFields(true)`
      结构性保证**（`internal/runner/manifest.go:818`），字段只加在 capability 结构上，测试把它锁住。
- [x] 与 `enabled_by` 叠加（R-009）：条件不成立时整个条目消失，条件成立时按无序处理。
- [x] `internal/runner/capability_ordering_test.go`：有序拓扑成环、无序拓扑解析成功且 Provider 仍在
      order 里且排在 Consumer 之后、无 Provider 仍失败、绑定与 lock 记录一致、缺省为 `before`、
      四种非法取值、Contract/Resource 上被拒、与条件依赖叠加的开关两态。
- [x] 通过 `go test ./...`、`go vet ./...`。

**变异验证**：不把 provider 记进 `unordered`（只丢边）→ 两个测试失败；记了但仍然 `append` 进 `deps`
→ 三个测试失败，其中一个直接报 `dependency cycle at db`。presence 与 ordering 两半各自被独立约束。

**字段暂不写进开发者文档**，那是 M1 的 R-010：在 calculate 环境隔离落地之前就把用法公开，等于邀请
别人踩 M1 要消除的那条竞态。

## 3. M1 检查表

- [x] `calculateEnvFor`（`internal/runner/capability.go`）按 **owner** 过滤，不按前缀猜：
      `applyCalculatePatch` 已经把 Hook 写的每个键记在模块名下，无论它走的是自己的前缀还是
      `config.exports`。前缀匹配会漏掉后者。
- [x] `a.calculate()` 传 `a.calculateEnvFor(name)` 而不是 `a.env`（R-006）。没有无序依赖的模块
      原样返回 `a.env`，不多一次拷贝。
- [x] 渲染环境不受影响（R-007）。
- [x] 单元测试：同一个键在 Consumer 的 calculate 里不可见、在渲染环境里可见；Provider 自己仍看得到
      自己的键；有序依赖保持完整的特权视图。
- [x] **注册了 output ABI 的 Capability 拒绝无序**（R-014）。这是 M1 才发现的洞：
      `publishCapabilityOutputs` 投影出来的键**归 Consumer 所有**，owner 过滤看不见它们，无序之后会
      变成时有时无。与其造推测性机制，不如在加载期拒绝。`forward_auth` 没有 Outputs，不受影响。
- [x] **无序 Consumer 必须显式声明 `consumes`**（R-015）。也是 M1 发现的：去掉排序边会把 Provider
      移出 Consumer 的依赖闭包，`envScopeFor` 的闭包与前缀可见性都不再覆盖它。第一版测试的 fixture
      漏了 `consumes`，渲染期读到空值——**是 fixture 错了，不是代码**，但这条必须进文档。
- [x] `docs/developer/capability-development.md` §6.2：三机制对照表、两条使用约束、两处拒绝
      （R-010）。
- [x] 通过 `go test ./...`、`go vet ./...`。

**变异验证**：取消过滤 → 两个测试失败；去掉 Outputs 守卫 → 一个测试失败。

## 4. M2 检查表

- [x] `postgres` 与 `mariadb` 的 `forward_auth` 依赖同时声明 `enabled_by: adminer_enabled` 与
      `ordering: any`，并在 `config.consumes` 里声明 `ANAS_FORWARD_AUTH_MIDDLEWARE`。
- [x] 接线重做：Compose 中间件标签、`forward_auth_interface` 参数与 `changes`、八张参数表、
      四个计数测试（170→172、153→155、131→133）。
- [x] `TestBundledAdminerResolvesOnASingleDatabase` 对真实 manifest 固定住回归：开时解析成功、
      `oauth2_proxy` 与 `llng` 都在 order 里、绑定记录完整、且 postgres 的 calculate 看不到网关的键；
      关时不引入 Provider（R-011、R-013）。
- [x] 条件依赖计划的 M2 改为完成，R-018/R-019 的暂缓标记已去掉。
- [x] **实际 render 产物验证通过**（不需要 Docker daemon，`docker compose config` 只做解析与插值）：
      在原先成环的拓扑（单 postgres + llng + `adminer_enabled=true`）上 `anas render` 成功，模块集合为
      `lego llng oauth2_proxy postgres samba_dc traefik`；`modules/postgres/.env` 里
      `ANAS_FORWARD_AUTH_MIDDLEWARE=anas-forward-auth@docker`（**非空**，这是 §3 两趟渲染的实证）；
      `docker compose config --quiet` 退出 0，标签插值为
      `traefik.http.routers.adminer.middlewares: anas-forward-auth@docker`。
      `adminer_enabled=false` 时模块集合缩为 `lego postgres traefik`，没有 `oauth2_proxy`。

## 5. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-012 | 待补 `test-env/scripts/server-adminer-forward-auth-e2e.sh` | 真实 Docker + oauth2_proxy + IAM，单数据库拓扑 | — | 待执行 |

这条 e2e 要验的不是「中间件标签渲染出来了」——那是单元能判的。要验的是**未登录的请求确实被网关拦下**，
以及**容器启动顺序颠倒时不会出现放行窗口**。

## 6. 当前阻塞

- 只剩 R-012 的 e2e：需要真实 Docker 主机与一套可登录的 IAM，验证**未登录请求确实被网关拦下**以及
  **启动顺序颠倒时没有放行窗口**。渲染与插值这一半已在本机验证（见 M2 检查表），剩下的是运行期行为。
