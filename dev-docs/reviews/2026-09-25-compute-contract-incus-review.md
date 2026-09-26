# compute Contract 与 Incus 实现审查

状态：审查结论，未修改实现。日期：2026-09-25。基线：`0b614488` 加当前累积工作树（含已暂存与未暂存改动）。

范围：`contracts/compute`、Runner 的 compute 资源路径、`internal/computeclient`、`modules/incus/provisioner`
及其消费者（Forgejo actions-controller、AI Agent orchestrator）。宿主供给、动作通道、入站转发和镜像烘焙只在影响
上述接口时提及，其进度以[实施计划](../plans/incus-module.md)为准。

> [!NOTE]
> **解决状态（2026-09-26）**：1.1 与 1.2 已在 Provider 修复；1.3 以租约归属标记与受限证书独占修复，
> 新增 `INCUS-R-106`，并补上宿主侧 prune/转发退役一直要求、但 Provider 从未写入的 project 标记。
> 均为单元层（fake daemon），细节见[实施计划](../plans/incus-module.md)当前接续状态与 Incus Module
> 技术文档「租约归属」「network 与 profile」节；daemon 的实际拒绝仍待实机验收。其余发现未处理。
> 勘误（2026-09-26）：3.1 建议的 `restricted.images.servers` 不可行——按 7.0.1/7.5.1 源码，它非空时也拒绝
> 从 project 内本地镜像创建实例，且不检查 simplestreams 复制。Provider 现把它作为必须不存在的键，
> 并在 7.x daemon 上按 API extension 写入 `restricted.storage-pools.access` 与 `restricted.virtual-machines.nesting`。
> 实机（2026-09-26）：1.1—1.3 与 7.x 键在 Incus 6.0.5、7.0.1、7.5.1 一次性 VM 上通过，见
> [实机核验](2026-09-26-incus-fence-native-validation.md)。

下文区分三类：**已观察缺陷**（调用链已核对，部分有探针复现）、**设计债**（行为与需求/命名/契约不一致，但不是
立即可利用的错误）、**未验证假设**（需要实机核对）。

## 1. 已观察缺陷

### 1.1 project 围栏只校验枚举键，既有 project 的放宽键会保留且 `inspect` 报 ready（高）

`ensure` 把现有 project 配置与 `projectConfig` 合并（`modules/incus/provisioner/ops.go` 的 `ensure`），
`projectFenceEnforced` 只比对 `projectConfig` 里列出的键。未列出的 `restricted.*` 键保持原值。VM 档还不写
`restricted.containers.privilege`，测试显式断言不写（`provisioner_test.go` 的 VM 档用例）。这与 2026-09-23 修复的
`restricted.devices.proxy` 是同一类问题，只修了那一个键。

探针（临时测试文件，用现有 fake daemon，执行后已删除）：预置 project 带
`restricted.containers.privilege=allow`、`restricted.containers.interception=allow`、`restricted.idmap.uid=0-65535`、
`restricted.cluster.target=allow`，VM 档 `ensure` 成功，结果 `ready=true`，四个放宽值原样保留，
`limits.containers` 为空。

触发路径是真实的：R-028 要求沿用旧 `anas-forgejo-runners`，而 Provider 会采纳任何同名已有 project（见 1.3）。
未列出的键至少包括 `restricted.containers.privilege`（VM 档）、`restricted.containers.interception`、
`restricted.idmap.uid/gid`、`restricted.devices.disk.paths`、`restricted.devices.infiniband`、
`restricted.devices.unix-hotplug`、`restricted.backups`、`restricted.snapshots`、`restricted.cluster.target`、
`restricted.networks.subnets/uplinks/zones`。

建议：把 `restricted.*` 作为显式全集写入并读回；遇到未知的 `restricted.*` 键失败关闭；VM 档同样写 `unprivileged`。

### 1.2 隔离档不由 daemon 强制（中高）

VM 档 project 没有设置 `limits.containers=0`，容器档也没有 `limits.virtual-machines=0`（全仓库无这两个键）。
持有 VM 租约的受限证书可以直接创建系统容器（与宿主共享内核），唯一约束是共享客户端 `Create` 里的 `--vm`。
这与 R-008（隔离由 daemon 执行，不依赖消费者自觉）和 R-052/R-036（档位不得降级）冲突。结合 1.1，
沿用旧 project 时 VM 租约下甚至可能创建特权容器。

