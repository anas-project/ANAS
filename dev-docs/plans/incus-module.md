---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-09-18
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
| M8b：staging 共享构建路径 | R-084 | 实施中；覆盖变量与双语路径示例已具备，源码/staging 实际构建待验收 |
| M8：长驻实例档预留 | R-039—R-042、R-046 | 未开始 |
| M9：网络 IPv6 姿态与 image_policy 预留 | R-043、R-045、R-052 | 已完成 |
| M9a：双栈出网验收 | R-044 | 实施中；实现已落地，真实验收待执行 |
| M10：宿主 Incus 供给（安装、发行版矩阵、控制台边界） | R-047—R-051、R-057、R-094 | 实施中；非 root 固定控制转发已编码，安装动作、拓扑发布与真实验收未完成 |
| M11：入站与 Traefik 发布 | R-053、R-054、R-062—R-065、R-070、R-071、R-086—R-088、R-095、R-096 | 实施中；独立租约 secret 与网络实验产物生成器已落地，真实宿主验证及生产发布链路待办 |
| M11a：独立租约命名密钥 | R-092 | 已完成；生成/复用、敏感投影、冻结引用与文件备份恢复回归通过 |
| M12：`guest_image` 契约与 distrobuilder 烘焙 | R-019、R-055、R-066、R-067、R-072、R-085 | 实施中；Core/消费者结构化接入与 deployment 冻结已落地，目录产物、导入与烘焙待补 |
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
- [x] 在 Module 双语技术文档中给出覆盖值应指向匹配版本的完整仓库源码根的绝对路径示例，注明源码不存在时不能本地构建（2026-09-18）；命令模板未作为实构验收运行。

R-084 从 M8 移到此处；退出条件是覆盖路径可用且文档同步，不能只凭变量存在判定完成。

2026-09-18 增加 `check-shared-build --source-root /... --staging-root /...` 静态预检与
`staging_test.go`：强制显式绝对 shared context，对实际存在的共享构建 Module 核对 Compose、
Dockerfile、构建目录及 shared_paths 的文件集合、字节和执行位；没有匹配模块、源码漂移、特殊文件
和被枚举到的符号链接都拒绝。它不验证完整 deployment manifest、不加载部署环境，也不调用 Docker。
测试源码已编写但未运行；静态预检不代替源码/staging 两条路径的实际构建，M8b 不改为已完成。

### 8.3 M8：长驻实例与 image_policy 扩展预留

本轮不实现长驻实例、任意命令、持久卷或 `image_policy: any`。保持当前拒绝边界；进入该阶段前，
先补生命周期、持久卷归属、删除/恢复和独立命令开关设计，再安排 R-041/R-042 的真实验收。
R-046 只在未来启用 `any` 时验收，不把它作为本轮阻塞。

### 8.4 M9：IPv6 与默认隔离档

现有实现已提供 IPv6 姿态、bridge NAT、默认容器档及拒绝 `any` 的行为。
剩余为双栈宿主实际出网验证：IPv6 开关关闭、无全局地址、有全局地址三种情况；结果归 §10。

### 8.5 M10：宿主供给

前置设计见宿主供给架构 §3.5—§3.8、§7。安装与配置依赖宿主特权动作通道；其未落地前不自动
安装或启动宿主服务，不增设收集 sudo 密码的入口。

2026-09-18 已新增 `modules/incus/control-relay` 非 root 传输组件：固定连接 `127.0.0.1:8443`，
只绑定安装配置指定的控制接口 IPv4/高位端口，不加载 TLS 私钥或任意 upstream。代码包含 root 所有的
配置逐级无符号链接读取、专用 UID/GID/capabilities 检查、源网段过滤、连接容量/空闲期限、双向半关闭、
停止清理及运行中接口漂移检查；配置与传输测试源码已补，均未运行。它不是第三个特权入口，也未接入
`configure/uninstall`、官方发布产物、服务单元、INPUT/FORWARD、endpoint 投影或生产启动。重启后
接口 index 变化须由宿主协调重新验证网络归属并生成配置，不允许静默接管同名新接口。

- [ ] 运行固定控制转发的配置/传输/身份测试，以及独立 Linux 宿主的 mTLS/pin/project、来源网络隔离、重启与卸载验收；

- [ ] 核验首批三个发行版的官方包、服务单元、版本与架构，形成声明式安装表；
- [ ] 按架构 §3.7 细化的独立控制 bridge、固定目的地非 root 转发和双栈防火墙实现原型，完成真实连通验证与宿主动作清单；
- [ ] 实现探测、安装、配置、登记、读回验证和状态记录，区分已存在的外部 daemon 与 ANAS 所有资源；
- [ ] 定义跳过、部分失败、重试和卸载的状态迁移，保证可选消费者关闭而主部署可继续；
- [ ] 卸载先清点受管资源与运行实例，不删除非 ANAS 资源；破坏性动作遵循宿主通道要求；
- [ ] 在三个干净发行版及未适配发行版执行安装、重复执行、失败恢复和卸载验收。

