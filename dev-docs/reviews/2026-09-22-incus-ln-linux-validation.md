---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# ln 主机隔离 Linux、KVM 与真实 Incus 客户端验证

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加此前未提交工作树。操作者明确指定
`whl@ln.hlong.wang:2200`，并要求不修改服务器已有 Docker。保留全部既有变更，没有切换分支、
提交、推送或网页调研。关联 INCUS-R-015、R-021—R-025、R-027、R-030、R-037/R-038，以及
M6/M10/M11 的分层验收；本记录不把部分检查当作整条需求完成。

## 实际环境与隔离

| 范围 | 观测 |
| --- | --- |
| 物理目标 | Ubuntu 22.04.1 LTS，Linux 5.15.0-186-generic，x86_64 |
| 资源 | Xeon E3-1225 v3，4 CPU，约 7.8 GiB RAM；实验前约 2.4 GiB 可用内存、54 GiB 磁盘可用 |
| 现有 Docker | 24.0.7；22 个容器，其中 21 个运行，16 个网络；不作为测试执行面 |
| 原生工具 | nft 1.0.2、iproute2 5.15、Python 3.10；Go 1.26.5 未修改 |
| KVM | `/dev/kvm`、vmx 存在；实际 ioctl 返回 API 12，成功创建/关闭空 VM；随后实际启动独立实验 OS |
| 独立实验机 | QEMU/KVM，Ubuntu 26.04.1 LTS / kernel 7.0.0-31-generic，1 vCPU/1 GiB，12 GiB qcow2 overlay |
| 实验机工具 | 官方 Incus/CLI 6.0.5-8，nft 1.1.6-1，conntrack 1:1.4.9-1；没有 Docker |

物理宿主未安装 Incus、未更新 APT 索引、未升级软件或内核。仅将原官方 Jammy 源的 conntrack
及两个库下载/解包到本轮目录，在隔离 mount namespace 中以只读 overlay 使用，未写系统 /usr。
普通包测试以普通用户运行，外层新建 mount/network/PID namespace，私有化挂载传播，私有
`/run` 遮蔽宿主 socket；所有网络变化只在新 namespace 内。测试有独立 timeout、内存软限制和
低 CPU 调度优先级，没有创建/停止 Docker 容器或修改其网络、卷、daemon 配置。

KVM 实验机通过已有 QEMU 程序以专用进程权限运行；未改用户组、未创建 libvirt/systemd 服务。
使用 user-mode NAT，不创建宿主 TAP/bridge；SSH 仅绑定回环 22126。新 cloud-init 身份只属于
本轮 OS；安装命令先核对其 instance-id。Ubuntu 26.04 cloud image 经官方 HTTPS 获取并与同源
SHA256SUMS 核对，未宣称独立 GPG 签名验证或 ANAS 产品镜像 provenance。临时 OS 启动成功
不是 distrobuilder 镜像、`incus_vm` 租约或真实 one-job 的验收。

## 先复现再修复

本机最终复测重新暴露相同租约并发初始化偶发失败。固定阶段诊断将错误定位到创建锁文件，
实际得到 ENOENT，而不是证书内容漂移。改为先 `O_CREATE|O_EXCL`，已存在时去掉创建标志
打开；后者消失仍失败，不能新建另一锁 inode。原有 flock、上下文、目录/文件身份、权限和
单硬链接检查保留。测试加同步开始屏障；修复后同一并发用例连续 40 次通过。诊断只返回固定
阶段与系统错误类别，不回显路径、argv 或 TLS 数据。

真实 Incus CLI 首次初始化在 `incus list` 失败。该机 CLI 的 `remote add --help` 明确列出
`incus` / `simplestreams` 协议，而原配置写成 `lxd`。新增精确 remote 配置回归先失败，再把
生成值修正为 `incus`，实际原生用例随后通过。旧错误协议配置不被覆盖或静默迁移，须使用
新的私有配置目录；证书 pin、project 和不覆盖文件边界未放宽。

前轮已落盘的 Forgejo 独立取消补偿、创建前持久化、未确认创建/退役、所有权拒绝和唯一临时
文件保存已完成本轮 Linux 回归。之前遗漏的 `securefs` 共享构建依赖及精简目录离线编译也
验证通过。本轮修正双语文档中的旧 preflight 夸大：共享客户端只验证租约输入、连接与项目列表，
完整 profile/配额/围栏依赖读回仍属于 Provider。

## 物理宿主测试：不得用实验机结果覆盖失败

