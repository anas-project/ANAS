# Incus 宿主服务执行期限与原生供给接续

状态：当前实施核对；完整发行版与宿主审批链路仍待验收。日期：2026-09-23。

接续 `/Users/whl/Documents/anas` 的已有未提交工作树，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。没有 reset、覆盖其他改动、提交或推送。
本记录对应 Incus 计划 M10，沿用[前轮卸载预检记录](2026-09-23-incus-host-uninstall-preflight.md)，
不把 HEAD 当成本轮未提交源码的完整身份。

## 已核实的第二轮失败

指定 `ssh whl@ln.hlong.wang -p 2200` 上的独立测试 VM
`anas-incus-host-81c335` 使用 Ubuntu 26.04 amd64、内核 `7.0.0-31-generic`、TCG 单核、
2048 MiB 内存。该轮日志监督器已改为有界管道，没有继承全局文件大小限制。

完整入口退出 1；确认拒绝、显式跳过通过，`install_official_packages` 子项失败。
私有状态的固定字段和系统服务日志证明：`install.packages` 的 intent/receipt 为 `ok`，
`install.service` 为 `failed`。后者从 `03:47:52.917878072Z` 开始，到
`03:48:22.989723783Z` 记录失败，匹配原固定 30 秒 `systemctl` 上限。实际 unit 的
启动上限为 10 分钟，daemon 在 `03:48:41Z` 进入 active，约为命令开始后的 49 秒。
不能把这个迟到的 active 状态当成原操作成功，也不能改写 failed intent 再重跑。

监督器没有整体超时、日志没有超限、JSON 完整，实验 Docker 的身份与空容器/网络基线一致。
因此这不是又一次日志限制故障，也不是 Docker 漂移。Incus 启动日志还报告 VM 驱动缺少
UEFI firmware；本轮只验收默认容器供给，不据此宣称 VM 计算档可用。

第二轮公开证据归档只含 `summary.json`、`environment.json`、`tests.jsonl` 和
`tests.stderr.log`，不含 TLS bundle 或完整 root 状态。归档位于远端
`anas-incus-followup-20260923.4p9ob_nk/host-v2/reports/native-run01-evidence.tar.gz`，
SHA-256 为 `9082df6ee451dee9b2f676665083afecd390051a132c940794192a4e79a86211`。
QMP 先核对精确 VM 名称再正常关机；QEMU 退出 0，socket 消失、回环端口已释放。
失败磁盘与私有状态保留，没有用其他实验盘覆盖。

## 代码修复

`fixedCommandSpec` 把服务查询与生命周期命令分开，只接受固定操作、选项和受管单元：
Incus `enable --now` 上限 11 分钟，relay 启停 2 分钟，`is-active --quiet` 仍为 30 秒。
不允许 restart、其他 unit、额外参数或 `--no-block`，没有增加请求侧命令自由度。
其他固定工具的期限不变，调用方取消/过期在创建命令之前拒绝；命令返回之后仍检查取消。

完整安装动作的编译预算为 45 分钟，涵盖两个各 15 分钟的 APT 步骤、Incus 启动和读回
余量。共享 broker 从同一动作清单取得预算；同版本 `anas-hostd@.service` 的外层 watchdog
为 2730 秒。打包测试直接遍历编译清单校验最长动作及 30 秒收尾余量，不再硬编码旧 615 秒。
其他动作的期限、原五分钟一次性确认、独立 systemd 退出证据和失败状态保留规则不变。

新增回归先复现了旧 30 秒命令期限、600 秒安装动作与 615 秒发行 unit 的不一致，再修复。
另有取消/超时后的迟到启动测试：通过 JSON 序列化重新打开状态，模拟已接受的 systemd
作业完成；fresh plan 仍 blocked，安装、配置、登记与卸载均拒绝，无新副作用、回执变更或
连接 bundle。这不是为 failed intent 提供隐式恢复后门。

原生 Go 入口失败时只增加固定 phase/step/status 诊断，不输出命令结果、私钥或完整状态。
十一项门禁及 `compute_ready=false` 边界没有降低。

## 当前验证

本轮源码实际通过：全仓 `go test -p 2 -count=1 ./...`；incusprovision、hostaction、
jobexecutor 三包竞态测试；全仓 `go vet`；38 项 Incus Python 回归；Module/Contract
文档生成与检查、需求覆盖和状态检查、共享构建静态检查及升级测试目录检查。
相关 Go 执行使用 `GOPROXY=off`，没有联网搜索。

Linux amd64/arm64 的 incusprovision 测试程序、hostd 和 relay 交叉编译通过；这不代表
ARM64 原生运行。当前完整日志在本机 `/tmp/anas-incus-service-20260923.yvRO22`。

