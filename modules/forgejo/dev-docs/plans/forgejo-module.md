---
doc_type: plan
status: done
created: 2026-08-21
updated: 2026-11-02
---

# Forgejo Module 实施计划

2026-09-25 联合停止夹具通过完整十阶段、五个Core停止用例：真实工作流运行时由旧
冻结Hook排空controller，确认实例/根盘/有效registration清空后才移除Compose；
关闭时口令失效，重新启用保留账号ID且第二个真实workflow成功。异常controller状态
仍阻止停止和撤权，失败状态与原口令保留。公开归档、正常关机及物理宿主对照已独立
验证。此处采用显式可路由Docker、SQLite和准备的生命周期workspace，不是完整公开
CLI业务部署或默认DROP网络兼容验收；参见
[本轮记录](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-25-forgejo-stop-forwarding-continuation.md)。

2026-09-24 账号生命周期接续：固定 Forgejo 15.0.7、真实入口 helper 和 controller 在全新
Debian 13 amd64 VM 通过完整 **17 项**账号/状态门禁：管理口令关闭后失效、重复不旋转、
重新启用与重启保留数值 ID、未知同名/替换账号拒绝，缺少租约的未完成状态和链接状态
不会被当作清理完成。Hook 增加同容器清理等待、上下文下传与严格状态观察。本机回归与
账号 API 原生验收不代替完整 Core/Compose 开关和真实 guest 排空；站点管理员权限偏差
及 state volume 丢失仍未解决。证据见
[账号停用接续](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-24-forgejo-actions-account-lifecycle.md)。

2026-09-24 继续真实消费者接入：发现早期 one-job 夹具以管理权限向 guest 手工安装公开
CA，而生产 controller 只交付 token。现已把公开部署 CA 接到两个固定只读挂载，并经
原 stdin 通道按长度/摘要投影到单次 Runner 的临时 trust bundle；guest 系统根、engine
和工作流镜像不被修改，token 仍不进入 argv/env/state。已删除原生入口的手工信任替身，
新 recipe/不可变 revision 必须独立通过烘焙与 TLS 工作流，不能继承 lab-r11 的通过状态。
本轮结果及范围见[Runner 信任接续](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-24-forgejo-runner-trust-projection.md)。

该新修订 `trust-r2` 随后完成真实 bake/export/重复复用、9 项镜像门禁及五种工作流场景；
四种执行场景均清空实例/根盘/注册/state，未授权仓库保持等待。原生入口没有手工 CA
安装；独立归档、正常关机与物理宿主基线复核均通过。这个范围已闭环，完整业务 Compose、
工作流内部 checkout trust、VM/ARM64 与正式发布不由此提前完成。

2026-09-22 默认系统容器 one-job 运行闭环已验收：使用不可变 `lab-r11` 与真实 Forgejo
15.0.7，正常、显式失败、controller SIGTERM 取消、SIGKILL 后保留状态的恢复均完成
实例/根盘/注册回收，未授权仓库未获得执行资源。内层 OCI exec 实际限额门禁同时通过。
见[闭环记录](../../../../dev-docs/reviews/2026-09-22-incus-onejob-closeout.md)。
这不将 M2/M3/M4/M5 整体改为完成：VM/ARM64、state volume 丢失、网页取消、真实容器
镜像构建、Actions 开关与账号撤销、数据库/IAM/升级及正式发布矩阵仍按原需求跟踪。

验收依据是[Forgejo Module 集成要求](../requirements/forgejo-module.md)的需求矩阵，设计依据是
[Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md)。Forgejo 应用 Module 与 M1 安全开关已
落地；本轮amd64的M2—M4已经结算（§17.15），M5全量Samba/Core复验已通过（§17.16），本轮交付到ln待发布目录。Actions 默认关闭，只有执行面前置条件完整时才允许唯一开关同时
改变服务端和 controller，始终不暴露 server-only 或 Runner 第二开关。

本计划只跟踪施工顺序、需求归属和剩余工作。当前实现事实见
[`modules/forgejo/docs/technical.md`](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/docs/technical.md)。身份只保留 OIDC
一条链路，LDAP + OIDC/SAML 双链路不进入待实现里程碑；恢复它的唯一入口是 M8 的固定版本升级复核。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Forgejo 应用、数据库、OIDC 与恢复账号 | R-001—R-007 | 已完成；`R-006` 于 2026-09-20 恢复为规范来源（2026-09-13—09-20 期间曾废弃） |
| M1：Git Hooks/local-path import 安全开关 | R-010—R-012 | 已完成 |
| M2：compute contract 消费与两档隔离选择 | R-020—R-026 | 已完成；按本轮amd64范围，契约/凭据/档位/类型架构反例及两档真实生命周期、默认网络通过，见§17.12—17.15；ARM64按用户确认延期 |
| M3：Actions 单开关、控制面账号与 one-job 执行面 | R-030—R-039、R-067—R-070 | 已完成；既定单元、账号API及完整Core单开关R-052、amd64两档作业R-053通过，固定字节签收见§17.15；R-069站点管理员偏差按要求持续登记，未宣称权限已收敛 |
| M4：预热、扩缩容、空闲资源与回收 | R-040—R-046 | 已完成；配额/隔离/janitor/签名孤儿单元及真实API通过，真实600秒waiting TTL、1800秒idle及两档正常/失败/SIGTERM/SIGKILL恢复通过；不要求未发布旧注册迁移 |
| M5：真实发布验收 | R-050—R-054 | 已完成；按用户确认的首发amd64范围，数据库/hosting/一致备份恢复、两种真实IAM、Core单开关、两档Runner与固定字节签收均通过；全量Samba依赖/Core最终r5通过（§17.16）。用户仅要求ln待发布包，未公开发布；ARM64延期 |
| M6：外部自动化消费者的边界（AI Agent 依赖） | R-060—R-062 | 已完成；R-060 真实 Casdoor/LLNG 映射及登录移除通过，R-061/R-062 的 Module 对象边界单元与调用链审阅通过，见 §16 |
| M8：固定版本升级的身份复核 | R-066 | 已完成；当前17.0.1的四点互操作探针及设计回写通过（§7.3/§14）；以后每次固定版本变化必须重新执行 |

> M7（LDAP 同步 + OIDC 登录的双源身份）已于 2026-09-20 整体撤回，`R-063`—`R-065` 废弃、实现已删除，
> 因此不再占一个里程碑行。撤回理由与删除清单见 §7.2。`R-008` 同日废弃：它约束的是"M3 接入前不得
> 暴露 `actions_enabled`"这个过渡态，过渡已经发生，剩余约束由 `R-030`—`R-032` 承担。

覆盖统计：47 项在册需求全部有且只有一个里程碑归属（`R-008`、`R-063`—`R-065` 已废弃，不计入）；
M0/M1 的 10 项、M2/M3 的 21 项按本轮amd64范围已完成；完整发布状态仍由M5结算。这两个数字由
`npm run docs:check-requirements` 的输出核对，不要凭记忆改。

## 2. 落地快照

2026-09-22 引擎准入接续：新 starter 在读取 token 前确认固定 Podman API 可由 Runner 用户
调用并报告 rootless，失败有界退出，原有 controller 补偿通过专项回归；实际 shell 行为
测试已加入 CI。增强后的镜像原生门禁提供就绪等待与固定类别诊断，但本轮指定主机 SSH
握手失败，未运行新实机测试。旧 `lab-r4` 的 exit 125 保留为未解决问题，不因本轮本机
测试而标记 M3/M5 完成；构建/实机分层状态见
[本轮核对](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-22-incus-runner-engine-admission.md)。

| 范围 | 状态 | 当前边界 |
| --- | --- | --- |
| Forgejo `17.0.1-r1` rootless image、只读 rootfs、Web/SSH 入口 | 已实现 | amd64 正式镜像与完整 apply 通过；ARM64本轮延期，后续独立验收 |
| PostgreSQL Resource 与持久数据边界 | 已实现；本轮用户确认仅 PostgreSQL，旧 MySQL/MariaDB 明确拒绝 | amd64真实备份恢复通过；ARM64延期，首发无已发布旧制品兼容要求 |
| OIDC JIT、应用 Group 门禁、管理员 Group 映射 | 已实现 | 不接 LDAP/SAML，不做双链路 |
| `break_glass` 本地管理员 | 已实现 | apply 已验证；事务式 rotate 未声明 |
| Actions 单一功能开关 | 本轮amd64 Core E2E通过（§17.12） | 默认关闭；`actions_enabled` 同时投影服务端/controller，无 Runner 第二开关 |
| Git Hooks/local-path import 正向配置 | 已实现 | 默认关闭、独立启停，不增加宿主挂载 |
| compute contract 消费（Incus 容器/VM 两档） | 本轮amd64验收完成 | 契约/凭据/围栏反例及独立宿主两档生命周期通过，见§17.12—17.15；ARM64延期 |
| 隔离档选择 `actions_isolation` | 已接线 | 默认 `auto` 解析为 `incus_container`（与宿主共享内核），`incus_vm` 需要宿主具备 KVM；R-024/R-025 已检查，R-026 由 Provider 在登记证书前校验镜像一致性（2026-10-30 复核） |
| Actions 控制面账号 `anas_actions_controller` | 管理密码生命周期已实现并独立验收 | 独立恢复 owner、stdin、ID/HMAC 归属、关闭口令失效/重新启用及重复执行通过；站点管理员角色与其他 token/SSH key 不由此撤销，完整开关路径另验 |
| Runner 执行组件 | 本轮amd64验收与待发布包签收完成 | 两档固定guest字节、真实工作流/构建/回收及包反例通过，见§17.14—17.16；尚未公开发布 |

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

本里程碑只负责 Forgejo **作为 `compute` 消费者**接入。围栏（restricted project、profile、受管 bridge、
配额与受限证书登记）由 `incus` Provider Module 在 apply 时建立，实例生命周期由共享
`internal/computeclient` 实现，二者的实现、隔离与证书轮换归
[Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md)，本文不重复跟踪。

- manifest 经 `dependencies.contracts` 与 `resources.requires` 声明 compute 租约，沿用 sandbox
  `anas-forgejo-runners`，不要求迁移数据或重建 project；
- 消费 `incus_container` 与 `incus_vm` 两档接口；`actions_isolation` 的 `auto` 解析为
  `incus_container`，宿主缺少 KVM 不得自动降级，也不得自动升级（`R-024`）；
- controller 只读取 Core 投影的私有租约，Forgejo 应用容器不持有 endpoint、证书或私钥；
- 为 create、inspect、stop、delete 和 reconcile/janitor 使用共享客户端的稳定 instance identity；
- 验证真实宿主上 managed network、DNS 和到 Forgejo HTTPS 的最小连通性。

当前完成：controller 已改为 `internal/computeclient` 的消费者，删除自有 Incus 适配代码与 endpoint/
证书配置项；默认容器档的真实 one-job（正常、失败、SIGTERM 取消、SIGKILL 后保留 state 恢复）已在
独立宿主验收，证据见 Incus 计划 M5a。Forgejo 移除或关闭 Actions 时租约证书由 Core 撤销（`INCUS-R-111`）。

本轮结算：amd64容器/VM及repo/org容器构建已通过（§17.14），ARM64延期；R-046按既定单元及真实API判据通过，不把SIGKILL保留state混称实际state丢失。取消实际使用controller SIGTERM，不宣称网页取消已执行。下列消费者侧条目已完成。

两档隔离的消费者侧条目：

- [x] `R-024`：`TestModuleIsolationTierDefaultsToTheContainerTier`（`hook`）钉住 compute contract 的
      `default: incus_container`、`selected_by`/`enabled_by`、两档 interface 集合，以及
      `actions_isolation` 的枚举与默认值。把 manifest 的默认翻成 `incus_vm` 会让它失败；
- [x] `R-025`：中英 README 与技术文档写明默认档与宿主共享内核、两档边界差异，以及跨信任域或执行
      不受信输入的 scope 必须选 `incus_vm`（§10 文档同步，2026-09-20）；
- [x] `R-026`：2026-10-30 按当前调用链复核：Hook 检查 fingerprint 格式，compute Provider 的
      `ensure → verifyImages` 在登记租约证书前检查 fingerprint、目标架构与 container/VM 类型；
      `TestEnsureRefusesWrongImageBeforeRegisteringTrust` 三个反例通过。旧“配错只能等创建失败”
      表述已过时；本轮amd64的container/vm真实Runner镜像与固定字节签收通过（§17.14—17.15），ARM64相关验收经用户确认延期。

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

本轮结算：固定 Forgejo17.0.1/PG、amd64两档Runner的批准/未批准仓库、repo/org容器构建、失败/取消/恢复、适用隔离反例及磁盘/registration清理通过；原guest字节签收见§17.15。ARM64延期，尚未公开发布；伴随Samba全量构建/Core复验仍属M5交付门禁。

控制面账号（`R-067`—`R-070`）单列。当前 `local_account_apply` 验证独立恢复管理员后调和
`anas_actions_controller`，Actions 开启且账号确实不存在时建成
**站点管理员**，而 controller 实际只调用获批 scope 下的三个 `actions/runners` 端点（列表、创建、
删除）。站点管理员不是这个调用集合的要求，而是"没有按 scope 授予组织 owner / 仓库 admin 的调和
路径"的后果：

- [x] `R-067`：账号与 `break_glass` 分离、口令来自 Secret Store，类型化 stdin 和 owner-first
      顺序已有回归；真实 helper 验证独立 owner，未把明文放入宿主 argv；
- [x] `R-068`：`TestForgejoClientNeverLeavesTheApprovedScopeRunnerAPI`（`actions-controller`）对 org 与
      repo 两种 scope 驱动三个方法，断言请求恰好三条、全部落在该 scope 的 `actions/runners` 子树内，
      且不含 `/admin/`。把路径改成 `/api/v1/admin/...` 会让它失败；
- [x] `R-069`：中英 README 与技术文档写明用户名、创建时机、站点管理员偏差，以及管理口令
      失效与其他 token/SSH key 撤销的区别；不宣称已完成按 scope 的权限收敛；
- [x] `R-070`：停用 helper 使原管理口令失效并读回 401/403，重复禁用不旋转；同数值 ID 的
      重新启用、重启与替换账号拒绝通过真实 API 验收。Hook 先验证同一 controller 完成清理，
      缺少租约且状态未完/链接/含糊时不能撤密码；完整 Core/Compose 开关及运行作业排空另验。

- [x] `R-052`：2026-11-01完整Core单开关验收通过；标准主机安装/配置/登记、真实工作流、运行中关闭与账号撤销、重新启用、普通CLI停止及宿主不变验证见§17.12。`R-053`的amd64 VM Runner档随后通过，见§17.14；不将实验制品混称正式发布。

- [x] `R-053`：2026-11-01本轮amd64容器/VM两档通过；获批repo/org真实无Secret构建、未获批waiting无Runner、正常/失败/SIGTERM取消/SIGKILL恢复及资源回收，VM最终r13见§17.14。ARM64按用户确认延期。

## 6. M4：预热、扩缩容与回收

- 默认 `prewarm=0`，controller 轮询获批 scope 的 `actions/runners/jobs`，只在发现 waiting job 后按
  job `handle` 创建 ephemeral Runner 和执行实例；
- 管理员未来显式配置预热时，每个启用 scope 最多维持一个尚未启动 Runner 的预热实例；
- 按队列压力增加独立执行实例数量，受 Incus project 和 scope quota 双重限制；
- janitor 处理排队取消、启动失败、超时和 controller 崩溃；
- 对已经启动 `one-job --handle ... --wait` 但尚未领取任务的执行实例设置 10 分钟 waiting TTL，超时
  后注销 registration、销毁执行实例/root disk，并对同一 handle 退避；
- 外置 cache/registry 使用仓库范围短期凭据，不持久化跨信任域 workspace；
- **state 丢失后仍能收敛（`R-046`）**。2026-10-30 已补齐周期性 `Reconcile` 与 `CleanupAll` 的
  实例清单及分页 registration 核对，签名绑定 scope/租约后才能回收孤儿，已登记实例受保护。
  旧无签名 registration 丢失 state 后仍不能安全认领，须人工核对；`forgejo_actions_state`
  重建时继续保留，升级、改 scope/租约/口令前先排空。原始缺陷与验证见 §13—§14。

正常、失败、取消和 controller crash 四条路径都不得残留有效 runner token、执行实例或可挂载 volume。
空队列连续两个调和周期后不得存在 Runner registration、Runner 执行实例或临时 root disk；30 分钟空闲
验收记录 controller RSS、平均单核 CPU、每 scope API 频率和 Actions 关闭时的 Forgejo 对照基线，门限
分别为 128 MiB、1% 和每 10 秒至多一次。

## 7. M5：发布门禁

- PostgreSQL、amd64；ARM64按2026-10-30用户确认延期，不阻塞本轮；
- LLNG/Casdoor OIDC 浏览器登录、Group 准入/拒绝、管理员降权、IAM-down；
- HTTP/SSH clone/push、LFS、Package、备份恢复；首发不要求未发布旧版本兼容，发布后再验已发布基线升级回滚；
- repo/org scope、网络隔离、资源限制、token 清理和执行实例回收 E2E；两档隔离各跑一遍；
- 单次切换 `forgejo.actions_enabled` 同时改变服务端和 Runner，且不存在第二个 Runner 开关；
- custom Git Hooks 与 local import 开关的安全回归。

门禁分两组，按 `R-054` 分别结算：纯代码托管的那组（数据库、架构、OIDC、Git/LFS/Package、备份恢复、
适用的已发布基线升级回滚、两个高风险开关的安全回归）全部通过后即可评估应用 Module 的 `release`；Actions 的那组
（`R-052`、`R-053`）未完成时，Actions 不得标为可用能力，但不阻塞应用 Module 发布。当前 Runner
执行组件是 `forgejo` 自己的 Compose service，不是第二个 Module——"独立 Runner 包"只是允许的打包
形态，不是已存在的交付物。

## 7.1 M6：外部自动化消费者的边界

由 [AI Agent 编排](../../../ai_agent/dev-docs/plans/ai-agent.md) 驱动的三项依赖，本 Module 只负责“不妨碍且可声明”，不实现 Agent 逻辑：

- [x] 确认 `forgejo admin auth add-oauth` 有 `--group-team-map` 与 `--group-team-map-removal`
      （2026-09-20，固定镜像 `15.0.7+gitea-1.22.0` CLI 实测，报告在 `test-env/reports/forgejo-cli-probe-20260920.md`（报告目录不入库））。`R-060`、本里程碑
      与设计 §2.2 的"组变更在下次登录到达 team"都成立，可以继续实现；
- [x] OIDC reconcile 增加声明式的 group→team 映射（`--group-team-map` 与登录时移除），映射内容来自
      消费方配置，Module 不硬编码组名；2026-10-30 新增 `oidc_group_team_map`，加入、更新、清空与无效输入均有单元覆盖；真实 IAM 验收独立记录；
- [x] reconcile 明确不触碰由管理端 API 创建的自动化账号、token 与 SSH key，并补单元测试守住。
      2026-10-30 `TestLocalAdminRefusesToAdoptOrMutateAnUnmanagedAccount` 钉住同名外部账号不得引起任何 CLI/HTTP 写操作。
      调用链复核：`ensureLocalAdmin` 遇到同名但凭据不符的既有账号时返回错误，
      `cleanupLocalAdmin` 只回滚它自己刚创建的账号；controller 账号另受 ID/凭据签名绑定保护，
      不调用用户 token/key 删除端点。旧记录中的测试缺口现已补齐；
- [x] 文档写明系统 webhook 与管理凭据的归属：Forgejo Module 不注册业务 hook、不清理他人 hook，
      管理凭据的持有方与审计要求由消费方 Module 声明；
- [x] 同步中英文 README 与技术文档。

验收：要求文档 `FORGEJO-R-060`—`FORGEJO-R-062`，2026-10-30 当前固定基线通过；详见 §16。

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

- [x] 2026-10-30 按[互操作基线](../../../../docs/developer/forgejo-interop.md) §4 在固定 17.0.1 跑 CLI/API/真实会话探针，结果见 §14；
- [x] 复核[设计 §2.3](../../../../docs/architecture/forgejo-module-design.md) 四点；未出现足以恢复双链路的
      LDAP 不可变 ID/组同步、按 claim 绑定已有账号或认证源/会话撤销接口，保持 OIDC-only；
- [x] `prohibit_login` 已实测拒绝既有受保护 Web、API token 与 Git HTTP/SSH 请求；这不删除 token/key，
      永久销毁凭据仍须显式吊销，不能将登录拒绝等同于凭据已销毁；
