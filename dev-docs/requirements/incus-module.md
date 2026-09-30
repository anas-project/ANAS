---
doc_type: requirement
status: current
created: 2026-08-23
updated: 2026-09-30
---

# Incus compute Provider Module 集成要求

本文规定 ANAS 把 Incus 适配能力落成 `compute` Contract Provider 时必须交付的结果、边界和验收
标准。设计背景见 [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../modules/ai_agent/docs/architecture/orchestration-design.md) §5.2；施工顺序见
[Incus compute Provider 实施计划](../plans/incus-module.md)。Contract 定义以
[`contracts/compute/contract.yml`](https://github.com/anas-project/ANAS/blob/master/contracts/compute/contract.yml)
为准，本文不复制其字段。

关键词“必须”“不得”“应该”具有规范性。本文包含目标要求，不能把矩阵存在视为功能已实现。
当前声明使用结构化镜像对象，Core 在 deployment 准备阶段冻结解析结果，运行时仍传裸摘要。
独立租约命名密钥已接入 Secret Store、敏感投影和部署引用；命名算法已编码，专属轮换和生产发布尚未完成。
受信生产镜像目录当前为空。宿主供给与显式镜像清理已有实现代码，但不代表默认供给、正式镜像分发或真实烘焙
已经验收。入站按 §7septies 改为租约 ACL 规则与 anasd 内的无特权中介，尚未实现，旧的逐发布入站代码待删除。
实施状态及真实验收证据以配套计划为准。

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

运行期软件安装与 guest 发布烘焙分别使用 `CHINESE_SPEEDUP` 和 `CHINESE_BUILD_SPEEDUP`。
国内镜像仅改变发行版包的下载位置，仍核验发行版签名，Incus 的固定上游与版本策略不变。
宿主源选择在计划中确认，guest 源选择属于不可变配方，以避免执行环境改变已经批准或归档的输入。

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
- **用户主机实例**（2026-09-28 操作者方向）：用户在控制台手动创建 Incus 虚拟机，完全当成一台主机使用。它建立在
  长驻实例档之上，放在一份只允许 VM 的用户专属租约里，由 ANAS 代用户持有证书；另需持久磁盘、槽位（槽位名就是
  实例名）、端口绑定与 HTTP 发布（§7septies），以及可选的 LAN 模式（`INCUS-R-062`）。创建流程、控制台访问与 LAN
  模式细节在实施前另定需求。

必须包含（第四阶段，设计见 [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)）：

- 宿主 Incus daemon 的自动安装与配置：一次性、幂等、可跳过、有卸载路径；
- guest 镜像的自动产出：distrobuilder 配方，构建一次记录摘要。

不要求：

- 支持 Incus 以外的虚拟化后端（libvirt、Proxmox、Firecracker）；
- 嵌套虚拟化、设备直通或跨宿主迁移；
- 预热池、自动扩缩容和跨作业复用实例；
- 让 ANAS Core 参与单个实例的创建、执行或销毁。

此处「嵌套虚拟化」指 guest 内再次运行虚拟机／hypervisor，不是非特权系统容器中 OCI
runtime 使用的用户、挂载和进程 namespace。后者属于既定 one-job 容器执行范围。其能力由
Provider 的固定 profile 决定，不成为消费者可传入的低层配置；R-011 对宿主设备、宿主路径
挂载和 raw 配置的禁令，以及 R-035 的外层非特权约束仍然适用。namespace 嵌套本身不证明
内层引擎为 rootless，必须另验实际服务身份与运行行为。

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
7. sandbox 名写在消费者 manifest 里，在每个工作区都相同，不能证明归属。多个 ANAS 工作区可以
   连到同一 daemon，因此 Core 的单部署内 sandbox 查重不够：Provider 必须用写在 project 与 bridge
   上的租约归属标记，以及 daemon 信任库中的受限证书，拒绝接管其他租约的 project。

## 5. Contract operation 语义

1. `ensure` 必须幂等：重复调用不得产生第二个 project 或第二条 trust 条目，且必须合并而非覆盖
   project 上与本 Contract 无关的既有配置键。
2. `inspect` 必须只读，并分别报告 `exists`、`ready`、`restricted` 与 `quota_enforced`；project
   不存在必须返回明确的“不存在”结果而不是失败。
3. `revoke` 必须撤销消费者证书并保留 project；对不存在的证书必须幂等成功。
4. Provider 必须固定（pin）Incus server certificate，不得在 TLS 校验失败时回退到不校验。
5. 每次操作的错误必须可归因到具体参数，且不得回显证书、私钥或消费者 Secret。
6. 目标部署不再声明某份租约（消费者被移除，或申请它的能力被关闭）时，Core 必须经上一个部署冻结的
   Provider 执行 `revoke`，撤销证书并保留 project 与实例。保留数据库保留的是数据，保留租约若不撤销，
   保留的是访问授权。撤销未确认时不得记为完成：默认中止激活，只有显式风险确认才可记录为未确认。
   `deletion_policy` 只接受 `retain`，因为没有 Provider 实现删除租约 project。

## 6. Core 与凭据

1. Core 必须为每个 `compute` Resource 生成稳定的客户端证书与私钥，并作为单条 Secret 保存；重复
   apply 不得重新签发，否则会作废 daemon 上已登记的 trust 条目。
2. 只有证书半边可以进入 Provider；私钥必须直接投影给消费者，不得经过 Provider。
3. Core 必须校验 `sandbox`、`instance_prefix`、`quota` 区间与镜像引用语法。声明层接受
   互斥的 `{fingerprint: <64hex>}` 或 `{catalog: anas, name: <name>, revision: <revision>}`；运行时租约中的 allowlist 必须
   归一为 SHA-256 fingerprint，拒绝未解析的名字、可变 tag、alias 与远程 URL。
   不兼容字符串声明（包括裸摘要及 `anas:`/`fingerprint:` 前缀形式）；字段混用、缺失或未知字段必须拒绝。运行时解析结果仍为裸摘要。
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
4. 稳定入站地址必须经受管 network 的显式端口/转发声明表达，不得让消费者直接持有宿主端口；端口绑定见 §7septies。
5. 长驻实例不改变配额语义：它照样占 `limits.instances`，且不得绕开 project 配额。
6. 本档不得削弱已有边界——受限证书、project 隔离、镜像 fingerprint allowlist 与非特权容器约束
   在长驻档下必须同样成立。

## 7quater. 预留：image_policy

`image_policy` 已作为 schema 字段存在，取值 `pinned`（默认）与 `any`。**当前实现只接受 `pinned`，
`any` 一律拒绝**。预留的边界：

1. `any` 必须是消费者 manifest 里显式写下的值，且出现在 `config list` 与审计中；不得由
   `image_allowlist` 里的通配符或空列表隐式打开。
2. `any` 不得用于状态为 `release` 的 Module。
3. 启用 `any` 之后不再限定镜像摘要，因此文档必须写明该租约的镜像来源等同于消费者代码的
   可信度。daemon 的镜像服务器限制与逐摘要 allowlist 不是同一能力，不能相互替代；上游核验
   与适用版本见宿主供给设计及 R-085。
4. `any` 不得连带放宽任何其他约束：project 隔离、配额、实例名前缀与非特权容器仍然成立。

## 7quinquies. 宿主供给、入站与镜像烘焙（第四阶段）

设计见 [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)。

1. 宿主供给必须一次性、幂等、可跳过、可卸载。装不上时不得让整个部署失败：没有 daemon 就没有
   `compute` provider，声明了 `enabled_by` 的消费者保持关闭。
2. 发行版到安装步骤的映射必须是**声明式的表**，不得在代码里按发行版名写分支；新增一个发行版
   应当是加一行数据。
3. Incus 本身统一取自 Incus 7.0 LTS：一级发行版官方仓库最多只有 6.0，缺少围栏依赖的 7.0 项目
   限制。唯一允许的 Incus 包来源是上游维护者的 Zabbly `lts-7.0` 仓库，签名密钥编译进二进制并
   固定指纹，`incus`/`incus-base`/`incus-client` 钉死到该来源、禁止回落到发行版的旧版本；其余依赖
   仍只取自发行版官方仓库或保留发行版签名的固定国内镜像，不得添加其他第三方软件仓库。安装计划必须写明该来源；取不到时失败并给出
   手工指引，不静默降级到发行版 6.0。
4. daemon 默认只监听回环。对外暴露 Incus API 是一次独立的加固决策，不得作为本流程的副产物。
5. **Web 管理端不得接受 root 或 sudo 密码。** 安装期那一次 `sudo` 之后，特权动作必须经
   [宿主特权动作通道](../../docs/architecture/host-action-channel.md) 触发：通道只接受动作 id 与
   类型化参数，永不接受命令、argv、路径或脚本，动作实现编译进 root 二进制。通道实现之前，控制台
   显示待执行命令并轮询状态。
5bis. CLI 与 Web 必须走同一条特权通道：能力差异只可能来自动作清单，不得来自入口。
6. 实例不得依赖公网 IPv6 地址或上游 DHCPv6-PD。出站经受管 bridge NAT；入站由 Traefik 提供 IPv4/IPv6 入口，经租约网桥 ACL 访问
   guest 的内网地址和获批端口，不要求 proxy device 或 network forward，规则见 §7septies。
7. 实例内 HTTP 服务经 Traefik 发布必须复用既有的 `ANAS_TRAEFIK_ROUTE__*` 文件 provider 路由，
   不得为此在 Traefik 侧新增机制。
8. guest 镜像配方必须使用 distrobuilder，不得自造 Dockerfile 方言。
9. 镜像语义必须是**构建一次、记录摘要、之后不再重建**：apply 时摘要已存在即不动作；配方变更是
   显式版本变更，产出新摘要而不是就地覆盖。每次 apply 重新烘焙会让 fingerprint 钉死失去意义。

入站发布分两种：经 Traefik 的 HTTP 发布，与四层原样转发的端口绑定，见 §7septies。Traefik 的 TCP/UDP 路由
与按 SNI 分流的 TLS TCP 不在范围内，不因 Traefik 支持而隐式启用。

## 7sexies. 租约出站分级与租约网络视图（2026-09-28）

默认 Docker 会把宿主 FORWARD 链的默认策略设为 DROP，租约里的实例因此出不了自己的网桥。原方案
（`INCUS-R-101`—`R-104`）逐实例授权，许可 30 秒过期，依靠常驻 owner 续期。同一租约的实例属于同一个消费者，
权限本来就相同，这套逐实例的身份证明与续期代价过高。因此改为按租约的静态出站策略，面向所有 compute 消费者，
不只是 Forgejo。

1. **档位**：由申请实例的 Module 在租约声明中选择，随部署冻结，默认 `internet`。

   | 档位 | 允许 | 仍然拒绝 |
   | --- | --- | --- |
   | `internet`（默认） | 公网 | 局域网、宿主地址（含 Docker 发布到宿主的端口）、其他租约、Docker 网络、链路本地地址 |
   | `internet_lan` | 公网、局域网 | 宿主地址、其他租约、Docker 网络、链路本地地址 |
   | `internet_lan_host` | 公网、局域网、宿主端口与 Docker 发布端口 | 未发布的容器端口、其他租约、链路本地地址 |
   | `modules_only` | 只能经 Traefik 访问 ANAS 的 Module | 其余全部出站 |

2. **开关**：
   - `module_access`：默认关闭。打开后，`internet` 与 `internet_lan` 档也能经 Traefik 访问 ANAS 的 Module。
   - `intra_lease`：默认关闭，即同一租约内的实例互相隔离。one-job 这类场景下，同时运行的作业不应互相访问。
3. **局域网正向定义**：宿主默认路由所在网卡的直连网段，加上操作者配置的附加网段；每次 apply 时重新计算。
   不采用「全部私有地址减去 Docker 与 Incus 网段」：之后新建的 Docker 网络不在排除清单里，会被当成局域网放行。
4. **Traefik 地址清单**：
   - 为什么需要：guest 用域名访问 Module 时，Docker 会先把目的地址改写（DNAT）成 Traefik 容器的地址，Incus ACL
     看到的是改写之后的地址。所以放行「经 Traefik 访问」，就要放行 Traefik 容器当前的地址。
   - 怎么维护（2026-09-30 定）：Traefik 地址不固定。清单是宿主上一份全局的 address set，只由 hostd 的边界内
     同步动作写入，与端口表同步用同一套机制：操作者在 `incus.configure` 时批准一次；之后 apply 启动 Traefik 后、
     Traefik 容器自行重启后、anasd 启动时，由 anasd 触发同步，hostd 自己从 Docker 读取 Traefik 容器的地址。
     anasd 只触发，不提供地址；消费者也不能提供。
   - 不由 anasd 调 Provider 更新：那样 Traefik 每重启一次，anasd 就要在无人操作时拿 Incus 管理证书运行 Provider，
     与 apply 争工作区锁，compute 契约还要为此加一个每个 Provider 都得实现的运行时操作。
5. **执行**：
   - Provider 把档位、开关与局域网清单写进租约网桥的 Incus network ACL 和 address set，与来源围栏共用同一份 ACL；
     Traefik 地址清单只按名字引用；
   - 宿主侧只有一条随 Incus 宿主配置安装的静态转发规则，把租约网桥的转发交给 ACL 决定，不按实例或按租约增删；
   - 租约网桥改用不以 `anas` 开头的专用前缀，因为 anas-helper 能操作所有 `anas*` 接口；
   - 租约撤销时，由 Provider 在 `INCUS-R-111` 的路径中关闭该租约的出站、结束已建立的连接，CLI 与控制台部署
     都能覆盖。
6. **租约网络视图**：控制台用只读图形展示每个租约的网段、档位、开关、实例，以及允许和拒绝的方向，
   调研见[网络可视化调研](../../docs/research/incus-network-visualization-research.md)。

否决的方案：

- 逐实例许可加期限续期：需要常驻 owner，部署之后无法续接，实例一增减就会断开同租约其他作业的连接；
- 事件驱动的逐实例许可：事件监听需要持有 Incus 管理凭据，anasd 停机期间发生的撤销会延后生效；
- 固定 Traefik 地址：操作者要求 Traefik 地址保持不固定。

Forgejo 官方安全指南建议隔离局域网与宿主，没有要求限制公网访问，对应 `internet` 档加 `module_access`。
设计过程见[出站分级草案](../reviews/2026-09-28-compute-egress-tiers-draft.md)。

## 7septies. 租约入站：档位、两种发布与多租约规则（2026-09-28，2026-09-29 定稿）

原方案把每次发布当成对某个实例某一次运行的授权：宿主上逐次发布 nft 许可，30 秒过期，由常驻 owner 核验实例身份后
续期。期限防的是中介失联时，旧许可指向复用了该 IP 的新实例。按租约信任之后（前提与 §7sexies 相同），消费者本来就
持有租约证书、能控制租约里的全部实例，同一租约内「指错实例」挡不住它任何原本做不到的事，逐发布许可、期限和续期
owner 都不再需要。设计过程见[入站简化草案](../reviews/2026-09-28-compute-ingress-simplification-draft.md)；端口绑定选用
Docker 式规则的依据见[proxy NAT 实机探测](../reviews/2026-09-29-incus-proxy-nat-wildcard-probe.md)。

### 单租约

1. **入站档位**：由申请实例的 Module 在租约声明中选择，随部署冻结，默认 `none`。

   | 档位 | 允许主动连入 | 典型用途 |
   | --- | --- | --- |
   | `none`（默认） | 无 | 一次性作业，例如 Forgejo runner |
   | `published` | 只有已声明的发布：Traefik 到 HTTP 发布端口，端口绑定到槽位 | 经域名或宿主端口对外提供服务 |

   - 所有档位下，实例主动出站的回包、本租约网关上的 DHCP 与 DNS 都不受影响。
   - 不设「全部端口对外开放」的档位：NAT 网桥外面没人能路由进来，开放了也用不上。实例需要像局域网里的一台主机那样
     被访问时，用预留的 LAN 模式（`INCUS-R-062`）。
   - 不按「局域网来源」设档：宿主在 NAT 之后时，路由器转发或回环进来的流量可能带着局域网地址，来源地址区分不了
     局域网和互联网。
   - 同一租约内实例之间的互访由 `intra_lease` 决定（§7sexies），不属于入站档位。
2. **发布**：分两种，语义不同，分开声明，可以同时使用；只有 `published` 档能声明发布。

   | | HTTP 发布（`publish.http`） | 端口绑定（`publish.ports`） |
   | --- | --- | --- |
   | 层次 | 七层反向代理：Traefik 终止 HTTPS，以 HTTP 转给 guest | 四层：TCP 或 UDP 报文原样转发 |
   | 入口 | Traefik 的 HTTPS 入口，按域名分流 | 独占一个宿主端口，宿主所有地址都能访问：显式指定，或写 `auto` 从操作者确认过的范围随机分配 |
   | apply 时冻结 | guest 端口、认证方式、域名模式与前缀（`INCUS-R-064`、`R-086`、`R-095`） | 协议、宿主端口、槽位、guest 端口 |
   | 运行时由消费者决定 | 由哪个实例承接，named 模式的 label | 无：槽位在声明里写死实例名 |
   | 认证 | `none` 或 `forward_auth` | ANAS 不提供，guest 服务自行认证 |
   | guest 看到的来源 | 租约网桥网关，真实客户端在 `X-Forwarded-For` | 真实客户端地址 |
   | IPv6 | Traefik 在两个协议族上接入，后端用 IPv4 | 租约启用 IPv6 时，经宿主 IPv6 地址同样可用，转到槽位的固定 IPv6 |
   | 适用实例 | 一次性与长驻实例 | 名称固定的实例：Module 的长驻实例、用户主机实例 |
   | 由谁生效 | anasd 内的发布中介写 Traefik 路由文件 | hostd 在 apply 时同步宿主上的端口表 |

   - 同一域名同一时刻只指向一个实例；一个宿主端口只指向一个槽位。
3. **槽位**：端口绑定的目标。
   - 租约在声明里列出槽位，每个槽位写死一个实例名。Provider 为槽位预留一个不在 DHCP 动态范围内的固定 IPv4；租约
     启用 IPv6 时再预留一个固定 IPv6（租约网段内的内网地址，网桥开启有状态 DHCPv6）。地址经 `ensure` 结果交回，
     跨 apply 不变，槽位不再声明时释放。
   - 共享客户端创建这个名称的实例时使用槽位地址：复制 profile 的网卡再加上地址，防冒用过滤保持不变。同名实例重建
     后拿回同一地址，端口绑定不用改。
   - 一个槽位同一时刻只有一台实例，可以承载任意多个端口绑定。名称随机的一次性实例不占用槽位（2026-09-30 定）：
     需要入站时用 HTTP 发布。
4. **执行**：
   - 租约网桥默认拒绝入站。Provider 把档位与发布写进 §7sexies 的那份 ACL：`published` 放行 Traefik 地址清单到
     HTTP 发布端口，以及其他租约以外的任意来源到槽位地址上的端口绑定端口。
   - HTTP 发布：消费者只写本租约的请求目录，请求里给出承接实例的地址和 guest 端口。发布中介在 anasd 进程内运行，
     校验地址在本租约网段内、端口已声明、域名落在本租约命名空间，再写出 Traefik 路由文件。中介不持有凭据或
     Secret，不调用宿主特权动作，随时可以由冻结授权与请求文件全量重算。共享客户端停止或删除实例时撤销它的发布，
     janitor 清理孤立请求。部署改变或撤销授权时，Core 在 apply 中删除受影响租约的路由文件，不依赖中介是否在线；
     中介停机时，已发布的路由继续可用，新发布等待。
   - 端口绑定：ANAS 在宿主上装一条固定的 Docker 式规则链，只接管目的地址是本机的流量（`fib daddr type local`），
     按端口表把它改写到槽位地址。宿主外连与经宿主转发的流量不受影响，宿主之后新增的地址自动覆盖。端口表在 apply
     时由 hostd 同步：操作者在 `incus.configure` 时确认一次可用端口范围，范围内的同步不再逐次确认，hostd 逐条校验
     目标与端口，范围外一律拒绝（`HOSTACT-R-014`、`R-015`）。可用端口范围默认 30000–32767：低于 Linux 默认的临时
     端口范围（32768–60999），与 Kubernetes NodePort 的惯例相同；操作者可在 `incus.configure` 时修改。宿主端口写
     `auto` 时，Core 从这个范围里随机选一个空闲端口，记入部署状态并保持不变。宿主没有可用的 hostd（未以系统服务
     安装、非 systemd 发行版或开发构建）时，声明了端口绑定的部署在 apply 时失败，不跳过绑定。每个生效的绑定由 systemd 套接字占住宿主端口，之后在同一端口绑定的宿主服务
     或 Docker 发布会直接失败，而不是被静默遮住。运行时不增删绑定，也没有 owner。
   - 冲突与报错：显式端口在 apply 时已被占用，apply 失败并记录运行问题；`auto` 跳过被占的端口。开机恢复时先建
     占位再恢复转发，端口已被其他进程占住的绑定不生效，并记录运行问题。anasd 定期并在 Docker 事件时检查已生效的
     绑定，发现冲突、占位丢失或规则漂移时记录运行问题。记录与查看方式见[运行问题记录要求](runtime-issues.md)。
   - 宿主侧规则只有两类：`INCUS-R-126` 那条静态转发规则（对进出租约网桥两个方向都把决定交给 ACL），以及端口绑定的
     规则链、端口表与端口占位。

### 多租约

5. 不同租约的实例之间不能直接连接，不论档位，也不论是否属于同一消费者。出站各档已经拒绝其他租约；端口绑定也不对
   其他租约开放：改写后的目的地址落在其他租约的网段，访问方的出站 ACL 会拒绝。
6. 跨租约访问只能经 Traefik 的 HTTP 发布：访问方与访问 ANAS Module 一样，要靠出站档位或 `module_access` 到达
   Traefik。
7. 共享资源在租约之间唯一：
   - 「协议 + 宿主端口」在宿主上只属于一个租约，也不能与 Traefik 已有入口、其他 Module 发布的宿主端口，或宿主上
     已有进程监听的端口重复；Docker 式规则按端口区分，多个租约可以各自在宿主所有地址上绑定不同端口；
   - HTTP 域名命名空间在租约之间不重叠，也不覆盖部署中已有服务的域名；
   - 每个租约有自己的网桥和网段，槽位地址只在本租约网段内。
8. 撤销一个租约或改变它的授权，只影响这个租约自己的 ACL 放行、路由文件与端口绑定。

否决的方案：

- 逐发布许可加期限续期：需要常驻受信 owner，还要为可靠撤销引入回执、墓碑与部署前排空；
- 保留逐发布许可、去掉期限：失去失联兜底，却仍要常驻 owner 逐实例增删许可；
- 中介用租约证书向 Incus 核验实例与地址：消费者本来就持有同一张证书，这项核验挡不住任何它原本做不到的事；
- Traefik 的 TCP/UDP 入口：四层流量经用户态代理，guest 看不到客户端地址，入口增删还要重启 Traefik；
- Incus network forward：只能监听具体地址，宿主新增地址要重新 apply；一个监听地址只归一个网络，多个租约不能共用
  宿主地址（维护者说明与 7.0.1—7.5.1 源码见宿主供给设计 §5.1.8）；
- Incus proxy 设备的 NAT 模式：要在租约 project 上放开 proxy 设备，容器就能建非 NAT 代理、连接宿主套接字；通配
  监听只匹配端口、不匹配目的地址，宿主外连和经宿主转发的同端口流量都会被转进实例（已实测）；
- 端口绑定不用固定地址、在实例重建后把转发改指新地址：运行时改端口表需要常驻的特权组件。固定地址让规则一直有效，
  同名实例重建后拿回同一地址，不需要重绑；
- `internet`（全部端口开放）档：NAT 网桥外面没人能路由进来；需要完全对外开放时用 LAN 模式；
- 按局域网来源设档：NAT 之后来源地址不可靠，见第 1 条；
- 租约之间对等直连：需要双方声明并维护彼此的地址清单，跨租约访问改走 Traefik 的 HTTP 发布。

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
| `INCUS-R-019` | Core 必须校验 sandbox、instance_prefix、quota 区间及镜像引用；声明语法见 R-066，运行时 allowlist 只接受解析后的 SHA-256 fingerprint | 单元 |
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
| `INCUS-R-049` | 发行版到安装步骤的映射与软件源要求；由 `INCUS-R-107`、`INCUS-R-108` 取代——一级发行版改为默认从固定的 Zabbly `lts-7.0` 安装 Incus 7.0 LTS（已废弃） | 静态 + 审阅 |
| `INCUS-R-050` | daemon 默认只监听回环；对外暴露 Incus API 不得作为宿主供给的副产物发生 | 静态 + e2e |
| `INCUS-R-051` | Web 管理端不得接受 root 或 sudo 密码；一键提权只能经安装期预置的受限特权单元触发，密码不得进入应用 | 审阅 + 安全测试 |
| `INCUS-R-052` | 默认隔离档必须是 `incus_container`；不得因宿主缺少 KVM 而自动降级，也不得自动升级为 VM | 单元 + 契约 |
| `INCUS-R-053` | 实例不得依赖公网 IPv6 或上游 DHCPv6-PD；HTTP 发布必须经 Traefik（公网为 HTTPS）与租约网桥 ACL 到达获批 guest 端口，公网入口同时支持 IPv4 与 IPv6，guest 不必双栈 | 单元 + e2e |
| `INCUS-R-054` | 实例内 HTTP 服务经 Traefik 发布必须复用既有 `ANAS_TRAEFIK_ROUTE__*` 机制，不得在 Traefik 侧新增能力 | 契约 + e2e |
| `INCUS-R-055` | guest 镜像必须由 distrobuilder 配方产出，且语义为构建一次记录摘要；不得每次 apply 重新烘焙 | 契约 + e2e |
| `INCUS-R-056` | `guest_image` 需要 Provider 把 fingerprint 交回 Runner；由 R-066、R-067 取代——镜像改为 `anas:`/`fingerprint:` 命名引用后，引用在 apply 时即确定，无需结果通道（已废弃） | 契约 + 审阅 |
| `INCUS-R-057` | 一级发行版为 Debian 13、Ubuntu 24.04 LTS 与 Ubuntu 26.04 LTS，其中 26.04 是主要测试环境；其余标记待适配，未适配时相关功能保持关闭而不是失败 | 静态 + e2e |
| `INCUS-R-058` | 宿主特权动作通道要求；由 `HOSTACT-R-001` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 + 安全测试 |
| `INCUS-R-059` | 宿主特权动作通道要求；由 `HOSTACT-R-002` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 单元 + 安全测试 |
| `INCUS-R-060` | 宿主特权动作通道要求；由 `HOSTACT-R-003` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 + e2e |
| `INCUS-R-061` | 宿主特权动作通道要求；由 `HOSTACT-R-004` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 + e2e |
| `INCUS-R-062` | 必须预留 `network_mode: nat \| lan` 两种网络模式，默认 `nat`；LAN 模式暂不实施，文档须写明它同时移除两个协议族的围栏 | 文档 |
| `INCUS-R-063` | Traefik 绑定必须跟随实例生命周期：启动即绑、停止或暂停即解绑，与实例是否长驻无关；运行时绑定经 Traefik 文件 provider 目录完成，Core 不在这条路径上；由 `INCUS-R-146`、`INCUS-R-148` 取代——解绑改由共享客户端与 janitor 撤销请求、中介全量重算，不再由中介核验实例身份（已废弃） | 契约 + e2e |
| `INCUS-R-064` | 可经 HTTP 发布的 guest 端口必须在租约的 `publish.http.allowed_ports` 中于 apply 时固定；省略 `publish.http` 即不允许 HTTP 发布 | 契约 + 单元 |
| `INCUS-R-065` | 随机域名必须由 `workload_id` 与租约 secret 确定性派生，不得抽取随机数再存表：同一任务恒定、不同任务不重复、外部不可预测 | 单元 |
| `INCUS-R-066` | `image_allowlist` 条目必须为互斥的 `{fingerprint: <64hex>}` 或 `{catalog: anas, name: <name>, revision: <revision>}` 对象，拒绝字段混用、未知字段和所有字符串旧格式；目录引用按架构与隔离档解析并冻结摘要，同一版本键不得改变内容 | 契约 + 审阅 |
| `INCUS-R-067` | 不得为 `guest_image` 新增 Provider→Runner 结果通道：镜像引用在 apply 时即确定，Provider 只保证该引用对应的镜像存在 | 审阅 |
| `INCUS-R-068` | 宿主特权动作通道要求；由 `HOSTACT-R-005` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 |
| `INCUS-R-069` | 宿主特权动作通道要求；由 `HOSTACT-R-006` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 静态 + 审阅 |
| `INCUS-R-070` | 入站路径必须与网络模式解耦：发布授权与 Traefik 路由不得绑定 NAT/LAN 的实现细节；LAN 暂不实现，启用前必须单独证明受管后端可达及访问控制，不得默认复用 macvlan shim 绕过入站授权 | 契约 + 审阅 |
| `INCUS-R-071` | 随机域名派生必须确定性、掺入租约 secret、碰撞时失败而不静默复用；`mode: fixed` 时不派生 | 单元 |
| `INCUS-R-086` | 域名模式必须支持 `fixed` / `named` / `random` 三档；`named` 的 label 只允许 `[a-z0-9-]` 且最终域名必须落在该租约 prefix 的命名空间内 | 契约 + 单元 |
| `INCUS-R-087` | 消费者不得直接写入 Traefik 动态配置目录：只能写受约束的请求文件，由中介校验域名落在本租约命名空间内后渲染；middleware 与 entrypoint 由渲染方决定，消费者无权指定 | 契约 + e2e |
| `INCUS-R-088` | 域名校验必须在消费者进程之外完成——放在共享客户端库不足以约束被攻陷的消费者 | 审阅 + e2e |
| `INCUS-R-072` | 旧 revision 镜像不得在 apply 时自动删除：当前与上一个 deployment 引用的镜像必须保留，清理只能经显式 `incus.image-prune` 动作并先 dry-run | 单元 + e2e |
| `INCUS-R-073` | 宿主特权动作通道要求；由 `HOSTACT-R-007` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 契约 + 审阅 |
| `INCUS-R-074` | 宿主特权动作通道要求；由 `HOSTACT-R-008` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 |
| `INCUS-R-075` | 统一动作 ABI 要求；由 `ACTABI-R-001` 取代——与 Incus 无关（已废弃） | 契约 + e2e |
| `INCUS-R-076` | 统一动作 ABI 要求；由 `ACTABI-R-002` 取代——与 Incus 无关（已废弃） | 单元 |
| `INCUS-R-077` | 统一动作 ABI 要求；由 `ACTABI-R-003` 取代——与 Incus 无关（已废弃） | 单元 |
| `INCUS-R-078` | 统一动作 ABI 要求；由 `ACTABI-R-004` 取代——与 Incus 无关（已废弃） | 契约 + 审阅 |
| `INCUS-R-079` | 统一动作 ABI 要求；由 `ACTABI-R-005` 取代——与 Incus 无关（已废弃） | 单元 |
| `INCUS-R-080` | 统一动作 ABI 要求；由 `ACTABI-R-006` 取代——与 Incus 无关（已废弃） | 单元 + e2e |
| `INCUS-R-081` | 统一动作 ABI 要求；由 `ACTABI-R-007` 取代——与 Incus 无关（已废弃） | 单元 |
| `INCUS-R-082` | 统一动作 ABI 要求；由 `ACTABI-R-008` 取代——与 Incus 无关（已废弃） | 契约 + e2e |
| `INCUS-R-083` | 批量数据只走「动作自己打开目的地」一条路径：不得提供经浏览器取回的制品下载端点，也不得在控制流内联 base64。将来开放下载端点前必须一并定案有效期、可否重复使用与是否允许非浏览器客户端，且不得使用 URL 内明文 token | 审阅 + 安全测试 |
| `INCUS-R-084` | Compose 的 `additional_contexts` 默认值 `../..` 只在源码 checkout 下正确，从 staging 树构建时够不到仓库根；必须提供可用的覆盖路径并在文档中写明，或改由 Runner 发布源码根 | 静态 + 审阅 |
| `INCUS-R-085` | 「Incus project 没有镜像 allowlist 原生开关」这一判断必须对照 Incus 上游 project 配置参考核实；若存在等价键，镜像约束必须下沉到 project 由 daemon 兜底 | 审阅 |
| `INCUS-R-089` | 宿主特权动作通道要求；由 `HOSTACT-R-009` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 单元 + 安全测试 |
| `INCUS-R-090` | 宿主特权动作通道要求；由 `HOSTACT-R-010` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 契约 + 审阅 |
| `INCUS-R-091` | 宿主特权动作通道要求；由 `HOSTACT-R-011` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 单元 + e2e |
| `INCUS-R-092` | `lease_secret` 必须是独立的一条 Secret Store 条目（32 字节随机），不得并入客户端证书 bundle、也不得由部署级 secret 派生；随租约投影给消费者并标为敏感，resource state 只留引用 | 单元 |
| `INCUS-R-095` | ingress 的认证方式必须是租约里 apply 时的声明（`auth` 取 `none` 或 `forward_auth`，默认 `none`），不得由运行时逐次发布选择——否则被攻陷的消费者可自行关闭认证 | 契约 + e2e |
| `INCUS-R-096` | 文档必须在 `auth: none` 旁写明：域名不可预测不是访问控制（SNI 明文、URL 进 Referer 与日志），不得用它发布含敏感数据或带写入能力的服务 | 文档 + 审阅 |
| `INCUS-R-093` | 统一动作 ABI 要求；由 `ACTABI-R-009` 取代——与 Incus 无关（已废弃） | 单元 |
| `INCUS-R-094` | 发行版自动安装首批只覆盖 Debian 13、Ubuntu 24.04 LTS、Ubuntu 26.04 LTS；其余标记待适配并作为后续计划，未适配时不自动安装且相关功能保持关闭 | 静态 + e2e |
| `INCUS-R-097` | 资源凭据的 `rotation_mode` 声明位；由 `CRED-R-001`、`CRED-R-002` 取代——这是 Core 的凭据要求，不是 Incus 的（已废弃） | 契约 + 单元 |
| `INCUS-R-098` | compute 两条凭据的轮换模式；由 `CRED-R-006`、`CRED-R-007` 取代（已废弃） | 契约 + e2e |
| `INCUS-R-099` | 宿主特权动作通道要求；由 `HOSTACT-R-012` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 审阅 |
| `INCUS-R-100` | 宿主特权动作通道要求；由 `HOSTACT-R-013` 取代——通道服务于任何需要特权宿主操作的功能，不是 Incus 的要求（已废弃） | 契约 + 审阅 |
| `INCUS-R-101` | guest 转发许可必须经既有宿主确认通道绑定当前 Core compute 租约、实际投递凭据及明确目的地址/端口；不得仅凭实例名称前缀或子网成员关系授权；由 `INCUS-R-112`—`INCUS-R-127` 取代——出站改为租约级静态分级，不再逐实例授权与续期（已废弃） | 单元 + e2e |
| `INCUS-R-102` | 每次新增或续期许可必须核验实际实例 UUID、运行代际、分配地址、MAC、物理接口及目的路由，并拒绝跨租约来源冒用与身份替换；由 `INCUS-R-112`—`INCUS-R-127` 取代——出站改为租约级静态分级，不再逐实例授权与续期（已废弃） | 单元 + e2e |
| `INCUS-R-103` | 停止、取消、失败或租约撤销必须阻止新连接并验证既有连接撤销；两种结果不得互相替代，未确认撤销时不得报告清理完成；由 `INCUS-R-112`—`INCUS-R-127` 取代——出站改为租约级静态分级，不再逐实例授权与续期（已废弃） | 单元 + e2e |
| `INCUS-R-104` | 许可期限、旧接口/地址、重启及未完成外部效果必须保留可归属记录；不得删除失败 intent/receipt 或自动认领同名对象，残留许可应阻止依赖拆除；显式退役须核验部署停止、租约撤权、完整实例/操作与物理端口为空，并在所有原归属内核对象清除后保留墓碑，失败不得解除阻止；由 `INCUS-R-112`—`INCUS-R-127` 取代——出站改为租约级静态分级，不再逐实例授权与续期（已废弃） | 单元 + e2e |
| `INCUS-R-105` | 最小转发适配必须保留更早的管理员显式拒绝和 Docker 默认策略；不得改成全局 FORWARD ACCEPT、关闭安全约束或给消费者增加宿主网络特权 | 单元 + e2e |
| `INCUS-R-106` | project 与受管 bridge 必须带可核验的租约归属标记；Provider 必须在写入前拒绝属于其他租约（含证书已撤销者）或能被其他受限客户端证书操作的 project，未标记 project 仅在无其他受限证书时采纳；`inspect` 以相同条件判定 ready，`default` project 不得作为 sandbox | 单元 + e2e |
| `INCUS-R-107` | 发行版到安装步骤的映射必须是声明式的表；未适配的发行版不自动安装，报错给手工指引并让依赖 `compute` 的功能保持关闭 | 静态 + 审阅 |
| `INCUS-R-108` | 一级发行版必须从 Zabbly `lts-7.0` 安装 Incus 7.0 LTS：密钥编译进二进制并固定指纹，`incus`/`incus-base`/`incus-client` 钉死到该来源且禁止回落到发行版版本，其余依赖只取自发行版官方仓库或保留发行版签名校验的固定国内镜像，不得添加其他第三方软件仓库；来源不可用时失败并给出手工指引 | 静态 + 单元 + e2e |
| `INCUS-R-109` | 宿主安装计划必须在确认前冻结工作区有效 `CHINESE_SPEEDUP` 对应的编译源选择；执行使用冻结值，开关不得改变发行版签名校验或 Incus 来源钉包 | 单元 |
| `INCUS-R-110` | guest 发布构建的 `CHINESE_BUILD_SPEEDUP` 必须同时覆盖 bootstrap 与 APT 软件源，并将选择冻结进配方摘要；运行期开关不得重烘焙既有 revision | 单元 + CI |
| `INCUS-R-111` | 目标部署不再声明 compute 租约时，Core 必须经上一部署冻结的 Provider 执行 `revoke`，撤销证书并保留 project 与实例；撤销未确认时默认中止激活，只有显式风险确认才可记录为未确认；`deletion_policy` 只接受 `retain` | 单元 + e2e |
| `INCUS-R-112` | compute 租约声明必须包含出站档位，取值为 `internet`、`internet_lan`、`internet_lan_host`、`modules_only` 之一；未声明时取 `internet`；档位随部署冻结，运行时消费者不得更改 | 契约 + 单元 |
| `INCUS-R-113` | `internet` 档租约的实例必须能访问公网地址，且不能访问局域网、宿主地址（含 Docker 发布到宿主的端口）、其他租约、Docker 网络和链路本地地址 | e2e |
| `INCUS-R-114` | `internet_lan` 档租约的实例在 `INCUS-R-113` 允许的范围之外还必须能访问局域网，其余拒绝项不变 | e2e |
| `INCUS-R-115` | `internet_lan_host` 档租约的实例在 `INCUS-R-114` 允许的范围之外还必须能访问宿主地址上的端口和 Docker 发布到宿主的端口；未发布的容器端口、其他租约与链路本地地址仍必须拒绝 | e2e |
| `INCUS-R-116` | `modules_only` 档租约的实例只能经 Traefik 访问 ANAS 的 Module，其余出站必须全部拒绝 | e2e |
| `INCUS-R-117` | 租约声明的 `module_access` 默认关闭；打开时 `internet` 与 `internet_lan` 档实例必须能经 Traefik 访问 ANAS 的 Module，关闭时必须不能 | 契约 + e2e |
| `INCUS-R-118` | 放行 Traefik 的地址清单必须跟随 Traefik 容器的当前地址，且只由 hostd 的边界内同步动作写入（`HOSTACT-R-014`、`R-015`）：`incus.configure` 时由操作者批准一次；apply 启动 Traefik 后、Traefik 容器重启后与 anasd 启动时由 anasd 触发同步，hostd 自己从 Docker 读取地址；Provider 的 ACL 只按名字引用这份清单 | 单元 + e2e |
| `INCUS-R-119` | Traefik 地址清单只能由 hostd 依据自己从 Docker 读取的 Traefik 容器地址更新；anasd 的同步请求、消费者、Module 声明与请求参数都不得提供或覆盖其中的地址 | 单元 |
| `INCUS-R-120` | 局域网必须按正向定义计算：宿主默认路由所在网卡的直连网段，加上操作者配置的附加网段；Docker 网络与 Incus 网络不得因为创建先后或地址范围被当作局域网 | 单元 + e2e |
| `INCUS-R-121` | 每次 apply 都必须按当前宿主网络重新计算局域网清单，并写入各租约的出站策略 | 单元 |
| `INCUS-R-122` | 各档租约的实例都必须能访问本租约网桥网关上的 DNS 与 DHCP | e2e |
| `INCUS-R-123` | 租约声明的 `intra_lease` 默认关闭：关闭时同一租约内的实例之间必须不能互相访问，打开时必须可以 | 契约 + e2e |
| `INCUS-R-124` | 租约撤销后，该租约实例的新出站连接必须被拒绝，已建立的连接必须结束；无论撤销由 CLI 还是控制台的部署触发 | e2e |
| `INCUS-R-125` | Provider 的 `inspect` 必须把租约网桥的 ACL 或地址清单与所声明的档位、开关不一致判为未就绪，`ensure` 必须把它修复到声明状态 | 单元 |
| `INCUS-R-126` | 宿主侧只允许一组随 Incus 宿主配置安装、随卸载删除的固定静态转发规则，把进出租约网桥的转发交给 Incus ACL 决定；其中从租约网桥到 Docker 网桥只放行 Docker 已做 DNAT 的连接（发布端口），其余丢弃，使不自带拦截的 Docker 版本（Debian docker.io 26）也挡住未发布的容器端口（2026-09-30 实测）；规则必须排在管理员规则与 Docker 规则之后，不得按实例或按租约增删 | 单元 + e2e |
| `INCUS-R-127` | 租约网桥必须使用不以 `anas` 开头的专用名称前缀，宿主静态转发规则只匹配这个前缀 | 单元 |
| `INCUS-R-128` | 控制台 API 必须为每个租约返回网络视图数据：网桥名与网段、网关、出站档位、`module_access`、`intra_lease`、入站档位、HTTP 发布的域名、端口绑定的宿主端口映射、实例及其地址，以及由档位推导出的允许与拒绝方向；数据不得包含凭据 | 契约 |
| `INCUS-R-129` | 控制台必须以图形展示租约网络视图，区分放行与拒绝的方向；该页面只读，不提供修改入口 | e2e |
| `INCUS-R-130` | 租约网桥的默认入站动作必须是拒绝：未被本租约入站档位或发布放行的来源，不能主动连到租约实例；实例主动出站的回包，以及本租约网关上的 DHCP 与 DNS 不受影响 | e2e |
| `INCUS-R-131` | compute 租约声明必须包含入站档位，取值为 `none` 或 `published`；未声明时取 `none`；档位随部署冻结，运行时消费者不得更改 | 契约 + 单元 |
| `INCUS-R-132` | 入站档位为 `none` 的租约不得声明任何发布，Core 必须在 apply 前拒绝这样的声明 | 单元 |
| `INCUS-R-133` | `published` 档租约的 ACL 只能放行已声明发布需要的流量：Traefik 地址清单到 HTTP 发布的 guest 端口，以及其他租约以外的来源到槽位地址上端口绑定的 guest 端口与协议；其余来源与端口按默认拒绝处理 | 单元 + e2e |
| `INCUS-R-134` | Provider 的 `inspect` 必须把入站档位、发布与槽位对应的 ACL 规则或网桥默认入站动作与冻结声明不一致的情况判为未就绪，`ensure` 必须把它修复到声明状态 | 单元 |
| `INCUS-R-135` | 为入站放在宿主侧的规则只能有两类：`INCUS-R-126` 的静态转发规则，以及端口绑定的规则链、端口表与端口占位；不得为 HTTP 发布或按实例增删宿主防火墙规则、路由或邻居项 | 审阅 + e2e |
| `INCUS-R-136` | 不同租约的实例之间不得直接建立连接，不论档位，也不论是否属于同一消费者；端口绑定也不得让其他租约的实例连到本租约的实例 | e2e |
| `INCUS-R-137` | 访问另一租约的服务只能经 Traefik 的 HTTP 发布，并与访问 ANAS Module 一样受访问方的出站档位与 `module_access` 约束 | e2e |
| `INCUS-R-138` | 同一个「协议 + 宿主端口」只能属于一个端口绑定，且不得与 Traefik 已有入口、其他 Module 发布的宿主端口或宿主上已有进程监听的端口重复；多个租约可以在宿主所有地址上各自绑定不同端口 | 单元 + e2e |
| `INCUS-R-139` | 不同租约的 HTTP 域名命名空间不得重叠，也不得覆盖部署中已有服务的域名；Core 必须在 apply 时拒绝 | 单元 |
| `INCUS-R-140` | 撤销一个租约或改变它的授权，只能移除或改变该租约自己的 ACL 放行、路由文件与端口绑定；其他租约的档位与发布不受影响 | e2e |
| `INCUS-R-141` | HTTP 发布与端口绑定必须分开声明、各自生效：HTTP 发布由 Traefik 终止 HTTPS 后以 HTTP 转给 guest，端口绑定原样转发 TCP 或 UDP 报文；任一种的声明、撤销或失败不得改变另一种 | 契约 + 单元 |
| `INCUS-R-142` | HTTP 发布中介必须在 anasd 进程内运行，不得为它新增常驻服务 | 审阅 |
| `INCUS-R-143` | HTTP 发布中介不得持有 Incus 凭据、租约证书、`lease_secret` 或其他 Secret，不得调用宿主特权动作；它唯一的输出是专属子目录里的 Traefik 动态配置文件 | 审阅 |
| `INCUS-R-144` | HTTP 发布中介只能接受目标地址在本租约 IPv4 网段内、且不是网络地址、网关或广播地址的请求；请求的端口必须在 `publish.http.allowed_ports` 内 | 单元 |
| `INCUS-R-145` | HTTP 发布中介必须能由冻结授权与当前请求文件全量重算路由：重启或恢复后删除没有对应有效请求的路由文件、补齐缺失的；中介停机期间已发布的路由保持可用 | 单元 + e2e |
| `INCUS-R-146` | 共享客户端停止或删除实例时，必须撤销该实例的全部 HTTP 发布请求；janitor 必须删除实例已不存在的请求 | 单元 |
| `INCUS-R-147` | 部署激活时，Core 必须删除授权被移除或改变的租约的全部路由文件，并由 Provider 同步 ACL；CLI 与控制台部署都必须覆盖，且不依赖中介是否在运行 | 单元 + e2e |
| `INCUS-R-148` | HTTP 发布在运行时的发布与撤销只经 Traefik 文件 provider 目录生效，Core 不参与单个作业的发布 | 审阅 |
| `INCUS-R-149` | 同一域名同一时刻只能指向一个实例：同一租约内多个请求得到同一域名时，中介只能发布其中一个（已发布的优先），其余拒绝，不得覆盖 | 单元 |
| `INCUS-R-150` | 端口绑定必须原样转发 TCP 或 UDP 报文：不终止 TLS、不解析应用协议，guest 看到的来源地址是真实客户端地址 | e2e |
| `INCUS-R-151` | 端口绑定只能接管目的地址是宿主本机地址的流量：宿主的每个地址（包括绑定之后新增的）上该端口都必须转到槽位，回环地址（`127.0.0.0/8`、`::1`）除外；宿主自己往外的连接和经宿主转发的同端口流量不得被改写 | e2e |
| `INCUS-R-152` | 端口绑定必须在 apply 时声明并冻结协议、宿主端口、槽位与 guest 端口，由 hostd 同步到宿主上的端口表；运行时不得增删绑定或改变目标 | 契约 + e2e |
| `INCUS-R-153` | 端口绑定的宿主端口可以显式指定，也可以写 `auto`：Core 从操作者在 `incus.configure` 时确认的端口范围（默认 30000–32767，可修改）里随机选择一个空闲端口并记入部署状态，此后的 apply 保持不变，直到这条绑定被删除 | 单元 |
| `INCUS-R-154` | 显式指定的宿主端口在 apply 时已被占用，apply 必须失败并记录运行问题；`auto` 必须跳过被占用的端口 | 单元 + e2e |
| `INCUS-R-155` | 槽位必须在租约声明里写死实例名；Provider 必须为槽位预留不在 DHCP 动态范围内的固定 IPv4，租约启用 IPv6 时另预留固定 IPv6，并由 `ensure` 结果交回；槽位地址跨 apply 不变，槽位不再声明时释放 | 契约 + 单元 |
| `INCUS-R-156` | 共享客户端创建槽位对应名称的实例时，必须使用槽位地址，其余网卡设置与 profile 一致；一个槽位同一时刻只能有一台实例，可以承载多个端口绑定 | 单元 |
| `INCUS-R-157` | 租约启用 IPv6 时，端口绑定必须同样在宿主的 IPv6 地址上生效，转到槽位的固定 IPv6 | e2e |
| `INCUS-R-158` | hostd 同步端口表时必须逐条校验：目标是 ANAS 租约网段内的槽位地址，端口在操作者确认过的范围内，当前没有宿主进程、Docker 或其他绑定占用，条目与冻结部署一致；校验与写入必须原子完成，不合格的条目不得生效 | 单元 + 安全测试 |
| `INCUS-R-159` | Provider 的 `ensure` 结果必须交回租约网桥的 IPv4 网段与网关，由 Core 记入 resource state | 契约 + 单元 |
| `INCUS-R-160` | 每个生效的端口绑定必须占住对应的宿主端口，使之后在同一端口绑定的宿主进程或 Docker 发布直接失败；占位不得因为收到连接或报文而失效（2026-09-30 实测：被报文触发的 UDP 占位会失效） | e2e |
| `INCUS-R-161` | 宿主重启后必须先恢复端口占位、再恢复转发；某端口已被其他进程占用时，该绑定不得生效，必须记录运行问题，且不得遮住那个进程 | e2e |
| `INCUS-R-162` | anasd 必须定期并在 Docker 事件发生时检查已生效的端口绑定，发现冲突、占位丢失或规则漂移时记录运行问题 | 单元 + e2e |
| `INCUS-R-163` | 文档必须写明两种发布的差异：HTTP 发布下 guest 看到的来源是租约网桥网关，真实客户端在 `X-Forwarded-For`；端口绑定没有 ANAS 层认证，实例未运行时连接会超时，guest 内部端口被占用 ANAS 看不到 | 文档 + 审阅 |
| `INCUS-R-164` | 声明了端口绑定而宿主没有可用的 hostd（未以系统服务安装、非 systemd 发行版或开发构建）时，apply 必须失败并说明原因，不得跳过或推迟这条绑定 | 单元 |