退出条件：默认路径无需用户手工准备 daemon/证书，回环限制与失败降级均有真实证据。

### 8.6 M11：入站与 Traefik 发布

不依赖长驻实例落地；依赖已定案的网络连接路径与消费者之外的受信执行方。

已新增独立 `cmd/incus-network-prototype` 与脱敏观测夹具：提供只读实验观测采集与 HTTP 产物生成，包含限时 nft tuple、
来源 veth 过滤、发布/替换/撤销计划及 Traefik 字段；不执行网络变更，不开放生产 ingress。
静态产物测试不能代替 Docker/Incus 的规则顺序、IP 复用与长连接撤销实测。

- [x] 先交付独立 `lease_secret`：生成/复用、敏感投影、引用持久化、旧部署升级与备份恢复测试；
- [ ] 补 ingress schema 与 Core 校验，冻结 allowed_ports、auth 和 domain 配置（代码已接入，测试暂缓）；
- [ ] 实现 fixed/named/random 派生与跨租约命名空间冲突检查（128 位 HMAC 与内存预占代码已接入，待回归）；
- [ ] 按架构 §5.1.7 实现 Traefik 到受管 guest 的精确路由与默认拒绝规则，验证两档流量、IP 复用和已建立连接撤销；保持 proxy 禁令，不引入 network forward；
- [ ] 受信中介验证租约、实例、端口与目标，复用既有 Traefik 路由渲染，消费者仅写请求目录；
- [ ] 实现发布、撤销、暂停/停止清理和中介重启后的对账；先建可用后端再发布路由，撤销先移除路由；
- [ ] 验证绕过客户端直接写请求、跨租约冒用、auth 覆盖、域名碰撞及两档 IPv4/IPv6 入站。

2026-09-12 授权续作：新增 `internal/computeingress`，Core 在 calculate 后冻结 `compute_ingress`，
启动/激活仍明确拦截 ingress。新增请求 schema、Linux `openat` 限制、fixed/named/random 域名
和跨租约/已知服务冲突检查；内存预占带会话序号，重复请求幂等，旧回收回调不能清除新预占。
实验命令 `--mediation` 将管理员给定授权、活动部署断言与单租约请求目录连接到现有生成器，
支持冻结 ForwardAuth 字段及身份绑定的撤销计划。该入口仅生成 `.example.test` 单路由计划。

续作已补 `deployment.Reader.HTTPAuthorizations`、独立请求目录登记、Core 单条命名密钥读取
及 workspace 中介输入，按实际 workspace/激活/清单摘要核对代次。实验命令只支持 example.test；
登记正例留给独立元数据夹具，不能修改实际 active 状态绕过入口拦截。上述分支仍未执行。

续作新增可持久化执行顺序内核：当前 epoch 与新鲜 observer 交集之外的旧记录先撤销；发布按地址
保留、`/32`、窄 HTTP 许可、探测、独占 Traefik 文件的顺序，撤销按路由、许可、存量连接、`/32`、
地址释放的顺序。每一步完成后持久化回执，清理失败不释放地址；文件 renderer 不覆盖或删除不同
内容的文件。接口不接受 argv、规则文本、消费者 URL 或 entrypoint，生产启动拦截仍保留。

仍待：目录挂载/自动登记接入、生产窄范围密钥交付、只读受限 Incus 观测身份、宿主动作实际实现、
真实 IP 保留与探测、路由实际消费/撤销确认、孤立外部工件盘点、事件来源和消费者 API/挂载；没有把实验管理
socket 或离线授权文件作为生产信任源。新代码按要求不跑测试或服务器操作，相关用例已追加到
`test-env/fixtures/incus-network-prototype/e2e-plan.md`；本地编译/验证门禁一并留待测试阶段。
双语文档与索引仅执行生成。上述复选项在相关验证完成前保留未完成，M11/30-of-75 不变。

2026-09-13 继续提供 `IncusFactReader`：固定 HTTPS/mTLS 连接、安装期租约映射、版本/身份核对、
受管 bridge/NIC/唯一地址分配双采样，可注入工作区请求源；每个 GET 有大小和超时上限，禁止重定向、
代理与响应回显。执行目标新增 UUID/generation/最后启动时间派生的 incarnation，快速重启不复用旧
reservation。API 字段对照 Incus v7.3.0 源码；代码未实例化或测试，其他版本没有兼容性验收。
客户端 GET 限制不代表证书只读；专用服务端授权供给及宿主适配器仍未完成，Traefik 读取代码见下文。
新增传输、权限、双采样、重启代次用例已登记 E2E 清单，继续保持未完成状态。

