---
doc_type: plan
status: implementing
created: 2026-08-21
updated: 2026-09-20
---

# Forgejo Module 实施计划

验收依据是[Forgejo Module 集成要求](../requirements/forgejo-module.md)的需求矩阵，设计依据是
[Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md)。Forgejo 应用 Module 与 M1 安全开关已
落地；当前并行实施 M2 与 M3。Actions 默认关闭，只有执行面前置条件完整时才允许唯一开关同时
改变服务端和 controller，始终不暴露 server-only 或 Runner 第二开关。

本计划只跟踪施工顺序、需求归属和剩余工作。当前实现事实见
[`modules/forgejo/docs/technical.md`](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/docs/technical.md)。身份只保留 OIDC
一条链路，LDAP + OIDC/SAML 双链路不进入待实现里程碑；恢复它的唯一入口是 M8 的固定版本升级复核。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Forgejo 应用、数据库、OIDC 与恢复账号 | R-001—R-007 | 已完成；`R-006` 于 2026-09-20 恢复为规范来源（2026-09-13—09-20 期间曾废弃） |
| M1：Git Hooks/local-path import 安全开关 | R-010—R-012 | 已完成 |
| M2：compute contract 消费与两档隔离选择 | R-020—R-026 | 实施中；目录契约、Go 边界和 Incus 适配器已完成单元验证，两档隔离的参数语义与镜像一致性校验待补，真实宿主待验收 |
| M3：Actions 单开关、控制面账号与 one-job 执行面 | R-030—R-039、R-067—R-070 | 实施中；Module/controller/guest 资产已接线，控制面账号的权限收敛与关闭时失效待实现，真实 one-job 与隔离 E2E 待验收 |
| M4：预热、扩缩容、空闲资源与回收 | R-040—R-046 | 未开始 |
| M5：真实发布验收 | R-050—R-054 | 未开始 |
| M6：外部自动化消费者的边界（AI Agent 依赖） | R-060—R-062 | 未开始 |
| M8：固定版本升级的身份复核 | R-066 | 常设；每次变更 Forgejo 固定版本时执行 |

> M7（LDAP 同步 + OIDC 登录的双源身份）已于 2026-09-20 整体撤回，`R-063`—`R-065` 废弃、实现已删除，
> 因此不再占一个里程碑行。撤回理由与删除清单见 §7.2。`R-008` 同日废弃：它约束的是"M3 接入前不得
> 暴露 `actions_enabled`"这个过渡态，过渡已经发生，剩余约束由 `R-030`—`R-032` 承担。

覆盖统计：47 项在册需求全部有且只有一个里程碑归属（`R-008`、`R-063`—`R-065` 已废弃，不计入）；
M0/M1 的 10 项已完成，M2/M3 的 21 项处于实现与外部验收阶段。这两个数字由
`npm run docs:check-requirements` 的输出核对，不要凭记忆改。

## 2. 落地快照

| 范围 | 状态 | 当前边界 |
| --- | --- | --- |
| Forgejo `15.0.7-r1` rootless image、只读 rootfs、Web/SSH 入口 | 已实现 | 单元测试覆盖；真实双架构 E2E 待完成 |
| PostgreSQL/MariaDB Resource 与持久数据边界 | 已实现 | 数据迁移、备份恢复仍是 release gate |
| OIDC JIT、应用 Group 门禁、管理员 Group 映射 | 已实现 | 不接 LDAP/SAML，不做双链路 |
| `break_glass` 本地管理员 | 已实现 | apply 已验证；事务式 rotate 未声明 |
| Actions 单一功能开关 | 已接线、待 E2E | 默认关闭；`actions_enabled` 同时投影服务端/controller，无 Runner 第二开关 |
| Git Hooks/local-path import 正向配置 | 已实现 | 默认关闭、独立启停，不增加宿主挂载 |
| compute contract 消费（Incus 容器/VM 两档） | 实施中 | contract catalog、固定 restricted project/profile 验证和实例 CRUD 已有单测；独立宿主待验收 |
| 隔离档选择 `actions_isolation` | 已接线、需求待补齐 | 默认 `auto` 解析为 `incus_container`（与宿主共享内核），`incus_vm` 需要宿主具备 KVM；`R-024`—`R-026` 的单测与文档披露待补 |
| Actions 控制面账号 `anas_actions_controller` | 已实现、边界待收敛 | Actions 开启时建为站点管理员；权限收敛、关闭时失效与中英文档披露（`R-067`—`R-070`）待做 |
| Runner 执行组件 | 实施中 | queue controller、ephemeral registration、stdin token、one-job guest/rootless Podman 资产已实现 |

