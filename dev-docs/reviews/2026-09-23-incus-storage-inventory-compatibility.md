# Debian 存储池空清单与显式卸载接续

状态：实施与独立原生验收记录。日期：2026-09-23。

接续已有暂存/未暂存工作树，HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。未提交、推送、reset 或重新暂存。
依据 Incus M10、宿主供给架构与前轮
[Debian 包触发器记录](2026-09-23-incus-debian-package-triggers.md)。使用指定
`ssh whl@ln.hlong.wang -p 2200`，没有联网搜索或修改物理宿主既有 Docker 容器。

## 已复核的第二十六轮终态

第二十六轮 `anas-incus-host-dd55ba` 的实际审批入口退出 1。确认 skip、真实重启后的消费
记录保持、安装、配置、登记、消费者传输、默认卸载、自然过期及新计划执行通过，随后
`confirmed_owned_package_removal` 失败。完整 25 项门禁未通过。

公开归档 `host-v26/reports/root-action-run01-evidence.tar.gz` 已按实际字节复核摘要：
`9e31c00dbad358e5d61e4e40df77e3137ca5ed7f0bb29b40b48d7f2c9c16495d`。
监督器记录正常关机、QEMU 退出 0、没有强制退出或清理错误，物理宿主全部独立对照相同。
公开 effect 摘要没有新 `uninstall.packages` intent，不能把该失败描述为 dpkg 删除失败。

## 独立副本诊断

在停止的失败磁盘之上创建单独 qcow2 overlay，通过原 SSH host key 固定新回环端口，
QEMU 用户态网络禁止外连。只读取固定状态字段及 Incus 盘点 API，不执行失败动作、
清空 intent 或改写凭据。两次诊断前后的原盘 SHA-256 都保持
`1682bc4a408c360143b964d611716137c77c4c1df6a4750a517500948d19ae0e`；
各自退出 0、正常关机、物理宿主对照一致，结果位于 `diagnose-v26-pinned` 与
`diagnose-v26-names` 的 reports 目录。

实际 `incus 6.0.4-2+deb13u10` 上实例、镜像与证书为空，只有 default project/profile，
网络都为 unmanaged。记录的所有包仍完整安装。最后一个池删除后，递归与非递归两个池
清单均以同步成功的 `metadata:null` 表示空集合。最初只考虑额外查询返回空数组的修复
因此仍不适用，不能将这个未执行的候选版本登记为通过。

## 实现与不变边界

`internal/incusprovision/uninstall.go` 将这种兼容性限定在两个固定的存储池清单端点：
先完整验证同步 HTTP/JSON 响应；递归清单明确为 null 时，另取名称清单并要求它也为空，
然后独立查询固定受管池，必须得到经过校验的不存在响应。存在的池、非空/矛盾列表、
失败请求、缺字段、错误码或未知结构均阻止删除。其他集合与统一客户端的 null 拒绝不变。

没有增加强制删除、autoremove 或破坏性重试，不改变默认保留包、逐包归属、调用方取消、
真实拓扑漂移与失败 intent 的规则。新测试先复现错误拒绝，再覆盖两种空集合编码、
矛盾盘点、已存在池、其他集合 null、缺失/错误响应、取消与固定查询次数。

本机记录在 `/tmp/anas-incus-continue-20260923.4Q3wUy`。新原生终态、工件摘要和最终
完整门禁见下方逐轮结果，不由局部回归推导。

## 第二十八轮：盘点修复通过，继续定位删除后读回

全新 Debian VM `anas-incus-host-2ec6c2` / 22166 使用实验版本
`0.0.0-native.20260923.7`，453 个源码/嵌入/回归输入在编译前后及提交测试前摘要一致。
实际工件为本机 `product-v2`；早先要求非递归清单必须返回数组的候选 `product-v1` 未作为
通过证据使用。第二十八轮再次通过安装、配置、登记、消费者控制桥、默认卸载和过期门禁。

该轮已创建 `uninstall.packages` intent，证明原来的只读存储池盘点已通过；随后失败回执
明确为 `package removal readback failed`，不能将其描述成盘点或 dpkg 命令仍未执行。
完整 25 项因此仍未通过，公开归档 SHA-256 为
`f54dec7c98d4b315f8f38c00046f5504241158799bdddf5c7539d65d303f5c22`。
原监督器记录 test_exit=1、qemu_exit=0、正常关机、无强制退出/收尾错误，物理宿主对照相同。

独立 `diagnose-v28-packages` overlay 的固定字段与 dpkg 日志证明六个受管包都已删除，
其中部分保留配置文件；未托管的依赖仍在。daemon inactive，触发器已结束。诊断没有重新
执行卸载，也没有把 failed intent 改为成功。原盘摘要
`f3b9a4fc46ed2866dc09bd0111ef965c77fe9a5e9f877f19d978b2110aecabc2`
在诊断前后未变，诊断退出/正常关机及全部宿主对照均通过。读回协议继续单独定位。

