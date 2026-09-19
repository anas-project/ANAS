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
| M0：Forgejo 应用、数据库、OIDC 与恢复账号 | R-001—R-008 | 已完成；`R-006` 于 2026-09-20 恢复为规范来源（2026-09-13—09-20 期间曾废弃） |
| M1：Git Hooks/local-path import 安全开关 | R-010—R-012 | 已完成 |
| M2：Incus compute contract 与 Provider | R-020—R-023 | 实施中；目录契约、Go 边界和 Incus 适配器已完成单元验证，真实宿主待验收 |
| M3：Actions 单开关与 one-job VM 执行面 | R-030—R-039 | 实施中；Module/controller/guest 资产已接线，真实 one-job 与隔离 E2E 待验收 |
| M4：预热、扩缩容、空闲资源与回收 | R-040—R-045 | 未开始 |
| M5：真实发布验收 | R-050—R-054 | 未开始 |
| M6：外部自动化消费者的边界（AI Agent 依赖） | R-060—R-062 | 未开始 |
| M8：固定版本升级的身份复核 | R-066 | 常设；每次变更 Forgejo 固定版本时执行 |

> M7（LDAP 同步 + OIDC 登录的双源身份）已于 2026-09-20 整体撤回，`R-063`—`R-065` 废弃、实现已删除，
> 因此不再占一个里程碑行。撤回理由与删除清单见 §7.2。

覆盖统计：34 项在册需求全部有且只有一个里程碑归属（`R-063`—`R-065` 已废弃，不计入）；M0/M1 的
12 项已完成，M2/M3 的 14 项处于实现与外部验收阶段。

## 2. 落地快照

| 范围 | 状态 | 当前边界 |
| --- | --- | --- |
| Forgejo `15.0.7-r1` rootless image、只读 rootfs、Web/SSH 入口 | 已实现 | 单元测试覆盖；真实双架构 E2E 待完成 |
| PostgreSQL/MariaDB Resource 与持久数据边界 | 已实现 | 数据迁移、备份恢复仍是 release gate |
| OIDC JIT、应用 Group 门禁、管理员 Group 映射 | 已实现 | 不接 LDAP/SAML，不做双链路 |
| `break_glass` 本地管理员 | 已实现 | apply 已验证；事务式 rotate 未声明 |
| Actions 单一功能开关 | 已接线、待 E2E | 默认关闭；`actions_enabled` 同时投影服务端/controller，无 Runner 第二开关 |
| Git Hooks/local-path import 正向配置 | 已实现 | 默认关闭、独立启停，不增加宿主挂载 |
| Incus VM compute/provider | 实施中 | contract catalog、固定 restricted project/profile 验证和 VM CRUD 已有单测；独立宿主待验收 |
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

## 4. M2：Incus compute contract 与 provider

- 定义中立 compute contract，不在 Core 中按 Forgejo 或 Incus 名称特判；
- 实现 Incus VM provider：restricted project、restricted TLS client、模板、profile、network 和 quota；
- Secret Store 管理 Incus client credential，Forgejo 应用容器不得消费；
- 提供 create、inspect、stop、delete 和 reconcile/janitor 所需的稳定 instance identity；
- 验证独立 Incus 宿主的防火墙、managed network、DNS 和到 Forgejo HTTPS 的最小连通性。

Provider 只能操作 `anas-forgejo-runners` project，不能修改 Incus 全局配置或其他 project。

当前完成：`contracts/compute` 已定义 provider-neutral schema；controller 只通过 `ComputeProvider` 接口
传 instance identity、固定 image fingerprint 与数值配额；Incus 适配器固定 remote/project/profile，
验证 restricted project、四类 project quota、受限 egress 标记、managed NIC，并拒绝 host disk、
physical NIC、cloud-init secret 与任意 device。Incus credential 只投影到 controller，Forgejo service
不再读取 module-wide `.env`。