## 3. M1：高风险功能配置（已完成，2026-08-22）

- 在 `module.yml` 增加 `custom_git_hooks_enabled` 与 `local_path_import_enabled` bool，默认 `false`；
- Hook 分别映射 `DISABLE_GIT_HOOKS=!enabled` 与 `IMPORT_LOCAL_PATHS=enabled`；
- 两项声明 `container_recreate`，同步中英文 README、技术文档和配置参考；
- 单元测试覆盖默认关闭、显式开启、无任意宿主挂载和环境变量输出。

验收结果：默认行为不变；两项可以独立切换；Hook 拒绝非布尔值；Compose 仍只挂载托管 CA 与
Forgejo 数据目录；上游配置键已规范化为 Hook ABI 接受的大写形式；Module 单测、真实 Runner
渲染矩阵、生成文档与配置 inventory 均已覆盖。

Actions 单开关收敛同时完成：移除尚未具备执行面的 `forgejo.actions_enabled` 参数，Hook 固定输出
`FORGEJO__ACTIONS__ENABLED=false`。M3 会重新引入同名参数作为服务端与 Runner 的唯一共同开关。

## 4. M2：compute contract 消费与两档隔离选择

- 定义中立 compute contract，不在 Core 中按 Forgejo 或 Incus 名称特判；
- 消费 `incus_container` 与 `incus_vm` 两档接口；`actions_isolation` 的 `auto` 解析为
  `incus_container`，宿主缺少 KVM 不得自动降级，也不得自动升级（`R-024`）；
- 实现 Incus provider：restricted project、restricted TLS client、模板、profile、network 和 quota；
- Secret Store 管理 Incus client credential，Forgejo 应用容器不得消费；
- 提供 create、inspect、stop、delete 和 reconcile/janitor 所需的稳定 instance identity；
- 验证独立 Incus 宿主的防火墙、managed network、DNS 和到 Forgejo HTTPS 的最小连通性。

Provider 只能操作 `anas-forgejo-runners` project，不能修改 Incus 全局配置或其他 project。

当前完成：`contracts/compute` 已定义 provider-neutral schema；controller 只通过 `ComputeProvider` 接口
传 instance identity、固定 image fingerprint 与数值配额；Incus 适配器固定 remote/project/profile，
验证 restricted project、四类 project quota、受限 egress 标记、managed NIC，并拒绝 host disk、
physical NIC、cloud-init secret 与任意 device。Incus credential 只投影到 controller，Forgejo service
不再读取 module-wide `.env`。

剩余：把 compute catalog 接入通用 Provider 注册/选择路径；在独立宿主创建受限证书、project、
network/profile/storage；验证防火墙、DNS、最小 egress 和 crash 回收。当前 Contract 状态保持 `proposal`，
不能把 Go 适配器单测等同于通用 Contract 已发布。

两档隔离的剩余工作单列，它们现在一条都没有实现：

- [ ] `R-024`：`auto → incus_container` 的解析与"不自动升降级"由单测钉住。当前 `module.yml` 的
      `default: incus_container` 没有任何测试守护——`modules/forgejo` 下没有一处测试提到隔离档；
- [ ] `R-025`：中英 README 与技术文档写明默认档与宿主共享内核、两档边界差异，以及跨信任域或执行
      不受信输入的 scope 必须选 `incus_vm`；
- [ ] `R-026`：开启 Actions 时校验固定 image fingerprint 与所选档、目标架构一致。`runner-image/`
      要求按 amd64/arm64 × container/vm 出四份镜像，而 `actions_runner_image` 是单值且只校验 64 位
      hex，配错只能等实例创建失败才暴露。