后续 `diagnose-v28-observation` 逐包取得与生产命令一致的 dpkg 状态，返回格式有效；
`diagnose-v28-service-policy` 只读解包保留的官方 incus-base 控制归档。实际 prerm 仅调用
`invoke-rc.d --skip-systemd-native incus stop`，没有 systemd 停服步骤，postrm 只 reload。
这解释了“包已移除而最终活动状态不能被确认”的边界；诊断重启后的 inactive 不能倒推
原失败时已经停服。三份诊断均保持原盘摘要，正常收尾、宿主对照相同。

新增局部 `StopIncus` 实现并放在现有 `uninstall.packages` intent 内：完整预检先行，
只对明确有服务归属的 daemon 执行固定 `systemctl stop incus.service`，独立读回后才删
精确受管包。该内部操作有两分钟上限，没有新公共动作、调用方 argv 或其他单元路径。
默认保留包不停止服务，活动但没有服务归属的情况提前拒绝。停止失败、读回仍活动、
停止后取消都阻止包删除，已有“删除后又活动”的反例仍需失败；服务/包归属最终读回后才清除。

相应回归先复现隐式依赖维护脚本、未确认停服继续删包、外部服务被误处理的缺口，再修复。
新增真实验证使用新的产品工件和干净 VM，不在第二十八轮 failed intent 上重试。

## 第三十轮：Debian 完整 25 项通过

全新 VM `anas-incus-host-407514` / 22171 / Debian 13 amd64，以实验版本
`0.0.0-native.20260923.8` 完整执行实际产品审批入口，**25 个必需门禁全部通过**，
**18 个作业与 18 次 systemd 退出证据**完整。实际安装、配置、pinned mTLS 管理登记、
非 root 控制桥探测与三类拒绝、默认卸载、真实五分钟自然过期均通过；明确删包移除六个
有归属的包，327 个原有包及未托管依赖保留，重复包卸载无新增破坏性效果。

最终工件对应编译前后未改变的 455 个输入，manifest 为本机 `product-v3/source-manifest.json`。
主要二进制 SHA-256：

| 工件 | SHA-256 |
| --- | --- |
| anas | `0d67dd6aea65de65ecc1e9336e4e8acdf71010b5456612ff8d9eb4bcab84fed3` |
| anasd | `cbe58d7a24266f823d923a9f69db7f7ed9ba910c2f6ffbcddaddb8aa02a8793e` |
| anas-hostd | `fcbec3b0e10ff36f27dc82b7086a13ecd4013b2555af8aed38cde74e1272daa2` |

完整公开归档：
`/home/whl/anas-incus-followup-20260923.4p9ob_nk/host-v30/reports/root-action-run01-evidence.tar.gz`。
其实际 SHA-256 为
`08ae94bf4bb506581e50770cd89c5955fa258b576664e511dd1fa456c9f2437d`。
test_exit=0、qemu_exit=0、正常 QMP 关机、无强制退出或收尾错误；物理宿主既有 24 个
容器、17 个网络、卷、Docker 服务/配置、nft、IPv4/IPv6 路由和 named netns 均前后一致。

这使 Debian 不再停留于准备或只读预检：真实产品的完整宿主审批与精确包卸载已有证据。
它不替代 ARM64 原生、Incus VM 档、实际业务 Compose 完整部署、正式签名发布、故障注入
恢复矩阵或生产 HTTP 入站。失败轮次保持失败记录，没有改写成第三十轮的成功。

## 同工件 Ubuntu 26.04：预装依赖保留对照通过

独立第二十九轮 VM `anas-incus-host-905bed` / 22167 使用完全相同的 `.8` 产品工件；
准备阶段先从官方源安装 nftables/conntrack，使其成为产品运行前的真实既有依赖。
完整 **25 项与 18 个作业/退出证据**全部通过，显式移除四个新归属包，682 个原有包
及其他未托管依赖完整保留。不是在删除之后重新安装依赖来伪造保留。

公开归档 `host-v29/reports/root-action-run01-evidence.tar.gz` 的实际摘要为
`e483cd60fe450ef8a705571dd84bcca6f2e0a6dd8647e0767578cac59caf9c9e`。
test_exit=0、qemu_exit=0、正常关机、无强制退出/收尾错误，物理宿主全部对照相同。
这证明 Debian 的局部空池兼容与显式停服没有把 Ubuntu 原包纳入删除集合。

第三十轮和第二十九轮的成功写盘在归档摘要、完整通过、正常关机及无其他镜像依赖再次
确认后分别回收，操作记录位于各自 `reports/successful-disk-retirement.json`。保留报告、
工件、manifest 与失败实验盘；下一发行版沿用同工件但采用另一干净磁盘，实验串行进行。

