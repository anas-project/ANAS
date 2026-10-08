---
doc_type: research
status: proposed
created: 2026-10-03
updated: 2026-10-03
evidence_as_of: 2026-10-03
---

# Docker 与 Podman 双环境兼容性

本文供 ANAS 维护者判断是否以及如何增加 Podman 宿主支持。结论是**可以改造，推荐保留 Docker Compose，通过 Podman 的 Docker API 兼容接口接入，优先支持本机 Linux rootful Podman**。

状态：选型建议，尚未实施，也没有完成 Podman 宿主实测。代码依据是本次读取的工作树（HEAD `c7891162`，包含既有未提交变更）；上游固定版本证据与滚动文档分别标注。本文不宣布新的正式支持版本，不替代后续需求矩阵和实施计划。

## 1. “同时兼容”的范围

推荐让同一套 ANAS 代码、Module、镜像和 Compose 文件可以选择 Docker 或 Podman。一次执行和一个活动 workspace 只绑定一个引擎；同一个 Module 的容器不跨两个引擎编排。现有 Docker 行为保持默认。

| 场景 | 建议范围 | 原因 |
| --- | --- | --- |
| Linux Docker Engine | 保留现有支持 | 作为每次兼容改造的回归基线 |
| Linux rootful Podman（以 root 运行） | 首选新增目标 | 更接近现有宿主网络、文件权限和特权操作需求 |
| Linux rootless Podman（普通用户运行） | 另行评估应用子集 | 现有 Samba 文件服务需要宿主 macvlan，不能据此承诺完整 NAS 功能 |
| 同一宿主分别部署 Docker/Podman workspace | 条件支持候选 | 需要不同 workspace、端口、网络和持久数据；宿主动作还有全局资源冲突，不能只靠容器前缀隔离 |
| 同一 workspace 热切换引擎或混合运行 | 不纳入首轮 | 会改变容器、网络、挂载引用与恢复对象的归属 |
| macOS/Windows Podman machine、远程 Podman | 不纳入首轮 | 当前 NAS 宿主网络及临时目录要求本机 Linux 路径和网络语义 |