> **拆分说明。** 通用 Provider Module（`modules/incus`）、多消费者隔离和内嵌 Incus 客户端的迁移已
> 独立跟踪，见[Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md)与其[要求](../../../../dev-docs/requirements/incus-module.md)。
> 本里程碑此后只负责 Forgejo **作为消费者**接入：controller 通过 Contract 调用、行为等价性和
> Forgejo 侧的连通性验收；Provider 自身的实现、隔离与证书轮换不再在本文重复跟踪。

## 5. M3：Actions 单开关、控制面账号与 one-job 执行面

- 重新引入唯一的 `forgejo.actions_enabled`；一次调和同时改变 Actions 服务端、controller 与 Runner
  desired state，不增加 `runner.enabled` 或第二个 Module 开关；
- Runner 可以独立打包和发布，但由 Forgejo Actions desired state 自动派生，管理员不手工启用；
- repo/org scope 是授权集合，只接受 `{owner}` 或 `{owner}/{repo}`，拒绝 global runner；
- 为每次执行创建 Forgejo ephemeral runner 和一个 Incus 执行实例，在实例内配置 `runner-agent`、
  `runner-engine` 与 rootless Podman，并运行 `forgejo-runner one-job`；
- 禁止 `host` label/privileged/任意 volume；扩容只增加 one-job 执行实例，不复用 Runner；
- runner token 使用一次性 credential lifecycle，只进入 tmpfs，不进入 cloud-init、argv、镜像或日志；
- 配置 egress allowlist、CPU/memory/PID/disk/job timeout 和作业后清理。

批准仓库应能运行无 Secret 的容器构建；未批准仓库没有可用 ANAS Runner；作业无法访问
ANAS 管理面、数据库、目录服务、宿主文件或其他 Runner 执行实例；作业后执行实例、root disk 和
token 均消失。

当前完成：唯一 bool 已重新接入 Hook/Compose；Hook 在开启时校验获批 scope、控制面账号口令和固定
image fingerprint（Incus endpoint 与证书已不再是 Forgejo 配置，改由 compute contract 供给）；一次性
preflight 在 Forgejo 启动前实际连接 Incus 验证 project/quota/profile，避免 server-only 半开启；
controller 每 15 秒轮询 repo/org jobs API，按 handle 创建 ephemeral registration 与一个执行实例，持久状态
不含 token；token 只经 Incus exec stdin 进入 guest tmpfs。guest 以独立 Runner/engine 用户运行
`one-job --handle --wait` 和 rootless Podman，capacity=1、无 `host` label、无 privileged、无任意 volume；
全局并发 4、每 scope 并发 2、job timeout 1 小时。关闭开关时 controller 只执行清理后退出。

剩余：构建并签收 amd64/arm64 guest image fingerprint；在真实 Forgejo 15.0.7 + Runner >=12.8 + Incus
环境验证批准/未批准仓库、容器构建、失败/取消/重启、网络隔离、磁盘与 registration 清理。完成这些
E2E 前不把 Actions 标为 release 能力。

控制面账号（`R-067`—`R-070`）单列。Actions 开启时 `after_start` 把 `anas_actions_controller` 建成
**站点管理员**，而 controller 实际只调用获批 scope 下的三个 `actions/runners` 端点（列表、创建、
删除）。站点管理员不是这个调用集合的要求，而是"没有按 scope 授予组织 owner / 仓库 admin 的调和
路径"的后果：

- [ ] `R-067`：该账号与 `break_glass` 分离、口令只来自 Secret Store、明文不进宿主 Docker argv
      （当前经 stdin 传入，需补单测钉住）；
- [ ] `R-068`：单测断言 controller 的调用集合限定在获批 scope 的 `actions/runners`，不含
      `/api/v1/admin/*`；
- [ ] `R-069`：中英 README 与技术文档写明该账号的用户名、创建时机、权限范围与撤销方式；
- [ ] `R-070`：关闭 `actions_enabled` 后停用该账号或使其口令失效。当前 `reconcileActionsAccount`
      在关闭时直接返回，站点管理员账号和它在 Secret Store 里的有效口令都会留下。

