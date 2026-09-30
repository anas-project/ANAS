# Workspace 临时存储设计评审

状态：历史评审记录，基于 2026-09-30 的 `9a6921a1` 加当前未提交工作树；仅提出建议，未修改需求、计划或实现。

评审对象：[临时存储要求](../requirements/workspace-temp-storage.md)与[实施计划](../plans/workspace-temp-storage.md)。两份文件目前均为提案，本评审不把“功能尚未实现”本身当作缺陷，也不把此前 Collabora 主机诊断当作本轮实机验证。

## 1. 结论

总体方向合理：默认 workspace 内存储、显式外部根、按所有者隔离、先登记租约再启动、停止全部 Module 后切换、不复制临时内容、失败补偿、清理失败不回滚新服务，都值得保留。全量重启是已经确定的产品行为，本评审不建议改回后台迁移或长期双根运行。

初审记录了 **3 项 P1 和 2 项 P2 设计缺口**。经同日讨论，F1 的 Core 保存确认建议撤回，F2 的通用应用就绪门槛撤回，F3 的历史回滚策略已明确；按操作者澄清的 Module 临时目录范围，F5 的 Runner 全量接管建议退出本功能。F4 的简化建议是固定 Docker 程序及连接地址，仍处于方案讨论。处理状态以本文 §5（特别是 §5.6）的最新补充为准。以下保留初审分析，不能把已撤回的建议继续作为实施前置条件。

这里的 P1 表示应在相关切换/清理功能开放前解决，P2 表示应在首版范围与契约定稿时补齐。以下风险是设计推演；现有代码用于证明复用路径的实际语义，不表示已经复现新功能的数据丢失。

## 2. 主要发现

### F1 · P1：会话退出不足以证明内容已保存，缺少清理前的应用确认

处理状态：同日讨论后撤回要求 Core 新增应用保存确认、会话排空及残留树隔离流程的建议；保存责任归容器内应用，见 §5。下文为初审建议，不再作为本功能阻塞。

**位置：**需求 §3、`TEMP-R-031`、`TEMP-R-033`；计划 M1/M4（第 28、34 行）。

正文要求正常停止和保存，矩阵 R-031 却只要求旧会话退出。进程被终止、连接断开或容器消失，都可能发生在保存失败之后；如果这些信号就能解除租约，R-033 会进一步删除最后一份未保存文档。R-034 暂停 Runner 的新使用者，也没有说明怎样阻止浏览器重连并建立新的应用会话。

当前可复用的停止链路是 `stopModules → beforeStopModule → compose down`：

- [lifecycle_select.go](../../internal/runner/lifecycle_select.go) 第 141—162 行调用停止屏障后执行 `down`。
- [hook.go](../../internal/runner/hook.go) 第 185—238 行已有显式 opt-in 的 `before_stop` 阶段；失败会阻止继续拆除依赖。
- [Collabora Hook](../../modules/collabora/hook/main.go) 第 95—116 行没有实现该阶段，[Module 声明](../../modules/collabora/module.yml)也没有声明该屏障。