剩余：把 compute catalog 接入通用 Provider 注册/选择路径；在独立 KVM 宿主创建受限证书、project、
network/profile/storage；验证防火墙、DNS、最小 egress 和 crash 回收。当前 Contract 状态保持 `proposal`，
不能把 Go 适配器单测等同于通用 Contract 已发布。

> **拆分说明。** 通用 Provider Module（`modules/incus`）、多消费者隔离和内嵌 Incus 客户端的迁移已
> 独立跟踪，见[Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md)与其[要求](../../../../dev-docs/requirements/incus-module.md)。
> 本里程碑此后只负责 Forgejo **作为消费者**接入：controller 通过 Contract 调用、行为等价性和
> Forgejo 侧的连通性验收；Provider 自身的实现、隔离与证书轮换不再在本文重复跟踪。

## 5. M3：Actions 单开关与 one-job VM 执行面

- 重新引入唯一的 `forgejo.actions_enabled`；一次调和同时改变 Actions 服务端、controller 与 Runner
  desired state，不增加 `runner.enabled` 或第二个 Module 开关；
- Runner 可以独立打包和发布，但由 Forgejo Actions desired state 自动派生，管理员不手工启用；
- repo/org scope 是授权集合，只接受 `{owner}` 或 `{owner}/{repo}`，拒绝 global runner；
- 为每次执行创建 Forgejo ephemeral runner 和 Incus VM，在 VM 内配置 `runner-agent`、`runner-engine`
  与 rootless Podman，并运行 `forgejo-runner one-job`；
- 禁止 `host` label/privileged/任意 volume；扩容只增加 one-job VM，不复用 Runner；
- runner token 使用一次性 credential lifecycle，只进入 tmpfs，不进入 cloud-init、argv、镜像或日志；
- 配置 egress allowlist、CPU/memory/PID/disk/job timeout 和作业后清理。

批准仓库应能运行无 Secret 的容器构建；未批准仓库没有可用 ANAS Runner；作业无法访问
ANAS 管理面、数据库、目录服务、宿主文件或其他 Runner VM；作业后 VM、root disk 和 token 均消失。

当前完成：唯一 bool 已重新接入 Hook/Compose；开启前强制检查 scope、Incus credential/profile/image；
一次性 preflight 必须在 Forgejo 启动前实际验证 Incus project/quota/profile，避免 server-only 半开启；
controller 每 15 秒轮询 repo/org jobs API，按 handle 创建 ephemeral registration 与一个 VM，持久状态
不含 token；token 只经 Incus exec stdin 进入 guest tmpfs。guest 以独立 Runner/engine 用户运行
`one-job --handle --wait` 和 rootless Podman，capacity=1、无 `host` label、无 privileged、无任意 volume；
全局并发 4、每 scope 并发 2、job timeout 1 小时。关闭开关时 controller 只执行清理后退出。

剩余：构建并签收 amd64/arm64 guest image fingerprint；在真实 Forgejo 15.0.7 + Runner >=12.8 + Incus
环境验证批准/未批准仓库、容器构建、失败/取消/重启、网络隔离、磁盘与 registration 清理。完成这些
E2E 前不把 Actions 标为 release 能力。

## 6. M4：预热、扩缩容与回收

- 默认 `prewarm=0`，controller 轮询获批 scope 的 `actions/runners/jobs`，只在发现 waiting job 后按
  job `handle` 创建 ephemeral Runner 和 VM；
- 管理员未来显式配置预热时，每个启用 scope 最多维持一个尚未启动 Runner 的预热 VM；
- 按队列压力增加独立 VM 数量，受 Incus project 和 scope quota 双重限制；
- janitor 处理排队取消、启动失败、超时和 controller 崩溃；
- 对已经启动 `one-job --handle ... --wait` 但尚未领取任务的 VM 设置 10 分钟 waiting TTL，超时后
  注销 registration、销毁 VM/root disk，并对同一 handle 退避；
- 外置 cache/registry 使用仓库范围短期凭据，不持久化跨信任域 workspace。