## 6. M4：预热、扩缩容与回收

- 默认 `prewarm=0`，controller 轮询获批 scope 的 `actions/runners/jobs`，只在发现 waiting job 后按
  job `handle` 创建 ephemeral Runner 和执行实例；
- 管理员未来显式配置预热时，每个启用 scope 最多维持一个尚未启动 Runner 的预热实例；
- 按队列压力增加独立执行实例数量，受 Incus project 和 scope quota 双重限制；
- janitor 处理排队取消、启动失败、超时和 controller 崩溃；
- 对已经启动 `one-job --handle ... --wait` 但尚未领取任务的执行实例设置 10 分钟 waiting TTL，超时
  后注销 registration、销毁执行实例/root disk，并对同一 handle 退避；
- 外置 cache/registry 使用仓库范围短期凭据，不持久化跨信任域 workspace；
- **state 丢失后仍能收敛（`R-046`）**。现状复核（2026-09-20）：`ListManaged` 只在 `CleanupAll`
  里被调用，也就是只在关闭开关时才按 Incus 侧实际存在的实例兜底；周期性 `Reconcile` 完全依据
  state。而孤立的 **Forgejo runner registration 没有任何兜底路径**——`ForgejoAPI` 只有
  `ListJobs`/`CreateRunner`/`DeleteRunner`，注销依赖 state 里的 `RunnerID`。因此
  `forgejo_actions_state` 这个 volume **不是可以随手丢弃的**：丢了它，Actions 仍开着时孤立实例要等
  到下次关闭才回收，崩在 `CreateRunner` 与作业开始之间的 registration 则永远留着。

正常、失败、取消和 controller crash 四条路径都不得残留有效 runner token、执行实例或可挂载 volume。
空队列连续两个调和周期后不得存在 Runner registration、Runner 执行实例或临时 root disk；30 分钟空闲
验收记录 controller RSS、平均单核 CPU、每 scope API 频率和 Actions 关闭时的 Forgejo 对照基线，门限
分别为 128 MiB、1% 和每 10 秒至多一次。

## 7. M5：发布门禁

- PostgreSQL/MariaDB、amd64/arm64；
- LLNG/Authentik OIDC 浏览器登录、Group 准入/拒绝、管理员降权、IAM-down；
- HTTP/SSH clone/push、LFS、Package、备份恢复、上一 LTS patch/minor 升级回滚；
- repo/org scope、网络隔离、资源限制、token 清理和执行实例回收 E2E；两档隔离各跑一遍；
- 单次切换 `forgejo.actions_enabled` 同时改变服务端和 Runner，且不存在第二个 Runner 开关；
- custom Git Hooks 与 local import 开关的安全回归。

门禁分两组，按 `R-054` 分别结算：纯代码托管的那组（数据库、架构、OIDC、Git/LFS/Package、备份恢复、
升级回滚、两个高风险开关的安全回归）全部通过后即可评估应用 Module 的 `release`；Actions 的那组
（`R-052`、`R-053`）未完成时，Actions 不得标为可用能力，但不阻塞应用 Module 发布。当前 Runner
执行组件是 `forgejo` 自己的 Compose service，不是第二个 Module——"独立 Runner 包"只是允许的打包
形态，不是已存在的交付物。

## 7.1 M6：外部自动化消费者的边界

由 [AI Agent 编排](../../../ai_agent/dev-docs/plans/ai-agent.md) 驱动的三项依赖，本 Module 只负责“不妨碍且可声明”，不实现 Agent 逻辑：

- [ ] **先跑探针确认 `forgejo admin auth add-oauth` 真的有 `--group-team-map` 与
      `--group-team-map-removal`。** 探针的 `group-team-map` 用例在最近一次记录里是 **SKIP**（需要
      设置 `PROBE_FORGEJO_CONTAINER`），从未真正执行过；`R-060`、本里程碑，以及设计 §2.2 里"组变更
      在下次登录到达 team"这条收敛路径全部建立在它上面。不成立就要重写 `R-060` 与设计 §2.2，而不是
      继续实现；
