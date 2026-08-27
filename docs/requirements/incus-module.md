---
doc_type: requirement
status: current
created: 2026-08-23
updated: 2026-08-23
---

# Incus compute Provider Module 集成要求

本文规定 ANAS 把现有 Incus 适配逻辑提取为独立 `incus` Provider Module 时必须交付的结果、边界和
验收标准。设计背景见 [Forgejo Module 设计](/architecture/forgejo-module-design) §4 与
[AI Agent 编排设计](/architecture/ai-agent-orchestration-design) §5.2；施工顺序见
[Incus compute Provider 实施计划](/plans/incus-module)。Contract 定义以
[`contracts/compute/contract.yml`](https://github.com/anas-project/ANAS/blob/master/contracts/compute/contract.yml)
为准，本文不复制其字段。

关键词“必须”“不得”“应该”具有规范性。

## 1. 目标

`compute` Contract 1.0.0 已定义一次性 VM 的生命周期，但仓库中没有任何 Module 声明
`contracts.provides: compute`；唯一的 Incus 实现内嵌在 Forgejo 的 Actions controller 中。当第二个
消费者（AI Agent 编排器）出现时，内嵌实现会导致同一套 Incus 客户端、证书处理和 janitor 逻辑被复制
两遍，且两个消费者共用同一个 restricted project 的假设会被固化。

因此必须提供独立 `incus` Provider Module：唯一实现 `compute/incus_vm`，为多个消费者提供**互相隔离**
的一次性 VM，并让消费者只通过 Contract 操作使用计算资源。

## 2. 范围

必须包含：

- `incus` Provider Module：manifest、Provider 实现、凭据、双语文档与单元测试；
- 每消费者独立的 restricted project、客户端证书与实例名前缀；
- `compute` 1.0.0 全部七个操作的实现与请求校验；
- Forgejo Actions controller 从内嵌 Incus 客户端迁移到 Contract 消费；
- `contracts/compute` 文档中单消费者假设的修订。

必须包含（第二阶段）：

- `incus_container` interface：非特权 Incus 系统容器（LXC）作为轻量隔离档，供内存受限宿主与
  高频短作业使用。

不要求：

- 由 ANAS 安装、配置或托管 Incus daemon 本身；
- 支持 Incus 以外的虚拟化后端（libvirt、Proxmox、Firecracker）；
- 嵌套虚拟化、设备直通或跨宿主迁移；
- 预热池、自动扩缩容和跨作业复用实例。

## 3. Module 与部署边界

1. Module 名必须为 `incus`，类别为 `compute`；真实 Incus 宿主验收完成前状态必须为 `developing`。
   `compute` 是新增类别，必须同步 Module 分类文档。
2. Module 必须只实现 Incus 的**客户端**控制面。它不得在 ANAS 宿主安装或运行 Incus daemon，不得要求
   ANAS 宿主具备 KVM，也不得挂载宿主虚拟化设备或 Docker socket。
3. Module 不得暴露任何 HTTP 服务、Traefik 路由或宿主端口。它只通过 Contract Provider operation 被
   Runner 调用。
4. Incus endpoint、pinned server certificate、restricted client certificate 与 key 必须由本 Module
   拥有并按敏感配置处理；不得写入 README、`config list` 输出、deployment manifest 或日志。
5. Provider 在任何变更操作前必须验证目标 project 存在、`restricted=true`，且实例数、CPU、内存与磁盘
   配额已设置；任一条件不满足必须 fail closed。

## 4. 多消费者隔离

1. 每个消费者必须绑定独立的 restricted project 与独立的客户端证书；不得让两个消费者共用同一
   project 或同一证书。
2. 每个消费者必须有独立的实例名前缀；`list_managed` 与 janitor 只能枚举和回收调用方自己前缀、
   自己 project 内的实例。
3. 一个消费者的请求不得读取、修改或删除另一个消费者的实例、镜像或 volume；越界请求必须被拒绝并
   记录审计事件。
4. Provider 必须拒绝调用方传入的 Incus device、raw config、挂载、网络或 profile 覆盖；profile、
   network 与 storage pool 归 Provider 所有。
5. `contracts/compute` 现有文档把 restricted project 固定为 `anas-forgejo-runners`，必须修订为
   per-consumer project，且修订不得改变 Contract 的 schema 与操作语义。

## 5. 操作语义

1. `create` 只接受固定 SHA-256 image fingerprint 与数值资源上限；镜像引用不得为 tag、alias 或远程
   URL，超出支持区间的资源上限必须拒绝。
2. `exec_stdin` 必须是唯一把一次性 Secret 送入 guest 的通道。Secret 不得出现在命令参数、环境变量、
   cloud-init、镜像、磁盘状态或日志中。
3. `exec_stdin` 的命令必须按**消费者各自**登记的 guest 入口 allowlist 校验；不得接受任意命令。
4. 全部七个操作必须幂等：重复 `create`/`delete` 不得产生重复实例或把已删除实例报成错误；`inspect`
   对不存在实例必须返回明确的“不存在”结果而不是失败。
5. 操作必须支持超时与取消；调用方取消后，Provider 不得遗留 running 实例。
6. 每次操作必须记录消费者、project、实例 id、镜像 fingerprint 与结果；日志不得包含证书、key 与
   stdin 内容。

## 6. 凭据与轮换

1. 客户端证书与 key 必须是仅绑定到目标 project 的 restricted certificate，不得使用 Incus 全局管理
   凭据。
2. 证书轮换必须可在不销毁运行中实例的前提下完成，或必须明确记录“轮换需要排空实例”的边界。
3. Provider 必须固定（pin）Incus server certificate，不得在 TLS 校验失败时回退到不校验。

## 7. Forgejo 迁移

1. Forgejo Actions controller 必须改为 `compute` Contract 消费者，删除自身的 Incus 客户端实现；
   仓库内不得保留两套 Incus 适配代码。
2. 迁移不得改变 Forgejo 既有的 one-job 行为：ephemeral runner 注册、stdin 注入 runner token、
   单作业后销毁 VM 与 janitor 回收必须保持等价。
3. 迁移不得要求现有部署迁移数据或重建 Incus project；已有 `anas-forgejo-runners` 必须继续可用。
4. 迁移后 Forgejo Module 不得再直接持有 Incus endpoint 与证书配置项，除非它作为 Contract binding
   的一部分由 Runner 注入。

## 7bis. 第二 interface：非特权系统容器

1. `incus_container` 必须在**不改变 `compute` schema 与操作语义**的前提下加入；消费者通过 interface
   选择隔离档，请求字段保持一致。
2. 系统容器实例必须与 VM 实例受同样约束：restricted project、配额、实例名前缀、镜像 fingerprint、
   `exec_stdin` 命令 allowlist 与销毁语义。
3. 容器必须以非特权（user namespace 映射）运行；不得授予宿主设备、宿主挂载或特权模式。
4. 文档必须写明两档的**边界差异**：系统容器与宿主共享内核，VM 提供独立 guest kernel；选择哪一档
   是部署决策，Provider 不得自动降级。

## 8. 验收标准

### 8.1 静态与单元验收

- Module manifest、Provider 声明、schema 与双语文档通过仓库全部 manifest/documentation gate；
- 单元测试覆盖：请求校验（image fingerprint、资源区间、命令 allowlist）、project/前缀隔离、
  幂等、超时取消、敏感值不出现在日志与错误中；
- 契约测试覆盖 `compute` 七个操作的请求/结果 schema；
- Forgejo controller 在迁移后仍通过既有单元测试，且不再引用内嵌 Incus 客户端。

### 8.2 真实部署验收

- 在独立 KVM/Incus 宿主完成 `create → start → exec_stdin → stop → delete` 全流程；
- 两个消费者（Forgejo 与第二消费者）并行运行，验证 project、证书、前缀与 `list_managed` 隔离；
- 验证 restricted 证书无法访问目标 project 之外的资源、无法创建非固定 fingerprint 的实例；
- 验证 Provider crash 或调用方取消后 janitor 能回收残留实例；
- 验证证书轮换流程与其对运行中实例的影响；
- 记录 VM 启动时延、常驻资源与一次典型作业的墙钟耗时，作为容量规划基线。

完成 §8.1 只代表实现进入 `developing`。只有 §8.2 全部有可复现证据才可以把 Module 提升为 `release`。

## 9. 需求矩阵

本矩阵是规范来源，正文是解释；两者冲突时以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `INCUS-R-001` | 必须提供名为 `incus`、类别 `compute` 的 Module，声明 `contracts.provides: compute@1.0.0 / incus_vm`；真实宿主验收完成前保持 `developing` | 静态 |
| `INCUS-R-002` | Module 必须只实现客户端控制面：不安装或运行 Incus daemon，不要求 ANAS 宿主具备 KVM，不挂载宿主虚拟化设备或 Docker socket | 静态 + 审阅 |
| `INCUS-R-003` | Module 不得暴露 HTTP 服务、Traefik 路由或宿主端口，只能通过 Provider operation 被 Runner 调用 | 静态 |
| `INCUS-R-004` | Incus endpoint、pinned server 证书、restricted client 证书与 key 必须按敏感配置处理，不进入 README、`config list`、deployment manifest 或日志 | 单元 |
| `INCUS-R-005` | 任何变更操作前必须验证目标 project 存在、`restricted=true` 且已设置实例/CPU/内存/磁盘配额，否则 fail closed | 单元 |
| `INCUS-R-006` | 每个消费者必须绑定独立 restricted project 与独立客户端证书，不得共用 | 单元 |
| `INCUS-R-007` | 每个消费者必须有独立实例名前缀，`list_managed` 与 janitor 只能枚举/回收本消费者前缀且在本 project 内的实例 | 单元 |
| `INCUS-R-008` | 跨消费者访问（读取、修改、删除对方实例或卷）必须被拒绝并记录审计事件 | 单元 + e2e |
| `INCUS-R-009` | Provider 必须拒绝调用方传入的 device、raw config、挂载、网络或 profile 覆盖 | 单元 |
| `INCUS-R-010` | `contracts/compute` 文档中固定 `anas-forgejo-runners` 的单消费者假设必须修订为 per-consumer project，且不得改变 schema 与操作语义 | 契约 |
| `INCUS-R-011` | `create` 只接受固定 SHA-256 image fingerprint 与受支持区间内的数值资源上限，其余一律拒绝 | 单元 |
| `INCUS-R-012` | `exec_stdin` 必须是唯一 Secret 注入通道，Secret 不得出现在命令参数、环境变量、cloud-init、镜像、磁盘状态或日志 | 单元 |
| `INCUS-R-013` | `exec_stdin` 命令必须按消费者各自登记的 guest 入口 allowlist 校验，不接受任意命令 | 单元 |
| `INCUS-R-014` | 七个操作必须幂等：重复 `create`/`delete` 不产生重复实例或伪失败，`inspect` 对不存在实例返回明确“不存在” | 单元 + 契约 |
| `INCUS-R-015` | 操作必须支持超时与取消，取消后不得遗留 running 实例 | 单元 |
| `INCUS-R-016` | 每次操作必须记录消费者、project、实例 id、镜像 fingerprint 与结果，且日志不含证书、key 与 stdin 内容 | 单元 |
| `INCUS-R-017` | 客户端证书必须是仅绑定目标 project 的 restricted certificate，不得使用 Incus 全局管理凭据 | 单元 + 审阅 |
| `INCUS-R-018` | 必须 pin Incus server 证书，TLS 校验失败时不得回退为不校验 | 单元 |
| `INCUS-R-019` | 证书轮换必须可在不销毁运行中实例的前提下完成，否则必须在文档中明确“轮换需排空实例”的边界 | e2e + 文档 |
| `INCUS-R-020` | Forgejo Actions controller 必须迁移为 `compute` 消费者并删除内嵌 Incus 客户端；仓库不得保留两套适配代码 | 单元 + 审阅 |
| `INCUS-R-021` | 迁移后 Forgejo 的 one-job 行为必须等价：ephemeral 注册、stdin 注入 runner token、作业后销毁与 janitor 回收 | 单元 + e2e |
| `INCUS-R-022` | 迁移不得要求现有部署迁移数据或重建 Incus project；已有 `anas-forgejo-runners` 必须继续可用 | 审阅 |
| `INCUS-R-023` | 真实 KVM/Incus 宿主必须完成 `create → start → exec_stdin → stop → delete` 全流程验收 | e2e |
| `INCUS-R-024` | 真实宿主必须验证两个消费者并行时的 project/证书/前缀/`list_managed` 隔离，以及 restricted 证书无法越出目标 project | e2e |
| `INCUS-R-025` | 真实宿主必须验证 Provider crash 或调用方取消后 janitor 能回收残留实例 | e2e |
| `INCUS-R-026` | 必须记录 VM 启动时延、Provider 常驻资源与一次典型作业墙钟耗时，作为容量规划基线 | e2e |
| `INCUS-R-027` | 必须能在不改变 `compute` schema 与操作语义的前提下提供 `incus_container` interface；系统容器实例受与 VM 相同的 project、配额、前缀、fingerprint 与命令 allowlist 约束，且以非特权方式运行 | 单元 + 契约 |
| `INCUS-R-028` | 真实宿主必须验证系统容器档的 `exec_stdin`、非特权映射、配额与销毁，并与 VM 档记录同一 golden task 的墙钟与内存基线 | e2e |