退出条件：§10 对应 HTTP 入站验收通过；LAN 和 TCP/UDP 端口发布仍不实施。TCP/UDP 按架构 §5.1.8 另立需求与计划，不隐式扩展当前 HTTP 授权。

2026-09-13：执行内核补持久化 `retiring` 意图，撤销中断后的重试先完成退役，不能因请求恢复有效
重新放行；宿主步骤失败及探测前观测失效统一清理。状态 schema/epoch 校验收紧。仅编码与文档
更新，测试继续暂缓，M11 状态不变。

持续执行续作：`FileStateStore.WithExclusive` 为完整会话持有同一状态根的非阻塞 flock，检查目录和
锁文件 inode/owner/权限，不删除锁文件；执行器每个外部步骤前核对会话。新增 `Controller.Run`
串行周期循环与事件唤醒，启动先退役持久化目标，完整授权读取失败后全量清理，取消时用独立的有界
上下文清理并保持锁。退役 tombstone 阻止同一 reservation 下轮复活，无 TTL 删除；4 MiB 上限拒绝
继续膨胀，历史维护仍须单独实现。该锁仅覆盖使用同一状态目录的进程，不代表跨宿主选主或全局隔离。

`WorkspaceSource` 已连接 Core、登记目录、严格请求读取、Planner 与独立 FactReader。每租约最多
256 个目录项、每轮最多 1024 个 JSON 请求；重复实例端口拒绝。每个发布步骤重读请求、冻结授权和
路由库存，独立核对 UUID/IP/MAC；同一内容和目标保留原 token。全部清理确认后才清空 token 缓存，
再通过完整授权与观测恢复有效请求的新 token，支持停止后启动和暂时故障恢复。Incus/Traefik 查询代码
已提供；服务端受限身份、可信读取配置、生产命名密钥交付、HostActions/Probe 仍须供给，不能用空库存
或管理 socket 伪装完成。

文件 renderer 已去除独立 YAML 模板，改为构造 `ANAS_TRAEFIK_ROUTE__*` 并调用既有 entrypoint 的
`ANAS_TRAEFIK_RENDER_ONLY=true` 分支，在私有临时目录渲染。正常启动路径仍生成证书并启动
Traefik。当前新代码均未编译/测试，也未调用 Controller、渲染器或实际宿主动作；详细待测项见 E2E 清单。

同日继续加入 `TraefikReader`，实现 `RouteInventory` 与 `RouteConfirmation`。只读既有受保护 API，
固定证书与 3.7.10 版本，完整 rawdata 双快照确认且限制大小/期限。HTTPS Host 库存支持仓库当前的
Host/Path/PathPrefix 与布尔组合，未知 matcher、无界规则、多层路由或 HTTPS TCP 抢占均拒绝。
Controller 把当前持锁 Journal 传给请求源；库存候选来自回执，只有完整文件和实际 router/service/auth
匹配才排除自有路由，孤立/仿冒名称不能排除。发布前和加载后都核对可信 ForwardAuth 配置摘要；
撤销要求文件、router、service 及直接路由引用消失。API UP 只是构建状态，不是后端 HTTP 探测。
凭据与认证摘要供给、服务装配、孤立工件恢复和真实验收仍待实现；未新增 API 或自动打开 ingress。
已同步双语文档及待测清单，继续只编码/格式化/文档生成，不执行测试、门禁、服务器操作或提交。

2026-09-15 继续补私有读取配置交付与 fixture 探测。`runner.DeliverComputeHTTPReaders` 从当前活动
授权导出 random 租约需要的命名密钥，合并受信安装方提供的 Incus 观测身份、Traefik API 凭据与
精确 ForwardAuth 摘要。交付为私有目录中的新 0400 文件，限 1 MiB，不覆盖旧文件；前后重查 Core
与命名密钥来源。`OpenWorkspaceReaders` 连接事实读取、库存、加载确认及命名密钥；按完整活动快照
检查配置作用域，fixed/named 也受约束，每个 API GET 前后复核配置文件身份/内容。不挂载完整 Store，
也不把 GET-only 代码当成服务端只读授权。安装供给、UID/挂载与轮换清理接线仍待实现。

`FixtureHTTPProbe` 提供仅限 `.example.test` 的 BackendProbe 实现，绑定完整目标及预登记的独有响应
长度/SHA-256。Linux 中检查 Traefik PID、启动 tick、boot ID、netns inode 和实际 socket 的
SO_NETNS_COOKIE，绑定指定入站 IPv4；进程必须由受信启动方预先放在同一网络命名空间。只做
有界 HTTP GET，前后核对授权与独立实例事实，不执行 setns/宿主命令。复用已锁定的 x/sys v0.47.0，
仅将其改为直接依赖。真实启动身份/cookie 供给、fixture 登记、HostActions/IP 保留、生产应用探测、
孤立工件恢复及完整服务安装仍待办。新增代码未运行；待测表已同步，M6/M11 和 30/75 不变。

