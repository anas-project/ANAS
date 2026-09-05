---
doc_type: requirement
status: current
created: 2026-08-23
updated: 2026-09-01
---

# Incus compute Provider Module 集成要求

本文规定 ANAS 把 Incus 适配能力落成 `compute` Contract Provider 时必须交付的结果、边界和验收
标准。设计背景见 [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../docs/architecture/ai-agent-orchestration-design.md) §5.2；施工顺序见
[Incus compute Provider 实施计划](../plans/incus-module.md)。Contract 定义以
[`contracts/compute/contract.yml`](https://github.com/anas-project/ANAS/blob/master/contracts/compute/contract.yml)
为准，本文不复制其字段。

关键词“必须”“不得”“应该”具有规范性。

## 1. 目标

`compute` Contract 曾把七个实例生命周期操作定义为 Contract operation。这条路走不通：Provider
operation 的唯一 runtime 是 `compose_run`（`internal/runner/contracts.go`），由 Runner 在 apply 时
以 `docker compose run --rm` 执行一次；ANAS 运行时不存在「模块容器 → Core」的调用通道，而一次性
实例是 per-job 热路径、发起方是常驻消费者容器。同一原因也使 `exec_stdin` 承诺的「stdin 是唯一
Secret 通道」在 provider ABI 上无法实现——该 ABI 只传 `ANAS_RESOURCE_*` 环境变量。

因此职责按时机切分：**Contract 交付围栏，消费者在围栏内自行驱动实例，管理员经 Module Command
观测围栏**。一个受限 project 之于 `compute`，等于一个 database + role 之于
`relational_database`：Provider 在 apply 时把它建好并交出凭据，之后不再位于数据路径上。

| 层 | 机制 | 时机 | 发起方 |
| --- | --- | --- | --- |
| 围栏供给 | `compute` Contract `ensure` | apply，一次 | Runner |
| 围栏内使用 | 共享客户端库直连 Incus daemon | 每个 job | 消费者容器 |
| 围栏运维 | Module Command | 按需 | 管理员 |

## 2. 范围

必须包含：

- `compute` Contract 从七个运行时操作改形为 `ensure` / `inspect` / `revoke`，schema 描述租约而
  非实例；
- `incus` Provider Module：manifest、Provider 实现、凭据、双语文档与单元测试；
- 每消费者独立的 restricted project、客户端证书与实例名前缀；
- Core 对 `compute` Resource 的校验、凭据生成与投影；
- 共享 Incus 客户端库：实例生命周期的唯一实现，供 Forgejo Actions 与后续消费者 import；
- Forgejo Actions controller 迁移为 `compute` 消费者，删除自有 Incus 适配代码。

必须包含（第二阶段）：

- `incus_container` interface：非特权 Incus 系统容器（LXC）作为轻量隔离档。

预留（第三阶段，本轮不实现）：

- **长驻实例档**：让消费者把租约里的实例当一台可用主机使用，而不只是一次性作业执行器。当前
  三条限制使它做不到——exec 走入口 allowlist（跑不了任意命令）、没有持久卷、没有稳定入站地址。
  预留的意思是：`compute` 的 schema 与操作语义为它留出扩展位，但在需求 §7ter 明确之前不实现，
  也不得靠放宽现有约束来变相提供。

必须包含（第四阶段，设计见 [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)）：

- 宿主 Incus daemon 的自动安装与配置：一次性、幂等、可跳过、有卸载路径；
- guest 镜像的自动产出：distrobuilder 配方，构建一次记录摘要。

不要求：

- 支持 Incus 以外的虚拟化后端（libvirt、Proxmox、Firecracker）；
- 嵌套虚拟化、设备直通或跨宿主迁移；
- 预热池、自动扩缩容和跨作业复用实例；
- 让 ANAS Core 参与单个实例的创建、执行或销毁。

## 3. Module 与部署边界

1. Module 名必须为 `incus`，类别为 `compute`；真实 Incus 宿主验收完成前状态必须为 `developing`。
2. Module 必须只实现 Incus 的**客户端**控制面：它不得在容器内运行 Incus daemon，不得挂载宿主
   虚拟化设备或 Docker socket。daemon 由**安装期的一次性宿主供给步骤**装好，不由 Module 安装
   ——两者是不同的特权边界，不得合并。
2bis. 默认隔离档必须是 `incus_container`。ANAS 宿主不保证具备 KVM，把需要 KVM 的档位设为默认
   会让服务在目标硬件上装不上；VM 是消费者显式选择的升级，Provider 不得自动降级或自动升级。
3. Module 不得暴露任何 HTTP 服务、Traefik 路由或宿主端口。它只通过 Contract Provider operation
   被 Runner 调用，且只有 run-only 服务。
4. Incus endpoint、pinned server certificate、供给用管理证书与 key 必须由本 Module 拥有并按敏感
   配置处理；不得写入 README、`config list` 输出、deployment manifest 或日志。
5. Provider 在写入后必须**读回**并断言目标 project 存在、`restricted=true` 且四项配额均已设置；
   任一条件不满足必须 fail closed，且不得继续登记消费者证书。

## 4. 多消费者隔离

1. 每个消费者必须绑定独立的 restricted project 与独立的客户端证书；不得让两个消费者共用同一
   project 或同一证书，Core 必须在解析阶段拒绝重复 sandbox。
2. 跨消费者隔离必须完全由 project 承担，不得依赖实例名前缀：受限证书越不出自己的 project。
3. 实例名前缀解决的是同一 project 内部的归属问题——区分 ANAS 托管实例与运维手工创建的实例，
   使 janitor 不回收不属于它的实例。它是消费者侧过滤，不是安全边界。
4. 隔离必须由 Incus daemon 自身执行（受限证书 + project 配额），不得仅依赖消费者代码自觉遵守。
5. Provider 必须拒绝复用已被以全局权限信任的证书，也必须拒绝已绑定其他 project 的证书。
6. profile、network 与 storage pool 归 Provider 所有；project 必须封禁 device、raw config、挂载与
   低层配置。

## 5. Contract operation 语义

1. `ensure` 必须幂等：重复调用不得产生第二个 project 或第二条 trust 条目，且必须合并而非覆盖
   project 上与本 Contract 无关的既有配置键。
2. `inspect` 必须只读，并分别报告 `exists`、`ready`、`restricted` 与 `quota_enforced`；project
   不存在必须返回明确的“不存在”结果而不是失败。
3. `revoke` 必须撤销消费者证书并保留 project；对不存在的证书必须幂等成功。
4. Provider 必须固定（pin）Incus server certificate，不得在 TLS 校验失败时回退到不校验。
5. 每次操作的错误必须可归因到具体参数，且不得回显证书、私钥或消费者 Secret。

## 6. Core 与凭据

1. Core 必须为每个 `compute` Resource 生成稳定的客户端证书与私钥，并作为单条 Secret 保存；重复
   apply 不得重新签发，否则会作废 daemon 上已登记的 trust 条目。
2. 只有证书半边可以进入 Provider；私钥必须直接投影给消费者，不得经过 Provider。
3. Core 必须校验 `sandbox`、`instance_prefix`、`quota` 区间与 `image_allowlist`（仅接受 SHA-256
   fingerprint，拒绝 tag、alias 与远程 URL）。
4. 就绪租约必须投影到消费者私有命名空间，私钥标为敏感；deployment manifest 与 resource state 只
   保存 Secret 引用，不保存明文。
5. 管理证书轮换必须可在不销毁运行中实例的前提下完成。

## 7. 共享客户端与 Forgejo 迁移

1. 实例生命周期（create/start/exec/stop/delete/list/janitor）必须由**一个共享客户端库**实现；
   仓库不得保留两套 Incus 适配代码。
2. 共享库必须校验镜像 fingerprint 在 allowlist 内、实例名在本消费者前缀内、资源上限在租约配额
   内，并拒绝调用方传入 device、raw config、挂载或 profile 覆盖。
3. 一次性 Secret 必须只经 stdin 进入 guest；不得出现在命令参数、环境变量、cloud-init、镜像、
   磁盘状态或日志中。
4. guest 入口命令必须按消费者各自登记的 allowlist 校验，不得接受任意命令。
5. 操作必须支持超时与取消；调用方取消后不得遗留 running 实例。
6. Forgejo Actions controller 必须改为 `compute` 消费者，删除自身的 Incus 客户端与配置项；
   one-job 行为必须等价：ephemeral 注册、stdin 注入 runner token、单作业后销毁、janitor 回收。
7. 迁移不得要求现有部署迁移数据或重建 Incus project；已有 `anas-forgejo-runners` 必须继续可用。

## 7bis. 第二 interface：非特权系统容器

1. `incus_container` 必须在**不改变 schema 与操作语义**的前提下加入；消费者通过 interface 选择
   隔离档，请求字段保持一致。
2. 系统容器租约必须与 VM 租约受同样约束：restricted project、配额、实例名前缀、镜像 fingerprint
   与命令 allowlist。
3. 容器必须以非特权运行，且该约束必须写在 project 上（`restricted.containers.privilege`），不得
   只靠创建实例时的参数。
4. 文档必须写明两档的**边界差异**：系统容器与宿主共享内核，VM 提供独立 guest kernel；选择哪一档
   是部署决策，Provider 不得自动降级。

## 7ter. 预留：长驻实例档（第三阶段）

本节只规定**预留位的边界**，不规定实现。它存在的目的是让第三阶段无法靠悄悄放宽现有约束达成。

1. 长驻档必须是一个**显式的新 interface 或新 lifecycle 值**，不得通过放宽 `exec` 入口 allowlist、
   取消实例名前缀或延长一次性实例寿命来变相提供。
2. 任意命令执行必须是一个独立的、可在配置中看见的开关，且默认关闭；不得由 allowlist 里的通配符
   隐式打开。
3. 持久卷必须仍由 Provider 拥有：卷落在受管存储池上，消费者不得传入 host 路径或 source。
4. 稳定入站地址必须经受管 network 的显式端口/转发声明表达，不得让消费者直接持有宿主端口。
5. 长驻实例不改变配额语义：它照样占 `limits.instances`，且不得绕开 project 配额。
6. 本档不得削弱已有边界——受限证书、project 隔离、镜像 fingerprint allowlist 与非特权容器约束
   在长驻档下必须同样成立。

## 7quater. 预留：image_policy

`image_policy` 已作为 schema 字段存在，取值 `pinned`（默认）与 `any`。**当前实现只接受 `pinned`，
`any` 一律拒绝**。预留的边界：

1. `any` 必须是消费者 manifest 里显式写下的值，且出现在 `config list` 与审计中；不得由
   `image_allowlist` 里的通配符或空列表隐式打开。
2. `any` 不得用于状态为 `release` 的 Module。
3. 启用 `any` 之后，镜像约束在 Provider 侧完全消失——它是本 Contract 唯一不由 daemon 兜底的约束，
   因此文档必须写明该租约的镜像来源等同于消费者代码的可信度。
4. `any` 不得连带放宽任何其他约束：project 隔离、配额、实例名前缀与非特权容器仍然成立。

## 7quinquies. 宿主供给、入站与镜像烘焙（第四阶段）

设计见 [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)。

1. 宿主供给必须一次性、幂等、可跳过、可卸载。装不上时不得让整个部署失败：没有 daemon 就没有
   `compute` provider，声明了 `enabled_by` 的消费者保持关闭。
2. 发行版到安装步骤的映射必须是**声明式的表**，不得在代码里按发行版名写分支；新增一个发行版
   应当是加一行数据。
3. 一级发行版不得添加第三方软件源；需要第三方源的发行版必须显式征得同意，且失败时给出手工
   指引而不是静默降级。
4. daemon 默认只监听回环。对外暴露 Incus API 是一次独立的加固决策，不得作为本流程的副产物。
5. **Web 管理端不得接受 root 或 sudo 密码。** 安装期那一次 `sudo` 之后，特权动作必须经
   [宿主特权动作通道](../../docs/architecture/host-action-channel.md) 触发：通道只接受动作 id 与
   类型化参数，永不接受命令、argv、路径或脚本，动作实现编译进 root 二进制。通道实现之前，控制台
   显示待执行命令并轮询状态。
5bis. CLI 与 Web 必须走同一条特权通道：能力差异只可能来自动作清单，不得来自入口。
6. 实例不得依赖公网 IPv6 地址或上游 DHCPv6-PD。出站经受管 bridge NAT，入站统一经 Incus proxy
   device，且必须同时支持 IPv4 与 IPv6 入站。
7. 实例内 HTTP 服务经 Traefik 发布必须复用既有的 `ANAS_TRAEFIK_ROUTE__*` 文件 provider 路由，
   不得为此在 Traefik 侧新增机制。
8. guest 镜像配方必须使用 distrobuilder，不得自造 Dockerfile 方言。
9. 镜像语义必须是**构建一次、记录摘要、之后不再重建**：apply 时摘要已存在即不动作；配方变更是
   显式版本变更，产出新摘要而不是就地覆盖。每次 apply 重新烘焙会让 fingerprint 钉死失去意义。

## 8. 验收标准

### 8.1 静态与单元验收

- Contract、Module manifest、Provider 声明、schema 与双语文档通过仓库全部 manifest/documentation
  gate；
- 单元测试覆盖：租约校验（sandbox、前缀、配额区间、image fingerprint）、幂等、fail-closed、
  越权证书拒绝、证书固定失配、敏感值不出现在日志与错误中；
- Core 单元测试覆盖凭据稳定性、消费者间隔离与 sandbox 冲突拒绝；
- 共享客户端单元测试覆盖 allowlist、前缀、配额、命令 allowlist、超时取消与 Secret 边界；
- Forgejo controller 在迁移后仍通过既有单元测试，且不再引用自有 Incus 客户端。

### 8.2 真实部署验收

- 在独立 KVM/Incus 宿主完成 `ensure` → 消费者 `create → start → exec → delete` 全流程；
- 两个消费者并行运行，验证 project 与证书确实隔离（前缀相同也不得互相可见）；
- 验证 restricted 证书无法访问目标 project 之外的资源；
- 验证配额确实由 daemon 执行（超配额创建被拒绝）；
- 验证消费者 crash 或取消后 janitor 能回收残留实例；
- 验证管理证书轮换流程与其对运行中实例的影响；
- 记录 VM 与系统容器两档的启动时延与一次典型作业墙钟耗时，作为容量规划基线。

完成 §8.1 只代表实现进入 `developing`。只有 §8.2 全部有可复现证据才可以把 Module 提升为
`release`。

## 9. 需求矩阵

本矩阵是规范来源，正文是解释；两者冲突时以矩阵为准。ID 一经分配即固定，废弃的需求保留行并标 `已废弃`，编号不复用。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `INCUS-R-001` | 必须提供名为 `incus`、类别 `compute` 的 Module，声明 `contracts.provides: compute@1.0.0` 的 `incus_vm` 与 `incus_container`；真实宿主验收完成前保持 `developing` | 静态 |
| `INCUS-R-002` | Module 必须只实现客户端控制面：不安装或运行 Incus daemon，不要求 ANAS 宿主具备 KVM，不挂载宿主虚拟化设备或 Docker socket | 静态 + 审阅 |
| `INCUS-R-003` | Module 不得暴露 HTTP 服务、Traefik 路由或宿主端口，只能通过 Provider operation 被 Runner 调用 | 静态 |
| `INCUS-R-004` | endpoint、pinned server 证书、供给用管理证书与 key 必须按敏感配置处理，不进入 README、`config list`、deployment manifest 或日志 | 单元 |
| `INCUS-R-005` | `ensure` 写入后必须读回并断言 `restricted=true` 且四项配额已设置，否则 fail closed 且不登记消费者证书 | 单元 |
| `INCUS-R-006` | 每个消费者必须绑定独立 restricted project 与独立客户端证书；Core 必须拒绝两个消费者共用同一 sandbox | 单元 |
| `INCUS-R-007` | 跨消费者隔离必须由 project 承担而不是由实例名前缀承担；前缀只用于在同一 project 内区分 ANAS 托管实例，使 janitor 不回收运维手工创建的实例 | 单元 + e2e |
| `INCUS-R-008` | 隔离必须由 Incus daemon 自身执行；受限证书越不出自己的 project，配额写在 project 上 | e2e |
| `INCUS-R-009` | Provider 必须拒绝复用无限制的已信任证书，以及已绑定其他 project 的证书 | 单元 |
| `INCUS-R-010` | `contracts/compute` 必须改形为交付租约的 `ensure`/`inspect`/`revoke`，并去除固定 `anas-forgejo-runners` 的单消费者假设 | 契约 |
| `INCUS-R-011` | project 必须封禁 device、raw config、挂载与低层配置；profile、network、storage pool 归 Provider 所有 | 单元 + e2e |
| `INCUS-R-012` | `ensure` 必须幂等，且合并而非覆盖 project 上与本 Contract 无关的既有配置键 | 单元 |
| `INCUS-R-013` | `inspect` 必须只读并分别报告 `exists`/`ready`/`restricted`/`quota_enforced`，project 不存在时返回明确“不存在” | 单元 |
| `INCUS-R-014` | `revoke` 必须撤销消费者证书、保留 project，且对不存在的证书幂等成功 | 单元 |
| `INCUS-R-015` | 必须 pin Incus server 证书，TLS 校验失败时不得回退为不校验 | 单元 |
| `INCUS-R-016` | 操作错误必须可归因到具体参数，且不得回显证书、私钥或消费者 Secret | 单元 |
| `INCUS-R-017` | Core 必须为每个 Resource 生成稳定客户端证书与私钥并存为单条 Secret；重复 apply 不得重新签发 | 单元 |
| `INCUS-R-018` | 只有证书半边可进入 Provider；私钥必须直接投影给消费者，不经过 Provider | 单元 |
| `INCUS-R-019` | Core 必须校验 sandbox、instance_prefix、quota 区间与 image_allowlist，只接受 SHA-256 fingerprint | 单元 |
| `INCUS-R-020` | 就绪租约必须投影到消费者私有命名空间，私钥标为敏感；manifest 与 resource state 只存 Secret 引用 | 单元 |
| `INCUS-R-021` | 实例生命周期必须由一个共享客户端库实现，仓库不得保留两套 Incus 适配代码 | 审阅 + 单元 |
| `INCUS-R-022` | 共享库必须校验镜像 fingerprint 在 allowlist 内、实例名在本前缀内、资源上限在租约配额内，并拒绝 device/raw config/挂载/profile 覆盖 | 单元 |
| `INCUS-R-023` | 一次性 Secret 必须只经 stdin 进入 guest，不得出现在命令参数、环境变量、cloud-init、镜像、磁盘状态或日志 | 单元 |
| `INCUS-R-024` | guest 入口命令必须按消费者各自登记的 allowlist 校验，不接受任意命令 | 单元 |
| `INCUS-R-025` | 实例操作必须支持超时与取消，取消后不得遗留 running 实例 | 单元 + e2e |
| `INCUS-R-026` | Forgejo Actions controller 必须迁移为 `compute` 消费者并删除自有 Incus 客户端与配置项 | 单元 + 审阅 |
| `INCUS-R-027` | 迁移后 Forgejo 的 one-job 行为必须等价：ephemeral 注册、stdin 注入 runner token、作业后销毁与 janitor 回收 | 单元 + e2e |
| `INCUS-R-028` | 迁移不得要求现有部署迁移数据或重建 Incus project；已有 `anas-forgejo-runners` 必须继续可用 | 审阅 |
| `INCUS-R-029` | 管理证书轮换必须可在不销毁运行中实例的前提下完成 | e2e + 文档 |
| `INCUS-R-030` | 真实 KVM/Incus 宿主必须完成 `ensure` → 消费者 `create → start → exec → delete` 全流程验收 | e2e |
| `INCUS-R-031` | 真实宿主必须验证两个消费者并行时受限证书无法越出目标 project；即使两份租约使用相同实例名前缀也不得互相可见 | e2e |
| `INCUS-R-032` | 真实宿主必须验证配额由 daemon 执行：超配额创建被拒绝 | e2e |
| `INCUS-R-033` | 真实宿主必须验证消费者 crash 或取消后 janitor 能回收残留实例 | e2e |
| `INCUS-R-034` | 必须记录 VM 与系统容器两档的启动时延与一次典型作业墙钟耗时，作为容量规划基线 | e2e |
| `INCUS-R-035` | `incus_container` 必须在不改变 schema 与操作语义的前提下提供，且非特权约束写在 project 上而非创建参数上 | 单元 + 契约 |
| `INCUS-R-036` | 文档必须写明两档边界差异（共享内核 vs 独立 guest kernel），且 Provider 不自动降级 | 文档 + 审阅 |
| `INCUS-R-037` | 共享 Go 包必须经命名 build context 进入 Module 镜像，不得由 go.mod 从网络拉取；Dockerfile 必须以 `GOPROXY=off` 构建，使意外的网络依赖当场失败 | 静态 + 审阅 |
| `INCUS-R-038` | CI 必须校验 `.github/images.json` 的 `shared_paths` 路径真实存在，且对应 Dockerfile 确实从 `shared` context COPY 了它们——否则「改了共享库不重建镜像」会静默发生 | CI |
| `INCUS-R-039` | 长驻实例档必须是显式的新 interface 或 lifecycle 值；不得通过放宽 exec allowlist、取消实例名前缀或延长一次性实例寿命变相提供 | 契约 + 审阅 |
| `INCUS-R-040` | 长驻档下任意命令执行必须是独立、可见、默认关闭的开关，不得由 allowlist 通配符隐式打开 | 契约 + 单元 |
| `INCUS-R-041` | 长驻档的持久卷必须落在受管存储池，消费者不得传入 host 路径或 source；稳定入站必须经受管 network 显式声明 | 契约 + e2e |
| `INCUS-R-042` | 长驻档不得削弱受限证书、project 隔离、镜像 fingerprint allowlist 与非特权容器约束中的任何一条 | 审阅 + e2e |
| `INCUS-R-043` | 租约网络的 IPv6 必须跟随宿主：仅当 IPv6 开关未关闭且宿主存在全局 IPv6 地址时启用，否则显式设为 `none` 而不是留空 | 单元 |
| `INCUS-R-044` | 启用时 IPv6 必须与 IPv4 一样经同一张受管 bridge 做 NAT，不得为 guest 开辟绕过该 bridge 的出网路径 | 单元 + e2e |
| `INCUS-R-045` | `image_policy` 必须作为 schema 字段存在（`pinned` 默认 / `any` 预留），且 `any` 在实现前必须被显式拒绝而不是静默接受 | 契约 + 单元 |
| `INCUS-R-046` | `any` 实现后必须是 manifest 中显式可见的值、不得用于 `release` Module、不得由通配符或空 allowlist 隐式打开、且不得连带放宽其他约束 | 契约 + 审阅 |
| `INCUS-R-047` | 宿主 Incus daemon 必须由 ANAS 的一次性安装步骤装好并配置，用户不得被要求先手工准备 daemon、证书或 endpoint | e2e + 审阅 |
| `INCUS-R-048` | 宿主供给必须幂等、可跳过、可卸载；装不上时依赖 `compute` 的功能保持关闭而不是让部署失败 | 单元 + e2e |
| `INCUS-R-049` | 发行版到安装步骤的映射必须是声明式的表；一级发行版不得添加第三方软件源；未适配的发行版不自动安装，报错给手工指引并让依赖 `compute` 的功能保持关闭 | 静态 + 审阅 |
| `INCUS-R-050` | daemon 默认只监听回环；对外暴露 Incus API 不得作为宿主供给的副产物发生 | 静态 + e2e |
| `INCUS-R-051` | Web 管理端不得接受 root 或 sudo 密码；一键提权只能经安装期预置的受限特权单元触发，密码不得进入应用 | 审阅 + 安全测试 |
| `INCUS-R-052` | 默认隔离档必须是 `incus_container`；不得因宿主缺少 KVM 而自动降级，也不得自动升级为 VM | 单元 + 契约 |
| `INCUS-R-053` | 实例不得依赖公网 IPv6 或上游 DHCPv6-PD；入站必须经 proxy device 且同时支持 IPv4 与 IPv6 | 单元 + e2e |
| `INCUS-R-054` | 实例内 HTTP 服务经 Traefik 发布必须复用既有 `ANAS_TRAEFIK_ROUTE__*` 机制，不得在 Traefik 侧新增能力 | 契约 + e2e |
| `INCUS-R-055` | guest 镜像必须由 distrobuilder 配方产出，且语义为构建一次记录摘要；不得每次 apply 重新烘焙 | 契约 + e2e |
| `INCUS-R-056` | `guest_image` 需要 Provider 把 fingerprint 交回 Runner；由 R-066、R-067 取代——镜像改为 `anas:`/`fingerprint:` 命名引用后，引用在 apply 时即确定，无需结果通道（已废弃） | 契约 + 审阅 |
| `INCUS-R-057` | 一级发行版为 Debian 13、Ubuntu 24.04 LTS 与 Ubuntu 26.04 LTS，其中 26.04 是主要测试环境；其余标记待适配，未适配时相关功能保持关闭而不是失败 | 静态 + e2e |
| `INCUS-R-058` | 特权通道只接受动作 id 与类型化参数，动作实现必须编译进 root 二进制；不得接受调用方提供的命令、argv、路径或脚本，也不得执行位于 anas 可写目录中的文件 | 审阅 + 安全测试 |
| `INCUS-R-059` | 特权通道必须 socket 激活而非常驻 root 进程，校验对端 uid/gid，并逐次审计动作 id、规范化参数、调用方与结果；敏感参数不落日志 | 单元 + 安全测试 |
| `INCUS-R-060` | 每个特权动作必须有对称的撤销动作；`incus.install` 之所以可接受正是因为 `incus.uninstall` 存在 | 审阅 + e2e |
| `INCUS-R-061` | CLI 与 Web 必须共用同一条特权通道与同一份审计记录，能力差异不得来自入口 | 审阅 + e2e |
| `INCUS-R-062` | 必须预留 `network_mode: nat \| lan` 两种网络模式，默认 `nat`；LAN 模式暂不实施，文档须写明它同时移除两个协议族的围栏 | 文档 |
| `INCUS-R-063` | Traefik 绑定必须跟随实例生命周期：启动即绑、停止或暂停即解绑，与实例是否长驻无关；运行时绑定经 Traefik 文件 provider 目录完成，Core 不在这条路径上 | 契约 + e2e |
| `INCUS-R-064` | 可发布的 guest 端口必须在租约的 `ingress.allowed_ports` 中于 apply 时固定；省略 `ingress` 段即完全不允许发布 | 契约 + 单元 |
| `INCUS-R-065` | 随机域名必须由 `workload_id` 与租约 secret 确定性派生，不得抽取随机数再存表：同一任务恒定、不同任务不重复、外部不可预测 | 单元 |
| `INCUS-R-066` | `image_allowlist` 条目必须是 `fingerprint:<64hex>` 或 `anas:<name>@<revision>`；`anas:` 名字一旦发布不得指向不同内容，配方变更产出新 revision | 契约 + 审阅 |
| `INCUS-R-067` | 不得为 `guest_image` 新增 Provider→Runner 结果通道：镜像引用在 apply 时即确定，Provider 只保证该引用对应的镜像存在 | 审阅 |
| `INCUS-R-068` | 特权入口只有两个：`anas-helper`（`CAP_NET_ADMIN`，日常热路径，无产物）与 `anas-hostd`（完整 root，罕见、有产物、逐次审计）。不得为 `CAP_SYS_ADMIN` 单开第三个入口 | 审阅 |
| `INCUS-R-069` | `anas-hostd` 必须与 `anas` 同版本发布并一起升级；不得存在可单独更新的动作目录或可写入的脚本目录 | 静态 + 审阅 |
| `INCUS-R-070` | 入站路径必须与网络模式解耦：LAN 模式若启用也走同一条 proxy device 路径，不得复用 macvlan shim 作为 Traefik 路由目标 | 契约 + 审阅 |
| `INCUS-R-071` | 随机域名派生必须确定性、掺入租约 secret、碰撞时失败而不静默复用；`mode: fixed` 时不派生 | 单元 |
| `INCUS-R-086` | 域名模式必须支持 `fixed` / `named` / `random` 三档；`named` 的 label 只允许 `[a-z0-9-]` 且最终域名必须落在该租约 prefix 的命名空间内 | 契约 + 单元 |
| `INCUS-R-087` | 消费者不得直接写入 Traefik 动态配置目录：只能写受约束的请求文件，由中介校验域名落在本租约命名空间内后渲染；middleware 与 entrypoint 由渲染方决定，消费者无权指定 | 契约 + e2e |
| `INCUS-R-088` | 域名校验必须在消费者进程之外完成——放在共享客户端库不足以约束被攻陷的消费者 | 审阅 + e2e |
| `INCUS-R-072` | 旧 revision 镜像不得在 apply 时自动删除：当前与上一个 deployment 引用的镜像必须保留，清理只能经显式 `incus.image-prune` 动作并先 dry-run | 单元 + e2e |
| `INCUS-R-073` | 特权通道的长时动作必须支持进度回显；调用方断连时的语义必须由与 Module Command 共用的 job 模型定义，不得让宿主通道另造一套 | 契约 + 审阅 |
| `INCUS-R-074` | 非 systemd 发行版上常驻部分必须只做 accept，动作逻辑仍在每请求短命进程中执行；不得改用 setuid 二进制 | 审阅 |
| `INCUS-R-075` | Web 端发起的任务必须留在服务端执行至结束：调用方断连不得中止执行，只有显式 `cancel` 才停止；重新打开控制台必须能看到进行中的 job 及其状态 | 契约 + e2e |
| `INCUS-R-076` | 每次调用都必须创建 job 并写入带序号的追加事件日志，支持从任意位置重放；日志超限必须写入显式 `truncated` 事件而不是静默丢弃 | 单元 |
| `INCUS-R-077` | 取消为协作式；杀进程组后 job 必须标记 `outcome: unknown` 而不是 `cancelled`——动作未确认收敛时不得把未知报成已取消 | 单元 |
| `INCUS-R-078` | 大块数据不得复用 JSONL 控制流：目的地由动作自己校验并打开；需要经浏览器取回时走一次性 token 的独立下载端点，不得内联 base64 | 契约 + 审阅 |
| `INCUS-R-079` | `btrfs send` 的已写字节必须精确；总量估算只在能廉价获得时进行（增量用 `find-new`、已开 quota 用 referenced），不得为估算阻塞传输；估算值放独立字段，实际超出时不得钳制 | 单元 |
| `INCUS-R-080` | `btrfs send` 取消必须清理不完整目的文件，并在确认时明确告知不支持断点续传 | 单元 + e2e |
| `INCUS-R-081` | 重复触发必须按「是否已有参数相同的同名动作在跑」判定，动作声明 `coalesce`/`reject`/`queue`；不得使用时间窗防抖 | 单元 |
| `INCUS-R-082` | job 存储必须单一、不按入口分区：控制台能看到 CLI 发起的 job，反之亦然；`list` 只按查看者权限过滤，不按创建者过滤 | 契约 + e2e |
| `INCUS-R-083` | 批量数据只走「动作自己打开目的地」一条路径：不得提供经浏览器取回的制品下载端点，也不得在控制流内联 base64。将来开放下载端点前必须一并定案有效期、可否重复使用与是否允许非浏览器客户端，且不得使用 URL 内明文 token | 审阅 + 安全测试 |
| `INCUS-R-084` | Compose 的 `additional_contexts` 默认值 `../..` 只在源码 checkout 下正确，从 staging 树构建时够不到仓库根；必须提供可用的覆盖路径并在文档中写明，或改由 Runner 发布源码根 | 静态 + 审阅 |
| `INCUS-R-085` | 「Incus project 没有镜像 allowlist 原生开关」这一判断必须对照 Incus 上游 project 配置参考核实；若存在等价键，镜像约束必须下沉到 project 由 daemon 兜底 | 审阅 |
| `INCUS-R-089` | 确认 token 有效期 5 分钟且一次性消费；`/run/anas/` 只存 token 摘要与被批准动作的摘要，不存 token 本身 | 单元 + 安全测试 |
| `INCUS-R-090` | 确认 token 属于动作 ABI 而非 HTTP 层：CLI 与 Web 的 plan/apply 用同一机制，并留下同一对互相引用的 job 记录 | 契约 + 审阅 |
| `INCUS-R-091` | token 过期后 `apply` 必须重新 plan 并重新展示摘要要求再次确认，不得沿用旧摘要执行；客户端应在用户停留过久时静默换新 token 而不是延长有效期 | 单元 + e2e |
| `INCUS-R-092` | `lease_secret` 必须是独立的一条 Secret Store 条目（32 字节随机），不得并入客户端证书 bundle、也不得由部署级 secret 派生；随租约投影给消费者并标为敏感，resource state 只留引用 | 单元 |
| `INCUS-R-095` | 经 ingress 发布的服务默认必须挂 ForwardAuth；发布无认证服务必须是租约里显式声明的选择。域名不可预测只是纵深防御，不得作为访问控制——SNI 明文、URL 会进 Referer 与日志 | 契约 + e2e |
| `INCUS-R-093` | `idempotency_key` 在 job 到达终态后保留 1 小时，之后同一 key 视为新请求 | 单元 |
| `INCUS-R-094` | 发行版自动安装首批只覆盖 Debian 13、Ubuntu 24.04 LTS、Ubuntu 26.04 LTS；其余标记待适配并作为后续计划，未适配时不自动安装且相关功能保持关闭 | 静态 + e2e |