- [ ] OIDC reconcile 增加声明式的 group→team 映射（`--group-team-map` 与登录时移除），映射内容来自
      消费方配置，Module 不硬编码组名；
- [ ] reconcile 明确不触碰由管理端 API 创建的自动化账号、token 与 SSH key，并补单元测试守住。
      **这一条值得先于本里程碑其余部分落地**：`ai_agent` 已经在创建这些对象，而守护测试还没有。
      现状复核（2026-09-20）：`ensureLocalAdmin` 遇到同名但凭据不符的既有账号时返回错误，
      `cleanupLocalAdmin` 只回滚它自己刚创建的账号，因此当前**不会**删改他人对象——这是一个没有
      测试守护的不变量，不是已经发生的缺陷；
- [ ] 文档写明系统 webhook 与管理凭据的归属：Forgejo Module 不注册业务 hook、不清理他人 hook，
      管理凭据的持有方与审计要求由消费方 Module 声明；
- [ ] 同步中英文 README 与技术文档。

验收：要求文档 `FORGEJO-R-060`—`FORGEJO-R-062`。

## 7.2 M7：LDAP 同步 + OIDC 登录的双源身份（已撤回）

2026-09-13 依据当时的[设计 §2.2](../../../../docs/architecture/forgejo-module-design.md) 引入，
2026-09-20 连同实现一并撤回。撤回理由见改写后的设计 §2.2：核对固定版本 `cmd/admin_auth_ldap.go`
后确认 LDAP CLI 没有组同步选项，双源形态换不到 `ai_agent` §6.2 要的 team 成员实时性，只换到"账号能否
登录"的加速，却要付出目录副本、`ACCOUNT_LINKING=auto` 的三条前提（含"只能有一个 OAuth2 source"这条
硬约束）和一个常驻 watcher 的代价。

已删除的实现：`directory_sync_enabled` 与 `account_linking` 配置、`forgejo.dirwatch_password`
凭据、Hook 的 LDAP source 与 dirwatch 调和、容器 helper 的 `ldap`/`directory-watch` 子命令、
`anas_forgejo_dirwatch` Compose 服务与 `directory-watch` 网络。代码保留在 Git 历史中，`R-066`
复核推翻现结论时从那里取回，不要凭记忆重写。

需求：`R-063`—`R-065` 已废弃；OIDC-only 的边界与禁令回到 `R-006`。

## 7.3 M8：固定版本升级的身份复核（常设）

每次变更 Forgejo 固定版本（含 patch）执行一次，验收 `R-066`：

- [ ] 按[互操作基线](../../../../docs/developer/forgejo-interop.md) §4 跑探针，不凭 changelog 下结论；
- [ ] 复核[设计 §2.3](../../../../docs/architecture/forgejo-module-design.md) 的四点：LDAP source 的不可变 ID
      字段、OIDC source 按 claim 绑定既有账号、认证源 REST API 或 LDAP CLI 组同步选项、IAM 主动 logout
      receiver 或按用户撤销会话/token 的管理端接口；
- [ ] 顺带复核 `prohibit_login` 是否同时关闭 access token 与 Git over SSH（当前"尚未复核"，撤权流程
      因此要求显式吊销 token 与 SSH key）；
- [ ] 结论写回设计 §2.2/§2.3 与互操作基线 §1；第 1、2 点同时成立才评估恢复双链路，并重新走
      `R-006` 的修订流程。

## 8. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...`（`modules/forgejo` 的 `hook`、`forgejo`、`actions-controller` 包） | `25433bd`（GitHub CI，2026-08-29），包含 `b38fd7c` 落地的 M0—M3 已实现部分，但不含 `2b44a2b`（2026-09-05）对 Hook、controller 与 manifest 的后续改动。`5306b63` 上三个包都通过，整体因 `internal/runner` 的无关用例失败；其后提交均未经 CI |
| `go run ./cmd/gen-module-docs --check` | `5306b63`（GitHub CI，2026-09-05） |
| `go run ./cmd/check-shared-build`（`actions-controller` 经命名上下文取 `internal/computeclient`） | 从未在 CI 中运行：该门禁由 `63083e9`（2026-09-09）加入，其后提交均未推送 |
| `npm run docs:check-requirements`、`docs:check-requirement-status` | `5306b63`（GitHub CI），当时本计划仍在 `dev-docs/plans/`、还没有 M7；`9888ae1` 移入 Module 目录、`4317f7c`（2026-09-13）加入 M7 并废弃 `R-006` 之后没有 CI 记录，本地 `6823232` 通过 |
| M1 验收提到的真实 Runner 渲染矩阵 | 不在 CI 中；§3 记为 2026-08-22 本地覆盖，未注明提交 |