第三轮全新 VM 为 `anas-incus-host-cbc403`，仍使用 TCG 单核/2048 MiB，避免更快环境
掩盖旧期限问题。只读基础镜像摘要再次核对为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
交付清单 `host-source-manifest-v3.json` 绑定 330 个仓库源码文件、生成测试 main 的摘要和
实际二进制/单元/监督器摘要；来源是已有 dirty checkout，不是宣称正式签名发布。

| 交付工件 | SHA-256 |
| --- | --- |
| incusprovision 原生测试 | `7bbea31c9512d8fa101784bbffe57666c2e586987bb1c6bafffaad274ebb9f35` |
| relay | `ec1b27d3cd3ad73804cf47547a1f5a6f05c997881e89035a2dc8779e8e8be7a8` |
| hostd（交叉编译工件，不作为审批链路已验收） | `25ba5bdbc7444b4cf01e4df9257725b0d030fbe26bf03171f8bffe294860dac1` |
| hostd unit | `e05682b136a6aae2ec14faabe08680595032c4c76e18a1f531a873bbf7c852fa` |

第三轮完整入口已退出 1。确认拒绝、skip 和官方包安装通过；安装子项历时 348.69 秒，
供给动作本身为 344598 ms，验证了第二轮的服务期限修复。随后 configure 的 HTTPS、
btrfs 存储、Docker 控制桥和防火墙四个 effect 均为 `ok`，relay 的 effect 为 `failed`，
固定 blocker 为 `relay_readback_failed`。完整父测试为 390.98 秒，没有超时、日志超限
或 JSON 缺失，仍不是完整生命周期通过。

第三轮公开四文件归档 `host-v3/reports/native-run01-evidence.tar.gz` 的 SHA-256 为
`5f89e9441546ae4be077f8ab556b22372ff5460c2672f04dc8ea8469db9248a9`。
该 VM 的 Docker 基线终态为 false，因为失败时保留了本轮创建的受管控制桥；这不是物理
宿主 Docker 变更证据，也没有通过删除桥来制造通过。精确 QMP 身份核验后正常关机，
QEMU 退出 0，socket 消失、22137 端口释放，私有失败状态和磁盘保留。

## 第三轮诊断与追加修复

真实 systemd journal 显示非 root relay 无法读取安装配置并反复退出。只读 stat 确认
`/etc/anas` 为 root:root、0700，普通用户读叶文件得到 Permission denied。后端原先
正确保持父目录私有，但单元又要求非 root 进程从这个私有目录读取公开配置，两者不兼容。

修复不改变源目录权限：固定 unit 只读 bind 投影单个公开配置到服务 mount namespace 的
`/run/anas-incus-control-relay.json`，ExecStart 使用该路径。安装器原来的路径重写同时
修正并纳入回归；没有投影整个目录、凭据或 Docker/Incus socket。源码还发现随后会碰到
两个限制：接口身份检查依赖 route netlink，而旧 unit 仅允许 AF_INET；宿主 pinned mTLS
探测固定绑定网关源地址，而旧 relay 把它当成禁用源。单元增加 AF_NETLINK，但仍非 root、
空 capability 集、没有网络写权限；来源过滤允许精确网关，其 loopback 交付仍由独立 INPUT
规则约束，mTLS/pin 未变。后两项是源码与回归发现，不冒充第三轮实际已执行的失败阶段。

VM 的官方包元数据还确认 Ubuntu 26.04 `incus 6.0.5-8` 为元包，实际 daemon 位于
`incus-base`，并依赖 `incus-client`。配方改为显式安装并逐包记录 base，不使用自动清理
扩展删除集合。新增回归实际复现并修复：补装缺失辅助包会误接管/启动已有 daemon，以及
仅根据包缺席就清除仍活动 daemon 的归属。现按 daemon 的逐包归属判断，历史聚合布尔值
不自动升级为权限；外部 daemon 的 install/configure/enroll 与无归属的显式卸载均先拒绝。

上述 relay/安装器/包归属回归与相关包测试通过。第四轮另建干净 VM
`anas-incus-host-39bb1d`、端口 22138、KVM 单核/2048 MiB。QEMU 使用临时进程凭据
UID 1000、GID 108、无附加组、空 capability 集及 no-new-privileges，不修改宿主用户组或
设备权限。它不替代第三轮 TCG 环境对慢启动的证明。第四轮清单
`host-source-manifest-v4.json` 绑定 333 个仓库输入及工件，完整门禁结果仍须单独记录。