代码层已确认未设置；`limits.containers=0` 在目标 Incus 版本上的实际拒绝效果需实机验证。

### 1.3 project 没有归属标记，租约身份不含部署/工作区（中）

`sandbox` 与 `instance_prefix` 写死在消费者 `module.yml`（Forgejo 为 `anas-forgejo-runners` / `anas-forgejo-`），
`NetworkName` 只由 sandbox 派生，网络归属只比对 `user.anas.consumer` 与 `user.anas.sandbox`；project 本身没有
任何 `user.anas.*` 标记，`ensure` 会接管任何同名 project。Core 的 sandbox 唯一性只在单个部署内检查。

失败场景：两个 ANAS 工作区（测试与生产，或同机多实例）指向同一 Incus daemon 时，第二个工作区无声采纳同一
project 与网络，并把自己的证书也限定到该 project。两边 Forgejo controller 的 `CleanupAll`
（`modules/forgejo/actions-controller/controller.go`）会把对方正在运行的 job 实例当作孤儿删除。这违反 R-006
（每个消费者独立 project 与证书）。

建议：project 写入归属标记（至少含工作区身份与 consumer/resource），`ensure` 只采纳带匹配标记的 project，
旧 `anas-forgejo-runners` 走一次显式迁移；或由 Core 把工作区身份并入 sandbox/prefix 派生。

### 1.4 租约没有结束路径（中）

- Runner 只调用 `ensure`（`internal/runner/resources.go`）；`inspect`、`revoke` 在 Core 中没有调用方，Incus Module
  也没有 Module Command。需求 §1 表中「围栏运维 / Module Command / 管理员」这一层实际不存在。
- 消费者移除或被 `enabled_by` 关闭时，`retainRemovedResources` 只把状态文件改成 `retained`，不调用 Provider。
  daemon 上的受限证书继续受信，实例与网络继续存在。对数据库和桶，retain 是保留数据；对 compute，它保留的是
  访问授权。
- `deletion_policy: delete` 通过校验但在任何路径上都不起作用。
- CRED-R-006 的 overlap 轮换未实现（凭据轮换要求 0/18），轮换后没有撤销旧证书的步骤。

建议：consumer 移除/关闭时调用 `revoke`；明确 compute 的 `delete` 语义或暂时拒绝该值；补 `inspect` 的调用入口。

## 2. Contract 设计问题

### 2.1 Contract 文档不是实际接口（中）

- Contract 的 request/result schema 只检查文件存在（`internal/runner/contracts.go`），从不用于校验。
- `ensure` 声明 `result_schema: sandbox-result.yml`（要求 endpoint、profile、证书指纹等），Provider 实际输出
  inspect-result 形状 `{exists, ready, restricted, quota_enforced}`；Runner 丢弃 stdout，只看退出码。
- 真实接口是三组没有在 Contract 中声明的环境变量：
  1. Runner→Provider：`ANAS_RESOURCE_*`；
  2. Runner 从 Provider 配置读取：`<PROVIDER>_ENDPOINT`、`<PROVIDER>_SERVER_CERT_B64`、
     `<PROVIDER>_CONTROL_NETWORK_NAME`，前缀由 Provider 模块名拼出，其中 `INCUS_SERVER_CERT_B64` 依赖 Incus Hook
     里的别名赋值；
  3. Runner→消费者：`ANAS_COMPUTE_RESOURCE__*`。
- 同一组约束在 schema、`internal/runner/compute.go`、`internal/computeclient/lease.go`、
  `modules/incus/provisioner/main.go`、`internal/deployment/compute_authorization.go` 各写一遍，已出现漂移：
  `sandbox: default` 能通过 Core 校验，宿主侧 reader 拒绝它，Provider 只因 `features.networks` 检查而碰巧拒绝。

建议：要么让 Runner 真正按 schema 校验 Provider 输出并用它生成租约，要么删掉不生效的 result schema，并把
Provider 必须导出的配置键写进 Contract。

### 2.2 `compute` 名义通用，实为 Incus 租约（设计债）

interface 名是 `incus_vm` / `incus_container`；结果字段是 Incus 的 endpoint、project、profile、服务端证书；
消费者库直接执行 `incus` CLI。需求明确不支持其他后端，所以这不是缺陷，但名字承诺了不存在的可替换性：将来换
后端只能新开 Contract 或做破坏性 v2。另外绑定按 `bindings["compute.interface"]` 记录，一个消费者的所有 compute
资源只能用同一档位。

