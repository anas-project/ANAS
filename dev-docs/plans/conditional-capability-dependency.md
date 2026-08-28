---
doc_type: plan
status: implementing
created: 2026-08-28
updated: 2026-08-28
---

# 条件 Capability 依赖实施计划

验收依据是[条件 Capability 依赖要求](../requirements/conditional-capability-dependency.md)的需求矩阵。
本主题没有独立架构文档——它扩展的是既有 Capability 解析路径，不引入新的运行时边界；与应用目录角色
模型的关系写在[应用目录设计](../../docs/architecture/app-catalog-design.md) §4.1。

M0、M1、M2 全部完成。M2 曾被一个结构性依赖环阻塞（`oauth2_proxy` 需要 IAM，IAM 需要数据库，而那正是
要守卫其 Adminer 的 Module），由[无序 Capability 依赖](weak-capability-dependency.md)解除。
M1 的结论是 `adminer_enabled`
**沿用现有的 `container_recreate`**，不新增 effect 取值——理由见要求文档 §6，R-013 与 R-014 已据此改写。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Manifest 字段与解析器 | R-001—R-011、R-016—R-017 | 已完成 |
| M1：输出、锁与变更分类 | R-012—R-015 | 已完成 |
| M2：Adminer 成为第一个消费者 | R-018—R-019 | 已完成 |

## 2. M0 检查表

- [x] `manifestRequiredCapability`（`internal/runner/manifest.go:151`）与 `RequiredCapability`
      （`internal/runner/modules.go:207`）增加 `EnabledBy`，拼写沿用 `enabled_by`。
- [x] 加载期校验 `normalizeCapabilityCondition`（`internal/runner/capability.go`）：拒绝 `global.`
      前缀与裸环境变量、拒绝非 lower-snake-case、拒绝未在 `config.types` 声明的参数、拒绝非
      `bool` 参数（R-004、R-005）。与 `interface_selected_by` 的校验在同一个规范化函数里。
- [x] 条件求值器 `capabilityRequired`：先读 `a.env[key]`，为空时回落到 `mod.Defaults[key]`（R-006）。
      没有动 `applyModuleDefaults` 的调用位置。
- [x] `resolveOrder`（`internal/runner/runner.go`）在 `RequiresCapabilities` 循环体**第一行**判断，
      条件不成立即 `continue`：不解析 Provider、不追加排序边、不写 `resolvedBindings`（R-001）。
- [x] 条件成立时走完全相同的代码路径（R-002、R-003）：唯一的改动就是那个 `continue`，没有并行分支。
- [x] 缺省即无条件（R-017）。`ddns_updater` 未改动。
- [x] `internal/runner/capability_condition_test.go`：条件真/假、显式值压过声明默认值、未显式配置
      时按声明默认值求值、条件为真而无 Provider 必须失败、条件为假而无 Provider 必须通过、
      无条件声明行为不变、五种 Manifest 拒绝。
- [x] `registryOnlyResolution` 路径与部署路径行为一致（R-016），表驱动跑开/关两组。
- [x] `docs/developer/capability-development.md` §6.1：字段说明、三条限制各自的理由、与
      `services.optional[].enabled_by`、`dependencies.requires[].optional`（R-009）和 launcher
      `enabled_if`（R-010）的对照表。
- [x] 通过 `go test ./...`、`go vet ./...`。

**变异验证**：去掉 `Defaults` 回落后
`TestConditionalCapabilityUsesDeclaredDefaultBeforeDefaultsAreApplied` 失败；去掉 `continue` 后四个
测试失败。两处都确认测试真的在约束实现，不是陪跑。

## 3. M1 检查表

- [x] `resolveCapabilityDependency` 在条件成立时写入 `<capability>.enabled_by`，与既有
      `<capability>.interface` 同一个位置、同一个形状（R-015）。`plan` 的 `capability_bindings` 与
      lock 的 `bindings` 都读这一份记录，没有新开输出通道。
- [x] 条件不成立时 `resolvedBindings` 与 `lock.Bindings` 里没有该 Capability 的任何键，无占位符
      （R-012）。
- [x] 条件成立时的绑定与无条件依赖一样写入 lock（R-011）。
- [x] `conditionalPullReason` + `validateLockedModuleBundles`：模块因条件依赖进入部署而锁里没有它
      时，错误信息指名是哪个 Consumer 的哪个参数带进来的（R-013）。解释只对条件带入的模块给出，
      运维自己选的模块保持原有的简短信息。