同日续作接入实验准备入口。`--capture-probe` 从选定实验 Docker 容器和 bridge 的一致快照取得
PID/源 IPv4；调用 `CaptureTraefikProbeIdentity` 在已位于目标 netns 的进程内锁定 OS 线程，读取
启动身份、procfs/nsfs 及新建未绑定 socket 的 cookie，再重查 Docker/内核身份。只输出观测文件，
不执行 setns，不创建新的特权入口，也不把实验管理 socket 挂给中介。

`--prepare-fixtures` 在无未撤销 publication 的持锁会话中，从实际 WorkspaceReaders 读取当前
example.test 请求与实例事实，预生成每目标独有响应，并经 `RegisterHTTPFixtures` 发布新私有登记。
文件不包含 reservation；它绑定完整 epoch/请求/实例身份，`NewRegisteredFixtureHTTPProbe` 只在
新鲜授权及独立观测通过后绑定当前 token，支持中介重启而不重放旧 token。guest 重启、IP/MAC、
域名、认证或请求变化均不能借旧登记恢复。登记失效/换文件在探测前后检查；响应尚需装入对应 guest，
准备成功不代表已启动 HTTP 服务。原有静态完整目标构造器仍可用于单会话。

该续作仅代码与双语文档生成，未运行新命令、编译、测试、门禁或服务器操作。真实宿主动作与生产
启动安装仍依赖尚未实现的统一动作 ABI/宿主通道，不另造 root RPC；孤立工件恢复和实机退出条件
继续待办，M6/M11、30/75 与 developing 状态保持不变。

同日恢复续作：新增必需 `HTTPArtifactInventory` 边界及 `Executor.Recover`。Controller 启动/失败/
退出清理只有在撤销全部已登记目标并确认外部作用域清空后才完成；对账开通/续租前后都检查盘点。
每次地址释放前也检查完整作用域，孤立工件出现时可继续关闭已知路由、许可和连接，但保留地址与
retiring 回执。缺失状态不能代替外部清空证明，损坏状态不覆盖，已退役 token 不作为清理候选。

已实现 Traefik 专属目录及 API 盘点：目录最多 4096 项、一次盘点 30 秒，目录身份、项集与完整文件
内容在 API 读取前后核对；已加载 HTTP router/service 必须匹配现存回执和完整文件。其他 provider、
协议、动态 section 或服务引用使用 `anas-compute-` 保留命名空间时不能自动获信任；仅排除已核验的
ForwardAuth `usedBy` 边。撤销确认补服务间引用检查。崩溃留下的规范临时文件只有在匹配现存回执的
完整渲染内容时才清理；部分写入、未知文件和模板变化仍拒绝自动删除。

2026-09-16 前置续作：核对确认宿主通道没有实现，现有 `consolejobs`/`jobexecutor` 可作为共用
job 的复用基础。先补 `internal/actionabi` 的有界 JSONL、job/invocation 绑定、受信序号/截断校验
和真实进程终态判定；执行器不能自报 seq/truncated，强杀或不完整结果只能 unknown。该包只用
标准库，没有调用入口或 root RPC，尚未接入持久化 job、Module Command 或宿主动作。详情和剩余
迁移归 [统一动作 ABI 计划](action-abi.md)，不能据此把 HTTP 宿主适配器标为完成。

2026-09-17 前置续作：在既有 console job journal 补动作 binding/独立 seq、公有事件投影、原子
截断/终态、重放与重启 unknown 恢复，并补 `jobexecutor.ActionRecorder`，等待真实 EOF/进程退出
证据后再提交终态。没有第二份 job store；普通 worker 暂不领取动作 job，现有 Module Command
与 CLI/HTTP 未切换。注册表/权限、受监督进程、合流/幂等保留/协作取消和宿主通道仍待接线。
这些改动均未编译/测试，待测场景已登记，不能替代 HTTP 宿主盘点适配器或实际 guest 验收。

2026-09-18 审查续作：补写中断后缺失的 Linux Module 动作执行入口；同一工作树还观察到注册表与
进程组盘点的并发改动，继续保留，不把其他未提交改动覆盖或计为本轮独立验证结果。现有执行代码
仍没有接入生产 dispatcher、CLI/HTTP 或宿主通道。

另补 `consolejobs.actionExecutionBarrier`，在普通及动作任务的统一启动检查中拒绝清理失联、
守护进程重启留下的 unknown 动作；涵盖同一 store 的跨 workspace 和只读任务。普通补偿确认不解除
进程清理阻断。新增 `action_admission_test.go` 覆盖 journal 压缩/重开、补偿确认、旧领取入口、
动作入口、只读与跨 workspace，以及阻断期间仍可读取/重放/取消排队任务；测试未运行。真实残留
进程与 writer 的核验和受限解除动作仍待交付，不能依赖 registry 的内存标记替代重启后的阻断。