| 第四轮工件 | SHA-256 |
| --- | --- |
| incusprovision 原生测试 | `677aa6bc3de57a6f2d07af2b667148b980c2f463a5e4a338dc6d893e9055d8ac` |
| relay | `bc930efaed199652ee98df2b56c0012976fe1b3541a609cbf810f243e2eb9089` |
| relay unit | `7a23bd9f1542e8a673b5d6faa0eec29849cc1470807b4d3d788cadca19520f2e` |
| hostd（不是已安装审批链路的验收证据） | `00f40edcd73c77fb0ebe1c5a2aa2384f678b7dbc04bed4e60d46dddfe1994f3c` |

## 第四轮到第五轮：稳定摘要与完整原生闭环

第四轮完整入口退出 1，但实际通过七个子项：确认拒绝、skip、安装、配置、登记、幂等重复
登记以及保留卷拒绝卸载。说明 relay 的配置投影、接口查询和宿主 mTLS 探测修复已经过
真实 systemd/Incus 验证。正式卸载在任何新副作用之前被 `host provisioning plan no longer
matches observed state` 拒绝；不是包删除或其他清理命令执行失败。

针对该 VM 的实验 Docker 做了 24 次只读网络枚举：只有一组不可变 network IDs、相同 CIDR
多重集合，却出现两种枚举顺序。旧摘要将顺序也计入状态。新增回归先复现，再仅对三个网络
CIDR 列表的克隆副本排序后计算观察摘要；保留重复数、地址、不可变 ID、接口名/index、网关
和其他就绪信息。真实拓扑或资源变化仍使确认失效，不自动重试变更计划、不删除漂移校验。

第四轮公开归档 SHA-256 为
`441f1af6c98e7611ed85c97881ba07842fa413e6542d37b7681bbf5b75c75ec3`。
该 VM 正常关机，精确 QMP socket/22138 端口释放，物理宿主本轮全部对照项均相同。

第五轮全新 VM `anas-incus-host-5ee738`、22139、KVM 单核/2048 MiB 上，监督器退出 **0**，
**11 个必需 pass 事件全部满足**：父测试与十个子项，包括真实安装、配置、管理连接登记、
重复执行、保留卷保护、默认保留包卸载、显式逐包删除和重复卸载。没有超时、日志超限、
缺少终态或缺少门禁；实验 Docker 的身份及容器/网络基线恢复一致。

第五轮是 Ubuntu 26.04 amd64 的实际后端/包管理/systemd/Incus/nft/Docker 生命周期验收，
不是将 Mock、交叉编译或静态清单当成通过。它仍**不是**真实 anasd→共享队列→hostd 的
审批链路、消费者 bridge 可达性、其他发行版、ARM64、VM 计算档或正式签名镜像发布的证明。

第五轮 `host-source-manifest-v5.json` 记录编译前后未变化的 334 个仓库输入、测试入口与工件
摘要，仍明确来自 dirty checkout。原生测试摘要为
`47ed748a96161de8b435daf6d504a93be4570f73d63b4685a859a6a759c62222`；公开四文件归档
`host-v5/reports/native-run01-evidence.tar.gz` 的 SHA-256 为
`12fbfca4218cd92059e003c520f2153a748679ab390ff23615e48a3fcb1c9bf8`。
第五轮正常关机后，QMP socket 与 22139 端口释放，物理宿主全部对照项均相同。

继续加入 VM 专用 `server-incus-host-action-e2e.py`：真实 HTTPS owner 注册、CLI stdin
会话、计划/确认/共享 job、socket 激活的发行形态 hostd、跨 workspace 与确认重放拒绝、
重启后消费记录持久化。该入口使用显式 native 预发布版本和源工件摘要；不签署正式发行、
不启用开发后门、不替换生产后端，也不以源码绑定工件冒充已有正式发布。其终态另记，
不能由第五轮后端验收结果推导。

## 物理宿主保护边界

第二轮停止后的只读对照确认 24 个已有 Docker 容器、17 个网络、卷、服务进程身份、
unit/config 摘要、nft、IPv4 路由及 named netns 均与原基线一致。
IPv6 路由对照存在差异：物理接口的 RA 前缀和相应本地地址发生变化，原始差异保留；
本次没有执行修改 IPv6 路由的命令，也不据此声称宿主所有网络状态完全未变。
第三轮在自身 `host-v3/reports` 另取开始基线，结束对照不能覆盖第二轮报告。

第三轮自身的结束对照已完成：24 个原容器、17 个原网络、卷、Docker 服务身份、unit 与
配置摘要、nft、IPv4/IPv6 路由、named netns 全部与该轮开始基线相同。
第四轮及第五轮也各自记录了独立前后基线，以上全部对照项相同。

全部写操作只涉及仓库、明确测试目录/进程或全新 VM。物理宿主既有 Docker 容器、服务、
配置、卷和网络没有作为安装、重启、连接、断开或清理对象。