- [x] `anas config set` 的 `lock_stale` 分支补上 `--update-lock` 提示：这条命令自己就能一步做完，
      而「设置一个值」这件事本身不会让人想到锁。
- [x] **判定 `changes.effect`：不新增取值**（R-014）。读 `effectExecutor`
      （`internal/runner/config_cli.go:346`）后确认 effect 描述的是**用什么动作应用变更**，不是波及
      范围；`container_recreate` 落到 `deployment_apply_fallback`（渲染并激活新的不可变部署），对
      `adminer_enabled` 本来就是对的。缺的是锁，不是 effect。**要求文档 §6 与 R-013／R-014 已据此
      改写**——初稿那条要求是在读这段代码之前写的。
- [x] 测试覆盖绑定记录、无条件时不长出该键、条件为假时无占位符（含 lock）、锁错误的解释、
      无条件模块保持简短信息。
- [x] 通过 `go test ./...`、`go vet ./...`。

**变异验证**：无条件时也写 `enabled_by` 键 → 两个测试失败；去掉锁错误里的解释分支 → 一个测试失败。

## 4. M2 检查表

本轮完整接线过一遍，因依赖环回退；环由[无序 Capability 依赖](weak-capability-dependency.md)的
`ordering: any` 解除后，接线在该特性的 M2 中重做并落地。

- [x] `postgres` 与 `mariadb` 声明 `requires_capabilities: forward_auth`，`enabled_by: adminer_enabled`。
      `forward_auth` 在 registry 里没有 `ImplicitInterface`，因此还有 `interface_selected_by` 与配套的
      `forward_auth_interface` 参数（`auto`/`http`）。
- [x] 两者的 `consumes` 增加 `ANAS_FORWARD_AUTH_MIDDLEWARE`。
- [x] 两者的 Adminer 路由加 middlewares 标签（R-019）。postgres 的 router 名是 `adminer`，
      mariadb 的是 `mariadb_adminer`。
- [x] `adminer_enabled` 的 `changes` 未改（M1 结论：不新增 effect 取值）。
- [x] 八张参数表各加一行，`go run ./cmd/gen-module-docs --check` 通过。
- [x] 硬编码参数计数四个测试：170→172、153→155、131→133。
- [x] 实际 render 产物的 `docker compose config --quiet` 通过，中间件标签插值为
      `anas-forward-auth@docker`。证据见无序依赖计划的 M2 检查表。

**顺带记录的观察**：`forward_auth` 只有 `http` 一个 interface，和 `object_storage` 一样满足登记
`ImplicitInterface` 的条件。登记之后 Consumer 就能用 name-only 简写，不必为一个只有一个真实取值的
选择器给 postgres/mariadb 各加一个用户可见参数。这是共享 registry 的 ABI 改动，超出本特性范围，
值得单独立项。

## 5. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-018 | 待补 `test-env/scripts/server-conditional-capability-e2e.sh` | 真实 Docker；`adminer_enabled` 开/关两组配置对照 | — | 待执行 |
| R-019 | 待补 `test-env/scripts/server-adminer-forward-auth-e2e.sh` | 真实 Docker + oauth2_proxy + IAM；未登录必须被网关拦下 | — | 待执行 |

R-018 的关键是**关**的那一组：必须确认 `adminer_enabled=false` 的部署里没有 oauth2_proxy，而不是
只验证开启时它在。只验证正向的用例发现不了「条件永远为真」这类实现错误。

## 6. 当前阻塞

- **M2 等一个设计决策**，三选一，均超出本特性范围（要求文档 §8 有完整分析）：A 去掉 Adminer 的公网
  路由；B 给基础设施控制台用一道不依赖 IAM 的门；C 引入「存在性强制、不参与排序」的弱依赖。
  C 最通用**也最便宜**：Runner 的 calculate 与 render 本来就是两趟，渲染读的是已填满的 `a.env`，
  所以弱依赖只放弃 calculate 的先后，Adminer 的 Compose label 不受影响。
- e2e 需要可用的真实 Docker 主机与一套可登录的 IAM；本轮 Docker daemon 未运行，
  `docker compose config --quiet` 未执行。
