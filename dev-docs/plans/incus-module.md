---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-09-01
---

# Incus compute Provider 实施计划

验收依据是[Incus compute Provider Module 集成要求](../requirements/incus-module.md)的需求矩阵；
设计依据是 [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../docs/architecture/ai-agent-orchestration-design.md) §5.2。

本计划把原属 [Forgejo Module 实施计划](forgejo-module.md) M2 的“Incus compute contract 与
Provider”拆出来独立跟踪。Forgejo 计划 M2 只保留“作为消费者接入”的部分。

**M0—M5 已完成：Contract 已按围栏语义改形，Provider Module、Core 支持、共享客户端库与 Forgejo
迁移均已落地。剩余工作是真实宿主验收（阻塞）与 M8 的构建校验、长驻档预留。**

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Contract 改形为租约语义 | R-010 | 已完成 |
| M1：Provider Module 骨架、凭据与部署边界 | R-001—R-005 | 已完成 |
| M2：Provider operation、隔离与可观测性 | R-006、R-009、R-011—R-016 | 已完成 |
| M3：Core 的 compute Resource 支持 | R-017—R-020 | 已完成 |
| M4：共享 Incus 客户端库 | R-021—R-025、R-037 | 已完成 |
| M5：Forgejo 迁移为 Contract 消费者 | R-026—R-028 | 已完成 |
| M6：真实 Incus 宿主验收 | R-007、R-008、R-029—R-034 | 阻塞；等待独立 KVM/Incus 宿主 |
| M7：非特权系统容器 interface | R-035—R-036 | 已完成；单元层，e2e 归 M6 |
| M8：共享构建校验与长驻实例档预留 | R-038—R-042、R-046、R-084 | 未开始 |
| M9：网络 IPv6 姿态与 image_policy 预留 | R-043—R-045、R-052 | 已完成 |
| M10：宿主 Incus 供给（安装、发行版矩阵、控制台边界） | R-047—R-051、R-057、R-094 | 未开始；设计已定 |
| M14：其余发行版适配 | — | 未开始；待适配清单见[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §2 |
| M10bis：宿主特权动作通道 | R-058—R-061、R-068、R-069、R-073、R-074、R-089—R-091 | 未开始；设计已定 |
| M10ter：统一动作 ABI（job、事件重放、取消、数据流） | R-075—R-082、R-093 | 未开始；设计已定 |
| M13：批量数据路径边界（只保留「动作自己打开目的地」） | R-083 | 未开始；下载端点为明确的不做，见[统一动作 ABI](../../docs/architecture/action-abi.md) §13 |
| M11：入站与 Traefik 发布 | R-053、R-054、R-062—R-065、R-070、R-071、R-086—R-088、R-092、R-095 | 未开始 |
| M12：`guest_image` 契约与 distrobuilder 烘焙 | R-055、R-066、R-067、R-072、R-085 | 未开始 |

覆盖统计：94 项需求全部有且只有一个里程碑归属（R-056 已废弃：镜像改为命名引用，不再需要结果通道）。

M10—M12 来自「默认可用，高级可替换」这条产品原则（[Core 实现标准](../../docs/architecture/core-implementation-standard.md) §4）：
现状要求运维先手工装好 Incus 并烘焙镜像，按该原则的判据这等于服务在默认情况下装不上。设计见
[Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)。

M8 的两半互不相关，只是都属于「已登记但本轮不做」：R-038 是一条 CI 校验（`shared_paths` 的路径
存在、且 Dockerfile 确实 COPY 了它们），R-039—R-042 是长驻实例档的预留边界，规定第三阶段不得
靠放宽现有约束来实现。

## 2. M0：Contract 改形（已完成）

七个运行时操作换成 `ensure` / `inspect` / `revoke`。理由记录在
[compute Contract 技术文档](../../contracts/compute/docs/technical.md)：Provider operation 的唯一
runtime 是 `compose_run`，只在 apply 时跑一次，且 ABI 只传 `ANAS_RESOURCE_*` 环境变量，没有 stdin
流——`exec_stdin` 承诺的 Secret 通道在这条 ABI 上无法实现。

- [x] `contract.yml`：interfaces 改为 `incus_vm` + `incus_container`，identity 改为
      `consumer` + `resource_id`；
- [x] schema 重写为租约：`resource.yml`、`ensure-request.yml`、`sandbox-result.yml`、
      `inspect-request.yml`、`inspect-result.yml`、`revoke-request.yml`、`revoke-result.yml`；
- [x] 删除 `create`/`exec`/`instance`/`list`/`delete` 七组实例 schema；
- [x] 中英 README 与技术文档改写，去掉固定 `anas-forgejo-runners` 的单消费者假设。

## 3. M1：Provider Module 骨架（已完成）

- [x] `modules/incus`：manifest（`category: compute`、两个 interface 的
      `contracts.provides`）、`providers/compute/incus_vm.yml` 与 `incus_container.yml`、Hook 与
      双语文档；
- [x] 敏感配置 `endpoint`、`server_certificate_b64`、`admin_certificate_b64`、`admin_key_b64`，
      Hook 在任一项缺失或不是对应类型的 PEM 时于 apply 早期拒绝；
- [x] 部署边界：只有一个 run-only Compose 服务，无端口、无 Traefik label、无卷、无宿主 socket；
- [x] 在 `.github/images.json` 与 `.github/modules.json` 登记 `anas-incus-provisioner`。

## 4. M2：Provider operation 与隔离（已完成）

- [x] `ensure` 幂等：project 存在则合并配置后 `PUT`，不存在则 `POST`；不覆盖无关的 `user.*` 键；
- [x] 写入后读回并断言 `restricted=true` 与四项 limits 非空，不满足即 fail closed 且不登记证书；
- [x] 证书登记拒绝两类越权：已被无限制信任的证书、已绑定其他 project 的证书；
- [x] 配额映射：Contract 的每实例上限乘以 `max_instances` 写成 project 总量；
- [x] project 封禁 device/raw config/挂载/低层配置；`incus_container` 额外写入
      `restricted.containers.privilege=unprivileged`；
- [x] 证书固定用精确 DER 比对，失配无继续分支；
- [x] `inspect` 只读且分开报告四个标志；`revoke` 撤销证书、保留 project、幂等；
- [x] 错误可归因到具体参数且不回显凭据，配套回显测试。

## 5. M3：Core 的 compute Resource 支持（已完成）

- [x] `internal/runner/compute.go`：租约校验、客户端证书生成与拆分、server fingerprint 计算；
- [x] `materializeResourceSecrets` 为 compute 生成稳定 keypair（重复 apply 不重签），并拒绝两个
      消费者共用同一 sandbox；
- [x] `ensureResourcesFor` 只把证书半边交给 Provider，私钥不经过 Provider；
- [x] `publishModuleResources` 投影 `ANAS_COMPUTE_RESOURCE__*`，私钥标为敏感并按消费者作用域隔离；
- [x] `saveResourceReady` 记录租约事实，只存 Secret 引用；
- [x] `internal/runner/compute_test.go` 覆盖凭据稳定性、消费者隔离、sandbox 冲突与租约校验。

## 6. M4：共享 Incus 客户端库（未开始）

实例生命周期的唯一实现，供 Forgejo Actions 与后续 `ai_agent` import。

- [ ] 确定包位置与构建方式。当前 `modules/forgejo/actions-controller` 的镜像用
      `GO111MODULE=off` 且只 `COPY *.go ./`，构建上下文是模块子目录，够不到仓库共享包；需要先决定
      是改构建上下文、还是把共享包放进各消费者的构建上下文；
- [ ] 从 `modules/forgejo/actions-controller/{compute,incus}.go` 提取 create/start/exec/stop/
      delete/list/janitor；
- [ ] 消费租约环境变量：endpoint、sandbox、前缀、fingerprint、证书、配额、镜像 allowlist；
- [ ] 校验镜像 fingerprint 在 allowlist 内、实例名在本前缀内、资源上限在配额内；拒绝 device、
      raw config、挂载与 profile 覆盖；
- [ ] Secret 只经 stdin；guest 入口命令按消费者 allowlist 校验；
- [ ] 超时与取消，取消后不遗留 running 实例。

验收：要求文档 §7 第 1—5 条。

## 7. M5：Forgejo 迁移（未开始）

- [ ] `actions-controller` 改为读取 `ANAS_COMPUTE_RESOURCE__FORGEJO__*`，删除自有 Incus 客户端；
- [ ] Forgejo manifest 声明 `contracts.compute` 与 `resources.requires`，删除
      `actions_incus_*` 四个配置项；
- [ ] 行为等价性用既有 controller 测试守住：ephemeral 注册、stdin token、单作业销毁、janitor；
- [ ] 现有部署不迁移数据、不重建 project：`anas-forgejo-runners` 继续作为 sandbox 值。

验收：要求文档 §7 第 6—7 条。

## 8. M6：真实宿主验收（阻塞）

在独立 KVM/Incus 宿主执行，记录固定 Incus 版本、镜像 fingerprint 与 project 配置。

## 9. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | 待记录（本地全绿） |
| `go run ./cmd/gen-module-docs --check` | 待记录（本地全绿） |
| `go run ./cmd/gen-contract-docs --check` | 待记录（本地全绿） |
| `npm run docs:check-requirements` | 待记录 |
| 渲染产物 `docker compose config --quiet` | 待记录 |

## 10. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-007 | 待新增 `test-env/scripts/server-incus-isolation-e2e.sh` | 双消费者、独立 project | — | 待执行 |
| R-011 | 待新增 `test-env/scripts/server-incus-fence-e2e.sh` | project 封禁 device 与挂载 | — | 待执行 |
| R-025 | 待新增 `test-env/scripts/server-incus-lifecycle-e2e.sh` | 作业中途取消 | — | 待执行 |
| R-031 | 待新增 `test-env/scripts/server-incus-isolation-e2e.sh` | 双消费者并行 | — | 待执行 |
| R-008 | 待新增 `test-env/scripts/server-incus-fence-e2e.sh` | 受限证书越界尝试 | — | 待执行 |
| R-029 | 待新增 `test-env/scripts/server-incus-cert-rotation-e2e.sh` | 运行中实例 + 证书轮换 | — | 待执行 |
| R-030 | 待新增 `test-env/scripts/server-incus-lifecycle-e2e.sh` | 单消费者全流程 | — | 待执行 |
| R-032 | 待新增 `test-env/scripts/server-incus-quota-e2e.sh` | 超配额创建 | — | 待执行 |
| R-033 | 待新增 `test-env/scripts/server-incus-janitor-e2e.sh` | 强制中断消费者 | — | 待执行 |
| R-034 | 待新增 `test-env/scripts/server-incus-baseline.sh` | 目标 NAS 规格、两档 | — | 待执行 |
| R-027 | 待新增 `test-env/scripts/server-forgejo-one-job-e2e.sh` | Forgejo + Incus 宿主 | — | 待执行 |
| R-041 | 待新增 `test-env/scripts/server-incus-longlived-e2e.sh` | 长驻档持久卷与入站（第三阶段） | — | 待执行 |
| R-042 | 待新增 `test-env/scripts/server-incus-longlived-e2e.sh` | 长驻档下既有边界不被削弱 | — | 待执行 |
| R-044 | 待新增 `test-env/scripts/server-incus-network-e2e.sh` | 双栈宿主上的租约网络出网 | — | 待执行 |
| R-047 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | 干净宿主上的一次性安装 | — | 待执行 |
| R-048 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | 重复执行与卸载、装不上时的降级 | — | 待执行 |
| R-050 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | 校验 daemon 只监听回环 | — | 待执行 |
| R-053 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | v4/v6 入站经 proxy device | — | 待执行 |
| R-054 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | Traefik 发布实例内 HTTP 服务 | — | 待执行 |
| R-055 | 待新增 `test-env/scripts/server-incus-image-bake-e2e.sh` | 首次烘焙与二次 apply 不重建 | — | 待执行 |
| R-057 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | Debian 13 / Ubuntu 24.04 / 26.04 三档 | — | 待执行 |
| R-094 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | 未适配发行版上保持关闭而非失败 | — | 待执行 |
| R-060 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | 安装与卸载对称性 | — | 待执行 |
| R-061 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | CLI 与 Web 同一通道同一审计 | — | 待执行 |
| R-063 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 实例启停与 Traefik 路由增删同步 | — | 待执行 |
| R-087 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 消费者无法注册本租约命名空间之外的域名 | — | 待执行 |
| R-088 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 绕过客户端库直写请求文件仍被拒绝 | — | 待执行 |
| R-095 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 默认发布的服务未认证时不可访问 | — | 待执行 |
| R-091 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | token 过期后重新展示而非沿用旧摘要 | — | 待执行 |
| R-072 | 待新增 `test-env/scripts/server-incus-image-bake-e2e.sh` | prune 保留上一个 deployment 的镜像 | — | 待执行 |
| R-075 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | 断连后任务继续、重连可见 | — | 待执行 |
| R-080 | 待新增 `test-env/scripts/server-backup-send-e2e.sh` | 取消后不完整目的文件被清理 | — | 待执行 |
| R-082 | 待新增 `test-env/scripts/server-action-job-e2e.sh` | CLI 发起的 job 在控制台可见 | — | 待执行 |

## 11. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `contracts/compute` README 与技术文档（中英文） | 改为租约语义，去掉单消费者假设 | 已完成 |
| `modules/incus` README 与技术文档（中英文） | 新建 | 已完成 |
| [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 | Incus 控制面归属改为 `incus` Module + 共享客户端 | 未开始 |
| [Forgejo Module 实施计划](forgejo-module.md) M2 | 只保留“作为消费者接入”，Provider 工作引用本计划 | 未开始 |
| [Module 专属命令能力](module-command-capability.md) M4 | `incus-doctor` / `incus-runner-reconcile` 的归属确认 | 未开始 |

## 12. 当前阻塞

- 没有可用的独立 Incus/KVM 宿主，M6 全部无法执行；M4、M5 不受影响。
- M4 的第一步是构建上下文决策，不是代码提取：消费者镜像目前以模块子目录为构建上下文并关闭
  Go module 模式，够不到仓库级共享包。这个决定会影响 `.github/images.json` 的 `context` 与构建
  缓存，必须先定再写代码。