建议：在 Contract 文档中明说它是 Incus 租约，或把 Incus 专有字段收进 Provider 专属载荷。

### 2.3 版本号失去兼容性含义（低）

Contract 经历两次不兼容改形（7 个实例操作→租约；`image_allowlist` 字符串→对象），版本始终是 `1.0.0`，
R-001 也钉在 `compute@1.0.0`。Module 处于 developing 阶段，这可以接受，但进入 release 前需要确定版本策略。

## 3. 共享客户端（消费者侧接口）

### 3.1 镜像 allowlist 只在消费者进程内执行（中，已知）

project 开启 `features.images=true`，并且没有设置 `restricted.images.servers`。被攻陷的消费者可以用自己的受限证书
拉取或导入任意镜像再启动，fingerprint allowlist 只存在于 `computeclient.Validate`。文档已承认，R-085 未关闭。
可以先用 `restricted.images.servers` 堵住公共远端，作为纵深防御（具体语义需实机确认）。profile 上的 NIC
过滤同样可以被实例级覆盖，这部分已由宿主授权设计（R-102）承担。

### 3.2 通过执行 `incus` CLI 驱动实例（低—中）

每个消费者镜像都要自带 incus CLI，Forgejo controller 在构建时从网络 `go install` incus v7.3.0，客户端 CLI 与
daemon 版本耦合。错误只保留 `incus <cmd> failed: exit status N`（刻意不回显 stderr），排障成本高。仓库里目前有
三套 Incus 访问实现：Provider 的 REST 客户端、`internal/incusprovision` 的 REST 客户端、`computeclient` 的 CLI。

### 3.3 workload ID 规则不一致（低）

`Create` 接受不超过 128 字节的任意非控制字符；HTTP 发布请求要求 `^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`。能创建的
实例不一定能发布。

## 4. 未验证假设

- **配额与镜像共用 `limits.disk`**：project 总量等于单实例配额 × `max_instances`，而 `features.images=true` 时
  镜像也存放在该 project。若 Incus 的 `limits.disk` 计入 project 镜像（上游配置参考的表述如此，未在目标版本
  实测），再加上 R-072 保留上一 revision，满配 `max_instances` 时最后一个实例会被拒。
- **无宿主容量约束**：各租约总量之和不与宿主容量比较；AI Agent 单个租约就声明 32 vCPU / 64 GiB / 320 GiB。

## 5. 计划中已知的未完成范围（不是新发现）

- VM 档、ARM64、ZFS、双栈未实机验收；Module 仍为 `developing`。`modules/incus/module.yml` 注释仍写「未对真实
  daemon 验证」，与计划中默认容器档的实机证据不符，已过时。
- 生产入站关闭；租约转发被 `forwarding_lifecycle_integration_unavailable` 门禁阻止；受信生产镜像目录为空；
  正式签名发布、回滚与破坏性 prune 未验收；长驻实例档（M8）未开始。
- 第二个消费者 AI Agent 只解析租约（`orchestrator/config.go`），镜像中没有 incus CLI，也不驱动实例。共享库
  目前只有 Forgejo 一个真实用户。

## 6. 验证

- 在当前工作树（macOS）运行 `go test ./internal/computeclient/... ./internal/computeimage/...
  ./internal/computeingress/... ./modules/incus/...` 及 `go test ./internal/runner/ -run 'Compute|compute'`，全部通过。
- 1.1 的探针用临时测试文件复现，执行后已删除，未留在工作树。
- 未运行 Linux 原生测试、未使用真实 Incus daemon。1.2、3.1 与第 4 节中关于 daemon 行为的判断需要实机核对。

## 7. 建议顺序

1. 补全 project 围栏（1.1、1.2）：改动小，直接收紧已声明的安全边界。
2. 加 project 归属标记和工作区身份（1.3），并给旧 `anas-forgejo-runners` 一次显式迁移。
3. 接上 `revoke` 与移除/关闭流程，确定 compute 的 `deletion_policy` 语义（1.4）。
4. 决定 Contract 的约束方式：按 schema 校验 Provider 输出，或删掉不生效的声明，并声明 Provider 配置键（2.1）。