同时补齐 M8b 的双语路径示例，并将宿主供给 §3.3 的目标安装步骤改为遵守现有 btrfs/zfs 配额准入，
不再写默认 dir。没有增加安装器或执行宿主变更；这两项文档修正不等于 M8b/M10 验收完成。

同日队列接线：新增显式装配的 `ModuleActionWorker`，从共用 journal 消费登记 workspace 的动作，
使用 daemon 生命周期；运行取消在再授权/审计和意图持久化之后通知，排队取消与 Start 原子竞争。
补队列、取消、权限撤销和 EOF/终态/强杀回归源，并同步统一 ABI 架构及英文摘要。该 worker 尚未
接入主 daemon 或现有 Module Command/CLI/HTTP，不能视为生产 dispatcher 或宿主通道已经完成。
新增用例仍未编译/执行；保留既有暂停测试、门禁和服务器操作的限制。

同日补充执行器侧 `actionabi.ReadModuleCancellation` 与协议、recorder、Linux 原生 ELF 回归源，
明确专用取消管道与 stdin/订阅断连的区别；原生夹具覆盖确认/忽略取消、成功后挂起、缺终态、
尾随坏数据与 stderr 超限。断言和待执行命令已加入 E2E 清单；源码未编译、测试未运行，平台
或权限导致的 skip 不等于通过，也不替代 Incus/KVM、Traefik 或宿主网络验收。

同日继续补动作前置的预检与取消控制：排队预检失败以固定 `action_not_started` 事件和 failed
job 原子收尾，不伪造 StartedAt，也不要求未发生执行的业务补偿；运行中失败不得冒用此通道。
当前 Module worker 所用取消接口先持久化首次操作者/时间，再通知执行器，重复请求不改写；
记录不随事件尾部截断丢失。预检与取消的压缩/重开、审计失败、身份不符和竞争回归源码已补，
详见 `internal/consolejobs/action_preflight_recovery_test.go` 与 `action_cancellation_journal_test.go`。
它们均未编译或执行，不能据此通过 Incus 宿主盘点、真实进程清理或生产入口验收；ABI 细节归配套计划。

同日消费者请求文件续作：新增 `computeingress.RequestWriter`，与中介共用有界严格读取器，
支持私有目录中的原子提交、同请求重试、跨进程协作锁和基于保留 inode 的撤回。`Resume` 只接管
与消费者持久化预期完全匹配的已有文件，不创建缺失请求；关闭本地句柄不自动撤回。目录/句柄均
有容量限制，链接、FIFO、目录替换和提交结果不确定不按成功处理。文件回执不证明路由、认证、
网络许可或地址释放完成，未知工件仍走原有独立恢复边界。

共享 `computeclient` 已有显式 `OpenHTTPPublisher`、`PublishPort`、`UnpublishPort` 请求接线，
对照当前运行实例的 workload 与冻结策略，返回请求回执及预测 URL，不宣告网络 ready；不增加自动
挂载、Secret Store 整体读取或生产启用。三处消费者/Provider Dockerfile 和两份 CI 目录已纳入
`internal/computeingress` 共享依赖。新增文件完整性回归源覆盖幂等、恢复、旧回执、原位篡改、
链接/FIFO、目录置换、256 项上限、取消及并发；尚未编译/执行，也未执行镜像构建或宿主验证。

共用 ABI 另已加入 `ModuleActionDispatcher` 的调用、查询和订阅服务，执行与持久取消直接复用
ModuleActionWorker。入口权限在创建/启动提交及排队预检时复核；历史任务可见性不依赖动作仍在
注册表中，订阅断连不取消任务，Module worker 不领取宿主动作。相关回归源未运行，实际
CLI/HTTP、主 daemon 与宿主适配器仍未接线；本变化不完成 M10/M11 或放开 ingress。

同日消费者 API 边界补充：`HTTPPublicationConfig` 改为租约范围、公开策略、基础域名和私有目录
的最小投影，random 命名密钥独立传入且不进入默认 JSON/格式化输出；不向消费者交付完整冻结
授权、middleware、entrypoint 或全局 Store 引用。共享 `Policy.Host` 只负责名称预测，中介继续
经 `Authorization.Host` 校验完整授权。`Inspect` 改为精确匹配实例名并拒绝重复身份；发布读取
受管 Running 实例的 workload，撤销绑定原 `HTTPPublication` 回执而非可复用的实例名/端口。
新增 `http_publication_contract_test.go`、`http_publication_projection_test.go` 与
`request_writer_receipt_regression_linux_test.go`，覆盖投影/授权边界、精确身份、名称冲突、旧回执、
取消、提交不确定和跨 writer 文件替换。仅写入源码及双语文档，测试、构建和实机验收仍未执行；
最小投影的自动交付、应用侧恢复与真实发布状态确认仍未接线。