正常、失败、取消和 controller crash 四条路径都不得残留有效 runner token、VM 或可挂载 volume。
空队列连续两个调和周期后不得存在 Runner registration、Runner VM 或临时 root disk；30 分钟空闲
验收记录 controller RSS、平均单核 CPU、每 scope API 频率和 Actions 关闭时的 Forgejo 对照基线，门限
分别为 128 MiB、1% 和每 10 秒至多一次。

## 7. M5：发布门禁

- PostgreSQL/MariaDB、amd64/arm64；
- LLNG/Authentik OIDC 浏览器登录、Group 准入/拒绝、管理员降权、IAM-down；
- HTTP/SSH clone/push、LFS、Package、备份恢复、上一 LTS patch/minor 升级回滚；
- repo/org scope、网络隔离、资源限制、token 清理和 VM 回收 E2E；
- 单次切换 `forgejo.actions_enabled` 同时改变服务端和 Runner，且不存在第二个 Runner 开关；
- custom Git Hooks 与 local import 开关的安全回归。

全部通过后才评估 Forgejo 应用 Module 和独立 Runner Module 的 `release` 状态；二者分别发布，Runner
未完成不阻塞纯代码托管 Module。

## 7.1 M6：外部自动化消费者的边界

由 [AI Agent 编排](../../../ai_agent/dev-docs/plans/ai-agent.md) 驱动的三项依赖，本 Module 只负责“不妨碍且可声明”，不实现 Agent 逻辑：

- [ ] OIDC reconcile 增加声明式的 group→team 映射（`--group-team-map` 与登录时移除），映射内容来自
      消费方配置，Module 不硬编码组名；
- [ ] reconcile 明确不触碰由管理端 API 创建的自动化账号、token 与 SSH key，并补单元测试守住；
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

## 10. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `forgejo` 的 README、技术文档与配置参考（中英文） | M1 的 Git Hooks 与 local-path import 开关 | 已完成（§3，2026-08-22） |
| [Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md) §2.2、§2.3 | 身份收敛为 OIDC-only，写明撤回理由、运维必须承担的代价，以及 §2.3 的升级复核四点 | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | 删除目录同步段落与两个配置参数，改写为单链路身份的边界表与「尚未复核」项 | 已完成（2026-09-20） |
| [Module IAM / OIDC 支持清单](../../../../docs/reference/module-iam-support.md)与英文镜像 | `forgejo` 一行与双接入段落回到 OIDC-only | 已完成（2026-09-20） |
| `forgejo` 的 README 与技术文档（中英文） | M6 的系统 webhook 与管理凭据归属落地时改写 | 未开始 |
| 本计划 §4（M2） | 已加与 [Incus compute Provider 实施计划](../../../../dev-docs/plans/incus-module.md) 的拆分说明，但要点列表与「当前完成/剩余」仍按 Provider 实现叙述 | 部分完成 |

## 11. 当前阻塞

- 当前没有可用的独立 Incus/KVM 宿主、restricted project 或测试 credential，M2/M3 无法完成真实验收；
- compute Contract 尚未接入 Core 的通用 Provider 注册/选择路径；当前 controller 使用同形 Go 接口和
  首个 Incus adapter；
- Runner VM 镜像尚未在 amd64/arm64 实际构建并产出获批 fingerprint；
- 真实 Docker daemon 未运行，Forgejo 应用镜像启动与浏览器 E2E 仍待执行。

## 12. 明确排除

以下项目不属于剩余工作：

- Forgejo LDAP 用户/Group 预配（恢复的唯一入口是 §7.3 M8 的升级复核）；
- Forgejo SAML；
- `anasIdentityAnchor` claim/reconciler；
- LDAP 预建用户与 OIDC 自动合并；
- global runner、宿主 Docker socket 和 ANAS 宿主 privileged DinD；
- 独立 `runner.enabled`、`forgejo_runner.enabled` 或要求管理员二次启用 Runner Module。