Compose 的停止宽限期届满后会发送 SIGKILL，默认宽限期为 10 秒；因此不能把“调用正常停止命令”当成保存保证。[Docker 官方停止语义](https://docs.docker.com/reference/compose-file/services/#stop_grace_period)

**建议：**为清理资格补一条独立需求：先阻止新编辑会话，再取得目标镜像可验证的保存/排空成功证据；超时、保存失败或证据未知均不授予清理资格。优先接入已有 `before_stop`，避免另造停止协议。失败补偿重新启动旧实例时也不能让应用启动清理抹掉待恢复内容；必要时先隔离保留该实例的残留树，再用新实例目录启动。若目标镜像无法提供自动确认，明确返回需要处理的阻塞状态，不能降级为“容器已退出”。

**补充验收：**编辑中切换；WOPI 保存失败；保存超过停止超时；客户端持续重连；停止屏障失败后 Nextcloud 等依赖仍可用；补偿启动后保留的未保存内容仍在。仅测试保存成功后正常关闭，不覆盖这些风险。

### F2 · P1：提交切换与删除旧目录所需的“验证通过”尚无判据

处理状态：当前已具备依赖顺序、逐 Module 启动链路和 workspace 排他锁。进一步讨论后撤回把应用全面就绪作为临时路径切换门槛的建议；目录及挂载正确性仍需验证，启动失败沿用现有失败处理，见 §5.6。

**位置：**需求第 32 行、`TEMP-R-016`、`TEMP-R-033`；计划第 28—29 行。

文档把“全部 Module 通过启动验证”作为不可逆清理的前置条件，但没有定义验证层级、超时与失败信号。当前 [startModuleContainers](../../internal/runner/runner.go) 第 1268—1277 行执行 `compose up -d --remove-orphans`，没有等待健康就绪；[startDeployment](../../internal/runner/deployment.go) 第 1028—1047 行随后运行凭据及 `after_start` 屏障。Collabora 的 `after_start` 当前为空，健康探针又是背景诊断中已经不能反映编辑可用性的 `coolwsd --probe`。

直接复用这条成功返回路径，就可能在新容器稍后因权限、jail 创建或实际挂载错误失败前，已经提交新路径并删除旧树。开发时跑过一次真实编辑 E2E，不能替代每次运行切换时的验证。

**建议：**在需求中定义切换提交条件，至少包括实际 Docker 挂载与登记一致、以应用有效 UID/GID 在目标目录完成受控读写探测、声明的容量/文件系统条件仍成立、各 Module 的就绪屏障在期限内通过。Collabora 还需明确能识别 jail/document 创建失败的运行验证；具体方式由目标镜像试验决定。就绪成功应作为单独的持久阶段，先于 applied 状态提交和旧树清理。

**补充验收：**`up -d` 返回成功后再退出；健康探针成功但文档临时树不可写；挂载错根；验证阶段取消；最后一个 Module 验证失败。以上均应保留旧内容并进入补偿，不得清理。

### F3 · P1：不可变 deployment 与重新分配租约之间缺少运行绑定规则

处理状态：操作者已明确历史回滚复用修改配置时的临时路径切换策略，不另建回滚专用策略，见 §5。具体接入及既有恢复隔离要求属于后续实施验收。

**位置：**需求第 30、44 行、`TEMP-R-009`、`TEMP-R-015`、`TEMP-R-027`；计划 M0/M1/M3。

文档同时要求按容器实例分配目录、deployment 不可变、切换后删除旧树、恢复时重新分配，但没有说明物理挂载路径冻结在哪里，以及历史 deployment 再次启动时怎样得到有效路径。

这不是仅靠“备份排除 tmp”能解决的：[snapshot.go](../../internal/runner/snapshot.go) 第 379—384 行原样复制 deployment；[backup_restore.go](../../internal/runner/backup_restore.go) 第 149—167、198—202 行恢复制品并提示后续 `anas start`；[runner.go](../../internal/runner/runner.go) 第 1324—1357 行只迁移旧 workspace 路径前缀，不能重写 workspace 外的临时绝对路径。

例如 A 路径切到 B 并清理 A 后，再回滚历史 deployment：若照旧 `.env` 挂载，可能失败、重新创建已退休路径，或绕过新租约登记。跨宿主恢复时，即使外部路径文本相同，也不能把源实例目录重新交给目标实例。文档只覆盖切换当次失败补偿，未定义切换成功后的显式历史回滚。

**建议：**明确区分不可变的临时目录声明/desired root 与可重新建立的实际运行绑定。可复用 `relocateDeploymentEnv` 所在的运行投影边界，但不能只是对任意环境值做字符串替换；绑定需关联 workspace 临时身份、deployment、实例代次与声明名。所有启动入口（apply、start、restart、rollback、恢复后启动及补偿）都需走同一分配/验证入口。还必须决策历史回滚是否恢复历史 temp_path；无论选哪种，都要明确计划展示、全量停启和旧盘缺失时的失败行为。

**补充验收：**A→B→历史回滚；GC 后启动旧制品；跨宿主恢复且外部路径同名；新宿主无旧盘时修改配置完成恢复；容器自动重启沿用有效租约、受控重建建立新代次。全程验证旧制品摘要不变，且源 workspace 的目录不被挂载或清理。

### F4 · P2：文件系统和容器使用者没有绑定到同一个 Docker 宿主

**位置：**需求 §2、`TEMP-R-006`、`TEMP-R-011`、`TEMP-R-017`、`TEMP-R-023`；计划 M0/M2。

当前登记字段只有文件系统身份和使用者，没有约束宿主/daemon 身份与路径命名空间。[endpoint.go](../../internal/compose/endpoint.go) 第 8—29 行保留 Docker endpoint 选择；[Compose 执行边界要求](../requirements/compose-execution-boundary.md) §2 也保留 CLI 的 `DOCKER_HOST` 和默认 context 语义。不能假定每次调用都观察同一个 daemon。

Docker bind mount 使用 daemon 宿主的路径。若 Runner 在 A 上检查容量、权限和标记，实际容器由 B 启动，A 上的预检不能证明 B 挂载正确；即使都在本机，下一次 GC 改用了另一个 daemon，也不能把“没有找到容器”解释为原租约已释放。[Docker 官方 bind mount 边界](https://docs.docker.com/engine/storage/bind-mounts/#considerations-and-constraints)

**建议：**首版明确支持的执行拓扑。可以先只支持能够证明 Runner 与 Docker 共享受管路径命名空间的本地 Linux 部署；对无法证明的拓扑在停服务前拒绝，并说明 Docker Desktop、远程 daemon、rootless/userns 的支持边界。租约绑定 host/daemon 身份和容器 ID；GC 查询原 daemon，原 daemon 不可达或身份变化时保留并报告。仅保存 context 名称或看到 Unix socket 不足以证明身份。

**补充验收：**两个 daemon 具有同名容器；切换 context 后 GC；远程宿主同名目录；原 daemon 不可达。除非正式纳入首版支持，无需为这些环境扩展远程存储管理器。

### F5 · P2：Runner 临时写入只有盘点任务，没有可验收的接管范围

处理状态：操作者进一步澄清范围为 Module 内类似 Collabora 的专用临时目录。下文针对 Runner 系统临时文件的统一接管建议退出本功能；原需求与计划中较宽的 job/测试产物范围建议随后同步收窄，见 §5.6。

**位置：**需求第 16—18 行、`TEMP-R-007`、`TEMP-R-009`、`TEMP-R-018`；计划第 27 行。

范围包含 Runner 操作，M0 却只要求“盘点现有临时写入”和提供分配能力，没有指定哪些现有调用必须改用租约。这样可以完成分配器与 Collabora 接入，但实际 Runner 仍写系统盘或 `.anas/tmp`，也仍可能立即删除本应保留 24 小时的失败产物。

已核对的具体入口：

| 入口 | 当前行为 | 需要决策的接管边界 |
| --- | --- | --- |
| [module_validation.go](../../internal/runner/module_validation.go) 第 84—88 行 | 系统临时目录；退出时无条件删除 | validation/build 工作树及失败保留策略 |
| [config_application.go](../../internal/runner/config_application.go) 第 1247—1256 行 | 配置解析中间文件写系统临时目录 | 优先内存解析，或显式受管分配 |
| [config_import.go](../../internal/runner/config_import.go) 第 1003—1009 行 | 导入校验中间文件写系统临时目录 | init/import 尚未有有效 temp_path 时的引导规则 |
| [compute_image_supply.go](../../internal/runner/compute_image_supply.go) 第 149—165 行 | 固定 `.anas/tmp`，自带清理 | 供给目录的容器引用与 job 生命周期 |
| [hook.go](../../internal/runner/hook.go) 第 455—473 行 | validation 子进程继承 TMPDIR/TMP/TEMP | 每个子进程显式注入，避免修改 daemon 进程全局环境 |

**建议：**为首版列出入口清单与例外，把“这些生产调用使用受管分配和统一终结策略”写入需求矩阵并分配里程碑。原子替换继续在目标旁创建，持久缓存维持独立生命周期。另写明旧临时盘不可用时，诊断、修改 temp_path 和恢复操作所需的最小控制路径，防止修复配置本身依赖失效临时盘；这不应变成普通 job 静默回退系统 tmp 的后门。

**补充验收：**把调用者 TMPDIR 指向不可写位置，验证声明纳管的成功路径仍能工作；让受管根不可用，验证普通 job 明确失败、诊断和配置修复按约定可用；取消/失败后按清单检查保留策略。不要为满足 24 小时保留而无必要地增加敏感配置副本。

## 3. 需要补充的决策与实施顺序

这些是文档已留白的细化项或建议，不另计为已观察缺陷。

| 主题 | 建议补充 |
| --- | --- |
| 安全路径集合 | M0 不只排除业务目录及祖先，还要处理其后代、`.anas` 元数据、deployment、snapshot/backup 目的地，以及新旧受管树之间的嵌套；按解析后的目录身份检查。新根位于待回收旧实例树内必须拒绝。共享父根本身不必禁止，清理边界始终是登记的受管子树。 |
| 文件系统身份 | 已列为 M0 阻塞的首次磁盘证明需定稿；稳定存储身份与本次挂载实例分开。保护也应覆盖默认 `<workspace>/tmp` 被独立挂载的情形。只比较路径文本或跨重启不稳定的设备号不够。 |
| 克隆与移动 | 现有 Compose owner 是工作目录推导的路径，不是可直接复用的临时 UUID。定义支持的克隆/恢复入口如何签发新身份，以及原地恢复、移动目录、原样复制 `.anas` 如何识别或拒绝；不能承诺靠复制后的两个相同标记自动分辨源与克隆。 |
| 切换锁与准入 | 明确 CLI、anasd、Module command、备份/恢复、GC 共用的锁顺序。当前 Apply 在 materialize 前已取得 workspace 排他锁（`deployment_application.go:237`）；计划却写先生成制品再取排他权。应择一并说明快照校验。等待旧 job 时不能持有其释放租约所需的锁，也不能把切换 job 自身算作待排空使用者。 |
| 同次配置变更 | 全量停止集合取旧 active deployment，启动集合取新目标，分别使用各自依赖顺序；覆盖同时禁用/启用 Module 的情况。明确同时升级版本或执行不可逆业务迁移时，temp_path 补偿不能自动保证业务数据回退。可以要求路径切换与这类变更拆开执行。 |
| 空间与状态 | 补字节/inode 下限的单位、默认值、Runner job 默认值、未知统计的处理、检查触发点，以及状态恢复的判据。区分“最低空闲阈值”和“启动所需增量”，对同一文件系统的启动预算按声明语义处理，不宣称普通目录能够保证容量。 |
| 清理与错误契约 | 规定部分成功、等待超时、所有者未知、文件系统缺失的稳定 JSON 字段/错误码；重试复核实际状态。GC 不应把 stopped 但可再次启动的容器当成已解除绑定，需区分停止与销毁/退休。 |

建议调整实施依赖：

1. **先做目标 Collabora 镜像的小型可行性验证，再冻结 M0 声明。** 当前 M4 才验证 child-roots 同文件系统、权限和启动清理；结果可能要求多个目录共同初始化，影响 M0 的通用声明。上游配置明确提到 child_root_path 应与模板处于同一文件系统且初始为空，但本轮只核对了上游 main，不能代替仓库固定镜像 `26.04.2.4.1` 的验证。[Collabora 配置来源](https://raw.githubusercontent.com/CollaboraOnline/online.mirror/main/coolwsd.xml.in)
2. **M0 建立路径、身份、租约和具体 Runner 接管清单。** 同时定稿运行绑定与恢复模型，避免到 M3 才发现制品里的路径不可迁移。
3. **把 M2 中删除前核验、异常对账、空间预检的安全原语作为 M1 前置依赖。** 显式 GC 命令和展示可以后做；M1 的旧树删除不能先有一套临时清理实现，再由 M2 另做一套。
4. **M1 在保存、就绪、持久提交三个条件明确后开放切换；M3 恢复回归作为首版开放门槛。** 保留现有完整 E2E 清单，并加入 F1—F5 的反例。
5. **M5 复用已有远程测试目录授权。** `internal/remotetest/profile.go` 已有 `remote_work_root`，应说明它与本次受管租约的关系，以及宿主共享挂载/Incus 引用由哪个管理器负责释放，避免另立一套可删除判定。

文档本身还建议补上“本矩阵是规范来源，正文是解释；冲突以矩阵为准”，以及计划头部“当前没有独立架构文档”的说明。计划应指向需求判据，避免继续复制整段约束；新增需求使用新 ID，并同步归属、E2E 记录和索引。

## 4. 验证与边界

已完成：

- 阅读需求、计划、原始诊断与相关现行源码；核对停止屏障、启动链路、不可变制品、恢复路径、锁和临时文件调用。
- `npm run docs:check-requirements`：通过；本主题 39 项需求归属及 E2E 记录一致；全仓用例 catalog 校验通过。
- `npm run docs:check-requirement-status`、`npm run docs:check-plan-status`、`npm run docs:check-status`：通过。
- `npm run docs:build`：通过；有 bundle 超过 500 kB 的体积提示，无构建错误。
- 本评审 18 个本地链接均可解析，Markdown 空白检查及评审索引 `git diff --check` 通过。

本轮只新增本评审及评审索引条目。未修改需求/计划成员、矩阵或里程碑状态，因此无需重生成需求与计划索引。未运行功能单元测试、Docker/Collabora 实机、低磁盘或挂载攻击 E2E；这些功能尚未实现，文档门禁不能替代验收。

## 5. 同日讨论后的修正与说明

### 5.1 停止保存由容器内应用负责

采纳操作者的责任划分：容器内应用负责正确处理停止信号、完成正常保存；Module 负责正确的信号传递和适当的停止等待配置。Workspace 临时存储功能复用现有停止入口及目录引用核验，不新增理解文档内容、调用 WOPI 保存或管理编辑会话的通用流程。撤回 F1 作为 Core P1 阻塞的建议，初审 §3 第 4 项的保存条件也不再要求新增 Core 保存协议。

技术事实仍须区分：Docker 发送停止信号并等待，超时后可以强杀；Docker 本身不承诺应用数据无损。这里采用的是应用应满足的正常停机契约，不是认定 Collabora 目标镜像已通过验证。保存失败、强杀、OOM 等应用异常的可靠性问题由 Module 自身测试与修复。[Docker 停止说明](https://docs.docker.com/reference/cli/docker/container/stop/)

### 5.2 启动与重启已有顺序和排他锁

现有路径为 CLI `runActive` → 共享服务 `ExecuteLifecycle` → workspace 排他锁 → 计算依赖集合 → 停止/启动 → 写回运行状态 → 释放锁。CLI 与管理服务使用同一个执行入口；该锁也由 apply、rollback 等写操作复用。

- [lifecycle_select.go](../../internal/runner/lifecycle_select.go) 第 58—122 行：start 自动包含依赖；stop/restart 自动包含受影响的下游依赖者。停止逆序，启动正序；指定叶子 Module 的 restart 不会无条件重启所有上游。
- [deployment_application.go](../../internal/runner/deployment_application.go) 第 389—450 行：在执行前取得排他锁，restart 先完成选定范围的停止，再启动；停止返回错误则不进入启动。
- [deployment.go](../../internal/runner/deployment.go) 第 1897、1945—1999 行：使用 `.anas/state/lock` 上的 `flock(LOCK_EX)` 实现跨进程排他，等待可取消，作用域是单个 workspace。
- 同文件第 1028—1047 行：逐 Module 完成 Resource 准备、容器启动、凭据处理和 `after_start` 后，再处理下一个 Module。

因此“没有则补充”的条件不成立，不新增执行锁或编排层。锁约束遵守该协议的 ANAS 入口，不阻止操作者直接执行 Docker 命令。F2 剩余问题是 `compose up -d` 与部分空 `after_start` 不能普遍证明应用就绪；应在现有链路内明确所需检查。

### 5.3 历史回滚复用修改配置的路径切换策略

按操作者决定：回滚目标中的 temp_path 与当前已应用路径不同，就按既有配置修改策略执行目标预检、全量停止、在目标根分配新临时目录、重建启动、成功后清理已释放的旧目录；不恢复或复制历史临时内容，不重用历史租约。路径相同则不因本项额外触发全量切换。目标旧盘缺失等情况沿用同一预检失败语义。

这是临时存储的切换规则；不改变其他配置或业务数据原有的回滚语义。现有不可变制品不改写，具体如何把新目录交给启动链路在接入该共同流程时实现，不再把历史回滚列为待选产品方案。

### 5.4 Docker 宿主问题的具体含义

“同一宿主运行两个 Docker daemon”是一个例子，例如系统服务与用户自己的 rootless daemon；核心问题是运行和清理时可能连到不同 Docker 服务。也可能同一 Docker CLI 通过 context 或 DOCKER_HOST 连接另一台机器。后一种情况下，本地检查的目录与远程容器实际挂载的目录不一定是同一个。[Docker 挂载边界](https://docs.docker.com/engine/storage/bind-mounts/#considerations-and-constraints)

该风险不要求首版支持多 Docker 或远程部署。最简单的候选范围是固定一个本地 Docker 服务；清理时沿用原绑定，连接变化或无法确认时拒绝清理。这个支持范围仍是建议，操作者本轮只是要求解释，未据此扩展实现范围。

### 5.5 Runner 接管范围问题的具体含义

`global.temp_path` 是配置值，新增它不会自动改掉现有 Go 调用。例如 `os.MkdirTemp("", ...)` 仍按进程环境使用系统临时目录，compute image supply 仍显式使用 `.anas/tmp`。因此只实现新目录分配器与 Collabora 挂载，系统盘上的其他临时写入仍可能继续。

F5 要求的是一张明确的接入清单及完成标准：哪些现有调用改用配置路径，哪些是例外。大体积 Module 校验/构建及镜像供给工作目录应优先核对；配置解析能用内存就无需落盘；用于原子替换的中间文件保留在目标文件旁；长期缓存不自动归入临时清理。不需要为此另建服务或通用框架。

本节复核运行了 `internal/runner` 中现有的生命周期依赖选择、预览变更拒绝与运行锁测试，全部通过；未新增测试或修改实现，未执行 Docker 实机。修订后的 21 个本地链接和文档空白检查通过。§4 的“未运行功能单元测试”记录初审时点，本节补充此次定向复核结果。

### 5.6 后续澄清：切换判据、Docker 路径与 Module 范围

**临时路径切换不以应用全面可用为新增前置条件。** 容器启动后应用仍在初始化，不妨碍挂载已经切到新路径；与存储无关的服务健康问题单独报告。撤回 F2 要求 Core 新增通用应用就绪验证的建议。仍保留本功能直接需要的目标目录/文件系统/权限预检、实际挂载核验、旧引用释放确认，以及现有启动失败处理。Module 原有启动钩子和失败语义照常执行；Collabora 真实编辑往返作为实现验收，不要求每次修改路径都执行一遍。

**init 固定 Docker 执行文件路径可行，但不能单独固定 Docker 服务。** `/usr/bin/docker` 是客户端程序，同一程序仍可通过 DOCKER_HOST、DOCKER_CONTEXT 或默认 context 连接不同服务；DOCKER_CONTEXT 还会覆盖 DOCKER_HOST。[Docker 客户端环境变量](https://docs.docker.com/reference/cli/docker/#environment-variables)

简化建议是在初始化或首次 Docker 绑定时记录执行文件绝对路径和实际本地连接地址（例如 `unix:///var/run/docker.sock`），后续所有 Compose、inspect 和清理调用复用该绑定，隔离调用者环境里的冲突选择项。持久信息可复用 workspace 运行状态，环境变量只是每次启动子进程时的传递方式；仅在 init 进程中设置环境变量不会影响之后的独立命令。若使用自定义的程序路径变量，Runner 必须显式读取并用它执行命令，Docker 不会自动理解它。恢复到新宿主时重新确认本机路径和连接。当前 `internal/compose/compose.go` 仍通过 PATH 查找程序，`endpoint.go` 只冻结一次命令的环境，尚未实现上述 workspace 持久绑定。本段是对可行性的建议，不表示已决定或实现新的 Docker 配置契约。

**范围以 Module 显式声明为准。** 操作者的目标是像 Collabora 文档临时树这样的 Module 专用目录，而非所有 Runner 临时文件。推荐判据为“Module 明确声明该目录可在容器停止并解除引用后丢弃，且希望交给 workspace 存储管理”；不单凭名字、是否位于 `/tmp` 或文件年龄识别。

具体建议是：Module 声明名称、容器内目标路径及必要权限/文件系统条件；Runner 在统一临时根下为该 Module 分配隔离宿主目录，并注入挂载。Collabora 是首个使用者，实际布局仍需验证目标镜像对模板、jail 同文件系统和目录初始化的要求。未声明的容器目录保持现状。今后若某个 Module 也希望管理自身 `/tmp`，可以显式声明，不需要另外设计一类系统。

按这个范围，原需求/计划中的 Runner 系统临时文件统一、失败 job 24 小时保留和远程测试产物接入建议移出本功能；保留 Module 目录登记、停启切换、已释放目录清理及备份排除。原子替换文件、构建缓存等不因此迁移。此前 F5 列举的 Runner 调用不再是本功能的缺口。此轮只修正评审结论，未直接重写需求矩阵或实施计划。

### 5.7 已确定结论同步到需求与计划

操作者随后要求完善需求和实施文档，已更新[需求](../requirements/workspace-temp-storage.md)及[实施计划](../plans/workspace-temp-storage.md)：Module 显式声明范围、应用正常停机责任、复用既有顺序与锁、存储切换判据和历史回滚策略均已入文；Runner job 与远程测试范围退出。矩阵保留 5 个废弃 ID，新增 R-040—R-045，合计 40 条有效需求；原 M5 撤销。Docker init 固定程序与连接的候选方案保留在计划待决策项，不改变已生效的 Compose 连接契约。

需求/计划索引已重新生成，文档门禁与站点构建通过。此处的完成指文档同步，临时存储实现及真实验收仍未开始。