同日统一动作前置续作：注册表、dispatcher 与单一 job store 已接入动作级可选 key、终态后
1 小时保留及 `coalesce/reject/queue`。相同 key 改变冻结参数或 workspace 时冲突，合流新键
原子持久化并以实际加入者审计；读取权限不能由 key 绕过。压缩恢复核对键归属链，无键合流不
制造别名，64 键上限显式拒绝。相关回归源已补，尚未编译/执行；细节与剩余迁移由
[统一动作 ABI 计划](action-abi.md) 维护，不据此完成宿主盘点、二段确认或 Incus 验收。

以下为截至 2026-09-18 的当前剩余工作；上文按日期记录实现过程，不表示各阶段已验收。

| 工作 | 当前代码与剩余边界 | 后续依赖 |
| --- | --- | --- |
| HTTP 宿主动作与孤立网络工件 | 接口强制盘点地址保留、`/32`、HTTP 许可、存量连接及默认拒绝基线；ABI 协议、共用 journal/recorder、内部 Module worker/dispatcher 及动作级幂等/合流已编码，仍无真实宿主适配器 | 生产执行所有者和客户端接线、宿主通道、独立分配与回执来源；保留 IP 复用/已有连接验收 |
| 缺失/损坏回执恢复 | 已登记目标有持锁撤销，文件/API 孤立工件会拦截开通与地址释放；未知归属不自动导入/删除，不复活 tombstone | 独立备份/宿主证据、版本化 renderer 证据及受限管理员恢复动作；不得先删状态再重跑 |
| 服务与消费者装配 | Source、Controller、凭据交付、fixture 登记、Probe 与消费者请求文件 API 已编码；生产入口仍拒绝 ingress | 服务端只读身份、UID/挂载、可信启动/netns、guest HTTP 服务、事件来源、应用生命周期及请求恢复接线、凭据轮换清理；文件回执不代表发布完成 |
| 回归与实机验收 | 所有续作代码未编译/测试，新场景已记入 E2E 待测表；仅允许格式化和文档/索引生成 | 用户恢复测试后运行相关门禁，再在已指定独立宿主执行真实 Docker/Incus/Traefik E2E；无 KVM 时 VM 仍待验收 |

没有新增生产服务、特权 RPC 或 TCP/UDP 发布实现；M6/M11、30/75 和 developing 保持不变。

### 8.7 M12：guest_image 与镜像烘焙

2026-09-10：`internal/computeimage` 已实现严格对象数组解析、互斥字段验证、按架构/interface
精确解析受信目录、稳定目录摘要、目录历史不可变校验和可序列化解析结果。单元测试覆盖字符串旧格式
拒绝、未知/混用字段、缺失目标、重复目录键、历史变更和冻结值不受调用方修改影响。
Core 已接入 deployment 准备与重放；Forgejo 使用单镜像对象，AI Agent 使用 runtime→对象映射。
Provider 核验租约 project 镜像 metadata；受信 bundle 目录为空，尚无可分发的命名镜像产物。

- [x] 实现独立结构化解析与目录解析包，不读取网络、不以 alias 解析，也不引入依赖；
- [x] 接入 Core 前统一 `spec_from` 的结构化值传递：Forgejo 单镜像与 AI Agent runtime→image 映射均需显式投影为对象数组，不按 Module 名分支；
- [x] 将解析结果、目录摘要和目标冻结到实际 deployment，并补消费者投影/回滚端到端单元测试；

2026-09-18 增加离线核验接线：`release_verify.go` 复用共享 `ArtifactRelease`，将实际工件与
独立冻结的引用/目标/配方摘要及 fingerprint 比较，拒绝同 revision 用不同字节替换已有摘要。
`cmd/compute-image-artifact` 只读本地 split 文件，inspect 输出候选规范描述，verify 必须另给
可信预期 fingerprint；不执行 builder、导入、下载、发布或修改受信目录。新增独立
`release_verify_test.go` 和 CLI 用例，覆盖身份漂移、片段损坏/尾随数据、无进展 Reader、
自证信任拒绝及普通文件边界。测试均未运行，不能把字节完整性当成镜像可启动或 M12/M13 验收。
同一工作树有并行写入，已将命令和用例适配到当前共用格式，不保留第二套 split 描述协议。