- [x] 结论已写回设计 §2.2/§2.3 与互操作基线 §1；第 1、2 点同时成立才评估恢复双链路，并重新走
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
| R-043 | `server-forgejo-onejob-e2e.py --idle-baseline` | Incus + 空队列两个实际调和周期 | 2026-10-31 | 通过；117次零资源检查，见§17.2 |
| R-044 | `server-forgejo-onejob-e2e.py --idle-baseline` | Incus + 30分钟controller；60秒Actions关闭Forgejo基线 | 2026-10-31 | 通过；RSS 19.3MiB、CPU 0.30%、队列约15秒，见§17.2 |
| R-045 | `server-forgejo-onejob-e2e.py --waiting-ttl` | 真实17.0.1/PG任务未领取；合成host端点不可达控制 | 2026-10-31 §17.2：600s TTL，622.688s回收，30s退避 | 通过；外层正常关机、清理及10项宿主基线通过 |
| R-060 | `server-forgejo-oidc-e2e.py`、`server-forgejo-iam-e2e.py` | 协议探针 + 真实 Samba/Casdoor/LLNG 目录组变更及浏览器 | 2026-10-30 | 通过；基线及证据见 §14—§16 |
| R-050 | `server-forgejo-iam-e2e.py`、`server-forgejo-postgres-upgrade-e2e.py`；归档内普通 CLI 备份夹具 | PostgreSQL、amd64 | 2026-10-30 | 本轮首发amd64范围通过；普通CLI apply/hosting/一致备份恢复见§16/17；用户豁免未发布旧兼容，ARM64延期 |
| R-051 | `server-forgejo-iam-e2e.py`、`forgejo-iam-native.mjs` | LLNG/Casdoor 真实浏览器 | 2026-10-30 | 两种 Provider 均通过，见 §16 |
| R-052 | `server-forgejo-core-actions-e2e.py --standard-host --resume-standard` | 真实标准主机流程 + CLI/Hook/Compose/Secret Store；17.0.1/PG/amd64容器Runner | 2026-11-01 | 通过；真实作业、运行中关闭及账号撤销、重新启用、普通CLI停止、QEMU退出与10项宿主基线，见§17.12 |
| R-053 | `server-forgejo-onejob-e2e.py`、`--container-build [--org-scope]` | Forgejo17.0.1/PG + amd64容器/VM两档 | 2026-11-01 | 本轮amd64范围通过；两档五场景、repo/org真实无Secret构建与回收，VM最终r13及宿主不变见§17.14；ARM64按用户确认延期 |

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
| 本计划 §4（M2） | 要点列表与「当前完成/剩余」改为只叙述消费者接入，Provider 工作引用 [Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md) | 已完成（2026-09-26） |
| [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §1、§3、§4、§6 | 承认 `incus_container`/`incus_vm` 两档，默认档为系统容器，并写明两档的威胁模型差异 | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | 隔离档披露（`R-025`）与控制面账号披露（`R-069`） | 已完成（2026-09-20） |
| [Forgejo 互操作基线](../../../../docs/developer/forgejo-interop.md) §1、§3 | 开启 Actions 的实际前置校验项，以及 `--group-team-map` 等未经探针证实条目的标注 | 已完成（2026-09-20） |

## 11. 当前阻塞

2026-11-02最新状态（历史失败及旧结论保留在§13—17.16）：

- 本轮amd64的R-050/R-051/R-052/R-053已通过；container/VM Runner固定字节签收、命名目录及Module包完整性/反例通过，待发布包和摘要见§17.15。ARM64按用户确认延期，不再作为本轮阻塞。
- 完整Core使用的Samba DNS修复已完成原Dockerfile全量构建、相同镜像健康观察及完整Core r5复验，见§17.16；此前仅增量镜像的交付限制已消除。
- 用户选择只把待发布包保存到ln，不进行公开发布。尚未执行image-release最终Module revision计算、对应OCI registry上传或GitHub Release；工作树未提交、未整体冻结为正式发布提交。不以包内摘要冒充加密签名或已发布身份。
- R-046首发签名孤儿路径与R-060两种IAM映射已闭环；旧未发布兼容已明确不适用。R-069的站点管理员权限偏差按要求继续披露，不把未实现的按scope授权宣称已交付。网页取消不等同本轮SIGTERM取消，未另行新增其为发布门禁。

## 12. 明确排除

以下项目不属于剩余工作：

- Forgejo LDAP 用户/Group 预配（恢复的唯一入口是 §7.3 M8 的升级复核）；
- Forgejo SAML；
- `anasIdentityAnchor` claim/reconciler；
- LDAP 预建用户与 OIDC 自动合并；
- global runner、宿主 Docker socket 和 ANAS 宿主 privileged DinD；
- 独立 `runner.enabled`、`forgejo_runner.enabled` 或要求管理员二次启用 Runner Module。

## 13. 2026-10-30 代码与本机回归复核

基线为 `master a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加当前工作树，Go 1.26.6 / darwin arm64。
审阅发现见[联合评审](../../../../dev-docs/reviews/2026-10-30-forgejo-incus-release-readiness.md)。本轮未修改产品代码，
没有生成正式发布制品，不改变 M2—M6 的里程碑状态，也不替代既有实机验收。

| 需求或范围 | 本轮结果 | 限制与下一步 |
| --- | --- | --- |
| R-026 镜像一致性 | 当前 Provider 调用链已实现；`TestEnsureRefusesWrongImageBeforeRegisteringTrust` 的 fingerprint、architecture、type 反例通过，§4 检查项改为完成 | 尚未交付并验收四种真实 Runner 镜像 |
| R-046 状态丢失回收 | **失败**：临时 Go overlay 的 `TestReviewStateLossReclaimsOrphanWhileEnabled` 在两轮正常调和后观察到 `lists=0 deleted=[]` | 修复常规对账及注册回收；不将保留 state 的 SIGKILL 通过记录当作本项通过 |
| R-060 group→team | 代码缺口确认：输入及 `ensureOIDC` 均没有映射/自动移除参数 | 完成既定映射及其单元/实机验收 |
| R-067—R-070 | 现有 helper、Hook、controller 回归通过；站点管理员角色仍保留 | 只说明现有测试通过，未新增账号/权限真实验收 |
| R-044、R-050—R-053 | 本轮未执行 30 分钟空闲基准、双数据库/双架构业务、IAM、备份升级或完整 Actions E2E | 保持未验收；R-051 先与现行 Provider 范围对齐 |

实际执行：

- `go test -count=1 -json ./modules/forgejo/...` 随相关包测试执行，`hook`、`forgejo` helper、
  `actions-controller` 三包通过；相关 12 包 `go vet` 通过。
- `python3 -m unittest discover -s test-env/scripts -p 'test_forgejo*.py'`：44 项通过。
- Forgejo/Incus 模块及 `cmd/anas`、`cmd/anasd`、`cmd/anas-helper` 的 Linux amd64、arm64 静态交叉编译通过。
  这不是目标平台执行或镜像构建。
- 联合范围共有 16 个 Go 包：15 包最终通过，`internal/runner` 有 3 个顶层测试在 macOS 因
  `managed temporary storage requires a local Linux filesystem` 失败；详情见 Incus §16。
- `docs:check-requirements` 的需求归属检查通过，随后测试目录校验因邮件反例未登记失败；
  `check-shared-build` 因邮件镜像缺 `internal/application` 输入失败；`check-upgrade-tests` 因 LLNG
  版本不一致及邮件缺条目失败。三者均为当前全仓阻塞，不是这两个模块已被证明存在的业务故障。
- 收尾复查时邮件反例已登记，需求测试目录仍因 `MAILMOD-T-007` 的 `implementation_digest` 与
  `MAILMOD-T-015` 的 `requirement_digest` 过期失败；共享构建和升级目录复查仍为上述错误。
  当前门禁原因以此次结果为准，未修改这些其他工作中的文件。
- Module 文档生成一致性及需求/计划索引、文档状态检查通过。未运行全仓 `go test ./...`、全部 CI、
  VitePress 完整构建或真实业务浏览器测试。

证据在本机 `/tmp/anas-forgejo-incus-review-20261030-evidence.tar.gz`，SHA-256
`7bc94e648851a86aaa20253d3a7c9ef5d449bfe72edf67ebd3f42b867e2910fb`；包含测试输出、临时反例和源码摘要清单。
清单 `/tmp/anas-review-source-manifest.json` 覆盖相关路径的 691 个已跟踪及未跟踪文件，SHA-256
`3c38a411b47db9139e4c2f4ed3e5603d0c4877b139b73b768acbaf5676103701`，记录本轮文档回写前的工作树；
不是完整源码归档或共享持久证据库，正式验收需另固定源码及制品并保存到可共享位置。

结论：保持 `developing`；先修复 R-046、完成消费方映射和候选版本门禁，再补已确认范围内的真实发布验收。

## 14. 2026-10-30：17.0.1 候选、PostgreSQL 收敛与修复验收

状态：本轮修复与隔离探针已完成，保持 `developing`；下列未执行项仍阻止发布，不将局部通过等同整体验收。

用户明确确认本轮 Forgejo 仅支持 PostgreSQL；旧 MySQL/MariaDB 配置明确拒绝，不自动迁移、改绑或
删除数据。R-001/R-002/R-050 随确认更新，当前 IAM 矩阵按已批准目录范围使用 LLNG/Casdoor。
版本按上游下载页固定为 `17.0.1-r1`；15.0.7→17.0.1 跨越 v16/v17，回退须恢复数据库、应用数据、
Secret Store 与部署元数据的一致备份，不能直接用旧二进制打开新 schema。上游 v17 的 Actions webhook
改用 `workflow_run`、镜像迁移的 SSRF 限制及 Git hooks 变化已复核；Module 默认禁用自定义 Git hooks。

实现复杂度为中等：复用既有 controller、Secret Store、scope API 和队列，没有新生产服务或同步副本。

| 要求 | 本轮实现与已执行验证 | 尚未完成的边界 |
| --- | --- | --- |
| R-001/R-002 | 固定 17.0.1-r1；manifest/Hook 拒绝 MySQL/MariaDB；无效选择在生成 Secret 前失败，render 独立拒绝；Core 核对既有/保留 Resource 与当前部署的引擎，拒绝 `auto` 隐式改绑，冻结重放也复核 | 历史 MariaDB 不迁移，必须保留原部署与数据 |
| R-046/R-068 | 周期性与关闭路径都枚举 lease 实例和获批 scope 的分页 Runner；签名绑定托管口令、scope、sandbox；归属反例、活动 state 保护、分页截短/重复/失败及新供给阻断单元通过 | 15.0.7 未签名且 state 已丢失的孤儿仍不能安全自动认领，不把此边界算成已完成；升级/改 scope/改 sandbox/口令迁移前须带 state 排空 |
| R-060 | Hook→stdin→helper 已传递显式 JSON 组映射及登录移除；加入、更新、清空、无效输入单元通过 | 删除映射不追溯撤销旧成员；真实 LLNG/Casdoor 目录与浏览器矩阵另验 |
| R-061/R-062 | 同名外部账号拒绝接管且无写操作；业务 hook/token/SSH key 仍归创建者；双语文档明确归属 | 不代替消费者自身的凭据管理验收 |
| R-050/R-066 | 使用 finance 独立 Docker/网络环境执行 PostgreSQL 升级与真实 API/CLI 探针 | amd64 PostgreSQL 真实升级/恢复和身份探针通过（详见下文）；ARM64、完整 ANAS apply 与 IAM 矩阵不由该夹具覆盖 |

本地：Forgejo 三个 Go 包、jobexecutor、hostaction、构建检查器回归通过；相关 `go vet` 通过；
Forgejo Python 44、Incus Python 113 个测试通过。Incus 同步触发先红后绿的证据见 Incus 配对计划。
全量 `go test ./...` 会扫入 Git 忽略的历史邮件源码工件，出现缺少上下文的编译失败；另观察到
`modules/bookkeeping` 尚只有需求文档而无 `module.yml` 导致 inventory/docgen 失败，以及外部应用目录
严格性测试失败。它们均保留失败状态，不通过缩小命令范围宣称全库通过。

已修复升级目录中的 Forgejo/LLNG 版本元数据和 Mail 首发登记；共享构建检查改为实际 COPY 输入及
对应 Compose Dockerfile，避免把未复制的其他程序依赖误报为缺失。检查器单元与静态门禁通过。
Mail 测试清单的实现摘要仍随其他工作变化，未未经核对刷新通过状态。

R-002 的 `auto` 反例在修复前实际失败：ready、retained、仅当前部署记录三种旧 MariaDB 状态均被接受，
损坏 Resource state 也被当成新资源。新增引擎绑定检查后，凭据生成和 Provider 写入前拒绝这些情况；
同引擎保持通过。定向 Runner 数据库/资源/维护测试通过，原始红绿日志为
`/tmp/anas-db-rebinding-before.log`、`/tmp/anas-db-rebinding-after.log`。

文档生成在 `/tmp/anas-forgejo-docgen` 的当前源码副本完成，仅省略尚无 manifest 的 bookkeeping
需求草稿目录，再回写本任务的生成输出。当前工作树的全局 docgen 门禁仍失败，不将该辅助生成算成通过。

服务器隔离目录：`/home/whl/anas-forgejo-1030`，Docker socket `/run/anas-fj-1030-docker.sock`，
netns `anas-fj-1030`；生产 Docker/Incus 配置没有被用作测试目标。第一次夹具启动探测认证头错误、
第二次启动等待不足 120 秒的失败记录分别保留在 `fixture-attempt1` 与 `fixture-attempt2`。
最终夹具使用有界 600 秒启动窗口；恢复时重建独立测试数据库，避免保留新版新增表。LFS 请求媒体类型、备份认证及恢复夹具修订均保留初始结果与重试日志。


### 14.1 最终实测结果与升级检查表

| 需求 / 检查 | 结果 | 限制 |
| --- | --- | --- |
| R-050 / 升级恢复 | 15.0.7→17.0.1、恢复完整 SQL/数据/配置到 15.0.7、再升级均通过；每阶段 Git HTTP/SSH、LFS、通用包数据一致 | 真实 amd64 PostgreSQL；镜像为官方运行层加本轮静态 helper，不是完整构建流水线或 ANAS apply |
| R-060 | 真实 OIDC JIT、team 加入/登录移除、管理员降权、手工 team 保留、映射清空通过 | 测试 issuer，不等于 LLNG/Casdoor 或目录递归组验收 |
| R-046/R-068 | 真实 17.0.1 的 repo/org scoped API 上，两轮正常 Reconcile 回收签名孤儿注册，保留手工注册 | compute 为替身；不替代真实 Incus instance/root disk 回收。旧无签名孤儿仍未安全闭合 |
| R-066 / FJPROBE-T-001/002/003 | 最小 prohibit_login PATCH 可用；既有 Web 会话收到停用页（HTTP 200、私有文件不可读），API token 403，Git HTTP/SSH push 失败；login_name 等于 sub | 只证明停用期间阻断，不证明 session 或凭据已销毁 |
| R-066 四点复核 | LDAP CLI 无不可变 ID/组同步入口；OAuth CLI 无按任意 claim 合并既有 LDAP 账号入口；实例 Swagger 和候选 API 探针无认证源/独立会话路由；v17 Web 路由未发现 IAM logout receiver；新版 admin token 创建/删除实测通过 | OIDC-only 结论保持；旧 15.0.7 token API 不存在结论不再套用 |
| FJPROBE-T-004 | 稳定 sub 下改变用户名/邮箱 claim，用户 ID、旧名、旧邮箱及仓库不变 | 真实目录→LLNG/Casdoor 的改名链路未执行，不能签收整项 |
| 本地化 | 固定 v17.0.1 的 LANGS/NAMES 与现有映射一致，版本/revision/reviewed_at 已同步 | UI 协商、DST、非 UTC、未知语言及偏好矩阵未执行 |
| Secret / 运行生命周期 | 稳定 Secret、无效数据库选择、外部同名账号不接管、参数/stdin 及引擎改绑保护回归通过 | 未执行完整 credential rotate、重复 apply 和迁移中断恢复矩阵 |

真实 Runner API 首次 org 子项因总计 45 秒预算耗尽失败；repo 已通过。夹具总预算改为 180 秒，
不改变产品及单次 HTTP 超时，最终两项均通过（94.14 秒）。原始失败与最终日志分别为
`/tmp/anas-fj-runner-recovery-real.log` 和 `/tmp/anas-fj-runner-recovery-real-final.log`。

相关 Go、race、vet、Python 44/113、Linux 双架构交叉构建，以及 Linux 同步队列、watcher、
数据库资源保护和三个 Runner 受限平台用例通过。原生 processgroup 连跑 20 次通过。
共享构建、升级目录、Contract 文档生成一致性、需求/计划状态索引检查通过。
全仓 `go test ./...`、正常 Module docgen、VitePress 构建和测试案例目录仍未全绿：
bookkeeping 无 manifest、历史邮件源码工件被 Go 扫入、外部 catalog 严格性测试、邮件案例摘要不一致等
错误按各日志保留。辅助副本生成不代替工作树门禁。

发布结论：**未达到可发布状态**。M5 纯代码托管仍缺真实 LLNG/Casdoor、ARM64、完整 apply/轮换/
本地化和升级中断矩阵；Actions 的正式四目标镜像、真实执行/回收及空闲资源门禁另按 M2—M4/M5 补齐。
未合并、提交或发布制品。

### 14.2 基线、证据与清理

源码基线为 `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加本任务工作树；收尾源码清单覆盖 1141 个
实现/共享依赖文件，SHA-256 `04aa2391c2969564f717963f22bd33ddf9c341fb4c113b52efec36f0006f46a6`。
源码快照 SHA-256 `a44b6989bdabc1b675e56e0e2a809a43616cabbe3e7209b30d3fe9ab102e0b7f`。
工作树另有并行改动，未冻结为发布基线；实际执行 ELF 的摘要独立记录，不能把快照当成全库已验收。

| 工件 | SHA-256 |
| --- | --- |
| 官方 17.0.1-rootless 镜像 digest | `5ef02fbcf22beef56b8c844952ffad58cfe177b6f98e46e6abb5df90b88dc8d3` |
| 本轮生产 entrypoint helper | `143a6694177a66bd3718eeb57dc1a7f4ad81d52ef58fbedd720bb4016f7a4623` |
| 最终真实 Runner API 测试 ELF | `bc8c7d9f3585dd652fe9b379bc8311ac64dd7af47bd110c9d2306f04073a774c` |
| 最终数据库/Runner 测试 ELF | `fc9ab7e92ca1d69e28537fc5a0eca2eb223736b91c7eb1fd431c1ecb4dd02da6` |
| Linux jobexecutor 测试 ELF | `a608ec7f368c44bc32dc6ca1bd6079f372447134d8f1a26c0fec273a87173f9a` |

本机证据归档：`/tmp/anas-forgejo-incus-fixes-20261030-evidence.tar.gz`，SHA-256
`95966107c39e5ec2d8d0e3056495b8612301175784603a0dd6ac4eea5abc2e93`。
持久副本在 `whl@finance.hlong.wang:/home/whl/anas-evidence/forgejo-incus-20261030/` 的同名文件，
包含源码快照、源码/工件摘要、上游固定源码、失败与最终日志、真实实例 Swagger/CLI/协议结果。
同目录的 `server-private-fixtures.tar.gz` 保存完整隔离夹具、测试数据/配置及 ELF；SHA-256
`cff23b9177f54041a2e430e1c3d619b3132b6b1c9cae6e6c54edf2a2490bbe1c`。该私有归档含合成测试凭据，
目录 0700、文件 0600，不进入 Git 或公开报告。

独立容器、卷、Docker/containerd、netns、网络规则及镜像缓存已清理；两 unit 均为
inactive/dead、MainPID=0，专用 socket/netns 已不存在。生产 Docker/Incus 未作为测试目标。
最新全库需求归属检查通过，但 Mail 案例 T-006/007/015/016/017/019/021/022/080/082 摘要过期且
`test_mail_runtime_runners.py` 未登记；以归档中的末次日志为准，未盲目刷新其他工作中的摘要。

下一步：保留 developing；先完成全仓门禁，再用正式制品补 M5 的真实 IAM、双架构与完整生命周期，
Actions 和 Incus 的剩余 guest/网络/卷矩阵继续按各自配对计划执行。


## 15. 2026-10-30：继续收敛全仓门禁与真实 IAM 联合验收

状态：执行中，保留 §14 的通过基线和发布阻塞。

- 目录配置失败是旧反例错误拒绝现行已支持的 `fr`；修正为 `it` 并补法语正例，config/appcatalog 回归通过。
- `test-env/reports/go.mod` 为生成证据建立独立模块边界；历史源代码保留原路径，root `go list ./...`
  不再把其中 18 个归档包当成当前应用源码。该目录仍为忽略的测试工件，未缩小当前实现的测试范围。
- 全库重跑发现 Mail 两个 Traefik HTTP 入口漏标客户端地址处理方式；其服务不按转发地址授权，
  Roundcube 已关闭 IP 会话绑定和登录 IP 日志，因此显式采用 edge，沿用 Traefik 访问日志。
  Traefik 全路由检查修复后通过。相关源码文档同步，未放宽代理信任清单。
- 沙箱限制引起的 socket 失败另在允许本地监听后复跑；cmd/anasd、Incus Provider 和两个网络探针已通过。
  bookkeeping 无 manifest 仍是当前工作树门禁失败；不删除草稿或伪造 Module 清单。

新的真实 IAM 夹具位于 finance `/home/whl/anas-fj-iam-20261030`，独立 socket/netns
`anas-fj-iam-1030`、子网 `10.253.31.0/24`。只打包 Forgejo、PostgreSQL、Samba DC、Casdoor、LLNG、
Traefik、Lego 七个完整依赖 Module，使用当前源码交叉构建的标准 prebuilt Hook 和 CLI；
这不是全仓 docgen 通过记录。真实 CLI init 和 render 已成功，正在执行正式 Forgejo Dockerfile 构建。
生产镜像缓存仅只读导出到独立 daemon，未复用生产目录或凭据。结果待实测完成后按 R-050/R-051 回写。

### 15.1 本轮联调发现与修复

R-060 的自定义组在 Casdoor 原先只管理准入组/目录组的情况下不会进入 `groups` claim。
Forgejo 现在声明可选 `CLAIM_GROUPS` JSON 数组，包含管理员组和 team 映射键；Casdoor 将其加入
既有目录 Group/Role 投影，不加入 `ALLOW_GROUPS`、Application Permission 或自动创建 AD 组。
非法名字/JSON 在渲染前拒绝。真实 CLI 渲染确认 `forgejo-reviewers` Role 存在、准入权限中不存在。
新增/清空 team map、claim 与准入分离、无效元数据反例，以及两个 Hook 的 race/vet 通过；
这仍不是 R-051 真实登录通过记录。

连续三次完整 apply 均在 Samba health 阶段失败并执行补偿，未激活 Forgejo/Casdoor。
克隆失败数据的诊断确认目录初始化完成，DNS 在 canonical DC 检查失败：配置为 `FJIAM`，
实际 provision 却继承容器主机名 `fengoffice`，计算机对象与原生 DNS 为 `FENGOFFICE`。
修复首次 provision 显式传入配置的 DC 名和 IP；测试先红后绿，不放宽 health、不原地更改已有目录。
原失败数据保存在 `casdoor-failed-first-provision`，诊断克隆单独保留，新的 `casdoor-work` 重新 init/render。

Forgejo 正式 Dockerfile 的 amd64 镜像构建成功，OCI index 摘要
`8bac412efcdb84c9c39043d243caf8c0b6985d70a023e437e4f838dac8bc2535`；
Playwright 1.62.1 工具镜像摘要 `dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e`。
Samba 正式构建成功，OCI index 摘要 `deb2d9c936cf23da2568237c1cdce2a0014683e387b70c5ec80f4c4a8605771b`；
LLNG 正式构建成功，OCI index 摘要 `0d77f570fbe5c621a57d1de9d76e2022e251485a170295ed7435039b83eb043c`。
真实 IAM 两种 Provider 的浏览器矩阵尚未执行。
浏览器夹具检查凭据只提交到 IAM origin，使用本轮叶证书 SPKI，拒绝沿用上一个 case 的结果文件。

后续 v2 apply 因同步 Hook 源码后的旧 bundle lock 被安全拒绝，显式更新锁后进入 v3 冷启动。
v3 实测计算机对象为 `FJIAM$`、`dNSHostName=fjiam.fj-iam.test`，DNS 解析到 `10.253.31.2` 且
`anas-zone.ready` 已发布，命名修复生效。但受限服务器上目录结构初始化仍在执行时，90 秒启动
宽限期加失败重试已耗尽；域密码策略写入成功，尚未到结构 ready，最终失败补偿。
新候选将启动宽限期改为 180 秒，保留全部 readiness、30 秒间隔和 6 次失败；旧目录保存在
`casdoor-failed-cold-window-v3`，用另一个全新目录测试，不能用重试预热数据替代冷启动。

v4 新目录冷启动已通过 Samba、anchor、Traefik、PostgreSQL 和 Casdoor 的真实 health；
计算机对象和 DNS 均使用 FJIAM。180 秒宽限期的冷启动检查通过，未删改 readiness。
Traefik 实际挂载为 `/run/anas-fj-iam-1030-docker.sock`，不是生产 socket。
整栈随后在未预构建的 Actions controller 镜像供给阶段失败并补偿：候选 tag 尚未发布，自动
构建访问 Docker Hub 返回 EOF。关闭 Actions 仍需要该镜像执行禁用清理，这是现有设计；
不将供给失败误记为禁用开关错误。正在用正式 Dockerfile 和同源 shared context 构建 controller，
再从保留数据重试；真实 IAM 登录、Git/LFS/包 smoke 仍待执行。

controller 正式构建首次又实测失败：`state_read_unix.go` 使用 `golang.org/x/sys/unix`，而
Dockerfile 在空缓存中直接 `GOPROXY=off` 编译。现在先经已声明的 `GO_MODULE_PROXY` 下载
go.mod/go.sum 固定的 x/sys，再离线编译当前 shared 源码；没有增加新的运行时依赖或关闭校验。
原始失败 `/tmp/anas-fj-iam-controller-build.log` 保留；修复后正式 Dockerfile 的 amd64 镜像构建成功，
OCI index 摘要 `b123f4e8a65ef7d03546d4bf04b65373376767ad16d08dfcd7291046ce0c4e89`。
同源 shared context 归档 SHA-256 为 `36f5ce0e8a4c24842bd71f643a9e0758e0ec7dc8f5804fcb88e8bc06a0cefd35`，
日志 `/tmp/anas-fj-iam-controller-build-fixed.log`；controller 离线编译及 Incus CLI 编译均真实执行。

v5 普通重试被 `postgres_restore_required` 拒绝：v4 第一次 apply 在写入 Casdoor PostgreSQL
Resource 后失败，尚无 active deployment；当前保护不能从该 Resource 记录证明保留数据与新候选的匹配关系。
未绕过校验、删除 Resource state 或修改 active。现场移至 `casdoor-failed-first-pg-retry-v5` 保留。
这是一条已观察到的首次安装恢复限制，不能把另外新建测试 workspace 记成该故障已恢复。
若要支持安全重试，需要另行明确首次写入来源、匹配镜像与冻结候选的校验/失败处理，不能仅按
“没有 active”放行历史数据。新增恢复路径的范围和方案尚未确认，当前不修改此保护。

v6 在另一个全新 workspace、全部所需镜像已预构建的条件下重新 apply，继续 R-050/R-051 的
独立验收；该结果不用于豁免上述恢复限制。controller 镜像的 Incus CLI `--version` 真实输出 `7.3`。

v6 在 Forgejo readiness 阶段失败：生产 helper 请求 `/-/healthcheck`，实际 v17.0.1 返回 404。
固定上游源码 `routers/web/web.go` 和运行实例均确认端点为 `/api/healthz`；真实返回 HTTP 200、
`status=pass`，包含 database/cache ping。helper 改用该端点并要求成功 JSON，避免把 HTML 或失败
JSON 的 200 当成就绪；路径回归先红后绿，503/失败 JSON/HTML/缺失状态反例通过。正式镜像重建中。
此前 §14 的 runtime fixture 只以 API version 判断启动，不覆盖这个生产健康检查，不能外推为
完整 Module 启动通过。

Casdoor dirwatch 的 unhealthy 原因为测试映射组尚未创建：`managed group forgejo-reviewers resolved
to 0 LDAP entries`。生产声明要求组已存在，不自动建 AD 组；夹具增加独立的 Samba-ready 建组步骤，
在 IAM 启动前准备带所有权标记的测试组。拒绝用户也加入该 claim-only 组而不给 APP 准入组，
以真实登录验证不会扩大准入。以上准备不计作生产缺陷修复或 R-051 通过。

v6 的候选停止补偿也失败：controller 因应用未健康而停留 Created，旧 Hook 调用 Docker stop
不能得到已执行清理的退出状态，因而保留应用及依赖。测试先对禁用、无 compute endpoint 的已观测
进程执行一次真实清理，确认两次退出 0 后人工停止本轮容器；这是手工隔离清理，不算 Core 补偿通过。
原始 `candidate_stop` 错误与清理 receipt 保留。

产品 Hook 增加 Created + disabled 专用路径：按固定 ID 启动既有清理进程，等待真实成功，再核对同一 ID；
启用模式、已有失败退出、替换、超时和取消仍拒绝。新增单元先红后绿，Hook 全包通过；服务器上同一
新建的隔离容器中，旧 Hook 失败并保持 Created，新 Hook 成功且容器退出 0，见
`/tmp/anas-fj-iam-created-cleanup-native.log`。新 Hook ELF SHA-256
`2eacddbefb64da1cf8850146f6f33a18b23cb910b15f1ce411a4c750902cf6bb`。

健康探针修复后的正式 Forgejo amd64 镜像 OCI index 为
`894ebdf369d8a812dc8476bd1ea5bb81273bb3bdbdf67c5cc590ea97450b3119`；
v7 使用新镜像与新 Hook，在全新 workspace、提前建好目录测试组的条件下重新执行完整 apply。

v7 中 Forgejo 主服务新探针、Casdoor 主服务和 dirwatch 均已健康，但 Forgejo `after_start` 的
OIDC CLI 配置失败，完整 apply 未通过。此次 `candidate_stop` 由 Core 正常执行并返回 succeeded，
容器清理完成；没有人工补过后再算补偿成功。首次建组夹具的 smb.conf 路径错误独立保留，改为
`/etc/samba/smb.conf` 后在 IAM 启动前建组成功。native IAM 入口因 apply 失败保持未执行。

Samba 在整栈负载峰值多次被 Docker 以 `Health check exceeded timeout (10s)` 标为不健康；同一个
完整检查单独运行 exit 0、耗时 4.455 秒。新候选将单次预算调整为 30 秒，检查内容、间隔、失败
次数保持；Samba Hook 单元通过，新预算仍待整栈验证。原始健康时间戳/退出码与完整检查耗时保留。

为取得被生产安全错误屏蔽隐藏的 OIDC CLI 原因，使用 v7 匹配镜像和数据恢复专用诊断运行；
不更改 active/Resource state，不算普通 apply 或 R-051 验收。原始输出只存私有目录，脱敏后分析。

诊断中直接容器 helper 成功；原 Hook 实测 47.109 秒退出，固定错误为
`context deadline exceeded`，证明 45 秒预算不足这一独立问题。再次直接 helper 成功耗时
19.133 秒，表明耗时受整栈负载影响；不能据此把 v7 原始泛化错误认定为同一根因。
仅对固定 UID、入口、OIDC 子命令和合法容器名前缀放宽到 180 秒，其他普通命令仍 45 秒，
更短 parent deadline 优先，失败文本不透传。白名单正反例、实际取消、输出上限、Hook 全包、
race/vet 通过；v8 全新冷启动和原生登录仍待验证。

当前整仓 Go 回归未通过：大量 inventory/CLI 用例因其他工作中的
`modules/bookkeeping/module.yml` 尚不存在而失败；另有三个要求本地 Linux 文件系统的
Core 用例在 macOS 失败（§14 有 Linux 对应测试证据）。不删除草稿或临时伪造 manifest，
不把本轮定向测试通过写成整仓通过。日志 `/tmp/anas-fj-iam-all-go-current.log`。

新 Hook 诊断成功后，再次执行也出现 30.125 秒非 deadline 失败，直接原生 auth list/update
分别 8.735/13.227 秒成功；故原始间歇失败尚未确认已修复。Hook 仅对精确匹配的四条固定
helper 错误增加阶段分类，其他 CLI/Docker/发现响应仍屏蔽；白名单与夹杂私有文本反例通过。
BusyBox wget 的默认根校验失败、且不支持 `--ca-certificate`，这不是 Go OIDC 路径的信任
失败证据；不以该诊断命令推断产品 TLS 问题，也不关闭校验。

v8 在 Samba 创建阶段因测试手工清理遗漏已退出的 `samba_dc_events_init` 而发生容器名冲突，
Core 再次正常补偿；这是夹具问题，不计作产品缺陷。原 v8 输出与数据保留，改按同一 workspace
的 Compose working-dir 标签清理全部已退出容器后，以 v9 全新目录重新执行。

v9 使用当前正式镜像和修复后的 Hook，从全新目录重跑。测试清理额外识别 PostgreSQL
provision 的默认 Compose 名称，仍须匹配冻结 working-dir 标签、专用 DockerRootDir 且已退出；
不清理无标签的诊断克隆。当前服务器测试源码清单 `source-manifest-v9.json` 含 448 个文件，
首次摘要 `0eb5ec0eaecd1fe0e22628e5a8e77988ef38530c99f4f3c900dd92cf194bce83`；
随后仅更新浏览器按钮选择器，最终验收摘要在收尾归档时另记。
新固定失败原因的 Hook 全包/race/vet、共享构建静态检查、升级目录 26 modules、
需求覆盖 994 项和 187 个 active case、两个生成索引检查通过。整仓 Go 的未通过结论不变。
普通 CLI 备份创建/校验/恢复测试已准备，尚未执行，不提前记 R-050 通过。

v9 普通完整 apply 在 Casdoor 方案通过，已激活真实 deployment；不是手工启动补过。
全新 Samba 的 30 秒完整健康预算已实测通过；Forgejo v17 的新 readiness 返回 200，OIDC
after_start、独立恢复管理员、禁用 Actions 清理均通过。主服务无 OOM；冷数据库和 HTTP/SSH
初始化约 1 分 40 秒，保留完整日志。真实浏览器与 Git/LFS/包 smoke 正在执行，尚不将
R-051 或 R-050 整项标为完成。ARM64 实机地址已向用户询问，finance 仅覆盖 amd64。

v9 首次浏览器尝试在 finance 上触发严重内存/I/O 等待，240 秒未完成；只通过镜像/TLS与
禁用账号前置检查，没有任何 IAM 登录用例通过。已核验本轮 browser 标签/ID 后终止该
浏览器，保留原始 TimeoutExpired。主机 memory pressure avg10 一度 82%、I/O 99%。
用户随后明确授权把本轮合成测试凭据经加密 SSH 提供给本机独立 Chrome；首次下载完整
载荷被自动审批拒绝的记录保留，授权后仅取公开叶证书并准备私有回环 BrowserServer。
测试工具的远程浏览器模式不更改应用部署、IAM 或 TLS 校验；连接 token 只在私有文件和
进程环境，不能放命令行或公开日志。该模式尚未完成真实登录验收。

finance 专用 Docker/containerd 在 23:07 UTC 被正常停止，journal `Result=success`，不是
运行到期或已证实的 OOM。已按原配置恢复，但随后 API socket 在 daemon 仍 active 时
不可见，当前正在核实命名空间/路径状态；不能把恢复中的 hosting-only 失败当产品 Git 失败。

ARM64：用户提供的 QWRT/OpenWrt 设备上，Forgejo Hook 66、entrypoint 20、controller 74
个 PASS（含子用例）；controller 的 3 个原生集成探针按环境开关跳过。Incus Hook 45、
Provider 328、computeclient 157 个 PASS，后者另跳过 10 个 native 场景。未在该设备安装
Docker/Btrfs，故不能将这六个包的通过外推为 R-050 ARM64 完整应用或 Actions/Incus guest 验收。

ARM64 原始失败、可信 TMPDIR 重跑、源码与 ELF 归档为
`anas-forgejo-incus-arm64-native-20261030.tar.gz`（65,902,691 bytes），SHA-256
`3462f68ea72a9488034a5cf0967f58b9072a826589ca114fca35ad89dcd10d20`。
持久副本放到既有 finance 私有证据目录，传输摘要仍待末次日志核验。

测试 socket 已改到本轮私有根 `docker-api.sock`，保留 `/run` 兼容链接用于旧冻结 Compose；
普通 CLI `start` 成功。等待夹具原来错误假定最终 JSON 的首字段为 api_version，改用真实
最终文档后开始 v11；不修改原始 CLI 输出或伪造成功。原始恢复与夹具等待日志保留。

v11 浏览器真实提交密码到 Casdoor（password_posts=1），已得到 Forgejo 的认证资料页，
可见 Signed in as 对应测试用户名。旧探针因 Forgejo 17 profile 不再含 `input[name=email]`
而误判；失败页面保留，不记为身份缺陷。改为核对 settings 路径、只读用户名精确匹配和
full_name 资料表单后，以全新 AD 用户执行 v12，避免复用旧登录算通过。

ARM64 持久归档摘要已核验一致（`/tmp/anas-fj-iam-arm-durable.log`）。

拒绝用户的 Casdoor 原始 `/api/login` 响应为 HTTP 200 + status=error +
`Unauthorized operation`，停留 IAM origin 且没有 Forgejo 会话；短暂页面提示消失导致原
探针没识别。只将该固定结构化权限拒绝加入判定，仍要求无会话及无 JIT 用户，非泛化登录
失败。保留 v12 与单 case 原始失败；v13 使用全新用户重跑完整矩阵。

正式镜像 hosting-only 已通过：Git HTTP push/clone、SSH read、LFS 与 Package 字节往返。
原生 LFS 422 记录为 `content size does not match`；夹具 PUT 未设 Content-Type，被默认
作为表单处理，改为 `application/octet-stream` 后全部通过。原始 422、请求修复和最终日志
分别保留在 `casdoor-hosting-probe-urlencoded-lfs`、`casdoor-hosting-probe` 与
`/tmp/anas-fj-iam-hosting-octet-stream.log`。这仅补足 R-050 amd64 的 hosting 子项。

v13 已通过普通/管理员登录、目录主体、claim-only 拒绝、管理员降权、映射 team 移除与
手工 team 保留。IAM-down 时父进程确认服务已停，浏览器实际落到 IAM origin 的 404：
Traefik 撤掉路由后没有匹配项。原探针仅接受 5xx，故该次未算通过，原始失败保留。
现在仅额外接受已停止 IAM 的精确 origin 404，仍必须证明无应用会话；v14 全新用户重跑。

v14 Casdoor 完整真实矩阵 exit 0：准入/拒绝、目录 sub、管理员映射与降权、claim-only 组
到 team 映射和移除、手工 team 保留、IAM-down 拒绝新会话、恢复保持相同用户 ID，随后
Git HTTP/SSH、LFS、Package 字节往返全部通过。证据 `casdoor-probe/result.json`、
`casdoor-native-v14.log`、`casdoor-native-v14-exit.json`；本机独立 Chrome 经加密 SSH，
每次独立 context，未用个人浏览器资料。普通 Core stop 与正式 backup/restore 正在执行。
这是 R-051 Casdoor 半矩阵通过；LLNG 与 R-050 其他架构/生命周期仍未完成，不提前发布。

正式普通 CLI 备份恢复闭环通过，备份 ID `20261029T235520Z-b233ff69`，模式 snapshot，
持久目标 `/home/whl/anas-evidence/forgejo-incus-20261030/casdoor-formal-backup/`。
create/verify、restore、恢复冻结制品 start 均由正常 Core 路径完成；恢复后原正式镜像 ID
与测试基线一致，目录 login_name 保持原 anchor，Git commit
`d42ed6df9f18d47b49bc257e720f09149ad60f88`、LFS/Package SHA-256
`aed64d581208771ea7ee2e2366dbbc08c95318c7eb2c8e2e800676038e0e6336` 一致，最后普通 stop 通过。

原夹具数据复核漏发 LFS Accept，CLI 三步已通过后探针报 415；原始 exit 1、脚本与结果
保留。仅为同一已恢复工作区补正确请求头重新核对数据，没有重造数据或重做恢复；最终
`backup-restore-probe/result.json` 明记 resumed_after 和 initial_execution_exit_code=1。
后续数据复核/普通停机通过不是把原始 415 写成产品通过。恢复后 SSH 未另重跑，本轮
SSH smoke 在恢复前通过；R-050 整项还需 ARM64 和剩余生命周期，不能因本子项通过发布。

LLNG v13 普通 apply 在 health 阶段失败并正常补偿：`AnasOIDC.pm`/`AnasDirectory.pm`
在镜像中为 root:root 0600，UID33 无法读取，门户 HTTP 500。公开源码的私有 bundle
权限可保留为 0600，镜像必须明确设置运行时读取权限。Dockerfile 对公开模块/模板/SQL
固定 0644，主题目录/文件分别 0755/0644；运行时私有配置权限不变。另补声明原来漏投影的
`ANAS_TLS_TRUST_BUNDLE_NAME`，避免目录 watcher CA 文件名为空；Hook 合同测试/vet 通过。

本次重新冻结当前 LLNG 工作树与新 Hook（50 个文件，源码 tar SHA-256
`a4720dcf09a0f87fb59484cf34bd004f2fa72aecb558308e615f44444c74cc9f`），保留旧快照与
旧镜像 0d77...。新的正式 Dockerfile amd64 OCI index 为
`f79cbb658aafd35c689dfb366ba2d7d8d3d41df6c90cfe3900a6501d1ed1d7e2`。
隔离 `--user 33:33 --network none` 对插件、目录模块、lmConf 与 jq 的可读性检查通过，
原镜像同样检查失败；不是用 root 运行服务来补过。
旧失败 workspace 移至 `llng-failed-plugin-permissions-v13`，没有删 PG Resource state 或绕过
首次失败后的重试保护；新 v14 workspace 完整 apply 执行中，尚未计 LLNG 通过。

## 16. 2026-10-30：本轮最终验收结论

> 本节保留上一轮基线；用户随后确认首发不要求未发布旧版本兼容，当前范围与新增验证见 §17。

**未达到发布状态，保持 developing；M5 未完成，M6 已完成。** §13—§15 保留发现和重试历史，
本节给出本轮最终结果。没有提交、合并或发布制品。

LLNG v14 的全新 workspace 普通 apply、13 项真实 IAM/hosting 检查和普通 stop 全部通过；
Casdoor 同样完整通过。最终 `matrix-completed.json` 中两种 IAM、普通停机和备份恢复数据均为 passed。
两种 IAM 都使用真实 Samba 目录用户，分别验证登录、APP 组准入/拒绝、目录 sub、管理员映射与降权、
自定义组到 team 的加入/移除、手工 team 保留、IAM 停止后拒绝新会话及恢复保持同一用户 ID。
拒绝用户仅有 claim-only 映射组而没有 APP 组，证实 claim 投影不扩大应用准入。

| 需求 ID | 最终结果 | 实测范围与剩余工作 |
| --- | --- | --- |
| R-001/R-002 | PostgreSQL-only 已实现 | 旧 MariaDB 配置明确拒绝；Core 跨引擎重绑定守卫已回归，不自动迁移或删除数据 |
| R-050 | **部分通过，仍阻塞** | amd64 正式 apply、Git HTTP push/clone、SSH read、LFS/Package 字节往返通过；普通 CLI snapshot backup/create/verify/restore/start/stop 及恢复后目录身份/Git/LFS/Package 数据一致。恢复后 SSH 未重跑；§14 的旧版升级回滚使用上游运行层夹具，不能替代正式完整 Module 升级回滚。ARM64 完整应用尚未执行 |
| R-051 | **通过** | Casdoor/LLNG 全部既定 IAM 判据通过；各自 13 项组合检查也含 hosting 和前置检查，不能解读为 13 条独立需求 |
| R-060 | **通过** | 消费方声明 JSON 映射，加入/更新/清空/非法输入单元及两种真实 IAM 登录移除均通过；上游仅在登录时同步。清空整个映射不追溯撤销旧 team 成员，按文档人工清理 |
| R-061/R-062 | **通过，M6 闭环** | entrypoint 拒绝接管外部同名账号的单元通过；Actions 账号的 ID/凭据绑定反例通过。调用链不操作他人 token/key 或系统业务 webhook，controller API 仅限获批 scope 的 jobs/runners；中英文归属文档已更新。此处按既定单元+审阅判据结算 |
| R-066 | 固定 17.0.1 复核通过 | §14 CLI/API/会话和身份探针结果已写回设计与互操作基线；保持 OIDC-only。真实目录改名不在本轮已通过矩阵内 |
| R-043/R-044/R-045/R-046 | 未闭环 | 长时空闲/等待 TTL 原生验收仍待执行；签名 registration 的常规孤儿核对通过单元及真实 Forgejo API，但旧无签名 registration 丢失 state 后仍不能安全自动回收 |
| R-052/R-053 | 未完成 | 完整业务开关、四目标 Runner 镜像、VM/ARM64、真实构建/取消/崩溃回收和隔离矩阵仍按 M2—M5 跟踪 |
| R-054 | 发布边界保持 | 纯代码托管可先行的规则保留；其自身门禁尚未全部通过，不能据此发布 |

ARM64 实机原生六包共 690 个 PASS（含子用例）、13 个 native 集成场景跳过；目标设备没有
Docker/Btrfs，不能视为 R-050 或 Incus guest/VM 验收。原始不可信 TMPDIR 拒绝和可信目录重跑均保留，
详见 [Incus 计划 §17.1](../../../../dev-docs/plans/incus-module.md)。未在该设备安装服务或调整网络。

### 16.1 源码与制品基线

基线仍为 `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加工作树；并行修改未整体冻结为 release。
实际服务器源码、Hook/CLI、Compose 冻结元数据及全部重试保存在私有 operational archive，
最终文件清单 `final-evidence-manifest.json` 的 SHA-256 为
`d4f4bd79ce54ed9c8ae2bf03609817430d95c1b4ec6333175c7ae2c5824f5474`。
清单含 5181 个归档前文件，归档共 5182 个文件（另含清单本身），不能当作需求数。

| 制品 | 本轮实际使用的摘要 |
| --- | --- |
| Forgejo 17.0.1-r1 正式 Dockerfile amd64 OCI index | `894ebdf369d8a812dc8476bd1ea5bb81273bb3bdbdf67c5cc590ea97450b3119` |
| Actions controller 正式 Dockerfile amd64 OCI index | `b123f4e8a65ef7d03546d4bf04b65373376767ad16d08dfcd7291046ce0c4e89` |
| Samba DC 正式 Dockerfile amd64 OCI index | `deb2d9c936cf23da2568237c1cdce2a0014683e387b70c5ec80f4c4a8605771b` |
| LLNG 修复后正式 Dockerfile amd64 OCI index | `f79cbb658aafd35c689dfb366ba2d7d8d3d41df6c90cfe3900a6501d1ed1d7e2` |
| Casdoor 导入的依赖镜像 OCI index | `50456c6d25c7350fc72cbf7de4955ec5ef29acc7381802630b9003f8f1759fc8` |
| Forgejo 最终 Linux amd64 Hook ELF | `d2703f44679e133250f813f85f3d3cae1a108a38871599f36314604923d940a3` |
| Casdoor CLAIM_GROUPS 修复后的 Linux amd64 Hook ELF | `35d32fbd66b34086d3077a9153da363ba8b3f4c5fecb32eb74e3d4517a396648` |
| 正常 CLI ELF | `23c90b6ceed584e873c4780b8c4d4f3b6a53ef6d5d78441c6e19fa8a07fbfe51` |
| LLNG 本轮重新冻结的 50 文件源码 tar | `a4720dcf09a0f87fb59484cf34bd004f2fa72aecb558308e615f44444c74cc9f` |

Casdoor 使用匹配 Hook 配合导入镜像，不代表当前工作树额外的 Casdoor patch 0009/0010 已由本轮重新构建验收。
CLI 是本轮初始冻结 ELF，不能把其结果外推为所有并行 Core 修改均已验收。LLNG 使用本轮重新冻结的完整源码
与新 Hook；旧镜像、旧源码和失败证据均保留。浏览器为用户授权的本机独立 Chrome，通过 SSH 回环连接、
独立 context 和固定公开叶证书 SPKI 校验，不使用个人资料，不关闭 TLS 校验。

### 16.2 证据、门禁与下一步

持久证据根：`whl@finance.hlong.wang:/home/whl/anas-evidence/forgejo-incus-20261030/`，目录 0700、
私有归档 0600。operational archive 含合成测试凭据、私钥及失败页面，仅留服务器私有目录，不入 Git。

| 归档 | 字节数 | SHA-256 |
| --- | --- | --- |
| `forgejo-iam-20261030-operational-evidence.tar.gz` | 395946089 | `d487e7f08f79b1633248c9b113d52bb04f59037a20c06ea66bd0e363a52083fa` |
| `forgejo-iam-llng-formal-image-20261030.tar.gz` | 194899971 | `fbab173980b42f099081ed977864d253355aa007693725d4f532cb9026a21452` |
| `anas-forgejo-incus-arm64-native-20261030.tar.gz` | 65902691 | `3462f68ea72a9488034a5cf0967f58b9072a826589ca114fca35ad89dcd10d20` |
| `forgejo-iam-20261030-local-evidence.tar.gz` | 1191930 | `f682da2c4a631d81be64db2f17a59bfac877e4eeff1da410bccad25ff996d039` |

普通 snapshot 备份单独保存在该根下 `casdoor-formal-backup/20261029T235520Z-b233ff69`，
没有用 tar 复制原始 Btrfs 数据来代替正常备份。原 LFS Accept 缺失导致的探针 exit 1 与后续同一恢复数据的
复核均保留。最终结果文件分别为 `casdoor-probe/result.json`、`llng-probe/result.json`、
`backup-restore-probe/result.json`，成功结论不覆盖原始失败记录。

本机日志/相关当前源码清单（248 文件）与清理脚本、最终清理输出另存 local-evidence 归档，
本地和 finance 摘要已核验一致；该源码清单用于工作树盘点，实际执行基线以服务器归档为准。

相关 Go 包、race/vet、共享构建与升级目录门禁通过；需求归属、测试 catalog 与需求/计划索引检查通过。
整仓 `go test ./...` 未全绿：bookkeeping 草稿缺 `module.yml` 导致 inventory/CLI 回归失败，
macOS 上另有三个要求本地 Linux 文件系统的 Runner 用例失败（对应 Linux 用例已通过）。
不临时伪造 manifest 或删除其他工作来获得全绿。VitePress 全量构建、全部 CI 和剩余实机发布矩阵未执行。

2026-10-30 01:00 UTC 清理核验：本轮容器为零，专用 Docker/containerd 均 inactive/dead、MainPID=0，
socket、兼容链接、netns、veth 及测试子网规则均已移除。本机 Chrome/SSH 转发也已关闭，私有浏览器
连接文件已删除。首次清理审计发现剩余的精确测试子网代理旁路规则，保留失败日志后仅删除该条，复核通过。
生产 Docker PID 1213、Incus PID 3410176 及 active 状态保持不变。
服务器 `/home/whl/anas-fj-iam-20261030/` 仍保留私有源码、原始数据/Btrfs 快照和已停用 daemon 缓存，
没有声称完整删除夹具；备份、证据与运行服务的清理分别核对。

下一步：先解决整仓门禁，另提供具备 Docker/Btrfs 的 ARM64 Linux 验收环境，完成 R-050 正式完整升级回滚
与双架构矩阵；Actions/Incus 按各自既有需求补齐四目标交付、空闲基准与真实执行回收。

## 17. 2026-10-30：补齐 amd64 门禁与首发范围确认

状态：执行中，保留 §14—§16 的历史基线。

用户明确确认“之前未发布，不用考虑之前兼容性”。本轮因此以 17.0.1 首发为范围，
不再要求从 15.x 未发布开发版本执行升级/回滚，也不要求迁移旧无签名 Runner。
R-050 仍要求 PostgreSQL、两种架构的新安装、Git/LFS/Package 与一致备份恢复；
以后存在已发布旧版本时，再恢复对应升级回滚门禁。Manifest 的 `upgrade.from` 已收敛到
`>=17.0.1 <18.0.0`，`data_breaking` 为空；不再声明支持未发布的 15.x。
历史 15→17 协议探针结果保留，不能称为已发布旧制品升级。R-046 的当前签名路径已通过
单元及真实 API，未发布旧注册的兼容不再是当前阻塞；未知归属对象继续保留，不放宽回收校验。

新增实际缺陷：Incus CLI 首次连接会写 `last_working_address`，改写既有 JSON 配置，
导致第二次初始化被安全校验拒绝。共享客户端预填租约唯一 endpoint 后，实际重启和并发初始化通过。
随后发现无权项目的列表可被过滤为空，不能据此证明 project 准入；初始化新增精确 project
读取/名称校验，两项反例均先红后绿。证书/key 的内容、权限、链接及不可覆盖校验保持。
没有观察到跨项目数据泄露，原失败是客户端把空清单误判为初始化成功。
更详细的验证边界见 [Incus §18](../../../../dev-docs/plans/incus-module.md)。

| 门禁 | 当前结果与边界 |
| --- | --- |
| Web CI | OpenAPI 类型过期导致初次失败；使用生成器更新 `schema.d.ts` 后，API 一致性、typecheck、22 文件/106 项测试及主/应急两套构建通过 |
| VitePress | `npm run docs:build` 全量通过，不部署站点 |
| 文档/发布脚本 | 两个文档生成器、版本/需求/状态测试与检查、五项发布/安装脚本，共 14 项通过 |
| Linux 特定 Core | 三个 macOS 无法执行的顶层用例在 Linux 通过；首次因 SSH umask 产生组可写目录而拒绝，私有 umask 原样重跑通过，未放宽检查 |
| host-action | 普通 Linux 用户运行完整 CI 原生脚本 exit 0，必需原生用例实际执行，无 skip 冒充 |
| 共享客户端/controller | 全包、vet、race 通过；真实 Incus 7.0.1 + 系统 CLI，以及正式镜像内 CLI 7.3 的重启、并发、project/TLS/撤证边界通过 |
| Linux 整仓 Go | 首轮干净 CI 环境全量 test、host-action、shared-build、upgrade-catalog 全部 exit 0；test 1195.18 秒。首次 host 直跑的路径长度、umask、生产目录和无 Git index 干扰有独立失败日志；未放宽产品检查。安全依赖更新后 vet 再次通过（443.58 秒），最终整仓 test exit 0（198.51 秒），同一安全基线已有完整成功命令 |
| 正式 controller 镜像 | 首轮及安全补丁重建均通过，当前 OCI index `687b66f605f898b634afbf2eee81a26b7704e205cde1d60001689aa048342c61`；Docker Hub EOF 原始失败保留，使用上一轮镜像源重试；实际 ELF 扫描与 native CLI 通过 |

初始源码快照包含 3195 个文件，SHA-256
`3199528370b8e12c617b16d162f7c1ec78b4cf0a1c55276b9a62f229d2b59f93`，含两项客户端修复，
但生成于后续首发范围确认之前；Manifest/文档更新需另记录增量基线。
正式 controller 最小构建输入为 36 个文件/65317 bytes，SHA-256
`5ba350678df0467c33ef46e44520cfc1842c059cd955ca01f6922018c181ca03`。
镜像内 controller ELF SHA-256 `a35eec3b2aecb9b79fe8c2c8be34b664ece1b963b55c5887d5559418cb363e61`，
CLI ELF SHA-256 `3a0e6a6355715ccb8f6f276109bf2d1e801e45a799303a9ce40178308381c25c`。

安全门禁补充：固定 `govulncheck v1.7.0` 首次扫描发现 16 个可达公告，涉及 Go 1.26.6
标准库、`x/net v0.57.0` 和 `x/crypto v0.54.0`。按官方公告将最低 Go 更新到 1.26.10、
`x/net` 更新到 v0.60.0，并采用它要求的 `x/crypto v0.57.0`。使用 Go 1.26.10
重新编译的扫描器复测 exit 0、无可达漏洞；3 条仅位于依赖模块而没有发现调用路径的公告仍明确保留。
本机旧 Go 1.24 编译扫描器不支持当前源码的失败也保留，未作为漏洞复测结果。
正式 helper/controller 构建层固定 `1.26.10-alpine`；镜像内独立 Incus CLI 的同类依赖
也需修补并重建，不能仅凭 ANAS 主模块扫描通过而宣称工具二进制合格。
依据：[Go 标准库](https://pkg.go.dev/vuln/GO-2026-6809)、
[HTTP/2](https://pkg.go.dev/vuln/GO-2026-6617)、[SSH](https://pkg.go.dev/vuln/GO-2026-6354)。

其余 23 组 CI 夹具/脚本中 22 组首次通过；VM 清理探针因本机沙箱不允许监听回环端口
首次失败，在允许监听的环境原样重跑 8 项通过。包含 Runner 输入/启动、Incus 烘焙/隔离、
浏览器与 VM 清理、七项升级报告脚本、Immich 固定补丁等。原失败和重跑分别保存。
两条 Core 公开版本 v0.1.0/v0.1.1 的实际 CLI 脚本升级检查通过，与已取消的未发布
Forgejo 15.x 兼容性范围分开记录。

正式安全重建镜像 OCI index 为
`687b66f605f898b634afbf2eee81a26b7704e205cde1d60001689aa048342c61`。
实际 controller ELF SHA-256 为 `25e6dfc109d114e456daa42ce8e6c1ea011363882fd9395162e3f3b57c69051e`，
Incus CLI ELF 为 `6f6c210f197b20bbb81faaaa55f3630da222f2b15654590056983d16585677ff`；
二者以固定扫描器 `-mode=binary` 检查均无可达漏洞。使用该 CLI 的真实 Incus namespace
重启/并发/project/TLS/撤证回归通过。未把本地构建 tag 称为已发布 registry 制品。

恢复后 SSH 补充检查通过：普通 CLI 启动原恢复工作区，备份前原私钥能克隆旧仓库，
原提交 `d42ed6df9f18d47b49bc257e720f09149ad60f88` 及文件摘要
`aed64d581208771ea7ee2e2366dbbc08c95318c7eb2c8e2e800676038e0e6336` 一致；
该密钥能推送独立 `restored-ssh-acceptance` 分支，HTTP 读回同一提交。新建仓库的 HTTP/SSH
clone/push、LFS/Package 全部通过。最后正常 CLI stop，专用 Docker 运行容器为空。
这组托管检查使用原归档的 Forgejo 17.0.1 冻结制品，安全重建 controller 的基线另列。

安全依赖更新后的全量 test 首次 exit 1（1739.35 秒），失败仅为
`cmd/check-shared-build` 的 AI Agent ARM64 冷缓存交叉编译超过现有 120 秒；其余包通过。
host-action、共享输入检查与升级目录均再次 exit 0。保留原失败，受影响包随后按原限制复测 exit 0（57.96 秒），
未将该超时直接豁免为通过。SSH 中断后从服务器结果文件结算，未使用 SSH 客户端退出码替代测试结果。

受影响包原样重跑后，再次执行同一安全基线的完整 `go test -p=1 -json ./...`，
exit 0（198.51 秒）。前一轮失败与最后通过记录均保留；没有修改测试 timeout 或删除用例。
当前 Linux Go/原生 host-action/共享构建/升级目录门禁结算为通过。

独立 VM 矩阵已继续尝试：官方 Debian 13 外层 qcow2 与 Incus Debian trixie 内层
container/VM 镜像均按官方摘要校验，实际 Go 1.26.10 测试二进制另记摘要。owner 仅适配
真实 `genisoimage`、`/opt/incus` QEMU 库路径及 finance 实际 KVM group 992；
guest 内验收入口未改。首轮写死旧组号 108 导致 KVM 拒绝、尚无验收执行，QMP stale socket
使清理报告失败，原记录保留。新轮次使用独立磁盘、SSH/cloud-init 身份和端口，待实际结算，
不得把已准备/已启动计为 VM 档通过。

冻结托管镜像内的 entrypoint 另外从未启动的精确镜像中导出核验，实际编译器为 Go 1.26.10，
未把 Dockerfile 浮动 tag 推断为编译版本。安全 controller 的 Docker save gzip 工件 SHA-256
为 `5d845f9c758ee70f130d559548edb3cce7d29e7c2f783dd169577adff132b3d3`，已核验保存到 finance `/home/whl/anas-evidence/forgejo-incus-20261030/forgejo-controller-amd64-security-20261030.tar.gz`（0600，父目录 0700）。
2026-10-30 再核对[官方版本目录](https://forgejo.org/releases/17.x/)，当前 stable 仍为 17.0.1。

当前证据工作目录：本机 `/tmp/anas-amd64-gates-20261030/` 及 `/tmp/anas-amd64-*.log`；
服务器 `/home/whl/anas-amd64-gates-20261030/`。finance 专用 Docker/netns 已清理，执行证据检查点已持久归档；详情和 ln 接续见 §17.1。

Linux 整仓门禁及恢复后 SSH 数据核验已通过；R-043/R-044/R-045、完整 Actions/Incus guest 与 ARM64 范围仍未由上述检查替代。


### 17.1 finance 结算与 ln 后续边界

Linux 整仓 Go 门禁、SSH clone/push、恢复后原 SSH 密钥及原数据检查均已通过，结果见本节上文。finance container 23 项、volumes 12 项及独立网络 7 项通过；VM agent 未就绪，重试因严重换页中断，没有通过。第五轮另有宿主 portal/nft/IPv6 路由基线差异，来源未确定，保留失败；详见 [Incus §18.1](../../../../dev-docs/plans/incus-module.md)。本轮专用 finance Docker/containerd/netns/规则等已清理，私有诊断磁盘保留。

用户提供 ln amd64/KVM 主机，并要求不修改现有 Docker 容器。后续仅在私有 QEMU VM 内测试，宿主 Docker 只读盘点。ln 实际可用 RAM 约 3 GiB，足以尝试 2304 MiB 外层 VM，不能据此宣称覆盖 Actions 默认每实例 4 GiB 或完整四目标镜像矩阵。已有 768 MiB 单任务组件测试也须真实执行后才可结算。

下一步：继续 ln 的 VM/Actions 原生门禁；完整 Actions、正式 runner 镜像、未完成的 Incus 矩阵和 ARM64 实机仍阻止发布。不会因首版无需旧兼容性而免除这些当前功能验收。


finance 本轮执行证据检查点已持久保存为 `/home/whl/anas-evidence/forgejo-incus-20261030/amd64-finance-gates-checkpoint-20261030.tar.gz`，26,060,552 bytes、188 文件，SHA-256 `f9e221dfdf98a103ccb554fc8093ef5ebaa6c23207f5f98edb980432ace0b245`（0600/父目录0700）。包含冻结源码、修复增量、全仓及原生检查、owner 版本和外层报告；排除 runtime 凭据、私钥、VM 磁盘、缓存与 Git 历史。初次打包因 root-owned receipt 无读权限失败，不完整包独立保留；重打包成功。此为阶段检查点，ln 结果及后续本机收尾证据另行保存。


后续文档收尾复检：`docs:check-requirement-status`、`docs:check-plan-status` 通过；`docs:check-requirements` 当前失败于其他未提交工作区的 user_portal 计划：PORTALSSO-R-001—005 各归属两个里程碑，R-006 缺 E2E 执行记录。Forgejo 与 Incus 需求覆盖条目均通过。未修改该独立工作或把全仓文档门禁继续计为当前全绿；须在最终发布前重新通过。


本机宽泛日志批量上传曾被自动审批拒绝（尚未逐项核验敏感信息），未执行该上传。随后仅选取已完整检查的文档构建、托管 PASS 标签及 17 项 owner/harness 单元日志三份，成功保存为同一持久目录下 `reviewed-local-checkpoint-20261030.tar.gz`，872 bytes，SHA-256 `b1441b4bdeecdd51eb0834f02b9e16786640adc255d2be575c124ce9c5690552`。其他本机日志仍为工作副本，不宣称已完整归档。


补验夹具检查：`server-forgejo-onejob-e2e.py` 仍固定 15.0.7/SQLite，不能作为本轮 17.0.1/PostgreSQL 结论。已核对 [17.0.1 官方 OpenAPI 源](https://codeberg.org/forgejo/forgejo/raw/tag/v17.0.1/templates/swagger/v1_json.tmpl)：已有 run/jobs 与单 job API，`ActionRunJob.steps` 在单 job 返回中提供 name/status/started/stopped（date-time 字符串）。后续应使用当前 PostgreSQL 测试实例及这些 API，避免沿用“无 jobs/steps API”的旧假设和 SQLite 直接查询。这里只记录已核实的夹具缺口，没有把尚未更新或执行的 Actions 算作通过。


ln 第二轮最终通过 container23/volumes12/VM21/network7 四阶段，正常关机、测试磁盘/SSH身份回收和10项物理宿主基线全部通过；Docker现有容器/服务/网络/卷未变且未写操作。当前 amd64 Incus 两档核心生命周期已补齐，首轮资源中断及基线失败另存。完整输入、指标、边界和持久证据摘要见 [Incus §18.2](../../../../dev-docs/plans/incus-module.md)。这不替代 R-052/R-053 的当前 Forgejo Actions 作业、开关和隔离矩阵，也不替代 ARM64。


### 17.2 当前 Actions 默认容器夹具接续（执行中）

单任务夹具已更新到17.0.1/PostgreSQL。它只在身份匹配的独立VM内创建私有、peer认证、仅Unix socket的PG集群，以普通anas-test账号连接，finally停止；不连接宿主DB、不安装依赖。观察改用官方run/jobs→single-job steps API，校验唯一job、run/job identity及带时区日期，保留原controller信号取消和state恢复语义。12项夹具回归通过；image夹具11项回归通过。两个入口均修复当前lease…和历史anas…桥/ACL清理识别，原资源围栏不变。真实执行尚未结算。

ln既有不可变trust-r2导出已重新核对：归档244,784,046bytes/SHA256 `923dfc9db6749be224bfce2edb28c3ab08348319fb380ab525037983a005dcd5`，fingerprint `63c2313fba50f8ab861413322587334a78ba59ccccda7d776b539de1e70874c5`。配方代码相对历史归档基线9a6921a1无差异；本轮重跑当前Go1.26.10 Provider/computeclient/controller，不把历史15.0.7工作流继承为17通过，也不把实验镜像称为正式签名发行。当前controller测试ELF SHA256 `11c707cf8e35a4dbcdca63e4f9abeef2b3cfa8001a60f0cd460a8e42d964e66b`。

官方Forgejo原生17.0.1测试ELF为122,057,320bytes，SHA256 `51a8cdf4a1252e79f4b3855d7a7891af6c6635105420768c460e2f0690385d6f`；初始HTTP传输在32,052,216bytes截断，校验失败且未执行。随后校验HTTP Content-Range分段补齐，整文件摘要与官方.sha256一致才进入输入。它是测试服务器原生程序，发布Compose冻结制品仍见§16/17的独立摘要。

全新VM `anas-runner-bake-53fafb`、2CPU/1536MiB、端口22142；目录 `/home/whl/anas-amd64-gates-20261030-ln/vm-lab/actions-lab/r1-actions-current`。仅guest安装依赖，先跑当前镜像九项门禁，再跑17/PG五场景工作流；原768MiB/CPU1/disk8GiB单任务预算不变。尚未覆盖生产默认4GiB、网页取消、state丢失、完整Core开关和真实容器构建等原剩余项。


接续状态：Actions第一轮仅因guest /run容量不足被启动前拒绝；第二轮因旧夹具把ensure响应限定为四字段而失败，当前Provider还返回R-159租约network报告。两轮均正常关机、宿主10项基线一致，原失败不豁免。夹具现严格校验四个布尔标志和ensure网络字段，inspect仍禁止额外字段，新增结构化非秘密诊断。第三轮全新VM `anas-runner-bake-2cd3c3`、端口22144重跑中，guest /run容量384MiB，CPU/768MiB/8GiB作业预算与原时限不变。

当前13项onejob/idle Python、12项image Python回归通过；Linux controller全包（含真实HTTP请求不变/不泄露凭据的时间戳观察反例）通过0.183s。新增Go仅为测试观察，不变更生产controller。新native测试ELF SHA256 `a819c4f7db15ea08a549eccb7067ee0deeb15645d22466e85457f6c57b059ae0`。`--idle-baseline`已实现60秒Actions关闭Forgejo基线、两个实际调和周期后零资源、1800秒controller RSS/CPU和实际队列请求频率观测；尚未执行，不计R-043/R-044通过。队列原始数据只有时间戳，不保存URL/鉴权/请求体；资源样本含回收后的CLI子进程CPU时间，正常工作流20分钟测试timeout不变，长观测模式单独给予40分钟监督预算。


第三轮已通过当前ensure/ensure/inspect与镜像摘要供给，真实Runner13.2能启动，但现行第10项镜像门禁`guest-podman-namespace-policy`失败：trust-r2缺`/usr/share/anas/forgejo-runner/podman.apparmor`，因此没有进入onejob或idle。正常关机及宿主10项基线全通过，原始JSONL保留。此前只确认当前配方相对9a6921a1未变，不足以证明更早0b614…实际烘焙的trust-r2含现行资产；本轮已纠正该假设，没有在旧镜像中手工打补丁或删除门禁。

已从当前固定源码生成新的含AppArmor配方（官方支持的`--chinese-build-speedup`，保留Debian签名校验），YAML SHA256 `02e9d4ba619bbb1c2524477581ee158ac80e592570ae7ba8800e970a991d8286`；Go1.26.10 artifact CLI SHA256 `8fd9f13394c55ef7e0ef34e1f5b9dc8a6e02415ae6da40a58afaddb31b37b66b`。固定Runner13.2输入摘要`fadaec897f5e6641c363f87ecaf75f866f99c81b7097ec6b433e4c48123fcad1`重新核对。新实验修订`native-20261030-r1`在 `/data/anas-amd64-gates-20261030-ln/bake-lab/r1-bake-current`，身份`anas-runner-bake-a28bf8`、端口22145，使用私有Btrfs NOCOW普通文件，不挂宿主块设备；正在guest内安装发行版distrobuilder等依赖。预定build/export/重复复用→当前10项镜像→17/PG工作流→30分钟idle逐阶段执行；尚未结算。全流程宿主Docker只读。

官方Forgejo17.0.1原生ELF的实际BuildInfo也确认Go1.26.10、x/crypto v0.57.0、x/net v0.60.0；Runner13.2固定ELF为Go1.26.8/x/net v0.58.0，正在补充实际二进制漏洞扫描，不从版本差异直接断定存在可达漏洞。


2026-10-30 最新范围确认：用户明确“目前没有arm的测试暂缓，如果x86过了，就算过了”。本轮以amd64/x86_64结算，ARM64不再阻止本轮完成；此前ARM64阻塞叙述保留为历史，适用性由此更新。ARM64未实测、后续单独验收，不据此宣称ARM已通过。其他x86门槛没有豁免，当前Actions镜像/工作流/资源等仍按实际结果推进。


补充扫描结算规则：CI的Go漏洞和发布镜像扫描当前均为reporting-only（`.github/workflows/ci.yml`、`container-images.yml`），没有经本轮改成阻断门禁。官方Forgejo17 ELF的3项符号命中为x/net HTML的GO-2026-6866/6860（修复v0.62.0）及未维护OpenPGP的GO-2026-5932（无固定修复版本）；Runner13.2 ELF有15项命中，含Go1.26.8及x/net v0.58.0。属于实际二进制扫描风险报告，不等同已复现利用；没有将符号扫描保守命中自动新增为验收失败。迁移上游加密库/维护Forgejo构建分支需另行评估方案；本轮保持既定x86验收，原报告保留，不宣称这些上游ELF零已知风险。ANAS/controller/CLI此前source/binary扫描无可达项的基线独立，不用于替代上游应用程序扫描。


本轮范围/文档收尾复核：模块中英文源文档已同步amd64结算、ARM64延期和Actions待验收边界，并执行源生成。最新`docs:check-requirements`重跑通过（32份文档/995项需求、16主题190活动用例），此前Portal归属失败已在当前共享工作区消失；首次本机Go默认缓存权限失败也保留，使用既有私有GOCACHE后重跑成功。两个状态索引检查与当前VitePress构建通过。没有把历史失败日志覆盖或用先前结果代替本次范围变更后的检查。


2026-10-31 接续：新镜像 `native-20261030-r1` 构建、导出、重复复用均exit 0，当前10项镜像检查全部通过；fingerprint `f7983f293abd5fe67cc5ca71120a1d0ad397d855d6b5fd24b55e813591419564`。独立导出245,588,750 bytes/SHA256 `9537fc82dbec3ec6aea16dee95b355aa41f6141bbdc38d7cc91e69aa8fc861e2` 已校验分片及组合摘要，保存在 ln `/data/anas-evidence/forgejo-incus-20261030/runner-native-20261030-r1-export.tar.gz`，属于实验候选制品，未签名发布。原轮onejob等待240秒后失败，idle未运行；证据归档SHA256 `1523658fa9b8970d4c19d5e9a8491d97ad3ca4b952a79fec01dfbd0a1a282be3`，位置为 `/data/anas-amd64-gates-20261030-ln/bake-lab/r1-bake-current/reports/lifecycle-evidence.tgz`。正常关机；宿主Docker六类基线相同，netns/nft/IPv4/IPv6路由不同，原因未确认，宿主基线检查失败保留，未做回滚。

静态排查发现synthetic Forgejo位于执行宿主机，原fixture默认internet策略不授予该地址访问；连接诊断因VM已关机未实际执行，此原因尚待重跑证实。新fixture正常/idle明确声明host访问，waiting模式刻意禁止，生产Module网络策略不变，不以此替代默认网络/Traefik验收。新增真实600秒waiting TTL与30秒backoff观察，idle增加调和错误检查；15项onejob及12项image回归通过。脚本SHA256 `afe721ee26412d74f7feef18f5f0106d8eb0a9e27d450542ac891603971c6f4d`。新独立轮 `/data/anas-amd64-gates-20261030-ln/reuse-lab/r1-reuse-current`，身份 `anas-runner-bake-213f91`、端口22146，1536MiB/2CPU，依次image→workflow→waiting→idle，已启动，尚未结算。完整输入摘要见该轮src/manifest.json；宿主Docker保持只读。

上轮外层报告/输入manifest已另存共享测试服务器证据路径 `/data/anas-evidence/forgejo-incus-20261030/bake-r1-owner-checkpoint-20261031.tar.gz`，245,766,754 bytes/23 files，SHA256 `f4e790e5f80d95bb373e083f3876fdb361e4c5270bec1d0f5ab51814b819ab07`；不含运行时凭据目录、磁盘和SSH私钥。


reuse第一轮未进入工作流：10项镜像门禁中的rootless-oci-exec-limits在实际CPU=0.5/memory=128MiB/PID=32/NNP=1及token_visible=false均确认后，诊断容器清理20秒超时；因此门禁失败保留，waiting/idle未执行。归档SHA256 `5c6cd2e301e9d3ec3864778eeae12a82b79f16a438a6575f95eef697e77565be` 位于该轮reports/lifecycle-evidence.tgz。正常关机、cleanup_passed=true，宿主基线一致。测试清理现显式`podman rm --force --time=0 --ignore`，诊断sleep无须优雅退出，原20秒清理deadline及错误判定不变；没有修改产品镜像或生产controller。computeclient本机包测试通过6.665s，新的Linux测试ELF SHA256 `b9558fbbe05e8e74a80b671ee38618756ba97d174e8e12b45cd070c9a6534ae2`。独立重验轮 `reuse-lab/r2-reuse-current`，身份`anas-runner-bake-942e8a`、端口22147，已启动，尚无最终结果。


reuse第二轮：当前10项镜像门禁全部通过；正常及故意失败工作流均实际执行、结果匹配且资源回收通过。进入crash场景观察时，夹具把Forgejo 17合法的可空步骤时间戳拒绝为invalid step timestamp，整体onejob失败、waiting/idle未运行。归档SHA256 `f9d31358fe1a9b130e50fb0f105f9a0d8acb58cbdf4124a65b6426dbd0b4a8e6`，该轮reports/lifecycle-evidence.tgz。正常关机，guest资源清理完成；外层host_baseline失败仍保留，不能表述整轮clean。正常工作流成功支持合成endpoint网络配置修正，但不替代生产默认网络验收。

核对官方v17.0.1 `modules/structs/action.go`：Started/Stopped为`optional.Option[time.Time]`，合法null应表示尚未发生；已修复只接受显式null归零，缺字段/非法值/无时区仍拒绝，null不能作为payload已开始/已完成的证据。回归16项通过，脚本SHA256 `e4003506c249d0467645efb9de65b3e11694540110cdedf3747002a431b9f4a8`；公开API定义已保存 `/data/anas-evidence/forgejo-incus-20261030/forgejo-v17.0.1-action-api-types.go`，SHA256 `39ec22086d967fde13fca0b860248a08f2d0910667e21fd34a5f59ca721ad39b`。第三轮 `reuse-lab/r3-reuse-current`，身份`anas-runner-bake-4c54a5`、端口22148，已启动，待实际结算。

reuse前两轮外层报告与输入manifest另存 `/data/anas-evidence/forgejo-incus-20261030/reuse-r1-r2-checkpoint-20261031.tar.gz`，112,020 bytes/37 files，SHA256 `f5294145be3b18807b5715c5c8eeee1dfd2c5b8670c0fe02c34b62bf00ce0fdc`。


reuse第三轮中间结果（尚未结束）：镜像10项门禁通过；17.0.1/PG五场景全部通过，normal124.074s、failure87.934s、SIGKILL保留状态恢复142.302s、SIGTERM取消66.270s，未获批repo保持waiting且无registration/instance。四个执行场景均检查state空、instance/root disk/registration不存在。TLS校验、controller stdin公有信任投影保持，guest系统信任未改。该结果不覆盖state丢失、Forgejo Web取消、生产Core开关或真实容器构建。真实waiting TTL已启动，TCP negative control通过且API task_id=0；尚未到600秒，不计R-045通过。

该轮发现已结束normal fixture的PG数据72MiB占/run，后续多模式会叠加容量；仅将已停止的两个`.completed-before-waiting`目录移入guest `/var/lib/anas-private-completed` 保留私有数据，未触碰当前fixture/state/时钟。首次pg_ctl错指父目录而安全拒绝、未移动；核对正确data目录返回exit3后执行。结构化非秘密记录保存在该轮guest `/opt/anas-lifecycle-report-onejob/completed-runtime-relocation.json`，随最终证据归档；不把存储迁移视为测试本身通过。


reuse第三轮waiting TTL结果：真实生产600秒WaitingTTL下，622.688秒观察到清理完成，job保持waiting/task_id=0，state workload/registration/执行实例/root disk清空，retry_after持久化且随后30秒未重建。连接负控制TCP exit124。未伪造时钟或直接写controller状态，未执行payload。本控制使用刻意不授予执行宿主Forgejo网络访问的真实未领取任务，不宣称验证concurrency group排队本身。waiting stage exit0；外层VM仍在执行idle，因此最终宿主清理/基线尚未结算。


### 17.3 2026-10-31 当前配方 amd64 容器执行面与空闲门禁结算

第三轮四阶段image/onejob/waiting/idle均exit0；QEMU正常关机exit0、cleanup_passed=true，测试磁盘/SSH密钥/seed等8文件已删除，宿主10项基线全部相同（包括Docker容器/服务/配置/网络/卷、netns/nft/IPv4/IPv6路由），无宿主Docker写操作。此前失败及宿主差异单独保留，不用本轮覆盖历史。

- R-043：Actions开启、prewarm=0，两个实际调和周期后零registration/实例/root disk；全程117次资源清单检查均空。
- R-044：1800.126秒连续观测，RSS峰值20,197,376bytes（19.3MiB），平均单核CPU0.2994%（含已回收CLI子进程），120次真实queue请求，0.066662Hz、最短间隔14.985959秒；低于既定128MiB/1%/每10秒门限。对照Forgejo关闭Actions基线60.046秒，RSS峰值194,605,056bytes、平均CPU0.1332%。时间戳观察无URL/鉴权/请求体，调和错误检查通过。
- R-045：600秒waiting TTL，622.688秒回收，真实未领取job/task_id=0，全部资源清空、退避30秒无重建。
- 当前不可变镜像10项门禁及五场景onejob矩阵通过，具体输入、场景时长与测试修复见§17.2；并非生产Core开关、默认Traefik网络、org容器构建、VM Runner、state丢失或Web取消验收。R-052/R-053保持待完成，Actions不能标release。纯托管首发门禁按R-054独立结算。

原始guest公开报告归档SHA256 `251220dcef8f0d912596f7be12ea06c9674429ad608b0a6bb6415187a0e50d33`。共享持久归档 `/data/anas-evidence/forgejo-incus-20261030/reuse-r3-passed-checkpoint-20261031.tar.gz`，88,227bytes/25files，SHA256 `28cec78f3519319580e6c51ded374cab42924b292c566a77ecdfce8184363291`，包含原始报告、外层基线/清理、完整输入manifest/source摘要与owner源码，不含runtime配置、凭据或磁盘；主机ln。正式签名目录/发布制品未创建。下一步继续R-052/R-053和amd64 VM Runner剩余门禁。


### 17.4 真实容器构建与组织scope接续（进行中）

补充`--container-build`，使用公开ECR固定amd64 Docker CLI镜像摘要 `f56779b4e86550493153cc8642c9c8e40b5d934e43cb5b4ea463aea5245c5c01`（27.5.1-cli）构建FROM scratch/COPY marker镜像，检查label、创建停止容器并复制产物逐字节比较，随后删除内层容器与构建镜像；无Secrets/checkout/特权参数。`--org-scope`只允许用于该模式，测试controller只在固定`anas-onejob-org`组织scope与既有固定repo scope之间选择，不放宽任意身份/端点。其他五场景及idle/TTL默认模式不变，构建模式明确不宣称crash矩阵或Core开关覆盖。Python16项回归、controller包测试3.417秒通过；最新测试脚本SHA256 `7915cf1e70fdaa7bb0644493db6aa5d01a47b5deb0306f6808b6a030dfd6cf5f`，Linux controller测试ELF `3c7f6e18eaef8a9b680f84e41e087fb0c81f513d9c27b13fcfcffe0d1aa8925f`。没有生产Go逻辑/配方修改，§17.3已通过基线保留。

2.1KiB阶段脚本上传自动审批连续两次超时、均未执行，用户随后明确允许再次上传并继续验收，已成功传输并校验SHA256 `14c9edafd5ac1a2d9ad8fb04006b65ac3862e2a9a0100923b902b5ca9029d7e5`。新轮 `/data/anas-amd64-gates-20261030-ln/build-lab/r1-build-current`，身份`anas-runner-bake-99821c`、端口22149，2CPU/1536MiB，依次image→buildrepo→buildorg。已启动，尚未结算；宿主Docker仅只读基线。阶段切换将确认已停PG后保存私有历史目录到guest磁盘，避免/run容量叠加，不改当前状态。

当前本地收尾门禁：module文档源生成/检查、两个状态索引检查通过；`docs:check-requirements`在当前共享树新增的auth-entry-and-signout计划失败（AUTHUX-R-001/002/003缺E2E记录），Forgejo/Incus自身覆盖记录一致；不改无关工作、不声称整仓门禁全绿。


构建第一轮结算：image exit0；buildrepo真实workflow失败，buildorg未执行，guest清理完成/QEMU正常关机，外层host_baseline失败保留。原始归档SHA256 `63c85a7bde5123e6af021ad2db9b956285604d6b731dd23c931916cd61b29ad3`。只读取固定无Secret步骤的独立诊断记录已随该归档保存，实际错误是`Cannot connect to the Docker daemon at unix:///run/anas-podman/podman.sock`。Runner官方v17文档说明指定Unix socket映射到工作流容器 `/var/run/docker.sock`，确认是当前runner.envs.DOCKER_HOST路径错误，普通shell工作流未覆盖此缺陷。

修复仅将工作流环境`DOCKER_HOST`改为`unix:///var/run/docker.sock`；Runner进程启动环境及container.docker_host继续使用guest路径 `/run/anas-podman/podman.sock`。未暴露宿主引擎、未增加服务/挂载白名单/特权，不在旧镜像内手工改配置。中英文技术文档与runner-image说明同步。controller/computeimage/artifact CLI针对测试均通过（0.753s/3.131s/3.151s）。新自包含配方SHA256 `85ff927a36fd0253b0566cf94ff87f879262dfc03508b786cc9260f825dda495`，新不可变修订`native-20261031-r2`，待新原生基线，不把旧r1通过继承为新镜像验收。

新轮 `/data/anas-amd64-gates-20261030-ln/rebake-lab/r1-rebake-current`，身份`anas-runner-bake-431fd8`、端口22150，2CPU/1536MiB：bake/export/reuse→image→五场景onejob→buildrepo→buildorg。bake阶段将三个公开制品文件打包入公开报告，避免成功后测试磁盘删除导致镜像不可追溯。已启动，尚未结算；完整输入见该轮src/manifest.json/source.json。§17.3旧配方通过作为历史基线保留，但含该构建缺陷，不用于宣称Actions可发布。

构建失败轮次持久归档 `/data/anas-evidence/forgejo-incus-20261030/build-r1-failed-checkpoint-20261031.tar.gz`，61,393bytes/21files，SHA256 `50e0d48e1722fe40f7bc3bb75b20356296a716e898a19f9d274f5bec03158ed9`。宿主容器基线差异只涉及既有`/ln-mesh-v2fly`，同ID `c4c220e2e823…`，测试前后均restarting/PID0，RestartCount512→527，StartedAt10:02:26Z→10:17:28Z；它在测试前已反复重启，不能把该变化归因为本轮容器写操作（本轮未执行宿主Docker写操作）。Docker服务/配置/unit/network/volume一致；netns/nft/routes另有差异，未归因、未回滚。原严格host_baseline失败保持，不将外部重启自动豁免。

组织观察复核：官方v17 `shared.ListRunners`默认WithVisible=false，repo列表不能证明org registration已清空。新夹具将org scope回收观察改查 `/api/v1/orgs/anas-onejob-org/actions/runners`，未获批个人repo仍独立查自身端点；新增API调用边界回归，17项通过。当前本地脚本SHA256 `b2790011363052960b1a73332c54dce10c7fa2116ab69b0dbdf555bb637b8385`。当前烘焙轮已冻结的脚本仍为7915…，不修改其输入；即使其中org构建成功，也只计构建功能，不足以独立结算组织registration回收。待用新镜像与修正观察入口另验组织scope，R-053不提前通过。


### 17.5 完整Core单开关E2E拟用方案（待设计确认，未实现）

R-052无法由原生controller启停替代：需普通CLI在新workspace中执行唯一`actions_enabled`变更及apply，独立核对Forgejo Actions配置、controller desired state/实际进程、registration/实例/磁盘回收及重新启用，仍使用真实Hook/Compose/Secret Store。

拟在一个全新受owner约束的amd64 QEMU VM内复用已验收的纯托管组合（Forgejo/PostgreSQL/Casdoor/Samba/Traefik/Lego），增加真实Incus Provider/daemon与本轮新Runner制品；仅使用公开制品及新合成凭据，不复制finance已有workspace/Secret Store。Incus使用已支持的显式远端连接配置；新镜像通过私有实验Provider bundle供给，不写生产目录、不声称正式发布。保留Docker默认转发策略，不加测试专用通行规则来替代生产网络兼容。宿主Docker保持只读。

复杂度高：涉及多个真实服务、CLI→Hook→Secret Store→Compose→Incus→Runner数据流，部署/失败/禁用/重新启用需顺序验证，失败须保留原状态、私有日志与源码/制品摘要，最终由外层owner有界关机及核对宿主。预计需更大的单VM内存预算（拟3GiB，启动前加512MiB真实余量），实际资源不足则拒绝启动，不动宿主现有服务。正式应用镜像尽量复用已验证公开制品；若需从finance旧专属空测试缓存导出，采用断网的隔离导出进程并校验其无容器，不接触生产daemon。

更简单方案是保留当前原生组件门禁及纯托管验收；代价是无法证明R-052完整业务联动，Actions继续未验收，不将其豁免或标release。推荐继续完整Core E2E。按AGENTS.md高复杂度设计确认规则，等待用户确认后再实现该部分；新镜像烘焙/既有工作流及构建门禁独立继续。

修复镜像中间基线：native-20261031-r2 build/export/reuse通过，fingerprint `3e03737b6bb78b96b82b7a5036919a23a5ca26620a09b504451144933db6e940`、recipe_digest `eff6e2084f70b747884fbe1773f66a0f19351c85e53faf662556dc27bcfc2ec0`。三个公开制品文件已单独保存在 `/data/anas-evidence/forgejo-incus-20261030/runner-native-20261031-r2-export.tar.gz`，245,545,814bytes，SHA256 `cc1aac93bb18d0c8624438a349190757cfb13d1567fe9d6e67db2024006924c7`；对应receipt同目录`runner-native-20261031-r2-export.json`，分片与组合FP重新校验通过。当前10项镜像门禁通过，五场景中normal/failure/crash及回收已通过，cancel/buildrepo/buildorg仍待结算。

新修订中间结果更新：五场景onejob全部通过（normal107.303s、failure90.208s、SIGKILL恢复128.716s、SIGTERM取消60.695s，未获批repo等待且无资源）；真实repo容器构建通过182.390s，scratch镜像label、停止容器产物复制/字节比较及内层镜像/容器删除均成功，外层state/实例/root disk/registration清空。该红绿对照确认工作流DOCKER_HOST映射路径修复有效。org构建与正确组织registration观察仍待结算，R-053保持未完成。


新修订rebake轮最终功能结果：bake/image/onejob/buildrepo/buildorg五阶段exit0；repo构建182.390s、org构建181.985s，产物检查与未获批repo拒绝均成功。但org registration回收仍受旧观察入口限制，不单独据此通过R-053。QEMU正常关机exit0，外层host_baseline失败（既有ln-mesh-v2fly同ID重启次数530→575，测试前后均restarting/PID0；nft另有差异；其余8类基线相同），不删除失败诊断磁盘/密钥、不宣称整轮clean。没有宿主Docker写操作。

原始公开报告归档SHA256 `52b32a1da3e17929311c182bde2d2e697088a34c1750878dc6b3d9078086f71d`。共享外层/输入manifest/原始报告归档 `/data/anas-evidence/forgejo-incus-20261030/rebake-r1-checkpoint-20261031.tar.gz`，245,730,139bytes/26files，SHA256 `a981e0bc3b77b584350fff81d4db2ef4127bfef1250f5705b4596c999c08075d`；不含运行磁盘、配置或私钥。

正确org端点观察的独立轮 `/data/anas-amd64-gates-20261030-ln/org-recheck-lab/r1-org-current`，身份`anas-runner-bake-0db331`、端口22151、1536MiB/2CPU，使用新r2镜像及b279…脚本、3c7…controller测试ELF；已确认前VM退出后启动，依次image→buildorg，尚未结算。§17.5高复杂度完整Core方案已向用户请求设计确认，尚未收到该问题的答复；独立组件验收继续，不默认同意。


### 17.6 组织scope正确端点复核与当前边界

独立org-recheck轮image/buildorg均exit0，正确org Runner列表API下的真实构建166.265秒成功，scope_kind=org；确认state空、组织registration清空、执行实例/root disk不存在，未获批个人repo保持waiting且无registration/实例。使用b279…脚本及r2镜像（FP3e037…），不继承旧仓库列表的假阴性观察。QEMU正常关机exit0，测试工作负载及进程清理完成；严格host_baseline仍失败，只因既有ln-mesh-v2fly同ID重复重启（576→590，前后均restarting/PID0），其余9类基线相同。没有宿主Docker写操作，失败磁盘/密钥保留私有目录，不自动豁免此失败。

原始公开报告SHA256 `54dd3b4e3dde4afb5f82a0dcc0c4c9f916bb81353464276e0617b21fb327b00a`；共享完整检查点 `/data/anas-evidence/forgejo-incus-20261030/org-recheck-r1-checkpoint-20261031.tar.gz`，61,647bytes/21files，SHA256 `04a006e3c838bce9044fe03d9606f3f249327039d0f570751ae0eb550a3f148c`，包含输入manifest/owner源码及外层/原始报告，不含运行磁盘、配置和私钥。

当前结算：R-043/R-044/R-045已补齐，生产controller逻辑未因job侧引擎路径修复而改变；30分钟/TTL按§17.3的明确基线保留，不声称新镜像重复执行过这两个长时测试。当前配方amd64容器Runner镜像、五场景及repo/org无Secret镜像构建和回收功能通过。R-052完整Core唯一开关尚未执行，§17.5高复杂度方案待用户确认；R-053的amd64 VM档仍未执行，不以Incus核心VM生命周期通过替代Runner。宿主基线失败、无关AUTHUX文档门禁失败、上游二进制reporting-only漏洞报告均保留，不能宣称全部x86门禁全绿或完整Actions可发布。

默认VM Runner为4096MiB，ln在保留现有服务时实测可用约4.4GiB，不足以覆盖外层系统及应用服务加该VM默认预算；已请求更大（建议16GiB、支持KVM）的amd64测试机信息，不停现有容器腾内存，不将swap计作真实余量。ARM64仅按用户确认延期，不扩张为VM或其他x86门禁豁免。下一步依用户确认继续Core方案，并在合适硬件补VM Runner；所有本轮实际运行的VM已正常退出。

用户已明确确认§17.5“继续完整Core验收”，该方案及高复杂度范围获确认，后续同范围不再重复询问。开始准备独立公开制品输入、新workspace/合成凭据和受内存门禁约束的测试VM；不复制旧Secret Store，ln宿主Docker只读。VM默认规格所需更大amd64测试机的信息问题仍待答复，ARM延期不变。当前本地29项Python回归、module文档生成检查、两状态索引及VitePress构建均通过；ln本轮QEMU进程及22140—22151监听端口均已退出。


### 17.7 用户要求核对ln容器是否有可停测试残留

用户在VM硬件询问后要求“检查当前容器，看哪个是测试遗留下来，可以停止的”。已只读盘点所有dockerd进程（仅默认实例）、精简容器归属/挂载/网络/资源及进程名，未读容器环境或配置内容、未停止容器。原始非敏感盘点保存在 ln `/data/anas-evidence/forgejo-incus-20261030/ln-container-review-20261031.json`。本轮Forgejo/Incus执行面全部在QEMU内，已退出，无本轮宿主运行容器。

- 明确旧构建残留 `anas-incus-tools-xkotbq` 为created、`buildx_buildkit_anas-incus-build-xkotbq0` 为exited，均零运行内存；多个旧test-client也已exited，不停止/删除它们来虚称释放内存。
- 活跃旧网络实验：`ln-access-ss-test`约29.6MiB、`mesh-ss-test-a`约48.7MiB、`ln-client-tunnel-test`约70.6MiB、`ln-phx-mesh-test`约10.2MiB，合计约159MiB；不是本轮创建且无本轮归属标记。常驻`mesh-ss-v1-a-v2ray-1`及Traefik同时连接`mesh-ss-test-a_default`，有实际拓扑关联，不能只凭test命名认定可停。
- `/data/anas/.anas/deployments/...`下Samba/Traefik/PostgreSQL/LLNG/Nextcloud等属于现有部署，保留。Samba FS已被其他工作更新为healthy，不能把新image后缀当本轮残留。
- 既有`ln-mesh-v2fly`持续restarting/PID0，603次重启时统计0B，停它不能释放所需GiB级内存；不在本轮代修旧网络服务。

最新MemAvailable为5,852,792KiB（约5.58GiB），较此前4.4GiB回升；3GiB Core VM加512MiB真实余量可在启动时重新检查后执行，不需要先停旧网络容器。默认4GiB VM Runner仍须按完整外层预算即时检查，不把之前硬件评估固化成永久不足，也不把swap当余量。未自动将“检查可以停止的”扩展成停止不明业务用途的网络容器。Core方案已确认，继续独立准备。

### 17.8 完整Core验收输入准备（2026-10-31，执行中）

用户已确认§17.5高复杂度方案。finance已从此前获准上传的3195文件快照及本轮安全补丁冻结源码，身份`1f287f49f98e996b90184dd6ec383c22bd6a89e4c7aec78b11dde9ff0f10c870`，Go1.26.10成功编译CLI及七个实际Module Hook；源码/程序归档35,425,278bytes，SHA256`6fb28f7d2d6efea874eac504c56df16041be71e2011a0aa3c79cddf50b0c2a72`，ln已逐文件校验。Incus Provider Dockerfile另叠加Go编译器固定1.26.10，摘要`34da615ad3dc8678ba14bf7988c82bf2cc23e4bed1f0790bd07508de27145cda`；静态shared-build检查通过，真实容器构建尚待执行。

finance旧已回收IAM测试缓存通过私有mount/net namespace、私有Docker socket及768MiB内存限制仅导出十个公开正式镜像，无容器创建/运行，私有daemon已退出。公开镜像归档673,414,265bytes，SHA256`1a8958fe695e56433239142a68ca3433556c1e2ffdfffe58bb1f1c5a2dfe48f9`，正在传往ln；安全controller归档25,141,239bytes，SHA256`5d845f9c758ee70f130d559548edb3cce7d29e7c2f783dd169577adff132b3d3`，ln校验通过，须替换公开镜像缓存中的旧controller。新Core VM尚未启动，R-052仍未通过；所有运行配置和新合成凭据仅在新VM私有目录中生成，不进入公开报告。

Core执行更新：公开镜像已在ln校验完成。r1在安装前因owner残留旧可执行文件名而失败，无业务测试；QEMU正常退出0，宿主containers/nft/routes6基线不同，其他7项相同，失败保留。修正owner后新r2身份`anas-runner-bake-d29ccd`，端口22152，3072MiB/2CPU，物理余量准入通过，当前安装中。验收driver在首次执行前显式叠加`config set --root ... --defer --update-lock`，随后仍执行普通apply，避免CLI默认自动apply造成重复部署或锁过期；driver SHA256`e7c8696503e61bee0ed90611f36cec205900aa121dd7fdd1e263661050288966`，VM公开input-overlay报告保存替换前后摘要。此为夹具修正，未改变产品逻辑或网络策略。宿主Docker全程只读，现有容器不停止。

Core后续执行：r2的软件安装完成（Incus7.0.2、Docker29.9.2、Compose5.6.1），源码/CLI/Hook校验和私有Docker启动通过；镜像阶段因夹具使用Docker保存归档的config摘要重新tag而失败（command7），尚无workspace/合成凭据。Docker29采用containerd image store，改为从校验归档的唯一RepoTag重标记。r2正常关机0，10项宿主基线全等；失败磁盘保留。两次诊断启动分别因复用空staging目录冲突、删改脚本遗漏scp定义失败，修正后r5脱敏诊断完成，私有stderr未导出。自动审批两次拒绝可能泄露私有日志的读取，改用VM内有限错误分类。

r6从r2安装环境的独立qcow2叠层开始（未复制业务workspace/Secret Store），保留原失败私有目录并新建业务凭据。正式Provider Dockerfile构建、当前安全controller加载、实验Runner bundle及首次完整CLI apply均成功；随后夹具ConfigParser未支持Forgejo节标题前的全局配置项，报MissingSectionHeaderError，R-052仍未完成。Provider公开镜像ID`sha256:02d7ee78539908de9aa947aed1cf125b31cf041d220eca54ef6e124749340ecc`，公开导出6,989,094bytes，SHA256`fd3005ac9d4ac948362ce7be6a13ff9ad345b502f035379cdba4b41268207230`，保存在r6公开报告内。修正INI观察方式后，r7再次从r2无业务凭据基线启动独立VM/新workspace；加入实际controller及Incus CLI ELF摘要核对。所有容器启停限于测试VM，宿主容器保持不变。

### 17.9 Core真实网络故障与DNS修复（2026-11-01，完整Core仍执行中）

后续关闭状态已验证：服务端Actions=false、controller实际exited/exit0、当前安全ELF匹配。HTTP检查曾在Traefik路由初始化时返回404；r12加入120秒有界等待后通过，真实版本`17.0.1+gitea-1.22.0`，仓库创建201与配置PATCH200；版本比较统一按现有夹具去除构建元数据。此前将404直接归因于版本后缀的推测不充分，以此实测纠正。r9一次Docker恢复时的单次查询超时已修正为原90秒就绪期限内重试，未放宽业务条件。

r12正式启用apply失败于Incus POST `/1.0/networks`（HTTP500/code500），源码错误模板指向ensure计算资源；daemon日志有限分类确认dnsmasq监听IPv6地址被占用。r14仅确认named进程存在的独立网络探针成功，不能据其认定故障已复现。r15等待完整Samba健康后实测：IPv6 auto桥创建exit1/dnsmasq Address already in use；IPv6 none桥exit0。修复BIND仅监听回环及已配置服务地址后，同类IPv6 auto桥创建exit0，Samba仍healthy。没有关闭IPv6或增加测试网络放行。BIND接口监听行为参考官方[配置说明](https://bind9.readthedocs.io/en/stable/reference.html)；根因结论来自本轮红绿对照。

产品修复仅为Samba运行资产`12-bind.sh`及`named.conf.envsubst`，SHA256分别`3fe98cae2b83a41c8544d5168341533a0a53fd04368fc4f61f6053c4eb49de8c`与`7c999d634b9342826e8423af4ff9b57e9afb85d0a8e1d96e5613bbb3c6e79bfa`；按地址协议生成两个内部监听列表，保留IPv4/IPv6配置地址及回环，无新参数/依赖。双语技术源及镜像文档同步，bash语法及module文档检查通过。

r16从r7本轮独立合成workspace接续（不复制operator凭据），在首次关闭状态apply之前加入DNS候选。仓库正式revision13不变，遵循开发标准，在测试副本的module/localization/Compose/文档投影统一临时revision14；只在测试VM中从公开r13镜像ID`sha256:deb2d9c936cf23da2568237c1cdce2a0014683e387b70c5ec80f4c4a8605771b`叠加两个文本运行文件，未修改其余上游二进制；未变更的anchor仅本地重标记。此为实验增量镜像构建，**不声称完整原Samba Dockerfile重建或正式发布**。候选镜像ID`sha256:3507e2737fd19f336bc57f89b17c59ba8631cedc7b6b0fd3eff1fa9e09d6bf14`，公开归档132,998,483bytes，SHA256`d722cbf0412d2616ceadf59ee42c9eb71bb01ff3567136eef62e71ddfe47b06a`；构建配方、全部源码覆盖摘要与制品均在r16公开报告中。

r16额外使用该固定候选的全新无网络、256MiB/1CPU临时容器运行真实named-checkconf，IPv4、IPv6、IPv4回环、IPv6回环四种渲染/监听断言全部exit0，临时容器自动删除。实际运行Samba image ID与候选一致；完整Module默认租约桥`lease120067207a`已成功Created，先前POST网络故障不再阻断。Actions关闭基线已通过，当前继续正式启用/真实作业/运行中关闭/账号撤销/重新启用/CLI停止，R-052仍不提前标完成。

### 17.10 Core标准主机前提补齐（2026-11-01，执行中）

r16已解决真实DNS冲突、成功创建租约桥且四种真实BIND解析用例通过；随后启用apply被Provider明确拒绝：缺少`incus.configure`维护的`anas-traefik`网络地址集合。它属于正常安全前提，不以手工写集合或额外转发放行绕过。旧测试的显式远端连接仅配置daemon/TLS，未执行完整主机配置，故R-052保持未完成。

改用全新Debian13测试VM，初始只安装Docker，由仓库现有`server-incus-host-action-e2e.py`的真实anasd/hostd/HTTPS owner session/public CLI计划—确认—执行链完成Incus install/configure/enroll，再使用自动连接bundle进行Core部署。所有动作只在该VM内；Docker专属data-root不变，在VM内额外暴露标准`/run/docker.sock`给正式主机流程，ln宿主socket/容器均不写。正式主机流程负责地址集、转发及同步，不添加测试专用规则。该补齐属于已确认完整Core范围的运行前提，未增加产品服务或改变网络验收标准。

finance从同一冻结源码`1f287f49f98e996b90184dd6ec383c22bd6a89e4c7aec78b11dde9ff0f10c870`、Go1.26.10成功构建CLI/anasd/anas-hostd/control-probe；新测试版本`0.0.0-native.20261101.1`，安装协议要求的40位commit使用真实base commit `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59`，完整源覆盖仍由64位source identity及清单单独绑定，不声称clean checkout。实际Core CLI替换为同源码/正确安装身份的新ELF（SHA256`fbbd02b1e29cd5bde694db639dfd614e0c20a6519d218f3efe8948deebfcd2ce`）；anasd `e44f1003b6c03ad0d486fdffb4d18b2af3746d57434e34083fad9047a8683f5e`，hostd `5846e22b82b0fc60c7bcefd31be10a405165758f35486f042dc45cd859074e22`，probe `8b8b255959567988042c3f6b251e5ab5492c0a2dacc469f7f039ca8abef4acea`。公开程序/原规范unit/原fixture归档19,629,447bytes，SHA256`1087ed1be993666239147914f35079bdfe247af064be7fede663fb377038cb75`，ln已校验。

r18使用独立新workspace `/srv/anas/core-actions`及新合成凭据，拟正式供给`anas-btrfs`24GiB池，配置标准主机后再启用Actions；旧r16/r17结果、源清单与私有磁盘均保留。仍需真实作业、运行中关闭、回收、账号撤销/恢复、普通CLI停止及外层结算，不能将主机前提修正当成Core通过。

文档验证更新（2026-11-01）：module文档生成/检查与需求、计划状态索引通过。全库VitePress本次失败于现有门户通知索引的4个断链：`architecture/index.md -> ./portal-notifications`、`research/index.md -> ./portal-notifications-feasibility`及对应两条英文索引链接；它们属于共享工作树中的其他文档工作，本轮未改写该内容，不关闭死链检查，也不将失败写成通过。日志`/tmp/anas-core-dns-docs-build-20261101.log`；后续在来源补齐后重跑。r18运行时保存的owner源码摘要`a72ca55431702dc3273a413fae7cf594dfe68147002943060b518105231c881b`与上传版本一致，清理helper摘要`beace7ddc2051c6a032c726ae28213aeaba006d9d74d9db9bf95d9e12958efdd`。


### 17.11 标准主机实测与仓库 Actions 前提（2026-11-01，执行中）

r18的真实主机 install/configure/enroll 三阶段均通过，使用标准连接、24GiB Btrfs池及正式CLI；关闭基线、实际安全controller/Incus CLI ELF核对、单开关启用服务端与controller均通过。后续工作流提交201，但 Actions runs/Runner API返回404，完整R-052仍未通过。r19尝试直接关闭时，正式before_stop因无法确认清理而拒绝继续，保留其fail-closed结果，不改状态文件绕过。r19、r20均已正常关机exit0、私有daemon退出，10类宿主基线全部相同；无宿主Docker写操作。

r20在VM内进行有限API诊断：恢复账号及程序账号的身份端点均200，仓库`has_actions=false`，runs及Runner端点404；仅对本轮合成仓库显式PATCH `has_actions=true`后，仓库和Runner端点恢复200。此为Forgejo原有仓库功能开关，不增加Module执行实例开关。原夹具仅启用实例级Actions，遗漏仓库前提；修正准备顺序，保留首次404，不把它写成真实作业通过。冷启动Traefik路由就绪也实测晚于120秒，恢复步骤采用300秒有界等待并记录有限HTTP状态；不对运行中关闭的独立API观察引入等待或豁免。

r21从已正常关闭的r18测试磁盘建立独立qcow2叠层，接续同一合成workspace与标准主机，修正脚本SHA256 `e18f607d7a35997d9cc0e3bf3e4a855ec09fa003cef41203b648e117eb8897b0`。准备阶段先启用测试仓库Actions，再用其声明Compose恢复controller，随后重新执行正式CLI关闭基线和完整启停。只修改测试夹具；未改controller状态文件、网络安全策略或产品认证。宿主现有容器保持不变。尚待真实作业、运行中关闭与回收、账号撤销/恢复、普通CLI停止及外层结算。


r21已确认冷启动后版本、仓库、PATCH及Runner API均200；随后恢复夹具的Compose默认项目名与现有容器名冲突，尚未进入业务重跑。改为启动同一个已声明的controller容器，不重建、不改状态；r22由此完整通过关闭基线、单开关启用、真实workflow及回收、运行中关闭（555次独立观察，API关闭前资源全零）、旧账号凭据撤销、重新启用保持程序账号ID2及无旧资源。最后普通CLI停止因夹具多传`stop`不支持的`-module-root/-y`参数而返回`usage`；保留该轮失败，不归因于产品逻辑。r21/r22均QEMU正常关机0、10类宿主基线相同。

r23只修正为正式`anas stop -w WORKSPACE -json`后完整重跑，冻结夹具SHA256 `d19c8b4e4d26b8769323856ca13d760f8ac31f459151b5703136b0de1a218f62`；相同源码已纳入`test-env/scripts/server-forgejo-core-actions-e2e.py`，用法和冻结输入边界见test-env README。运行依赖为测试VM既有的Python3/PyYAML，无新增产品依赖；本地语法编译通过，本地默认Python缺PyYAML，未以本地执行替代VM验收。完整Core仍待r23最终结算。

文档状态更新：共享工作树已补齐之前门户通知目标页，重新构建VitePress通过（16.26秒，`/tmp/anas-core-final-docs-build-20261101.log`）；本轮未修改那些目标页或放宽死链检查。需求覆盖（31份/989项）、17topic/197active用例目录、需求及计划状态检查通过，完整日志在`/tmp/anas-core-check-*-20261101.log`。


### 17.12 完整Core单开关验收通过（2026-11-01）

`r23-core-standard-resume`完整业务脚本exit0，外层supervisor无错误、cleanup_passed=true、QEMU正常关机exit0、未请求强制退出；最终CLI stop后运行容器为零，测试qcow2/SSH密钥/cloud seed已回收，22152监听与QEMU进程均不存在。物理宿主containers、Docker配置/服务/unit、netns、networks、nft、IPv4/IPv6 routes、volumes十项基线全部相同。全程没有宿主Docker写操作或停止现有容器。历史失败轮的私有诊断磁盘仍保留，不声称全部历史文件已删除。

本轮标准主机install/configure/enroll来自r18实际通过的同一测试主机/workspace，r23在其关闭磁盘的独立叠层中完整重跑业务；summary明确保留继承与本轮两组检查，不虚称重新安装或重复构建。冻结源码`1f287f49f98e996b90184dd6ec383c22bd6a89e4c7aec78b11dde9ff0f10c870`，实际CLI/host服务程序见§17.10，Go1.26.10；真实Forgejo `17.0.1+gitea-1.22.0`、PostgreSQL、amd64容器Runner FP `3e03737b6bb78b96b82b7a5036919a23a5ca26620a09b504451144933db6e940`。controller及Incus CLI运行ELF核对通过；Samba候选image `sha256:c183739a3c3aa128240176f233e917e2409a9222a250c6a8ea4176919fc60c52`，公开归档132,998,480bytes，SHA256 `2af807ec1c5c003886319a6af935dce51132a14395318ad5c647bed6e1bdf4a6`，仍为§17.9明确的实验增量镜像，未正式发布。夹具SHA256 `d19c8b4e4d26b8769323856ca13d760f8ac31f459151b5703136b0de1a218f62`与仓库新增脚本一致。

| 适用需求/检查 | 实际结果 |
| --- | --- |
| R-052 关闭基线 | Actions=false，controller正常退出0；真实安全ELF匹配 |
| R-052 单开关启用 | 正式config set/apply同时启用服务端和controller；仓库功能单元按上游API准备，不引入第二个Module Runner开关 |
| R-052 真实作业 | 默认网络策略下workflow成功，注册/执行实例/root disk全部回收，无额外放行规则 |
| R-052 运行中关闭 | 先确认作业步骤已运行、恰有一个registration和实例；644次独立观察中确认Actions API仍可查询时三类资源已清空；controller退出0、旧管理凭据401/403 |
| R-052 重新启用 | 同一程序账号数值ID2恢复，旧registration/实例/root disk均为零 |
| R-052 最终停止 | 再关闭开关后普通`anas stop -w WORKSPACE -json`成功，运行容器为零；外层正常退出和十项宿主基线全部通过 |
| R-053 | 当前容器Runner功能结果保持；amd64 VM Runner档尚未执行，整体仍未完成 |

结论：**R-052通过**，不再以完整Core单开关作为阻塞。**Actions整体仍不可标release**，R-053的VM Runner及其他未完成适用项继续按原矩阵执行；ARM64仅按用户决定延期，没有推断为已通过。没有新豁免、改需求或发布制品。普通CLI stop的夹具参数错误及r18—r22失败原始公开报告一并保留。

原始r23公开报告归档SHA256 `9c624fe4a5df015dc9ebe0b47d800643a3132ffe60ec7cf6357589bc1475d26e`；共享完整证据归档及摘要在下面补记。归档只含公开报告、基线、输入manifest和测试源码，不含runtime配置、合成凭据、SSH私钥或运行磁盘。

共享持久归档（ln）：`/data/anas-evidence/forgejo-incus-20261030/core-r23-checkpoint-20261101.tar.gz`，1,191,356,601bytes、305文件，SHA256 `e6d618135ae43a76a9f009226c3398e68625f7d3ab0b6182e2a43a9d842ef69b`。包含已完成Core各轮有限公开报告、DNS红绿对照及候选公共制品、输入manifest和可获得的owner/driver源码；`evidence-manifest.json`逐文件绑定摘要。历史轮未留存执行时owner源码的，不用当前owner快照补称历史字节一致；r18后各轮以对应supervisor-source-identity和owner-source为准。最终r23 owner/helper及driver均有对应快照。下一步为独立R-053 amd64 VM Runner剩余门禁，本次Core任务已完成。


### 17.13 amd64 VM Runner验收历史（2026-11-01，最终结算见§17.14）

用户要求继续补齐R-053，明确禁止停止或修改现有宿主容器。ln只读检查确认嵌套KVM=Y，开始前MemAvailable约5.2GiB，无其他测试QEMU。新`vm-runner-lab/r1-vm-bake`为1536MiB/2CPU、loopback SSH22153、独立cloud身份`anas-runner-bake-09ad00`，从同一Debian13基础镜像启动；只执行新VM制品烘焙，不在这个小内存VM里启动4GiB Runner。外层owner保留512MiB物理余量门禁及十项宿主只读前后核对。

新VM recipe由当前正式artifact CLI生成，与已验收r2容器recipe的差异仅为VM内核/EFI包、fstab/incus-agent及10GiB ext4磁盘target；Runner配置、脚本和Podman策略一致。制品revision `native-20261101-vm-r1`、Runner13.2.0摘要沿用固定输入，未修改已记录容器制品。完整VM实测前不宣称通过。

现有image/onejob夹具新增`--isolation vm`，明确校验incus_vm/disk.qcow2及独立fingerprint，保留2CPU/4096MiB/20GiB默认资源；Go原生夹具从同一受保护lease读取资源并拒绝错误预算。新增容器制品冒充VM、缺VM磁盘及字节篡改反例通过；Python共30项、computeclient/controller两组Go回归通过。测试程序由原冻结源码1f287…加两个原生测试文件覆盖在finance Go1.26.10重编，生产源码未变；manifest和ELF摘要保留。较大VM制品仅在测试VM内以磁盘支撑固定/run/anas供给目录，不降低完整性或权限检查、不占用内存临时盘。

下一步：完成VM镜像build/export/reuse，按实时内存准入启动独立大内存外层VM，执行镜像/engine门禁、五场景workflow、repo/org无Secret构建与回收。宿主容器始终只读；资源不足时不降低VM配额或借swap伪称通过。R-053仍未完成。

VM执行准备更新：HDD轮r1在debootstrap/包配置阶段多次出现100秒级ext4 journal提交等待，磁盘空间充足、非内存不足；经精确cloud-id与CLI路径/revision核对，仅向该测试builder发送SIGTERM。该轮bake exit1（人为中断，不归因产品故障），QEMU正常关机0、10类宿主基线相同。原始公开报告摘要`e2f1112bbddc0c17e321d77828e416508d52bb1d1cfc7d074e1331d2d059804f`；旧私有测试盘保留。

同配方改到ln私有SSD测试目录`/home/whl/anas-amd64-gates-20261030-ln/vm-runner-ssd`，宿主根SSD初始约16GiB可用。owner启动前要求至少12GiB空闲，以prlimit将测试QEMU可写文件限制8GiB，保留4GiB余量；宿主Docker及其目录不动。ext4不适用Btrfs的chattr +C，首次准备在启动前即失败，保留空目录；修正为按实际文件系统处理后，r2-vm-bake-ssd已启动，1536MiB/2CPU、cloud-id `anas-runner-bake-d0acaa`、SSH22154、制品revision `native-20261101-vm-r2`。

为避免重复安装，使用已正常关闭的旧r2容器实验基础盘展平副本（无operator凭据；仅该轮合成配置及公开缓存），11,259,019,264bytes，SHA256 `ef361e60963d3a2a7b7c67c9f83d4ee270458b852e41ef27a687c94b973524ee`。新workspace前核对空Incus daemon，将旧报告/实验archive留存到新VM私有目录，不覆盖旧证据、不继承其通过状态。此私有基础盘不进入公开证据归档。VM recipe SHA256 `4e2029479eec500ccf232cd32ff448befa24626dd69eccab6caab4074ccdb35d`，与HDD轮一致。

两个Go测试覆盖文件SHA256分别为onejob `9fd4b595e53aa17ac548d8d4b247d7f6050d2b35de06f07f0db7204f0e85904d`、image `3940530f51d38f02312f2d543736b942df6ed7c32e34871c7a4a4420a37b9e34`；finance重编controller测试ELF `b49f8b7c458de41b8261282c0b2b2fe5902163123289fc2bc0f09884397e2f75`、computeclient测试ELF `ef9dd99bb0b8810f1638ecc6fc36850df5fa2208b5f46d005021e75c89550062`，两者退出0。其他生产输入仍按原冻结基线。

SSD后续：r2在上传准备时因旧空`/home/anas-test/lifecycle-inputs`目录已存在而退出，无业务/构建执行；修正为明确检查同属测试用户、非链接且空目录后复用，不删除未知内容。r3-vm-bake-ssd（身份`anas-runner-bake-325051`）完成真实build/export/reuse，bake exit0、QEMU正常退出0、测试盘/SSH身份回收、10类宿主基线全等。首次build existing=false，再build existing=true且release完全相同。公开原始报告SHA256 `bb3ebb092383dbdb271f14dfd60d0d9966cd675a509f93f00d48c72a006584e9`。

VM制品revision `native-20261101-vm-r2`，FP `c045d5bab9baabf0b69801a1edd28a1ee84d780300c447de1574e03d62e82591`，recipe_digest `3b5b3040c3f5bd9497956925494e1ac99c71d8dde0729bd4b709223a2d323057`；split元数据696bytes/SHA256 `5ebec068d69a6c9287caa26366fd4bc7728ca3fe698304846b28f904130edbf7`，disk.qcow2 352,719,360bytes/SHA256 `129923fa82e169641ccb06e1f9ca6595bf96b80b386f7e822283b984a1fa26a1`。独立first-build记录作为expected fingerprint随冻结输入传给执行轮，不从待验制品自报值推定通过。r4-vm-runner计划4864MiB/2CPU、SSH22154，镜像/onejob/repo构建/org构建四阶段；当前准备中，R-053仍未通过。

R4执行准备因dpkg-query查询不存在的`qemu-system-x86`包而失败（Incus自带QEMU；未执行VM门禁），已移除错误查询，正常退出及宿主基线均通过。r5通过内存准入（5954400KiB可用，外层4864MiB加512MiB真实余量）、制品导入/复用，实际实例type=virtual-machine/Running；随后镜像smoke在来宾入口前超时失败，R-053未通过。

发现真实产品缺陷：VM配方只安装EFI/内核包，未在post-files安装引导文件。UEFI日志显示磁盘启动项Not Found并转PXE；对固定导出qcow2做VM内只读NBD分区检查确认GPT/EFI分区存在，但`EFI/BOOT/BOOTX64.EFI`、`EFI/debian/grubx64.efi`和`EFI/debian/shimx64.efi`全部不存在。诊断连接均已断开，未改导出字节或关闭Secure Boot。第一次两个分区探针因NBD就绪异步而过早查询失败；增加等待后获得上述只读实测，不将探针准备错误混为产品故障。原始有限结论及开机日志在r5公开报告。

按[上游Debian配方](https://github.com/lxc/lxc-ci/blob/main/images/debian.yaml)的post-files步骤修复：明确增加对应架构的Debian签名GRUB和shim包，生成EFI/BOOT fallback及EFI/debian入口，按`DISTROBUILDER_ROOT_UUID`绑定根文件系统。两个包是VM签名引导所需，不新增Go依赖/产品服务，不通过改成Secure Boot=false绕过。ARM64只做静态生成/官方包名核对，实机仍按约定延期。新的生成回归校验action阶段、VM限定、目标架构、签名包和根UUID；computeimage/artifact CLI两组Go测试通过，双语技术文档、Runner README、module文档生成检查同步。

旧容器配方重新生成SHA256仍为`85ff927a36fd0253b0566cf94ff87f879262dfc03508b786cc9260f825dda495`，与已验收r2字节相同。新VM配方SHA256 `ba6bac7afb39a71d1c2f19f48543910813a030adfa640d214693567880c93e14`，准备以不可变新修订`native-20261101-vm-r3`在SSD轮r6重建；旧VM-r2失败制品保留，不覆写、不提前声称引导已通过。

R6新EFI配方正式build在hook阶段失败，bake exit1、QEMU正常退出0、10项宿主基线全部相同，原始公开报告SHA256 `37415808460e5a322d4e2bae885d01977c25b915defe33d0b9b4f4d39c4bc203`。自动审批拒绝原始stderr经SSH输出，改用VM内固定错误类别；没有导出私有原始日志。独立诊断使用相同已核对builder/recipe、全新私有cache，仅保留raw log在VM内，诊断产物不记录为制品；闭合类别定位`update-grub: not found`。包状态实测kernel/GRUB/shim全部未安装，确认第二个产品缺陷：VM包集的raw架构过滤（x86_64/aarch64）未匹配Debian映射架构，整个包集被静默跳过。

修复直接移除这两个冗余过滤：包名本来已由Go生成器按目标架构选择，VM目标才添加该集合，无需再让builder过滤一次。新增解析后的包集回归，确保kernel/对应签名GRUB/shim存在且不受架构过滤；Go computeimage/artifact CLI回归通过。新配方SHA256 `ce4c985dd5759ae4dc0003de7f0859f63bfee166cff438ece9fcc98dbe729135`，准备使用独立VM-r4修订重建；仍未声称可启动。R4/R5诊断盘已逐文件SHA核对后迁至同机/data私有目录，SSD空间余量受保护，实际位置/摘要见各轮storage-relocation.json；不进入公开归档。

R7修复版VM-r4已完成build/export/reuse（首次existing=false、再执行existing=true且release一致），bake exit0、正常关机0、测试磁盘/身份回收、10项宿主基线全部相同。构建内实测linux-image-amd64、grub-efi-amd64、grub-efi-amd64-signed、shim-signed四包均install ok installed；版本和有限包状态记录在公开vm-package-probe.json。原始报告SHA256 `337fd991728952f9f05a98e4861f4a4c7429895de6e840ef38a0468f79eb7d71`。

新VM FP `5f3df49c805164d3eef535aa9357de804185a9ee17f98fea825f9bf64e012f17`，recipe_digest `5d1dbbf01581eb6892753fb10c530657d2ff8c1a58624c56865908491d104965`。元数据696bytes/SHA256 `88b8ef8c0767c5b9e1398ab72683b57b019ed028f45789b446fcf9765a465fe4`，disk.qcow2 539,681,792bytes/SHA256 `81a11eea94a8edc119feac349c46f2e2648f8b470c35994bb43c91aaf89dbd19`；导出归档533,685,821bytes/SHA256 `0fe0e2d703e33617f9aa43528aae26b9ec8c087a32628c0f7a787df4a88c928d`。旧VM-r2不被覆写。R3/R7公开bake归档经SHA校验迁往同机/data public-bake-checkpoints目录，原路径/实际位置/摘要见archive-relocation.json；R6私有盘同样校验迁移，均未丢弃证据。

实际产品修复源forgejo_recipe.go SHA256 `4bb446cdd68791bdb4812ad03ae325dd2bf32a074d0c6497aa7c99448c710867`，新增回归文件摘要`2682295c3c453ab42a16898ac0cd26b11a8db2ee5e40013406405bf72b6df35e`。finance固定Go1.26.10上computeimage/artifact CLI测试、CLI编译及Linux配方生成均exit0，配方SHA与ce4c…相同；新CLI验证ELF SHA256 `eb3873f20239a67fefc652e724edaa755b84ccf531b8bf5c9f538b1bf4003b8f`，该验证不混称实际烘焙使用的新二进制（烘焙沿用已固定builder CLI8fd9…，仅显式recipe输入更新）。源码/两轮Linux验证元数据保存在vm-efi-source，测试源码覆盖仍由各轮输入manifest分别绑定。文档生成检查与VitePress构建通过（16.31秒）。

R8已准备并启动完整执行轮：4864MiB/2CPU，身份`anas-runner-bake-75fa1f`、SSH22154，使用上述新VM-r4固定字节。先image，再onejob/repo构建/org构建；现有独立观察要求实际实例type=virtual-machine且security.secureboot=true。未完成前R-053保持待验收，宿主容器始终不停止、不修改。

R8修复镜像已实测成功开机，实际实例virtual-machine/security.secureboot=true，真实Runner程序、one-job接口、配置权限、namespace策略、rootless engine API、user session、OCI限额均通过；完整image门禁仍失败于provider-namespace-fence。有限源行/错误字段确认仅`restricted.containers.nesting`断言不匹配：原夹具把容器档的allow/true套到VM档，而现行Provider对VM项目声明block、profile.security.nesting=false。这是夹具档位断言错误，不是放宽产品策略的理由。

修正原生镜像夹具为按lease档位验证：VM需禁止容器、禁止再嵌套、禁止VM lowlevel/raw QEMU及宿主设备挂载，保留容器档原策略；额外真实反例尝试raw.qemu与security.nesting覆盖必须被拒绝。computeclient Go回归通过。finance Go1.26.10用显式Go overlay重编测试（原冻结生产源码不变），新测试源SHA256 `33401d8aa839beb9b55fdf7364c1d4e037152c7030718c869ca2b2f2073cab8d`，新computeclient测试ELF `b40007bd7e40986aa8b96465a9b1bf0182eb0ee1d2e7d6d0afc493992a2fc95d`；不改实际镜像或Provider。R8image exit1保留，正常关机0、10类宿主基线全等，原始公开报告SHA256 `80a0064103e201add55bbe7184314adb4094c9e2f1ed862325dca7271140207b`，其余VM作业门禁尚未执行。

R9image仍exit1，失败在负向覆盖用例（配置核对已通过，其他Runner/engine/OCI子项通过），不能把配置核对通过写成整个隔离门禁通过。正常关机0、10类宿主基线一致，公开报告SHA256 `b3aae023246f86be429a6ce3de7b895c21bd3e908188209930698d9ceac6ec59`。复核[Incus选项文档](https://linuxcontainers.org/incus/docs/main/reference/instance_options/)后，修正负向用例的适用类型：security.privileged/raw.lxc为容器专用，不能将VM接受这些元数据直接判为VM越权；VM仍必须拒绝raw.qemu、宿主设备，以及有对应API扩展时的security.nesting=true。旧daemon无VM nesting扩展时按既有Provider契约核对缺省键，不将新版本字段强加到旧接口；能力由测试VM本地管理员读取，未知/失败不能当作不支持。

最终待重跑原生镜像测试源SHA256 `5ca4c8cbd592473318de143f90e98bbea6482b31d335f010ddfc2d622d97fc70`，finance Go1.26.10重编computeclient测试ELF `2864a5ac5739574562046fa63c91b7a3ef878f9254a7d091143289ab57ec00ee`；生产Provider及固定VM-r4字节不变。每轮测试程序/有限错误均保留，R9未计通过，不豁免真正的VM raw/device隔离门槛。

R10镜像门禁完整通过（实际VM/Secure Boot、适用namespace/device/raw QEMU反例、Runner/engine/OCI限额与回收）；五场景业务检查也通过：normal120.462s、failure75.378s、SIGKILL crash/recovery131.391s、SIGTERM cancellation67.575s、未授权仓库保持waiting且无资源，均state/registration/实例/root disk清空，实际Secure Boot VM已独立观察。随后夹具关闭Forgejo进程超时强杀，onejob整体exit1，不计整轮通过，不继续构建阶段。

发现夹具只给Forgejo15秒退出预算，短于上游默认60秒graceful hammer及正式Compose已有150秒stop_grace_period；改为同样150秒有界等待，仍不允许强杀作为通过。新增有限进程清理JSON（预算/耗时/退出码/强杀标记），且将summary通过写入延迟到所有进程及daemon资源清理成功之后；失败时不得留下passed=true。原17项Python夹具回归通过，正式产品/镜像/配额均未变。将以此修正夹具重跑完整image/五场景/repo+org构建。此前五场景功能结果与整体失败同时保留。

R11image通过，onejob在normal已通过、failure API已终态时出现AttributeError，整轮未通过；旧错误输出无源行，无法确证原异常来源。补强实例观察对过渡行空config的处理：只有完整同档实例和Secure Boot=true可作证据；未完整行最多5秒有界重读，不能当作已回收；错误档位/实际Secure Boot=false立即失败。新增离线反例后19项通过；异常只新增有限源行与固定属性名定位，不输出原始堆栈/私密值。新onejob脚本SHA256 `3192c6d73c2a8ed0a2f0be9c48f7cc3a11d6b26df6b54c15b7f4b62d158bdd56`。R11正常退出及10项宿主基线通过，公开报告SHA256 `418e76bf4625eeac58c33510484f10149a2456145fde3ecbea19abac8e60d3cc`，未把推测当已证根因。

R12image/完整五场景与正常进程清理均exit0：normal105.873s、failure75.161s、SIGKILL恢复111.082s、SIGTERM取消64.738s，未授权仓库仍waiting，无registration/实例/root disk；fixture_cleanup_passed=true。随后buildrepo准备被旧基础盘`/var/lib/anas-private-completed`同名历史目录挡住，尚未执行构建。VM内有限目录探针确认before-buildrepo两个旧目标均已存在、当前/run业务目录均未移动。修正准备脚本，把该旧实验目录与旧报告/旧export一起移入新VM私有prior-fixture目录；不删旧数据，不改正式产品或已固定VM-r4。将完整重跑以补repo/org构建及最终结算。

测试owner仅在这个隔离QEMU的可写盘启用discard/unmap，阶段成功且资源回收后fstrim根盘并记录实际文件大小/分配块/宿主空间；避免连续创建删除Btrfs loop造成历史空闲块累计。仍保留8GiB单文件上限、启动前12GiB空闲（4GiB余量）及4864MiB+512MiB真实内存门槛。R11/R12镜像阶段实际trim exit0；不对任何宿主卷执行trim，不放宽资源或业务验收条件。失败私有盘仍经SHA确认迁往同机/data；大公开输入可按摘要共用，实际位置见逐轮relocation元数据。


### 17.14 R-053 amd64 VM Runner最终验收（2026-11-01，通过）

本轮用户确认的amd64范围已通过；ARM64延期。最终独立执行轮`vm-runner-ssd/r13-vm-runner`，cloud身份`anas-runner-bake-ff209f`，外层4864MiB/2CPU，内层真实VM Runner固定4096MiB/2CPU/20GiB，Secure Boot=true；未降低配额，未停止或修改任何现有物理宿主容器。前面失败轮保留原判定及证据，不以最终通过覆盖历史。

| R-053检查 | 最终r13结果 |
| --- | --- |
| 固定VM-r4导入/复用、启动、Runner/engine/OCI限额及适用隔离反例 | image exit0；VM类型和Secure Boot独立观察，raw QEMU/宿主设备等越权反例拒绝 |
| 正常工作流 / 显式失败 | success108.303s / failure71.311s；均回收实例、root disk、registration并清空state |
| controller crash / 取消 | SIGKILL后保留state恢复success114.260s；运行中SIGTERM取消69.821s；均完成上述回收 |
| 未获批仓库 | 保持waiting，无Runner registration或执行实例 |
| 获批repo真实无Secret容器构建 | success106.124s；镜像/容器实际构建、产物字节检查及删除，执行资源全回收 |
| 获批org真实无Secret容器构建 | success106.255s；正确org端点观察，构建与回收同上 |
| 服务进程及测试环境清理 | onejob/buildrepo/buildorg均fixture_cleanup_passed=true；Forgejo正常退出0且无强杀（150秒预算）、PostgreSQL退出0；外层QEMU正常退出0 |
| 物理宿主不变 | containers/docker_config/docker_services/docker_unit_digest/named_netns/networks/nft_sha256/routes4/routes6/volumes十项前后全部相同；仅只读Docker检查 |

四阶段image/onejob/buildrepo/buildorg全部exit0，supervisor_error=null、cleanup_errors为空、forced_quit_requested=false。当前测试磁盘、SSH身份及cloud-init种子均已删除。原始有限公开报告归档SHA256 `4664ca3220ed55ad89882e3af36e41e57ff268ccdae787dfb1eca46e945ec34f`。

源码和制品以§17.13的冻结身份为准：原生产源码基线1f287…，实际修复配方源码4bb446…及VM配方ce4c…；VM-r4 FP `5f3df49c805164d3eef535aa9357de804185a9ee17f98fea825f9bf64e012f17`、导出归档SHA256 `0fe0e2d703e33617f9aa43528aae26b9ec8c087a32628c0f7a787df4a88c928d`。最终输入manifest逐文件绑定执行字节；computeclient测试ELF2864…、controller测试ELFb49f…，完整摘要及测试覆盖源码在归档。两个真实产品缺陷（VM包集架构过滤导致未装内核/GRUB/shim、缺EFI引导安装）均已修复并用真实开机/工作流验证。

验证：computeimage/artifact CLI/computeclient/actions-controller四组Go测试通过；Python夹具19+13=32项通过；module文档生成检查及VitePress构建通过。最终五场景取消特指controller-context-SIGTERM，崩溃恢复保留state；本轮未执行Forgejo网页取消或state volume丢失，不把它们混称已执行。独立state丢失要求仍按R-046既有证据跟踪。

R-052与本轮R-053的amd64验收已补齐；这不把所有M2/M3/M4/M5或正式发布状态自动改为完成。VM-r4为固定实验候选，未签名发布到正式Runner目录；后续发布应按既定制品签收和剩余需求复核，不再将VM业务验收列为未执行。


共享持久证据归档（ln）：`/data/anas-evidence/forgejo-incus-20261030/vm-runner-r13-vm-runner-checkpoint-20261101.tar.gz`，1,908,119,211bytes、297文件（48份按SHA去重公共输入），SHA256 `1a181b96d36c31e81bafda7bac6bd3dec448782fe5853780ad4781234801097d`。归档前独立解析最终四阶段结果、各scope summary、进程清理、资源清理、Secure Boot/配额/FP及十项宿主基线，全部断言通过。包括原冻结源码/程序、测试覆盖源码、逐轮输入manifest和公开失败/成功报告，`evidence-manifest.json`绑定字节；不含合成凭据、私钥、运行配置、私有构建日志或测试磁盘。

最终文档门禁：`docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status`均通过。首次requirements命令因本机默认Go缓存读权限退出，改用`GOCACHE=/tmp/anas-r053-go-cache`后完整通过，未改变检查内容。下一步为正式Runner制品签收及计划其余发布要求复核；R-053本轮amd64执行验收无需继续补跑。


### 17.15 正式Runner固定字节签收与待发布包（2026-11-01）

用户明确选择“先保存在ln持久制品目录，完成待发布包”，不执行公开发布。签收沿用原不可变修订，不重烘焙、不改指纹、不伪造正式Module revision。现有工具没有独立的catalog加密签名实现；此前“签名发布”的泛称不能作为已存在签名服务的证据。本次由独立期望指纹、冻结配方/构建输入、SSH受控传输及Module payload摘要绑定交付，未生成独立密码学签名。

持久根目录：ln `/data/anas-artifacts-20261101-forgejo-runner`；目录0700。`images/`含两个已实机验收的amd64版本，`archive/`保存恢复后的不可变历史，`source.tar.gz`及输入配方保留来源。源归档3238文件/18,105,536bytes、SHA256 `ca56faa8062ee25771e0487d453ecc01d7ca27c751366565ffcc6eb9095bbe7f`；基于工作树，不冒充已提交的正式发布源码。operator仓库的catalog仍为[]，制品只叠加到独立打包副本，不向Git添加大镜像字节、不修改宿主Docker。

| 交付文件（根目录packages/） | bytes | SHA256 |
| --- | --- | --- |
| `runner-images-amd64-20261101.tar.gz` | 779,458,053 | `6ae74e5d665606d22424641b29878f94cec77b50fac238c9778c0f3dde32e277` |
| `anas-module-incus-amd64.tar.gz` | 781,320,991 | `29d6f53b8ee58593770b5eb494bf8968f259b5c9b7f00d5425bf38a8ff362214` |
| `anas-module-forgejo-amd64.tar.gz` | 1,584,635 | `110dca04505035df91ab60e6f496a05a983b5c2194cabb4b296b001295949fa1` |
| `anas-samba-dc-amd64-full-20261101.tar.gz` | 132,968,218 | `51393c76cf681870e9ab11cc14c83f126797f68ea2663858145346d5df0d53f2` |

目录逻辑摘要`655617452177a2a75fff0b7fc6aecd84f308a27888a545791aaf84a8ca4c2486`，catalog.json字节摘要`a5c9bcd26a465a1e6b44b4932d987513302104265a02aa0e43354f50faf49e66`，二者含义不同。条目为`native-20261031-r2`/incus_container/FP3e037…、`native-20261101-vm-r4`/incus_vm/FP5f3df…，完整身份沿用§17.4/17.13/17.14。Module payload摘要：Incus `9780b80c73313244c770ec11d3e2fead3627eb5ff9f9b38753f35d2a03e4e294`，Forgejo `3640fd70bd24867b6e2e829e73d43fee90c1a956fcb251f124cbe2f716278d60`。

实际签收顺序：从独立已验收导出读取相同三个成员；核对导出/每部分/组合指纹；重建原冻结配方（含builder cc942…与Runner fadaec…来源注释）并核对原recipe_digest；正式CLI Record首次existing=false、再次existing=true且release结构完全相同；Bundle显式first-release输出两条目录及完整字节。新Linux artifact CLI摘要`ab3d20c9e65c0e935289be55ee178d68eab11cd8ec11804d2e01c8e8b196c35f`。未把普通raw recipe哈希混为含构建来源的recipe_digest。

使用Go1.26.10在ln构建真实linux/amd64预编译Hook及两个Module包，不启用skip-hook-build；独立解包后VerifyUnpacked通过。九组包验证全部通过：两个payload、两档命名引用Freeze及全字节VerifyArtifactRelease、两档ARM64不能替代、历史key不可替换、目录字节篡改拒绝、镜像缺件拒绝与恢复、真实ELFamd64 Hook。最终有限结果在`reports/package-acceptance-r2.json`。首次反例夹具直接写0400只读目录文件而失败，修正只对解包测试副本临时改mode并在校验前恢复，产品原件不变；首轮失败另存。ln自动下载Go1.26.10的IPv6超时另存，改用finance既有工具链归档（SHA256 `177d33f901e4f694b2a6b2c843044901d31d906d173b620dee14991b7ae1cffc`），不修改主机网络/服务。

本轮新增回归：computeimage/artifact CLI/modulepackage/Forgejo hook/应用helper/controller/Incus hook通过；Incus provisioner首次因本机IPv6监听权限失败，允许监听后同命令通过；相关包vet、Core供给边界测试、shared-build、module文档检查通过。upgrade catalog首次暴露共享树Casdoor manifest r13与目录r12漂移，仅同步该目录current及两条transition的to端点为现有r13，未更改Casdoor实现；复测26模块/22transition通过。这不是新增Casdoor升级E2E通过声明。

对照已验收冻结源码，Forgejo/controller/Provider生产Go实现没有变化；新生产输入为已实测的VM配方修复和Incus Dockerfile Go1.26.10固定。文档/测试覆盖与未打包历史Hook二进制差异单列于`reports/source-delta.json`，不把整库工作树误认为单一已验收提交。

后续发布必须保留原guest修订/指纹，按image-release流程分配最终Module revision并绑定对应OCI镜像；Module包不嵌入伴随OCI镜像。不得直接从仍为空的operator源码catalog重新打包而丢弃这里的images/。本轮不自动上传registry或GitHub，不创建额外签名服务。正式Samba全量构建与Core接续另结算。

### 17.16 正式Samba Dockerfile与Core发布依赖复核（2026-11-01—02，已完成）

按用户“补齐剩余发布门禁”继续消除§17.12中只有实验增量Samba镜像的交付限制。源码对照原冻结基线确认Samba运行源码仅两处已验收DNS修复及文档变化，未借此接入其他新功能。新的隔离QEMU从正常关闭的r18标准主机测试盘创建独立叠层；基础私有盘SHA256 `24d6d69c00ad14fac5d30cd874d7509cc31ca38cde4f066454b40887616a7ca1`，其只读父盘仍为官方Debian13/dd7f…；不读取operator凭据、不写宿主Docker。

外层3072MiB/2CPU、真实内存余量512MiB，SSD启动前12GiB可用/8GiB测试文件上限/4GiB保留继续适用。r1身份`anas-runner-bake-b91945`、SSH22155，完整原Samba Dockerfile构建后，将固定公开镜像保存并以原r23夹具复验Core；同一VM内独立观察实际新Samba镜像健康，不把仅build退出0作为联动通过。

r1全量构建退出1：测试驱动将`APT_MIRROR_URL`误设为`https://mirrors.aliyun.com/ubuntu`，而产品Dockerfile会保留源URL的`/ubuntu`部分，实际生成`mirrors.aliyun.com/ubuntu/ubuntu`并404。VM内有限分类已独立确认重复路径与404，未输出原私有构建日志。这是本轮测试参数错误，不归为产品Dockerfile缺陷；驱动改回仓库规定默认`https://mirrors.aliyun.com`，计划以新轮重跑，原失败不豁免。

全库文档门禁首次因并行邮件需求表空行使新增R-037—R-041脱离矩阵而失败；仅合并该表的空行格式后，需求覆盖31文档/1003条通过。随后两个邮件测试implementation_digest过期，审阅diff仅为现有迁移测试的schema版本26→27；其摘要在并行工作中已同步，本轮未覆盖别人的更新。store本地回归通过（未配置实际PG，不混称邮件原生E2E），重新生成/校验18主题204活动用例通过。VitePress完整构建通过17.98秒。以上不把邮件新API或整体邮件Module记为已验收。

2026-11-01再核对[Forgejo官方17.x发布页](https://forgejo.org/releases/17.x/)，该系列当前Stable仍为17.0.1，本轮未再次改变固定Forgejo版本，不触发一轮新的R-066版本变更探针。Runner固定13.2.0及既有报告型漏洞扫描边界沿用§17.1；不声称上游二进制零已知风险，也未把原reporting-only扫描临时改成新增阻断标准。

2026-11-02接续：r2首次因SSD空闲不足12GiB在启动前被拒绝，无VM或业务执行；保留原准入失败日志。仅将已完成r13及原inputs的两个重复VM导出副本与同机/data既有原件逐SHA核对后迁出SSD，未降低余量门槛，实际位置分别记source-artifact-relocation/input-export-relocation。r2随后使用同一未启动过的准备身份`anas-runner-bake-e3ddd2`启动。

r2完整正式Samba Dockerfile构建exit0，实际版本`4.23.6-Ubuntu-4.23.6+dfsg-1ubuntu2.2`，两处DNS运行文件SHA与已验收修复完全相同。Docker image ID `sha256:72b64903450dfca1f3be21c4394dd013408c6200560a3b6a727ceb1c37bcc3ac`（这是本地image ID，不冒充registry manifest digest）；公开Docker-save归档132,968,218bytes/SHA256 `51393c76cf681870e9ab11cc14c83f126797f68ea2663858145346d5df0d53f2`，已独立核对并保存到持久根目录packages/`anas-samba-dc-amd64-full-20261101.tar.gz`。r14仅沿用临时E2E投影，不手工决定正式发布revision。

随后Core在`prepare_existing_repository_unit`出现URLError，core exit1，未进入本轮普通apply/真实workflow；不计整体通过。夹具在构建前显式停止了合成测试容器，但未恢复接续所需的长期服务。旧输出未保留具体URL reason，不断言所有网络原因均已证实。修正准备为恢复声明的长期服务，并在原300秒API启动预算内只重试ConnectionRefusedError/TimeoutError；HTTP401/403、TLS证书失败及其他URL错误仍立即失败。修改限测试脚本，不改产品或跳过TLS校验。

r2正常关机0、cleanup_passed=true、10项宿主基线全等；原始公开报告SHA256 `203203f3c0db55329bc6156aa83dcdf2b8a5d3ea911eee3b13f98561bbe242b0`。r1也正常关机0及10项基线相同，原始公开报告SHA256 `bc530ffec60cbce1709d0b71655de2235220a2ee4acca2b0c3d2c06296ee4eb5`。失败私有盘均逐SHA确认留存/data，位置见各轮storage-relocation.json，未进入公开归档。

r3身份`anas-runner-bake-268d02`/SSH22155，复用上述固定Samba归档与image ID，不再次烘焙。测试wrapper SHA256 `9987af2853eb83d5349e28a847b2b50d4897ac27da170c1609fd3495968f96f9`，Core夹具`ac169861044d6e6fd2fe36f0bb019812cbbca3dd660879522e508ae72fd9b72d`；新owner `c9bdbffa1781a8fd196e07018528012dc02c5c491a1eee75b51aa86dc55ad530`在prepare记录自身/helper摘要及快照，run前拒绝字节变化。r1/r2仅有结算时同版owner快照，明确不是启动前记录，不把新owner倒填为历史执行字节。

一次Core脚本上传因自动审批超时未执行，按提示重试后完成；另一次wrapper传输为网络超时，核对服务器原主机密钥后重传并逐SHA确认。SSH中断未代替服务器结果判定；未改主机DNS/网络/服务。VitePress在Incus中英文旧描述纠正后再次全量构建通过17.48秒。

r3完整Core业务exit0（真实workflow、运行中关闭/撤权、重新启用、普通stop均通过），但外层独立观察未见新Samba image ID健康运行，组合门禁exit1，不计通过。原模块修订r14与既有冻结部署相同，复用旧镜像是待核对的原因；一次只读容器元数据探针因测试引号导致Python语法错误，未读取原始内容、不作为证据。r3正常关机0、清理及10项宿主基线通过，公开报告SHA256 `92941834266861ce8bef6b4535bdc1b72640bd14db963f1964c63d9b5e3a223e`。

r4采用新的临时E2E修订15：先验证旧source_overlays，再在测试副本调和module/localization/Compose/文档投影并复用同一72b…镜像；未更改operator仓库正式revision。原Anchor源码未变，只本地重标记。新准备步骤已通过，但普通CLI的initial_disabled_apply返回RuntimeError（私有命令21），整轮exit1，不能据prepare通过签收运行结果。新wrapper SHA256 `2c762e0db31b316cd21a0441f2d41d3c4e0a0fe44f1bce5d0e02c70b3dff04e6`，Core脚本`cae40f28915572d43d387bf33257b962003ab38f3b43e3bcb10c3553cbb9b1c2`。r4正常关机0、cleanup_passed=true、10项基线全等；原始公开报告SHA256 `df14a65f0f50f63ec7b2cfd42fa0ade42bd36edb3e08791d37e816a276eb3280`。

r4内核/虚拟机日志的有限分类未见OOM、进程被杀、I/O、EXT4、文件上限或空间不足标记；原CLI stdout为64条可解析日志加其他输出，未导出原始消息，目前没有充分依据断言具体产品或准备错误。将从独立只读来源的诊断叠层解析固定错误代码及字段名，不修改宿主容器，不跳过失败门禁。r1—r3公开报告已逐SHA核对迁往/data/anas-evidence/forgejo-incus-20261030/formal-public-checkpoints，实际位置见archive-relocation.json；私有盘另存，未丢弃证据。

2026-11-02诊断补充：r4真正首个失败为私有命令17，公开CLI错误码`lock_stale`；命令21是失败后的Traefik诊断日志，不能据其日志字段判断CLI错误。独立诊断叠层恢复全部原source_overlays摘要后，使用实际`/usr/local/bin/anas config set --root … --defer --update-lock`同参数调用退出0。代码核对确认defer候选校验仍须验证旧锁信任的Module字节，显式update-lock不跳过这一步；测试先把源码投影到r15再config set触发旧锁校验。现将配置修改置于原r14来源校验后、r15投影前，再由普通apply --update-lock执行显式信任更新，不修改产品锁校验规则。新夹具保存首个失败命令编号/程序/退出码/阶段，避免后续诊断增加计数造成误读。诊断仅用于定位，不计验收通过；公开有限报告存交付根reports/cli-diagnostic-public.tgz，私有输出留VM内。诊断VM正常关闭、qemu_exit=0、cleanup_passed=true，未操作宿主Docker。

2026-11-02 r5最终全量依赖联动通过：身份`anas-runner-bake-9986f2`，Core脚本SHA256 `9104a21aadf0a67c3cffb79f814c3d1cd6acaa1cb48a3b8c6a7c088bda8b372b`、wrapper仍为2c762…。原r14来源校验后先完成defer配置，再投影临时r15，由正式CLI apply --update-lock更新信任；全量Samba image ID72b…已独立观察healthy。真实默认网络工作流及回收、运行中关闭（121次观察）与账号撤权、同ID重新启用及零旧资源、普通CLI stop全部通过；Core与组合门禁exit0。QEMU正常退出0、未请求强制退出、cleanup_passed=true，当前叠层/SSH身份/cloud seed已回收；10项物理宿主严格前后对照全部相同。原生公开归档SHA256 `beb4999d9e4d9d2947058bfea7e1745467500bfc141eae62dacd7419c649f4cc`。r1—r4失败历史保留，不以最后成功覆盖。

额外跨日交付目录对照（区别于每轮验收前后对照）9/10相同，routes6严格比较失败原样保留。新增两条anas_bridge上的kernel/local /128路由，逐地址核对恰好对应现存temporary=true、dynamic=true的IPv6地址，没有旧路由删除；r1/r2开始前尚不存在，r3/r4/r5开始前已存在，后三轮各自严格10项对照均通过。未过滤/替换旧基线，不将跨日全部宿主状态宣称完全相同；容器、Docker配置/服务/unit/网络/卷跨日也全部相同。本轮未执行宿主Docker或网络变更。有限分类保存在交付根reports/delivery-long-span-baseline-classification.json。

本轮已确认首发amd64范围的47项要求均结算完成；计划标记done，Module manifest仍保留要求规定的developing，未擅自宣布公开release。ARM64延期、R-069已登记权限偏差、旧未发布兼容不适用的边界不变。交付为用户指定的ln待发布包；后续分配最终Module revision、绑定OCI及公开发布属于下一次发布操作，不作为本轮已执行项。

最终共享持久证据：ln `/data/anas-evidence/forgejo-incus-20261030/runner-release-checkpoint-20261102.tar.gz`，1,401,896,957bytes、146文件，SHA256 `9c7359958ed70b2c865637615cf45e077750ad103c8015f4e735b4ae46e31f3a`。逐SHA绑定交付manifest、四包摘要、原来源快照/冻结配方、五轮公开报告/驱动输入及诊断有限结果；私有日志、凭据、运行配置、VM磁盘不入归档，包字节本身保存在既定持久packages/而不重复嵌入此证据包。`release-manifest.json`与`SHA256SUMS`现包含四个待发布文件，原三文件版另存runner-only，失败历史保留。

本轮最终检查：需求覆盖31文档/1003条、测试目录18主题/204活动用例、需求/计划索引、151文档状态均通过；VitePress完整构建16.46秒。七份公开检查日志持久包`reports/anas-runner-release-completion-checks-20261102.tgz`，2,970bytes、SHA256 `e89d3661211ba7924aeda876d964e52eb79e1f16c4a110fab9f56602f1d17ccd`。之后仅同步私有计划索引状态和落地快照，并重新生成/验证两种索引；未改变生产代码或已签收制品。

归档后独立读回通过：整包SHA/长度、146个成员、每文件摘要、按摘要去重的公开输入及全部source_refs逐项核对，无解包写入；`reports/checkpoint-independent-readback.json`保留结果。当前交付可以进入下一次正式发布操作；本轮不提交Git、不上传OCI或公开Release。