## 第三十一轮：Ubuntu 24.04 与三发行版终态

独立第三十一轮 `anas-incus-host-e8f7c6` / 22172 / Ubuntu 24.04 amd64 同样使用 `.8`
产品工件。完整 **25 项门禁、18 个真实作业和 18 次退出观察**全部通过，明确删除三个
有归属的包并保留 667 个原有包及其他未托管依赖，重复卸载通过。旧的 23 项通过记录
仍是其当时的真实结果，但本轮已补上新的显式包删除与重复卸载，不再以旧报告替代。

公开归档 `host-v31/reports/root-action-run01-evidence.tar.gz` 的实际 SHA-256 为
`df640731aed4e208f406efd3f97da5d2e94e7e141ec50695abaae08b6ba9a666`。
test_exit=0、qemu_exit=0、正常关机、没有强制退出或收尾错误；物理宿主所有对照项一致。
本轮成功写盘未删除，公开报告和源工件保留，当前没有需要等待的测试进程。

| 当前同工件原生矩阵 | 完整门禁 | 作业 / 独立激活退出 | 原有包保留 | 精确删除的新归属包 | 终态 |
| --- | --- | --- | --- | --- | --- |
| Debian 13 amd64，第三十轮 | 25 / 25 | 18 / 18 | 327 | 6 | 测试与正常收尾通过 |
| Ubuntu 26.04 amd64，第二十九轮 | 25 / 25 | 18 / 18 | 682 | 4 | 测试与正常收尾通过 |
| Ubuntu 24.04 amd64，第三十一轮 | 25 / 25 | 18 / 18 | 667 | 3 | 测试与正常收尾通过 |

最终独立复核了三份公开归档的实际摘要、完整 REQUIRED 集合、无跳过/重复门禁、每个
工件的字节摘要、三轮相同产品工件和 `.8` release identity。归档不含私有目录、连接
bundle、bootstrap 凭据、私钥、软链接或硬链接。三轮各自的物理宿主前后对照全部为真；
另外确认本轮诊断/验收回环端口 22164—22172 没有遗留监听，当前没有 QEMU 进程。
这些是同一套宿主验收在三个发行版的重复验证，不表示 Incus 的 75 项需求全部完成。

## 最终本机验证与未完成边界

最终产品源码通过 `go test -p 2 -count=1 ./...`、incusprovision/incushost/hostaction/
jobexecutor/hostd 五包 race、全仓 go vet、66 项 Incus Python 回归和 4 项浏览器离线
guard。Linux arm64 的现有供给测试程序与 hostd 交叉编译通过，不据此登记 ARM64 原生
或 Incus VM 档运行通过。代码日志为 `final-code-gates.log`，实际退出码为 0。

文档最终同步使用 Module/Contract 生成器、需求与计划状态索引；检查涵盖需求覆盖、文档
状态、两类索引、共享构建静态门禁、升级目录，以及暂存/未暂存 diff 的格式错误。
最终三发行版记录对应日志 `matrix-documentation-gates.log`；代码和原生工件不因文档
归档更新而改变。

455 个编译输入在最终原生运行后再次比对，均与 `product-v3/source-manifest.json` 一致；
HEAD 未改变。新增修复集中于两个只读库存端点的兼容检查、明确归属下的 daemon 停止、
相应负例与文档，不混淆已有暂存/未暂存工作，也没有提交、推送或重新暂存。

本轮关闭的是三个一级发行版 amd64 的同工件宿主审批/控制桥/正常卸载矩阵。完整 Core/
业务 Compose 自动供给与投影、未适配系统的产品降级场景、ARM64/VM 原生、完整故障
注入恢复、生产 HTTP 入站地址/health/长连接生命周期、正式签名镜像与回滚/prune 仍分别
实施与验收。M10 保持实施中，Module developing 和生产 ingress 关闭状态不提前改变。

## 实验空间与本机门禁

空间检查一度发现物理宿主仅剩约 7.5 GiB，拒绝启动新 VM。只回收已成功并完整归档的
第五轮及第十五轮两份独立实验写盘：先复核归档摘要、真实通过、QEMU 退出及没有其他
qcow2 依赖，再记录实际磁盘摘要并删除精确写盘。共释放 4,093,878,272 字节。
每轮的 `reports/successful-disk-retirement.json` 保留操作证据；公开报告、基础镜像、
所有失败盘和物理 Docker 数据均保留。没有通过清理业务容器或镜像腾空间。

当前修复已通过全仓 Go 测试、五包 race、全仓 go vet、66 项 Incus Python、4 项浏览器
离线 guard、Module/Contract 文档生成与检查、需求/计划状态及覆盖检查、共享构建静态
门禁和升级目录检查。它们不替代第二十八轮失败的完整原生结果；后续产品变化须重新验证。