13 个包使用 Mac Go 1.26.6 / GOPROXY=off 交叉编译后，在指定 Linux 主机实际执行；源码定位
映射到远端快照。`-count=1`、`-parallel=2`、GOMAXPROCS=2；非 root 普通回归不启用真实
daemon/root 夹具。不是仅有编译证据。

| 范围 | 首轮结果 |
| --- | --- |
| computeclient / computeimage / computeingress / computeingressruntime | 全包退出 0；computeclient 两个 daemon 夹具明确 skip |
| incusingresshost / incusprovision / incushost | 全包退出 0；未启用的 root/daemon 夹具分别 skip，原生门禁另跑 |
| Incus Provider / control-relay / Forgejo controller / anasd | 四包退出 0 |
| hostaction | 48 个父测试 pass、3 fail、2 skip；包失败 |
| jobexecutor | 102 个父测试 pass、1 fail；包失败 |
| root 强制原生网络门禁 | 八个父用例及四个 namespace 子用例全部通过，不接受 skip |

hostaction 的 listener 三项失败与 `Fchmodat(..., AT_SYMLINK_NOFOLLOW)` 路径对应；实际对
不存在路径的 fchmodat2 探针返回 ENOSYS，未操作任何文件。项目所用 x/sys 不会静默去掉
该安全标志。jobexecutor supervisor 的三种确认结果子用例失败；外层 `--mount-proc` 在
该环境叠加出两个 `/proc` 视图，而生产库存校验拒绝重复挂载。试图调整实验 wrapper 的请求
被工具拦截，没有执行，也没有修改生产检查来兼容这一夹具。

原生网络门禁真实执行 namespace 身份/恢复、固定 executable FD、nft 基线与读回、设备绑定
FIB、回复来源冒用/同名设备重建及双向 conntrack 精确清理。该结果不证明 Docker/Incus
规则共存、完整旧 TCP、生产 HTTP/TLS/认证、持续地址身份或 VM/TAP 已验收。

## 独立 KVM 实验机与真实 daemon

相同 hostaction 和 jobexecutor 二进制在实验机的正常单一 `/proc` 视图、普通用户身份下
分别为 53、103 个父测试通过，均无 fail/skip；清理和隔离约束未改。它们证明干净的新内核
环境通过，不能改写物理 5.15 的失败记录。

真实 daemon 使用仓库 `test-incus-daemon-native.sh`，在实验机内再隔离 mount/network/PID，
状态和密钥仅保存在私有 `/run` tmpfs。没有启动产品 guest，也不使用默认 Incus 数据根。

| 检查 | 结果 |
| --- | --- |
| `TestNativeIncusUnixStorageLifecycle` | 修复后具名测试和包终态均 pass，0 skip；逐事件证据保留 |
| Provider dir 池/缺失池拒绝、只读 inspect、错误 pin、库存不变及池清理 | 七项通过 |
| 旧 lxd 协议配置 | 真实初始化失败，保留旧 JSONL，不算撤证用例已执行 |
| 修复后真实 CLI | 初次、两次重启、八份并发初始化均成功；配置 inode/mtime 不变 |
| 跨项目与错误 pin | 另一已存在 project 拒绝；不同但格式有效的 server pin 拒绝 |
| 撤销证书 | 重复初始化被拒绝，具名测试和包终态通过 |
| 测试 project/证书清理 | 独立库存与初始值一致 |
| 最终协议修复后二进制完整 computeclient 包 | 实验机中 46 个父测试 pass；两个需专用 daemon flag 的测试在普通包运行中 skip，但已在上述专用门禁分别通过 |

## 清理与既有 Docker 前后核验

原生入口返回后，实验机中无 incusd 进程、无本轮私有 tmpfs，默认 Incus service/socket 均
inactive。报告归档后，通过本轮私有 QMP socket 先核对 VM 名再请求正常 powerdown；QEMU
退出码 0，原进程、QMP socket、回环 22126 监听及本轮挂载均不存在。只逐项删除本轮临时
SSH 密钥、cloud-init/seed 和可写 qcow2 磁盘，没有递归按前缀清扫。日志、源码、二进制及
只读基础 OS 镜像保留；此前 finance 实验目录未触碰。

物理宿主的 `baseline.py` 在测试前后读取相同集合。比较结果为：

| 基线字段 | 比较 |
| --- | --- |
| 22 个 Docker 容器的 ID、状态、PID、StartedAt、RestartCount、health | 完全一致 |
| Docker service/socket 的 MainPID、InvocationID、启动时间、重启数、active 状态 | 完全一致 |
| Docker daemon 配置、systemd 单元摘要 | 完全一致 |
| 16 个 Docker 网络及已有卷集合 | 完全一致 |
| 物理宿主 nft stateless 完整规则摘要 | 完全一致 |
| IPv4 路由、命名 namespace 集合 | 完全一致 |
| IPv6 路由 | 均为 129 条，但一条本地地址发生替换；不报告完全一致 |