同日本地归档续作：`ArtifactArchive` 和 `cmd/incus-image-artifacts` 增加显式 init/record/inspect/
catalog，复用 `DescribeArtifactRelease` 和规范描述。私有本地归档持排他锁；内容寻址对象先
同步、无覆盖发布，再提交 revision 元数据。相同版本内容可幂等重录，缺失对象只从相同原始
字节恢复；冲突、损坏对象、符号链接和未知元数据保留并失败。生成候选目录需显式上一份可信
目录或首次发布，旧版本键不能丢失/改变，不自动修改 bundle 的空目录。

已新增 `artifact_test.go`、`artifact_archive_test.go` 和 `cmd/incus-image-artifacts/main_test.go`，
覆盖摘要顺序、规范记录、原字节恢复、冲突不覆盖、坏对象、锁/路径替换、取消、目录历史与 CLI
元数据输出边界，均未编译或执行。仅有归档代码不能证明真实 distrobuilder provenance、镜像
可启动、Provider 导入/回滚恢复或 prune 保留策略；下列端到端目标仍保持未完成。相关用法和
待测场景已同步双语技术文档、架构 §6.2.3 及 E2E 清单。

- [ ] 定义 guest_image 契约与配方/revision 的所有权，采用 distrobuilder；
- [ ] 评估架构 §6.2.1 的发布时烘焙、apply 前冻结目录候选，明确首次烘焙阶段与产物分发；不新增 Provider 结果通道；
- [x] 声明只接受互斥的 catalog/name/revision 或 fingerprint 对象，拒绝字段混用、未知字段及所有字符串旧格式；同步消费者声明、配置示例和测试，运行时仍只传解析后的摘要；
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

以下记录于 2026-09-10，基线为 `f7642c5` 加本轮未提交工作树；逐项结果见表，不代表已提交版本的 CI 结果。

下表通过记录对应 2026-09-11 的代码；2026-09-12 新增 HTTP 采集/生命周期、授权冻结与实验中介路径尚未运行编译门禁或测试；文档生成不计作验收。

| 门禁 | 最近验证结果 |
| --- | --- |
| `go vet ./...` / `go test ./...` | 本地通过；覆盖结构化投影、冻结重放、ensure ABI、镜像 metadata 和网络原型 |
| `go run ./cmd/check-shared-build` | 本地通过；不代表实际镜像构建通过 |
| `go run ./cmd/gen-module-docs --check` | 本地通过 |
| `go run ./cmd/gen-contract-docs --check` | 本地通过 |
| `npm run docs:check-requirements` | 本地通过 |
| `npm run docs:check-requirement-status` / `docs:check-plan-status` / `docs:check-status` | 本地通过 |
| `npm run docs:build` | 本地通过；存在非阻断的 chunk 大小警告 |
| `npm --prefix web run check:api` | 本地通过 |
| `go run ./cmd/check-upgrade-tests` | 失败：已登记的 `ai_agent` 缺少升级目录条目；目录、登记文件与校验代码均与 HEAD 相同，是基线缺口，未以跳过项掩盖 |
| 渲染产物 `docker compose config --quiet` | 未执行；缺少真实 apply 渲染产物 |
| Linux `nft --check` 与隔离 namespace 实际 HTTP 流量 | 2026-09-11 通过；20 项 namespace 检查、宿主规则不变，见实验记录 |
| Docker/Incus 规则共存与两档真实生命周期 | 未执行；指定宿主未发现 Incus 与 `/dev/kvm`，namespace 模拟后端不替代该验收 |

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
| R-053 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | Traefik 公网双栈、受管 guest IPv4 后端 | — | 待执行 |
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

1. 网络控制连接按架构 §3.7 推进验证；入站按 §5.1.7 验证 Traefik 直达 guest 的受限路由与授权回收；命名镜像解析仍需明确产物目录与烘焙阶段。
2. 可独立推进 M8b staging 验证、M11 ingress 声明校验、M6 测试脚本；不得因此宣称完整入站可用。
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

### 本轮接续边界（2026-09-10）

结构化镜像声明不兼容旧字符串。没有镜像冻结信息的历史 deployment 不再可由新 Core 重放，
需新 apply；旧元数据仍可加载用于停止与替换，执行前拒绝旧镜像声明；已有 Secret 不重铸。镜像目录历史独立存于 Core state，不随 deployment 留存清理。
宿主供给、独立 lease_secret、生产中介、运行时生命周期对账、自动导入与 distrobuilder 尚未完成。
本轮不提交、不合并；真实宿主验收继续阻塞，不以单元测试改变 Module developing 状态。
文档和 Web API 检查最初因工作树缺少 node_modules 失败，按已有锁文件离线安装后重跑通过，锁文件未改变。

### 接续记录（2026-09-11）

