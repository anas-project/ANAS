---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-09-26
---

# Incus compute Provider 实施计划

验收依据是[Incus compute Provider Module 集成要求](../requirements/incus-module.md)的需求矩阵；
设计依据是 [Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md) §4 与
[AI Agent 编排设计](../../modules/ai_agent/docs/architecture/orchestration-design.md) §5.2。

本计划把原属 [Forgejo Module 实施计划](../../modules/forgejo/dev-docs/plans/forgejo-module.md) M2 的“Incus compute contract 与
Provider”拆出来独立跟踪。Forgejo 计划 M2 只保留“作为消费者接入”的部分。

**已落地和剩余范围以 §1 里程碑表为准。真实宿主验收、共享构建校验以及宿主供给、动作通道、入站和镜像烘焙分别跟踪，不以单元层完成代替端到端验收。**

2026-09-18 本轮从干净 `master`（`49bbf45`）直接接续修改。此前多段“未编译/测试”属于当时
快照；本轮实际验证与修复统一见 §1.1 和[核对记录](../reviews/2026-09-18-incus-implementation-verification.md)。
没有将只通过本机回归或交叉编译的项目改为实机验收完成。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：Contract 改形为租约语义 | R-010 | 已完成 |
| M1：Provider Module 骨架、凭据与部署边界 | R-001—R-005 | 已完成 |
| M2：Provider operation、隔离与可观测性 | R-006、R-009、R-012—R-016 | 已完成 |
| M2a：围栏真实验收 | R-011、R-106 | 实施中；租约归属标记与受限证书独占已接入（单元层，2026-09-26）；容器 host disk/跨租约网络/直接 proxy 反例已实测，显式 proxy 禁令修复及两档回归通过；完整 6.0 `restricted.*` 键集与隔离档实例类型上限已收紧（单元层，2026-09-26）；完整设备/VM 矩阵及 daemon 实际拒绝待验收 |
| M3：Core 的 compute Resource 凭据与投影 | R-017、R-018、R-020 | 已完成 |
| M4：共享 Incus 客户端库 | R-021—R-024、R-037 | 已完成 |
| M4a：取消后清理验收 | R-025 | 已完成；超时/取消补偿回归和真实运行中容器 SIGTERM 回收通过；独立 VM 运行矩阵仍归 M6 |
| M5：Forgejo 迁移为 Contract 消费者 | R-026、R-028 | 已完成 |
| M5a：one-job 行为等价验收 | R-027 | 已完成；默认系统容器档真实正常/失败/SIGTERM/SIGKILL 保留状态恢复及注册/根盘回收通过；VM/ARM64 和正式镜像发布仍归 M6/M12 |
| M6：真实 Incus 宿主验收 | R-007、R-008、R-029—R-034 | 实施中；指定独立实验环境已通过默认容器双租约、btrfs 实际配额、客户端身份、管理证书重叠/撤销及 lab-r11 one-job；VM/ARM64、ZFS、双栈及完整矩阵待验收 |
| M7：非特权系统容器 interface | R-035—R-036 | 已完成；单元层，e2e 归 M6 |
| M8a：共享构建校验 | R-038 | 已完成；校验与 CI 已接入 |
| M8b：staging 共享构建路径 | R-084 | 已完成；三个镜像的源码/staging 六次无缓存实际构建、缺少覆盖路径反例、输入/业务二进制及固定 CLI 摘要比对通过；不代表完整 Core 部署验收 |
| M8：长驻实例档预留 | R-039—R-042、R-046 | 未开始 |
| M9：网络 IPv6 姿态与 image_policy 预留 | R-043、R-045、R-052 | 已完成 |
| M9a：双栈出网验收 | R-044 | 实施中；实现已落地，真实验收待执行 |
| M10：宿主 Incus 供给（安装、发行版矩阵、控制台边界） | R-047、R-048、R-050、R-051、R-057、R-094、R-107、R-108 | 实施中；三个一级发行版 amd64 各通过 25 项审批/卸载，26.04 另有后端 11 项、浏览器 8 项；真实 Core/Compose 自动投影、双合成消费者、新部署凭据保持和撤销后拒绝闭环已通过；实际 Forgejo/AI Agent 部署、ARM64/VM、未适配系统降级与失败恢复另验 |
| M10a：默认 Docker 的租约转发授权与撤销 | R-101—R-105 | 实施中；确认、来源及原状态回执已连接，失败启用统一有界撤回；独立内核精确许可/源冒用拒绝/既有连接撤销及安装后端显式退役、重复/证据保留通过；生产启用、自动续期/停止/重启协调、真实 guest/Forgejo 与完整反向隔离未关闭 |
| M11：入站与 Traefik 发布 | R-053、R-054、R-062—R-065、R-070、R-071、R-086—R-088、R-095、R-096 | 实施中；观察、配置、任务排空和跨进程围栏已连接；新增同队列 owner 的统一启动入口、目录隔离准入及固定 renderer 身份；自动 CLI 排空、完整内核生命周期、VM/TAP、health、生产 UID/挂载安装及实机验收待办 |
| M11a：独立租约命名密钥 | R-092 | 已完成；生成/复用、敏感投影、冻结引用与文件备份恢复回归通过 |
| M12：`guest_image` 契约与 distrobuilder 烘焙 | R-019、R-055、R-066、R-067、R-072、R-085 | 实施中；默认配方、冻结归档、历史 bundle、发布脚本与逐摘要供给已接通，lab-r11 默认容器真实烘焙/启动/one-job 通过；VM/ARM64、正式签名分发、回滚及破坏性 prune 待验收 |
| M13：批量数据路径边界（只保留「动作自己打开目的地」） | R-083 | 实施中；本地 bundle 动作自行写入目的地、元数据控制输出及 URL/base64 拒绝回归已落地；不提供浏览器下载端点，统一动作整体安全审阅仍待完成，见[统一动作 ABI](../../docs/architecture/action-abi.md) §13 |
| M14：其余发行版适配 | — | 未开始；待适配清单见[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §2 |

覆盖统计：82 项需求全部有且只有一个里程碑归属（另有 26 项已废弃）。

### 当前接续状态（2026-09-26）

默认 Incus 7.0 LTS（`INCUS-R-049` 废弃，由 `INCUS-R-107`、`INCUS-R-108` 取代）：三份一级发行版配方
改为从 Zabbly `lts-7.0` 安装 `incus`/`incus-base`/`incus-client`，其余依赖仍取官方源。密钥编译进二进制、
内嵌于 deb822 `Signed-By`（`_apt` 读不到 root 私有配置目录），指纹由测试钉住；Incus 包钉到 Zabbly、
禁止回落发行版 6.0。编译配置、夹具与单元回归通过；宿主供给在三个发行版上的实机验收（原 25 项门禁）
需以 7.0.1 重跑，旧 6.0 配方宿主无迁移动作，Zabbly 源不进入宿主日常 `apt upgrade`，见宿主供给设计 §2.2。

围栏完整性修复（[审查](../reviews/2026-09-25-compute-contract-incus-review.md) §1.1、§1.2）：Provider
现在拥有 Incus 6.0.0—6.0.5 全部 `restricted.*` 键，要么显式写入严格值、要么要求不存在；VM 档也写
`restricted.containers.privilege=unprivileged`；隔离档由 `limits.containers` / `limits.virtual-machines`
写在 project 上；既有 project 带未受管的 `restricted.*` 键时写入前拒绝。新回归先在旧实现上失败再通过，
仅为单元层（fake daemon）；daemon 对跨档创建和已有实例冲突的实际拒绝、旧 `anas-forgejo-runners`
的升级收敛仍须在 M6/M2a 实机验收，不据此关闭 M2a。

租约归属（审查 §1.3，新增 R-106）：project 与 bridge 写入 `user.anas.consumer`、`user.anas.sandbox`
与 `user.anas.lease_credential`（租约证书指纹）；属于其他租约或被其他受限证书共享的 project 在写入前
拒绝，未标记 project 仅在无其他受限证书时采纳，`inspect` 同条件判定；Core 与 Provider 拒绝
`default` sandbox。宿主侧 image prune 与转发退役原已要求 project 带 consumer/sandbox 标记，而 Provider
此前从未写入，真实 project 上两者都会被阻止，本次一并补齐（仍待实机验证）。旧 controller 遗留的受限
证书若仍信任该 project，升级需运维先移除。仅单元层，多工作区同 daemon 的实机验收见 §10。

按 daemon 能力处理 7.x 限制键：`ensure`/`inspect` 读 `GET /1.0` 的 `api_extensions`，daemon 支持时写
`restricted.storage-pools.access=<storage_pool>`（7.0+）与 `restricted.virtual-machines.nesting=block`
（7.x，默认 `allow`）；`restricted.images.servers` 按 7.0.1/7.5.1 源码会连本地镜像的实例创建一起拒绝，
不能作镜像约束下沉，改为必须不存在（R-085 证据见宿主供给设计 §7.2，仍待实机）。一级发行版官方仓库
当前仍是 6.0.x，7.x 路径只有 fake daemon 单元证据；需在 7.0.1 LTS（Debian trixie-backports）与 7.5 daemon
上实机验证写入、默认值与存储池改配置时的拒绝。

2026-09-26 实机核验（接上段）：源码阅读先发现 7.5 默认给 VM 开嵌套虚拟化、限制为 `block` 时拒绝未显式
关闭的 VM，已改为在支持该限制的 daemon 上给 VM 档 profile 写 `security.nesting=false`。随后三台一次性 VM
分别跑 Ubuntu 26.04 官方 6.0.5、Debian 13 trixie-backports 7.0.1（经阿里云镜像）与 Ubuntu 26.04 Zabbly
7.5.1，`server-incus-fence-e2e.py` 的 16/18/19 项必需检查全部通过，覆盖第 1–3 条修复与 7.x 键；7.5.1 上
租约 VM 请求实际创建成功，显式开启 nesting 的请求被拒。第一轮宿主基线因物理网卡 03:00 定时断链 22 秒而
判定失败，已单独核实与 VM 无关。存储池改配置时的拒绝、VM 启动、ZFS、ARM64 与真实 Core 多工作区部署仍
未验收，M2a/M6 不据此关闭。详见[实机核验](../reviews/2026-09-26-incus-fence-native-validation.md)。

最新原生退役第三轮已完整通过：独立默认 Docker DROP 环境中，真实内核事务与实际
installed 退役后端分别验收。第一/二轮失败保留，第二轮诊断确认 Incus 暂留已完成操作
阻止退役，现由夹具等待自然消失并建立正例，不放宽产品清单判据。许可刷新后的读回、
最终保存或会话关闭失败已先以红灯复现，再统一接入一次有界撤回；失败与依赖阻止仍
保留。253个新工件源码输入、原始事件、退出及正常关机/宿主十项对照均独立核验，详见
[失败撤回与退役终态](../reviews/2026-09-26-incus-forwarding-failure-withdrawal.md)。
Core stopped 元数据/历史 grant 是显式夹具，额外故障注入仍是单元测试，真实
guest/Forgejo、自动运行 owner 和跨重启闭环没有因此完成。

重新核对保留目录后确认第六轮已通过，78 个当轮输入与本次开工前源码相同，17 项
唯一 run/pass 与 package 终态、正常关机及宿主十项对照均重新验证。随后新增显式
`retire`，复用现有确认动作和状态事务，在已停止/撤权/完全空闲后退役原内核对象，
保留墓碑与失败历史；同时修复 released 状态掩盖残留及最终会话失败丢失阻止状态。
本次新代码必须用新冻结工件验收，不把第六轮结果归因于这些后续修复。详见
[转发退役接续](../reviews/2026-09-26-incus-forwarding-retirement.md)。M10a/M10/M11/M12
保持未完成，真实 guest/Forgejo 的默认 Docker 路径没有因此被验收。

租约转发已从孤立执行器接入编译动作清单、共享确认与宿主状态事务。实际启用仍被
`forwarding_lifecycle_integration_unavailable` 阻止；确认不能绕过该门禁，也没有自动
续期。停用使用原回执，不要求已撤销的租约再次授权，保留拒绝规则和失败历史；残留
对象阻止宿主依赖拆除。新的内核轮次与真实 guest/Forgejo 验收严格区分，最新实际终态见
[转发许可接续](../reviews/2026-09-25-incus-forwarding-permission-continuation.md)。

默认Docker转发兼容性接续：增加宿主只读路由/filter policy诊断和固定warning，不把
早期nft ACCEPT视为最终放行，计数器不污染确认摘要。新受控报文入口使用真实Docker/
Incus bridge和本地namespace端点，区分原始DROP、精确临时许可、其他流拒绝及撤回。
产品尚未新增自动转发授权写入，M10/M11不由诊断关闭。实际终态见
[转发观察与受控实验](../reviews/2026-09-25-incus-forwarding-observation.md)。

该受控实验第四轮已在全新Ubuntu26.04 amd64通过9项：真实Incus bridge上的报文先
命中早期nft ACCEPT，仍被后续Docker默认DROP拒绝；精确临时许可后成功，其他源/端口
及更早显式拒绝仍受限，撤回后再次拒绝。生产观察器实际读回3条相关base chain并保留
未验证警告；归档、源码身份、正常关机与宿主原Docker/网络对照均独立通过。它关闭
因果定位与只读诊断，不代表自动租约转发授权、源防伪或默认完整产品部署已完成。

随后补齐元信息别名/重复和未知地址族的诊断拒绝边界，并重新编译执行第五轮；相同9项
在最终代码上再次全部通过。最终归档、220个输入、实际读取结果、正常关机与原宿主
完整对照已独立核验；第四轮记录保持原样，不把旧工件结果归因于新增代码。

联合停止链路继续分层核验：第八轮实际日志经有界Zstandard解压后，确认Runner已领取
任务，但固定工作流镜像拉取超时。第九轮使用明确可路由的全新隔离环境，已实际进入
payload并完成Core停止子测试；完整父入口仍因软删除registration计数检查而失败。
新增严格Core事件判定与只读软删除计数、在线scope API复核后，第十轮完整十阶段、
五个Core用例及六个宿主job记录通过，包括运行作业清理、口令停用、同账号重启执行
与失败清理保留证据。独立归档、正常关机和物理宿主对照相同；完整终态见
[停止与出站前置条件接续](../reviews/2026-09-25-forgejo-stop-forwarding-continuation.md)。
显式可路由夹具不能替代默认Docker DROP共存、完整业务部署或生产入站验收，不据此
提前关闭M10/M11，也不重写旧失败结果。

新的 `policy-loader-r1` 已在 Ubuntu 26.04 amd64 完成真实 build/export/repeat、十项
镜像门禁及五种Forgejo工作流。固定guest AppArmor加载器不再带入发行版通用userns
许可；普通程序仍拒绝、rootless OCI限额通过。原始事件、工件与导出摘要、正常关机和
物理宿主基线均独立复核。记录见[固定策略加载器](../reviews/2026-09-25-forgejo-fixed-policy-loader.md)。
随后以新工件和该已验收镜像接续 `forgejo-stop-r8` 的联合停止/停用/重新启用，结果单独
记录；M10/M12仍保留完整业务、VM/ARM64、正式发布及回滚等未完成范围。

2026-09-25 已定位后续镜像失败：不是guest策略拒绝或post-files失败，而是AppArmor包
维护脚本继承宿主TMPDIR，在chroot内调用mktemp时目录不存在。构建环境已固定为
chroot也可用的HOME/TMPDIR，私有cache/sources/output仍走原显式参数；先复现再修复，
不修改包脚本、额外挂载宿主目录或关闭安全策略。新镜像与真实停止链路另验，记录见
[构建chroot环境](../reviews/2026-09-25-incus-build-chroot-environment.md)。

2026-09-25 工具恢复后的构建接续：实际诊断确认Ubuntu构建Debian guest时缺少官方
keyring会让debootstrap警告后继续，现补齐构建前置验签条件及零退出不得掩盖警告的
拒绝路径。Ubuntu官方包的固定 `.gpg -> .pgp` 相对别名已由实机核实并增加精确兼容，
任意链接仍拒绝。缺keyring的真实CLI拒绝、未占用revision和进程回归已取得证据；新
镜像、自动guest策略及停止链路结果单独记录，M12不据此提前关闭。详见
[引导验签接续](../reviews/2026-09-25-incus-bootstrap-signature-validation.md)。

消费者侧继续补齐 Forgejo 管理账号生命周期：真实 Forgejo 15.0.7 与产品 helper/controller
在独立 Debian 13 amd64 VM 通过17项口令停用、同账号重新启用、重启、未知/替换账号拒绝
及不确定 controller 状态反例。Hook 的同一容器清理等待、期限下传和状态证明也已补回归。
这是 Forgejo M3 的账号子链路，不代表整个 Core/Compose 开关、真实在运行 guest 排空或
全部业务栈已完成，不改变 Incus M10/M11/M12 的整体状态。证据见
[账号生命周期接续](../reviews/2026-09-24-forgejo-actions-account-lifecycle.md)。

真实 Forgejo 接续另外发现并修复 Runner 的内部 CA 交付缺口：旧 one-job 夹具的手工
信任安装不在生产路径中。现通过同一 stdin token 通道追加校验过的有界公开 CA，仅供
单次 Runner 的 SSL_CERT_FILE 使用，不更改 guest 系统根或任意工作流镜像。默认配方
冻结新增 helper，须新不可变 revision 的独立 bake/boot/one-job 证据；详细状态见
[Runner 信任投影](../reviews/2026-09-24-forgejo-runner-trust-projection.md)，不自动关闭 M12。

该接续的实际终态已取得：新 `trust-r2` 完成真实烘焙与重复复用、9 项镜像/引擎门禁及
真实 Forgejo 五场景。没有手工 guest CA 安装，TLS 校验、隔离、资源回收和宿主最终对照
均通过。它关闭 Runner 进程的部署 CA 交付缺口，仍不是完整业务 Compose、工作流 OCI
checkout trust、正式签名发布或其他平台矩阵的替代。

第三十七轮全新 Ubuntu 26.04 amd64 已通过完整 Core 自动投影入口：五个外层阶段、主
测试与八个子项共九个 pass、撤销后拒绝的一个 pass，以及八个真实宿主作业。实际 CLI
初始化/导入/render/apply 使用生产 Hook/Provider，两个非 root 合成消费者获得独立
受限租约与控制网桥，业务网关保持。两个项目存在后的双向权限、第二份冻结部署激活后
私钥保持、清理和宿主撤销后不回退旧凭据均通过；VM 正常退出及宿主原 Docker 对照一致。
完整证据与失败链见[Core 投影接续核对](../reviews/2026-09-24-incus-core-projection-continuation.md)。
这不替代真实 Forgejo/AI Agent 部署、可启动或签名 guest 镜像、全部降级/故障恢复与
ARM64/VM 矩阵。下方按日期保留较早阶段，不把它们当作当前尚未接线的结论。

最新同工件矩阵已完成：第三十轮 Debian 13、第二十九轮 Ubuntu 26.04、第三十一轮
Ubuntu 24.04 的 amd64 环境各通过 **25 / 25 门禁、18 个作业及退出观察**。使用同一
`.8` 实验版本和经核对的源/工件摘要，三轮分别删除 6/4/3 个受管包，327/682/667 个
原有包及未托管依赖保留；26.04 对照特别预装 nftables/conntrack，实际未被移除。
三份归档、正常关机、物理宿主对照和无遗留进程/监听均已复核；这不是全部 75 项需求完成。
详细结果见[空池盘点与显式卸载接续](../reviews/2026-09-23-incus-storage-inventory-compatibility.md)。

第三十轮全新 Debian 13 amd64 的实际 CLI/HTTPS→共享 job→systemd 审批闭环已通过完整
**25 项门禁、18 个作业及退出证据**，包括真实控制桥认证/拒绝、五分钟自然过期、精确
删除六个受管包和重复卸载；327 个原有包及其他未托管依赖保留。修复了 Debian 空池清单
null 编码的只读兼容性，以及其维护脚本不停止 systemd daemon 导致删除后读回失败的问题。
没有清除失败 intent 或放宽外部服务归属；归档、正常关机与物理宿主对照均通过。
详见[空池盘点与显式卸载接续](../reviews/2026-09-23-incus-storage-inventory-compatibility.md)。
下文较早的 Debian 准备失败与第二十六轮状态是历史记录，不替代本轮真实终态。

后续已独立复核 Ubuntu 24.04 第十五轮的完整 23 项通过、14 个成功作业、公开归档摘要和
正常关机/宿主对照，证书 POST 的 base64 DER 修复有该发行版真实证明。Debian 第十六、
十七轮停在实验 Docker 环境的官方索引下载阶段，尚未运行产品审批，不冒充产品通过或
Incus 登记失败。当前证据见
[一级发行版核对](../reviews/2026-09-23-incus-distribution-matrix.md)。

浏览器第十九轮已核对真实证书，但发现维护页深链接在首次恢复 owner 会话前被临时匿名
能力列表重置。本轮补齐 Vue 响应式回归并修复实际 App 接线，前端构建与 96 项测试通过；
重新构建嵌入资源后，真实浏览器 8 项通过；旧外层收尾脚本的端口检查中断问题也已修复。
第二十二轮再次完整通过浏览器门禁、正常关机、实际 QEMU 退出 0 和物理宿主对照，终态见
[浏览器会话恢复接续](../reviews/2026-09-23-incus-browser-session-recovery.md)。

同日更早的完整结果：已安装审批入口 **23 项全部通过**，14 个共享 job 与 14 个真实 hostd 激活
相互对应。除安装/配置/登记/卸载、跨工作区和重放拒绝外，新增非 root Docker 容器的
控制桥 mTLS、错误 pin、无证书以及其他 bridge 拒绝；真实五分钟过期后的执行和旧计划
续签拒绝、新计划确认后可执行也已通过。本轮自动归档并正常关机，物理宿主全部对照相同。
源码/工件、失败与终态见
[消费者控制桥与确认过期记录](../reviews/2026-09-23-incus-consumer-control-bridge.md)。
该结果不替代受限消费者 project/配额测试、完整自动投影、浏览器重新确认交互、其余
发行版或正式签名发布；Module 仍保持 developing，M10 不整体提前关闭。

本轮继续修复原生测试实际暴露的服务期限、非 root relay 配置可读性、Ubuntu 26.04 daemon
拆包归属和网络枚举顺序误报计划漂移；对应回归先复现再修复，未清除旧 failed intent 或放宽
审批、归属、mTLS 和真实拓扑漂移校验。第五轮全新 Ubuntu 26.04 amd64 VM 的完整 **11 项**
原生后端门禁通过，实验 Docker 恢复其基线；该 VM 正常退出后，物理宿主 24 个已有容器、
17 个网络、卷、服务身份、配置、nft 和双栈路由均与本轮开始相同。
详细失败链、源码/工件摘要及终态见
[服务期限与原生供给接续](../reviews/2026-09-23-incus-service-execution-budgets.md)。

以下是主要发行版直接后端先前阶段的记录，不是完整 M10：另增
[`server-incus-host-action-e2e.py`](../../test-env/scripts/server-incus-host-action-e2e.py)
验证实际安装的 CLI/HTTPS owner、共享 job、一次性确认及 socket 激活的 systemd hostd。
独立入口使用明确的 native 实验版本与源码摘要，不冒充正式签名发行版；其审批和消费者
控制桥结果见上方最新记录，其他发行版、ARM64 和完整失败恢复仍须分别有真实证据。

本次沿用已有未提交改动，补齐宿主卸载的提前只读盘点：停止/冻结实例、池引用/卷、控制
网络端点和删除包时的共享 daemon 外部对象都须先排空。不完整清单不能当作空清单，
拒绝与取消不先撤销管理连接，也不留下不确定的变更 intent；删除阶段仍再次读回。
同时修复空网桥字段误匹配无关实例，并把 root Docker 客户端收紧到固定网络生命周期，
排除 connect/disconnect/prune 和容器操作。原生宿主门禁现有 11 项必需事件，新增保留卷
反例；不得用旧的 10 项结果替代新增验收。当前验证见
[宿主卸载接续核对](../reviews/2026-09-23-incus-host-uninstall-preflight.md)。

以下为本轮早期故障记录。真实宿主夹具首轮已通过确认拒绝与显式跳过，但安装因监督器继承的 32 MiB 文件大小限制
截断 APT 索引而失败。现已改为只限制日志管道，macOS/Linux 监督器回归通过，失败状态
与原 VM 磁盘保留，全新 VM 的完整 11 项重跑单独记录。不以删掉 failed intent 或降低
日志完整性门禁制造成功。非 root broker 的 16 项必需原生用例在内核 7.0 的隔离 VM
通过；物理宿主内核 5.15 缺少所需 `SO_PEERPIDFD`，该侧失败保留，不冒充 root/systemd
安装审批链路验收。

以下记录同日更早的原生与构建接续，不能当作本次新增用例的执行记录。

从干净 `master` / `0b6144887d6c1ecc89847be6d2773f139e8b7f23` 继续核对。新增回归先复现
Provider 合并旧 project 时保留 `restricted.devices.proxy=allow`，且 inspect 误报 ready。
实现改为两档都显式写入 `block`，ensure 收紧并读回，inspect 对缺失/漂移只读拒绝。
本机和指定 ln Linux 主机的完整 Provider 回归通过；Linux 首次执行还发现测试供给父目录
权限依赖 umask，现将夹具显式设为 0700，不放宽生产文件检查，保留原失败日志。

原可销毁 VM 生命周期入口扩展为 15 项必需通过事件，增加直接 proxy 反例和管理证书
重叠/撤销后的运行实例身份检查。本次在已有未提交改动上接续，已用指定 SSH 服务器中的
全新 Ubuntu 26.04 amd64 / Incus 6.0.5 VM 执行完整入口，15 项全部通过。旧管理证书撤销后
inspect 失败，新证书仍可 ensure/inspect，原消费者与运行实例身份不变；直接 proxy 设备
请求被 daemon 拒绝。清理回读、报告归档与 VM 正常退出已确认，不以该结果关闭整个 M6
或开放生产 ingress。详见[本轮验收记录](../reviews/2026-09-23-incus-native-and-shared-build.md)。

共享构建新发现并修复运行层绕过仓库镜像源策略的问题。三份 Dockerfile 的构建/运行基础
镜像均接入 `DOCKER_HUB_REGISTRY`，既有 builder 覆盖值优先，外部模块代理回退到
`GOPROXY_URL`；仓库共享代码离线和 Go checksum 边界不变。新增只读 `--json` 构建输入报告
及独立 VM 六次源码/staging 构建门禁，明确区分 build-only staging 与完整 Core 部署。

### 默认容器已验收范围（2026-09-22）

默认系统容器 Runner 的当前运行闭环已完成。`lab-r9` 的 cgroupfs 方案被实际限额门禁
拒绝；最终改为 engine 用户管理器内运行 Podman，保留系统侧只读文件系统与私有临时目录
限制，并修复冷启动的私有配置父目录所有权。真实 distrobuilder 的不可变 `lab-r11`
通过八个必需子项，包括 OCI create/exec 实际 0.5 CPU、128 MiB、32 PID 与 NNP=1。
真实 Forgejo 15.0.7 的正常、显式失败、controller SIGTERM 取消和 SIGKILL 后保留 state
恢复均通过；四场景结束均确认工作状态、实例、根盘和 registration 清空，未授权仓库
始终 waiting、无计算资源。详细证据和宿主收尾见
[闭环记录](../reviews/2026-09-22-incus-onejob-closeout.md)。

M4a/M5a 的验收范围是上述默认容器链路。独立 VM/ARM64、Ubuntu AppArmor 兼容性、
state volume 丢失、Forgejo 网页取消、正式签名分发、容器镜像构建工作流及生产 ingress
仍分别留在原里程碑，不能由本次 shell 作业矩阵替代；Module 继续 developing。

以下是前轮阶段记录，不表示这些故障仍是当前阻塞。

本轮完整 lab-r5 在 Ubuntu/Debian 独立 VM 中复测，已确认 image 根目录 0700 导致非 root
服务 CHDIR 失败，以及 runner-engine passwd 主组与 service GID 不同导致 newuidmap 拒绝。
配方已修复 guest 根目录与主组，保留私有归档/home 和全部 Incus 围栏。原生组合对照与真实
one-job 接续以[本轮记录](../reviews/2026-09-22-incus-onejob-runtime-completion.md)的实际终态为准。

以下为前轮引擎复测与 scope 容量修复记录。

MCP/SSH 已恢复，在 ln 新独立 QEMU VM 中原样恢复 `lab-r4` 复测。Provider 与 Runner CLI
仍通过，但 35 秒 19 次 engine 探测失败，服务保持 inactive/dead；根因尚未证明。新增
staging 空间准入和固定 boot-jobs 诊断，避免将 tmpfs 耗尽误归因给 guest。另先复现再修复
controller 在失败补偿后漏计本轮 scope 名额的问题；回归同时覆盖占位和确认清理后的释放。
详见[本轮核对](../reviews/2026-09-22-incus-engine-recheck-scope-capacity.md)。不修改旧镜像、
Podman/Incus 隔离策略或生产 gate，M5a/M12 与 30/75 不据局部结果提升。

以下为前轮 SSH 不可用时的准入切片。

本轮指定 ln 的 SSH 在握手阶段关闭，未运行新 VM 或修改已有 Docker。已补齐 guest starter
在 token 目录创建/读取前的有界 rootless engine 准入，八项 shell 行为测试和 controller
补偿场景通过，并接入 CI。原生镜像测试新增有界等待及仅固定类别的服务诊断，仍须真实
API 成功，不能用诊断放行。没有重新烘焙或改变 `lab-r4`，也没有把 exit 125 标为修复。
实际代码、门禁与远端边界见[引擎准入核对](../reviews/2026-09-22-incus-runner-engine-admission.md)；
真实 one-job、更新镜像与故障根因仍待指定通道恢复后验证，M5a/M12、30/75 和生产 gate 不变。

以下为前轮完整构建恢复切片。

恢复 `anas-runner-bake-bzsj8q` 实验机后确认 `lab-r2` 失败，补充只保留固定阶段的发布侧诊断。
`lab-r3` 指向 hooks，实际控制复现 post-files 改写 resolver 失败；配方改为 guest tmpfiles
规则并显式声明 init 包，原生打包/链接测试通过。另新增真实 Forgejo 15.0.7 repo/org API
门禁，修复客户端重定向、响应有界完整性与错误净化，并按实际 nullable queue 语义兼容。
`lab-r4` 已完成真实全量烘焙、不可变归档/复用、Provider 导入和受限 guest 启动；实际 Runner
及 one-job CLI 通过，但 rootless engine API 退出 125，整体镜像 gate 仍失败。最终真实
Forgejo repo/org API 通过；VM 已正常退出，物理 Docker 前后基线一致。完整构建、测试和收尾以
[恢复核对](../reviews/2026-09-22-incus-runner-build-recovery.md)为准；
one-job、正式发布及其他架构/隔离档仍未整体验收，M12/M5a 与 30/75 不据局部结果提升。

以下为前轮 copy 路径和构建取消切片。

在 ln 的另一新建独立 QEMU VM 中启动默认 Runner 的真实 distrobuilder 构建。发现
冻结输入位于 `sources/forgejo-runner`，而 copy generator 使用裸文件名；先复现 Go 与
原生工具反例，再修正共用配方，两档结构检查及真实打包字节正对照通过。原构建仍在基础包
下载阶段时取消，原 revision 的实际重试被拒绝，attempt 身份/内容保持不变。完整镜像尚未
产出，不将小型 pack 夹具或 Runner CLI help 算作 one-job。新增完整镜像导入/启动/engine
测试入口，完整运行待真实产物；见[构建核对](../reviews/2026-09-22-incus-runner-bake-validation.md)。
M12、M5a、30/75 与 production gate 不变。

以下为前轮 btrfs 容器生命周期切片。

继续使用 ln 的新建独立 QEMU VM，未使用业务 Docker。新增可复现的
`server-incus-lifecycle-e2e.py` 与共享客户端原生测试；实际 btrfs 池、两份 Provider 租约、
同名容器、stdin、磁盘写满、取消后的独立删除及直接超配/host disk/跨租约接网反例通过。
真实缺陷是最小安装遗漏 `dnsmasq-base`：推荐依赖被 `--no-install-recommends` 排除，网桥
创建失败；三份声明式配方已补齐，并验证预装 helper 不被卸载。完整经过、平台范围和清理
见[容器生命周期核对](../reviews/2026-09-22-incus-container-lifecycle.md)。镜像是测试夹具，
不是正式 distrobuilder/Runner，M6/M10/M11 与 30/75 统计不据此整体提升。

以下为前轮 CLI/KVM 验证切片。

操作者改为指定 `whl@ln.hlong.wang:2200`，并明确不得修改既有 Docker 服务。本轮在新私有
目录及隔离 namespace 执行 Linux 回归；宿主 Ubuntu 22.04 的 13 包中 11 包成功，宿主动作
和执行器各有失败，原始结果保留。八项强制原生网络用例通过。实际探测确认 KVM ioctl 和
独立 QEMU 启动可用；没有将 Ubuntu 22.04 纳入默认安装范围，也没有升级物理宿主内核/软件包。

独立 KVM 实验机使用 Ubuntu 26.04、1 vCPU/1 GiB 和用户态网络，不接入现有 Docker。相同
宿主动作/任务执行器二进制在干净实验机全部通过；真实 Incus CLI 首次暴露 `protocol: lxd`
配置错误，修正为 `incus` 后重复/并发初始化、错误 pin、跨 project 和撤证拒绝均通过。
未用旧配置自动覆盖来“迁移”；旧实验需新私有配置目录。另修复本机并发锁创建 ENOENT，
独占创建与已存在打开分离后连续 40 轮通过。最终本机全仓/竞态及实机证据分别记录，不互相替代。

前轮 Forgejo 取消清理、未确认创建、持久退役和三份共享构建缺依赖的修改现已验证，并补齐
双语技术说明。全量测试、清理、Docker 基线及未完成范围见
[ln Linux 验证](../reviews/2026-09-22-incus-ln-linux-validation.md)。M6/M11、30/75 和生产 gate
保持不变；QEMU 操作系统成功启动不是 ANAS `incus_vm` 产品 guest 生命周期通过。

以下为前轮 finance 受阻后共享客户端边界的历史切片。

本轮远端只读预检被工具安全检查拦截，未执行；没有绕过拦截或处理上轮四个诊断目录。转而
修复进入真实消费者验收前发现的共享客户端问题：旧实现可跟随链接覆盖外部文件、替换另一
租约凭据，并把整个消费者环境传入 CLI。先用七个顶层回归实际复现，再实现受上下文约束的
凭据锁、完整 TLS 校验、私有目录/文件身份、不覆盖读回以及最小子进程环境与输出上限；另补
四个锁/特殊文件/输出/初始化回归。Provider 和 Forgejo controller 的本机相关包回归通过。
凭据准备幂等不等于 `remote add` 或重复 `New` 已实机验证。btrfs、guest、配额、one-job 与
旧诊断状态清理仍待授权通道可执行后继续，M6/M11、30/75 和生产 gate 保持不变。核对见
[共享客户端边界](../reviews/2026-09-22-incus-client-credential-boundary.md)。

以下为前轮独立 daemon 存储验证记录。

独立 daemon 的存储 API 已在同机完成创建、读回、删除与消失确认。实机发现同步 POST 返回
HTTP 201 / envelope status_code 200，宿主客户端原来误报未确认；先在旧二进制复现，再以
限定 POST 的响应处理修复。GET/其他方法的 201、异步接受、冲突错误及任意其他 2xx 不放行。
真实 Provider 的 dir/缺失池拒绝、重复操作、缺失 project inspect 和错误 pin 通过，完整
project/network/profile/trust 库存未改变。新增固定独立 socket、私有 PID/net/mount namespace
与 tmpfs 状态的可重复原生入口；不安装系统软件包，不启用生产 ingress，不执行 guest/VM。
本轮范围和旧 CLI 超时的诊断边界见
[daemon 存储验证](../reviews/2026-09-21-incus-daemon-storage-validation.md)。

以下为前轮原生网络读回修复记录。

同机接续已修复 nft 原生读回、地址/前缀长度分列格式及 namespace 夹具污染。nft 使用符号
协议输出，拒绝歧义数字 EtherType；回包 ifindex 由只读内核 GETRULE 取证并用 GETGEN 绑定
完整 JSON，不从设备名学习替代身份。跨 netns veth 夹具保留生产数值索引检查。八项强制原生
门禁、三轮乱序批量及宿主网络包 Linux 回归已通过；包含真实错误 EtherType 和读回期间规则
generation 变化反例。完整证据、最终清理及本地门禁见
[原生读回修复](../reviews/2026-09-21-incus-native-readback-fixes.md)。生产 gate、30/75 与
M6/M11 退出条件不变；本轮未重跑 daemon 建池或实际 guest/VM 矩阵。

以下为首次指定主机执行的历史记录。

操作者指定 `whl@finance.hlong.wang` 后已实际运行 Linux 测试，不再只有 macOS 回归与交叉编译。
修正远端测试目录权限与源码映射后，12 个选定包中 11 个完整包退出成功，Runner 的 Incus 定向
和原生 broker 所需用例通过。独立 HTTP 原型 20 项行为检查通过；强制内核用例仍有 4/7 失败，
涉及 nft 读回、veth 对端和 namespace 批量干扰；未解除生产 gate。独立官方 Incus 6.0.5 仅验证
到 daemon 响应，后续建池超时；无 `/dev/kvm`，不能勾选 M6。宿主规则/容器集合/namespace 与
前置基线一致，路由仅 RA 到期时间变化。完整结果与保留证据见
[指定 Linux 主机验证](../reviews/2026-09-21-incus-finance-linux-validation.md)。

以下为前轮内部启动装配切片。

本轮补 `ControllerCoordinator.StartHostWorkspace` 与 `HostActionService.StartIngressWorkspace`。
后者使用实际服务 owner context、同一协调器和内部构造的 HostObservationInvoker，不接受外部
观察 invoker。私有文件的 scope 必须匹配启动工作区；准入成功不是首次对账或生产可用证明。

工作区读取器在认领前核验真实路径：请求、私有凭据、HTTP 日志和 Traefik 输出不可相互包含，
不能暴露整棵 Core 状态。捕获目录身份、原始注册文件与 renderer 的文件身份/内容，进入原围栏
前再复核；替换目录、同内容换 inode、脚本变更与硬链接拒绝。启动前失败只关闭本次新开读取器，
不释放其他 owner；请求目录丢失拒绝发布，但不作为旧 Traefik 清理凭据的失效条件。
这仍是受信同 owner 的内部启动装配，不是非特权进程或挂载安装器；实机开关保持关闭。见
[启动边界核对](../reviews/2026-09-21-incus-workspace-launch-boundary.md)。

以下为前轮跨进程围栏切片。

本轮恢复连接并补齐上轮文档复验。`WorkspaceReaders.NewControllerService` 将文件日志装配为
`WorkspaceStateStore`：复用现有 `.anas/state/lock`，先持久化绑定原日志目录路径摘要与设备/inode
的未清理标记，再持共享锁运行；完整排空与原读取器关闭后才清除。进程死亡释放 flock 不会清除
标记，恢复不能用缺失、损坏或另建的日志目录替代原记录。

Runner 的工作区独占写锁在等待时和成功获取后检查同一描述符，所以独立 `credential rotate`、
本地管理员轮换、直接应用写调用及锁内恢复不能绕过正在运行/待恢复的中介。它们明确拒绝，不
新增无认证的自动排空 RPC；纯共享读仍可用于诊断。该机制已接入工作区读取器装配，不是已安装
生产服务，低层裸 FileStateStore 仍仅为显式实验入口。测试和兼容边界见
[跨进程工作区围栏记录](../reviews/2026-09-21-incus-workspace-process-fence.md)。

以下为上一轮普通队列接线切片。

本轮补普通 `jobexecutor.Executor` 的领取前屏障，`anasd` 显式向普通部署/维护队列和宿主队列
注入同一个协调器。apply/start/stop/restart/rollback、本地管理员轮换及其他由该 worker 领取的
写任务都先排空目标工作区；等待不占 running 位置或工作区写锁。失败以未执行的 failed 终态
记录，取消等待不能让下一任务接管未完成清理。终态写入丢失或补偿待确认时保留原屏障。
排空前及实际执行前重查持久 actor；bootstrap/enrollment 仍只限原事务的 apply。
该路径不包含独立本地 `anas credential rotate`，也不是生产中介启动器。实现与验证见
[工作区任务排空核对](../reviews/2026-09-21-incus-workspace-mutation-gates.md)。

以下是前轮宿主配置任务切片。

配置变更不再只依赖调用方手工停止中介。共享 `HostActionService` 在确认配置任务真正开始前，
通过同一个 `ControllerCoordinator` 阻止替代实例启动并排空受影响中介；等待期间任务保持 queued，
队列仍可执行旧清理需要的只读调用。scope 配置只影响目标工作区，宿主供给/卸载/prune 排空全部
登记工作区。排空失败拒绝未开始任务，保留原中介；成功屏障绑定完整 job/调用/参数直到确认终态。

共享宿主服务的正常取消路径先关闭新配置准入，保留 broker/执行租约至中介排空；失败不自动
重试，可信所有者可显式继续清理。已加入真实 Controller/文件锁/共享队列/确认账本的联合回归，
但宿主/Traefik/探测和 root 执行仍是明确夹具。生产 launcher、跨进程恢复及实际网络验收未完成。
具体边界见[配置与排空协调核对](../reviews/2026-09-21-incus-ingress-coordination.md)。

以下为前轮中介所有者切片，其中配置协调待办由上述实现推进。

本轮将无凭据宿主观察接入现有私有配置交付、WorkspaceSource 和 Traefik 读取器；Host/direct
两种配置严格互斥，Host 模式不交付 Incus 证书，也不回退直连。增加 `ControllerService` 复用
原 Controller/journal：首次完整对账后才报告启动，停止清理失败保留同一 flock 和旧读取器，
显式重试只排空、不发布，完整清理后才关闭资源。没有启动或安装生产中介，observer 配置变更
仍须由未来安装协调器与此排空屏障连接。实际验证见
[中介所有者与配置装配核对](../reviews/2026-09-21-incus-controller-owner.md)。

以下是前轮观察配置交付切片。

本轮补 `incus.ingress.observer.plan` / `incus.ingress.observer` 配置动作。scope 由宿主根据活动
部署和已验证本机连接自动生成，不再要求运维手写授权 JSON；交付/refresh/disable 都走现有
计划确认机制。既有宿主状态记录 pending/enabled/disabled 和摘要，写入中断阻断观察，已撤销
文件回放不能复活。CLI/HTTP observer 分支、OpenAPI 与生成类型同步，卸载要求先撤销 scope。
这不是默认启动生产中介，也不将自动生成配置称为原生验收完成。详见
[配置交付与撤销核对](../reviews/2026-09-21-incus-observer-configuration.md)。

以下为前轮观察处理器切片；其中 scope 自动交付待办由上述实现推进。

本轮将 `incus.ingress.observe_http` 注册到既有编译宿主动作清单，复用共享 job、对端/执行绑定、
审计与终态监督，没有增加监听器或通用命令入口。Linux 后端读取固定 observer scope、受管宿主
连接 bundle 和已登记工作区，持有现有共享锁，核对活动 Core 快照后，通过固定回环 mTLS 与本机
只读 veth 查询取得新事实。管理凭据不交给中介；这不是签发了一张 daemon 只读证书。

投影升级为 v3，补 workload 和隔离档绑定；共享 job 在入队、恢复与执行绑定处拒绝跨工作区 scope。
`HostProjectionReader` / `HostObservationInvoker` 已连接无凭据中介和共享服务的调用路径。
scope 仍需受保护的显式安装输入，尚无自动交付/轮换或生产中介启动，生产发布开关不变。具体边界与
验证见[受限宿主观察接线](../reviews/2026-09-21-incus-host-observation-wiring.md)。

以下观测与生命周期段落为上一轮切片，root 处理器未接线的历史表述由本段更新。

观测与生命周期续作保留前轮镜像供给的已暂存/未暂存修改，未切换分支、提交或推送。
先实际复现未托管/错误 workload 被接受、安装授权可被调用方扩大、JSON 大小写字段混淆、旧
宿主观测响应重放、取消后仍接受结果，以及路由消费期间身份改变后仍报成功，再修复相应调用路径。
读取器接入执行器独立 Observer；暂停/停止/恢复与失败清理使用真实 mTLS 和文件 journal 联合回归。
这不是服务端只读证书供给、root 观测处理器或生产服务装配；这些缺口继续保留，生产 ingress 不启用。
本轮细节与实际检查见[观测与生命周期核对](../reviews/2026-09-21-incus-observation-lifecycle.md)。

以下是前轮镜像供给切片，验证结果不能自动沿用为上述代码的结果。

从干净 `claude/forgejo-docs-audit-20260920` / `3f5242e` 直接继续实现，未切换分支、提交或推送。
实际先复现重复镜像 staging 冲突、unified 片段越界、取消被忽略、供给元数据漂移和导出缺少二次
哈希的问题，再补实现与回归。发布侧新增完整历史 bundle，原发布脚本使用同一入口，避免目录引用
历史镜像但只导出本轮两份工件。合成字节集成覆盖归档、打包、冻结选择和 Core 供给，没有伪造真实镜像。

Provider 同时修复就绪误报：登记证书前精确读回全部受管 project 围栏与四项配额；profile 配置和
设备属性严格匹配。完整只读 inspect 核对网络、profile、受限证书和冻结镜像，撤销证书后保留 project
但不再 ready。Incus 包修订号提升为 `7.3.0-r2`，不改变上游版本、持久格式或 `developing` 状态。
详细反例、变更与实际验证见[镜像供给与就绪性核对](../reviews/2026-09-21-incus-image-supply-readiness.md)。

生产 ingress 的完整生命周期、独立身份、VM/TAP、health 与服务装配仍有实现缺口；正式镜像信任链、
真实 Linux/Incus/KVM/双栈/one-job/回滚/prune 验收也未完成。不得仅因本轮本机回归而标记文档全部完成。

### 历史接续状态（2026-09-20）

本次接续在 bridge prerouting 增加原始 ifindex/MAC/获批端口绑定的回复入口，和 inet 许可同事务
创建/续租/撤销；两侧均须读回。conntrack 改为完整双向元组与默认 zone 的精确清理，许可未撤销
不得删连接。新增真实包与 conntrack 原生测试源，但本轮只编译，没有 native 通过证据。
完整生命周期、独立 Incus 身份供给、VM/TAP、health 与生产装配仍待完成；见
[回复来源接续核对](../reviews/2026-09-20-incus-reply-origin.md)。以下地址段落保留前一切片的范围。

本次继续补 `.address-routing.json`、宿主独立 policy/table、设备绑定 `/32` 与永久邻居，接入
hold/route/permit/inventory/release。通过终止 unreachable 防止旧设备路由消失后沿普通 bridge 路由
跟随地址复用，不是 DHCP reservation。故障测试复现并修复地址创建失败缺少 publication 回执导致的
普通撤销阻塞；新增原生 FIB 反例加入必跑清单，尚未原生执行。
当前仅为 container veth 前向候选，不覆盖反向旧连接或 VM/TAP；真实身份、health 与服务装配仍未完成。
精确变更与本轮验证见[地址路由接续核对](../reviews/2026-09-20-incus-address-routing.md)。

前轮 MCP 中断后的文档已在本轮补写并读回；当前核对与本轮结果见
[实现恢复记录](../reviews/2026-09-20-incus-ingress-recovery.md)。以下日期小节保留历史，不代表当前代码均未测试。

当前 `anasd` 保留需求规定的 root/root 身份，通过固定安装策略和独立 systemd 进程身份准入，
不再要求非 root TLS/状态迁移。历史切片中的该前置判断已撤回。安装器、CLI/Web 计划/确认/
执行、共享 Store 与五分钟确认 ledger 已连接；参数从结构体进入 job map 后的规范化往返已补回归。

宿主供给代码生成并验证专用官方 APT 配置，按实际资源读回后记录归属；控制防火墙只作用于受管
bridge/endpoint，不影响无关宿主 IPv6 转发。私有 bundle 携带宿主观察架构和存储池，Incus Hook
自动导入既有 Secret Store。完整显式远端配置仍独立处理；自动来源在恢复 Env 后也必须重新检查
原 bundle，不能用旧凭据掩盖撤销或漂移。

Provider 及 Forgejo/AI Agent 的 compute 服务现在经受管资源投影连接相同控制网桥，业务网络
仍是默认出口；Compose 静态解析和真实网络可达性分别记录。镜像供给使用冻结 catalog 和原字节，
没有生成虚假 fingerprint，也没有把镜像工件完整性当成 guest 启动成功。

显式镜像 prune 的 plan/apply、一次性确认、锁定工作区视图、引用保护和删除读回已有代码，真实删除未验收。
入站已编码完整身份回执、可取消锁、真实 Docker/procfs/nsfs 核验及 opened-netns 执行器。
本轮补受管 nft 基线、先许可后拒绝、有界原生 JSON、精确 AST 与独立 installing/installed/removing/removed
归属记录；专项及竞态回归通过，namespace/nft 原生门禁仍待执行。
尚未完成的实现包含生产 ingress 的真实地址分配生命周期、health 身份与入口装配、正式签名镜像发布、
部分失败 intent 的受限恢复及非 systemd 启动；另有真实 Linux/Incus/KVM/双栈/one-job 验收。状态保持
`developing`，不得将这些代码接线作为整项需求完成。实际检查见
[中断恢复与集成回归](../reviews/2026-09-19-incus-integration-recovery.md)。

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

### 1.1 2026-09-18：直接工作树修复与实际验证

- 已修复动作 ABI 两份测试的同名 helper 编译冲突，以及 dispatcher 测试目录未显式满足 0700
  导致的失败；没有降低正式 Secret/Store 权限检查。Linux Module 执行改用 daemon-owned
  `CommandContext` 守卫启动，保留现有 pidfd/进程组监督，未给子进程清单门禁加例外。
- 已修复真实配置到 Hook 的证书变量断层：规范 `*_CERTIFICATE_B64` 校验后派生运行时
  `*_CERT_B64`，不以原始环境别名替代缺失的规范输入。endpoint 与消费者连接/证书投影纳入
  敏感配置；Provider 拒绝重定向、异步确认、超限响应和不安全 endpoint，错误不回显服务端原文。
- 已增加 `ArtifactArchive.BuildOnce` 及显式 `incus-image-artifacts build`；原生 Linux root
  发布构建机预检、sealed ELF、冻结配方、持久尝试、同 revision 复用与失败保留均已编码。
  本机回归通过，但真实 distrobuilder 未执行，也未将构建入口加入 apply 或宿主动作。
- 已补 HTTP 策略/Planner 与 executor 事务测试：固定认证、命名冲突、绕过客户端的恶意字段、
  旧 reservation、每步发布/撤销故障、无回执副作用、未知外部工件及地址保留等本机回归通过。
  这些测试使用内存适配器，不证明真实 nft/conntrack/Traefik 状态。

实测环境为 macOS arm64、Go 1.26.6；Docker CLI 存在但 daemon 不可用，没有本机 Incus/KVM。
相关 Linux 测试源码已对 amd64/arm64 交叉编译，但不能据此登记原生执行通过。
`check-shared-build` 静态校验通过；全仓门禁中的 `check-upgrade-tests` 另发现已有 `ai_agent`
缺少升级测试登记，该项不属于 Incus 实机证据，未通过删除 Module 或放宽门禁规避。
全仓测试、竞态、文档与最后工作树核验结果统一记录于本轮 review。

剩余实施顺序不变：统一动作 ABI 的产品入口迁移与宿主通道 → 安装/盘点/卸载及控制网络发布 →
生产入站的受信观察、地址保留、防火墙和路由适配 → 默认 guest 配方、签名分发/导入/回滚恢复 →
发行版及双 interface 实机矩阵。保留范围的长驻实例、TCP/UDP 和其他发行版不得借本轮提前启用。

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
- [x] 写入后读回全部受管 project 围栏与四项 limits 的精确总量，不满足即 fail closed 且不登记证书；
- [x] 证书登记拒绝两类越权：已被无限制信任的证书、已绑定其他 project 的证书；
- [x] 配额映射：Contract 的每实例上限乘以 `max_instances` 写成 project 总量；
- [x] project 封禁 device/raw config/挂载/低层配置；`incus_container` 额外写入
      `restricted.containers.privilege=unprivileged`；
- [x] 显式收紧并读回 `restricted.devices.proxy=block`，避免合并时保留历史 `allow`；两档回归及默认容器直接 daemon 反例通过，完整 VM 设备矩阵仍待验收；
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

## 6. M4/M4a：共享 Incus 客户端库（默认容器验收已完成）

- [x] 共享包位于 `internal/computeclient`，消费者 Dockerfile 从命名 `shared` context 复制源码，
      用 Go module 模式与 `GOPROXY=off` 编译，不从网络拉取共享包；
- [x] create/start/exec/stop/delete/list/janitor 由共享库实现；Forgejo 保留业务适配层；
- [x] 读取消费者租约，校验镜像摘要、实例前缀、配额和 guest 入口 allowlist；
- [x] 实例请求不开放 device、raw config、挂载或 profile 覆盖；Secret 经 stdin；
- [x] 默认容器档真实 exec 取消和运行中 controller SIGTERM 的独立清理已验收，证据登记到 §10；超时/失败补偿已有回归。
- [ ] 独立 VM 及完整平台/故障运行矩阵仍归 M6，不能由默认容器结果替代。

验收：要求文档 §7 第 1—5 条。取消的原生证据与补偿单测分别登记，不混为同一矩阵。

## 7. M5/M5a：Forgejo 迁移（默认容器 one-job 已验收）

- [x] controller 使用 `internal/computeclient` 并读取消费者私有租约环境变量；
- [x] manifest 通过 `dependencies.contracts` 与 `resources.requires` 接入 compute，默认
      `incus_container`，VM 由消费者显式选择；
- [x] 保留既有 sandbox 名 `anas-forgejo-runners`，不要求迁移数据或重建 project；
- [x] 默认容器真实 one-job 验证 ephemeral 注册、stdin token、正常/失败后的销毁、SIGTERM 取消及 SIGKILL 后保留 state 的恢复回收。
- [ ] VM/ARM64、state volume 丢失、网页取消和容器镜像构建工作流仍须分别验收。

验收：要求文档 §7 第 6—7 条。Forgejo 自身的应用与身份验收仍归其私有计划。

## 8. M6：真实宿主验收（默认容器部分完成，完整矩阵待验收）

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
- [x] 从源码 checkout 和 staging 布局分别验证实际 Compose 解析及构建（2026-09-23，三镜像六次无缓存构建）；保留真实 build 输入，不冒充完整 Core 渲染部署；
- [x] 在 Module 双语技术文档中给出覆盖值应指向匹配版本的完整仓库源码根的绝对路径示例，注明源码不存在时不能本地构建（2026-09-18）；命令模板未作为实构验收运行。

R-084 从 M8 移到此处；退出条件是覆盖路径可用且文档同步，不能只凭变量存在判定完成。

2026-09-18 增加 `check-shared-build --source-root /... --staging-root /...` 静态预检与
`staging_test.go`：强制显式绝对 shared context，对实际存在的共享构建 Module 核对 Compose、
Dockerfile、构建目录及 shared_paths 的文件集合、字节和执行位；没有匹配模块、源码漂移、特殊文件
和被枚举到的符号链接都拒绝。它不验证完整 deployment manifest、不加载部署环境，也不调用 Docker。
当日测试源码已编写但未运行，静态预检没有代替两条路径的实际构建。

2026-09-23 在独立 Ubuntu 26.04 / Docker 29.1.3 / Compose 2.40.3 VM 中完成三镜像的
source/staging 六次 `--no-cache` 构建。实际 Compose 解析、缺少 shared override 的静态与
真实构建反例、非 root 镜像探针、输入复核、业务二进制及固定 Incus CLI 摘要一致性均通过。
测试镜像与容器清理、测试 daemon 停止和报告归档已确认，M8b/R-084 可关闭。
该门禁使用 build-only staging，不覆盖完整 `anas build/apply`、Hook、Provider/guest 或
正式镜像发布，详见[验收记录](../reviews/2026-09-23-incus-native-and-shared-build.md)。

### 8.3 M8：长驻实例与 image_policy 扩展预留

本轮不实现长驻实例、任意命令、持久卷或 `image_policy: any`。保持当前拒绝边界；进入该阶段前，
先补生命周期、持久卷归属、删除/恢复和独立命令开关设计，再安排 R-041/R-042 的真实验收。
R-046 只在未来启用 `any` 时验收，不把它作为本轮阻塞。

### 8.4 M9：IPv6 与默认隔离档

现有实现已提供 IPv6 姿态、bridge NAT、默认容器档及拒绝 `any` 的行为。
剩余为双栈宿主实际出网验证：IPv6 开关关闭、无全局地址、有全局地址三种情况；结果归 §10。

### 8.5 M10：宿主供给

同日接续补 `OpenSystemdActivation` 的固定安装策略/已接受 fd 校验、拒绝审计，及唯一 job
store 的 `HostJobBinding`。CLI `anas host actions` 只显示本机编译清单，不开放宿主写动作。
发现现有 `anasd.service` root 身份与非 root peer 准入冲突，保持显式阻塞，不放宽验证；跨进程
broker、实际进程监督与服务账号迁移仍待交付。详见
[本次接续记录](../reviews/2026-09-19-host-action-activation-job-binding.md)，不据此完成安装或实机验收。

2026-09-19 新增 `internal/incushost`、`cmd/incus-host-preflight` 和宿主通道内部边界。
一级三发行版按官方包目录核对并进入编译期 JSON 表；精确 ID/版本/架构匹配，不 source shell，
不凭 ID_LIKE 接纳衍生发行版。默认容器、显式 VM、跳过和未适配关闭分开表达；包版本观察不代表
daemon 运行兼容或安全准入，所有预检结果仍不启用 compute。

新代码单元及 race 已通过；Linux 文件读取和 SO_PEERCRED 用例单独跟踪，不以 macOS 结果替代。
`hostaction` 仅有只读预检处理器，复用 action ABI 和既有审计，不启动 root 服务、不登记写动作。
细节及验证记录见[本轮核对](../reviews/2026-09-19-incus-host-preflight-implementation.md)；
安装/配置/卸载、服务单元、包签名/来源、job 接线和实机验收仍未交付，M10 不标完成。

前置设计见宿主供给架构 §3.5—§3.8、§7。安装与配置依赖宿主特权动作通道；其未落地前不自动
安装或启动宿主服务，不增设收集 sudo 密码的入口。

同日接续已将只读激活处理器与跨进程 job 绑定传输连接，加入双向内核身份检查、固定私有端点、
执行后权限复核及进程尚未退出时的执行租约保留。尚无生产 listener、非 root 所有者迁移、
实际退出码/完整进程树证据或写动作；本机协议测试不能替代这些实现及实机验收。新增原生
socket/子进程门禁要求关键用例实际执行，记录见
[跨进程 broker 核对](../reviews/2026-09-19-host-job-broker-implementation.md)。M10 与 30/75 统计不变。

后续已补固定私有 listener、32 绑定/8 连接上限和共享 Store 分派；socket 不创建或重建任务。
退休需终态与远端清理，授权后失败保持执行租约并停止准入；相关 native 脚本已接入 Go CI。
见[监听与分派核对](../reviews/2026-09-19-host-job-broker-listener.md)。服务迁移/生产装配、
真实退出状态监督、root 程序及宿主写动作仍未交付，不计作 M10 完成或真实宿主验收。

同日最新接续已新增 systemd 独立退出观察和 `ExecutePreflight` 的共享 recorder 终态接线，
`anas-hostd` 与候选 socket/service 单元进入同版本打包；缺证据时沿用持久 containment barrier。
上一段的退出适配器和 root 程序缺口已编码，但非 root 服务迁移、安装器、公共入队入口、二段
确认及 Incus 写动作仍未交付。实际验证见
[退出与终态核对](../reviews/2026-09-19-host-action-exit-completion.md)，不据此改变 M10 或 30/75。

后续已将只读 `incus.status` 接入可选 HTTP 入队及 anasd 同进程共享队列，复用原 Store/lease/
授权/审计；配置默认关闭。root-owned 0640 的非 root 配置读取不改变 TLS 私钥的 root-only 政策。
生产仍缺 TLS/状态/权限迁移、安装器、CLI invoke 和 Incus 写动作；详见
[队列与 HTTP 接续核对](../reviews/2026-09-19-host-action-queue-http.md)。不把预检 job 成功解释为 compute ready。

2026-09-18 已新增 `modules/incus/control-relay` 非 root 传输组件：固定连接 `127.0.0.1:8443`，
只绑定安装配置指定的控制接口 IPv4/高位端口，不加载 TLS 私钥或任意 upstream。代码包含 root 所有的
配置逐级无符号链接读取、专用 UID/GID/capabilities 检查、源网段过滤、连接容量/空闲期限、双向半关闭、
停止清理及运行中接口漂移检查；配置与传输测试源码已补，均未运行。它不是第三个特权入口，也未接入
`configure/uninstall`、官方发布产物、服务单元、INPUT/FORWARD、endpoint 投影或生产启动。重启后
接口 index 变化须由宿主协调重新验证网络归属并生成配置，不允许静默接管同名新接口。

- [ ] 运行固定控制转发的配置/传输/身份测试，以及独立 Linux 宿主的 mTLS/pin/project、来源网络隔离、重启与卸载验收；

- [ ] 核验首批三个发行版的官方包、服务单元、版本与架构，形成声明式安装表；
- [ ] 按架构 §3.7 细化的独立控制 bridge、固定目的地非 root 转发和双栈防火墙实现原型，完成真实连通验证与宿主动作清单；
- [x] 实现探测、安装、配置、登记、读回验证和状态记录，区分已存在的外部 daemon 与 ANAS 所有资源；主要发行版直接后端与已安装审批闭环均通过，其他发行版单独验收；
- [ ] 定义跳过、部分失败、重试和卸载的状态迁移，保证可选消费者关闭而主部署可继续；
- [x] 卸载先清点受管资源与运行实例，不删除非 ANAS 资源；真实保留卷拒绝、原包保留/逐包归属、确认后卸载与重复卸载通过，不代表完整故障恢复矩阵；
- [x] 三个一级发行版 amd64 使用同批产品工件，分别完成新版 25 项实际审批、控制桥、精确包删除/原包保留与重复卸载；各 18 个共享 job 和独立退出、正常 VM 收尾及宿主对照通过；
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
| R-007 | `test-env/scripts/server-incus-lifecycle-e2e.py` | Ubuntu 26.04 / Incus 6.0.5 / amd64 / btrfs 容器夹具 | 2026-09-22 | 同名双租约实际并行运行及独立清理通过；产品/VM 矩阵待验收 |
| R-011 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，受限证书直接变更请求 | 2026-09-22 | host disk 与其他租约网络拒绝、原配置不变；完整设备/VM 矩阵待验收 |
| R-011 | `test-env/scripts/server-incus-fence-e2e.py` | 6.0.5 / 7.0.1 / 7.5.1 一次性 VM，放宽的旧 project | 2026-09-26 | 完整 restricted 键收紧、清除键删除、inspect 只读拒绝；daemon 以实例冲突拒绝收紧（特权容器、换档）；7.x 存储池越界与 7.5 VM nesting 被拒，见[实机核验](../reviews/2026-09-26-incus-fence-native-validation.md) |
| R-025 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，真实运行 exec 取消 | 2026-09-22 | 取消错误、独立删除及另一租约保持运行通过；controller 迟到创建/crash 待验收 |
| R-031 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，两份独立证书与相同实例名 | 2026-09-22 | 各自只读回自己的 workload；全平台/产品镜像矩阵待验收 |
| R-008 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，绕过共享库参数校验 | 2026-09-22 | 直接 CPU/内存/磁盘超配及跨租约 NIC 请求被拒绝；完整围栏矩阵待验收 |
| R-008 | `test-env/scripts/server-incus-fence-e2e.py` | 同上三档，受限证书直接请求 | 2026-09-26 | 容器档拒绝 VM、VM 档拒绝容器（`Reached maximum number of instances of type`），同档请求通过项目准入；VM 实际启动未验收，见[实机核验](../reviews/2026-09-26-incus-fence-native-validation.md) |
| R-029 | `test-env/scripts/server-incus-lifecycle-e2e.py` 的 `management-certificate-rotation` 子项 | 独立 Ubuntu 26.04 amd64 / Incus 6.0.5 / 运行中双租约 | 2026-09-23 | 前轮 15 项原生门禁通过，旧证书拒绝、新证书可管理且 guest 身份不变；不代表 Core 自动轮换事务或 VM/ARM64 |
| R-030 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，实际 Provider 与共享客户端 | 2026-09-22 | ensure/重复/inspect → 容器 create/start/exec/stop/delete 通过；正式镜像与 VM 档待验收 |
| R-032 | `test-env/scripts/server-incus-lifecycle-e2e.py` | 同上，实例数限制及 btrfs 4 GiB 根盘 | 2026-09-22 | 实例数与直接超配拒绝、实际写满通过；ZFS/VM/正式镜像待验收 |
| R-033 | 待新增 `test-env/scripts/server-incus-janitor-e2e.sh` | 强制中断消费者 | — | 待执行 |
| R-034 | 待新增 `test-env/scripts/server-incus-baseline.sh` | 目标 NAS 规格、两档 | — | 待执行 |
| R-027 | 待新增 `test-env/scripts/server-forgejo-one-job-e2e.sh` | Forgejo + Incus 宿主 | — | 待执行 |
| R-041 | 待新增 `test-env/scripts/server-incus-longlived-e2e.sh` | 长驻档持久卷与入站（第三阶段） | — | 待执行 |
| R-042 | 待新增 `test-env/scripts/server-incus-longlived-e2e.sh` | 长驻档下既有边界不被削弱 | — | 待执行 |
| R-044 | 待新增 `test-env/scripts/server-incus-network-e2e.sh` | 双栈宿主上的租约网络出网 | — | 待执行 |
| R-047 | `test-env/scripts/server-incus-host-provision-e2e.py`、`server-incus-host-action-e2e.py`、`server-incus-core-projection-e2e.py` | 三个一级发行版 amd64 宿主审批；26.04 真实 Core CLI/Hook/Provider/Compose 双合成消费者 | 2026-09-24 | 三档宿主各 25 项通过；Core 五阶段、主流程九项与撤销一项通过，无需手填连接/架构；正式安装器及实际业务部署另验 |
| R-048 | `test-env/scripts/server-incus-host-provision-e2e.py`、`server-incus-host-action-e2e.py`、`server-incus-core-projection-e2e.py` | 跳过、重复执行、保留卷拒绝、两种卸载及 Core 新冻结部署/撤销 | 2026-09-24 | 三档精确删包/保留原包/重复卸载通过；Core 新版本实际激活保持凭据、撤销后拒绝旧凭据且私有 Store 不变；完整降级/失败恢复仍未完成 |
| R-050 | `test-env/scripts/server-incus-host-action-e2e.py` 与 `server-incus-core-projection-e2e.py` | 回环 daemon、实际控制桥及 Core 自动投影的两个非 root 消费者 | 2026-09-24 | 宿主传输正反例与 Core 自身租约/既存项目隔离通过，业务网关优先级不变；完整 LAN/双栈及实际 Forgejo/AI Agent 部署另验 |
| R-053 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | Traefik 公网双栈、受管 guest IPv4 后端 | — | 待执行 |
| R-054 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | Traefik 发布实例内 HTTP 服务 | — | 待执行 |
| R-055 | 待新增 `test-env/scripts/server-incus-image-bake-e2e.sh` | 首次烘焙与二次 apply 不重建 | — | 待执行 |
| R-057 | `test-env/scripts/server-incus-host-action-e2e.py` | Debian 13 / Ubuntu 24.04 / 26.04 三档 amd64 | 2026-09-23 | 三档同工件各 25 项审批/正常供给卸载通过；其他架构、故障恢复与正式默认部署不由此推导 |
| R-094 | 待新增 `test-env/scripts/server-incus-host-setup-e2e.sh` | 未适配发行版上保持关闭而非失败 | — | 待执行 |
| R-063 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 实例启停与 Traefik 路由增删同步 | — | 待执行 |
| R-087 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 消费者无法注册本租约命名空间之外的域名 | — | 待执行 |
| R-088 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 绕过客户端库直写请求文件仍被拒绝 | — | 待执行 |
| R-095 | 待新增 `test-env/scripts/server-incus-ingress-e2e.sh` | 运行时无法覆盖租约声明的认证方式 | — | 待执行 |
| R-072 | 待新增 `test-env/scripts/server-incus-image-bake-e2e.sh` | prune 保留上一个 deployment 的镜像 | — | 待执行 |
| R-101 | 待接续 `test-env/scripts/server-forgejo-stop-e2e.py` | 真实宿主审批、Core 租约与默认 Docker 下的 guest/Forgejo | — | 待执行；确认事务单元通过不替代产品联合验收 |
| R-102 | `internal/incusingresshost/forwarding_native_linux_test.go`；真实 guest 场景待接续 | 全新 Ubuntu 26.04 amd64 / 默认 Docker DROP | 2026-09-26 | 内核精确许可、物理 veth 源冒用拒绝再次通过；端点是 namespace，真实租约/跨租约替换待验收 |
| R-103 | `internal/incusingresshost/forwarding_native_linux_test.go`、失败撤回回归；真实 stop/revoke 待接续 | 新连接拒绝、原已建立 socket 及精确 conntrack 分开核验 | 2026-09-26 | 内核新连接/原连接撤销通过，启用后失败统一补偿回归通过；完整业务停止与租约撤销未关闭 |
| R-104 | `internal/incusprovision/forwarding_retirement_native_linux_test.go`；业务入口待接续 | 实际安装后端、Incus trust/空实例/网桥，Core stopped 和旧 grant 为夹具 | 2026-09-26 | 原始拒绝、自然空清单、显式/重复退役及墓碑/依赖解除通过；地址/接口变化、崩溃/重启和完整宿主卸载仍待验收 |
| R-105 | `test-env/scripts/server-incus-forwarding-e2e.py` 与 `internal/incusingresshost/forwarding_native_linux_test.go` | 独立默认 Docker、既有管理员拒绝和原始策略保留 | 2026-09-25 | 原9项临时许可因果实验通过；候选生产执行器完整正反向验收仍待完成 |
| R-106 | `test-env/scripts/server-incus-fence-e2e.py` | 一次性 VM：Ubuntu 26.04 官方 6.0.5、Debian 13 trixie-backports 7.0.1、Ubuntu 26.04 Zabbly 7.5.1 | 2026-09-26 | 三档 16/18/19 项必需检查全部通过：第二工作区与所有者撤销后均拒绝、未标记共享 project 拒绝、额外受限证书使 inspect 未就绪、`default` 拒绝；真实 Core 多工作区部署未验收，见[实机核验](../reviews/2026-09-26-incus-fence-native-validation.md) |
| R-108 | 待扩展 `test-env/scripts/server-incus-host-provision-e2e.py` | 三个一级发行版 amd64，Zabbly `lts-7.0` 固定来源与钉包 | — | 待执行；编译配置、密钥指纹与钉包单元回归通过（2026-09-26）；本日 7.0.1 实机仅为 fence 围栏验证，非宿主供给验收 |

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
2. M8b staging 构建验证已完成；继续 M10 宿主原生生命周期与 M11/M6 剩余矩阵，不得因此宣称完整入站可用。
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
