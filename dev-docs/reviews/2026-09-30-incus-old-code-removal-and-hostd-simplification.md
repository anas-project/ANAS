---
doc_type: review
status: current
created: 2026-09-30
updated: 2026-09-30
---

# 入站旧实现删除与宿主通道身份链简化

状态：实施记录。基线：`7ad1876a` 加累积工作树（含另一任务尚未提交的 Incus 原生测试与脚本改动，本轮未触碰）。
对应操作者 2026-09-30 的要求：删除不在需求范围内的代码，实施[设计简化评审](2026-09-28-incus-design-simplification-review.md)
第 4、5 项，并在本轮完成实机测试。

## 结论

- 已删除逐实例转发许可与逐发布 HTTP 入站运行时的全部实现。连同下一条删掉的 broker 部分，本轮共删除 225 个文件：
  非测试 Go 约 2.35 万行、测试 Go 约 1.36 万行，另有 2,405 行脚本与夹具。
- 已实施第 4 项：宿主通道不再证明对端是哪一个 root 进程，回连 broker、PID 1 私有总线核验与 systemd 退出观察删除，
  改由 hostd 自己的调用记录决定任务终态（`HOSTACT-R-016`）。
- 第 5 项（Incus 直接监听控制网桥网关、去掉控制转发服务）已实施：ln 恢复后的实机探测确认了启动顺序的前提
  （[租约网络探测](2026-09-30-incus-lease-network-probe.md)），见 §3。
- 实机测试：上午 ln 的 SSH 在握手阶段被断开，下午恢复后完成了租约网络探测（§4）、Linux 原生门禁与宿主动作审批门禁（§5）：Ubuntu 26.04 与 Debian 13 都是 23/23。途中发现并修复两处缺陷（宿主未配置时观察失败、一个未随简化更新的 Linux 测试）。

## 1. 删除的代码

| 部分 | 内容 |
| --- | --- |
| 出站逐实例许可 | `internal/incusingresshost/forwarding_*`、`internal/incusprovision/forwarding_*`、宿主动作 `incus.forwarding.permission(.plan)` 与 `incus.forwarding.withdraw`、job 执行器里的转发撤回、宿主 `state.json` 的 `forwarding_scopes` |
| HTTP 入站运行时 | `internal/computeingressruntime`（约 9,900 行）、`internal/incusingresshost` 其余部分（与上一行合计约 12,800 行）、`internal/incusprovision` 的 `ingress_observation*`、`observer_configuration*` 与 `observer_file.go`、`internal/computeingress/planner.go`、宿主 `state.json` 的 `observer_scopes` |
| 宿主动作与接口 | `incus.ingress.observe_http`、`incus.ingress.observer(.plan)`；OpenAPI 的 `observer`、`forwarding-permission` 阶段及四个 schema；CLI 与控制台客户端的同名阶段 |
| 部署热路径 | job 执行器的入站排空、转发撤回与工作区跨进程围栏；`cmd/anasd` 的协调器装配；运行锁里的围栏检查 |
| 原型与测试 | `cmd/incus-network-prototype`、`test-env/fixtures/incus-network-prototype/`、`test-env/fixtures/incus-forwarding/`、`server-incus-forwarding-e2e.py`、`server-incus-http-netns.py`、`prepare-incus-forwarding-native.sh`、`test-incus-ingress-native.sh` 及对应 CI 步骤 |

保留的部分：job 执行器在领取和执行前对任务创建者的授权复核（原先与入站排空写在同一个文件里，拆到
`internal/jobexecutor/workspace_claim.go`）；Core 对 `spec.ingress` 的解析与冻结、`internal/computeingress` 的请求文件与
授权类型、租约命名密钥与读取适配器，以及启动时对声明了 ingress 的消费者的拦截——它们在 M11 实施时改为 `publish.http`。
Traefik 的 `ANAS_TRAEFIK_RENDER_ONLY` 模式也保留，留给 anasd 内的发布中介复用路由模板（`INCUS-R-007` 的要求）。

宿主 `state.json` 去掉了两个字段，严格解码会拒绝仍带这两个键的旧文件。compute 没有生产使用，只有做过转发或观察
实验的一次性实验宿主会有这两个键。

## 2. 宿主通道身份链简化

设计见[宿主通道架构](../../docs/architecture/host-action-channel.md) §14，计划见[宿主通道计划](../plans/host-action-channel.md)
M6 与 §14。