M11 的独立 `lease_secret` 已落地：32 字节随机、每资源单独 Secret Store 条目、跨 apply 稳定，
不与客户端证书同存或派生。Forgejo 两个 compute 服务与 AI Agent orchestrator 接收敏感投影；
Provider ensure 与 guest 不接收。Deployment/resource state 只存引用。旧冻结部署读取/重放不
生成密钥；新 apply 补齐时保留客户端证书。已损坏/空值、错误归属、跨租约引用和已记录引用对应
的 Store 条目丢失均失败，不静默换 URL。禁止凭据声明冒用命名密钥，排除通用轮换与 `--all`。

新增回归覆盖持久化重载、证书独立性、作用域、config list 与验证 Hook 脱敏、旧部署补齐、
冻结重放，以及实际文件 copy backup/restore 后密钥和 HMAC 派生结果一致。仅测试命名结果稳定性，
未交付域名派生 API 或运行时发布；专属密钥轮换命令仍待凭据计划 CRED-R-015 对接。

全仓 `go vet ./...` / `go test ./...`、共享构建、Module/Contract 文档生成检查、需求/计划/状态门禁、
API 一致性与双语文档构建通过。升级门禁仍因基线缺少 `ai_agent` 目录条目失败。
操作者指定的 Ubuntu 26.04 宿主已完成只读检查与隔离 namespace 数据面实验：实际加载生成 nft
规则，20 项 HTTP/来源隔离/冒用/反向/IPv6/已有流撤销/超时检查及宿主规则不变检查通过。
已补可复现入口 `test-env/scripts/server-incus-http-netns.py`；记录见
[2026-09-11 实验报告](../reviews/2026-09-11-incus-http-netns-validation.md)。首轮夹具来源恢复失败，
修正接口恢复步骤后完整复测通过。未修改现有 Docker、宿主防火墙或其他测试实例。
该宿主缺少 Incus CLI、`/dev/kvm` 与 conntrack CLI；没有运行真实受管 guest、Traefik、规则共存、
IP 复用或 conntrack 删除验收。M6 与 M11 的实机退出条件保持未完成。未提交、未合并。


### 编码优先与后续 E2E（2026-09-11）

按操作者最新要求，先完成代码、文档与本地门禁，之后执行服务器 E2E。待测场景已提前写入
[服务器 E2E 清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md)：结构化 apply/冻结回滚、
消费者配置、存储池准入、真实写满、双消费者围栏、两档生命周期与 HTTP 规则共存/撤销/IP 复用。
已有原型和密钥实现不重做；上述待测项不因本地测试通过而标记完成。

本轮发行版 Incus 6.0.5 探查已观察到 dir 池跳过磁盘配额而旧 Provider 返回 quota_enforced=true。
修复增加存储池只读准入和写后复核，仅接受 Created 的 btrfs/zfs；缺失、未就绪、dir 与未准入驱动
在 ensure 租约副作用前失败，inspect 保留项目存在/限制信息但不再报告配额就绪。该准入并非实际
磁盘写满证据，M6/R-032 保持待验收。未开放生产 ingress、TCP/UDP 或自动宿主安装。

本轮新代码的全仓 `go vet ./...` / `go test ./...`、共享构建、Module/Contract 生成检查、需求/计划/
状态检查与双语文档构建均通过。已有 `ai_agent` 升级目录缺口未改变。远程测试 guest 最后确认仍未成功启动；已停止并清理
本轮独立 daemon、临时目录与残留测试 AppArmor profile，确认无测试进程/挂载/loop，
停止后的宿主 nft 摘要与基线一致。后续按上述清单统一执行，不继续即兴扩大远程实验。


### HTTP 原型编码接续（2026-09-12；本轮不测试）

按操作者要求只继续代码、文档与格式化，暂不运行编译、测试、验证门禁或服务器操作。新增
`cmd/incus-network-prototype/capture.go`：显式实验 Unix sockets 的 GET 采集，交叉核对 Incus
project/bridge/NIC/地址分配、Docker endpoint 和宿主 veth，并以两轮观测拒绝漂移；复用已有
`computeclient.NetworkName`，未加 Go 依赖。此特权实验入口不是生产只读中介，不改变 Module 挂载。

`--previous`/`--withdraw` 生成有顺序的生命周期计划。旧身份撤销后保留拒绝表，同拓扑替换使用
单次 nft 事务；拓扑改变要求结束旧实验，不能沿用旧路由。采集失败或当前观测不可发布时只输出
有效旧记录的撤销计划。输入增加 64 KiB、重复/未知字段、符号链接及嵌套深度拒绝，输出独占创建。
计划是待执行工件，publication.json 不是应用回执；没有宿主动作执行器、IP 预留或运行时 watcher。

用法与边界已同步原型 README、Module 双语技术文档和架构；具体待测项补入
[服务器 E2E 清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md)。2026-09-11 的通过结果
不覆盖本轮代码。M6/M11、30/75 状态与 developing 保持不变，未提交、未合并。