IPv6 差异位于既有 `anas_bridge`，只有同一前缀下的 local 地址改变。后置独立读取显示新地址
带 temporary/dynamic 标志，接口 use_tempaddr=2，符合隐私地址轮换；没有完整事件跟踪来独立
证明轮换触发原因。本轮没有向该接口发出地址/路由修改命令，QEMU 使用用户态网络而非宿主桥。
原始差异保存在私有 `reports/ipv6-route-difference.json`，本文不复制业务地址。

Docker service/socket 最终仍 active，物理宿主仍未安装 Incus/conntrack 软件包。容器健康
字段一致不等于逐个业务应用的功能探测；本轮没有修改、重启或使用现有业务 Docker 执行测试。

## 最终本机验证

| 检查 | 结果 |
| --- | --- |
| 并发锁缺陷反例与修复后 40 次重复 | 先重复失败，修复后全部通过 |
| 协议配置断言 | 旧实现失败，修复后通过；另有真实 CLI 正反证据 |
| `go test ./...` / `go vet ./...` | 最终协议修复代码全部通过；未变更包允许缓存 |
| computeclient / Forgejo controller `-race -count=1` | 最终代码通过 |
| Linux arm64 共享客户端测试程序 | 编译通过，未在 arm64 运行 |
| 共享构建 / 升级目录 | 静态门禁通过；不是 Docker build 或实际升级 E2E |
| Module/Contract 双语生成与检查、需求覆盖、需求/计划索引、文档状态 | 通过；Incus 统计仍为 30/75 |
| 双语 `npm run docs:build` | v0.1.1 构建通过，仅原有非阻断 chunk 大小警告 |

首轮本机强制 GOTOOLCHAIN=local 误选到旧安装 Go，构建失败；改用仓库已缓存的 1.26.6
工具链后完成上述验证，没有修改全局 Go 安装。被工具拦截的远端组合预检、实验 wrapper 调整
及额外 CLI 探针均不计为执行。后续已有工具与正常测试的通过记录不覆盖这些未执行动作。

## 源码与证据位置

物理测试根为 `/home/whl/anas-incus-ln-20260922.e3i1qu`；本机结果根为
`/tmp/anas-ln-20260922.a6B523`。实验机内部曾使用 `/home/anas-test/verification`；报告通过
`reports/guest-evidence.tgz` 回收到物理测试根，不把原始业务配置、TLS 私钥或完整租约复制
到仓库。Go 逐测试 JSONL、退出码、初始失败和修复后通过分别保留。

| 工件 | SHA-256 |
| --- | --- |
| 初始 1508 文件源码快照 `source.tgz` | `000659c3b9ff466f8225ab2fd2a04232fa172b3cd25025e9aaff62350470222b` |
| 初始测试包 `tests.tgz` | `dd396c3c577cac4c2a177f3a6749bd662bb007766742e37f182cf659b9e1ee24` |
| 锁修复稀疏源码 `client-fix-source.tgz` | `8f07b0bf0bb71b7082bc86ddc917121da863e3d899c1279a270f5addc9e7d44e` |
| 协议修复稀疏源码 `protocol-fix-source.tgz` | `40f35e1243e1287b419c0e8e7ccbd096ea97a65a5347c51531e60009af3b293f` |
| 最终原生 client 测试二进制 | `2d5cf2ef4fd1ac53a8f8a8248870215409e63ade57acf97e352db9655afacb1b` |
| 实验机证据 `reports/guest-evidence.tgz` | `e6e7fa952a4feefe45fc027a7510046e0e282965ade232c74231fab6e2360d72` |

稀疏归档必须与原始源码快照结合，不是假称一份独立完整发行包。运行环境时钟产生的 JSONL
UTC 时间原样保留，本记录文件日期按本次任务基线，不修改服务器时间来调整报告。

## 尚未完成

真实 btrfs/zfs 写满与配额、产品 guest create/start/exec/delete、取消/迟到创建、one-job、
管理证书轮换、默认安装/卸载三发行版矩阵、镜像烘焙/签名/回滚/prune，以及 production
ingress 的安装 UID/挂载/health、VM/TAP 和持续身份仍未验收。没有采用物理宿主系统升级、
现有 Docker 容器、放宽安全校验或填入虚假镜像摘要来换取通过。

Incus 保持 `7.3.0-r2 / developing`、30/75，production publication gate 未解除。