- **准入**：hostd 的 socket 是 root:root 0600，对端只接受 root/root。安装策略升到 v3，只含 `schema` 与 `release`。
- **调用记录**：`/var/lib/anas-hostd/invocations` 下每次调用一对文件：`.lock` 以独占方式创建并在执行期间持有 flock，
  `.json` 原子替换，先记开始，再记终态。同一调用 id 只能开始一次；终态先落盘、后发帧；保留 30 天。
- **只读动作 `host.invocation.status`**：返回 `absent`、`running`、`finished`、`lost`。anasd 的执行器
  （`internal/jobexecutor/host_action_runner.go`）在事件流中断时按它结束任务：从未开始记为失败，`lost` 记为未知并保留
  执行阻断，`running` 继续轮询到该动作的编译预算。
- **删除**：`internal/hostaction` 的 broker、listener、协议、systemd 私有总线核验与退出观察共 2,823 行；
  `internal/jobexecutor` 的 `HostJobBroker`、`HostJobBinding` 与基于退出证据的收尾共 1,745 行；
  `github.com/godbus/dbus/v5` 依赖；`anasd.service` 的 `anas-job-broker` 运行目录。安装器不再创建 broker 目录，卸载时
  仍清理早期版本留下的空目录。
- **新增**：约 720 行实现（调用记录 457、执行器 266）与 330 行测试；原生门禁
  `test-env/scripts/test-host-action-native.sh` 取代 `test-host-job-broker-native.sh`。

限制：anasd 重启时正在运行的宿主任务仍记为 `daemon_restarted` 并阻断宿主队列，还没有在启动时用调用记录结清。

## 3. 控制连接转发服务（第 5 项）

已实施，依据与细节见[租约网络探测](2026-09-30-incus-lease-network-probe.md) §2.6 与 §4。要点：Incus 7.0.1 绑定失败
只在 30 秒后重试一次，所以 `incus.configure` 在把 `core.https_address` 移到控制网桥网关的 `8443` 之前，先装好防火墙，
并写入 `After=docker.service` 的 drop-in；连接包升为 v2；删除转发程序、单元、账号、配置与 `18443` 规则；安装器在
升级和卸载时删除旧版留下的单元与二进制。

同一轮还落实了操作者 2026-09-30 的两项决定：服务安装必须同时装好 `anas-helper` 与 `anas-hostd`
（`HOSTACT-R-017`，安装器在归档缺少 helper 或宿主没有 `setcap` 时，于改动任何文件之前退出，已装的旧版本保持原样）；hostd 不可用时，
宿主任务的失败消息写明缺少 `anas-hostd`。

## 4. 实机测试

准备了一次性实验 VM 的探测脚本 `test-env/fixtures/incus-lifecycle-lab/lease-network-probe.py`，按阶段运行：

| 阶段 | 覆盖的待验证项 |
| --- | --- |
| `setup` | 发行版 Docker、真实 Provider 建两份租约（一份开 IPv6）、Docker 里的 Traefik 与其他 Module 替身、局域网 netns |
| `acl` | ACL 规则的执行顺序；address set 作为出站目的地址（经 Docker DNAT 访问 Traefik）与入站来源（Traefik 在 masquerade 之前的地址）；默认拒绝入站后回包、DHCP、DNS 是否正常；局域网、其他容器与其他租约是否被挡住 |
| `docker` | `internet_lan_host` 档下能否直连未发布的容器端口，宽、窄两种宿主静态规则的对比 |
| `isolation` | `security.port_isolation` 能否隔离同一租约内的实例 |
| `slots` | 受限 project 里用租约证书给网卡写固定 IPv4/IPv6、防冒用过滤是否保留、重复地址是否被拒、租约证书能否关掉过滤、有状态 DHCPv6 |
| `ports` | Docker 式端口表（TCP、UDP、IPv6，来自局域网、宿主本机、Docker 容器与其他租约）、真实客户端地址、systemd 套接字占位、与 Docker 发布同一端口（开关 userland proxy）的冲突、Docker 重启后 Traefik 地址是否变化 |
| `control` | 第 5 项的启动顺序 |
| `dockerce` | 换成 Docker CE 后重跑 Docker 相关检查 |

下午在 ln 上以 `hold-leasenet` 诊断轮 `r13-lease-network-probe` 执行，全部阶段都有结果，另外补测了 Docker CE 下的
路由与 UDP 占位的替代方案。结果与据此做的修改见[租约网络探测](2026-09-30-incus-lease-network-probe.md)。

## 5. 本轮验证