## 9. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-043 | 待补 `server-forgejo-actions-idle-e2e.sh` | Incus + 空队列两个调和周期 | — | 待执行 |
| R-044 | 待补 `server-forgejo-actions-idle-e2e.sh` | Incus + 30 分钟 Actions on/off 对照 | — | 待执行 |
| R-045 | 待补 `server-forgejo-actions-waiting-ttl-e2e.sh` | Incus + concurrency group 阻塞 | — | 待执行 |
| R-060 | 待补 `server-forgejo-group-team-map-e2e.sh` | LLNG/Authentik + 目录组变更 | — | 待执行 |
| R-050 | 待补 `server-forgejo-app-e2e.sh` | PostgreSQL/MariaDB、amd64/arm64 | — | 待执行 |
| R-051 | 待补 `server-forgejo-oidc-e2e.sh` | LLNG/Authentik 浏览器 | — | 待执行 |
| R-052 | 待补 `server-forgejo-actions-state-e2e.sh` | Incus + Forgejo | — | 待执行 |
| R-053 | 待补 `server-forgejo-runner-isolation-e2e.sh` | Incus + repo/org scopes | — | 待执行 |

## 9.1 身份撤权探针用例

这四条不是需求门禁上的 e2e，而是 `FORGEJO-R-066`(§7.3 M8)每次固定版本变更都要执行的探针。
它们回答的是现在写在文档里、但标着 `推断` 或「尚未复核」的几件事——撤权流程直接建立在这些答案
上，答错一条，运维按文档执行的撤权就是无效的。

统一纪律：按[互操作基线](../../../../docs/developer/forgejo-interop.md) §4，结论必须来自真实固定
版本上的实测，不得凭上游文档或 changelog；结论写回互操作基线 §1 与本 Module 的技术文档，并把对应
行的证据等级从 `推断` 改为 `已验证`。

### FJPROBE-T-001 `prohibit_login` 的实际关闭范围

**这是最重要的一条。** 当前撤权流程写成「停用账号 **并** 显式吊销 token 与 SSH key」，正是因为不
知道第一步能关掉什么。

| 项 | 内容 |
| --- | --- |
| 前置 | 一个 OIDC 建出的普通账号，已有：一个活跃的浏览器 session(保存 Cookie)、一个 access token、一枚 SSH key、一个可读写的仓库 |
| 步骤 | 经管理端把该账号置 `prohibit_login` → 依次重试四条通路 |
| 断言 | 逐条记录**关闭还是仍然可用**：① 已有 Web session 访问需认证页面；② access token 调 API；③ Git over SSH `push`；④ Git over HTTP `push` |
| 反例 | 无需构造——任何一条仍然可用就是本用例要暴露的事实 |
| 清理 | 解除 `prohibit_login`，删除测试账号、token 与 key |

四条通路的结果直接决定撤权流程要写几步。若 ① 仍然可用，则说明没有任何管理端动作能立即失效已有
session，文档必须明说这一点，而不是让运维以为停用就够了。

### FJPROBE-T-002 管理端能否单独置 `prohibit_login`

| 项 | 内容 |
| --- | --- |
| 前置 | 同上 |
| 步骤 | `PATCH /api/v1/admin/users/{u}` 只带 `prohibit_login` → 若被拒，补 `login_name`/`source_id` 重试 |
| 断言 | 记录最小可接受的请求体；若必须先 `GET` 取 `login_name`/`source_id`，把这个顺序写进技术文档 |
| 反例 | 缺少必填字段时必须是明确的 4xx，不得部分生效 |

任何自动撤权路径都要先过这一关，否则它连第一步都做不出来。