Podman 官方支持通过 `DOCKER_HOST` 将 Docker API 客户端接到 Podman socket；rootful 默认 socket 为 `/run/podman/podman.sock`。官方把兼容层描述为 Docker v1.40 API，并说明不会仅因请求版本不支持而拒绝请求。因此“接口可以连通”不能证明所需字段和行为等价。[Podman API 服务](https://docs.podman.io/en/latest/markdown/podman-system-service.1.html)

## 2. 当前代码已经具备什么，还依赖什么

| 位置 | 当前事实 | 对 Podman 的影响 |
| --- | --- | --- |
| `internal/compose/compose.go` | 探测 `docker compose`、`docker-compose`；生命周期走 Compose 子进程 | 可复用编排，但必须锁定实际 Compose provider 和版本 |
| `internal/compose/endpoint.go` | 冻结 Docker 选择环境，防止 deployment 变量覆盖端点 | 有复用基础；仍需把所有辅助操作绑定到同一端点 |
| `internal/runner/process_environment.go` | daemon 作业基础环境只取 `PATH/HOME/LANG`，不直接继承 `DOCKER_HOST` | 只在 shell 中设置变量不足以让 CLI 与控制台一致使用 Podman |
| `internal/runner/compose_scope.go` | 用 Docker CLI 和 `com.docker.compose.project.working_dir` 检查 workspace 归属 | 不能切换 Compose provider 后忽略缺失标签；必须保留拒绝未知归属的行为 |
| `internal/runner/network.go` | 直接调用 Docker 建立/检查 macvlan，并校验 `AuxiliaryAddresses` | 需要处理 Podman 网络参数和 inspect 结果差异 |
| `internal/runner/temp_storage_docker.go` | 使用 `docker info` 的 `ID`，并枚举全部容器挂载引用 | 引擎 ID 稳定性是明确的兼容阻碍，不能仅修改 socket |
| `modules/*/hook`、Runner 的 `docker cp` | 多处直接调用 Docker 的 exec、inspect、stop、restart、cp | 要统一传入受信任的端点；采用 Docker 客户端兼容路径可保留大部分调用 |
| `modules/traefik/docker-compose.yml` | 已支持 `DOCKER_SOCKET_PATH`；保留 Docker provider 与容器标签 | socket 挂载已有入口，仍需验证发现、事件、网络 IP 和路由 |
| `internal/incusprovision` | 固定 `/run/docker.sock`、Docker 网络 API v1.44、专属网络选项和 `After=docker.service` | 需要独立的宿主网络适配，不能被普通 Compose 冒烟测试覆盖 |
| `modules/forgejo/runner-image` | Runner 来宾内部已有 Podman 及专门的权限/资源限制验证 | 是不同运行层，不代表 ANAS 宿主已兼容 Podman |

现有 [Compose 执行边界计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/compose-execution-boundary.md)已经指出端点解析与辅助调用尚未完全收口。新增支持应接续这项工作，避免再建立一套互相独立的端点配置。

## 3. 候选路径比较

| 路径 | 收益 | 代价与判断 |
| --- | --- | --- |
| **Docker CLI + Docker Compose + Podman API socket** | 保留 Compose 语义、Hook 调用和大部分输出解析 | **推荐**；只把确认存在的语义差异放进小范围适配，仍需安装 Docker 客户端和 Compose，但无需 Docker daemon |
| 原生 Podman CLI + `podman-compose` | 宿主可以完全不装 Docker 客户端 | 要重做 CLI 输出、参数、标签和 provider 差异验证；适合以后明确要求移除 Docker 客户端时评估 |
| 全部转为 Quadlet/systemd 单元 | 改由 systemd 管理容器 | 会扩大到现有生命周期、依赖、回滚和 Module 渲染模型，不适合作为本轮兼容改造 |

`podman compose` 本身只是外部 provider 的包装器，可能执行 `docker-compose`，也可能执行 `podman-compose`；默认优先前者。不能把这个命令名当成稳定一致的编排实现，也不能认为 shell alias 会覆盖 Go 的 `exec.Command("docker", ...)`。[Podman Compose 文档](https://docs.podman.io/en/latest/markdown/podman-compose.1.html)

推荐依赖组合是 Podman、固定版本的 Docker CLI、固定版本的 Docker Compose，以及所选发行版提供的 Podman 网络组件。不给“所有 Podman 5.x/6.x + 任意 Compose”作兼容承诺；先实测一组明确版本，再扩展支持矩阵。

## 4. 建议的改造边界

### 4.1 端点选择与命令执行

在现有 Compose 执行上下文上扩展少量运行时信息：引擎类型、规范化端点、rootful/rootless、Compose 实现、版本和已验证能力。这里列的是拟议内部信息，不是当前可用的配置键。

- 由 CLI 入口或 `anasd` 的受信任宿主配置选定端点；Module 配置和 Hook 输出不能改变它。
- 一次作业解析一次，并传给 Compose、owner 检查、网络操作、容器复制、Hook、状态查询和临时存储检查。不能让其中一部分回落到默认 Docker daemon。
- 保留 Docker 用户现有 context 行为；在 Podman 路径上明确消除 `DOCKER_CONTEXT` 与 `DOCKER_HOST` 的冲突。Traefik socket 挂载也由同一结果派生。
- 检查实际服务器身份，不能只根据是否安装 `podman` 或 socket 文件名推断引擎。端点失效时立即报错，不自动换用另一个引擎。
- 活动 workspace 保留引擎归属，换引擎必须走迁移流程。首轮不设计一个 `anasd` 同时调度多种引擎的控制平面。

普通容器操作继续使用现有 Docker CLI；只有证实无法通过兼容 API 表达的网络和身份能力，才增加定点的 Podman 实现。若增加原生 CLI 调用，它也必须显式绑定同一服务端，不能依赖当前登录用户的默认 Podman 存储。

### 4.2 临时存储：先解决稳定身份

当前代码把 `docker info` 返回的 `ID` 保存到临时目录租约中，后续观察不一致就返回 `docker_daemon_identity_changed`。已核验的 **Podman v5.8.3** `GetInfo` 实现每次填入 `uuid.New().String()`。据此可推断：直接使用该接口会使现有租约身份检查反复失败；这属于源码推断，尚未在 ANAS + Podman 上复现。[固定版本源码](https://raw.githubusercontent.com/containers/podman/v5.8.3/pkg/api/handlers/compat/info.go)

因此，Podman 稳定身份是首个可行性验证门槛。候选做法是在引擎内保存一个专用持久标记，并与规范化端点、执行身份和存储根绑定；重启保持身份，重建或标记丢失则拒绝清理。该候选增加一个持久对象，其创建、遗失和旧租约处理尚需设计评审，本文不将它定为已确认实现。

不能通过去掉 ID 比较、只比较 socket 路径，或将未知引擎当成“无容器”来获得表面兼容。完整容器清单、停止容器的挂载引用、目录标记验证和不确定时拒绝删除，仍是必须保留的安全性质。身份方案未通过前，不能宣布带受管临时目录的 Module 已兼容。

### 4.3 LAN 网络与 Incus

Podman 支持 bridge、macvlan 和 ipvlan，但 rootless 的 macvlan/ipvlan 无法访问宿主网卡。这是完整 NAS 首轮选择 rootful 的具体理由。[Podman 网络文档](https://docs.podman.io/en/latest/markdown/podman-network-create.1.html)

Samba 文件服务使用静态 LAN 地址和宿主 macvlan 辅助接口。现有 Runner 创建时传 `--aux-address`，检查时要求读回该地址。核验的 Podman `main` 兼容层源码转换 IPAM 时没有处理这项地址保留，且把 `parent` 放入原生接口字段；现有字段逐项比较不能假定成立。该证据是滚动源码，不作为所有发行版的结论。PoC 必须对锁定版本验证地址排除、网关、IP 范围、宿主路由和重复 apply；不能简单删除现有检查。[Podman 网络兼容层源码](https://raw.githubusercontent.com/containers/podman/main/pkg/api/handlers/compat/networks.go)

Incus 宿主配置还要求关闭容器互通和地址伪装，并校验桥名与实际接口。上述滚动兼容层只翻译部分 Docker 网络选项，对其他选项返回 warning；现有 ANAS 又会拒绝非空 warning。因此 `enable_icc=false`、`enable_ip_masquerade=false` 不能靠改 socket 获得原有隔离。

建议为 Incus 的网络创建、观察和启动顺序增加有限的 Podman 分支，复用现有具名宿主动作与 nftables 检查。必须实测桥何时出现、容器停止及宿主重启后是否仍可供 Incus 监听，以及隔离规则是否实际生效。若必须改为宿主独立维护网桥，需单独讨论其生命周期与恢复成本后再定案。完成之前，Podman 路径应明确拒绝尚未兼容的 Incus 宿主配置动作；不能据普通应用可运行就声明全功能支持。

### 4.4 Module、入口和存储

- 保留一份 Compose 源文件；仅对已证实的引擎差异使用小范围渲染差异，不复制整套 `podman-compose.yml`。
- 继续使用 Traefik Docker provider 和既有标签，挂载选定的 Podman socket。Traefik 官方说明其端点、事件监听和网络选择机制，但这不是其对 ANAS/Podman 组合的验收背书；需验证动态路由、健康状态、容器重建与 host-network 回源。[Traefik Docker provider](https://doc.traefik.io/traefik/reference/install-configuration/providers/docker/)
- 在 SELinux enforcing 宿主上逐类处理绑定目录。共享证书/事件目录和单容器私有数据的标签需求不同；不要批量添加私有 `:Z`，也不要全局禁用 SELinux。Podman 文档明确区分共享 `:z` 与私有 `:Z`，且不应随意重新标记系统目录。[卷标签说明](https://docs.podman.io/en/latest/markdown/podman-run.1.html)
- socket 的 `:ro` 挂载不等于只读 API 授权。沿用受信任 socket 边界；SELinux 下 socket 访问的处理应限定在确有需要的容器。
- 校验固定 UID/GID、文件权限、ACL/xattr、`read_only`、`tmpfs`、健康检查和一次性初始化容器；身份认证、数据库 Resource、备份和升级的应用契约不应因引擎改变而重写。

### 4.5 构建、启动恢复与测试工具

运行预构建镜像与本地构建是两个支持项。仓库已有 `build.additional_contexts`，因此拉取并启动成功不能推出 `anas build` 也兼容。建议先验证预构建镜像路径；本地构建要单独验证 Compose/BuildKit 与 Podman 的边界，必要时评估一个有限的原生构建入口，并明确镜像最终进入哪个引擎。尚未支持时应在开始构建前报错，不暗中启动 Docker daemon。

多个 Module 使用 `restart: unless-stopped`。Podman 当前文档把重启后的容器恢复交给 `podman-restart.service`；需按锁定版本验证“运行中重启会恢复”和“显式停止后重启不会恢复”，以及 ANAS 临时挂载与网络先就绪的启动顺序。只启用 API socket 不等于完成开机恢复。[Podman 重启策略](https://docs.podman.io/en/latest/markdown/podman-run.1.html#restart-policy)

`internal/remotetest` 和现有测试脚本有 Docker 专用的 daemon/socket 隔离逻辑。Podman API 进程通常共享所属用户的容器存储，不能通过启动第二个 API 进程就假设得到独立测试引擎。优先使用专用 Linux VM 验证，记录内核、Podman、Compose、网络后端及 SELinux 状态。

## 5. 可行性验证问题

下面是决定能否正式立项和扩大范围的 PoC 清单，不是已经通过的验收记录。

| 验证面 | 必须观察的结果 |
| --- | --- |
| 两个引擎都存在 | 同一个作业的所有查询与变更只到选定引擎；CLI 与控制台一致 |
| 普通应用链路 | Traefik、PostgreSQL、一个 IAM Provider 和应用完成启动、登录、读写、重启、停止 |
| workspace 隔离 | 同名 project、未知 owner、缺失标签均不会误操作其他 workspace |
| 临时存储 | 连续观察与 API/宿主重启不误判身份；切换、失败补偿、回滚和引用阻断全部成立 |
| Samba/LAN | AD DNS、文件访问、macvlan 静态地址、宿主回程、重复 apply 和网络清理正确 |
| Incus | 控制网桥、API 可达范围、nftables 隔离、启动顺序和宿主重启均实测成立 |
| 存储权限 | SELinux enforcing、共享目录、UID/GID、ACL/xattr、备份恢复的数据往返正确 |
| 重启恢复 | 运行中的服务恢复；已停止服务不复活；临时目录准备早于容器恢复 |
| 构建与发布 | 预构建镜像和本地构建分别记录结果；额外构建上下文及目标镜像存储可确认 |
| 回归与版本 | Docker 原路径继续通过；Podman 结果绑定具体发行版和组件版本 |

不按“容器能启动”给全部 Module 打兼容标记。基础应用通过只证明基础应用范围；Samba、受管临时目录和 Incus 的门槛都通过后，才有依据扩大到完整 NAS 功能。amd64、arm64 也应分别积累真实运行证据。

## 6. 现有 Docker 部署如何迁移

新增引擎支持与迁移现有 workspace 是两项工作。建议首轮在新 workspace 验证 Podman，然后使用现有应用一致性备份与恢复能力迁移数据；切换前停止旧引擎上的写入，在确认新环境后再转移入口。

Docker 与 Podman 不能同时写同一份数据库目录。镜像、容器和命名卷属于各自引擎，不能假设修改端点就会出现；应明确重新拉取镜像以及数据恢复的范围。回退也必须考虑切换后的新写入，不能简单重新启动旧数据库。

若要求原 workspace 原地迁移，需先定义 deployment 归属、临时目录租约、网络及备份恢复的转换规则，再单独立项。本文不提供未经实测的生产切换命令。

## 7. 结论与未决项

推荐采用 Docker 客户端/Compose 复用方案，新增范围先锁定为本机 Linux rootful Podman 和预构建镜像。改造重点是受信任端点贯穿、稳定引擎身份、网络语义、权限和真实生命周期验证；大部分 Module 的业务配置可继续复用。

立项前需要用小规模 PoC 收敛三项未知：Podman 稳定身份方案、Samba macvlan 参数与观察等价性、Incus 控制网桥的隔离及生命周期。rootless 全功能、完全移除 Docker 客户端、本地构建全面兼容和原地引擎迁移均不应隐含进入同一承诺。

本次仅完成代码静态检查和上游文档/源码核验；未安装 Podman、未操作宿主服务、未运行双引擎 E2E，也未修改实现或既有需求状态。