| 检查 | 结果 |
| --- | --- |
| `go build ./...`、`go vet ./...`（macOS）与 `GOOS=linux go vet ./...` | 通过 |
| `go test ./...`（macOS） | 通过 |
| `go mod tidy` | 去掉 `godbus`，无其他变化 |
| 控制台 `generate:api`、`check:api`、`typecheck` 与 vitest | 通过 |
| `test_incus_host_action_e2e.py`、`test_incus_lifecycle_lab.py`、`test_incus_network_e2e.py`、`test_incus_host_provision_e2e.py` | 通过 |
| `gen-module-docs --check`、`gen-contract-docs --check` | 通过 |
| Linux 原生门禁 `test-host-action-native.sh` | 通过：在一次性 Debian 13 VM（`r14-native-gate`）里以非 root 用户、用同一快照交叉编译的测试二进制运行，11 个必需用例全部通过。第一次运行 `TestRejectedActivationInputIsAuditedWithoutPayload` 失败：简化后准入由测试接缝决定，空 `PeerPolicy{}` 不再意味着拒绝；已改为把接缝设回 root/root 再断言拒绝。同一 VM 中 `internal/hostaction`、`internal/jobexecutor`、`internal/computeingress`、`internal/incusprovision` 的全部测试在 umask 077 下通过；umask 002/022 下 jobexecutor 与 computeingress 的部分用例因 `t.TempDir` 子目录权限不是 0700 而失败，与本轮改动无关。`internal/runner` 的 Linux 用例依赖源码路径与重新执行测试二进制，这种运行方式验证不了 |
| 宿主动作实机审批门禁 `server-incus-host-action-e2e.py` | 见下表 |
| `scripts/ci/install-test.sh`（安装、升级、卸载夹具，含 v3 安装策略、`RuntimeDirectory`、必须带 helper 与旧转发服务的清理） | 通过 |
| 探测脚本 | 已运行，见[租约网络探测](2026-09-30-incus-lease-network-probe.md) |
| 下午的改动之后：`go build`、`go vet`（含 Linux）、`go test ./...`、155 个 Python 夹具测试、文档门禁、模块与契约文档生成检查、站点构建 | 通过 |

宿主动作实机审批门禁在 ln 的一次性 VM 上运行（实验根 `/data/anas-incus-hostaction-20260930.dVPVQe`，外层所有者脚本沿用
2026-09-26 的版本，只从输入清单里去掉转发服务两项）。输入是工作树快照提交（不在任何分支上）按发布参数交叉编译的
`0.0.0-native.N`：

| 轮次 | 系统 | 版本 | 结果 |
| --- | --- | --- | --- |
| r1-n30-debian13 | Debian 13 | native.30（快照 `4686074e`） | 11/23 后失败：重启 anasd 后的第一个 `incus.uninstall` 计划返回 `incus_host_action_failed`。原因是本轮引入的缺陷：宿主未配置时 `/etc/systemd/system/incus.service.d` 不存在，祖先目录检查把它报成不安全，观察整体失败。已修复并补回归测试（`TestUnitOrderingAbsentBeforeConfigure`） |
| r2-n31-debian13 | Debian 13 | native.31（快照 `fb22ad0e`） | 越过上一轮的失败点，`confirmed_install` 失败：APT 的 15 分钟上限到时仍在配置依赖包，`incus`、`incus-base` 停在 `iU`。代码未变的装包步骤受网络影响；同时段 ln 到 `deb.debian.org` 的 IPv4 不通、IPv6 正常。VM 磁盘保留 |
| r3-n31-ubuntu2604 | Ubuntu 26.04 | native.31 | **23/23 通过**：控制桥 mTLS、错误 pin、匿名与其他网络四项直接连 Incus 网关；14 个共享 job 各有一份带终态的 hostd 调用记录且结果一致，另有 14 次激活；卸载删除 4 个受管包、保留 681 个原有包，过期后的新计划即重复卸载；证据归档 SHA-256 `0a097e3b…f0ca`；正常关机，宿主 10 项基线一致 |
| r4-n31-debian13 | Debian 13 | native.31 | **23/23 通过**：与 r3 相同的各项，卸载删除 6 个受管包、保留 327 个原有包；14 个 job、14 份调用记录、14 次激活；证据归档 SHA-256 `ea4e6b4b…fd13`；正常关机，宿主 10 项基线一致 |

失败轮 r1、r2 的 VM 磁盘（0.7 GiB、1.8 GiB）按惯例保留；诊断轮 `r13-lease-network-probe`（3.9 GiB）与 `r14-native-gate`（1.8 GiB）的磁盘也在，清理与否由操作者决定。
