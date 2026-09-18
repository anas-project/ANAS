---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-09-10
---

# Incus compute Provider 实施计划

验收依据是[Incus compute Provider Module 集成要求](../requirements/incus-module.md)的需求矩阵；
设计依据是 [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../modules/ai_agent/docs/architecture/orchestration-design.md) §5.2。

本计划把原属 [Forgejo Module 实施计划](../../modules/forgejo/dev-docs/plans/forgejo-module.md) M2 的“Incus compute contract 与
Provider”拆出来独立跟踪。Forgejo 计划 M2 只保留“作为消费者接入”的部分。

**已落地和剩余范围以 §1 里程碑表为准。真实宿主验收、共享构建校验以及宿主供给、动作通道、入站和镜像烘焙分别跟踪，不以单元层完成代替端到端验收。**

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Contract 改形为租约语义 | R-010 | 已完成 |
| M1：Provider Module 骨架、凭据与部署边界 | R-001—R-005 | 已完成 |
| M2：Provider operation、隔离与可观测性 | R-006、R-009、R-012—R-016 | 已完成 |
| M2a：围栏真实验收 | R-011 | 实施中；实现已落地，bridge/project 修复与回归已落地，真实围栏验收待执行 |
| M3：Core 的 compute Resource 凭据与投影 | R-017、R-018、R-020 | 已完成 |
| M4：共享 Incus 客户端库 | R-021—R-024、R-037 | 已完成 |
| M4a：取消后清理验收 | R-025 | 实施中；单元实现已落地，真实验收待执行 |
| M5：Forgejo 迁移为 Contract 消费者 | R-026、R-028 | 已完成 |
| M5a：one-job 行为等价验收 | R-027 | 实施中；迁移已落地，真实验收待执行 |
| M6：真实 Incus 宿主验收 | R-007、R-008、R-029—R-034 | 阻塞；等待独立 KVM/Incus 宿主 |
| M7：非特权系统容器 interface | R-035—R-036 | 已完成；单元层，e2e 归 M6 |
| M8a：共享构建校验 | R-038 | 已完成；校验与 CI 已接入 |
| M8b：staging 共享构建路径 | R-084 | 实施中；覆盖变量已存在，实际构建与双语说明待补 |
| M8：长驻实例档预留 | R-039—R-042、R-046 | 未开始 |
| M9：网络 IPv6 姿态与 image_policy 预留 | R-043、R-045、R-052 | 已完成 |
| M9a：双栈出网验收 | R-044 | 实施中；实现已落地，真实验收待执行 |
| M10：宿主 Incus 供给（安装、发行版矩阵、控制台边界） | R-047—R-051、R-057、R-094 | 未开始；回环连通路径与安装状态迁移待定 |
| M11：入站与 Traefik 发布 | R-053、R-054、R-062—R-065、R-070、R-071、R-086—R-088、R-092、R-095、R-096 | 未开始 |
| M12：`guest_image` 契约与 distrobuilder 烘焙 | R-019、R-055、R-066、R-067、R-072、R-085 | 未开始；R-019 已有裸摘要校验，命名引用解析待补 |
| M13：批量数据路径边界（只保留「动作自己打开目的地」） | R-083 | 未开始；下载端点为明确的不做，见[统一动作 ABI](../../docs/architecture/action-abi.md) §13 |
| M14：其余发行版适配 | — | 未开始；待适配清单见[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §2 |

覆盖统计：75 项需求全部有且只有一个里程碑归属（另有 25 项已废弃）。

废弃分三批，都是**归属地修正**而非取消：R-056 因镜像改为命名引用而不再需要结果通道；
R-097/R-098 迁往[凭据轮换](credential-rotation.md)；R-058 等 22 项迁往
[宿主特权动作通道](host-action-channel.md)与[统一动作 ABI](action-abi.md)——它们都是被 Incus 撞
出来的 Core 要求，没有一条是 Incus 自身的。

M10—M12 来自「默认可用，高级可替换」这条产品原则（[Core 实现标准](../../docs/architecture/core-implementation-standard.md) §4）：
现状要求运维先手工装好 Incus 并烘焙镜像，按该原则的判据这等于服务在默认情况下装不上。设计见
[Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)。

M8a 的共享构建门禁已由 `go run ./cmd/check-shared-build` 落地：校验路径存在、Dockerfile
共享 COPY、Compose 命名上下文和 Module revision 触发路径。现有实现已配置 Incus 的 Compose
`additional_contexts.shared`，以及共享库消费者的 `internal/computeclient` revision 触发路径。
M8 的长驻实例档仍未开始，与这条 CI 校验分别验收。

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

## 4. M2/M2a：Provider operation 与隔离（实现已落地，围栏真实验收待办）

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

## 6. M4：共享 Incus 客户端库（实现已落地，真实验收待办）

- [x] 共享包位于 `internal/computeclient`，消费者 Dockerfile 从命名 `shared` context 复制源码，
      用 Go module 模式与 `GOPROXY=off` 编译，不从网络拉取共享包；
- [x] create/start/exec/stop/delete/list/janitor 由共享库实现；Forgejo 保留业务适配层；
- [x] 读取消费者租约，校验镜像摘要、实例前缀、配额和 guest 入口 allowlist；
- [x] 实例请求不开放 device、raw config、挂载或 profile 覆盖；Secret 经 stdin；
- [ ] 在真实宿主验证取消、超时与清理失败场景，证据登记到 §10。

验收：要求文档 §7 第 1—5 条。已有单元测试不等于真实宿主取消验收。

## 7. M5：Forgejo 迁移（实现已落地，真实验收待办）

- [x] controller 使用 `internal/computeclient` 并读取消费者私有租约环境变量；
- [x] manifest 通过 `dependencies.contracts` 与 `resources.requires` 接入 compute，默认
      `incus_container`，VM 由消费者显式选择；
- [x] 保留既有 sandbox 名 `anas-forgejo-runners`，不要求迁移数据或重建 project；
- [ ] 真实 one-job 验证 ephemeral 注册、stdin token、作业后销毁及 crash 后 janitor 回收。

验收：要求文档 §7 第 6—7 条。Forgejo 自身的应用与身份验收仍归其私有计划。

## 8. M6：真实宿主验收（阻塞）

需要独立 Incus 宿主；容器档可先在无 KVM 的 Linux 宿主验证，VM 档另需 KVM。没有 KVM 不应
阻止容器档用例编写与执行，但不能以容器结果替代 VM 结果。

- [ ] 固定 daemon 版本、架构、镜像 fingerprint 与 project/profile/network 配置；
- [ ] 编写 §10 中待新增脚本，记录前置条件、反例、退出码和失败清理；
- [ ] 先修复并验证 managed bridge/project 的兼容性（架构 §7.3），再跑单租约生命周期、同前缀双租约隔离、配额、证书轮换、取消与 crash 回收；
- [ ] 记录两档启动与典型作业耗时；每份证据关联测试提交、环境和日期。

退出条件：§10 对应真实宿主用例全部有可复现证据，再评估 Module 的 release 准入。

### 8.1 M7：系统容器档

实现已落地；保持非特权 project 约束和默认容器档。真实隔离、配额与网络行为与 VM 分档记录，
共享 M6 的测试设施，不额外宣称已完成宿主验收。

### 8.2 M8a/M8b：共享构建与 staging 路径

- [x] `cmd/check-shared-build` 校验共享源码、Dockerfile COPY 与 revision 触发路径；
- [x] Compose 提供 `ANAS_SHARED_BUILD_CONTEXT` 覆盖入口；
- [ ] 从源码 checkout 和部署 staging 树分别验证构建上下文解析及实际构建；
- [ ] 在 Module 双语技术文档中给出覆盖值应指向仓库源码根的示例，注明源码不存在时不能本地构建。

R-084 从 M8 移到此处；退出条件是覆盖路径可用且文档同步，不能只凭变量存在判定完成。

### 8.3 M8：长驻实例与 image_policy 扩展预留

本轮不实现长驻实例、任意命令、持久卷或 `image_policy: any`。保持当前拒绝边界；进入该阶段前，
先补生命周期、持久卷归属、删除/恢复和独立命令开关设计，再安排 R-041/R-042 的真实验收。
R-046 只在未来启用 `any` 时验收，不把它作为本轮阻塞。

### 8.4 M9：IPv6 与默认隔离档

现有实现已提供 IPv6 姿态、bridge NAT、默认容器档及拒绝 `any` 的行为。
剩余为双栈宿主实际出网验证：IPv6 开关关闭、无全局地址、有全局地址三种情况；结果归 §10。

### 8.5 M10：宿主供给

前置设计见宿主供给架构 §3.5、§7。依赖宿主特权动作通道；其未落地前只能提供明确标为待实现的
终端操作方案与控制台状态读取设计，不增设收集 sudo 密码的入口。

- [ ] 核验首批三个发行版的官方包、服务单元、版本与架构，形成声明式安装表；
- [ ] 验证架构 §3.6 的固定目的地非 root 传输候选，再定案回环连接路径和宿主动作清单；
- [ ] 实现探测、安装、配置、登记、读回验证和状态记录，区分已存在的外部 daemon 与 ANAS 所有资源；
- [ ] 定义跳过、部分失败、重试和卸载的状态迁移，保证可选消费者关闭而主部署可继续；
- [ ] 卸载先清点受管资源与运行实例，不删除非 ANAS 资源；破坏性动作遵循宿主通道要求；
- [ ] 在三个干净发行版及未适配发行版执行安装、重复执行、失败恢复和卸载验收。

退出条件：默认路径无需用户手工准备 daemon/证书，回环限制与失败降级均有真实证据。

### 8.6 M11：入站与 Traefik 发布

不依赖长驻实例落地；依赖已定案的网络连接路径与消费者之外的受信执行方。

- [ ] 先交付独立 `lease_secret`：生成/复用、敏感投影、引用持久化、旧部署升级与备份恢复测试；
- [ ] 补 ingress schema 与 Core 校验，冻结 allowed_ports、auth 和 domain 配置；
- [ ] 实现 fixed/named/random 派生与跨租约命名空间冲突检查；
- [ ] 先执行架构 §5.1.5 的证书 × 实例/profile proxy 禁令反例矩阵，再定案两档拓扑；管理证书不能被假定为 project 禁令豁免；
- [ ] 受信中介验证租约、实例、端口与目标，复用既有 Traefik 路由渲染，消费者仅写请求目录；
- [ ] 实现发布、撤销、暂停/停止清理和中介重启后的对账；先建可用后端再发布路由，撤销先移除路由；
- [ ] 验证绕过客户端直接写请求、跨租约冒用、auth 覆盖、域名碰撞及两档 IPv4/IPv6 入站。

退出条件：§10 对应入站验收通过；LAN 仍不实施。

### 8.7 M12：guest_image 与镜像烘焙

- [ ] 定义 guest_image 契约与配方/revision 的所有权，采用 distrobuilder；
- [ ] 评估架构 §6.2.1 的发布时烘焙、apply 前冻结目录候选，明确首次烘焙阶段与产物分发；不新增 Provider 结果通道；
- [ ] 兼容读取旧配置中的裸 64 位摘要并归一为 `fingerprint:`；新文档使用带前缀形式，运行时仍只传摘要；
- [ ] 已记录摘要但本地镜像缺失时先尝试恢复相同产物；无法恢复即失败，不以同名重新烘焙的新字节替代；
- [ ] 首次构建记录摘要，再次 apply 不重建；同 revision 对应不同摘要时失败；
- [ ] 对照目标 daemon 版本验证镜像服务器限制、镜像隔离与导入权限，明确逐摘要约束的强制范围；
- [ ] 实现显式 prune 的 dry-run 与确认，保留当前、上一个 deployment 及运行实例所需镜像。

退出条件：首次构建、重复 apply、缺失恢复、revision 冲突与回滚/prune 均有证据。

### 8.8 M13：批量数据路径

依赖统一动作 ABI 的控制流边界。镜像导入导出由动作打开受支持目的地；审查不得增加浏览器制品
下载端点或内联 base64 数据。补充大块数据、取消、失败清理与错误脱敏测试后验收。

### 8.9 M14：后续发行版

仅在首批发行版通过后排期。逐个核验上游来源、架构与服务差异，补声明表和真实安装测试；涉及
第三方源按既有要求取得同意。未适配时继续保持自动安装关闭，不把候选清单当作已支持列表。

## 9. CI 门禁

以下本地验证于 2026-09-10 在 `f7642c5` 加本轮未提交工作树上通过；不代表已提交版本的 CI 结果。

| 门禁 | 最近验证结果 |
| --- | --- |
| `go test ./...` | 本地通过 |
| `go run ./cmd/check-shared-build` | 本地通过；不代表实际镜像构建通过 |
| `go run ./cmd/gen-module-docs --check` | 本地通过 |
| `go run ./cmd/gen-contract-docs --check` | 本地通过 |
| `npm run docs:check-requirements` | 本地通过 |
| `npm run docs:check-requirement-status` / `docs:check-plan-status` / `docs:check-status` | 本地通过 |
| `npm run docs:build` | 本地通过；存在非阻断的 chunk 大小警告 |
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
| R-063 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 实例启停与 Traefik 路由增删同步 | — | 待执行 |
| R-087 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 消费者无法注册本租约命名空间之外的域名 | — | 待执行 |
| R-088 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 绕过客户端库直写请求文件仍被拒绝 | — | 待执行 |
| R-095 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 运行时无法覆盖租约声明的认证方式 | — | 待执行 |
| R-072 | 待新增 `test-env/scripts/server-incus-image-bake-e2e.sh` | prune 保留上一个 deployment 的镜像 | — | 待执行 |

## 11. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| `contracts/compute` README 与技术文档（中英文） | 改为租约语义，去掉单消费者假设 | 已完成 |
| `modules/incus` README 与技术文档（中英文） | 新建 | 已完成 |
| [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 | Incus 控制面归属改为 `incus` Module + 共享客户端 | 未开始 |
| [Forgejo Module 实施计划](../../modules/forgejo/dev-docs/plans/forgejo-module.md) M2 | 只保留“作为消费者接入”，Provider 工作引用本计划 | 未开始 |
| [Module 专属命令能力](module-command-capability.md) M4 | `incus-doctor` / `incus-runner-reconcile` 的归属确认 | 未开始 |

## 12. 当前阻塞与执行顺序

1. 先完成架构 §7 的回环连通、proxy 权限/VM 拓扑和命名镜像解析决策；M10 不再标“设计已定”。
2. 可独立推进 M8a staging 验证、M11 租约 secret 与声明校验、M6 测试脚本；不得因此宣称完整入站可用。
3. 宿主动作通道与统一动作 ABI 的实现仍分别归各自计划，Incus 只接入，不在本计划重复分配其需求。
4. v7.3.0 bridge/project 兼容性缺陷已修复为 default project 独立 bridge 与精确网络授权，历史证据与反例见[核验记录](../reviews/2026-09-10-incus-network-proxy-validation.md)。独立宿主尚无可用验收证据；容器与 VM 分别跟踪，M2/M4/M5/M9 的真实验收均登记到 §10。
5. M8 长驻档和 M14 后续发行版保持预留。完成文档整理不表示这些阶段已实施。

2026-09-10 文档整理仅核对代码与契约、修正进度表达；本轮没有执行真实 Incus、Docker 镜像构建或
one-job 验收。旧清单中的共享构建决策和客户端提取阻塞已经移除。

## 13. 2026-09-10 bridge 修复记录

- [x] 网络 API 显式指向 default project；租约关闭 network feature 并精确授权单个 bridge。
- [x] 网络归属/type/外部接口守卫；保留已分配子网，IPv6 关闭时移除旧 NAT；写后读回。
- [x] 既有 network-isolated project 拒绝隐式迁移；`inspect.ready` 包含精确网络作用域。
- [x] TLS fake 拒绝非 default bridge 请求，profile 按 project 分区；回归覆盖双租约、冲突拒绝、
      子网稳定性及 project/network 写入未生效时不登记证书。
- [ ] 实机接网与网络写权限、跨租约流量隔离、两档生命周期仍待 M6 验收。

验证结果见 §9。最初沙箱测试因禁止本地监听失败；获准放开测试进程的沙箱限制后，
Incus/共享客户端专项测试和全仓 Go 测试均通过。TLS fake 测试不能替代真实宿主验收。