### FJPROBE-T-003 `sub` 是否存进 `login_name`

| 项 | 内容 |
| --- | --- |
| 前置 | 一个刚由 OIDC 首次登录建出的账号 |
| 步骤 | 以管理员身份 `GET /api/v1/admin/users`(或该用户)→ 取 `login_name`、`source_id` → 与 ID Token 的 `sub` 比对 |
| 断言 | `login_name` **逐字节等于** `sub` |
| 意义 | 成立则 `DIRKEY-R-008` 落地后 Forgejo 无需任何新能力即可精确对账(设计 §2.2)；不成立则该结论作废，必须改写 |

### FJPROBE-T-004 改名后的实际行为

| 项 | 内容 |
| --- | --- |
| 前置 | 账号已登录过一次并建有仓库 |
| 步骤 | 在目录改 `sAMAccountName` → 等 IAM 同步 → 再登录一次 |
| 断言 | 是同一个 Forgejo 账号(账号 id 不变)；Forgejo 用户名冻结在旧值；仓库路径不变；邮箱不刷新 |
| 反例 | 若建出第二个账号，说明该 Provider 的 `sub` 不跨改名稳定，属于 `DIRKEY-R-008` 的违例，回报到目录身份键计划 |
| 附带 | 顺便确认管理端有没有重命名用户的接口(`EditUserOption` 是否含 `new_name`)——它决定改名后能否把用户名对回去 |

## 10. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `forgejo` 的 README、技术文档与配置参考（中英文） | M1 的 Git Hooks 与 local-path import 开关 | 已完成（§3，2026-08-22） |
| [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §2.2、§2.3 | 身份收敛为 OIDC-only，写明撤回理由、运维必须承担的代价，以及 §2.3 的升级复核四点 | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | 删除目录同步段落与两个配置参数，改写为单链路身份的边界表与「尚未复核」项 | 已完成（2026-09-20） |
| [Module IAM / OIDC 支持清单](../../../../docs/reference/module-iam-support.md)与英文镜像 | `forgejo` 一行与双接入段落回到 OIDC-only | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | M6 的系统 webhook 与管理凭据归属落地时改写 | 未开始 |
| 本计划 §4（M2） | 已加与 [Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md) 的拆分说明，但要点列表与「当前完成/剩余」仍按 Provider 实现叙述 | 部分完成 |
| [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §1、§3、§4、§6 | 承认 `incus_container`/`incus_vm` 两档，默认档为系统容器，并写明两档的威胁模型差异 | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | 隔离档披露（`R-025`）与控制面账号披露（`R-069`） | 已完成（2026-09-20） |
| [Forgejo 互操作基线](../../../../docs/developer/forgejo-interop.md) §1、§3 | 开启 Actions 的实际前置校验项，以及 `--group-team-map` 等未经探针证实条目的标注 | 已完成（2026-09-20） |

## 11. 当前阻塞

- 当前没有可用的独立 Incus 宿主、restricted project 或测试 credential，M2/M3 无法完成真实验收；
  `incus_vm` 档还额外要求宿主具备 KVM，而 `incus_container` 档不要求；
- compute Contract 尚未接入 Core 的通用 Provider 注册/选择路径；当前 controller 使用同形 Go 接口和
  首个 Incus adapter；
- Runner 镜像尚未按 amd64/arm64 × container/vm 实际构建并产出获批 fingerprint；
- 真实 Docker daemon 未运行：Forgejo 应用镜像启动、浏览器 E2E，以及 §7.3/§9.1 的全部探针用例
  （含 `group-team-map`）都无法执行。

## 12. 明确排除

以下项目不属于剩余工作：

- Forgejo LDAP 用户/Group 预配（恢复的唯一入口是 §7.3 M8 的升级复核）；
- Forgejo SAML；
- `anasIdentityAnchor` claim/reconciler；
- LDAP 预建用户与 OIDC 自动合并；
- global runner、宿主 Docker socket 和 ANAS 宿主 privileged DinD；
- 独立 `runner.enabled`、`forgejo_runner.enabled` 或要求管理员二次启用 Runner Module。
